// Package ollama implements backend.Backend against a local Ollama
// server using its native /api/chat endpoint.
package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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
// the default local address; an invalid URL (wrong scheme, no host)
// fails here rather than at the first request.
func New(cfg backend.Config) (*Ollama, error) {
	base, err := backend.ParseBaseURL(cfg.BaseURL, defaultURL)
	if err != nil {
		return nil, err
	}
	return &Ollama{
		baseURL: base,
		client:  &http.Client{Timeout: 120 * time.Second},
	}, nil
}

// Name implements backend.Backend.
func (o *Ollama) Name() string { return "ollama" }

// Chat implements backend.Backend by delegating to ChatStream with
// no observer: one wire path serves both callers, streaming or not.
func (o *Ollama) Chat(ctx context.Context, req backend.ChatRequest) (backend.ChatResponse, error) {
	return o.ChatStream(ctx, req, nil)
}

// ChatStream implements backend.Streamer via /api/chat with
// stream: true. Ollama answers in newline-delimited JSON: one
// {"message":{"content":...}} object per token, a bare {"error":...}
// object on failure, and a final {"done":true} object.
func (o *Ollama) ChatStream(ctx context.Context, req backend.ChatRequest, onDelta func(string)) (backend.ChatResponse, error) {
	var full strings.Builder
	err := backend.PostJSONLines(ctx, o.client, o.baseURL, "/api/chat", map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   true,
	}, func(line []byte) error {
		var chunk struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done  bool   `json:"done"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(line, &chunk); err != nil {
			return fmt.Errorf("decode chunk: %w", err)
		}
		if chunk.Error != "" {
			return errors.New(chunk.Error)
		}
		if c := chunk.Message.Content; c != "" {
			full.WriteString(c)
			if onDelta != nil {
				onDelta(c)
			}
		}
		return nil
	})
	if err != nil {
		return backend.ChatResponse{}, fmt.Errorf("ollama: %w", err)
	}
	return backend.ChatResponse{Content: full.String()}, nil
}

// Models implements backend.Backend via Ollama's /api/tags: every
// locally installed model, in the server's own order.
func (o *Ollama) Models(ctx context.Context) ([]string, error) {
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := backend.GetJSON(ctx, o.client, o.baseURL, "/api/tags", &payload); err != nil {
		return nil, fmt.Errorf("ollama: %w", err)
	}
	names := make([]string, 0, len(payload.Models))
	for _, m := range payload.Models {
		names = append(names, m.Name)
	}
	return names, nil
}
