package session

import (
	"strings"
	"testing"

	"go-reins/internal/agent"
	"go-reins/internal/config"
	"go-reins/internal/tui"
)

func TestNewAgentBuildsFromConfig(t *testing.T) {
	cfg := config.Config{Backend: "ollama", Model: "llama3.2"}
	a, err := NewAgent(cfg, nil)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if a == nil {
		t.Fatal("NewAgent: nil agent, nil error")
	}
}

func TestNewAgentRejectsUnknownBackend(t *testing.T) {
	cfg := config.Config{Backend: "gpt-9000", Model: "llama3.2"}
	_, err := NewAgent(cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown backend") {
		t.Fatalf("NewAgent error = %v, want unknown backend", err)
	}
}

func TestNewAgentRejectsMissingModel(t *testing.T) {
	_, err := NewAgent(config.Config{Backend: "ollama"}, nil)
	if err == nil || !strings.Contains(err.Error(), "no model set") {
		t.Fatalf("NewAgent error = %v, want no model set", err)
	}
}

func TestGateApproverHonorsYes(t *testing.T) {
	gate := tui.NewApprovalGate()
	defer gate.Close()

	if got := GateApprover(gate, config.Config{Yes: true}); got != nil {
		t.Error("GateApprover with --yes: want nil approver")
	}
	if got := GateApprover(gate, config.Config{}); got == nil {
		t.Error("GateApprover without --yes: want gate approver")
	}
}

func TestToolsDefaultLoadsAll(t *testing.T) {
	tools, err := Tools(config.Config{})
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name() != "read" || tools[1].Name() != "shell" {
		var names []string
		for _, tl := range tools {
			names = append(names, tl.Name())
		}
		t.Errorf("tools = %v, want [read shell] in default order", names)
	}
	if tools[0].Risk() != agent.RiskReadOnly || tools[1].Risk() != agent.RiskMutating {
		t.Error("default risk tiers changed")
	}
}

func TestToolsSelection(t *testing.T) {
	tools, err := Tools(config.Config{Tools: []string{"read"}})
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "read" {
		t.Errorf("tools = %+v, want only read", tools)
	}

	// An explicitly empty list loads no tools at all.
	tools, err = Tools(config.Config{Tools: []string{}})
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("tools = %+v, want none", tools)
	}
}

func TestToolsRejectsUnknownName(t *testing.T) {
	_, err := Tools(config.Config{Tools: []string{"flame-thrower"}})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("Tools error = %v, want unknown tool", err)
	}
}

func TestToolsRiskOverride(t *testing.T) {
	tools, err := Tools(config.Config{
		Tools:    []string{"read", "shell"},
		ToolRisk: map[string]string{"shell": "read-only", "read": "mutating"},
	})
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	// Both tiers are flipped: the override, not the tool's own
	// declaration, decides the approval gate.
	if tools[0].Risk() != agent.RiskMutating {
		t.Errorf("read risk = %v, want mutating (overridden)", tools[0].Risk())
	}
	if tools[1].Risk() != agent.RiskReadOnly {
		t.Errorf("shell risk = %v, want read-only (overridden)", tools[1].Risk())
	}
	// The wrapper delegates everything but Risk.
	if tools[1].Description() == "" {
		t.Error("wrapped tool lost its description")
	}
}

func TestToolsRejectsInvalidRiskValue(t *testing.T) {
	_, err := Tools(config.Config{
		Tools:    []string{"shell"},
		ToolRisk: map[string]string{"shell": "spicy"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid risk") {
		t.Fatalf("Tools error = %v, want invalid risk", err)
	}
}

func TestNewAgentRejectsUnknownTool(t *testing.T) {
	_, err := NewAgent(config.Config{
		Backend: "ollama",
		Model:   "llama3.2",
		Tools:   []string{"nope"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("NewAgent error = %v, want unknown tool", err)
	}
}
