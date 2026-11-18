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
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	stepAndRPCSlotKind = dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_STEP_AND_RPC
	rpcOnlySlotKind    = dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_RPC_ONLY
)

// adaptiveSlotFlow runs one Step whose input chooses how long it holds its slot.
type adaptiveSlotFlow struct {
	FlowDefaults
	handlers *adaptiveSlotHandlers
}

// adaptiveSlotParkedFlow stays running without a slot, so RPCs can target it.
type adaptiveSlotParkedFlow struct {
	FlowDefaults
	handlers *adaptiveSlotHandlers
}

type adaptiveSlotStep struct {
	StepDefaultsNoWaitFor[adaptiveSlotInput]
	handlers *adaptiveSlotHandlers
}

type adaptiveSlotParkedStep struct {
	StepDefaults
}

type adaptiveSlotInput struct {
	Mode string `json:"mode"`
}

// adaptiveSlotHandlers records handler entries and releases blocked handlers.
type adaptiveSlotHandlers struct {
	entered chan adaptiveSlotEntry
	release chan struct{}
	mu      sync.Mutex
	// stubbornReleases releases handlers that ignore cancellation, keyed by Flow ID or "rpc".
	stubbornReleases map[string]chan struct{}
	releasedStubborn map[string]bool
	isRPCStubborn    bool
}

type adaptiveSlotEntry struct {
	flowID  string
	kind    string
	attempt int32
}

const (
	adaptiveSlotModeBlock    = "block"
	adaptiveSlotModeStubborn = "stubborn"
	adaptiveSlotModeQuick    = "quick"
	stubbornRPCKey           = "rpc"
)

// registerSendBarrier holds armed slot streams after their admission and before their Register's Send.
type registerSendBarrier struct {
	isArmed     atomic.Bool
	held        atomic.Int32
	passes      chan struct{}
	released    chan struct{}
	releaseOnce sync.Once
}

// blockingRegisterPollClient makes the next Step-and-RPC Register Sends block until their stream ends or release closes.
type blockingRegisterPollClient struct {
	dexpb.WorkerTaskServiceClient
	remainingBlocks atomic.Int32
	blockedSends    atomic.Int32
	release         chan struct{}
	releaseOnce     sync.Once
}

type blockingRegisterPollStream struct {
	dexpb.WorkerTaskService_PollWorkerTaskClient
	client *blockingRegisterPollClient
	ctx    context.Context
}

// adaptiveSlotWorkerRun owns a started Worker and its Start result.
type adaptiveSlotWorkerRun struct {
	worker    *Worker
	started   chan error
	result    error
	hasResult bool
}

// adaptiveSlotLogger records WARN and INFO messages with their values.
type adaptiveSlotLogger struct {
	Logger
	mu       sync.Mutex
	warnings []string
	infos    []string
}

// TestWorkerPullAdaptiveGrowShrinkWithRealServer covers design Tests 2, 3, 4, 5, 9, and 11.
func TestWorkerPullAdaptiveGrowShrinkWithRealServer(t *testing.T) {
	server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{})
	proxy := startPullSlotProxy(t, server.address)
	handlers := newAdaptiveSlotHandlers()
	appWorkerID := "adaptive-grow-" + uuid.NewString()[:8]
	run := startAdaptiveSlotWorker(t, proxy.address(), handlers, PullWorkerOptions{MaxConcurrentTasks: 16, MinOpenSlots: 3, AppWorkerID: appWorkerID}, time.Second, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, run.worker.WaitPullRegistered(ctx))

	// Test 2: an idle adaptive Worker holds exactly L + R streams.
	requireRegisteredStreams(t, proxy, appWorkerID, 2, 1)
	state := run.worker.PullSlotStreamState()
	require.True(t, state.IsAdaptive)
	require.Equal(t, 2, state.Open)
	require.Equal(t, 1, state.RPCOnlyOpen)
	require.Equal(t, 2, state.Floor)
	require.Equal(t, 15, state.EffectiveCeiling)
	require.Equal(t, 16, state.TotalCapacity)
	require.Equal(t, 1, state.RPCOnlySlots)

	// Test 3: a burst of N long Steps with L < N <= E grows to at least N and at most min(E, 2N).
	client := server.newClient(t, run.worker.registry)
	flow := &adaptiveSlotFlow{handlers: handlers}
	flowIDs := startAdaptiveSlotFlows(t, ctx, client, flow, adaptiveSlotModeBlock, 5)
	entries := awaitAdaptiveSlotEntries(t, handlers, 5)
	requireSlotState(t, run.worker, "five busy Steps", func(state PullSlotStreamState) bool {
		return state.Busy == 5 && state.Registering == 0
	})
	state = run.worker.PullSlotStreamState()
	require.GreaterOrEqual(t, state.Open, 5)
	require.LessOrEqual(t, state.Open, 10)

	// Test 9: growth up to E keeps the RPC-only stream at R, and an RPC runs while every Step slot is busy.
	flowIDs = append(flowIDs, startAdaptiveSlotFlows(t, ctx, client, flow, adaptiveSlotModeBlock, 10)...)
	entries = append(entries, awaitAdaptiveSlotEntries(t, handlers, 10)...)
	requireSlotState(t, run.worker, "fifteen busy Steps", func(state PullSlotStreamState) bool {
		return state.Busy == 15 && state.Open == 15 && state.Registering == 0
	})
	requireRegisteredStreams(t, proxy, appWorkerID, 15, 1)
	require.Equal(t, 1, run.worker.PullSlotStreamState().RPCOnlyOpen)
	rpcCtx, cancelRPC := context.WithTimeout(ctx, 5*time.Second)
	var rpcStatus string
	require.NoError(t, client.InvokeRPC(rpcCtx, flowIDs[0], flow.GetSlotStatus, nil, &rpcStatus))
	cancelRPC()
	require.Equal(t, "ok", rpcStatus)
	close(handlers.release)
	requireAdaptiveSlotFlowsCompleted(t, ctx, client, flowIDs)

	// Tests 4 and 5: streams return to the floor while short Steps keep arriving, and no Step spends a retry.
	trickleIDs := make(chan []string, 1)
	trickleCtx, stopTrickle := context.WithCancel(ctx)
	go func() { trickleIDs <- startAdaptiveSlotTrickle(t, trickleCtx, client, flow) }()
	shrinkDeadline := time.Duration(adaptiveSlotWindowsToFloor(15, 2)+5) * time.Second
	require.Eventually(t, func() bool { return run.worker.PullSlotStreamState().Open == 2 }, shrinkDeadline, 10*time.Millisecond,
		"streams did not return to the floor: %+v", run.worker.PullSlotStreamState())
	stopTrickle()
	shortFlowIDs := <-trickleIDs
	require.NotEmpty(t, shortFlowIDs)
	requireAdaptiveSlotFlowsCompleted(t, ctx, client, shortFlowIDs)
	entries = append(entries, drainAdaptiveSlotEntries(handlers)...)
	for _, entry := range entries {
		require.Equal(t, int32(1), entry.attempt, "a Step spent a retry: %+v", entry)
	}
	state = run.worker.PullSlotStreamState()
	require.GreaterOrEqual(t, state.StreamsGrown, uint64(13))
	require.GreaterOrEqual(t, state.StreamsShrunk, uint64(13))
	t.Logf("shrink race drops: %d", state.ShrinkRaceDrops)

	// Test 11 and invariant 5: the count stays at the floor, and no stream closed on purpose
	// reopens, for longer than two maximum reconnect backoffs (2 x 7.5 s).
	requireRegisteredStreams(t, proxy, appWorkerID, 2, 1)
	since := time.Now()
	require.Never(t, func() bool {
		return run.worker.PullSlotStreamState().Open != 2 || len(proxy.registersSince(appWorkerID, since)) != 0
	}, 16*time.Second, 50*time.Millisecond, "streams rose above the floor at rest, or a stream closed on purpose registered again")
}

// TestWorkerPullAdaptiveRPCBurstWithRealServer covers the RPC variant of design Test 3, where S + R serve the burst.
func TestWorkerPullAdaptiveRPCBurstWithRealServer(t *testing.T) {
	server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{})
	handlers := newAdaptiveSlotHandlers()
	run := startAdaptiveSlotWorker(t, server.address, handlers, PullWorkerOptions{MaxConcurrentTasks: 8, MinOpenSlots: 2, AppWorkerID: "adaptive-rpc-" + uuid.NewString()[:8]}, time.Hour, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, run.worker.WaitPullRegistered(ctx))
	client := server.newClient(t, run.worker.registry)
	parked := &adaptiveSlotParkedFlow{handlers: handlers}
	parkedID := "adaptive-rpc-parked-" + uuid.NewString()
	_, err := client.StartFlow(ctx, parked, parkedID, nil, StartFlowOptions{})
	require.NoError(t, err)
	const rpcCount = 6
	rpcErrors := make(chan error, rpcCount)
	for range rpcCount {
		go func() {
			var output string
			rpcErrors <- client.InvokeRPC(ctx, parkedID, parked.BlockUntilReleased, nil, &output)
		}()
	}
	awaitAdaptiveSlotEntries(t, handlers, rpcCount)
	// E = 7 and R = 1: S + R >= min(N, E + R) and S <= min(E, 2N).
	state := run.worker.PullSlotStreamState()
	require.GreaterOrEqual(t, state.Open+state.RPCOnlyOpen, rpcCount)
	require.LessOrEqual(t, state.Open, 7)
	require.Equal(t, 1, state.RPCOnlyOpen)
	close(handlers.release)
	for range rpcCount {
		require.NoError(t, <-rpcErrors)
	}
}

