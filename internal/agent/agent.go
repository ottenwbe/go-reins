// Package agent contains the agent loop: the part that decides what to
// do next. The harness feeds it tools and a backend; the loop is
// reason -> act -> observe until the model produces a final answer.
package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"go-reins/internal/backend"
)

const defaultMaxTurns = 8

// Tool-call convention of the text protocol. The agent injects these
// rules into the system prompt; the model requests a tool by emitting
// a single TOOLCALL line as its whole reply, and receives the outcome
// as a TOOLRESULT user message. Arguments must be single-line JSON.
const (
	toolCallLine    = "TOOLCALL"
	toolResultLine  = "TOOLRESULT"
	toolCallPattern = `(?m)^TOOLCALL\s+(\S+)\s+(\{.*\})\s*$`
)

var toolCallRe = regexp.MustCompile(toolCallPattern)

// Tool is a capability the agent can invoke. The model refers to a
// tool by Name and passes single-line JSON arguments; Execute turns
// them into a human-readable observation for the model.
type Tool interface {
	// Name is what the model uses to refer to the tool.
	Name() string
	// Description tells the model what the tool is for.
	Description() string
	// Execute runs the tool with raw JSON arguments and returns
	// a human-readable observation.
	Execute(ctx context.Context, args string) (string, error)
}

// Approver gates every tool call before it runs — the human in the
// loop. Returning false denies the call; the model receives the
// denial as an observation and can react (ask the user, try another
// way, or answer without the tool).
type Approver func(name, args string) bool

// Agent owns the conversation with a single backend.
type Agent struct {
	backend  backend.Backend
	model    string
	system   string
	tools    []Tool
	byName   map[string]Tool
	approver Approver
	maxTurns int
}

// Option configures an Agent at construction time.
type Option func(*Agent)

// WithMaxTurns caps how many backend round trips a single Run may
// make before giving up. Values below 1 are ignored.
func WithMaxTurns(n int) Option {
	return func(a *Agent) {
		if n >= 1 {
			a.maxTurns = n
		}
	}
}

// WithApprover installs a human-in-the-loop gate: every tool call is
// presented to the approver before execution. A nil approver allows
// everything.
func WithApprover(ap Approver) Option {
	return func(a *Agent) {
		a.approver = ap
	}
}

// New builds an agent over the given backend. Registered tools are
// announced to the model via the text tool-call protocol.
func New(b backend.Backend, model, system string, tools []Tool, opts ...Option) *Agent {
	a := &Agent{
		backend:  b,
		model:    model,
		system:   system,
		tools:    tools,
		byName:   make(map[string]Tool, len(tools)),
		maxTurns: defaultMaxTurns,
	}
	for _, t := range tools {
		a.byName[t.Name()] = t
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// RunResult captures the outcome of a single Run: the final answer,
// how many backend round trips it took, and the full conversation
// history for review.
type RunResult struct {
	Answer  string
	Turns   int
	History []backend.Message
}

// Run processes a user prompt through the agent loop and returns the
// final assistant answer. A reply without a tool call is final; a
// reply with a TOOLCALL line executes the tool, appends the
// observation to the history, and takes another turn.
func (a *Agent) Run(ctx context.Context, prompt string) (RunResult, error) {
	history := []backend.Message{
		{Role: backend.RoleSystem, Content: a.system + a.toolDocs()},
		{Role: backend.RoleUser, Content: prompt},
	}

	for turn := 1; turn <= a.maxTurns; turn++ {
		resp, err := a.backend.Chat(ctx, backend.ChatRequest{
			Model:    a.model,
			Messages: history,
		})
		if err != nil {
			return RunResult{}, fmt.Errorf("agent: turn %d: %w", turn, err)
		}

		history = append(history, backend.Message{
			Role:    backend.RoleAssistant,
			Content: resp.Content,
		})

		name, args, ok := parseToolCall(resp.Content)
		if !ok {
			return RunResult{Answer: resp.Content, Turns: turn, History: history}, nil
		}

		observation := a.executeTool(ctx, name, args)
		history = append(history, backend.Message{
			Role:    backend.RoleUser,
			Content: toolResultLine + ": " + observation,
		})
	}

	return RunResult{}, fmt.Errorf("agent: exceeded %d turns without a final answer", a.maxTurns)
}

// executeTool runs one tool call and returns the observation to feed
// back to the model. Errors are observations too: the model sees them
// and can react instead of the whole run failing.
func (a *Agent) executeTool(ctx context.Context, name, args string) string {
	tool, ok := a.byName[name]
	if !ok {
		names := make([]string, 0, len(a.byName))
		for n := range a.byName {
			names = append(names, n)
		}
		return fmt.Sprintf("unknown tool %q, available tools: %s",
			name, strings.Join(names, ", "))
	}

	if a.approver != nil && !a.approver(name, args) {
		return fmt.Sprintf("tool call %q with args %s was not approved by the operator; "+
			"ask the user, try a different way, or answer without the tool", name, args)
	}

	observation, err := tool.Execute(ctx, args)
	if err != nil {
		return fmt.Sprintf("tool %q failed: %v", name, err)
	}
	return observation
}

// parseToolCall extracts the first TOOLCALL line from a model reply.
// It reports ok=false for replies that are final answers.
func parseToolCall(reply string) (name, args string, ok bool) {
	m := toolCallRe.FindStringSubmatch(reply)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// toolDocs renders the tool-call protocol for the system prompt. With
// no tools registered it is empty and the protocol stays invisible.
func (a *Agent) toolDocs() string {
	if len(a.tools) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\nYou can call tools to help answer. Available tools:\n")
	for _, t := range a.tools {
		fmt.Fprintf(&sb, "- %s: %s\n", t.Name(), t.Description())
	}
	fmt.Fprintf(&sb, `
To call a tool, reply with a single line and nothing else:
%s <name> <json-arguments>

The arguments must be single-line JSON. You will receive the outcome as a
%s message; use it to continue. When no tool is needed, reply normally
without a %s line.`,
		toolCallLine, toolResultLine, toolCallLine)
	return sb.String()
}
