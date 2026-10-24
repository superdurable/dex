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
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	apiKeyAccessTokenA = "access-token-a"
	apiKeyAccessTokenB = "access-token-b"
)

// bearerTokenGate rejects calls whose bearer token it does not accept and records every attempt.
type bearerTokenGate struct {
	mu         sync.Mutex
	isAccepted func(fullMethod string, authorization string) bool
	attempts   map[string][]string
	rejections map[string]int
}

func newBearerTokenGate(isAccepted func(fullMethod string, authorization string) bool) *bearerTokenGate {
	return &bearerTokenGate{isAccepted: isAccepted, attempts: map[string][]string{}, rejections: map[string]int{}}
}

func (gate *bearerTokenGate) authorize(ctx context.Context, fullMethod string) error {
	incoming, _ := metadata.FromIncomingContext(ctx)
	authorization := strings.Join(incoming.Get("authorization"), ",")
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.attempts[fullMethod] = append(gate.attempts[fullMethod], authorization)
	if gate.isAccepted(fullMethod, authorization) {
		return nil
	}
	gate.rejections[fullMethod]++
	return status.Error(codes.Unauthenticated, "bearer token rejected")
}

func (gate *bearerTokenGate) interceptUnary(
	ctx context.Context,
	request any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	if err := gate.authorize(ctx, info.FullMethod); err != nil {
		return nil, err
	}
	return handler(ctx, request)
}

func (gate *bearerTokenGate) interceptStream(
	service any,
	stream grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	if err := gate.authorize(stream.Context(), info.FullMethod); err != nil {
		return err
	}
	return handler(service, stream)
}

func (gate *bearerTokenGate) acceptOnly(authorization string) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.isAccepted = func(_ string, received string) bool { return received == authorization }
}

func (gate *bearerTokenGate) rejectAll() {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.isAccepted = func(string, string) bool { return false }
}

func (gate *bearerTokenGate) attemptsFor(fullMethod string) []string {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return append([]string(nil), gate.attempts[fullMethod]...)
}

func (gate *bearerTokenGate) rejectionsFor(fullMethod string) int {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.rejections[fullMethod]
}

type healthCheckFlowService struct {
	workerSyncFlowService
}

func (*healthCheckFlowService) HealthCheck(context.Context, *emptypb.Empty) (*dexpb.HealthInfo, error) {
	return &dexpb.HealthInfo{Condition: "OK"}, nil
}

func startBearerTokenGatedServer(
	t *testing.T,
	gate *bearerTokenGate,
	workerTaskService dexpb.WorkerTaskServiceServer,
) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(gate.interceptUnary), grpc.StreamInterceptor(gate.interceptStream))
	dexpb.RegisterFlowServiceServer(grpcServer, &healthCheckFlowService{})
	if workerTaskService != nil {
		dexpb.RegisterWorkerTaskServiceServer(grpcServer, workerTaskService)
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		requireServeStopped(t, <-serveResult)
	})
	return listener.Addr().String()
}

// startSequentialAPIKeyTokenEndpoint hands out access-token-a, then access-token-b, and so on.
func startSequentialAPIKeyTokenEndpoint(t *testing.T) *apiKeyTokenEndpoint {
	return startAPIKeyTokenEndpoint(t, func(requestNumber int, writer http.ResponseWriter, _ *http.Request) {
		writeAPIKeyAccessToken(writer, fmt.Sprintf("access-token-%c", 'a'+requestNumber-1), 3600)
	})
}

