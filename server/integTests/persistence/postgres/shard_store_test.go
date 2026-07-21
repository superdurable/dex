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
	ctx := context.Background()
	shardID := nextShardID(t)

	_, err := shardStore.ClaimShard(ctx, shardID, "member-a", time.Minute)
	require.NoError(t, err)

	_, err = shardStore.ClaimShard(ctx, shardID, "member-b", time.Minute)
	require.Error(t, err)
	require.True(t, err.IsConflictError(), "got %v", err)
}

func TestShardStore_SameMemberReclaim(t *testing.T) {
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
	ctx := context.Background()
	shardID := nextShardID(t)

	first, err := shardStore.ClaimShard(ctx, shardID, "member-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, shardStore.ReleaseShard(ctx, shardID, "member-a", first.Version))

	second, err := shardStore.ClaimShard(ctx, shardID, "member-b", time.Minute)
	require.NoError(t, err)
	require.Equal(t, "member-b", second.MemberID)
	require.Equal(t, first.Version+1, second.Version)
	require.Equal(t, first.Metadata.RangeID+1, second.Metadata.RangeID)
}

func TestShardStore_ReclaimAfterLeaseExpiry(t *testing.T) {
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
	ctx := context.Background()
	shardID := nextShardID(t)

	claimed, err := shardStore.ClaimShard(ctx, shardID, "member-a", time.Second)
	require.NoError(t, err)

	renewedAt, err := shardStore.RenewShardLease(ctx, shardID, "member-a", claimed.Version, time.Minute, nil)
	require.NoError(t, err)
	require.True(t, renewedAt.After(claimed.LeaseExpiresAt))
}

func TestShardStore_RenewCAS(t *testing.T) {
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

func TestShardStore_RenewMetadataMerge(t *testing.T) {
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

func TestShardStore_ReleaseCAS(t *testing.T) {
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

func TestShardStore_BatchRelease(t *testing.T) {
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

	claimedB, err := shardStore.ClaimShard(ctx, shardB, "member-b", time.Minute)
	require.NoError(t, err)
	require.Equal(t, "member-b", claimedB.MemberID)
}
