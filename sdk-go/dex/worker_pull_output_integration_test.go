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

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
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
