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
	"fmt"
	"sort"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PullSlotStreamState is a snapshot of a pull Worker's slot streams.
//
// Obtain it with Worker.PullSlotStreamState, including concurrently with Start,
// Stop, and running handlers. It is an independent copy; changing it does not
// change the Worker. Every count is a number of slot streams or handlers, in the
// same unit as PullWorkerOptions.MaxConcurrentTasks.
//
// Open, Registering, Busy, and Idle count Step-and-RPC streams only, the streams
// that accept both Steps and RPCs. RPCOnlyOpen counts the reserved RPC-only streams.
// While every stream is registered and no handler outlives its stream, an adaptive Worker,
// whose PullWorkerOptions.MinOpenSlots is below MaxConcurrentTasks, keeps Open between
// Floor and EffectiveCeiling, and a fixed Worker keeps Open equal to EffectiveCeiling.
// Reconnects, orphaned handlers, and a lowered Server cap move Open outside that range
// until the Worker converges again.
//
// Open of zero with OrphanedHandlers above zero means that the Worker holds no live
// Step-and-RPC stream and waits for running handlers to return, for example after a
// Server restart. PullRegistrationState does not report that gap, because readiness
// stays latched across reconnects.
//
//	state := worker.PullSlotStreamState()
//	log.Printf("open=%d busy=%d idle=%d orphaned=%d capacity=%d",
//		state.Open, state.Busy, state.Idle, state.OrphanedHandlers, state.TotalCapacity)
//
// Since 1.45.7.
type PullSlotStreamState struct {
	// IsAdaptive reports whether the Worker varies its open Step-and-RPC streams,
	// which is when MinOpenSlots is below MaxConcurrentTasks.
	IsAdaptive bool
	// Open counts the Step-and-RPC streams that are registered or registering and not closing.
	Open int
	// Registering counts the Step-and-RPC streams that wait for the Server's acknowledgement,
	// including a reconnect that waits for its backoff.
	Registering int
	// Busy counts the Step-and-RPC streams that run a task.
	Busy int
	// Idle counts the registered Step-and-RPC streams that wait for a task.
	Idle int
	// OrphanedHandlers counts Step-and-RPC handlers that still run after their stream ended.
	// Each one keeps its slot, so the Worker does not register a stream for it until it returns.
	OrphanedHandlers int
	// OrphanedRPCOnlyHandlers counts RPC-only handlers that still run after their stream ended.
	OrphanedRPCOnlyHandlers int
	// Floor is the number of Step-and-RPC streams the Worker keeps open at rest:
	// MinOpenSlots minus the reserved RPC slots, limited by EffectiveCeiling.
	Floor int
	// EffectiveCeiling is the most Step-and-RPC streams and orphaned handlers the Worker may hold:
	// TotalCapacity minus the RPC-only slots, or minus the RPC-only streams and handlers when they exceed them.
	EffectiveCeiling int
	// GrowthTarget is the Step-and-RPC stream count that growth may reach. It equals
	// EffectiveCeiling, or less for the rest of a shrink window after the Server rejected
	// a growth stream at its stream limit.
	GrowthTarget int
	// TotalCapacity is the Worker's slot count: MaxConcurrentTasks, limited by the
	// per-Worker cap that the Server reported in its last acknowledgement.
	TotalCapacity int
	// RPCOnlyOpen counts the RPC-only streams that are registered or registering and not closing.
	RPCOnlyOpen int
	// RPCOnlySlots is the number of RPC-only streams the Worker keeps: ReservedRPCTasks,
	// or fewer when the Server cap leaves no room for them next to one Step-and-RPC stream.
	RPCOnlySlots int
	// StreamsGrown counts the Step-and-RPC streams opened by growth since the Worker started.
	StreamsGrown uint64
	// StreamsShrunk counts the streams that the Worker half-closed because it had more than it needed.
	StreamsShrunk uint64
	// GrowRejectedAtCap counts the registrations that the Server rejected with RESOURCE_EXHAUSTED
	// and that the adaptive Worker treated as a soft limit instead of a permanent rejection.
	GrowRejectedAtCap uint64
	// ShrinkRaceDrops counts the tasks that arrived on a stream after the Worker decided to close it.
	// The Server returns each such task as not delivered and dispatches it again without spending a retry.
	ShrinkRaceDrops uint64
}

