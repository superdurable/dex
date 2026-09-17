// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package attributestore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/log"
)

type warehouseMetadataConnector struct {
	catalog   string
	namespace string
}

type warehouseMetadataConnection struct {
	catalog   string
	namespace string
}

type warehouseMetadataRows struct {
	values  []driver.Value
	wasRead bool
}

type warehouseContractConnector struct {
	mu           sync.Mutex
	metadataRows [][]driver.Value
	queryErr     error
	pingCount    int
	execQueries  []string
	execArgs     [][]driver.NamedValue
	isClosed     bool
}

type warehouseContractConnection struct {
	connector *warehouseContractConnector
}

type warehouseContractRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func TestWarehouseTableReferenceResolution(t *testing.T) {
	database := sql.OpenDB(&warehouseMetadataConnector{catalog: "analytics", namespace: "reporting"})
	t.Cleanup(func() { require.NoError(t, database.Close()) })

	for _, storeType := range []config.AttributeStoreType{
		config.AttributeStoreTypeDatabricks,
		config.AttributeStoreTypeSnowflake,
	} {
		dialect, err := newSQLDialect(storeType)
		require.NoError(t, err)

		reference, err := dialect.resolveTableReference(context.Background(), database, "flow_attributes")
		require.NoError(t, err)
		require.Equal(t, tableReference{catalog: "analytics", namespace: "reporting", table: "flow_attributes"}, reference)

		reference, err = dialect.resolveTableReference(context.Background(), database, "audit.flow_attributes")
		require.NoError(t, err)
		require.Equal(t, tableReference{catalog: "analytics", namespace: "audit", table: "flow_attributes"}, reference)

		reference, err = dialect.resolveTableReference(context.Background(), database, "other.audit.flow_attributes")
		require.NoError(t, err)
		require.Equal(t, tableReference{catalog: "other", namespace: "audit", table: "flow_attributes"}, reference)

		_, err = dialect.resolveTableReference(context.Background(), database, "too.many.name.parts")
		require.ErrorContains(t, err, "at most 3")
	}
}

func TestWarehouseFlowIDColumnValidation(t *testing.T) {
	store := &sqlStore{cfg: config.AttributeStoreConfigEntry{
		Type: config.AttributeStoreTypeSnowflake, FlowIDColumn: "flow_id",
	}}
	columns := map[string]columnSchema{
		"flow_id": {name: "flow_id", dataType: "text"},
	}
	flowIDColumn, err := store.resolveFlowIDColumn(context.Background(), columns)
	require.NoError(t, err)
	require.Equal(t, "flow_id", flowIDColumn)

	columns["flow_id"] = columnSchema{name: "flow_id", dataType: "text", nullable: true}
	_, err = store.resolveFlowIDColumn(context.Background(), columns)
	require.ErrorContains(t, err, "non-null")

	delete(columns, "flow_id")
	_, err = store.resolveFlowIDColumn(context.Background(), columns)
	require.ErrorContains(t, err, "does not exist")
}

func TestWarehouseDescribeColumnsQueries(t *testing.T) {
	reference := tableReference{catalog: "analytics", namespace: "reporting", table: "flow_attributes"}

	databricks, err := newSQLDialect(config.AttributeStoreTypeDatabricks)
	require.NoError(t, err)
	query, arguments := databricks.describeColumnsQuery(reference)
	require.Contains(t, query, "FROM `analytics`.`information_schema`.`columns`")
	require.Contains(t, query, "full_data_type")
	require.Equal(t, []any{"reporting", "flow_attributes"}, arguments)

	snowflake, err := newSQLDialect(config.AttributeStoreTypeSnowflake)
	require.NoError(t, err)
	query, arguments = snowflake.describeColumnsQuery(reference)
	require.Contains(t, query, `FROM "analytics"."information_schema"."columns"`)
	require.Contains(t, query, "data_type_alias")
	require.Contains(t, query, "expression")
	require.Equal(t, []any{"reporting", "flow_attributes"}, arguments)
}

