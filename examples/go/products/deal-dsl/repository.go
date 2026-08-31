// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package dealdsl

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrProcessExists     = errors.New("deal DSL process already exists")
	ErrProcessNotFound   = errors.New("deal DSL process not found")
	ErrExecutionNotFound = errors.New("deal DSL execution not found")
)

//go:embed schema.sql
var schemaSQL string

type Repository interface {
	CreateProcess(context.Context, DealProcess) error
	ListProcesses(context.Context) ([]DealProcess, error)
	GetProcess(context.Context, string) (DealProcess, error)
	UpdateProcess(context.Context, DealProcess) error
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	if pool == nil {
		panic("dealdsl.NewPostgresRepository requires pgxpool.Pool")
	}
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) EnsureSchema(ctx context.Context) error {
	if _, err := repository.pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("initialize deal DSL schema: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) CreateProcess(
	ctx context.Context,
	process DealProcess,
) error {
	definition, err := encodeProcess(process)
	if err != nil {
		return err
	}
	_, err = repository.pool.Exec(
		ctx,
		`INSERT INTO deal_dsl_processes (process_id, definition)
		 VALUES ($1, $2::jsonb)`,
		process.ProcessID,
		definition,
	)
	if err == nil {
		return nil
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return ErrProcessExists
	}
	return fmt.Errorf("insert deal DSL process: %w", err)
}

func (repository *PostgresRepository) ListProcesses(
	ctx context.Context,
) ([]DealProcess, error) {
	rows, err := repository.pool.Query(
		ctx,
		`SELECT definition FROM deal_dsl_processes ORDER BY created_at DESC, process_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list deal DSL processes: %w", err)
	}
	defer rows.Close()
	processes := make([]DealProcess, 0)
	for rows.Next() {
		var definition []byte
		if err := rows.Scan(&definition); err != nil {
			return nil, fmt.Errorf("scan deal DSL process: %w", err)
		}
		process, err := decodeProcess(definition)
		if err != nil {
			return nil, err
		}
		processes = append(processes, process)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deal DSL processes: %w", err)
	}
	return processes, nil
}

func (repository *PostgresRepository) GetProcess(
	ctx context.Context,
	processID string,
) (DealProcess, error) {
	var definition []byte
	err := repository.pool.QueryRow(
		ctx,
		`SELECT definition FROM deal_dsl_processes WHERE process_id = $1`,
		processID,
	).Scan(&definition)
	if errors.Is(err, pgx.ErrNoRows) {
		return DealProcess{}, ErrProcessNotFound
	}
	if err != nil {
		return DealProcess{}, fmt.Errorf("read deal DSL process: %w", err)
	}
	return decodeProcess(definition)
}

func (repository *PostgresRepository) UpdateProcess(
	ctx context.Context,
	process DealProcess,
) error {
	definition, err := encodeProcess(process)
	if err != nil {
		return err
	}
	result, err := repository.pool.Exec(
		ctx,
		`UPDATE deal_dsl_processes
		 SET definition = $2::jsonb
		 WHERE process_id = $1`,
		process.ProcessID,
		definition,
	)
	if err != nil {
		return fmt.Errorf("update deal DSL process: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrProcessNotFound
	}
	return nil
}

func encodeProcess(process DealProcess) ([]byte, error) {
	definition, err := json.Marshal(process)
	if err != nil {
		return nil, fmt.Errorf("encode deal DSL process: %w", err)
	}
	return definition, nil
}

func decodeProcess(definition []byte) (DealProcess, error) {
	var process DealProcess
	if err := json.Unmarshal(definition, &process); err != nil {
		return DealProcess{}, fmt.Errorf("decode deal DSL process: %w", err)
	}
	return process, nil
}