// pullSlotStreams holds the slot stream state machine of one pull Worker; worker.mu guards it.
type pullSlotStreams struct {
	// totalCapacity is C, from the last acknowledgement.
	totalCapacity int
	// rpcOnlySlots is R_eff.
	rpcOnlySlots int
	// stepFloor is L, MinOpenSlots minus R.
	stepFloor int
	// effectiveFloor is L_eff.
	effectiveFloor int
	// effectiveCeiling is E.
	effectiveCeiling int
	// growthTarget is G, never above effectiveCeiling.
	growthTarget            int
	stepPhaseCounts         [pullSlotPhaseCount]int
	rpcOnlyPhaseCounts      [pullSlotPhaseCount]int
	orphanedStepHandlers    int
	orphanedRPCOnlyHandlers int
	isGrowthTargetClamped   bool
	hasGrownThisWindow      bool
	// isReopenDeferred is the retry latch that a soft rejection sets.
	isReopenDeferred       bool
	hasBatchRejection      bool
	peakBusy               int
	nextIdleSequence       uint64
	shrinkWindowTimer      *time.Timer
	shrinkWindowGeneration uint64
	streamsGrown           uint64
	streamsShrunk          uint64
	growRejectedAtCap      uint64
	shrinkRaceDrops        uint64
}

// pullSlotTimings holds the slot stream timings; only tests change them.
type pullSlotTimings struct {
	shrinkWindow time.Duration
}

// pullSlotAttempt is one registration attempt of a slot stream; worker.mu guards it.
type pullSlotAttempt struct {
	phase                  pullSlotPhase
	idleSequence           uint64
	isGrowth               bool
	isOpenedAfterReadiness bool
	hasEnded               bool
	hasReceivedRegistered  bool
	// isRegisterSent is set when the attempt passes its last capacity check; it may then reach the Server.
	isRegisterSent    bool
	isOrphaned        bool
	isClosedOnPurpose bool
	isSoftRejected    bool
	isReopenGranted   bool
}

// pullSlotPhase is the phase of one attempt; untracked attempts count in no total.
type pullSlotPhase uint8

const (
	pullSlotPhaseUntracked pullSlotPhase = iota
	pullSlotPhaseRegistering
	pullSlotPhaseIdle
	pullSlotPhaseBusy
	// pullSlotPhaseReconnecting is a stream draining after the Server's Reconnect; it takes no task.
	pullSlotPhaseReconnecting
	pullSlotPhaseCount
)

// defaultPullShrinkWindow is W, the shrink window of decision D6.
const defaultPullShrinkWindow = 30 * time.Second

func defaultPullSlotTimings() pullSlotTimings {
	return pullSlotTimings{shrinkWindow: defaultPullShrinkWindow}
}

// defaultReservedRPCTasks keeps one RPC-only slot when the ceiling and the floor both leave room for it.
func defaultReservedRPCTasks(maxConcurrentTasks int, minOpenSlots int) int {
	if maxConcurrentTasks >= 2 && minOpenSlots >= 2 {
		return 1
	}
	return 0
}

// initializeSlotCapacityLocked sets C, R_eff, and L from the configuration before any acknowledgement.
func (worker *pullWorker) initializeSlotCapacityLocked() {
	worker.slots.totalCapacity = worker.maxConcurrentTasks
	worker.slots.rpcOnlySlots = worker.reservedRPCTasks
	worker.slots.stepFloor = worker.minOpenSlots - worker.reservedRPCTasks
	worker.recomputeSlotCapacityLocked()
}

// startSlotStreams opens the startup streams and starts the shrink window of an adaptive Worker.
func (worker *pullWorker) startSlotStreams() {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.isStopping {
		return
	}
	worker.restartShrinkWindowLocked()
	worker.maybeReopenSlotStreamsLocked(nil)
}

// reconcile re-evaluates the reopen rule; it opens nothing that the rule does not allow.
func (worker *pullWorker) reconcile() {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.isStopping {
		return
	}
	worker.maybeReopenSlotStreamsLocked(nil)
}

