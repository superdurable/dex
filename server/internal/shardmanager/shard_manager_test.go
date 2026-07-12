package shardmanager

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/config"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Test doubles for the shard manager
// ============================================================================

// fakeShardStore lets tests prescribe the metadata returned by ClaimShard so
// we can drive the post-claim skew gate. RenewShardLease and ReleaseShard are
// stubs because the readiness path doesn't depend on their behavior.
type fakeShardStore struct {
	p.ShardStore // promote to satisfy unused methods (panics if called)

	mu sync.Mutex
	// claimMetadataByShard prescribes ShardMetadata returned by ClaimShard
	// for each shard. If a shard is not in the map, an empty metadata is
	// returned (no skew, no committed timer).
	claimMetadataByShard map[int32]p.ShardMetadata
}

func (s *fakeShardStore) ClaimShard(_ context.Context, shardID int32, memberID string, leaseDuration time.Duration) (*p.Shard, errors.CategorizedError) {
	s.mu.Lock()
	md := s.claimMetadataByShard[shardID]
	s.mu.Unlock()
	return &p.Shard{
		ShardID:        shardID,
		Version:        1,
		MemberID:       memberID,
		LeaseExpiresAt: time.Now().Add(leaseDuration),
		Metadata:       md,
	}, nil
}

func (s *fakeShardStore) RenewShardLease(_ context.Context, _ int32, _ string, _ int64, leaseDuration time.Duration, _ *p.ShardMetadata) (time.Time, errors.CategorizedError) {
	return time.Now().Add(leaseDuration), nil
}

func (s *fakeShardStore) ReleaseShard(_ context.Context, _ int32, _ string, _ int64) errors.CategorizedError {
	return nil
}

func (s *fakeShardStore) BatchReleaseShards(_ context.Context, _ string, _ []p.ShardReleaseEntry) errors.CategorizedError {
	return nil
}

// recordingFactory tracks when StartComponents fires per shard, plus the
// first-call timestamp. Used to assert deferred start happens at-or-after
// the configured readyAt.
type recordingFactory struct {
	mu        sync.Mutex
	startCh   map[int32]chan struct{} // closed when StartComponents fires
	startedAt map[int32]time.Time     // wall-clock time of StartComponents
	calls     atomic.Int32
}

func newRecordingFactory() *recordingFactory {
	return &recordingFactory{
		startCh:   make(map[int32]chan struct{}),
		startedAt: make(map[int32]time.Time),
	}
}

func (f *recordingFactory) StartComponents(shardID int32, _ *ShardHandle, _ ShardManager) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls.Add(1)
	f.startedAt[shardID] = time.Now()
	if ch, ok := f.startCh[shardID]; ok {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
	return nil
}

func (f *recordingFactory) StopComponents(_ int32) {}

func (f *recordingFactory) waitForStart(shardID int32) <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch, ok := f.startCh[shardID]
	if !ok {
		ch = make(chan struct{})
		f.startCh[shardID] = ch
	}
	return ch
}

func (f *recordingFactory) startedTime(shardID int32) (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.startedAt[shardID]
	return t, ok
}

func newSingleShardManager(t *testing.T, factory ComponentFactory, store p.ShardStore) *shardManagerImpl {
	t.Helper()
	cluster := config.DefaultClusterConfig()
	// 1-member cluster on an ephemeral gossip port: no peers, ready at once.
	cluster.BindAddress = "127.0.0.1:0"
	cluster.MinMembersBeforeReady = 1
	cfg := config.ShardConfig{
		MaxShards:                     1,
		LeaseDuration:                 30 * time.Second,
		LeaseRenewInterval:            10 * time.Second,
		LeaseRenewJitter:              500 * time.Millisecond,
		LeaseExpiryBuffer:             3 * time.Second,
		ShutdownGracefulPeriod:        100 * time.Millisecond,
		DefaultShardsForNewNamespaces: 1,
		Cluster:                       cluster,
	}
	return NewShardManager(cfg, store, log.NewNoop(), "test-member", factory, "127.0.0.1:0", nil).(*shardManagerImpl)
}

// ============================================================================
// Tests
// ============================================================================

// TestClaimShard_NoWaitWhenCommittedInPast: TimerTaskCommittedSortKey is in
// the past → StartComponents fires immediately and AwaitShardReady returns
// without blocking.
func TestClaimShard_NoWaitWhenCommittedInPast(t *testing.T) {
	factory := newRecordingFactory()
	store := &fakeShardStore{
		claimMetadataByShard: map[int32]p.ShardMetadata{
			0: {TimerTaskCommittedSortKey: time.Now().Add(-5 * time.Second).UnixMilli()},
		},
	}
	sm := newSingleShardManager(t, factory, store)

	require.NoError(t, sm.Start(context.Background()))
	t.Cleanup(sm.Stop)

	// Components must start synchronously during claim — no goroutine wait.
	require.Eventually(t, func() bool {
		return factory.calls.Load() >= 1
	}, 2*time.Second, 10*time.Millisecond, "StartComponents must fire immediately for past-committed metadata")

	err := sm.AwaitShardReady(context.Background(), 0)
	assert.Nil(t, err, "AwaitShardReady must return nil immediately for a no-skew claim")
}

