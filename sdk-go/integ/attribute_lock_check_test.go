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
		dex.DefineStep(lockCheckExactInstanceStep{}),
		dex.DefineStep(lockCheckReadThenDeleteStep{}),
		dex.DefineStep(lockCheckUnloadedMapReadStep{}),
		dex.DefineStep(lockCheckConcurrentReadStep{}),
		dex.DefineStep(lockCheckHandlerErrorStep{}),
		dex.DefineStep(lockCheckRetriedStep{}),
		dex.DefineStep(lockCheckRecoveryStep{}),
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
	case "unlocked-exact-instance":
		return dex.GoTo[dex.None](lockCheckExactInstanceStep{}, nil), nil
	case "read-then-delete":
		return dex.GoTo[dex.None](lockCheckReadThenDeleteStep{}, nil), nil
	case "unloaded-map-read":
		return dex.GoTo[dex.None](lockCheckUnloadedMapReadStep{}, nil), nil
	case "concurrent-reads":
		return dex.GoTo[dex.None](lockCheckConcurrentReadStep{}, nil), nil
	case "handler-error":
		return dex.GoTo[dex.None](lockCheckHandlerErrorStep{}, nil), nil
	case "retried":
		return dex.GoTo[dex.None](lockCheckRetriedStep{}, nil), nil
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

type lockCheckExactInstanceStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockCheckExactInstanceStep) GetStepOptions() *dex.StepOptions {
	options := lockCheckSingleAttemptOptions()
	options.ExecuteLoadAttributeMapInstances = []dex.AttributeMapLoad{lockCheckEntries.Load("e1")}
	return options
}

func (lockCheckExactInstanceStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := lockCheckIncrementEntry(ctx, "e1"); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

type lockCheckReadThenDeleteStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockCheckReadThenDeleteStep) GetStepOptions() *dex.StepOptions {
	return lockCheckSingleAttemptOptions()
}

func (lockCheckReadThenDeleteStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	var notFound *dex.AttributeNotFoundError
	if _, err := lockCheckCounter.Get(ctx); err != nil && !errors.As(err, &notFound) {
		return nil, err
	}
	if err := lockCheckCounter.Delete(ctx); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

// lockCheckUnloadedMapReadStep reads an instance it did not load; that failed read is not a snapshot read.
type lockCheckUnloadedMapReadStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockCheckUnloadedMapReadStep) GetStepOptions() *dex.StepOptions {
	return lockCheckSingleAttemptOptions()
}

func (lockCheckUnloadedMapReadStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	var notLoaded *dex.AttributeMapNotLoadedError
	if _, err := lockCheckEntries.Get(ctx, "e1"); !errors.As(err, &notLoaded) {
		return nil, fmt.Errorf("expected AttributeMapNotLoadedError, got %v", err)
	}
	if err := lockCheckEntries.Set(ctx, "e1", 1); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

// lockCheckConcurrentReadStep reads from two goroutines; run it with -race.
type lockCheckConcurrentReadStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockCheckConcurrentReadStep) GetStepOptions() *dex.StepOptions {
	options := lockCheckSingleAttemptOptions()
	options.ExecuteLoadAttributeMaps = []dex.AttributeDef{lockCheckEntries}
	return options
}

func (lockCheckConcurrentReadStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	var group sync.WaitGroup
	errs := make([]error, 2)
	for reader := range errs {
		group.Add(1)
		go func() {
			defer group.Done()
			var notFound *dex.AttributeNotFoundError
			for i := 0; i < 50; i++ {
				if _, err := lockCheckCounter.Get(ctx); err != nil && !errors.As(err, &notFound) {
					errs[reader] = err
					return
				}
				instance := fmt.Sprintf("e%d", i%5)
				if _, err := lockCheckEntries.Get(ctx, instance); err != nil && !errors.As(err, &notFound) {
					errs[reader] = err
					return
				}
			}
		}()
	}
	group.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

type lockCheckHandlerErrorStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockCheckHandlerErrorStep) GetStepOptions() *dex.StepOptions {
	return lockCheckSingleAttemptOptions()
}

func (lockCheckHandlerErrorStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := lockCheckIncrement(ctx); err != nil {
		return nil, err
	}
	return nil, errors.New("lock check handler error")
}

// lockCheckRetriedStep fails in strict mode on both attempts, then routes to the recovery Step.
type lockCheckRetriedStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockCheckRetriedStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry: &dex.RetryPolicy{
			InitialInterval: time.Second,
			MaximumInterval: time.Second,
			MaximumAttempts: 2,
		},
		ExecuteFailure: dex.ProceedToOnExecuteFailure[dex.None](lockCheckRecoveryStep{}, nil),
	}
}

func (lockCheckRetriedStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := lockCheckIncrement(ctx); err != nil {
		return nil, err
	}
	return dex.GracefulComplete("first attempt succeeded"), nil
}

type lockCheckRecoveryStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (lockCheckRecoveryStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	recovery := ctx.RecoveryError()
	if recovery == nil {
		return nil, errors.New("recovery Step has no recovery error")
	}
	return dex.GracefulComplete(recovery.ErrorType), nil
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
		dex.DefineRPC(flow.IncrementEntriesExceptE2, &dex.RPCOptions{
			LoadAttributeMaps: []dex.AttributeDef{lockCheckEntries},
		}),
	}
}

