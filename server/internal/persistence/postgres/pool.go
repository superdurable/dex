// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

// Package postgres implements the persistence store interfaces on top of
// PostgreSQL using jackc/pgx. It mirrors the Mongo backend's logical layout:
// each store opens its own connection pool to its own database (dex_runs,
// dex_history, ...). Nested document fields are stored as JSONB; query/CAS
// keys are real indexed columns. See docs/postgres-persistence-design.md.
package postgres

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/superdurable/dex/server/common/errors"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// OperationTimeouts holds the per-operation timeout caps applied by each store.
// If the caller's context already has a tighter deadline, the caller's wins.
type OperationTimeouts struct {
	Short time.Duration // single-row ops
	Long  time.Duration // multi-row / transaction ops
}

// DefaultOperationTimeouts returns sensible defaults matching the config.
func DefaultOperationTimeouts() OperationTimeouts {
	return OperationTimeouts{Short: 5 * time.Second, Long: 30 * time.Second}
}

// PoolConfig is the resolved per-store connection config. The wiring layer
// builds it from config.PostgresConfig so this package never imports config.
type PoolConfig struct {
	URI      string
	Database string
	MaxConns int32
	Timeouts OperationTimeouts
}

// Per-store default database names. Mirror config.DefaultPostgresPersistenceConfig
// and the databases provisioned by schema/v0.sql. DLQ co-locates with runs.
const (
	defaultRunsDatabase       = "dex_runs"
	defaultBlobsDatabase      = "dex_blobs"
	defaultShardsDatabase     = "dex_shards"
	defaultTasklistsDatabase  = "dex_tasklists"
	defaultVisibilityDatabase = "dex_visibility"
	defaultHistoryDatabase    = "dex_history"
)

// resolveDatabase returns name when non-empty, else fallback.
func resolveDatabase(name, fallback string) string {
	if name == "" {
		return fallback
	}
	return name
}

// newPool builds a pgxpool bound to a specific database. The URI is a DSN
// without a database path; database + pool size are applied to the parsed
// config. Connect is lazy — the first query dials.
func newPool(ctx context.Context, cfg PoolConfig, fallbackDB string) (*pgxpool.Pool, errors.CategorizedError) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URI)
	if err != nil {
		return nil, p.NewInternalError("postgres: parse URI", err)
	}
	poolCfg.ConnConfig.Database = resolveDatabase(cfg.Database, fallbackDB)
	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, p.NewInternalError("postgres: create pool", err)
	}
	return pool, nil
}

// cappedCtx caps the parent context to timeout unless the parent already has
// a tighter deadline.
func cappedCtx(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok {
		if time.Until(deadline) <= timeout {
			return parent, func() {}
		}
	}
	return context.WithTimeout(parent, timeout)
}

// SQLSTATE codes we special-case.
const (
	sqlStateUniqueViolation    = "23505"
	sqlStateSerializationFail  = "40001"
	sqlStateDeadlockDetected   = "40P01"
	sqlStateUndefinedTable     = "42P01"
	sqlStateInvalidCatalogName = "3D000" // database does not exist
)

// mapError converts a pgx/pgconn error into a CategorizedError. Unique
// violations map to a conflict (caller decides version-mismatch vs duplicate);
// serialization/deadlock map to a retryable CAS error; context deadline maps
// to timeout; everything else is internal.
func mapError(err error, msg string) errors.CategorizedError {
	if err == nil {
		return nil
	}
	if stderrors.Is(err, context.DeadlineExceeded) || stderrors.Is(err, context.Canceled) {
		return p.NewTimeoutError(msg, err)
	}
	var pgErr *pgconn.PgError
	if stderrors.As(err, &pgErr) {
		switch pgErr.Code {
		case sqlStateUniqueViolation:
			return p.NewConflictError(msg + ": " + pgErr.Message)
		case sqlStateSerializationFail, sqlStateDeadlockDetected:
			return p.NewVersionMismatchError(msg + ": " + pgErr.Message)
		}
	}
	return p.NewInternalError(msg, err)
}

// isNoRows reports whether err is pgx.ErrNoRows.
func isNoRows(err error) bool {
	return stderrors.Is(err, pgx.ErrNoRows)
}
