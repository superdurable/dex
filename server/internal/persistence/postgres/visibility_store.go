// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
)

const maxListRunsLimit = 1000

type pgVisibilityStore struct {
	pool     *pgxpool.Pool
	timeouts OperationTimeouts
}

// NewVisibilityStore opens a pool to the visibility database.
func NewVisibilityStore(ctx context.Context, cfg PoolConfig) (p.VisibilityStore, errors.CategorizedError) {
	pool, err := newPool(ctx, cfg, defaultVisibilityDatabase)
	if err != nil {
		return nil, err
	}
	return &pgVisibilityStore{pool: pool, timeouts: cfg.Timeouts}, nil
}

func (s *pgVisibilityStore) Close() error { s.pool.Close(); return nil }

// BatchUpsertVisibility upserts by (namespace, run_id). start_time is pinned on
// insert only; the rest are overwritten with the latest state.
func (s *pgVisibilityStore) BatchUpsertVisibility(ctx context.Context, entries []p.VisibilityEntry) errors.CategorizedError {
	if len(entries) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	batch := &pgx.Batch{}
	for _, e := range entries {
		batch.Queue(
			`INSERT INTO visibility (namespace, run_id, flow_type, task_list_name, status, start_time, updated_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)
			 ON CONFLICT (namespace, run_id) DO UPDATE SET
			   flow_type = EXCLUDED.flow_type,
			   task_list_name = EXCLUDED.task_list_name,
			   status = EXCLUDED.status,
			   updated_at = EXCLUDED.updated_at`,
			e.Namespace, e.RunID, e.FlowType, e.TaskListName, int32(e.Status), e.StartTime, e.UpdatedAt)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range entries {
		if _, err := br.Exec(); err != nil {
			return mapError(err, "BatchUpsertVisibility")
		}
	}
	return nil
}

func (s *pgVisibilityStore) ListRuns(ctx context.Context, q p.ListRunsQuery) (*p.ListRunsResult, errors.CategorizedError) {
	if q.Namespace == "" {
		return nil, errors.NewInvalidInputError("ListRuns: namespace is required", nil)
	}
	limit := q.Limit
	if limit <= 0 || limit > maxListRunsLimit {
		limit = maxListRunsLimit
	}
	orderCol, oerr := orderColumn(q.OrderBy)
	if oerr != nil {
		return nil, oerr
	}

	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	var (
		conds = []string{"namespace=$1"}
		args  = []any{q.Namespace}
	)
	next := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if q.FlowType != "" {
		conds = append(conds, "flow_type="+next(q.FlowType))
	}
	if q.Status != nil {
		conds = append(conds, "status="+next(int32(*q.Status)))
	}
	if q.PageToken != "" {
		ts, runID, perr := parseListPageToken(q.PageToken)
		if perr != nil {
			return nil, errors.NewInvalidInputError("ListRuns: invalid page_token", perr)
		}
		// orderCol < ts  OR  (orderCol = ts AND run_id > runID)
		tsP := next(ts)
		runP := next(runID)
		conds = append(conds, fmt.Sprintf("(%s < %s OR (%s = %s AND run_id > %s))", orderCol, tsP, orderCol, tsP, runP))
	}
	limitP := next(limit)

	query := `SELECT namespace, run_id, flow_type, task_list_name, status, start_time, updated_at FROM visibility WHERE ` +
		strings.Join(conds, " AND ") +
		" ORDER BY " + orderCol + " DESC, run_id ASC LIMIT " + limitP

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapError(err, "ListRuns")
	}
	defer rows.Close()

	var entries []p.VisibilityEntry
	for rows.Next() {
		var (
			e      p.VisibilityEntry
			status int32
		)
		if err := rows.Scan(&e.Namespace, &e.RunID, &e.FlowType, &e.TaskListName, &status, &e.StartTime, &e.UpdatedAt); err != nil {
			return nil, mapError(err, "ListRuns scan")
		}
		e.Status = p.RunStatus(status)
		entries = append(entries, e)
	}
	if catErr := mapErrIfRows(rows, "ListRuns"); catErr != nil {
		return nil, catErr
	}

	result := &p.ListRunsResult{Entries: entries}
	if len(entries) == limit {
		last := entries[len(entries)-1]
		lastTime := last.StartTime
		if q.OrderBy == p.ListByUpdatedAtDesc {
			lastTime = last.UpdatedAt
		}
		result.NextPageToken = encodeListPageToken(lastTime, last.RunID)
	}
	return result, nil
}

func (s *pgVisibilityStore) DeleteAll(ctx context.Context) error {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()
	_, err := s.pool.Exec(ctx, `TRUNCATE visibility`)
	return err
}

// orderColumn maps the OrderBy enum to a validated column name (never user input).
func orderColumn(o p.ListRunsOrderBy) (string, errors.CategorizedError) {
	switch o {
	case p.ListByStartTimeDesc:
		return "start_time", nil
	case p.ListByUpdatedAtDesc:
		return "updated_at", nil
	default:
		return "", errors.NewInvalidInputError(fmt.Sprintf("ListRuns: unknown OrderBy %d", o), nil)
	}
}

func encodeListPageToken(t time.Time, runID string) string {
	return strconv.FormatInt(t.UnixMilli(), 10) + ":" + runID
}

func parseListPageToken(token string) (time.Time, string, error) {
	idx := strings.IndexByte(token, ':')
	if idx <= 0 || idx == len(token)-1 {
		return time.Time{}, "", fmt.Errorf("malformed page_token %q", token)
	}
	ms, err := strconv.ParseInt(token[:idx], 10, 64)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("malformed page_token unix_millis: %w", err)
	}
	return time.UnixMilli(ms), token[idx+1:], nil
}
