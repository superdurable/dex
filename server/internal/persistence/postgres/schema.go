// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"

	"github.com/superdurable/dex/server/config"
)

// Per-store DDL. Each store owns its own database (config.For(store)). The
// statements are idempotent (CREATE ... IF NOT EXISTS) so they can run on
// every deploy. Mirrors the Mongo indexes in mongo/schema.go and the
// collection layout. Production applies these via schema/v0.sql; tests apply
// them through EnsureSchemaForConfig.

const ddlShards = `
CREATE TABLE IF NOT EXISTS shards (
	shard_id          INTEGER PRIMARY KEY,
	version           BIGINT NOT NULL,
	member_id         TEXT NOT NULL,
	claimed_at        TIMESTAMPTZ NOT NULL,
	lease_expires_at  TIMESTAMPTZ NOT NULL,
	released_at       TIMESTAMPTZ,
	metadata          JSONB NOT NULL
);`

const ddlRuns = `
CREATE TABLE IF NOT EXISTS runs (
	shard_id                          INTEGER NOT NULL,
	namespace                         TEXT NOT NULL,
	id                                TEXT NOT NULL,
	flow_type                         TEXT NOT NULL DEFAULT '',
	task_list_name                    TEXT NOT NULL DEFAULT '',
	status                            INTEGER NOT NULL DEFAULT 0,
	version                           BIGINT NOT NULL,
	worker_id                         TEXT NOT NULL DEFAULT '',
	state_map                         JSONB NOT NULL DEFAULT '{}',
	unconsumed_channel_messages       JSONB NOT NULL DEFAULT '{}',
	step_exe_id_counters              JSONB NOT NULL DEFAULT '{}',
	active_step_executions            JSONB NOT NULL DEFAULT '{}',
	step_method_exe_counter           BIGINT NOT NULL DEFAULT 0,
	worker_request_counter            BIGINT NOT NULL DEFAULT 0,
	external_channel_message_counter  BIGINT NOT NULL DEFAULT 0,
	last_heartbeat_time               TIMESTAMPTZ,
	heartbeat_timer_id                UUID,
	active_durable_timer_id           UUID,
	durable_timer_fire_at             BIGINT NOT NULL DEFAULT 0,
	durable_timer_fired               BOOLEAN NOT NULL DEFAULT FALSE,
	last_history_event_id             BIGINT NOT NULL DEFAULT 0,
	created_at                        TIMESTAMPTZ NOT NULL,
	updated_at                        TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (shard_id, namespace, id)
);
CREATE TABLE IF NOT EXISTS immediate_tasks (
	shard_id    INTEGER NOT NULL,
	sort_key    BIGINT NOT NULL,
	id          UUID NOT NULL,
	task_type   INTEGER NOT NULL,
	task_info   JSONB NOT NULL,
	created_at  TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (shard_id, sort_key, id)
);
CREATE TABLE IF NOT EXISTS timer_tasks (
	shard_id    INTEGER NOT NULL,
	sort_key    BIGINT NOT NULL,
	id          UUID NOT NULL,
	task_type   INTEGER NOT NULL,
	task_info   JSONB NOT NULL,
	created_at  TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (shard_id, sort_key, id)
);
CREATE TABLE IF NOT EXISTS opsfifo_tasks (
	shard_id    INTEGER NOT NULL,
	sort_key    BIGINT NOT NULL,
	id          UUID NOT NULL,
	task_type   INTEGER NOT NULL,
	payload     JSONB NOT NULL,
	created_at  TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (shard_id, sort_key, id)
);
CREATE TABLE IF NOT EXISTS task_dlq (
	shard_id        INTEGER NOT NULL,
	task_id         UUID NOT NULL,
	queue_type      INTEGER NOT NULL,
	task_type       INTEGER NOT NULL,
	run_id          TEXT NOT NULL,
	namespace       TEXT NOT NULL,
	task_list_name  TEXT NOT NULL,
	sort_key        BIGINT NOT NULL,
	error           TEXT NOT NULL,
	error_category  TEXT NOT NULL,
	created_at      TIMESTAMPTZ NOT NULL,
	dlq_at          TIMESTAMPTZ NOT NULL,
	member_id       TEXT NOT NULL,
	PRIMARY KEY (shard_id, task_id)
);
CREATE INDEX IF NOT EXISTS task_dlq_poll_idx ON task_dlq (shard_id, dlq_at);`

