\connect dex_runs
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