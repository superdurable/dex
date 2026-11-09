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

// fakeWakeService models one long-lived wake Flow. GetFlowState fails stateFailures times, then reports a
// waiting Step. A publish wakes the Step, which signals Execute and re-enters WaitFor with a new notice.
// The first publishFailures publishes are delivered but still return an error, like a lost response.
type fakeWakeService struct {
	dexpb.FlowServiceClient
	mu              sync.Mutex
	stateFailures   int
	publishFailures int
	stateReads      int
	signals         *wakeSignals
}

func (service *fakeWakeService) GetFlowState(context.Context, *dexpb.GetFlowStateRequest, ...grpc.CallOption) (*dexpb.GetFlowStateResponse, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.stateReads++
	if service.stateFailures > 0 {
		service.stateFailures--
		return nil, errors.New("transient GetFlowState failure")
	}
	return &dexpb.GetFlowStateResponse{ActiveStepExecutions: []*dexpb.ActiveStepExecutionState{
		{Phase: dexpb.ActiveStepPhase_ACTIVE_STEP_PHASE_WAITING},
	}}, nil
}

func (service *fakeWakeService) PublishToChannel(_ context.Context, request *dexpb.PublishToChannelRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	sequence := request.GetMessages()[0].GetValue().GetIntValue()
	service.signals.notify(0, sequence, time.Now())
	service.signals.notifyWaited(0)
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.publishFailures > 0 {
		service.publishFailures--
		return nil, errors.New("publish response lost")
	}
	return &emptypb.Empty{}, nil
}

func (service *fakeWakeService) stateReadCount() int {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.stateReads
}

func newWakeTestRunner() (*Runner, *wakeSignals) {
	signals := newWakeSignals(1)
	runner := &Runner{
		wake:               &wakeFlow{signals: signals},
		wakeSequences:      []int64{1},
		wakeNeedsStatePoll: []bool{false},
	}
	return runner, signals
}

// timedWake runs one wake with a 1-second operation timeout and returns its error and elapsed time.
func timedWake(runner *Runner, connection *benchmarkConnection) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	_, err := runner.wakeWaitingFlow(ctx, connection, 0, "flow")
	return time.Since(started), err
}

func TestChannelWakeRecoversAfterConsumedNoticeAndFailedStateRead(t *testing.T) {
	runner, signals := newWakeTestRunner()
	service := &fakeWakeService{stateFailures: 1, signals: signals}
	connection := &benchmarkConnection{service: service}
	signals.notifyWaited(0)

	if _, err := timedWake(runner, connection); err == nil {
		t.Fatal("first wake succeeded although GetFlowState failed")
	}
	// The failed attempt consumed the only notice, so the next attempt must poll at once.
	elapsed, err := timedWake(runner, connection)
	if err != nil {
		t.Fatalf("wake after the failure: %v", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("wake after the failure took %v; it waited for a consumed notice", elapsed)
	}
	for index := range 3 {
		if _, err := timedWake(runner, connection); err != nil {
			t.Fatalf("wake %d: %v", index+3, err)
		}
	}
}

func TestChannelWakeLeavesNoStaleNoticeAfterLostPublishResponse(t *testing.T) {
	runner, signals := newWakeTestRunner()
	service := &fakeWakeService{publishFailures: 1, signals: signals}
	connection := &benchmarkConnection{service: service}
	signals.notifyWaited(0)

	if _, err := timedWake(runner, connection); err == nil {
		t.Fatal("first wake succeeded although its publish response was lost")
	}
	for index := range 5 {
		readsBefore := service.stateReadCount()
		if _, err := timedWake(runner, connection); err != nil {
			t.Fatalf("wake %d: %v", index+2, err)
		}
		// Only the notice of the next wait may remain queued.
		if queued := len(signals.waitedChannels[0]); queued != 1 {
			t.Fatalf("after wake %d, %d WaitFor notices are queued, want 1", index+2, queued)
		}
		if reads := service.stateReadCount() - readsBefore; reads != 1 {
			t.Fatalf("wake %d read Flow state %d times, want 1", index+2, reads)
		}
	}
}

func TestDrainWakeNoticesEmptiesQueueWithoutBlocking(t *testing.T) {
	notices := make(chan struct{}, 4)
	notices <- struct{}{}
	notices <- struct{}{}
	drainWakeNotices(notices)
	drainWakeNotices(notices)
	if len(notices) != 0 {
		t.Fatalf("%d notices remain after draining", len(notices))
	}
}

// missingFlowWaiter reports a missing Flow from WaitForFlow after an accepted stop.
type missingFlowWaiter struct {
	fakeFlowStopper
	waitErr error
}

func (waiter *missingFlowWaiter) WaitForFlow(context.Context, string, dex.WaitForFlowOptions) (dex.FlowResult, error) {
	return dex.FlowResult{}, waiter.waitErr
}

func TestWaitForFlowClosedAcceptsMissingFlow(t *testing.T) {
	for name, waitErr := range map[string]error{
		"not found":             &dex.FlowNotFoundError{ServiceError: serviceError(codes.NotFound)},
		"not active or missing": &dex.FlowNotActiveOrNotFoundError{ServiceError: serviceError(codes.NotFound)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := waitForFlowClosed(&missingFlowWaiter{waitErr: waitErr}, "flow", time.Second); err != nil {
				t.Fatalf("waitForFlowClosed failed: %v", err)
			}
		})
	}
}
