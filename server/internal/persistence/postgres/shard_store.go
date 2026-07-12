// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
)

type pgShardStore struct {
	pool     *pgxpool.Pool
	timeouts OperationTimeouts
}

// NewShardStore opens a pool to the shards database and returns a ShardStore.
func NewShardStore(ctx context.Context, cfg PoolConfig) (p.ShardStore, errors.CategorizedError) {
	pool, err := newPool(ctx, cfg, defaultShardsDatabase)
	if err != nil {
		return nil, err
	}
	return &pgShardStore{pool: pool, timeouts: cfg.Timeouts}, nil
}

func (s *pgShardStore) Close() error { s.pool.Close(); return nil }

// maxClaimCASRetries bounds the optimistic-CAS retry loop when a concurrent
// claim/renew changes the row's version between our read and write.
const maxClaimCASRetries = 5

// ClaimShard claims a shard using optimistic concurrency (mirroring the Mongo
// backend's findOneAndUpdate). It deliberately does NOT take a row lock
// (SELECT ... FOR UPDATE): a lock would block behind the current owner's
// lease-renewal UPDATE on the same row, making a contending node read a stale
// released state and lose the claim race during rebalance.
func (s *pgShardStore) ClaimShard(ctx context.Context, shardID int32, memberID string, leaseDuration time.Duration) (*p.Shard, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	for attempt := 0; ; attempt++ {
		shard, retry, err := s.tryClaimShard(ctx, shardID, memberID, leaseDuration)
		if err != nil {
			return nil, err
		}
		if !retry {
			return shard, nil
		}
		if attempt >= maxClaimCASRetries {
			return nil, p.NewVersionMismatchError(fmt.Sprintf("shard %d: claim CAS retries exhausted", shardID))
		}
	}
}

// tryClaimShard performs one optimistic claim attempt. retry=true means a
// concurrent version change lost the CAS and the caller should retry.
func (s *pgShardStore) tryClaimShard(ctx context.Context, shardID int32, memberID string, leaseDuration time.Duration) (shard *p.Shard, retry bool, err errors.CategorizedError) {
	now := time.Now()
	leaseExpiry := now.Add(leaseDuration)

	var (
		version  int64
		member   string
		leaseExp time.Time
		released *time.Time
		metaJSON []byte
	)
	scanErr := s.pool.QueryRow(ctx,
		`SELECT version, member_id, lease_expires_at, released_at, metadata FROM shards WHERE shard_id=$1`,
		shardID).Scan(&version, &member, &leaseExp, &released, &metaJSON)

	if isNoRows(scanErr) {
		meta := p.ShardMetadata{RangeID: 1}
		mb, _ := json.Marshal(meta)
		if _, insErr := s.pool.Exec(ctx,
			`INSERT INTO shards (shard_id, version, member_id, claimed_at, lease_expires_at, released_at, metadata)
			 VALUES ($1,$2,$3,$4,$5,NULL,$6)`,
			shardID, int64(1), memberID, now, leaseExpiry, mb); insErr != nil {
			// Lost the insert race; retry through the existing-row path.
			if catErr := mapError(insErr, "ClaimShard insert"); catErr.IsConflictError() {
				return nil, true, nil
			} else {
				return nil, false, catErr
			}
		}
		return &p.Shard{ShardID: shardID, Version: 1, MemberID: memberID, ClaimedAt: now, LeaseExpiresAt: leaseExpiry, Metadata: meta}, false, nil
	}
	if scanErr != nil {
		return nil, false, mapError(scanErr, "ClaimShard read")
	}

	// Lease still held by a different member → not claimable yet.
	if released == nil && leaseExp.After(now) && member != memberID {
		return nil, false, p.NewLeaseNotExpiredError(fmt.Sprintf("shard %d owned by %s, lease expires at %v", shardID, member, leaseExp))
	}

	var meta p.ShardMetadata
	if jErr := json.Unmarshal(metaJSON, &meta); jErr != nil {
		return nil, false, p.NewInternalError("ClaimShard decode metadata", jErr)
	}
	meta.RangeID++
	newVersion := version + 1
	mb, _ := json.Marshal(meta)
	ct, updErr := s.pool.Exec(ctx,
		`UPDATE shards SET version=$2, member_id=$3, claimed_at=$4, lease_expires_at=$5, released_at=NULL, metadata=$6
		 WHERE shard_id=$1 AND version=$7`,
		shardID, newVersion, memberID, now, leaseExpiry, mb, version)
	if updErr != nil {
		return nil, false, mapError(updErr, "ClaimShard update")
	}
	if ct.RowsAffected() == 0 {
		// Concurrent claim/renew bumped the version; retry.
		return nil, true, nil
	}
	return &p.Shard{ShardID: shardID, Version: newVersion, MemberID: memberID, ClaimedAt: now, LeaseExpiresAt: leaseExpiry, Metadata: meta}, false, nil
}

