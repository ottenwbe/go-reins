package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"go-reins/internal/agent"
	"go-reins/internal/backend"
)

// TestChatModelStepFlow exercises the prompt -> step -> answer cycle.
func TestChatModelStepFlow(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	a := agent.New(&stubBackend{reply: "the answer"}, "m", "sys", nil)
	m := newChatModel(a, gate, NewToolFeed(), "go-reins chat · stub/m")
	m.Init() // focuses the input area

	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(*chatModel)

	// Typing reaches the input area; enter submits and starts a step.
	next, _ = m.Update(tea.KeyPressMsg{Code: 'h', Text: "hello"})
	m = next.(*chatModel)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(*chatModel)
	if m.state != chatBusy {
		t.Fatalf("state after submit = %v, want chatBusy", m.state)
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "hello") {
		t.Errorf("prompt not logged: %v", m.lines)
	}

	// The step finishes: the answer lands in the transcript, history
	// is carried, and the input is ready again.
	next, _ = m.Update(stepDoneMsg{res: agent.RunResult{
		Answer: "the answer",
		Turns:  1,
		History: []backend.Message{
			{Role: backend.RoleSystem, Content: "sys"},
			{Role: backend.RoleUser, Content: "hello"},
			{Role: backend.RoleAssistant, Content: "the answer"},
		},
	}})
	m = next.(*chatModel)
	if m.state != chatReady {
		t.Errorf("state after step = %v, want chatReady", m.state)
	}
	if len(m.history) != 3 {
		t.Errorf("history length = %d, want 3", len(m.history))
	}
	transcript := m.View().Content
	if !strings.Contains(transcript, "the answer") {
		t.Errorf("answer missing from view: %q", transcript)
	}

	// A step error keeps the session usable.
	next, _ = m.Update(stepDoneMsg{err: errors.New("backend down")})
	m = next.(*chatModel)
	if m.state != chatReady {
		t.Errorf("state after step error = %v, want chatReady", m.state)
	}
	if !strings.Contains(m.View().Content, "backend down") {
		t.Error("error missing from transcript")
	}
}

// TestChatShowsCurrentTool verifies that tool events from the feed
// show up in the busy view and clear when the step finishes.
func TestChatShowsCurrentTool(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	feed := NewToolFeed()
	defer feed.Close()
	a := agent.New(&stubBackend{reply: "done"}, "m", "sys", nil)
	m := newChatModel(a, gate, feed, "go-reins chat · stub/m")
	m.Init()

	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(*chatModel)

	// A step is running; the agent reports the tool it is using.
	next, _ = m.Update(tea.KeyPressMsg{Code: 'h', Text: "hello"})
	m = next.(*chatModel)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(*chatModel)
	if m.state != chatBusy {
		t.Fatalf("state after submit = %v, want chatBusy", m.state)
	}

	next, _ = m.Update(ToolEvent{Name: "read", Args: `{"path":"notes.txt"}`})
	m = next.(*chatModel)
	view := m.View().Content
	if !strings.Contains(view, "read") || !strings.Contains(view, "notes.txt") {
		t.Errorf("busy view missing tool and args: %q", view)
	}

	// The step finishes and the tool line clears.
	next, _ = m.Update(stepDoneMsg{res: agent.RunResult{Answer: "done"}})
	m = next.(*chatModel)
	if m.tool.Name != "" {
		t.Errorf("tool after step = %q, want cleared", m.tool.Name)
	}
}

// TestChatModelMultilineInput verifies that ctrl+j inserts a newline
// into the prompt while enter still submits the whole text.
func TestChatModelMultilineInput(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	a := agent.New(&stubBackend{reply: "ok"}, "m", "sys", nil)
	m := newChatModel(a, gate, NewToolFeed(), "go-reins chat · stub/m")
	m.Init()

	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(*chatModel)

	keys := []tea.KeyPressMsg{
		{Code: 'l', Text: "line one"},
		{Code: 'j', Mod: tea.ModCtrl},
		{Code: 'l', Text: "line two"},
	}
	for _, k := range keys {
		next, _ := m.Update(k)
		m = next.(*chatModel)
	}
	if got := m.textarea.Value(); got != "line one\nline two" {
		t.Fatalf("input after ctrl+j = %q, want %q", got, "line one\nline two")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(*chatModel)
	if m.state != chatBusy {
		t.Fatalf("state after enter = %v, want chatBusy", m.state)
	}
	if m.prompt != "line one\nline two" {
		t.Errorf("submitted prompt = %q, want multi-line prompt", m.prompt)
	}
}
