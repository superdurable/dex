// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

var gracefulTestMessages = DefineChannel[string]("graceful-test-messages")

func TestGracefulCompletionConditionMapping(t *testing.T) {
	mapped, err := mapWait(AnyOf(gracefulTestMessages.ForOne(), GracefulCompletionRequested()))
	require.NoError(t, err)
	require.Len(t, mapped.GetChannelConditions(), 1)
	require.Len(t, mapped.GetGracefulCompletionConditions(), 1)
	require.Empty(t, mapped.GetGracefulCompletionConditions()[0].GetConditionId())

	graceful := GracefulCompletionRequested(WithConditionID("graceful"))
	mapped, err = mapWait(AnyComboOf(
		Combo(Timer(time.Minute, WithConditionID("deadline")), graceful),
		Combo(graceful),
	))
	require.NoError(t, err)
	require.Len(t, mapped.GetGracefulCompletionConditions(), 1)
	require.Equal(t, "graceful", mapped.GetGracefulCompletionConditions()[0].GetConditionId())
	require.Equal(t, []string{"deadline", "graceful"}, mapped.GetConditionCombinations()[0].GetConditionIds())
	require.Equal(t, []string{"graceful"}, mapped.GetConditionCombinations()[1].GetConditionIds())

	_, err = mapWait(AnyOf(GracefulCompletionRequested(), GracefulCompletionRequested()))
	require.ErrorContains(t, err, "at most one graceful completion condition")

	_, err = mapWait(AnyComboOf(Combo(GracefulCompletionRequested())))
	require.ErrorContains(t, err, "every condition")
}

func TestWorkerGracefulCompletionConditionNeedsProtocolSeven(t *testing.T) {
	waitFor := func(t *testing.T, serverProtocolVersion uint32) ([]*dexpb.InvokeWaitForMethodOutput, error) {
		t.Helper()
		client, closeService := newWorkerClientForServerProtocol(
			t, []Flow{gracefulConditionTestFlow{}}, nil, serverProtocolVersion,
		)
		defer closeService()
		return collectWaitForOutputs(client, &dexpb.InvokeWaitForMethodRequest{
			Context:   workerStepContext(),
			FlowType:  GetFinalFlowType(gracefulConditionTestFlow{}),
			StepType:  GetFinalStepType(gracefulConditionTestStep{}),
			StepInput: mustEncodeWorkerTestValue(t, nil),
		})
	}

	outputs, err := waitFor(t, 7)
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Len(t, outputs[0].GetResult().GetWaitingCondition().GetGracefulCompletionConditions(), 1)

	_, err = waitFor(t, 6)
	require.ErrorContains(t, err, "requires Server protocol 7 or later")
}

func TestExecuteReadsTheGracefulCompletionConditionResult(t *testing.T) {
	execute := func(t *testing.T, status dexpb.ConditionStatus) *dexpb.StepDecision {
		t.Helper()
		client, closeService := newWorkerClientForServerProtocol(
			t, []Flow{gracefulConditionTestFlow{}}, nil, maximumSupportedServerProtocolVersion,
		)
		defer closeService()
		outputs, err := collectExecuteOutputs(client, &dexpb.InvokeExecuteMethodRequest{
			Context:   workerStepContext(),
			FlowType:  GetFinalFlowType(gracefulConditionTestFlow{}),
			StepType:  GetFinalStepType(gracefulConditionTestStep{}),
			StepInput: mustEncodeWorkerTestValue(t, nil),
			ConditionResults: &dexpb.ConditionResults{
				ChannelResults: []*dexpb.ChannelResult{{
					ChannelName:     "graceful-test-messages",
					ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_WAITING,
				}},
				GracefulCompletionResults: []*dexpb.GracefulCompletionResult{{
					ConditionStatus: status,
				}},
			},
		})
		require.NoError(t, err)
		require.Len(t, outputs, 1)
		return outputs[0].GetResult().GetStepDecision()
	}

	decision := execute(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED)
	require.Equal(
		t,
		dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_DEAD_END,
		decision.GetCloseDecision().GetCloseDecisionType(),
	)

	decision = execute(t, dexpb.ConditionStatus_CONDITION_STATUS_WAITING)
	require.Equal(
		t,
		dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL,
		decision.GetCloseDecision().GetCloseDecisionType(),
	)

	require.False(t, IsGracefulCompletionConditionCompleted(nil))
}

type gracefulConditionTestFlow struct {
	FlowDefaults
}

func (gracefulConditionTestFlow) GetSteps() []StepDef {
	return []StepDef{DefineStartStep(gracefulConditionTestStep{})}
}

func (gracefulConditionTestFlow) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{Channels: []ChannelDef{gracefulTestMessages}}
}

type gracefulConditionTestStep struct {
	StepDefaults
}

func (gracefulConditionTestStep) WaitFor(Context, None) (*Wait, error) {
	return AnyOf(gracefulTestMessages.ForOne(), GracefulCompletionRequested()), nil
}

func (gracefulConditionTestStep) Execute(ctx Context, _ None) (*StepDecision, error) {
	if IsGracefulCompletionConditionCompleted(ctx) {
		return DeadEnd(), nil
	}
	return ForceFail("the graceful completion condition did not complete"), nil
}
