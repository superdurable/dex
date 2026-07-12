-- Copyright (c) 2025 superdurable
-- SPDX-License-Identifier: MIT
--
-- v0 PostgreSQL schema for the dex server. One database per logical store
-- (mirrors the Mongo per-database layout). The databases themselves are
-- created by init.sh; this script connects to each and creates its tables +
-- indexes idempotently (CREATE ... IF NOT EXISTS). Keep in sync with the Go
-- DDL in server/internal/persistence/postgres/schema.go (used by tests).

-- ============================================================================
-- dex_shards
-- ============================================================================
\connect dex_shards
CREATE TABLE IF NOT EXISTS shards (
	shard_id          INTEGER PRIMARY KEY,
	version           BIGINT NOT NULL,
	member_id         TEXT NOT NULL,
	claimed_at        TIMESTAMPTZ NOT NULL,
	lease_expires_at  TIMESTAMPTZ NOT NULL,
	released_at       TIMESTAMPTZ,
	metadata          JSONB NOT NULL
);

-- ============================================================================
-- dex_runs  (run rows + immediate/timer/opsfifo task outbox + DLQ)
-- ============================================================================
\connect dex_runs
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
CREATE INDEX IF NOT EXISTS task_dlq_poll_idx ON task_dlq (shard_id, dlq_at);

-- ============================================================================
-- dex_blobs
-- ============================================================================
\connect dex_blobs
CREATE TABLE IF NOT EXISTS blobs (
	shard_id   INTEGER NOT NULL,
	namespace  TEXT NOT NULL,
	run_id     TEXT NOT NULL,
	id         UUID NOT NULL,
	encoding   TEXT NOT NULL,
	payload    BYTEA,
	PRIMARY KEY (shard_id, namespace, run_id, id)
);

-- ============================================================================
-- dex_tasklists
-- ============================================================================
\connect dex_tasklists
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
);

-- ============================================================================
-- dex_visibility
-- ============================================================================
\connect dex_visibility
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
CREATE INDEX IF NOT EXISTS visibility_by_updated_at_idx ON visibility (namespace, flow_type, status, updated_at DESC, run_id);

-- ============================================================================
-- dex_history
-- ============================================================================
\connect dex_history
CREATE TABLE IF NOT EXISTS history (
	run_id          TEXT NOT NULL,
	event_id        BIGINT NOT NULL,
	namespace       TEXT NOT NULL,
	occurred_at_ms  BIGINT NOT NULL,
	worker_id       TEXT NOT NULL,
	payload_type    TEXT NOT NULL,
	payload         BYTEA NOT NULL,
	PRIMARY KEY (run_id, event_id)
);
