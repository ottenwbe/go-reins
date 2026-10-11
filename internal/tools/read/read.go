// Package read implements an agent.Tool that reads a file under the
// working directory and returns it with line numbers. It is the
// read-only sibling of the shell tool: it cannot change machine
// state, so the agent loop runs it without operator approval. A
// later edit tool can reference the line numbers read reports.
package read

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go-reins/internal/agent"
)

const (
	// defaultLimit is the number of lines returned when the caller
	// does not ask for a window.
	defaultLimit = 200
	// maxLimit bounds the requested window; output is also capped by
	// maxOutput bytes.
	maxLimit = 1000
	// maxOutput caps the observation so one huge file cannot blow up
	// the conversation history — the same budget as the shell tool.
	maxOutput = 4000
	// maxLineLen caps a single line inside the observation: without
	// it, one multi-megabyte line would sail past the byte budget
	// before the budget check sees it.
	maxLineLen = 512
)

// truncNote is appended when a cap was hit; the byte budget
// reserves space for it so even a truncated observation stays
// within maxOutput.
const truncNote = "(output truncated — narrow the window with offset and limit)\n"

// Read returns file contents as a numbered observation.
type Read struct{}

// New creates a read tool.
func New() *Read { return &Read{} }

// Name implements agent.Tool.
func (r *Read) Name() string { return "read" }

// Risk implements agent.Tool: reading only observes, so the agent
// loop runs it without operator approval.
func (r *Read) Risk() agent.Risk { return agent.RiskReadOnly }

// Description implements agent.Tool.
func (r *Read) Description() string {
	return `Read a file under the working directory and return it with
1-based line numbers. Use it to inspect source, configs, and notes
without a shell round trip. Pass arguments as JSON:
{"path": "relative/path.txt", "offset": 1, "limit": 200}. "path" is
relative to the working directory and must stay inside it; "offset"
is the first line to return (default 1), "limit" the number of lines
(default 200). Large files are truncated — narrow the window and
read on.`
}

// Execute implements agent.Tool. Errors (missing file, path outside
// the working directory) are returned as errors; the agent loop
// turns them into observations, so the model sees what went wrong.
func (r *Read) Execute(ctx context.Context, args string) (string, error) {
	var call struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(args), &call); err != nil {
		return "", fmt.Errorf("invalid arguments, expected {\"path\": \"...\", \"offset\": 1, \"limit\": 200}: %w", err)
	}
	if call.Path == "" {
		return "", errors.New("empty path: pass {\"path\": \"...\"}")
	}

	path, err := resolveInWorkdir(call.Path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", call.Path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory, not a file", call.Path)
	}

	if call.Offset < 1 {
		call.Offset = 1
	}
	lines, truncated, err := readWindow(ctx, path, call.Offset, call.Limit)
	if err != nil {
		return "", err
	}
	return render(lines, call.Offset, truncated), nil
}

// resolveInWorkdir turns a relative path into an absolute one and
// rejects anything that leaves the working directory, including
// via symlink. Absolute paths are rejected outright.
func resolveInWorkdir(path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("path %s must be relative to the working directory", path)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	realCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}

	abs := filepath.Join(cwd, path)
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	if real != realCwd && !strings.HasPrefix(real, realCwd+string(filepath.Separator)) {
		return "", fmt.Errorf("path %s escapes the working directory", path)
	}
	return real, nil
}

// readWindow streams the file and returns the requested window of
// lines - the file is never loaded whole, and no line is ever held
// beyond maxLineLen bytes, so neither a huge file nor a huge line
// costs memory. The offset is 1-based; zero or negative means 1.
// The limit defaults to defaultLimit and is capped at maxLimit.
//
// The byte budget counts what render will actually emit - the
// "%5d|" prefix, the content, the ellipsis, the newline - and
// reserves space for the truncation note, so the final observation
// fits maxOutput no matter how many short lines the window holds.
// truncated reports that either cap hit. Scanning honors ctx, so
// cancellation during a long skip or a huge line is prompt. Binary
// detection sees only the requested window, not the whole file.
func readWindow(ctx context.Context, path string, offset, limit int) (lines []string, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("read file: %w", err)
	}
	defer f.Close()

	if offset < 1 {
		offset = 1
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	cancelled := func(err error) bool {
		return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	}

	r := bufio.NewReaderSize(f, 64*1024)
	// Skipping drains without retaining anything (limit 0).
	for i := 1; i < offset; i++ {
		if _, _, err := readCappedLine(ctx, r, 0); err != nil {
			if cancelled(err) {
				return nil, false, fmt.Errorf("read: %w", err)
			}
			return nil, false, fmt.Errorf("offset %d is beyond the end of the file", offset)
		}
	}

	budget := maxOutput - len(truncNote)
	total := 0
	for len(lines) < limit {
		line, tooLong, rerr := readCappedLine(ctx, r, maxLineLen)
		if cancelled(rerr) {
			return nil, false, fmt.Errorf("read: %w", rerr)
		}
		// EOF before the window produced anything: an empty file
		// is an empty file, but with lines skipped it means the
		// offset points past the last line.
		if rerr == io.EOF && line == "" {
			if len(lines) == 0 && offset > 1 {
				return nil, false, fmt.Errorf("offset %d is beyond the end of the file", offset)
			}
			break
		}
		if rerr != nil && rerr != io.EOF {
			return nil, false, fmt.Errorf("read file: %w", rerr)
		}
		if strings.ContainsRune(line, 0x00) {
			return nil, false, errors.New("binary file, refusing to read")
		}
		if tooLong {
			line += "…"
			truncated = true
		}
		rendered := fmt.Sprintf("%5d|%s\n", offset+len(lines), line)
		if total+len(rendered) > budget {
			truncated = true
			break
		}
		lines = append(lines, line)
		total += len(rendered)
	}
	return lines, truncated, nil
}

// readCappedLine reads one line, retaining at most the first limit
// bytes of it: ReadString would buffer a multi-megabyte line whole
// before the caller could truncate it, so this drains through the
// newline and drops everything past the cap. One extra byte is
// retained beyond the cap so a line of exactly limit bytes keeps
// its newline slot and is not falsely marked truncated; the newline
// is trimmed before the line is returned. tooLong reports that
// bytes were dropped. A limit of 0 drains the line and returns
// nothing. The drain honors ctx, so even one gigantic line cannot
// outlast a cancelled run.
func readCappedLine(ctx context.Context, r *bufio.Reader, limit int) (line string, tooLong bool, err error) {
	keep := limit + 1 // room for the newline
	var kept []byte
	for {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
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
		line := strings.TrimSuffix(string(kept), "\n")
		// The newline slot is reserved, so a terminated line of
		// exactly limit bytes fits whole. An unterminated one
		// (EOF) can arrive with the slot filled by content instead
		// - cut it here rather than trusting the caller.
		if len(line) > limit {
			line = line[:limit]
			tooLong = true
		}
		return line, tooLong, rerr
	}
}

// render numbers the lines of the window; readWindow has already
// capped the content. An empty window over a real file is an empty
// file, not an error.
func render(lines []string, offset int, truncated bool) string {
	if len(lines) == 0 && !truncated {
		return "(empty file)"
	}
	var sb strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&sb, "%5d|%s\n", offset+i, line)
	}
	if truncated {
		sb.WriteString(truncNote)
	}
	return sb.String()
}
