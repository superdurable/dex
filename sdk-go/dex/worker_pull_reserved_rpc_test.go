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
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

type reservedRPCFlow struct {
	FlowDefaults
	entered                 chan string
	release                 chan struct{}
	cancellationObserved    chan struct{}
	finishAfterCancellation <-chan struct{}
}

func TestWorkerPullReservedRPCValidation(t *testing.T) {
	for _, reserve := range []int{-1, 2, 3} {
		_, err := newWorkerForTest(t, nil, WorkerOptions{Pull: &PullWorkerOptions{MaxConcurrentTasks: 2, ReservedRPCTasks: ptr.Any(reserve)}})
		require.ErrorContains(t, err, "ReservedRPCTasks")
	}
}

func TestWorkerPullReservedSlotAdmission(t *testing.T) {
	for _, reserve := range []int{0, 1} {
		t.Run(fmt.Sprint(reserve), func(t *testing.T) {
			address, server := startPullTestServer(t)
			server.supportsUnknownNameRejection = true
			flow := &reservedRPCFlow{entered: make(chan string, 8), release: make(chan struct{})}
			worker, err := newWorkerForTest(t, []Flow{flow, clientNoStartFlow{}}, WorkerOptions{FlowServiceAddress: address, Pull: &PullWorkerOptions{MaxConcurrentTasks: 2, ReservedRPCTasks: ptr.Any(reserve), IdleStreamsPerQueue: 20}})
			require.NoError(t, err)
			started := make(chan error, 1)
			go func() { started <- worker.Start() }()
			var releaseOnce sync.Once
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(flow.release) })
				require.NoError(t, worker.Stop(context.Background()))
				require.NoError(t, <-started)
			})
			streams := []*pullTestStream{awaitPullRegistration(t, server), awaitPullRegistration(t, server)}
			rpcOnly := 0
			for index, stream := range streams {
				registration := stream.registration
				require.True(t, registration.SupportsReservedSlotDispatch)
				require.Empty(t, registration.Queue)
				require.Equal(t, worker.pull.appWorkerID, registration.AppWorkerId)
				require.ElementsMatch(t, []string{GetFinalFlowType(flow), GetFinalFlowType(clientNoStartFlow{})}, registration.FlowTypes)
				kind := "execute"
				if registration.SlotKind == dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_RPC_ONLY {
					rpcOnly++
					kind = "rpc"
				} else {
					require.Equal(t, dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_STEP_AND_RPC, registration.SlotKind)
				}
				if kind == "execute" {
					unknownStep := reservedRPCTestTask("execute", "unknown-step")
					unknownStep.GetTask().GetExecute().StepType = "MissingStep"
					require.NoError(t, stream.send(unknownStep))
					require.Equal(t, dexpb.PollWorkerTaskRejectReason_POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_STEP, awaitPullFrame(t, stream).GetReject().GetReason())
				}
				unknownRPC := reservedRPCTestTask("rpc", "unknown-rpc")
				unknownRPC.GetTask().GetRpc().RpcName = "MissingRPC"
				require.NoError(t, stream.send(unknownRPC))
				require.Equal(t, dexpb.PollWorkerTaskRejectReason_POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_RPC, awaitPullFrame(t, stream).GetReject().GetReason())
				require.NoError(t, stream.send(reservedRPCTestTask(kind, fmt.Sprint(index))))
				require.Equal(t, kind, awaitReservedRPCEntry(t, flow))
			}
			require.Equal(t, reserve, rpcOnly)
			worker.pull.mu.Lock()
			require.Len(t, worker.pull.streams, 2)
			require.Equal(t, 2, worker.pull.busyTasks)
			worker.pull.mu.Unlock()
			require.Zero(t, worker.PullReservedRPCDeclines())
			releaseOnce.Do(func() { close(flow.release) })
			for _, stream := range streams {
				frame := awaitPullFrame(t, stream)
				require.Nil(t, frame.GetReject())
				require.NotNil(t, frame.GetTaskFrame())
			}
			awaitPullTaskCompletion(t, worker)
		})
	}
}

func TestWorkerPullExecutesStepDeliveredToRPCOnlySlot(t *testing.T) {
	address, server := startPullTestServer(t)
	flow := &reservedRPCFlow{entered: make(chan string, 2), release: make(chan struct{})}
	worker, err := newWorkerForTest(t, []Flow{flow}, WorkerOptions{FlowServiceAddress: address, Pull: &PullWorkerOptions{MaxConcurrentTasks: 2}})
	require.NoError(t, err)
	started := make(chan error, 1)
	go func() { started <- worker.Start() }()
	t.Cleanup(func() {
		close(flow.release)
		require.NoError(t, worker.Stop(context.Background()))
		require.NoError(t, <-started)
	})
	var rpcOnly *pullTestStream
	for range 2 {
		stream := awaitPullRegistration(t, server)
		if stream.registration.SlotKind == dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_RPC_ONLY {
			rpcOnly = stream
		}
	}
	require.NotNil(t, rpcOnly)
	// The Server owns slot routing; even a misrouted Step must execute rather than consume an attempt through rejection.
	require.NoError(t, rpcOnly.send(reservedRPCTestTask("execute", "misrouted-step")))
	require.Equal(t, "execute", awaitReservedRPCEntry(t, flow))
}

