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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
)

func TestWorkerPullStepOutputRejectsInactiveTasks(t *testing.T) {
	for _, method := range []string{"WaitFor", "Execute"} {
		for _, state := range []string{"cancelled", "completed", "reused"} {
			t.Run(method+"/"+state, func(t *testing.T) {
				address, server := startPullTestServer(t)
				connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, connection.Close()) })
				streamContext, cancelStream := context.WithCancel(context.Background())
				defer cancelStream()
				clientStream, err := dexpb.NewWorkerTaskServiceClient(connection).PollWorkerTask(streamContext)
				require.NoError(t, err)
				require.NoError(t, clientStream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Register{
					Register: &dexpb.PollWorkerTaskRegister{Queue: "output-flow", AppWorkerId: "output-worker"},
				}}))
				registered, err := clientStream.Recv()
				require.NoError(t, err)
				require.NotNil(t, registered.GetRegistered())
				serverStream := awaitPullRegistration(t, server)
				taskContext, cancelTask := context.WithCancel(streamContext)
				defer cancelTask()
				stream := &pullStream{ctx: streamContext, stream: clientStream}
				sender := newPullStepOutputTestSender(stream, taskContext, method, "first-task")
				require.NoError(t, sender.sendHeartbeat(&dexpb.StepMethodHeartbeat{}))
				require.NoError(t, sender.sendStreamWrite(&dexpb.StepStreamWrite{}))
				for range 2 {
					require.Equal(t, "first-task", awaitPullFrame(t, serverStream).GetTaskFrame().GetTaskId())
				}
				if state == "cancelled" {
					cancelTask()
				} else {
					require.NoError(t, sender.sendResult())
					require.Equal(t, "first-task", awaitPullFrame(t, serverStream).GetTaskFrame().GetTaskId())
				}
				if state == "reused" {
					cancelTask()
					stream.sendMu.Lock()
					stream.isFinalFrameSent = false
					stream.sendMu.Unlock()
				}
				require.Error(t, sender.sendHeartbeat(&dexpb.StepMethodHeartbeat{}))
				require.Error(t, sender.sendStreamWrite(&dexpb.StepStreamWrite{}))
				require.Error(t, sender.sendResult())
				if state == "reused" {
					nextSender := newPullStepOutputTestSender(stream, streamContext, method, "next-task")
					require.NoError(t, nextSender.sendHeartbeat(&dexpb.StepMethodHeartbeat{}))
					require.NoError(t, nextSender.sendStreamWrite(&dexpb.StepStreamWrite{}))
					require.NoError(t, nextSender.sendResult())
					for range 3 {
						require.Equal(t, "next-task", awaitPullFrame(t, serverStream).GetTaskFrame().GetTaskId())
					}
				}
				require.NoError(t, stream.send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Pong{
					Pong: &dexpb.PollWorkerTaskPong{},
				}}))
				require.NotNil(t, awaitPullFrame(t, serverStream).GetPong())
			})
		}
	}
}

func TestWorkerPullStepCancellationSendsOnlyCanceledFinalFrame(t *testing.T) {
	for _, method := range []string{"WaitFor", "Execute"} {
		t.Run(method, func(t *testing.T) {
			address, server := startPullTestServer(t)
			step := &pullCancellationStep{invocations: make(chan Context, 1), release: make(chan struct{})}
			t.Cleanup(func() { close(step.release) })
			flow := &pullCancellationFlow{step: step}
			worker, err := newWorkerForTest(t, []Flow{flow}, WorkerOptions{FlowServiceAddress: address})
			require.NoError(t, err)
			stopContext, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelStop()
			t.Cleanup(func() { require.NoError(t, worker.Stop(stopContext)) })
			connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, connection.Close()) })
			streamContext, cancelStream := context.WithCancel(context.Background())
			defer cancelStream()
			clientStream, err := dexpb.NewWorkerTaskServiceClient(connection).PollWorkerTask(streamContext)
			require.NoError(t, err)
			require.NoError(t, clientStream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Register{
				Register: &dexpb.PollWorkerTaskRegister{Queue: GetFinalFlowType(flow), AppWorkerId: "cancel-step-worker"},
			}}))
			registered, err := clientStream.Recv()
			require.NoError(t, err)
			require.NotNil(t, registered.GetRegistered())
			serverStream := awaitPullRegistration(t, server)
			stream := &pullStream{worker: worker.pull, queue: GetFinalFlowType(flow), ctx: streamContext, stream: clientStream}
			stream.startTask(newPullCancellationTask(t, flow, method, "cancelled-step"))
			cancelledContext := awaitPullCancellationInvocation(t, step)
			stream.cancelTaskContext()
			select {
			case <-cancelledContext.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("Step did not observe cancellation")
			}
			require.Error(t, cancelledContext.RecordHeartbeat("after cancellation"))
			require.Error(t, workerTestThinking.Write(cancelledContext, "after cancellation"))
			step.release <- struct{}{}
			frame := awaitPullFrame(t, serverStream).GetTaskFrame()
			require.Equal(t, "cancelled-step", frame.GetTaskId())
			require.NotNil(t, frame.GetWorkerError())
			require.Equal(t, int32(codes.Canceled), frame.GetWorkerError().GetGrpcStatus())
			require.Nil(t, frame.GetWaitFor())
			require.Nil(t, frame.GetExecute())
			stream.waitForTaskFinished()
			require.NoError(t, stream.send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Pong{Pong: &dexpb.PollWorkerTaskPong{}}}))
			require.NotNil(t, awaitPullFrame(t, serverStream).GetPong())

			stream.startTask(newPullCancellationTask(t, flow, method, "next-step"))
			awaitPullCancellationInvocation(t, step)
			require.Error(t, cancelledContext.RecordHeartbeat("after reuse"))
			require.Error(t, workerTestThinking.Write(cancelledContext, "after reuse"))
			require.NoError(t, stream.send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Pong{Pong: &dexpb.PollWorkerTaskPong{}}}))
			require.NotNil(t, awaitPullFrame(t, serverStream).GetPong())
			step.release <- struct{}{}
			frame = awaitPullFrame(t, serverStream).GetTaskFrame()
			require.Equal(t, "next-step", frame.GetTaskId())
			require.Nil(t, frame.GetWorkerError())
			if method == "WaitFor" {
				require.NotNil(t, frame.GetWaitFor().GetResult())
			} else {
				require.NotNil(t, frame.GetExecute().GetResult())
			}
			stream.waitForTaskFinished()
			require.NoError(t, stream.send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Pong{Pong: &dexpb.PollWorkerTaskPong{}}}))
			require.NotNil(t, awaitPullFrame(t, serverStream).GetPong())
		})
	}
}

