// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

var (
	channelFlowFirst  = dex.DefineChannel[int]("first")
	channelFlowSecond = dex.DefineChannel[int]("second")
)

type channelFlow struct {
	dex.FlowDefaults
}

func (channelFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(channelFlowFirstStep{}),
		dex.DefineStep(channelFlowSecondStep{}),
	}
}

func (channelFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{
		channelFlowFirst,
		channelFlowSecond,
	}}
}

type channelFlowFirstStep struct {
	dex.StepDefaults
}

func (channelFlowFirstStep) WaitFor(
	dex.Context,
	struct{},
) (*dex.Wait, error) {
	return dex.AnyOf(
		channelFlowFirst.ForOne(dex.WithConditionID("first")),
		channelFlowSecond.ForOne(),
	), nil
}

func (channelFlowFirstStep) Execute(
	ctx dex.Context,
	_ struct{},
) (*dex.StepDecision, error) {
	first, err := channelFlowFirst.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	second, err := channelFlowSecond.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(first) != 0 || len(second) != 1 || second[0] != 10 {
		return nil, fmt.Errorf(
			"unexpected first-step channel results: first=%v second=%v",
			first,
			second,
		)
	}
	return dex.GoTo(channelFlowSecondStep{}, struct{}{}), nil
}

type channelFlowSecondStep struct {
	dex.StepDefaults
}

func (channelFlowSecondStep) WaitFor(
	dex.Context,
	struct{},
) (*dex.Wait, error) {
	return dex.AnyComboOf(dex.Combo(
		channelFlowFirst.ForOne(dex.WithConditionID("first")),
		dex.Timer(24*time.Hour, dex.WithConditionID("finish-timer")),
	)), nil
}

func (channelFlowSecondStep) Execute(
	ctx dex.Context,
	_ struct{},
) (*dex.StepDecision, error) {
	if !ctx.HasTimerFired() || !ctx.HasTimerFiredByIndex(0) {
		return nil, fmt.Errorf("skipped timer was not reported as fired")
	}
	first, err := channelFlowFirst.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	second, err := channelFlowSecond.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(first) != 1 || first[0] != 100 || len(second) != 0 {
		return nil, fmt.Errorf(
			"unexpected second-step channel results: first=%v second=%v",
			first,
			second,
		)
	}
	return dex.GracefulComplete(first[0]), nil
}

func TestChannelFlow(t *testing.T) {
	runChannelFlow(t, channelFlowFirst, channelFlowSecond)
}

func TestChannelFlowWithErasedDefinitions(t *testing.T) {
	var first dex.ChannelDef = channelFlowFirst
	var second dex.ChannelDef = channelFlowSecond
	runChannelFlow(t, first, second)
}

func runChannelFlow(
	t *testing.T,
	first dex.ChannelDef,
	second dex.ChannelDef,
) {
	t.Helper()
	ctx := integrationContext(t)
	flowID := newFlowID(t, "channel")
	_, err := integClient.StartFlow(
		ctx,
		channelFlow{},
		flowID,
		struct{}{},
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	waitCtx, cancelWait := context.WithTimeout(ctx, time.Second)
	err = integClient.WaitForStepCompletion(
		waitCtx,
		flowID,
		dex.StepExecutionID{StepType: dex.GetFinalStepType(channelFlowFirstStep{})},
	)
	cancelWait()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, integClient.PublishToChannel(
		ctx,
		flowID,
		second,
		10,
	))
	waitCtx, cancelWait = context.WithTimeout(ctx, 20*time.Second)
	require.NoError(t, integClient.WaitForStepCompletion(
		waitCtx,
		flowID,
		dex.StepExecutionID{StepType: dex.GetFinalStepType(channelFlowFirstStep{})},
	))
	cancelWait()
	require.NoError(t, integClient.PublishToChannel(
		ctx,
		flowID,
		first,
		100,
	))
	require.Eventually(t, func() bool {
		err = integClient.SkipTimer(
			ctx,
			flowID,
			dex.StepExecutionID{StepType: dex.GetFinalStepType(channelFlowSecondStep{})},
			dex.TimerID{Index: ptr.Any(int32(0))},
		)
		return err == nil
	}, 20*time.Second, 100*time.Millisecond, "SkipTimer failed: %v", err)

	result := waitForFlow(t, flowID, true)
	require.Equal(t, dex.FlowCompleted, result.Status)
	require.Len(t, result.Completions, 1)
	var output int
	require.NoError(t, result.Completions[0].Output.Decode(&output))
	require.Equal(t, 100, output)

	err = integClient.PublishToChannel(
		ctx,
		newFlowID(t, "missing-channel-flow"),
		first,
		100,
	)
	var inactive *dex.FlowNotActiveError
	require.ErrorAs(t, err, &inactive)
}