// resolveSlotCeilingAtFirstAcknowledgementLocked applies D4 and F6; it returns a start failure.
func (worker *pullWorker) resolveSlotCeilingAtFirstAcknowledgementLocked(limit uint32) error {
	hasLimit := limit != 0
	if !worker.isAdaptive && worker.isMaxConcurrentTasksExplicit {
		if hasLimit && uint64(worker.maxConcurrentTasks) > uint64(limit) {
			return fmt.Errorf("dex: MaxConcurrentTasks %d exceeds Server matching.maxConcurrentTasksCap %d", worker.maxConcurrentTasks, limit)
		}
		return nil
	}
	ceiling := worker.configuredMaxConcurrentTasks
	if hasLimit && uint64(ceiling) > uint64(limit) {
		ceiling = int(limit)
		worker.service.logger.Warn("dex: clamped MaxConcurrentTasks to the Server matching.maxConcurrentTasksCap",
			"maxConcurrentTasks", worker.configuredMaxConcurrentTasks, "maxConcurrentTasksPerAppWorker", limit,
			"isMaxConcurrentTasksExplicit", worker.isMaxConcurrentTasksExplicit)
	}
	minOpenSlots := worker.minOpenSlots
	if !worker.isMinOpenSlotsExplicit {
		minOpenSlots = ceiling
	}
	reservedRPCTasks := worker.reservedRPCTasks
	if !worker.isReservedRPCTasksExplicit {
		reservedRPCTasks = defaultReservedRPCTasks(ceiling, minOpenSlots)
	}
	if reservedRPCTasks >= ceiling {
		return fmt.Errorf("dex: ReservedRPCTasks %d must be less than the slot ceiling %d, which is MaxConcurrentTasks %d limited by Server matching.maxConcurrentTasksCap %d",
			reservedRPCTasks, ceiling, worker.configuredMaxConcurrentTasks, limit)
	}
	if minOpenSlots > ceiling {
		return fmt.Errorf("dex: MinOpenSlots %d exceeds the slot ceiling %d, which is MaxConcurrentTasks %d limited by Server matching.maxConcurrentTasksCap %d",
			minOpenSlots, ceiling, worker.configuredMaxConcurrentTasks, limit)
	}
	worker.maxConcurrentTasks = ceiling
	worker.minOpenSlots = minOpenSlots
	worker.reservedRPCTasks = reservedRPCTasks
	worker.slots.stepFloor = minOpenSlots - reservedRPCTasks
	worker.slots.totalCapacity = ceiling
	if reservedRPCTasks == 0 {
		worker.queues = []string{dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_STEP_AND_RPC.String()}
	}
	return nil
}

// recordSlotRegisteredLocked is the registered event of the state machine.
func (worker *pullWorker) recordSlotRegisteredLocked(stream *pullStream, limit uint32) {
	stream.slot.hasReceivedRegistered = true
	if stream.slot.phase == pullSlotPhaseRegistering {
		worker.setSlotPhaseLocked(stream, pullSlotPhaseIdle)
	}
	if worker.isAdaptive {
		capacity := worker.maxConcurrentTasks
		if limit != 0 && uint64(limit) < uint64(capacity) {
			capacity = int(limit)
		}
		worker.slots.totalCapacity = capacity
	}
	rpcOnlySlots := worker.reservedRPCTasks
	if rpcOnlySlots+1 > worker.slots.totalCapacity {
		rpcOnlySlots = max(0, worker.slots.totalCapacity-1)
	}
	if rpcOnlySlots < worker.slots.rpcOnlySlots {
		worker.service.logger.Warn("dex: Server matching.maxConcurrentTasksCap leaves fewer RPC-only slots than ReservedRPCTasks",
			"reservedRPCTasks", worker.reservedRPCTasks, "rpcOnlySlots", rpcOnlySlots, "maxConcurrentTasksPerAppWorker", limit)
	}
	worker.slots.rpcOnlySlots = rpcOnlySlots
	worker.closeSurplusSlotStreamsLocked()
	worker.recomputeSlotCapacityLocked()
	worker.maybeReopenSlotStreamsLocked(nil)
	if worker.slots.stepPhaseCounts[pullSlotPhaseRegistering] == 0 {
		worker.settleGrowthBatchLocked()
	}
}

// recordSlotTaskStartedLocked is the task_frame event.
func (worker *pullWorker) recordSlotTaskStartedLocked(stream *pullStream) {
	if stream.slot.phase == pullSlotPhaseUntracked {
		return
	}
	worker.setSlotPhaseLocked(stream, pullSlotPhaseBusy)
	if stream.slotKind == dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_RPC_ONLY {
		return
	}
	worker.slots.peakBusy = max(worker.slots.peakBusy, worker.slots.stepPhaseCounts[pullSlotPhaseBusy])
	worker.maybeGrowSlotStreamsLocked()
}

// recordSlotTaskDroppedLocked counts a task dropped on a stream that the Worker chose to close.
func (worker *pullWorker) recordSlotTaskDroppedLocked(stream *pullStream) {
	if stream.slot.isClosedOnPurpose && !worker.isStopping {
		worker.slots.shrinkRaceDrops++
	}
}