// TestWorkerPullAdaptiveSoftRejectionAtNodeLimitWithRealServer covers design Tests 6 and 18.
func TestWorkerPullAdaptiveSoftRejectionAtNodeLimitWithRealServer(t *testing.T) {
	server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 20, maxStreams: 20})
	proxy := startPullSlotProxy(t, server.address)
	fixedHandlers := newAdaptiveSlotHandlers()
	fixed := startAdaptiveSlotParkedOnlyWorker(t, server.address, fixedHandlers, PullWorkerOptions{MaxConcurrentTasks: 14, AppWorkerID: "fixed-filler-" + uuid.NewString()[:8]})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, fixed.worker.WaitPullRegistered(ctx))
	handlers := newAdaptiveSlotHandlers()
	appWorkerID := "adaptive-limit-" + uuid.NewString()[:8]
	const window = 2 * time.Second
	run := startAdaptiveSlotWorker(t, proxy.address(), handlers, PullWorkerOptions{MaxConcurrentTasks: 12, MinOpenSlots: 3, AppWorkerID: appWorkerID}, window, nil)
	require.NoError(t, run.worker.WaitPullRegistered(ctx))
	requireRegisteredStreams(t, proxy, appWorkerID, 2, 1)
	client := server.newClient(t, run.worker.registry)
	flow := &adaptiveSlotFlow{handlers: handlers}
	flowIDs := startAdaptiveSlotFlows(t, ctx, client, flow, adaptiveSlotModeBlock, 10)

	// 14 + 3 streams leave room for 3: growth reaches S = 5 and a partly rejected batch clamps G to S + H.
	requireSlotState(t, run.worker, "growth clamped at the node limit", func(state PullSlotStreamState) bool {
		return state.Open == 5 && state.Busy == 5 && state.Registering == 0 && state.GrowthTarget == 5 && state.GrowRejectedAtCap > 0
	})
	state := run.worker.PullSlotStreamState()
	require.Equal(t, 11, state.EffectiveCeiling)
	require.Equal(t, state.Open+state.OrphanedHandlers, state.GrowthTarget)
	requireNoPermanentRegistrationError(t, run)

	// At most one growth batch per window while rejections continue: a batch is min(E - S, S) = 5.
	since := time.Now()
	require.Eventually(t, func() bool {
		return countRegisterBatches(proxy.registersSince(appWorkerID, since), window) >= 3
	}, 6*window, 20*time.Millisecond, "the Worker did not retry growth once per window")
	requireRegisterBatchesPerWindow(t, proxy.registersSince(appWorkerID, since), window, 5)
	requireNoPermanentRegistrationError(t, run)

	// Room returns: growth resumes after the next window, and G returns to E.
	require.NoError(t, fixed.worker.Stop(ctx))
	requireSlotState(t, run.worker, "growth after the node limit cleared", func(state PullSlotStreamState) bool {
		return state.Busy == 10 && state.Registering == 0 && state.GrowthTarget == state.EffectiveCeiling
	})
	close(handlers.release)
	requireAdaptiveSlotFlowsCompleted(t, ctx, client, flowIDs)
	for _, entry := range drainAdaptiveSlotEntries(handlers) {
		require.Equal(t, int32(1), entry.attempt)
	}
}

// TestWorkerPullAdaptiveFloorRetryLatchWithRealServer covers design Test 19.
func TestWorkerPullAdaptiveFloorRetryLatchWithRealServer(t *testing.T) {
	server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 20, maxStreams: 20})
	proxy := startPullSlotProxy(t, server.address)
	handlers := newAdaptiveSlotHandlers()
	appWorkerID := "adaptive-latch-" + uuid.NewString()[:8]
	const window = time.Second
	run := startAdaptiveSlotWorker(t, proxy.address(), handlers, PullWorkerOptions{MaxConcurrentTasks: 12, ReservedRPCTasks: ptr.Any(1), MinOpenSlots: 3, AppWorkerID: appWorkerID}, window, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, run.worker.WaitPullRegistered(ctx))
	requireRegisteredStreams(t, proxy, appWorkerID, 2, 1)
	competitor := startPullSlotCompetitor(t, server.address, "competitor-"+uuid.NewString()[:8], 18)
	require.Eventually(t, func() bool { return competitor.heldStreams() == 17 }, 20*time.Second, 10*time.Millisecond)

	// Resetting a floor stream frees a node slot; the competitor takes it, so the reconnect is rejected softly.
	since := time.Now()
	require.True(t, proxy.resetRegisteredStream(appWorkerID, stepAndRPCSlotKind))
	requireSlotStateWithin(t, run.worker, "the reset stream ended", 5*time.Second, func(state PullSlotStreamState) bool {
		return state.Open-state.Registering == 1
	})
	// One reconnect after the reset, then one retry per window: wait for three window retries.
	violation := ""
	require.Eventually(t, func() bool {
		state := run.worker.PullSlotStreamState()
		if state.Open-state.Registering > 1 || !run.worker.PullRegistrationState().IsReady {
			violation = fmt.Sprintf("a stream registered at the node limit, or readiness dropped: %+v", state)
			return true
		}
		return len(stepRegisters(proxy.registersSince(appWorkerID, since))) >= 4
	}, 6*window, 10*time.Millisecond, "the Worker did not retry at each window")
	require.Empty(t, violation)
	registers := stepRegisters(proxy.registersSince(appWorkerID, since))
	for index := 1; index+1 < len(registers); index++ {
		require.GreaterOrEqual(t, registers[index+1].at.Sub(registers[index].at), window*8/10, "a Register followed a rejected stream's end at once")
	}
	require.Equal(t, 1, proxy.registeredStreams(appWorkerID, stepAndRPCSlotKind))
	require.GreaterOrEqual(t, run.worker.PullSlotStreamState().GrowRejectedAtCap, uint64(2))
	requireNoPermanentRegistrationError(t, run)

	// A competitor stream that ends does not clear this Worker's latch.
	beforeClose := stepRegisters(proxy.registersSince(appWorkerID, since))
	lastRegisterBeforeClose := beforeClose[len(beforeClose)-1]
	competitorClosedAt := time.Now()
	competitor.closeOneStream()
	require.Eventually(t, func() bool {
		return len(stepRegisters(proxy.registersSince(appWorkerID, competitorClosedAt))) >= 1
	}, 3*window, 10*time.Millisecond, "the Worker stopped retrying at the window")
	nextRegister := stepRegisters(proxy.registersSince(appWorkerID, competitorClosedAt))[0]
	require.GreaterOrEqual(t, nextRegister.at.Sub(lastRegisterBeforeClose.at), window*8/10, "the competitor's close triggered a Register")

	// When the competitor leaves, the floor comes back within one window.
	competitor.stop()
	require.Eventually(t, func() bool {
		state := run.worker.PullSlotStreamState()
		return proxy.registeredStreams(appWorkerID, stepAndRPCSlotKind) == 2 && state.Open == 2 && state.Registering == 0
	}, 2*window+3*time.Second, 10*time.Millisecond)
	require.True(t, run.worker.PullRegistrationState().IsReady)
}

// TestWorkerPullAdaptiveCapOfOneWithRealServer covers design Test 14.
func TestWorkerPullAdaptiveCapOfOneWithRealServer(t *testing.T) {
	t.Run("fixed with an unset ceiling probes and clamps", func(t *testing.T) {
		server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 1})
		proxy := startPullSlotProxy(t, server.address)
		releaseAcknowledgements := proxy.holdAcknowledgements()
		logger := newAdaptiveSlotLogger()
		appWorkerID := "fixed-probe-" + uuid.NewString()[:8]
		run := startAdaptiveSlotWorker(t, proxy.address(), newAdaptiveSlotHandlers(), PullWorkerOptions{AppWorkerID: appWorkerID}, time.Hour, logger)
		// F4: only the probe stream registers before the first acknowledgement.
		require.Eventually(t, func() bool { return len(proxy.registersSince(appWorkerID, time.Time{})) == 1 }, 10*time.Second, 10*time.Millisecond)
		require.Never(t, func() bool { return len(proxy.registersSince(appWorkerID, time.Time{})) > 1 }, 300*time.Millisecond, 10*time.Millisecond,
			"a stream registered before the probe's acknowledgement")
		releaseAcknowledgements()
		requireCapOfOneWorkerServes(t, server, proxy, run, appWorkerID)
		if runtime.NumCPU() > 1 {
			require.True(t, logger.hasWarning("clamped MaxConcurrentTasks", fmt.Sprint(runtime.NumCPU()), "1"))
		}
		require.False(t, run.worker.PullSlotStreamState().IsAdaptive)
	})
	for _, maxConcurrentTasks := range []int{4, 0} {
		t.Run(fmt.Sprintf("adaptive floor of one with ceiling %d", maxConcurrentTasks), func(t *testing.T) {
			if maxConcurrentTasks == 0 && runtime.NumCPU() < 2 {
				maxConcurrentTasks = 2
			}
			server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 1})
			proxy := startPullSlotProxy(t, server.address)
			logger := newAdaptiveSlotLogger()
			appWorkerID := "adaptive-one-" + uuid.NewString()[:8]
			run := startAdaptiveSlotWorker(t, proxy.address(), newAdaptiveSlotHandlers(), PullWorkerOptions{MaxConcurrentTasks: maxConcurrentTasks, MinOpenSlots: 1, AppWorkerID: appWorkerID}, time.Hour, logger)
			requireCapOfOneWorkerServes(t, server, proxy, run, appWorkerID)
			require.True(t, run.worker.PullSlotStreamState().IsAdaptive)
			require.True(t, logger.hasWarning("clamped MaxConcurrentTasks", "1"))
			for _, register := range proxy.registersSince(appWorkerID, time.Time{}) {
				require.Equal(t, stepAndRPCSlotKind, register.slotKind)
			}
		})
	}
	t.Run("explicit reserved RPC slot fails start", func(t *testing.T) {
		optionsByMode := map[string]PullWorkerOptions{
			"fixed":    {ReservedRPCTasks: ptr.Any(1)},
			"adaptive": {MaxConcurrentTasks: 4, MinOpenSlots: 2, ReservedRPCTasks: ptr.Any(1)},
		}
		if runtime.NumCPU() < 2 {
			optionsByMode["fixed"] = PullWorkerOptions{MaxConcurrentTasks: 2, MinOpenSlots: 2, ReservedRPCTasks: ptr.Any(1)}
		}
		for mode, options := range optionsByMode {
			t.Run(mode, func(t *testing.T) {
				server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 1})
				options.AppWorkerID = "reserved-one-" + uuid.NewString()[:8]
				run := startAdaptiveSlotWorker(t, server.address, newAdaptiveSlotHandlers(), options, time.Hour, nil)
				err := run.awaitStartResult(t, 30*time.Second)
				if mode == "fixed" && options.MaxConcurrentTasks == 2 {
					require.ErrorContains(t, err, "MaxConcurrentTasks 2 exceeds Server matching.maxConcurrentTasksCap 1")
					return
				}
				require.ErrorContains(t, err, "ReservedRPCTasks 1 must be less than the slot ceiling 1")
				require.ErrorContains(t, err, "matching.maxConcurrentTasksCap 1")
			})
		}
	})
	t.Run("fixed with an explicit ceiling above the cap fails start", func(t *testing.T) {
		server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 1})
		run := startAdaptiveSlotWorker(t, server.address, newAdaptiveSlotHandlers(), PullWorkerOptions{MaxConcurrentTasks: 2, AppWorkerID: "fixed-two-" + uuid.NewString()[:8]}, time.Hour, nil)
		require.ErrorContains(t, run.awaitStartResult(t, 30*time.Second), "MaxConcurrentTasks 2 exceeds Server matching.maxConcurrentTasksCap 1")
	})
}

