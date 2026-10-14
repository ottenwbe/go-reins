//go:build !unix

package search

import "os"

// openForSearch opens a file the walk already vetted. This is the
// portable fallback: it cannot request O_NONBLOCK, so the
// nonblocking guarantee holds only where the Unix implementation
// is compiled in - the stat-regular and SameFile checks after the
// open still guard what is read, but the open itself may block on
// a swapped special file.
func openForSearch(path string) (*os.File, error) {
	return os.Open(path)
}
