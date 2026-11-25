// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

const missingLockErrorType = "*dex.MissingAttributeLockError"

// lockReqSpacedInstance needs path escaping, so the test covers escaped map lock keys.
const lockReqSpacedInstance = "item a"

var (
	lockReqTotal        = dex.DefineAttribute[int]("lock-req-total", dex.WritesRequireLock())
	lockReqCoordination = dex.DefineAttribute[bool]("lock-req-coordination")
	lockReqItems        = dex.DefineAttributeMap[int]("lock-req-items", dex.WritesRequireLock())
	lockReqShared       = dex.DefineAttributeMap[int](
		"lock-req-shared", dex.WritesRequireAttributeLock(lockReqCoordination))
	lockReqFree    = dex.DefineAttribute[int]("lock-req-free")
	lockReqReplies = dex.DefineChannelWithOptions[string](
		"lock-req-replies", dex.PublishesRequireAttributeLock(lockReqCoordination))
	lockReqEvents = dex.DefineChannelMapWithOptions[string](
		"lock-req-events", dex.PublishesRequireAttributeLock(lockReqCoordination))
	lockReqFreeChannel = dex.DefineChannel[string]("lock-req-free-channel")
)

func lockReqSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{lockReqTotal, lockReqCoordination, lockReqItems, lockReqShared, lockReqFree},
		Channels:   []dex.ChannelDef{lockReqReplies, lockReqEvents, lockReqFreeChannel},
	}
}

// lockReqStepOptions runs one attempt and routes an Execute failure to the recovery Step.
func lockReqStepOptions(locks ...dex.AttributeLock) *dex.StepOptions {
	return &dex.StepOptions{
		WaitForRetry:          &dex.RetryPolicy{MaximumAttempts: 1},
		ExecuteRetry:          &dex.RetryPolicy{MaximumAttempts: 1},
		ExecuteLockAttributes: locks,
		ExecuteFailure:        dex.ProceedToOnExecuteFailure[dex.None](lockReqRecoveryStep{}, nil),
	}
}

// attributeLockRequirementStepFlow routes its input to one Step per case.
type attributeLockRequirementStepFlow struct {
	dex.FlowDefaults
}

func (attributeLockRequirementStepFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(lockReqDispatchStep{}),
		dex.DefineStep(lockReqUnlockedTotalStep{}),
		dex.DefineStep(lockReqWaitForTotalStep{}),
		dex.DefineStep(lockReqLockedWaitForTotalStep{}),
		dex.DefineStep(lockReqLockedTotalStep{}),
		dex.DefineStep(lockReqLockedItemStep{}),
		dex.DefineStep(lockReqOtherItemStep{}),
		dex.DefineStep(lockReqLockedSharedStep{}),
		dex.DefineStep(lockReqUnlockedSharedStep{}),
		dex.DefineStep(lockReqUndeclaredStep{}),
		dex.DefineStep(lockReqRecoveryStep{}),
	}
}

func (attributeLockRequirementStepFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return lockReqSchema()
}

type lockReqDispatchStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (lockReqDispatchStep) Execute(_ dex.Context, stepCase string) (*dex.StepDecision, error) {
	switch stepCase {
	case "unlocked-total":
		return dex.GoTo[dex.None](lockReqUnlockedTotalStep{}, nil), nil
	case "movement-lock-total":
		// A per-movement override does not count, even when it names the required lock.
		return dex.GoTo[dex.None](lockReqUnlockedTotalStep{}, nil, dex.WithStepOptions(
			lockReqStepOptions(dex.LockAttribute(lockReqTotal)))), nil
	case "wait-for-total":
		return dex.GoTo[dex.None](lockReqWaitForTotalStep{}, nil), nil
	case "locked-wait-for-total":
		return dex.GoTo[dex.None](lockReqLockedWaitForTotalStep{}, nil), nil
	case "locked-total":
		return dex.GoTo[dex.None](lockReqLockedTotalStep{}, nil), nil
	case "locked-item":
		return dex.GoTo[dex.None](lockReqLockedItemStep{}, nil), nil
	case "other-item":
		return dex.GoTo[dex.None](lockReqOtherItemStep{}, nil), nil
	case "locked-shared":
		return dex.GoTo[dex.None](lockReqLockedSharedStep{}, nil), nil
	case "unlocked-shared":
		return dex.GoTo[dex.None](lockReqUnlockedSharedStep{}, nil), nil
	case "undeclared":
		return dex.GoTo[dex.None](lockReqUndeclaredStep{}, nil), nil
	default:
		return nil, fmt.Errorf("unknown lock requirement case %q", stepCase)
	}
}

type lockReqUnlockedTotalStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockReqUnlockedTotalStep) GetStepOptions() *dex.StepOptions {
	return lockReqStepOptions()
}

func (lockReqUnlockedTotalStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := lockReqTotal.Set(ctx, 1); err != nil {
		return nil, err
	}
	return dex.GracefulComplete("written"), nil
}

// lockReqWaitForTotalStep registers the lock for Execute only, so its WaitFor write fails.
type lockReqWaitForTotalStep struct {
	dex.StepDefaults
}

func (lockReqWaitForTotalStep) GetStepOptions() *dex.StepOptions {
	options := lockReqStepOptions(dex.LockAttribute(lockReqTotal))
	options.WaitForFailure = dex.ProceedOnWaitForFailure
	return options
}

func (lockReqWaitForTotalStep) WaitFor(ctx dex.Context, _ dex.None) (*dex.Wait, error) {
	if err := lockReqTotal.Set(ctx, 1); err != nil {
		return nil, err
	}
	return dex.SkipWaitImmediately(), nil
}

func (lockReqWaitForTotalStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return lockReqWaitForResult(ctx), nil
}

// lockReqLockedWaitForTotalStep registers the lock for WaitFor, so its WaitFor write commits.
type lockReqLockedWaitForTotalStep struct {
	dex.StepDefaults
}

func (lockReqLockedWaitForTotalStep) GetStepOptions() *dex.StepOptions {
	options := lockReqStepOptions()
	options.WaitForLockAttributes = []dex.AttributeLock{dex.LockAttribute(lockReqTotal)}
	options.WaitForFailure = dex.ProceedOnWaitForFailure
	return options
}