// recordSlotReconnectRequestedLocked keeps a stream that the Server asked to reconnect out of the idle set.
func (worker *pullWorker) recordSlotReconnectRequestedLocked(stream *pullStream) {
	if stream.slot.phase == pullSlotPhaseIdle {
		worker.setSlotPhaseLocked(stream, pullSlotPhaseReconnecting)
	}
}

// recordSlotTaskCompletedLocked is final_frame_sent, or orphaned_handler_returned after the stream ended.
// It reports whether the stream must half-close because it is surplus.
func (worker *pullWorker) recordSlotTaskCompletedLocked(stream *pullStream, isDraining bool) bool {
	if stream.slot.isOrphaned {
		stream.slot.isOrphaned = false
		if stream.slotKind == dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_RPC_ONLY {
			worker.slots.orphanedRPCOnlyHandlers--
		} else {
			worker.slots.orphanedStepHandlers--
		}
		worker.recomputeSlotCapacityLocked()
		worker.maybeReopenSlotStreamsLocked(worker.reopenCandidateLocked(stream))
		return false
	}
	if stream.slot.phase != pullSlotPhaseBusy {
		return false
	}
	if isDraining {
		worker.setSlotPhaseLocked(stream, pullSlotPhaseReconnecting)
		return false
	}
	worker.setSlotPhaseLocked(stream, pullSlotPhaseIdle)
	isSurplus := false
	if stream.slotKind == dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_RPC_ONLY {
		isSurplus = worker.rpcOnlySlotUsageLocked() > worker.slots.rpcOnlySlots
	} else {
		isSurplus = worker.stepSlotUsageLocked() > max(0, worker.slots.totalCapacity-worker.slots.rpcOnlySlots)
	}
	if !isSurplus {
		return false
	}
	worker.halfCloseSlotStreamLocked(stream, false)
	worker.slots.streamsShrunk++
	worker.recomputeSlotCapacityLocked()
	worker.maybeReopenSlotStreamsLocked(nil)
	return true
}

// isSoftSlotRejectionLocked applies F5: only an adaptive Worker softens RESOURCE_EXHAUSTED,
// and only for a growth stream or a reconnect after initial readiness that never registered.
func (worker *pullWorker) isSoftSlotRejectionLocked(stream *pullStream, err error) bool {
	if !worker.isAdaptive || status.Code(err) != codes.ResourceExhausted || stream.slot.hasReceivedRegistered {
		return false
	}
	if stream.slot.phase != pullSlotPhaseRegistering {
		return false
	}
	return stream.slot.isGrowth || stream.slot.isOpenedAfterReadiness
}

// recordSlotRegistrationRejectedLocked is the soft registration_rejected event.
func (worker *pullWorker) recordSlotRegistrationRejectedLocked(stream *pullStream) {
	stream.slot.isSoftRejected = true
	worker.setSlotPhaseLocked(stream, pullSlotPhaseUntracked)
	if stream.slot.isGrowth {
		worker.slots.hasBatchRejection = true
	}
	worker.slots.growRejectedAtCap++
	worker.slots.isReopenDeferred = true
	worker.recomputeSlotCapacityLocked()
	if worker.slots.stepPhaseCounts[pullSlotPhaseRegistering] == 0 {
		worker.settleGrowthBatchLocked()
	}
}

// recordSlotStreamEndedLocked is stream_ended; every end of an attempt passes here once.
func (worker *pullWorker) recordSlotStreamEndedLocked(stream *pullStream) {
	if stream.slot.hasEnded {
		return
	}
	stream.slot.hasEnded = true
	if stream.slot.phase != pullSlotPhaseUntracked {
		if stream.slot.phase == pullSlotPhaseBusy {
			stream.slot.isOrphaned = true
			if stream.slotKind == dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_RPC_ONLY {
				worker.slots.orphanedRPCOnlyHandlers++
			} else {
				worker.slots.orphanedStepHandlers++
			}
		}
		worker.setSlotPhaseLocked(stream, pullSlotPhaseUntracked)
	}
	if stream.slot.hasReceivedRegistered {
		worker.slots.isReopenDeferred = false
	}
	worker.recomputeSlotCapacityLocked()
	var parked *pullStream
	if !stream.slot.isOrphaned {
		parked = worker.reopenCandidateLocked(stream)
	}
	worker.maybeReopenSlotStreamsLocked(parked)
	worker.maybeGrowSlotStreamsLocked()
	if worker.slots.stepPhaseCounts[pullSlotPhaseRegistering] == 0 {
		worker.settleGrowthBatchLocked()
	}
}

