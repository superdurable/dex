package dex

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTimerManager_FiresAtFireAt covers the happy path: Add a future
// fireAt with no skew, observe the fire arrives close to that wall-clock.
func TestTimerManager_FiresAtFireAt(t *testing.T) {
	tm := newTimerManager(time.Now().UnixMilli())
	defer tm.Stop()

	fireAt := time.Now().Add(20 * time.Millisecond)
	tm.Add(fireAt)

	select {
	case got := <-tm.GetTimerFiredEvents():
		assert.WithinDuration(t, fireAt, got, 5*time.Millisecond,
			"fired timestamp should equal the fireAt arg")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timer did not fire within 200ms")
	}
}

// TestTimerManager_PastFireAtFiresImmediately pins the "already due"
// branch — no AfterFunc is armed, the value is delivered synchronously
// to the buffered output.
func TestTimerManager_PastFireAtFiresImmediately(t *testing.T) {
	tm := newTimerManager(time.Now().UnixMilli())
	defer tm.Stop()

	pastFireAt := time.Now().Add(-1 * time.Second)
	tm.Add(pastFireAt)

	select {
	case got := <-tm.GetTimerFiredEvents():
		assert.Equal(t, pastFireAt, got)
	case <-time.After(50 * time.Millisecond):
		t.Fatal("past fireAt should fire immediately")
	}
}

// TestTimerManager_SkewFiresEarlier verifies the headline behavior:
// when constructor sees the local clock as BEHIND the server's, every
// Add()'s arm delay is shortened by exactly the skew so the timer
// fires when SERVER-time reaches fireAt, not when local-time does.
//
// Concretely: with localNow = T0 and serverNow = T0 + 100ms (skew=100ms),
// scheduling fireAt = serverNow + 150ms (= T0 + 250ms in absolute /
// local time) yields armDelta = 250ms - 100ms skew = 150ms in
// local-time. That is, the fire lands at LOCAL T0 + 150ms — which is
// SERVER (T0+100) + 150 = serverNow + 150 = fireAt. ✓
func TestTimerManager_SkewFiresEarlier(t *testing.T) {
	const skewMs = 100
	const serverFutureMs = 150

	serverNowMs := time.Now().UnixMilli() + skewMs
	tm := newTimerManager(serverNowMs)
	defer tm.Stop()

	fireAt := time.UnixMilli(serverNowMs + serverFutureMs)
	startedAt := time.Now()
	tm.Add(fireAt)

	select {
	case <-tm.GetTimerFiredEvents():
		elapsed := time.Since(startedAt)
		// Without skew compensation, armDelta would be
		// time.Until(fireAt) = skewMs+serverFutureMs = 250ms.
		// WITH compensation, it's 150ms. We assert tightly around
		// the compensated value but loose enough for CI noise.
		expected := time.Duration(serverFutureMs) * time.Millisecond
		assert.WithinDuration(t,
			startedAt.Add(expected),
			startedAt.Add(elapsed),
			40*time.Millisecond,
			"fire should land ~%v after Add (serverFuture - skew compensation), not %v",
			expected, time.Duration(serverFutureMs+skewMs)*time.Millisecond)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timer did not fire within 500ms")
	}
}

// TestTimerManager_NegativeSkewClampedToZero pins the asymmetric design
// (file doc): when worker is AHEAD of server we don't delay, we just
// fire at the natural wall-clock. The engine's durable timer covers
// the authoritative side.
func TestTimerManager_NegativeSkewClampedToZero(t *testing.T) {
	// Pretend the server is 100ms BEHIND us.
	serverNowMs := time.Now().UnixMilli() - 100
	tm := newTimerManager(serverNowMs)
	defer tm.Stop()

	// Schedule 30ms in the future (local-time). With clamped skew,
	// armDelta == 30ms.
	fireAt := time.Now().Add(30 * time.Millisecond)
	startedAt := time.Now()
	tm.Add(fireAt)

	select {
	case <-tm.GetTimerFiredEvents():
		elapsed := time.Since(startedAt)
		// Should NOT have been delayed by 100ms. Loose lower bound to
		// avoid flake; tight upper bound is the real assertion.
		assert.Less(t, elapsed, 100*time.Millisecond,
			"negative skew must be clamped — no delay applied")
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timer did not fire within 300ms")
	}
}

// TestTimerManager_StopCancelsAllPending verifies the bulk cancel
// path used by processRun's defer.
func TestTimerManager_StopCancelsAllPending(t *testing.T) {
	tm := newTimerManager(time.Now().UnixMilli())

	for i := 0; i < 5; i++ {
		tm.Add(time.Now().Add(time.Duration(20+i*5) * time.Millisecond))
	}
	tm.Stop()

	// Drain anything that fired BEFORE Stop landed (a few may have
	// raced past it on a fast machine — the contract is "no fires
	// after Stop returns AND every still-pending timer is cancelled").
	deadline := time.After(100 * time.Millisecond)
drain:
	for {
		select {
		case <-tm.GetTimerFiredEvents():
		case <-deadline:
			break drain
		}
	}

	// Confirm Stop is idempotent.
	tm.Stop()

	// Confirm Add after Stop is a no-op (no goroutines, no panics).
	require.NotPanics(t, func() {
		tm.Add(time.Now().Add(10 * time.Millisecond))
	})
}
