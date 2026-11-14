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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var taskQueueRelease = dex.DefineChannel[string]("task-queue-release")

// taskQueueRoutingFlow reports which Worker ran its RPC and its Step.
type taskQueueRoutingFlow struct {
	dex.FlowDefaults
	workerID string
}

type taskQueueRoutingResult struct {
	RPCWorkerID  string
	StepWorkerID string
}

func (flow taskQueueRoutingFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(taskQueueRoutingStep{workerID: flow.workerID})}
}

func (flow taskQueueRoutingFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.ReleaseWithWorkerID, nil)}
}

func (taskQueueRoutingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{taskQueueRelease}}
}

func (flow taskQueueRoutingFlow) ReleaseWithWorkerID(ctx dex.Context, _ dex.None) (*dex.RPCResult[string], error) {
	if err := taskQueueRelease.Publish(ctx, flow.workerID); err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: flow.workerID}, nil
}

type taskQueueRoutingStep struct {
	dex.StepDefaults
	workerID string
}

func (taskQueueRoutingStep) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.AllOf(taskQueueRelease.ForOne()), nil
}

func (step taskQueueRoutingStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	released, err := taskQueueRelease.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(released) != 1 {
		return nil, fmt.Errorf("expected one release message, got %d", len(released))
	}
	return dex.GracefulComplete(taskQueueRoutingResult{RPCWorkerID: released[0], StepWorkerID: step.workerID}), nil
}

// taskQueueParentFlow starts a SubFlow on the task queue named by its input.
type taskQueueParentFlow struct {
	emptyFlowSchema
	workerID string
}

type taskQueueParentResult struct {
	ParentWorkerID string
	ChildWorkerID  string
}

func (flow taskQueueParentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(taskQueueParentStep{workerID: flow.workerID})}
}

type taskQueueParentStep struct {
	dex.StepDefaults
	workerID string
}

func (taskQueueParentStep) WaitFor(_ dex.Context, childTaskQueue string) (*dex.Wait, error) {
	return dex.AllOf(dex.SubFlow(taskQueueChildFlow{}, nil, dex.SubFlowOptions{TaskQueue: childTaskQueue})), nil
}

func (step taskQueueParentStep) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
	childResult, err := dex.SubFlowResult(ctx)
	if err != nil {
		return nil, err
	}
	var childWorkerID string
	if err := childResult.DecodeSingleOutput(&childWorkerID); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(taskQueueParentResult{ParentWorkerID: step.workerID, ChildWorkerID: childWorkerID}), nil
}

type taskQueueChildFlow struct {
	emptyFlowSchema
	workerID string
}

func (flow taskQueueChildFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(taskQueueChildStep{workerID: flow.workerID})}
}

type taskQueueChildStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
	workerID string
}

func (step taskQueueChildStep) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete(step.workerID), nil
}

func TestTaskQueueRoutesFlowsToTheirWorkers(t *testing.T) {
	queueA := "go-a-" + uuid.NewString()
	queueB := "go-b-" + uuid.NewString()
	workerAID := "tq-go-a-" + uuid.NewString()
	workerBID := "tq-go-b-" + uuid.NewString()
	workerDefaultID := "tq-go-d-" + uuid.NewString()
	workerA := startTaskQueueWorker(t, workerAID, queueA)
	workerB := startTaskQueueWorker(t, workerBID, queueB)
	workerDefault := startTaskQueueWorker(t, workerDefaultID, "")
	for worker, taskQueue := range map[*dex.Worker]string{workerA: queueA, workerB: queueB, workerDefault: ""} {
		require.NoError(t, worker.WaitPullRegistered(integrationContext(t)))
		for _, queue := range worker.PullRegistrationState().Queues {
			require.Equal(t, taskQueue, queue.TaskQueue)
		}
	}
	client := newTaskQueueClient(t)

	for _, scenario := range []struct {
		name         string
		taskQueue    string
		wantWorkerID string
	}{
		{name: "named task queue A", taskQueue: queueA, wantWorkerID: workerAID},
		{name: "named task queue B", taskQueue: queueB, wantWorkerID: workerBID},
		{name: "default task queue", taskQueue: "", wantWorkerID: workerDefaultID},
		{name: "default task queue alias", taskQueue: dex.DefaultTaskQueue, wantWorkerID: workerDefaultID},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for range 3 {
				ctx := integrationContext(t)
				flowID := newFlowID(t, "task-queue-go")
				_, err := client.StartFlow(ctx, taskQueueRoutingFlow{}, flowID, nil, dex.StartFlowOptions{TaskQueue: scenario.taskQueue})
				require.NoError(t, err)
				var rpcWorkerID string
				require.NoError(t, client.InvokeRPC(ctx, flowID, taskQueueRoutingFlow{}.ReleaseWithWorkerID, nil, &rpcWorkerID))
				require.Equal(t, scenario.wantWorkerID, rpcWorkerID)
				result, err := client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
				require.NoError(t, err)
				require.Equal(t, dex.FlowCompleted, result.Status)
				var output taskQueueRoutingResult
				require.NoError(t, result.DecodeSingleOutput(&output))
				require.Equal(t, taskQueueRoutingResult{RPCWorkerID: scenario.wantWorkerID, StepWorkerID: scenario.wantWorkerID}, output)
			}
		})
	}

	for _, scenario := range []struct {
		name              string
		childTaskQueue    string
		wantChildWorkerID string
	}{
		{name: "SubFlow inherits the parent task queue", childTaskQueue: "", wantChildWorkerID: workerAID},
		{name: "SubFlow selects the default task queue", childTaskQueue: dex.DefaultTaskQueue, wantChildWorkerID: workerDefaultID},
		{name: "SubFlow selects another named task queue", childTaskQueue: queueB, wantChildWorkerID: workerBID},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := integrationContext(t)
			flowID := newFlowID(t, "task-queue-go-parent")
			_, err := client.StartFlow(ctx, taskQueueParentFlow{}, flowID, scenario.childTaskQueue, dex.StartFlowOptions{TaskQueue: queueA})
			require.NoError(t, err)
			result, err := client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
			require.NoError(t, err)
			require.Equal(t, dex.FlowCompleted, result.Status)
			var output taskQueueParentResult
			require.NoError(t, result.DecodeSingleOutput(&output))
			require.Equal(t, taskQueueParentResult{ParentWorkerID: workerAID, ChildWorkerID: scenario.wantChildWorkerID}, output)
		})
	}
}

