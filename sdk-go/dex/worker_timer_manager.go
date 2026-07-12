package dex

import (
	"sync"
	"sync/atomic"
	"time"
)

// timerManager is the per-run server-aligned clock authority. It
// owns two related capabilities, both rooted in the same
// constructor-time skew observation:
//
//  1. Multiplex N dynamically-scheduled wall-clock timers into a
//     single channel (run's main select can only listen on a fixed
//     number of channels).
//  2. Expose Now() — the current SERVER-aligned wall-clock in ms —
//     for callers like EvaluateCondition that compare against
//     pb.TimerCondition.fire_at_unix_ms.
//
// Both capabilities share the same frame of reference: timers fire
// at server-aligned wall-clock, and Now() returns server-aligned
// wall-clock, so the consumer can do a pure clock check without any
// firedTimers bookkeeping.
//
// Skew compensation
//
// At run dispatch we know two clocks:
//   - PollForRunResponse.server_timestamp_ms — the engine's wall-clock at
//     the moment the run was handed off to us
//   - time.Now() — this worker's local wall-clock at receipt
//
// The constructor captures both into (serverEpochMs, localEpoch).
// Every subsequent operation derives from these:
//
//   - Now() = serverEpochMs + time.Since(localEpoch).Milliseconds()
//   - Add()'s arming delta uses skew = max(0, serverEpochMs -
//     localEpoch.UnixMilli()) so a server-aligned fireAt fires at
//     the right local instant.
//
// If the worker's local clock is BEHIND the server's, skew is
// positive and Add subtracts it from each arming delta, so timers
// fire when SERVER-time reaches their fireAt. This protects against
// the dangerous case where the engine's durable timer would fire
// (and the engine would re-dispatch the run, transition statuses,
// etc.) BEFORE the worker's local AfterFunc fires — leaving the
// worker holding a Running run whose conditions the engine thinks
// have been satisfied.
//
// When the worker is AHEAD of the server we don't delay (skew is
// clamped to 0 for arming). Worker firing slightly early just means
// an extra reEvaluateWaitingSteps call that happens to find the
// timer's fire_at_unix_ms still in the (server-aligned) future and
// treat the timer as un-fired; the engine's durable timer covers
// the authoritative transition on that side. (Note: Now() does NOT
// clamp — it returns the unclamped server-aligned time, since
// worker-ahead is informational rather than dangerous for clock
// queries.)
//
// Lifecycle
//
//   - newTimerManager(pr.ServerTimestampMs) at processRun entry.
//   - Add(fireAt time.Time) arms one fire. There is no per-timer
//     cancel: spurious fires for steps that have already left
//     waitingSteps (sibling cancellation / promotion) are
//     correctness-safe — reEvaluateWaitingSteps filters by waitingSteps,
//     so a fire whose owning step is gone just triggers a cheap
//     re-eval over the current wait set.
//   - GetTimerFiredEvents() returns the read channel for the main
//     select. The emitted value is the original fireAt; the consumer
//     ignores it and just re-evaluates all waiting steps, since
//     EvaluateCondition is pure and a wholesale re-eval against the
//     current clock is the cheapest correct treatment.
//   - Now() returns server-aligned wall-clock for EvaluateCondition.
//   - Stop() at processRun exit cancels all pending timers (defer it).
//
// Drop semantics
//
// The output channel is buffered. On overflow the manager drops the
// fire (select-default). This is correctness-safe because the consumer
// always re-evaluates against the current clock — a missed fire just
// means the next consumed fire (or the next non-timer event) drives
// the same re-eval that would have happened, and EvaluateCondition
// reads server-aligned wall-clock directly through Now().
type timerManager struct {
	// serverEpochMs is the server's wall-clock at constructor time
	// (= pollResponse.ServerTimestampMs). Combined with localEpoch
	// it pins the local→server time mapping for the run's lifetime.
	serverEpochMs int64

	// localEpoch is the worker's wall-clock at constructor time.
	// time.Time carries Go's monotonic clock, so time.Since(localEpoch)
	// is robust against NTP / wall-clock adjustments while the run runs.
	localEpoch time.Time

	// armSkew is the precomputed max(0, serverEpochMs -
	// localEpoch.UnixMilli()) used by Add. Stored as a Duration so
	// the hot Add path doesn't re-clamp / re-multiply each call.
	// Always >= 0.
	armSkew time.Duration

	// out is the buffered fan-in channel of fired timestamps. Buffer
	// size of 16 absorbs simultaneous fires across multiple waiting
	// steps without stalling the runtime's timer goroutine; on
	// overflow we drop (see Drop semantics in file doc).
	out chan time.Time

	mu     sync.Mutex
	closed bool
	timers map[uint64]*time.Timer
	nextID atomic.Uint64
}

