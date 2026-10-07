package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"go-reins/internal/backend"
)

type fakeBackend struct {
	reply   backend.ChatResponse
	replies []backend.ChatResponse
	calls   []backend.ChatRequest
}

func (f *fakeBackend) Name() string { return "fake" }

func (f *fakeBackend) Chat(_ context.Context, req backend.ChatRequest) (backend.ChatResponse, error) {
	f.calls = append(f.calls, req)
	if len(f.replies) > 0 {
		next := f.replies[0]
		f.replies = f.replies[1:]
		return next, nil
	}
	return f.reply, nil
}

type fakeTool struct {
	name  string
	desc  string
	calls []string
	reply string
	err   error
}

func (f *fakeTool) Name() string        { return f.name }
func (f *fakeTool) Description() string { return f.desc }
func (f *fakeTool) Execute(_ context.Context, args string) (string, error) {
	f.calls = append(f.calls, args)
	if f.err != nil {
		return "", f.err
	}
	return f.reply, nil
}

func TestRunReturnsAssistantReply(t *testing.T) {
	fb := &fakeBackend{reply: backend.ChatResponse{Content: "hello there"}}
	a := New(fb, "test-model", "be brief", nil)

	res, err := a.Run(context.Background(), "say hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "hello there" {
		t.Errorf("Run reply = %q, want %q", res.Answer, "hello there")
	}

	if len(fb.calls) != 1 {
		t.Fatalf("backend calls = %d, want 1", len(fb.calls))
	}
	req := fb.calls[0]
	if req.Model != "test-model" {
		t.Errorf("request model = %q, want %q", req.Model, "test-model")
	}
	if len(req.Messages) != 2 {
		t.Fatalf("request messages = %d, want 2 (system + user)", len(req.Messages))
	}
	if req.Messages[0].Role != backend.RoleSystem || req.Messages[1].Role != backend.RoleUser {
		t.Errorf("unexpected roles: %+v", req.Messages)
	}
	if req.Messages[1].Content != "say hi" {
		t.Errorf("user message = %q, want %q", req.Messages[1].Content, "say hi")
	}
	if strings.Contains(req.Messages[0].Content, "TOOLCALL") {
		t.Errorf("system prompt mentions tools although none are registered: %q", req.Messages[0].Content)
	}
	if res.Turns != 1 {
		t.Errorf("Turns = %d, want 1", res.Turns)
	}
	if len(res.History) != 3 {
		t.Errorf("History length = %d, want 3 (system, user, final assistant)", len(res.History))
	}
}

func TestRunDispatchesToolCall(t *testing.T) {
	fb := &fakeBackend{replies: []backend.ChatResponse{
		{Content: `TOOLCALL weather {"city":"Berlin"}`},
		{Content: "It is raining in Berlin."},
	}}
	weather := &fakeTool{name: "weather", desc: "current weather for a city", reply: "rain, 12C"}
	a := New(fb, "test-model", "be brief", []Tool{weather})

	res, err := a.Run(context.Background(), "weather in Berlin?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "It is raining in Berlin." {
		t.Errorf("Run reply = %q, want final answer", res.Answer)
	}
	if len(weather.calls) != 1 || weather.calls[0] != `{"city":"Berlin"}` {
		t.Errorf("tool calls = %v, want one call with JSON args", weather.calls)
	}

	if len(fb.calls) != 2 {
		t.Fatalf("backend calls = %d, want 2 (tool turn + final turn)", len(fb.calls))
	}
	sys := fb.calls[0].Messages[0].Content
	if !strings.Contains(sys, "Available tools") || !strings.Contains(sys, "weather: current weather for a city") {
		t.Errorf("system prompt missing tool docs: %q", sys)
	}
	second := fb.calls[1].Messages
	if len(second) != 4 {
		t.Fatalf("second request messages = %d, want 4 (system, user, assistant, tool result)", len(second))
	}
	obs := second[len(second)-1]
	if obs.Role != backend.RoleUser || !strings.Contains(obs.Content, "TOOLRESULT: rain, 12C") {
		t.Errorf("tool observation message = %+v, want user message with TOOLRESULT", obs)
	}
	if res.Turns != 2 {
		t.Errorf("Turns = %d, want 2", res.Turns)
	}
	if len(res.History) != 5 {
		t.Fatalf("History length = %d, want 5 (system, user, tool call, result, final)", len(res.History))
	}
	last := res.History[len(res.History)-1]
	if last.Role != backend.RoleAssistant || last.Content != "It is raining in Berlin." {
		t.Errorf("last history message = %+v, want final assistant answer", last)
	}
}

