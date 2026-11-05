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
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestWorkerPullRegistrationConcurrencyLimit(t *testing.T) {
	address, server := startPullTestServer(t)
	server.maxConcurrentTasksPerAppWorker = 1
	server.holdAfterEOFQueue = "*"
	flow := &reservedRPCFlow{entered: make(chan string, 4), release: make(chan struct{})}
	close(flow.release)
	worker, err := newWorkerForTest(t, []Flow{flow}, WorkerOptions{FlowServiceAddress: address, Pull: &PullWorkerOptions{MaxConcurrentTasks: 3}})
	require.NoError(t, err)
	started := make(chan error, 1)
	go func() { started <- worker.Start() }()
	streams := []*pullTestStream{awaitPullRegistration(t, server), awaitPullRegistration(t, server), awaitPullRegistration(t, server)}
	// A task sent right after Registered must be returned to the Server through the half-close, not reset.
	require.NoError(t, streams[0].send(reservedRPCTestTask("rpc", "after-limit-registered")))
	for _, stream := range streams {
		awaitSlotHalfClose(t, stream)
	}
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelWait()
	require.ErrorContains(t, worker.WaitPullRegistered(waitCtx), "matching.maxConcurrentTasksCap 1")
	require.False(t, worker.PullRegistrationState().IsReady)
	for _, stream := range streams {
		close(stream.allowReturn)
		require.NoError(t, <-stream.ended)
	}
	select {
	case err := <-started:
		require.ErrorContains(t, err, "MaxConcurrentTasks 3")
		require.ErrorContains(t, err, "matching.maxConcurrentTasksCap 1")
		require.ErrorIs(t, worker.WaitPullRegistered(context.Background()), err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start retried the registration limit instead of failing")
	}
	require.NoError(t, worker.Stop(context.Background()))
	require.False(t, worker.PullRegistrationState().IsReady)
	worker.pull.mu.Lock()
	require.Empty(t, worker.pull.streams)
	worker.pull.mu.Unlock()
	requireNoHandlerCall(t, flow)
	for _, stream := range streams {
		requireNoPullFrame(t, stream)
	}
}

func TestWorkerPullLaterLowerRegistrationLimitKeepsRunning(t *testing.T) {
	address, server := startPullTestServer(t)
	flow := &reservedRPCFlow{entered: make(chan string, 4), release: make(chan struct{})}
	close(flow.release)
	worker, err := newWorkerForTest(t, []Flow{flow}, WorkerOptions{FlowServiceAddress: address, Pull: &PullWorkerOptions{MaxConcurrentTasks: 2, ReservedRPCTasks: ptr.Any(0)}})
	require.NoError(t, err)
	started := make(chan error, 1)
	go func() { started <- worker.Start() }()
	first := awaitPullRegistration(t, server)
	awaitPullRegistration(t, server)
	require.NoError(t, worker.WaitPullRegistered(context.Background()))
	server.mu.Lock()
	server.maxConcurrentTasksPerAppWorker = 1
	server.mu.Unlock()
	close(first.abort)
	replacement := awaitPullRegistration(t, server)
	require.Equal(t, uint32(1), replacement.maxConcurrentTasksPerAppWorker)
	require.NoError(t, replacement.send(reservedRPCTestTask("rpc", "after-lower-limit")))
	require.Equal(t, "rpc", awaitReservedRPCEntry(t, flow))
	require.NotNil(t, awaitPullFrame(t, replacement).GetTaskFrame().GetRpc())
	require.True(t, worker.PullRegistrationState().IsReady)
	select {
	case err := <-started:
		t.Fatalf("a later lower limit stopped the Worker: %v", err)
	default:
	}
	require.NoError(t, worker.Stop(context.Background()))
	require.NoError(t, <-started)
}

func TestWorkerPullShutdownFinalOutputBeforeHalfCloseAndDropsLateTasks(t *testing.T) {
	for _, kind := range []string{"rpc", "execute", "waitFor"} {
		t.Run(kind, func(t *testing.T) {
			address, server := startPullTestServer(t)
			server.holdAfterEOFQueue = "*"
			flow := &reservedRPCFlow{entered: make(chan string, 8), release: make(chan struct{})}
			worker, err := newWorkerForTest(t, []Flow{flow}, WorkerOptions{FlowServiceAddress: address, Pull: &PullWorkerOptions{MaxConcurrentTasks: 2, ReservedRPCTasks: ptr.Any(0)}})
			require.NoError(t, err)
			started := make(chan error, 1)
			go func() { started <- worker.Start() }()
			busy := awaitPullRegistration(t, server)
			idle := awaitPullRegistration(t, server)
			var releaseOnce sync.Once
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(flow.release) })
				worker.pull.cancel()
				require.NoError(t, worker.Stop(context.Background()))
			})
			require.NoError(t, busy.send(reservedRPCTestTask(kind, "started-before-stop")))
			require.Equal(t, kind, awaitReservedRPCEntry(t, flow))
			stopped := make(chan error, 1)
			go func() { stopped <- worker.Stop(context.Background()) }()
			awaitSlotHalfClose(t, idle)
			select {
			case <-busy.eofReceived:
				t.Fatal("busy slot half-closed before final output")
			default:
			}
			require.NoError(t, busy.send(reservedRPCTestTask(kind, "after-stop-before-half-close")))
			require.NoError(t, idle.send(reservedRPCTestTask(kind, "idle-after-half-close")))
			releaseOnce.Do(func() { close(flow.release) })
			final := awaitFinalPullTaskFrame(t, busy)
			require.Equal(t, "started-before-stop", final.GetTaskId())
			// The slot is reusable once the Server has the final output; a stopping Worker drops the next task.
			require.NoError(t, busy.send(reservedRPCTestTask(kind, "right-after-final-output")))
			awaitSlotHalfClose(t, busy)
			require.NoError(t, busy.send(reservedRPCTestTask(kind, "busy-after-half-close")))
			select {
			case err := <-stopped:
				t.Fatalf("Stop returned before the Server ended the streams: %v", err)
			default:
			}
			close(idle.allowReturn)
			close(busy.allowReturn)
			require.NoError(t, <-stopped)
			require.NoError(t, <-started)
			requireNoHandlerCall(t, flow)
			for _, stream := range []*pullTestStream{busy, idle} {
				requireNoPullFrame(t, stream)
				require.NoError(t, <-stream.ended)
			}
		})
	}
}

