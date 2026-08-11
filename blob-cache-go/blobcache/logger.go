// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package blobcache

import "log/slog"

// Logger receives structured cache logs.
type Logger interface {
	// Debug records diagnostic information.
	Debug(msg string, keyvals ...interface{})
	// Info records routine operational information.
	Info(msg string, keyvals ...interface{})
	// Warn records a recoverable problem.
	Warn(msg string, keyvals ...interface{})
	// Error records an operation failure.
	Error(msg string, keyvals ...interface{})
}

func defaultLogger(logger Logger) Logger {
	if logger == nil {
		return slog.Default()
	}
	return logger
}
