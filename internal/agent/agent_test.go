package agent

import (
	"context"
	"testing"

	"go-reins/internal/backend"
)

type fakeBackend struct {
	reply backend.ChatResponse
	calls []backend.ChatRequest
}

func (f *fakeBackend) Name() string { return "fake" }

func (f *fakeBackend) Chat(_ context.Context, req backend.ChatRequest) (backend.ChatResponse, error) {
	f.calls = append(f.calls, req)
	return f.reply, nil
}

func TestRunReturnsAssistantReply(t *testing.T) {
	fb := &fakeBackend{reply: backend.ChatResponse{Content: "hello there"}}
	a := New(fb, "test-model", "be brief", nil)

	got, err := a.Run(context.Background(), "say hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "hello there" {
		t.Errorf("Run reply = %q, want %q", got, "hello there")
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
}
