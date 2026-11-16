// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

func TestSubFlowResultMapsAbandonedAndUnrecognizedErrorTypes(t *testing.T) {
	conditionResults := &dexpb.ConditionResults{SubFlowResults: []*dexpb.FlowResult{
		{
			FlowStatus:   dexpb.FlowStatus_FLOW_STATUS_FAILED,
			ErrorType:    dexpb.FlowErrorType_FLOW_ERROR_TYPE_SUB_FLOW_ABANDONED,
			ErrorMessage: "abandoned by a Time Travel",
		},
		{
			FlowStatus: dexpb.FlowStatus_FLOW_STATUS_FAILED,
			ErrorType:  dexpb.FlowErrorType(99),
		},
	}}
	invocation, err := newInvocationContext(
		context.Background(), invocationExecute, nil, nil, workerStepContext(),
		nil, nil, conditionResults, nil, nil, nil, nil, nil, &negotiatedServerProtocol{},
	)
	require.NoError(t, err)

	abandoned, err := SubFlowResult(invocation, 0)
	require.NoError(t, err)
	require.Equal(t, FlowFailed, abandoned.Status)
	require.Equal(t, FlowErrorSubFlowAbandoned, abandoned.ErrorType)
	require.Equal(t, "abandoned by a Time Travel", abandoned.ErrorMessage)

	unrecognized, err := SubFlowResult(invocation, 1)
	require.NoError(t, err)
	require.Equal(t, FlowFailed, unrecognized.Status)
	require.Equal(t, FlowErrorType(0), unrecognized.ErrorType)
}
