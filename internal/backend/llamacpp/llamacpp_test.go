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
		// Chat delegates to ChatStream, so the request always asks
		// for the SSE protocol; a nil observer just collects.
		if req["stream"] != true {
			t.Errorf("stream = %v, want true", req["stream"])
		}
		_, _ = w.Write([]byte("data: " + `{"choices":[{"delta":{"content":"hi"}}]}` + "\n\n" + "data: [DONE]\n"))
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

// TestChatStreamsTokens verifies ChatStream against llama-server's
// SSE protocol: delta.content chunks reach the observer in order,
// comments and the DONE marker are ignored, and the full text
// comes back.
func TestChatStreamsTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		for _, line := range []string{
			": keep-alive", // SSE comment: ignored
			`data: {"choices":[{"delta":{"content":"hel"}}]}`,
			`data: {"choices":[{"delta":{"content":"lo"}}]}`,
			`data: {"choices":[{"delta":{}}]}`, // empty delta: ignored
			`data: [DONE]`,                     // the terminator
		} {
			_, _ = w.Write([]byte(line + "\n"))
			fl.Flush()
		}
	}))
	defer srv.Close()

	l, err := New(backend.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var deltas []string
	resp, err := l.ChatStream(context.Background(), backend.ChatRequest{Model: "m"}, func(d string) {
		deltas = append(deltas, d)
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if resp.Content != "hello" {
		t.Errorf("content = %q, want %q", resp.Content, "hello")
	}
	if strings.Join(deltas, "") != "hello" {
		t.Errorf("deltas = %v, want hel lo in order", deltas)
	}
}

// TestChatStreamReportsUsage pins the usage half of the SSE
// protocol: the request asks for stream_options.include_usage, and
// the usage object of the final content-free chunk becomes the
// response's Usage - without counting as a content chunk or
// tripping the empty-response guard.
func TestChatStreamReportsUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		opts, _ := req["stream_options"].(map[string]any)
		if opts["include_usage"] != true {
			t.Errorf("stream_options = %v, want include_usage: true", req["stream_options"])
		}
		_, _ = w.Write([]byte(
			"data: " + `{"choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
				"data: " + `{"choices":[],"usage":{"prompt_tokens":512,"completion_tokens":2}}` + "\n\n" +
				"data: [DONE]\n"))
	}))
	defer srv.Close()

	l, err := New(backend.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := l.ChatStream(context.Background(), backend.ChatRequest{Model: "m"}, func(string) {})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if resp.Content != "hi" {
		t.Errorf("content = %q, want %q", resp.Content, "hi")
	}
	if resp.Usage.PromptTokens != 512 || resp.Usage.CompletionTokens != 2 {
		t.Errorf("usage = %+v, want {PromptTokens:512 CompletionTokens:2}", resp.Usage)
	}
}

// TestChatStreamCarriesErrorChunk pins that an error object in an
// SSE chunk fails the call with the server's own message.
func TestChatStreamCarriesErrorChunk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`data: {"error":{"message":"bad gateway"}}` + "\n"))
	}))
	defer srv.Close()

	l, err := New(backend.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = l.ChatStream(context.Background(), backend.ChatRequest{Model: "m"}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "bad gateway") {
		t.Errorf("ChatStream error = %v, want the server's message", err)
	}
}

func TestNewRejectsInvalidURL(t *testing.T) {
	for _, bad := range []string{"localhost:8080", "ftp://x", "http://"} {
		if _, err := New(backend.Config{BaseURL: bad}); err == nil {
			t.Errorf("New(%q): want error, got nil", bad)
		}
	}
}

// TestModelsListsV1Models verifies the /models listing: a GET to the
// OpenAI-style /v1/models endpoint, ids in the server's own order.
func TestModelsListsV1Models(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen2.5-coder-7b"},{"id":"llama3.2"}]}`))
	}))
	defer srv.Close()

	l, err := New(backend.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	names, err := l.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(names) != 2 || names[0] != "qwen2.5-coder-7b" || names[1] != "llama3.2" {
		t.Errorf("names = %v, want [qwen2.5-coder-7b llama3.2]", names)
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

// TestChatStreamRequiresDone pins that a stream ending without the
// SSE terminator is a truncated reply, not a short answer.
func TestChatStreamRequiresDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: " + `{"choices":[{"delta":{"content":"hal"}}]}` + "\n"))
	}))
	defer srv.Close()

	l, err := New(backend.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = l.ChatStream(context.Background(), backend.ChatRequest{Model: "m"}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "before the terminator") {
		t.Errorf("Chat error = %v, want the truncated-stream error", err)
	}
}