func newAPIKeyTestClient(t *testing.T, address string, credential Credential) *Client {
	t.Helper()
	registry, err := NewRegistry(nil)
	require.NoError(t, err)
	cache, err := blobcache.New(&blobcache.Config{Dir: t.TempDir(), MaxBytes: 1 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	client, err := NewClient(registry, cache, ClientOptions{FlowServiceAddress: address, Credential: credential})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client
}

func TestClientRetriesRejectedUnaryCallOnceWithFreshAPIKeyToken(t *testing.T) {
	gate := newBearerTokenGate(func(_ string, authorization string) bool { return authorization == "Bearer "+apiKeyAccessTokenB })
	address := startBearerTokenGatedServer(t, gate, nil)
	tokenEndpoint := startSequentialAPIKeyTokenEndpoint(t)
	credential, err := APIKeyCredential(tokenEndpoint.tokenURL(), testAPIServerID, testAPIKey)
	require.NoError(t, err)
	client := newAPIKeyTestClient(t, address, credential)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	health, err := client.HealthCheck(ctx)
	require.NoError(t, err)
	require.Equal(t, "OK", health.Condition)
	require.Equal(t,
		[]string{"Bearer " + apiKeyAccessTokenA, "Bearer " + apiKeyAccessTokenB},
		gate.attemptsFor(dexpb.FlowService_HealthCheck_FullMethodName),
		"the rejected call is retried exactly once with a fresh token",
	)
	require.Equal(t, 2, tokenEndpoint.exchangeCount(), "the rejection causes exactly one exchange")

	_, err = client.HealthCheck(ctx)
	require.NoError(t, err)
	require.Len(t, gate.attemptsFor(dexpb.FlowService_HealthCheck_FullMethodName), 3)
	require.Equal(t, 2, tokenEndpoint.exchangeCount(), "the fresh token is cached")
}

func TestClientFailsUnaryCallAfterSecondRejectedAPIKeyToken(t *testing.T) {
	gate := newBearerTokenGate(func(string, string) bool { return false })
	address := startBearerTokenGatedServer(t, gate, nil)
	tokenEndpoint := startSequentialAPIKeyTokenEndpoint(t)
	credential, err := APIKeyCredential(tokenEndpoint.tokenURL(), testAPIServerID, testAPIKey)
	require.NoError(t, err)
	client := newAPIKeyTestClient(t, address, credential)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = client.HealthCheck(ctx)
	var serviceError *ServiceError
	require.True(t, errors.As(err, &serviceError), "error: %v", err)
	require.Equal(t, codes.Unauthenticated, serviceError.Code)
	require.Equal(t,
		[]string{"Bearer " + apiKeyAccessTokenA, "Bearer " + apiKeyAccessTokenB},
		gate.attemptsFor(dexpb.FlowService_HealthCheck_FullMethodName),
		"a second rejection fails the call without another attempt",
	)
	require.Equal(t, 2, tokenEndpoint.exchangeCount())
}

func TestClientRetryOfRejectedUnaryCallWaitsForFreshAPIKeyTokenOnlyUntilCallContextEnds(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		expectedErr error
	}{
		{name: "deadline", expectedErr: context.DeadlineExceeded},
		{name: "cancellation", expectedErr: context.Canceled},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			gate := newBearerTokenGate(func(_ string, authorization string) bool { return authorization != "Bearer "+apiKeyAccessTokenA })
			address := startBearerTokenGatedServer(t, gate, nil)
			refreshStarted := make(chan struct{})
			releaseRefresh := make(chan struct{})
			tokenEndpoint := startAPIKeyTokenEndpoint(t, func(requestNumber int, writer http.ResponseWriter, _ *http.Request) {
				if requestNumber == 1 {
					writeAPIKeyAccessToken(writer, apiKeyAccessTokenA, 3600)
					return
				}
				close(refreshStarted)
				<-releaseRefresh
				writeAPIKeyAccessToken(writer, apiKeyAccessTokenB, 3600)
			})
			// Registered after the endpoint, so it runs first and lets the endpoint shut down.
			t.Cleanup(func() { close(releaseRefresh) })
			credential, err := APIKeyCredential(tokenEndpoint.tokenURL(), testAPIServerID, testAPIKey)
			require.NoError(t, err)
			client := newAPIKeyTestClient(t, address, credential)
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			if errors.Is(scenario.expectedErr, context.Canceled) {
				go func() {
					<-refreshStarted
					cancel()
				}()
			}

			startedAt := time.Now()
			_, err = client.HealthCheck(ctx)
			require.ErrorIs(t, err, scenario.expectedErr)
			require.Less(t, time.Since(startedAt), 3*time.Second, "the call does not wait for the 10 second exchange timeout")
			select {
			case <-refreshStarted:
			default:
				t.Fatal("the rejection did not start a refresh")
			}
			require.Equal(t,
				[]string{"Bearer " + apiKeyAccessTokenA},
				gate.attemptsFor(dexpb.FlowService_HealthCheck_FullMethodName),
				"the retry is abandoned before it reaches Dex",
			)
		})
	}
}

