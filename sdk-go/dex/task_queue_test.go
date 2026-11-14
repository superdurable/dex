// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func taskQueueServerInfo(currentProtocolVersion uint32) *dexpb.ServerInfo {
	return &dexpb.ServerInfo{ServerVersion: "test", MinimumSupportedProtocolVersion: 2, CurrentProtocolVersion: currentProtocolVersion}
}

func TestTaskQueueNameRules(t *testing.T) {
	for _, valid := range []string{"", "env-1.A_b", strings.Repeat("q", 63)} {
		require.NoError(t, validateRegisteredTaskQueueName(valid), valid)
		require.NoError(t, validateTargetTaskQueueName(valid), valid)
	}
	for _, invalid := range []string{strings.Repeat("q", 64), "has space", "ü", "dex.other"} {
		require.Error(t, validateRegisteredTaskQueueName(invalid), invalid)
		require.Error(t, validateTargetTaskQueueName(invalid), invalid)
	}
	require.Error(t, validateRegisteredTaskQueueName(DefaultTaskQueue))
	require.NoError(t, validateTargetTaskQueueName(DefaultTaskQueue))
}

func TestNewWorkerRejectsInvalidTaskQueue(t *testing.T) {
	for _, invalid := range []string{DefaultTaskQueue, "dex.other", strings.Repeat("q", 64), "has space"} {
		_, err := newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{Pull: &PullWorkerOptions{TaskQueue: invalid}})
		require.ErrorContains(t, err, "task queue", invalid)
	}
}

func TestWorkerStartRefusesTaskQueueOnProtocolBeforeFive(t *testing.T) {
	worker, _, result := startRegistrationTestWorker(t, PullWorkerOptions{TaskQueue: "tenant-a"}, &workerSyncFlowService{serverInfo: taskQueueServerInfo(4)})
	go func() { result <- worker.Start() }()
	startErr := <-result
	var unsupported *TaskQueueUnsupportedError
	require.ErrorAs(t, startErr, &unsupported)
	require.Equal(t, "tenant-a", unsupported.TaskQueue)
	require.Equal(t, uint32(4), unsupported.NegotiatedServerProtocol)
	require.Equal(t, uint32(5), unsupported.RequiredServerProtocolVersion)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.ErrorAs(t, worker.WaitPullRegistered(ctx), &unsupported)
}

func TestWorkerDefaultTaskQueueStartsOnProtocolFour(t *testing.T) {
	worker, server, result := startRegistrationTestWorker(t, PullWorkerOptions{}, &workerSyncFlowService{serverInfo: taskQueueServerInfo(4)})
	go func() { result <- worker.Start() }()
	for range 2 {
		stream := awaitRegistrationTestStream(t, server, 5*time.Second)
		require.Empty(t, stream.taskQueue)
		stream.register()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, worker.WaitPullRegistered(ctx))
}

func TestWorkerRegistersOnTaskQueue(t *testing.T) {
	worker, server, result := startRegistrationTestWorker(t, PullWorkerOptions{TaskQueue: "tenant-a"}, &workerSyncFlowService{serverInfo: taskQueueServerInfo(5)})
	go func() { result <- worker.Start() }()
	for range 2 {
		stream := awaitRegistrationTestStream(t, server, 5*time.Second)
		require.Equal(t, "tenant-a", stream.taskQueue)
		stream.registerOnTaskQueue("tenant-a")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, worker.WaitPullRegistered(ctx))
	state := worker.PullRegistrationState()
	require.True(t, state.IsReady)
	require.Len(t, state.Queues, 2)
	for _, queue := range state.Queues {
		require.Equal(t, "tenant-a", queue.TaskQueue)
		require.True(t, queue.HasRegisteredStream)
	}
}

