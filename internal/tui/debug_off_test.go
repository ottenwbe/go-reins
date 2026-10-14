//go:build !debugui

package tui

import (
	"strings"
	"testing"

	"go-reins/internal/agent"
)

// TestDebugCommandNotBuiltIn pins the default build: /debug exists
// only as an explanation, the overlay never renders, and the
// command hints never advertise it.
func TestDebugCommandNotBuiltIn(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	a := agent.New(&stubBackend{reply: "done"}, "m", "sys", nil)
	m := startChat(a, gate, nil)

	// /debug explains the missing overlay instead of toggling one.
	m, _ = runCommand(m, "/debug")
	if !strings.Contains(strings.Join(m.lines, "\n"), "not built in") {
		t.Errorf("/debug did not report the missing overlay: %v", m.lines)
	}
	if strings.Contains(m.View().Content, "[debug]") {
		t.Error("debug overlay rendered in a build without debugui")
	}

	// The command hints never mention it: the input placeholder
	// and the unknown-command help list the offered commands only.
	if strings.Contains(m.textarea.Placeholder, "/debug") {
		t.Errorf("placeholder advertises /debug: %q", m.textarea.Placeholder)
	}
	m, _ = runCommand(m, "/nope")
	if hint := strings.Join(m.lines[len(m.lines)-1:], "\n"); strings.Contains(hint, "/debug") {
		t.Errorf("unknown-command help advertises /debug: %q", hint)
	}
}
