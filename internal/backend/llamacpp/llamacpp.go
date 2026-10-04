// Package llamacpp implements backend.Backend against a llama.cpp
// server (llama-server), which exposes an OpenAI-compatible
// /v1/chat/completions endpoint.
package llamacpp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go-reins/internal/backend"
)

const defaultURL = "http://localhost:8080"

// LlamaCpp talks to a llama-server instance.
type LlamaCpp struct {
	baseURL string
	client  *http.Client
}

// New creates a llama.cpp backend. An empty cfg.BaseURL falls back to
// the default local address.
func New(cfg backend.Config) *LlamaCpp {
	url := cfg.BaseURL
	if url == "" {
		url = defaultURL
	}
	return &LlamaCpp{
		baseURL: url,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// Name implements backend.Backend.
func (l *LlamaCpp) Name() string { return "llamacpp" }

// Chat implements backend.Backend.
func (l *LlamaCpp) Chat(ctx context.Context, req backend.ChatRequest) (backend.ChatResponse, error) {
	body, err := json.Marshal(map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   false,
	})
	if err != nil {
		return backend.ChatResponse{}, fmt.Errorf("llamacpp: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, l.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return backend.ChatResponse{}, fmt.Errorf("llamacpp: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := l.client.Do(httpReq)
	if err != nil {
		return backend.ChatResponse{}, fmt.Errorf("llamacpp: request %s: %w", l.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return backend.ChatResponse{}, fmt.Errorf("llamacpp: unexpected status %s", resp.Status)
	}

	var payload struct {
		Choices []struct {
			Message backend.Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return backend.ChatResponse{}, fmt.Errorf("llamacpp: decode response: %w", err)
	}
	if len(payload.Choices) == 0 {
		return backend.ChatResponse{}, fmt.Errorf("llamacpp: empty response")
	}
	return backend.ChatResponse{Content: payload.Choices[0].Message.Content}, nil
}
