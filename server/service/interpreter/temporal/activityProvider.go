// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package temporal

import (
	"context"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/interpreter/interfaces"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

type activityProvider struct{}

func (a *activityProvider) GetLogger(ctx context.Context) interfaces.UnifiedLogger {
	return activity.GetLogger(ctx)
}

func (a *activityProvider) NewFlowError(
	errType dexpb.FlowErrorType,
	errorResponse *dexpb.ErrorResponse,
) error {
	return temporal.NewApplicationError("", errType.String(), errorResponse)
}

func (a *activityProvider) GetActivityInfo(ctx context.Context) interfaces.ActivityInfo {
	info := activity.GetInfo(ctx)
	return interfaces.ActivityInfo{
		ScheduledTime:   info.ScheduledTime,
		ActivityID:      info.ActivityID,
		Attempt:         info.Attempt,
		IsLocalActivity: info.IsLocalActivity,
		WorkflowExecution: interfaces.WorkflowExecution{
			ID:    info.WorkflowExecution.ID,
			RunID: info.WorkflowExecution.RunID,
		},
	}
}

func (a *activityProvider) RecordHeartbeat(ctx context.Context, details ...interface{}) {
	activity.RecordHeartbeat(ctx, details...)
}