const ddlBlobs = `
CREATE TABLE IF NOT EXISTS blobs (
	shard_id   INTEGER NOT NULL,
	namespace  TEXT NOT NULL,
	run_id     TEXT NOT NULL,
	id         UUID NOT NULL,
	encoding   TEXT NOT NULL,
	payload    BYTEA,
	PRIMARY KEY (shard_id, namespace, run_id, id)
);`

const ddlTasklists = `
CREATE TABLE IF NOT EXISTS tasklist_metadata (
	namespace        TEXT NOT NULL,
	tasklist_name    TEXT NOT NULL,
	partition_id     INTEGER NOT NULL,
	range_id         INTEGER NOT NULL,
	ack_level        BIGINT NOT NULL,
	owner_member_id  TEXT NOT NULL,
	owner_address    TEXT NOT NULL,
	claimed_at       TIMESTAMPTZ NOT NULL,
	updated_at       TIMESTAMPTZ,
	PRIMARY KEY (namespace, tasklist_name, partition_id)
);
CREATE TABLE IF NOT EXISTS tasklist_tasks (
	namespace      TEXT NOT NULL,
	tasklist_name  TEXT NOT NULL,
	partition_id   INTEGER NOT NULL,
	task_id        BIGINT NOT NULL,
	run_id         TEXT NOT NULL,
	shard_id       INTEGER NOT NULL,
	created_at     TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (namespace, tasklist_name, partition_id, task_id)
);`

const ddlVisibility = `
CREATE TABLE IF NOT EXISTS visibility (
	namespace       TEXT NOT NULL,
	run_id          TEXT NOT NULL,
	flow_type       TEXT NOT NULL,
	task_list_name  TEXT NOT NULL,
	status          INTEGER NOT NULL,
	start_time      TIMESTAMPTZ NOT NULL,
	updated_at      TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (namespace, run_id)
);
CREATE INDEX IF NOT EXISTS visibility_by_start_time_idx ON visibility (namespace, flow_type, status, start_time DESC, run_id);
CREATE INDEX IF NOT EXISTS visibility_by_updated_at_idx ON visibility (namespace, flow_type, status, updated_at DESC, run_id);`

const ddlHistory = `
CREATE TABLE IF NOT EXISTS history (
	run_id          TEXT NOT NULL,
	event_id        BIGINT NOT NULL,
	namespace       TEXT NOT NULL,
	occurred_at_ms  BIGINT NOT NULL,
	worker_id       TEXT NOT NULL,
	payload_type    TEXT NOT NULL,
	payload         BYTEA NOT NULL,
	PRIMARY KEY (run_id, event_id)
);`

// ddlForStore returns the DDL block owning a logical store.
func ddlForStore(store string) string {
	switch store {
	case config.StoreShards:
		return ddlShards
	case config.StoreRuns:
		return ddlRuns
	case config.StoreBlobs:
		return ddlBlobs
	case config.StoreTasklists:
		return ddlTasklists
	case config.StoreVisibility:
		return ddlVisibility
	case config.StoreHistory:
		return ddlHistory
	default:
		return ""
	}
}

// tablesForStore lists the tables owned by a store (for TRUNCATE on test reset).
func tablesForStore(store string) []string {
	switch store {
	case config.StoreShards:
		return []string{"shards"}
	case config.StoreRuns:
		return []string{"runs", "immediate_tasks", "timer_tasks", "opsfifo_tasks", "task_dlq"}
	case config.StoreBlobs:
		return []string{"blobs"}
	case config.StoreTasklists:
		return []string{"tasklist_metadata", "tasklist_tasks"}
	case config.StoreVisibility:
		return []string{"visibility"}
	case config.StoreHistory:
		return []string{"history"}
	default:
		return nil
	}
}