type pullCancellationFlow struct {
	FlowDefaults
	step *pullCancellationStep
}

func (flow *pullCancellationFlow) GetSteps() []StepDef {
	return []StepDef{DefineStartStep(flow.step)}
}

func (*pullCancellationFlow) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{Streams: []StreamDef{workerTestThinking}}
}

type pullCancellationStep struct {
	StepDefaults
	invocations chan Context
	release     chan struct{}
}

func (step *pullCancellationStep) WaitFor(ctx Context, _ struct{}) (*Wait, error) {
	step.invocations <- ctx
	<-step.release
	return SkipWaitImmediately(), nil
}

func (step *pullCancellationStep) Execute(ctx Context, _ struct{}) (*StepDecision, error) {
	step.invocations <- ctx
	<-step.release
	return DeadEnd(), nil
}

func newPullCancellationTask(t *testing.T, flow *pullCancellationFlow, method, taskID string) *dexpb.PollWorkerTask {
	t.Helper()
	task := &dexpb.PollWorkerTask{TaskId: taskID}
	if method == "WaitFor" {
		task.Request = &dexpb.PollWorkerTask_WaitFor{WaitFor: &dexpb.InvokeWaitForMethodRequest{
			Context: workerStepContext(), FlowType: GetFinalFlowType(flow), StepType: GetFinalStepType(flow.step),
			StepInput: mustEncodeWorkerTestValue(t, struct{}{}),
		}}
	} else {
		task.Request = &dexpb.PollWorkerTask_Execute{Execute: &dexpb.InvokeExecuteMethodRequest{
			Context: workerStepContext(), FlowType: GetFinalFlowType(flow), StepType: GetFinalStepType(flow.step),
			StepInput: mustEncodeWorkerTestValue(t, struct{}{}),
		}}
	}
	return task
}

func awaitPullCancellationInvocation(t *testing.T, step *pullCancellationStep) Context {
	t.Helper()
	select {
	case ctx := <-step.invocations:
		return ctx
	case <-time.After(5 * time.Second):
		t.Fatal("Step did not start")
		return nil
	}
}

type pullStepOutputTestSender struct {
	waitFor *waitForOutputStream
	execute *executeOutputStream
}

func newPullStepOutputTestSender(stream *pullStream, ctx context.Context, method, taskID string) pullStepOutputTestSender {
	if method == "WaitFor" {
		return pullStepOutputTestSender{waitFor: newWaitForOutputStream(&pullWaitForSender{stream: stream, ctx: ctx, taskID: taskID})}
	}
	return pullStepOutputTestSender{execute: newExecuteOutputStream(&pullExecuteSender{stream: stream, ctx: ctx, taskID: taskID})}
}

func (sender pullStepOutputTestSender) sendHeartbeat(heartbeat *dexpb.StepMethodHeartbeat) error {
	if sender.waitFor != nil {
		return sender.waitFor.sendHeartbeat(heartbeat)
	}
	return sender.execute.sendHeartbeat(heartbeat)
}

func (sender pullStepOutputTestSender) sendStreamWrite(write *dexpb.StepStreamWrite) error {
	if sender.waitFor != nil {
		return sender.waitFor.sendStreamWrite(write)
	}
	return sender.execute.sendStreamWrite(write)
}

func (sender pullStepOutputTestSender) sendResult() error {
	if sender.waitFor != nil {
		return sender.waitFor.sendResult(&dexpb.InvokeWaitForMethodResponse{})
	}
	return sender.execute.sendResult(&dexpb.InvokeExecuteMethodResponse{})
}