// abandonSlotAttemptLocked ends an attempt whose runner exits before the attempt ends.
func (worker *pullWorker) abandonSlotAttemptLocked(stream *pullStream) {
	if stream.slot.phase == pullSlotPhaseUntracked || stream.slot.hasEnded {
		return
	}
	stream.slot.isClosedOnPurpose = true
	worker.recordSlotStreamEndedLocked(stream)
}

// expireShrinkWindow runs the every-W step of the state machine for one window generation.
func (worker *pullWorker) expireShrinkWindow(generation uint64) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.isStopping || generation != worker.slots.shrinkWindowGeneration {
		return
	}
	worker.restartShrinkWindowLocked()
	worker.slots.isGrowthTargetClamped = false
	worker.slots.hasGrownThisWindow = false
	worker.slots.isReopenDeferred = false
	worker.recomputeSlotCapacityLocked()
	worker.maybeReopenSlotStreamsLocked(nil)
	worker.shrinkSlotStreamsLocked()
	worker.slots.peakBusy = worker.slots.stepPhaseCounts[pullSlotPhaseBusy]
	worker.maybeGrowSlotStreamsLocked()
}

// shrinkSlotStreamsLocked closes half the idle surplus above the window's peak plus headroom.
func (worker *pullWorker) shrinkSlotStreamsLocked() {
	peakBusy := worker.slots.peakBusy
	headroom := max(1, (peakBusy+3)/4)
	target := min(worker.slots.effectiveCeiling, max(worker.slots.effectiveFloor, peakBusy+headroom))
	open := worker.stepSlotOpenLocked()
	if open <= target {
		return
	}
	closeCount := (open - target + 1) / 2
	idleStreams := worker.idleStepStreamsNewestFirstLocked()
	// The last idle stream stays open, and the floor stays open.
	closeCount = min(closeCount, len(idleStreams)-1, open-worker.slots.effectiveFloor)
	if closeCount <= 0 {
		return
	}
	for _, stream := range idleStreams[:closeCount] {
		worker.halfCloseSlotStreamLocked(stream, true)
	}
	worker.slots.streamsShrunk += uint64(closeCount)
	worker.service.logger.Debug("dex: shrink pull slot streams", "from", open, "to", open-closeCount, "peakBusy", peakBusy)
	worker.recomputeSlotCapacityLocked()
}

// closeSurplusSlotStreamsLocked releases capacity above a lowered cap; RPC-only surplus goes first.
// An attempt whose Register has not left yet is revoked first, because it costs the Server nothing.
func (worker *pullWorker) closeSurplusSlotStreamsLocked() {
	for _, slotKind := range []dexpb.PollWorkerSlotKind{dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_RPC_ONLY, dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_STEP_AND_RPC} {
		for worker.isSlotKindOverQuotaLocked(slotKind) {
			if pending := worker.unsentSlotAttemptLocked(slotKind); pending != nil {
				worker.revokeSlotAttemptLocked(pending)
				continue
			}
			stream := worker.newestIdleStreamLocked(slotKind)
			if stream == nil {
				break
			}
			worker.halfCloseSlotStreamLocked(stream, true)
			worker.slots.streamsShrunk++
		}
	}
}

// admitSlotRegistration is the last check before a Register leaves. An attempt granted before the
// Worker learned a lower cap is revoked when it no longer fits, so no Register exceeds a known cap (F5).
func (worker *pullWorker) admitSlotRegistration(stream *pullStream) bool {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.isStopping || stream.slot.phase != pullSlotPhaseRegistering || stream.slot.hasEnded {
		return false
	}
	if !worker.isSlotAttemptOverCapacityLocked(stream) {
		stream.slot.isRegisterSent = true
		return true
	}
	worker.revokeSlotAttemptLocked(stream)
	worker.recomputeSlotCapacityLocked()
	worker.maybeReopenSlotStreamsLocked(nil)
	if worker.slots.stepPhaseCounts[pullSlotPhaseRegistering] == 0 {
		worker.settleGrowthBatchLocked()
	}
	return false
}

// isSlotAttemptOverCapacityLocked checks one registering attempt, which the totals already count.
func (worker *pullWorker) isSlotAttemptOverCapacityLocked(stream *pullStream) bool {
	if worker.stepSlotUsageLocked()+worker.rpcOnlySlotUsageLocked() > worker.slots.totalCapacity {
		return true
	}
	if stream.slotKind == dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_RPC_ONLY {
		return worker.rpcOnlySlotUsageLocked() > worker.slots.rpcOnlySlots
	}
	// F5 gives the first Step-and-RPC stream any free slot, so only later ones need the Step quota.
	return worker.stepSlotOpenLocked() > 1 && worker.stepSlotUsageLocked() > worker.slots.effectiveCeiling
}