// IncrementEntriesExceptE2 increments every instance and declares itself the single writer of "e2".
func (attributeLockCheckRPCFlow) IncrementEntriesExceptE2(
	ctx dex.Context,
	instances []string,
) (*dex.RPCResult[dex.None], error) {
	for _, instance := range instances {
		if err := lockCheckIncrementEntry(ctx, instance); err != nil {
			return nil, err
		}
	}
	return &dex.RPCResult[dex.None]{}, lockCheckEntries.DeclareSingleWriter(ctx, "e2")
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

func (logger *lockCheckLogger) warningCount(handler string, attribute string) int {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	count := 0
	for _, warning := range logger.warnings {
		if warning.handler == handler && warning.attribute == attribute {
			count++
		}
	}
	return count
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

func (workers lockCheckWorkers) runStepCase(
	t *testing.T,
	taskQueue string,
	stepCase string,
) (string, dex.FlowResult) {
	t.Helper()
	ctx := integrationContext(t)
	flowID := newFlowID(t, "lock-check-"+stepCase)
	_, err := workers.client.StartFlow(ctx, attributeLockCheckStepFlow{}, flowID, stepCase,
		dex.StartFlowOptions{TaskQueue: taskQueue})
	require.NoError(t, err)
	result, err := workers.client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	return flowID, result
}

func (workers lockCheckWorkers) readCounter(t *testing.T, flowID string) (int, bool) {
	t.Helper()
	counter := -1
	result, err := workers.client.GetAttributes(integrationContext(t), flowID,
		dex.ReadAttribute(lockCheckCounter, &counter))
	require.NoError(t, err)
	return counter, result.IsSet(lockCheckCounter)
}

// TestUnlockedAttributeReadModifyWriteStepDetection covers Step methods in warning and strict mode.
func TestUnlockedAttributeReadModifyWriteStepDetection(t *testing.T) {
	workers := startLockCheckWorkers(t)
	cases := []struct {
		stepCase   string
		isReported bool
		isFailing  bool
		handler    string
		attribute  string
	}{
		{stepCase: "unlocked", isReported: true, handler: "lockCheckUnlockedStep.Execute", attribute: "lock-check-counter"},
		{stepCase: "unlocked-wait-for", isReported: true, handler: "lockCheckUnlockedWaitForStep.WaitFor", attribute: "lock-check-counter"},
		{stepCase: "unlocked-map", isReported: true, handler: "lockCheckUnlockedMapStep.Execute", attribute: "lock-check-entries"},
		{stepCase: "unlocked-exact-instance", isReported: true, handler: "lockCheckExactInstanceStep.Execute", attribute: "lock-check-entries"},
		{stepCase: "read-then-delete", isReported: true, handler: "lockCheckReadThenDeleteStep.Execute", attribute: "lock-check-counter"},
		{stepCase: "locked"},
		{stepCase: "locked-map"},
		{stepCase: "single-writer"},
		{stepCase: "read-or-write-only"},
		{stepCase: "unloaded-map-read"},
		{stepCase: "concurrent-reads"},
		{stepCase: "handler-error", isFailing: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.stepCase+"/warn", func(t *testing.T) {
			before := workers.warnWorker.UnlockedAttributeReadModifyWriteCount()
			_, result := workers.runStepCase(t, workers.warnQueue, testCase.stepCase)
			if testCase.isFailing {
				require.Equal(t, dex.FlowFailed, result.Status)
			} else {
				require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
			}
			detections := workers.warnWorker.UnlockedAttributeReadModifyWriteCount() - before
			if testCase.isReported {
				require.Equal(t, uint64(1), detections)
				require.Equal(t, 1, workers.warnLogger.warningCount(testCase.handler, testCase.attribute))
			} else {
				require.Zero(t, detections)
			}
		})
		t.Run(testCase.stepCase+"/strict", func(t *testing.T) {
			before := workers.strictWorker.UnlockedAttributeReadModifyWriteCount()
			_, result := workers.runStepCase(t, workers.strictQueue, testCase.stepCase)
			detections := workers.strictWorker.UnlockedAttributeReadModifyWriteCount() - before
			if testCase.isReported || testCase.isFailing {
				require.Equal(t, dex.FlowFailed, result.Status)
			} else {
				require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
			}
			if testCase.isReported {
				require.Equal(t, uint64(1), detections)
			} else {
				require.Zero(t, detections)
			}
		})
	}
}

// TestUnlockedAttributeReadModifyWriteWarnsOnceAndCommits repeats one handler on one warning-mode Worker.
func TestUnlockedAttributeReadModifyWriteWarnsOnceAndCommits(t *testing.T) {
	workers := startLockCheckWorkers(t)
	for run := 1; run <= 3; run++ {
		before := workers.warnWorker.UnlockedAttributeReadModifyWriteCount()
		flowID, result := workers.runStepCase(t, workers.warnQueue, "unlocked")
		require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
		require.Equal(t, uint64(1), workers.warnWorker.UnlockedAttributeReadModifyWriteCount()-before)
		counter, isSet := workers.readCounter(t, flowID)
		require.True(t, isSet, "warning mode commits the write")
		require.Equal(t, 1, counter)
	}
	require.Equal(t, 1, workers.warnLogger.warningCount("lockCheckUnlockedStep.Execute", "lock-check-counter"))
}

// lockCheckPanickingLogger panics on the check's warning, like a broken application Logger.
type lockCheckPanickingLogger struct {
	lockCheckLogger
}

func (logger *lockCheckPanickingLogger) Warn(msg string, keyvals ...interface{}) {
	if strings.HasPrefix(msg, lockCheckWarningPrefix) {
		panic("lock check logger failure")
	}
}

// TestUnlockedAttributeReadModifyWriteLoggerPanicKeepsSuccess shows a failing Logger cannot fail the invocation.
func TestUnlockedAttributeReadModifyWriteLoggerPanicKeepsSuccess(t *testing.T) {
	workers := startLockCheckWorkers(t)
	taskQueue := "lock-check-panic-" + uuid.NewString()[:8]
	worker := startLockCheckWorker(t, taskQueue, false, &lockCheckPanickingLogger{})
	flowID, result := workers.runStepCase(t, taskQueue, "unlocked")
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	require.Equal(t, uint64(1), worker.UnlockedAttributeReadModifyWriteCount())
	counter, isSet := workers.readCounter(t, flowID)
	require.True(t, isSet)
	require.Equal(t, 1, counter)
}

// TestUnlockedAttributeReadModifyWriteStrictRetriesThenRecovers checks each attempt and the recovery error type.
func TestUnlockedAttributeReadModifyWriteStrictRetriesThenRecovers(t *testing.T) {
	workers := startLockCheckWorkers(t)
	before := workers.strictWorker.UnlockedAttributeReadModifyWriteCount()
	flowID, result := workers.runStepCase(t, workers.strictQueue, "retried")
	require.Equal(t, dex.FlowCompleted, result.Status, result.ErrorMessage)
	require.Equal(t, uint64(2), workers.strictWorker.UnlockedAttributeReadModifyWriteCount()-before)
	var recoveryErrorType string
	require.NoError(t, result.DecodeSingleOutput(&recoveryErrorType))
	require.Equal(t, "*dex.UnlockedAttributeReadModifyWriteError", recoveryErrorType)
	_, isSet := workers.readCounter(t, flowID)
	require.False(t, isSet, "strict mode discards the failed attempts' writes")
}

// TestUnlockedAttributeReadModifyWriteRPCDetection covers RPCs, including a runtime-selected map lock.
func TestUnlockedAttributeReadModifyWriteRPCDetection(t *testing.T) {
	workers := startLockCheckWorkers(t)
	flow := attributeLockCheckRPCFlow{}
	invokeOnQueue := func(
		t *testing.T,
		taskQueue string,
		invoke func(ctx context.Context, flowID string) error,
	) (string, error) {
		t.Helper()
		ctx := integrationContext(t)
		flowID := newFlowID(t, "lock-check-rpc")
		_, err := workers.client.StartFlow(ctx, flow, flowID, nil, dex.StartFlowOptions{TaskQueue: taskQueue})
		require.NoError(t, err)
		return flowID, invoke(ctx, flowID)
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
			_, err := invokeOnQueue(t, workers.warnQueue, testCase.invoke)
			require.NoError(t, err)
			detections := workers.warnWorker.UnlockedAttributeReadModifyWriteCount() - before
			if testCase.isReported {
				require.Equal(t, uint64(1), detections)
				require.Equal(t, 1, workers.warnLogger.warningCount(testCase.handler, testCase.attribute))
			} else {
				require.Zero(t, detections)
			}
		})
		t.Run(testCase.name+"/strict", func(t *testing.T) {
			before := workers.strictWorker.UnlockedAttributeReadModifyWriteCount()
			flowID, err := invokeOnQueue(t, workers.strictQueue, testCase.invoke)
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
			_, isSet := workers.readCounter(t, flowID)
			require.False(t, isSet, "strict mode discards the RPC's writes")
		})
	}

	t.Run("several-map-instances", func(t *testing.T) {
		instances := []string{"e3", "e1", "e2"}
		invoke := func(ctx context.Context, flowID string) error {
			return workers.client.InvokeRPC(ctx, flowID, flow.IncrementEntriesExceptE2, instances, new(dex.None))
		}
		for run := 0; run < 2; run++ {
			before := workers.warnWorker.UnlockedAttributeReadModifyWriteCount()
			_, err := invokeOnQueue(t, workers.warnQueue, invoke)
			require.NoError(t, err)
			require.Equal(t, uint64(2), workers.warnWorker.UnlockedAttributeReadModifyWriteCount()-before)
		}
		require.Equal(t, 1, workers.warnLogger.warningCount("RPC.IncrementEntriesExceptE2", "lock-check-entries"))

		before := workers.strictWorker.UnlockedAttributeReadModifyWriteCount()
		_, err := invokeOnQueue(t, workers.strictQueue, invoke)
		require.Equal(t, uint64(2), workers.strictWorker.UnlockedAttributeReadModifyWriteCount()-before)
		var invocationErr *dex.WorkerInvocationError
		require.ErrorAs(t, err, &invocationErr)
		detail := invocationErr.Worker.Detail
		require.NotContains(t, detail, `instance "e2"`)
		firstIndex := strings.Index(detail, `instance "e1"`)
		thirdIndex := strings.Index(detail, `instance "e3"`)
		require.GreaterOrEqual(t, firstIndex, 0, detail)
		require.Greater(t, thirdIndex, firstIndex, "instances are sorted by stored key")
	})
}
