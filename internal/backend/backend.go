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
	// Usage carries the token counts the backend measured for the
	// reply, when it reports them. Zero fields mean the backend
	// said nothing about usage, not that no tokens moved.
	Usage Usage
}

// Usage is the token accounting of one reply, as the backend
// reported it in the stream's final chunk.
type Usage struct {
	// PromptTokens is the size of the processed conversation.
	PromptTokens int
	// CompletionTokens is the number of tokens the reply contains -
	// the number a token/s rate is measured against.
	CompletionTokens int
}

// Backend is an inference backend (Ollama, llama.cpp server, ...).
type Backend interface {
	// Name identifies the backend in logs and config.
	Name() string
	// Chat sends the message history and returns the model's reply.
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
	// Models lists the names of the models the backend can serve.
	// It backs the chat REPL's /models command; names are the same
	// strings ChatRequest.Model accepts.
	Models(ctx context.Context) ([]string, error)
}

// Config carries everything a backend adapter needs to connect.
// The model is deliberately not here: it travels per request in
// ChatRequest, one source of truth instead of two.
type Config struct {
	BaseURL string // e.g. http://localhost:11434 for Ollama
}