func TestParseDatabricksColumnDefinitions(t *testing.T) {
	definitions, err := parseDatabricksColumnDefinitions(`CREATE TABLE `+"`analytics`.`reporting`.`flow attributes`"+` (
  `+"`flow``id`"+` STRING NOT NULL,
  `+"`profile`"+` STRUCT<name: STRING, tags: ARRAY<STRING>>,
  `+"`created_at`"+` TIMESTAMP DEFAULT current_timestamp(),
  `+"`derived`"+` STRING GENERATED ALWAYS AS (concat(`+"`flow``id`"+`, ','))
) USING delta`, "`analytics`.`reporting`.`flow attributes`")
	require.NoError(t, err)
	require.Equal(t, "STRING NOT NULL", definitions["flow`id"])
	require.Contains(t, definitions["profile"], "STRUCT<name: STRING, tags: ARRAY<STRING>>")
	require.True(t, databricksDefaultPattern.MatchString(definitions["created_at"]))
	require.True(t, databricksGeneratedPattern.MatchString(definitions["derived"]))
	require.False(t, databricksDefaultPattern.MatchString(sqlOutsideQuotedValues("STRING COMMENT 'DEFAULT value'")))
	require.False(t, databricksGeneratedPattern.MatchString(sqlOutsideQuotedValues("STRING COMMENT 'GENERATED ALWAYS AS'")))
}

func TestWarehouseSQLStoreContract(t *testing.T) {
	for _, storeType := range []config.AttributeStoreType{
		config.AttributeStoreTypeDatabricks,
		config.AttributeStoreTypeSnowflake,
	} {
		t.Run(string(storeType), func(t *testing.T) {
			connector := &warehouseContractConnector{metadataRows: warehouseContractMetadata(storeType)}
			database := sql.OpenDB(connector)
			dialect, err := newSQLDialect(storeType)
			require.NoError(t, err)
			store, err := initializeSQLStore(context.Background(), config.AttributeStoreConfigEntry{
				Type:         storeType,
				TableName:    "analytics.reporting.flow_attributes",
				FlowIDColumn: "flow_id",
			}, log.NewNoop(), dialect, database)
			require.NoError(t, err)
			require.Equal(t, 1, connector.pingCount)
			require.Equal(t, "flow_id", store.schema.Load().flowIDColumn)

			err = store.writeBatch(context.Background(), "flow-1", []*dexpb.AttributeSyncItem{
				{Key: "name", Value: stringValue("first")},
				{Key: "profile", Value: objectValue("json", `{"active":true}`)},
				{Key: "name", Value: stringValue("latest")},
			})
			require.NoError(t, err)
			connector.mu.Lock()
			execQueries := append([]string(nil), connector.execQueries...)
			execArguments := append([][]driver.NamedValue(nil), connector.execArgs...)
			connector.metadataRows = append(connector.metadataRows, warehouseMetadataColumn(
				storeType, "late_column", warehouseStringType(storeType), warehouseStringType(storeType), "YES",
			))
			connector.mu.Unlock()
			require.Len(t, execQueries, 1)
			require.Contains(t, execQueries[0], "MERGE INTO")
			require.NotContains(t, execQueries[0], "latest")
			require.Equal(t, "flow-1", execArguments[0][0].Value)
			require.Equal(t, "latest", execArguments[0][1].Value)
			require.Equal(t, `{"active":true}`, execArguments[0][2].Value)

			require.NoError(t, store.refreshSchema(context.Background()))
			require.Contains(t, store.schema.Load().columns, "late_column")
			retainedSnapshot := store.schema.Load()
			connector.mu.Lock()
			connector.queryErr = errors.New("metadata unavailable")
			connector.mu.Unlock()
			require.ErrorContains(t, store.refreshSchema(context.Background()), "metadata unavailable")
			require.Same(t, retainedSnapshot, store.schema.Load())

			connector.mu.Lock()
			connector.queryErr = nil
			executionCount := len(connector.execQueries)
			connector.mu.Unlock()
			err = store.writeBatch(context.Background(), "flow-1", []*dexpb.AttributeSyncItem{
				{Key: "flow_id", Value: stringValue("replacement")},
				{Key: "missing", Value: stringValue("ignored")},
				{Key: "profile", Value: objectValue("json", `{invalid`)},
			})
			require.NoError(t, err)
			connector.mu.Lock()
			actualExecutionCount := len(connector.execQueries)
			connector.mu.Unlock()
			require.Equal(t, executionCount, actualExecutionCount)

			require.NoError(t, store.close())
			connector.mu.Lock()
			isClosed := connector.isClosed
			connector.mu.Unlock()
			require.True(t, isClosed)
		})
	}
}

func warehouseContractMetadata(storeType config.AttributeStoreType) [][]driver.Value {
	return [][]driver.Value{
		warehouseMetadataColumn(storeType, "flow_id", warehouseStringType(storeType), warehouseStringType(storeType), "NO"),
		warehouseMetadataColumn(storeType, "name", warehouseStringType(storeType), warehouseStringType(storeType), "YES"),
		warehouseMetadataColumn(storeType, "profile", "variant", "variant", "YES"),
	}
}

func warehouseStringType(storeType config.AttributeStoreType) string {
	if storeType == config.AttributeStoreTypeDatabricks {
		return "string"
	}
	return "varchar"
}

