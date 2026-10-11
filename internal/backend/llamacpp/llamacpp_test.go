package llamacpp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

	l, err := New(backend.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
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

func TestNewRejectsInvalidURL(t *testing.T) {
	for _, bad := range []string{"localhost:8080", "ftp://x", "http://"} {
		if _, err := New(backend.Config{BaseURL: bad}); err == nil {
			t.Errorf("New(%q): want error, got nil", bad)
		}
	}
}

func TestChatCarriesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"bad gateway"}}`))
	}))
	defer srv.Close()

	l, err := New(backend.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = l.Chat(context.Background(), backend.ChatRequest{Model: "m"})
	if err == nil || !strings.Contains(err.Error(), "bad gateway") {
		t.Errorf("Chat error = %v, want the server message included", err)
	}
}