// isSlotKindOverQuotaLocked reports surplus of a kind after a lowered cap: RPC-only above R_eff,
// or Step-and-RPC above C - R_eff.
func (worker *pullWorker) isSlotKindOverQuotaLocked(slotKind dexpb.PollWorkerSlotKind) bool {
	if slotKind == dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_RPC_ONLY {
		return worker.rpcOnlySlotUsageLocked() > worker.slots.rpcOnlySlots
	}
	return worker.stepSlotUsageLocked() > max(0, worker.slots.totalCapacity-worker.slots.rpcOnlySlots)
}

// revokeSlotAttemptLocked takes back an attempt whose Register has not left; its runner then exits.
func (worker *pullWorker) revokeSlotAttemptLocked(stream *pullStream) {
	stream.slot.isClosedOnPurpose = true
	stream.slot.hasEnded = true
	worker.setSlotPhaseLocked(stream, pullSlotPhaseUntracked)
}

// settleGrowthBatchLocked runs when no stream registers; a rejected batch clamps G until the window ends.
func (worker *pullWorker) settleGrowthBatchLocked() {
	if worker.slots.hasBatchRejection {
		worker.slots.hasBatchRejection = false
		worker.slots.growthTarget = min(worker.slots.effectiveCeiling, worker.stepSlotUsageLocked())
		worker.slots.isGrowthTargetClamped = true
		worker.service.logger.Info("dex: the Server rejected pull slot stream growth at its stream limit; holding growth until the next shrink window",
			"growthTarget", worker.slots.growthTarget, "effectiveCeiling", worker.slots.effectiveCeiling)
	}
	worker.maybeGrowSlotStreamsLocked()
}

// maybeGrowSlotStreamsLocked doubles the open streams when a hand-out took the last idle one.
func (worker *pullWorker) maybeGrowSlotStreamsLocked() {
	if !worker.isAdaptive || worker.isStopping || worker.slots.isReopenDeferred {
		return
	}
	if worker.slots.isGrowthTargetClamped && worker.slots.hasGrownThisWindow {
		return
	}
	open := worker.stepSlotOpenLocked()
	usage := worker.stepSlotUsageLocked()
	if worker.slots.stepPhaseCounts[pullSlotPhaseIdle] != 0 || worker.slots.stepPhaseCounts[pullSlotPhaseRegistering] != 0 || usage >= worker.slots.growthTarget {
		return
	}
	count := min(worker.slots.growthTarget-usage, max(1, open))
	worker.service.logger.Debug("dex: grow pull slot streams", "from", open, "to", open+count, "reason", "no idle stream")
	for range count {
		worker.openSlotStreamLocked(dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_STEP_AND_RPC, true, nil)
	}
	worker.slots.streamsGrown += uint64(count)
	worker.slots.hasGrownThisWindow = true
	worker.restartShrinkWindowLocked()
}

// maybeReopenSlotStreamsLocked opens streams within free capacity in F5 order. A parked runner of
// the same kind is reused, so it keeps its reconnect backoff.
func (worker *pullWorker) maybeReopenSlotStreamsLocked(parked *pullStream) {
	if worker.isStopping || worker.slots.isReopenDeferred {
		return
	}
	stepOpen := worker.stepSlotOpenLocked()
	free := max(0, worker.slots.totalCapacity-(worker.stepSlotUsageLocked()+worker.rpcOnlySlotUsageLocked()))
	if stepOpen == 0 && free > 0 {
		worker.openSlotStreamLocked(dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_STEP_AND_RPC, false, parked)
		stepOpen++
		free--
	}
	if worker.isCapProbePendingLocked() {
		return
	}
	if worker.hasCheckedRegistrationLimit || !worker.isAdaptive {
		count := min(free, max(0, worker.slots.rpcOnlySlots-worker.rpcOnlySlotUsageLocked()))
		for range count {
			worker.openSlotStreamLocked(dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_RPC_ONLY, false, parked)
		}
		free -= count
	}
	count := min(free, max(0, min(worker.slots.effectiveFloor, worker.slots.effectiveCeiling-worker.slots.orphanedStepHandlers)-stepOpen))
	for range count {
		worker.openSlotStreamLocked(dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_STEP_AND_RPC, false, parked)
	}
}

