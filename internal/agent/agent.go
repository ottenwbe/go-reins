// Package agent contains the agent loop: the part that decides what to
// do next. The harness feeds it tools and a backend; the loop is
// reason -> act -> observe until the model produces a final answer.
package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"go.uber.org/zap"

	"go-reins/internal/backend"
)

// DefaultMaxTurns is the turn budget used when the operator does not
// set one. Config and the CLI flag take their default from here, so
// the shipped default cannot drift apart.
const DefaultMaxTurns = 8

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

// toolCallAttemptedRe matches any reply line that starts with the
// TOOLCALL marker, well-formed or not. It lets the loop tell a
// final answer from a malformed call: models drop the tool name or
// mangle the JSON often enough that the distinction matters - the
// raw call text must not silently become the answer.
var toolCallAttemptedRe = regexp.MustCompile(`(?m)^TOOLCALL\b`)

// Risk classifies a tool by what an approved call can do to the
// machine. It drives the approval policy: mutating tools go through
// the Approver, read-only tools run without interrupting the
// operator.
type Risk int

const (
	// RiskReadOnly tools only observe: they cannot change machine
	// state, so the agent loop runs them without approval.
	RiskReadOnly Risk = iota
	// RiskMutating tools can change machine state; every call is
	// gated by the Approver.
	RiskMutating
)

// ParseRisk maps a configuration string to a Risk tier: "read-only"
// or "mutating", case-insensitive.
func ParseRisk(s string) (Risk, error) {
	switch strings.ToLower(s) {
	case "read-only":
		return RiskReadOnly, nil
	case "mutating":
		return RiskMutating, nil
	}
	return 0, fmt.Errorf("invalid risk %q: expected read-only or mutating", s)
}

// Tool is a capability the agent can invoke. The model refers to a
// tool by Name and passes single-line JSON arguments; Execute turns
// them into a human-readable observation for the model.
type Tool interface {
	// Name is what the model uses to refer to the tool.
	Name() string
	// Description tells the model what the tool is for.
	Description() string
	// Risk is the approval tier of the tool.
	Risk() Risk
	// Execute runs the tool with raw JSON arguments and returns
	// a human-readable observation.
	Execute(ctx context.Context, args string) (string, error)
}

// Approver gates every mutating tool call before it runs — the human
// in the loop. Returning false denies the call; the model receives
// the denial as an observation and can react (ask the user, try
// another way, or answer without the tool). Read-only tools
// (RiskReadOnly) skip this gate.
type Approver func(name, args string) bool

// ToolObserver is notified of every tool call just before it runs,
// with the tool name and its raw JSON arguments. It lets a UI
// report which tool the agent is using; it has no influence on the
// run.
type ToolObserver func(name, args string)

