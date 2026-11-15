// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

var (
	cloneForkLabel   = dex.DefineAttribute[string]("clone-fork-label")
	cloneForkRelease = dex.DefineChannel[string]("clone-fork-release")
)

type cloneForkSessionFlow struct {
	dex.FlowDefaults
}

func (cloneForkSessionFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(cloneForkWaitStep{})}
}

func (flow cloneForkSessionFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.ReleaseSession, nil)}
}

func (cloneForkSessionFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{cloneForkLabel},
		Channels:   []dex.ChannelDef{cloneForkRelease},
	}
}

func (cloneForkSessionFlow) ReleaseSession(ctx dex.Context, answer string) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, cloneForkRelease.Publish(ctx, answer)
}

type cloneForkWaitStep struct {
	dex.StepDefaults
}

func (cloneForkWaitStep) WaitFor(ctx dex.Context, label string) (*dex.Wait, error) {
	if err := cloneForkLabel.Set(ctx, label); err != nil {
		return nil, err
	}
	return dex.Until(cloneForkRelease.ForOne()), nil
}

func (cloneForkWaitStep) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
	answers, err := cloneForkRelease.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	label, err := cloneForkLabel.Get(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(label + "|" + answers[0]), nil
}

func TestCloneForkFlowCopiesRunningSourceAtCurrentAndEventIDPoints(t *testing.T) {
	flowService := newFlowServiceClient(t)
	sourceID := newFlowID(t, "clone-fork-source")
	sourceRunID, err := integClient.StartFlow(
		integrationContext(t), cloneForkSessionFlow{}, sourceID, "source", dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	waitEventID := awaitWaitForCompletedEventID(t, flowService, sourceID, sourceRunID)

	currentCloneID := newFlowID(t, "clone-fork-current")
	relabel, err := dex.InitialAttribute(cloneForkLabel, "current")
	require.NoError(t, err)
	currentClone, err := integClient.CloneForkFlow(
		integrationContext(t), cloneForkSessionFlow{}, sourceID, currentCloneID, dex.CloneForkFlowOptions{
			Attributes: []dex.InitialAttributeDef{relabel},
			Reason:     "branch at the current point",
		},
	)
	require.NoError(t, err)
	require.NotEmpty(t, currentClone.RunID)
	require.Equal(t, sourceRunID, currentClone.SourceRunID)
	require.Equal(t, int32(1), currentClone.SourceRunAttempt)
	require.GreaterOrEqual(t, currentClone.SourceEventID, waitEventID)

	eventCloneID := newFlowID(t, "clone-fork-event")
	eventClone, err := integClient.CloneForkFlow(
		integrationContext(t), cloneForkSessionFlow{}, sourceID, eventCloneID, dex.CloneForkFlowOptions{
			SourceRunID: sourceRunID,
			Type:        dex.TimeTravelByEventID,
			EventID:     waitEventID,
		},
	)
	require.NoError(t, err)
	require.Equal(t, sourceRunID, eventClone.SourceRunID)
	require.Equal(t, waitEventID, eventClone.SourceEventID)

	require.NoError(t, integClient.InvokeRPC(
		integrationContext(t), currentCloneID, cloneForkSessionFlow{}.ReleaseSession, "a", nil,
	))
	require.NoError(t, integClient.InvokeRPC(
		integrationContext(t), eventCloneID, cloneForkSessionFlow{}.ReleaseSession, "b", nil,
	))
	var currentOutput string
	require.NoError(t, waitForFlow(t, currentCloneID, true).DecodeSingleOutput(&currentOutput))
	require.Equal(t, "current|a", currentOutput)
	var eventOutput string
	require.NoError(t, waitForFlow(t, eventCloneID, true).DecodeSingleOutput(&eventOutput))
	require.Equal(t, "source|b", eventOutput)

	source, err := integClient.DescribeFlow(integrationContext(t), sourceID)
	require.NoError(t, err)
	require.Equal(t, dex.FlowRunning, source.Status)
	require.Equal(t, sourceRunID, source.RunID)
	require.NoError(t, integClient.StopFlow(integrationContext(t), sourceID, dex.StopOptions{Type: dex.TerminateFlow}))
}

func TestTimeTravelByEventIDReturnsToTheWaitingPoint(t *testing.T) {
	flowService := newFlowServiceClient(t)
	flowID := newFlowID(t, "time-travel-event-id")
	runID, err := integClient.StartFlow(
		integrationContext(t), cloneForkSessionFlow{}, flowID, "first", dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	waitEventID := awaitWaitForCompletedEventID(t, flowService, flowID, runID)
	require.NoError(t, integClient.InvokeRPC(
		integrationContext(t), flowID, cloneForkSessionFlow{}.ReleaseSession, "a", nil,
	))
	waitForFlow(t, flowID, false)

	_, err = integClient.TimeTravel(integrationContext(t), flowID, dex.TimeTravelOptions{
		Type:               dex.TimeTravelByEventID,
		EventID:            waitEventID,
		SkipWritesReapply:  true,
		SubFlowResetPolicy: dex.SubFlowResetAttachElseReject,
		Reason:             "answer again",
	})
	require.NoError(t, err)
	require.NoError(t, integClient.InvokeRPC(
		integrationContext(t), flowID, cloneForkSessionFlow{}.ReleaseSession, "b", nil,
	))
	var output string
	require.NoError(t, waitForFlow(t, flowID, true).DecodeSingleOutput(&output))
	require.Equal(t, "first|b", output)
}

func awaitWaitForCompletedEventID(
	t *testing.T,
	flowService dexpb.FlowServiceClient,
	flowID string,
	runID string,
) int64 {
	t.Helper()
	var eventID int64
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
				eventID = event.GetEventId()
				return true
			}
		}
		return false
	}, 30*time.Second, 20*time.Millisecond)
	require.NoError(t, historyErr)
	return eventID
}