func (lockReqLockedWaitForTotalStep) WaitFor(ctx dex.Context, _ dex.None) (*dex.Wait, error) {
	if err := lockReqTotal.Set(ctx, 1); err != nil {
		return nil, err
	}
	return dex.SkipWaitImmediately(), nil
}

func (lockReqLockedWaitForTotalStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return lockReqWaitForResult(ctx), nil
}

// lockReqWaitForResult completes with the WaitFor failure, or "written" when WaitFor succeeded.
func lockReqWaitForResult(ctx dex.Context) *dex.StepDecision {
	if !ctx.WaitForMethodFailed() {
		return dex.GracefulComplete("written")
	}
	recovery := ctx.RecoveryError()
	if recovery == nil {
		return dex.GracefulComplete("WaitFor failed without a recovery error")
	}
	return dex.GracefulComplete(recovery.ErrorType + ": " + recovery.Detail)
}

type lockReqLockedTotalStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockReqLockedTotalStep) GetStepOptions() *dex.StepOptions {
	return lockReqStepOptions(dex.LockAttribute(lockReqTotal))
}

func (lockReqLockedTotalStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := lockReqTotal.Set(ctx, 1); err != nil {
		return nil, err
	}
	return dex.GracefulComplete("written"), nil
}

type lockReqLockedItemStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockReqLockedItemStep) GetStepOptions() *dex.StepOptions {
	return lockReqStepOptions(dex.LockAttributeMap(lockReqItems, lockReqSpacedInstance))
}

func (lockReqLockedItemStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := lockReqItems.Set(ctx, lockReqSpacedInstance, 1); err != nil {
		return nil, err
	}
	return dex.GracefulComplete("written"), nil
}

// lockReqOtherItemStep holds one instance's lock and writes another instance.
type lockReqOtherItemStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockReqOtherItemStep) GetStepOptions() *dex.StepOptions {
	return lockReqStepOptions(dex.LockAttributeMap(lockReqItems, lockReqSpacedInstance))
}

func (lockReqOtherItemStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := lockReqItems.Set(ctx, lockReqSpacedInstance, 1); err != nil {
		return nil, err
	}
	if err := lockReqItems.Set(ctx, "item b", 1); err != nil {
		return nil, err
	}
	return dex.GracefulComplete("written"), nil
}

type lockReqLockedSharedStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockReqLockedSharedStep) GetStepOptions() *dex.StepOptions {
	return lockReqStepOptions(dex.LockAttribute(lockReqCoordination))
}

func (lockReqLockedSharedStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := lockReqShared.Set(ctx, "x", 1); err != nil {
		return nil, err
	}
	return dex.GracefulComplete("written"), nil
}

// lockReqUnlockedSharedStep holds the written instance's own lock, not the declared coordination lock.
type lockReqUnlockedSharedStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockReqUnlockedSharedStep) GetStepOptions() *dex.StepOptions {
	return lockReqStepOptions(dex.LockAttributeMap(lockReqShared, "x"))
}

func (lockReqUnlockedSharedStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := lockReqShared.Set(ctx, "x", 1); err != nil {
		return nil, err
	}
	return dex.GracefulComplete("written"), nil
}

type lockReqUndeclaredStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockReqUndeclaredStep) GetStepOptions() *dex.StepOptions {
	return lockReqStepOptions()
}

func (lockReqUndeclaredStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := lockReqFree.Set(ctx, 1); err != nil {
		return nil, err
	}
	if err := lockReqFreeChannel.Publish(ctx, "free"); err != nil {
		return nil, err
	}
	return dex.GracefulComplete("written"), nil
}

// lockReqRecoveryStep completes with the failed attempt's error type and detail.
type lockReqRecoveryStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockReqRecoveryStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	recovery := ctx.RecoveryError()
	if recovery == nil {
		return nil, errors.New("recovery Step has no recovery error")
	}
	return dex.GracefulComplete(recovery.ErrorType + ": " + recovery.Detail), nil
}

// attributeLockRequirementRPCFlow has no Steps, so it stays running for RPC cases.
type attributeLockRequirementRPCFlow struct {
	dex.FlowDefaults
}

func (attributeLockRequirementRPCFlow) GetSteps() []dex.StepDef {
	return nil
}

func (attributeLockRequirementRPCFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return lockReqSchema()
}

func (flow attributeLockRequirementRPCFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.PublishReplyUnlocked, nil),
		dex.DefineRPC(flow.PublishReplyLocked, &dex.RPCOptions{
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(lockReqCoordination)},
		}),
		dex.DefineRPC(flow.SetItem, nil),
		dex.DefineRPC(flow.SetItemLocked, &dex.RPCOptions{
			LockAttributes: []dex.AttributeLock{dex.LockAttributeMap(lockReqItems, lockReqSpacedInstance)},
		}),
		dex.DefineRPC(flow.PublishEventUnlocked, nil),
		dex.DefineRPC(flow.CountEvents, nil),
		dex.DefineRPC(flow.CountReplies, nil),
		dex.DefineRPC(flow.SetTotalLocked, &dex.RPCOptions{
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(lockReqTotal)},
		}),
		dex.DefineRPC(flow.DeleteTotalUnlocked, nil),
	}
}

// PublishEventUnlocked also writes an undeclared Attribute, which the failure must discard too.
func (attributeLockRequirementRPCFlow) PublishEventUnlocked(
	ctx dex.Context,
	instance string,
) (*dex.RPCResult[dex.None], error) {
	if err := lockReqFree.Set(ctx, 1); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, lockReqEvents.Publish(ctx, instance, "event")
}

func (attributeLockRequirementRPCFlow) CountEvents(ctx dex.Context, instance string) (*dex.RPCResult[int], error) {
	return &dex.RPCResult[int]{Output: lockReqEvents.Size(ctx, instance)}, nil
}

func (attributeLockRequirementRPCFlow) SetTotalLocked(ctx dex.Context, value int) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, lockReqTotal.Set(ctx, value)
}

func (attributeLockRequirementRPCFlow) DeleteTotalUnlocked(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, lockReqTotal.Delete(ctx)
}

