// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

//go:build attributestore_integration

package attributestore

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/log"
)

func TestMySQLAndPostgresAttributeStoreIntegration(t *testing.T) {
	ctx := context.Background()
	postgresDSN := environmentOrDefault(
		"DEX_ATTRIBUTE_STORE_POSTGRES_DSN",
		"postgres://dex:dex@127.0.0.1:55432/dex?sslmode=disable",
	)
	mysqlDSN := environmentOrDefault(
		"DEX_ATTRIBUTE_STORE_MYSQL_DSN",
		"dex:dex@tcp(127.0.0.1:53306)/dex?parseTime=true",
	)
	postgres := openIntegrationDatabase(t, "pgx", postgresDSN)
	mysql := openIntegrationDatabase(t, "mysql", mysqlDSN)
	prepareIntegrationTables(t, postgres, mysql)
	assertInvalidSchemasFailStartup(t, postgresDSN, postgres)

	manager, err := NewManager(ctx, &config.AttributeStoreConfig{
		SchemaSyncInterval: 50 * time.Millisecond,
		Stores: map[string]config.AttributeStoreConfigEntry{
			"reporting": {
				Type:      config.AttributeStoreTypePostgres,
				DSN:       postgresDSN,
				TableName: "public.flow_attributes",
			},
			"operational": {
				Type:      config.AttributeStoreTypeMySQL,
				DSN:       mysqlDSN,
				TableName: "dex.flow_attributes",
			},
		},
	}, log.NewNoop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })

	writeIntegrationBatch(t, manager, "reporting", "postgres-flow")
	writeIntegrationBatch(t, manager, "operational", "mysql-flow")
	assertIntegrationRow(t, postgres, "postgres-flow")
	assertIntegrationRow(t, mysql, "mysql-flow")

	previousSnapshot := manager.entries["reporting"].schema.Load()
	_, err = postgres.ExecContext(ctx, `ALTER TABLE flow_attributes RENAME TO flow_attributes_hidden`)
	require.NoError(t, err)
	require.Never(t, func() bool {
		return manager.entries["reporting"].schema.Load() != previousSnapshot
	}, 200*time.Millisecond, 20*time.Millisecond)
	_, err = postgres.ExecContext(ctx, `ALTER TABLE flow_attributes_hidden RENAME TO flow_attributes`)
	require.NoError(t, err)
	_, err = postgres.ExecContext(ctx, `ALTER TABLE flow_attributes ADD COLUMN late_column TEXT`)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return manager.entries["reporting"].schema.Load().columns["late_column"].name == "late_column"
	}, 3*time.Second, 20*time.Millisecond)
	filteredCount, err := manager.WriteBatch(ctx, &dexpb.SyncAttributeBatchActivityInput{
		FlowId:     "postgres-flow",
		ConfigName: "reporting",
		Mutations: []*dexpb.AttributeSyncMutation{{
			ConfigName: "reporting",
			Key:        "late_column",
			Value:      stringValue("available"),
		}},
	})
	require.NoError(t, err)
	require.Zero(t, filteredCount)
	var lateColumn string
	require.NoError(t, postgres.QueryRowContext(ctx,
		`SELECT late_column FROM flow_attributes WHERE flow_id = $1`, "postgres-flow").Scan(&lateColumn))
	require.Equal(t, "available", lateColumn)
}

func assertInvalidSchemasFailStartup(t *testing.T, dsn string, postgres *sql.DB) {
	t.Helper()
	statements := []string{
		`DROP TABLE IF EXISTS no_primary_key`,
		`CREATE TABLE no_primary_key (flow_id TEXT)`,
		`DROP TABLE IF EXISTS composite_primary_key`,
		`CREATE TABLE composite_primary_key (first_id TEXT, second_id TEXT, PRIMARY KEY (first_id, second_id))`,
		`DROP TABLE IF EXISTS integer_primary_key`,
		`CREATE TABLE integer_primary_key (flow_id BIGINT PRIMARY KEY)`,
		`DROP TABLE IF EXISTS required_column`,
		`CREATE TABLE required_column (flow_id TEXT PRIMARY KEY, required_value TEXT NOT NULL)`,
	}
	for _, statement := range statements {
		_, err := postgres.ExecContext(context.Background(), statement)
		require.NoError(t, err)
	}
	for _, tableName := range []string{
		"missing_table",
		"no_primary_key",
		"composite_primary_key",
		"integer_primary_key",
		"required_column",
	} {
		_, err := NewManager(context.Background(), &config.AttributeStoreConfig{
			Stores: map[string]config.AttributeStoreConfigEntry{
				"invalid": {
					Type:      config.AttributeStoreTypePostgres,
					DSN:       dsn,
					TableName: "public." + tableName,
				},
			},
		}, log.NewNoop())
		require.Error(t, err, tableName)
	}
}

