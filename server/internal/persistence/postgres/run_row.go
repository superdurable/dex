// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/utils/ids"
	p "github.com/superdurable/dex/server/internal/persistence"
)

func unmarshalJSON(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

func mapErrIfRows(rows pgx.Rows, msg string) errors.CategorizedError {
	if err := rows.Err(); err != nil {
		return mapError(err, msg)
	}
	return nil
}

// nilIfZeroTime maps a zero time.Time to nil so a nullable TIMESTAMPTZ column
// stores SQL NULL rather than the year-1 sentinel.
func nilIfZeroTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// scanRunRow scans a runs row in runColumns order into a RunRow.
func scanRunRow(row pgx.Row) (*p.RunRow, error) {
	var (
		r         p.RunRow
		status    int32
		stateJSON []byte
		ucmJSON   []byte
		cntsJSON  []byte
		aseJSON   []byte
		lastHB    *time.Time
	)
	if err := row.Scan(
		&r.ShardID, &r.Namespace, &r.ID, &r.FlowType, &r.TaskListName, &status, &r.Version, &r.WorkerID,
		&stateJSON, &ucmJSON, &cntsJSON, &aseJSON,
		&r.StepMethodExeCounter, &r.WorkerRequestCounter, &r.ExternalChannelMessageCounter, &lastHB, &r.HeartbeatTimerID,
		&r.ActiveDurableTimerID, &r.DurableTimerFireAt, &r.DurableTimerFired,
		&r.LastHistoryEventID, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return nil, err
	}
	r.RowType = p.RowTypeRun
	r.Status = p.RunStatus(status)
	if lastHB != nil {
		r.LastHeartbeatTime = *lastHB
	}
	if err := unmarshalJSON(stateJSON, &r.StateMap); err != nil {
		return nil, err
	}
	if err := unmarshalJSON(ucmJSON, &r.UnconsumedChannelMessages); err != nil {
		return nil, err
	}
	if err := unmarshalJSON(cntsJSON, &r.StepExeIDCounters); err != nil {
		return nil, err
	}
	if err := unmarshalJSON(aseJSON, &r.ActiveStepExecutions); err != nil {
		return nil, err
	}
	return &r, nil
}

// jsonbObj marshals a map to JSON, substituting "{}" for a nil/empty map so
// the column holds a JSON object (not the scalar `null`). This is required so
// later partial updates can use object operators (`||`, `jsonb_set`) which
// error with "cannot set path in scalar" on a JSON null.
func jsonbObj[K comparable, V any](m map[K]V) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

// runRowArgs returns the INSERT/UPDATE args in runColumns order.
func runRowArgs(r *p.RunRow) ([]any, error) {
	stateJSON, err := jsonbObj(r.StateMap)
	if err != nil {
		return nil, err
	}
	ucmJSON, err := jsonbObj(r.UnconsumedChannelMessages)
	if err != nil {
		return nil, err
	}
	cntsJSON, err := jsonbObj(r.StepExeIDCounters)
	if err != nil {
		return nil, err
	}
	aseJSON, err := jsonbObj(r.ActiveStepExecutions)
	if err != nil {
		return nil, err
	}
	return []any{
		r.ShardID, r.Namespace, r.ID, r.FlowType, r.TaskListName, int32(r.Status), r.Version, r.WorkerID,
		stateJSON, ucmJSON, cntsJSON, aseJSON,
		r.StepMethodExeCounter, r.WorkerRequestCounter, r.ExternalChannelMessageCounter, nilIfZeroTime(r.LastHeartbeatTime), r.HeartbeatTimerID,
		r.ActiveDurableTimerID, r.DurableTimerFireAt, r.DurableTimerFired,
		r.LastHistoryEventID, r.CreatedAt, r.UpdatedAt,
	}, nil
}

func insertRunRow(ctx context.Context, tx pgx.Tx, r *p.RunRow) errors.CategorizedError {
	args, err := runRowArgs(r)
	if err != nil {
		return p.NewInternalError("encode run row", err)
	}
	_, execErr := tx.Exec(ctx,
		`INSERT INTO runs (`+runColumns+`) VALUES
		 ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`,
		args...)
	if execErr != nil {
		if catErr := mapError(execErr, "insert run"); catErr.IsConflictError() {
			return p.NewConflictError("run already exists: " + r.ID)
		} else {
			return catErr
		}
	}
	return nil
}

// buildRunUpdateSet renders the SET clause that applies a RunRowUpdate's
// partial-update deltas directly in SQL — no prior read needed:
//   - scalar fields: set only when the pointer is non-nil
//   - state_map / step_exe_id_counters: `col || $delta` (top-level key upsert)
//   - active_step_executions: `(col || $upserts) - $deleteKeys` (upsert + delete)
//   - unconsumed_channel_messages: chained jsonb_set per channel (full replace)
//
// Returns the SET fragment (params $1..$N) and the matching args. The caller
// appends the WHERE-clause params after these.
func buildRunUpdateSet(u *p.RunRowUpdate) (string, []any, error) {
	var (
		sets []string
		args []any
	)
	add := func(frag string, val any) {
		args = append(args, val)
		sets = append(sets, fmt.Sprintf(frag, len(args)))
	}

	sets = append(sets, "version = version + 1")
	add("updated_at = $%d", time.Now())

	if u.Status != nil {
		add("status = $%d", int32(*u.Status))
	}
	if u.WorkerID != nil {
		add("worker_id = $%d", *u.WorkerID)
	}
	if u.WorkerRequestCounter != nil {
		add("worker_request_counter = $%d", *u.WorkerRequestCounter)
	}
	if u.StepMethodExeCounter != nil {
		add("step_method_exe_counter = $%d", *u.StepMethodExeCounter)
	}
	if u.ExternalChannelMessageCounter != nil {
		add("external_channel_message_counter = $%d", *u.ExternalChannelMessageCounter)
	}
	if u.LastHeartbeatTime != nil {
		add("last_heartbeat_time = $%d", nilIfZeroTime(*u.LastHeartbeatTime))
	}
	if u.HeartbeatTimerID != nil {
		add("heartbeat_timer_id = $%d", *u.HeartbeatTimerID)
	}
	if u.ActiveDurableTimerID != nil {
		add("active_durable_timer_id = $%d", *u.ActiveDurableTimerID)
	}
	if u.DurableTimerFireAt != nil {
		add("durable_timer_fire_at = $%d", *u.DurableTimerFireAt)
	}
	if u.DurableTimerFired != nil {
		add("durable_timer_fired = $%d", *u.DurableTimerFired)
	}
	if u.LastHistoryEventID != nil {
		add("last_history_event_id = $%d", *u.LastHistoryEventID)
	}

	if len(u.StateMap) > 0 {
		b, err := json.Marshal(u.StateMap)
		if err != nil {
			return "", nil, err
		}
		add("state_map = state_map || $%d::jsonb", b)
	}
	if len(u.StepExeIDCounters) > 0 {
		b, err := json.Marshal(u.StepExeIDCounters)
		if err != nil {
			return "", nil, err
		}
		add("step_exe_id_counters = step_exe_id_counters || $%d::jsonb", b)
	}
	if len(u.ActiveStepExecutions) > 0 {
		upserts := make(map[string]p.ActiveStepExecution)
		deletes := []string{} // empty (non-nil) so `- '{}'::text[]` is a no-op
		for k, v := range u.ActiveStepExecutions {
			if v == nil {
				deletes = append(deletes, k)
			} else {
				upserts[k] = *v
			}
		}
		ub, err := json.Marshal(upserts)
		if err != nil {
			return "", nil, err
		}
		args = append(args, ub)
		ui := len(args)
		args = append(args, deletes)
		di := len(args)
		sets = append(sets, fmt.Sprintf("active_step_executions = (active_step_executions || $%d::jsonb) - $%d::text[]", ui, di))
	}

	ucmExpr, ucmSet, err := buildUCMExpr(u, &args)
	if err != nil {
		return "", nil, err
	}
	if ucmSet {
		sets = append(sets, "unconsumed_channel_messages = "+ucmExpr)
	}

	return strings.Join(sets, ", "), args, nil
}

// buildUCMExpr builds the unconsumed_channel_messages SET expression by
// chaining jsonb_set calls — one per channel. Appends its params to *args.
// Returns ("", false, nil) when there is nothing to change.
func buildUCMExpr(u *p.RunRowUpdate, args *[]any) (string, bool, error) {
	if len(u.ReplaceUnconsumedChannels) == 0 {
		return "", false, nil
	}
	expr := "unconsumed_channel_messages"
	for ch, vals := range u.ReplaceUnconsumedChannels {
		b, err := json.Marshal(vals)
		if err != nil {
			return "", false, err
		}
		*args = append(*args, ch)
		ki := len(*args)
		*args = append(*args, b)
		vi := len(*args)
		expr = fmt.Sprintf("jsonb_set(%s, ARRAY[$%d::text], $%d::jsonb)", expr, ki, vi)
	}
	return expr, true, nil
}

// --- task inserts ---

func insertTasks(ctx context.Context, tx pgx.Tx, tasks []p.TaskRow) errors.CategorizedError {
	now := time.Now()
	for _, t := range tasks {
		switch {
		case t.Immediate != nil:
			if catErr := insertImmediateTask(ctx, tx, t.Immediate, now); catErr != nil {
				return catErr
			}
		case t.Timer != nil:
			if catErr := insertTimerTask(ctx, tx, t.Timer, now); catErr != nil {
				return catErr
			}
		case t.OpsFIFO != nil:
			if catErr := insertOpsFIFOTask(ctx, tx, t.OpsFIFO, now); catErr != nil {
				return catErr
			}
		}
	}
	return nil
}

func insertImmediateTask(ctx context.Context, tx pgx.Tx, t *p.ImmediateTaskRow, now time.Time) errors.CategorizedError {
	if t.ID.IsZero() {
		t.ID = ids.NewTaskID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	infoJSON, err := jsonbOf(t.TaskInfo)
	if err != nil {
		return p.NewInternalError("encode immediate task_info", err)
	}
	_, execErr := tx.Exec(ctx,
		`INSERT INTO immediate_tasks (shard_id, sort_key, id, task_type, task_info, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		t.ShardID, t.SortKey, t.ID, int32(t.TaskType), infoJSON, t.CreatedAt)
	return mapError(execErr, "insert immediate task")
}

func insertTimerTask(ctx context.Context, tx pgx.Tx, t *p.TimerTaskRow, now time.Time) errors.CategorizedError {
	if t.ID.IsZero() {
		t.ID = ids.NewTaskID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	infoJSON, err := jsonbOf(t.TaskInfo)
	if err != nil {
		return p.NewInternalError("encode timer task_info", err)
	}
	_, execErr := tx.Exec(ctx,
		`INSERT INTO timer_tasks (shard_id, sort_key, id, task_type, task_info, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		t.ShardID, t.SortKey, t.ID, int32(t.TaskType), infoJSON, t.CreatedAt)
	return mapError(execErr, "insert timer task")
}

func insertOpsFIFOTask(ctx context.Context, tx pgx.Tx, t *p.OpsFIFOTaskRow, now time.Time) errors.CategorizedError {
	if t.ID.IsZero() {
		t.ID = ids.NewTaskID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	payload, err := encodeOpsFIFOPayload(t)
	if err != nil {
		return p.NewInternalError("encode OpsFIFO payload", err)
	}
	_, execErr := tx.Exec(ctx,
		`INSERT INTO opsfifo_tasks (shard_id, sort_key, id, task_type, payload, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		t.ShardID, t.SortKey, t.ID, int32(t.TaskType), payload, t.CreatedAt)
	return mapError(execErr, "insert OpsFIFO task")
}
