// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
)

const maxGetHistoryEventsLimit = 1000

type pgHistoryStore struct {
	pool     *pgxpool.Pool
	timeouts OperationTimeouts
}

// NewHistoryStore opens a pool to the history database.
func NewHistoryStore(ctx context.Context, cfg PoolConfig) (p.HistoryStore, errors.CategorizedError) {
	pool, err := newPool(ctx, cfg, defaultHistoryDatabase)
	if err != nil {
		return nil, err
	}
	return &pgHistoryStore{pool: pool, timeouts: cfg.Timeouts}, nil
}

func (s *pgHistoryStore) Close() error { s.pool.Close(); return nil }

// BatchInsertHistory inserts every event. ON CONFLICT (run_id, event_id) DO
// NOTHING makes a replayed OpsFIFO batch a no-op, mirroring Mongo's
// ordered=false + duplicate-key-swallow contract.
func (s *pgHistoryStore) BatchInsertHistory(ctx context.Context, events []p.HistoryEvent) errors.CategorizedError {
	if len(events) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	batch := &pgx.Batch{}
	for i := range events {
		e := events[i]
		if vErr := e.Payload.Validate(); vErr != nil {
			return p.NewInternalError("BatchInsertHistory: invalid payload", vErr)
		}
		payloadType, payloadBytes, mErr := marshalHistoryPayload(e.Payload)
		if mErr != nil {
			return p.NewInternalError("BatchInsertHistory: marshal payload", mErr)
		}
		batch.Queue(
			`INSERT INTO history (run_id, event_id, namespace, occurred_at_ms, worker_id, payload_type, payload)
			 VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (run_id, event_id) DO NOTHING`,
			e.RunID, e.EventID, e.Namespace, e.OccurredAtMs, e.WorkerID, payloadType, payloadBytes)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range events {
		if _, err := br.Exec(); err != nil {
			return mapError(err, "BatchInsertHistory")
		}
	}
	return nil
}

func (s *pgHistoryStore) GetHistoryEvents(ctx context.Context, namespace, runID string, afterID int64, limit int) ([]p.HistoryEvent, errors.CategorizedError) {
	if runID == "" {
		return nil, errors.NewInvalidInputError("GetHistoryEvents: run_id is required", nil)
	}
	if limit <= 0 || limit > maxGetHistoryEventsLimit {
		limit = maxGetHistoryEventsLimit
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	// namespace is an optional defensive filter (empty == any).
	rows, err := s.pool.Query(ctx,
		`SELECT run_id, event_id, namespace, occurred_at_ms, worker_id, payload_type, payload FROM history
		 WHERE run_id=$1 AND event_id>$2 AND ($3 = '' OR namespace=$3)
		 ORDER BY event_id ASC LIMIT $4`,
		runID, afterID, namespace, limit)
	if err != nil {
		return nil, mapError(err, "GetHistoryEvents")
	}
	defer rows.Close()

	var out []p.HistoryEvent
	for rows.Next() {
		var (
			e            p.HistoryEvent
			payloadType  string
			payloadBytes []byte
		)
		if err := rows.Scan(&e.RunID, &e.EventID, &e.Namespace, &e.OccurredAtMs, &e.WorkerID, &payloadType, &payloadBytes); err != nil {
			return nil, mapError(err, "GetHistoryEvents scan")
		}
		payload, decErr := unmarshalHistoryPayload(payloadType, payloadBytes)
		if decErr != nil {
			return nil, p.NewInternalError("GetHistoryEvents decode payload", decErr)
		}
		e.Payload = payload
		out = append(out, e)
	}
	return out, mapErrIfRows(rows, "GetHistoryEvents")
}

func (s *pgHistoryStore) GetLatestEvent(ctx context.Context, namespace, runID string) (*p.HistoryEvent, errors.CategorizedError) {
	if runID == "" {
		return nil, errors.NewInvalidInputError("GetLatestEvent: run_id is required", nil)
	}
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()

	// namespace is an optional defensive filter (empty == any).
	var (
		e            p.HistoryEvent
		payloadType  string
		payloadBytes []byte
	)
	err := s.pool.QueryRow(ctx,
		`SELECT run_id, event_id, namespace, occurred_at_ms, worker_id, payload_type, payload FROM history
		 WHERE run_id=$1 AND ($2 = '' OR namespace=$2)
		 ORDER BY event_id DESC LIMIT 1`,
		runID, namespace).Scan(&e.RunID, &e.EventID, &e.Namespace, &e.OccurredAtMs, &e.WorkerID, &payloadType, &payloadBytes)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, mapError(err, "GetLatestEvent")
	}
	payload, decErr := unmarshalHistoryPayload(payloadType, payloadBytes)
	if decErr != nil {
		return nil, p.NewInternalError("GetLatestEvent decode payload", decErr)
	}
	e.Payload = payload
	return &e, nil
}

func (s *pgHistoryStore) DeleteAll(ctx context.Context) error {
	ctx, cancel := cappedCtx(ctx, s.timeouts.Long)
	defer cancel()
	_, err := s.pool.Exec(ctx, `TRUNCATE history`)
	return err
}
