// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package flowindex

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	definitionVersion = 1
	defaultPageSize   = 1000
	maxPageSize       = 1000
)

var (
	ErrSchemaNotApplied = errors.New("ParadeDB flow index schema has not been applied")
	identifierPattern   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type Store interface {
	Close()
	Ping(context.Context) error
	GetInfo(context.Context) (*dexpb.GetFlowIndexInfoResponse, error)
	ApplySchema(context.Context, *dexpb.ApplyFlowIndexSchemaRequest) (*dexpb.ApplyFlowIndexSchemaResponse, error)
	Write(context.Context, *dexpb.WriteFlowIndexActivityInput) error
	WriteTerminated(context.Context, string, string) error
	Search(context.Context, *dexpb.SearchFlowsRequest) (*dexpb.SearchFlowsResponse, error)
}

type RequestError struct {
	err error
}

func (e *RequestError) Error() string {
	return e.err.Error()
}

func IsRequestError(err error) bool {
	var requestError *RequestError
	return errors.As(err, &requestError)
}

type ParadeDBStore struct {
	cfg         *config.FlowIndexConfig
	pool        *pgxpool.Pool
	schema      string
	table       string
	qualified   string
	catalog     string
	meta        string
	searchIndex string
}

type catalogField struct {
	Name       string
	Type       dexpb.IndexType
	Dimensions int32
	Metric     dexpb.VectorDistanceMetric
}

type storedFence struct {
	RunID        string
	RunStartedAt time.Time
	LastSequence int64
	Terminal     bool
}

type searchPageToken struct {
	Offset      int    `json:"offset"`
	Fingerprint string `json:"fingerprint"`
	Version     int64  `json:"version"`
}

func NewParadeDBStore(ctx context.Context, cfg *config.FlowIndexConfig) (*ParadeDBStore, error) {
	if cfg == nil {
		panic("flow index config must not be nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.EffectiveBackend() != config.FlowIndexBackendParadeDB {
		return nil, fmt.Errorf("ParadeDB store requires paradedb backend")
	}
	if err := validateIdentifier(cfg.ParadeDB.EffectiveSchema()); err != nil {
		return nil, fmt.Errorf("invalid ParadeDB schema: %w", err)
	}
	if err := validateIdentifier(cfg.ParadeDB.EffectiveTable()); err != nil {
		return nil, fmt.Errorf("invalid ParadeDB table: %w", err)
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.ParadeDB.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse ParadeDB DSN: %w", err)
	}
	poolCfg.MaxConns = cfg.ParadeDB.EffectiveMaxConnections()
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create ParadeDB pool: %w", err)
	}
	store := &ParadeDBStore{
		cfg:         cfg,
		pool:        pool,
		schema:      cfg.ParadeDB.EffectiveSchema(),
		table:       cfg.ParadeDB.EffectiveTable(),
		qualified:   qualifiedName(cfg.ParadeDB.EffectiveSchema(), cfg.ParadeDB.EffectiveTable()),
		catalog:     qualifiedName(cfg.ParadeDB.EffectiveSchema(), compactIdentifier(cfg.ParadeDB.EffectiveTable(), "fields")),
		meta:        qualifiedName(cfg.ParadeDB.EffectiveSchema(), compactIdentifier(cfg.ParadeDB.EffectiveTable(), "meta")),
		searchIndex: compactIdentifier(cfg.ParadeDB.EffectiveTable(), "search_idx"),
	}
	if err := store.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to ParadeDB: %w", err)
	}
	return store, nil
}

func (s *ParadeDBStore) Close() {
	s.pool.Close()
}

func (s *ParadeDBStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *ParadeDBStore) GetInfo(ctx context.Context) (*dexpb.GetFlowIndexInfoResponse, error) {
	version, err := s.schemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	fields, err := s.loadCatalog(ctx)
	if err != nil {
		return nil, err
	}
	response := &dexpb.GetFlowIndexInfoResponse{
		Backend:       dexpb.AttributeIndexBackend_ATTRIBUTE_INDEX_BACKEND_PARADEDB,
		SchemaVersion: version,
		Fields:        systemFields(),
	}
	for _, field := range fields {
		response.Fields = append(response.Fields, &dexpb.FlowIndexField{
			Name:                 field.Name,
			Type:                 field.Type,
			VectorDimensions:     field.Dimensions,
			VectorDistanceMetric: field.Metric,
		})
	}
	return response, nil
}

