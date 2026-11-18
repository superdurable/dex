// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build pullintegration

package dex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// adaptiveSlotTestServer runs one dex-server-zd process per test, so a test can set caps and restart it.
type adaptiveSlotTestServer struct {
	t             *testing.T
	binary        string
	dataDirectory string
	logPath       string
	address       string
	command       *exec.Cmd
	exited        chan struct{}
}

// adaptiveSlotServerLimits sets matching limits; zero keeps the server default.
type adaptiveSlotServerLimits struct {
	maxConcurrentTasksCap int
	maxStreams            int
}

// pullSlotProxy forwards a Worker's gRPC traffic to the current server and observes its slot streams.
type pullSlotProxy struct {
	t        *testing.T
	listener net.Listener
	server   *grpc.Server
	mu       sync.Mutex
	upstream *grpc.ClientConn
	streams  map[*pullSlotProxyStream]struct{}
	// registers lists every Register frame, in arrival order.
	registers []pullSlotProxyRegister
	// acknowledgements lists every Registered frame forwarded to a Worker, in order.
	acknowledgements []pullSlotProxyRegister
	// stepRegisterGate, when set, holds STEP_AND_RPC Registers until an RPC_ONLY stream is acknowledged.
	stepRegisterGate chan struct{}
	// acknowledgementGate, when set, holds every acknowledgement until it closes.
	acknowledgementGate chan struct{}
	firstAckForwardedAt time.Time
}

type pullSlotProxyStream struct {
	appWorkerID  string
	slotKind     dexpb.PollWorkerSlotKind
	isRegistered bool
	hasEnded     bool
	isReset      atomic.Bool
	cancel       context.CancelFunc
}

type pullSlotProxyRegister struct {
	appWorkerID string
	slotKind    dexpb.PollWorkerSlotKind
	at          time.Time
}

// pullSlotCompetitor holds node slots under another app Worker ID and retakes any freed slot at once.
type pullSlotCompetitor struct {
	t       *testing.T
	client  dexpb.WorkerTaskServiceClient
	conn    *grpc.ClientConn
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	held    map[*pullSlotCompetitorStream]struct{}
	appID   string
	stopped atomic.Bool
}

type pullSlotCompetitorStream struct {
	cancel context.CancelFunc
}

// rawProxyFrame carries one encoded gRPC message through the proxy unchanged.
type rawProxyFrame struct {
	payload []byte
}

type rawProxyCodec struct{}

const pullSlotProxyResetMessage = "pull slot proxy reset the stream"

func startAdaptiveSlotTestServer(t *testing.T, limits adaptiveSlotServerLimits) *adaptiveSlotTestServer {
	t.Helper()
	binary := os.Getenv("DEX_SERVER_ZD_BINARY")
	require.NotEmpty(t, binary, "set DEX_SERVER_ZD_BINARY to a dex-server-zd binary")
	dataDirectory := t.TempDir()
	server := &adaptiveSlotTestServer{t: t, binary: binary, dataDirectory: dataDirectory, logPath: filepath.Join(dataDirectory, "server.log")}
	server.launch(limits)
	t.Cleanup(func() {
		server.stop()
		if t.Failed() {
			server.logTail()
		}
	})
	return server
}

