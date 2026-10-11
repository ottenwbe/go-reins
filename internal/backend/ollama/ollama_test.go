package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

	o, err := New(backend.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
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

func TestNewRejectsInvalidURL(t *testing.T) {
	for _, bad := range []string{"localhost:11434", "ftp://x", "http://", "://"} {
		if _, err := New(backend.Config{BaseURL: bad}); err == nil {
			t.Errorf("New(%q): want error, got nil", bad)
		}
	}
}

func TestNewNormalizesTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}`))
	}))
	defer srv.Close()

	o, err := New(backend.Config{BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := o.Chat(context.Background(), backend.ChatRequest{Model: "m"}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if gotPath != "/api/chat" {
		t.Errorf("path = %q, want /api/chat (trailing slash must not double it)", gotPath)
	}
}

func TestChatCarriesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"model 'nope' not found"}`))
	}))
	defer srv.Close()

	o, err := New(backend.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = o.Chat(context.Background(), backend.ChatRequest{Model: "nope"})
	if err == nil || !strings.Contains(err.Error(), "model 'nope' not found") {
		t.Errorf("Chat error = %v, want server's message included", err)
	}
}
