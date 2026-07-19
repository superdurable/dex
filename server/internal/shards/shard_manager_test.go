// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package shards

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/errors"
	"github.com/superdurable/dex/server/internal/log"
	"github.com/superdurable/dex/server/internal/membership"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// These fakes let us drive shardManagerImpl without a DB or a real memberlist.
var (
	_ membership.Membership = (*fakeMembership)(nil)
	_ p.ShardStore          = (*fakeShardStore)(nil)
	_ TaskProcessorsManager = (*fakeFactory)(nil)
)

func TestShardManager_ClaimsDesiredThenStopReleases(t *testing.T) {
	sm, store, factory, _ := newTestManager(t, []int32{0, 1, 2})
	require.NoError(t, sm.Start(context.Background()))
	t.Cleanup(sm.Stop)

	requireOwnedEventually(t, sm, []int32{0, 1, 2})
	require.ElementsMatch(t, []int32{0, 1, 2}, factory.startedShards())

	sm.Stop()

	require.Empty(t, sm.GetOwnedShards())
	require.ElementsMatch(t, []int32{0, 1, 2}, store.releasedShards())
	require.ElementsMatch(t, []int32{0, 1, 2}, factory.stoppedShards())
}

func TestShardManager_RebalanceReleasesUndesiredShards(t *testing.T) {
	sm, store, factory, mem := newTestManager(t, []int32{0, 1, 2})
	require.NoError(t, sm.Start(context.Background()))
	t.Cleanup(sm.Stop)
	requireOwnedEventually(t, sm, []int32{0, 1, 2})

	// Drop 1 and 2 from this member's assignment, then drive one rebalance
	// directly (bypassing the debounce loop for determinism).
	mem.setDesired([]int32{0})
	sm.rebalanceShards()

	require.ElementsMatch(t, []int32{0}, sm.GetOwnedShards())
	require.ElementsMatch(t, []int32{1, 2}, store.releasedShards())
	require.ElementsMatch(t, []int32{1, 2}, factory.stoppedShards())
}

// TestShardManager_StopHaltsLeaseRenewals guards the loopWG fix: Stop must wait
// for the per-shard lease-renewal goroutines, so no renewal can fire after Stop
// returns. Without the wait, a renewal in flight when Stop returns could still
// hit the store.
func TestShardManager_StopHaltsLeaseRenewals(t *testing.T) {
	sm, store, _, _ := newTestManager(t, []int32{0, 1})
	require.NoError(t, sm.Start(context.Background()))
	t.Cleanup(sm.Stop)
	requireOwnedEventually(t, sm, []int32{0, 1})

	require.Eventually(t, func() bool { return store.renewCount() > 0 },
		2*time.Second, 5*time.Millisecond, "expected lease renewals to run")

	sm.Stop()
	settled := store.renewCount()
	time.Sleep(200 * time.Millisecond) // several LeaseRenewInterval ticks
	require.Equal(t, settled, store.renewCount(),
		"no lease renewals should occur after Stop returns")
}

func TestShardManager_SignalShardLostReleasesShard(t *testing.T) {
	sm, store, factory, _ := newTestManager(t, []int32{0, 1})
	require.NoError(t, sm.Start(context.Background()))
	t.Cleanup(sm.Stop)
	requireOwnedEventually(t, sm, []int32{0, 1})

	sm.InformShardLost(0)

	require.False(t, sm.IsLocalShard(0))
	require.True(t, sm.IsLocalShard(1))
	require.Contains(t, store.releasedShards(), int32(0))
	require.Contains(t, factory.stoppedShards(), int32(0))
}

func newTestManager(t *testing.T, desired []int32) (*shardManagerImpl, *fakeShardStore, *fakeFactory, *fakeMembership) {
	t.Helper()
	cfg := &config.ShardConfig{
		TotalShards:            8,
		LeaseDuration:          30 * time.Second,
		LeaseRenewInterval:     20 * time.Millisecond,
		LeaseExpiryBuffer:      5 * time.Second,
		ShutdownGracefulPeriod: 0,
		Membership:             config.MembershipConfig{OwnershipOpsMaxAttempts: 3},
	}
	store := newFakeShardStore(cfg.LeaseDuration)
	factory := newFakeFactory()
	mem := &fakeMembership{desired: desired}

	sm := NewShardManager(cfg, store, log.NewDefaultLogger(), "member-1", factory,
		"127.0.0.1:7233", func(string) {}).(*shardManagerImpl)
	sm.membership = mem // replace the real (unstarted) membership with the fake
	return sm, store, factory, mem
}