// attributeLockRequirementTimeoutFlow writes a declared Attribute from its unchecked timeout handler.
type attributeLockRequirementTimeoutFlow struct {
	dex.FlowDefaults
}

func (attributeLockRequirementTimeoutFlow) GetSteps() []dex.StepDef {
	return nil
}

func (attributeLockRequirementTimeoutFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return lockReqSchema()
}

func (attributeLockRequirementTimeoutFlow) HandleTimeout(ctx dex.Context) (*dex.StepDecision, error) {
	if err := lockReqTotal.Set(ctx, 5); err != nil {
		return nil, err
	}
	return dex.ForceComplete("expired"), nil
}

// PublishReplyUnlocked also writes an undeclared Attribute, which the failure must discard too.
func (attributeLockRequirementRPCFlow) PublishReplyUnlocked(
	ctx dex.Context,
	reply string,
) (*dex.RPCResult[dex.None], error) {
	if err := lockReqFree.Set(ctx, 1); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, lockReqReplies.Publish(ctx, reply)
}

func (attributeLockRequirementRPCFlow) CountReplies(ctx dex.Context, _ dex.None) (*dex.RPCResult[int], error) {
	return &dex.RPCResult[int]{Output: lockReqReplies.Size(ctx)}, nil
}

func (attributeLockRequirementRPCFlow) PublishReplyLocked(
	ctx dex.Context,
	reply string,
) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, lockReqReplies.Publish(ctx, reply)
}

func (attributeLockRequirementRPCFlow) SetItem(ctx dex.Context, instance string) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, lockReqItems.Set(ctx, instance, 1)
}

func (attributeLockRequirementRPCFlow) SetItemLocked(
	ctx dex.Context,
	instance string,
) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, lockReqItems.Set(ctx, instance, 1)
}

func lockReqRegistry(t *testing.T) *dex.Registry {
	t.Helper()
	registry, err := dex.NewRegistry([]dex.Flow{
		attributeLockRequirementStepFlow{},
		attributeLockRequirementRPCFlow{},
		attributeLockRequirementTimeoutFlow{},
	})
	require.NoError(t, err)
	return registry
}

type lockReqHarness struct {
	client    *dex.Client
	taskQueue string
}

func startLockReqHarness(t *testing.T) lockReqHarness {
	t.Helper()
	return startLockHarness(t, lockReqRegistry)
}

