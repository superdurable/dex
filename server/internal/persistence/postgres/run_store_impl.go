package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"common-go/ids"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/errors"

	p "github.com/superdurable/dex/server/internal/persistence"
)

type pgRunStore struct {
	pool *pgxpool.Pool
	cfg  *config.ResolvedPGStoreConfig
}

// NewRunStore opens a pool to the runs database and returns a RunStore.
func NewRunStore(ctx context.Context, cfg *config.ResolvedPGStoreConfig) (p.RunStore, errors.CategorizedError) {
	pool, err := newPgxPool(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &pgRunStore{pool: pool, cfg: cfg}, nil
}

func (s *pgRunStore) Close() error { s.pool.Close(); return nil }

// runColumns lists every run column in a fixed order, shared by SELECT + scan.
// durable_timer_fired is a schema leftover with no Go field; always written as false.
const runColumns = `shard_id, namespace, id, flow_type, task_list_name, status, heartbeat_timeout_seconds, version, worker_id,
	data_attributes, unconsumed_channel_messages, step_exe_id_counters, active_step_executions,
	step_method_exe_counter, worker_request_counter, external_channel_message_counter, last_heartbeat_time, heartbeat_timer_id,
	active_durable_timer_id, durable_timer_fired_at, durable_timer_fired,
	last_history_event_id, created_at, updated_at`

func (s *pgRunStore) CreateRunWithTasks(ctx context.Context, run *p.RunRow, tasks []p.TaskRow) errors.CategorizedError {
	if run.RowType != p.RowTypeRun {
		return errors.NewInvalidInputError("run.RowType must be RowTypeRun", nil)
	}

	ctx, cancel := cappedCtx(ctx, s.cfg.ShortOperationTimeout)
	defer cancel()

	run.CreatedAt = time.Now()
	run.UpdatedAt = run.CreatedAt

	return s.inTx(ctx, func(tx pgx.Tx) errors.CategorizedError {
		if err := insertRunRow(ctx, tx, run); err != nil {
			return err
		}
		return insertTasks(ctx, tx, tasks)
	})
}

func (s *pgRunStore) GetRun(ctx context.Context, shardID int32, namespace, runID string) (*p.RunRow, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.cfg.ShortOperationTimeout)
	defer cancel()

	row := s.pool.QueryRow(ctx,
		`SELECT `+runColumns+` FROM runs WHERE shard_id=$1 AND namespace=$2 AND id=$3`,
		shardID, namespace, runID)
	run, err := scanRunRow(row)
	if err != nil {
		if isNotFoundError(err) {
			return nil, errors.NewNotFoundError("run not found: "+runID, nil)
		}
		return nil, categorizeError(err, "GetRun")
	}
	return run, nil
}

func (s *pgRunStore) UpdateRunWithNewTasks(ctx context.Context, shardID int32, namespace, runID string,
	expectedVersion int64, update *p.RunRowUpdate, newTasks []p.TaskRow) errors.CategorizedError {

	ctx, cancel := cappedCtx(ctx, s.cfg.ShortOperationTimeout)
	defer cancel()

	return s.inTx(ctx, func(tx pgx.Tx) errors.CategorizedError {
		setSQL, args, encErr := buildRunUpdateSet(update)
		if encErr != nil {
			return errors.NewInternalError("encode run update", encErr)
		}
		args = append(args, shardID, namespace, runID, expectedVersion)
		n := len(args)
		query := fmt.Sprintf(
			`UPDATE runs SET %s WHERE shard_id=$%d AND namespace=$%d AND id=$%d AND version=$%d`,
			setSQL, n-3, n-2, n-1, n)

		ct, execErr := tx.Exec(ctx, query, args...)
		if execErr != nil {
			return categorizeError(execErr, "UpdateRunWithNewTasks")
		}
		if ct.RowsAffected() == 0 {
			return errors.NewCASError("run "+runID, nil)
		}
		return insertTasks(ctx, tx, newTasks)
	})
}

func (s *pgRunStore) RangeReadImmediateTasks(ctx context.Context, shardID int32, afterSeq int64, limit int) ([]*p.ImmediateTaskRow, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.cfg.LongOperationTimeout)
	defer cancel()

	rows, err := s.pool.Query(ctx,
		`SELECT shard_id, sort_key, id, task_type, task_info, created_at FROM immediate_tasks
		 WHERE shard_id=$1 AND sort_key>$2 ORDER BY sort_key, id LIMIT $3`,
		shardID, afterSeq, limit)
	if err != nil {
		return nil, categorizeError(err, "RangeReadImmediateTasks")
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
			return nil, categorizeError(err, "RangeReadImmediateTasks scan")
		}
		r.RowType = p.RowTypeImmediateTask
		r.TaskType = p.ImmediateTaskType(taskType)
		if err := unmarshalJSON(infoJSON, &r.TaskInfo); err != nil {
			return nil, errors.NewInternalError("decode immediate task_info", err)
		}
		out = append(out, &r)
	}
	return out, mapErrIfRows(rows, "RangeReadImmediateTasks")
}

