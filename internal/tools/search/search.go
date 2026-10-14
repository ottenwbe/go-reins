// Package search implements an agent.Tool that finds files in the
// working directory by glob and, optionally, lines within them
// matching a regular expression - ripgrep-style find + grep in one
// read-only call. It is the locating sibling of the read tool: the
// model searches first, then reads the interesting paths. It cannot
// change machine state, so the agent loop runs it without operator
// approval.
package search

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go-reins/internal/agent"
)

const (
	// defaultLimit is the number of result lines returned when the
	// caller does not ask for a cap.
	defaultLimit = 50
	// maxLimit bounds the requested cap; output is also capped by
	// maxOutput bytes.
	maxLimit = 200
	// maxOutput caps the observation so a broad glob over a big tree
	// cannot blow up the conversation history - the same budget as
	// the read and shell tools.
	maxOutput = 4000
	// maxLineLen caps a single matched line inside the observation:
	// without it, one multi-megabyte line would sail past the byte
	// budget before the budget check sees it.
	maxLineLen = 512
	// matchLineLen bounds how much of a line the query is matched
	// against. Matching the display cap would miss matches deeper
	// in a long line, so the query sees a much wider window than
	// the observation renders; a line longer than this is matched
	// only in its first matchLineLen bytes, and one that long is
	// binary content in practice (the NUL check skips those).
	matchLineLen = 1024 * 1024
)

// truncNote is appended when a cap was hit; the byte budget
// reserves space for it so even a truncated observation stays
// within maxOutput.
const truncNote = "(output truncated — narrow the glob or query)\n"

// partialNote is appended when a line exceeded the match window:
// the query then saw only its first matchLineLen bytes, and the
// observation must say so instead of silently missing matches.
// The budget reserves space for both notes.
const partialNote = "(long lines are matched only in their first 1 MiB — matches deeper in them may be missing)\n"

// Search finds files by glob and optionally greps their contents.
type Search struct{}

// New creates a search tool.
func New() *Search { return &Search{} }

// Name implements agent.Tool.
func (s *Search) Name() string { return "search" }

// Risk implements agent.Tool: searching only observes, so the
// agent loop runs it without operator approval.
func (s *Search) Risk() agent.Risk { return agent.RiskReadOnly }

// Description implements agent.Tool.
func (s *Search) Description() string {
	return `Search the working directory for files by glob and,
optionally, lines matching a pattern. Use it to locate code or
notes before reading them. Pass arguments as JSON:
{"glob": "**/*.go", "query": "StreamFeed", "limit": 50}. "glob" is
a path pattern relative to the working directory: ** matches any
number of directories, * and ? match within one, and hidden
directories are skipped. With "query" (a regular expression) the
result lines are matches as path:line:text; without it, the result
lists matching file paths. Results are capped — narrow the glob
or query and search again.`
}

// Execute implements agent.Tool. Errors (bad arguments, invalid
// pattern) are returned as errors; the agent loop turns them into
// observations, so the model sees what went wrong. A search that
// finds nothing is a result, not an error.
func (s *Search) Execute(ctx context.Context, args string) (string, error) {
	var call struct {
		Glob  string `json:"glob"`
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(args), &call); err != nil {
		return "", fmt.Errorf("invalid arguments, expected {\"glob\": \"...\", \"query\": \"...\", \"limit\": 50}: %w", err)
	}
	pattern, err := cleanGlob(call.Glob)
	if err != nil {
		return "", err
	}
	if call.Limit <= 0 {
		call.Limit = defaultLimit
	}
	if call.Limit > maxLimit {
		call.Limit = maxLimit
	}

	var re *regexp.Regexp
	if call.Query != "" {
		re, err = regexp.Compile(call.Query)
		if err != nil {
			return "", fmt.Errorf("invalid query %q: %w", call.Query, err)
		}
	}

	res := newResults(call.Limit)
	if re == nil {
		// List mode: matching paths, one per line.
		err = collect(ctx, pattern, func(rel string, _ fs.FileInfo) (bool, error) {
			return res.add(rel + "\n"), nil
		})
	} else {
		err = collect(ctx, pattern, func(rel string, info fs.FileInfo) (bool, error) {
			return grepInto(ctx, rel, info, re, res)
		})
	}
	if err != nil {
		return "", err
	}
	return res.render(), nil
}

