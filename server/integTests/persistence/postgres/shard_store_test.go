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

package postgres_test

import (
	"common-go/ids"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	p "github.com/superdurable/dex/server/internal/persistence"
)

var shardSeq atomic.Int32

func nextShardID(t *testing.T) int32 {
	t.Helper()
	return shardSeq.Add(1)
}

func TestShardStore_FirstClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)
	lease := 30 * time.Second

	before := time.Now()
	shard, err := shardStore.ClaimShard(ctx, shardID, "member-a", lease)
	require.NoError(t, err)
	require.Equal(t, shardID, shard.ShardID)
	require.Equal(t, int64(1), shard.Version)
	require.Equal(t, "member-a", shard.MemberID)
	require.Equal(t, int32(1), shard.Metadata.RangeID)
	require.True(t, shard.LeaseExpiresAt.After(before))
	require.True(t, !shard.ClaimedAt.Before(before))
}

func TestShardStore_ConflictWhileHeld(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)

	_, err := shardStore.ClaimShard(ctx, shardID, "member-a", time.Minute)
	require.NoError(t, err)

	_, err = shardStore.ClaimShard(ctx, shardID, "member-b", time.Minute)
	require.Error(t, err)
	require.True(t, err.IsConflictError(), "got %v", err)
}

func TestShardStore_SameMemberReclaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)

	first, err := shardStore.ClaimShard(ctx, shardID, "member-a", time.Minute)
	require.NoError(t, err)

	second, err := shardStore.ClaimShard(ctx, shardID, "member-a", time.Minute)
	require.NoError(t, err)
	require.Equal(t, first.Version+1, second.Version)
	require.Equal(t, first.Metadata.RangeID+1, second.Metadata.RangeID)
	require.Equal(t, "member-a", second.MemberID)
}

func TestShardStore_ReclaimAfterRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)

	first, err := shardStore.ClaimShard(ctx, shardID, "member-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, shardStore.ReleaseShard(ctx, shardID, "member-a", first.Version))

	second, err := shardStore.ClaimShard(ctx, shardID, "member-b", time.Minute)
	require.NoError(t, err)
	require.Equal(t, "member-b", second.MemberID)
	// Release does not bump version; the reclaim advances it by exactly one.
	require.Equal(t, first.Version+1, second.Version)
	require.Equal(t, first.Metadata.RangeID+1, second.Metadata.RangeID)
}

func TestShardStore_ReclaimAfterLeaseExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)
	lease := 200 * time.Millisecond

	first, err := shardStore.ClaimShard(ctx, shardID, "member-a", lease)
	require.NoError(t, err)

	var second *p.Shard
	require.Eventually(t, func() bool {
		claimed, claimErr := shardStore.ClaimShard(ctx, shardID, "member-b", time.Minute)
		if claimErr != nil {
			return false
		}
		second = claimed
		return true
	}, 3*time.Second, 50*time.Millisecond)

	require.Equal(t, "member-b", second.MemberID)
	require.Greater(t, second.Version, first.Version)
	require.Greater(t, second.Metadata.RangeID, first.Metadata.RangeID)
}

func TestShardStore_RenewOK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)

	claimed, err := shardStore.ClaimShard(ctx, shardID, "member-a", time.Second)
	require.NoError(t, err)

	renewedAt, err := shardStore.RenewShardLease(ctx, shardID, "member-a", claimed.Version, time.Minute, nil)
	require.NoError(t, err)
	require.True(t, renewedAt.After(claimed.LeaseExpiresAt))

	// Renew does not bump the version: a second renew at the same version still succeeds.
	_, err = shardStore.RenewShardLease(ctx, shardID, "member-a", claimed.Version, time.Minute, nil)
	require.NoError(t, err)
}

func TestShardStore_RenewCAS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)

	claimed, err := shardStore.ClaimShard(ctx, shardID, "member-a", time.Minute)
	require.NoError(t, err)

	_, err = shardStore.RenewShardLease(ctx, shardID, "member-a", claimed.Version+1, time.Minute, nil)
	require.Error(t, err)
	require.True(t, err.IsCASError(), "wrong version: %v", err)

	_, err = shardStore.RenewShardLease(ctx, shardID, "member-b", claimed.Version, time.Minute, nil)
	require.Error(t, err)
	require.True(t, err.IsCASError(), "wrong member: %v", err)
}

func TestShardStore_RenewNonexistentShard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	_, err := shardStore.RenewShardLease(ctx, nextShardID(t), "member-a", 1, time.Minute, nil)
	require.Error(t, err)
	require.True(t, err.IsCASError(), "got %v", err)
}

