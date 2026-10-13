// Package llamacpp implements backend.Backend against a llama.cpp
// server (llama-server), which exposes an OpenAI-compatible
// /v1/chat/completions endpoint.
package llamacpp

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

// Chat implements backend.Backend by delegating to ChatStream with
// no observer: one wire path serves both callers, streaming or not.
func (l *LlamaCpp) Chat(ctx context.Context, req backend.ChatRequest) (backend.ChatResponse, error) {
	return l.ChatStream(ctx, req, nil)
}

// ChatStream implements backend.Streamer via /v1/chat/completions
// with stream: true. llama-server answers in SSE: "data: {json}"
// lines carrying choices[0].delta.content, closed by
// "data: [DONE]". Comment and keep-alive lines are ignored.
func (l *LlamaCpp) ChatStream(ctx context.Context, req backend.ChatRequest, onDelta func(string)) (backend.ChatResponse, error) {
	var full strings.Builder
	chunks := 0
	err := backend.PostJSONLines(ctx, l.client, l.baseURL, "/v1/chat/completions", map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   true,
	}, func(line []byte) error {
		s := string(line)
		if !strings.HasPrefix(s, "data:") {
			return nil // SSE comment or keep-alive
		}
		data := strings.TrimSpace(strings.TrimPrefix(s, "data:"))
		if data == "" || data == "[DONE]" {
			return nil // keep-alive or the stream terminator
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("decode chunk: %w", err)
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return errors.New(chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			return nil
		}
		chunks++
		if c := chunk.Choices[0].Delta.Content; c != "" {
			full.WriteString(c)
			if onDelta != nil {
				onDelta(c)
			}
		}
		return nil
	})
	if err != nil {
		return backend.ChatResponse{}, fmt.Errorf("llamacpp: %w", err)
	}
	if chunks == 0 {
		return backend.ChatResponse{}, errors.New("llamacpp: empty response")
	}
	return backend.ChatResponse{Content: full.String()}, nil
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