func TestWorkerRejectsTaskQueueEchoMismatch(t *testing.T) {
	for _, scenario := range []struct {
		name               string
		requestedTaskQueue string
		registeredQueue    string
	}{
		{name: "older Server ignores the named task queue", requestedTaskQueue: "tenant-a", registeredQueue: ""},
		{name: "Server registers another named task queue", requestedTaskQueue: "tenant-a", registeredQueue: "tenant-b"},
		{name: "Server registers a default Worker on a named task queue", requestedTaskQueue: "", registeredQueue: "tenant-b"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			worker, server, result := startRegistrationTestWorker(
				t,
				PullWorkerOptions{TaskQueue: scenario.requestedTaskQueue, ReservedRPCTasks: new(int)},
				&workerSyncFlowService{serverInfo: taskQueueServerInfo(5)},
			)
			go func() { result <- worker.Start() }()
			stream := awaitRegistrationTestStream(t, server, 5*time.Second)
			require.Equal(t, scenario.requestedTaskQueue, stream.taskQueue)
			stream.registerOnTaskQueue(scenario.registeredQueue)
			select {
			case <-stream.halfClosed:
			case <-time.After(5 * time.Second):
				t.Fatal("Worker did not half-close the mismatched stream")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var rejection *PullRegistrationError
			require.ErrorAs(t, worker.WaitPullRegistered(ctx), &rejection)
			require.Equal(t, codes.InvalidArgument, status.Code(rejection.Err))
			require.Equal(t, scenario.requestedTaskQueue, rejection.TaskQueue)
			require.ErrorContains(t, rejection, "registered the stream on task queue")
			state := worker.PullRegistrationState()
			require.False(t, state.IsReady)
			require.Len(t, state.Queues, 1)
			require.False(t, state.Queues[0].HasRegisteredStream)
			require.Equal(t, scenario.requestedTaskQueue, state.Queues[0].TaskQueue)
			require.Equal(t, codes.InvalidArgument, status.Code(state.Queues[0].LastPermanentError))
		})
	}
}

func TestClientStartFlowSendsTaskQueue(t *testing.T) {
	for _, testCase := range []struct {
		name                  string
		serverProtocolVersion uint32
		taskQueue             string
		wantError             string
	}{
		{name: "default task queue on protocol 4", serverProtocolVersion: 4},
		{name: "named task queue on protocol 5", serverProtocolVersion: 5, taskQueue: "tenant-a"},
		{name: "default alias on protocol 5", serverProtocolVersion: 5, taskQueue: DefaultTaskQueue},
		{name: "named task queue on protocol 4", serverProtocolVersion: 4, taskQueue: "tenant-a", wantError: "requires Server protocol 5 or later"},
		{name: "reserved name", serverProtocolVersion: 5, taskQueue: "dex.other", wantError: "reserved"},
		{name: "invalid characters", serverProtocolVersion: 5, taskQueue: "tenant a", wantError: "names use only"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client, service := newHardDeleteTestClient(t, testCase.serverProtocolVersion)
			_, err := client.StartFlow(context.Background(), hardDeleteTestFlow{}, "flow-1", nil, StartFlowOptions{TaskQueue: testCase.taskQueue})
			if testCase.wantError != "" {
				require.ErrorContains(t, err, testCase.wantError)
				require.Nil(t, service.startRequest)
				return
			}
			require.NoError(t, err)
			target := service.startRequest.GetFlowStartOptions().GetFlowConfigOverride().GetWorkerTarget()
			require.True(t, target.GetIsPullMode())
			require.Equal(t, testCase.taskQueue, target.GetTaskQueue())
		})
	}
}

func TestClientStartFlowTaskQueueReportsUnsupportedError(t *testing.T) {
	client, _ := newHardDeleteTestClient(t, 4)
	_, err := client.StartFlow(context.Background(), hardDeleteTestFlow{}, "flow-1", nil, StartFlowOptions{TaskQueue: "tenant-a"})
	var unsupported *TaskQueueUnsupportedError
	require.ErrorAs(t, err, &unsupported)
	require.Equal(t, "tenant-a", unsupported.TaskQueue)
}

