package postgres

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/errors"
)

func newPgxPool(ctx context.Context, cfg *config.ResolvedStoreConfig) (*pgxpool.Pool, errors.CategorizedError) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URI)
	if err != nil {
		return nil, errors.NewInternalError("postgres: parse URI", err)
	}
	poolCfg.ConnConfig.Database = cfg.Database
	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, errors.NewInternalError("postgres: create pool", err)
	}
	return pool, nil
}

func cappedCtx(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok {
		if time.Until(deadline) <= timeout {
			return parent, func() {}
		}
	}
	return context.WithTimeout(parent, timeout)
}

const (
	sqlStateUniqueViolation = "23505"
)

// categorizeError converts a pgx/pgconn error into a CategorizedError.
func categorizeError(err error, msg string) errors.CategorizedError {
	if err == nil {
		return nil
	}
	if stderrors.Is(err, context.DeadlineExceeded) {
		return errors.NewTimeoutError(msg, err)
	}
	if stderrors.Is(err, context.Canceled) {
		return errors.NewCancelError(msg, err)
	}

	if pgErr, ok := stderrors.AsType[*pgconn.PgError](err); ok {
		if pgErr.Code == sqlStateUniqueViolation {
			return errors.NewConflictError(msg+": "+pgErr.Message, nil)
		}
	}
	return errors.NewInternalError(msg, err)
}

func isNotFoundError(err error) bool {
	return stderrors.Is(err, pgx.ErrNoRows)
}

func unmarshalJSON(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

func mapErrIfRows(rows pgx.Rows, msg string) errors.CategorizedError {
	if err := rows.Err(); err != nil {
		return categorizeError(err, msg)
	}
	return nil
}

func nilIfZeroTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func jsonbOf(v any) ([]byte, error) {
	if v == nil {
		return []byte("null"), nil
	}
	return json.Marshal(v)
}

// jsonbObj marshals a map to JSON, substituting "{}" for a nil/empty map so
// the column holds a JSON object (not the scalar `null`). Required so later
// partial updates can use object operators (`||`, `jsonb_set`).
func jsonbObj[K comparable, V any](m map[K]V) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}
