package dex

import (
	"log/slog"
)

// Logger is an interface for structured logging. Pass a custom implementation
// via WorkerOptions.Logger to integrate with your logging framework.
type Logger interface {
	Debug(msg string, keyvals ...any)
	Info(msg string, keyvals ...any)
	Warn(msg string, keyvals ...any)
	Error(msg string, keyvals ...any)
}

// defaultLogger wraps slog.Default() to implement Logger.
type defaultLogger struct{}

func (defaultLogger) Debug(msg string, keyvals ...any) { slog.Debug(msg, keyvals...) }
func (defaultLogger) Info(msg string, keyvals ...any)  { slog.Info(msg, keyvals...) }
func (defaultLogger) Warn(msg string, keyvals ...any)  { slog.Warn(msg, keyvals...) }
func (defaultLogger) Error(msg string, keyvals ...any) { slog.Error(msg, keyvals...) }

// NewDefaultLogger returns a Logger backed by slog.Default().
func NewDefaultLogger() Logger {
	return defaultLogger{}
}