var validDBName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// EnsureSchemaForConfig connects to each per-store database (resolved via
// cfg.For(store)), creating the database if missing, then drops + recreates
// the store's tables for a clean test slate. Mirrors mongo.EnsureSchemaForConfig.
func EnsureSchemaForConfig(ctx context.Context, cfg config.PostgresPersistenceConfig) error {
	for _, store := range config.AllStoreNames() {
		resolved := cfg.For(store)
		if err := ensureSchemaForOneStore(ctx, store, resolved); err != nil {
			return fmt.Errorf("ensure schema for store %q: %w", store, err)
		}
	}
	return nil
}

func ensureSchemaForOneStore(ctx context.Context, store string, resolved config.PostgresConfig) error {
	if resolved.Database == "" {
		return fmt.Errorf("ensure schema: store %q resolved to empty database", store)
	}
	if !validDBName.MatchString(resolved.Database) {
		return fmt.Errorf("ensure schema: invalid database name %q", resolved.Database)
	}
	if err := createDatabaseIfNotExists(ctx, resolved.URI, resolved.Database); err != nil {
		return err
	}
	pool, perr := newPool(ctx, PoolConfig{URI: resolved.URI, Database: resolved.Database, MaxConns: 2}, "")
	if perr != nil {
		return perr
	}
	defer pool.Close()

	// Drop tables for a clean slate (tests), then (re)create.
	for _, tbl := range tablesForStore(store) {
		if !validDBName.MatchString(tbl) {
			return fmt.Errorf("invalid table name %q", tbl)
		}
		if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+tbl+" CASCADE"); err != nil {
			return fmt.Errorf("drop table %s: %w", tbl, err)
		}
	}
	if _, err := pool.Exec(ctx, ddlForStore(store)); err != nil {
		return fmt.Errorf("create schema for store %q: %w", store, err)
	}
	return nil
}

// EnsureSchemaAllInDatabase creates EVERY store's tables in a single database.
// Used by package tests (engine, postgres) that route all stores to one
// throw-away database, mirroring mongo.EnsureSchemaForDatabase. Drops existing
// tables first for a clean slate.
func EnsureSchemaAllInDatabase(ctx context.Context, uri, dbName string) error {
	if !validDBName.MatchString(dbName) {
		return fmt.Errorf("invalid database name %q", dbName)
	}
	if err := createDatabaseIfNotExists(ctx, uri, dbName); err != nil {
		return err
	}
	pool, perr := newPool(ctx, PoolConfig{URI: uri, Database: dbName, MaxConns: 2}, "")
	if perr != nil {
		return perr
	}
	defer pool.Close()

	for _, store := range config.AllStoreNames() {
		for _, tbl := range tablesForStore(store) {
			if _, err := pool.Exec(ctx, "DROP TABLE IF EXISTS "+tbl+" CASCADE"); err != nil {
				return fmt.Errorf("drop table %s: %w", tbl, err)
			}
		}
	}
	for _, store := range config.AllStoreNames() {
		if _, err := pool.Exec(ctx, ddlForStore(store)); err != nil {
			return fmt.Errorf("create schema for store %q: %w", store, err)
		}
	}
	return nil
}

// createDatabaseIfNotExists connects to the server's maintenance database and
// creates dbName when absent. Database names cannot be parameterized in DDL, so
// dbName is validated against validDBName by the caller before quoting.
func createDatabaseIfNotExists(ctx context.Context, uri, dbName string) error {
	connCfg, err := pgx.ParseConfig(uri)
	if err != nil {
		return fmt.Errorf("parse admin URI: %w", err)
	}
	connCfg.Database = "postgres" // maintenance DB always present
	conn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		return fmt.Errorf("connect admin DB: %w", err)
	}
	defer conn.Close(ctx)

	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", dbName).Scan(&exists); err != nil {
		return fmt.Errorf("check database exists: %w", err)
	}
	if exists {
		return nil
	}
	// dbName is validated by the caller; quote defensively.
	if _, err := conn.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		// Another concurrent ensure may have created it between our check
		// and create; treat duplicate as success.
		if catErr := mapError(err, "create database"); catErr != nil && !catErr.IsConflictError() {
			return fmt.Errorf("create database %s: %w", dbName, err)
		}
	}
	return nil
}
