// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package cadence

import (
	"context"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/interpreter/interfaces"
	"go.uber.org/cadence"
	"go.uber.org/cadence/activity"
)

type activityProvider struct{}

func (a *activityProvider) GetLogger(ctx context.Context) interfaces.UnifiedLogger {
	zLogger := activity.GetLogger(ctx)
	return &loggerImpl{
		zlogger: zLogger,
	}
}

func (a *activityProvider) NewFlowError(
	errType dexpb.FlowErrorType,
	errorResponse *dexpb.ErrorResponse,
	_ int32,
) error {
	return cadence.NewCustomError(errType.String(), errorResponse)
}

func (a *activityProvider) NewLocalActivityError(
	errType dexpb.FlowErrorType,
	errorResponse *dexpb.ErrorResponse,
	failure *dexpb.InternalLocalStepActivityFailure,
	_ int32,
) error {
	return cadence.NewCustomError(errType.String(), errorResponse, failure)
}

func (a *activityProvider) GetActivityInfo(ctx context.Context) interfaces.ActivityInfo {
	info := activity.GetInfo(ctx)
	// Cadence LocalActivity leaves ScheduledTimestamp unset (zero).
	scheduled := info.ScheduledTimestamp
	if scheduled.IsZero() {
		scheduled = time.Now()
	}
	return interfaces.ActivityInfo{
		ScheduledTime:   scheduled,
		ActivityID:      info.ActivityID,
		Attempt:         info.Attempt + 1, // Cadence attempts are 0-based; Temporal is 1-based.
		IsLocalActivity: len(info.TaskToken) == 0,
		WorkflowExecution: interfaces.WorkflowExecution{
			ID:    info.WorkflowExecution.ID,
			RunID: info.WorkflowExecution.RunID,
		},
	}
}

func (a *activityProvider) RecordHeartbeat(ctx context.Context, details ...interface{}) {
	activity.RecordHeartbeat(ctx, details...)
}
