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

	files, err := collect(ctx, pattern)
	if err != nil {
		return "", err
	}

	var results []string
	truncated := false
	if re == nil {
		// List mode: matching paths, one per line.
		for _, path := range files {
			if len(results) == call.Limit {
				truncated = true
				break
			}
			line := path + "\n"
			results, truncated = fit(results, line)
			if truncated {
				break
			}
		}
	} else {
		for _, path := range files {
			if len(results) == call.Limit {
				truncated = true
				break
			}
			more, fileTrunc, err := grepFile(ctx, path, re, call.Limit-len(results))
			if err != nil {
				return "", err
			}
			for _, line := range more {
				results, truncated = fit(results, line)
				if truncated {
					break
				}
			}
			if truncated || fileTrunc {
				truncated = true
				break
			}
		}
	}

	if len(results) == 0 {
		return "(no matches)", nil
	}
	var sb strings.Builder
	for _, line := range results {
		sb.WriteString(line)
	}
	if truncated {
		sb.WriteString(truncNote)
	}
	return sb.String(), nil
}

// fit appends line to results under the byte budget. It reports
// whether the budget - not the caller's line limit - ran out; the
// caller decides that either way the observation is truncated.
func fit(results []string, line string) ([]string, bool) {
	if budgetSpent(results)+len(line) > maxOutput-len(truncNote) {
		return results, true
	}
	return append(results, line), false
}

// budgetSpent counts what the rendered observation holds so far.
func budgetSpent(results []string) int {
	total := 0
	for _, line := range results {
		total += len(line)
	}
	return total
}

// cleanGlob validates and normalizes the glob: it must be relative
// to the working directory and must not climb out of it. Absolute
// patterns and any ".." segment are rejected outright — the walk
// starts at the working directory root, so such a pattern could
// never match anything anyway and is a caller mistake worth an
// error rather than a silent empty result.
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
	}
	return pattern, nil
}

// matchSegments reports whether a cleaned path matches the pattern:
// "**" spans any number of directories (including none), every
// other segment is a filepath.Match pattern within its directory.
func matchSegments(pattern, name []string) bool {
	if len(pattern) == 0 {
		return len(name) == 0
	}
	if pattern[0] == "**" {
		for skip := 0; skip <= len(name); skip++ {
			if matchSegments(pattern[1:], name[skip:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	ok, err := filepath.Match(pattern[0], name[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pattern[1:], name[1:])
}

// collect walks the working directory and returns the file paths
// matching the pattern, in the walk's lexical order - the output
// is deterministic. Hidden directories and files (dot-prefixed)
// are skipped the way ripgrep does: they are almost always noise
// (.git alone can dwarf a source tree), and the model did not ask
// for them. The walk honors ctx, so cancellation is prompt even
// in a big tree.
func collect(ctx context.Context, pattern []string) ([]string, error) {
	var files []string
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
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel := filepath.ToSlash(path)
		if matchSegments(pattern, strings.Split(rel, "/")) {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk: %w", err)
	}
	return files, nil
}

// grepFile returns the lines of path that match re, rendered as
// path:line:text with 1-based line numbers and content capped at
// maxLineLen for display. The query is matched against a wider
// window (matchLineLen), so a match past the display cap is still
// found - see the constant. atMost bounds how many matches one
// file may contribute, so a file of a thousand matches cannot
// crowd out every other file. fileTrunc reports that the per-file
// bound was hit, which truncates the whole observation. Files that
// look binary (a NUL byte in a scanned line) are skipped without
// an error - grepping a binary is noise, not a failure.
// Cancellation is honored between lines.
func grepFile(ctx context.Context, path string, re *regexp.Regexp, atMost int) (lines []string, fileTrunc bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 64*1024)
	for lineno := 1; ; lineno++ {
		if err := ctx.Err(); err != nil {
			return nil, false, fmt.Errorf("search: %w", err)
		}
		line, _, rerr := cappedLine(r, matchLineLen)
		if rerr == io.EOF && line == "" {
			return lines, false, nil
		}
		if rerr != nil && rerr != io.EOF {
			return nil, false, fmt.Errorf("read %s: %w", path, rerr)
		}
		if strings.ContainsRune(line, 0x00) {
			return lines, false, nil // binary file: skip it silently
		}
		if re.MatchString(line) {
			if len(lines) == atMost {
				return lines, true, nil
			}
			if len(line) > maxLineLen {
				line = line[:maxLineLen] + "…"
			}
			lines = append(lines, fmt.Sprintf("%s:%d:%s\n", path, lineno, line))
		}
		if rerr == io.EOF {
			return lines, false, nil
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
