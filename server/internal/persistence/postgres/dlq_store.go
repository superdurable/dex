// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
)

type pgDLQStore struct {
	pool     *pgxpool.Pool
	timeouts OperationTimeouts
}

// NewDLQStore opens a pool to the runs database (DLQ is co-located with runs).
func NewDLQStore(ctx context.Context, cfg PoolConfig) (p.DLQStore, errors.CategorizedError) {
	pool, err := newPool(ctx, cfg, defaultRunsDatabase)
	if err != nil {
		return nil, err
	}
	return &pgDLQStore{pool: pool, timeouts: cfg.Timeouts}, nil
}

func (s *pgDLQStore) Close() error { s.pool.Close(); return nil }

// WriteDLQ inserts one DLQ entry. Idempotent on (shard_id, task_id): a
// duplicate from a lease-handoff race is a silent no-op (the first record is
// authoritative — mirror Mongo).
func (s *pgDLQStore) WriteDLQ(ctx context.Context, entry *p.DLQEntry) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	_, err := s.pool.Exec(ctx,
		`INSERT INTO task_dlq
		   (shard_id, task_id, queue_type, task_type, run_id, namespace, task_list_name,
		    sort_key, error, error_category, created_at, dlq_at, member_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		 ON CONFLICT (shard_id, task_id) DO NOTHING`,
		entry.ShardID, entry.TaskID, int32(entry.QueueType), entry.TaskType, entry.RunID,
		entry.Namespace, entry.TaskListName, entry.SortKey, entry.Error, entry.ErrorCategory,
		entry.CreatedAt, time.Now(), entry.MemberID)
	return mapError(err, "WriteDLQ")
}