func (s *ParadeDBStore) ApplySchema(
	ctx context.Context,
	request *dexpb.ApplyFlowIndexSchemaRequest,
) (response *dexpb.ApplyFlowIndexSchemaResponse, retErr error) {
	if err := validateSchemaRequest(request); err != nil {
		return nil, &RequestError{err: err}
	}
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire ParadeDB schema connection: %w", err)
	}
	defer connection.Release()

	lockName := s.schema + "." + s.table
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock(hashtext($1))`, lockName); err != nil {
		return nil, fmt.Errorf("lock ParadeDB flow index schema: %w", err)
	}
	defer func() {
		_, unlockErr := connection.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext($1))`, lockName)
		if unlockErr != nil {
			response = nil
			retErr = errors.Join(retErr, fmt.Errorf("unlock ParadeDB flow index schema: %w", unlockErr))
		}
	}()

	if err := s.ensureBaseSchema(ctx, connection); err != nil {
		return nil, err
	}
	existing, err := s.loadCatalogFrom(ctx, connection)
	if err != nil {
		return nil, err
	}
	additions, err := schemaAdditions(existing, request.GetAttributes())
	if err != nil {
		return nil, &RequestError{err: err}
	}
	version, err := s.schemaVersionFrom(ctx, connection)
	if err != nil {
		return nil, err
	}
	if version > 0 && len(additions) == 0 {
		return &dexpb.ApplyFlowIndexSchemaResponse{SchemaVersion: version}, nil
	}

	for _, field := range additions {
		statement := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s`,
			s.qualified, quoteIdentifier(field.Name), columnType(field))
		if _, err := connection.Exec(ctx, statement); err != nil {
			return nil, fmt.Errorf("add flow index field %q: %w", field.Name, err)
		}
	}
	allFields := append(existing, protoFieldsToCatalog(additions)...)
	if err := s.rebuildSearchIndex(ctx, connection, allFields); err != nil {
		return nil, err
	}

	transaction, err := connection.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin flow index catalog transaction: %w", err)
	}
	defer func() {
		rollbackErr := transaction.Rollback(ctx)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			response = nil
			retErr = errors.Join(retErr, fmt.Errorf("rollback flow index catalog: %w", rollbackErr))
		}
	}()
	for _, field := range additions {
		if _, err := transaction.Exec(ctx,
			fmt.Sprintf(`INSERT INTO %s (name, index_type, vector_dimensions, vector_metric) VALUES ($1, $2, $3, $4)`, s.catalog),
			field.GetName(), int32(field.GetType()), field.GetVectorDimensions(), int32(field.GetVectorDistanceMetric())); err != nil {
			return nil, fmt.Errorf("store flow index field %q: %w", field.GetName(), err)
		}
	}
	version++
	if _, err := transaction.Exec(ctx,
		fmt.Sprintf(`UPDATE %s SET schema_version = $1, definition_version = $2 WHERE singleton = TRUE`, s.meta),
		version, definitionVersion); err != nil {
		return nil, fmt.Errorf("activate flow index schema: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit flow index catalog: %w", err)
	}

	addedNames := make([]string, 0, len(additions))
	for _, field := range additions {
		addedNames = append(addedNames, field.GetName())
	}
	return &dexpb.ApplyFlowIndexSchemaResponse{
		SchemaVersion: version,
		Changed:       true,
		AddedFields:   addedNames,
	}, nil
}

func (s *ParadeDBStore) Write(ctx context.Context, input *dexpb.WriteFlowIndexActivityInput) (retErr error) {
	if err := validateWriteInput(input); err != nil {
		return err
	}
	version, err := s.requireSchema(ctx)
	if err != nil {
		return err
	}
	fields, err := s.loadCatalog(ctx)
	if err != nil {
		return err
	}
	fieldByName := make(map[string]catalogField, len(fields))
	for _, field := range fields {
		fieldByName[field.Name] = field
	}

	transaction, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin flow index mutation: %w", err)
	}
	defer func() {
		rollbackErr := transaction.Rollback(ctx)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			retErr = errors.Join(retErr, fmt.Errorf("rollback flow index mutation: %w", rollbackErr))
		}
	}()
	fence, exists, err := s.lockFence(ctx, transaction, input.GetFlowId())
	if err != nil {
		return err
	}
	mutation := input.GetMutation()
	runStartedAt := input.GetRunStartedAt().AsTime()
	if exists && !acceptMutation(fence, input.GetRunId(), runStartedAt, mutation) {
		return transaction.Commit(ctx)
	}
	if !exists {
		if _, err := transaction.Exec(ctx,
			fmt.Sprintf(`INSERT INTO %s ("FlowID", "RunID", "FlowType", "FlowStatus", "StartTime", __dex_run_started_at, __dex_last_sequence, __dex_terminal, __dex_schema_version) VALUES ($1, $2, $3, $4, $5, $5, -1, FALSE, $6)`, s.qualified),
			input.GetFlowId(), input.GetRunId(), input.GetFlowType(), int32(dexpb.FlowStatus_FLOW_STATUS_RUNNING), runStartedAt, version); err != nil {
			return fmt.Errorf("insert flow index row: %w", err)
		}
	}
	if err := s.applyMutation(ctx, transaction, input, fields, fieldByName, version); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit flow index mutation: %w", err)
	}
	return nil
}

func (s *ParadeDBStore) Search(
	ctx context.Context,
	request *dexpb.SearchFlowsRequest,
) (*dexpb.SearchFlowsResponse, error) {
	if request == nil || request.GetPageSize() < 0 || request.GetPageSize() > maxPageSize {
		return nil, &RequestError{err: fmt.Errorf("page size must be between 0 and %d", maxPageSize)}
	}
	version, err := s.requireSchema(ctx)
	if err != nil {
		return nil, err
	}
	fields, err := s.loadCatalog(ctx)
	if err != nil {
		return nil, err
	}
	fieldByName := make(map[string]catalogField, len(fields))
	for _, field := range fields {
		fieldByName[field.Name] = field
	}
	if err := validateVectorQuery(request.GetVectorQuery(), fieldByName); err != nil {
		return nil, &RequestError{err: err}
	}
	fingerprint := searchFingerprint(request)
	offset, err := decodePageToken(request.GetNextPageToken(), fingerprint, version)
	if err != nil {
		return nil, &RequestError{err: err}
	}
	pageSize := int(request.GetPageSize())
	if pageSize == 0 {
		pageSize = defaultPageSize
	}
	statement, arguments := s.searchStatement(request, fieldByName, offset, pageSize+1)
	rows, err := s.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("search ParadeDB flow index: %w", err)
	}
	defer rows.Close()

	response := &dexpb.SearchFlowsResponse{}
	for rows.Next() {
		var rowJSON []byte
		var rank *float64
		if err := rows.Scan(&rowJSON, &rank); err != nil {
			return nil, fmt.Errorf("scan ParadeDB flow index result: %w", err)
		}
		entry, err := decodeSearchEntry(rowJSON, fields)
		if err != nil {
			return nil, err
		}
		if request.GetVectorQuery() != nil {
			entry.VectorDistance = rank
		} else if request.GetQuery() != "" {
			entry.Bm25Score = rank
		}
		response.FlowRuns = append(response.FlowRuns, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ParadeDB flow index results: %w", err)
	}
	if len(response.FlowRuns) > pageSize {
		response.FlowRuns = response.FlowRuns[:pageSize]
		response.NextPageToken, err = encodePageToken(offset+pageSize, fingerprint, version)
		if err != nil {
			return nil, err
		}
	}
	return response, nil
}

func (s *ParadeDBStore) WriteTerminated(ctx context.Context, flowID string, runID string) error {
	if _, err := s.requireSchema(ctx); err != nil {
		return err
	}
	statement := fmt.Sprintf(
		`UPDATE %s SET "FlowStatus" = $2, "CloseTime" = NOW(), __dex_terminal = TRUE, __dex_updated_at = NOW() WHERE "FlowID" = $1 AND __dex_terminal = FALSE`,
		s.qualified,
	)
	arguments := []any{flowID, int32(dexpb.FlowStatus_FLOW_STATUS_TERMINATED)}
	if runID != "" {
		arguments = append(arguments, runID)
		statement += fmt.Sprintf(` AND "RunID" = $%d`, len(arguments))
	}
	result, err := s.pool.Exec(ctx, statement, arguments...)
	if err != nil {
		return fmt.Errorf("write terminated flow index status: %w", err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("flow index row was not available for terminated execution")
	}
	return nil
}

func (s *ParadeDBStore) ensureBaseSchema(ctx context.Context, connection *pgxpool.Conn) error {
	statements := []string{
		`CREATE EXTENSION IF NOT EXISTS vector`,
		fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, quoteIdentifier(s.schema)),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			"FlowID" TEXT PRIMARY KEY,
			"RunID" TEXT NOT NULL,
			"FlowType" TEXT NOT NULL,
			"FlowStatus" BIGINT NOT NULL,
			"StartTime" TIMESTAMPTZ NOT NULL,
			"CloseTime" TIMESTAMPTZ,
			"ActiveStepTypes" TEXT[],
			__dex_run_started_at TIMESTAMPTZ NOT NULL,
			__dex_last_sequence BIGINT NOT NULL,
			__dex_terminal BOOLEAN NOT NULL,
			__dex_schema_version BIGINT NOT NULL,
			__dex_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`, s.qualified),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			name TEXT PRIMARY KEY,
			index_type INTEGER NOT NULL,
			vector_dimensions INTEGER NOT NULL,
			vector_metric INTEGER NOT NULL
		)`, s.catalog),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
			schema_version BIGINT NOT NULL,
			definition_version INTEGER NOT NULL
		)`, s.meta),
		fmt.Sprintf(`INSERT INTO %s (singleton, schema_version, definition_version) VALUES (TRUE, 0, $1) ON CONFLICT (singleton) DO NOTHING`, s.meta),
	}
	for index, statement := range statements {
		var err error
		if index == len(statements)-1 {
			_, err = connection.Exec(ctx, statement, definitionVersion)
		} else {
			_, err = connection.Exec(ctx, statement)
		}
		if err != nil {
			return fmt.Errorf("initialize ParadeDB flow index schema: %w", err)
		}
	}
	return nil
}