type taskQueueSubFlowParentFlow struct {
	FlowDefaults
}

func (taskQueueSubFlowParentFlow) GetSteps() []StepDef {
	return []StepDef{
		DefineStartStep(taskQueueSubFlowStep{taskQueue: "tenant-b"}),
		DefineStep(taskQueueInheritingSubFlowStep{}),
		DefineStep(taskQueueInvalidSubFlowStep{}),
	}
}

func (taskQueueSubFlowParentFlow) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{}
}

type taskQueueSubFlowStep struct {
	StepDefaults
	taskQueue string
}

func (step taskQueueSubFlowStep) WaitFor(Context, None) (*Wait, error) {
	return Until(SubFlow(subFlowStepTypeChildFlow{}, nil, SubFlowOptions{TaskQueue: step.taskQueue})), nil
}

func (taskQueueSubFlowStep) Execute(Context, None) (*StepDecision, error) { return DeadEnd(), nil }

type taskQueueInheritingSubFlowStep struct {
	StepDefaults
}

func (taskQueueInheritingSubFlowStep) WaitFor(Context, None) (*Wait, error) {
	return Until(SubFlow(subFlowStepTypeChildFlow{}, nil)), nil
}

func (taskQueueInheritingSubFlowStep) Execute(Context, None) (*StepDecision, error) {
	return DeadEnd(), nil
}

type taskQueueInvalidSubFlowStep struct {
	StepDefaults
}

func (taskQueueInvalidSubFlowStep) WaitFor(Context, None) (*Wait, error) {
	return Until(SubFlow(subFlowStepTypeChildFlow{}, nil, SubFlowOptions{TaskQueue: "dex.other"})), nil
}

func (taskQueueInvalidSubFlowStep) Execute(Context, None) (*StepDecision, error) {
	return DeadEnd(), nil
}

func TestWorkerSubFlowTaskQueue(t *testing.T) {
	waitFor := func(t *testing.T, serverProtocolVersion uint32, step Step[None]) ([]*dexpb.InvokeWaitForMethodOutput, error) {
		t.Helper()
		client, closeService := newWorkerClientForServerProtocol(
			t, []Flow{taskQueueSubFlowParentFlow{}, subFlowStepTypeChildFlow{}}, nil, serverProtocolVersion,
		)
		defer closeService()
		return collectWaitForOutputs(client, &dexpb.InvokeWaitForMethodRequest{
			Context:   workerStepContext(),
			FlowType:  GetFinalFlowType(taskQueueSubFlowParentFlow{}),
			StepType:  GetFinalStepType(step),
			StepInput: mustEncodeWorkerTestValue(t, nil),
		})
	}
	subFlowTarget := func(t *testing.T, outputs []*dexpb.InvokeWaitForMethodOutput) *dexpb.WorkerTarget {
		t.Helper()
		require.Len(t, outputs, 1)
		conditions := outputs[0].GetResult().GetWaitingCondition().GetSubFlowConditions()
		require.Len(t, conditions, 1)
		return conditions[0].GetOptions().GetFlowConfigOverride().GetWorkerTarget()
	}

	outputs, err := waitFor(t, 5, taskQueueSubFlowStep{})
	require.NoError(t, err)
	require.Equal(t, "tenant-b", subFlowTarget(t, outputs).GetTaskQueue())

	outputs, err = waitFor(t, 4, taskQueueInheritingSubFlowStep{})
	require.NoError(t, err)
	target := subFlowTarget(t, outputs)
	require.True(t, target.GetIsPullMode())
	require.Empty(t, target.GetTaskQueue())

	_, err = waitFor(t, 4, taskQueueSubFlowStep{})
	require.ErrorContains(t, err, "requires Server protocol 5 or later")

	_, err = waitFor(t, 5, taskQueueInvalidSubFlowStep{})
	require.ErrorContains(t, err, "reserved")
}
