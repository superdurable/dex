// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package workerclient

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeHostResolver struct {
	mu        sync.Mutex
	addresses []string
	err       error
	calls     int
}

func (r *fakeHostResolver) LookupHost(context.Context, string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return append([]string(nil), r.addresses...), r.err
}

func (r *fakeHostResolver) set(addresses []string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addresses = append([]string(nil), addresses...)
	r.err = err
}

func (r *fakeHostResolver) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type testWorkerHandler struct {
	dexpb.UnimplementedWorkerServiceServer

	mu              sync.Mutex
	calls           int
	failuresLeft    int
	failureStatus   codes.Code
	permanentStatus codes.Code
}

func (h *testWorkerHandler) InvokeWorkerRPC(
	context.Context,
	*dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	if h.failuresLeft > 0 {
		h.failuresLeft--
		failureStatus := h.failureStatus
		if failureStatus == codes.OK {
			failureStatus = codes.Unavailable
		}
		return nil, status.Error(failureStatus, "temporary")
	}
	if h.permanentStatus != codes.OK {
		return nil, status.Error(h.permanentStatus, "permanent")
	}
	return &dexpb.InvokeWorkerRPCResponse{}, nil
}

func (h *testWorkerHandler) callCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

func (h *testWorkerHandler) setPermanentStatus(statusCode codes.Code) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.permanentStatus = statusCode
}

type testWorkerServer struct {
	t          *testing.T
	listener   net.Listener
	grpcServer *grpc.Server
	serveError chan error
}

func newTestWorkerServer(
	t *testing.T,
	address string,
	handler dexpb.WorkerServiceServer,
) *testWorkerServer {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	require.NoError(t, err)
	grpcServer := grpc.NewServer()
	dexpb.RegisterWorkerServiceServer(grpcServer, handler)
	server := &testWorkerServer{
		t:          t,
		listener:   listener,
		grpcServer: grpcServer,
		serveError: make(chan error, 1),
	}
	go server.serve()
	t.Cleanup(server.close)
	return server
}

func (s *testWorkerServer) serve() {
	s.serveError <- s.grpcServer.Serve(s.listener)
}

func (s *testWorkerServer) close() {
	s.grpcServer.GracefulStop()
	if err := s.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		require.NoError(s.t, err)
	}
	if err := <-s.serveError; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		require.NoError(s.t, err)
	}
}

func (s *testWorkerServer) port() string {
	_, port, err := net.SplitHostPort(s.listener.Addr().String())
	require.NoError(s.t, err)
	return port
}