// startAPIKeyPullWorker starts a pull Worker whose poll streams use credential, and returns its streams by queue.
func startAPIKeyPullWorker(
	t *testing.T,
	address string,
	credential Credential,
	pullServer *pullTestServer,
	configurePull func(*Worker),
) (*Worker, map[string]*pullTestStream) {
	t.Helper()
	worker, err := newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{
		FlowServiceAddress: address,
		Credential:         credential,
		Pull:               &PullWorkerOptions{MaxConcurrentTasks: 1, AppWorkerID: apiKeyTestAppWorkerID},
	})
	require.NoError(t, err)
	if configurePull != nil {
		configurePull(worker)
	}
	started := make(chan error, 1)
	go func() { started <- worker.Start() }()
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, worker.Stop(stopCtx))
		require.NoError(t, <-started)
	})
	registeredByQueue := map[string]*pullTestStream{}
	for len(registeredByQueue) < 2 {
		stream := awaitPullRegistration(t, pullServer)
		registeredByQueue[stream.registration.Queue] = stream
	}
	require.NotNil(t, registeredByQueue[GetFinalFlowType(workerFlow)])
	require.NotNil(t, registeredByQueue[apiKeyTestAppWorkerID])
	return worker, registeredByQueue
}

const apiKeyTestAppWorkerID = "api-key-worker-12345678"

func TestWorkerPollStreamDiscardsRejectedAPIKeyTokenAndReconnectsAtOnceWithFreshToken(t *testing.T) {
	gate := newBearerTokenGate(func(string, string) bool { return true })
	pullServer := &pullTestServer{registered: make(chan *pullTestStream, 32), firstReconnectQueue: make(chan string, 1)}
	address := startBearerTokenGatedServer(t, gate, pullServer)
	tokenEndpoint := startSequentialAPIKeyTokenEndpoint(t)
	credential, err := APIKeyCredential(tokenEndpoint.tokenURL(), testAPIServerID, testAPIKey)
	require.NoError(t, err)
	_, registeredByQueue := startAPIKeyPullWorker(t, address, credential, pullServer, nil)
	flowTypeStream := registeredByQueue[GetFinalFlowType(workerFlow)]
	appWorkerStream := registeredByQueue[apiKeyTestAppWorkerID]
	require.Equal(t, "Bearer "+apiKeyAccessTokenA, pullStreamAuthorization(flowTypeStream))
	require.Equal(t, "Bearer "+apiKeyAccessTokenA, pullStreamAuthorization(appWorkerStream))
	require.Equal(t, 1, tokenEndpoint.exchangeCount())

	gate.acceptOnly("Bearer " + apiKeyAccessTokenB)
	close(flowTypeStream.abort)
	reconnected := awaitPullRegistration(t, pullServer)
	require.Equal(t, GetFinalFlowType(workerFlow), reconnected.registration.Queue)
	require.Equal(t, "Bearer "+apiKeyAccessTokenB, pullStreamAuthorization(reconnected),
		"the rejected stream reconnects at once with a fresh token instead of waiting about a minute")
	require.Equal(t, 1, gate.rejectionsFor(dexpb.WorkerTaskService_PollWorkerTask_FullMethodName))
	require.Equal(t, 2, tokenEndpoint.exchangeCount())

	close(appWorkerStream.abort)
	reconnected = awaitPullRegistration(t, pullServer)
	require.Equal(t, apiKeyTestAppWorkerID, reconnected.registration.Queue)
	require.Equal(t, "Bearer "+apiKeyAccessTokenB, pullStreamAuthorization(reconnected), "other streams reuse the fresh token")
	require.Equal(t, 1, gate.rejectionsFor(dexpb.WorkerTaskService_PollWorkerTask_FullMethodName))
	require.Equal(t, 2, tokenEndpoint.exchangeCount())
}