func TestShardStore_RenewMetadataMerge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)
	timerID := ids.NewUID()

	first, err := shardStore.ClaimShard(ctx, shardID, "member-a", time.Minute)
	require.NoError(t, err)

	meta := &p.ShardMetadata{
		ImmediateTaskCommittedSeq: 42,
		TimerTaskCommittedSortKey: 100,
		TimerTaskCommittedID:      timerID,
	}
	_, err = shardStore.RenewShardLease(ctx, shardID, "member-a", first.Version, time.Minute, meta)
	require.NoError(t, err)
	require.NoError(t, shardStore.ReleaseShard(ctx, shardID, "member-a", first.Version))

	second, err := shardStore.ClaimShard(ctx, shardID, "member-b", time.Minute)
	require.NoError(t, err)
	require.Equal(t, first.Metadata.RangeID+1, second.Metadata.RangeID)
	require.Equal(t, int64(42), second.Metadata.ImmediateTaskCommittedSeq)
	require.Equal(t, int64(100), second.Metadata.TimerTaskCommittedSortKey)
	require.Equal(t, timerID, second.Metadata.TimerTaskCommittedID)
}

// Renew merges only the committed watermarks; it must never overwrite range_id,
// which belongs to the claim path (see shard_store_impl.go RenewShardLease).
func TestShardStore_RenewDoesNotOverwriteRangeID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)

	first, err := shardStore.ClaimShard(ctx, shardID, "member-a", time.Minute)
	require.NoError(t, err)
	require.Equal(t, int32(1), first.Metadata.RangeID)

	// A hostile range_id in the renew payload must be ignored.
	_, err = shardStore.RenewShardLease(ctx, shardID, "member-a", first.Version, time.Minute,
		&p.ShardMetadata{RangeID: 999, ImmediateTaskCommittedSeq: 7})
	require.NoError(t, err)
	require.NoError(t, shardStore.ReleaseShard(ctx, shardID, "member-a", first.Version))

	second, err := shardStore.ClaimShard(ctx, shardID, "member-b", time.Minute)
	require.NoError(t, err)
	require.Equal(t, int32(2), second.Metadata.RangeID, "range_id advances from 1, not from the payload's 999")
	require.Equal(t, int64(7), second.Metadata.ImmediateTaskCommittedSeq)
}

// Locks the current lease semantics: an expired lease is still renewable by the
// original owner as long as no one else has reclaimed (which would bump version).
func TestShardStore_RenewAfterOwnLeaseExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)

	// Zero lease expires immediately without any wall-clock wait.
	claimed, err := shardStore.ClaimShard(ctx, shardID, "member-a", 0)
	require.NoError(t, err)

	_, err = shardStore.RenewShardLease(ctx, shardID, "member-a", claimed.Version, time.Minute, nil)
	require.NoError(t, err, "owner may renew its own expired lease when unclaimed")
}

func TestShardStore_ReleaseCAS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)

	claimed, err := shardStore.ClaimShard(ctx, shardID, "member-a", time.Minute)
	require.NoError(t, err)

	err = shardStore.ReleaseShard(ctx, shardID, "member-a", claimed.Version+1)
	require.Error(t, err)
	require.True(t, err.IsCASError(), "wrong version: %v", err)

	err = shardStore.ReleaseShard(ctx, shardID, "member-b", claimed.Version)
	require.Error(t, err)
	require.True(t, err.IsCASError(), "wrong member: %v", err)
}

func TestShardStore_ReleaseNonexistentShard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	err := shardStore.ReleaseShard(ctx, nextShardID(t), "member-a", 1)
	require.Error(t, err)
	require.True(t, err.IsCASError(), "got %v", err)
}

// Release does not bump version, so releasing an already-released shard at the
// same version is idempotent and must not error.
func TestShardStore_ReleaseIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)

	claimed, err := shardStore.ClaimShard(ctx, shardID, "member-a", time.Minute)
	require.NoError(t, err)

	require.NoError(t, shardStore.ReleaseShard(ctx, shardID, "member-a", claimed.Version))
	require.NoError(t, shardStore.ReleaseShard(ctx, shardID, "member-a", claimed.Version))
}

func TestShardStore_BatchRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardA := nextShardID(t)
	shardB := nextShardID(t)
	staleID := nextShardID(t)

	a, err := shardStore.ClaimShard(ctx, shardA, "member-a", time.Minute)
	require.NoError(t, err)
	b, err := shardStore.ClaimShard(ctx, shardB, "member-a", time.Minute)
	require.NoError(t, err)

	require.NoError(t, shardStore.BatchReleaseShards(ctx, "member-a", nil))
	require.NoError(t, shardStore.BatchReleaseShards(ctx, "member-a", []p.ShardReleaseEntry{}))

	err = shardStore.BatchReleaseShards(ctx, "member-a", []p.ShardReleaseEntry{
		{ShardID: shardA, ExpectedVersion: a.Version},
		{ShardID: shardB, ExpectedVersion: b.Version},
		{ShardID: staleID, ExpectedVersion: 99},
	})
	require.NoError(t, err)

	claimedA, err := shardStore.ClaimShard(ctx, shardA, "member-b", time.Minute)
	require.NoError(t, err)
	require.Equal(t, "member-b", claimedA.MemberID)
	// Batch release leaves version untouched; the reclaim advances it by one.
	require.Equal(t, a.Version+1, claimedA.Version)

	claimedB, err := shardStore.ClaimShard(ctx, shardB, "member-b", time.Minute)
	require.NoError(t, err)
	require.Equal(t, "member-b", claimedB.MemberID)
}

