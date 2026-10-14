//go:build unix

package search

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"
	"time"

	"io/fs"
)

// TestOpenForSearchDoesNotParkOnFIFO pins the reason openForSearch exists
// exists: opening a FIFO must return immediately instead of
// waiting for a writer. The test fails by hanging (the go test
// timeout turns that into a failure), so a regression to a plain
// os.Open shows up here, not as a stuck agent in the field.
func TestOpenForSearchDoesNotParkOnFIFO(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		var f *os.File
		f, err = openForSearch(fifo)
		if f != nil {
			f.Close()
		}
	}()
	select {
	case <-done:
		if err != nil {
			t.Fatalf("openForSearch: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("open on a FIFO blocked - the search can be parked by a swapped file")
	}
}

// TestGrepIntoSkipsFIFO pins the caller side of the same guard:
// even if a FIFO reaches grepInto - the walk vetted a regular file
// that was swapped in between - the open returns promptly and the
// descriptor's stat rejects what is not regular before a single
// byte is read. The guard fails by hanging, which the test turns
// into a timeout failure.
func TestGrepIntoSkipsFIFO(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	done := make(chan struct{})
	res := newResults(defaultLimit)
	re := regexp.MustCompile("anything")
	var err error
	var stop bool
	go func() {
		defer close(done)
		// The walk-time stat is irrelevant here: the regular-file
		// check must reject the FIFO before the identity check.
		stop, err = grepInto(context.Background(), fifo, fifoStat(t), re, res)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("grepInto on a FIFO blocked - a swapped file can park the search")
	}
	if err != nil {
		t.Fatalf("grepInto: %v", err)
	}
	if stop {
		t.Error("a skipped file must not stop the search")
	}
	if got := res.render(); got != "(no matches)" {
		t.Errorf("results = %q, want (no matches)", got)
	}
}

// fifoStat returns any regular-file stat to stand in for the
// walk-time entry: grepInto must reject the FIFO on the
// regular-file check, so the identity check must never matter.
func fifoStat(t *testing.T) fs.FileInfo {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plain.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
