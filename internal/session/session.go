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
	"go-reins/internal/tools/read"
	"go-reins/internal/tools/shell"
	"go-reins/internal/tui"
)

// systemPrompt is the base prompt every session starts with; the
// agent appends its tool docs.
const systemPrompt = `You are a helpful assistant running inside a small agent harness.
Answer concisely and accurately.`

// NewAgent builds the agent for one session from the resolved
// configuration: backend and model from cfg, the shell and read
// tools, the logger, and the approver. A nil approver lets every
// tool call through (--yes); a non-nil approver gates only mutating
// tools (see agent.Risk). Extra agent options (e.g. a tool observer
// for the UI) are appended after the built-ins.
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

	opts = append(opts, agent.WithLogger(logger))
	if approve != nil {
		opts = append(opts, agent.WithApprover(approve))
	}

	logger.Info("starting session",
		zap.String("backend", b.Name()),
		zap.String("model", cfg.Model))

	return agent.New(b, cfg.Model, systemPrompt, []agent.Tool{shell.New(), read.New()}, opts...), nil
}

// newBackend builds the backend selected by cfg. It cannot live in
// the backend package: the adapters import it for the Backend
// interface, so a selector there would be an import cycle.
func newBackend(cfg config.Config) (backend.Backend, error) {
	bcfg := backend.Config{
		BaseURL: cfg.URL,
		Model:   cfg.Model,
	}
	switch cfg.Backend {
	case "ollama":
		return ollama.New(bcfg), nil
	case "llamacpp":
		return llamacpp.New(bcfg), nil
	default:
		return nil, fmt.Errorf("unknown backend %q: expected ollama or llamacpp", cfg.Backend)
	}
}

// GateApprover returns the TUI gate's approver, or nil to
// auto-approve every tool call when cfg.Yes is set.
func GateApprover(gate *tui.ApprovalGate, cfg config.Config) agent.Approver {
	if cfg.Yes {
		return nil
	}
	return gate.Approver()
}
