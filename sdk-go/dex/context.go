// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"context"
	"time"
)

type Context interface {
	context.Context

	FlowID() string
	RunID() string
	FlowStartedAt() time.Time
	StepExecutionID() string
	FromStepExecutionID() string
	FirstAttemptAt() time.Time
	Attempt() int32

	HasTimerFired() bool
	HasTimerFiredByIndex(index int) bool
	WaitForMethodFailed() bool

	SetStepExecutionLocal(key string, value any) error
	GetStepExecutionLocal(key string, valuePtr any) (found bool, err error)
	RecordEvent(name string, value any) error
}