func (server *adaptiveSlotTestServer) launch(limits adaptiveSlotServerLimits) {
	t := server.t
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server.address = listener.Addr().String()
	require.NoError(t, listener.Close())
	configuration := "dev:\n  durabilityMode: processCrashSafe\nmatching:\n  pingInterval: 2s\n"
	if limits.maxConcurrentTasksCap != 0 {
		configuration += fmt.Sprintf("  maxConcurrentTasksCap: %d\n", limits.maxConcurrentTasksCap)
	}
	if limits.maxStreams != 0 {
		configuration += fmt.Sprintf("  maxStreams: %d\n", limits.maxStreams)
	}
	configurationPath := filepath.Join(server.dataDirectory, "adaptive-slot-config.yaml")
	require.NoError(t, os.WriteFile(configurationPath, []byte(configuration), 0o600))
	logFile, err := os.OpenFile(server.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	command := exec.Command(server.binary, "dev", "--data-dir", filepath.Join(server.dataDirectory, "data"), "--listen", server.address, "--config", configurationPath)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Stdout = logFile
	command.Stderr = logFile
	require.NoError(t, command.Start())
	exited := make(chan struct{})
	go func() {
		// The exit status of a killed test server carries no information.
		if waitErr := command.Wait(); waitErr != nil {
			t.Logf("dex-server-zd exited: %v", waitErr)
		}
		if closeErr := logFile.Close(); closeErr != nil {
			t.Logf("close dex-server-zd log: %v", closeErr)
		}
		close(exited)
	}()
	server.command = command
	server.exited = exited
	require.NoError(t, waitForAdaptiveSlotTestServer(server.address, exited))
}

func waitForAdaptiveSlotTestServer(address string, exited <-chan struct{}) (waitErr error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() { waitErr = errors.Join(waitErr, conn.Close()) }()
	health := grpc_health_v1.NewHealthClient(conn)
	deadline := time.Now().Add(30 * time.Second)
	for {
		checkCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		response, checkErr := health.Check(checkCtx, &grpc_health_v1.HealthCheckRequest{})
		cancel()
		if checkErr == nil && response.GetStatus() == grpc_health_v1.HealthCheckResponse_SERVING {
			return nil
		}
		select {
		case <-exited:
			return fmt.Errorf("dex-server-zd exited before serving")
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("dex-server-zd did not serve: %v", checkErr)
		}
	}
}

// kill ends the server like a crash: every stream breaks and nothing drains.
func (server *adaptiveSlotTestServer) kill() {
	if err := syscall.Kill(-server.command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		server.t.Errorf("kill dex-server-zd: %v", err)
	}
	<-server.exited
}

// restart starts a new process on the same data directory, on a new address.
func (server *adaptiveSlotTestServer) restart(limits adaptiveSlotServerLimits) {
	server.launch(limits)
}

func (server *adaptiveSlotTestServer) stop() {
	select {
	case <-server.exited:
		return
	default:
	}
	if err := syscall.Kill(-server.command.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		server.t.Errorf("terminate dex-server-zd: %v", err)
	}
	select {
	case <-server.exited:
	case <-time.After(10 * time.Second):
		server.kill()
	}
}

func (server *adaptiveSlotTestServer) logTail() {
	output, err := os.ReadFile(server.logPath)
	if err != nil {
		server.t.Logf("read dex-server-zd log: %v", err)
		return
	}
	lines := strings.Split(string(output), "\n")
	server.t.Logf("dex-server-zd log tail:\n%s", strings.Join(lines[max(0, len(lines)-200):], "\n"))
}

// newClient returns a client of the current process; a restart needs a new client.
func (server *adaptiveSlotTestServer) newClient(t *testing.T, registry *Registry) *Client {
	t.Helper()
	cache, err := blobcache.New(&blobcache.Config{Dir: t.TempDir(), MaxBytes: 1 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	client, err := NewClient(registry, cache, ClientOptions{FlowServiceAddress: server.address})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client
}

func startPullSlotProxy(t *testing.T, upstreamAddress string) *pullSlotProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	proxy := &pullSlotProxy{t: t, listener: listener, streams: make(map[*pullSlotProxyStream]struct{})}
	proxy.setUpstream(upstreamAddress)
	proxy.server = grpc.NewServer(grpc.ForceServerCodec(rawProxyCodec{}), grpc.UnknownServiceHandler(proxy.forward))
	served := make(chan error, 1)
	go func() { served <- proxy.server.Serve(listener) }()
	t.Cleanup(func() {
		proxy.server.Stop()
		require.NoError(t, <-served)
		proxy.mu.Lock()
		defer proxy.mu.Unlock()
		require.NoError(t, proxy.upstream.Close())
	})
	return proxy
}

func (proxy *pullSlotProxy) address() string { return proxy.listener.Addr().String() }

// setUpstream points new calls at address; calls already open stay on the old server.
func (proxy *pullSlotProxy) setUpstream(address string) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(rawProxyCodec{})))
	require.NoError(proxy.t, err)
	proxy.mu.Lock()
	previous := proxy.upstream
	proxy.upstream = conn
	proxy.mu.Unlock()
	if previous != nil {
		// The old server is gone, so its connection has nothing left to flush.
		if closeErr := previous.Close(); closeErr != nil {
			proxy.t.Logf("close old upstream: %v", closeErr)
		}
	}
}

