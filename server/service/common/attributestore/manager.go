// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package attributestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/log"
	"github.com/superdurable/dex/service/common/log/tag"
	"golang.org/x/sync/errgroup"
)

type Manager struct {
	cfg     *config.AttributeStoreConfig
	logger  log.Logger
	entries map[string]*storeEntry
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

type storeEntry struct {
	name   string
	cfg    config.AttributeStoreConfigEntry
	db     *sql.DB
	table  tableReference
	schema atomic.Pointer[tableSchema]
	logger log.Logger
}

type tableReference struct {
	namespace string
	table     string
}

type tableSchema struct {
	reference  tableReference
	primaryKey string
	columns    map[string]columnSchema
}

type columnSchema struct {
	name             string
	dataType         string
	columnType       string
	nullable         bool
	characterMaximum *int64
	numericPrecision *int64
	numericScale     *int64
	hasDefault       bool
	generated        bool
}

type filteredValue struct {
	column columnSchema
	value  any
}

func NewManager(
	ctx context.Context,
	cfg *config.AttributeStoreConfig,
	logger log.Logger,
) (*Manager, error) {
	if cfg == nil || logger == nil {
		panic("Attribute Store Manager requires config and logger")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	managerCtx, cancel := context.WithCancel(ctx)
	manager := &Manager{
		cfg:     cfg,
		logger:  logger,
		entries: make(map[string]*storeEntry, len(cfg.Stores)),
		cancel:  cancel,
	}
	if err := manager.openEntries(managerCtx); err != nil {
		cancel()
		if closeErr := manager.closeDatabases(); closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		return nil, err
	}
	manager.startRefreshers(managerCtx)
	return manager, nil
}

func (m *Manager) openEntries(ctx context.Context) error {
	group, groupCtx := errgroup.WithContext(ctx)
	opened := make(chan *storeEntry, len(m.cfg.Stores))
	for name, entryCfg := range m.cfg.Stores {
		name := name
		entryCfg := entryCfg
		group.Go(func() error {
			entry, err := openStoreEntry(groupCtx, name, entryCfg, m.logger)
			if err != nil {
				return fmt.Errorf("initialize Attribute Store %q: %w", name, err)
			}
			opened <- entry
			return nil
		})
	}
	err := group.Wait()
	close(opened)
	for entry := range opened {
		m.entries[entry.name] = entry
	}
	return err
}

func openStoreEntry(
	ctx context.Context,
	name string,
	cfg config.AttributeStoreConfigEntry,
	logger log.Logger,
) (*storeEntry, error) {
	driverName := "pgx"
	if cfg.Type == config.AttributeStoreTypeMySQL {
		driverName = "mysql"
	}
	database, err := sql.Open(driverName, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("open connection: %w", err)
	}
	entry := &storeEntry{
		name:   name,
		cfg:    cfg,
		db:     database,
		logger: logger.WithTags(tag.Value(name)),
	}
	if err := database.PingContext(ctx); err != nil {
		if closeErr := database.Close(); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("ping database: %w", err), closeErr)
		}
		return nil, fmt.Errorf("ping database: %w", err)
	}
	reference, err := entry.resolveTableReference(ctx)
	if err != nil {
		if closeErr := database.Close(); closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		return nil, err
	}
	entry.table = reference
	snapshot, err := entry.describe(ctx)
	if err != nil {
		if closeErr := database.Close(); closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		return nil, err
	}
	entry.schema.Store(snapshot)
	return entry, nil
}

