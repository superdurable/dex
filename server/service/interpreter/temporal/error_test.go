// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package temporal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"go.temporal.io/api/enums/v1"
	temporalsdk "go.temporal.io/sdk/temporal"
)

func TestActivityProviderAppliesNextRetryDelay(t *testing.T) {
	errorResponse := &dexpb.ErrorResponse{OriginalWorkerRetryAfterSeconds: 7}
	err := (&activityProvider{}).NewFlowError(
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
		errorResponse,
	)

	var applicationError *temporalsdk.ApplicationError
	require.ErrorAs(t, err, &applicationError)
	require.Equal(t, 7*time.Second, applicationError.NextRetryDelay())
	var decoded *dexpb.ErrorResponse
	require.NoError(t, applicationError.Details(&decoded))
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
	workerFailure := temporalsdk.NewApplicationError(
		"",
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL.String(),
		*original,
	)

	workerError, err := provider.MapToWorkerError(workerFailure)
	require.NoError(t, err)
	require.Equal(t, "worker detail", workerError.GetDetail())
	require.Equal(t, "worker type", workerError.GetErrorType())
	require.Equal(t, "worker stack", workerError.GetStackTrace())
	require.Equal(t, int32(11), workerError.GetRetryAfterSeconds())

	localWorkerFailure := (&activityProvider{}).NewLocalActivityError(
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
		&dexpb.InternalLocalStepActivityError{
			ErrorResponse: original,
			Failure:       &dexpb.InternalLocalStepActivityFailure{Attempt: 2},
		},
	)
	workerError, err = provider.MapToWorkerError(localWorkerFailure)
	require.NoError(t, err)
	require.Equal(t, "worker detail", workerError.GetDetail())
	require.Equal(t, "worker type", workerError.GetErrorType())
	require.Equal(t, "worker stack", workerError.GetStackTrace())
	require.Equal(t, int32(11), workerError.GetRetryAfterSeconds())
	var localApplicationError *temporalsdk.ApplicationError
	require.ErrorAs(t, localWorkerFailure, &localApplicationError)
	require.Equal(t, 11*time.Second, localApplicationError.NextRetryDelay())
	decodedApplicationError, localError, isApplicationFailure := temporalLocalStepActivityError(localWorkerFailure)
	require.True(t, isApplicationFailure)
	require.Same(t, localApplicationError, decodedApplicationError)
	require.Equal(t, int32(2), localError.GetFailure().GetAttempt())

	finalFailure := temporalFinalFlowError(decodedApplicationError, localError.GetErrorResponse())
	var finalApplicationError *temporalsdk.ApplicationError
	require.ErrorAs(t, finalFailure, &finalApplicationError)
	var finalResponse *dexpb.ErrorResponse
	require.NoError(t, finalApplicationError.Details(&finalResponse))
	require.Equal(t, original, finalResponse)
	var leakedLocalError *dexpb.InternalLocalStepActivityError
	require.Error(t, temporalApplicationErrorDetails(finalApplicationError, &leakedLocalError))

	timeoutFailure := temporalsdk.NewTimeoutError(enums.TIMEOUT_TYPE_START_TO_CLOSE, nil)
	timeoutError, err := provider.MapToWorkerError(timeoutFailure)
	require.NoError(t, err)
	require.Equal(t, enums.TIMEOUT_TYPE_START_TO_CLOSE.String(), timeoutError.GetErrorType())
	require.NotEmpty(t, timeoutError.GetDetail())
}
