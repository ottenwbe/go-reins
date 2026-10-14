package session

import (
	"context"
	"fmt"
	"strings"

	"go-reins/internal/agent"
	"go-reins/internal/config"
	"go-reins/internal/tools/read"
	"go-reins/internal/tools/search"
	"go-reins/internal/tools/shell"
)

// toolOrder lists every tool the harness can load, in the order a
// session registers them when the operator does not restrict the set.
var toolOrder = []string{"read", "search", "shell"}

// toolRegistry maps tool names to their constructors. Adding a tool
// to the harness means one entry here.
var toolRegistry = map[string]func() agent.Tool{
	"read":   func() agent.Tool { return read.New() },
	"search": func() agent.Tool { return search.New() },
	"shell":  func() agent.Tool { return shell.New() },
}

// Tools assembles the agent's tool set from configuration:
//
//   - cfg.Tools selects which tools load by name; nil means all of
//     them (the default), an explicitly empty list loads none.
//   - cfg.ToolRisk overrides the risk tier of individual tools, so
//     the operator decides how the approval gate scores a tool - a
//     sandboxed shell can run without approval, a shared machine can
//     gate even the read tool.
//
// Unknown tool names, risk overrides that name a tool that is not
// loaded, and unparseable risk values are errors: failing at
// assembly is better than silently running a different tool set -
// or a different risk policy - than the operator asked for.
func Tools(cfg config.Config) ([]agent.Tool, error) {
	names := cfg.Tools
	if names == nil {
		names = toolOrder
	}

	// A risk override for a tool that is not loaded is an error, not
	// a silent no-op: the operator's policy must actually apply.
	// Duplicate tool names error too - announcing a tool twice in
	// the system prompt is a config mistake, not an intent.
	selected := make(map[string]bool, len(names))
	for _, name := range names {
		if selected[name] {
			return nil, fmt.Errorf("tool %q listed twice in tools", name)
		}
		selected[name] = true
	}
	for name := range cfg.ToolRisk {
		if selected[name] {
			continue
		}
		if _, ok := toolRegistry[name]; !ok {
			return nil, fmt.Errorf("unknown tool %q in tool-risk, available: %s",
				name, strings.Join(toolOrder, ", "))
		}
		return nil, fmt.Errorf("tool-risk overrides %q, but the tools list does not load it", name)
	}

	tools := make([]agent.Tool, 0, len(names))
	for _, name := range names {
		build, ok := toolRegistry[name]
		if !ok {
			return nil, fmt.Errorf("unknown tool %q, available: %s",
				name, strings.Join(toolOrder, ", "))
		}
		t := build()
		if riskName, ok := cfg.ToolRisk[name]; ok {
			risk, err := agent.ParseRisk(riskName)
			if err != nil {
				return nil, fmt.Errorf("tool %q: %w", name, err)
			}
			t = riskOverride{tool: t, risk: risk}
		}
		tools = append(tools, t)
	}
	return tools, nil
}

// riskOverride wraps a tool with an operator-configured risk tier.
// Everything but Risk delegates to the wrapped tool; the agent loop
// asks only Risk when scoring a call for approval.
type riskOverride struct {
	tool agent.Tool
	risk agent.Risk
}

func (r riskOverride) Name() string        { return r.tool.Name() }
func (r riskOverride) Description() string { return r.tool.Description() }
func (r riskOverride) Risk() agent.Risk    { return r.risk }
func (r riskOverride) Execute(ctx context.Context, args string) (string, error) {
	return r.tool.Execute(ctx, args)
}
