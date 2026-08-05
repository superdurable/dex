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
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

type noStartStepFlow struct {
	emptyFlowSchema
}

func (noStartStepFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStep(noStartFinishStep{})}
}

func (noStartStepFlow) Start(
	dex.Context,
	int,
) (dex.RPCResult[int], error) {
	return dex.RPCResult[int]{
		Output:    2,
		NextSteps: []dex.StepMovement{dex.MovementOf(noStartFinishStep{}, 2)},
	}, nil
}

type noStartFinishStep struct {
	dex.StepDefaultsNoWaitFor[int]
}

func (noStartFinishStep) Execute(
	_ dex.Context,
	input int,
) (*dex.StepDecision, error) {
	return dex.GracefulComplete(input + 1), nil
}

func TestFlowWithoutStartingStep(t *testing.T) {
	ctx := integrationContext(t)
	flow := noStartStepFlow{}
	flowID := newFlowID(t, "no-start-step")
	_, err := integClient.StartFlow(
		ctx,
		flow,
		flowID,
		nil,
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	var rpcOutput int
	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		flow.Start,
		1,
		&rpcOutput,
		dex.InvokeOptions{},
	))
	require.Equal(t, 2, rpcOutput)
	result := waitForFlow(t, flowID, true)
	require.Equal(t, dex.FlowCompleted, result.Status)
	require.Len(t, result.Completions, 1)
	var output int
	require.NoError(t, result.Completions[0].Output.Decode(&output))
	require.Equal(t, 3, output)
}
