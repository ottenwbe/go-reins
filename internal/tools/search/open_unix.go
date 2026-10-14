//go:build unix

package search

import (
	"os"
	"syscall"
)

// openForSearch opens a file the walk already vetted, without
// letting a swapped FIFO park the search. The Unix implementation
// requests O_NONBLOCK: a path replaced by a FIFO between the walk
// and the open would otherwise block until a writer appears -
// long before the identity checks could run. The flag is
// irrelevant to reads from regular files, so it stays on after
// the checks.
func openForSearch(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