// TestWorkerPullAdaptiveCeilingAboveCapWithRealServer covers design Tests 20a and 20b.
func TestWorkerPullAdaptiveCeilingAboveCapWithRealServer(t *testing.T) {
	t.Run("adaptive clamps", func(t *testing.T) {
		server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{})
		handlers := newAdaptiveSlotHandlers()
		logger := newAdaptiveSlotLogger()
		run := startAdaptiveSlotWorker(t, server.address, handlers, PullWorkerOptions{MaxConcurrentTasks: 300, MinOpenSlots: 8, AppWorkerID: "adaptive-300-" + uuid.NewString()[:8]}, time.Hour, logger)
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		require.NoError(t, run.worker.WaitPullRegistered(ctx))
		require.True(t, logger.hasWarning("clamped MaxConcurrentTasks", "300", "256"))
		require.Equal(t, 1, logger.countWarnings("clamped MaxConcurrentTasks"))
		require.Equal(t, 256, run.worker.PullSlotStreamState().TotalCapacity)
		sampler := startSlotUsageSampler(t, run.worker)
		client := server.newClient(t, run.worker.registry)
		flow := &adaptiveSlotFlow{handlers: handlers}
		flowIDs := startAdaptiveSlotFlowsConcurrently(t, ctx, client, flow, adaptiveSlotModeBlock, 400)
		requireSlotStateWithin(t, run.worker, "every slot busy at the clamped ceiling", 90*time.Second, func(state PullSlotStreamState) bool {
			return state.Busy == 255 && state.Open+state.RPCOnlyOpen == 256
		})
		require.Zero(t, run.worker.PullSlotStreamState().GrowRejectedAtCap)
		close(handlers.release)
		requireAdaptiveSlotFlowsCompleted(t, ctx, client, flowIDs)
		sampler.requireWithinCapacity(t)
		for _, entry := range drainAdaptiveSlotEntries(handlers) {
			require.Equal(t, int32(1), entry.attempt)
		}
	})
	t.Run("fixed fails start", func(t *testing.T) {
		server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{})
		run := startAdaptiveSlotWorker(t, server.address, newAdaptiveSlotHandlers(), PullWorkerOptions{MaxConcurrentTasks: 300, AppWorkerID: "fixed-300-" + uuid.NewString()[:8]}, time.Hour, nil)
		require.ErrorContains(t, run.awaitStartResult(t, 30*time.Second), "MaxConcurrentTasks 300 exceeds Server matching.maxConcurrentTasksCap 256")
	})
}

// TestWorkerPullAdaptiveCapDropToOneWithRealServer covers design Test 17a.
func TestWorkerPullAdaptiveCapDropToOneWithRealServer(t *testing.T) {
	server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 4})
	proxy := startPullSlotProxy(t, server.address)
	handlers := newAdaptiveSlotHandlers()
	handlers.isRPCStubborn = true
	appWorkerID := "adaptive-drop-" + uuid.NewString()[:8]
	run := startAdaptiveSlotWorker(t, proxy.address(), handlers, PullWorkerOptions{
		MaxConcurrentTasks: 4, MinOpenSlots: 2, AppWorkerID: appWorkerID, StopOnPermanentStreamRejection: true,
	}, time.Second, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, run.worker.WaitPullRegistered(ctx))
	client := server.newClient(t, run.worker.registry)
	flow := &adaptiveSlotFlow{handlers: handlers}
	parked := &adaptiveSlotParkedFlow{handlers: handlers}
	parkedID := "adaptive-drop-parked-" + uuid.NewString()
	_, err := client.StartFlow(ctx, parked, parkedID, nil, StartFlowOptions{})
	require.NoError(t, err)
	flowIDs, rpcResult := occupyEverySlot(t, ctx, run.worker, client, flow, parked, parkedID)

	// The restart lowers the cap to 1; the proxy lets the RPC-only stream register first.
	server.kill()
	proxy.holdStepRegistersUntilRPCOnlyAcknowledged()
	server.restart(adaptiveSlotServerLimits{maxConcurrentTasksCap: 1})
	proxy.setUpstream(server.address)
	handlers.releaseStubborn(append(flowIDs, stubbornRPCKey)...)
	awaitRPCAfterRestart(t, rpcResult)

	requireSlotStateWithin(t, run.worker, "one Step-and-RPC stream at cap 1", 30*time.Second, func(state PullSlotStreamState) bool {
		return state.TotalCapacity == 1 && state.RPCOnlySlots == 0 && state.RPCOnlyOpen == 0 && state.Open == 1 && state.Registering == 0 &&
			state.OrphanedHandlers == 0 && state.OrphanedRPCOnlyHandlers == 0
	})
	require.Eventually(t, func() bool {
		return proxy.registeredStreams(appWorkerID, stepAndRPCSlotKind) == 1 && proxy.registeredStreams(appWorkerID, rpcOnlySlotKind) == 0
	}, 10*time.Second, 10*time.Millisecond)
	require.GreaterOrEqual(t, run.worker.PullSlotStreamState().GrowRejectedAtCap, uint64(1), "the Step-and-RPC Register was not rejected at the cap")
	requireNoPermanentRegistrationError(t, run)
	require.True(t, run.worker.PullRegistrationState().IsReady)

	// Steps and RPCs keep running on the one stream.
	restartedClient := server.newClient(t, run.worker.registry)
	requireAdaptiveSlotFlowsCompleted(t, ctx, restartedClient, flowIDs)
	quickIDs := startAdaptiveSlotFlows(t, ctx, restartedClient, flow, adaptiveSlotModeQuick, 2)
	requireAdaptiveSlotFlowsCompleted(t, ctx, restartedClient, quickIDs)
	rpcCtx, cancelRPC := context.WithTimeout(ctx, 10*time.Second)
	defer cancelRPC()
	var rpcStatus string
	require.NoError(t, restartedClient.InvokeRPC(rpcCtx, parkedID, parked.GetSlotStatus, nil, &rpcStatus))
	require.Equal(t, "ok", rpcStatus)
}

// TestWorkerPullFixedCapDropToOneWithRealServer covers design Test 17b: fixed mode keeps today's behavior.
func TestWorkerPullFixedCapDropToOneWithRealServer(t *testing.T) {
	for _, shouldStop := range []bool{true, false} {
		t.Run(fmt.Sprintf("StopOnPermanentStreamRejection=%t", shouldStop), func(t *testing.T) {
			server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 4})
			proxy := startPullSlotProxy(t, server.address)
			appWorkerID := "fixed-drop-" + uuid.NewString()[:8]
			run := startAdaptiveSlotWorker(t, proxy.address(), newAdaptiveSlotHandlers(), PullWorkerOptions{
				MaxConcurrentTasks: 4, AppWorkerID: appWorkerID, StopOnPermanentStreamRejection: shouldStop,
			}, time.Second, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			require.NoError(t, run.worker.WaitPullRegistered(ctx))
			requireRegisteredStreams(t, proxy, appWorkerID, 3, 1)
			server.kill()
			proxy.holdStepRegistersUntilRPCOnlyAcknowledged()
			server.restart(adaptiveSlotServerLimits{maxConcurrentTasksCap: 1})
			proxy.setUpstream(server.address)
			if shouldStop {
				err := run.awaitStartResult(t, 45*time.Second)
				var rejection *PullRegistrationError
				require.ErrorAs(t, err, &rejection)
				require.Equal(t, stepAndRPCSlotKind.String(), rejection.Queue)
				require.Equal(t, codes.ResourceExhausted, status.Code(rejection.Err))
				return
			}
			require.Eventually(t, func() bool {
				queue := run.worker.PullRegistrationState().Queues[0]
				return !queue.HasRegisteredStream && status.Code(queue.LastPermanentError) == codes.ResourceExhausted
			}, 45*time.Second, 10*time.Millisecond)
			require.False(t, run.worker.PullRegistrationState().IsReady)
			// Today's permanent-error backoff is one minute, so no Step-and-RPC stream registers again soon.
			rejectedAt := time.Now()
			require.Never(t, func() bool { return len(stepRegisters(proxy.registersSince(appWorkerID, rejectedAt))) != 0 }, 5*time.Second, 50*time.Millisecond,
				"a Step-and-RPC stream retried before the permanent-error backoff")
			require.False(t, run.worker.PullSlotStreamState().IsAdaptive)
			require.Zero(t, run.worker.PullSlotStreamState().GrowRejectedAtCap)
		})
	}
}