func (e *storeEntry) resolveTableReference(ctx context.Context) (tableReference, error) {
	parts := strings.Split(e.cfg.TableName, ".")
	if len(parts) > 2 || len(parts) == 0 {
		return tableReference{}, fmt.Errorf("tableName must be table or namespace.table")
	}
	for _, part := range parts {
		if part == "" || strings.IndexByte(part, 0) >= 0 {
			return tableReference{}, fmt.Errorf("tableName contains an invalid identifier")
		}
	}
	if len(parts) == 2 {
		return tableReference{namespace: parts[0], table: parts[1]}, nil
	}
	var namespace string
	query := "SELECT current_schema()"
	if e.cfg.Type == config.AttributeStoreTypeMySQL {
		query = "SELECT DATABASE()"
	}
	if err := e.db.QueryRowContext(ctx, query).Scan(&namespace); err != nil {
		return tableReference{}, fmt.Errorf("resolve table namespace: %w", err)
	}
	if namespace == "" {
		return tableReference{}, fmt.Errorf("database has no active namespace")
	}
	return tableReference{namespace: namespace, table: parts[0]}, nil
}

func (e *storeEntry) describe(ctx context.Context) (*tableSchema, error) {
	columns, err := e.describeColumns(ctx)
	if err != nil {
		return nil, err
	}
	primaryKeys, err := e.describePrimaryKeys(ctx)
	if err != nil {
		return nil, err
	}
	if len(primaryKeys) != 1 {
		return nil, fmt.Errorf("table must have exactly one primary-key column")
	}
	primaryKey, found := columns[primaryKeys[0]]
	if !found {
		return nil, fmt.Errorf("primary-key column was not described")
	}
	if !primaryKey.acceptsString() {
		return nil, fmt.Errorf("primary-key column %q cannot store FlowID strings", primaryKey.name)
	}
	for name, column := range columns {
		if name == primaryKey.name {
			continue
		}
		if !column.nullable && !column.hasDefault && !column.generated {
			return nil, fmt.Errorf("column %q prevents partial row inserts", name)
		}
	}
	return &tableSchema{
		reference:  e.table,
		primaryKey: primaryKey.name,
		columns:    columns,
	}, nil
}

func (e *storeEntry) describeColumns(ctx context.Context) (map[string]columnSchema, error) {
	query := `SELECT column_name, data_type, udt_name, is_nullable,
character_maximum_length, numeric_precision, numeric_scale, column_default,
is_identity, is_generated
FROM information_schema.columns
WHERE table_schema = $1 AND table_name = $2
ORDER BY ordinal_position`
	if e.cfg.Type == config.AttributeStoreTypeMySQL {
		query = `SELECT column_name, data_type, column_type, is_nullable,
character_maximum_length, numeric_precision, numeric_scale, column_default,
extra, generation_expression
FROM information_schema.columns
WHERE table_schema = ? AND table_name = ?
ORDER BY ordinal_position`
	}
	rows, err := e.db.QueryContext(ctx, query, e.table.namespace, e.table.table)
	if err != nil {
		return nil, fmt.Errorf("describe columns: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			e.logger.Error("close Attribute Store describe rows", tag.Error(closeErr))
		}
	}()
	columns := map[string]columnSchema{}
	for rows.Next() {
		column, scanErr := scanColumn(rows, e.cfg.Type)
		if scanErr != nil {
			return nil, scanErr
		}
		columns[column.name] = column
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate described columns: %w", err)
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("table does not exist or has no columns")
	}
	return columns, nil
}

func scanColumn(rows *sql.Rows, storeType config.AttributeStoreType) (columnSchema, error) {
	var (
		column                               columnSchema
		dataType, columnType, nullable       string
		characterMaximum, precision, scale   sql.NullInt64
		defaultValue, generatedA, generatedB sql.NullString
	)
	if err := rows.Scan(
		&column.name,
		&dataType,
		&columnType,
		&nullable,
		&characterMaximum,
		&precision,
		&scale,
		&defaultValue,
		&generatedA,
		&generatedB,
	); err != nil {
		return columnSchema{}, fmt.Errorf("scan described column: %w", err)
	}
	column.dataType = strings.ToLower(dataType)
	column.columnType = strings.ToLower(columnType)
	column.nullable = strings.EqualFold(nullable, "YES")
	column.characterMaximum = nullableInt64(characterMaximum)
	column.numericPrecision = nullableInt64(precision)
	column.numericScale = nullableInt64(scale)
	column.hasDefault = defaultValue.Valid
	if storeType == config.AttributeStoreTypePostgres {
		column.generated = strings.EqualFold(generatedA.String, "YES") ||
			strings.EqualFold(generatedB.String, "ALWAYS")
	} else {
		column.generated = generatedA.String != "" || generatedB.String != ""
	}
	return column, nil
}

func nullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func (e *storeEntry) describePrimaryKeys(ctx context.Context) ([]string, error) {
	query := `SELECT kcu.column_name
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
  ON tc.constraint_name = kcu.constraint_name
 AND tc.table_schema = kcu.table_schema
 AND tc.table_name = kcu.table_name
WHERE tc.constraint_type = 'PRIMARY KEY'
  AND tc.table_schema = $1 AND tc.table_name = $2
ORDER BY kcu.ordinal_position`
	if e.cfg.Type == config.AttributeStoreTypeMySQL {
		query = `SELECT column_name
FROM information_schema.key_column_usage
WHERE constraint_name = 'PRIMARY' AND table_schema = ? AND table_name = ?
ORDER BY ordinal_position`
	}
	rows, err := e.db.QueryContext(ctx, query, e.table.namespace, e.table.table)
	if err != nil {
		return nil, fmt.Errorf("describe primary key: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			e.logger.Error("close Attribute Store primary-key rows", tag.Error(closeErr))
		}
	}()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan primary key: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate primary keys: %w", err)
	}
	return keys, nil
}

func (m *Manager) startRefreshers(ctx context.Context) {
	for _, entry := range m.entries {
		entry := entry
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			entry.refreshLoop(ctx, m.cfg.EffectiveSchemaSyncInterval())
		}()
	}
}

func (e *storeEntry) refreshLoop(ctx context.Context, interval time.Duration) {
	random := rand.New(rand.NewSource(time.Now().UnixNano()))
	for {
		timer := time.NewTimer(jitterInterval(interval, random.Float64()))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
		snapshot, err := e.describe(ctx)
		if err != nil {
			e.logger.Error("refresh Attribute Store schema", tag.Error(err))
			continue
		}
		e.schema.Store(snapshot)
	}
}

func jitterInterval(interval time.Duration, sample float64) time.Duration {
	return time.Duration(float64(interval) * (0.9 + sample*0.2))
}

func (m *Manager) HasStore(name string) bool {
	_, found := m.entries[name]
	return found
}

func (m *Manager) WriteBatch(ctx context.Context, input *dexpb.SyncAttributeBatchActivityInput) (int, error) {
	if input == nil || input.GetFlowId() == "" || input.GetConfigName() == "" {
		return 0, fmt.Errorf("Attribute Store batch requires FlowID and config name")
	}
	entry, found := m.entries[input.GetConfigName()]
	if !found {
		return 0, fmt.Errorf("Attribute Store %q is unavailable", input.GetConfigName())
	}
	return entry.writeBatch(ctx, input.GetFlowId(), input.GetMutations())
}

func (e *storeEntry) writeBatch(
	ctx context.Context,
	flowID string,
	mutations []*dexpb.AttributeSyncMutation,
) (int, error) {
	snapshot := e.schema.Load()
	if snapshot == nil {
		return 0, fmt.Errorf("Attribute Store schema is unavailable")
	}
	latest := make(map[string]*dexpb.Value, len(mutations))
	for _, mutation := range mutations {
		if mutation == nil {
			continue
		}
		latest[mutation.GetKey()] = mutation.GetValue()
	}
	filtered := make(map[string]filteredValue, len(latest))
	for name, value := range latest {
		column, found := snapshot.columns[name]
		if !found {
			e.logger.Error("skip Attribute Store mutation: column does not exist", tag.Value(name))
			continue
		}
		if name == snapshot.primaryKey {
			e.logger.Error("skip Attribute Store mutation: primary key is immutable", tag.Value(name))
			continue
		}
		converted, err := column.convert(value, e.cfg.Type)
		if err != nil {
			e.logger.Error("skip incompatible Attribute Store mutation", tag.Value(name), tag.Error(err))
			continue
		}
		filtered[name] = filteredValue{column: column, value: converted}
	}
	if len(filtered) == 0 {
		return len(latest), nil
	}
	filteredCount := len(latest) - len(filtered)
	query, arguments := e.buildUpsert(snapshot, flowID, filtered)
	if _, err := e.db.ExecContext(ctx, query, arguments...); err != nil {
		return filteredCount, fmt.Errorf("execute Attribute Store upsert: %w", err)
	}
	return filteredCount, nil
}

