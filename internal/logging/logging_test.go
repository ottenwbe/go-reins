package logging

import (
	"bytes"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func newTestLogger(t *testing.T, level string) (*zap.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	l, err := newLogger(level, zapcore.AddSync(&buf))
	if err != nil {
		t.Fatalf("newLogger(%q): %v", level, err)
	}
	return l, &buf
}

func TestNewAcceptsKnownLevels(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		if _, err := New(level); err != nil {
			t.Errorf("New(%q): %v", level, err)
		}
	}
}

func TestNewRejectsInvalidLevel(t *testing.T) {
	if _, err := New("chatty"); err == nil {
		t.Error("New: want error for invalid level, got nil")
	}
}

func TestLoggerFiltersBelowLevel(t *testing.T) {
	l, buf := newTestLogger(t, "warn")

	l.Info("should be filtered")
	if buf.Len() != 0 {
		t.Errorf("output = %q, want empty (info filtered at warn level)", buf.String())
	}

	l.Warn("should pass", zap.String("key", "value"))
	out := buf.String()
	if !strings.Contains(out, "should pass") || !strings.Contains(out, "key") {
		t.Errorf("output = %q, want warn entry with fields", out)
	}
}

func TestLoggerEmitsDebugAtDebugLevel(t *testing.T) {
	l, buf := newTestLogger(t, "debug")

	l.Debug("detail")
	if !strings.Contains(buf.String(), "detail") {
		t.Errorf("output = %q, want debug entry", buf.String())
	}
}