func TestRunFeedsToolErrorBackToModel(t *testing.T) {
	fb := &fakeBackend{replies: []backend.ChatResponse{
		{Content: `TOOLCALL weather {"city":"Berlin"}`},
		{Content: "I could not check the weather."},
	}}
	weather := &fakeTool{name: "weather", desc: "weather", err: errors.New("service unreachable")}
	a := New(fb, "test-model", "be brief", []Tool{weather})

	res, err := a.Run(context.Background(), "weather in Berlin?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "I could not check the weather." {
		t.Errorf("Run reply = %q, want final answer", res.Answer)
	}

	obs := fb.calls[1].Messages[len(fb.calls[1].Messages)-1].Content
	if !strings.Contains(obs, `tool "weather" failed: service unreachable`) {
		t.Errorf("observation = %q, want tool error text", obs)
	}
}

func TestRunFeedsUnknownToolBackToModel(t *testing.T) {
	fb := &fakeBackend{replies: []backend.ChatResponse{
		{Content: `TOOLCALL misspelled {"x":1}`},
		{Content: "Sorry, no tool then."},
	}}
	weather := &fakeTool{name: "weather", desc: "weather", reply: "ok"}
	a := New(fb, "test-model", "be brief", []Tool{weather})

	res, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "Sorry, no tool then." {
		t.Errorf("Run reply = %q, want final answer", res.Answer)
	}

	obs := fb.calls[1].Messages[len(fb.calls[1].Messages)-1].Content
	if !strings.Contains(obs, `unknown tool "misspelled"`) || !strings.Contains(obs, "weather") {
		t.Errorf("observation = %q, want unknown-tool text with available tools", obs)
	}
}

