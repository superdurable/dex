// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/emptypb"
)

// fakeFlowStopper scripts StopFlow results and reports a terminal Flow after closeAfterWaits WaitForFlow calls.
type fakeFlowStopper struct {
	mu              sync.Mutex
	stopErrors      []error
	stopCalls       int
	waitCalls       int
	closeAfterWaits int
}

func (stopper *fakeFlowStopper) StopFlow(context.Context, string, dex.StopOptions) error {
	stopper.mu.Lock()
	defer stopper.mu.Unlock()
	stopper.stopCalls++
	if len(stopper.stopErrors) == 0 {
		return nil
	}
	err := stopper.stopErrors[0]
	if len(stopper.stopErrors) > 1 {
		stopper.stopErrors = stopper.stopErrors[1:]
	}
	return err
}

func (stopper *fakeFlowStopper) WaitForFlow(ctx context.Context, _ string, _ dex.WaitForFlowOptions) (dex.FlowResult, error) {
	stopper.mu.Lock()
	stopper.waitCalls++
	isClosed := stopper.closeAfterWaits >= 0 && stopper.waitCalls > stopper.closeAfterWaits
	stopper.mu.Unlock()
	if isClosed {
		return dex.FlowResult{Status: dex.FlowTerminated}, nil
	}
	// An open Flow ends the long poll without a result.
	select {
	case <-ctx.Done():
		return dex.FlowResult{}, ctx.Err()
	case <-time.After(20 * time.Millisecond):
		return dex.FlowResult{}, errors.New("long poll ended")
	}
}

func serviceError(code codes.Code) *dex.ServiceError {
	return &dex.ServiceError{Op: "StopFlow", FlowID: "flow", Code: code}
}

func TestStopAndConfirmFlowFailsWhenAcceptedStopNeverCloses(t *testing.T) {
	stopper := &fakeFlowStopper{closeAfterWaits: -1}
	err := stopAndConfirmFlow(stopper, "flow", time.Second, 200*time.Millisecond)
	if err == nil {
		t.Fatal("cleanup succeeded although the Flow never closed")
	}
	if stopper.waitCalls == 0 {
		t.Fatal("cleanup did not wait for the Flow to close")
	}
}

func TestStopAndConfirmFlowWaitsForAcceptedStop(t *testing.T) {
	stopper := &fakeFlowStopper{closeAfterWaits: 2}
	if err := stopAndConfirmFlow(stopper, "flow", time.Second, 5*time.Second); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if stopper.waitCalls != 3 {
		t.Fatalf("WaitForFlow calls = %d, want 3", stopper.waitCalls)
	}
}

func TestStopAndConfirmFlowWaitsForClosingFlow(t *testing.T) {
	stopper := &fakeFlowStopper{
		stopErrors:      []error{&dex.FlowClosingError{ServiceError: serviceError(codes.FailedPrecondition)}},
		closeAfterWaits: 1,
	}
	if err := stopAndConfirmFlow(stopper, "flow", time.Second, 5*time.Second); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if stopper.waitCalls != 2 {
		t.Fatalf("WaitForFlow calls = %d, want 2", stopper.waitCalls)
	}
}

func TestStopAndConfirmFlowAcceptsClosedOrMissingFlow(t *testing.T) {
	stopper := &fakeFlowStopper{
		stopErrors:      []error{&dex.FlowNotActiveOrNotFoundError{ServiceError: serviceError(codes.NotFound)}},
		closeAfterWaits: -1,
	}
	if err := stopAndConfirmFlow(stopper, "flow", time.Second, time.Second); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if stopper.waitCalls != 0 {
		t.Fatalf("WaitForFlow calls = %d, want 0", stopper.waitCalls)
	}
}

func TestStopAndConfirmFlowFailsAfterStopErrors(t *testing.T) {
	stopper := &fakeFlowStopper{stopErrors: []error{serviceError(codes.Unavailable)}, closeAfterWaits: -1}
	if err := stopAndConfirmFlow(stopper, "flow", time.Second, time.Second); err == nil {
		t.Fatal("cleanup succeeded although every StopFlow failed")
	}
	if stopper.stopCalls != persistentFlowStopAttempts {
		t.Fatalf("StopFlow calls = %d, want %d", stopper.stopCalls, persistentFlowStopAttempts)
	}
}

func TestWaitForWakeNoticeLeavesTimeBeforeDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	if err := waitForWakeNotice(ctx, make(chan struct{}), wakeNoticeWait); err != nil {
		t.Fatalf("wait failed: %v", err)
	}
	if waited := time.Since(started); waited > 400*time.Millisecond {
		t.Fatalf("waited %v for a missing notice with a 1 s deadline", waited)
	}
}

// fakeWakeService fails GetFlowState failures times, then reports a waiting Step and wakes it on publish.
type fakeWakeService struct {
	dexpb.FlowServiceClient
	mu       sync.Mutex
	failures int
	signals  chan wakeSignal
}

func (service *fakeWakeService) GetFlowState(context.Context, *dexpb.GetFlowStateRequest, ...grpc.CallOption) (*dexpb.GetFlowStateResponse, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.failures > 0 {
		service.failures--
		return nil, errors.New("transient GetFlowState failure")
	}
	return &dexpb.GetFlowStateResponse{ActiveStepExecutions: []*dexpb.ActiveStepExecutionState{
		{Phase: dexpb.ActiveStepPhase_ACTIVE_STEP_PHASE_WAITING},
	}}, nil
}

func (service *fakeWakeService) PublishToChannel(_ context.Context, request *dexpb.PublishToChannelRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	sequence := request.GetMessages()[0].GetValue().GetIntValue()
	service.signals <- wakeSignal{sequence: sequence, startedAt: time.Now()}
	return &emptypb.Empty{}, nil
}

func TestChannelWakeRecoversAfterConsumedNoticeAndFailedStateRead(t *testing.T) {
	signals := newWakeSignals(1)
	runner := &Runner{
		wake:               &wakeFlow{signals: signals},
		wakeSequences:      []int64{1},
		wakeNeedsStatePoll: []bool{false},
	}
	service := &fakeWakeService{failures: 1, signals: signals.channels[0]}
	connection := &benchmarkConnection{service: service}
	signals.waitedChannels[0] <- struct{}{}

	attempt := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := runner.wakeWaitingFlow(ctx, connection, 0, "flow")
		return err
	}
	if err := attempt(); err == nil {
		t.Fatal("first wake succeeded although GetFlowState failed")
	}
	// The first attempt consumed the only WaitFor notice, so later attempts must recover without one.
	for index := range 3 {
		if err := attempt(); err != nil {
			t.Fatalf("wake %d after the failure: %v", index+2, err)
		}
	}
}
