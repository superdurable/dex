// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"bytes"
	"context"
	"log"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

type pullKeepaliveFlow struct {
	emptyFlowSchema
	stepEntered chan<- struct{}
	rpcEntered  chan<- struct{}
	stepRelease <-chan struct{}
	rpcRelease  <-chan struct{}
}

func (flow pullKeepaliveFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(pullKeepaliveStep{entered: flow.stepEntered, release: flow.stepRelease})}
}

func (flow pullKeepaliveFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.GetValue, nil)}
}

func (flow pullKeepaliveFlow) GetValue(ctx dex.Context, input dex.None) (*dex.RPCResult[string], error) {
	flow.rpcEntered <- struct{}{}
	select {
	case <-flow.rpcRelease:
		return &dex.RPCResult[string]{Output: "rpc-result"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type pullKeepaliveStep struct {
	dex.StepDefaultsNoWaitFor[string]
	entered chan<- struct{}
	release <-chan struct{}
}

func (step pullKeepaliveStep) Execute(ctx dex.Context, input string) (*dex.StepDecision, error) {
	step.entered <- struct{}{}
	select {
	case <-step.release:
		return dex.GracefulComplete(input), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestPullSDKKeepaliveThroughDataIdleProxy(t *testing.T) {
	if !isPullWorkerMode {
		t.Skip("pull Worker mode only")
	}
	proxyAddress, proxy := startKeepaliveDataIdleProxy(t, flowServiceAddress(), "pullKeepaliveFlow", 25*time.Second)
	stepEntered := make(chan struct{}, 1)
	rpcEntered := make(chan struct{}, 1)
	stepRelease := make(chan struct{})
	rpcRelease := make(chan struct{})
	flow := pullKeepaliveFlow{stepEntered: stepEntered, rpcEntered: rpcEntered, stepRelease: stepRelease, rpcRelease: rpcRelease}
	workerRegistry, err := dex.NewRegistry([]dex.Flow{flow})
	require.NoError(t, err)
	worker, err := dex.NewWorker(workerRegistry, newPullSDKCache(t), dex.WorkerOptions{
		FlowServiceAddress: proxyAddress,
		Pull:               &dex.PullWorkerOptions{AppWorkerID: "pull-keepalive-" + uuid.NewString(), MaxConcurrentTasks: 2},
	})
	require.NoError(t, err)
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Start() }()
	t.Cleanup(func() {
		stopContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, worker.Stop(stopContext))
		require.NoError(t, <-workerResult)
	})
	operationContext, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	require.NoError(t, worker.WaitPullRegistered(operationContext))
	clientRegistry, err := dex.NewRegistry([]dex.Flow{pullKeepaliveFlow{}})
	require.NoError(t, err)
	client, err := dex.NewClient(clientRegistry, newPullSDKCache(t), dex.ClientOptions{FlowServiceAddress: flowServiceAddress(), WorkerTarget: &dex.WorkerTarget{Pull: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	flowID := newFlowID(t, "pull-keepalive")
	_, err = client.StartFlow(operationContext, pullKeepaliveFlow{}, flowID, "step-result", dex.StartFlowOptions{})
	require.NoError(t, err)
	select {
	case <-stepEntered:
	case <-operationContext.Done():
		t.Fatal("Step did not start")
	}
	rpcResult := make(chan error, 1)
	var rpcOutput string
	go func() {
		rpcResult <- client.InvokeRPC(operationContext, flowID, pullKeepaliveFlow{}.GetValue, nil, &rpcOutput)
	}()
	select {
	case <-rpcEntered:
	case <-operationContext.Done():
		t.Fatal("RPC did not start")
	}
	select {
	case <-time.After(30 * time.Second):
	case <-operationContext.Done():
		t.Fatal("long tasks did not survive the DATA-idle window")
	}
	close(rpcRelease)
	require.NoError(t, <-rpcResult)
	require.Equal(t, "rpc-result", rpcOutput)
	close(stepRelease)
	result, err := client.WaitForFlow(operationContext, flowID, dex.WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	require.Equal(t, dex.FlowCompleted, result.Status)
	var stepOutput string
	require.NoError(t, result.DecodeSingleOutput(&stepOutput))
	require.Equal(t, "step-result", stepOutput)
	proxy.mu.Lock()
	resetCount, trackedCount := proxy.resetCount, proxy.trackedCount
	proxy.mu.Unlock()
	require.Zero(t, resetCount)
	require.GreaterOrEqual(t, trackedCount, 2)
}

type keepaliveDataIdleProxy struct {
	listener       net.Listener
	backendAddress string
	flowType       []byte
	idleTimeout    time.Duration
	mu             sync.Mutex
	connections    map[*keepaliveDataIdleConnection]struct{}
	resetCount     int
	trackedCount   int
}

type keepaliveDataIdleConnection struct {
	proxy     *keepaliveDataIdleProxy
	client    net.Conn
	backend   net.Conn
	mu        sync.Mutex
	timers    map[uint32]*time.Timer
	closeOnce sync.Once
}

type keepaliveHTTP2Observer struct {
	isClient   bool
	hasPreface bool
	buffer     []byte
}

func startKeepaliveDataIdleProxy(t *testing.T, backendAddress, flowType string, idleTimeout time.Duration) (string, *keepaliveDataIdleProxy) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	proxy := &keepaliveDataIdleProxy{listener: listener, backendAddress: backendAddress, flowType: []byte(flowType), idleTimeout: idleTimeout, connections: make(map[*keepaliveDataIdleConnection]struct{})}
	go proxy.serve()
	t.Cleanup(proxy.close)
	return listener.Addr().String(), proxy
}

func (proxy *keepaliveDataIdleProxy) serve() {
	for {
		client, err := proxy.listener.Accept()
		if err != nil {
			return
		}
		backend, err := net.Dial("tcp", proxy.backendAddress)
		if err != nil {
			if closeErr := client.Close(); closeErr != nil {
				log.Printf("close proxy client: %v", closeErr)
			}
			continue
		}
		connection := &keepaliveDataIdleConnection{proxy: proxy, client: client, backend: backend, timers: make(map[uint32]*time.Timer)}
		proxy.mu.Lock()
		proxy.connections[connection] = struct{}{}
		proxy.mu.Unlock()
		go connection.forward(client, backend, true)
		go connection.forward(backend, client, false)
	}
}

func (proxy *keepaliveDataIdleProxy) close() {
	if err := proxy.listener.Close(); err != nil {
		log.Printf("close proxy listener: %v", err)
	}
	proxy.mu.Lock()
	connections := make([]*keepaliveDataIdleConnection, 0, len(proxy.connections))
	for connection := range proxy.connections {
		connections = append(connections, connection)
	}
	proxy.mu.Unlock()
	for _, connection := range connections {
		connection.close()
	}
}

func (connection *keepaliveDataIdleConnection) forward(source, destination net.Conn, isClient bool) {
	defer connection.close()
	observer := &keepaliveHTTP2Observer{isClient: isClient}
	buffer := make([]byte, 32*1024)
	for {
		count, err := source.Read(buffer)
		if count > 0 {
			observer.observe(buffer[:count], func(frameType, flags byte, streamID uint32, payload []byte) {
				connection.observeFrame(!isClient, frameType, flags, streamID, payload)
			})
			remaining := buffer[:count]
			for len(remaining) > 0 {
				written, writeErr := destination.Write(remaining)
				if writeErr != nil {
					return
				}
				remaining = remaining[written:]
			}
		}
		if err != nil {
			return
		}
	}
}

func (observer *keepaliveHTTP2Observer) observe(data []byte, frame func(byte, byte, uint32, []byte)) {
	observer.buffer = append(observer.buffer, data...)
	if observer.isClient && !observer.hasPreface {
		if len(observer.buffer) < 24 {
			return
		}
		observer.buffer = observer.buffer[24:]
		observer.hasPreface = true
	}
	for len(observer.buffer) >= 9 {
		length := int(observer.buffer[0])<<16 | int(observer.buffer[1])<<8 | int(observer.buffer[2])
		if len(observer.buffer) < 9+length {
			return
		}
		streamID := uint32(observer.buffer[5]&0x7f)<<24 | uint32(observer.buffer[6])<<16 | uint32(observer.buffer[7])<<8 | uint32(observer.buffer[8])
		frame(observer.buffer[3], observer.buffer[4], streamID, observer.buffer[9:9+length])
		observer.buffer = observer.buffer[9+length:]
	}
}

func (connection *keepaliveDataIdleConnection) observeFrame(isServer bool, frameType, flags byte, streamID uint32, payload []byte) {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	if isServer && frameType == 0 && bytes.Contains(payload, connection.proxy.flowType) {
		if _, exists := connection.timers[streamID]; !exists {
			connection.proxy.mu.Lock()
			connection.proxy.trackedCount++
			connection.proxy.mu.Unlock()
		}
		connection.resetTimer(streamID)
	}
	timer, isTracked := connection.timers[streamID]
	if !isTracked {
		return
	}
	if frameType == 0 && len(payload) > 0 {
		connection.resetTimer(streamID)
	} else if isServer && frameType == 1 && flags&1 != 0 {
		timer.Stop()
		delete(connection.timers, streamID)
	}
}

func (connection *keepaliveDataIdleConnection) resetTimer(streamID uint32) {
	if timer, exists := connection.timers[streamID]; exists {
		timer.Reset(connection.proxy.idleTimeout)
		return
	}
	connection.timers[streamID] = time.AfterFunc(connection.proxy.idleTimeout, func() {
		connection.proxy.mu.Lock()
		connection.proxy.resetCount++
		connection.proxy.mu.Unlock()
		connection.close()
	})
}

func (connection *keepaliveDataIdleConnection) close() {
	connection.closeOnce.Do(func() {
		connection.mu.Lock()
		for _, timer := range connection.timers {
			timer.Stop()
		}
		connection.mu.Unlock()
		if err := connection.client.Close(); err != nil {
			log.Printf("close proxy client: %v", err)
		}
		if err := connection.backend.Close(); err != nil {
			log.Printf("close proxy backend: %v", err)
		}
		connection.proxy.mu.Lock()
		delete(connection.proxy.connections, connection)
		connection.proxy.mu.Unlock()
	})
}
