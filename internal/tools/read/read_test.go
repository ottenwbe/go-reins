package read

import (
	"context"
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
