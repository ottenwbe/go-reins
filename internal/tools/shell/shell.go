// Package shell implements an agent.Tool that runs a shell command
// on the local machine and returns its combined output. It is the
// "figure out which system you run on" tool: with it, the agent can
// inspect its environment instead of guessing.
package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"go-reins/internal/agent"
)

const (
	// defaultTimeout bounds a single command.
	defaultTimeout = 30 * time.Second
	// pipeGrace bounds how long the run keeps draining output
	// after the command - or its timeout - has ended: a background
	// child inheriting the pipes can outlive its parent, and
	// without a bound the run would wait for it indefinitely. The
	// process-group kill makes the grace unnecessary on unix; it
	// is the portable floor underneath.
	pipeGrace = 2 * time.Second
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

	// Cap while reading, not after: CombinedOutput would buffer a
	// noisy command's whole output in memory for the timeout window.
	out := &cappedWriter{limit: maxOutput}
	cmd := exec.CommandContext(ctx, "sh", "-c", call.Command)
	cmd.Stdout = out
	cmd.Stderr = out
	// The command runs in its own process group and a timeout
	// kills the group: a command that backgrounds a child which
	// inherits the pipes would otherwise leave that child running -
	// sh dies on time, the child holds the output open, and the
	// run waits for it long past the timeout. WaitDelay is the
	// portable floor: past the grace the pipes close and the run
	// returns no matter what still holds them.
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killTree(cmd) }
	cmd.WaitDelay = pipeGrace
	err := cmd.Run()
	// The run is over, but its process group may not be: a command
	// that exited early can leave a background child running. reap
	// the group so a gated tool call never leaks processes; a group
	// that fully exited is already gone (killGroup tolerates ESRCH),
	// and a command that means to detach can setsid its way out.
	killTree(cmd) // reap any survivors: no group, no leak
	observation := out.String()

	// The command exited on its own, but a background child held
	// the pipes past the grace: WaitDelay closed them and reported
	// it. The context may have expired while waiting for the pipes,
	// but the command itself finished - its exit status and output
	// stand, timeout or not. This check comes before the context
	// switch for exactly that reason.
	if errors.Is(err, exec.ErrWaitDelay) {
		if st := cmd.ProcessState; st == nil || st.ExitCode() == 0 {
			if len(observation) == 0 {
				return "(no output)", nil
			}
			return observation, nil
		}
		return fmt.Sprintf("%s\n(exit status %d)", observation, cmd.ProcessState.ExitCode()), nil
	}

	// Cancellation first: exec reports a killed process as an
	// ordinary non-zero exit, and a cancelled command must not be
	// presented to the model as a failed program.
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return observation, fmt.Errorf("command timed out after %s", s.timeout)
	case context.Canceled:
		return observation, fmt.Errorf("command cancelled: %w", ctx.Err())
	}
	if err != nil {
		// Non-zero exit: report output and status as the observation
		// so the model can react, not as a hard failure.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Sprintf("%s\n(exit status %d)", observation, exitErr.ExitCode()), nil
		}
		return "", fmt.Errorf("run command: %w", err)
	}
	if len(observation) == 0 {
		return "(no output)", nil
	}
	return observation, nil
}

// cappedWriter accumulates output up to limit bytes, then keeps
// draining but discarding: the child process must never block on a
// full pipe, but its noise must not eat memory either. exec calls
// Write from copier goroutines for stdout and stderr, hence the
// mutex.
type cappedWriter struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	room := w.limit - w.buf.Len()
	if room > 0 {
		if len(p) <= room {
			w.buf.Write(p)
			return len(p), nil
		}
		w.buf.Write(p[:room])
	}
	w.truncated = true
	return len(p), nil
}

// String returns the capped output plus the truncation note, in the
// same shape the old post-hoc truncation produced.
func (w *cappedWriter) String() string {
	s := w.buf.String()
	if w.truncated {
		return s + "\n(output truncated)"
	}
	return s
}
