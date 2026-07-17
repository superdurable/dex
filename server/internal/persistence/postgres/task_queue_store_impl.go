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
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/errors"

	p "github.com/superdurable/dex/server/internal/persistence"
)

type pgTaskQueueStore struct {
	pool *pgxpool.Pool
	cfg  *config.ResolvedPGStoreConfig
}

// NewTaskQueueStore opens a pool to the taskqueues database and returns a TaskQueueStore.
func NewTaskQueueStore(ctx context.Context, cfg *config.ResolvedPGStoreConfig) (p.TaskQueueStore, errors.CategorizedError) {
	pool, err := newPgxPool(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &pgTaskQueueStore{pool: pool, cfg: cfg}, nil
}

func (s *pgTaskQueueStore) Close() error { s.pool.Close(); return nil }

func (s *pgTaskQueueStore) GetTaskQueueInfo(ctx context.Context, namespace, taskQueueName string, partitionID int32) (*p.TaskQueueInfo, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.cfg.ShortOperationTimeout)
	defer cancel()

	md := &p.TaskQueueInfo{Namespace: namespace, TaskQueueName: taskQueueName, PartitionID: partitionID}
	err := s.pool.QueryRow(ctx,
		`SELECT range_id, ack_level, owner_member_id, owner_address, claimed_at FROM taskqueue
		 WHERE namespace=$1 AND queue_name=$2 AND partition_id=$3`,
		namespace, taskQueueName, partitionID).Scan(&md.RangeID, &md.AckLevel, &md.OwnerMemberID, &md.OwnerAddress, &md.ClaimedAt)
	if err != nil {
		if isNotFoundError(err) {
			return nil, errors.NewNotFoundError("task queue metadata not found", nil)
		}
		return nil, categorizeError(err, "GetTaskQueueMetadata")
	}
	return md, nil
}

// ClaimTaskQueue upserts the metadata row, incrementing range_id (fencing
// token). Any member may claim; the prior owner discovers loss on its next
// fenced write.
func (s *pgTaskQueueStore) ClaimTaskQueue(ctx context.Context, namespace, taskQueueName string, partitionID int32, memberID, matchingAddress string) (*p.TaskQueueInfo, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.cfg.ShortOperationTimeout)
	defer cancel()

	now := time.Now()
	var rangeID int32
	var ackLevel int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO taskqueue
		   (namespace, queue_name, partition_id, range_id, ack_level, owner_member_id, owner_address, claimed_at)
		 VALUES ($1,$2,$3,1,0,$4,$5,$6)
		 ON CONFLICT (namespace, queue_name, partition_id) DO UPDATE SET
		   range_id = taskqueue.range_id + 1,
		   owner_member_id = EXCLUDED.owner_member_id,
		   owner_address = EXCLUDED.owner_address,
		   claimed_at = EXCLUDED.claimed_at
		 RETURNING range_id, ack_level`,
		namespace, taskQueueName, partitionID, memberID, matchingAddress, now).Scan(&rangeID, &ackLevel)
	if err != nil {
		return nil, categorizeError(err, "ClaimTaskQueue")
	}
	return &p.TaskQueueInfo{
		Namespace:     namespace,
		TaskQueueName: taskQueueName,
		PartitionID:   partitionID,
		RangeID:       rangeID,
		AckLevel:      ackLevel,
		OwnerMemberID: memberID,
		OwnerAddress:  matchingAddress,
		ClaimedAt:     now,
	}, nil
}

func (s *pgTaskQueueStore) UpdateTaskQueueInfo(ctx context.Context, namespace, taskQueueName string, partitionID int32, rangeID int32, ackLevel int64) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.cfg.ShortOperationTimeout)
	defer cancel()

	ct, err := s.pool.Exec(ctx,
		`UPDATE taskqueue SET ack_level=$5, updated_at=$6
		 WHERE namespace=$1 AND queue_name=$2 AND partition_id=$3 AND range_id=$4`,
		namespace, taskQueueName, partitionID, rangeID, ackLevel, time.Now())
	if err != nil {
		return categorizeError(err, "UpdateTaskQueueMetadata")
	}
	if ct.RowsAffected() == 0 {
		return errors.NewConflictError(fmt.Sprintf(
			"range_id mismatch: task queue %s/%s/%d: expected range_id=%d",
			namespace, taskQueueName, partitionID, rangeID), nil)
	}
	return nil
}

// CreateTasks fences on range_id then batch-inserts task rows in one txn.
func (s *pgTaskQueueStore) CreateTasks(ctx context.Context, namespace, taskQueueName string, partitionID int32, rangeID int32, tasks []*p.TaskQueueTaskRow) errors.CategorizedError {
	if len(tasks) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.cfg.ShortOperationTimeout)
	defer cancel()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return categorizeError(err, "CreateTasks begin")
	}

	if catErr := createTasksInTx(ctx, tx, namespace, taskQueueName, partitionID, rangeID, tasks); catErr != nil {
		_ = tx.Rollback(ctx)
		return catErr
	}
	if err := tx.Commit(ctx); err != nil {
		_ = tx.Rollback(ctx)
		return categorizeError(err, "CreateTasks commit")
	}
	return nil
}

func createTasksInTx(ctx context.Context, tx pgx.Tx, namespace, taskQueueName string, partitionID int32, rangeID int32, tasks []*p.TaskQueueTaskRow) errors.CategorizedError {
	ct, err := tx.Exec(ctx,
		`UPDATE taskqueue SET updated_at=$5
		 WHERE namespace=$1 AND queue_name=$2 AND partition_id=$3 AND range_id=$4`,
		namespace, taskQueueName, partitionID, rangeID, time.Now())
	if err != nil {
		return categorizeError(err, "CreateTasks fence")
	}
	if ct.RowsAffected() == 0 {
		return errors.NewConflictError(fmt.Sprintf(
			"range_id mismatch: CreateTasks: task queue %s/%s/%d: expected range_id=%d",
			namespace, taskQueueName, partitionID, rangeID), nil)
	}

	now := time.Now()
	batch := &pgx.Batch{}
	for _, t := range tasks {
		createdAt := t.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		batch.Queue(
			`INSERT INTO taskqueue_tasks (namespace, queue_name, partition_id, task_id, run_id, shard_id, created_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			namespace, taskQueueName, partitionID, t.TaskID, t.RunID, t.ShardID, createdAt)
	}
	br := tx.SendBatch(ctx, batch)
	for range tasks {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return categorizeError(err, "CreateTasks insert")
		}
	}
	if err := br.Close(); err != nil {
		return categorizeError(err, "CreateTasks batch close")
	}
	return nil
}