func (s *ParadeDBStore) rebuildSearchIndex(
	ctx context.Context,
	connection *pgxpool.Conn,
	fields []catalogField,
) error {
	qualifiedIndex := qualifiedName(s.schema, s.searchIndex)
	candidateName := compactIdentifier(s.searchIndex, "candidate")
	qualifiedCandidate := qualifiedName(s.schema, candidateName)
	if _, err := connection.Exec(ctx, fmt.Sprintf(`DROP INDEX CONCURRENTLY IF EXISTS %s`, qualifiedCandidate)); err != nil {
		return fmt.Errorf("drop stale ParadeDB candidate index: %w", err)
	}
	expressions := []string{
		`"FlowID"`,
		`("RunID"::pdb.literal)`,
		`("FlowType"::pdb.literal)`,
		`"FlowStatus"`,
		`"StartTime"`,
		`"CloseTime"`,
		`("ActiveStepTypes"::pdb.literal)`,
	}
	for _, field := range fields {
		expression := quoteIdentifier(field.Name)
		switch field.Type {
		case dexpb.IndexType_INDEX_TYPE_KEYWORD, dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY:
			expression = fmt.Sprintf(`(%s::pdb.literal)`, expression)
		case dexpb.IndexType_INDEX_TYPE_VECTOR:
			expression += " " + vectorOperatorClass(field.Metric)
		}
		expressions = append(expressions, expression)
	}
	statement := fmt.Sprintf(
		`CREATE INDEX CONCURRENTLY %s ON %s USING paradedb (%s) WITH (key_field = 'FlowID')`,
		quoteIdentifier(candidateName), s.qualified, strings.Join(expressions, ", "))
	if _, err := connection.Exec(ctx, statement); err != nil {
		return fmt.Errorf("create ParadeDB candidate index: %w", err)
	}
	var valid bool
	if err := connection.QueryRow(ctx,
		`SELECT indisvalid AND indisready AND indislive FROM pg_index WHERE indexrelid = $1::regclass`,
		qualifiedCandidate).Scan(&valid); err != nil {
		return fmt.Errorf("validate ParadeDB candidate index: %w", err)
	}
	if !valid {
		return fmt.Errorf("ParadeDB candidate index is not valid")
	}
	if _, err := connection.Exec(ctx, fmt.Sprintf(`DROP INDEX CONCURRENTLY IF EXISTS %s`, qualifiedIndex)); err != nil {
		return fmt.Errorf("drop previous ParadeDB search index: %w", err)
	}
	if _, err := connection.Exec(ctx,
		fmt.Sprintf(`ALTER INDEX %s RENAME TO %s`, qualifiedCandidate, quoteIdentifier(s.searchIndex))); err != nil {
		return fmt.Errorf("activate ParadeDB candidate index: %w", err)
	}
	return nil
}

