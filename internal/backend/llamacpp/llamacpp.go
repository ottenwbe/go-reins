// Package llamacpp implements backend.Backend against a llama.cpp
// server (llama-server), which exposes an OpenAI-compatible
// /v1/chat/completions endpoint.
package llamacpp

import (
	"context"
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

// New creates a llama.cpp backend. An empty cfg.BaseURL falls back
// to the default local address; an invalid URL (wrong scheme, no
// host) fails here rather than at the first request.
func New(cfg backend.Config) (*LlamaCpp, error) {
	base, err := backend.ParseBaseURL(cfg.BaseURL, defaultURL)
	if err != nil {
		return nil, err
	}
	return &LlamaCpp{
		baseURL: base,
		client:  &http.Client{Timeout: 120 * time.Second},
	}, nil
}

// Name implements backend.Backend.
func (l *LlamaCpp) Name() string { return "llamacpp" }

// Chat implements backend.Backend.
func (l *LlamaCpp) Chat(ctx context.Context, req backend.ChatRequest) (backend.ChatResponse, error) {
	var payload struct {
		Choices []struct {
			Message backend.Message `json:"message"`
		} `json:"choices"`
	}
	err := backend.PostJSON(ctx, l.client, l.baseURL, "/v1/chat/completions", map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   false,
	}, &payload)
	if err != nil {
		return backend.ChatResponse{}, fmt.Errorf("llamacpp: %w", err)
	}
	if len(payload.Choices) == 0 {
		return backend.ChatResponse{}, fmt.Errorf("llamacpp: empty response")
	}
	return backend.ChatResponse{Content: payload.Choices[0].Message.Content}, nil
}

// Models implements backend.Backend via the OpenAI-style /v1/models
// endpoint llama-server exposes: the loaded model(s) by id.
func (l *LlamaCpp) Models(ctx context.Context) ([]string, error) {
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := backend.GetJSON(ctx, l.client, l.baseURL, "/v1/models", &payload); err != nil {
		return nil, fmt.Errorf("llamacpp: %w", err)
	}
	names := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		names = append(names, m.ID)
	}
	return names, nil
}
