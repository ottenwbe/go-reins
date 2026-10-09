// Package shell implements an agent.Tool that runs a shell command
// on the local machine and returns its combined output. It is the
// "figure out which system you run on" tool: with it, the agent can
// inspect its environment instead of guessing.
package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"go-reins/internal/agent"
)

const (
	// defaultTimeout bounds a single command.
	defaultTimeout = 30 * time.Second
	// maxOutput caps the observation so one noisy command cannot
	// blow up the conversation history.
	maxOutput = 4000
)

// Shell runs commands through /bin/sh -c.
type Shell struct {
	timeout time.Duration
}

// New creates a shell tool with the default per-command timeout.
func New() *Shell {
	return &Shell{timeout: defaultTimeout}
}

// Name implements agent.Tool.
func (s *Shell) Name() string { return "shell" }

// Risk implements agent.Tool: a shell command can change anything,
// so every call is gated by the Approver.
func (s *Shell) Risk() agent.Risk { return agent.RiskMutating }

// Description implements agent.Tool.
func (s *Shell) Description() string {
	return `Run a shell command on this machine and return its combined
stdout and stderr. Use it to inspect the environment (e.g. uname -a,
sw_vers, pwd, ls). Pass arguments as JSON: {"command": "uname -a"}.
Commands are subject to operator approval.`
}

// Execute implements agent.Tool. A non-zero exit status is not an
// error here: the output plus the exit status become the observation,
// so the model sees what went wrong. Real failures (bad arguments,
// unparseable output) return an error.
func (s *Shell) Execute(ctx context.Context, args string) (string, error) {
	var call struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(args), &call); err != nil {
		return "", fmt.Errorf("invalid arguments, expected {\"command\": \"...\"}: %w", err)
	}
	if call.Command == "" {
		return "", errors.New("empty command: pass {\"command\": \"...\"}")
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", call.Command)
	out, err := cmd.CombinedOutput()
	observation := string(out)

	if ctx.Err() == context.DeadlineExceeded {
		return truncate(observation), fmt.Errorf("command timed out after %s", s.timeout)
	}
	if err != nil {
		// Non-zero exit: report output and status as the observation
		// so the model can react, not as a hard failure.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return truncate(fmt.Sprintf("%s\n(exit status %d)", observation, exitErr.ExitCode())), nil
		}
		return "", fmt.Errorf("run command: %w", err)
	}
	if len(observation) == 0 {
		return "(no output)", nil
	}
	return truncate(observation), nil
}

// truncate caps the observation length and notes the cut.
func truncate(s string) string {
	if len(s) <= maxOutput {
		return s
	}
	return s[:maxOutput] + "\n(output truncated)"
}
