// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
)

type pgRunStore struct {
	pool     *pgxpool.Pool
	timeouts OperationTimeouts
}

// NewRunStore opens a pool to the runs database and returns a RunStore.
func NewRunStore(ctx context.Context, cfg PoolConfig) (p.RunStore, errors.CategorizedError) {
	pool, err := newPool(ctx, cfg, defaultRunsDatabase)
	if err != nil {
		return nil, err
	}
	return &pgRunStore{pool: pool, timeouts: cfg.Timeouts}, nil
}

func (s *pgRunStore) Close() error { s.pool.Close(); return nil }

// runColumns lists every run column in a fixed order, shared by SELECT + scan.
const runColumns = `shard_id, namespace, id, flow_type, task_list_name, status, version, worker_id,
	state_map, unconsumed_channel_messages, step_exe_id_counters, active_step_executions,
	step_method_exe_counter, worker_request_counter, external_channel_message_counter, last_heartbeat_time, heartbeat_timer_id,
	active_durable_timer_id, durable_timer_fire_at, durable_timer_fired,
	last_history_event_id, created_at, updated_at`

func (s *pgRunStore) CreateRunWithTasks(ctx context.Context, run *p.RunRow, tasks []p.TaskRow) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	run.RowType = p.RowTypeRun
	run.SortKey = 0
	run.Version = 1
	run.CreatedAt = time.Now()
	run.UpdatedAt = run.CreatedAt

	return s.inTx(ctx, func(tx pgx.Tx) errors.CategorizedError {
		if err := insertRunRow(ctx, tx, run); err != nil {
			return err
		}
		return insertTasks(ctx, tx, tasks)
	})
}

func (s *pgRunStore) GetRun(ctx context.Context, shardID int32, namespace, runID string, opts p.GetRunOptions) (*p.RunRow, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	// ReadPreference is a no-op on a single Postgres primary; reads are
	// always consistent. (A read-replica pool could honor it later.)
	row := s.pool.QueryRow(ctx,
		`SELECT `+runColumns+` FROM runs WHERE shard_id=$1 AND namespace=$2 AND id=$3`,
		shardID, namespace, runID)
	run, err := scanRunRow(row)
	if err != nil {
		if isNoRows(err) {
			return nil, p.NewNotFoundError("run not found: " + runID)
		}
		return nil, mapError(err, "GetRun")
	}
	return run, nil
}

func (s *pgRunStore) UpdateRunWithNewTasks(ctx context.Context, shardID int32, namespace, runID string,
	expectedVersion int64, update *p.RunRowUpdate, newTasks []p.TaskRow) errors.CategorizedError {

	ctx, cancel := cappedCtx(ctx, s.timeouts.Short)
	defer cancel()

	return s.inTx(ctx, func(tx pgx.Tx) errors.CategorizedError {
		// Single optimistic CAS UPDATE: the partial-update deltas are applied
		// in SQL via JSONB operators (no prior read, no FOR UPDATE lock). A
		// concurrent writer that bumped the version makes this match 0 rows →
		// version mismatch (mirrors Mongo's UpdateOne MatchedCount==0, which
		// conflates not-found and stale-version). The WHERE clause params
		// follow the SET params.
		setSQL, args, encErr := buildRunUpdateSet(update)
		if encErr != nil {
			return p.NewInternalError("encode run update", encErr)
		}
		args = append(args, shardID, namespace, runID, expectedVersion)
		n := len(args)
		query := fmt.Sprintf(
			`UPDATE runs SET %s WHERE shard_id=$%d AND namespace=$%d AND id=$%d AND version=$%d`,
			setSQL, n-3, n-2, n-1, n)

		ct, execErr := tx.Exec(ctx, query, args...)
		if execErr != nil {
			return mapError(execErr, "UpdateRunWithNewTasks")
		}
		if ct.RowsAffected() == 0 {
			return p.NewVersionMismatchError("run " + runID)
		}
		return insertTasks(ctx, tx, newTasks)
	})
}

// --- Immediate task range read/delete ---

