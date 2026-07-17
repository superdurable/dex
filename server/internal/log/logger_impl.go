// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/errors"
	"github.com/superdurable/dex/server/internal/log/tag"
)

type loggerImpl struct {
	slogger *slog.Logger
	skip    int
}

var _ Logger = (*loggerImpl)(nil)

const (
	skipForDefaultLogger = 3
	defaultMsgForEmpty   = "none"
)

func NewLogger(cfg *config.LogConfig) (Logger, error) {
	slogger, err := newSLoggerFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	return newLoggerFromSlogger(slogger), nil
}

// NewDefaultLogger creates a logger with INFO level by default.
// Respects LOG_LEVEL env var if set (e.g., LOG_LEVEL=DEBUG).
func NewDefaultLogger() Logger {
	level := config.LogLevelInfo
	if envLevel := os.Getenv("LOG_LEVEL"); envLevel != "" {
		level = config.LogLevel(envLevel)
	}
	return MustNewLogger(&config.LogConfig{
		Level: level,
	})
}

func MustNewLogger(cfg *config.LogConfig) Logger {
	logger, err := NewLogger(cfg)
	if err != nil {
		panic(err)
	}
	return logger
}

func MustNewDevelopmentLogger() Logger {
	slogger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	return newLoggerFromSlogger(slogger)
}

func newLoggerFromSlogger(slogger *slog.Logger) *loggerImpl {
	return &loggerImpl{
		slogger: slogger,
		skip:    skipForDefaultLogger,
	}
}

func (lg *loggerImpl) buildAttrsWithCallAt(tags []tag.Tag) []slog.Attr {
	attrs := lg.buildAttrs(tags)
	attrs = append(attrs, slog.String(tag.LoggingCallAtKey, errors.PathToCaller(lg.skip)))
	return attrs
}

func (lg *loggerImpl) buildAttrs(tags []tag.Tag) []slog.Attr {
	numExtras := 0
	for _, t := range tags {
		attr := t.Attr()
		if attr.Key == "" {
			continue
		}
		numExtras += len(t.ExtraAttrs())
	}
	attrs := make([]slog.Attr, 0, len(tags)+1+numExtras)
	for _, t := range tags {
		attr := t.Attr()
		if attr.Key == "" {
			continue
		}
		attrs = append(attrs, attr)
		if len(t.ExtraAttrs()) > 0 {
			attrs = append(attrs, t.ExtraAttrs()...)
		}
	}
	return attrs
}

func setDefaultMsg(msg string) string {
	if msg == "" {
		return defaultMsgForEmpty
	}
	return msg
}

func (lg *loggerImpl) Debugf(msg string, args ...any) {
	attrs := lg.buildAttrsWithCallAt(nil)
	lg.slogger.LogAttrs(context.TODO(), slog.LevelDebug, setDefaultMsg(fmt.Sprintf(msg, args...)), attrs...)
}

func (lg *loggerImpl) Debug(msg string, tags ...tag.Tag) {
	attrs := lg.buildAttrsWithCallAt(tags)
	lg.slogger.LogAttrs(context.TODO(), slog.LevelDebug, msg, attrs...)
}

func (lg *loggerImpl) Info(msg string, tags ...tag.Tag) {
	msg = setDefaultMsg(msg)
	attrs := lg.buildAttrsWithCallAt(tags)
	lg.slogger.LogAttrs(context.TODO(), slog.LevelInfo, msg, attrs...)
}

func (lg *loggerImpl) Warn(msg string, tags ...tag.Tag) {
	msg = setDefaultMsg(msg)
	attrs := lg.buildAttrsWithCallAt(tags)
	lg.slogger.LogAttrs(context.TODO(), slog.LevelWarn, msg, attrs...)
}

func (lg *loggerImpl) Error(msg string, tags ...tag.Tag) {
	msg = setDefaultMsg(msg)
	attrs := lg.buildAttrsWithCallAt(tags)
	lg.slogger.LogAttrs(context.TODO(), slog.LevelError, msg, attrs...)
}

func (lg *loggerImpl) Fatal(msg string, tags ...tag.Tag) {
	msg = setDefaultMsg(msg)
	attrs := lg.buildAttrsWithCallAt(tags)
	lg.slogger.LogAttrs(context.TODO(), slog.LevelError, msg, attrs...)
	os.Exit(1)
}

func (lg *loggerImpl) WithTags(tags ...tag.Tag) Logger {
	attrs := lg.buildAttrs(tags)
	args := make([]any, 0, len(attrs)*2)
	for _, attr := range attrs {
		args = append(args, attr.Key, attr.Value)
	}
	sloggerWithTags := lg.slogger.With(args...)
	return &loggerImpl{
		slogger: sloggerWithTags,
		skip:    lg.skip,
	}
}

func (lg *loggerImpl) GetSlogger() *slog.Logger {
	return lg.slogger
}

// newSLoggerFromConfig creates a new slog.Logger based on the provided configuration
func newSLoggerFromConfig(cfg *config.LogConfig) (*slog.Logger, error) {
	if cfg == nil {
		return nil, fmt.Errorf("logger config cannot be nil")
	}

	var writer io.Writer
	if cfg.OutputFile != "" && !cfg.Stdout {
		file, err := os.OpenFile(cfg.OutputFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file %s: %w", cfg.OutputFile, err)
		}
		writer = file
	} else if cfg.Stdout {
		writer = os.Stdout
	} else {
		writer = os.Stderr
	}

	opts := &slog.HandlerOptions{
		Level: mapLogLevel(cfg.Level),
	}

	opts.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.MessageKey {
			a.Key = "_msg"
		}
		if cfg.LevelKey != "" && a.Key == slog.LevelKey {
			a.Key = cfg.LevelKey
		}
		return a
	}

	var handler slog.Handler
	if cfg.LogJson {
		handler = slog.NewJSONHandler(writer, opts)
	} else {
		handler = slog.NewTextHandler(writer, opts)
	}

	logger := slog.New(handler)
	return logger, nil
}

func mapLogLevel(level config.LogLevel) slog.Level {
	switch level {
	case config.LogLevelDebug:
		return slog.LevelDebug
	case config.LogLevelInfo:
		return slog.LevelInfo
	case config.LogLevelWarn:
		return slog.LevelWarn
	case config.LogLevelError:
		return slog.LevelError
	case config.LogLevelFatal:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
