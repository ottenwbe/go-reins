//go:build debugui

package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// This file is compiled only with -tags debugui (make build-debug):
// the chat then offers the /debug state overlay. The off build
// (debug_off.go) compiles no-ops in its place, so the feature
// cannot reach a release binary by accident.

// debugCommands appends the overlay command to every place the
// chat lists its slash commands.
const debugCommands = ", /debug"

// debugCommand toggles the overlay for hands-on testing.
func (m *chatModel) debugCommand() tea.Cmd {
	m.debug = !m.debug
	if m.debug {
		m.appendLine(dimStyle.Render("debug overlay on"))
	} else {
		m.appendLine(dimStyle.Render("debug overlay off"))
	}
	return nil
}

// debugOverlay renders the state readout: what the REPL is waiting
// for, whether a step's waiters are armed, and how much live text
// the forming line holds. The View calls it on every frame, so the
// spinner keeps it fresh even between deltas.
func (m *chatModel) debugOverlay() string {
	step := "none"
	if m.step != nil {
		step = "armed"
	}
	return "\n" + dimStyle.Render(fmt.Sprintf(
		"[debug] state=%s step=%s live=%d chars", m.state, step, len(m.live)))
}
