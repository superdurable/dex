package log

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/superdurable/dex/benchmark/internal/log/tag"
)

// Logger is the benchmark logging interface, mirroring the server's Logger
// but without pulling in the server module.
type Logger interface {
	Debugf(msg string, args ...any)
	Debug(msg string, tags ...tag.Tag)
	Info(msg string, tags ...tag.Tag)
	Warn(msg string, tags ...tag.Tag)
	Error(msg string, tags ...tag.Tag)
	Fatal(msg string, tags ...tag.Tag)
}

// NewDefaultLogger creates a Logger that writes to stderr at INFO level.
// Respects the LOG_LEVEL env var (DEBUG, INFO, WARN, ERROR).
func NewDefaultLogger() Logger {
	level := slog.LevelInfo
	if envLevel := os.Getenv("LOG_LEVEL"); envLevel != "" {
		switch strings.ToUpper(envLevel) {
		case "DEBUG":
			level = slog.LevelDebug
		case "WARN":
			level = slog.LevelWarn
		case "ERROR":
			level = slog.LevelError
		}
	}
	return &logger{slogger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))}
}

type logger struct {
	slogger *slog.Logger
}

func (l *logger) buildAttrs(tags []tag.Tag) []slog.Attr {
	attrs := make([]slog.Attr, 0, len(tags))
	for _, t := range tags {
		if a := t.Attr(); a.Key != "" {
			attrs = append(attrs, a)
		}
	}
	return attrs
}

func (l *logger) Debugf(msg string, args ...any) {
	l.slogger.LogAttrs(context.Background(), slog.LevelDebug, fmt.Sprintf(msg, args...))
}

func (l *logger) Debug(msg string, tags ...tag.Tag) {
	l.slogger.LogAttrs(context.Background(), slog.LevelDebug, msg, l.buildAttrs(tags)...)
}

func (l *logger) Info(msg string, tags ...tag.Tag) {
	l.slogger.LogAttrs(context.Background(), slog.LevelInfo, msg, l.buildAttrs(tags)...)
}

func (l *logger) Warn(msg string, tags ...tag.Tag) {
	l.slogger.LogAttrs(context.Background(), slog.LevelWarn, msg, l.buildAttrs(tags)...)
}

func (l *logger) Error(msg string, tags ...tag.Tag) {
	l.slogger.LogAttrs(context.Background(), slog.LevelError, msg, l.buildAttrs(tags)...)
}

func (l *logger) Fatal(msg string, tags ...tag.Tag) {
	l.slogger.LogAttrs(context.Background(), slog.LevelError, msg, l.buildAttrs(tags)...)
	os.Exit(1)
}