// results accumulates observation lines under the caller's line
// limit and the byte budget. add reports whether the search should
// stop: either cap running out marks the observation truncated.
type results struct {
	lines     []string
	spent     int // bytes the rendered lines hold
	limit     int
	truncated bool
	partial   bool
	budget    int
}

func newResults(limit int) *results {
	return &results{
		limit:  limit,
		budget: maxOutput - len(truncNote) - len(partialNote),
	}
}

// add appends one line unless a cap is exhausted; it reports
// whether the caller should stop searching.
func (r *results) add(line string) bool {
	if len(r.lines) >= r.limit {
		r.truncated = true
		return true
	}
	if r.spent+len(line) > r.budget {
		r.truncated = true
		return true
	}
	r.lines = append(r.lines, line)
	r.spent += len(line)
	return false
}

// render assembles the observation: the lines, then the partial
// note when a long line was not fully matched, then the
// truncation note when a cap was hit.
func (r *results) render() string {
	if len(r.lines) == 0 && !r.partial && !r.truncated {
		return "(no matches)"
	}
	var sb strings.Builder
	for _, line := range r.lines {
		sb.WriteString(line)
	}
	if r.partial {
		sb.WriteString(partialNote)
	}
	if r.truncated {
		sb.WriteString(truncNote)
	}
	return sb.String()
}

// errStopWalk ends the walk early: the results hit a cap. It is
// swallowed by collect and never reaches the caller.
var errStopWalk = errors.New("stop walk")

// cleanGlob validates and normalizes the glob: it must be relative
// to the working directory and must not climb out of it. Absolute
// patterns and any ".." segment are rejected outright — the walk
// starts at the working directory root, so such a pattern could
// never match anything anyway and is a caller mistake worth an
// error rather than a silent empty result. Malformed pattern
// syntax fails the same way a malformed query does: a bad glob
// that silently matched nothing would read to the model as
// "nothing there" instead of "bad pattern".
func cleanGlob(glob string) ([]string, error) {
	if glob == "" {
		return nil, errors.New("empty glob: pass {\"glob\": \"...\"}")
	}
	if filepath.IsAbs(glob) {
		return nil, fmt.Errorf("glob %s must be relative to the working directory", glob)
	}
	pattern := strings.Split(filepath.ToSlash(filepath.Clean(glob)), "/")
	for _, seg := range pattern {
		if seg == ".." {
			return nil, fmt.Errorf("glob %s escapes the working directory", glob)
		}
		if seg == "**" {
			continue
		}
		// filepath.Match reports ErrBadPattern no matter the name,
		// so one probe per segment is a full syntax check.
		if _, err := filepath.Match(seg, ""); err != nil {
			return nil, fmt.Errorf("invalid glob %s: %w", glob, err)
		}
	}
	return pattern, nil
}

// matchSegments reports whether a cleaned path matches the pattern:
// "**" spans any number of directories (including none), every
// other segment is a filepath.Match pattern within its directory.
// The match is a dynamic program over (pattern, name) positions, so
// any number of "**" segments costs O(len(pattern)*len(name))
// segment matches - a recursive matcher would backtrack
// combinatorially on adversarial patterns with several "**".
func matchSegments(pattern, name []string) bool {
	// dp[j] holds one row of the table: dp[j] is true when
	// pattern[:i] matches name[:j]. dp[0] starts true: the empty
	// pattern matches the empty prefix.
	dp := make([]bool, len(name)+1)
	dp[0] = true
	for i := 1; i <= len(pattern); i++ {
		seg := pattern[i-1]
		if seg == "**" {
			// "**" absorbs any number of segments: once any prefix
			// matched on the row above, everything from there on
			// matches here too.
			for j := 1; j <= len(name); j++ {
				dp[j] = dp[j] || dp[j-1]
			}
			continue
		}
		// A concrete segment consumes exactly one name segment.
		// Scanning right to left keeps dp[j-1] holding the row
		// above, so no second row is needed.
		for j := len(name); j >= 1; j-- {
			ok, err := filepath.Match(seg, name[j-1])
			dp[j] = dp[j-1] && err == nil && ok
		}
		dp[0] = false // a concrete segment cannot match the empty prefix
	}
	return dp[len(name)]
}