// Best-effort batch release skips entries whose member or version does not match,
// releasing only the caller's still-held shards.
func TestShardStore_BatchReleaseMixedMembers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mine := nextShardID(t)
	otherMember := nextShardID(t)
	wrongVersion := nextShardID(t)

	mino, err := shardStore.ClaimShard(ctx, mine, "member-a", time.Minute)
	require.NoError(t, err)
	_, err = shardStore.ClaimShard(ctx, otherMember, "member-b", time.Minute)
	require.NoError(t, err)
	wrong, err := shardStore.ClaimShard(ctx, wrongVersion, "member-a", time.Minute)
	require.NoError(t, err)

	err = shardStore.BatchReleaseShards(ctx, "member-a", []p.ShardReleaseEntry{
		{ShardID: mine, ExpectedVersion: mino.Version},
		{ShardID: otherMember, ExpectedVersion: 1},                  // held by member-b → skipped
		{ShardID: wrongVersion, ExpectedVersion: wrong.Version + 1}, // stale version → skipped
	})
	require.NoError(t, err)

	// Only "mine" was released and is now claimable.
	got, err := shardStore.ClaimShard(ctx, mine, "member-x", time.Minute)
	require.NoError(t, err)
	require.Equal(t, "member-x", got.MemberID)

	// The other two are still held, so a foreign claim conflicts.
	_, err = shardStore.ClaimShard(ctx, otherMember, "member-x", time.Minute)
	require.True(t, err.IsConflictError(), "otherMember should stay held: %v", err)
	_, err = shardStore.ClaimShard(ctx, wrongVersion, "member-x", time.Minute)
	require.True(t, err.IsConflictError(), "wrongVersion should stay held: %v", err)
}

func TestShardStore_ZeroLeaseImmediatelyClaimable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)

	_, err := shardStore.ClaimShard(ctx, shardID, "member-a", 0)
	require.NoError(t, err)

	// The zero lease is already expired, so another member claims without conflict.
	second, err := shardStore.ClaimShard(ctx, shardID, "member-b", time.Minute)
	require.NoError(t, err)
	require.Equal(t, "member-b", second.MemberID)
}

// Many members race a brand-new shard: exactly one INSERT wins, the rest hit the
// unique constraint and are categorized as ConflictError.
func TestShardStore_ConcurrentFirstClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)
	const claimers = 8

	var wg sync.WaitGroup
	var successes atomic.Int32
	nonConflict := make([]bool, claimers)
	for i := 0; i < claimers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := shardStore.ClaimShard(ctx, shardID, fmt.Sprintf("member-%d", i), time.Minute)
			if err == nil {
				successes.Add(1)
				return
			}
			nonConflict[i] = !err.IsConflictError()
		}(i)
	}
	wg.Wait()

	require.Equal(t, int32(1), successes.Load(), "exactly one claimer wins the new shard")
	for i, bad := range nonConflict {
		require.False(t, bad, "loser %d must get ConflictError", i)
	}
}

// Many members race an already-expired shard: exactly one wins the CAS UPDATE,
// the rest lose the version race (CAS) or observe the winner's fresh lease (Conflict).
func TestShardStore_ConcurrentClaimExpired(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)

	// Zero lease expires immediately, so every claimer races an expired shard.
	_, err := shardStore.ClaimShard(ctx, shardID, "seed", 0)
	require.NoError(t, err)

	const claimers = 8
	var wg sync.WaitGroup
	var successes atomic.Int32
	unexpected := make([]bool, claimers)
	for i := 0; i < claimers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := shardStore.ClaimShard(ctx, shardID, fmt.Sprintf("member-%d", i), time.Minute)
			if err == nil {
				successes.Add(1)
				return
			}
			unexpected[i] = !err.IsCASError() && !err.IsConflictError()
		}(i)
	}
	wg.Wait()

	require.Equal(t, int32(1), successes.Load(), "exactly one claimer reclaims the expired shard")
	for i, bad := range unexpected {
		require.False(t, bad, "loser %d must get CAS or Conflict error", i)
	}
}
