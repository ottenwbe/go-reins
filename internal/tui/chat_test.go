package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"go-reins/internal/agent"
	"go-reins/internal/backend"
)

// switchStub is a second backend for the /backend tests: its own
// name and model list, so a mid-session swap is observable.
type switchStub struct {
	name   string
	models []string
}

func (s *switchStub) Name() string { return s.name }
func (s *switchStub) Chat(context.Context, backend.ChatRequest) (backend.ChatResponse, error) {
	return backend.ChatResponse{Content: "ok"}, nil
}
func (s *switchStub) Models(context.Context) ([]string, error) { return s.models, nil }

// failModels wraps a stub whose listing always fails.
type failModels struct{ *stubBackend }

func (f *failModels) Models(context.Context) ([]string, error) {
	return nil, errors.New("listing broke")
}

// startChat builds a chat model in the state the command tests
// exercise: input focused and a window size applied.
func startChat(a *agent.Agent, gate *ApprovalGate, switchBackend func(string) error) *chatModel {
	m := newChatModel(a, gate, NewToolFeed(), "go-reins chat", switchBackend)
	m.Init()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(*chatModel)
}

// runCommand types a slash command into the input area and presses
// enter, returning the model and the Cmd the submit armed (nil for
// pure transcript commands).
func runCommand(m *chatModel, line string) (*chatModel, tea.Cmd) {
	next, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: line})
	m = next.(*chatModel)
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return next.(*chatModel), cmd
}

