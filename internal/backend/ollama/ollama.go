// Package ollama implements backend.Backend against a local Ollama
// server using its native /api/chat endpoint.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go-reins/internal/backend"
)

const defaultURL = "http://localhost:11434"

// Ollama talks to an Ollama server.
type Ollama struct {
	baseURL string
	client  *http.Client
}

// New creates an Ollama backend. An empty cfg.BaseURL falls back to
// the default local address.
func New(cfg backend.Config) *Ollama {
	url := cfg.BaseURL
	if url == "" {
		url = defaultURL
	}
	return &Ollama{
		baseURL: url,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// Name implements backend.Backend.
func (o *Ollama) Name() string { return "ollama" }

// Chat implements backend.Backend.
func (o *Ollama) Chat(ctx context.Context, req backend.ChatRequest) (backend.ChatResponse, error) {
	body, err := json.Marshal(map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   false,
	})
	if err != nil {
		return backend.ChatResponse{}, fmt.Errorf("ollama: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return backend.ChatResponse{}, fmt.Errorf("ollama: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return backend.ChatResponse{}, fmt.Errorf("ollama: request %s: %w", o.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return backend.ChatResponse{}, fmt.Errorf("ollama: unexpected status %s", resp.Status)
	}

	var payload struct {
		Message backend.Message `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return backend.ChatResponse{}, fmt.Errorf("ollama: decode response: %w", err)
	}
	return backend.ChatResponse{Content: payload.Message.Content}, nil
}
