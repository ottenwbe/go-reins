package session

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// TestNewBackendNamed pins the exported constructor behind the
// chat REPL's /backend switch: it builds the named backend
// regardless of what cfg selects, with the same validation.
func TestNewBackendNamed(t *testing.T) {
	cfg := config.Config{Backend: "ollama", Model: "llama3.2"}

	b, err := NewBackend(cfg, "llamacpp")
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	if b.Name() != "llamacpp" {
		t.Errorf("Name = %q, want llamacpp (the argument, not cfg.Backend)", b.Name())
	}

	if _, err := NewBackend(cfg, "gpt-9000"); err == nil || !strings.Contains(err.Error(), "unknown backend") {
		t.Errorf("NewBackend(unknown) error = %v, want unknown backend", err)
	}
}

// TestNewBackendURLScopedToConfiguredBackend pins the --url scoping
// behind /backend: the URL from the config chain applies only to
// the backend it was configured for; any other backend gets the
// empty URL and falls back to its own default. The no-URL case is
// covered too - empty stays empty for both names.
func TestNewBackendURLScopedToConfiguredBackend(t *testing.T) {
	cfg := config.Config{Backend: "ollama", Model: "m", URL: "http://ollama.example:11434"}

	if got := backendURL(cfg, "ollama"); got != cfg.URL {
		t.Errorf("backendURL(ollama) = %q, want the configured URL %q", got, cfg.URL)
	}
	if got := backendURL(cfg, "llamacpp"); got != "" {
		t.Errorf("backendURL(llamacpp) = %q, want empty so the adapter default applies", got)
	}

	// Without an explicit URL both sides are empty, which the
	// adapters turn into their per-backend defaults.
	bare := config.Config{Backend: "ollama", Model: "m"}
	for _, name := range []string{"ollama", "llamacpp"} {
		if got := backendURL(bare, name); got != "" {
			t.Errorf("backendURL(%q) = %q, want empty when no URL is configured", name, got)
		}
	}
}

// TestNewBackendUsesConfiguredURL verifies the wiring end to end:
// a backend built for the configured name reaches the configured
// URL (here an httptest server), not the adapter default.
func TestNewBackendUsesConfiguredURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	cfg := config.Config{Backend: "ollama", Model: "m", URL: srv.URL}
	b, err := NewBackend(cfg, "ollama")
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	if _, err := b.Models(context.Background()); err != nil {
		t.Fatalf("Models: %v", err)
	}
	if gotPath != "/api/tags" {
		t.Errorf("request path = %q, want /api/tags (the configured URL must be used)", gotPath)
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

// TestToolsRejectsRiskOverrideForUnloadedTool pins that a risk
// override for a tool that is not loaded fails instead of silently
// not applying - the operator's policy must actually take effect.
func TestToolsRejectsRiskOverrideForUnloadedTool(t *testing.T) {
	_, err := Tools(config.Config{
		Tools:    []string{"read"},
		ToolRisk: map[string]string{"shell": "read-only"},
	})
	if err == nil || !strings.Contains(err.Error(), "does not load it") {
		t.Fatalf("Tools error = %v, want override-for-unloaded-tool error", err)
	}
}

func TestToolsRejectsRiskOverrideForUnknownTool(t *testing.T) {
	_, err := Tools(config.Config{
		ToolRisk: map[string]string{"shel": "read-only"}, // typo
	})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("Tools error = %v, want unknown-tool error", err)
	}
}

func TestToolsAcceptsOverrideForLoadedTool(t *testing.T) {
	tools, err := Tools(config.Config{
		ToolRisk: map[string]string{"shell": "read-only"},
	})
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if tools[1].Risk() != agent.RiskReadOnly {
		t.Errorf("shell risk = %v, want read-only (loaded, so the override applies)", tools[1].Risk())
	}
}

// TestToolsRejectsDuplicateNames pins that a tools list naming a
// tool twice fails instead of announcing it twice.
func TestToolsRejectsDuplicateNames(t *testing.T) {
	_, err := Tools(config.Config{Tools: []string{"read", "read"}})
	if err == nil || !strings.Contains(err.Error(), "listed twice") {
		t.Fatalf("Tools error = %v, want duplicate-name error", err)
	}
}