func TestRunExceedsMaxTurns(t *testing.T) {
	fb := &fakeBackend{reply: backend.ChatResponse{Content: `TOOLCALL weather {"city":"Berlin"}`}}
	weather := &fakeTool{name: "weather", desc: "weather", reply: "rain"}
	a := New(fb, "test-model", "be brief", []Tool{weather}, WithMaxTurns(3))

	_, err := a.Run(context.Background(), "weather?")
	if err == nil {
		t.Fatal("Run: want max-turns error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeded 3 turns") {
		t.Errorf("Run error = %v, want max-turns message", err)
	}
	if len(weather.calls) != 3 {
		t.Errorf("tool calls = %d, want 3 (one per turn)", len(weather.calls))
	}
}

func TestRunDeniedByApprover(t *testing.T) {
	fb := &fakeBackend{replies: []backend.ChatResponse{
		{Content: `TOOLCALL weather {"city":"Berlin"}`},
		{Content: "I was not allowed to check."},
	}}
	weather := &fakeTool{name: "weather", desc: "weather", reply: "rain"}

	var seen []string
	approve := func(name, args string) bool {
		seen = append(seen, name+" "+args)
		return false
	}
	a := New(fb, "test-model", "be brief", []Tool{weather}, WithApprover(approve))

	res, err := a.Run(context.Background(), "weather in Berlin?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "I was not allowed to check." {
		t.Errorf("Run reply = %q, want final answer", res.Answer)
	}

	if len(seen) != 1 || seen[0] != `weather {"city":"Berlin"}` {
		t.Errorf("approver saw %v, want one call with name and args", seen)
	}
	if len(weather.calls) != 0 {
		t.Errorf("tool calls = %d, want 0 (denied call must not execute)", len(weather.calls))
	}
	obs := fb.calls[1].Messages[len(fb.calls[1].Messages)-1].Content
	if !strings.Contains(obs, "not approved by the operator") {
		t.Errorf("observation = %q, want denial text", obs)
	}
}

func TestRunApprovedByApprover(t *testing.T) {
	fb := &fakeBackend{replies: []backend.ChatResponse{
		{Content: `TOOLCALL weather {"city":"Berlin"}`},
		{Content: "It is raining."},
	}}
	weather := &fakeTool{name: "weather", desc: "weather", reply: "rain"}
	a := New(fb, "test-model", "be brief", []Tool{weather}, WithApprover(func(name, args string) bool {
		return name == "weather"
	}))

	res, err := a.Run(context.Background(), "weather in Berlin?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "It is raining." {
		t.Errorf("Run reply = %q, want final answer", res.Answer)
	}
	if len(weather.calls) != 1 {
		t.Errorf("tool calls = %d, want 1 (approved call must execute)", len(weather.calls))
	}
}

func TestRunLogsToolLifecycle(t *testing.T) {
	fb := &fakeBackend{replies: []backend.ChatResponse{
		{Content: `TOOLCALL weather {"city":"Berlin"}`},
		{Content: "It is raining."},
	}}
	weather := &fakeTool{name: "weather", desc: "weather", reply: "rain"}

	core, logs := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	a := New(fb, "test-model", "be brief", []Tool{weather}, WithLogger(logger))

	if _, err := a.Run(context.Background(), "weather in Berlin?"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	messages := logs.FilterLevelExact(zapcore.InfoLevel)
	want := []string{"tool call", "final answer"}
	if messages.Len() != len(want) {
		t.Fatalf("info log entries = %d, want %d: %v", messages.Len(), len(want), messages.All())
	}
	for i, m := range want {
		if got := messages.All()[i].Message; got != m {
			t.Errorf("log entry %d = %q, want %q", i, got, m)
		}
	}

	call := messages.All()[0]
	toolLogged := false
	for _, f := range call.Context {
		if f.Key == "tool" && f.String == "weather" {
			toolLogged = true
		}
	}
	if !toolLogged {
		t.Errorf("tool call log fields missing tool=weather: %v", call.Context)
	}
	if entries := logs.FilterLevelExact(zapcore.WarnLevel); entries.Len() != 0 {
		t.Errorf("warn entries = %d, want 0 in a successful run: %v", entries.Len(), entries.All())
	}
}

func TestRunLogsDeniedCall(t *testing.T) {
	fb := &fakeBackend{replies: []backend.ChatResponse{
		{Content: `TOOLCALL weather {"city":"Berlin"}`},
		{Content: "No weather for you."},
	}}
	weather := &fakeTool{name: "weather", desc: "weather", reply: "rain"}

	core, logs := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	a := New(fb, "test-model", "be brief", []Tool{weather},
		WithApprover(func(string, string) bool { return false }),
		WithLogger(logger))

	if _, err := a.Run(context.Background(), "weather in Berlin?"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	warns := logs.FilterLevelExact(zapcore.WarnLevel)
	if warns.Len() != 1 || warns.All()[0].Message != "tool call denied by operator" {
		t.Errorf("warn entries = %v, want one denial entry", warns.All())
	}
}

func TestMaxTurnsConfiguration(t *testing.T) {
	fb := &fakeBackend{reply: backend.ChatResponse{Content: "hello there"}}

	if got := New(fb, "test-model", "be brief", nil).maxTurns; got != defaultMaxTurns {
		t.Errorf("default maxTurns = %d, want %d", got, defaultMaxTurns)
	}

	a := New(fb, "test-model", "be brief", nil, WithMaxTurns(3))
	if a.maxTurns != 3 {
		t.Errorf("maxTurns = %d, want 3", a.maxTurns)
	}

	ignored := New(fb, "test-model", "be brief", nil, WithMaxTurns(0))
	if ignored.maxTurns != defaultMaxTurns {
		t.Errorf("maxTurns = %d after invalid option, want %d", ignored.maxTurns, defaultMaxTurns)
	}
}

func TestStepContinuesSession(t *testing.T) {
	fb := &fakeBackend{replies: []backend.ChatResponse{
		{Content: "first answer"},
		{Content: "second answer"},
	}}
	a := New(fb, "test-model", "be brief", nil)

	res1, err := a.Step(context.Background(), nil, "first prompt")
	if err != nil {
		t.Fatalf("step 1: %v", err)
	}
	res2, err := a.Step(context.Background(), res1.History, "second prompt")
	if err != nil {
		t.Fatalf("step 2: %v", err)
	}
	if res2.Answer != "second answer" {
		t.Errorf("step 2 answer = %q, want %q", res2.Answer, "second answer")
	}

	if len(fb.calls) != 2 {
		t.Fatalf("backend calls = %d, want 2", len(fb.calls))
	}
	msgs := fb.calls[1].Messages
	if len(msgs) != 4 {
		t.Fatalf("step 2 messages = %d, want 4 (system + user + assistant + user)", len(msgs))
	}
	if msgs[0].Role != backend.RoleSystem {
		t.Errorf("step 2 must reuse the system prompt, got role %q first", msgs[0].Role)
	}
	if msgs[1].Content != "first prompt" || msgs[2].Content != "first answer" || msgs[3].Content != "second prompt" {
		t.Errorf("step 2 conversation = %v", msgs)
	}
}