func (s *ParadeDBStore) schemaVersion(ctx context.Context) (int64, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, s.meta).Scan(&exists); err != nil {
		return 0, fmt.Errorf("find flow index schema catalog: %w", err)
	}
	if !exists {
		return 0, nil
	}
	var version int64
	err := s.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT schema_version FROM %s WHERE singleton = TRUE`, s.meta)).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("read flow index schema version: %w", err)
	}
	return version, nil
}

func (s *ParadeDBStore) schemaVersionFrom(ctx context.Context, connection *pgxpool.Conn) (int64, error) {
	var version int64
	if err := connection.QueryRow(ctx,
		fmt.Sprintf(`SELECT schema_version FROM %s WHERE singleton = TRUE`, s.meta)).Scan(&version); err != nil {
		return 0, fmt.Errorf("read flow index schema version: %w", err)
	}
	return version, nil
}

func (s *ParadeDBStore) requireSchema(ctx context.Context) (int64, error) {
	version, err := s.schemaVersion(ctx)
	if err != nil {
		return 0, err
	}
	if version == 0 {
		return 0, ErrSchemaNotApplied
	}
	return version, nil
}

func (s *ParadeDBStore) loadCatalog(ctx context.Context) ([]catalogField, error) {
	version, err := s.schemaVersion(ctx)
	if err != nil || version == 0 {
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		fmt.Sprintf(`SELECT name, index_type, vector_dimensions, vector_metric FROM %s ORDER BY name`, s.catalog))
	if err != nil {
		return nil, fmt.Errorf("read flow index catalog: %w", err)
	}
	defer rows.Close()
	return scanCatalog(rows)
}

func (s *ParadeDBStore) loadCatalogFrom(ctx context.Context, connection *pgxpool.Conn) ([]catalogField, error) {
	rows, err := connection.Query(ctx,
		fmt.Sprintf(`SELECT name, index_type, vector_dimensions, vector_metric FROM %s ORDER BY name`, s.catalog))
	if err != nil {
		return nil, fmt.Errorf("read flow index catalog: %w", err)
	}
	defer rows.Close()
	return scanCatalog(rows)
}

func (s *ParadeDBStore) lockFence(
	ctx context.Context,
	transaction pgx.Tx,
	flowID string,
) (storedFence, bool, error) {
	var fence storedFence
	err := transaction.QueryRow(ctx,
		fmt.Sprintf(`SELECT "RunID", __dex_run_started_at, __dex_last_sequence, __dex_terminal FROM %s WHERE "FlowID" = $1 FOR UPDATE`, s.qualified),
		flowID).Scan(&fence.RunID, &fence.RunStartedAt, &fence.LastSequence, &fence.Terminal)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedFence{}, false, nil
	}
	if err != nil {
		return storedFence{}, false, fmt.Errorf("lock flow index fence: %w", err)
	}
	return fence, true, nil
}

func (s *ParadeDBStore) applyMutation(
	ctx context.Context,
	transaction pgx.Tx,
	input *dexpb.WriteFlowIndexActivityInput,
	fields []catalogField,
	fieldByName map[string]catalogField,
	version int64,
) error {
	mutation := input.GetMutation()
	values := make(map[string]*dexpb.Value, len(mutation.GetUpserts())+len(mutation.GetDeletes()))
	if mutation.GetReplace() {
		for _, field := range fields {
			values[field.Name] = nil
		}
		values["ActiveStepTypes"] = nil
	}
	for _, name := range mutation.GetDeletes() {
		if _, exists := fieldByName[name]; !exists {
			return fmt.Errorf("flow index field %q is not in the applied schema", name)
		}
		values[name] = nil
	}
	for name, value := range mutation.GetUpserts() {
		if name == "ActiveStepTypes" {
			values[name] = value
			continue
		}
		if _, exists := fieldByName[name]; !exists {
			return fmt.Errorf("flow index field %q is not in the applied schema", name)
		}
		values[name] = value
	}

	assignments := []string{
		`"RunID" = $2`,
		`"FlowType" = $3`,
		`"StartTime" = $4`,
		`__dex_run_started_at = $4`,
		`__dex_last_sequence = $5`,
		`__dex_schema_version = $6`,
		`__dex_updated_at = NOW()`,
	}
	if mutation.GetReplace() {
		assignments = append(assignments, `"CloseTime" = NULL`)
	}
	arguments := []any{
		input.GetFlowId(), input.GetRunId(), input.GetFlowType(), input.GetRunStartedAt().AsTime(),
		mutation.GetSequence(), version,
	}
	if mutation.GetFlowStatus() != dexpb.FlowStatus_FLOW_STATUS_UNSPECIFIED {
		arguments = append(arguments, int32(mutation.GetFlowStatus()))
		assignments = append(assignments, fmt.Sprintf(`"FlowStatus" = $%d`, len(arguments)))
		terminal := isTerminalStatus(mutation.GetFlowStatus())
		arguments = append(arguments, terminal)
		assignments = append(assignments, fmt.Sprintf(`__dex_terminal = $%d`, len(arguments)))
	}
	if mutation.GetCloseTime() != nil {
		arguments = append(arguments, mutation.GetCloseTime().AsTime())
		assignments = append(assignments, fmt.Sprintf(`"CloseTime" = $%d`, len(arguments)))
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := values[name]
		if value == nil {
			assignments = append(assignments, quoteIdentifier(name)+` = NULL`)
			continue
		}
		if name == "ActiveStepTypes" {
			var stepTypes []string
			if err := json.Unmarshal(objectOrStringPayload(value), &stepTypes); err != nil {
				return fmt.Errorf("encode active step types: %w", err)
			}
			arguments = append(arguments, stepTypes)
			assignments = append(assignments, fmt.Sprintf(`"ActiveStepTypes" = $%d`, len(arguments)))
			continue
		}
		argument, cast, err := indexValue(fieldByName[name], value)
		if err != nil {
			return fmt.Errorf("encode flow index field %q: %w", name, err)
		}
		arguments = append(arguments, argument)
		assignments = append(assignments,
			fmt.Sprintf(`%s = $%d%s`, quoteIdentifier(name), len(arguments), cast))
	}
	statement := fmt.Sprintf(`UPDATE %s SET %s WHERE "FlowID" = $1`, s.qualified, strings.Join(assignments, ", "))
	if _, err := transaction.Exec(ctx, statement, arguments...); err != nil {
		return fmt.Errorf("write flow index mutation: %w", err)
	}
	return nil
}

func (s *ParadeDBStore) searchStatement(
	request *dexpb.SearchFlowsRequest,
	fieldByName map[string]catalogField,
	offset int,
	limit int,
) (string, []any) {
	where := `"FlowID" @@@ pdb.all()`
	arguments := []any{}
	if request.GetQuery() != "" {
		arguments = append(arguments, request.GetQuery())
		where = fmt.Sprintf(`"FlowID" @@@ pdb.parse($%d)`, len(arguments))
	}
	rank := `NULL::DOUBLE PRECISION`
	order := `"FlowID" ASC`
	if vectorQuery := request.GetVectorQuery(); vectorQuery != nil {
		field := fieldByName[vectorQuery.GetIndexKey()]
		arguments = append(arguments, vectorLiteral(vectorQuery.GetVector()))
		rank = fmt.Sprintf(`%s %s $%d::vector`, quoteIdentifier(field.Name), vectorDistanceOperator(field.Metric), len(arguments))
		order = rank + ` ASC, "FlowID" ASC`
	} else if request.GetQuery() != "" {
		rank = `pdb.score("FlowID")`
		order = rank + ` DESC, "FlowID" ASC`
	}
	arguments = append(arguments, limit, offset)
	statement := fmt.Sprintf(
		`SELECT to_jsonb(flow_row), %s FROM %s AS flow_row WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d`,
		rank, s.qualified, where, order, len(arguments)-1, len(arguments))
	return statement, arguments
}

func validateSchemaRequest(request *dexpb.ApplyFlowIndexSchemaRequest) error {
	if request == nil || request.GetDefinitionVersion() != definitionVersion {
		return fmt.Errorf("definition_version must be %d", definitionVersion)
	}
	seen := map[string]struct{}{}
	for _, field := range request.GetAttributes() {
		if field == nil {
			return fmt.Errorf("flow index field is required")
		}
		if err := validateIdentifier(field.GetName()); err != nil {
			return fmt.Errorf("invalid flow index field %q: %w", field.GetName(), err)
		}
		if isReservedField(field.GetName()) {
			return fmt.Errorf("flow index field %q is reserved", field.GetName())
		}
		if _, exists := seen[field.GetName()]; exists {
			return fmt.Errorf("duplicate flow index field %q", field.GetName())
		}
		seen[field.GetName()] = struct{}{}
		if err := validateField(field); err != nil {
			return fmt.Errorf("invalid flow index field %q: %w", field.GetName(), err)
		}
	}
	return nil
}

func validateField(field *dexpb.FlowIndexField) error {
	switch field.GetType() {
	case dexpb.IndexType_INDEX_TYPE_KEYWORD,
		dexpb.IndexType_INDEX_TYPE_TEXT,
		dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY,
		dexpb.IndexType_INDEX_TYPE_INT,
		dexpb.IndexType_INDEX_TYPE_DOUBLE,
		dexpb.IndexType_INDEX_TYPE_BOOL,
		dexpb.IndexType_INDEX_TYPE_DATETIME:
		if field.GetVectorDimensions() != 0 || field.GetVectorDistanceMetric() != dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_UNSPECIFIED {
			return fmt.Errorf("vector settings require vector type")
		}
		return nil
	case dexpb.IndexType_INDEX_TYPE_VECTOR:
		if field.GetVectorDimensions() <= 0 || field.GetVectorDimensions() > 16000 {
			return fmt.Errorf("vector_dimensions must be between 1 and 16000")
		}
		switch field.GetVectorDistanceMetric() {
		case dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_L2,
			dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_COSINE,
			dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_INNER_PRODUCT:
			return nil
		default:
			return fmt.Errorf("vector_distance_metric is required")
		}
	default:
		return fmt.Errorf("index type is required")
	}
}

func schemaAdditions(
	existing []catalogField,
	requested []*dexpb.FlowIndexField,
) ([]*dexpb.FlowIndexField, error) {
	existingByName := make(map[string]catalogField, len(existing))
	for _, field := range existing {
		existingByName[field.Name] = field
	}
	requestedByName := make(map[string]*dexpb.FlowIndexField, len(requested))
	additions := make([]*dexpb.FlowIndexField, 0)
	for _, field := range requested {
		requestedByName[field.GetName()] = field
		current, exists := existingByName[field.GetName()]
		if !exists {
			additions = append(additions, field)
			continue
		}
		if current.Type != field.GetType() || current.Dimensions != field.GetVectorDimensions() || current.Metric != field.GetVectorDistanceMetric() {
			return nil, fmt.Errorf(
				"schema diff changes field %q: existing=%s requested=%s",
				field.GetName(), formatCatalogField(current), formatProtoField(field),
			)
		}
	}
	for _, field := range existing {
		if _, exists := requestedByName[field.Name]; !exists {
			return nil, fmt.Errorf("schema diff removes field %q: existing=%s", field.Name, formatCatalogField(field))
		}
	}
	return additions, nil
}

func formatCatalogField(field catalogField) string {
	return fmt.Sprintf("{type:%s dimensions:%d metric:%s}", field.Type, field.Dimensions, field.Metric)
}

func formatProtoField(field *dexpb.FlowIndexField) string {
	return fmt.Sprintf(
		"{type:%s dimensions:%d metric:%s}",
		field.GetType(), field.GetVectorDimensions(), field.GetVectorDistanceMetric(),
	)
}

func validateWriteInput(input *dexpb.WriteFlowIndexActivityInput) error {
	if input == nil || input.GetMutation() == nil || input.GetFlowId() == "" || input.GetRunId() == "" || input.GetFlowType() == "" {
		return fmt.Errorf("flow ID, run ID, flow type, and mutation are required")
	}
	if input.GetRunStartedAt() == nil || !input.GetRunStartedAt().IsValid() {
		return fmt.Errorf("valid run_started_at is required")
	}
	if input.GetMutation().GetSequence() < 0 {
		return fmt.Errorf("mutation sequence must be non-negative")
	}
	return nil
}

func acceptMutation(
	fence storedFence,
	runID string,
	runStartedAt time.Time,
	mutation *dexpb.FlowIndexMutation,
) bool {
	if runStartedAt.Before(fence.RunStartedAt) {
		return false
	}
	if runStartedAt.Equal(fence.RunStartedAt) && runID != fence.RunID {
		return false
	}
	if runID == fence.RunID {
		if mutation.GetSequence() <= fence.LastSequence || fence.Terminal {
			return false
		}
	}
	return true
}

func indexValue(field catalogField, value *dexpb.Value) (any, string, error) {
	switch field.Type {
	case dexpb.IndexType_INDEX_TYPE_KEYWORD, dexpb.IndexType_INDEX_TYPE_TEXT:
		return stringIndexValue(value)
	case dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY:
		var values []string
		if err := json.Unmarshal(objectOrStringPayload(value), &values); err != nil {
			return nil, "", err
		}
		return values, "", nil
	case dexpb.IndexType_INDEX_TYPE_INT:
		if typed, ok := value.GetKind().(*dexpb.Value_IntValue); ok {
			return typed.IntValue, "", nil
		}
	case dexpb.IndexType_INDEX_TYPE_DOUBLE:
		switch typed := value.GetKind().(type) {
		case *dexpb.Value_DoubleValue:
			return typed.DoubleValue, "", nil
		case *dexpb.Value_IntValue:
			return float64(typed.IntValue), "", nil
		}
	case dexpb.IndexType_INDEX_TYPE_BOOL:
		if typed, ok := value.GetKind().(*dexpb.Value_BoolValue); ok {
			return typed.BoolValue, "", nil
		}
	case dexpb.IndexType_INDEX_TYPE_DATETIME:
		if typed, ok := value.GetKind().(*dexpb.Value_StringValue); ok {
			timestamp, err := time.Parse(time.RFC3339Nano, typed.StringValue)
			if err != nil {
				return nil, "", err
			}
			return timestamp, "", nil
		}
	case dexpb.IndexType_INDEX_TYPE_VECTOR:
		var vector []float64
		if err := json.Unmarshal(objectOrStringPayload(value), &vector); err != nil {
			return nil, "", err
		}
		if len(vector) != int(field.Dimensions) {
			return nil, "", fmt.Errorf("expected %d dimensions, got %d", field.Dimensions, len(vector))
		}
		for _, component := range vector {
			if math.IsInf(component, 0) || math.IsNaN(component) {
				return nil, "", fmt.Errorf("vector components must be finite")
			}
		}
		return vectorLiteral64(vector), "::vector", nil
	}
	return nil, "", fmt.Errorf("value does not match %s", field.Type.String())
}

func stringIndexValue(value *dexpb.Value) (any, string, error) {
	switch typed := value.GetKind().(type) {
	case *dexpb.Value_StringValue:
		return typed.StringValue, "", nil
	case *dexpb.Value_ObjValue:
		return string(typed.ObjValue.GetPayload()), "", nil
	case *dexpb.Value_IntValue:
		return strconv.FormatInt(typed.IntValue, 10), "", nil
	case *dexpb.Value_DoubleValue:
		return strconv.FormatFloat(typed.DoubleValue, 'g', -1, 64), "", nil
	case *dexpb.Value_BoolValue:
		return strconv.FormatBool(typed.BoolValue), "", nil
	default:
		return nil, "", fmt.Errorf("value is not string-compatible")
	}
}

func validateVectorQuery(query *dexpb.SearchVectorQuery, fields map[string]catalogField) error {
	if query == nil {
		return nil
	}
	field, exists := fields[query.GetIndexKey()]
	if !exists || field.Type != dexpb.IndexType_INDEX_TYPE_VECTOR {
		return fmt.Errorf("vector field %q is not in the applied schema", query.GetIndexKey())
	}
	if len(query.GetVector()) != int(field.Dimensions) {
		return fmt.Errorf("vector field %q requires %d dimensions", query.GetIndexKey(), field.Dimensions)
	}
	for _, component := range query.GetVector() {
		if math.IsInf(float64(component), 0) || math.IsNaN(float64(component)) {
			return fmt.Errorf("vector components must be finite")
		}
	}
	return nil
}

func decodeSearchEntry(rowJSON []byte, fields []catalogField) (*dexpb.SearchFlowsResponseEntry, error) {
	var row map[string]json.RawMessage
	if err := json.Unmarshal(rowJSON, &row); err != nil {
		return nil, fmt.Errorf("decode ParadeDB flow index row: %w", err)
	}
	entry := &dexpb.SearchFlowsResponseEntry{}
	if err := decodeJSONField(row, "FlowID", &entry.FlowId); err != nil {
		return nil, err
	}
	if err := decodeJSONField(row, "RunID", &entry.RunId); err != nil {
		return nil, err
	}
	if err := decodeJSONField(row, "FlowType", &entry.FlowType); err != nil {
		return nil, err
	}
	var status int32
	if err := decodeJSONField(row, "FlowStatus", &status); err != nil {
		return nil, err
	}
	entry.FlowStatus = dexpb.FlowStatus(status)
	entry.StartTime = decodeTimestamp(row["StartTime"])
	entry.CloseTime = decodeTimestamp(row["CloseTime"])
	for _, field := range fields {
		raw := row[field.Name]
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		value, err := jsonIndexValue(field, raw)
		if err != nil {
			return nil, fmt.Errorf("decode flow index field %q: %w", field.Name, err)
		}
		entry.SearchAttributes = append(entry.SearchAttributes, &dexpb.KV{Key: field.Name, Value: value})
	}
	return entry, nil
}

func jsonIndexValue(field catalogField, raw json.RawMessage) (*dexpb.Value, error) {
	switch field.Type {
	case dexpb.IndexType_INDEX_TYPE_KEYWORD, dexpb.IndexType_INDEX_TYPE_TEXT, dexpb.IndexType_INDEX_TYPE_DATETIME:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: value}}, nil
	case dexpb.IndexType_INDEX_TYPE_INT:
		var value int64
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: value}}, nil
	case dexpb.IndexType_INDEX_TYPE_DOUBLE:
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return &dexpb.Value{Kind: &dexpb.Value_DoubleValue{DoubleValue: value}}, nil
	case dexpb.IndexType_INDEX_TYPE_BOOL:
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return &dexpb.Value{Kind: &dexpb.Value_BoolValue{BoolValue: value}}, nil
	case dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY, dexpb.IndexType_INDEX_TYPE_VECTOR:
		payload := raw
		if field.Type == dexpb.IndexType_INDEX_TYPE_VECTOR && len(raw) > 0 && raw[0] == '"' {
			var encoded string
			if err := json.Unmarshal(raw, &encoded); err != nil {
				return nil, err
			}
			payload = []byte(encoded)
		}
		return &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: payload}}}, nil
	default:
		return nil, fmt.Errorf("unsupported index type %s", field.Type.String())
	}
}

func scanCatalog(rows pgx.Rows) ([]catalogField, error) {
	fields := make([]catalogField, 0)
	for rows.Next() {
		var field catalogField
		var indexType int32
		var metric int32
		if err := rows.Scan(&field.Name, &indexType, &field.Dimensions, &metric); err != nil {
			return nil, fmt.Errorf("scan flow index catalog: %w", err)
		}
		field.Type = dexpb.IndexType(indexType)
		field.Metric = dexpb.VectorDistanceMetric(metric)
		fields = append(fields, field)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate flow index catalog: %w", err)
	}
	return fields, nil
}

func systemFields() []*dexpb.FlowIndexField {
	return []*dexpb.FlowIndexField{
		{Name: "FlowID", Type: dexpb.IndexType_INDEX_TYPE_KEYWORD, System: true},
		{Name: "RunID", Type: dexpb.IndexType_INDEX_TYPE_KEYWORD, System: true},
		{Name: "FlowType", Type: dexpb.IndexType_INDEX_TYPE_KEYWORD, System: true},
		{Name: "FlowStatus", Type: dexpb.IndexType_INDEX_TYPE_INT, System: true},
		{Name: "StartTime", Type: dexpb.IndexType_INDEX_TYPE_DATETIME, System: true},
		{Name: "CloseTime", Type: dexpb.IndexType_INDEX_TYPE_DATETIME, System: true},
		{Name: "ActiveStepTypes", Type: dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY, System: true},
	}
}

func protoFieldsToCatalog(fields []*dexpb.FlowIndexField) []catalogField {
	result := make([]catalogField, 0, len(fields))
	for _, field := range fields {
		result = append(result, catalogField{
			Name: field.GetName(), Type: field.GetType(), Dimensions: field.GetVectorDimensions(), Metric: field.GetVectorDistanceMetric(),
		})
	}
	return result
}

func columnType(field *dexpb.FlowIndexField) string {
	switch field.GetType() {
	case dexpb.IndexType_INDEX_TYPE_KEYWORD, dexpb.IndexType_INDEX_TYPE_TEXT:
		return "TEXT"
	case dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY:
		return "TEXT[]"
	case dexpb.IndexType_INDEX_TYPE_INT:
		return "BIGINT"
	case dexpb.IndexType_INDEX_TYPE_DOUBLE:
		return "DOUBLE PRECISION"
	case dexpb.IndexType_INDEX_TYPE_BOOL:
		return "BOOLEAN"
	case dexpb.IndexType_INDEX_TYPE_DATETIME:
		return "TIMESTAMPTZ"
	case dexpb.IndexType_INDEX_TYPE_VECTOR:
		return fmt.Sprintf("VECTOR(%d)", field.GetVectorDimensions())
	default:
		panic("validated flow index field has unsupported type")
	}
}

func vectorOperatorClass(metric dexpb.VectorDistanceMetric) string {
	switch metric {
	case dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_L2:
		return "vector_l2_ops"
	case dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_COSINE:
		return "vector_cosine_ops"
	case dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_INNER_PRODUCT:
		return "vector_ip_ops"
	default:
		panic("validated vector field has unsupported metric")
	}
}

func vectorDistanceOperator(metric dexpb.VectorDistanceMetric) string {
	switch metric {
	case dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_L2:
		return "<->"
	case dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_COSINE:
		return "<=>"
	case dexpb.VectorDistanceMetric_VECTOR_DISTANCE_METRIC_INNER_PRODUCT:
		return "<#>"
	default:
		panic("validated vector field has unsupported metric")
	}
}

func isReservedField(name string) bool {
	for _, field := range systemFields() {
		if strings.EqualFold(name, field.GetName()) {
			return true
		}
	}
	return strings.HasPrefix(strings.ToLower(name), "__dex_")
}

func validateIdentifier(identifier string) error {
	if !utf8.ValidString(identifier) || len(identifier) > 63 || !identifierPattern.MatchString(identifier) {
		return fmt.Errorf("must match %s and be at most 63 bytes", identifierPattern.String())
	}
	return nil
}

func qualifiedName(schema string, name string) string {
	return quoteIdentifier(schema) + "." + quoteIdentifier(name)
}

func quoteIdentifier(identifier string) string {
	return pgx.Identifier{identifier}.Sanitize()
}

func compactIdentifier(base string, suffix string) string {
	candidate := base + "_" + suffix
	if len(candidate) <= 63 {
		return candidate
	}
	hash := sha256.Sum256([]byte(candidate))
	hashText := fmt.Sprintf("%x", hash[:6])
	return candidate[:50] + "_" + hashText
}

func objectOrStringPayload(value *dexpb.Value) []byte {
	switch typed := value.GetKind().(type) {
	case *dexpb.Value_ObjValue:
		return typed.ObjValue.GetPayload()
	case *dexpb.Value_StringValue:
		return []byte(typed.StringValue)
	default:
		return nil
	}
}

func isTerminalStatus(status dexpb.FlowStatus) bool {
	return status != dexpb.FlowStatus_FLOW_STATUS_UNSPECIFIED && status != dexpb.FlowStatus_FLOW_STATUS_RUNNING
}

func decodeTimestamp(raw json.RawMessage) *timestamppb.Timestamp {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, encoded)
	if err != nil {
		return nil
	}
	return timestamppb.New(parsed)
}

func decodeJSONField(row map[string]json.RawMessage, name string, target any) error {
	if err := json.Unmarshal(row[name], target); err != nil {
		return fmt.Errorf("decode ParadeDB system field %q: %w", name, err)
	}
	return nil
}

func searchFingerprint(request *dexpb.SearchFlowsRequest) string {
	hash := sha256.New()
	hash.Write([]byte(request.GetQuery()))
	if vectorQuery := request.GetVectorQuery(); vectorQuery != nil {
		hash.Write([]byte(vectorQuery.GetIndexKey()))
		for _, component := range vectorQuery.GetVector() {
			hash.Write([]byte(strconv.FormatFloat(float64(component), 'g', -1, 32)))
		}
	}
	return base64.RawURLEncoding.EncodeToString(hash.Sum(nil))
}

func decodePageToken(encoded string, fingerprint string, version int64) (int, error) {
	if encoded == "" {
		return 0, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return 0, fmt.Errorf("invalid next page token")
	}
	var token searchPageToken
	if err := json.Unmarshal(payload, &token); err != nil || token.Offset < 0 || token.Fingerprint != fingerprint || token.Version != version {
		return 0, fmt.Errorf("invalid next page token")
	}
	return token.Offset, nil
}

func encodePageToken(offset int, fingerprint string, version int64) (string, error) {
	payload, err := json.Marshal(searchPageToken{Offset: offset, Fingerprint: fingerprint, Version: version})
	if err != nil {
		return "", fmt.Errorf("encode next page token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func vectorLiteral(vector []float32) string {
	values := make([]string, 0, len(vector))
	for _, component := range vector {
		values = append(values, strconv.FormatFloat(float64(component), 'g', -1, 32))
	}
	return "[" + strings.Join(values, ",") + "]"
}

func vectorLiteral64(vector []float64) string {
	values := make([]string, 0, len(vector))
	for _, component := range vector {
		values = append(values, strconv.FormatFloat(component, 'g', -1, 64))
	}
	return "[" + strings.Join(values, ",") + "]"
}
