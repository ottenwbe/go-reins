package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-reins/internal/backend"
)

func TestChatPostsToAPIChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %q, want /api/chat", r.URL.Path)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req["model"] != "llama3.2" {
			t.Errorf("model = %v, want llama3.2", req["model"])
		}
		if req["stream"] != false {
			t.Errorf("stream = %v, want false", req["stream"])
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"hi"}}`))
	}))
	defer srv.Close()

	o := New(backend.Config{BaseURL: srv.URL})
	resp, err := o.Chat(context.Background(), backend.ChatRequest{
		Model:    "llama3.2",
		Messages: []backend.Message{{Role: backend.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "hi" {
		t.Errorf("content = %q, want %q", resp.Content, "hi")
	}
}