func (s *pgRunStore) RangeDeleteImmediateTasks(ctx context.Context, shardID int32, upToSeq int64) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.cfg.LongOperationTimeout)
	defer cancel()
	_, err := s.pool.Exec(ctx, `DELETE FROM immediate_tasks WHERE shard_id=$1 AND sort_key<=$2`, shardID, upToSeq)
	return categorizeError(err, "RangeDeleteImmediateTasks")
}

func (s *pgRunStore) RangeReadTimerTasks(ctx context.Context, shardID int32, sortKeyUpTo int64, afterSortKey int64, afterID ids.UID, limit int) ([]*p.TimerTaskRow, errors.CategorizedError) {
	ctx, cancel := cappedCtx(ctx, s.cfg.LongOperationTimeout)
	defer cancel()

	rows, err := s.pool.Query(ctx,
		`SELECT shard_id, sort_key, id, task_type, task_info, created_at FROM timer_tasks
		 WHERE shard_id=$1 AND sort_key<=$2
		   AND ($3 OR (sort_key, id) > ($4, $5))
		 ORDER BY sort_key, id LIMIT $6`,
		shardID, sortKeyUpTo, afterID.IsZero(), afterSortKey, afterID, limit)
	if err != nil {
		return nil, categorizeError(err, "RangeReadTimerTasks")
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
			return nil, categorizeError(err, "RangeReadTimerTasks scan")
		}
		r.RowType = p.RowTypeTimerTask
		r.TaskType = p.TimerTaskType(taskType)
		if err := unmarshalJSON(infoJSON, &r.TaskInfo); err != nil {
			return nil, errors.NewInternalError("decode timer task_info", err)
		}
		out = append(out, &r)
	}
	return out, mapErrIfRows(rows, "RangeReadTimerTasks")
}

func (s *pgRunStore) RangeDeleteTimerTasks(ctx context.Context, shardID int32, upToSortKey int64, upToID ids.UID) errors.CategorizedError {
	ctx, cancel := cappedCtx(ctx, s.cfg.LongOperationTimeout)
	defer cancel()
	// Exclusive upper bound: (sort_key, id) < (upToSortKey, upToID).
	_, err := s.pool.Exec(ctx,
		`DELETE FROM timer_tasks WHERE shard_id=$1 AND (sort_key, id) < ($2, $3)`,
		shardID, upToSortKey, upToID)
	return categorizeError(err, "RangeDeleteTimerTasks")
}

func (s *pgRunStore) DeleteImmediateTasksByIDBatch(ctx context.Context, shardID int32, uids []ids.UID) errors.CategorizedError {
	return s.deleteTasksByID(ctx, "immediate_tasks", shardID, uids)
}

func (s *pgRunStore) DeleteTimerTasksByIDBatch(ctx context.Context, shardID int32, uids []ids.UID) errors.CategorizedError {
	return s.deleteTasksByID(ctx, "timer_tasks", shardID, uids)
}

func (s *pgRunStore) deleteTasksByID(ctx context.Context, table string, shardID int32, uids []ids.UID) errors.CategorizedError {
	if len(uids) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.cfg.LongOperationTimeout)
	defer cancel()
	idStrings := make([]string, 0, len(uids))
	for _, id := range uids {
		idStrings = append(idStrings, id.String())
	}
	_, err := s.pool.Exec(ctx,
		`DELETE FROM `+table+` WHERE shard_id=$1 AND id = ANY($2::uuid[])`, shardID, idStrings)
	return categorizeError(err, "DeleteTasksByIDBatch")
}

func (s *pgRunStore) DeleteAll(ctx context.Context) error {
	ctx, cancel := cappedCtx(ctx, s.cfg.LongOperationTimeout)
	defer cancel()
	_, err := s.pool.Exec(ctx, `TRUNCATE runs, immediate_tasks, timer_tasks`)
	return err
}

func (s *pgRunStore) inTx(ctx context.Context, fn func(pgx.Tx) errors.CategorizedError) errors.CategorizedError {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return categorizeError(err, "begin tx")
	}
	if catErr := fn(tx); catErr != nil {
		_ = tx.Rollback(ctx)
		return catErr
	}
	if err := tx.Commit(ctx); err != nil {
		_ = tx.Rollback(ctx)
		return categorizeError(err, "commit tx")
	}
	return nil
}