func openIntegrationDatabase(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	database, err := sql.Open(driver, dsn)
	require.NoError(t, err)
	require.NoError(t, database.PingContext(context.Background()))
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	return database
}

func prepareIntegrationTables(t *testing.T, postgres, mysql *sql.DB) {
	t.Helper()
	statements := []struct {
		database *sql.DB
		query    string
	}{
		{postgres, `DROP TABLE IF EXISTS flow_attributes`},
		{postgres, `CREATE TABLE flow_attributes (
flow_id TEXT PRIMARY KEY,
name VARCHAR(32), count_value BIGINT, ratio DOUBLE PRECISION, active BOOLEAN,
json_value JSONB, binary_value BYTEA, nullable_value TEXT)`},
		{mysql, `DROP TABLE IF EXISTS flow_attributes`},
		{mysql, `CREATE TABLE flow_attributes (
flow_id VARCHAR(255) PRIMARY KEY,
name VARCHAR(32), count_value BIGINT, ratio DOUBLE, active BOOLEAN,
json_value JSON, binary_value BLOB, nullable_value TEXT)`},
	}
	for _, statement := range statements {
		_, err := statement.database.ExecContext(context.Background(), statement.query)
		require.NoError(t, err)
	}
}

func writeIntegrationBatch(t *testing.T, manager *Manager, configName, flowID string) {
	t.Helper()
	filteredCount, err := manager.WriteBatch(context.Background(), &dexpb.SyncAttributeBatchActivityInput{
		FlowId:     flowID,
		ConfigName: configName,
		Mutations: []*dexpb.AttributeSyncMutation{
			{ConfigName: configName, Key: "name", Value: stringValue("old")},
			{ConfigName: configName, Key: "count_value", Value: intValue(42)},
			{ConfigName: configName, Key: "ratio", Value: doubleValue(2.5)},
			{ConfigName: configName, Key: "active", Value: boolValue(true)},
			{ConfigName: configName, Key: "json_value", Value: objectValue("json", `{"ok":true}`)},
			{ConfigName: configName, Key: "binary_value", Value: objectValue("proto", "bytes")},
			{ConfigName: configName, Key: "nullable_value", Value: nullValue()},
			{ConfigName: configName, Key: "missing", Value: stringValue("filtered")},
			{ConfigName: configName, Key: "name", Value: stringValue("latest")},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, filteredCount)
}

func assertIntegrationRow(t *testing.T, database *sql.DB, flowID string) {
	t.Helper()
	placeholder := "?"
	if flowID == "postgres-flow" {
		placeholder = "$1"
	}
	var (
		name          string
		countValue    int64
		ratio         float64
		active        bool
		jsonValue     []byte
		binaryValue   []byte
		nullableValue sql.NullString
	)
	err := database.QueryRowContext(context.Background(),
		"SELECT name, count_value, ratio, active, json_value, binary_value, nullable_value FROM flow_attributes WHERE flow_id = "+placeholder,
		flowID,
	).Scan(&name, &countValue, &ratio, &active, &jsonValue, &binaryValue, &nullableValue)
	require.NoError(t, err)
	require.Equal(t, "latest", name)
	require.Equal(t, int64(42), countValue)
	require.Equal(t, 2.5, ratio)
	require.True(t, active)
	require.JSONEq(t, `{"ok":true}`, string(jsonValue))
	require.Equal(t, []byte("bytes"), binaryValue)
	require.False(t, nullableValue.Valid)
}

func doubleValue(value float64) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_DoubleValue{DoubleValue: value}}
}

func environmentOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
