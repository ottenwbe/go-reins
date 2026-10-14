//go:build !debugui

package tui

import (
	tea "charm.land/bubbletea/v2"
)

// This file is the default build: the /debug overlay is not
// compiled in, so the chat offers no way to reach it. Build with
// -tags debugui (make build-debug) to get it.

// debugCommands appends nothing: the overlay command is absent.
const debugCommands = ""

// debugCommand explains that this build carries no overlay.
func (m *chatModel) debugCommand() tea.Cmd {
	m.appendLine(errorStyle.Render("debug overlay not built in - build with make build-debug"))
	return nil
}

// debugOverlay has nothing to render in this build.
func (m *chatModel) debugOverlay() string { return "" }