func TestWorkerPullAcceptsNextTaskRightAfterFinalOutput(t *testing.T) {
	for _, kind := range []string{"rpc", "execute"} {
		t.Run(kind, func(t *testing.T) {
			address, server := startPullTestServer(t)
			flow := &reservedRPCFlow{entered: make(chan string, 8), release: make(chan struct{})}
			close(flow.release)
			worker, err := newWorkerForTest(t, []Flow{flow}, WorkerOptions{FlowServiceAddress: address, Pull: &PullWorkerOptions{MaxConcurrentTasks: 1}})
			require.NoError(t, err)
			started := make(chan error, 1)
			go func() { started <- worker.Start() }()
			t.Cleanup(func() { require.NoError(t, worker.Stop(context.Background())); require.NoError(t, <-started) })
			stream := awaitPullRegistration(t, server)
			for index := range 20 {
				taskID := fmt.Sprintf("task-%d", index)
				require.NoError(t, stream.send(reservedRPCTestTask(kind, taskID)))
				require.Equal(t, kind, awaitReservedRPCEntry(t, flow))
				final := awaitFinalPullTaskFrame(t, stream)
				require.Equal(t, taskID, final.GetTaskId())
				require.Nil(t, final.GetWorkerError())
			}
			require.NoError(t, stream.stream.Context().Err())
		})
	}
}

func awaitFinalPullTaskFrame(t *testing.T, stream *pullTestStream) *dexpb.PollWorkerTaskFrame {
	t.Helper()
	for {
		frame := awaitPullFrame(t, stream)
		require.Nil(t, frame.GetReject())
		output := frame.GetTaskFrame()
		if output == nil {
			continue
		}
		if output.GetRpc() != nil || output.GetWorkerError() != nil || output.GetWaitFor().GetResult() != nil || output.GetExecute().GetResult() != nil {
			return output
		}
	}
}

func requireNoHandlerCall(t *testing.T, flow *reservedRPCFlow) {
	t.Helper()
	select {
	case kind := <-flow.entered:
		t.Fatalf("late task called a handler: %s", kind)
	default:
	}
}

func requireNoPullFrame(t *testing.T, stream *pullTestStream) {
	t.Helper()
	select {
	case frame := <-stream.frames:
		t.Fatalf("unexpected frame after shutdown: %v", frame)
	default:
	}
}

func TestWorkerPullShutdownTimeoutResetsBusyStream(t *testing.T) {
	address, server := startPullTestServer(t)
	server.holdAfterEOFQueue = "*"
	finishCleanup := make(chan struct{})
	var cleanupOnce sync.Once
	t.Cleanup(func() { cleanupOnce.Do(func() { close(finishCleanup) }) })
	flow := &reservedRPCFlow{entered: make(chan string, 4), release: make(chan struct{}), cancellationObserved: make(chan struct{}), finishAfterCancellation: finishCleanup}
	worker, err := newWorkerForTest(t, []Flow{flow}, WorkerOptions{FlowServiceAddress: address, Pull: &PullWorkerOptions{MaxConcurrentTasks: 1, StopDrainTimeout: 20 * time.Millisecond}})
	require.NoError(t, err)
	started := make(chan error, 1)
	go func() { started <- worker.Start() }()
	stream := awaitPullRegistration(t, server)
	require.NoError(t, stream.send(reservedRPCTestTask("rpc", "slow-handler")))
	require.Equal(t, "rpc", awaitReservedRPCEntry(t, flow))
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Stop(context.Background()) }()
	select {
	case <-flow.cancellationObserved:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel the slow handler")
	}
	select {
	case <-stream.stream.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("busy stream was not reset")
	}
	select {
	case <-stream.eofReceived:
		t.Fatal("timeout half-closed a running handler")
	default:
	}
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned while handler cleanup still ran: %v", err)
	default:
	}
	cleanupOnce.Do(func() { close(finishCleanup) })
	require.ErrorIs(t, <-stopped, context.DeadlineExceeded)
	require.NoError(t, <-started)
}

func TestWorkerPullConnectionBreakCancelsTaskBeforeReconnect(t *testing.T) {
	address, server := startPullTestServer(t)
	flow := &reservedRPCFlow{entered: make(chan string, 4), release: make(chan struct{})}
	worker, err := newWorkerForTest(t, []Flow{flow}, WorkerOptions{FlowServiceAddress: address, Pull: &PullWorkerOptions{MaxConcurrentTasks: 1}})
	require.NoError(t, err)
	started := make(chan error, 1)
	go func() { started <- worker.Start() }()
	t.Cleanup(func() { require.NoError(t, worker.Stop(context.Background())); require.NoError(t, <-started) })
	first := awaitPullRegistration(t, server)
	require.NoError(t, first.send(reservedRPCTestTask("rpc", "lost-task")))
	require.Equal(t, "rpc", awaitReservedRPCEntry(t, flow))
	close(first.abort)
	replacement := awaitPullRegistration(t, server)
	awaitPullTaskCompletion(t, worker)
	require.NoError(t, replacement.send(&dexpb.PollWorkerTaskResponse{Frame: &dexpb.PollWorkerTaskResponse_Ping{Ping: &dexpb.PollWorkerTaskPing{}}}))
	require.NotNil(t, awaitPullFrame(t, replacement).GetPong())
	select {
	case frame := <-replacement.frames:
		t.Fatalf("old task output crossed to replacement connection: %v", frame)
	default:
	}
}