// openSlotStreamLocked counts a new attempt as registering and grants it to parked when possible.
func (worker *pullWorker) openSlotStreamLocked(slotKind dexpb.PollWorkerSlotKind, isGrowth bool, parked *pullStream) {
	attempt := pullSlotAttempt{isGrowth: isGrowth, isOpenedAfterReadiness: worker.hasRegisteredAllQueues}
	if !isGrowth && parked != nil && parked.slotKind == slotKind && !parked.slot.isReopenGranted {
		attempt.isReopenGranted = true
		parked.slot = attempt
		worker.setSlotPhaseLocked(parked, pullSlotPhaseRegistering)
		return
	}
	streamCtx, cancel := context.WithCancel(worker.ctx)
	stream := &pullStream{worker: worker, queue: slotKind.String(), slotKind: slotKind, ctx: streamCtx, cancel: cancel, slot: attempt}
	worker.setSlotPhaseLocked(stream, pullSlotPhaseRegistering)
	worker.streams[stream] = struct{}{}
	worker.streamWG.Add(1)
	go stream.run()
}

// halfCloseSlotStreamLocked takes an idle stream out of the totals and half-closes it; its later end changes no count.
func (worker *pullWorker) halfCloseSlotStreamLocked(stream *pullStream, shouldCloseSendInBackground bool) {
	worker.setSlotPhaseLocked(stream, pullSlotPhaseUntracked)
	stream.slot.isClosedOnPurpose = true
	stream.mu.Lock()
	stream.isDraining = true
	stream.mu.Unlock()
	if shouldCloseSendInBackground {
		// The stream is still in worker.streams, so its own WaitGroup count keeps Add safe here.
		worker.streamWG.Add(1)
		go stream.closeSendForSlotRelease()
	}
}

// restartShrinkWindowLocked starts a new shrink window; an older window's timer then does nothing.
func (worker *pullWorker) restartShrinkWindowLocked() {
	if !worker.isAdaptive || worker.slotTimings.shrinkWindow <= 0 {
		return
	}
	worker.slots.shrinkWindowGeneration++
	generation := worker.slots.shrinkWindowGeneration
	if worker.slots.shrinkWindowTimer != nil {
		worker.slots.shrinkWindowTimer.Stop()
	}
	worker.slots.shrinkWindowTimer = time.AfterFunc(worker.slotTimings.shrinkWindow, func() { worker.expireShrinkWindow(generation) })
}

// stopShrinkWindowLocked stops the shrink window timer at Stop.
func (worker *pullWorker) stopShrinkWindowLocked() {
	worker.slots.shrinkWindowGeneration++
	if worker.slots.shrinkWindowTimer != nil {
		worker.slots.shrinkWindowTimer.Stop()
	}
}

// setSlotPhaseLocked moves an attempt between phases and keeps the per-phase totals.
func (worker *pullWorker) setSlotPhaseLocked(stream *pullStream, phase pullSlotPhase) {
	counts := &worker.slots.stepPhaseCounts
	if stream.slotKind == dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_RPC_ONLY {
		counts = &worker.slots.rpcOnlyPhaseCounts
	}
	if stream.slot.phase != pullSlotPhaseUntracked {
		counts[stream.slot.phase]--
	}
	stream.slot.phase = phase
	if phase != pullSlotPhaseUntracked {
		counts[phase]++
	}
	if phase == pullSlotPhaseIdle {
		worker.slots.nextIdleSequence++
		stream.slot.idleSequence = worker.slots.nextIdleSequence
	}
}

// recomputeSlotCapacityLocked derives E, L_eff, and G; RPC-only overflow takes Step capacity.
func (worker *pullWorker) recomputeSlotCapacityLocked() {
	rpcOnlyReserve := max(worker.slots.rpcOnlySlots, worker.rpcOnlySlotUsageLocked())
	worker.slots.effectiveCeiling = max(0, worker.slots.totalCapacity-rpcOnlyReserve)
	worker.slots.effectiveFloor = max(0, min(worker.slots.stepFloor, worker.slots.effectiveCeiling))
	if worker.slots.isGrowthTargetClamped {
		worker.slots.growthTarget = min(worker.slots.growthTarget, worker.slots.effectiveCeiling)
	} else {
		worker.slots.growthTarget = worker.slots.effectiveCeiling
	}
}

// reopenCandidateLocked returns stream when its runner may serve the next attempt of its kind.
func (worker *pullWorker) reopenCandidateLocked(stream *pullStream) *pullStream {
	if stream.slot.isClosedOnPurpose || stream.slot.isSoftRejected || stream.slot.isReopenGranted {
		return nil
	}
	return stream
}

