//go:build unix

package shell

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestShellTimeoutKillsBackgroundChild pins the reason the command
// runs in its own process group: a command that backgrounds a child
// and leaves it inheriting the output pipes must not extend the run
// far past the timeout, must not leak the child, and must not lose
// the output it produced. Without the group kill the parent dies on
// time while the child holds the pipes open, and the run waits for
// it - this test fails by taking as long as the child's sleep.
func TestShellTimeoutKillsBackgroundChild(t *testing.T) {
	s := &Shell{timeout: 300 * time.Millisecond}

	// The command exits fast; the background child holds the pipes.
	start := time.Now()
	out, err := s.Execute(context.Background(), `{"command":"sleep 30 & echo started"}`)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute: %v - a backgrounding command must report normally", err)
	}
	if !strings.Contains(out, "started") {
		t.Errorf("observation lost the command output: %q", out)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("run took %s, want it bounded near the pipe grace - a background child outlived it", elapsed)
	}
	// The grace is the ceiling; the group kill usually ends the
	// wait sooner.
	t.Logf("backgrounding run took %s", elapsed)

	// A foreground command past the timeout fails with the timeout
	// error, promptly.
	start = time.Now()
	_, err = s.Execute(context.Background(), `{"command":"sleep 30"}`)
	elapsed = time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Execute error = %v, want the timeout error", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("timed-out run took %s, want it bounded near the 300ms timeout", elapsed)
	}
	t.Logf("timed-out run took %s", elapsed)
}
