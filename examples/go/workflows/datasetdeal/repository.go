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

package datasetdeal

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
	ErrProcessExists     = errors.New("dataset deal process already exists")
	ErrProcessNotFound   = errors.New("dataset deal process not found")
	ErrExecutionNotFound = errors.New("dataset deal execution not found")
)

//go:embed schema.sql
var schemaSQL string

type Repository interface {
	CreateProcess(context.Context, DealProcess) error
	GetProcess(context.Context, string) (DealProcess, error)
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	if pool == nil {
		panic("datasetdeal.NewPostgresRepository requires pgxpool.Pool")
	}
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) EnsureSchema(ctx context.Context) error {
	if _, err := repository.pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("initialize dataset deal schema: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) CreateProcess(
	ctx context.Context,
	process DealProcess,
) error {
	definition, err := json.Marshal(process)
	if err != nil {
		return fmt.Errorf("encode dataset deal process: %w", err)
	}
	_, err = repository.pool.Exec(
		ctx,
		`INSERT INTO dataset_deal_processes (process_id, definition)
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
	return fmt.Errorf("insert dataset deal process: %w", err)
}

func (repository *PostgresRepository) GetProcess(
	ctx context.Context,
	processID string,
) (DealProcess, error) {
	var definition []byte
	err := repository.pool.QueryRow(
		ctx,
		`SELECT definition FROM dataset_deal_processes WHERE process_id = $1`,
		processID,
	).Scan(&definition)
	if errors.Is(err, pgx.ErrNoRows) {
		return DealProcess{}, ErrProcessNotFound
	}
	if err != nil {
		return DealProcess{}, fmt.Errorf("read dataset deal process: %w", err)
	}
	var process DealProcess
	if err := json.Unmarshal(definition, &process); err != nil {
		return DealProcess{}, fmt.Errorf("decode dataset deal process: %w", err)
	}
	return process, nil
}