// idleStepStreamsNewestFirstLocked lists idle Step-and-RPC streams, the most recently idle first.
func (worker *pullWorker) idleStepStreamsNewestFirstLocked() []*pullStream {
	idleStreams := make([]*pullStream, 0, worker.slots.stepPhaseCounts[pullSlotPhaseIdle])
	for stream := range worker.streams {
		if stream.slotKind != dexpb.PollWorkerSlotKind_POLL_WORKER_SLOT_KIND_RPC_ONLY && stream.slot.phase == pullSlotPhaseIdle {
			idleStreams = append(idleStreams, stream)
		}
	}
	sort.Slice(idleStreams, func(i, j int) bool { return idleStreams[i].slot.idleSequence > idleStreams[j].slot.idleSequence })
	return idleStreams
}

// unsentSlotAttemptLocked returns a registering attempt of the kind whose Register has not left, or nil.
func (worker *pullWorker) unsentSlotAttemptLocked(slotKind dexpb.PollWorkerSlotKind) *pullStream {
	for stream := range worker.streams {
		if stream.slotKind == slotKind && stream.slot.phase == pullSlotPhaseRegistering && !stream.slot.isRegisterSent {
			return stream
		}
	}
	return nil
}

func (worker *pullWorker) newestIdleStreamLocked(slotKind dexpb.PollWorkerSlotKind) *pullStream {
	var newest *pullStream
	for stream := range worker.streams {
		if stream.slotKind == slotKind && stream.slot.phase == pullSlotPhaseIdle && (newest == nil || stream.slot.idleSequence > newest.slot.idleSequence) {
			newest = stream
		}
	}
	return newest
}

// isCapProbePendingLocked reports F4: a fixed Worker with an unset ceiling opens one stream until the first acknowledgement.
func (worker *pullWorker) isCapProbePendingLocked() bool {
	return !worker.isAdaptive && !worker.isMaxConcurrentTasksExplicit && !worker.hasCheckedRegistrationLimit
}

// stepSlotOpenLocked returns S.
func (worker *pullWorker) stepSlotOpenLocked() int {
	counts := worker.slots.stepPhaseCounts
	return counts[pullSlotPhaseRegistering] + counts[pullSlotPhaseIdle] + counts[pullSlotPhaseBusy] + counts[pullSlotPhaseReconnecting]
}

// stepSlotUsageLocked returns S + H.
func (worker *pullWorker) stepSlotUsageLocked() int {
	return worker.stepSlotOpenLocked() + worker.slots.orphanedStepHandlers
}

// rpcOnlySlotOpenLocked returns Sr.
func (worker *pullWorker) rpcOnlySlotOpenLocked() int {
	counts := worker.slots.rpcOnlyPhaseCounts
	return counts[pullSlotPhaseRegistering] + counts[pullSlotPhaseIdle] + counts[pullSlotPhaseBusy] + counts[pullSlotPhaseReconnecting]
}

// rpcOnlySlotUsageLocked returns Sr + Hr.
func (worker *pullWorker) rpcOnlySlotUsageLocked() int {
	return worker.rpcOnlySlotOpenLocked() + worker.slots.orphanedRPCOnlyHandlers
}

func (worker *pullWorker) slotStreamState() PullSlotStreamState {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	counts := worker.slots.stepPhaseCounts
	return PullSlotStreamState{
		IsAdaptive:              worker.isAdaptive,
		Open:                    worker.stepSlotOpenLocked(),
		Registering:             counts[pullSlotPhaseRegistering],
		Busy:                    counts[pullSlotPhaseBusy],
		Idle:                    counts[pullSlotPhaseIdle],
		OrphanedHandlers:        worker.slots.orphanedStepHandlers,
		OrphanedRPCOnlyHandlers: worker.slots.orphanedRPCOnlyHandlers,
		Floor:                   worker.slots.effectiveFloor,
		EffectiveCeiling:        worker.slots.effectiveCeiling,
		GrowthTarget:            worker.slots.growthTarget,
		TotalCapacity:           worker.slots.totalCapacity,
		RPCOnlyOpen:             worker.rpcOnlySlotOpenLocked(),
		RPCOnlySlots:            worker.slots.rpcOnlySlots,
		StreamsGrown:            worker.slots.streamsGrown,
		StreamsShrunk:           worker.slots.streamsShrunk,
		GrowRejectedAtCap:       worker.slots.growRejectedAtCap,
		ShrinkRaceDrops:         worker.slots.shrinkRaceDrops,
	}
}
