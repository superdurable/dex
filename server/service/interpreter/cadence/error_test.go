// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package cadence

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/retry"
	"go.uber.org/cadence"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/workflow"
)

func TestActivityProviderRejectsRetryAfter(t *testing.T) {
	errorResponse := &dexpb.ErrorResponse{OriginalWorkerRetryAfterSeconds: 7}
	err := (&activityProvider{}).NewFlowError(
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
		errorResponse,
	)

	var customError *cadence.CustomError
	require.ErrorAs(t, err, &customError)
	require.Equal(t, retry.CadenceRetryAfterErrorReason, customError.Reason())
	var decoded *dexpb.ErrorResponse
	require.NoError(t, customError.Details(&decoded))
	require.Equal(t, errorResponse, decoded)
}

func TestWorkflowProviderMapsWorkerAndTimeoutErrors(t *testing.T) {
	provider := &workflowProvider{}
	original := &dexpb.ErrorResponse{
		OriginalWorkerErrorDetail:       "worker detail",
		OriginalWorkerErrorType:         "worker type",
		OriginalWorkerErrorStackTrace:   "worker stack",
		OriginalWorkerRetryAfterSeconds: 11,
	}
	workerFailure := cadence.NewCustomError(
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL.String(),
		original,
	)

	workerError, err := provider.WorkerError(workerFailure)
	require.NoError(t, err)
	require.Equal(t, "worker detail", workerError.GetDetail())
	require.Equal(t, "worker type", workerError.GetErrorType())
	require.Equal(t, "worker stack", workerError.GetStackTrace())
	require.Equal(t, int32(11), workerError.GetRetryAfterSeconds())

	timeoutFailure := workflow.NewTimeoutError(shared.TimeoutTypeStartToClose)
	timeoutError, err := provider.WorkerError(timeoutFailure)
	require.NoError(t, err)
	require.Equal(t, shared.TimeoutTypeStartToClose.String(), timeoutError.GetErrorType())
	require.NotEmpty(t, timeoutError.GetDetail())
}