func (s *pgTaskQueueStore) GetTasks(ctx context.Context, namespace, taskQueueName string, partitionID int32, readLevel, maxReadLevel int64, batchSize int) ([]*p.TaskQueueTaskRow, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.cfg.LongOperationTimeout)
	defer cancel()

	rows, err := s.pool.Query(ctx,
		`SELECT task_id, run_id, shard_id, created_at FROM taskqueue_tasks
		 WHERE namespace=$1 AND queue_name=$2 AND partition_id=$3 AND task_id>$4 AND task_id<=$5
		 ORDER BY task_id LIMIT $6`,
		namespace, taskQueueName, partitionID, readLevel, maxReadLevel, batchSize)
	if err != nil {
		return nil, categorizeError(err, "GetTasks")
	}
	defer rows.Close()

	var out []*p.TaskQueueTaskRow
	for rows.Next() {
		r := &p.TaskQueueTaskRow{Namespace: namespace, TaskQueueName: taskQueueName, PartitionID: partitionID}
		if err := rows.Scan(&r.TaskID, &r.RunID, &r.ShardID, &r.CreatedAt); err != nil {
			return nil, categorizeError(err, "GetTasks scan")
		}
		out = append(out, r)
	}
	return out, mapErrIfRows(rows, "GetTasks")
}

// DeleteTasksLessThan deletes every task with task_id <= ackLevel. The limit
// arg is intentionally ignored; the range is already bounded by ackLevel.
func (s *pgTaskQueueStore) DeleteTasksLessThan(ctx context.Context, namespace, taskQueueName string, partitionID int32, ackLevel int64, limit int) (int, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.cfg.LongOperationTimeout)
	defer cancel()

	ct, err := s.pool.Exec(ctx,
		`DELETE FROM taskqueue_tasks
		 WHERE namespace=$1 AND queue_name=$2 AND partition_id=$3 AND task_id<=$4`,
		namespace, taskQueueName, partitionID, ackLevel)
	if err != nil {
		return 0, categorizeError(err, "DeleteTasksLessThan")
	}
	return int(ct.RowsAffected()), nil
}

func (s *pgTaskQueueStore) DeleteTasksByIDBatch(ctx context.Context, namespace, taskQueueName string, partitionID int32, taskIDs []int64) errors.CategorizedError {
	if len(taskIDs) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.cfg.LongOperationTimeout)
	defer cancel()

	_, err := s.pool.Exec(ctx,
		`DELETE FROM taskqueue_tasks
		 WHERE namespace=$1 AND queue_name=$2 AND partition_id=$3 AND task_id = ANY($4)`,
		namespace, taskQueueName, partitionID, taskIDs)
	return categorizeError(err, "DeleteTasksByIDBatch")
}
