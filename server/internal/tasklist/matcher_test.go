package tasklist

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/stretchr/testify/require"
)

// fakePollForwarder is a test double for the matcher's poll fan-in
// dependency. It records call count and returns a canned result.
type fakePollForwarder struct {
	mu     sync.Mutex
	calls  int
	result *Task
	err    errors.CategorizedError
}

func (f *fakePollForwarder) ForwardPoll(ctx context.Context, workerID string) (*Task, errors.CategorizedError) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.result, f.err
}

func (f *fakePollForwarder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newTestMatcher(t *testing.T, fwd pollForwarder) *matcher {
	t.Helper()
	id, err := NewIdentifier("ns", "tl", 1) // non-root
	require.NoError(t, err)
	return newMatcher(id, fwd, log.NewNoop(), 10)
}

// A local task that is already ready must be returned WITHOUT consulting
// the forwarder. This is the core no-drop guarantee: if we never ask root
// to commit a task while a local one is ready, root can never commit a
// task we'd then have to drop.
func TestMatcherPoll_LocalReady_DoesNotForward(t *testing.T) {
	fwd := &fakePollForwarder{result: newAsyncPickupTask("forwarded", "ns", 0, 99)}
	m := newTestMatcher(t, fwd)

	local := newAsyncPickupTask("local", "ns", 0, 5)
	m.BufferCh() <- local

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := m.FullPoll(ctx, "worker-1")
	require.NoError(t, err)
	require.Equal(t, local, got)
	require.Zero(t, fwd.callCount(), "forwarder must not be consulted when a local task is ready")
}

// When nothing is ready locally, the forwarded task is consumed inline
// and returned — never dropped.
func TestMatcherPoll_LocalEmpty_ReturnsForwardedTask(t *testing.T) {
	forwarded := newAsyncPickupTask("forwarded", "ns", 0, 99)
	fwd := &fakePollForwarder{result: forwarded}
	m := newTestMatcher(t, fwd)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := m.FullPoll(ctx, "worker-1")
	require.NoError(t, err)
	require.Equal(t, forwarded, got)
	require.Equal(t, 1, fwd.callCount())
}

// Forward miss (root empty) → fall back to blocking on local sources;
// a task arriving on bufferedCh after the forward is still delivered.
func TestMatcherPoll_ForwardEmpty_BlocksLocally(t *testing.T) {
	fwd := &fakePollForwarder{result: nil}
	m := newTestMatcher(t, fwd)

	late := newAsyncPickupTask("late", "ns", 0, 7)
	go func() {
		time.Sleep(20 * time.Millisecond)
		m.BufferCh() <- late
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := m.FullPoll(ctx, "worker-1")
	require.NoError(t, err)
	require.Equal(t, late, got)
	require.Equal(t, 1, fwd.callCount())
}

// Forward error is non-fatal: Poll falls back to local and honors ctx.
// On long-poll timeout the matcher returns (nil, nil) — an empty poll,
// not an error.
func TestMatcherPoll_ForwardError_FallsBackToCtx(t *testing.T) {
	fwd := &fakePollForwarder{err: errors.NewUnavailableError("boom", nil)}
	m := newTestMatcher(t, fwd)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	got, err := m.FullPoll(ctx, "worker-1")
	require.NoError(t, err)
	require.Nil(t, got)
	require.Equal(t, 1, fwd.callCount())
}

// Root partition (nil forwarder) never forwards and blocks on local only.
func TestMatcherPoll_NilForwarder_LocalOnly(t *testing.T) {
	m := newTestMatcher(t, nil)

	local := newAsyncPickupTask("local", "ns", 0, 3)
	m.BufferCh() <- local

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := m.FullPoll(ctx, "worker-1")
	require.NoError(t, err)
	require.Equal(t, local, got)
}

// TryLocalOffer is a non-blocking local handoff: it succeeds only when a poller
// is currently waiting on syncOfferCh, false otherwise.
func TestMatcherOffer_LocalOnly(t *testing.T) {
	m := newTestMatcher(t, &fakePollForwarder{})

	// No poller waiting → TryLocalOffer misses.
	require.False(t, m.TryLocalOffer(newSyncMatchTask("r", "ns", 0)))

	// With a poller blocked in Poll, TryLocalOffer hands off.
	got := make(chan *Task, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		task, _ := m.FullPoll(ctx, "worker-1")
		got <- task
	}()
	// Let the poller reach its blocking select (past the initial TryLocalPoll
	// and the forward attempt, which returns nil here).
	require.Eventually(t, func() bool {
		return m.TryLocalOffer(newSyncMatchTask("r2", "ns", 0))
	}, time.Second, 5*time.Millisecond)

	select {
	case task := <-got:
		require.NotNil(t, task)
		require.Equal(t, "r2", task.RunID())
	case <-time.After(time.Second):
		t.Fatal("poller did not receive offered task")
	}
}