func TestWorkerClientPoolStickyRoutingAndDNSRefresh(t *testing.T) {
	firstHandler := &testWorkerHandler{}
	firstServer := newTestWorkerServer(t, "127.0.0.1:0", firstHandler)
	secondHandler := &testWorkerHandler{}
	newTestWorkerServer(t, net.JoinHostPort("::1", firstServer.port()), secondHandler)

	resolver := &fakeHostResolver{addresses: []string{"::1", "127.0.0.1"}}
	pool, err := newWorkerClientPool(testPoolConfig(), resolver)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	target := &dexpb.WorkerTarget{
		Address:           net.JoinHostPort("workers.test", firstServer.port()),
		IsHeadlessAddress: true,
	}

	invokeTestWorker(t, pool, target, "flow-one")
	invokeTestWorker(t, pool, target, "flow-one")
	invokeTestWorker(t, pool, target, "flow-two")
	require.Equal(t, 2, firstHandler.callCount())
	require.Equal(t, 1, secondHandler.callCount())

	requireStickyRoute(t, pool, target.GetAddress(), "flow-two")
	requireNoStickyRoute(t, pool, target.GetAddress(), "flow-one")

	resolver.set(nil, errors.New("dns unavailable"))
	previousCalls := resolver.callCount()
	require.Eventually(t, func() bool {
		return resolver.callCount() > previousCalls
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, 2, resolvedAddressCount(pool, target.GetAddress()))

	resolver.set([]string{"::1"}, nil)
	require.Eventually(t, func() bool {
		return resolvedAddressCount(pool, target.GetAddress()) == 1
	}, time.Second, 10*time.Millisecond)
	invokeTestWorker(t, pool, target, "flow-one")
	require.Equal(t, 2, firstHandler.callCount())
	require.Equal(t, 2, secondHandler.callCount())

	pool.Close()
	callsAfterClose := resolver.callCount()
	require.Never(t, func() bool {
		return resolver.callCount() > callsAfterClose
	}, 100*time.Millisecond, 10*time.Millisecond)
}

func TestWorkerClientPoolRetriesSameAddressWithBackoff(t *testing.T) {
	handler := &testWorkerHandler{failuresLeft: 1}
	server := newTestWorkerServer(t, "127.0.0.1:0", handler)
	resolver := &fakeHostResolver{addresses: []string{"127.0.0.1"}}
	cfg := testPoolConfig()
	cfg.Worker.WorkerServiceRequestMaxAttempts = 3
	pool, err := newWorkerClientPool(cfg, resolver)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	target := &dexpb.WorkerTarget{
		Address:           net.JoinHostPort("workers.test", server.port()),
		IsHeadlessAddress: true,
	}

	started := time.Now()
	invokeTestWorker(t, pool, target, "flow-one")
	require.GreaterOrEqual(t, time.Since(started), initialFailoverBackoff/2)
	require.Equal(t, 2, handler.callCount())
}

func TestWorkerClientPoolFailsOverOnServerDeadlineExceeded(t *testing.T) {
	handler := &testWorkerHandler{
		failuresLeft:  1,
		failureStatus: codes.DeadlineExceeded,
	}
	server := newTestWorkerServer(t, "127.0.0.1:0", handler)
	cfg := testPoolConfig()
	cfg.Worker.WorkerServiceRequestMaxAttempts = 2
	pool, err := newWorkerClientPool(
		cfg,
		&fakeHostResolver{addresses: []string{"127.0.0.1"}},
	)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	invokeTestWorker(
		t,
		pool,
		&dexpb.WorkerTarget{
			Address:           net.JoinHostPort("workers.test", server.port()),
			IsHeadlessAddress: true,
		},
		"flow-one",
	)
	require.Equal(t, 2, handler.callCount())
}

func TestWorkerClientPoolDoesNotRetryNonHeadlessTarget(t *testing.T) {
	handler := &testWorkerHandler{permanentStatus: codes.Unavailable}
	server := newTestWorkerServer(t, "127.0.0.1:0", handler)
	cfg := testPoolConfig()
	cfg.Worker.WorkerServiceRequestMaxAttempts = 3
	pool, err := newWorkerClientPool(cfg, &fakeHostResolver{})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	client, callCtx, release, err := pool.Acquire(
		context.Background(),
		&dexpb.WorkerTarget{Address: server.listener.Addr().String()},
		"flow-one",
	)
	require.NoError(t, err)
	defer release()
	_, err = client.InvokeWorkerRPC(callCtx, &dexpb.InvokeWorkerRPCRequest{})
	require.Error(t, err)
	require.Equal(t, 1, handler.callCount())
}

func TestWorkerClientPoolFailsOverAndUpdatesStickyRoute(t *testing.T) {
	firstHandler := &testWorkerHandler{permanentStatus: codes.Unavailable}
	firstServer := newTestWorkerServer(t, "127.0.0.1:0", firstHandler)
	secondHandler := &testWorkerHandler{}
	newTestWorkerServer(t, net.JoinHostPort("::1", firstServer.port()), secondHandler)
	resolver := &fakeHostResolver{addresses: []string{"127.0.0.1", "::1"}}
	cfg := testPoolConfig()
	cfg.Worker.WorkerServiceRequestMaxAttempts = 3
	pool, err := newWorkerClientPool(cfg, resolver)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	target := &dexpb.WorkerTarget{
		Address:           net.JoinHostPort("workers.test", firstServer.port()),
		IsHeadlessAddress: true,
	}

	resolvedAddress := invokeTestWorkerAndGetResolvedAddress(t, pool, target, "flow-one")
	require.Equal(t, 1, firstHandler.callCount())
	require.Equal(t, 1, secondHandler.callCount())
	require.Equal(t, secondHandlerAddress(firstServer), resolvedAddress)
	requireStickyAddress(t, pool, target.GetAddress(), "flow-one", secondHandlerAddress(firstServer))

	invokeTestWorker(t, pool, target, "flow-one")
	require.Equal(t, 1, firstHandler.callCount())
	require.Equal(t, 2, secondHandler.callCount())
}

func TestWorkerClientPoolStopsOnPermanentError(t *testing.T) {
	handler := &testWorkerHandler{permanentStatus: codes.InvalidArgument}
	server := newTestWorkerServer(t, "127.0.0.1:0", handler)
	pool, err := newWorkerClientPool(
		testPoolConfig(),
		&fakeHostResolver{addresses: []string{"127.0.0.1"}},
	)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	target := &dexpb.WorkerTarget{Address: server.listener.Addr().String()}

	client, callCtx, release, err := pool.Acquire(context.Background(), target, "flow-one")
	require.NoError(t, err)
	defer release()
	_, err = client.InvokeWorkerRPC(callCtx, &dexpb.InvokeWorkerRPCRequest{})
	require.Error(t, err)
	require.Equal(t, 1, handler.callCount())
}

func TestWorkerClientPoolStopsAfterMaxAttempts(t *testing.T) {
	handler := &testWorkerHandler{permanentStatus: codes.Unavailable}
	server := newTestWorkerServer(t, "127.0.0.1:0", handler)
	cfg := testPoolConfig()
	cfg.Worker.WorkerServiceRequestMaxAttempts = 3
	pool, err := newWorkerClientPool(
		cfg,
		&fakeHostResolver{addresses: []string{"127.0.0.1"}},
	)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	target := &dexpb.WorkerTarget{
		Address:           net.JoinHostPort("workers.test", server.port()),
		IsHeadlessAddress: true,
	}

	client, callCtx, release, err := pool.Acquire(context.Background(), target, "flow-one")
	require.NoError(t, err)
	defer release()
	_, err = client.InvokeWorkerRPC(callCtx, &dexpb.InvokeWorkerRPCRequest{})
	require.Error(t, err)
	require.Equal(t, 3, handler.callCount())
}

func TestWorkerClientPoolUsesConfiguredHeadlessFailoverStatusCodes(t *testing.T) {
	handler := &testWorkerHandler{
		failuresLeft:  1,
		failureStatus: codes.ResourceExhausted,
	}
	server := newTestWorkerServer(t, "127.0.0.1:0", handler)
	cfg := testPoolConfig()
	cfg.Worker.WorkerServiceRequestMaxAttempts = 2
	cfg.Worker.HeadlessFailoverStatusCodes = []int{int(codes.ResourceExhausted)}
	pool, err := newWorkerClientPool(
		cfg,
		&fakeHostResolver{addresses: []string{"127.0.0.1"}},
	)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	invokeTestWorker(
		t,
		pool,
		&dexpb.WorkerTarget{
			Address:           net.JoinHostPort("workers.test", server.port()),
			IsHeadlessAddress: true,
		},
		"flow-one",
	)
	require.Equal(t, 2, handler.callCount())
}

func TestWorkerClientPoolClearsFailedStickyRouteAfterFinalAttempt(t *testing.T) {
	firstHandler := &testWorkerHandler{}
	firstServer := newTestWorkerServer(t, "127.0.0.1:0", firstHandler)
	secondHandler := &testWorkerHandler{}
	newTestWorkerServer(t, net.JoinHostPort("::1", firstServer.port()), secondHandler)
	cfg := testPoolConfig()
	cfg.Worker.WorkerServiceRequestMaxAttempts = 1
	pool, err := newWorkerClientPool(
		cfg,
		&fakeHostResolver{addresses: []string{"127.0.0.1", "::1"}},
	)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	target := &dexpb.WorkerTarget{
		Address:           net.JoinHostPort("workers.test", firstServer.port()),
		IsHeadlessAddress: true,
	}

	invokeTestWorker(t, pool, target, "flow-one")
	requireStickyRoute(t, pool, target.GetAddress(), "flow-one")
	firstHandler.setPermanentStatus(codes.Unavailable)

	client, callCtx, release, err := pool.Acquire(context.Background(), target, "flow-one")
	require.NoError(t, err)
	_, err = client.InvokeWorkerRPC(callCtx, &dexpb.InvokeWorkerRPCRequest{})
	release()
	require.Error(t, err)
	requireNoStickyRoute(t, pool, target.GetAddress(), "flow-one")

	invokeTestWorker(t, pool, target, "flow-one")
	require.Equal(t, 2, firstHandler.callCount())
	require.Equal(t, 1, secondHandler.callCount())
}

func TestWorkerClientPoolHeadlessFailoverStatusCodeDefaultsAndValidation(t *testing.T) {
	pool, err := newWorkerClientPool(testPoolConfig(), &fakeHostResolver{})
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.Contains(t, pool.failoverStatusCodes, codes.Unavailable)
	require.Contains(t, pool.failoverStatusCodes, codes.DeadlineExceeded)
	require.Contains(t, pool.failoverStatusCodes, codes.Unknown)

	cfg := testPoolConfig()
	cfg.Worker.HeadlessFailoverStatusCodes = []int{0}
	_, err = newWorkerClientPool(cfg, &fakeHostResolver{})
	require.ErrorContains(t, err, "invalid headless failover gRPC status code 0")
}

func testPoolConfig() *config.Config {
	return &config.Config{
		Api: config.ApiConfig{
			GrpcMaxMessageBytes: 1024 * 1024,
		},
		Worker: config.WorkerConfig{
			WorkerConnectionIdleTimeout:     time.Minute,
			HeadlessAddressRefreshInterval:  20 * time.Millisecond,
			MaxWorkerConnections:            10,
			MaxStickyRoutingEntries:         1,
			WorkerServiceRequestMaxAttempts: 1,
		},
	}
}

func invokeTestWorker(
	t *testing.T,
	pool *WorkerClientPool,
	target *dexpb.WorkerTarget,
	flowID string,
) {
	t.Helper()
	invokeTestWorkerAndGetResolvedAddress(t, pool, target, flowID)
}

func invokeTestWorkerAndGetResolvedAddress(
	t *testing.T,
	pool *WorkerClientPool,
	target *dexpb.WorkerTarget,
	flowID string,
) string {
	t.Helper()
	client, callCtx, release, err := pool.Acquire(context.Background(), target, flowID)
	require.NoError(t, err)
	defer release()
	_, err = client.InvokeWorkerRPC(callCtx, &dexpb.InvokeWorkerRPCRequest{})
	require.NoError(t, err)
	return ResolvedWorkerAddressFromContext(callCtx)
}

func requireStickyRoute(t *testing.T, pool *WorkerClientPool, target, flowID string) {
	t.Helper()
	pool.mu.Lock()
	defer pool.mu.Unlock()
	_, ok := pool.sticky[stickyKey{target: target, flowID: flowID}]
	require.True(t, ok)
}

func requireNoStickyRoute(t *testing.T, pool *WorkerClientPool, target, flowID string) {
	t.Helper()
	pool.mu.Lock()
	defer pool.mu.Unlock()
	_, ok := pool.sticky[stickyKey{target: target, flowID: flowID}]
	require.False(t, ok)
}

func requireStickyAddress(
	t *testing.T,
	pool *WorkerClientPool,
	target string,
	flowID string,
	address string,
) {
	t.Helper()
	pool.mu.Lock()
	defer pool.mu.Unlock()
	element := pool.sticky[stickyKey{target: target, flowID: flowID}]
	require.NotNil(t, element)
	require.Equal(t, address, element.Value.(*stickyRoute).address)
}

func secondHandlerAddress(firstServer *testWorkerServer) string {
	return net.JoinHostPort("::1", firstServer.port())
}

func resolvedAddressCount(pool *WorkerClientPool, target string) int {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	return len(pool.headless[target].addresses)
}