func scanRunRow(row pgx.Row) (*p.RunRow, error) {
	var (
		r         p.RunRow
		status    int32
		stateJSON []byte
		ucmJSON   []byte
		cntsJSON  []byte
		aseJSON   []byte
		lastHB    *time.Time
		fired     bool // schema leftover; discarded
	)
	if err := row.Scan(
		&r.ShardID, &r.Namespace, &r.ID, &r.FlowType, &r.TaskListName, &status, &r.HeartbeatTimeoutSeconds, &r.Version, &r.WorkerID,
		&stateJSON, &ucmJSON, &cntsJSON, &aseJSON,
		&r.StepMethodExeCounter, &r.WorkerRequestCounter, &r.ExternalChannelMessageCounter, &lastHB, &r.HeartbeatTimerID,
		&r.ActiveDurableTimerID, &r.DurableTimerFiredAt, &fired,
		&r.LastHistoryEventID, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return nil, err
	}
	r.RowType = p.RowTypeRun
	r.Status = p.RunStatus(status)
	if lastHB != nil {
		r.LastHeartbeatTime = *lastHB
	}
	if err := unmarshalJSON(stateJSON, &r.DataAttributes); err != nil {
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

func runRowArgs(r *p.RunRow) ([]any, error) {
	stateJSON, err := jsonbObj(r.DataAttributes)
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
		r.ShardID, r.Namespace, r.ID, r.FlowType, r.TaskListName, int32(r.Status), r.HeartbeatTimeoutSeconds, r.Version, r.WorkerID,
		stateJSON, ucmJSON, cntsJSON, aseJSON,
		r.StepMethodExeCounter, r.WorkerRequestCounter, r.ExternalChannelMessageCounter, nilIfZeroTime(r.LastHeartbeatTime), r.HeartbeatTimerID,
		r.ActiveDurableTimerID, r.DurableTimerFiredAt, false,
		r.LastHistoryEventID, r.CreatedAt, r.UpdatedAt,
	}, nil
}

func insertRunRow(ctx context.Context, tx pgx.Tx, r *p.RunRow) errors.CategorizedError {
	args, err := runRowArgs(r)
	if err != nil {
		return errors.NewInternalError("encode run row", err)
	}
	_, execErr := tx.Exec(ctx,
		`INSERT INTO runs (`+runColumns+`) VALUES
		 ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)`,
		args...)
	if execErr != nil {
		catErr := categorizeError(execErr, "insert run")
		if catErr.IsConflictError() {
			return errors.NewConflictError("run already exists: "+r.ID, nil)
		}
		return catErr
	}
	return nil
}

// buildRunUpdateSet renders the SET clause that applies a RunRowUpdate's
// partial-update deltas directly in SQL — no prior read needed:
//   - scalar fields: set only when the pointer is non-nil
//   - data_attributes / step_exe_id_counters: `col || $delta` (top-level key upsert)
//   - active_step_executions: `(col || $upserts) - $deleteKeys` (upsert + delete)
//   - unconsumed_channel_messages: chained jsonb_set per channel (full replace)
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
	if u.DurableTimerFiredAt != nil {
		add("durable_timer_fired_at = $%d", *u.DurableTimerFiredAt)
	}
	if u.LastHistoryEventID != nil {
		add("last_history_event_id = $%d", *u.LastHistoryEventID)
	}

	if u.ReplaceDataAttributes != nil {
		b, err := json.Marshal(*u.ReplaceDataAttributes)
		if err != nil {
			return "", nil, err
		}
		add("data_attributes = $%d::jsonb", b)
	}
	if u.ReplaceStepExeIDCounters != nil {
		b, err := json.Marshal(*u.ReplaceStepExeIDCounters)
		if err != nil {
			return "", nil, err
		}
		add("step_exe_id_counters = $%d::jsonb", b)
	}
	if u.ReplaceActiveStepExecutions != nil {
		b, err := json.Marshal(*u.ReplaceActiveStepExecutions)
		if err != nil {
			return "", nil, err
		}
		add("active_step_executions = $%d::jsonb", b)
	}
	if u.ReplaceAllUnconsumedChannels != nil {
		b, err := json.Marshal(*u.ReplaceAllUnconsumedChannels)
		if err != nil {
			return "", nil, err
		}
		add("unconsumed_channel_messages = $%d::jsonb", b)
	}

	if len(u.DataAttributes) > 0 {
		b, err := json.Marshal(u.DataAttributes)
		if err != nil {
			return "", nil, err
		}
		add("data_attributes = data_attributes || $%d::jsonb", b)
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
		}
	}
	return nil
}

func insertImmediateTask(ctx context.Context, tx pgx.Tx, t *p.ImmediateTaskRow, now time.Time) errors.CategorizedError {
	if t.ID.IsZero() {
		t.ID = ids.NewUID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	infoJSON, err := jsonbOf(t.TaskInfo)
	if err != nil {
		return errors.NewInternalError("encode immediate task_info", err)
	}
	_, execErr := tx.Exec(ctx,
		`INSERT INTO immediate_tasks (shard_id, sort_key, id, task_type, task_info, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		t.ShardID, t.SortKey, t.ID, int32(t.TaskType), infoJSON, t.CreatedAt)
	return categorizeError(execErr, "insert immediate task")
}

func insertTimerTask(ctx context.Context, tx pgx.Tx, t *p.TimerTaskRow, now time.Time) errors.CategorizedError {
	if t.ID.IsZero() {
		t.ID = ids.NewUID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	infoJSON, err := jsonbOf(t.TaskInfo)
	if err != nil {
		return errors.NewInternalError("encode timer task_info", err)
	}
	_, execErr := tx.Exec(ctx,
		`INSERT INTO timer_tasks (shard_id, sort_key, id, task_type, task_info, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		t.ShardID, t.SortKey, t.ID, int32(t.TaskType), infoJSON, t.CreatedAt)
	return categorizeError(execErr, "insert timer task")
}
