// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

type subFlowAbandonedParentFlow struct{ emptyFlowSchema }

func (subFlowAbandonedParentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(subFlowAbandonedParentStep{})}
}

type subFlowAbandonedParentStep struct{ dex.StepDefaults }

func (subFlowAbandonedParentStep) WaitFor(_ dex.Context, seconds int) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(subFlowTimerFlow{}, seconds)), nil
}

func (subFlowAbandonedParentStep) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	result, err := dex.SubFlowResult(ctx)
	if err != nil {
		return nil, err
	}
	isAbandoned := result.Status == dex.FlowFailed &&
		result.ErrorType == dex.FlowErrorSubFlowAbandoned
	if !isAbandoned {
		return dex.GracefulComplete("unexpected|" + result.Status.String()), nil
	}
	return dex.GracefulComplete("abandoned|" + result.Status.String()), nil
}

func TestSubFlowAlwaysFailResetDeliversAbandonedChildResult(t *testing.T) {
	flowService := newFlowServiceClient(t)
	flowID := newFlowID(t, "sub-flow-abandoned")
	childID := subFlowID(flowID, subFlowAbandonedParentStep{}, 0)
	runID, err := integClient.StartFlow(
		integrationContext(t), subFlowAbandonedParentFlow{}, flowID, 3600, dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	awaitSubFlowRunID(t, childID, "")
	waitTime := awaitWaitForCompletedTime(t, flowService, flowID, runID)

	_, err = integClient.TimeTravel(integrationContext(t), flowID, dex.TimeTravelOptions{
		Type:               dex.TimeTravelByHistoryEventTime,
		HistoryEventTime:   waitTime,
		Reason:             "verify SubFlow ALWAYS_FAIL reset",
		SubFlowResetPolicy: dex.SubFlowResetAlwaysFail,
	})
	require.NoError(t, err)

	var output string
	require.NoError(t, waitForFlow(t, flowID, true).DecodeSingleOutput(&output))
	require.Equal(t, []string{"abandoned", dex.FlowFailed.String()}, strings.Split(output, "|"))
	require.Equal(t, dex.FlowTerminated, waitForUncompletedFlow(t, childID, false).Status)
}

func awaitWaitForCompletedTime(
	t *testing.T,
	flowService dexpb.FlowServiceClient,
	flowID string,
	runID string,
) time.Time {
	t.Helper()
	var waitTime time.Time
	var historyErr error
	require.Eventually(t, func() bool {
		var response *dexpb.GetHistoryEventsResponse
		response, historyErr = flowService.GetHistoryEvents(integrationContext(t), &dexpb.GetHistoryEventsRequest{
			FlowId: flowID, RunId: runID, EstimatePageSize: 100,
		})
		if historyErr != nil {
			return false
		}
		for _, event := range response.Events {
			if event.GetStepWaitForCompleted() != nil {
				waitTime = event.EventTime.AsTime()
				return true
			}
		}
		return false
	}, 30*time.Second, 20*time.Millisecond)
	require.NoError(t, historyErr)
	return waitTime
}
