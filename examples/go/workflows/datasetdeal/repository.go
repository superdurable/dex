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
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrProcessExists       = errors.New("dataset deal process already exists")
	ErrProcessNotFound     = errors.New("dataset deal process not found")
	ErrExecutionExists     = errors.New("dataset deal execution already exists")
	ErrExecutionNotFound   = errors.New("dataset deal execution not found")
	ErrExecutionConflict   = errors.New("dataset deal execution changed concurrently")
	ErrExecutionNotWaiting = errors.New("dataset deal execution is not waiting")
	ErrConditionNotPending = errors.New("dataset deal condition is not pending")
)

const executionColumns = `
	flow_id,
	latest_run_id,
	process_id,
	buyer_id,
	process_definition,
	state_data,
	current_state,
	target_state,
	current_action_phase,
	current_action_index_to_execute,
	pending_condition_state,
	pending_condition_name,
	pending_condition_phase,
	status,
	version,
	last_step_execution_id,
	created_at,
	updated_at,
	completed_at`

//go:embed schema.sql
var schemaSQL string

type Repository interface {
	CreateProcess(context.Context, DealProcess) error
	ListProcesses(context.Context) ([]DealProcess, error)
	GetProcess(context.Context, string) (DealProcess, error)
	UpdateProcess(context.Context, DealProcess) error
	CreateExecution(context.Context, DealExecution) (DealExecution, error)
	GetExecution(context.Context, string) (DealExecution, error)
	ListExecutions(context.Context, ExecutionFilter) ([]DealExecution, error)
	UpdateExecution(context.Context, DealExecution) (DealExecution, error)
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
	definition, err := encodeProcess(process)
	if err != nil {
		return err
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

func (repository *PostgresRepository) ListProcesses(
	ctx context.Context,
) ([]DealProcess, error) {
	rows, err := repository.pool.Query(
		ctx,
		`SELECT definition FROM dataset_deal_processes ORDER BY created_at DESC, process_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list dataset deal processes: %w", err)
	}
	defer rows.Close()
	processes := make([]DealProcess, 0)
	for rows.Next() {
		var definition []byte
		if err := rows.Scan(&definition); err != nil {
			return nil, fmt.Errorf("scan dataset deal process: %w", err)
		}
		process, err := decodeProcess(definition)
		if err != nil {
			return nil, err
		}
		processes = append(processes, process)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dataset deal processes: %w", err)
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
		`SELECT definition FROM dataset_deal_processes WHERE process_id = $1`,
		processID,
	).Scan(&definition)
	if errors.Is(err, pgx.ErrNoRows) {
		return DealProcess{}, ErrProcessNotFound
	}
	if err != nil {
		return DealProcess{}, fmt.Errorf("read dataset deal process: %w", err)
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
		`UPDATE dataset_deal_processes
		 SET definition = $2::jsonb
		 WHERE process_id = $1`,
		process.ProcessID,
		definition,
	)
	if err != nil {
		return fmt.Errorf("update dataset deal process: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrProcessNotFound
	}
	return nil
}

func (repository *PostgresRepository) CreateExecution(
	ctx context.Context,
	execution DealExecution,
) (DealExecution, error) {
	processDefinition, err := encodeJSON("process definition", execution.ProcessDefinition)
	if err != nil {
		return DealExecution{}, err
	}
	stateData, err := encodeJSON("stateData", execution.StateData)
	if err != nil {
		return DealExecution{}, err
	}
	created, err := scanExecution(repository.pool.QueryRow(
		ctx,
		`INSERT INTO dataset_deal_executions (
			flow_id,
			latest_run_id,
			process_id,
			buyer_id,
			process_definition,
			state_data,
			current_state,
			target_state,
			current_action_phase,
			current_action_index_to_execute,
			pending_condition_state,
			pending_condition_name,
			pending_condition_phase,
			status,
			last_step_execution_id
		) VALUES (
			$1, $2, $3, $4, $5::jsonb, $6::jsonb, $7, $8, $9, $10, $11, $12, $13, $14, $15
		)
		RETURNING `+executionColumns,
		execution.FlowID,
		execution.LatestRunID,
		execution.ProcessID,
		execution.BuyerID,
		processDefinition,
		stateData,
		execution.CurrentState,
		execution.TargetState,
		execution.CurrentActionPhase,
		execution.CurrentActionIndex,
		execution.PendingConditionState,
		execution.PendingConditionName,
		execution.PendingConditionPhase,
		execution.Status,
		execution.LastStepExecutionID,
	))
	if err == nil {
		return created, nil
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return DealExecution{}, ErrExecutionExists
	}
	return DealExecution{}, fmt.Errorf("insert dataset deal execution: %w", err)
}

func (repository *PostgresRepository) GetExecution(
	ctx context.Context,
	flowID string,
) (DealExecution, error) {
	execution, err := scanExecution(repository.pool.QueryRow(
		ctx,
		`SELECT `+executionColumns+`
		 FROM dataset_deal_executions
		 WHERE flow_id = $1`,
		flowID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return DealExecution{}, ErrExecutionNotFound
	}
	if err != nil {
		return DealExecution{}, fmt.Errorf("read dataset deal execution: %w", err)
	}
	return execution, nil
}

func (repository *PostgresRepository) ListExecutions(
	ctx context.Context,
	filter ExecutionFilter,
) ([]DealExecution, error) {
	if filter.Status != "" && !filter.Status.Valid() {
		return nil, fmt.Errorf("invalid dataset deal execution status %q", filter.Status)
	}
	var query strings.Builder
	query.WriteString("SELECT ")
	query.WriteString(executionColumns)
	query.WriteString(" FROM dataset_deal_executions WHERE TRUE")
	arguments := make([]any, 0, 5)
	if filter.BuyerID != "" {
		arguments = append(arguments, filter.BuyerID)
		appendExecutionFilter(&query, "buyer_id", len(arguments))
	}
	if filter.ProcessID != "" {
		arguments = append(arguments, filter.ProcessID)
		appendExecutionFilter(&query, "process_id", len(arguments))
	}
	if filter.Status != "" {
		arguments = append(arguments, filter.Status)
		appendExecutionFilter(&query, "status", len(arguments))
	}
	if filter.CurrentState != "" {
		arguments = append(arguments, filter.CurrentState)
		appendExecutionFilter(&query, "current_state", len(arguments))
	}
	if filter.PendingConditionName != "" {
		arguments = append(arguments, filter.PendingConditionName)
		appendExecutionFilter(&query, "pending_condition_name", len(arguments))
	}
	query.WriteString(" ORDER BY created_at DESC, flow_id")
	rows, err := repository.pool.Query(ctx, query.String(), arguments...)
	if err != nil {
		return nil, fmt.Errorf("list dataset deal executions: %w", err)
	}
	defer rows.Close()
	executions := make([]DealExecution, 0)
	for rows.Next() {
		execution, scanErr := scanExecution(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan dataset deal execution: %w", scanErr)
		}
		executions = append(executions, execution)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dataset deal executions: %w", err)
	}
	return executions, nil
}

func (repository *PostgresRepository) UpdateExecution(
	ctx context.Context,
	execution DealExecution,
) (DealExecution, error) {
	stateData, err := encodeJSON("stateData", execution.StateData)
	if err != nil {
		return DealExecution{}, err
	}
	updated, err := scanExecution(repository.pool.QueryRow(
		ctx,
		`UPDATE dataset_deal_executions
		 SET latest_run_id = $2,
		     state_data = $3::jsonb,
		     current_state = $4,
		     target_state = $5,
		     current_action_phase = $6,
		     current_action_index_to_execute = $7,
		     pending_condition_state = $8,
		     pending_condition_name = $9,
		     pending_condition_phase = $10,
		     status = $11,
		     version = version + 1,
		     last_step_execution_id = $12,
		     updated_at = NOW(),
		     completed_at = CASE
		       WHEN $11 = 'COMPLETED' THEN COALESCE(completed_at, NOW())
		       ELSE NULL
		     END
		 WHERE flow_id = $1 AND version = $13
		 RETURNING `+executionColumns,
		execution.FlowID,
		execution.LatestRunID,
		stateData,
		execution.CurrentState,
		execution.TargetState,
		execution.CurrentActionPhase,
		execution.CurrentActionIndex,
		execution.PendingConditionState,
		execution.PendingConditionName,
		execution.PendingConditionPhase,
		execution.Status,
		execution.LastStepExecutionID,
		execution.Version,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return DealExecution{}, ErrExecutionConflict
	}
	if err != nil {
		return DealExecution{}, fmt.Errorf("update dataset deal execution: %w", err)
	}
	return updated, nil
}

func appendExecutionFilter(query *strings.Builder, column string, argument int) {
	query.WriteString(" AND ")
	query.WriteString(column)
	query.WriteString(" = $")
	query.WriteString(fmt.Sprintf("%d", argument))
}

type executionScanner interface {
	Scan(destinations ...any) error
}

func scanExecution(scanner executionScanner) (DealExecution, error) {
	var execution DealExecution
	var processDefinition []byte
	var stateData []byte
	if err := scanner.Scan(
		&execution.FlowID,
		&execution.LatestRunID,
		&execution.ProcessID,
		&execution.BuyerID,
		&processDefinition,
		&stateData,
		&execution.CurrentState,
		&execution.TargetState,
		&execution.CurrentActionPhase,
		&execution.CurrentActionIndex,
		&execution.PendingConditionState,
		&execution.PendingConditionName,
		&execution.PendingConditionPhase,
		&execution.Status,
		&execution.Version,
		&execution.LastStepExecutionID,
		&execution.CreatedAt,
		&execution.UpdatedAt,
		&execution.CompletedAt,
	); err != nil {
		return DealExecution{}, err
	}
	if err := json.Unmarshal(processDefinition, &execution.ProcessDefinition); err != nil {
		return DealExecution{}, fmt.Errorf("decode process definition: %w", err)
	}
	if err := json.Unmarshal(stateData, &execution.StateData); err != nil {
		return DealExecution{}, fmt.Errorf("decode stateData: %w", err)
	}
	return execution, nil
}

func encodeJSON(name string, value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", name, err)
	}
	return encoded, nil
}

func encodeProcess(process DealProcess) ([]byte, error) {
	return encodeJSON("dataset deal process", process)
}

func decodeProcess(definition []byte) (DealProcess, error) {
	var process DealProcess
	if err := json.Unmarshal(definition, &process); err != nil {
		return DealProcess{}, fmt.Errorf("decode dataset deal process: %w", err)
	}
	return process, nil
}