// newTimerManager constructs a manager aligned to the engine's
// wall-clock at run dispatch. serverNowMs is the value of
// PollForRunResponse.server_timestamp_ms; it is the ONLY clock-skew
// observation we ever make for this run. (Every subsequent timer is
// scheduled in absolute server-time via fireAt and the same
// constructor-time skew, so worker clock drift over the lifetime of
// the run does not silently corrupt fire timing.)
func newTimerManager(serverNowMs int64) *timerManager {
	localEpoch := time.Now()
	armSkew := time.Duration(serverNowMs-localEpoch.UnixMilli()) * time.Millisecond
	if armSkew < 0 {
		armSkew = 0
	}
	return &timerManager{
		serverEpochMs: serverNowMs,
		localEpoch:    localEpoch,
		armSkew:       armSkew,
		out:           make(chan time.Time, 16),
		timers:        map[uint64]*time.Timer{},
	}
}

// GetTimerFiredEvents returns the read-only channel emitting one
// fireAt per fired timer. The consumer is expected to use the value
// as a "kick to re-evaluate" rather than acting on the timestamp
// itself; EvaluateCondition reads the current server-aligned clock
// via Now() directly.
func (tm *timerManager) GetTimerFiredEvents() <-chan time.Time { return tm.out }

// Now returns the current SERVER-aligned wall-clock in milliseconds,
// computed from constructor-time (serverEpochMs, localEpoch) plus
// the monotonic elapsed time since localEpoch. EvaluateCondition
// uses this value for TimerCondition fire-at comparisons, so it
// shares a frame with both the engine's durable timer and Add()'s
// arming math.
func (tm *timerManager) Now() int64 {
	return tm.serverEpochMs + time.Since(tm.localEpoch).Milliseconds()
}

// Add schedules a fire at fireAt (a server-aligned wall-clock time
// — typically pb.TimerCondition.fire_at_unix_ms re-wrapped via
// time.UnixMilli). Add after Stop is a no-op.
//
// If fireAt is already due (after skew compensation) the manager
// emits immediately on a best-effort basis (select-default). See
// "Drop semantics" in the file doc.
func (tm *timerManager) Add(fireAt time.Time) {
	armDelta := time.Until(fireAt) - tm.armSkew
	if armDelta <= 0 {
		select {
		case tm.out <- fireAt:
		default:
		}
		return
	}

	id := tm.nextID.Add(1)
	tm.mu.Lock()
	if tm.closed {
		tm.mu.Unlock()
		return
	}
	t := time.AfterFunc(armDelta, func() {
		select {
		case tm.out <- fireAt:
		default:
		}
		tm.mu.Lock()
		delete(tm.timers, id)
		tm.mu.Unlock()
	})
	tm.timers[id] = t
	tm.mu.Unlock()
}

// Stop cancels all pending timers and prevents further Add calls
// from arming new ones. Idempotent. Typical use:
//
//	tm := newTimerManager(pr.ServerTimestampMs)
//	defer tm.Stop()
func (tm *timerManager) Stop() {
	tm.mu.Lock()
	if tm.closed {
		tm.mu.Unlock()
		return
	}
	tm.closed = true
	for _, t := range tm.timers {
		t.Stop()
	}
	tm.timers = nil
	tm.mu.Unlock()
}