func (flow *reservedRPCFlow) GetSteps() []StepDef {
	return []StepDef{DefineStartStep(reservedRPCStep{flow: flow})}
}

func (flow *reservedRPCFlow) GetRPCs() []RPCDef {
	return []RPCDef{DefineRPC(flow.GetResult, nil)}
}

func (*reservedRPCFlow) GetPersistenceSchema() PersistenceSchema { return PersistenceSchema{} }

func (flow *reservedRPCFlow) GetResult(ctx Context, _ None) (*RPCResult[bool], error) {
	if err := flow.block(ctx, "rpc"); err != nil {
		return nil, err
	}
	return &RPCResult[bool]{Output: true}, nil
}

func (flow *reservedRPCFlow) block(ctx Context, kind string) error {
	flow.entered <- kind
	select {
	case <-flow.release:
		return nil
	case <-ctx.Done():
		if flow.cancellationObserved != nil {
			close(flow.cancellationObserved)
		}
		if flow.finishAfterCancellation != nil {
			<-flow.finishAfterCancellation
		}
		return ctx.Err()
	}
}

type reservedRPCStep struct {
	StepDefaults
	flow *reservedRPCFlow
}

func (step reservedRPCStep) WaitFor(ctx Context, _ None) (*Wait, error) {
	if err := step.flow.block(ctx, "waitFor"); err != nil {
		return nil, err
	}
	return SkipWaitImmediately(), nil
}

func (step reservedRPCStep) Execute(ctx Context, _ None) (*StepDecision, error) {
	if err := step.flow.block(ctx, "execute"); err != nil {
		return nil, err
	}
	return GracefulComplete(true), nil
}

func awaitReservedRPCEntry(t *testing.T, flow *reservedRPCFlow) string {
	t.Helper()
	select {
	case kind := <-flow.entered:
		return kind
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not start")
		return ""
	}
}

func reservedRPCTestTask(kind, taskID string) *dexpb.PollWorkerTaskResponse {
	task := &dexpb.PollWorkerTask{TaskId: taskID}
	switch kind {
	case "waitFor":
		task.Request = &dexpb.PollWorkerTask_WaitFor{WaitFor: &dexpb.InvokeWaitForMethodRequest{Context: workerStepContext(), FlowType: "reservedRPCFlow", StepType: "reservedRPCStep", StepInput: newNullValue()}}
	case "execute":
		task.Request = &dexpb.PollWorkerTask_Execute{Execute: &dexpb.InvokeExecuteMethodRequest{Context: workerStepContext(), FlowType: "reservedRPCFlow", StepType: "reservedRPCStep", StepInput: newNullValue()}}
	case "rpc":
		task.Request = &dexpb.PollWorkerTask_Rpc{Rpc: &dexpb.InvokeWorkerRPCRequest{Context: workerRPCContext(), FlowType: "reservedRPCFlow", RpcName: "GetResult", Input: newNullValue()}}
	}
	return &dexpb.PollWorkerTaskResponse{Frame: &dexpb.PollWorkerTaskResponse_Task{Task: task}}
}

func awaitReservedRPCStream(t *testing.T, server *pullTestServer, queue string) *pullTestStream {
	t.Helper()
	for {
		candidate := awaitPullRegistration(t, server)
		if candidate.registration.Queue == queue {
			return candidate
		}
	}
}

func pullRegisteredIdleStreams(worker *Worker, queue string) int {
	worker.pull.mu.Lock()
	defer worker.pull.mu.Unlock()
	idleStreams := 0
	for stream := range worker.pull.streams {
		if stream.queue != queue {
			continue
		}
		stream.mu.Lock()
		if stream.isRegistered && !stream.isBusy && !stream.isDraining {
			idleStreams++
		}
		stream.mu.Unlock()
	}
	return idleStreams
}

type reservedRPCWarningLogger struct {
	Logger
	warnings atomic.Int32
}

func TestWorkerPullReservedRPCDefaults(t *testing.T) {
	for _, capacity := range []int{1, 4} {
		address, server := startPullTestServer(t)
		logger := &reservedRPCWarningLogger{Logger: slog.Default()}
		worker, err := newWorkerForTest(t, nil, WorkerOptions{FlowServiceAddress: address, Logger: logger, Pull: &PullWorkerOptions{MaxConcurrentTasks: capacity}})
		require.NoError(t, err)
		expectedReserve := 1
		expectedWarnings := int32(0)
		if capacity == 1 {
			expectedReserve = 0
			expectedWarnings = 1
		}
		require.Equal(t, expectedReserve, worker.pull.reservedRPCTasks)
		require.Zero(t, worker.PullReservedRPCDeclines())
		started := make(chan error, 1)
		go func() { started <- worker.Start() }()
		awaitPullRegistration(t, server)
		require.NoError(t, worker.Stop(context.Background()))
		require.NoError(t, <-started)
		require.Equal(t, expectedWarnings, logger.warnings.Load())
	}
}

func (logger *reservedRPCWarningLogger) Warn(message string, keyvals ...interface{}) {
	if strings.Contains(message, "RPCs may wait") {
		logger.warnings.Add(1)
	}
	logger.Logger.Warn(message, keyvals...)
}
