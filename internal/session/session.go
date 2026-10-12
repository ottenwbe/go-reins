// Package session assembles the pieces every command needs to run
// the agent: the backend picked by the resolved configuration, the
// shell tool, and the logger. Commands call NewAgent instead of
// duplicating the wiring.
package session

import (
	"fmt"

	"go.uber.org/zap"

	"go-reins/internal/agent"
	"go-reins/internal/backend"
	"go-reins/internal/backend/llamacpp"
	"go-reins/internal/backend/ollama"
	"go-reins/internal/config"
	"go-reins/internal/logging"
	"go-reins/internal/tui"
)

// systemPrompt is the base prompt every session starts with; the
// agent appends its tool docs.
const systemPrompt = `You are a helpful assistant running inside a small agent harness.
Answer concisely and accurately.`

// NewAgent builds the agent for one session from the resolved
// configuration: backend and model from cfg, the tools selected and
// risk-scored by cfg.Tools/cfg.ToolRisk (see Tools), the logger, and
// the approver. A nil approver lets every tool call through (--yes);
// a non-nil approver gates only mutating tools (see agent.Risk).
// Extra agent options (e.g. a tool observer for the UI) are appended
// after the built-ins.
func NewAgent(cfg config.Config, approve agent.Approver, opts ...agent.Option) (*agent.Agent, error) {
	logger, err := logging.New(cfg.LogLevel)
	if err != nil {
		return nil, err
	}

	b, err := newBackend(cfg)
	if err != nil {
		return nil, err
	}

	if cfg.Model == "" {
		return nil, fmt.Errorf("no model set: use --model, config file, or GO_REINS_MODEL")
	}

	tools, err := Tools(cfg)
	if err != nil {
		return nil, err
	}

	opts = append(opts, agent.WithLogger(logger), agent.WithMaxTurns(cfg.MaxTurns))
	if approve != nil {
		opts = append(opts, agent.WithApprover(approve))
	}

	logger.Info("starting session",
		zap.String("backend", b.Name()),
		zap.String("model", cfg.Model),
		zap.Int("tools", len(tools)))

	return agent.New(b, cfg.Model, systemPrompt, tools, opts...), nil
}

// NewBackend builds the named backend from cfg. It backs the chat
// REPL's /backend switch: the same validation as startup applies to
// a mid-session swap. The model is deliberately not wired here - it
// travels per request - and cfg.URL belongs to the backend cfg
// selects: a switch to any other backend leaves the URL behind and
// the adapter applies its own default (see backendURL).
func NewBackend(cfg config.Config, name string) (backend.Backend, error) {
	bcfg := backend.Config{BaseURL: backendURL(cfg, name)}
	switch name {
	case "ollama":
		b, err := ollama.New(bcfg)
		return b, err
	case "llamacpp":
		b, err := llamacpp.New(bcfg)
		return b, err
	default:
		return nil, fmt.Errorf("unknown backend %q: expected ollama or llamacpp", name)
	}
}

// backendURL picks the base URL for a named backend: cfg.URL when
// the operator configured it for that backend, empty otherwise so
// the adapter applies its own default. A URL resolved for one
// backend - say ollama on a non-default port - would point every
// other backend at the wrong server if it carried over a switch.
func backendURL(cfg config.Config, name string) string {
	if name == cfg.Backend {
		return cfg.URL
	}
	return ""
}

// newBackend builds the backend selected by cfg. It cannot live in
// the backend package: the adapters import it for the Backend
// interface, so a selector there would be an import cycle.
func newBackend(cfg config.Config) (backend.Backend, error) {
	return NewBackend(cfg, cfg.Backend)
}

// GateApprover returns the TUI gate's approver, or nil to
// auto-approve every tool call when cfg.Yes is set.
func GateApprover(gate *tui.ApprovalGate, cfg config.Config) agent.Approver {
	if cfg.Yes {
		return nil
	}
	return gate.Approver()
}
