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
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	pollingwithtimer "github.com/superdurable/dex/examples/go/patterns/polling-with-timer"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestPollingWithTimerChecksTheJobWhenTheIntervalTimerFires(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "pattern-polling-with-timer")
	_, err := integClient.StartFlow(ctx, registry.PollingWithTimer, flowID, nil, dex.StartFlowOptions{})
	require.NoError(t, err)

	completeJobURL := examplesAPIURL + "/patterns/polling-with-timer/complete-job?" + url.Values{"workflowId": {flowID}}.Encode()
	require.Eventually(t, func() bool {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, completeJobURL, nil)
		require.NoError(t, err)
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		return response.StatusCode == http.StatusOK
	}, workflowPatternWaitTimeout, workflowPatternPollInterval, "StartJob must start the job")

	firstRound := int32(1)
	require.Eventually(t, func() bool {
		return integClient.SkipTimer(
			ctx,
			flowID,
			dex.StepExecutionID{StepType: "CheckJobStatus", ExecutionNumber: &firstRound},
			dex.TimerID{ConditionID: pollingwithtimer.PollIntervalTimerID},
		) == nil
	}, workflowPatternWaitTimeout, workflowPatternPollInterval, "the first interval Timer must be skipped")

	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowCompleted, result.Status)
	require.Len(t, result.Completions, 1)
	var output string
	require.NoError(t, result.Completions[0].Output.Decode(&output))
	require.Equal(t, "artifact for "+flowID, output)
}
