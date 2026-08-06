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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/examples/go/workflows"
	"github.com/superdurable/dex/examples/go/workflows/engagement"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestEngagementStartChannelRPCAndSearch(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "engagement")
	input := engagement.EngagementInput{
		EmployerID:  "employer-ci",
		JobSeekerID: "job-seeker-ci",
		Notes:       "created",
	}
	runID, err := integClient.StartFlow(
		ctx,
		workflows.Engagement,
		flowID,
		input,
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	require.NotEmpty(t, runID)
	require.NoError(t, integClient.WaitForAttributeEqual(
		ctx,
		flowID,
		engagement.EngagementStatus,
		engagement.StatusInitiated,
		dex.WaitOptions{Timeout: 20 * time.Second},
	))

	var description engagement.EngagementDescription
	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		workflows.Engagement.Describe,
		nil,
		&description,
		dex.InvokeOptions{},
	))
	require.Equal(t, engagement.StatusInitiated, description.CurrentStatus)
	require.NoError(t, integClient.PublishToChannel(
		ctx,
		flowID,
		engagement.OptOutReminder,
		nil,
	))

	var status engagement.Status
	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		workflows.Engagement.Decline,
		"declined in integration test",
		&status,
		dex.InvokeOptions{},
	))
	require.Equal(t, engagement.StatusDeclined, status)
	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		workflows.Engagement.Accept,
		"accepted in integration test",
		&status,
		dex.InvokeOptions{},
	))
	require.Equal(t, engagement.StatusAccepted, status)

	var searchPage dex.SearchFlowsPage
	require.Eventually(t, func() bool {
		searchPage, err = integClient.SearchFlows(
			ctx,
			dex.SearchQuery{Query: engagement.StatusSearchKey + " = 'Accepted'"},
			100,
			"",
		)
		if err != nil {
			return false
		}
		for _, entry := range searchPage.Flows {
			if entry.FlowID == flowID {
				return true
			}
		}
		return false
	}, 20*time.Second, 200*time.Millisecond, "SearchFlows failed: %v", err)

	result := waitForFlow(t, flowID)
	require.Equal(t, dex.FlowCompleted, result.Status)
	require.Len(t, result.Completions, 1)
	var output string
	require.NoError(t, result.Completions[0].Output.Decode(&output))
	require.Equal(t, "done", output)
}