func (s *pgRunStore) RangeReadImmediateTasks(ctx context.Context, shardID int32, afterSeq int64, limit int) ([]*p.ImmediateTaskRow, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	rows, err := s.pool.Query(ctx,
		`SELECT shard_id, sort_key, id, task_type, task_info, created_at FROM immediate_tasks
		 WHERE shard_id=$1 AND sort_key>$2 ORDER BY sort_key, id LIMIT $3`,
		shardID, afterSeq, limit)
	if err != nil {
		return nil, mapError(err, "RangeReadImmediateTasks")
	}
	defer rows.Close()

	var out []*p.ImmediateTaskRow
	for rows.Next() {
		var (
			r        p.ImmediateTaskRow
			taskType int32
			infoJSON []byte
		)
		if err := rows.Scan(&r.ShardID, &r.SortKey, &r.ID, &taskType, &infoJSON, &r.CreatedAt); err != nil {
			return nil, mapError(err, "RangeReadImmediateTasks scan")
		}
		r.RowType = p.RowTypeImmediateTask
		r.TaskType = p.ImmediateTaskType(taskType)
		if err := unmarshalJSON(infoJSON, &r.TaskInfo); err != nil {
			return nil, p.NewInternalError("decode immediate task_info", err)
		}
		out = append(out, &r)
	}
	return out, mapErrIfRows(rows, "RangeReadImmediateTasks")
}

func (s *pgRunStore) RangeDeleteImmediateTasks(ctx context.Context, shardID int32, upToSeq int64) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()
	_, err := s.pool.Exec(ctx, `DELETE FROM immediate_tasks WHERE shard_id=$1 AND sort_key<=$2`, shardID, upToSeq)
	return mapError(err, "RangeDeleteImmediateTasks")
}

// --- Timer task range read/delete ---

func (s *pgRunStore) RangeReadTimerTasks(ctx context.Context, shardID int32, sortKeyUpTo int64, afterSortKey int64, afterID ids.TaskID, limit int) ([]*p.TimerTaskRow, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	rows, err := s.pool.Query(ctx,
		`SELECT shard_id, sort_key, id, task_type, task_info, created_at FROM timer_tasks
		 WHERE shard_id=$1 AND sort_key<=$2
		   AND ($3 OR (sort_key, id) > ($4, $5))
		 ORDER BY sort_key, id LIMIT $6`,
		shardID, sortKeyUpTo, afterID.IsZero(), afterSortKey, afterID, limit)
	if err != nil {
		return nil, mapError(err, "RangeReadTimerTasks")
	}
	defer rows.Close()

	var out []*p.TimerTaskRow
	for rows.Next() {
		var (
			r        p.TimerTaskRow
			taskType int32
			infoJSON []byte
		)
		if err := rows.Scan(&r.ShardID, &r.SortKey, &r.ID, &taskType, &infoJSON, &r.CreatedAt); err != nil {
			return nil, mapError(err, "RangeReadTimerTasks scan")
		}
		r.RowType = p.RowTypeTimerTask
		r.TaskType = p.TimerTaskType(taskType)
		if err := unmarshalJSON(infoJSON, &r.TaskInfo); err != nil {
			return nil, p.NewInternalError("decode timer task_info", err)
		}
		out = append(out, &r)
	}
	return out, mapErrIfRows(rows, "RangeReadTimerTasks")
}

func (s *pgRunStore) RangeDeleteTimerTasks(ctx context.Context, shardID int32, upToSortKey int64, upToID ids.TaskID) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()
	// Exclusive upper bound: (sort_key, id) < (upToSortKey, upToID).
	_, err := s.pool.Exec(ctx,
		`DELETE FROM timer_tasks WHERE shard_id=$1 AND (sort_key, id) < ($2, $3)`,
		shardID, upToSortKey, upToID)
	return mapError(err, "RangeDeleteTimerTasks")
}

// --- OpsFIFO task range read/delete ---

