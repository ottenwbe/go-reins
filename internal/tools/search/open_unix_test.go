//go:build unix

package search

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// TestOpenNoBlockDoesNotParkOnFIFO pins the reason openNoBlock
// exists: opening a FIFO must return immediately instead of
// waiting for a writer. The test fails by hanging (the go test
// timeout turns that into a failure), so a regression to a plain
// os.Open shows up here, not as a stuck agent in the field.
func TestOpenNoBlockDoesNotParkOnFIFO(t *testing.T) {
	dir := t.TempDir()
	fifo := dir + "/pipe"
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		var f *os.File
		f, err = openNoBlock(fifo)
		if f != nil {
			f.Close()
		}
	}()
	select {
	case <-done:
		if err != nil {
			t.Fatalf("openNoBlock: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("open on a FIFO blocked - the search can be parked by a swapped file")
	}
}
