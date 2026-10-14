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

// debugMsg logs one message to the transcript, so the overlay
// doubles as an event log: every message Update accepts leaves a
// dim trace right before its effect. Spinner ticks and cursor
// blinks are timing artifacts of the view, not events - logging
// them would flood the transcript at frame rate - so they stay
// out; everything else is traced with what matters for its case.
func (m *chatModel) debugMsg(msg tea.Msg) {
	if !m.debug {
		return
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		m.appendLine(dimStyle.Render("[debug] <- key " + msg.String()))
	case tea.WindowSizeMsg:
		m.appendLine(dimStyle.Render(fmt.Sprintf("[debug] <- WindowSize %dx%d", msg.Width, msg.Height)))
	case StreamEvent:
		m.appendLine(dimStyle.Render(fmt.Sprintf("[debug] <- StreamEvent %d chars", len(msg.Delta))))
	case ToolEvent:
		m.appendLine(dimStyle.Render("[debug] <- ToolEvent " + msg.Name + " " + shorten(msg.Args, 40)))
	case ApprovalRequest:
		m.appendLine(dimStyle.Render("[debug] <- ApprovalRequest " + msg.Tool))
	case stepDoneMsg:
		if msg.err != nil {
			m.appendLine(dimStyle.Render("[debug] <- stepDoneMsg error " + msg.err.Error()))
		} else {
			m.appendLine(dimStyle.Render(fmt.Sprintf("[debug] <- stepDoneMsg %d turn(s)", msg.res.Turns)))
		}
	case modelsDoneMsg:
		m.appendLine(dimStyle.Render("[debug] <- modelsDoneMsg backend=" + msg.backend))
	}
}