// TestWorkerPullAdaptiveOrphanedHandlersAcrossRestartWithRealServer covers design Tests 15 and 8.
func TestWorkerPullAdaptiveOrphanedHandlersAcrossRestartWithRealServer(t *testing.T) {
	t.Run("same cap", func(t *testing.T) {
		scenario := startOrphanedHandlerScenario(t)
		server, proxy, run, handlers, appWorkerID, flowIDs := scenario.server, scenario.proxy, scenario.run, scenario.handlers, scenario.appWorkerID, scenario.flowIDs
		killedAt := time.Now()
		server.kill()
		server.restart(adaptiveSlotServerLimits{maxConcurrentTasksCap: 4})
		proxy.setUpstream(server.address)
		// No early registration: every slot still has a running handler.
		requireSlotStateWithin(t, run.worker, "every handler orphaned", 10*time.Second, func(state PullSlotStreamState) bool {
			return state.Open == 0 && state.RPCOnlyOpen == 0 && state.OrphanedHandlers == 3 && state.OrphanedRPCOnlyHandlers == 1
		})
		require.Never(t, func() bool {
			return len(proxy.registersSince(appWorkerID, killedAt)) != 0 || !run.worker.PullRegistrationState().IsReady
		}, time.Second, 10*time.Millisecond, "a stream registered while orphaned handlers filled the capacity, or readiness dropped")

		// Freed capacity goes to one Step-and-RPC stream first, then to the RPC-only stream.
		handlers.releaseStubborn(flowIDs[0])
		require.Eventually(t, func() bool { return proxy.registeredStreams(appWorkerID, stepAndRPCSlotKind) == 1 }, 10*time.Second, 10*time.Millisecond)
		handlers.releaseStubborn(stubbornRPCKey)
		require.Eventually(t, func() bool { return proxy.registeredStreams(appWorkerID, rpcOnlySlotKind) == 1 }, 10*time.Second, 10*time.Millisecond)
		registers := proxy.registersSince(appWorkerID, killedAt)
		require.Equal(t, stepAndRPCSlotKind, registers[0].slotKind)
		require.Equal(t, rpcOnlySlotKind, registers[len(registers)-1].slotKind)
		// Test 8: the Worker re-registers at most L + R streams first.
		require.LessOrEqual(t, len(registers), 2)
		handlers.releaseStubborn(flowIDs[1:]...)
		awaitRPCAfterRestart(t, scenario.rpcResult)
		restartedClient := server.newClient(t, run.worker.registry)
		requireAdaptiveSlotFlowsCompleted(t, context.Background(), restartedClient, flowIDs)
		require.True(t, run.worker.PullRegistrationState().IsReady)
		// With the cap unchanged, S + H + Sr + Hr <= C held at every sample.
		scenario.sampler.requireWithinCapacity(t)
	})
	t.Run("cap lowered below the orphaned handlers", func(t *testing.T) {
		scenario := startOrphanedHandlerScenario(t)
		server, proxy, run, handlers, appWorkerID, flowIDs := scenario.server, scenario.proxy, scenario.run, scenario.handlers, scenario.appWorkerID, scenario.flowIDs
		server.kill()
		server.restart(adaptiveSlotServerLimits{maxConcurrentTasksCap: 2})
		proxy.setUpstream(server.address)
		requireSlotStateWithin(t, run.worker, "every handler orphaned", 10*time.Second, func(state PullSlotStreamState) bool {
			return state.OrphanedHandlers == 3 && state.OrphanedRPCOnlyHandlers == 1
		})
		releasedAt := time.Now()
		handlers.releaseStubborn(flowIDs[0])
		// The first acknowledgement reveals cap 2 while usage is 4: the Worker registers nothing more until usage drops below 2.
		require.Eventually(t, func() bool { return len(proxy.acknowledgementsSince(appWorkerID, releasedAt)) == 1 }, 10*time.Second, 10*time.Millisecond)
		acknowledgedAt := proxy.acknowledgementsSince(appWorkerID, releasedAt)[0].at
		requireSlotStateWithin(t, run.worker, "capacity lowered to 2", 10*time.Second, func(state PullSlotStreamState) bool {
			return state.TotalCapacity == 2 && state.Open == 0 && state.Registering == 0
		})
		handlers.releaseStubborn(flowIDs[1])
		requireSlotStateWithin(t, run.worker, "the second handler returned", 10*time.Second, func(state PullSlotStreamState) bool {
			return state.OrphanedHandlers == 1
		})
		// Usage is still 2 of 2 with one Step and one RPC-only handler running.
		require.Never(t, func() bool { return len(proxy.registersSince(appWorkerID, acknowledgedAt.Add(time.Millisecond))) != 0 }, time.Second, 10*time.Millisecond,
			"a Register went out while usage reached the new cap")
		handlers.releaseStubborn(flowIDs[2], stubbornRPCKey)
		requireSlotStateWithin(t, run.worker, "usage converged to the new cap", 20*time.Second, func(state PullSlotStreamState) bool {
			return state.Open == 1 && state.RPCOnlyOpen == 1 && state.Registering == 0 && state.OrphanedHandlers == 0 && state.OrphanedRPCOnlyHandlers == 0
		})
		state := run.worker.PullSlotStreamState()
		require.LessOrEqual(t, state.Open+state.RPCOnlyOpen, state.TotalCapacity)
		requireRegisteredStreams(t, proxy, appWorkerID, 1, 1)
		// F5 allows the transient excess, so this case asserts convergence instead of a bound at every sample.
		t.Logf("samples above the new cap while old handlers ran: %d", len(scenario.sampler.stopSampling()))
		awaitRPCAfterRestart(t, scenario.rpcResult)
		restartedClient := server.newClient(t, run.worker.registry)
		requireAdaptiveSlotFlowsCompleted(t, context.Background(), restartedClient, flowIDs)
	})
	t.Run("failover while a growth batch registers", func(t *testing.T) {
		server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 8})
		proxy := startPullSlotProxy(t, server.address)
		handlers := newAdaptiveSlotHandlers()
		appWorkerID := "adaptive-growth-failover-" + uuid.NewString()[:8]
		run := startAdaptiveSlotWorker(t, proxy.address(), handlers, PullWorkerOptions{MaxConcurrentTasks: 8, MinOpenSlots: 3, AppWorkerID: appWorkerID}, time.Hour, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		require.NoError(t, run.worker.WaitPullRegistered(ctx))
		requireRegisteredStreams(t, proxy, appWorkerID, 2, 1)
		releaseGrowth := proxy.holdStepRegisters()
		client := server.newClient(t, run.worker.registry)
		flowIDs := startAdaptiveSlotFlows(t, ctx, client, &adaptiveSlotFlow{handlers: handlers}, adaptiveSlotModeStubborn, 2)
		// Both floor streams are busy, so a growth batch of 2 waits at the proxy.
		require.Eventually(t, func() bool { return proxy.heldStepRegisters() == 2 }, 20*time.Second, 10*time.Millisecond)
		requireSlotState(t, run.worker, "a growth batch registering", func(state PullSlotStreamState) bool {
			return state.Busy == 2 && state.Registering == 2
		})
		sampler := startSlotUsageSampler(t, run.worker)
		killedAt := time.Now()
		server.kill()
		server.restart(adaptiveSlotServerLimits{maxConcurrentTasksCap: 8})
		proxy.setUpstream(server.address)
		requireSlotStateWithin(t, run.worker, "the busy streams orphaned", 10*time.Second, func(state PullSlotStreamState) bool {
			return state.OrphanedHandlers == 2 && state.Registering == 2 && state.RPCOnlyOpen == 1
		})
		requireRegisteredStreams(t, proxy, appWorkerID, 0, 1)
		// The in-flight batch already holds the floor, so no other Step-and-RPC stream opens.
		require.Empty(t, stepRegisters(proxy.registersSince(appWorkerID, killedAt)))
		releasedAt := time.Now()
		releaseGrowth()
		// The held batch registers on the new server; retried Steps may then grow the Worker further.
		require.Eventually(t, func() bool {
			return len(stepRegisters(proxy.acknowledgementsSince(appWorkerID, releasedAt))) >= 2 && proxy.registeredStreams(appWorkerID, stepAndRPCSlotKind) >= 2
		}, 20*time.Second, 10*time.Millisecond, "the growth batch did not register after the failover")
		handlers.releaseStubborn(flowIDs...)
		restartedClient := server.newClient(t, run.worker.registry)
		requireAdaptiveSlotFlowsCompleted(t, ctx, restartedClient, flowIDs)
		requireSlotState(t, run.worker, "no orphaned handlers", func(state PullSlotStreamState) bool {
			return state.OrphanedHandlers == 0 && state.Registering == 0
		})
		sampler.requireWithinCapacity(t)
		requireNoPermanentRegistrationError(t, run)
	})
	t.Run("failover while a shrink half-close is in flight", func(t *testing.T) {
		server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 8})
		proxy := startPullSlotProxy(t, server.address)
		handlers := newAdaptiveSlotHandlers()
		appWorkerID := "adaptive-shrink-failover-" + uuid.NewString()[:8]
		run := startAdaptiveSlotWorker(t, proxy.address(), handlers, PullWorkerOptions{MaxConcurrentTasks: 8, MinOpenSlots: 3, AppWorkerID: appWorkerID}, time.Second, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		require.NoError(t, run.worker.WaitPullRegistered(ctx))
		client := server.newClient(t, run.worker.registry)
		flow := &adaptiveSlotFlow{handlers: handlers}
		flowIDs := startAdaptiveSlotFlows(t, ctx, client, flow, adaptiveSlotModeBlock, 3)
		// One handler ignores cancellation, so it outlives its stream in the crash.
		stubbornIDs := startAdaptiveSlotFlows(t, ctx, client, flow, adaptiveSlotModeStubborn, 1)
		requireSlotState(t, run.worker, "four busy Steps", func(state PullSlotStreamState) bool { return state.Busy == 4 && state.Registering == 0 })
		heldHalfCloses, releaseHalfCloses := proxy.holdHalfCloses()
		close(handlers.release)
		requireAdaptiveSlotFlowsCompleted(t, ctx, client, flowIDs)
		select {
		case <-heldHalfCloses:
		case <-time.After(20 * time.Second):
			t.Fatal("the Worker never shrank")
		}
		require.Equal(t, 1, run.worker.PullSlotStreamState().Busy)
		// The server still holds the half-closing stream when it crashes, and the stubborn handler still runs.
		sampler := startSlotUsageSampler(t, run.worker)
		server.kill()
		requireSlotStateWithin(t, run.worker, "the stubborn handler orphaned", 10*time.Second, func(state PullSlotStreamState) bool {
			return state.OrphanedHandlers == 1 && state.Busy == 0
		})
		server.restart(adaptiveSlotServerLimits{maxConcurrentTasksCap: 8})
		// Registers that reached the dead server were retries of the same runners, so count from the switch.
		switchedAt := time.Now()
		proxy.setUpstream(server.address)
		releaseHalfCloses()
		requireRegisteredStreams(t, proxy, appWorkerID, 2, 1)
		isShrunkStreamReopened := func() bool {
			return len(stepRegisters(proxy.registersSince(appWorkerID, switchedAt))) > 2 || proxy.registeredStreams(appWorkerID, stepAndRPCSlotKind) > 2
		}
		require.Never(t, isShrunkStreamReopened, 3*time.Second, 20*time.Millisecond, "a stream closed by shrink reopened after the failover")
		state := run.worker.PullSlotStreamState()
		require.Equal(t, 1, state.OrphanedHandlers, "the orphaned handler left the capacity count early: %+v", state)
		require.Equal(t, 2, state.Open)
		// The returning handler frees a slot, but the floor is already open, so nothing reopens.
		handlers.releaseStubborn(stubbornIDs...)
		requireSlotState(t, run.worker, "the orphaned handler returned", func(state PullSlotStreamState) bool {
			return state.OrphanedHandlers == 0 && state.Open == 2 && state.RPCOnlyOpen == 1 && state.Registering == 0
		})
		require.Never(t, isShrunkStreamReopened, 2*time.Second, 20*time.Millisecond, "a stream reopened when the orphaned handler returned")
		restartedClient := server.newClient(t, run.worker.registry)
		requireAdaptiveSlotFlowsCompleted(t, ctx, restartedClient, stubbornIDs)
		sampler.requireWithinCapacity(t)
		requireNoPermanentRegistrationError(t, run)
	})
}

