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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

const lockCheckWarningPrefix = "unlocked Attribute read-modify-write"

var (
	lockCheckCounter = dex.DefineAttribute[int]("lock-check-counter")
	lockCheckOther   = dex.DefineAttribute[int]("lock-check-other")
	lockCheckEntries = dex.DefineAttributeMap[int]("lock-check-entries")
)

func lockCheckIncrement(ctx dex.Context) error {
	count, err := lockCheckCounter.Get(ctx)
	var notFound *dex.AttributeNotFoundError
	if err != nil && !errors.As(err, &notFound) {
		return err
	}
	return lockCheckCounter.Set(ctx, count+1)
}

func lockCheckIncrementEntry(ctx dex.Context, instance string) error {
	count, err := lockCheckEntries.Get(ctx, instance)
	var notFound *dex.AttributeNotFoundError
	if err != nil && !errors.As(err, &notFound) {
		return err
	}
	return lockCheckEntries.Set(ctx, instance, count+1)
}

func lockCheckSingleAttemptOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForRetry: &dex.RetryPolicy{MaximumAttempts: 1},
		ExecuteRetry: &dex.RetryPolicy{MaximumAttempts: 1},
	}
}

// attributeLockCheckStepFlow routes its input to one Step per detection case.
type attributeLockCheckStepFlow struct {
	dex.FlowDefaults
}

func (attributeLockCheckStepFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(lockCheckDispatchStep{}),
		dex.DefineStep(lockCheckUnlockedStep{}),
		dex.DefineStep(lockCheckUnlockedWaitForStep{}),
		dex.DefineStep(lockCheckUnlockedMapStep{}),
		dex.DefineStep(lockCheckLockedStep{}),
		dex.DefineStep(lockCheckLockedMapStep{}),
		dex.DefineStep(lockCheckSingleWriterStep{}),
		dex.DefineStep(lockCheckReadOrWriteOnlyStep{}),
	}
}

func (attributeLockCheckStepFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{lockCheckCounter, lockCheckOther, lockCheckEntries},
	}
}

type lockCheckDispatchStep struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (lockCheckDispatchStep) Execute(_ dex.Context, stepCase string) (*dex.StepDecision, error) {
	switch stepCase {
	case "unlocked":
		return dex.GoTo[dex.None](lockCheckUnlockedStep{}, nil), nil
	case "unlocked-wait-for":
		return dex.GoTo[dex.None](lockCheckUnlockedWaitForStep{}, nil), nil
	case "unlocked-map":
		return dex.GoTo[dex.None](lockCheckUnlockedMapStep{}, nil), nil
	case "locked":
		return dex.GoTo[dex.None](lockCheckLockedStep{}, nil), nil
	case "locked-map":
		return dex.GoTo[dex.None](lockCheckLockedMapStep{}, nil), nil
	case "single-writer":
		return dex.GoTo[dex.None](lockCheckSingleWriterStep{}, nil), nil
	case "read-or-write-only":
		return dex.GoTo[dex.None](lockCheckReadOrWriteOnlyStep{}, nil), nil
	default:
		return nil, fmt.Errorf("unknown lock check case %q", stepCase)
	}
}

type lockCheckUnlockedStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockCheckUnlockedStep) GetStepOptions() *dex.StepOptions {
	return lockCheckSingleAttemptOptions()
}

func (lockCheckUnlockedStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := lockCheckIncrement(ctx); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

type lockCheckUnlockedWaitForStep struct {
	dex.StepDefaults
}

func (lockCheckUnlockedWaitForStep) GetStepOptions() *dex.StepOptions {
	return lockCheckSingleAttemptOptions()
}

func (lockCheckUnlockedWaitForStep) WaitFor(ctx dex.Context, _ dex.None) (*dex.Wait, error) {
	if err := lockCheckIncrement(ctx); err != nil {
		return nil, err
	}
	return dex.SkipWaitImmediately(), nil
}

func (lockCheckUnlockedWaitForStep) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

type lockCheckUnlockedMapStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockCheckUnlockedMapStep) GetStepOptions() *dex.StepOptions {
	options := lockCheckSingleAttemptOptions()
	options.ExecuteLoadAttributeMaps = []dex.AttributeDef{lockCheckEntries}
	return options
}

func (lockCheckUnlockedMapStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := lockCheckIncrementEntry(ctx, "e1"); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

type lockCheckLockedStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockCheckLockedStep) GetStepOptions() *dex.StepOptions {
	options := lockCheckSingleAttemptOptions()
	options.ExecuteLockAttributes = []dex.AttributeLock{dex.LockAttribute(lockCheckCounter)}
	return options
}

