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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestWorkerPullRegistrationConcurrencyLimit(t *testing.T) {
	address, server := startPullTestServer(t)
	server.maxConcurrentTasksPerAppWorker = 1
	worker, err := newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{FlowServiceAddress: address, Pull: &PullWorkerOptions{MaxConcurrentTasks: 3}})
	require.NoError(t, err)
	started := make(chan error, 1)
	go func() { started <- worker.Start() }()
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
}

func TestWorkerPullShutdownFinalOutputBeforeHalfCloseAndDropsLateTasks(t *testing.T) {
	address, server := startPullTestServer(t)
	server.holdAfterEOFQueue = "*"
	flow := &reservedRPCFlow{entered: make(chan string, 8), release: make(chan struct{})}
	worker, err := newWorkerForTest(t, []Flow{flow}, WorkerOptions{FlowServiceAddress: address, Pull: &PullWorkerOptions{MaxConcurrentTasks: 2}})
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
	require.NoError(t, busy.send(reservedRPCTestTask("rpc", "started-before-stop")))
	require.Equal(t, "rpc", awaitReservedRPCEntry(t, flow))
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Stop(context.Background()) }()
	awaitSlotHalfClose(t, idle)
	select {
	case <-busy.eofReceived:
		t.Fatal("busy slot half-closed before final output")
	default:
	}
	require.NoError(t, busy.send(reservedRPCTestTask("rpc", "after-stop-before-half-close")))
	require.NoError(t, idle.send(reservedRPCTestTask("rpc", "idle-after-half-close")))
	releaseOnce.Do(func() { close(flow.release) })
	output := awaitPullFrame(t, busy)
	require.Nil(t, output.GetReject())
	require.Equal(t, "started-before-stop", output.GetTaskFrame().GetTaskId())
	require.NotNil(t, output.GetTaskFrame().GetRpc())
	awaitSlotHalfClose(t, busy)
	require.NoError(t, busy.send(reservedRPCTestTask("rpc", "busy-after-half-close")))
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned before the Server ended the streams: %v", err)
	default:
	}
	close(idle.allowReturn)
	close(busy.allowReturn)
	require.NoError(t, <-stopped)
	require.NoError(t, <-started)
	select {
	case kind := <-flow.entered:
		t.Fatalf("late task called a handler: %s", kind)
	default:
	}
	for _, stream := range []*pullTestStream{busy, idle} {
		select {
		case frame := <-stream.frames:
			t.Fatalf("unexpected frame after shutdown: %v", frame)
		default:
		}
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
			require.ErrorContains(t, first, "protocol-test")
			require.ErrorContains(t, first, "protocol [1,3]")
			require.ErrorContains(t, first, "requires a server-zd release supporting protocol 4")
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
	require.ErrorContains(t, err, "old-server")
	require.ErrorContains(t, err, "protocol [1,3]")
	require.ErrorContains(t, err, "requires a server-zd release supporting protocol 4")
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