// TestWorkerPullFixedModeUnchangedWithRealServer covers design Tests 1 and 16.
func TestWorkerPullFixedModeUnchangedWithRealServer(t *testing.T) {
	server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{})
	proxy := startPullSlotProxy(t, server.address)
	releaseAcknowledgements := proxy.holdAcknowledgements()
	handlers := newAdaptiveSlotHandlers()
	appWorkerID := "fixed-parallel-" + uuid.NewString()[:8]
	run := startAdaptiveSlotWorker(t, proxy.address(), handlers, PullWorkerOptions{MaxConcurrentTasks: 4, AppWorkerID: appWorkerID}, time.Second, nil)
	// Test 16: every stream opens before the first acknowledgement arrives.
	require.Eventually(t, func() bool { return len(proxy.registersSince(appWorkerID, time.Time{})) == 4 }, 10*time.Second, 10*time.Millisecond)
	releaseAcknowledgements()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, run.worker.WaitPullRegistered(ctx))
	for _, register := range proxy.registersSince(appWorkerID, time.Time{}) {
		require.True(t, register.at.Before(proxy.firstAcknowledgementAt()))
	}
	// Test 1: the count stays at the ceiling through load and idle periods.
	requireRegisteredStreams(t, proxy, appWorkerID, 3, 1)
	client := server.newClient(t, run.worker.registry)
	flow := &adaptiveSlotFlow{handlers: handlers}
	flowIDs := startAdaptiveSlotFlows(t, ctx, client, flow, adaptiveSlotModeBlock, 3)
	awaitAdaptiveSlotEntries(t, handlers, 3)
	require.Equal(t, 3, run.worker.PullSlotStreamState().Open)
	close(handlers.release)
	requireAdaptiveSlotFlowsCompleted(t, ctx, client, flowIDs)
	since := time.Now()
	require.Never(t, func() bool {
		return run.worker.PullSlotStreamState().Open != 3 || len(proxy.registersSince(appWorkerID, since)) != 0
	}, 3*time.Second, 20*time.Millisecond, "a fixed Worker changed its stream count at rest")
	state := run.worker.PullSlotStreamState()
	require.False(t, state.IsAdaptive)
	require.Equal(t, 3, state.Open)
	require.Equal(t, 1, state.RPCOnlyOpen)
	require.Zero(t, state.StreamsGrown)
	require.Zero(t, state.StreamsShrunk)
	require.Empty(t, proxy.registersSince(appWorkerID, since))
	requireRegisteredStreams(t, proxy, appWorkerID, 3, 1)
}

// orphanedHandlerScenario is an adaptive Worker whose every slot runs a handler that ignores cancellation.
type orphanedHandlerScenario struct {
	server      *adaptiveSlotTestServer
	proxy       *pullSlotProxy
	run         *adaptiveSlotWorkerRun
	handlers    *adaptiveSlotHandlers
	appWorkerID string
	flowIDs     []string
	rpcResult   <-chan error
	sampler     *slotUsageSampler
}

// TestWorkerPullAdaptiveGrantRevokedAfterCapDropWithRealServer checks F5 for an attempt granted under the
// old cap: when another stream reports a lower cap during its backoff, it sends no Register above that cap.
func TestWorkerPullAdaptiveGrantRevokedAfterCapDropWithRealServer(t *testing.T) {
	server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 8})
	proxy := startPullSlotProxy(t, server.address)
	appWorkerID := "adaptive-revoke-" + uuid.NewString()[:8]
	run := startAdaptiveSlotWorker(t, proxy.address(), newAdaptiveSlotHandlers(), PullWorkerOptions{
		MaxConcurrentTasks: 8, MinOpenSlots: 3, ReservedRPCTasks: ptr.Any(0), AppWorkerID: appWorkerID,
	}, time.Hour, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, run.worker.WaitPullRegistered(ctx))
	requireRegisteredStreams(t, proxy, appWorkerID, 3, 0)

	// Every floor stream breaks and gets a reconnect grant under the last known cap of 8.
	server.kill()
	proxy.failStepRegistersFromNow()
	server.restart(adaptiveSlotServerLimits{maxConcurrentTasksCap: 2})
	proxy.setUpstream(server.address)
	// One runner registers and learns cap 2 while the other two wait in their backoff.
	proxy.passNextStepRegister()
	requireSlotStateWithin(t, run.worker, "the lower cap is known", 30*time.Second, func(state PullSlotStreamState) bool {
		return state.TotalCapacity == 2
	})
	proxy.stopFailingStepRegisters()
	capKnownAt := time.Now()
	// One registered stream leaves room for exactly one more; the other old grant must be revoked.
	require.Never(t, func() bool {
		return len(stepRegisters(proxy.registersSince(appWorkerID, capKnownAt))) > 1 || len(proxy.rejectionsSince(appWorkerID, capKnownAt)) != 0
	}, 8*time.Second, 20*time.Millisecond, "a Register granted under the old cap went out above the new cap")
	requireSlotState(t, run.worker, "two streams at cap 2", func(state PullSlotStreamState) bool {
		return state.Open == 2 && state.Registering == 0
	})
	requireRegisteredStreams(t, proxy, appWorkerID, 2, 0)
	requireNoPermanentRegistrationError(t, run)
}

// Admission is re-checked before Send; a cap drop after it gets a Server rejection, soft in adaptive mode.
func TestWorkerPullAdaptiveCapDropBetweenAdmissionAndSendWithRealServer(t *testing.T) {
	server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 8})
	proxy := startPullSlotProxy(t, server.address)
	barrier := &registerSendBarrier{passes: make(chan struct{}, 4), released: make(chan struct{})}
	appWorkerID := "adaptive-barrier-" + uuid.NewString()[:8]
	run := startAdaptiveSlotWorkerWithSettings(t, proxy.address(), newAdaptiveSlotHandlers(), PullWorkerOptions{
		MaxConcurrentTasks: 8, MinOpenSlots: 3, ReservedRPCTasks: ptr.Any(0), AppWorkerID: appWorkerID,
	}, pullSlotSettings{shrinkWindow: time.Second, beforeRegisterSendForTest: barrier.hold})
	t.Cleanup(barrier.release)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, run.worker.WaitPullRegistered(ctx))
	requireRegisteredStreams(t, proxy, appWorkerID, 3, 0)

	// Every floor stream breaks; each runner is admitted under the old cap of 8 and waits before its Send.
	barrier.isArmed.Store(true)
	server.kill()
	require.Eventually(t, func() bool { return barrier.held.Load() == 3 }, 30*time.Second, 10*time.Millisecond, "the runners did not reach the Send barrier")
	server.restart(adaptiveSlotServerLimits{maxConcurrentTasksCap: 2})
	proxy.setUpstream(server.address)
	// One runner sends, registers, and reports cap 2 while the other two stay admitted but unsent.
	barrier.passes <- struct{}{}
	requireSlotStateWithin(t, run.worker, "the lower cap is known", 30*time.Second, func(state PullSlotStreamState) bool {
		return state.TotalCapacity == 2
	})
	capKnownAt := time.Now()
	barrier.release()
	// The two held attempts are re-checked after cap 2 is known: one is revoked and sends nothing.
	// A Register that passed its re-check just before the cap became known may still meet the cap once;
	// that rejection is soft, and the latch retries it at the next window.
	require.Never(t, func() bool {
		rejections := len(proxy.rejectionsSince(appWorkerID, capKnownAt))
		return rejections > 1 || len(stepRegisters(proxy.registersSince(appWorkerID, capKnownAt))) > 1+rejections ||
			proxy.registeredStreams(appWorkerID, stepAndRPCSlotKind) > 2
	}, 3*time.Second, 10*time.Millisecond, "a revoked attempt sent a Register, or Registers exceeded the new cap")
	requireRegisteredStreams(t, proxy, appWorkerID, 2, 0)
	requireSlotState(t, run.worker, "two streams at cap 2", func(state PullSlotStreamState) bool {
		return state.Open == 2 && state.Registering == 0
	})
	if rejections := len(proxy.rejectionsSince(appWorkerID, capKnownAt)); rejections > 0 {
		require.GreaterOrEqual(t, run.worker.PullSlotStreamState().GrowRejectedAtCap, uint64(rejections), "the rejection was not handled as soft")
	}
	requireNoPermanentRegistrationError(t, run)
}

