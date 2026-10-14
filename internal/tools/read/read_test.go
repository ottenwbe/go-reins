package read

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-reins/internal/agent"
)

func call(t *testing.T, args string) (string, error) {
	t.Helper()
	return New().Execute(context.Background(), args)
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadReturnsNumberedLines(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "notes.txt", "alpha\nbeta\ngamma\n")

	out, err := call(t, `{"path":"notes.txt"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"    1|alpha", "    2|beta", "    3|gamma"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
			break
		}
	}
	if lines := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; lines != 3 {
		t.Errorf("line count = %d, want 3", lines)
	}
}

func TestReadOffsetAndLimitWindow(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	content := make([]string, 50)
	for i := range content {
		content[i] = "line"
	}
	writeFile(t, dir, "big.txt", strings.Join(content, "\n"))

	out, err := call(t, `{"path":"big.txt","offset":10,"limit":3}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "   10|line") || !strings.Contains(out, "   12|line") {
		t.Errorf("window missing its first or last line:\n%s", out)
	}
	if strings.Contains(out, "    9|line") || strings.Contains(out, "   13|line") {
		t.Error("window wider than requested")
	}
}

func TestReadTruncatesHugeOutput(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	long := strings.Repeat("x", 500)
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, long)
	}
	writeFile(t, dir, "huge.txt", strings.Join(lines, "\n"))

	out, err := call(t, `{"path":"huge.txt"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(out) > maxOutput+200 {
		t.Errorf("output length = %d, want capped near %d", len(out), maxOutput)
	}
	if !strings.Contains(out, "(output truncated") {
		t.Error("output missing truncation note")
	}
}

func TestReadRejectsPathsOutsideWorkdir(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "inside.txt", "safe\n")

	cases := []string{
		`{"path":"../../etc/passwd"}`,
		`{"path":"/etc/passwd"}`,
	}
	for _, args := range cases {
		if _, err := call(t, args); err == nil {
			t.Errorf("Execute(%s): want rejection, got nil error", args)
		}
	}

	if _, err := call(t, `{"path":"inside.txt"}`); err != nil {
		t.Errorf("Execute inside workdir failed: %v", err)
	}
}

func TestReadRejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	t.Chdir(dir)
	writeFile(t, outside, "secret.txt", "classified\n")

	link := filepath.Join(dir, "innocent.txt")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := call(t, `{"path":"innocent.txt"}`); err == nil {
		t.Error("Execute via symlink: want rejection, got nil error")
	}
}

func TestReadErrorsAreObservations(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if _, err := call(t, `{"path":"missing.txt"}`); err == nil {
		t.Error("missing file: want error")
	}
	if _, err := call(t, `{"path":"."}`); err == nil {
		t.Error("directory: want error")
	}
	if _, err := call(t, `{}`); err == nil {
		t.Error("empty args: want error")
	}
	writeFile(t, dir, "bin.dat", "a\x00b")
	if _, err := call(t, `{"path":"bin.dat"}`); err == nil {
		t.Error("binary file: want error")
	}
}

func TestReadRiskIsReadOnly(t *testing.T) {
	tool := New()
	if tool.Risk() != agent.RiskReadOnly {
		t.Errorf("Risk = %v, want read-only", tool.Risk())
	}
}

// TestReadCapsSingleHugeLine pins the line-level cap: one
// multi-megabyte line must not blow the output budget before the
// budget check runs.
func TestReadCapsSingleHugeLine(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	huge := strings.Repeat("x", 1024*1024)
	writeFile(t, dir, "huge-line.txt", huge+"\n")

	out, err := call(t, `{"path":"huge-line.txt"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(out) > maxOutput+100 {
		t.Errorf("output length = %d, want capped near %d", len(out), maxOutput)
	}
	if !strings.Contains(out, "(output truncated") {
		t.Error("output missing truncation note")
	}
}

// TestReadSkipsWithoutLoading pins the streaming path: a window far
// into a large file returns quickly without holding the file in
// memory (the important property is that skipped lines are never
// buffered at all).
func TestReadSkipsWithoutLoading(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var lines []string
	for i := 1; i <= 5000; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	writeFile(t, dir, "big.txt", strings.Join(lines, "\n"))

	out, err := call(t, `{"path":"big.txt","offset":4999,"limit":2}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "line 4999") || !strings.Contains(out, "line 5000") {
		t.Errorf("window wrong:\n%s", out)
	}
	if strings.Contains(out, "line 1|") {
		t.Error("window started at the beginning")
	}
}

func TestReadOffsetBeyondEnd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "small.txt", "one\ntwo\n")

	if _, err := call(t, `{"path":"small.txt","offset":10}`); err == nil {
		t.Error("offset past EOF: want error")
	}
}

// TestReadOffsetOnePastLastLine pins the trailing-newline boundary:
// an offset that points exactly one line past the last real line
// must report out-of-range, not "(empty file)".
func TestReadOffsetOnePastLastLine(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "two.txt", "one\ntwo\n")

	if _, err := call(t, `{"path":"two.txt","offset":3}`); err == nil {
		t.Error("offset one past last line: want error")
	}

	// One line further is still an error, and so is skipping past a
	// file whose last line has no newline.
	if _, err := call(t, `{"path":"two.txt","offset":9}`); err == nil {
		t.Error("offset far past end: want error")
	}
	writeFile(t, dir, "nonl.txt", "only")
	if _, err := call(t, `{"path":"nonl.txt","offset":2}`); err == nil {
		t.Error("offset past last line without newline: want error")
	}
}

// TestReadEmptyFileStillReportsAsEmpty makes sure the boundary fix
// did not eat the legitimate case: a zero-byte file is an empty
// file, not an out-of-range offset.
func TestReadEmptyFileStillReportsAsEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "empty.txt", "")

	out, err := call(t, `{"path":"empty.txt"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "(empty file)" {
		t.Errorf("empty file output = %q, want (empty file)", out)
	}
}

// TestReadBudgetCountsRenderedBytes pins that the cap applies to the
// observation as rendered - line-number prefixes and newlines
// included - so a window of many short lines cannot exceed it.
func TestReadBudgetCountsRenderedBytes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "many.txt", strings.Repeat("\n", 1000))

	out, err := call(t, `{"path":"many.txt","limit":1000}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(out) > maxOutput {
		t.Errorf("rendered length = %d, want <= %d (prefixes must count)", len(out), maxOutput)
	}
	if !strings.Contains(out, "(output truncated") {
		t.Error("window was cut but carries no truncation note")
	}
}

// TestReadLineExactlyAtCapNotTruncated pins the newline off-by-one:
// a line of exactly maxLineLen bytes fits without truncation, and
// one byte more gets the ellipsis.
func TestReadLineExactlyAtCapNotTruncated(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "exact.txt", strings.Repeat("x", maxLineLen)+"\n")

	out, err := call(t, `{"path":"exact.txt"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out, "…") || strings.Contains(out, "(output truncated") {
		t.Error("line of exactly maxLineLen must not be truncated")
	}

	writeFile(t, dir, "over.txt", strings.Repeat("x", maxLineLen+1)+"\n")
	out, err = call(t, `{"path":"over.txt"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "…") {
		t.Error("line over maxLineLen must carry the truncation marker")
	}
}

// TestReadHonorsCancellation pins that a cancelled context aborts
// the scan instead of grinding through a long skip.
func TestReadHonorsCancellation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var lines []string
	for i := 1; i <= 100000; i++ {
		lines = append(lines, "line")
	}
	writeFile(t, dir, "big.txt", strings.Join(lines, "\n"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New().Execute(ctx, `{"path":"big.txt","offset":99999}`)
	if err == nil {
		t.Fatal("cancelled scan: want error")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("error = %v, want context canceled", err)
	}
}

// TestReadUnterminatedLineAtCap pins the EOF half of the line-cap
// boundary: without a trailing newline, the reserved newline slot
// must not let content past the cap through.
func TestReadUnterminatedLineAtCap(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "exact-eof.txt", strings.Repeat("x", maxLineLen))

	out, err := call(t, `{"path":"exact-eof.txt"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out, "…") {
		t.Error("unterminated line of exactly maxLineLen must not be truncated")
	}

	writeFile(t, dir, "over-eof.txt", strings.Repeat("x", maxLineLen+1))
	out, err = call(t, `{"path":"over-eof.txt"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "…") {
		t.Error("unterminated line over maxLineLen must carry the truncation marker")
	}
}

// TestReadFollowsInTreeSymlink pins that the root-constrained open
// stays as permissive as the old containment check: a symlink that
// resolves inside the working directory is followed and read
// normally. Only escapes are refused - the tree's own links are
// the operator's business.
func TestReadFollowsInTreeSymlink(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "real.txt", "contents\n")

	// A relative target: the link resolves inside the root and is
	// followed. An absolute target is refused even when it names a
	// file inside the tree - it is not relative to the root.
	if err := os.Symlink("real.txt", filepath.Join(dir, "alias.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	out, err := call(t, `{"path":"alias.txt"}`)
	if err != nil {
		t.Fatalf("Execute via in-tree symlink: %v", err)
	}
	if !strings.Contains(out, "contents") {
		t.Errorf("output missing the file's contents:\n%s", out)
	}
}
