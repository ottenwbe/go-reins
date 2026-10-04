// Package agent contains the agent loop: the part that decides what to
// do next. The harness feeds it tools and a backend; the loop is
// reason -> act -> observe until the model produces a final answer.
package agent

import (
	"context"
	"fmt"

	"go-reins/internal/backend"
)

// Tool is a capability the agent can invoke. In this first version no
// tools are registered, but the loop is already shaped around them:
// a later step only has to parse tool calls out of the model reply
// and dispatch them here.
type Tool interface {
	// Name is what the model uses to refer to the tool.
	Name() string
	// Description tells the model what the tool is for.
	Description() string
	// Execute runs the tool with raw JSON arguments and returns
	// a human-readable observation.
	Execute(ctx context.Context, args string) (string, error)
}

// Agent owns the conversation with a single backend.
type Agent struct {
	backend  backend.Backend
	model    string
	system   string
	tools    []Tool
	history  []backend.Message
	maxTurns int
}

// New builds an agent over the given backend.
func New(b backend.Backend, model, system string, tools []Tool) *Agent {
	return &Agent{
		backend:  b,
		model:    model,
		system:   system,
		tools:    tools,
		maxTurns: 8,
	}
}

// Run processes a user prompt through the agent loop and returns the
// final assistant answer.
//
// Current shape: one round trip. The loop structure is in place so
// tool dispatch can slot in without reorganizing anything.
func (a *Agent) Run(ctx context.Context, prompt string) (string, error) {
	a.history = []backend.Message{
		{Role: backend.RoleSystem, Content: a.system},
		{Role: backend.RoleUser, Content: prompt},
	}

	for turn := 1; turn <= a.maxTurns; turn++ {
		resp, err := a.backend.Chat(ctx, backend.ChatRequest{
			Model:    a.model,
			Messages: a.history,
		})
		if err != nil {
			return "", fmt.Errorf("agent: turn %d: %w", turn, err)
		}

		a.history = append(a.history, backend.Message{
			Role:    backend.RoleAssistant,
			Content: resp.Content,
		})

		// No tools yet: the first reply is always final. Tool-call
		// parsing and dispatch will hook in here.
		return resp.Content, nil
	}

	return "", fmt.Errorf("agent: exceeded %d turns without a final answer", a.maxTurns)
}