func awaitSlotHalfClose(t *testing.T, stream *pullTestStream) {
	t.Helper()
	select {
	case <-stream.eofReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("idle slot did not half-close")
	}
}

type attemptProtocolTestService struct {
	clientTestFlowService
	protocolVersion uint32
	shouldFailFirst bool
	negotiations    atomic.Int32
}

func (service *attemptProtocolTestService) GetServerInfo(context.Context, *emptypb.Empty) (*dexpb.ServerInfo, error) {
	count := service.negotiations.Add(1)
	if count == 1 && service.shouldFailFirst {
		return nil, status.Error(codes.Unavailable, "temporary negotiation failure")
	}
	return &dexpb.ServerInfo{ServerVersion: "protocol-test", MinimumSupportedProtocolVersion: 1, CurrentProtocolVersion: service.protocolVersion}, nil
}

func TestClientNegotiatesBeforeAnyFirstRequestAndCachesIncompatibility(t *testing.T) {
	for _, call := range []struct {
		name   string
		invoke func(*Client) error
	}{
		{name: "health", invoke: func(client *Client) error { _, err := client.HealthCheck(context.Background()); return err }},
		{name: "read", invoke: func(client *Client) error { _, err := client.DescribeFlow(context.Background(), "flow"); return err }},
		{name: "write", invoke: func(client *Client) error { return client.StopFlow(context.Background(), "flow", StopOptions{}) }},
	} {
		t.Run(call.name, func(t *testing.T) {
			service := &attemptProtocolTestService{protocolVersion: 3}
			client := newAttemptProtocolTestClient(t, service)
			require.Zero(t, service.negotiations.Load())
			first := call.invoke(client)
			require.ErrorContains(t, first, "(server version protocol-test)")
			require.ErrorContains(t, first, "Upgrade the Server before upgrading the SDK.")
			require.ErrorContains(t, first, "The Server reports protocol versions 1-3")
			require.ErrorContains(t, first, "requires a Dex Server with protocol version 4 or later (server-zd newer than v0.1.16)")
			require.Same(t, first, call.invoke(client))
			require.Equal(t, int32(1), service.negotiations.Load())
			require.Nil(t, service.stopRequest)
			require.Nil(t, service.flowSummaryRequest)
		})
	}
}

func TestClientRetriesTransientProtocolNegotiation(t *testing.T) {
	service := &attemptProtocolTestService{protocolVersion: 4, shouldFailFirst: true}
	client := newAttemptProtocolTestClient(t, service)
	_, err := client.HealthCheck(context.Background())
	require.ErrorContains(t, err, "temporary negotiation failure")
	_, err = client.HealthCheck(context.Background())
	require.NoError(t, err)
	_, err = client.HealthCheck(context.Background())
	require.NoError(t, err)
	require.Equal(t, int32(2), service.negotiations.Load())
}

func TestWorkerRefusesServerProtocolThree(t *testing.T) {
	service := &workerSyncFlowService{serverInfo: &dexpb.ServerInfo{ServerVersion: "old-server", MinimumSupportedProtocolVersion: 1, CurrentProtocolVersion: 3}}
	address := startWorkerFlowService(t, service, &workerStartupTaskService{flowService: service})
	worker, err := newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{FlowServiceAddress: address})
	require.NoError(t, err)
	err = worker.Start()
	require.ErrorContains(t, err, "(server version old-server)")
	require.ErrorContains(t, err, "Upgrade the Server before upgrading the SDK.")
	require.ErrorContains(t, err, "The Server reports protocol versions 1-3")
	require.ErrorContains(t, err, "requires a Dex Server with protocol version 4 or later (server-zd newer than v0.1.16)")
	require.Equal(t, []string{"GetServerInfo"}, service.recordedCalls())
}

func TestRecoveryAttemptProgressMapping(t *testing.T) {
	for _, scenario := range []struct {
		progress dexpb.StepAttemptProgress
		expected StepAttemptProgress
	}{
		{dexpb.StepAttemptProgress_STEP_ATTEMPT_PROGRESS_UNSPECIFIED, StepAttemptProgressUnspecified},
		{dexpb.StepAttemptProgress_STEP_ATTEMPT_PROGRESS_NOT_STARTED, StepAttemptProgressNotStarted},
		{dexpb.StepAttemptProgress_STEP_ATTEMPT_PROGRESS_NOT_COMPLETED, StepAttemptProgressNotCompleted},
		{dexpb.StepAttemptProgress(99), StepAttemptProgressUnspecified},
	} {
		metadata := workerStepContext()
		metadata.RecoveryError = &dexpb.RecoveryErrorInfo{AttemptProgress: scenario.progress}
		invocation, err := newInvocationContext(context.Background(), invocationExecute, nil, nil, metadata, nil, nil, nil, nil, nil, nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, scenario.expected, invocation.RecoveryError().AttemptProgress)
	}
}

func newAttemptProtocolTestClient(t *testing.T, service *attemptProtocolTestService) *Client {
	t.Helper()
	client, _ := newClientIntegration(t)
	address := startWorkerFlowService(t, service)
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	client.service = dexpb.NewFlowServiceClient(connection)
	return client
}

func TestClientNegotiatesProtocolOnceForConcurrentCalls(t *testing.T) {
	service := &attemptProtocolTestService{protocolVersion: 4}
	client := newAttemptProtocolTestClient(t, service)
	results := make(chan error, 8)
	for range 8 {
		go func() { _, err := client.HealthCheck(context.Background()); results <- err }()
	}
	for range 8 {
		require.NoError(t, <-results)
	}
	require.Equal(t, int32(1), service.negotiations.Load())
}

type failedOutputPollTestStream struct {
	rejectTaskClientStream
	hasHalfClosed bool
}