func (e *storeEntry) buildUpsert(
	snapshot *tableSchema,
	flowID string,
	values map[string]filteredValue,
) (string, []any) {
	columnNames := make([]string, 0, len(values))
	for name := range values {
		columnNames = append(columnNames, name)
	}
	sort.Strings(columnNames)
	quotedTable := e.quoteIdentifier(snapshot.reference.namespace) + "." +
		e.quoteIdentifier(snapshot.reference.table)
	columns := []string{e.quoteIdentifier(snapshot.primaryKey)}
	placeholders := []string{e.placeholder(1)}
	updates := make([]string, 0, len(columnNames))
	arguments := []any{flowID}
	for index, name := range columnNames {
		quoted := e.quoteIdentifier(name)
		columns = append(columns, quoted)
		placeholders = append(placeholders, e.placeholder(index+2))
		arguments = append(arguments, values[name].value)
		if e.cfg.Type == config.AttributeStoreTypePostgres {
			updates = append(updates, quoted+" = EXCLUDED."+quoted)
		} else {
			updates = append(updates, quoted+" = VALUES("+quoted+")")
		}
	}
	query := "INSERT INTO " + quotedTable + " (" + strings.Join(columns, ", ") + ") VALUES (" +
		strings.Join(placeholders, ", ") + ")"
	if e.cfg.Type == config.AttributeStoreTypePostgres {
		query += " ON CONFLICT (" + e.quoteIdentifier(snapshot.primaryKey) + ") DO UPDATE SET " +
			strings.Join(updates, ", ")
	} else {
		query += " ON DUPLICATE KEY UPDATE " + strings.Join(updates, ", ")
	}
	return query, arguments
}

func (e *storeEntry) placeholder(position int) string {
	if e.cfg.Type == config.AttributeStoreTypePostgres {
		return "$" + strconv.Itoa(position)
	}
	return "?"
}

func (e *storeEntry) quoteIdentifier(identifier string) string {
	if e.cfg.Type == config.AttributeStoreTypePostgres {
		return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
	}
	return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
}

func (c columnSchema) convert(value *dexpb.Value, storeType config.AttributeStoreType) (any, error) {
	if value == nil || value.GetKind() == nil {
		return nil, fmt.Errorf("value is missing")
	}
	switch kind := value.GetKind().(type) {
	case *dexpb.Value_NullValue:
		if !c.nullable {
			return nil, fmt.Errorf("column is not nullable")
		}
		return nil, nil
	case *dexpb.Value_StringValue:
		if !c.acceptsString() {
			return nil, fmt.Errorf("column does not accept strings")
		}
		if c.characterMaximum != nil && int64(utf8.RuneCountInString(kind.StringValue)) > *c.characterMaximum {
			return nil, fmt.Errorf("string exceeds column length")
		}
		return kind.StringValue, nil
	case *dexpb.Value_IntValue:
		if !c.acceptsInt(kind.IntValue, storeType) {
			return nil, fmt.Errorf("column cannot store int64 value")
		}
		return kind.IntValue, nil
	case *dexpb.Value_DoubleValue:
		if !c.acceptsDouble() || math.IsNaN(kind.DoubleValue) || math.IsInf(kind.DoubleValue, 0) {
			return nil, fmt.Errorf("column cannot store double value")
		}
		return kind.DoubleValue, nil
	case *dexpb.Value_BoolValue:
		if !c.acceptsBool(storeType) {
			return nil, fmt.Errorf("column cannot store bool value")
		}
		return kind.BoolValue, nil
	case *dexpb.Value_ObjValue:
		return c.convertObject(kind.ObjValue, storeType)
	case *dexpb.Value_InternalBlobIdForStringValue, *dexpb.Value_InternalBlobIdForObjValue:
		return nil, fmt.Errorf("blob-backed value was not hydrated")
	default:
		return nil, fmt.Errorf("unsupported Attribute value")
	}
}

