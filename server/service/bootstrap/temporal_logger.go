// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package bootstrap

import (
	"fmt"

	temporallog "go.temporal.io/sdk/log"
	"go.uber.org/zap"
)

type temporalLogger struct {
	zapLogger *zap.Logger
}

var _ temporallog.Logger = (*temporalLogger)(nil)
var _ temporallog.WithLogger = (*temporalLogger)(nil)

func newTemporalLogger(zapLogger *zap.Logger) *temporalLogger {
	if zapLogger == nil {
		panic("Temporal logger requires a Zap logger")
	}
	return &temporalLogger{zapLogger: zapLogger}
}

func (l *temporalLogger) Debug(message string, keyvals ...interface{}) {
	l.zapLogger.Debug(message, temporalLogFields(keyvals)...)
}

func (l *temporalLogger) Info(message string, keyvals ...interface{}) {
	l.zapLogger.Info(message, temporalLogFields(keyvals)...)
}

func (l *temporalLogger) Warn(message string, keyvals ...interface{}) {
	l.zapLogger.Warn(message, temporalLogFields(keyvals)...)
}

func (l *temporalLogger) Error(message string, keyvals ...interface{}) {
	l.zapLogger.Error(message, temporalLogFields(keyvals)...)
}

func (l *temporalLogger) With(keyvals ...interface{}) temporallog.Logger {
	return newTemporalLogger(l.zapLogger.With(temporalLogFields(keyvals)...))
}

func temporalLogFields(keyvals []interface{}) []zap.Field {
	fields := make([]zap.Field, 0, (len(keyvals)+1)/2)
	for index := 0; index+1 < len(keyvals); index += 2 {
		fields = append(fields, zap.Any(fmt.Sprint(keyvals[index]), keyvals[index+1]))
	}
	if len(keyvals)%2 == 1 {
		fields = append(fields, zap.Any(fmt.Sprint(keyvals[len(keyvals)-1]), nil))
	}
	return fields
}