func (stream *failedOutputPollTestStream) CloseSend() error {
	stream.hasHalfClosed = true
	return nil
}

func TestWorkerPullFailedFinalOutputResetsInsteadOfHalfClosing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clientStream := &failedOutputPollTestStream{rejectTaskClientStream: rejectTaskClientStream{ctx: ctx, sendError: status.Error(codes.Unavailable, "lost final output")}}
	stream := &pullStream{worker: &pullWorker{busyTasks: 1, wake: make(chan struct{}, 1)}, ctx: ctx, cancel: cancel, pollCancel: cancel, stream: clientStream, taskCancel: func() {}, isBusy: true, isDraining: true}
	err := stream.sendTaskFrame(&dexpb.PollWorkerTaskFrame{TaskId: "lost-output", Output: &dexpb.PollWorkerTaskFrame_WorkerError{WorkerError: &dexpb.WorkerErrorResponse{Detail: "failure"}}})
	require.Error(t, err)
	stream.completeTask()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.False(t, clientStream.hasHalfClosed)
}

func TestWorkerPullShutdownDeadlineResetsHalfClosedStreamWithoutServerEnd(t *testing.T) {
	address, server := startPullTestServer(t)
	server.holdAfterEOFQueue = "*"
	worker, err := newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{FlowServiceAddress: address, Pull: &PullWorkerOptions{MaxConcurrentTasks: 1, StopDrainTimeout: 50 * time.Millisecond}})
	require.NoError(t, err)
	started := make(chan error, 1)
	go func() { started <- worker.Start() }()
	stream := awaitPullRegistration(t, server)
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Stop(context.Background()) }()
	awaitSlotHalfClose(t, stream)
	select {
	case err := <-stopped:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop waited past its deadline for a Server that never ended the stream")
	}
	select {
	case err := <-stream.ended:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("deadline did not reset the half-closed stream")
	}
	require.NoError(t, <-started)
}

func TestWorkerPullIdleReconnectHalfClosesAndDropsLateTask(t *testing.T) {
	address, server := startPullTestServer(t)
	server.holdAfterEOFQueue = "*"
	flow := &reservedRPCFlow{entered: make(chan string, 4), release: make(chan struct{})}
	close(flow.release)
	worker, err := newWorkerForTest(t, []Flow{flow}, WorkerOptions{FlowServiceAddress: address, Pull: &PullWorkerOptions{MaxConcurrentTasks: 1}})
	require.NoError(t, err)
	started := make(chan error, 1)
	go func() { started <- worker.Start() }()
	stream := awaitPullRegistration(t, server)
	require.NoError(t, stream.send(&dexpb.PollWorkerTaskResponse{Frame: &dexpb.PollWorkerTaskResponse_Reconnect{Reconnect: &dexpb.PollWorkerTaskReconnect{Reason: "owner moved"}}}))
	awaitSlotHalfClose(t, stream)
	require.NoError(t, stream.send(reservedRPCTestTask("rpc", "after-reconnect")))
	close(stream.allowReturn)
	require.NoError(t, <-stream.ended)
	replacement := awaitPullRegistration(t, server)
	requireNoHandlerCall(t, flow)
	requireNoPullFrame(t, stream)
	close(replacement.allowReturn)
	require.NoError(t, worker.Stop(context.Background()))
	require.NoError(t, <-started)
}

func TestWorkerPullOversizedTaskFrameResetsStream(t *testing.T) {
	address, server := startPullTestServer(t)
	server.holdAfterEOFQueue = "*"
	flow := &reservedRPCFlow{entered: make(chan string, 4), release: make(chan struct{}), cancellationObserved: make(chan struct{})}
	worker, err := newWorkerForTest(t, []Flow{flow}, WorkerOptions{FlowServiceAddress: address, GrpcMaxMessageBytes: 64 << 10, Pull: &PullWorkerOptions{MaxConcurrentTasks: 2, ReservedRPCTasks: ptr.Any(0)}})
	require.NoError(t, err)
	started := make(chan error, 1)
	go func() { started <- worker.Start() }()
	busy := awaitPullRegistration(t, server)
	idle := awaitPullRegistration(t, server)
	t.Cleanup(func() {
		server.mu.Lock()
		for _, stream := range server.streams {
			if stream.allowReturn != nil && stream != busy && stream != idle {
				close(stream.allowReturn)
			}
		}
		server.mu.Unlock()
		require.NoError(t, worker.Stop(context.Background()))
		require.NoError(t, <-started)
	})
	require.NoError(t, busy.send(reservedRPCTestTask("rpc", "running")))
	require.Equal(t, "rpc", awaitReservedRPCEntry(t, flow))
	oversizedTask := reservedRPCTestTask("rpc", strings.Repeat("x", 128<<10))
	for _, stream := range []*pullTestStream{busy, idle} {
		require.NoError(t, stream.send(oversizedTask))
		select {
		case err := <-stream.ended:
			t.Fatalf("local receive failure ended the stream cleanly: %v", err)
		case <-stream.stream.Context().Done():
		case <-time.After(5 * time.Second):
			t.Fatal("local receive failure did not reset the stream")
		}
		select {
		case <-stream.eofReceived:
			t.Fatal("local receive failure half-closed the stream")
		default:
		}
	}
	select {
	case <-flow.cancellationObserved:
	case <-time.After(5 * time.Second):
		t.Fatal("reset did not cancel the running handler")
	}
}

type blockedSendPollTestStream struct {
	rejectTaskClientStream
	sendEntered   chan struct{}
	releaseSend   chan struct{}
	hasHalfClosed atomic.Bool
}

func (stream *blockedSendPollTestStream) Send(*dexpb.PollWorkerTaskRequest) error {
	close(stream.sendEntered)
	select {
	case <-stream.releaseSend:
		return nil
	case <-stream.ctx.Done():
		return stream.ctx.Err()
	}
}