func (lockCheckLockedStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := lockCheckIncrement(ctx); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

type lockCheckLockedMapStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockCheckLockedMapStep) GetStepOptions() *dex.StepOptions {
	options := lockCheckSingleAttemptOptions()
	options.ExecuteLockAttributes = []dex.AttributeLock{dex.LockAttributeMap(lockCheckEntries, "e1")}
	options.ExecuteLoadAttributeMapInstances = []dex.AttributeMapLoad{lockCheckEntries.Load("e1")}
	return options
}

func (lockCheckLockedMapStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := lockCheckIncrementEntry(ctx, "e1"); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

type lockCheckSingleWriterStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockCheckSingleWriterStep) GetStepOptions() *dex.StepOptions {
	options := lockCheckSingleAttemptOptions()
	options.ExecuteLoadAttributeMaps = []dex.AttributeDef{lockCheckEntries}
	return options
}

func (lockCheckSingleWriterStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := lockCheckIncrement(ctx); err != nil {
		return nil, err
	}
	if err := lockCheckIncrementEntry(ctx, "e1"); err != nil {
		return nil, err
	}
	// The declarations follow the accesses: their order within the invocation does not matter.
	if err := lockCheckCounter.DeclareSingleWriter(ctx); err != nil {
		return nil, err
	}
	if err := lockCheckEntries.DeclareSingleWriter(ctx, "e1"); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

type lockCheckReadOrWriteOnlyStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockCheckReadOrWriteOnlyStep) GetStepOptions() *dex.StepOptions {
	return lockCheckSingleAttemptOptions()
}

func (lockCheckReadOrWriteOnlyStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	count, err := lockCheckCounter.Get(ctx)
	var notFound *dex.AttributeNotFoundError
	if err != nil && !errors.As(err, &notFound) {
		return nil, err
	}
	// Reading one Attribute to compute a write of another is not a read-modify-write.
	if err := lockCheckOther.Set(ctx, count+10); err != nil {
		return nil, err
	}
	// A read after the invocation's own write sees that write, not the snapshot.
	other, err := lockCheckOther.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := lockCheckOther.Set(ctx, other+1); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

// attributeLockCheckRPCFlow has no Steps, so it stays running for RPC cases.
type attributeLockCheckRPCFlow struct {
	dex.FlowDefaults
}

func (attributeLockCheckRPCFlow) GetSteps() []dex.StepDef {
	return nil
}

func (flow attributeLockCheckRPCFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.UnlockedIncrement, nil),
		dex.DefineRPC(flow.LockedIncrement, &dex.RPCOptions{
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(lockCheckCounter)},
		}),
		dex.DefineRPC(flow.RegisteredLoadEntryIncrement, &dex.RPCOptions{
			LoadAttributeMaps: []dex.AttributeDef{lockCheckEntries},
		}),
		dex.DefineRPC(flow.RuntimeSelectedEntryIncrement, nil),
	}
}

func (attributeLockCheckRPCFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{lockCheckCounter, lockCheckOther, lockCheckEntries},
	}
}

func (attributeLockCheckRPCFlow) UnlockedIncrement(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, lockCheckIncrement(ctx)
}

func (attributeLockCheckRPCFlow) LockedIncrement(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, lockCheckIncrement(ctx)
}

func (attributeLockCheckRPCFlow) RegisteredLoadEntryIncrement(
	ctx dex.Context,
	instance string,
) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, lockCheckIncrementEntry(ctx, instance)
}

func (attributeLockCheckRPCFlow) RuntimeSelectedEntryIncrement(
	ctx dex.Context,
	instance string,
) (*dex.RPCResult[dex.None], error) {
	return &dex.RPCResult[dex.None]{}, lockCheckIncrementEntry(ctx, instance)
}

type lockCheckWarning struct {
	handler   string
	attribute string
}

// lockCheckLogger records the Worker's unlocked read-modify-write warnings.
type lockCheckLogger struct {
	mu       sync.Mutex
	warnings []lockCheckWarning
}

func (*lockCheckLogger) Debug(string, ...interface{}) {}

func (*lockCheckLogger) Info(string, ...interface{}) {}

func (*lockCheckLogger) Error(string, ...interface{}) {}