// startLockHarness builds the Worker registry, then the Client registry, with newRegistry.
func startLockHarness(t *testing.T, newRegistry func(*testing.T) *dex.Registry) lockReqHarness {
	t.Helper()
	taskQueue := "lock-req-" + uuid.NewString()[:8]
	worker, err := dex.NewWorker(newRegistry(t), newPullSDKCache(t), dex.WorkerOptions{
		FlowServiceAddress: flowServiceAddress(),
		Pull:               &dex.PullWorkerOptions{MaxConcurrentTasks: 4, TaskQueue: taskQueue},
	})
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() { result <- worker.Start() }()
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, worker.Stop(stopCtx))
		require.NoError(t, <-result)
	})
	require.NoError(t, worker.WaitPullRegistered(integrationContext(t)))
	client, err := dex.NewClient(newRegistry(t), newPullSDKCache(t), dex.ClientOptions{
		FlowServiceAddress: flowServiceAddress(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return lockReqHarness{client: client, taskQueue: taskQueue}
}

func (harness lockReqHarness) startFlow(t *testing.T, flow dex.Flow, input any) string {
	t.Helper()
	flowID := newFlowID(t, "lock-req")
	_, err := harness.client.StartFlow(integrationContext(t), flow, flowID, input,
		dex.StartFlowOptions{TaskQueue: harness.taskQueue})
	require.NoError(t, err)
	return flowID
}

func (harness lockReqHarness) waitForFlow(t *testing.T, flowID string) dex.FlowResult {
	t.Helper()
	result, err := harness.client.WaitForFlow(integrationContext(t), flowID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	return result
}

// isSet reports whether the Flow stored the Attribute, or the AttributeMap instance when instance is set.
func (harness lockReqHarness) isSet(t *testing.T, flowID string, attribute dex.AttributeDef, instance string) bool {
	t.Helper()
	value := -1
	switch definition := attribute.(type) {
	case dex.Attribute[int]:
		result, err := harness.client.GetAttributes(integrationContext(t), flowID,
			dex.ReadAttribute(definition, &value))
		require.NoError(t, err)
		return result.IsSet(definition)
	case dex.AttributeMap[int]:
		result, err := harness.client.GetAttributes(integrationContext(t), flowID,
			dex.ReadAttributeMapInstance(definition, instance, &value))
		require.NoError(t, err)
		return result.IsMapInstanceSet(definition, instance)
	default:
		t.Fatalf("unsupported Attribute definition %T", attribute)
		return false
	}
}

// TestAttributeLockRequirementSteps covers declared writes from WaitFor and Execute.
func TestAttributeLockRequirementSteps(t *testing.T) {
	harness := startLockReqHarness(t)
	flow := attributeLockRequirementStepFlow{}
	cases := []struct {
		stepCase string
		// missing is the expected error detail fragment, or empty when the Step succeeds.
		missing string
		// written lists the keys the Step writes; a failed Step leaves all of them unset.
		written   []dex.AttributeDef
		instances []string
	}{
		{
			stepCase: "unlocked-total",
			missing:  `step "lockReqUnlockedTotalStep" Execute wrote Attribute "lock-req-total" without the required lock of Attribute "lock-req-total"`,
			written:  []dex.AttributeDef{lockReqTotal}, instances: []string{""},
		},
		{
			stepCase: "movement-lock-total",
			missing:  `wrote Attribute "lock-req-total"`,
			written:  []dex.AttributeDef{lockReqTotal}, instances: []string{""},
		},
		{
			stepCase: "locked-total",
			written:  []dex.AttributeDef{lockReqTotal}, instances: []string{""},
		},
		{
			stepCase: "locked-item",
			written:  []dex.AttributeDef{lockReqItems}, instances: []string{lockReqSpacedInstance},
		},
		{
			stepCase: "other-item",
			missing: `wrote AttributeMap "lock-req-items" instance "item b" without the required lock of ` +
				`AttributeMap "lock-req-items" instance "item b"`,
			written:   []dex.AttributeDef{lockReqItems, lockReqItems},
			instances: []string{lockReqSpacedInstance, "item b"},
		},
		{
			stepCase: "locked-shared",
			written:  []dex.AttributeDef{lockReqShared}, instances: []string{"x"},
		},
		{
			stepCase: "unlocked-shared",
			missing:  `wrote AttributeMap "lock-req-shared" instance "x" without the required lock of Attribute "lock-req-coordination"`,
			written:  []dex.AttributeDef{lockReqShared}, instances: []string{"x"},
		},
		{
			stepCase: "undeclared",
			written:  []dex.AttributeDef{lockReqFree}, instances: []string{""},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.stepCase, func(t *testing.T) {
			flowID := harness.startFlow(t, flow, testCase.stepCase)
			result := harness.waitForFlow(t, flowID)
			require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
			var output string
			require.NoError(t, result.DecodeSingleOutput(&output))
			if testCase.missing == "" {
				require.Equal(t, "written", output)
			} else {
				require.Contains(t, output, missingLockErrorType+": ")
				require.Contains(t, output, testCase.missing)
			}
			for index, attribute := range testCase.written {
				require.Equal(t, testCase.missing == "", harness.isSet(t, flowID, attribute, testCase.instances[index]),
					"key %d written by %s", index, testCase.stepCase)
			}
		})
	}

	t.Run("wait-for-total", func(t *testing.T) {
		flowID := harness.startFlow(t, flow, "wait-for-total")
		result := harness.waitForFlow(t, flowID)
		require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
		var output string
		require.NoError(t, result.DecodeSingleOutput(&output))
		require.Contains(t, output, missingLockErrorType+": ")
		require.Contains(t, output, `step "lockReqWaitForTotalStep" WaitFor wrote Attribute "lock-req-total"`)
		require.False(t, harness.isSet(t, flowID, lockReqTotal, ""), "the failed WaitFor write is discarded")
	})
	t.Run("locked-wait-for-total", func(t *testing.T) {
		flowID := harness.startFlow(t, flow, "locked-wait-for-total")
		result := harness.waitForFlow(t, flowID)
		require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
		var output string
		require.NoError(t, result.DecodeSingleOutput(&output))
		require.Equal(t, "written", output)
		require.True(t, harness.isSet(t, flowID, lockReqTotal, ""))
	})
}

// TestAttributeLockRequirementTimeoutHandler shows a Flow timeout handler is not checked.
func TestAttributeLockRequirementTimeoutHandler(t *testing.T) {
	harness := startLockReqHarness(t)
	flowID := newFlowID(t, "lock-req-timeout")
	timeout := time.Second
	_, err := harness.client.StartFlow(integrationContext(t), attributeLockRequirementTimeoutFlow{}, flowID, nil,
		dex.StartFlowOptions{TaskQueue: harness.taskQueue, Timeout: &timeout})
	require.NoError(t, err)
	result := harness.waitForFlow(t, flowID)
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	var output string
	require.NoError(t, result.DecodeSingleOutput(&output))
	require.Equal(t, "expired", output)
	total := -1
	values, err := harness.client.GetAttributes(integrationContext(t), flowID, dex.ReadAttribute(lockReqTotal, &total))
	require.NoError(t, err)
	require.True(t, values.IsSet(lockReqTotal))
	require.Equal(t, 5, total)
}

// TestAttributeLockRequirementRPCs covers declared publishes and writes from RPCs.
func TestAttributeLockRequirementRPCs(t *testing.T) {
	harness := startLockReqHarness(t)
	flow := attributeLockRequirementRPCFlow{}
	requireMissingLock := func(t *testing.T, err error, fragment string) {
		t.Helper()
		var invocationErr *dex.WorkerInvocationError
		require.ErrorAs(t, err, &invocationErr)
		require.NotNil(t, invocationErr.Worker)
		require.Equal(t, missingLockErrorType, invocationErr.Worker.Type)
		require.Contains(t, invocationErr.Worker.Detail, fragment)
	}

	t.Run("publish-locked-then-unlocked", func(t *testing.T) {
		flowID := harness.startFlow(t, flow, nil)
		replyCount := func() int {
			var size int
			require.NoError(t, harness.client.InvokeRPC(integrationContext(t), flowID, flow.CountReplies, nil, &size))
			return size
		}
		require.NoError(t, harness.client.InvokeRPC(integrationContext(t), flowID, flow.PublishReplyLocked, "hi", new(dex.None)))
		require.Equal(t, 1, replyCount(), "the locked publish commits")
		err := harness.client.InvokeRPC(integrationContext(t), flowID, flow.PublishReplyUnlocked, "hi", new(dex.None))
		requireMissingLock(t, err,
			`RPC "PublishReplyUnlocked" published to Channel "lock-req-replies" without the required lock of Attribute "lock-req-coordination"`)
		require.Equal(t, 1, replyCount(), "the rejected publish is discarded")
		require.False(t, harness.isSet(t, flowID, lockReqFree, ""), "the other buffered write is discarded")
	})
	t.Run("write-unlocked", func(t *testing.T) {
		flowID := harness.startFlow(t, flow, nil)
		err := harness.client.InvokeRPC(integrationContext(t), flowID, flow.SetItem, lockReqSpacedInstance, new(dex.None))
		requireMissingLock(t, err, `wrote AttributeMap "lock-req-items" instance "item a"`)
		require.False(t, harness.isSet(t, flowID, lockReqItems, lockReqSpacedInstance))
	})
	t.Run("write-call-time-lock", func(t *testing.T) {
		flowID := harness.startFlow(t, flow, nil)
		err := harness.client.InvokeRPCWithOptions(integrationContext(t), flowID, flow.SetItem, lockReqSpacedInstance,
			new(dex.None), dex.RPCInvokeOptions{
				LockAttributeMapInstances: []dex.AttributeLock{dex.LockAttributeMap(lockReqItems, lockReqSpacedInstance)},
			})
		requireMissingLock(t, err, `wrote AttributeMap "lock-req-items" instance "item a"`)
		require.False(t, harness.isSet(t, flowID, lockReqItems, lockReqSpacedInstance),
			"a call-time lock does not satisfy the declared requirement")
	})
	t.Run("write-registered-lock", func(t *testing.T) {
		flowID := harness.startFlow(t, flow, nil)
		err := harness.client.InvokeRPC(integrationContext(t), flowID, flow.SetItemLocked, lockReqSpacedInstance, new(dex.None))
		require.NoError(t, err)
		require.True(t, harness.isSet(t, flowID, lockReqItems, lockReqSpacedInstance))
	})
	t.Run("channel-map-publish-unlocked", func(t *testing.T) {
		flowID := harness.startFlow(t, flow, nil)
		err := harness.client.InvokeRPC(integrationContext(t), flowID, flow.PublishEventUnlocked,
			lockReqSpacedInstance, new(dex.None))
		requireMissingLock(t, err, `published to ChannelMap "lock-req-events" instance "item a" `+
			`without the required lock of Attribute "lock-req-coordination"`)
		var size int
		require.NoError(t, harness.client.InvokeRPC(integrationContext(t), flowID, flow.CountEvents,
			lockReqSpacedInstance, &size))
		require.Zero(t, size, "the rejected publish is discarded")
		require.False(t, harness.isSet(t, flowID, lockReqFree, ""), "the other buffered write is discarded")
	})
	t.Run("delete-unlocked", func(t *testing.T) {
		flowID := harness.startFlow(t, flow, nil)
		require.NoError(t, harness.client.InvokeRPC(integrationContext(t), flowID, flow.SetTotalLocked, 7, new(dex.None)))
		err := harness.client.InvokeRPC(integrationContext(t), flowID, flow.DeleteTotalUnlocked, nil, new(dex.None))
		requireMissingLock(t, err, `RPC "DeleteTotalUnlocked" wrote Attribute "lock-req-total" `+
			`without the required lock of Attribute "lock-req-total"`)
		total := -1
		values, err := harness.client.GetAttributes(integrationContext(t), flowID, dex.ReadAttribute(lockReqTotal, &total))
		require.NoError(t, err)
		require.True(t, values.IsSet(lockReqTotal), "the rejected delete is discarded")
		require.Equal(t, 7, total)
	})
}

var (
	// Shared options pointers that tests change after registration or during a call.
	lockSnapSharedStepOptions = &dex.StepOptions{ExecuteRetry: &dex.RetryPolicy{MaximumAttempts: 1}}
	lockSnapSharedRPCOptions  = &dex.RPCOptions{}
	// lockSnapFreshOptionsChanged drops the lock from the options lockSnapFreshStep builds on each call.
	lockSnapFreshOptionsChanged atomic.Bool
	lockSnapCurrentGate         atomic.Pointer[lockSnapGate]
)

func lockSnapTotalLocks() []dex.AttributeLock {
	return []dex.AttributeLock{dex.LockAttribute(lockReqTotal)}
}

// resetLockSnapOptions makes every snapshot option hold the total lock, now and after the test.
func resetLockSnapOptions(t *testing.T) {
	t.Helper()
	restore := func() {
		lockSnapSharedStepOptions.ExecuteLockAttributes = lockSnapTotalLocks()
		lockSnapSharedRPCOptions.LockAttributes = lockSnapTotalLocks()
		lockSnapFreshOptionsChanged.Store(false)
	}
	restore()
	t.Cleanup(restore)
}

// lockSnapGate pauses one handler until the test opens it.
type lockSnapGate struct {
	entered     chan string
	release     chan struct{}
	releaseOnce sync.Once
}

func armLockSnapGate(t *testing.T) *lockSnapGate {
	t.Helper()
	gate := &lockSnapGate{entered: make(chan string, 1), release: make(chan struct{})}
	lockSnapCurrentGate.Store(gate)
	t.Cleanup(gate.open)
	return gate
}

func (gate *lockSnapGate) open() {
	gate.releaseOnce.Do(func() { close(gate.release) })
}

// awaitEntered returns the Flow ID of the paused handler.
func (gate *lockSnapGate) awaitEntered(t *testing.T) string {
	t.Helper()
	select {
	case flowID := <-gate.entered:
		return flowID
	case <-time.After(30 * time.Second):
		t.Fatal("the gated handler did not start")
		return ""
	}
}

func pauseAtLockSnapGate(ctx dex.Context) error {
	gate := lockSnapCurrentGate.Load()
	if gate == nil {
		return errors.New("no lock snapshot gate is armed")
	}
	gate.entered <- ctx.FlowID()
	select {
	case <-gate.release:
		return nil
	case <-time.After(45 * time.Second):
		return errors.New("the gated handler was not released")
	}
}

func lockSnapGatedWrite(ctx dex.Context) (*dex.StepDecision, error) {
	if err := pauseAtLockSnapGate(ctx); err != nil {
		return nil, err
	}
	if err := lockReqTotal.Set(ctx, 1); err != nil {
		return nil, err
	}
	return dex.GracefulComplete("written"), nil
}

// lockSnapSharedStep returns one shared options pointer.
type lockSnapSharedStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockSnapSharedStep) GetStepOptions() *dex.StepOptions {
	return lockSnapSharedStepOptions
}

func (lockSnapSharedStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return lockSnapGatedWrite(ctx)
}

// lockSnapFreshStep builds new options on each call; later calls lose the lock.
type lockSnapFreshStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockSnapFreshStep) GetStepOptions() *dex.StepOptions {
	options := &dex.StepOptions{ExecuteRetry: &dex.RetryPolicy{MaximumAttempts: 1}}
	if !lockSnapFreshOptionsChanged.Load() {
		options.ExecuteLockAttributes = lockSnapTotalLocks()
	}
	return options
}

func (lockSnapFreshStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return lockSnapGatedWrite(ctx)
}

// lockSnapFailToSharedStep fails once, so its Execute failure route moves to lockSnapSharedStep.
type lockSnapFailToSharedStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockSnapFailToSharedStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry:   &dex.RetryPolicy{MaximumAttempts: 1},
		ExecuteFailure: dex.ProceedToOnExecuteFailure[dex.None](lockSnapSharedStep{}, nil),
	}
}

