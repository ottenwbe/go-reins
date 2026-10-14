package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func call(t *testing.T, args string) (string, error) {
	t.Helper()
	return New().Execute(context.Background(), args)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSearchGlobListsFiles pins list mode: ** spans directories, a
// bare glob stays in one, and hidden entries are skipped.
func TestSearchGlobListsFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "main.go", "package main")
	writeFile(t, dir, "notes.txt", "hello")
	writeFile(t, dir, filepath.Join("internal", "util.go"), "x")
	writeFile(t, dir, filepath.Join(".git", "config.go"), "x")

	out, err := call(t, `{"glob":"**/*.go"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"main.go\n", "internal/util.go\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "notes.txt") || strings.Contains(out, ".git") {
		t.Errorf("output matched non-go or hidden paths:\n%s", out)
	}

	// A bare glob matches only the root directory.
	out, err = call(t, `{"glob":"*.go"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "main.go") || strings.Contains(out, "internal/util.go") {
		t.Errorf("bare glob should match only the root:\n%s", out)
	}

	// ** alone matches every non-hidden file.
	out, err = call(t, `{"glob":"**"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "notes.txt") || strings.Contains(out, ".git") {
		t.Errorf("** should match all non-hidden files:\n%s", out)
	}
}

// TestSearchQueryGrepsLines pins grep mode: path:line:text output,
// regular expression semantics, and 1-based line numbers.
func TestSearchQueryGrepsLines(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "a.txt", "first\nsecond\nthird\n")
	writeFile(t, dir, filepath.Join("sub", "b.txt"), "alpha\nsecond too\n")

	out, err := call(t, `{"glob":"**/*.txt","query":"second"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"a.txt:2:second\n", "sub/b.txt:2:second too\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// The query is a regular expression.
	out, err = call(t, `{"glob":"**/*.txt","query":"colou?r"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "(no matches)" {
		t.Errorf("regex query should not have matched:\n%s", out)
	}
	writeFile(t, dir, "c.txt", "color\ncolour\n")
	out, err = call(t, `{"glob":"c.txt","query":"colou?r"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "c.txt:1:color") || !strings.Contains(out, "c.txt:2:colour") {
		t.Errorf("regex alternation missing matches:\n%s", out)
	}
}

// TestSearchNoMatchesIsAResult pins that an empty search is an
// observation, not an error: the model must learn its glob or
// query found nothing.
func TestSearchNoMatchesIsAResult(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "a.go", "x")

	out, err := call(t, `{"glob":"**/*.md"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "(no matches)" {
		t.Errorf("empty search = %q, want (no matches)", out)
	}

	out, err = call(t, `{"glob":"**/*.go","query":"nothing here"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "(no matches)" {
		t.Errorf("empty grep = %q, want (no matches)", out)
	}
}

// TestSearchRejectsBadArguments pins argument validation: an empty
// glob, an absolute glob, one that climbs out of the working
// directory, an invalid query regex, and unparseable JSON all
// fail as errors the model can react to.
func TestSearchRejectsBadArguments(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	for name, args := range map[string]string{
		"empty glob": `{"glob":""}`,
		"absolute":   `{"glob":"/tmp/*.go"}`,
		"escaping":   `{"glob":"../**"}`,
		"nested up":  `{"glob":"a/../.."}`,
		"bad regex":  `{"glob":"*.go","query":"("}`,
		"bad json":   `{`,
	} {
		if _, err := call(t, args); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// TestSearchCapsOutput pins the caps: the requested limit bounds
// the result lines, the byte budget truncates with a note, and a
// single huge line is cut with an ellipsis instead of blowing the
// budget.
func TestSearchCapsOutput(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for i := 0; i < 30; i++ {
		writeFile(t, dir, fmt.Sprintf("f%02d.go", i), "match\n")
	}

	// The line limit truncates with the note.
	out, err := call(t, `{"glob":"*.go","limit":10}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if lines := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; lines != 11 {
		t.Errorf("limit 10 should yield 10 results + note, got %d lines:\n%s", lines, out)
	}
	if !strings.Contains(out, "output truncated") {
		t.Errorf("truncation note missing:\n%s", out)
	}

	// A huge line is capped with an ellipsis and the match still
	// reports the right line number.
	big := strings.Repeat("x", 10_000)
	writeFile(t, dir, "big.txt", "short\n"+big+" match\n")
	out, err = call(t, `{"glob":"big.txt","query":"match"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "big.txt:2:") || !strings.HasSuffix(strings.TrimRight(out, "\n"), "…") {
		t.Errorf("huge line not capped with an ellipsis:\n%.200s…", out)
	}
	if len(out) > maxOutput {
		t.Errorf("output exceeds the byte budget: %d > %d", len(out), maxOutput)
	}
}

// TestSearchSkipsBinaryFiles pins grep mode over binary content: a
// NUL byte makes the file vanish from the results, not an error.
func TestSearchSkipsBinaryFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "bin.dat", "match\x00binary\nmatch\n")
	writeFile(t, dir, "plain.txt", "match\n")

	out, err := call(t, `{"glob":"**/*","query":"match"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out, "bin.dat") {
		t.Errorf("binary file leaked into the results:\n%s", out)
	}
	if !strings.Contains(out, "plain.txt:1:match") {
		t.Errorf("plain match missing:\n%s", out)
	}
}

// TestSearchHonorsCancellation pins that a cancelled context stops
// the search instead of grinding through the whole tree.
func TestSearchHonorsCancellation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "a.go", "x")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New().Execute(ctx, `{"glob":"**"}`); err == nil {
		t.Error("cancelled context should fail the search")
	}
}

// TestSearchSkipsSymlinks pins the escape hatch staying shut: a
// symlink inside the working directory can point anywhere, and the
// search must neither list it nor read through it - grep mode
// especially, or a link to a file outside the tree would leak its
// contents through a read-only, unapproved tool.
func TestSearchSkipsSymlinks(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	t.Chdir(dir)
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	// An in-tree symlink is skipped too: the tool's contract is
	// regular files only.
	writeFile(t, dir, "real.txt", "also secret\n")
	if err := os.Symlink("real.txt", filepath.Join(dir, "alias.txt")); err != nil {
		t.Fatal(err)
	}

	out, err := call(t, `{"glob":"**/*.txt","query":"secret"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out, "leak.txt") || strings.Contains(out, "alias.txt") || strings.Contains(out, "top secret") {
		t.Errorf("symlink leaked into the results:\n%s", out)
	}
	if !strings.Contains(out, "real.txt:1:also secret") {
		t.Errorf("regular file match missing:\n%s", out)
	}

	// List mode skips symlinks just the same.
	out, err = call(t, `{"glob":"**/*.txt"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out, "leak.txt") || strings.Contains(out, "alias.txt") {
		t.Errorf("symlink listed:\n%s", out)
	}
}

// TestSearchMultiStarGlobs pins patterns with several ** segments:
// they match deeply nested paths, and a pattern that matches
// nothing must terminate - the matcher is a dynamic program, not a
// backtracking search, so adversarial globs cannot stall it.
func TestSearchMultiStarGlobs(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, filepath.Join("a", "b", "c", "d", "f.txt"), "deep\n")
	writeFile(t, dir, "shallow.txt", "flat\n")

	out, err := call(t, `{"glob":"**/b/**/f.txt"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "a/b/c/d/f.txt") || strings.Contains(out, "shallow.txt") {
		t.Errorf("multi-** glob matched wrong paths:\n%s", out)
	}

	// Several ** segments that match nothing terminate promptly.
	out, err = call(t, `{"glob":"**/**/**/nope/**/x"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "(no matches)" {
		t.Errorf("unmatched glob = %q, want (no matches)", out)
	}

	// ** inside a name segment is two stars - within a segment
	// that is just a greedy *, so f**.txt matches f.txt the way
	// f*.txt does; only a whole ** segment spans directories.
	out, err = call(t, `{"glob":"**/f**.txt"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "a/b/c/d/f.txt") || strings.Contains(out, "shallow.txt") {
		t.Errorf("within-segment ** misbehaved:\n%s", out)
	}
}

// TestSearchReportsPartialLines pins the incompleteness note: a
// line longer than the 1 MiB match window is only matched in its
// first part, and the observation says so - a match past the
// window must not silently vanish.
func TestSearchReportsPartialLines(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// A match inside the window is found, and the note still
	// appears because the line was not fully searched.
	writeFile(t, dir, "early.txt", "needle"+strings.Repeat("x", 2<<20)+"\n")
	out, err := call(t, `{"glob":"early.txt","query":"needle"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "early.txt:1:") {
		t.Errorf("in-window match missing:\n%s", out)
	}
	if !strings.Contains(out, "matched only in their first 1 MiB") {
		t.Errorf("partial note missing:\n%s", out)
	}

	// A match past the window is not found, and the note explains
	// why instead of reporting a plain no-matches.
	writeFile(t, dir, "late.txt", strings.Repeat("x", 2<<20)+"needle\n")
	out, err = call(t, `{"glob":"late.txt","query":"^needle$"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out, "late.txt") {
		t.Errorf("out-of-window match should not be found:\n%s", out)
	}
	if !strings.Contains(out, "matched only in their first 1 MiB") {
		t.Errorf("partial note missing for an unmatched long line:\n%s", out)
	}
	if out == "(no matches)" {
		t.Errorf("long-line result must not claim a plain (no matches):\n%s", out)
	}
	if len(out) > maxOutput {
		t.Errorf("observation exceeds the byte budget: %d", len(out))
	}
}

// TestSearchRejectsMalformedGlob pins the symmetry with the query:
// a syntactically broken glob fails as an error, not as a silent
// (no matches) that would read to the model as "nothing there".
func TestSearchRejectsMalformedGlob(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, "a.txt", "x")

	for _, glob := range []string{`[.txt`, `a/[`} {
		_, err := call(t, `{"glob":"`+glob+`"}`)
		if err == nil {
			t.Errorf("glob %q: want an error, got none", glob)
		}
	}

	// Well-formed bracket classes still work.
	out, err := call(t, `{"glob":"[ab].txt"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "a.txt") {
		t.Errorf("valid bracket class should match:\n%s", out)
	}
}
