// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package logging

import "log/slog"

// Logger receives structured SDK logs.
//
// msg is the human-readable event and keyvals contains alternating string keys and
// values. Implementations should be safe for concurrent Client and Worker calls.
type Logger interface {
	// Debug records diagnostic detail normally disabled in production.
	Debug(msg string, keyvals ...interface{})
	// Info records routine SDK lifecycle and request information.
	Info(msg string, keyvals ...interface{})
	// Warn records a recoverable condition that may require operator attention.
	Warn(msg string, keyvals ...interface{})
	// Error records an SDK operation failure and its structured context.
	Error(msg string, keyvals ...interface{})
}

// OrDefault returns logger or slog.Default when logger is nil.
func OrDefault(logger Logger) Logger {
	if logger == nil {
		return slog.Default()
	}
	return logger
}
