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
		// Chat delegates to ChatStream, so the request always asks
		// for the NDJSON protocol; a nil observer just collects.
		if req["stream"] != true {
			t.Errorf("stream = %v, want true", req["stream"])
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"hi"}}` + "\n" + `{"done":true}` + "\n"))
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

// TestChatStreamsTokens verifies ChatStream against ollama's NDJSON
// protocol: every message.content delta reaches the observer in
// order, the full text comes back, and the final done marker and
// empty deltas are ignored.
func TestChatStreamsTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		for _, line := range []string{
			`{"message":{"role":"assistant","content":"hel"}}`,
			`{"message":{"role":"assistant","content":"lo "}}`,
			`{"message":{"role":"assistant","content":"world"},"done":false}`,
			`{"done":true}`,
		} {
			_, _ = w.Write([]byte(line + "\n"))
			fl.Flush()
		}
	}))
	defer srv.Close()

	o, err := New(backend.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var deltas []string
	resp, err := o.ChatStream(context.Background(), backend.ChatRequest{Model: "m"}, func(d string) {
		deltas = append(deltas, d)
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if resp.Content != "hello world" {
		t.Errorf("content = %q, want %q", resp.Content, "hello world")
	}
	if strings.Join(deltas, "") != "hello world" {
		t.Errorf("deltas = %v, want hel lo  world in order", deltas)
	}
}

// TestChatStreamReportsUsage pins that the token accounting on the
// final done object becomes the response's Usage: prompt tokens from
// prompt_eval_count, reply tokens from eval_count.
func TestChatStreamReportsUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(
			`{"message":{"role":"assistant","content":"hi"}}` + "\n" +
				`{"message":{"role":"assistant","content":""},"done":true,"prompt_eval_count":512,"eval_count":2}` + "\n"))
	}))
	defer srv.Close()

	o, err := New(backend.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := o.ChatStream(context.Background(), backend.ChatRequest{Model: "m"}, func(string) {})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if resp.Usage.PromptTokens != 512 || resp.Usage.CompletionTokens != 2 {
		t.Errorf("usage = %+v, want {PromptTokens:512 CompletionTokens:2}", resp.Usage)
	}
}

// TestChatStreamCarriesErrorLine pins that an error object streamed
// mid-answer fails the call with the server's own message.
func TestChatStreamCarriesErrorLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"hal"}}` + "\n" + `{"error":"model 'nope' not found"}` + "\n"))
	}))
	defer srv.Close()

	o, err := New(backend.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = o.ChatStream(context.Background(), backend.ChatRequest{Model: "nope"}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "model 'nope' not found") {
		t.Errorf("ChatStream error = %v, want the server's message", err)
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
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"}}` + "\n" + `{"done":true}` + "\n"))
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

// TestModelsListsAPITags verifies the /models listing: a GET to
// /api/tags, the model names in the server's own order.
func TestModelsListsAPITags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %q, want /api/tags", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:latest"},{"name":"qwen2.5:0.5b"}]}`))
	}))
	defer srv.Close()

	o, err := New(backend.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	names, err := o.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(names) != 2 || names[0] != "llama3.2:latest" || names[1] != "qwen2.5:0.5b" {
		t.Errorf("names = %v, want [llama3.2:latest qwen2.5:0.5b]", names)
	}
}

// TestModelsCarriesServerError pins that a listing failure keeps the
// server's own message, the same way a failed chat does.
func TestModelsCarriesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"daemon not running"}`))
	}))
	defer srv.Close()

	o, err := New(backend.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = o.Models(context.Background())
	if err == nil || !strings.Contains(err.Error(), "daemon not running") {
		t.Errorf("Models error = %v, want server's message included", err)
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

// TestChatStreamRequiresDone pins that a stream ending without the
// final done object is a truncated reply, not a short answer.
func TestChatStreamRequiresDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"hal"}}` + "\n"))
	}))
	defer srv.Close()

	o, err := New(backend.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = o.ChatStream(context.Background(), backend.ChatRequest{Model: "m"}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "before the final chunk") {
		t.Errorf("Chat error = %v, want the truncated-stream error", err)
	}
}
