package backoff

import (
	"context"
	"testing"
	"time"

	"github.com/superdurable/dex/server/common/utils/clock"
)

func TestRetryPolicy_DefaultValues(t *testing.T) {
	p := &RetryPolicy{}
	if got := p.getInitialInterval(); got != defaultInitialInterval {
		t.Errorf("getInitialInterval() = %v, want %v", got, defaultInitialInterval)
	}
	if got := p.getBackoffCoefficient(); got != defaultBackoffCoefficient {
		t.Errorf("getBackoffCoefficient() = %v, want %v", got, defaultBackoffCoefficient)
	}
	if got := p.getMaximumInterval(); got != defaultMaximumInterval {
		t.Errorf("getMaximumInterval() = %v, want %v", got, defaultMaximumInterval)
	}
}

func TestRetryPolicy_CustomValues(t *testing.T) {
	p := &RetryPolicy{
		InitialInterval:    200 * time.Millisecond,
		BackoffCoefficient: 3.0,
		MaximumInterval:    5 * time.Second,
	}
	if got := p.getInitialInterval(); got != 200*time.Millisecond {
		t.Errorf("getInitialInterval() = %v, want 200ms", got)
	}
	if got := p.getBackoffCoefficient(); got != 3.0 {
		t.Errorf("getBackoffCoefficient() = %v, want 3.0", got)
	}
	if got := p.getMaximumInterval(); got != 5*time.Second {
		t.Errorf("getMaximumInterval() = %v, want 5s", got)
	}
}

func TestRetryPolicy_ComputeNextDelay_ExponentialGrowth(t *testing.T) {
	p := &RetryPolicy{
		InitialInterval:    100 * time.Millisecond,
		BackoffCoefficient: 2.0,
		MaximumInterval:    10 * time.Second,
	}

	for attempt := 0; attempt < 5; attempt++ {
		delay := p.computeNextDelay(0, attempt)
		if delay == done {
			t.Fatalf("attempt %d: got done, want a positive delay", attempt)
		}
		// With 20% jitter, the delay should be in [0.8*expected, expected)
		expected := float64(100*time.Millisecond) * pow2(float64(attempt))
		minDelay := time.Duration(0.8 * expected)
		maxDelay := time.Duration(expected)
		if delay < minDelay || delay > maxDelay {
			t.Errorf("attempt %d: delay %v not in [%v, %v]", attempt, delay, minDelay, maxDelay)
		}
	}
}

func pow2(exp float64) float64 {
	result := 1.0
	for i := 0; i < int(exp); i++ {
		result *= 2
	}
	return result
}

func TestRetryPolicy_ComputeNextDelay_CappedByMaximumInterval(t *testing.T) {
	p := &RetryPolicy{
		InitialInterval:    1 * time.Second,
		BackoffCoefficient: 10.0,
		MaximumInterval:    5 * time.Second,
	}

	// attempt 1: 1s * 10^1 = 10s, capped to 5s, with jitter in [4s, 5s]
	delay := p.computeNextDelay(0, 1)
	if delay == done {
		t.Fatal("got done, want a positive delay")
	}
	if delay > 5*time.Second {
		t.Errorf("delay %v exceeds MaximumInterval 5s", delay)
	}
}

func TestRetryPolicy_ComputeNextDelay_StopsAtMaximumAttempts(t *testing.T) {
	p := &RetryPolicy{
		InitialInterval: 100 * time.Millisecond,
		MaximumAttempts: 3,
	}

	// MaximumAttempts=3 includes the first execution, so 2 retries allowed.
	// Retry indices 0 and 1 should return a delay.
	for i := 0; i < 2; i++ {
		if d := p.computeNextDelay(0, i); d == done {
			t.Errorf("retry %d: got done, want a delay", i)
		}
	}
	// Retry index 2 (would be 4th execution) should return done.
	if d := p.computeNextDelay(0, 2); d != done {
		t.Errorf("retry 2: got %v, want done", d)
	}
}