func (lockSnapFailToSharedStep) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return nil, errors.New("route to the Execute failure target")
}

// lockSnapFailToFreshStep fails once, so its Execute failure route moves to lockSnapFreshStep.
type lockSnapFailToFreshStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockSnapFailToFreshStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry:   &dex.RetryPolicy{MaximumAttempts: 1},
		ExecuteFailure: dex.ProceedToOnExecuteFailure[dex.None](lockSnapFreshStep{}, nil),
	}
}

func (lockSnapFailToFreshStep) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return nil, errors.New("route to the Execute failure target")
}

type lockSnapSubFlowStep struct {
	dex.StepDefaults
}

func (lockSnapSubFlowStep) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(lockSnapStartFlow{}, nil)), nil
}

func (lockSnapSubFlowStep) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("sub-flow done"), nil
}

type lockSnapDispatchStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (lockSnapDispatchStep) Execute(_ dex.Context, route string) (*dex.StepDecision, error) {
	switch route {
	case "shared-movement":
		return dex.GoTo[dex.None](lockSnapSharedStep{}, nil), nil
	case "shared-execute-failure":
		return dex.GoTo[dex.None](lockSnapFailToSharedStep{}, nil), nil
	case "fresh-execute-failure":
		return dex.GoTo[dex.None](lockSnapFailToFreshStep{}, nil), nil
	case "sub-flow":
		return dex.GoTo[dex.None](lockSnapSubFlowStep{}, nil), nil
	case "timeout":
		// The Flow stays open until its timeout handler runs.
		return dex.DeadEnd(), nil
	default:
		return nil, fmt.Errorf("unknown lock snapshot route %q", route)
	}
}