// collect walks the working directory and hands every matching
// regular file to visit - the path as the walk sees it, plus the
// walk-time stat so the reader can verify it is still the same
// file - in the walk's lexical order, so the output is
// deterministic. visit reports whether the search should stop
// early (a cap was hit) or fails; results are consumed as the walk
// finds them, so a broad glob over a big tree never allocates more
// than the observation it will return. Hidden directories and
// files (dot-prefixed) are skipped the way ripgrep does: they are
// almost always noise (.git alone can dwarf a source tree), and
// the model did not ask for them. The walk honors ctx, so
// cancellation is prompt even in a big tree.
func collect(ctx context.Context, pattern []string, visit func(rel string, info fs.FileInfo) (bool, error)) error {
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if path != "." && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		// Only regular files are considered - symlinks, fifos,
		// devices, sockets are not. A symlink inside the tree could
		// point outside the working directory, and the walk never
		// resolves entries: grep would open what the walk did not
		// vet. The read tool resolves and refuses such paths; the
		// search tool must not become the read-only back door.
		if !d.Type().IsRegular() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel := filepath.ToSlash(path)
		if !matchSegments(pattern, strings.Split(rel, "/")) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			// A file deleted between the directory read and this
			// stat is a race, not an operator mistake: skip the
			// vanished entry instead of failing the whole search.
			// Directory read errors stay loud - they say the tree
			// itself cannot be walked.
			return nil
		}
		stop, verr := visit(rel, info)
		if verr != nil {
			return verr
		}
		if stop {
			return errStopWalk
		}
		return nil
	})
	if errors.Is(err, errStopWalk) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("walk: %w", err)
	}
	return nil
}

// grepInto scans one file for lines matching re, adding matches to
// res as path:line:text with 1-based line numbers and content
// capped at maxLineLen for display. It reports whether the search
// should stop (a cap was hit); open and read failures are errors
// that fail the search, the way a failed walk does.
//
// The walk vetted a regular file, and the open must still be
// reading that same file. Two swaps are caught before anything is
// read: a path replaced by a FIFO would park a plain open until a
// writer appears - the open is non-blocking, so it returns and
// the stat below rejects what is not regular - and a path replaced
// by a symlink names a different inode, which os.SameFile against
// the walk-time stat catches. A changed path is skipped, not read:
// the search reads only what the walk saw.
//
// Files that look binary (a NUL byte in a scanned line) are
// skipped without an error - grepping a binary is noise, not a
// failure. A line longer than the match window marks the results
// partial: the query saw only its first matchLineLen bytes, and
// the observation says so. Cancellation is honored between lines.
func grepInto(ctx context.Context, path string, info fs.FileInfo, re *regexp.Regexp, res *results) (bool, error) {
	f, err := openForSearch(path)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return false, nil // swapped for something else: skip it
	}
	if !os.SameFile(fi, info) {
		return false, nil // the entry changed under the search: skip it
	}

	r := bufio.NewReaderSize(f, 64*1024)
	for lineno := 1; ; lineno++ {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("search: %w", err)
		}
		line, tooLong, rerr := cappedLine(r, matchLineLen)
		if rerr == io.EOF && line == "" {
			return false, nil
		}
		if rerr != nil && rerr != io.EOF {
			return false, fmt.Errorf("read %s: %w", path, rerr)
		}
		if strings.ContainsRune(line, 0x00) {
			return false, nil // binary file: skip it silently
		}
		if re.MatchString(line) {
			if len(line) > maxLineLen {
				line = line[:maxLineLen] + "…"
			}
			if res.add(fmt.Sprintf("%s:%d:%s\n", path, lineno, line)) {
				return true, nil
			}
		}
		if tooLong {
			res.partial = true // the match window did not cover this line
		}
		if rerr == io.EOF {
			return false, nil
		}
	}
}

// cappedLine reads one line, retaining at most the first limit
// bytes of it: ReadString would buffer a multi-megabyte line whole
// before the caller could truncate it, so this drains through the
// newline and drops everything past the cap. tooLong reports that
// bytes were dropped. A limit of 0 drains the line and returns
// nothing.
func cappedLine(r *bufio.Reader, limit int) (line string, tooLong bool, err error) {
	keep := limit + 1 // room for the newline
	var kept []byte
	for {
		chunk, rerr := r.ReadSlice('\n')
		if room := keep - len(kept); len(chunk) <= room {
			kept = append(kept, chunk...)
		} else {
			kept = append(kept, chunk[:max(room, 0)]...)
			tooLong = true
		}
		if rerr == bufio.ErrBufferFull {
			continue // no newline yet: drain on, retained part stays capped
		}
		line = strings.TrimSuffix(string(kept), "\n")
		if len(line) > limit {
			line = line[:limit]
			tooLong = true
		}
		return line, tooLong, rerr
	}
}