func (s *pgRunStore) RangeReadOpsFIFOTasks(ctx context.Context, shardID int32, afterSeq int64, limit int) ([]*p.OpsFIFOTaskRow, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	rows, err := s.pool.Query(ctx,
		`SELECT shard_id, sort_key, id, task_type, payload, created_at FROM opsfifo_tasks
		 WHERE shard_id=$1 AND sort_key>$2 ORDER BY sort_key, id LIMIT $3`,
		shardID, afterSeq, limit)
	if err != nil {
		return nil, mapError(err, "RangeReadOpsFIFOTasks")
	}
	defer rows.Close()

	var out []*p.OpsFIFOTaskRow
	for rows.Next() {
		var (
			r        p.OpsFIFOTaskRow
			taskType int32
			payload  []byte
		)
		if err := rows.Scan(&r.ShardID, &r.SortKey, &r.ID, &taskType, &payload, &r.CreatedAt); err != nil {
			return nil, mapError(err, "RangeReadOpsFIFOTasks scan")
		}
		r.RowType = p.RowTypeOpsFIFOTask
		r.TaskType = p.OpsFIFOTaskType(taskType)
		hist, vis, decErr := decodeOpsFIFOPayload(r.TaskType, payload)
		if decErr != nil {
			return nil, p.NewInternalError("decode OpsFIFO payload", decErr)
		}
		r.HistoryPayload = hist
		r.VisibilityPayload = vis
		out = append(out, &r)
	}
	return out, mapErrIfRows(rows, "RangeReadOpsFIFOTasks")
}

func (s *pgRunStore) RangeDeleteOpsFIFOTasks(ctx context.Context, shardID int32, upToSeq int64) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()
	_, err := s.pool.Exec(ctx, `DELETE FROM opsfifo_tasks WHERE shard_id=$1 AND sort_key<=$2`, shardID, upToSeq)
	return mapError(err, "RangeDeleteOpsFIFOTasks")
}

// --- Delete-by-id batch (shutdown path) ---

func (s *pgRunStore) DeleteImmediateTasksByIDBatch(ctx context.Context, shardID int32, taskIDs []ids.TaskID) errors.CategorizedError {
	return s.deleteTasksByID(ctx, "immediate_tasks", shardID, taskIDs)
}
func (s *pgRunStore) DeleteTimerTasksByIDBatch(ctx context.Context, shardID int32, taskIDs []ids.TaskID) errors.CategorizedError {
	return s.deleteTasksByID(ctx, "timer_tasks", shardID, taskIDs)
}
func (s *pgRunStore) DeleteOpsFIFOTasksByIDBatch(ctx context.Context, shardID int32, taskIDs []ids.TaskID) errors.CategorizedError {
	return s.deleteTasksByID(ctx, "opsfifo_tasks", shardID, taskIDs)
}

func (s *pgRunStore) deleteTasksByID(ctx context.Context, table string, shardID int32, taskIDs []ids.TaskID) errors.CategorizedError {
	if len(taskIDs) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()
	taskIDStrings := make([]string, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		taskIDStrings = append(taskIDStrings, taskID.String())
	}
	_, err := s.pool.Exec(ctx,
		`DELETE FROM `+table+` WHERE shard_id=$1 AND id = ANY($2::uuid[])`, shardID, taskIDStrings)
	return mapError(err, "DeleteTasksByIDBatch")
}

func (s *pgRunStore) DeleteAll(ctx context.Context) error {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()
	_, err := s.pool.Exec(ctx, `TRUNCATE runs, immediate_tasks, timer_tasks, opsfifo_tasks, task_dlq`)
	return err
}

// --- tx helper ---

func (s *pgRunStore) inTx(ctx context.Context, fn func(pgx.Tx) errors.CategorizedError) errors.CategorizedError {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapError(err, "begin tx")
	}
	if catErr := fn(tx); catErr != nil {
		_ = tx.Rollback(ctx)
		return catErr
	}
	if err := tx.Commit(ctx); err != nil {
		_ = tx.Rollback(ctx)
		return mapError(err, "commit tx")
	}
	return nil
}