// lockSnapStartFlow starts in lockSnapSharedStep, as a Flow or as a SubFlow.
type lockSnapStartFlow struct {
	dex.FlowDefaults
}

func (lockSnapStartFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(lockSnapSharedStep{})}
}

func (lockSnapStartFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return lockReqSchema()
}

func (flow lockSnapStartFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.SetTotalLocked, &dex.RPCOptions{LockAttributes: lockSnapTotalLocks()})}
}

func (lockSnapStartFlow) SetTotalLocked(ctx dex.Context, value int) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, lockReqTotal.Set(ctx, value)
}

// lockSnapRouteFlow reaches the gated Steps by movement, failure routes, and SubFlow.
type lockSnapRouteFlow struct {
	dex.FlowDefaults
}

func (lockSnapRouteFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(lockSnapDispatchStep{}),
		dex.DefineStep(lockSnapSharedStep{}),
		dex.DefineStep(lockSnapFreshStep{}),
		dex.DefineStep(lockSnapFailToSharedStep{}),
		dex.DefineStep(lockSnapFailToFreshStep{}),
		dex.DefineStep(lockSnapSubFlowStep{}),
	}
}

func (lockSnapRouteFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return lockReqSchema()
}

func (lockSnapRouteFlow) HandleTimeout(dex.Context) (*dex.StepDecision, error) {
	return nil, errors.New("route to the timeout handler failure target")
}

func (flow lockSnapRouteFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.SetTotalLocked, &dex.RPCOptions{LockAttributes: lockSnapTotalLocks()})}
}

func (lockSnapRouteFlow) SetTotalLocked(ctx dex.Context, value int) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, lockReqTotal.Set(ctx, value)
}

// lockSnapRPCFlow has no Steps; SetTotalGated registers the shared RPC options pointer.
type lockSnapRPCFlow struct {
	dex.FlowDefaults
}

func (lockSnapRPCFlow) GetSteps() []dex.StepDef {
	return nil
}

func (lockSnapRPCFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return lockReqSchema()
}

func (flow lockSnapRPCFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.SetTotalLocked, &dex.RPCOptions{LockAttributes: lockSnapTotalLocks()}),
		dex.DefineRPC(flow.SetTotalGated, lockSnapSharedRPCOptions),
	}
}

func (lockSnapRPCFlow) SetTotalLocked(ctx dex.Context, value int) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, lockReqTotal.Set(ctx, value)
}

func (lockSnapRPCFlow) SetTotalGated(ctx dex.Context, value int) (*dex.RPCResult[dex.None], error) {
	if err := pauseAtLockSnapGate(ctx); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, lockReqTotal.Set(ctx, value)
}

func lockSnapRegistry(t *testing.T) *dex.Registry {
	t.Helper()
	registry, err := dex.NewRegistry([]dex.Flow{lockSnapStartFlow{}, lockSnapRouteFlow{}, lockSnapRPCFlow{}})
	require.NoError(t, err)
	return registry
}

// requireGatedStepHoldsLock expects a lock conflict while the Step is paused, then its committed write.
func (harness lockReqHarness) requireGatedStepHoldsLock(
	t *testing.T,
	gate *lockSnapGate,
	conflictingRPC any,
	changeWhilePaused func(),
) {
	t.Helper()
	pausedFlowID := gate.awaitEntered(t)
	if changeWhilePaused != nil {
		changeWhilePaused()
	}
	err := harness.client.InvokeRPC(integrationContext(t), pausedFlowID, conflictingRPC, 2, new(dex.None))
	var conflict *dex.RPCLockConflictError
	require.ErrorAs(t, err, &conflict, "the paused Step holds its registered lock")
	gate.open()
	result := harness.waitForFlow(t, pausedFlowID)
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	var output string
	require.NoError(t, result.DecodeSingleOutput(&output))
	require.Equal(t, "written", output)
	total := -1
	values, err := harness.client.GetAttributes(integrationContext(t), pausedFlowID, dex.ReadAttribute(lockReqTotal, &total))
	require.NoError(t, err)
	require.Equal(t, 1, total, "the paused Step's write commits")
	require.True(t, values.IsSet(lockReqTotal))
}