func (stream *blockedSendPollTestStream) CloseSend() error {
	stream.hasHalfClosed.Store(true)
	return nil
}

func newBlockedSendPollTestStream(ctx context.Context) *blockedSendPollTestStream {
	return &blockedSendPollTestStream{rejectTaskClientStream: rejectTaskClientStream{ctx: ctx}, sendEntered: make(chan struct{}), releaseSend: make(chan struct{})}
}

func newDrainTestPullWorker(flushTimeout time.Duration) *pullWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &pullWorker{
		ctx: ctx, cancel: cancel, streams: make(map[*pullStream]struct{}), wake: make(chan struct{}, 1),
		stopBegan: make(chan struct{}), registrationChanged: make(chan struct{}),
		stopDrainTimeout: 20 * time.Millisecond, finalOutputFlushTimeout: flushTimeout,
	}
}

func (worker *pullWorker) addDrainTestStream(transport dexpb.WorkerTaskService_PollWorkerTaskClient, isBusy bool) *pullStream {
	streamCtx, cancel := context.WithCancel(worker.ctx)
	stream := &pullStream{worker: worker, ctx: streamCtx, cancel: cancel, pollCancel: cancel, stream: transport, isBusy: isBusy, taskCancel: func() {}}
	if isBusy {
		worker.busyTasks++
	}
	worker.streams[stream] = struct{}{}
	return stream
}

func TestWorkerStopDeadlineResetsStreamsWhileIdleSendBlocked(t *testing.T) {
	pull := newDrainTestPullWorker(time.Hour)
	transport := newBlockedSendPollTestStream(pull.ctx)
	idle := pull.addDrainTestStream(transport, false)
	busy := pull.addDrainTestStream(nil, true)
	transport.ctx = idle.ctx
	worker := &Worker{pull: pull, state: workerRunning, done: make(chan struct{})}
	sendFinished := make(chan error, 1)
	go func() { sendFinished <- idle.sendPong() }()
	<-transport.sendEntered
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelStop()
	stopFinished := make(chan error, 1)
	go func() { stopFinished <- worker.Stop(stopCtx) }()
	for _, stream := range []*pullStream{idle, busy} {
		select {
		case <-stream.ctx.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("Stop deadline did not reset a stream while an idle Send was blocked")
		}
	}
	require.ErrorIs(t, <-sendFinished, context.Canceled)
	pull.streamWG.Wait()
	require.False(t, transport.hasHalfClosed.Load())
	close(worker.done)
	require.ErrorIs(t, <-stopFinished, context.DeadlineExceeded)
}

func TestWorkerStopDeadlineFlushesQueuedFinalOutput(t *testing.T) {
	pull := newDrainTestPullWorker(time.Hour)
	defer pull.cancel()
	transport := newBlockedSendPollTestStream(pull.ctx)
	flushing := pull.addDrainTestStream(transport, true)
	transport.ctx = flushing.ctx
	flushing.isDraining = true
	flushing.taskFinished = make(chan struct{}, 1)
	pending := pull.addDrainTestStream(nil, true)
	sendFinished := make(chan error, 1)
	go func() {
		sendFinished <- flushing.sendTaskFrame(&dexpb.PollWorkerTaskFrame{TaskId: "queued", Output: &dexpb.PollWorkerTaskFrame_Rpc{Rpc: &dexpb.InvokeWorkerRPCResponse{}}})
	}()
	<-transport.sendEntered
	pull.expireDrain()
	require.ErrorIs(t, pending.ctx.Err(), context.Canceled)
	require.NoError(t, flushing.ctx.Err())
	close(transport.releaseSend)
	require.NoError(t, <-sendFinished)
	flushing.completeTask()
	require.True(t, transport.hasHalfClosed.Load())
	require.NoError(t, flushing.ctx.Err())
}

func TestWorkerStopDeadlineBoundsQueuedFinalOutputFlush(t *testing.T) {
	pull := newDrainTestPullWorker(10 * time.Millisecond)
	flushing := pull.addDrainTestStream(nil, true)
	require.NoError(t, flushing.admitFinalOutput(&dexpb.PollWorkerTaskFrame{Output: &dexpb.PollWorkerTaskFrame_Rpc{Rpc: &dexpb.InvokeWorkerRPCResponse{}}}))
	pull.expireDrain()
	require.NoError(t, flushing.ctx.Err())
	select {
	case <-flushing.ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("flush period did not end with a reset")
	}
}

type blockingProtocolFlowServiceClient struct {
	dexpb.FlowServiceClient
	negotiations atomic.Int32
	entered      chan struct{}
	release      chan struct{}
}

