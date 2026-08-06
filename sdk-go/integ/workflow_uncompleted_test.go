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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

type forceFailFlow struct {
	emptyFlowSchema
}

func (forceFailFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(forceFailStep{})}
}

type forceFailStep struct {
	dex.StepDefaultsNoWaitFor[struct{}]
}

func (forceFailStep) Execute(
	dex.Context,
	struct{},
) (*dex.StepDecision, error) {
	return dex.ForceFail("a failing message"), nil
}

type waitForFailureFlow struct {
	emptyFlowSchema
}

func (waitForFailureFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(waitForFailureStep{})}
}

type waitForFailureStep struct {
	dex.DefaultStepType
}

func (waitForFailureStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{WaitForRetry: &dex.RetryPolicy{MaximumAttempts: 1}}
}

func (waitForFailureStep) WaitFor(
	dex.Context,
	struct{},
) (*dex.Wait, error) {
	return nil, fmt.Errorf("test WaitFor failing")
}

func (waitForFailureStep) Execute(
	dex.Context,
	struct{},
) (*dex.StepDecision, error) {
	return dex.ForceFail("must not execute"), nil
}

type waitForTimeoutFlow struct {
	emptyFlowSchema
}

func (waitForTimeoutFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(waitForTimeoutStep{})}
}

type waitForTimeoutStep struct {
	dex.DefaultStepType
}

func (waitForTimeoutStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForTimeout: time.Second,
		WaitForRetry:   &dex.RetryPolicy{MaximumAttempts: 1},
	}
}

func (waitForTimeoutStep) WaitFor(
	ctx dex.Context,
	_ struct{},
) (*dex.Wait, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (waitForTimeoutStep) Execute(
	dex.Context,
	struct{},
) (*dex.StepDecision, error) {
	return dex.ForceFail("must not execute"), nil
}

func TestFlowTimeout(t *testing.T) {
	flowID := newFlowID(t, "flow-timeout")
	timeout := time.Second
	_, err := integClient.StartFlow(
		integrationContext(t),
		channelFlow{},
		flowID,
		struct{}{},
		dex.StartFlowOptions{Timeout: &timeout},
	)
	require.NoError(t, err)
	result := waitForFlow(t, flowID, false)
	require.Equal(t, dex.FlowTimedOut, result.Status)
}

func TestFlowCancel(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "flow-cancel")
	_, err := integClient.StartFlow(
		ctx,
		noStartStepFlow{},
		flowID,
		nil,
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	require.NoError(t, integClient.StopFlow(ctx, flowID, dex.StopOptions{}))
	result := waitForFlow(t, flowID, false)
	require.Equal(t, dex.FlowCanceled, result.Status)
}

func TestForceFailFlow(t *testing.T) {
	flowID := newFlowID(t, "force-fail")
	_, err := integClient.StartFlow(
		integrationContext(t),
		forceFailFlow{},
		flowID,
		struct{}{},
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	result := waitForFlow(t, flowID, false)
	require.Equal(t, dex.FlowFailed, result.Status)
	require.Equal(t, dex.FlowErrorStepDecision, result.ErrorType)
	require.True(t, strings.Contains(result.ErrorMessage, "a failing message"))
}

func TestWaitForFailureFlow(t *testing.T) {
	flowID := newFlowID(t, "wait-for-failure")
	_, err := integClient.StartFlow(
		integrationContext(t),
		waitForFailureFlow{},
		flowID,
		struct{}{},
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	result := waitForFlow(t, flowID, false)
	require.Equal(t, dex.FlowFailed, result.Status)
	require.Equal(t, dex.FlowErrorWorkerMethod, result.ErrorType)
	require.True(t, strings.Contains(result.ErrorMessage, "test WaitFor failing"))
}

func TestWaitForTimeoutFlow(t *testing.T) {
	flowID := newFlowID(t, "wait-for-timeout")
	_, err := integClient.StartFlow(
		integrationContext(t),
		waitForTimeoutFlow{},
		flowID,
		struct{}{},
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	result := waitForFlow(t, flowID, false)
	require.Equal(t, dex.FlowFailed, result.Status)
	require.Equal(t, dex.FlowErrorWorkerMethod, result.ErrorType)
	require.NotEmpty(t, result.ErrorMessage)
}
