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

type waitForMethodTimeoutFlow struct {
	emptyFlowSchema
}

func (waitForMethodTimeoutFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(waitForMethodTimeoutStep{})}
}

type waitForMethodTimeoutStep struct {
	dex.DefaultStepType
}

func (waitForMethodTimeoutStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForMethodTimeout: time.Second,
		WaitForRetry:         &dex.RetryPolicy{MaximumAttempts: 1},
	}
}

func (waitForMethodTimeoutStep) WaitFor(
	ctx dex.Context,
	_ struct{},
) (*dex.Wait, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (waitForMethodTimeoutStep) Execute(
	dex.Context,
	struct{},
) (*dex.StepDecision, error) {
	return dex.ForceFail("must not execute"), nil
}

type timeoutHandlerFlow struct {
	emptyFlowSchema
}

func (timeoutHandlerFlow) GetSteps() []dex.StepDef {
	return nil
}

func (timeoutHandlerFlow) HandleTimeout(dex.Context) (*dex.StepDecision, error) {
	return dex.ForceComplete("expired"), nil
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
	result := waitForUncompletedFlow(t, flowID, false)
	require.Equal(t, dex.FlowFailed, result.Status)
	require.Equal(t, dex.FlowErrorTimeout, result.ErrorType)
}

func TestFlowTimeoutHandler(t *testing.T) {
	flowID := newFlowID(t, "flow-timeout-handler")
	timeout := time.Second
	_, err := integClient.StartFlow(
		integrationContext(t),
		timeoutHandlerFlow{},
		flowID,
		nil,
		dex.StartFlowOptions{Timeout: &timeout},
	)
	require.NoError(t, err)
	result := waitForFlow(t, flowID, true)
	require.Equal(t, dex.FlowCompleted, result.Status)
	var output string
	require.NoError(t, result.DecodeSingleOutput(&output))
	require.Equal(t, "expired", output)
}

func TestFlowTimeoutHandlerCancelOverride(t *testing.T) {
	flowID := newFlowID(t, "flow-timeout-handler-cancel")
	timeout := time.Second
	_, err := integClient.StartFlow(
		integrationContext(t),
		timeoutHandlerFlow{},
		flowID,
		nil,
		dex.StartFlowOptions{
			Timeout:       &timeout,
			TimeoutPolicy: dex.TimeoutCancel,
		},
	)
	require.NoError(t, err)
	result := waitForUncompletedFlow(t, flowID, false)
	require.Equal(t, dex.FlowCanceled, result.Status)
}

func TestFlowTimeoutHandlerRequiresCapability(t *testing.T) {
	timeout := time.Second
	_, err := integClient.StartFlow(
		integrationContext(t),
		channelFlow{},
		newFlowID(t, "flow-timeout-policy-without-timeout"),
		struct{}{},
		dex.StartFlowOptions{TimeoutPolicy: dex.TimeoutCancel},
	)
	require.ErrorContains(t, err, "flow timeout policy requires a positive timeout")

	_, err = integClient.StartFlow(
		integrationContext(t),
		channelFlow{},
		newFlowID(t, "flow-timeout-handler-missing"),
		struct{}{},
		dex.StartFlowOptions{
			Timeout:       &timeout,
			TimeoutPolicy: dex.TimeoutHandler,
		},
	)
	require.ErrorContains(t, err, "does not implement FlowTimeoutHandler")
}

func TestFlowCancel(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "flow-cancel")
	_, err := integClient.StartFlow(
		ctx,
		channelFlow{},
		flowID,
		struct{}{},
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	require.NoError(t, integClient.StopFlow(ctx, flowID, dex.StopOptions{}))
	result := waitForUncompletedFlow(t, flowID, false)
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
	result := waitForUncompletedFlow(t, flowID, false)
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
	result := waitForUncompletedFlow(t, flowID, false)
	require.Equal(t, dex.FlowFailed, result.Status)
	require.Equal(t, dex.FlowErrorWorkerMethod, result.ErrorType)
	require.True(t, strings.Contains(result.ErrorMessage, "test WaitFor failing"))
}

func TestWaitForMethodTimeoutFlow(t *testing.T) {
	flowID := newFlowID(t, "wait-for-timeout")
	_, err := integClient.StartFlow(
		integrationContext(t),
		waitForMethodTimeoutFlow{},
		flowID,
		struct{}{},
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	result := waitForUncompletedFlow(t, flowID, false)
	require.Equal(t, dex.FlowFailed, result.Status)
	require.Equal(t, dex.FlowErrorWorkerMethod, result.ErrorType)
	require.NotEmpty(t, result.ErrorMessage)
}