func (logger *lockCheckLogger) Warn(msg string, keyvals ...interface{}) {
	if !strings.HasPrefix(msg, lockCheckWarningPrefix) {
		return
	}
	warning := lockCheckWarning{}
	for i := 0; i+1 < len(keyvals); i += 2 {
		value, _ := keyvals[i+1].(string)
		switch keyvals[i] {
		case "handler":
			warning.handler = value
		case "attribute":
			warning.attribute = value
		}
	}
	logger.mu.Lock()
	defer logger.mu.Unlock()
	logger.warnings = append(logger.warnings, warning)
}

func (logger *lockCheckLogger) hasWarning(handler string, attribute string) bool {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	for _, warning := range logger.warnings {
		if warning.handler == handler && warning.attribute == attribute {
			return true
		}
	}
	return false
}

func lockCheckRegistry(t *testing.T) *dex.Registry {
	t.Helper()
	registry, err := dex.NewRegistry([]dex.Flow{attributeLockCheckStepFlow{}, attributeLockCheckRPCFlow{}})
	require.NoError(t, err)
	return registry
}

func startLockCheckWorker(t *testing.T, taskQueue string, isStrict bool, logger dex.Logger) *dex.Worker {
	t.Helper()
	worker, err := dex.NewWorker(lockCheckRegistry(t), newPullSDKCache(t), dex.WorkerOptions{
		FlowServiceAddress:                     flowServiceAddress(),
		Pull:                                   &dex.PullWorkerOptions{MaxConcurrentTasks: 4, TaskQueue: taskQueue},
		Logger:                                 logger,
		FailOnUnlockedAttributeReadModifyWrite: isStrict,
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
	return worker
}

type lockCheckWorkers struct {
	client       *dex.Client
	warnQueue    string
	warnWorker   *dex.Worker
	warnLogger   *lockCheckLogger
	strictQueue  string
	strictWorker *dex.Worker
}

func startLockCheckWorkers(t *testing.T) lockCheckWorkers {
	t.Helper()
	workers := lockCheckWorkers{
		warnQueue:   "lock-check-warn-" + uuid.NewString()[:8],
		warnLogger:  &lockCheckLogger{},
		strictQueue: "lock-check-strict-" + uuid.NewString()[:8],
	}
	workers.warnWorker = startLockCheckWorker(t, workers.warnQueue, false, workers.warnLogger)
	workers.strictWorker = startLockCheckWorker(t, workers.strictQueue, true, nil)
	client, err := dex.NewClient(lockCheckRegistry(t), newPullSDKCache(t), dex.ClientOptions{
		FlowServiceAddress: flowServiceAddress(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	workers.client = client
	return workers
}

func (workers lockCheckWorkers) runStepCase(t *testing.T, taskQueue string, stepCase string) dex.FlowResult {
	t.Helper()
	ctx := integrationContext(t)
	flowID := newFlowID(t, "lock-check-"+stepCase)
	_, err := workers.client.StartFlow(ctx, attributeLockCheckStepFlow{}, flowID, stepCase,
		dex.StartFlowOptions{TaskQueue: taskQueue})
	require.NoError(t, err)
	result, err := workers.client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{})
	require.NoError(t, err)
	return result
}

// TestUnlockedAttributeReadModifyWriteStepDetection covers Step methods in warning and strict mode.
func TestUnlockedAttributeReadModifyWriteStepDetection(t *testing.T) {
	workers := startLockCheckWorkers(t)
	cases := []struct {
		stepCase   string
		isReported bool
		handler    string
		attribute  string
	}{
		{stepCase: "unlocked", isReported: true, handler: "lockCheckUnlockedStep.Execute", attribute: "lock-check-counter"},
		{stepCase: "unlocked-wait-for", isReported: true, handler: "lockCheckUnlockedWaitForStep.WaitFor", attribute: "lock-check-counter"},
		{stepCase: "unlocked-map", isReported: true, handler: "lockCheckUnlockedMapStep.Execute", attribute: "lock-check-entries"},
		{stepCase: "locked"},
		{stepCase: "locked-map"},
		{stepCase: "single-writer"},
		{stepCase: "read-or-write-only"},
	}
	for _, testCase := range cases {
		t.Run(testCase.stepCase+"/warn", func(t *testing.T) {
			before := workers.warnWorker.UnlockedAttributeReadModifyWriteCount()
			result := workers.runStepCase(t, workers.warnQueue, testCase.stepCase)
			require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
			detections := workers.warnWorker.UnlockedAttributeReadModifyWriteCount() - before
			if testCase.isReported {
				require.Equal(t, uint64(1), detections)
				require.True(t, workers.warnLogger.hasWarning(testCase.handler, testCase.attribute))
			} else {
				require.Zero(t, detections)
			}
		})
		t.Run(testCase.stepCase+"/strict", func(t *testing.T) {
			before := workers.strictWorker.UnlockedAttributeReadModifyWriteCount()
			result := workers.runStepCase(t, workers.strictQueue, testCase.stepCase)
			detections := workers.strictWorker.UnlockedAttributeReadModifyWriteCount() - before
			if testCase.isReported {
				require.Equal(t, dex.FlowFailed, result.Status)
				require.Equal(t, uint64(1), detections)
			} else {
				require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
				require.Zero(t, detections)
			}
		})
	}
}

// TestUnlockedAttributeReadModifyWriteRPCDetection covers RPCs, including a runtime-selected map lock.
func TestUnlockedAttributeReadModifyWriteRPCDetection(t *testing.T) {
	workers := startLockCheckWorkers(t)
	flow := attributeLockCheckRPCFlow{}
	invokeOnQueue := func(t *testing.T, taskQueue string, invoke func(ctx context.Context, flowID string) error) error {
		t.Helper()
		ctx := integrationContext(t)
		flowID := newFlowID(t, "lock-check-rpc")
		_, err := workers.client.StartFlow(ctx, flow, flowID, nil, dex.StartFlowOptions{TaskQueue: taskQueue})
		require.NoError(t, err)
		return invoke(ctx, flowID)
	}
	cases := []struct {
		name       string
		isReported bool
		handler    string
		attribute  string
		invoke     func(ctx context.Context, flowID string) error
	}{
		{
			name: "unlocked", isReported: true, handler: "RPC.UnlockedIncrement", attribute: "lock-check-counter",
			invoke: func(ctx context.Context, flowID string) error {
				return workers.client.InvokeRPC(ctx, flowID, flow.UnlockedIncrement, nil, new(dex.None))
			},
		},
		{
			name: "registered-map-load", isReported: true, handler: "RPC.RegisteredLoadEntryIncrement", attribute: "lock-check-entries",
			invoke: func(ctx context.Context, flowID string) error {
				return workers.client.InvokeRPC(ctx, flowID, flow.RegisteredLoadEntryIncrement, "e1", new(dex.None))
			},
		},
		{
			name: "locked",
			invoke: func(ctx context.Context, flowID string) error {
				return workers.client.InvokeRPC(ctx, flowID, flow.LockedIncrement, nil, new(dex.None))
			},
		},
		{
			name: "runtime-selected-map-lock",
			invoke: func(ctx context.Context, flowID string) error {
				return workers.client.InvokeRPCWithOptions(ctx, flowID, flow.RuntimeSelectedEntryIncrement, "e1",
					new(dex.None), dex.RPCInvokeOptions{
						LockAttributeMapInstances: []dex.AttributeLock{dex.LockAttributeMap(lockCheckEntries, "e1")},
						LoadAttributeMapInstances: []dex.AttributeMapLoad{lockCheckEntries.Load("e1")},
					})
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name+"/warn", func(t *testing.T) {
			before := workers.warnWorker.UnlockedAttributeReadModifyWriteCount()
			require.NoError(t, invokeOnQueue(t, workers.warnQueue, testCase.invoke))
			detections := workers.warnWorker.UnlockedAttributeReadModifyWriteCount() - before
			if testCase.isReported {
				require.Equal(t, uint64(1), detections)
				require.True(t, workers.warnLogger.hasWarning(testCase.handler, testCase.attribute))
			} else {
				require.Zero(t, detections)
			}
		})
		t.Run(testCase.name+"/strict", func(t *testing.T) {
			before := workers.strictWorker.UnlockedAttributeReadModifyWriteCount()
			err := invokeOnQueue(t, workers.strictQueue, testCase.invoke)
			detections := workers.strictWorker.UnlockedAttributeReadModifyWriteCount() - before
			if !testCase.isReported {
				require.NoError(t, err)
				require.Zero(t, detections)
				return
			}
			require.Equal(t, uint64(1), detections)
			var invocationErr *dex.WorkerInvocationError
			require.ErrorAs(t, err, &invocationErr)
			require.NotNil(t, invocationErr.Worker)
			require.Equal(t, "*dex.UnlockedAttributeReadModifyWriteError", invocationErr.Worker.Type)
			require.Contains(t, invocationErr.Worker.Detail, testCase.attribute)
		})
	}
}
