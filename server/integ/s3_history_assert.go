// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package integ

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	uclient "github.com/superdurable/dex/service/client"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
)

// requireTemporalHistoryStoresBlobIdsNotPayloads asserts Temporal history embeds
// blob IDs and does not embed the given large payloads.
func requireTemporalHistoryStoresBlobIdsNotPayloads(
	t *testing.T,
	ctx context.Context,
	unifiedClient uclient.UnifiedClient,
	flowId string,
	blobIds []string,
	forbiddenPayloads []string,
) {
	t.Helper()

	api, ok := unifiedClient.GetApiService().(workflowservice.WorkflowServiceClient)
	if !ok {
		t.Skip("history blob-id assertion requires Temporal WorkflowServiceClient")
	}

	historyText := temporalHistoryText(t, ctx, api, flowId)
	for _, payload := range forbiddenPayloads {
		require.NotContains(t, historyText, payload, "history must not store large payload")
	}
	for _, blobId := range blobIds {
		require.NotEmpty(t, blobId)
		require.Contains(t, historyText, blobId, "history must store blob id")
	}
}

func temporalHistoryText(
	t *testing.T,
	ctx context.Context,
	api workflowservice.WorkflowServiceClient,
	flowId string,
) string {
	t.Helper()

	describe, err := api.DescribeWorkflowExecution(ctx, &workflowservice.DescribeWorkflowExecutionRequest{
		Namespace: testNamespace,
		Execution: &common.WorkflowExecution{
			WorkflowId: flowId,
		},
	})
	require.NoError(t, err)

	runId := describe.GetWorkflowExecutionInfo().GetFirstRunId()
	if runId == "" {
		runId = describe.GetWorkflowExecutionInfo().GetExecution().GetRunId()
	}

	var builder strings.Builder
	for runId != "" {
		nextRunId := ""
		var nextPageToken []byte
		for {
			eventHistory, err := api.GetWorkflowExecutionHistory(ctx, &workflowservice.GetWorkflowExecutionHistoryRequest{
				Namespace: testNamespace,
				Execution: &common.WorkflowExecution{
					WorkflowId: flowId,
					RunId:      runId,
				},
				NextPageToken: nextPageToken,
			})
			require.NoError(t, err)
			require.NotNil(t, eventHistory.GetHistory())
			for _, event := range eventHistory.GetHistory().GetEvents() {
				builder.WriteString(event.String())
				if event.GetEventType() == enums.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW {
					nextRunId = event.GetWorkflowExecutionContinuedAsNewEventAttributes().GetNewExecutionRunId()
				}
			}
			nextPageToken = eventHistory.GetNextPageToken()
			if len(nextPageToken) == 0 {
				break
			}
		}
		runId = nextRunId
	}
	return builder.String()
}
