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
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	interStepFirstChannel  = dex.DefineChannel[int]("inter-step-first")
	interStepSecondChannel = dex.DefineChannel[int]("inter-step-second")
)

type interStepFlow struct{}

func (interStepFlow) GetFlowType() string {
	return "go-sdk-inter-step"
}

func (interStepFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(interStepStartStep{}),
		dex.DefineStep(interStepWaitStep{}),
		dex.DefineStep(interStepPublishStep{}),
	}
}

func (interStepFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{
		interStepFirstChannel,
		interStepSecondChannel,
	}}
}

type interStepStartStep struct {
	dex.StepDefaults[struct{}]
}

func (interStepStartStep) GetStepType() string {
	return "start"
}

func (interStepStartStep) Execute(
	dex.Context,
	struct{},
) (dex.StepDecision, error) {
	return dex.GoToMulti(
		dex.MovementOf(interStepWaitStep{}, struct{}{}),
		dex.MovementOf(interStepPublishStep{}, 2),
	), nil
}

type interStepWaitStep struct {
	dex.DefaultStepOptions
}

func (interStepWaitStep) GetStepType() string {
	return "wait"
}

func (interStepWaitStep) WaitFor(
	dex.Context,
	struct{},
) (dex.Wait, error) {
	return dex.AnyOf(
		interStepFirstChannel.ForOne(),
		interStepSecondChannel.ForOne(),
	), nil
}

func (interStepWaitStep) Execute(
	ctx dex.Context,
	_ struct{},
) (dex.StepDecision, error) {
	first, err := interStepFirstChannel.GetConditionResults(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	second, err := interStepSecondChannel.GetConditionResults(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if len(first) != 0 || len(second) != 1 || second[0] != 2 {
		return dex.StepDecision{}, fmt.Errorf(
			"unexpected channel results: first=%v second=%v",
			first,
			second,
		)
	}
	return dex.GracefulComplete(second[0]), nil
}

type interStepPublishStep struct {
	dex.DefaultStepOptions
}

func (interStepPublishStep) GetStepType() string {
	return "publish"
}

func (interStepPublishStep) WaitFor(
	ctx dex.Context,
	input int,
) (dex.Wait, error) {
	if err := interStepSecondChannel.Publish(ctx, input); err != nil {
		return dex.Wait{}, err
	}
	return dex.SkipWaitImmediately(), nil
}

func (interStepPublishStep) Execute(
	dex.Context,
	int,
) (dex.StepDecision, error) {
	return dex.DeadEnd(), nil
}

func TestInterStepChannelFlow(t *testing.T) {
	flowID := newFlowID(t, "inter-step")
	_, err := integClient.StartFlow(
		integrationContext(t),
		interStepFlow{},
		flowID,
		struct{}{},
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	result := waitForFlow(t, flowID, true)
	require.Equal(t, dex.FlowCompleted, result.Status)
	require.Len(t, result.Completions, 1)
	var output int
	require.NoError(t, result.Completions[0].Output.Decode(&output))
	require.Equal(t, 2, output)
}