func (service *blockingProtocolFlowServiceClient) GetServerInfo(ctx context.Context, _ *emptypb.Empty, _ ...grpc.CallOption) (*dexpb.ServerInfo, error) {
	service.negotiations.Add(1)
	service.entered <- struct{}{}
	select {
	case <-service.release:
		return &dexpb.ServerInfo{ServerVersion: "blocking", MinimumSupportedProtocolVersion: 4, CurrentProtocolVersion: 4}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (*blockingProtocolFlowServiceClient) HealthCheck(context.Context, *emptypb.Empty, ...grpc.CallOption) (*dexpb.HealthInfo, error) {
	return &dexpb.HealthInfo{}, nil
}

func TestClientNegotiationHonorsCallerDeadlines(t *testing.T) {
	service := &blockingProtocolFlowServiceClient{entered: make(chan struct{}, 8), release: make(chan struct{})}
	client := &Client{service: service}
	shortCtx, cancelShort := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShort()
	_, err := client.HealthCheck(shortCtx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	<-service.entered

	firstFinished := make(chan error, 1)
	go func() { _, err := client.HealthCheck(context.Background()); firstFinished <- err }()
	<-service.entered
	waiterCtx, cancelWaiter := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelWaiter()
	waiterFinished := make(chan error, 1)
	go func() { _, err := client.HealthCheck(waiterCtx); waiterFinished <- err }()
	select {
	case err := <-waiterFinished:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(5 * time.Second):
		t.Fatal("a call waiting for another call's negotiation ignored its own deadline")
	}
	close(service.release)
	require.NoError(t, <-firstFinished)
	_, err = client.HealthCheck(context.Background())
	require.NoError(t, err)
	require.Equal(t, int32(2), service.negotiations.Load())
}

type scriptedProtocolFlowServiceClient struct {
	dexpb.FlowServiceClient
	negotiations atomic.Int32
	serverInfo   *dexpb.ServerInfo
	failure      error
}

func (service *scriptedProtocolFlowServiceClient) GetServerInfo(context.Context, *emptypb.Empty, ...grpc.CallOption) (*dexpb.ServerInfo, error) {
	service.negotiations.Add(1)
	return service.serverInfo, service.failure
}

func (*scriptedProtocolFlowServiceClient) HealthCheck(context.Context, *emptypb.Empty, ...grpc.CallOption) (*dexpb.HealthInfo, error) {
	return &dexpb.HealthInfo{}, nil
}

func TestClientNegotiationErrorKinds(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		serverInfo   *dexpb.ServerInfo
		failure      error
		want         string
		isCached     bool
		isServiceErr bool
	}{
		{name: "unreachable", failure: status.Error(codes.Unavailable, "offline"), want: "dex: GetServerInfo: Unavailable: offline", isServiceErr: true},
		{name: "SDK too old", serverInfo: &dexpb.ServerInfo{ServerVersion: "future", MinimumSupportedProtocolVersion: maximumSupportedServerProtocolVersion + 1, CurrentProtocolVersion: maximumSupportedServerProtocolVersion + 1}, want: "Upgrade the Go SDK.", isCached: true},
		{name: "Server too old", serverInfo: &dexpb.ServerInfo{ServerVersion: "old", MinimumSupportedProtocolVersion: 1, CurrentProtocolVersion: minimumSupportedServerProtocolVersion - 1}, want: "requires a Dex Server with protocol version 4 or later (server-zd newer than v0.1.16)", isCached: true},
		{name: "empty ServerInfo", serverInfo: &dexpb.ServerInfo{}, want: "returned invalid ServerInfo"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service := &scriptedProtocolFlowServiceClient{serverInfo: scenario.serverInfo, failure: scenario.failure}
			client := &Client{service: service}
			_, first := client.HealthCheck(context.Background())
			require.ErrorContains(t, first, scenario.want)
			var serviceErr *ServiceError
			require.Equal(t, scenario.isServiceErr, errors.As(first, &serviceErr))
			if scenario.isServiceErr {
				require.Equal(t, codes.Unavailable, status.Code(first))
				require.NotContains(t, first.Error(), "incompatible")
			}
			_, second := client.HealthCheck(context.Background())
			require.ErrorContains(t, second, scenario.want)
			if scenario.isCached {
				require.Equal(t, int32(1), service.negotiations.Load())
			} else {
				require.Equal(t, int32(2), service.negotiations.Load())
			}
		})
	}
}

// orderedPollTestStream blocks its first Send until released and records frames and the half-close in order.
type orderedPollTestStream struct {
	rejectTaskClientStream
	mu          sync.Mutex
	events      []string
	sendCount   int
	sendEntered chan struct{}
	releaseSend chan struct{}
}

func (stream *orderedPollTestStream) Send(request *dexpb.PollWorkerTaskRequest) error {
	stream.mu.Lock()
	stream.sendCount++
	isFirstSend := stream.sendCount == 1
	stream.mu.Unlock()
	if isFirstSend {
		close(stream.sendEntered)
		select {
		case <-stream.releaseSend:
		case <-stream.ctx.Done():
			return stream.ctx.Err()
		}
	}
	event := "pong"
	if request.GetTaskFrame() != nil {
		event = "final:" + request.GetTaskFrame().GetTaskId()
	}
	stream.record(event)
	return nil
}

func (stream *orderedPollTestStream) CloseSend() error {
	stream.record("half-close")
	return nil
}

func (stream *orderedPollTestStream) record(event string) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.events = append(stream.events, event)
}

func (stream *orderedPollTestStream) recordedEvents() []string {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return append([]string(nil), stream.events...)
}

func TestWorkerStopDeadlineFlushesFinalOutputQueuedBehindBlockedPong(t *testing.T) {
	pull := newDrainTestPullWorker(time.Hour)
	defer pull.cancel()
	transport := &orderedPollTestStream{rejectTaskClientStream: rejectTaskClientStream{ctx: pull.ctx}, sendEntered: make(chan struct{}), releaseSend: make(chan struct{})}
	stream := pull.addDrainTestStream(transport, true)
	transport.ctx = stream.ctx
	stream.isDraining = true
	stream.taskFinished = make(chan struct{}, 1)
	pongFinished := make(chan error, 1)
	go func() { pongFinished <- stream.sendPong() }()
	<-transport.sendEntered
	finalFinished := make(chan error, 1)
	go func() {
		finalFinished <- stream.sendTaskFrame(&dexpb.PollWorkerTaskFrame{TaskId: "behind-pong", Output: &dexpb.PollWorkerTaskFrame_Rpc{Rpc: &dexpb.InvokeWorkerRPCResponse{}}})
	}()
	require.Eventually(t, func() bool {
		stream.finalOutputMu.Lock()
		defer stream.finalOutputMu.Unlock()
		return stream.hasQueuedFinalOutput
	}, 5*time.Second, time.Millisecond)
	pull.expireDrain()
	require.NoError(t, stream.ctx.Err(), "the deadline reset a stream whose final output waited behind a Pong")
	close(transport.releaseSend)
	require.NoError(t, <-pongFinished)
	require.NoError(t, <-finalFinished)
	stream.completeTask()
	require.NoError(t, stream.ctx.Err())
	require.Equal(t, []string{"pong", "final:behind-pong", "half-close"}, transport.recordedEvents())
	// The output stays unconfirmed after the handler finished and the stream half-closed.
	pull.expireDrain()
	require.NoError(t, stream.ctx.Err())
}

