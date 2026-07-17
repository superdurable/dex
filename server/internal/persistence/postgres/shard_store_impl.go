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

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/errors"

	p "github.com/superdurable/dex/server/internal/persistence"
)

type pgShardStore struct {
	pool *pgxpool.Pool
	cfg  *config.ResolvedPGStoreConfig
}

// NewShardStore opens a pool to the shards database and returns a ShardStore.
func NewShardStore(ctx context.Context, cfg *config.ResolvedPGStoreConfig) (p.ShardStore, errors.CategorizedError) {
	pool, err := newPgxPool(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &pgShardStore{pool: pool, cfg: cfg}, nil
}

func (s *pgShardStore) Close() error { s.pool.Close(); return nil }

func (s *pgShardStore) ClaimShard(ctx context.Context, shardID int32, memberID string, leaseDuration time.Duration) (*p.Shard, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.cfg.ShortOperationTimeout)
	defer cancel()
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

	if isNotFoundError(scanErr) {
		return s.insertFirstClaim(ctx, shardID, memberID, now, leaseExpiry)
	}
	if scanErr != nil {
		return nil, categorizeError(scanErr, "ClaimShard read")
	}

	// Lease still held by a different member → not claimable yet.
	if released == nil && leaseExp.After(now) && member != memberID {
		return nil, errors.NewConflictError(fmt.Sprintf("shard %d owned by %s, lease expires at %v", shardID, member, leaseExp), nil)
	}

	var meta p.ShardMetadata
	if decodeErr := json.Unmarshal(metaJSON, &meta); decodeErr != nil {
		return nil, errors.NewInternalError("ClaimShard decode metadata", decodeErr)
	}
	meta.RangeID++
	newVersion := version + 1
	metaBytes, encErr := json.Marshal(meta)
	if encErr != nil {
		return nil, errors.NewInternalError("ClaimShard encode metadata", encErr)
	}
	ct, updErr := s.pool.Exec(ctx,
		`UPDATE shards SET version=$2, member_id=$3, claimed_at=$4, lease_expires_at=$5, released_at=NULL, metadata=$6
		 WHERE shard_id=$1 AND version=$7`,
		shardID, newVersion, memberID, now, leaseExpiry, metaBytes, version)
	if updErr != nil {
		return nil, categorizeError(updErr, "ClaimShard update")
	}
	if ct.RowsAffected() == 0 {
		// Concurrent claim/renew bumped the version; retry.
		return nil, errors.NewCASError("shard is claimed by another instance", nil)
	}
	return &p.Shard{ShardID: shardID, Version: newVersion, MemberID: memberID, ClaimedAt: now, LeaseExpiresAt: leaseExpiry, Metadata: meta}, nil
}

func (s *pgShardStore) insertFirstClaim(ctx context.Context, shardID int32, memberID string, now, leaseExpiry time.Time) (*p.Shard, errors.CategorizedError) {
	meta := p.ShardMetadata{RangeID: 1}
	metaBytes, encErr := json.Marshal(meta)
	if encErr != nil {
		return nil, errors.NewInternalError("ClaimShard encode metadata", encErr)
	}
	// A racing claimant hits the unique constraint → categorized as ConflictError.
	_, insErr := s.pool.Exec(ctx,
		`INSERT INTO shards (shard_id, version, member_id, claimed_at, lease_expires_at, released_at, metadata)
		 VALUES ($1,$2,$3,$4,$5,NULL,$6)`,
		shardID, int64(1), memberID, now, leaseExpiry, metaBytes)
	if insErr != nil {
		return nil, categorizeError(insErr, "ClaimShard insert")
	}
	return &p.Shard{ShardID: shardID, Version: 1, MemberID: memberID, ClaimedAt: now, LeaseExpiresAt: leaseExpiry, Metadata: meta}, nil
}

func (s *pgShardStore) RenewShardLease(ctx context.Context, shardID int32, memberID string, expectedVersion int64, leaseDuration time.Duration, metadata *p.ShardMetadata) (time.Time, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.cfg.ShortOperationTimeout)
	defer cancel()

	leaseExpiry := time.Now().Add(leaseDuration)

	var (
		ct      pgconn.CommandTag
		execErr error
	)
	if metadata != nil {
		// Merge only the committed watermarks so a concurrent claim's range_id survives.
		committed, encErr := json.Marshal(map[string]any{
			"immediate_task_committed_seq":  metadata.ImmediateTaskCommittedSeq,
			"timer_task_committed_sort_key": metadata.TimerTaskCommittedSortKey,
			"timer_task_committed_id":       metadata.TimerTaskCommittedID,
		})
		if encErr != nil {
			return time.Time{}, errors.NewInternalError("RenewShardLease encode metadata", encErr)
		}
		ct, execErr = s.pool.Exec(ctx,
			`UPDATE shards SET lease_expires_at=$2, metadata = metadata || $3::jsonb
			 WHERE shard_id=$1 AND version=$4 AND member_id=$5`,
			shardID, leaseExpiry, committed, expectedVersion, memberID)
	} else {
		ct, execErr = s.pool.Exec(ctx,
			`UPDATE shards SET lease_expires_at=$2 WHERE shard_id=$1 AND version=$3 AND member_id=$4`,
			shardID, leaseExpiry, expectedVersion, memberID)
	}
	if execErr != nil {
		return time.Time{}, categorizeError(execErr, "RenewShardLease")
	}
	if ct.RowsAffected() == 0 {
		return time.Time{}, errors.NewCASError("shard lease renewal: version or member mismatch", nil)
	}
	return leaseExpiry, nil
}

func (s *pgShardStore) ReleaseShard(ctx context.Context, shardID int32, memberID string, expectedVersion int64) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.cfg.ShortOperationTimeout)
	defer cancel()

	ct, err := s.pool.Exec(ctx,
		`UPDATE shards SET released_at=$2 WHERE shard_id=$1 AND version=$3 AND member_id=$4`,
		shardID, time.Now(), expectedVersion, memberID)
	if err != nil {
		return categorizeError(err, "ReleaseShard")
	}
	if ct.RowsAffected() == 0 {
		return errors.NewCASError("shard release: version or member mismatch", nil)
	}
	return nil
}

func (s *pgShardStore) BatchReleaseShards(ctx context.Context, memberID string, entries []p.ShardReleaseEntry) errors.CategorizedError {
	if len(entries) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.cfg.LongOperationTimeout)
	defer cancel()

	now := time.Now()
	batch := &pgx.Batch{}
	for _, entry := range entries {
		batch.Queue(
			`UPDATE shards SET released_at=$2 WHERE shard_id=$1 AND version=$3 AND member_id=$4`,
			entry.ShardID, now, entry.ExpectedVersion, memberID)
	}
	batchResults := s.pool.SendBatch(ctx, batch)
	defer batchResults.Close()
	// Best-effort release: a version/member mismatch means another member already
	// took the shard, so RowsAffected==0 is not treated as an error here.
	for range entries {
		if _, err := batchResults.Exec(); err != nil {
			return categorizeError(err, "BatchReleaseShards")
		}
	}
	return nil
}