// TestClaimShard_DeferredStartUntilReadyAt: TimerTaskCommittedSortKey is
// 1.5s in the future → StartComponents must NOT fire for ~1.5s, and
// AwaitShardReady must block for ~1.5s.
func TestClaimShard_DeferredStartUntilReadyAt(t *testing.T) {
	skew := 1500 * time.Millisecond
	factory := newRecordingFactory()
	store := &fakeShardStore{
		claimMetadataByShard: map[int32]p.ShardMetadata{
			0: {TimerTaskCommittedSortKey: time.Now().Add(skew).UnixMilli()},
		},
	}
	sm := newSingleShardManager(t, factory, store)

	claimStart := time.Now()
	require.NoError(t, sm.Start(context.Background()))
	t.Cleanup(sm.Stop)

	// During the skew window, components must NOT start. We give it 700ms
	// (well under the 1500ms wait) to spot any premature start.
	time.Sleep(700 * time.Millisecond)
	assert.Equal(t, int32(0), factory.calls.Load(),
		"StartComponents must be deferred for the full skew wait, not started early")

	// AwaitShardReady should now be blocking. Verify via a separate goroutine.
	done := make(chan errors.CategorizedError, 1)
	awaitStart := time.Now()
	go func() {
		done <- sm.AwaitShardReady(context.Background(), 0)
	}()

	select {
	case err := <-done:
		require.Nil(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("AwaitShardReady never completed")
	}
	awaitElapsed := time.Since(awaitStart)
	assert.GreaterOrEqual(t, awaitElapsed, 500*time.Millisecond,
		"AwaitShardReady must have actually blocked through the remainder of the skew wait")

	// And StartComponents should have fired around the readyAt.
	startedAt, ok := factory.startedTime(0)
	require.True(t, ok, "StartComponents must have fired by now")
	startedSinceClaim := startedAt.Sub(claimStart)
	assert.GreaterOrEqual(t, startedSinceClaim, skew-200*time.Millisecond,
		"StartComponents fired too early (got %v, want >= ~%v)", startedSinceClaim, skew)
}

// TestAwaitShardReady_ContextCancelReturnsRetriable: caller's ctx fires
// before the readyCh closes → AwaitShardReady returns a retriable error
// without hanging.
func TestAwaitShardReady_ContextCancelReturnsRetriable(t *testing.T) {
	factory := newRecordingFactory()
	store := &fakeShardStore{
		claimMetadataByShard: map[int32]p.ShardMetadata{
			0: {TimerTaskCommittedSortKey: time.Now().Add(5 * time.Second).UnixMilli()},
		},
	}
	sm := newSingleShardManager(t, factory, store)
	require.NoError(t, sm.Start(context.Background()))
	t.Cleanup(sm.Stop)

	// Caller has a much shorter deadline than the 5s skew wait. AwaitShardReady
	// must return after the deadline, not block past it.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	awaitStart := time.Now()
	err := sm.AwaitShardReady(ctx, 0)
	awaitElapsed := time.Since(awaitStart)
	require.NotNil(t, err, "ctx-cancel-during-wait must propagate as a write error")
	assert.True(t, err.IsRetriable(),
		"ctx-cancel-during-wait should be retriable so callers retry/forward")
	assert.Less(t, awaitElapsed, 1*time.Second,
		"AwaitShardReady must observe ctx cancellation promptly (got %v)", awaitElapsed)
}

// TestAwaitShardReady_ShardNotOwnedReturnsRetriable: requesting a shard ID
// the manager has not claimed (or has released) returns a retriable error
// so the caller's forwarder can re-resolve ownership.
func TestAwaitShardReady_ShardNotOwnedReturnsRetriable(t *testing.T) {
	factory := newRecordingFactory()
	store := &fakeShardStore{}
	sm := newSingleShardManager(t, factory, store)
	require.NoError(t, sm.Start(context.Background()))
	t.Cleanup(sm.Stop)

	// Shard 99 is not in our 1-shard config.
	err := sm.AwaitShardReady(context.Background(), 99)
	require.NotNil(t, err)
	assert.True(t, err.IsRetriable(),
		"unknown shard should be retriable so the caller can re-resolve ownership")
}