func TestWorkerStopDeadlineRejectsFinalOutputAfterReset(t *testing.T) {
	pull := newDrainTestPullWorker(time.Hour)
	defer pull.cancel()
	transport := &orderedPollTestStream{rejectTaskClientStream: rejectTaskClientStream{ctx: pull.ctx}, sendEntered: make(chan struct{}), releaseSend: make(chan struct{})}
	stream := pull.addDrainTestStream(transport, true)
	pull.expireDrain()
	require.ErrorIs(t, stream.ctx.Err(), context.Canceled)
	err := stream.sendTaskFrame(&dexpb.PollWorkerTaskFrame{TaskId: "too-late", Output: &dexpb.PollWorkerTaskFrame_Rpc{Rpc: &dexpb.InvokeWorkerRPCResponse{}}})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, transport.recordedEvents())
}

// scriptedPollClient hands each PollWorkerTask call a scriptedPollStream controlled by the test.
type scriptedPollClient struct {
	streams chan *scriptedPollStream
}

func (client *scriptedPollClient) PollWorkerTask(ctx context.Context, _ ...grpc.CallOption) (dexpb.WorkerTaskService_PollWorkerTaskClient, error) {
	stream := &scriptedPollStream{
		ctx: ctx, responses: make(chan *dexpb.PollWorkerTaskResponse), sent: make(chan *dexpb.PollWorkerTaskRequest, 16),
		finalSendEntered: make(chan string, 4), releaseFinalSend: make(chan struct{}), halfClosed: make(chan struct{}),
	}
	select {
	case client.streams <- stream:
		return stream, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type scriptedPollStream struct {
	grpc.ClientStream
	ctx              context.Context
	responses        chan *dexpb.PollWorkerTaskResponse
	sent             chan *dexpb.PollWorkerTaskRequest
	finalSendEntered chan string
	releaseFinalSend chan struct{}
	halfCloseOnce    sync.Once
	halfClosed       chan struct{}
}

func (stream *scriptedPollStream) Context() context.Context { return stream.ctx }

func (stream *scriptedPollStream) Send(request *dexpb.PollWorkerTaskRequest) error {
	if frame := request.GetTaskFrame(); frame != nil && isFinalTaskFrame(frame) {
		stream.finalSendEntered <- frame.GetTaskId()
		select {
		case <-stream.releaseFinalSend:
		case <-stream.ctx.Done():
			return stream.ctx.Err()
		}
	}
	stream.sent <- request
	return nil
}

func (stream *scriptedPollStream) Recv() (*dexpb.PollWorkerTaskResponse, error) {
	select {
	case response, isOpen := <-stream.responses:
		if !isOpen {
			return nil, io.EOF
		}
		return response, nil
	case <-stream.ctx.Done():
		return nil, status.FromContextError(stream.ctx.Err()).Err()
	}
}

func (stream *scriptedPollStream) CloseSend() error {
	stream.halfCloseOnce.Do(func() { close(stream.halfClosed) })
	return nil
}

func (stream *scriptedPollStream) deliver(t *testing.T, response *dexpb.PollWorkerTaskResponse) {
	t.Helper()
	select {
	case stream.responses <- response:
	case <-time.After(5 * time.Second):
		t.Fatal("Worker did not receive the frame")
	}
}

func TestWorkerPullReusesSlotForTaskArrivingBeforeLocalCleanup(t *testing.T) {
	for _, isStopping := range []bool{false, true} {
		t.Run(fmt.Sprintf("stopping=%v", isStopping), func(t *testing.T) {
			flow := &reservedRPCFlow{entered: make(chan string, 4), release: make(chan struct{})}
			close(flow.release)
			worker, err := newWorkerForTest(t, []Flow{flow}, WorkerOptions{Pull: &PullWorkerOptions{MaxConcurrentTasks: 1}})
			require.NoError(t, err)
			pollClient := &scriptedPollClient{streams: make(chan *scriptedPollStream, 4)}
			worker.pull.client = pollClient
			started := make(chan error, 1)
			go func() { started <- worker.Start() }()
			stream := <-pollClient.streams
			require.NotNil(t, (<-stream.sent).GetRegister())
			stream.deliver(t, &dexpb.PollWorkerTaskResponse{Frame: &dexpb.PollWorkerTaskResponse_Registered{Registered: &dexpb.PollWorkerTaskRegistered{}}})
			stream.deliver(t, reservedRPCTestTask("rpc", "task-a"))
			require.Equal(t, "rpc", awaitReservedRPCEntry(t, flow))
			// Task A's final Send is held, so its local cleanup cannot finish before task B arrives.
			require.Equal(t, "task-a", <-stream.finalSendEntered)
			stream.deliver(t, reservedRPCTestTask("rpc", "task-b"))
			stopped := make(chan error, 1)
			if isStopping {
				go func() { stopped <- worker.Stop(context.Background()) }()
				require.Eventually(t, func() bool {
					worker.pull.mu.Lock()
					defer worker.pull.mu.Unlock()
					return worker.pull.isStopping
				}, 5*time.Second, time.Millisecond)
			}
			close(stream.releaseFinalSend)
			require.Equal(t, "task-a", (<-stream.sent).GetTaskFrame().GetTaskId())
			if !isStopping {
				require.Equal(t, "rpc", awaitReservedRPCEntry(t, flow))
				require.Equal(t, "task-b", <-stream.finalSendEntered)
				require.Equal(t, "task-b", (<-stream.sent).GetTaskFrame().GetTaskId())
				require.NoError(t, stream.ctx.Err())
				go func() { stopped <- worker.Stop(context.Background()) }()
				<-stream.halfClosed
				close(stream.responses)
				require.NoError(t, <-stopped)
				require.NoError(t, <-started)
				return
			}
			select {
			case <-stream.halfClosed:
			case <-time.After(5 * time.Second):
				t.Fatal("stopping Worker did not half-close after task A's final output")
			}
			require.NoError(t, stream.ctx.Err(), "stopping Worker reset the stream instead of dropping task B")
			requireNoHandlerCall(t, flow)
			select {
			case frame := <-stream.sent:
				t.Fatalf("stopping Worker sent a frame for task B: %v", frame)
			default:
			}
			close(stream.responses)
			require.NoError(t, <-stopped)
			require.NoError(t, <-started)
			select {
			case frame := <-stream.sent:
				t.Fatalf("stopping Worker sent a frame for task B: %v", frame)
			default:
			}
		})
	}
}

func TestClientRequestTimeoutBoundsProtocolNegotiation(t *testing.T) {
	waitCalls := map[string]func(*Client) error{
		"WaitForStepCompletion": func(client *Client) error {
			return client.WaitForStepCompletion(context.Background(), "flow", StepExecutionID{StepType: "clientTestStep"}, WaitForStepCompletionOptions{RequestTimeout: time.Second})
		},
		"WaitForAttributeMatch": func(client *Client) error {
			var matched string
			return client.WaitForAttributeMatch(context.Background(), "flow", clientTestStatus, AttributeMatchEqual("ready"), &matched, WaitForAttributeOptions{RequestTimeout: time.Second})
		},
		"WaitForAttributeMapInstanceMatch": func(client *Client) error {
			var matched int
			return client.WaitForAttributeMapInstanceMatch(context.Background(), "flow", clientTestItems, "tenant-a", AttributeMatchEqual(1), &matched, WaitForAttributeOptions{RequestTimeout: time.Second})
		},
	}
	for name, waitCall := range waitCalls {
		t.Run(name, func(t *testing.T) {
			client, _ := newClientIntegration(t)
			service := &blockingProtocolFlowServiceClient{entered: make(chan struct{}, 8), release: make(chan struct{})}
			client.service = service
			defer close(service.release)
			requireRequestTimeout := func(err error) {
				t.Helper()
				var requestTimeout *RequestTimeoutError
				require.ErrorAs(t, err, &requestTimeout)
			}

			firstStarted := time.Now()
			requireRequestTimeout(waitCall(client))
			require.Less(t, time.Since(firstStarted), 4*time.Second)
			<-service.entered

			leaderFinished := make(chan error, 1)
			go func() { _, err := client.HealthCheck(context.Background()); leaderFinished <- err }()
			<-service.entered
			waiterFinished := make(chan error, 1)
			waiterStarted := time.Now()
			go func() { waiterFinished <- waitCall(client) }()
			select {
			case err := <-waiterFinished:
				requireRequestTimeout(err)
				require.Less(t, time.Since(waiterStarted), 4*time.Second)
			case <-time.After(5 * time.Second):
				t.Fatal("RequestTimeout did not bound the wait for another call's negotiation")
			}
			require.Equal(t, int32(2), service.negotiations.Load())
			select {
			case err := <-leaderFinished:
				t.Fatalf("negotiation without a deadline ended early: %v", err)
			default:
			}
		})
	}
}

func TestWorkerPullMalformedTaskResetsStreamWithoutRunningHandler(t *testing.T) {
	for name, task := range map[string]*dexpb.PollWorkerTask{
		"overflowing deadline":   {TaskId: "bad-deadline", Deadline: &timestamppb.Timestamp{Seconds: math.MaxInt64}, Request: reservedRPCTestTask("rpc", "unused").GetTask().GetRequest()},
		"invalid deadline nanos": {TaskId: "bad-nanos", Deadline: &timestamppb.Timestamp{Seconds: 1, Nanos: -1}, Request: reservedRPCTestTask("rpc", "unused").GetTask().GetRequest()},
		"missing task ID":        {Request: reservedRPCTestTask("rpc", "unused").GetTask().GetRequest()},
		"missing request":        {TaskId: "no-request"},
	} {
		t.Run(name, func(t *testing.T) {
			address, server := startPullTestServer(t)
			server.holdAfterEOFQueue = "*"
			flow := &reservedRPCFlow{entered: make(chan string, 4), release: make(chan struct{})}
			close(flow.release)
			worker, err := newWorkerForTest(t, []Flow{flow}, WorkerOptions{FlowServiceAddress: address, Pull: &PullWorkerOptions{MaxConcurrentTasks: 1}})
			require.NoError(t, err)
			started := make(chan error, 1)
			go func() { started <- worker.Start() }()
			stream := awaitPullRegistration(t, server)
			require.NoError(t, stream.send(&dexpb.PollWorkerTaskResponse{Frame: &dexpb.PollWorkerTaskResponse_Task{Task: task}}))
			select {
			case <-stream.stream.Context().Done():
			case <-time.After(5 * time.Second):
				t.Fatal("malformed task did not reset the stream")
			}
			select {
			case <-stream.eofReceived:
				t.Fatal("malformed task half-closed the stream")
			default:
			}
			replacement := awaitPullRegistration(t, server)
			requireNoHandlerCall(t, flow)
			requireNoPullFrame(t, stream)
			worker.pull.mu.Lock()
			require.Zero(t, worker.pull.busyTasks)
			worker.pull.mu.Unlock()
			require.NoError(t, replacement.send(reservedRPCTestTask("rpc", "after-malformed")))
			require.Equal(t, "rpc", awaitReservedRPCEntry(t, flow))
			require.NotNil(t, awaitFinalPullTaskFrame(t, replacement).GetRpc())
			close(replacement.allowReturn)
			stopCtx, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelStop()
			require.NoError(t, worker.Stop(stopCtx))
			require.NoError(t, <-started)
		})
	}
}
