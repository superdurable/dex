-- ============================================================================
-- dex_shards database
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
-- dex_runs database
-- ============================================================================
\connect dex_runs
CREATE TABLE IF NOT EXISTS runs (
	shard_id                          INTEGER NOT NULL,
	namespace                         TEXT NOT NULL,
	id                                TEXT NOT NULL,
	flow_type                         TEXT NOT NULL DEFAULT '',
	task_queue_name                   TEXT NOT NULL DEFAULT '',
	status                            INTEGER NOT NULL DEFAULT 0,
	heartbeat_timeout_seconds         INTEGER NOT NULL DEFAULT 0,
	version                           BIGINT NOT NULL,
	worker_id                         TEXT NOT NULL DEFAULT '',
	attributes                        JSONB NOT NULL DEFAULT '{}',
	unconsumed_channel_messages       JSONB NOT NULL DEFAULT '{}',
	step_exe_id_counters              JSONB NOT NULL DEFAULT '{}',
	active_step_executions            JSONB NOT NULL DEFAULT '{}',
	step_method_exe_counter           BIGINT NOT NULL DEFAULT 0,
	worker_request_counter            BIGINT NOT NULL DEFAULT 0,
	external_channel_message_counter  BIGINT NOT NULL DEFAULT 0,
	last_heartbeat_time               TIMESTAMPTZ,
	heartbeat_timer_id                UUID,
	active_durable_timer_id           UUID,
	durable_timer_fired_at             BIGINT NOT NULL DEFAULT 0,
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

-- ============================================================================
-- dex_blobs database
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
-- dex_taskqueues
-- ============================================================================
\connect dex_taskqueues
CREATE TABLE IF NOT EXISTS taskqueue (
	namespace        TEXT NOT NULL,
	queue_name       TEXT NOT NULL,
	partition_id     INTEGER NOT NULL,
	range_id         INTEGER NOT NULL,
	ack_level        BIGINT NOT NULL,
	owner_member_id  TEXT NOT NULL,
	owner_address    TEXT NOT NULL,
	claimed_at       TIMESTAMPTZ NOT NULL,
	updated_at       TIMESTAMPTZ,
	PRIMARY KEY (namespace, queue_name, partition_id)
);
CREATE TABLE IF NOT EXISTS taskqueue_tasks (
	namespace      TEXT NOT NULL,
	queue_name     TEXT NOT NULL,
	partition_id   INTEGER NOT NULL,
	task_id        BIGINT NOT NULL,
	run_id         TEXT NOT NULL,
	shard_id       INTEGER NOT NULL,
	created_at     TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (namespace, queue_name, partition_id, task_id)
);
