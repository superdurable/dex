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
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	rpcFlowChannel = dex.DefineChannel[int]("rpc-values")
	rpcFlowStatus  = dex.DefineAttribute[string]("rpc-status")
)

type rpcFlow struct {
	dex.FlowDefaults
}

func (rpcFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(rpcFlowStep{})}
}

func (rpcFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{rpcFlowStatus},
		Channels:   []dex.ChannelDef{rpcFlowChannel},
	}
}

type rpcIncrementOutput struct {
	Value       int
	SizeBefore  int
	SizeAfter   int
	StatusFound bool
}

func (rpcFlow) Increment(
	ctx dex.Context,
	input int,
) (dex.RPCResult[rpcIncrementOutput], error) {
	_, err := rpcFlowStatus.Get(ctx)
	statusFound := true
	var notFound *dex.AttributeNotFoundError
	if errors.As(err, &notFound) {
		statusFound = false
	} else if err != nil {
		return dex.RPCResult[rpcIncrementOutput]{}, err
	}
	before := rpcFlowChannel.Size(ctx)
	if err := rpcFlowStatus.Set(ctx, "invoked"); err != nil {
		return dex.RPCResult[rpcIncrementOutput]{}, err
	}
	if err := rpcFlowChannel.Publish(ctx, input+1); err != nil {
		return dex.RPCResult[rpcIncrementOutput]{}, err
	}
	return dex.RPCResult[rpcIncrementOutput]{Output: rpcIncrementOutput{
		Value:       input + 1,
		SizeBefore:  before,
		SizeAfter:   rpcFlowChannel.Size(ctx),
		StatusFound: statusFound,
	}}, nil
}

func (rpcFlow) Fail(
	dex.Context,
	int,
) (dex.RPCResult[int], error) {
	return dex.RPCResult[int]{}, fmt.Errorf("planned RPC failure")
}

type rpcFlowStep struct {
	dex.StepDefaults
}

func (rpcFlowStep) WaitFor(
	dex.Context,
	int,
) (*dex.Wait, error) {
	return dex.AnyOf(rpcFlowChannel.ForOne()), nil
}

func (rpcFlowStep) Execute(
	ctx dex.Context,
	input int,
) (*dex.StepDecision, error) {
	values, err := rpcFlowChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(values) != 1 || values[0] != input+1 {
		return nil, fmt.Errorf("unexpected RPC channel values %v", values)
	}
	status, err := rpcFlowStatus.Get(ctx)
	if err != nil {
		return nil, err
	}
	if status != "invoked" {
		return nil, fmt.Errorf("RPC attribute write was not committed")
	}
	return dex.GracefulComplete(values[0] + 1), nil
}

func TestRPCFlow(t *testing.T) {
	ctx := integrationContext(t)
	flow := rpcFlow{}
	flowID := newFlowID(t, "rpc")
	_, err := integClient.StartFlow(
		ctx,
		flow,
		flowID,
		1,
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)

	var failedOutput int
	err = integClient.InvokeRPC(
		ctx,
		flowID,
		flow.Fail,
		1,
		&failedOutput,
		dex.InvokeOptions{},
	)
	sdkError := requireDexError(t, err, dex.ErrorWorkerAPI)
	require.NotNil(t, sdkError.OriginalWorkerError)
	require.True(t, strings.Contains(
		sdkError.OriginalWorkerError.Detail,
		"planned RPC failure",
	))

	var output rpcIncrementOutput
	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		flow.Increment,
		1,
		&output,
		dex.InvokeOptions{LockAttributes: []dex.AttributeLock{
			dex.LockAttribute(rpcFlowStatus),
		}},
	))
	require.Equal(t, 2, output.Value)
	require.Equal(t, 0, output.SizeBefore)
	require.Equal(t, 1, output.SizeAfter)
	require.False(t, output.StatusFound)

	result := waitForFlow(t, flowID, true)
	require.Equal(t, dex.FlowCompleted, result.Status)
	require.Len(t, result.Completions, 1)
	var flowOutput int
	require.NoError(t, result.Completions[0].Output.Decode(&flowOutput))
	require.Equal(t, 3, flowOutput)
}