func warehouseMetadataColumn(
	storeType config.AttributeStoreType,
	name, dataType, columnType, nullable string,
) []driver.Value {
	return []driver.Value{name, dataType, columnType, nullable, nil, nil, nil, nil, "NO", nil}
}

func (c *warehouseMetadataConnector) Connect(context.Context) (driver.Conn, error) {
	return &warehouseMetadataConnection{catalog: c.catalog, namespace: c.namespace}, nil
}

func (c *warehouseMetadataConnector) Driver() driver.Driver {
	return warehouseMetadataDriver{}
}

type warehouseMetadataDriver struct{}

func (warehouseMetadataDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

func (c *warehouseMetadataConnection) QueryContext(
	context.Context,
	string,
	[]driver.NamedValue,
) (driver.Rows, error) {
	return &warehouseMetadataRows{values: []driver.Value{c.catalog, c.namespace}}, nil
}

func (c *warehouseMetadataConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is unsupported")
}

func (c *warehouseMetadataConnection) Close() error {
	return nil
}

func (c *warehouseMetadataConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are unsupported")
}

func (r *warehouseMetadataRows) Columns() []string {
	return []string{"catalog", "namespace"}
}

func (r *warehouseMetadataRows) Close() error {
	return nil
}

func (r *warehouseMetadataRows) Next(destination []driver.Value) error {
	if r.wasRead {
		return io.EOF
	}
	copy(destination, r.values)
	r.wasRead = true
	return nil
}

func (c *warehouseContractConnector) Connect(context.Context) (driver.Conn, error) {
	return &warehouseContractConnection{connector: c}, nil
}

func (c *warehouseContractConnector) Driver() driver.Driver {
	return warehouseMetadataDriver{}
}

func (c *warehouseContractConnection) Ping(context.Context) error {
	c.connector.mu.Lock()
	defer c.connector.mu.Unlock()
	c.connector.pingCount++
	return nil
}

func (c *warehouseContractConnection) QueryContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	c.connector.mu.Lock()
	defer c.connector.mu.Unlock()
	if c.connector.queryErr != nil {
		return nil, c.connector.queryErr
	}
	if strings.Contains(strings.ToLower(query), "information_schema") {
		values := make([][]driver.Value, len(c.connector.metadataRows))
		for index, row := range c.connector.metadataRows {
			values[index] = append([]driver.Value(nil), row...)
		}
		return &warehouseContractRows{
			columns: []string{"column_name", "data_type", "column_type", "is_nullable", "character_maximum_length", "numeric_precision", "numeric_scale", "column_default", "is_identity", "is_generated"},
			values:  values,
		}, nil
	}
	if strings.HasPrefix(strings.ToUpper(query), "SHOW CREATE TABLE") {
		return &warehouseContractRows{
			columns: []string{"createtab_stmt"},
			values:  [][]driver.Value{{c.databricksCreateStatement()}},
		}, nil
	}
	return &warehouseContractRows{
		columns: []string{"catalog", "namespace"},
		values:  [][]driver.Value{{"analytics", "reporting"}},
	}, nil
}

func (c *warehouseContractConnection) databricksCreateStatement() string {
	definitions := make([]string, 0, len(c.connector.metadataRows))
	for _, row := range c.connector.metadataRows {
		name := strings.ReplaceAll(row[0].(string), "`", "``")
		definition := fmt.Sprintf("`%s` %s", name, row[1].(string))
		if row[3] == "NO" {
			definition += " NOT NULL"
		}
		definitions = append(definitions, definition)
	}
	return "CREATE TABLE `analytics`.`reporting`.`flow_attributes` (" + strings.Join(definitions, ", ") + ") USING delta"
}

func (c *warehouseContractConnection) ExecContext(
	_ context.Context,
	query string,
	arguments []driver.NamedValue,
) (driver.Result, error) {
	c.connector.mu.Lock()
	defer c.connector.mu.Unlock()
	c.connector.execQueries = append(c.connector.execQueries, query)
	c.connector.execArgs = append(c.connector.execArgs, append([]driver.NamedValue(nil), arguments...))
	return driver.RowsAffected(1), nil
}

func (c *warehouseContractConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is unsupported")
}

func (c *warehouseContractConnection) Close() error {
	c.connector.mu.Lock()
	defer c.connector.mu.Unlock()
	c.connector.isClosed = true
	return nil
}

func (c *warehouseContractConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are unsupported")
}

func (r *warehouseContractRows) Columns() []string {
	return r.columns
}

func (r *warehouseContractRows) Close() error {
	return nil
}

func (r *warehouseContractRows) Next(destination []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(destination, r.values[r.index])
	r.index++
	return nil
}
