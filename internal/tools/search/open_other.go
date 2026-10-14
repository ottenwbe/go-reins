//go:build !unix

package search

import "os"

// openNoBlock opens path the portable way. Platforms without
// O_NONBLOCK have no FIFO park in the first place (named pipes
// behave differently), so a plain open is the best available.
func openNoBlock(path string) (*os.File, error) {
	return os.Open(path)
}
