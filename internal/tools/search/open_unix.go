//go:build unix

package search

import (
	"os"
	"syscall"
)

// openNoBlock opens path without blocking. A path the walk vetted
// as a regular file can be swapped for a FIFO before the open; a
// plain os.Open would then park until a writer appears - long
// before the identity checks could run. O_NONBLOCK makes the open
// return immediately, the fstat then rejects what is not a regular
// file, and the flag is irrelevant to reads from regular files, so
// it stays on.
func openNoBlock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
