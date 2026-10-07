package tui

import (
	"os"

	"golang.org/x/term"
)

// Interactive reports whether stdin and stdout are both terminals;
// the bubbletea views need them, anything else gets the plain flow.
func Interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}
