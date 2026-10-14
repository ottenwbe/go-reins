//go:build debugui

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"go-reins/internal/agent"
)

// TestChatDebugOverlay verifies the /debug toggle in the debugui
// build: off by default, on it tracks the state machine through a
// step, off again it disappears.
func TestChatDebugOverlay(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	a := agent.New(&stubBackend{reply: "done"}, "m", "sys", nil)
	m := startChat(a, gate, nil)

	// Off by default: the overlay is absent while idle.
	if strings.Contains(m.View().Content, "[debug]") {
		t.Fatal("debug overlay shown without /debug")
	}

	// /debug turns it on and reports the ready state.
	m, _ = runCommand(m, "/debug")
	if !strings.Contains(m.View().Content, "[debug] state=ready step=none") {
		t.Errorf("overlay missing or wrong: %q", m.View().Content)
	}

	// A submitted prompt flips the tracked state and arms the step.
	next, _ := m.Update(tea.KeyPressMsg{Code: 'h', Text: "hello"})
	m = next.(*chatModel)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(*chatModel)
	if !strings.Contains(m.View().Content, "[debug] state=busy step=armed") {
		t.Errorf("overlay missing busy step: %q", m.View().Content)
	}

	// Streamed text grows the live counter the overlay reports.
	next, _ = m.Update(StreamEvent{Delta: "abc"})
	m = next.(*chatModel)
	if !strings.Contains(m.View().Content, "live=3 chars") {
		t.Errorf("overlay missing live size: %q", m.View().Content)
	}

	// The step ends: back to ready, live drained.
	next, _ = m.Update(stepDoneMsg{res: agent.RunResult{Answer: "done"}})
	m = next.(*chatModel)
	if !strings.Contains(m.View().Content, "[debug] state=ready step=none live=0 chars") {
		t.Errorf("overlay after step: %q", m.View().Content)
	}

	// /debug again turns it off.
	m, _ = runCommand(m, "/debug")
	if strings.Contains(m.View().Content, "[debug]") {
		t.Error("debug overlay shown after toggling off")
	}
}