func TestWorkerPollStreamFallsBackToBackoffAfterFreshAPIKeyTokenIsRejected(t *testing.T) {
	gate := newBearerTokenGate(func(string, string) bool { return true })
	pullServer := &pullTestServer{registered: make(chan *pullTestStream, 32), firstReconnectQueue: make(chan string, 1)}
	address := startBearerTokenGatedServer(t, gate, pullServer)
	tokenEndpoint := startSequentialAPIKeyTokenEndpoint(t)
	credential, err := APIKeyCredential(tokenEndpoint.tokenURL(), testAPIServerID, testAPIKey)
	require.NoError(t, err)
	worker, registeredByQueue := startAPIKeyPullWorker(t, address, credential, pullServer, nil)
	flowType := GetFinalFlowType(workerFlow)

	gate.rejectAll()
	close(registeredByQueue[flowType].abort)
	require.Eventually(t, func() bool {
		return gate.rejectionsFor(dexpb.WorkerTaskService_PollWorkerTask_FullMethodName) == 2
	}, 5*time.Second, 10*time.Millisecond, "the rejected stream reconnects once at once")
	require.Equal(t,
		[]string{"Bearer " + apiKeyAccessTokenA, "Bearer " + apiKeyAccessTokenB},
		gate.attemptsFor(dexpb.WorkerTaskService_PollWorkerTask_FullMethodName)[2:],
		"after the two initial streams, the rejected reconnection is retried at once with a fresh token",
	)
	require.Eventually(t, func() bool {
		state := worker.PullRegistrationState().Queues[queueStateIndex(worker, flowType)]
		return status.Code(state.LastPermanentError) == codes.Unauthenticated
	}, 5*time.Second, 10*time.Millisecond, "the second rejection is reported as permanent")
	require.Never(t, func() bool {
		return gate.rejectionsFor(dexpb.WorkerTaskService_PollWorkerTask_FullMethodName) > 2
	}, 3*time.Second, 50*time.Millisecond, "a second rejection falls back to the long backoff")
	require.Equal(t, 2, tokenEndpoint.exchangeCount())
}

// sendAfterRejection delays each send on a stream whose token the server rejects until that rejection arrives.
type sendAfterRejection struct {
	isRejected func(authorization string) bool

	mu       sync.Mutex
	sendErrs []error
}

func (interceptor *sendAfterRejection) interceptStream(
	ctx context.Context,
	descriptor *grpc.StreamDesc,
	connection *grpc.ClientConn,
	method string,
	streamer grpc.Streamer,
	callOptions ...grpc.CallOption,
) (grpc.ClientStream, error) {
	stream, err := streamer(ctx, descriptor, connection, method, callOptions...)
	if err != nil {
		return nil, err
	}
	outgoing, _ := metadata.FromOutgoingContext(ctx)
	if !interceptor.isRejected(strings.Join(outgoing.Get("authorization"), ",")) {
		return stream, nil
	}
	return &sendAfterRejectionStream{ClientStream: stream, interceptor: interceptor}, nil
}

func (interceptor *sendAfterRejection) recordedSendErrors() []error {
	interceptor.mu.Lock()
	defer interceptor.mu.Unlock()
	return append([]error(nil), interceptor.sendErrs...)
}

type sendAfterRejectionStream struct {
	grpc.ClientStream
	interceptor *sendAfterRejection
}

func (stream *sendAfterRejectionStream) SendMsg(message any) error {
	// Header returns once the server's rejection arrives, so the send below fails.
	if _, err := stream.ClientStream.Header(); err != nil {
		return err
	}
	err := stream.ClientStream.SendMsg(message)
	stream.interceptor.mu.Lock()
	stream.interceptor.sendErrs = append(stream.interceptor.sendErrs, err)
	stream.interceptor.mu.Unlock()
	return err
}