func (proxy *pullSlotProxy) forward(_ any, downstream grpc.ServerStream) error {
	method, ok := grpc.MethodFromServerStream(downstream)
	if !ok {
		return status.Error(codes.Internal, "proxy: no method")
	}
	ctx, cancel := context.WithCancel(downstream.Context())
	defer cancel()
	if incoming, hasMetadata := metadata.FromIncomingContext(downstream.Context()); hasMetadata {
		ctx = metadata.NewOutgoingContext(ctx, incoming)
	}
	if method == dexpb.WorkerTaskService_PollWorkerTask_FullMethodName {
		return proxy.forwardPollWorkerTask(ctx, cancel, downstream)
	}
	proxy.mu.Lock()
	conn := proxy.upstream
	proxy.mu.Unlock()
	upstream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true, ClientStreams: true}, method)
	if err != nil {
		return err
	}
	go pumpDownstreamFrames(downstream, upstream, nil)
	return pumpUpstreamFrames(upstream, downstream, nil)
}

// forwardPollWorkerTask opens the upstream stream only after the Register passes its gate.
func (proxy *pullSlotProxy) forwardPollWorkerTask(ctx context.Context, cancel context.CancelFunc, downstream grpc.ServerStream) error {
	first := &rawProxyFrame{}
	if err := downstream.RecvMsg(first); err != nil {
		return err
	}
	request := &dexpb.PollWorkerTaskRequest{}
	if err := proto.Unmarshal(first.payload, request); err != nil {
		return status.Errorf(codes.InvalidArgument, "proxy: decode first frame: %v", err)
	}
	stream := &pullSlotProxyStream{appWorkerID: request.GetRegister().GetAppWorkerId(), slotKind: request.GetRegister().GetSlotKind(), cancel: cancel}
	proxy.mu.Lock()
	proxy.registers = append(proxy.registers, pullSlotProxyRegister{appWorkerID: stream.appWorkerID, slotKind: stream.slotKind, at: time.Now()})
	proxy.streams[stream] = struct{}{}
	gate := proxy.stepRegisterGate
	conn := proxy.upstream
	proxy.mu.Unlock()
	defer proxy.markEnded(stream)
	if gate != nil && stream.slotKind == dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_STEP_AND_RPC {
		select {
		case <-gate:
		case <-time.After(15 * time.Second):
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		}
		proxy.mu.Lock()
		conn = proxy.upstream
		proxy.mu.Unlock()
	}
	upstream, err := conn.NewStream(ctx, &dexpb.WorkerTaskService_ServiceDesc.Streams[0], dexpb.WorkerTaskService_PollWorkerTask_FullMethodName)
	if err != nil {
		return err
	}
	if err := upstream.SendMsg(first); err != nil {
		return proxy.resetStatusOr(stream, err)
	}
	go pumpDownstreamFrames(downstream, upstream, nil)
	err = pumpUpstreamFrames(upstream, downstream, func(frame *rawProxyFrame) {
		response := &dexpb.PollWorkerTaskResponse{}
		if decodeErr := proto.Unmarshal(frame.payload, response); decodeErr != nil || response.GetRegistered() == nil {
			return
		}
		proxy.recordAcknowledgement(stream)
	})
	return proxy.resetStatusOr(stream, err)
}

func (proxy *pullSlotProxy) recordAcknowledgement(stream *pullSlotProxyStream) {
	proxy.mu.Lock()
	ackGate := proxy.acknowledgementGate
	proxy.mu.Unlock()
	if ackGate != nil {
		<-ackGate
	}
	proxy.mu.Lock()
	stream.isRegistered = true
	proxy.acknowledgements = append(proxy.acknowledgements, pullSlotProxyRegister{appWorkerID: stream.appWorkerID, slotKind: stream.slotKind, at: time.Now()})
	if proxy.firstAckForwardedAt.IsZero() {
		proxy.firstAckForwardedAt = time.Now()
	}
	stepRegisterGate := proxy.stepRegisterGate
	isReleasingStepRegisters := stepRegisterGate != nil && stream.slotKind == dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_RPC_ONLY
	if isReleasingStepRegisters {
		proxy.stepRegisterGate = nil
	}
	proxy.mu.Unlock()
	if isReleasingStepRegisters {
		close(stepRegisterGate)
		// The held Registers reach the server before the Worker sees this acknowledgement, so the cap rejects them.
		time.Sleep(200 * time.Millisecond)
	}
}