// TestAttributeLockRequirementRegisteredLockSnapshot checks that every send path and the check use registered locks.
func TestAttributeLockRequirementRegisteredLockSnapshot(t *testing.T) {
	startFlow := lockSnapStartFlow{}
	routeFlow := lockSnapRouteFlow{}
	clearSharedStepLocks := func() { lockSnapSharedStepOptions.ExecuteLockAttributes = nil }
	// A nil entry is invalid, so mapping the original field would fail the send.
	setInvalidSharedStepLock := func() { lockSnapSharedStepOptions.ExecuteLockAttributes = []dex.AttributeLock{nil} }
	cases := []struct {
		name string
		// changeAfterRegistration runs after both registries exist and before the Flow starts.
		changeAfterRegistration func()
		changeWhilePaused       func()
		flow                    dex.Flow
		input                   any
		conflictingRPC          any
	}{
		{
			name:                    "start-shared-options-cleared-after-registration",
			changeAfterRegistration: clearSharedStepLocks,
			flow:                    startFlow, input: nil, conflictingRPC: startFlow.SetTotalLocked,
		},
		{
			name:                    "start-shared-options-invalid-lock-after-registration",
			changeAfterRegistration: setInvalidSharedStepLock,
			flow:                    startFlow, input: nil, conflictingRPC: startFlow.SetTotalLocked,
		},
		{
			name:                    "movement-shared-options-invalid-lock-after-registration",
			changeAfterRegistration: setInvalidSharedStepLock,
			flow:                    routeFlow, input: "shared-movement", conflictingRPC: routeFlow.SetTotalLocked,
		},
		{
			name:              "start-shared-options-cleared-during-call",
			changeWhilePaused: clearSharedStepLocks,
			flow:              startFlow, input: nil, conflictingRPC: startFlow.SetTotalLocked,
		},
		{
			name:                    "movement-shared-options-cleared-after-registration",
			changeAfterRegistration: clearSharedStepLocks,
			flow:                    routeFlow, input: "shared-movement", conflictingRPC: routeFlow.SetTotalLocked,
		},
		{
			name:                    "execute-failure-shared-options-cleared-after-registration",
			changeAfterRegistration: clearSharedStepLocks,
			flow:                    routeFlow, input: "shared-execute-failure", conflictingRPC: routeFlow.SetTotalLocked,
		},
		{
			name:                    "execute-failure-fresh-options-changed-after-registration",
			changeAfterRegistration: func() { lockSnapFreshOptionsChanged.Store(true) },
			flow:                    routeFlow, input: "fresh-execute-failure", conflictingRPC: routeFlow.SetTotalLocked,
		},
		{
			// The paused Step runs in the child Flow.
			name:                    "sub-flow-start-shared-options-cleared-after-registration",
			changeAfterRegistration: clearSharedStepLocks,
			flow:                    routeFlow, input: "sub-flow", conflictingRPC: startFlow.SetTotalLocked,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			resetLockSnapOptions(t)
			harness := startLockHarness(t, lockSnapRegistry)
			if testCase.changeAfterRegistration != nil {
				testCase.changeAfterRegistration()
			}
			gate := armLockSnapGate(t)
			harness.startFlow(t, testCase.flow, testCase.input)
			harness.requireGatedStepHoldsLock(t, gate, testCase.conflictingRPC, testCase.changeWhilePaused)
		})
	}

	t.Run("timeout-handler-failure-fresh-options-changed-after-registration", func(t *testing.T) {
		resetLockSnapOptions(t)
		harness := startLockHarness(t, lockSnapRegistry)
		lockSnapFreshOptionsChanged.Store(true)
		gate := armLockSnapGate(t)
		timeout := time.Second
		_, err := harness.client.StartFlow(integrationContext(t), routeFlow, newFlowID(t, "lock-snap-timeout"), "timeout",
			dex.StartFlowOptions{
				TaskQueue:     harness.taskQueue,
				Timeout:       &timeout,
				TimeoutPolicy: dex.TimeoutHandler,
				TimeoutHandlerOptions: &dex.FlowTimeoutHandlerOptions{
					Retry:   &dex.RetryPolicy{MaximumAttempts: 1},
					Failure: dex.ProceedToOnFlowTimeoutHandlerFailure(lockSnapFreshStep{}, nil),
				},
			})
		require.NoError(t, err)
		harness.requireGatedStepHoldsLock(t, gate, routeFlow.SetTotalLocked, nil)
	})
}

// lockSnapSelfRecoveryStep routes an Execute failure to a recovery value of its own type with nil options.
type lockSnapSelfRecoveryStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
	recovery bool
}

func (step lockSnapSelfRecoveryStep) GetStepOptions() *dex.StepOptions {
	if step.recovery {
		return nil
	}
	return &dex.StepOptions{
		ExecuteRetry:   &dex.RetryPolicy{MaximumAttempts: 1},
		ExecuteFailure: dex.ProceedToOnExecuteFailure[dex.None](lockSnapSelfRecoveryStep{recovery: true}, nil),
	}
}

func (lockSnapSelfRecoveryStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if ctx.RecoveryError() != nil {
		return dex.GracefulComplete("recovered"), nil
	}
	return nil, errors.New("route to the recovery value")
}

// lockSnapSelfRouteStep returns new options on each call that route an Execute failure to itself with an override.
type lockSnapSelfRouteStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockSnapSelfRouteStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteFailure: dex.ProceedToOnExecuteFailure[dex.None](lockSnapSelfRouteStep{},
			&dex.StepOptions{ExecuteRetry: &dex.RetryPolicy{MaximumAttempts: 1}}),
	}
}

func (lockSnapSelfRouteStep) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("done"), nil
}

// TestAttributeLockRequirementUnboundedFailureRoute returns an error instead of recursing without end.
func TestAttributeLockRequirementUnboundedFailureRoute(t *testing.T) {
	_, err := dex.NewRegistry([]dex.Flow{lockSnapStepFlow{
		flowType: "lockSnapDeclaredSelfRouteFlow",
		start:    dex.DefineStartStep(lockSnapSelfRouteStep{}),
		schema:   lockReqSchema(),
	}})
	require.ErrorContains(t, err, `failure routes nest deeper than 32 Steps at step "lockSnapSelfRouteStep"`)
}

// lockSnapStepFlow registers one start Step under a schema with or without lock requirements.
type lockSnapStepFlow struct {
	dex.FlowDefaults
	flowType string
	start    dex.StepDef
	schema   dex.PersistenceSchema
}

func (flow lockSnapStepFlow) GetFlowType() string {
	return flow.flowType
}

func (flow lockSnapStepFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{flow.start}
}

func (flow lockSnapStepFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return flow.schema
}