func requireOwnedEventually(t *testing.T, sm ShardManager, want []int32) {
	t.Helper()
	require.Eventually(t, func() bool { return sameShardSet(sm.GetOwnedShards(), want) },
		2*time.Second, 10*time.Millisecond, "shard manager did not converge to owned=%v", want)
}

func sameShardSet(got, want []int32) bool {
	if len(got) != len(want) {
		return false
	}
	a := append([]int32(nil), got...)
	b := append([]int32(nil), want...)
	sort.Slice(a, func(i, j int) bool { return a[i] < a[j] })
	sort.Slice(b, func(i, j int) bool { return b[i] < b[j] })
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- fakes ---

type fakeMembership struct {
	mu      sync.Mutex
	desired []int32
}

func (f *fakeMembership) setDesired(shards []int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.desired = shards
}

func (f *fakeMembership) Start() errors.CategorizedError { return nil }
func (f *fakeMembership) Stop()                          {}
func (f *fakeMembership) MemberID() string               { return "member-1" }
func (f *fakeMembership) GetNodeForKey(string) string    { return "member-1" }
func (f *fakeMembership) GetAddress(string) string       { return "" }

func (f *fakeMembership) GetShardsForMember(string, int) []int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int32(nil), f.desired...)
}

type fakeShardStore struct {
	lease  time.Duration
	renews atomic.Int64

	mu       sync.Mutex
	versions map[int32]int64
	released []int32
}

func newFakeShardStore(lease time.Duration) *fakeShardStore {
	return &fakeShardStore{lease: lease, versions: make(map[int32]int64)}
}

func (f *fakeShardStore) ClaimShard(_ context.Context, shardID int32, memberID string, lease time.Duration) (*p.Shard, errors.CategorizedError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	version := f.versions[shardID] + 1
	f.versions[shardID] = version
	now := time.Now()
	return &p.Shard{
		ShardID:        shardID,
		Version:        version,
		MemberID:       memberID,
		ClaimedAt:      now,
		LeaseExpiresAt: now.Add(lease),
		Metadata:       p.ShardMetadata{RangeID: int32(version)},
	}, nil
}

func (f *fakeShardStore) RenewShardLease(_ context.Context, _ int32, _ string, _ int64, _ time.Duration, _ *p.ShardMetadata) (time.Time, errors.CategorizedError) {
	f.renews.Add(1)
	return time.Now().Add(f.lease), nil
}

func (f *fakeShardStore) ReleaseShard(_ context.Context, shardID int32, _ string, _ int64) errors.CategorizedError {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, shardID)
	return nil
}

func (f *fakeShardStore) BatchReleaseShards(_ context.Context, _ string, entries []p.ShardReleaseEntry) errors.CategorizedError {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, entry := range entries {
		f.released = append(f.released, entry.ShardID)
	}
	return nil
}

func (f *fakeShardStore) Close() error { return nil }

func (f *fakeShardStore) renewCount() int64 { return f.renews.Load() }

func (f *fakeShardStore) releasedShards() []int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int32(nil), f.released...)
}

type fakeFactory struct {
	mu      sync.Mutex
	started map[int32]int32
	stopped map[int32]int
}

func newFakeFactory() *fakeFactory {
	return &fakeFactory{started: make(map[int32]int32), stopped: make(map[int32]int)}
}

func (f *fakeFactory) StartShard(shardID int32, metadata p.ShardMetadata) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started[shardID] = metadata.RangeID
}

func (f *fakeFactory) StopShard(shardID int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped[shardID]++
	delete(f.started, shardID)
}

func (f *fakeFactory) GetShardMetadata(int32) *p.ShardMetadata { return &p.ShardMetadata{} }
func (f *fakeFactory) NotifyNewImmediateTask(int32)            {}
func (f *fakeFactory) NotifyNewTimerTask(int32, int64)         {}

func (f *fakeFactory) startedShards() []int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int32, 0, len(f.started))
	for shardID := range f.started {
		out = append(out, shardID)
	}
	return out
}

func (f *fakeFactory) stoppedShards() []int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int32, 0, len(f.stopped))
	for shardID := range f.stopped {
		out = append(out, shardID)
	}
	return out
}