func (c columnSchema) acceptsString() bool {
	switch c.dataType {
	case "character", "character varying", "text", "char", "varchar", "tinytext", "mediumtext", "longtext":
		return true
	default:
		return false
	}
}

func (c columnSchema) acceptsInt(value int64, storeType config.AttributeStoreType) bool {
	unsigned := storeType == config.AttributeStoreTypeMySQL && strings.Contains(c.columnType, "unsigned")
	if unsigned && value < 0 {
		return false
	}
	switch c.dataType {
	case "tinyint":
		return intInRange(value, unsigned, math.MinInt8, math.MaxInt8, math.MaxUint8)
	case "smallint":
		return intInRange(value, unsigned, math.MinInt16, math.MaxInt16, math.MaxUint16)
	case "mediumint":
		return intInRange(value, unsigned, -8388608, 8388607, 16777215)
	case "integer", "int":
		return intInRange(value, unsigned, math.MinInt32, math.MaxInt32, math.MaxUint32)
	case "bigint":
		return true
	case "numeric", "decimal":
		if c.numericScale != nil && *c.numericScale != 0 {
			return false
		}
		if c.numericPrecision == nil {
			return true
		}
		digits := len(strconv.FormatInt(value, 10))
		if value < 0 {
			digits--
		}
		return int64(digits) <= *c.numericPrecision
	default:
		return false
	}
}

func intInRange(value int64, unsigned bool, signedMinimum, signedMaximum, unsignedMaximum int64) bool {
	if unsigned {
		return value <= unsignedMaximum
	}
	return value >= signedMinimum && value <= signedMaximum
}

func (c columnSchema) acceptsDouble() bool {
	switch c.dataType {
	case "real", "double precision", "float", "double", "numeric", "decimal":
		return true
	default:
		return false
	}
}

func (c columnSchema) acceptsBool(storeType config.AttributeStoreType) bool {
	if storeType == config.AttributeStoreTypePostgres {
		return c.dataType == "boolean"
	}
	return c.columnType == "tinyint(1)" || c.columnType == "bit(1)" || c.dataType == "boolean" || c.dataType == "bool"
}

func (c columnSchema) convertObject(
	object *dexpb.EncodedObject,
	storeType config.AttributeStoreType,
) (any, error) {
	if object == nil {
		return nil, fmt.Errorf("object is missing")
	}
	if strings.EqualFold(object.GetEncoding(), "json") {
		if !json.Valid(object.GetPayload()) {
			return nil, fmt.Errorf("JSON payload is invalid")
		}
		if c.dataType != "json" && c.dataType != "jsonb" {
			return nil, fmt.Errorf("column does not accept JSON")
		}
		if storeType == config.AttributeStoreTypePostgres {
			return string(object.GetPayload()), nil
		}
		return object.GetPayload(), nil
	}
	if !c.acceptsBinary(storeType) {
		return nil, fmt.Errorf("column does not accept binary objects")
	}
	if c.characterMaximum != nil && int64(len(object.GetPayload())) > *c.characterMaximum {
		return nil, fmt.Errorf("object exceeds column length")
	}
	return object.GetPayload(), nil
}

func (c columnSchema) acceptsBinary(storeType config.AttributeStoreType) bool {
	if storeType == config.AttributeStoreTypePostgres {
		return c.dataType == "bytea"
	}
	switch c.dataType {
	case "binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob":
		return true
	default:
		return false
	}
}

func (m *Manager) Close() error {
	m.cancel()
	m.wg.Wait()
	return m.closeDatabases()
}

func (m *Manager) closeDatabases() error {
	var errs []error
	for name, entry := range m.entries {
		if err := entry.db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close Attribute Store %q: %w", name, err))
		}
	}
	return errors.Join(errs...)
}
