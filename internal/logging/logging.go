// Package logging provides the CLI logger: one zap logger per run,
// built from the configured level, writing to stderr so stdout stays
// reserved for the agent's answer.
package logging

import (
	"fmt"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New builds a console logger writing to stderr at the given level
// (debug, info, warn, error). An invalid level is rejected.
func New(level string) (*zap.Logger, error) {
	return newLogger(level, zapcore.Lock(os.Stderr))
}

// newLogger builds a console logger over the given sink. Split from
// New so tests can capture output.
func newLogger(level string, w zapcore.WriteSyncer) (*zap.Logger, error) {
	var lvl zapcore.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid log level %q: expected debug, info, warn, or error", level)
	}

	encoderCfg := zap.NewDevelopmentEncoderConfig()
	encoderCfg.EncodeLevel = zapcore.CapitalLevelEncoder
	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderCfg),
		w,
		lvl,
	)
	return zap.New(core), nil
}

// Nop returns a logger that discards everything — the default for
// code paths that were not given a logger.
func Nop() *zap.Logger { return zap.NewNop() }
