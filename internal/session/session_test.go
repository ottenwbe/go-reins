package session

import (
	"strings"
	"testing"

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
