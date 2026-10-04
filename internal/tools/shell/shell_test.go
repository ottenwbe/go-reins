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
