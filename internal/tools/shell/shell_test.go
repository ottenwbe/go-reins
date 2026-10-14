package shell

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestExecuteReturnsOutput(t *testing.T) {
	s := New()

	got, err := s.Execute(context.Background(), `{"command": "echo hello"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got != "hello\n" {
		t.Errorf("output = %q, want %q", got, "hello\n")
	}
}

func TestExecuteReportsExitStatus(t *testing.T) {
	s := New()

	got, err := s.Execute(context.Background(), `{"command": "echo boom >&2; exit 3"}`)
	if err != nil {
		t.Fatalf("Execute: a non-zero exit must be an observation, not an error: %v", err)
	}
	if !strings.Contains(got, "boom") || !strings.Contains(got, "(exit status 3)") {
		t.Errorf("output = %q, want command output and exit status", got)
	}
}

func TestExecuteTimesOut(t *testing.T) {
	s := New()
	s.timeout = 10 * time.Millisecond

	_, err := s.Execute(context.Background(), `{"command": "sleep 5"}`)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Execute error = %v, want timeout error", err)
	}
}

func TestExecuteRejectsBadArguments(t *testing.T) {
	s := New()

	if _, err := s.Execute(context.Background(), `not json`); err == nil {
		t.Error("Execute: want error for unparseable arguments, got nil")
	}
	if _, err := s.Execute(context.Background(), `{}`); err == nil {
		t.Error("Execute: want error for missing command, got nil")
	}
}

func TestExecuteTruncatesOutput(t *testing.T) {
	s := New()

	// yes repeats 'y' on every line; 10k lines exceed maxOutput.
	got, err := s.Execute(context.Background(), `{"command": "yes | head -10000"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(got) > maxOutput+len("\n(output truncated)") {
		t.Errorf("output length = %d, want capped at %d", len(got), maxOutput)
	}
	if !strings.Contains(got, "(output truncated)") {
		t.Errorf("output missing truncation note, tail: %q", got[len(got)-40:])
	}
}

// TestShellMemoryCappedOutput pins the streaming cap: a command that
// produces far more output than the budget must not exceed it, and
// the note must say so. (CombinedOutput would have buffered the
// whole thing in memory first.)
func TestShellMemoryCappedOutput(t *testing.T) {
	out, err := New().Execute(context.Background(),
		`{"command":"yes hello | head -c 300000"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(out) > maxOutput+len("\n(output truncated)") {
		t.Errorf("output length = %d, want capped at %d + note", len(out), maxOutput)
	}
	if !strings.Contains(out, "(output truncated)") {
		t.Error("output missing truncation note")
	}
}

// TestShellReportsCancellation pins that a command killed by parent
// cancellation is an error, not an "(exit status -1)" observation
// the model would read as a failed program.
func TestShellReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)

	_, err := New().Execute(ctx, `{"command":"sleep 5"}`)
	if err == nil {
		t.Fatal("cancelled command: want error")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("error = %v, want cancellation error", err)
	}
}

// TestShellNeverStartedDoesNotPanic pins the guard on the kill
// seam: a command that never started - an already-cancelled
// context, a missing shell - has no process, and the post-run
// cleanup must not dereference one. Fails by panicking.
func TestShellNeverStartedDoesNotPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New().Execute(ctx, `{"command":"echo hi"}`)
	if err == nil {
		t.Error("a cancelled context should fail the command")
	}
}