func (proxy *pullSlotProxy) resetStatusOr(stream *pullSlotProxyStream, err error) error {
	if stream.isReset.Load() {
		return status.Error(codes.Unavailable, pullSlotProxyResetMessage)
	}
	return err
}

func (proxy *pullSlotProxy) markEnded(stream *pullSlotProxyStream) {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	stream.hasEnded = true
	delete(proxy.streams, stream)
}

// holdStepRegistersUntilRPCOnlyAcknowledged forces the reconnect order of design Test 17.
func (proxy *pullSlotProxy) holdStepRegistersUntilRPCOnlyAcknowledged() {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	proxy.stepRegisterGate = make(chan struct{})
}

// holdAcknowledgements delays every acknowledgement until release is called.
func (proxy *pullSlotProxy) holdAcknowledgements() (release func()) {
	gate := make(chan struct{})
	proxy.mu.Lock()
	proxy.acknowledgementGate = gate
	proxy.mu.Unlock()
	return func() {
		proxy.mu.Lock()
		proxy.acknowledgementGate = nil
		proxy.mu.Unlock()
		close(gate)
	}
}

// resetRegisteredStream resets one acknowledged stream of the kind, like a network failure.
func (proxy *pullSlotProxy) resetRegisteredStream(appWorkerID string, slotKind dexpb.PollWorkerSlotKind) bool {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	for stream := range proxy.streams {
		if stream.appWorkerID == appWorkerID && stream.slotKind == slotKind && stream.isRegistered && !stream.hasEnded {
			stream.isReset.Store(true)
			stream.cancel()
			return true
		}
	}
	return false
}

// registeredStreams counts the acknowledged, live streams of a kind, as the server holds them.
func (proxy *pullSlotProxy) registeredStreams(appWorkerID string, slotKind dexpb.PollWorkerSlotKind) int {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	count := 0
	for stream := range proxy.streams {
		if stream.appWorkerID == appWorkerID && stream.slotKind == slotKind && stream.isRegistered && !stream.hasEnded {
			count++
		}
	}
	return count
}

// registersSince returns the Registers of appWorkerID that arrived at or after since.
func (proxy *pullSlotProxy) registersSince(appWorkerID string, since time.Time) []pullSlotProxyRegister {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	var registers []pullSlotProxyRegister
	for _, register := range proxy.registers {
		if register.appWorkerID == appWorkerID && !register.at.Before(since) {
			registers = append(registers, register)
		}
	}
	return registers
}

// acknowledgementsSince returns the acknowledgements of appWorkerID forwarded at or after since.
func (proxy *pullSlotProxy) acknowledgementsSince(appWorkerID string, since time.Time) []pullSlotProxyRegister {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	var acknowledgements []pullSlotProxyRegister
	for _, acknowledgement := range proxy.acknowledgements {
		if acknowledgement.appWorkerID == appWorkerID && !acknowledgement.at.Before(since) {
			acknowledgements = append(acknowledgements, acknowledgement)
		}
	}
	return acknowledgements
}

func (proxy *pullSlotProxy) firstAcknowledgementAt() time.Time {
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	return proxy.firstAckForwardedAt
}

// pumpDownstreamFrames forwards the client's frames and its half-close to the server.
func pumpDownstreamFrames(downstream grpc.ServerStream, upstream grpc.ClientStream, observe func(*rawProxyFrame)) {
	for {
		frame := &rawProxyFrame{}
		if err := downstream.RecvMsg(frame); err != nil {
			if errors.Is(err, io.EOF) {
				// CloseSend only fails after the stream already ended, which the other pump reports.
				if closeErr := upstream.CloseSend(); closeErr != nil {
					return
				}
			}
			return
		}
		if observe != nil {
			observe(frame)
		}
		if err := upstream.SendMsg(frame); err != nil {
			return
		}
	}
}