// TestAttributeLockRequirementSelfRecovery runs a bounded self-recovery route with and without declarations.
func TestAttributeLockRequirementSelfRecovery(t *testing.T) {
	cases := []struct {
		name   string
		schema dex.PersistenceSchema
	}{
		{name: "lockSnapUndeclaredSelfRecoveryFlow", schema: dex.PersistenceSchema{Attributes: []dex.AttributeDef{lockReqFree}}},
		{name: "lockSnapDeclaredSelfRecoveryFlow", schema: lockReqSchema()},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			flow := lockSnapStepFlow{
				flowType: testCase.name,
				start:    dex.DefineStartStep(lockSnapSelfRecoveryStep{}),
				schema:   testCase.schema,
			}
			harness := startLockHarness(t, func(t *testing.T) *dex.Registry {
				t.Helper()
				registry, err := dex.NewRegistry([]dex.Flow{flow})
				require.NoError(t, err)
				return registry
			})
			result := harness.waitForFlow(t, harness.startFlow(t, flow, nil))
			require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
			var output string
			require.NoError(t, result.DecodeSingleOutput(&output))
			require.Equal(t, "recovered", output)
		})
	}
}

// TestAttributeLockRequirementRegisteredRPCLockSnapshot checks that a shared RPCOptions change affects neither send nor check.
func TestAttributeLockRequirementRegisteredRPCLockSnapshot(t *testing.T) {
	flow := lockSnapRPCFlow{}
	clearSharedRPCLocks := func() { lockSnapSharedRPCOptions.LockAttributes = nil }
	cases := []struct {
		name                    string
		changeAfterRegistration func()
		changeWhilePaused       func()
	}{
		{name: "cleared-after-registration", changeAfterRegistration: clearSharedRPCLocks},
		{
			name: "invalid-lock-after-registration",
			changeAfterRegistration: func() {
				lockSnapSharedRPCOptions.LockAttributes = []dex.AttributeLock{nil}
			},
		},
		{name: "cleared-during-call", changeWhilePaused: clearSharedRPCLocks},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			resetLockSnapOptions(t)
			harness := startLockHarness(t, lockSnapRegistry)
			if testCase.changeAfterRegistration != nil {
				testCase.changeAfterRegistration()
			}
			flowID := harness.startFlow(t, flow, nil)
			gate := armLockSnapGate(t)
			gatedResult := make(chan error, 1)
			go func() {
				gatedResult <- harness.client.InvokeRPC(context.Background(), flowID, flow.SetTotalGated, 7, new(dex.None))
			}()
			gate.awaitEntered(t)
			if testCase.changeWhilePaused != nil {
				testCase.changeWhilePaused()
			}
			err := harness.client.InvokeRPC(integrationContext(t), flowID, flow.SetTotalLocked, 2, new(dex.None))
			var conflict *dex.RPCLockConflictError
			require.ErrorAs(t, err, &conflict, "the paused RPC holds its registered lock")
			gate.open()
			select {
			case err := <-gatedResult:
				require.NoError(t, err, "the paused RPC passes the check with its registered lock")
			case <-time.After(30 * time.Second):
				t.Fatal("the paused RPC did not return")
			}
			total := -1
			_, err = harness.client.GetAttributes(integrationContext(t), flowID, dex.ReadAttribute(lockReqTotal, &total))
			require.NoError(t, err)
			require.Equal(t, 7, total, "the paused RPC's write commits")
		})
	}
}

type lockReqInvalidFlow struct {
	dex.FlowDefaults
	schema dex.PersistenceSchema
}

func (lockReqInvalidFlow) GetSteps() []dex.StepDef {
	return nil
}

func (flow lockReqInvalidFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return flow.schema
}

// TestAttributeLockRequirementRegistration rejects a lock Attribute that is missing or an AttributeMap.
func TestAttributeLockRequirementRegistration(t *testing.T) {
	missingLock := dex.DefineAttribute[bool]("lock-req-missing")
	// An AttributeMap registered under the lock Attribute's name.
	mapLock := dex.DefineAttribute[bool]("lock-req-map-lock")
	mapLockCollision := dex.DefineAttributeMap[bool]("lock-req-map-lock")
	cases := []struct {
		name   string
		schema dex.PersistenceSchema
		want   string
	}{
		{
			name: "attribute-missing-lock",
			schema: dex.PersistenceSchema{Attributes: []dex.AttributeDef{
				dex.DefineAttribute[int]("lock-req-a", dex.WritesRequireAttributeLock(missingLock)),
			}},
			want: `attribute "lock-req-a" write lock: lock attribute "lock-req-missing" is not in the PersistenceSchema`,
		},
		{
			name: "channel-missing-lock",
			schema: dex.PersistenceSchema{Channels: []dex.ChannelDef{
				dex.DefineChannelMapWithOptions[string]("lock-req-c", dex.PublishesRequireAttributeLock(missingLock)),
			}},
			want: `channel "lock-req-c" publish lock: lock attribute "lock-req-missing" is not in the PersistenceSchema`,
		},
		{
			name: "attribute-map-lock",
			schema: dex.PersistenceSchema{
				Attributes: []dex.AttributeDef{mapLockCollision},
				Channels: []dex.ChannelDef{
					dex.DefineChannelWithOptions[string]("lock-req-c", dex.PublishesRequireAttributeLock(mapLock)),
				},
			},
			want: `lock attribute "lock-req-map-lock" is an AttributeMap; use a singleton Attribute`,
		},
		{
			// A zero Attribute must not turn into the written key's own lock.
			name: "attribute-zero-lock",
			schema: dex.PersistenceSchema{Attributes: []dex.AttributeDef{
				dex.DefineAttributeMap[int]("lock-req-a", dex.WritesRequireAttributeLock(dex.Attribute[bool]{})),
			}},
			want: `attribute "lock-req-a" write lock: lock attribute has an empty name`,
		},
		{
			// An empty lock name must not turn the publish requirement off.
			name: "channel-empty-lock",
			schema: dex.PersistenceSchema{Channels: []dex.ChannelDef{
				dex.DefineChannelWithOptions[string]("lock-req-c",
					dex.PublishesRequireAttributeLock(dex.DefineAttribute[bool](""))),
			}},
			want: `channel "lock-req-c" publish lock: lock attribute has an empty name`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := dex.NewRegistry([]dex.Flow{lockReqInvalidFlow{schema: testCase.schema}})
			require.ErrorContains(t, err, testCase.want)
		})
	}
}
