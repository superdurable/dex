// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
)

type pgTasklistStore struct {
	pool     *pgxpool.Pool
	timeouts OperationTimeouts
}

// NewTasklistStore opens a pool to the tasklists database.
func NewTasklistStore(ctx context.Context, cfg PoolConfig) (p.TasklistStore, errors.CategorizedError) {
	pool, err := newPool(ctx, cfg, defaultTasklistsDatabase)
	if err != nil {
		return nil, err
	}
	return &pgTasklistStore{pool: pool, timeouts: cfg.Timeouts}, nil
}

func (s *pgTasklistStore) Close() error { s.pool.Close(); return nil }

// ClaimTasklist upserts the metadata row, incrementing range_id (fencing
// token). Any member may claim; the prior owner discovers loss on its next
// fenced write.
func (s *pgTasklistStore) ClaimTasklist(ctx context.Context, namespace, tasklistName string, partitionID int32, memberID, matchingAddress string) (*p.TasklistMetadata, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	now := time.Now()
	var rangeID int32
	var ackLevel int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO tasklist_metadata
		   (namespace, tasklist_name, partition_id, range_id, ack_level, owner_member_id, owner_address, claimed_at)
		 VALUES ($1,$2,$3,1,0,$4,$5,$6)
		 ON CONFLICT (namespace, tasklist_name, partition_id) DO UPDATE SET
		   range_id = tasklist_metadata.range_id + 1,
		   owner_member_id = EXCLUDED.owner_member_id,
		   owner_address = EXCLUDED.owner_address,
		   claimed_at = EXCLUDED.claimed_at
		 RETURNING range_id, ack_level`,
		namespace, tasklistName, partitionID, memberID, matchingAddress, now).Scan(&rangeID, &ackLevel)
	if err != nil {
		return nil, mapError(err, "ClaimTasklist")
	}
	return &p.TasklistMetadata{
		Namespace:     namespace,
		TasklistName:  tasklistName,
		PartitionID:   partitionID,
		RangeID:       rangeID,
		AckLevel:      ackLevel,
		OwnerMemberID: memberID,
		OwnerAddress:  matchingAddress,
		ClaimedAt:     now,
	}, nil
}

func (s *pgTasklistStore) UpdateTasklistMetadata(ctx context.Context, namespace, tasklistName string, partitionID int32, rangeID int32, ackLevel int64) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	ct, err := s.pool.Exec(ctx,
		`UPDATE tasklist_metadata SET ack_level=$5, updated_at=$6
		 WHERE namespace=$1 AND tasklist_name=$2 AND partition_id=$3 AND range_id=$4`,
		namespace, tasklistName, partitionID, rangeID, ackLevel, time.Now())
	if err != nil {
		return mapError(err, "UpdateTasklistMetadata")
	}
	if ct.RowsAffected() == 0 {
		return p.NewRangeIDMismatchError(fmt.Sprintf("tasklist %s/%s/%d: expected range_id=%d", namespace, tasklistName, partitionID, rangeID))
	}
	return nil
}

// CreateTasks fences on range_id then batch-inserts task rows in one txn.
func (s *pgTasklistStore) CreateTasks(ctx context.Context, namespace, tasklistName string, partitionID int32, rangeID int32, tasks []*p.TasklistTaskRow) errors.CategorizedError {
	if len(tasks) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapError(err, "CreateTasks begin")
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx,
		`UPDATE tasklist_metadata SET updated_at=$5
		 WHERE namespace=$1 AND tasklist_name=$2 AND partition_id=$3 AND range_id=$4`,
		namespace, tasklistName, partitionID, rangeID, time.Now())
	if err != nil {
		return mapError(err, "CreateTasks fence")
	}
	if ct.RowsAffected() == 0 {
		return p.NewRangeIDMismatchError(fmt.Sprintf("CreateTasks: tasklist %s/%s/%d: expected range_id=%d", namespace, tasklistName, partitionID, rangeID))
	}

	batch := &pgx.Batch{}
	for _, t := range tasks {
		batch.Queue(
			`INSERT INTO tasklist_tasks (namespace, tasklist_name, partition_id, task_id, run_id, shard_id, created_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			namespace, tasklistName, partitionID, t.TaskID, t.RunID, t.ShardID, t.CreatedAt)
	}
	br := tx.SendBatch(ctx, batch)
	for range tasks {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return mapError(err, "CreateTasks insert")
		}
	}
	if err := br.Close(); err != nil {
		return mapError(err, "CreateTasks batch close")
	}
	if err := tx.Commit(ctx); err != nil {
		return mapError(err, "CreateTasks commit")
	}
	return nil
}