// TestWorkerPullAdaptiveBlockedRegisterSendWithRealServer blocks one Register's Send. Cap updates and
// handlers on the other streams must still progress, and Stop must still finish.
func TestWorkerPullAdaptiveBlockedRegisterSendWithRealServer(t *testing.T) {
	server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 4})
	handlers := newAdaptiveSlotHandlers()
	registry, err := NewRegistry([]Flow{&adaptiveSlotFlow{handlers: handlers}, &adaptiveSlotParkedFlow{handlers: handlers}})
	require.NoError(t, err)
	cache, err := blobcache.New(&blobcache.Config{Dir: t.TempDir(), MaxBytes: 1 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	worker, err := newWorker(registry, cache, WorkerOptions{FlowServiceAddress: server.address, Pull: &PullWorkerOptions{
		MaxConcurrentTasks: 8, MinOpenSlots: 3, StopDrainTimeout: time.Second, AppWorkerID: "adaptive-blocked-send-" + uuid.NewString()[:8],
	}}, pullSlotSettings{shrinkWindow: time.Hour})
	require.NoError(t, err)
	blocking := &blockingRegisterPollClient{WorkerTaskServiceClient: worker.pull.client, release: make(chan struct{})}
	blocking.remainingBlocks.Store(1)
	worker.pull.client = blocking
	started := make(chan error, 1)
	go func() { started <- worker.Start() }()
	t.Cleanup(blocking.releaseAll)

	// The first Step-and-RPC Register blocks in Send; the second registers and reports cap 4.
	require.Eventually(t, func() bool { return blocking.blockedSends.Load() == 1 }, 20*time.Second, 10*time.Millisecond)
	requireSlotStateWithin(t, worker, "a cap update on another stream", 20*time.Second, func(state PullSlotStreamState) bool {
		return state.TotalCapacity == 4 && state.Idle >= 1
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	client := server.newClient(t, registry)
	flowIDs := startAdaptiveSlotFlows(t, ctx, client, &adaptiveSlotFlow{handlers: handlers}, adaptiveSlotModeQuick, 2)
	requireAdaptiveSlotFlowsCompleted(t, ctx, client, flowIDs)
	require.Equal(t, int32(1), blocking.blockedSends.Load(), "the blocked Send returned on its own")

	// Stop finishes within its drain timeout although one Send never returns by itself.
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStop()
	stopped := make(chan error, 1)
	stopStartedAt := time.Now()
	go func() { stopped <- worker.Stop(stopCtx) }()
	select {
	case err := <-stopped:
		if err != nil {
			require.ErrorIs(t, err, context.DeadlineExceeded)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Stop did not finish while a Register Send was blocked")
	}
	require.Less(t, time.Since(stopStartedAt), 10*time.Second)
	select {
	case err := <-started:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return after Stop")
	}
}

// TestWorkerPullAdaptiveCapDropWithoutOrphansWithRealServer covers design Test 7 for a cap above and below the floor.
func TestWorkerPullAdaptiveCapDropWithoutOrphansWithRealServer(t *testing.T) {
	t.Run("new cap above the floor", func(t *testing.T) {
		server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 8})
		proxy := startPullSlotProxy(t, server.address)
		handlers := newAdaptiveSlotHandlers()
		appWorkerID := "adaptive-drop-above-" + uuid.NewString()[:8]
		run := startAdaptiveSlotWorker(t, proxy.address(), handlers, PullWorkerOptions{MaxConcurrentTasks: 8, MinOpenSlots: 3, AppWorkerID: appWorkerID}, time.Second, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		require.NoError(t, run.worker.WaitPullRegistered(ctx))
		client := server.newClient(t, run.worker.registry)
		flow := &adaptiveSlotFlow{handlers: handlers}
		flowIDs := startAdaptiveSlotFlows(t, ctx, client, flow, adaptiveSlotModeBlock, 6)
		requireSlotState(t, run.worker, "six busy Steps", func(state PullSlotStreamState) bool { return state.Busy == 6 })
		sampler := startSlotUsageSampler(t, run.worker)
		server.kill()
		restartedAt := time.Now()
		server.restart(adaptiveSlotServerLimits{maxConcurrentTasksCap: 4})
		proxy.setUpstream(server.address)
		// C = 4 and R = 1 leave E = 3; the retried Steps grow the Worker back to E.
		requireSlotStateWithin(t, run.worker, "growth up to the lower cap", 30*time.Second, func(state PullSlotStreamState) bool {
			return state.TotalCapacity == 4 && state.EffectiveCeiling == 3 && state.Busy == 3 && state.Open == 3 && state.RPCOnlyOpen == 1 && state.Registering == 0
		})
		requireRegisteredStreams(t, proxy, appWorkerID, 3, 1)
		require.Empty(t, proxy.rejectionsSince(appWorkerID, restartedAt))
		require.Empty(t, proxy.workerResetsSince(appWorkerID, restartedAt), "cap convergence reset a stream")
		requireNoPermanentRegistrationError(t, run)
		close(handlers.release)
		restartedClient := server.newClient(t, run.worker.registry)
		requireAdaptiveSlotFlowsCompleted(t, ctx, restartedClient, flowIDs)
		sampler.requireWithinCapacity(t)
		require.Empty(t, proxy.workerResetsSince(appWorkerID, restartedAt), "cap convergence reset a stream")
	})
	t.Run("new cap below the floor", func(t *testing.T) {
		server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 8})
		proxy := startPullSlotProxy(t, server.address)
		handlers := newAdaptiveSlotHandlers()
		appWorkerID := "adaptive-drop-below-" + uuid.NewString()[:8]
		run := startAdaptiveSlotWorker(t, proxy.address(), handlers, PullWorkerOptions{
			MaxConcurrentTasks: 8, MinOpenSlots: 5, AppWorkerID: appWorkerID, StopOnPermanentStreamRejection: true,
		}, time.Second, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		require.NoError(t, run.worker.WaitPullRegistered(ctx))
		requireRegisteredStreams(t, proxy, appWorkerID, 4, 1)
		server.kill()
		restartedAt := time.Now()
		server.restart(adaptiveSlotServerLimits{maxConcurrentTasksCap: 3})
		proxy.setUpstream(server.address)
		// C = 3 and R = 1 leave E = 2, so the floor of 4 becomes L_eff = 2.
		requireSlotStateWithin(t, run.worker, "the floor limited by the lower cap", 30*time.Second, func(state PullSlotStreamState) bool {
			return state.TotalCapacity == 3 && state.Floor == 2 && state.Open == 2 && state.RPCOnlyOpen == 1 && state.Registering == 0
		})
		requireRegisteredStreams(t, proxy, appWorkerID, 2, 1)
		require.Empty(t, proxy.workerResetsSince(appWorkerID, restartedAt), "cap convergence reset a stream")
		requireNoPermanentRegistrationError(t, run)
		restartedClient := server.newClient(t, run.worker.registry)
		flow := &adaptiveSlotFlow{handlers: handlers}
		quickIDs := startAdaptiveSlotFlows(t, ctx, restartedClient, flow, adaptiveSlotModeQuick, 3)
		requireAdaptiveSlotFlowsCompleted(t, ctx, restartedClient, quickIDs)
	})
}

// TestWorkerPullAdaptiveGracefulStopDuringGrowthWithRealServer covers design Test 12.
func TestWorkerPullAdaptiveGracefulStopDuringGrowthWithRealServer(t *testing.T) {
	server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{})
	handlers := newAdaptiveSlotHandlers()
	run := startAdaptiveSlotWorker(t, server.address, handlers, PullWorkerOptions{MaxConcurrentTasks: 32, MinOpenSlots: 3, AppWorkerID: "adaptive-stop-" + uuid.NewString()[:8]}, time.Second, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, run.worker.WaitPullRegistered(ctx))
	client := server.newClient(t, run.worker.registry)
	flow := &adaptiveSlotFlow{handlers: handlers}
	flowIDs := startAdaptiveSlotFlowsConcurrently(t, ctx, client, flow, adaptiveSlotModeBlock, 20)
	awaitAdaptiveSlotEntries(t, handlers, 4)
	stopped := make(chan error, 1)
	go func() { stopped <- run.worker.Stop(ctx) }()
	require.Eventually(t, func() bool {
		run.worker.pull.mu.Lock()
		defer run.worker.pull.mu.Unlock()
		return run.worker.pull.isStopping
	}, 10*time.Second, time.Millisecond)
	// Busy handlers finish during the drain; tasks that never started return without spending a retry.
	close(handlers.release)
	require.NoError(t, <-stopped)
	require.NoError(t, run.awaitStartResult(t, 10*time.Second))
	replacement := startAdaptiveSlotWorker(t, server.address, handlers, PullWorkerOptions{MaxConcurrentTasks: 8, AppWorkerID: "fixed-replacement-" + uuid.NewString()[:8]}, time.Hour, nil)
	require.NoError(t, replacement.worker.WaitPullRegistered(ctx))
	requireAdaptiveSlotFlowsCompleted(t, ctx, client, flowIDs)
	for _, entry := range drainAdaptiveSlotEntries(handlers) {
		require.Equal(t, int32(1), entry.attempt, "a Step spent a retry: %+v", entry)
	}
}

// TestWorkerPullAdaptiveMixedFleetWithRealServer covers design Test 13 against one server.
func TestWorkerPullAdaptiveMixedFleetWithRealServer(t *testing.T) {
	server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{})
	handlers := newAdaptiveSlotHandlers()
	fixed := startAdaptiveSlotWorker(t, server.address, handlers, PullWorkerOptions{MaxConcurrentTasks: 4, AppWorkerID: "fixed-fleet-" + uuid.NewString()[:8]}, time.Hour, nil)
	adaptive := startAdaptiveSlotWorker(t, server.address, handlers, PullWorkerOptions{MaxConcurrentTasks: 16, MinOpenSlots: 2, AppWorkerID: "adaptive-fleet-" + uuid.NewString()[:8]}, time.Second, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, fixed.worker.WaitPullRegistered(ctx))
	require.NoError(t, adaptive.worker.WaitPullRegistered(ctx))
	client := server.newClient(t, adaptive.worker.registry)
	flow := &adaptiveSlotFlow{handlers: handlers}
	quickIDs := startAdaptiveSlotFlows(t, ctx, client, flow, adaptiveSlotModeQuick, 10)
	requireAdaptiveSlotFlowsCompleted(t, ctx, client, quickIDs)
	// The fixed Worker leaves; its share moves to the adaptive Worker, which grows from its floor.
	require.NoError(t, fixed.worker.Stop(ctx))
	flowIDs := startAdaptiveSlotFlows(t, ctx, client, flow, adaptiveSlotModeBlock, 8)
	awaitAdaptiveSlotEntries(t, handlers, 8)
	requireSlotState(t, adaptive.worker, "the adaptive Worker grew to the load", func(state PullSlotStreamState) bool {
		return state.Busy == 8 && state.Open >= 8 && state.Open <= 15
	})
	close(handlers.release)
	requireAdaptiveSlotFlowsCompleted(t, ctx, client, flowIDs)
	for _, entry := range drainAdaptiveSlotEntries(handlers) {
		require.Equal(t, int32(1), entry.attempt, "a Step spent a retry: %+v", entry)
	}
}

