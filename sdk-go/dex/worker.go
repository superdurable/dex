// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/superdurable/dex/sdk-go/dex/blobcache"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultWorkerBindAddress = ":8803"
	defaultFlowServiceTarget = "localhost:8801"
)

// WorkerOptions configures the application-hosted WorkerService.
type WorkerOptions struct {
	// BindAddress is the plaintext WorkerService listener for Dex; it may differ from WorkerTarget. Default: ":8803".
	BindAddress string
	// WorkerTarget is advertised to Dex and may differ from BindAddress. Default Address derives from BindAddress; Headless defaults false.
	WorkerTarget WorkerTarget
	// FlowServiceAddress is the plaintext Dex endpoint used only for blob hydration. Default: "localhost:8801".
	FlowServiceAddress string
	// BlobCache optionally caches hydrated values. Default: nil.
	BlobCache *blobcache.Cache
}

// Worker hosts registered Flows over the private WorkerService protocol.
type Worker struct {
	registry     *registry
	bindAddress  string
	workerTarget WorkerTarget
	grpcServer   *grpc.Server
	flowConn     *grpc.ClientConn

	lifecycleMu sync.Mutex
	state       workerState
	done        chan struct{}
	finishOnce  sync.Once
}

type workerState uint8

const (
	workerCreated workerState = iota
	workerRunning
	workerStopping
	workerStopped
)

// NewWorker validates Flows and constructs a one-shot Worker.
func NewWorker(
	flows []Flow,
	options WorkerOptions,
) (*Worker, error) {
	registered, err := newRegistry(flows)
	if err != nil {
		return nil, err
	}
	bindAddress, target, flowServiceAddress, err := resolveWorkerOptions(options)
	if err != nil {
		return nil, err
	}
	flowConn, err := grpc.NewClient(
		flowServiceAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("dex: create FlowService client: %w", err)
	}

	grpcServer := grpc.NewServer()
	service := newWorkerService(
		registered,
		newWorkerValueHydrator(
			dexpb.NewFlowServiceClient(flowConn),
			options.BlobCache,
		),
	)
	dexpb.RegisterWorkerServiceServer(grpcServer, service)
	return &Worker{
		registry:     registered,
		bindAddress:  bindAddress,
		workerTarget: target,
		grpcServer:   grpcServer,
		flowConn:     flowConn,
		state:        workerCreated,
		done:         make(chan struct{}),
	}, nil
}

func resolveWorkerOptions(
	options WorkerOptions,
) (string, WorkerTarget, string, error) {
	bindAddress := strings.TrimSpace(options.BindAddress)
	if bindAddress == "" {
		bindAddress = defaultWorkerBindAddress
	}
	bindHost, bindPort, err := validateWorkerBindAddress(bindAddress)
	if err != nil {
		return "", WorkerTarget{}, "", err
	}

	target, err := resolveAdvertisedWorkerTarget(
		bindHost,
		bindPort,
		options.WorkerTarget,
	)
	if err != nil {
		return "", WorkerTarget{}, "", err
	}
	flowServiceAddress := strings.TrimSpace(options.FlowServiceAddress)
	if flowServiceAddress == "" {
		flowServiceAddress = defaultFlowServiceTarget
	}
	if err := validatePlaintextTarget(flowServiceAddress, false); err != nil {
		return "", WorkerTarget{}, "", fmt.Errorf(
			"dex: invalid FlowService address: %w",
			err,
		)
	}
	return bindAddress, target, flowServiceAddress, nil
}

func validateWorkerBindAddress(address string) (string, string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", "", fmt.Errorf("dex: invalid Worker bind address %q: %w", address, err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", "", fmt.Errorf("dex: invalid Worker bind address %q: port must be 1-65535", address)
	}
	return host, port, nil
}

func resolveAdvertisedWorkerTarget(
	bindHost string,
	bindPort string,
	target WorkerTarget,
) (WorkerTarget, error) {
	target.Address = strings.TrimSpace(target.Address)
	if target.Address == "" {
		targetHost := bindHost
		if targetHost == "" || targetHost == "0.0.0.0" || targetHost == "::" {
			targetHost = "localhost"
		}
		target.Address = net.JoinHostPort(targetHost, bindPort)
	}
	if err := validatePlaintextTarget(target.Address, target.Headless); err != nil {
		return WorkerTarget{}, fmt.Errorf("dex: invalid Worker target: %w", err)
	}
	return target, nil
}

func validatePlaintextTarget(address string, requireHostPort bool) error {
	lower := strings.ToLower(strings.TrimSpace(address))
	if lower == "" {
		return fmt.Errorf("target address must not be empty")
	}
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return fmt.Errorf("target %q must use plaintext gRPC", address)
	}
	if requireHostPort {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("headless target %q must use host:port: %w", address, err)
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return fmt.Errorf("headless target %q must use port 1-65535", address)
		}
	}
	return nil
}

// Start serves WorkerService and blocks until Stop or a serve failure.
func (worker *Worker) Start() error {
	worker.lifecycleMu.Lock()
	if worker.state != workerCreated {
		state := worker.state
		worker.lifecycleMu.Unlock()
		return fmt.Errorf("dex: Worker cannot start from state %s", state)
	}
	listener, err := net.Listen("tcp", worker.bindAddress)
	if err != nil {
		worker.state = workerStopped
		worker.lifecycleMu.Unlock()
		worker.finish()
		return fmt.Errorf("dex: listen on %q: %w", worker.bindAddress, err)
	}
	worker.state = workerRunning
	worker.lifecycleMu.Unlock()

	serveErr := worker.grpcServer.Serve(listener)
	if errors.Is(serveErr, grpc.ErrServerStopped) {
		serveErr = nil
	}
	worker.lifecycleMu.Lock()
	worker.state = workerStopped
	worker.lifecycleMu.Unlock()
	worker.finish()
	if serveErr != nil {
		return fmt.Errorf("dex: serve WorkerService: %w", serveErr)
	}
	return nil
}

// Stop drains handlers until ctx expires, then force-stops the Worker.
func (worker *Worker) Stop(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("dex: Worker Stop context must not be nil")
	}

	worker.lifecycleMu.Lock()
	switch worker.state {
	case workerCreated:
		worker.state = workerStopped
		worker.lifecycleMu.Unlock()
		worker.grpcServer.Stop()
		worker.finish()
		return nil
	case workerRunning:
		worker.state = workerStopping
		go worker.grpcServer.GracefulStop()
	case workerStopping:
	case workerStopped:
		worker.lifecycleMu.Unlock()
		return nil
	}
	done := worker.done
	worker.lifecycleMu.Unlock()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		worker.grpcServer.Stop()
		<-done
		return ctx.Err()
	}
}

func (worker *Worker) finish() {
	worker.finishOnce.Do(func() {
		if err := worker.flowConn.Close(); err != nil {
			slog.Default().Error("close Worker FlowService connection", "error", err)
		}
		close(worker.done)
	})
}

// WorkerTarget returns a copy suitable for FlowConfig.WorkerTarget.
func (worker *Worker) WorkerTarget() *WorkerTarget {
	target := worker.workerTarget
	return &target
}

func (state workerState) String() string {
	switch state {
	case workerCreated:
		return "created"
	case workerRunning:
		return "running"
	case workerStopping:
		return "stopping"
	case workerStopped:
		return "stopped"
	default:
		return "unknown"
	}
}
