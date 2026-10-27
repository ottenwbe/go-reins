package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"go-reins/internal/agent"
	"go-reins/internal/backend"
)

// stubBackend answers every chat request with the same reply.
type stubBackend struct{ reply string }

func (f *stubBackend) Name() string { return "stub" }
func (f *stubBackend) Chat(context.Context, backend.ChatRequest) (backend.ChatResponse, error) {
	return backend.ChatResponse{Content: f.reply}, nil
}
func (f *stubBackend) Models(context.Context) ([]string, error) {
	return []string{"stub-model"}, nil
}

func TestRunTaskCompletesHeadless(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	a := agent.New(&stubBackend{reply: "all done"}, "m", "sys", nil)

	res, err := RunTask(a, gate, nil, "do it", tea.WithoutRenderer(), tea.WithInput(nil))
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if res.Answer != "all done" {
		t.Errorf("answer = %q, want %q", res.Answer, "all done")
	}
	if res.Turns != 1 {
		t.Errorf("turns = %d, want 1", res.Turns)
	}
}

func TestRunModelApprovalFlow(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	a := agent.New(&stubBackend{reply: "ok"}, "m", "sys", nil,
		agent.WithApprover(gate.Approver()))
	m := newRunModel(a, gate, nil, "task")

	// A tool call arrives: the view switches to the approval dialog.
	reply := make(chan bool, 1)
	next, _ := m.Update(ApprovalRequest{Tool: "shell", Args: `{"command":"ls"}`, Reply: reply})
	m = next.(*runModel)
	if m.state != runApproving {
		t.Fatalf("state after request = %v, want runApproving", m.state)
	}
	if view := m.View(); !strings.Contains(view.Content, "allow?") || !strings.Contains(view.Content, `{"command":"ls"}`) {
		t.Errorf("approval view = %q", view.Content)
	}

	// Allowing it delivers true to the agent and returns to thinking.
	next, _ = m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = next.(*runModel)
	if m.state != runThinking {
		t.Errorf("state after allow = %v, want runThinking", m.state)
	}
	select {
	case ok := <-reply:
		if !ok {
			t.Error("decision delivered = deny, want allow")
		}
	default:
		t.Error("no decision delivered")
	}

	// Denying works the same way.
	denyReply := make(chan bool, 1)
	m.Update(ApprovalRequest{Tool: "shell", Args: "{}", Reply: denyReply})
	next, _ = m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = next.(*runModel)
	if m.state != runThinking {
		t.Errorf("state after deny = %v, want runThinking", m.state)
	}
	select {
	case ok := <-denyReply:
		if ok {
			t.Error("decision delivered = allow, want deny")
		}
	default:
		t.Error("no decision delivered")
	}
}

func TestRunModelDoneAndError(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	a := agent.New(&stubBackend{reply: "ok"}, "m", "sys", nil)

	done := newRunModel(a, gate, nil, "task")
	next, _ := done.Update(stepDoneMsg{res: agent.RunResult{Answer: "here it is", Turns: 2}})
	done = next.(*runModel)
	if done.state != runDone || !strings.Contains(done.View().Content, "here it is") {
		t.Errorf("done state = %v, view = %q", done.state, done.View().Content)
	}

	failing := newRunModel(a, gate, nil, "task")
	next, _ = failing.Update(stepDoneMsg{err: errors.New("backend down")})
	failing = next.(*runModel)
	if failing.state != runError || !strings.Contains(failing.View().Content, "backend down") {
		t.Errorf("error state = %v, view = %q", failing.state, failing.View().Content)
	}
}

// TestRunModelDoneShowsUsage pins the done frame's stats: the turn
// count extended with the token count and rate when the backend
// reported usage, turns alone when it did not.
func TestRunModelDoneShowsUsage(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	a := agent.New(&stubBackend{reply: "ok"}, "m", "sys", nil)

	withUsage := newRunModel(a, gate, nil, "task")
	next, _ := withUsage.Update(stepDoneMsg{res: agent.RunResult{
		Answer:  "here it is",
		Turns:   2,
		Usage:   backend.Usage{PromptTokens: 512, CompletionTokens: 42},
		Elapsed: 2 * time.Second,
	}})
	withUsage = next.(*runModel)
	if view := withUsage.View().Content; !strings.Contains(view, "2 turn(s) · 42 tokens · 21.0 tok/s") {
		t.Errorf("done view missing the stats line: %q", view)
	}

	bare := newRunModel(a, gate, nil, "task")
	next, _ = bare.Update(stepDoneMsg{res: agent.RunResult{Answer: "ok", Turns: 1}})
	bare = next.(*runModel)
	if view := bare.View().Content; !strings.Contains(view, "1 turn(s)") || strings.Contains(view, "tok/s") {
		t.Errorf("done view without usage should show turns only: %q", view)
	}
}