// TestChatModelStepFlow exercises the prompt -> step -> answer cycle.
func TestChatModelStepFlow(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	a := agent.New(&stubBackend{reply: "the answer"}, "m", "sys", nil)
	m := newChatModel(a, gate, NewToolFeed(), "go-reins chat", nil)
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
	m := newChatModel(a, gate, feed, "go-reins chat", nil)
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
	m := newChatModel(a, gate, NewToolFeed(), "go-reins chat", nil)
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

// TestChatDropsStaleToolEvents pins the waiter scoping fix: once a
// step has ended, a tool event that raced the final answer must be
// dropped instead of reviving the busy status line - and must not
// arm a new waiter while the model is idle.
func TestChatDropsStaleToolEvents(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	feed := NewToolFeed()
	defer feed.Close()
	a := agent.New(&stubBackend{reply: "done"}, "m", "sys", nil)
	m := newChatModel(a, gate, feed, "go-reins chat", nil)
	m.Init()

	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(*chatModel)

	// Run one step to completion; the step scope is canceled on
	// stepDoneMsg.
	next, _ = m.Update(tea.KeyPressMsg{Code: 'h', Text: "hello"})
	m = next.(*chatModel)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(*chatModel)
	next, _ = m.Update(stepDoneMsg{res: agent.RunResult{Answer: "done"}})
	m = next.(*chatModel)
	if m.state != chatReady {
		t.Fatalf("state after step = %v, want chatReady", m.state)
	}

	// A tool event delivered after the step ended: dropped, the
	// status line stays idle, and no waiter is re-armed (Update
	// returns a nil Cmd).
	next, cmd := m.Update(ToolEvent{Name: "read", Args: `{"path":"late.txt"}`})
	m = next.(*chatModel)
	if cmd != nil {
		t.Errorf("stale ToolEvent armed a waiter: %v", cmd)
	}
	if m.tool.Name != "" {
		t.Errorf("stale ToolEvent set the status line: %q", m.tool.Name)
	}
	if m.state != chatReady {
		t.Errorf("state after stale event = %v, want chatReady", m.state)
	}
}

// TestChatModelCommand covers /model: a name the backend offers
// switches the agent and the header after validation; a bare /model
// prints usage with the current model; an unknown command names the
// available ones.
func TestChatModelCommand(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()
	a := agent.New(&switchStub{name: "stub", models: []string{"llama3.2:latest", "stub-model"}}, "m", "sys", nil)
	m := startChat(a, gate, nil)

	// Bare /model: usage line naming the current model, no Cmd.
	m, cmd := runCommand(m, "/model")
	if cmd != nil {
		t.Errorf("/model armed %v, want nil", cmd)
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "usage: /model") ||
		!strings.Contains(strings.Join(m.lines, "\n"), "current: m") {
		t.Errorf("usage line missing: %v", m.lines)
	}

	// /model with a name: the switch is validated against the
	// backend's listing, then the agent switches and the live
	// header shows the new model.
	m, cmd = runCommand(m, "/model llama3.2")
	if cmd == nil {
		t.Fatal("/model <name> armed no validation Cmd")
	}
	next, _ := m.Update(cmd())
	m = next.(*chatModel)
	if a.Model() != "llama3.2" {
		t.Errorf("agent model = %q, want llama3.2", a.Model())
	}
	if !strings.Contains(m.View().Content, "go-reins chat · stub/llama3.2") {
		t.Errorf("header missing model switch: %q", m.headerText())
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "model set to llama3.2") {
		t.Errorf("confirmation line missing: %v", m.lines)
	}
	if m.state != chatReady {
		t.Errorf("state after /model = %v, want chatReady", m.state)
	}

	// Unknown command: the transcript names what is available.
	m, _ = runCommand(m, "/nope")
	if !strings.Contains(strings.Join(m.lines, "\n"), "unknown command /nope") {
		t.Errorf("unknown-command line missing: %v", m.lines)
	}
}

// TestChatModelCommandValidation pins the /model guard rails: a
// name the backend does not offer is refused with the available
// ones, tag shorthand is accepted, a failed or empty listing
// changes nothing, and a validation that lands while a step runs
// is not applied.
func TestChatModelCommandValidation(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()

	// Refused: the requested model is not in the listing, and the
	// transcript names what is.
	a := agent.New(&switchStub{name: "stub", models: []string{"a", "b"}}, "m", "sys", nil)
	m := startChat(a, gate, nil)
	m, cmd := runCommand(m, "/model nope")
	if cmd == nil {
		t.Fatal("/model armed no validation Cmd")
	}
	next, _ := m.Update(cmd())
	m = next.(*chatModel)
	if a.Model() != "m" {
		t.Errorf("model after refused switch = %q, want m", a.Model())
	}
	transcript := strings.Join(m.lines, "\n")
	if !strings.Contains(transcript, "no model nope") || !strings.Contains(transcript, "available: a, b") {
		t.Errorf("refusal line wrong: %v", m.lines)
	}

	// Accepted via tag shorthand: ollama resolves "llama3.2" to
	// "llama3.2:latest".
	sh := agent.New(&switchStub{name: "stub", models: []string{"llama3.2:latest"}}, "m", "sys", nil)
	m = startChat(sh, gate, nil)
	m, cmd = runCommand(m, "/model llama3.2")
	next, _ = m.Update(cmd())
	m = next.(*chatModel)
	if sh.Model() != "llama3.2" {
		t.Errorf("model after shorthand switch = %q, want llama3.2", sh.Model())
	}

	// A failed listing changes nothing.
	failing := agent.New(&failModels{&stubBackend{reply: "ok"}}, "m", "sys", nil)
	m = startChat(failing, gate, nil)
	m, cmd = runCommand(m, "/model x")
	next, _ = m.Update(cmd())
	m = next.(*chatModel)
	if failing.Model() != "m" {
		t.Errorf("model after failed validation = %q, want m", failing.Model())
	}

	// A backend with no models refuses the switch instead of
	// accepting every name.
	none := agent.New(&switchStub{name: "empty"}, "m", "sys", nil)
	m = startChat(none, gate, nil)
	m, cmd = runCommand(m, "/model x")
	next, _ = m.Update(cmd())
	m = next.(*chatModel)
	if none.Model() != "m" {
		t.Errorf("model after empty-listing switch = %q, want m", none.Model())
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "no model x") {
		t.Errorf("empty-listing refusal missing: %v", m.lines)
	}

	// A validation that lands while a step is running is not
	// applied: the running step owns the agent's model.
	busy := agent.New(&stubBackend{reply: "ok"}, "m", "sys", nil)
	m = startChat(busy, gate, nil)
	next, _ = m.Update(tea.KeyPressMsg{Code: 'h', Text: "hello"})
	m = next.(*chatModel)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(*chatModel)
	if m.state != chatBusy {
		t.Fatalf("state after submit = %v, want chatBusy", m.state)
	}
	next, _ = m.Update(modelsDoneMsg{names: []string{"llama3.2"}, want: "llama3.2", backend: "stub"})
	m = next.(*chatModel)
	if busy.Model() != "m" {
		t.Errorf("model set while busy = %q, want m (unchanged)", busy.Model())
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "retry /model") {
		t.Errorf("busy-refusal line missing: %v", m.lines)
	}
}

// TestChatStaleListingDroppedAfterBackendSwitch pins the race fix:
// a listing Cmd running against the previous backend must not land
// after a /backend switch - neither set a model the new backend
// may not serve nor print the old backend's list. The fresh
// listing the switch itself armed still shows.
func TestChatStaleListingDroppedAfterBackendSwitch(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()

	a := agent.New(&switchStub{name: "ollama", models: []string{"m2"}}, "m", "sys", nil)
	target := &switchStub{name: "llamacpp", models: []string{"qwen2.5"}}
	m := startChat(a, gate, func(string) error {
		a.SetBackend(target)
		return nil
	})

	// /model arms its validation against the current backend…
	m, stale := runCommand(m, "/model m2")
	if stale == nil {
		t.Fatal("/model armed no validation Cmd")
	}
	// …and the operator switches before the listing lands. The
	// switch arms a fresh listing of its own.
	m, fresh := runCommand(m, "/backend llamacpp")
	if fresh == nil {
		t.Fatal("/backend armed no listing Cmd")
	}

	// The stale validation arrives first: dropped, the model is
	// unchanged, and nothing claims it was set.
	next, _ := m.Update(stale())
	m = next.(*chatModel)
	if a.Model() != "m" {
		t.Errorf("stale validation changed model to %q, want m", a.Model())
	}
	if strings.Contains(strings.Join(m.lines, "\n"), "model set to m2") {
		t.Errorf("stale validation confirmed a switch: %v", m.lines)
	}

	// The listing the switch armed still lands and describes the
	// backend the session now uses.
	next, _ = m.Update(fresh())
	m = next.(*chatModel)
	if !strings.Contains(strings.Join(m.lines, "\n"), "qwen2.5") {
		t.Errorf("fresh listing missing from transcript: %v", m.lines)
	}
}

// TestChatModelsCommand covers /models: the listing runs as a Cmd
// and its result lands in the transcript; errors and empty lists
// are reported instead of swallowed.
func TestChatModelsCommand(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()

	// A backend with models to list.
	a := agent.New(&switchStub{name: "stub", models: []string{"a", "b"}}, "m", "sys", nil)
	m := startChat(a, gate, nil)

	m, cmd := runCommand(m, "/models")
	if cmd == nil {
		t.Fatal("/models armed no Cmd")
	}
	msg := cmd()
	done, ok := msg.(modelsDoneMsg)
	if !ok {
		t.Fatalf("Cmd produced %T, want modelsDoneMsg", msg)
	}
	if done.err != nil || len(done.names) != 2 || done.names[0] != "a" {
		t.Fatalf("modelsDoneMsg = %+v, want the stub's models", done)
	}
	next, _ := m.Update(msg)
	m = next.(*chatModel)
	// The listing is appended as one rendered transcript entry;
	// lipgloss styles and pads each name line, so assert on the
	// entry itself rather than a raw join.
	if last := m.lines[len(m.lines)-1]; !strings.Contains(last, "a") || !strings.Contains(last, "b") {
		t.Errorf("listing missing from transcript: %q", last)
	}
	if m.state != chatReady {
		t.Errorf("state after /models = %v, want chatReady", m.state)
	}

	// A backend that lists nothing says so instead of printing an
	// empty listing.
	empty := agent.New(&switchStub{name: "empty"}, "m", "sys", nil)
	m = startChat(empty, gate, nil)
	m, cmd = runCommand(m, "/models")
	msg = cmd()
	next, _ = m.Update(msg)
	m = next.(*chatModel)
	if !strings.Contains(strings.Join(m.lines, "\n"), "no models") {
		t.Errorf("empty listing not reported: %v", m.lines)
	}

	// A failed listing surfaces the error.
	failing := agent.New(&failModels{&stubBackend{reply: "ok"}}, "m", "sys", nil)
	m = startChat(failing, gate, nil)
	m, cmd = runCommand(m, "/models")
	msg = cmd()
	next, _ = m.Update(msg)
	m = next.(*chatModel)
	if !strings.Contains(strings.Join(m.lines, "\n"), "listing broke") {
		t.Errorf("listing error missing from transcript: %v", m.lines)
	}
}

// TestChatBackendCommand covers /backend: a successful switch swaps
// the agent's backend and arms the automatic listing against the
// new one; errors, missing wiring, and bare usage are reported.
func TestChatBackendCommand(t *testing.T) {
	gate := NewApprovalGate()
	defer gate.Close()

	a := agent.New(&stubBackend{reply: "ok"}, "m", "sys", nil)
	target := &switchStub{name: "llamacpp", models: []string{"qwen2.5"}}
	switched := ""
	m := startChat(a, gate, func(name string) error {
		switched = name
		if name != "llamacpp" {
			return errors.New("no such backend here")
		}
		a.SetBackend(target)
		return nil
	})

	// Bare /backend: usage with the current backend.
	m, cmd := runCommand(m, "/backend")
	if cmd != nil {
		t.Errorf("/backend armed %v, want nil", cmd)
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "usage: /backend") {
		t.Errorf("usage line missing: %v", m.lines)
	}

	// A switch: the closure runs, the header updates, and the
	// listing Cmd queries the new backend.
	m, cmd = runCommand(m, "/backend llamacpp")
	if switched != "llamacpp" {
		t.Errorf("switch closure saw %q, want llamacpp", switched)
	}
	if a.BackendName() != "llamacpp" {
		t.Errorf("agent backend = %q, want llamacpp", a.BackendName())
	}
	if !strings.Contains(m.headerText(), "llamacpp/m") {
		t.Errorf("header after switch = %q, want llamacpp/m", m.headerText())
	}
	if cmd == nil {
		t.Fatal("switch armed no listing Cmd")
	}
	msg := cmd()
	done, ok := msg.(modelsDoneMsg)
	if !ok || done.err != nil || len(done.names) != 1 || done.names[0] != "qwen2.5" {
		t.Fatalf("listing after switch = %+v (%T), want the new backend's models", msg, msg)
	}
	next, _ := m.Update(msg)
	m = next.(*chatModel)
	if !strings.Contains(strings.Join(m.lines, "\n"), "backend switched to llamacpp") {
		t.Errorf("switch confirmation missing: %v", m.lines)
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "qwen2.5") {
		t.Errorf("listing after switch missing: %v", m.lines)
	}

	// A failing switch reports the error, arms nothing, and leaves
	// the backend untouched.
	m, cmd = runCommand(m, "/backend ollama")
	if cmd != nil {
		t.Errorf("failed switch still armed %v", cmd)
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "no such backend here") {
		t.Errorf("switch error missing: %v", m.lines)
	}
	if a.BackendName() != "llamacpp" {
		t.Errorf("backend after failed switch = %q, want llamacpp", a.BackendName())
	}

	// Without wiring, /backend says so instead of crashing.
	bare := agent.New(&stubBackend{reply: "ok"}, "m", "sys", nil)
	m = startChat(bare, gate, nil)
	m, _ = runCommand(m, "/backend ollama")
	if !strings.Contains(strings.Join(m.lines, "\n"), "not wired up") {
		t.Errorf("unwired switch not reported: %v", m.lines)
	}
}

func TestStepScopeCancelIsIdempotent(t *testing.T) {
	s := newStepScope()
	s.cancel()
	s.cancel() // must not panic
	select {
	case <-s.done:
	default:
		t.Fatal("done channel not closed after cancel")
	}
	// The nil scope tolerates cancel: chatReady models have none.
	var none *stepScope
	none.cancel()
}
