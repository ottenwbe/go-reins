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

	// /debug again turns it off: the overlay line is gone; only
	// the message-log traces (which mention [debug] too) remain.
	m, _ = runCommand(m, "/debug")
	if strings.Contains(m.View().Content, "[debug] state=") {
		t.Error("debug overlay shown after toggling off")
	}
}

// TestChatDebugMsgLog verifies that with the overlay on, Update's
// messages leave a trace in the transcript: keys, stream deltas,
// and the step result - and that a message dropped by the state
// guards still shows up, since the log records arrivals, not
// effects.
func TestChatDebugMsgLog(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	a := agent.New(&stubBackend{reply: "done"}, "m", "sys", nil)
	m := startChat(a, gate, nil)

	// Before the overlay is on, nothing is logged.
	next, _ := m.Update(tea.KeyPressMsg{Code: 'h', Text: "hello"})
	m = next.(*chatModel)
	if strings.Contains(strings.Join(m.lines, "\n"), "[debug] <-") {
		t.Fatal("message logged before /debug")
	}
	m.textarea.Reset() // drop the typed text so /debug submits as a command
	m, _ = runCommand(m, "/debug")

	// A keypress and a step each leave their trace; the stream
	// delta records its size, the step result its turn count.
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(*chatModel)
	next, _ = m.Update(StreamEvent{Delta: "abc"})
	m = next.(*chatModel)
	next, _ = m.Update(stepDoneMsg{res: agent.RunResult{Answer: "done", Turns: 2}})
	m = next.(*chatModel)

	logged := strings.Join(m.lines, "\n")
	for _, want := range []string{
		"[debug] <- key",
		"[debug] <- StreamEvent 3 chars",
		"[debug] <- stepDoneMsg 2 turn(s)",
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("trace missing %q:\n%s", want, logged)
		}
	}

	// A stale delta arrives while ready: it is dropped by the
	// state guard, but the log still recorded its arrival.
	next, _ = m.Update(StreamEvent{Delta: "stale"})
	m = next.(*chatModel)
	logged = strings.Join(m.lines, "\n")
	if !strings.Contains(logged, "[debug] <- StreamEvent 5 chars") {
		t.Errorf("dropped message not logged: %s", logged)
	}
	if strings.Contains(m.View().Content, "stale") {
		t.Error("dropped delta reached the transcript")
	}
}