func TestRetryPolicy_ComputeNextDelay_StopsAfterTotalTimeout(t *testing.T) {
	p := &RetryPolicy{
		InitialInterval: 100 * time.Millisecond,
		TotalTimeout:    1 * time.Second,
	}

	// Within timeout: should return a delay
	if d := p.computeNextDelay(500*time.Millisecond, 0); d == done {
		t.Error("within timeout: got done, want a delay")
	}

	// Past timeout: should return done
	if d := p.computeNextDelay(2*time.Second, 0); d != done {
		t.Errorf("past timeout: got %v, want done", d)
	}
}

func TestRetryPolicy_ComputeNextDelay_ClampedByRemainingTime(t *testing.T) {
	p := &RetryPolicy{
		InitialInterval: 100 * time.Millisecond,
		TotalTimeout:    500 * time.Millisecond,
	}

	// With 400ms elapsed and 100ms remaining, the delay should not exceed
	// the remaining 100ms. With jitter it could be slightly less.
	delay := p.computeNextDelay(400*time.Millisecond, 0)
	if delay == done {
		t.Fatal("got done, want a delay clamped to remaining time")
	}
	if delay > 100*time.Millisecond {
		t.Errorf("delay %v exceeds remaining time 100ms", delay)
	}
}

func TestRetrier_NextBackOff_IncrementsAttempt(t *testing.T) {
	p := &RetryPolicy{
		InitialInterval: 100 * time.Millisecond,
		MaximumAttempts: 3,
	}

	r := newRetrier(p, newFakeClock())

	// MaximumAttempts=3: allows 2 retries (indices 0,1)
	if d := r.nextBackOff(); d == done {
		t.Error("retry 0: got done, want a delay")
	}
	if d := r.nextBackOff(); d == done {
		t.Error("retry 1: got done, want a delay")
	}
	// Third nextBackOff (retry index 2) should return done
	if d := r.nextBackOff(); d != done {
		t.Errorf("retry 2: got %v, want done", d)
	}
}

func TestRetrier_Reset(t *testing.T) {
	p := &RetryPolicy{
		InitialInterval: 100 * time.Millisecond,
		MaximumAttempts: 2,
	}

	r := newRetrier(p, newFakeClock())

	// MaximumAttempts=2: 1 retry allowed
	if d := r.nextBackOff(); d == done {
		t.Error("retry 0: got done, want a delay")
	}
	if d := r.nextBackOff(); d != done {
		t.Error("after exhaustion: want done")
	}

	r.reset()

	// After reset, should get a delay again
	if d := r.nextBackOff(); d == done {
		t.Error("after reset: got done, want a delay")
	}
}

func TestNewRetrier_PanicsOnNilPolicy(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil policy")
		}
	}()
	newRetrier(nil, newFakeClock())
}

func TestNewRetrier_PanicsOnNilClock(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil clock")
		}
	}()
	newRetrier(&RetryPolicy{}, nil)
}

// --- fakeClock for tests ---

func newFakeClock() clock.TimeSource {
	return &fakeClock{now: time.Now()}
}

type fakeClock struct {
	now time.Time
}

func (f *fakeClock) Now() time.Time                  { return f.now }
func (f *fakeClock) Since(t time.Time) time.Duration { return f.now.Sub(t) }
func (f *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- f.now.Add(d)
	return ch
}
func (f *fakeClock) Sleep(time.Duration) {}
func (f *fakeClock) SleepWithContext(_ context.Context, _ time.Duration) error {
	return nil
}
func (f *fakeClock) NewTicker(time.Duration) clock.Ticker { return nil }
func (f *fakeClock) NewTimer(time.Duration) clock.Timer   { return nil }
func (f *fakeClock) AfterFunc(time.Duration, func()) clock.Timer {
	return nil
}
func (f *fakeClock) ContextWithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}
func (f *fakeClock) ContextWithDeadline(ctx context.Context, t time.Time) (context.Context, context.CancelFunc) {
	return context.WithDeadline(ctx, t)
}
