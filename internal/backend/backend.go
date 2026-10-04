// Package backend defines the contract between the harness and the
// inference engine. A backend is any server that can turn a message
// history into a model response. Swapping Ollama for llama.cpp (or a
// future cloud API) means writing one new adapter, nothing else changes.
package backend

import "context"

// Role of a chat message. Mirrors the standard system/user/assistant roles.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one entry in the conversation history.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// ChatRequest asks the backend for the next assistant message.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// ChatResponse is the model's reply.
type ChatResponse struct {
	Content string
}

// Backend is an inference backend (Ollama, llama.cpp server, ...).
type Backend interface {
	// Name identifies the backend in logs and config.
	Name() string
	// Chat sends the message history and returns the model's reply.
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// Config carries everything a backend adapter needs to connect.
type Config struct {
	BaseURL string // e.g. http://localhost:11434 for Ollama
	Model   string // e.g. llama3.2, qwen2.5
}
