package llamacpp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-reins/internal/backend"
)

func TestChatPostsToV1Completions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req["model"] != "qwen2.5" {
			t.Errorf("model = %v, want qwen2.5", req["model"])
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer srv.Close()

	l := New(backend.Config{BaseURL: srv.URL})
	resp, err := l.Chat(context.Background(), backend.ChatRequest{
		Model:    "qwen2.5",
		Messages: []backend.Message{{Role: backend.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "hi" {
		t.Errorf("content = %q, want %q", resp.Content, "hi")
	}
}
