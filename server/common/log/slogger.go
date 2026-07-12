package log

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/superdurable/dex/server/config"
)

// NewSLoggerFromConfig creates a new slog.Logger based on the provided configuration
func NewSLoggerFromConfig(cfg *config.LoggerConfig) (*slog.Logger, error) {
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