// Agent owns the conversation with a single backend.
type Agent struct {
	backend  backend.Backend
	model    string
	system   string
	tools    []Tool
	byName   map[string]Tool
	approver Approver
	observer ToolObserver
	stream   StreamObserver
	logger   *zap.Logger
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

// WithToolObserver installs a listener that is notified of every
// tool call just before it runs. A nil observer is ignored.
func WithToolObserver(o ToolObserver) Option {
	return func(a *Agent) {
		if o != nil {
			a.observer = o
		}
	}
}

// WithLogger installs a logger for the agent loop: turns at debug,
// tool calls at info, denials and failures at warn. A nil logger
// leaves the default no-op logger in place.
func WithLogger(l *zap.Logger) Option {
	return func(a *Agent) {
		if l != nil {
			a.logger = l
		}
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
		logger:   zap.NewNop(),
		maxTurns: DefaultMaxTurns,
	}
	for _, t := range tools {
		a.byName[t.Name()] = t
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// SetModel switches the model used for the next and all following
// requests. The model travels per request, so a switch applies
// mid-conversation without rebuilding anything; the history stays
// valid. Not safe to call while a Run or Step is in flight.
func (a *Agent) SetModel(model string) { a.model = model }

// Model returns the model used for the next request.
func (a *Agent) Model() string { return a.model }

// SetBackend swaps the inference backend under a live conversation.
// Messages are backend-agnostic, so the history carries over; the
// model may not exist on the new backend - switch it too (see
// Models). Not safe to call while a Run or Step is in flight.
func (a *Agent) SetBackend(b backend.Backend) { a.backend = b }

// BackendName names the current backend, for display.
func (a *Agent) BackendName() string { return a.backend.Name() }

// Backend returns the current backend. Callers that outlive the
// moment - a chat listing Cmd running in its own goroutine -
// capture it now so a concurrent SetBackend cannot race them, and
// compare Backend().Name() later to spot results gone stale.
func (a *Agent) Backend() backend.Backend { return a.backend }

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
//
// Run is stateless: each call starts a fresh conversation. Use Step
// to continue one across multiple prompts.
func (a *Agent) Run(ctx context.Context, prompt string) (RunResult, error) {
	return a.Step(ctx, nil, prompt)
}

// Step runs one user prompt against an existing conversation and
// returns the updated history alongside the answer. A nil or empty
// history starts a fresh one (system prompt plus this prompt); the
// history from a previous Step can be passed back in to continue the
// session. Each Step gets its own turn budget.
func (a *Agent) Step(ctx context.Context, history []backend.Message, prompt string) (RunResult, error) {
	history = a.seedHistory(history)
	history = append(history, backend.Message{
		Role:    backend.RoleUser,
		Content: prompt,
	})

	for turn := 1; turn <= a.maxTurns; turn++ {
		a.logger.Debug("agent turn", zap.Int("turn", turn), zap.Int("messages", len(history)))

		resp, err := a.chat(ctx, turn, history)
		if err != nil {
			return RunResult{}, err
		}
		history = append(history, backend.Message{
			Role:    backend.RoleAssistant,
			Content: resp.Content,
		})

		name, args, ok, correction := parseToolCall(resp.Content)
		if !ok {
			if correction != "" {
				// A malformed tool call is not a final answer: the
				// correction goes back as a TOOLRESULT and the
				// model retries with the right shape. Without this,
				// the raw call text would land as the answer - the
				// failure mode where tool calls "silently break".
				a.logger.Warn("malformed tool call", zap.Int("turn", turn))
				history = append(history, backend.Message{
					Role:    backend.RoleUser,
					Content: toolResultLine + ": " + correction,
				})
				continue
			}
			a.logger.Info("final answer", zap.Int("turns", turn))
			return RunResult{Answer: resp.Content, Turns: turn, History: history}, nil
		}

		history = append(history, a.runToolCall(ctx, name, args))
	}

	return RunResult{}, fmt.Errorf("agent: exceeded %d turns without a final answer", a.maxTurns)
}

// seedHistory returns a conversation ready for the user prompt: an
// empty history grows the system message (base prompt plus tool
// docs); an existing one is used as-is, so Step can continue a
// session across calls.
func (a *Agent) seedHistory(history []backend.Message) []backend.Message {
	if len(history) == 0 {
		return []backend.Message{
			{Role: backend.RoleSystem, Content: a.system + a.toolDocs()},
		}
	}
	return history
}

// chat performs one backend round trip and turns a failure into the
// agent's turn-stamped error. When an observer is installed and the
// backend offers streaming, the reply text is handed to it as it
// arrives (through the stream gate, which hides tool-call lines);
// the full response still comes back, so the loop is unchanged.
func (a *Agent) chat(ctx context.Context, turn int, history []backend.Message) (backend.ChatResponse, error) {
	req := backend.ChatRequest{
		Model:    a.model,
		Messages: history,
	}

	var (
		resp backend.ChatResponse
		err  error
	)
	if s, ok := a.backend.(backend.Streamer); ok && a.stream != nil {
		// A fresh gate per turn: line state must not leak across
		// turns.
		gate := newStreamGate(a.stream)
		resp, err = s.ChatStream(ctx, req, gate.push)
	} else {
		resp, err = a.backend.Chat(ctx, req)
	}
	if err != nil {
		a.logger.Warn("backend chat failed", zap.Int("turn", turn), zap.Error(err))
		return backend.ChatResponse{}, fmt.Errorf("agent: turn %d: %w", turn, err)
	}
	return resp, nil
}

// runToolCall executes one requested tool call and returns the
// observation message for the history: notify the observer, run the
// tool (approval gate and error-to-observation live in executeTool),
// and log the result size.
func (a *Agent) runToolCall(ctx context.Context, name, args string) backend.Message {
	a.logger.Info("tool call", zap.String("tool", name), zap.String("args", args))
	if a.observer != nil {
		a.observer(name, args)
	}
	observation := a.executeTool(ctx, name, args)
	a.logger.Debug("tool result", zap.String("tool", name), zap.Int("bytes", len(observation)))
	return backend.Message{
		Role:    backend.RoleUser,
		Content: toolResultLine + ": " + observation,
	}
}

// executeTool runs one tool call and returns the observation to feed
// back to the model. Errors are observations too: the model sees them
// and can react instead of the whole run failing.
func (a *Agent) executeTool(ctx context.Context, name, args string) string {
	tool, ok := a.byName[name]
	if !ok {
		names := a.toolNames()
		a.logger.Warn("unknown tool requested",
			zap.String("tool", name), zap.Strings("available", names))
		return fmt.Sprintf("unknown tool %q, available tools: %s",
			name, strings.Join(names, ", "))
	}

	if a.approver != nil && tool.Risk() == RiskMutating && !a.approver(name, args) {
		a.logger.Warn("tool call denied by operator",
			zap.String("tool", name), zap.String("args", args))
		return fmt.Sprintf("tool call %q with args %s was not approved by the operator; "+
			"ask the user, try a different way, or answer without the tool", name, args)
	}

	observation, err := tool.Execute(ctx, args)
	if err != nil {
		a.logger.Warn("tool failed", zap.String("tool", name), zap.Error(err))
		return fmt.Sprintf("tool %q failed: %v", name, err)
	}
	return observation
}

// toolNames lists registered tool names in registration order.
func (a *Agent) toolNames() []string {
	names := make([]string, 0, len(a.tools))
	for _, t := range a.tools {
		names = append(names, t.Name())
	}
	return names
}

// parseToolCall extracts the first TOOLCALL line from a model
// reply. It reports ok=false for replies that are final answers.
// A reply that attempts a tool call but does not match the required
// format - a missing tool name, arguments that are not JSON -
// carries a correction instead: the loop feeds it back as a
// TOOLRESULT observation, so the model retries with the right
// shape instead of the harness giving up on the call.
func parseToolCall(reply string) (name, args string, ok bool, correction string) {
	m := toolCallRe.FindStringSubmatch(reply)
	if m != nil {
		return m[1], m[2], true, ""
	}
	if toolCallAttemptedRe.MatchString(reply) {
		return "", "", false, "the TOOLCALL line did not match the required format. " +
			"Reply with exactly one line: TOOLCALL <name> <json-arguments> - " +
			"the tool name comes right after TOOLCALL, before the JSON arguments. " +
			`For example: TOOLCALL weather {"city": "Berlin"}`
	}
	return "", "", false, ""
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
	example := "shell"
	if len(a.tools) > 0 {
		example = a.tools[0].Name()
	}
	fmt.Fprintf(&sb, `
To call a tool, reply with a single line and nothing else:
%s <name> <json-arguments>

The tool name comes right after %s, before the JSON arguments - not
inside them. For example: %s %s {"example": "value"}.

The arguments must be single-line JSON. You will receive the outcome as a
%s message; use it to continue. When no tool is needed, reply normally
without a %s line.`,
		toolCallLine, toolCallLine, toolCallLine, example, toolResultLine, toolCallLine)
	return sb.String()
}