func (s *pgTasklistStore) GetTasks(ctx context.Context, namespace, tasklistName string, partitionID int32, readLevel, maxReadLevel int64, batchSize int) ([]*p.TasklistTaskRow, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	rows, err := s.pool.Query(ctx,
		`SELECT task_id, run_id, shard_id, created_at FROM tasklist_tasks
		 WHERE namespace=$1 AND tasklist_name=$2 AND partition_id=$3 AND task_id>$4 AND task_id<=$5
		 ORDER BY task_id LIMIT $6`,
		namespace, tasklistName, partitionID, readLevel, maxReadLevel, batchSize)
	if err != nil {
		return nil, mapError(err, "GetTasks")
	}
	defer rows.Close()

	var out []*p.TasklistTaskRow
	for rows.Next() {
		r := &p.TasklistTaskRow{Namespace: namespace, TasklistName: tasklistName, PartitionID: partitionID}
		if err := rows.Scan(&r.TaskID, &r.RunID, &r.ShardID, &r.CreatedAt); err != nil {
			return nil, mapError(err, "GetTasks scan")
		}
		out = append(out, r)
	}
	return out, mapErrIfRows(rows, "GetTasks")
}

// DeleteTasksLessThan deletes every task with task_id <= ackLevel. The limit
// arg is intentionally ignored (mirror Mongo); the range is bounded by ackLevel.
func (s *pgTasklistStore) DeleteTasksLessThan(ctx context.Context, namespace, tasklistName string, partitionID int32, ackLevel int64, limit int) (int, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	ct, err := s.pool.Exec(ctx,
		`DELETE FROM tasklist_tasks
		 WHERE namespace=$1 AND tasklist_name=$2 AND partition_id=$3 AND task_id<=$4`,
		namespace, tasklistName, partitionID, ackLevel)
	if err != nil {
		return 0, mapError(err, "DeleteTasksLessThan")
	}
	return int(ct.RowsAffected()), nil
}

func (s *pgTasklistStore) DeleteTasksByIDBatch(ctx context.Context, namespace, tasklistName string, partitionID int32, taskIDs []int64) errors.CategorizedError {
	if len(taskIDs) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	_, err := s.pool.Exec(ctx,
		`DELETE FROM tasklist_tasks
		 WHERE namespace=$1 AND tasklist_name=$2 AND partition_id=$3 AND task_id = ANY($4)`,
		namespace, tasklistName, partitionID, taskIDs)
	return mapError(err, "DeleteTasksByIDBatch")
}

func (s *pgTasklistStore) GetTasklistMetadata(ctx context.Context, namespace, tasklistName string, partitionID int32) (*p.TasklistMetadata, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	md := &p.TasklistMetadata{Namespace: namespace, TasklistName: tasklistName, PartitionID: partitionID}
	err := s.pool.QueryRow(ctx,
		`SELECT range_id, ack_level, owner_member_id, owner_address, claimed_at FROM tasklist_metadata
		 WHERE namespace=$1 AND tasklist_name=$2 AND partition_id=$3`,
		namespace, tasklistName, partitionID).Scan(&md.RangeID, &md.AckLevel, &md.OwnerMemberID, &md.OwnerAddress, &md.ClaimedAt)
	if err != nil {
		if isNoRows(err) {
			return nil, p.NewNotFoundError("tasklist metadata not found")
		}
		return nil, mapError(err, "GetTasklistMetadata")
	}
	return md, nil
}