// startOrphanedHandlerScenario fills every slot of an adaptive Worker with handlers that ignore cancellation.
func startOrphanedHandlerScenario(t *testing.T) *orphanedHandlerScenario {
	t.Helper()
	server := startAdaptiveSlotTestServer(t, adaptiveSlotServerLimits{maxConcurrentTasksCap: 4})
	proxy := startPullSlotProxy(t, server.address)
	handlers := newAdaptiveSlotHandlers()
	handlers.isRPCStubborn = true
	appWorkerID := "adaptive-orphan-" + uuid.NewString()[:8]
	run := startAdaptiveSlotWorker(t, proxy.address(), handlers, PullWorkerOptions{MaxConcurrentTasks: 4, MinOpenSlots: 2, AppWorkerID: appWorkerID}, time.Hour, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	require.NoError(t, run.worker.WaitPullRegistered(ctx))
	client := server.newClient(t, run.worker.registry)
	parked := &adaptiveSlotParkedFlow{handlers: handlers}
	parkedID := "adaptive-orphan-parked-" + uuid.NewString()
	_, err := client.StartFlow(ctx, parked, parkedID, nil, StartFlowOptions{})
	require.NoError(t, err)
	flowIDs, rpcResult := occupyEverySlot(t, ctx, run.worker, client, &adaptiveSlotFlow{handlers: handlers}, parked, parkedID)
	return &orphanedHandlerScenario{
		server: server, proxy: proxy, run: run, handlers: handlers, appWorkerID: appWorkerID, flowIDs: flowIDs, rpcResult: rpcResult,
		sampler: startSlotUsageSampler(t, run.worker),
	}
}

// occupyEverySlot starts E stubborn Steps, then one stubborn RPC that can only land on the RPC-only stream.
func occupyEverySlot(t *testing.T, ctx context.Context, worker *Worker, client *Client, flow *adaptiveSlotFlow, parked *adaptiveSlotParkedFlow, parkedID string) ([]string, <-chan error) {
	t.Helper()
	ceiling := worker.PullSlotStreamState().EffectiveCeiling
	flowIDs := startAdaptiveSlotFlows(t, ctx, client, flow, adaptiveSlotModeStubborn, ceiling)
	awaitAdaptiveSlotEntries(t, flow.handlers, ceiling)
	requireSlotState(t, worker, "every Step-and-RPC slot busy", func(state PullSlotStreamState) bool {
		return state.Busy == ceiling && state.Open == ceiling
	})
	rpcResult := make(chan error, 1)
	go func() {
		rpcCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		var output string
		rpcResult <- client.InvokeRPC(rpcCtx, parkedID, parked.BlockUntilReleased, nil, &output)
	}()
	entry := awaitAdaptiveSlotEntries(t, flow.handlers, 1)[0]
	require.Equal(t, "rpc", entry.kind)
	return flowIDs, rpcResult
}

func requireCapOfOneWorkerServes(t *testing.T, server *adaptiveSlotTestServer, proxy *pullSlotProxy, run *adaptiveSlotWorkerRun, appWorkerID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	require.NoError(t, run.worker.WaitPullRegistered(ctx))
	requireSlotState(t, run.worker, "one Step-and-RPC stream at cap 1", func(state PullSlotStreamState) bool {
		return state.TotalCapacity == 1 && state.RPCOnlySlots == 0 && state.Open == 1 && state.RPCOnlyOpen == 0 && state.Registering == 0
	})
	requireRegisteredStreams(t, proxy, appWorkerID, 1, 0)
	registration := run.worker.PullRegistrationState()
	require.True(t, registration.IsReady)
	require.Len(t, registration.Queues, 1)
	handlers := newAdaptiveSlotHandlers()
	client := server.newClient(t, run.worker.registry)
	flow := &adaptiveSlotFlow{handlers: handlers}
	flowIDs := startAdaptiveSlotFlows(t, ctx, client, flow, adaptiveSlotModeQuick, 2)
	requireAdaptiveSlotFlowsCompleted(t, ctx, client, flowIDs)
	parked := &adaptiveSlotParkedFlow{handlers: handlers}
	parkedID := "cap-one-parked-" + uuid.NewString()
	_, err := client.StartFlow(ctx, parked, parkedID, nil, StartFlowOptions{})
	require.NoError(t, err)
	var rpcStatus string
	require.NoError(t, client.InvokeRPC(ctx, parkedID, parked.GetSlotStatus, nil, &rpcStatus))
	require.Equal(t, "ok", rpcStatus)
}

// awaitRPCAfterRestart waits for the RPC that the restart interrupted; its result depends on client retries.
func awaitRPCAfterRestart(t *testing.T, rpcResult <-chan error) {
	t.Helper()
	select {
	case err := <-rpcResult:
		t.Logf("RPC interrupted by the restart returned: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the RPC interrupted by the restart did not return")
	}
}

func requireRegisteredStreams(t *testing.T, proxy *pullSlotProxy, appWorkerID string, stepStreams int, rpcOnlyStreams int) {
	t.Helper()
	isReached := assert.Eventually(t, func() bool {
		return proxy.registeredStreams(appWorkerID, stepAndRPCSlotKind) == stepStreams && proxy.registeredStreams(appWorkerID, rpcOnlySlotKind) == rpcOnlyStreams
	}, 20*time.Second, 10*time.Millisecond)
	if !isReached {
		t.Fatalf("server streams of %s: want %d Step-and-RPC and %d RPC-only, have %d and %d\n%s", appWorkerID, stepStreams, rpcOnlyStreams,
			proxy.registeredStreams(appWorkerID, stepAndRPCSlotKind), proxy.registeredStreams(appWorkerID, rpcOnlySlotKind), proxy.describe(appWorkerID))
	}
}

func requireSlotState(t *testing.T, worker *Worker, description string, condition func(PullSlotStreamState) bool) {
	t.Helper()
	requireSlotStateWithin(t, worker, description, 30*time.Second, condition)
}

func requireSlotStateWithin(t *testing.T, worker *Worker, description string, timeout time.Duration, condition func(PullSlotStreamState) bool) {
	t.Helper()
	var last PullSlotStreamState
	isReached := assert.Eventually(t, func() bool {
		last = worker.PullSlotStreamState()
		return condition(last)
	}, timeout, 10*time.Millisecond)
	if !isReached {
		t.Fatalf("slot streams never reached %s: %+v", description, last)
	}
}

func requireNoPermanentRegistrationError(t *testing.T, run *adaptiveSlotWorkerRun) {
	t.Helper()
	registration := run.worker.PullRegistrationState()
	require.True(t, registration.IsReady)
	for _, queue := range registration.Queues {
		require.NoError(t, queue.LastPermanentError, "slot kind %s recorded a permanent error", queue.Queue)
	}
	select {
	case err := <-run.started:
		run.result, run.hasResult = err, true
		t.Fatalf("Start returned during a soft rejection: %v", err)
	default:
	}
}

// requireRegisterBatchesPerWindow checks that Registers come in at most one batch per window.
func requireRegisterBatchesPerWindow(t *testing.T, registers []pullSlotProxyRegister, window time.Duration, batchSize int) {
	t.Helper()
	batchStart := registers[0].at
	batchCount := 0
	for _, register := range registers {
		if register.at.Sub(batchStart) > window/2 {
			require.GreaterOrEqual(t, register.at.Sub(batchStart), window*8/10, "two growth batches in one window")
			batchStart = register.at
			batchCount = 0
		}
		batchCount++
		require.LessOrEqual(t, batchCount, batchSize)
	}
}

// countRegisterBatches groups Registers that arrive within half a window of a batch's first one.
func countRegisterBatches(registers []pullSlotProxyRegister, window time.Duration) int {
	batches := 0
	var batchStart time.Time
	for _, register := range registers {
		if batches == 0 || register.at.Sub(batchStart) > window/2 {
			batches++
			batchStart = register.at
		}
	}
	return batches
}

func stepRegisters(registers []pullSlotProxyRegister) []pullSlotProxyRegister {
	var stepOnly []pullSlotProxyRegister
	for _, register := range registers {
		if register.slotKind == stepAndRPCSlotKind {
			stepOnly = append(stepOnly, register)
		}
	}
	return stepOnly
}

// adaptiveSlotWindowsToFloor is invariant 5: ceil(log2(E - L + 1)) + 1 windows.
func adaptiveSlotWindowsToFloor(effectiveCeiling int, floor int) int {
	windows := 1
	for span := 1; span < effectiveCeiling-floor+1; span *= 2 {
		windows++
	}
	return windows
}

type slotUsageSampler struct {
	stop       chan struct{}
	done       chan struct{}
	violations []PullSlotStreamState
}

// startSlotUsageSampler records every sample where S + H + Sr + Hr exceeds C.
func startSlotUsageSampler(t *testing.T, worker *Worker) *slotUsageSampler {
	t.Helper()
	sampler := &slotUsageSampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(sampler.done)
		for {
			select {
			case <-sampler.stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
			state := worker.PullSlotStreamState()
			if state.Open+state.OrphanedHandlers+state.RPCOnlyOpen+state.OrphanedRPCOnlyHandlers > state.TotalCapacity && len(sampler.violations) < 8 {
				sampler.violations = append(sampler.violations, state)
			}
		}
	}()
	t.Cleanup(func() { sampler.stopSampling() })
	return sampler
}

func (sampler *slotUsageSampler) requireWithinCapacity(t *testing.T) {
	t.Helper()
	require.Empty(t, sampler.stopSampling(), "slot usage exceeded the capacity")
}

// stopSampling stops the sampler and returns the samples above the capacity.
func (sampler *slotUsageSampler) stopSampling() []PullSlotStreamState {
	select {
	case <-sampler.stop:
	default:
		close(sampler.stop)
	}
	<-sampler.done
	return sampler.violations
}

func (client *blockingRegisterPollClient) PollWorkerTask(ctx context.Context, options ...grpc.CallOption) (dexpb.WorkerTaskService_PollWorkerTaskClient, error) {
	stream, err := client.WorkerTaskServiceClient.PollWorkerTask(ctx, options...)
	if err != nil {
		return nil, err
	}
	return &blockingRegisterPollStream{WorkerTaskService_PollWorkerTaskClient: stream, client: client, ctx: ctx}, nil
}

func (client *blockingRegisterPollClient) releaseAll() {
	client.releaseOnce.Do(func() { close(client.release) })
}

// Send blocks like a stalled transport; a cancelled stream context still ends it, as in grpc.
func (stream *blockingRegisterPollStream) Send(request *dexpb.PollWorkerTaskRequest) error {
	isStepRegister := request.GetRegister().GetSlotKind() == dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_STEP_AND_RPC
	if isStepRegister && stream.client.remainingBlocks.Add(-1) >= 0 {
		stream.client.blockedSends.Add(1)
		defer stream.client.blockedSends.Add(-1)
		select {
		case <-stream.client.release:
		case <-stream.ctx.Done():
			return stream.ctx.Err()
		}
	}
	return stream.WorkerTaskService_PollWorkerTaskClient.Send(request)
}

// hold waits for a pass token, for release, or for the stream's end.
func (barrier *registerSendBarrier) hold(stream *pullStream) {
	if !barrier.isArmed.Load() {
		return
	}
	barrier.held.Add(1)
	defer barrier.held.Add(-1)
	select {
	case <-barrier.passes:
	case <-barrier.released:
	case <-stream.ctx.Done():
	}
}

func (barrier *registerSendBarrier) release() {
	barrier.releaseOnce.Do(func() { close(barrier.released) })
}

func newAdaptiveSlotHandlers() *adaptiveSlotHandlers {
	return &adaptiveSlotHandlers{
		entered: make(chan adaptiveSlotEntry, 1024), release: make(chan struct{}),
		stubbornReleases: make(map[string]chan struct{}), releasedStubborn: make(map[string]bool),
	}
}

func (handlers *adaptiveSlotHandlers) stubbornRelease(key string) chan struct{} {
	handlers.mu.Lock()
	defer handlers.mu.Unlock()
	release, found := handlers.stubbornReleases[key]
	if !found {
		release = make(chan struct{})
		handlers.stubbornReleases[key] = release
	}
	return release
}

func (handlers *adaptiveSlotHandlers) releaseStubborn(keys ...string) {
	for _, key := range keys {
		release := handlers.stubbornRelease(key)
		handlers.mu.Lock()
		if !handlers.releasedStubborn[key] {
			handlers.releasedStubborn[key] = true
			close(release)
		}
		handlers.mu.Unlock()
	}
}

// releaseAllStubborn lets every handler that ignores cancellation return, so a failed test can stop its Worker.
func (handlers *adaptiveSlotHandlers) releaseAllStubborn() {
	handlers.mu.Lock()
	keys := make([]string, 0, len(handlers.stubbornReleases))
	for key := range handlers.stubbornReleases {
		keys = append(keys, key)
	}
	handlers.mu.Unlock()
	handlers.releaseStubborn(append(keys, stubbornRPCKey)...)
}

func (flow *adaptiveSlotFlow) GetSteps() []StepDef {
	return []StepDef{DefineStartStep(adaptiveSlotStep{handlers: flow.handlers})}
}

func (flow *adaptiveSlotFlow) GetRPCs() []RPCDef {
	return []RPCDef{DefineRPC(flow.GetSlotStatus, nil)}
}

func (*adaptiveSlotFlow) GetPersistenceSchema() PersistenceSchema { return PersistenceSchema{} }

func (*adaptiveSlotFlow) GetSlotStatus(Context, None) (*RPCResult[string], error) {
	return &RPCResult[string]{Output: "ok"}, nil
}

func (step adaptiveSlotStep) Execute(ctx Context, input adaptiveSlotInput) (*StepDecision, error) {
	step.handlers.entered <- adaptiveSlotEntry{flowID: ctx.FlowID(), kind: "step", attempt: ctx.Attempt()}
	switch input.Mode {
	case adaptiveSlotModeBlock:
		select {
		case <-step.handlers.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case adaptiveSlotModeStubborn:
		// Only the first attempt ignores cancellation, so the retry after a restart completes.
		if ctx.Attempt() == 1 {
			<-step.handlers.stubbornRelease(ctx.FlowID())
		}
	}
	return GracefulComplete(input.Mode), nil
}

func (flow *adaptiveSlotParkedFlow) GetSteps() []StepDef {
	return []StepDef{DefineStartStep(adaptiveSlotParkedStep{})}
}

func (flow *adaptiveSlotParkedFlow) GetRPCs() []RPCDef {
	return []RPCDef{DefineRPC(flow.BlockUntilReleased, nil), DefineRPC(flow.GetSlotStatus, nil)}
}

func (*adaptiveSlotParkedFlow) GetPersistenceSchema() PersistenceSchema { return PersistenceSchema{} }

func (flow *adaptiveSlotParkedFlow) BlockUntilReleased(ctx Context, _ None) (*RPCResult[string], error) {
	flow.handlers.entered <- adaptiveSlotEntry{flowID: ctx.FlowID(), kind: "rpc", attempt: ctx.Attempt()}
	if flow.handlers.isRPCStubborn {
		<-flow.handlers.stubbornRelease(stubbornRPCKey)
		return &RPCResult[string]{Output: "released"}, nil
	}
	select {
	case <-flow.handlers.release:
		return &RPCResult[string]{Output: "released"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (*adaptiveSlotParkedFlow) GetSlotStatus(Context, None) (*RPCResult[string], error) {
	return &RPCResult[string]{Output: "ok"}, nil
}

func (adaptiveSlotParkedStep) WaitFor(Context, None) (*Wait, error) {
	return Until(Timer(time.Hour)), nil
}

func (adaptiveSlotParkedStep) Execute(Context, None) (*StepDecision, error) {
	return GracefulComplete("parked"), nil
}

// startAdaptiveSlotWorker starts a Worker with both test Flows and a test shrink window.
func startAdaptiveSlotWorker(t *testing.T, address string, handlers *adaptiveSlotHandlers, pull PullWorkerOptions, shrinkWindow time.Duration, logger Logger) *adaptiveSlotWorkerRun {
	t.Helper()
	flows := []Flow{&adaptiveSlotFlow{handlers: handlers}, &adaptiveSlotParkedFlow{handlers: handlers}}
	return startAdaptiveSlotWorkerWithFlows(t, address, handlers, flows, pull, pullSlotSettings{shrinkWindow: shrinkWindow}, logger)
}

// startAdaptiveSlotWorkerWithSettings starts a Worker with both test Flows and test-only slot settings.
func startAdaptiveSlotWorkerWithSettings(t *testing.T, address string, handlers *adaptiveSlotHandlers, pull PullWorkerOptions, settings pullSlotSettings) *adaptiveSlotWorkerRun {
	t.Helper()
	flows := []Flow{&adaptiveSlotFlow{handlers: handlers}, &adaptiveSlotParkedFlow{handlers: handlers}}
	return startAdaptiveSlotWorkerWithFlows(t, address, handlers, flows, pull, settings, nil)
}

// startAdaptiveSlotParkedOnlyWorker serves only the parked Flow, so it never takes the other Flow's Steps.
func startAdaptiveSlotParkedOnlyWorker(t *testing.T, address string, handlers *adaptiveSlotHandlers, pull PullWorkerOptions) *adaptiveSlotWorkerRun {
	t.Helper()
	return startAdaptiveSlotWorkerWithFlows(t, address, handlers, []Flow{&adaptiveSlotParkedFlow{handlers: handlers}}, pull, pullSlotSettings{shrinkWindow: time.Hour}, nil)
}

func startAdaptiveSlotWorkerWithFlows(t *testing.T, address string, handlers *adaptiveSlotHandlers, flows []Flow, pull PullWorkerOptions, settings pullSlotSettings, logger Logger) *adaptiveSlotWorkerRun {
	t.Helper()
	registry, err := NewRegistry(flows)
	require.NoError(t, err)
	cache, err := blobcache.New(&blobcache.Config{Dir: t.TempDir(), MaxBytes: 1 << 20})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close()) })
	worker, err := newWorker(registry, cache, WorkerOptions{FlowServiceAddress: address, Pull: &pull, Logger: logger}, settings)
	require.NoError(t, err)
	run := &adaptiveSlotWorkerRun{worker: worker, started: make(chan error, 1)}
	go func() { run.started <- worker.Start() }()
	t.Cleanup(func() {
		// A failed test may leave handlers that ignore cancellation; Stop waits for every handler.
		handlers.releaseAllStubborn()
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := worker.Stop(stopCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("stop Worker: %v", err)
		}
		if !run.hasResult {
			run.result = <-run.started
		}
	})
	return run
}

func (run *adaptiveSlotWorkerRun) awaitStartResult(t *testing.T, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-run.started:
		run.result, run.hasResult = err, true
		return err
	case <-time.After(timeout):
		t.Fatal("Start did not return")
		return nil
	}
}

func startAdaptiveSlotFlows(t *testing.T, ctx context.Context, client *Client, flow *adaptiveSlotFlow, mode string, count int) []string {
	t.Helper()
	flowIDs := make([]string, count)
	for index := range flowIDs {
		flowIDs[index] = "adaptive-slot-" + uuid.NewString()
		_, err := client.StartFlow(ctx, flow, flowIDs[index], adaptiveSlotInput{Mode: mode}, StartFlowOptions{})
		require.NoError(t, err)
	}
	return flowIDs
}

func startAdaptiveSlotFlowsConcurrently(t *testing.T, ctx context.Context, client *Client, flow *adaptiveSlotFlow, mode string, count int) []string {
	t.Helper()
	flowIDs := make([]string, count)
	startErrors := make(chan error, count)
	var wg sync.WaitGroup
	for index := range flowIDs {
		flowIDs[index] = "adaptive-slot-" + uuid.NewString()
		wg.Add(1)
		go func(flowID string) {
			defer wg.Done()
			_, err := client.StartFlow(ctx, flow, flowID, adaptiveSlotInput{Mode: mode}, StartFlowOptions{})
			startErrors <- err
		}(flowIDs[index])
	}
	wg.Wait()
	close(startErrors)
	for err := range startErrors {
		require.NoError(t, err)
	}
	return flowIDs
}

// startAdaptiveSlotTrickle starts a quick Flow every 100 ms until ctx ends and returns their IDs.
func startAdaptiveSlotTrickle(t *testing.T, ctx context.Context, client *Client, flow *adaptiveSlotFlow) []string {
	var flowIDs []string
	for ctx.Err() == nil {
		flowID := "adaptive-trickle-" + uuid.NewString()
		_, err := client.StartFlow(ctx, flow, flowID, adaptiveSlotInput{Mode: adaptiveSlotModeQuick}, StartFlowOptions{})
		if err != nil {
			if ctx.Err() == nil {
				t.Errorf("start trickle Flow: %v", err)
			}
			break
		}
		flowIDs = append(flowIDs, flowID)
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	return flowIDs
}

func awaitAdaptiveSlotEntries(t *testing.T, handlers *adaptiveSlotHandlers, count int) []adaptiveSlotEntry {
	t.Helper()
	entries := make([]adaptiveSlotEntry, 0, count)
	timeout := time.After(60 * time.Second)
	for len(entries) < count {
		select {
		case entry := <-handlers.entered:
			entries = append(entries, entry)
		case <-timeout:
			t.Fatalf("only %d of %d handlers started", len(entries), count)
		}
	}
	return entries
}

func drainAdaptiveSlotEntries(handlers *adaptiveSlotHandlers) []adaptiveSlotEntry {
	var entries []adaptiveSlotEntry
	for {
		select {
		case entry := <-handlers.entered:
			entries = append(entries, entry)
		default:
			return entries
		}
	}
}

func requireAdaptiveSlotFlowsCompleted(t *testing.T, ctx context.Context, client *Client, flowIDs []string) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for _, flowID := range flowIDs {
		result, err := client.WaitForFlow(waitCtx, flowID, WaitForFlowOptions{})
		require.NoError(t, err, "wait for Flow %s", flowID)
		require.Equal(t, FlowCompleted, result.Status, "Flow %s", flowID)
	}
}

func newAdaptiveSlotLogger() *adaptiveSlotLogger {
	return &adaptiveSlotLogger{Logger: slog.Default()}
}

func (logger *adaptiveSlotLogger) Warn(message string, keyvals ...interface{}) {
	logger.mu.Lock()
	logger.warnings = append(logger.warnings, fmt.Sprintln(append([]interface{}{message}, keyvals...)...))
	logger.mu.Unlock()
	logger.Logger.Warn(message, keyvals...)
}

func (logger *adaptiveSlotLogger) Info(message string, keyvals ...interface{}) {
	logger.mu.Lock()
	logger.infos = append(logger.infos, fmt.Sprintln(append([]interface{}{message}, keyvals...)...))
	logger.mu.Unlock()
	logger.Logger.Info(message, keyvals...)
}

// hasWarning reports whether one WARN message contains every fragment.
func (logger *adaptiveSlotLogger) hasWarning(fragments ...string) bool {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	for _, warning := range logger.warnings {
		hasAll := true
		for _, fragment := range fragments {
			if !strings.Contains(warning, fragment) {
				hasAll = false
				break
			}
		}
		if hasAll {
			return true
		}
	}
	return false
}

func (logger *adaptiveSlotLogger) countWarnings(fragment string) int {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	count := 0
	for _, warning := range logger.warnings {
		if strings.Contains(warning, fragment) {
			count++
		}
	}
	return count
}
