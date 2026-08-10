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
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

var noStepCounter = dex.DefineAttribute[int]("counter")

type noStepFlow struct {
	emptyFlowSchema
}

func (noStepFlow) GetSteps() []dex.StepDef {
	return nil
}

func (noStepFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{noStepCounter}}
}

func (noStepFlow) Fail(
	dex.Context,
	int,
) (*dex.RPCResult[int], error) {
	return nil, fmt.Errorf("planned no-step RPC failure")
}

func (noStepFlow) IncreaseCounter(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[int], error) {
	current, err := noStepCounter.Get(ctx)
	var missing *dex.AttributeNotFoundError
	if errors.As(err, &missing) {
		current = 0
	} else if err != nil {
		return nil, err
	}
	next := current + 1
	if err := noStepCounter.Set(ctx, next); err != nil {
		return nil, err
	}
	return &dex.RPCResult[int]{Output: next}, nil
}

func (noStepFlow) GetCounter(
	ctx dex.Context,
	_ dex.None,
) (*dex.RPCResult[int], error) {
	counter, err := noStepCounter.Get(ctx)
	var missing *dex.AttributeNotFoundError
	if errors.As(err, &missing) {
		return &dex.RPCResult[int]{Output: 0}, nil
	}
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[int]{Output: counter}, nil
}

func TestFlowWithoutSteps(t *testing.T) {
	ctx := integrationContext(t)
	flow := noStepFlow{}
	flowID := newFlowID(t, "no-step")
	_, err := integClient.StartFlow(
		ctx,
		flow,
		flowID,
		nil,
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	var searchPage dex.SearchFlowsPage
	require.Eventually(t, func() bool {
		searchPage, err = integClient.SearchFlows(
			ctx,
			"FlowType = '"+dex.GetFinalFlowType(flow)+"'",
			100,
			"",
		)
		if err != nil {
			return false
		}
		for _, entry := range searchPage.Flows {
			if entry.FlowID == flowID && entry.Status == dex.FlowRunning {
				return true
			}
		}
		return false
	}, 20*time.Second, 200*time.Millisecond, "SearchFlows failed: %v", err)
	var output int
	err = integClient.InvokeRPC(
		ctx,
		flowID,
		flow.Fail,
		1,
		&output,
		dex.InvokeOptions{},
	)
	var workerError *dex.WorkerInvocationError
	require.ErrorAs(t, err, &workerError)
	require.NotNil(t, workerError.Worker)
	require.True(t, strings.Contains(
		workerError.Worker.Detail,
		"planned no-step RPC failure",
	))
	require.NoError(t, integClient.StopFlow(
		ctx,
		flowID,
		dex.StopOptions{Type: dex.FailFlow, Reason: "test"},
	))
	result := waitForUncompletedFlow(t, flowID, false)
	require.Equal(t, dex.FlowFailed, result.Status)
}

func TestRPCLockConflict(t *testing.T) {
	ctx := integrationContext(t)
	flow := noStepFlow{}
	flowID := newFlowID(t, "rpc-lock")
	_, err := integClient.StartFlow(ctx, flow, flowID, nil, dex.StartFlowOptions{})
	require.NoError(t, err)

	const attempts = 100
	results := make(chan error, attempts)
	for index := 0; index < attempts; index++ {
		go func() {
			var output int
			results <- integClient.InvokeRPC(
				ctx,
				flowID,
				flow.IncreaseCounter,
				nil,
				&output,
				dex.InvokeOptions{LockAttributes: []dex.AttributeLock{
					dex.LockAttribute(noStepCounter),
				}},
			)
		}()
	}

	succeeded := 0
	conflicted := 0
	for index := 0; index < attempts; index++ {
		rpcErr := <-results
		var conflict *dex.RPCLockConflictError
		if errors.As(rpcErr, &conflict) {
			conflicted++
			continue
		}
		require.NoError(t, rpcErr)
		succeeded++
	}
	require.Positive(t, succeeded)
	require.Positive(t, conflicted)

	var counter int
	require.NoError(t, integClient.InvokeRPC(
		ctx,
		flowID,
		flow.GetCounter,
		nil,
		&counter,
		dex.InvokeOptions{},
	))
	require.Equal(t, succeeded, counter)
	require.NoError(t, integClient.StopFlow(
		ctx,
		flowID,
		dex.StopOptions{Type: dex.FailFlow, Reason: "test"},
	))
}