func TestTaskQueueFlowWaitsForItsWorker(t *testing.T) {
	queue := "go-late-" + uuid.NewString()
	client := newTaskQueueClient(t)
	ctx := integrationContext(t)
	flowID := newFlowID(t, "task-queue-go-late")
	_, err := client.StartFlow(ctx, taskQueueChildFlow{}, flowID, nil, dex.StartFlowOptions{TaskQueue: queue})
	require.NoError(t, err)
	// Other Workers of the Flow type, on the default task queue, must not run it.
	startTaskQueueWorker(t, "tq-go-other-"+uuid.NewString(), "")
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err = client.WaitForFlow(waitCtx, flowID, dex.WaitForFlowOptions{})
	require.True(t, errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded, "want a deadline error, got %v", err)
	lateWorkerID := "tq-go-late-" + uuid.NewString()
	startTaskQueueWorker(t, lateWorkerID, queue)
	result, err := client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	var workerID string
	require.NoError(t, result.DecodeSingleOutput(&workerID))
	require.Equal(t, lateWorkerID, workerID)
}

func TestTaskQueueRejectsInvalidNames(t *testing.T) {
	registry, err := dex.NewRegistry([]dex.Flow{taskQueueChildFlow{}})
	require.NoError(t, err)
	for _, invalid := range []string{dex.DefaultTaskQueue, "dex.other", "has space"} {
		_, err := dex.NewWorker(registry, newPullSDKCache(t), dex.WorkerOptions{
			FlowServiceAddress: flowServiceAddress(),
			Pull:               &dex.PullWorkerOptions{TaskQueue: invalid},
		})
		require.ErrorContains(t, err, "task queue", invalid)
	}
	client := newTaskQueueClient(t)
	for _, invalid := range []string{"dex.other", "has space"} {
		_, err := client.StartFlow(integrationContext(t), taskQueueChildFlow{}, newFlowID(t, "task-queue-go-invalid"), nil, dex.StartFlowOptions{TaskQueue: invalid})
		require.ErrorContains(t, err, "task queue", invalid)
	}
}

func startTaskQueueWorker(t *testing.T, workerID string, taskQueue string) *dex.Worker {
	t.Helper()
	registry, err := dex.NewRegistry([]dex.Flow{
		taskQueueRoutingFlow{workerID: workerID},
		taskQueueParentFlow{workerID: workerID},
		taskQueueChildFlow{workerID: workerID},
	})
	require.NoError(t, err)
	worker, err := dex.NewWorker(registry, newPullSDKCache(t), dex.WorkerOptions{
		FlowServiceAddress: flowServiceAddress(),
		Pull:               &dex.PullWorkerOptions{AppWorkerID: workerID, MaxConcurrentTasks: 4, TaskQueue: taskQueue},
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

func newTaskQueueClient(t *testing.T) *dex.Client {
	t.Helper()
	registry, err := dex.NewRegistry([]dex.Flow{taskQueueRoutingFlow{}, taskQueueParentFlow{}, taskQueueChildFlow{}})
	require.NoError(t, err)
	client, err := dex.NewClient(registry, newPullSDKCache(t), dex.ClientOptions{FlowServiceAddress: flowServiceAddress()})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client
}