// pumpUpstreamFrames forwards the server's frames and returns the server's final status.
func pumpUpstreamFrames(upstream grpc.ClientStream, downstream grpc.ServerStream, observe func(*rawProxyFrame)) error {
	for {
		frame := &rawProxyFrame{}
		if err := upstream.RecvMsg(frame); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if observe != nil {
			observe(frame)
		}
		if err := downstream.SendMsg(frame); err != nil {
			return err
		}
	}
}

func (rawProxyCodec) Marshal(value any) ([]byte, error) {
	frame, ok := value.(*rawProxyFrame)
	if !ok {
		return nil, fmt.Errorf("proxy codec cannot marshal %T", value)
	}
	return frame.payload, nil
}

func (rawProxyCodec) Unmarshal(data []byte, value any) error {
	frame, ok := value.(*rawProxyFrame)
	if !ok {
		return fmt.Errorf("proxy codec cannot unmarshal into %T", value)
	}
	frame.payload = append([]byte(nil), data...)
	return nil
}

func (rawProxyCodec) Name() string { return "proto" }

// startPullSlotCompetitor runs loops raw streams; each one re-registers in a tight loop after a rejection or a close.
func startPullSlotCompetitor(t *testing.T, address string, appWorkerID string, loops int) *pullSlotCompetitor {
	t.Helper()
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	competitor := &pullSlotCompetitor{t: t, client: dexpb.NewWorkerTaskServiceClient(conn), conn: conn, cancel: cancel, held: make(map[*pullSlotCompetitorStream]struct{}), appID: appWorkerID}
	for range loops {
		competitor.wg.Add(1)
		go competitor.holdSlot(ctx)
	}
	t.Cleanup(competitor.stop)
	return competitor
}

func (competitor *pullSlotCompetitor) holdSlot(ctx context.Context) {
	defer competitor.wg.Done()
	for ctx.Err() == nil {
		streamCtx, cancel := context.WithCancel(ctx)
		competitor.registerAndHold(streamCtx, cancel)
		cancel()
		select {
		case <-ctx.Done():
		case <-time.After(time.Millisecond):
		}
	}
}

func (competitor *pullSlotCompetitor) registerAndHold(ctx context.Context, cancel context.CancelFunc) {
	stream, err := competitor.client.PollWorkerTask(ctx)
	if err != nil {
		return
	}
	register := &dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Register{Register: &dexpb.PollWorkerTaskRegister{
		SupportsReservedSlotDispatch: true, SlotKind: dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_STEP_AND_RPC,
		AppWorkerId: competitor.appID, FlowTypes: []string{"pullSlotCompetitorFlow"}, SdkName: "go", SdkVersion: currentGoSDKVersion(),
	}}}
	if err := stream.Send(register); err != nil {
		return
	}
	response, err := stream.Recv()
	if err != nil || response.GetRegistered() == nil {
		return
	}
	held := &pullSlotCompetitorStream{cancel: cancel}
	competitor.mu.Lock()
	competitor.held[held] = struct{}{}
	competitor.mu.Unlock()
	defer func() {
		competitor.mu.Lock()
		delete(competitor.held, held)
		competitor.mu.Unlock()
	}()
	for {
		response, err := stream.Recv()
		if err != nil {
			return
		}
		if response.GetPing() != nil {
			if err := stream.Send(&dexpb.PollWorkerTaskRequest{Frame: &dexpb.PollWorkerTaskRequest_Pong{Pong: &dexpb.PollWorkerTaskPong{}}}); err != nil {
				return
			}
		}
	}
}

func (competitor *pullSlotCompetitor) heldStreams() int {
	competitor.mu.Lock()
	defer competitor.mu.Unlock()
	return len(competitor.held)
}

// closeOneStream ends one held stream; its loop re-registers into the freed slot at once.
func (competitor *pullSlotCompetitor) closeOneStream() {
	competitor.mu.Lock()
	defer competitor.mu.Unlock()
	for held := range competitor.held {
		held.cancel()
		return
	}
}

func (competitor *pullSlotCompetitor) stop() {
	if competitor.stopped.Swap(true) {
		return
	}
	competitor.cancel()
	competitor.wg.Wait()
	require.NoError(competitor.t, competitor.conn.Close())
}