func TestWorkerPollStreamRejectedBeforeRegistrationIsSentReconnectsWithFreshAPIKeyToken(t *testing.T) {
	gate := newBearerTokenGate(func(fullMethod string, authorization string) bool {
		return fullMethod != dexpb.WorkerTaskService_PollWorkerTask_FullMethodName || authorization == "Bearer "+apiKeyAccessTokenB
	})
	pullServer := &pullTestServer{registered: make(chan *pullTestStream, 32), firstReconnectQueue: make(chan string, 1)}
	address := startBearerTokenGatedServer(t, gate, pullServer)
	tokenEndpoint := startSequentialAPIKeyTokenEndpoint(t)
	credential, err := APIKeyCredential(tokenEndpoint.tokenURL(), testAPIServerID, testAPIKey)
	require.NoError(t, err)
	delayedSends := &sendAfterRejection{isRejected: func(authorization string) bool { return authorization == "Bearer "+apiKeyAccessTokenA }}
	worker, registeredByQueue := startAPIKeyPullWorker(t, address, credential, pullServer, func(worker *Worker) {
		worker.pull.client = newDelayedSendPollClient(t, address, credential, delayedSends)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, worker.WaitPullRegistered(ctx))
	for queue, stream := range registeredByQueue {
		require.Equal(t, "Bearer "+apiKeyAccessTokenB, pullStreamAuthorization(stream), queue)
	}
	sendErrors := delayedSends.recordedSendErrors()
	require.Len(t, sendErrors, 2, "each queue's first registration send happened after its rejection")
	for _, sendErr := range sendErrors {
		require.ErrorIs(t, sendErr, io.EOF, "gRPC reports a send on a rejected stream as io.EOF")
	}
	require.Equal(t, 2, gate.rejectionsFor(dexpb.WorkerTaskService_PollWorkerTask_FullMethodName))
	require.Equal(t, 2, tokenEndpoint.exchangeCount(), "the rejected token is discarded once and refreshed once")
}

func TestWorkerReportsUnauthenticatedPollStreamRejectedBeforeRegistrationIsSent(t *testing.T) {
	gate := newBearerTokenGate(func(fullMethod string, _ string) bool {
		return fullMethod != dexpb.WorkerTaskService_PollWorkerTask_FullMethodName
	})
	pullServer := &pullTestServer{registered: make(chan *pullTestStream, 32), firstReconnectQueue: make(chan string, 1)}
	address := startBearerTokenGatedServer(t, gate, pullServer)
	tokenEndpoint := startSequentialAPIKeyTokenEndpoint(t)
	credential, err := APIKeyCredential(tokenEndpoint.tokenURL(), testAPIServerID, testAPIKey)
	require.NoError(t, err)
	delayedSends := &sendAfterRejection{isRejected: func(string) bool { return true }}
	worker, err := newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{
		FlowServiceAddress: address,
		Credential:         credential,
		Pull:               &PullWorkerOptions{MaxConcurrentTasks: 1, AppWorkerID: apiKeyTestAppWorkerID},
	})
	require.NoError(t, err)
	worker.pull.client = newDelayedSendPollClient(t, address, credential, delayedSends)
	started := make(chan error, 1)
	go func() { started <- worker.Start() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = worker.WaitPullRegistered(ctx)
	require.Equal(t, codes.Unauthenticated, status.Code(err), "the final status replaces io.EOF: %v", err)
	require.Never(t, func() bool {
		return gate.rejectionsFor(dexpb.WorkerTaskService_PollWorkerTask_FullMethodName) > 4
	}, 2*time.Second, 50*time.Millisecond, "each queue reconnects once at once, then backs off")
	require.Equal(t, 4, gate.rejectionsFor(dexpb.WorkerTaskService_PollWorkerTask_FullMethodName))
	require.Len(t, delayedSends.recordedSendErrors(), 4)
	for _, queueState := range worker.PullRegistrationState().Queues {
		require.Equal(t, codes.Unauthenticated, status.Code(queueState.LastPermanentError), queueState.Queue)
	}
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStop()
	require.NoError(t, worker.Stop(stopCtx))
	require.NoError(t, <-started)
}

// newDelayedSendPollClient returns a poll client whose streams carry credential and then delayedSends.
func newDelayedSendPollClient(
	t *testing.T,
	address string,
	credential Credential,
	delayedSends *sendAfterRejection,
) dexpb.WorkerTaskServiceClient {
	t.Helper()
	connection, err := grpc.NewClient(address, append(credentialDialOptions(credential),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainStreamInterceptor(delayedSends.interceptStream),
	)...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	return dexpb.NewWorkerTaskServiceClient(connection)
}

func pullStreamAuthorization(stream *pullTestStream) string {
	incoming, _ := metadata.FromIncomingContext(stream.stream.Context())
	return strings.Join(incoming.Get("authorization"), ",")
}
