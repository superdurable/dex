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
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	pollingwithtimer "github.com/superdurable/dex/examples/go/patterns/polling-with-timer"
	"github.com/superdurable/dex/examples/go/registry"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

// pollingWithTimerMaxRounds mirrors the example's unexported round limit.
const pollingWithTimerMaxRounds = 60

func TestPollingWithTimerMovesToTheNextRoundWhileTheJobRuns(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "pattern-polling-with-timer")
	registry.PollingJobs.KeepJobRunning(flowID)
	_, err := integClient.StartFlow(ctx, registry.PollingWithTimer, flowID, nil, dex.StartFlowOptions{})
	require.NoError(t, err)

	skipPollIntervalTimer(t, flowID, 1)
	require.Eventually(t, func() bool {
		return registry.PollingJobs.CountStatusChecks(flowID) == 1
	}, workflowPatternWaitTimeout, workflowPatternPollInterval, "round 1 must check the running job")

	completeJobURL := examplesAPIURL + "/patterns/polling-with-timer/complete-job?" + url.Values{"workflowId": {flowID}}.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, completeJobURL, nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode)

	skipPollIntervalTimer(t, flowID, 2)
	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	require.Len(t, result.Completions, 1)
	var output string
	require.NoError(t, result.Completions[0].Output.Decode(&output))
	require.Equal(t, "artifact for "+flowID, output)
	require.Equal(t, 2, registry.PollingJobs.CountStatusChecks(flowID))
}

func TestPollingWithTimerFailsAfterTheMaximumRounds(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "pattern-polling-with-timer-rounds")
	registry.PollingJobs.KeepJobRunning(flowID)
	_, err := integClient.StartFlow(ctx, registry.PollingWithTimer, flowID, nil, dex.StartFlowOptions{})
	require.NoError(t, err)

	for round := 1; round <= pollingWithTimerMaxRounds; round++ {
		skipPollIntervalTimer(t, flowID, round)
	}
	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowFailed, result.Status, result.ErrorMessage)
	require.Contains(t, result.ErrorMessage, fmt.Sprintf("did not finish in %d rounds", pollingWithTimerMaxRounds))
	require.Equal(t, pollingWithTimerMaxRounds, registry.PollingJobs.CountStatusChecks(flowID))
}

func TestPollingWithTimerRetriesATransientStatusFailure(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "pattern-polling-with-timer-retry")
	registry.PollingJobs.FailNextStatusChecks(flowID, 1)
	_, err := integClient.StartFlow(ctx, registry.PollingWithTimer, flowID, nil, dex.StartFlowOptions{})
	require.NoError(t, err)
	completeJobWhenStarted(t, flowID)

	skipPollIntervalTimer(t, flowID, 1)
	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	require.Equal(t, 2, registry.PollingJobs.CountStatusChecks(flowID))
}

func TestPollingWithTimerRecordsTheFailureWhenRetriesRunOut(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "pattern-polling-with-timer-recovery")
	registry.PollingJobs.FailNextStatusChecks(flowID, 3)
	_, err := integClient.StartFlow(ctx, registry.PollingWithTimer, flowID, nil, dex.StartFlowOptions{})
	require.NoError(t, err)
	completeJobWhenStarted(t, flowID)

	skipPollIntervalTimer(t, flowID, 1)
	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowFailed, result.Status, result.ErrorMessage)
	require.Contains(t, result.ErrorMessage, "checking job "+flowID+" failed")
	require.Equal(t, 3, registry.PollingJobs.CountStatusChecks(flowID))
}

func completeJobWhenStarted(t *testing.T, flowID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return registry.PollingJobs.CompleteJob(flowID) == nil
	}, workflowPatternWaitTimeout, workflowPatternPollInterval, "StartJob must start the job")
}

func skipPollIntervalTimer(t *testing.T, flowID string, round int) {
	t.Helper()
	require.Eventually(t, func() bool {
		return integClient.SkipTimer(
			integrationContext(t),
			flowID,
			dex.StepExecutionID{StepType: "CheckJobStatus", ExecutionNumber: ptr.Any(int32(round))},
			dex.TimerID{ConditionID: pollingwithtimer.PollIntervalTimerID},
		) == nil
	}, workflowPatternWaitTimeout, workflowPatternPollInterval, "the interval Timer of round %d must be skipped", round)
}
