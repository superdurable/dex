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
	"go.uber.org/cadence"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/workflow"
)

func TestWorkflowProviderMapsWorkerAndTimeoutErrors(t *testing.T) {
	provider := &workflowProvider{}
	original := &dexpb.ErrorResponse{
		OriginalWorkerErrorDetail:     "worker detail",
		OriginalWorkerErrorType:       "worker type",
		OriginalWorkerErrorStackTrace: "worker stack",
	}
	workerFailure := cadence.NewCustomError(
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL.String(),
		original,
	)

	recoveryError, err := provider.MapToRecoveryError(workerFailure)
	require.NoError(t, err)
	require.Equal(t, "worker detail", recoveryError.GetDetail())
	require.Equal(t, "worker type", recoveryError.GetErrorType())

	localWorkerFailure := (&activityProvider{}).NewLocalActivityError(
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
		original,
		&dexpb.InternalLocalStepActivityFailure{Attempt: 2},
		0,
	)
	recoveryError, err = provider.MapToRecoveryError(localWorkerFailure)
	require.NoError(t, err)
	require.Equal(t, "worker detail", recoveryError.GetDetail())
	require.Equal(t, "worker type", recoveryError.GetErrorType())
	customError, decodedResponse, localFailure, isApplicationFailure := cadenceLocalStepActivityError(localWorkerFailure)
	require.True(t, isApplicationFailure)
	require.Equal(t, original, decodedResponse)
	require.Equal(t, int32(2), localFailure.GetAttempt())

	finalFailure := cadenceFinalFlowError(customError, decodedResponse)
	var finalCustomError *cadence.CustomError
	require.ErrorAs(t, finalFailure, &finalCustomError)
	var finalResponse *dexpb.ErrorResponse
	require.NoError(t, finalCustomError.Details(&finalResponse))
	require.Equal(t, original, finalResponse)
	var leakedFailure *dexpb.InternalLocalStepActivityFailure
	require.Error(t, finalCustomError.Details(&finalResponse, &leakedFailure))

	timeoutFailure := workflow.NewTimeoutError(shared.TimeoutTypeStartToClose)
	timeoutError, err := provider.MapToRecoveryError(timeoutFailure)
	require.NoError(t, err)
	require.Equal(t, shared.TimeoutTypeStartToClose.String(), timeoutError.GetErrorType())
	require.NotEmpty(t, timeoutError.GetDetail())
}
