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

	"common-go/ids"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/errors"

	p "github.com/superdurable/dex/server/internal/persistence"
)

type pgBlobStore struct {
	pool *pgxpool.Pool
	cfg  *config.ResolvedPGStoreConfig
}

// NewBlobStore opens a pool to the blobs database and returns a BlobStore.
func NewBlobStore(ctx context.Context, cfg *config.ResolvedPGStoreConfig) (p.BlobStore, errors.CategorizedError) {
	pool, err := newPgxPool(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &pgBlobStore{pool: pool, cfg: cfg}, nil
}

func (s *pgBlobStore) Close() error { s.pool.Close(); return nil }

func (s *pgBlobStore) BatchInsert(ctx context.Context, shardID int32, namespace, runID string, blobs []p.BlobEntry) errors.CategorizedError {
	if len(blobs) == 0 {
		return nil
	}
	ctx, cancel := cappedCtx(ctx, s.cfg.LongOperationTimeout)
	defer cancel()

	batch := &pgx.Batch{}
	for _, b := range blobs {
		// ON CONFLICT DO NOTHING: idempotent across whole-RPC retries.
		batch.Queue(
			`INSERT INTO blobs (shard_id, namespace, run_id, id, encoding, payload)
			 VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (shard_id, namespace, run_id, id) DO NOTHING`,
			shardID, namespace, runID, b.BlobID, b.Encoding, b.Payload)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range blobs {
		if _, err := br.Exec(); err != nil {
			return categorizeError(err, "BatchInsertBlobs")
		}
	}
	return nil
}

func (s *pgBlobStore) BatchGet(ctx context.Context, shardID int32, namespace, runID string, blobIDs []ids.UID) ([]p.BlobEntry, errors.CategorizedError) {
	if len(blobIDs) == 0 {
		return nil, nil
	}
	ctx, cancel := cappedCtx(ctx, s.cfg.LongOperationTimeout)
	defer cancel()

	blobIDStrings := make([]string, 0, len(blobIDs))
	for _, blobID := range blobIDs {
		blobIDStrings = append(blobIDStrings, blobID.String())
	}

	rows, err := s.pool.Query(ctx,
		`SELECT id, encoding, payload FROM blobs
		 WHERE shard_id=$1 AND namespace=$2 AND run_id=$3 AND id = ANY($4::uuid[])`,
		shardID, namespace, runID, blobIDStrings)
	if err != nil {
		return nil, categorizeError(err, "BatchGetBlobs")
	}
	defer rows.Close()

	var out []p.BlobEntry
	for rows.Next() {
		var e p.BlobEntry
		if scanErr := rows.Scan(&e.BlobID, &e.Encoding, &e.Payload); scanErr != nil {
			return nil, categorizeError(scanErr, "BatchGetBlobs scan")
		}
		out = append(out, e)
	}
	return out, mapErrIfRows(rows, "BatchGetBlobs")
}
