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
	errorResponse := &dexpb.ErrorResponse{OriginalWorkerErrorDetail: "worker detail"}
	err := (&activityProvider{}).NewFlowError(
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
		errorResponse,
		7,
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
		OriginalWorkerErrorDetail:     "worker detail",
		OriginalWorkerErrorType:       "worker type",
		OriginalWorkerErrorStackTrace: "worker stack",
	}
	workerFailure := temporalsdk.NewApplicationError(
		"",
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL.String(),
		*original,
	)

	recoveryError, err := provider.MapToRecoveryError(workerFailure)
	require.NoError(t, err)
	require.Equal(t, "worker detail", recoveryError.GetDetail())
	require.Equal(t, "worker type", recoveryError.GetErrorType())

	localWorkerFailure := (&activityProvider{}).NewLocalActivityError(
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
		original,
		&dexpb.InternalLocalStepActivityFailure{Attempt: 2},
		11,
	)
	recoveryError, err = provider.MapToRecoveryError(localWorkerFailure)
	require.NoError(t, err)
	require.Equal(t, "worker detail", recoveryError.GetDetail())
	require.Equal(t, "worker type", recoveryError.GetErrorType())
	var localApplicationError *temporalsdk.ApplicationError
	require.ErrorAs(t, localWorkerFailure, &localApplicationError)
	require.Equal(t, 11*time.Second, localApplicationError.NextRetryDelay())
	decodedApplicationError, decodedResponse, localFailure, isApplicationFailure := temporalLocalStepActivityError(localWorkerFailure)
	require.True(t, isApplicationFailure)
	require.Same(t, localApplicationError, decodedApplicationError)
	require.Equal(t, original, decodedResponse)
	require.Equal(t, int32(2), localFailure.GetAttempt())

	finalFailure := temporalFinalFlowError(decodedApplicationError, decodedResponse)
	var finalApplicationError *temporalsdk.ApplicationError
	require.ErrorAs(t, finalFailure, &finalApplicationError)
	var finalResponse *dexpb.ErrorResponse
	require.NoError(t, finalApplicationError.Details(&finalResponse))
	require.Equal(t, original, finalResponse)
	var leakedFailure *dexpb.InternalLocalStepActivityFailure
	require.Error(t, temporalApplicationErrorDetails(finalApplicationError, &finalResponse, &leakedFailure))

	timeoutFailure := temporalsdk.NewTimeoutError(enums.TIMEOUT_TYPE_START_TO_CLOSE, nil)
	timeoutError, err := provider.MapToRecoveryError(timeoutFailure)
	require.NoError(t, err)
	require.Equal(t, enums.TIMEOUT_TYPE_START_TO_CLOSE.String(), timeoutError.GetErrorType())
	require.NotEmpty(t, timeoutError.GetDetail())
}
