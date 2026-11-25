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
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

// At the default batch size the backfill needs 100 seconds, longer than the wait timeout.
func TestControlLongRunningWorkReadsTheBatchSizeEachBatch(t *testing.T) {
	flow := registry.RecordBackfill
	flowID := startWorkflowPattern(t, flow, "pattern-control-long-running-work-batch", nil)

	for _, invalidSize := range []int{0, 1001} {
		require.ErrorContains(
			t,
			integClient.InvokeRPC(integrationContext(t), flowID, flow.SetBatchSize, invalidSize, nil),
			"batch size must be between 1 and 1000",
		)
	}
	// The first batch has used the default size, so a later batch must read the new one.
	waitForFirstRecordBatch(t, flowID)
	invokeWorkflowPatternRPC(t, flowID, flow.SetBatchSize, 1000, nil)

	result := waitForCompletedWorkflowPattern(t, flowID)
	require.Equal(t, []string{"processed 1000 records"}, decodeStepOutputs[string](t, result, "ProcessRecordBatch"))
}

func TestControlLongRunningWorkStopsBeforeTheNextBatch(t *testing.T) {
	flow := registry.RecordBackfill
	flowID := startWorkflowPattern(t, flow, "pattern-control-long-running-work-stop", nil)

	waitForFirstRecordBatch(t, flowID)
	invokeWorkflowPatternRPC(t, flowID, flow.RequestStop, nil, nil)

	result := waitForCompletedWorkflowPattern(t, flowID)
	outputs := decodeStepOutputs[string](t, result, "ProcessRecordBatch")
	require.Len(t, outputs, 1)
	stoppedBefore, found := strings.CutPrefix(outputs[0], "stopped before record ")
	require.True(t, found, outputs[0])
	record, err := strconv.Atoi(stoppedBefore)
	require.NoError(t, err)
	require.GreaterOrEqual(t, record, 11)
}

func waitForFirstRecordBatch(t *testing.T, flowID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), workflowPatternWaitTimeout)
	defer cancel()
	require.NoError(t, integClient.WaitForStepCompletion(
		ctx,
		flowID,
		dex.StepExecutionID{StepType: "ProcessRecordBatch"},
		dex.WaitForStepCompletionOptions{},
	))
}