func (s *pgShardStore) RenewShardLease(ctx context.Context, shardID int32, memberID string, expectedVersion int64, leaseDuration time.Duration, metadata *p.ShardMetadata) (time.Time, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	leaseExpiry := time.Now().Add(leaseDuration)

	// Merge only the committed-offset fields into the existing metadata via
	// jsonb concatenation so range_id is preserved.
	var tag interface{ RowsAffected() int64 }
	if metadata != nil {
		committed, _ := json.Marshal(map[string]any{
			"immediate_task_committed_seq":  metadata.ImmediateTaskCommittedSeq,
			"timer_task_committed_sort_key": metadata.TimerTaskCommittedSortKey,
			"timer_task_committed_id":       metadata.TimerTaskCommittedID,
			"ops_fifo_task_committed_seq":   metadata.OpsFIFOTaskCommittedSeq,
		})
		ct, err := s.pool.Exec(ctx,
			`UPDATE shards SET lease_expires_at=$2, metadata = metadata || $3::jsonb
			 WHERE shard_id=$1 AND version=$4 AND member_id=$5`,
			shardID, leaseExpiry, committed, expectedVersion, memberID)
		if err != nil {
			return time.Time{}, mapError(err, "RenewShardLease")
		}
		tag = ct
	} else {
		ct, err := s.pool.Exec(ctx,
			`UPDATE shards SET lease_expires_at=$2 WHERE shard_id=$1 AND version=$3 AND member_id=$4`,
			shardID, leaseExpiry, expectedVersion, memberID)
		if err != nil {
			return time.Time{}, mapError(err, "RenewShardLease")
		}
		tag = ct
	}
	if tag.RowsAffected() == 0 {
		return time.Time{}, p.NewVersionMismatchError("shard lease renewal: version or member mismatch")
	}
	return leaseExpiry, nil
}

func (s *pgShardStore) ReleaseShard(ctx context.Context, shardID int32, memberID string, expectedVersion int64) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	ct, err := s.pool.Exec(ctx,
		`UPDATE shards SET released_at=$2 WHERE shard_id=$1 AND version=$3 AND member_id=$4`,
		shardID, time.Now(), expectedVersion, memberID)
	if err != nil {
		return mapError(err, "ReleaseShard")
	}
	if ct.RowsAffected() == 0 {
		return p.NewVersionMismatchError("shard release: version or member mismatch")
	}
	return nil
}

func (s *pgShardStore) BatchReleaseShards(ctx context.Context, memberID string, entries []p.ShardReleaseEntry) errors.CategorizedError {
	if len(entries) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	now := time.Now()
	batch := &pgx.Batch{}
	for _, e := range entries {
		batch.Queue(
			`UPDATE shards SET released_at=$2 WHERE shard_id=$1 AND version=$3 AND member_id=$4`,
			e.ShardID, now, e.ExpectedVersion, memberID)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range entries {
		// Best-effort (mirror Mongo unordered bulk): ignore per-row mismatch,
		// surface only hard DB errors.
		if _, err := br.Exec(); err != nil {
			return mapError(err, "BatchReleaseShards")
		}
	}
	return nil
}
