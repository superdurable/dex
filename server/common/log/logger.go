package log

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/common/utils/caller"
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

func NewLogger(cfg *config.LoggerConfig) (Logger, error) {
	slogger, err := NewSLoggerFromConfig(cfg)
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
	return MustNewLogger(&config.LoggerConfig{
		Level: level,
	})
}

func MustNewLogger(cfg *config.LoggerConfig) Logger {
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
	attrs = append(attrs, slog.String(tag.LoggingCallAtKey, caller.PathToCaller(lg.skip)))
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
