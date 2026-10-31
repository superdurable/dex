CREATE TABLE IF NOT EXISTS dexzd_visibility_runs (
    tenant_id text NOT NULL,
    partition_id integer NOT NULL DEFAULT 0,
    run_id text NOT NULL,
    workflow_id text NOT NULL,
    workflow_type text NOT NULL,
    flow_type text NOT NULL,
    execution_status text NOT NULL,
    is_running boolean GENERATED ALWAYS AS (execution_status = 'Running') STORED,
    start_time_ns bigint NOT NULL,
    close_time_ns bigint,
    dex_parent_flow_id text,
    active_step_types text[],
    dex_work_queue_permissions text[],
    version bigint NOT NULL,
    sa_keyword jsonb NOT NULL DEFAULT '{}'::jsonb,
    sa_text jsonb NOT NULL DEFAULT '{}'::jsonb,
    sa_number jsonb NOT NULL DEFAULT '{}'::jsonb,
    sa_number_types jsonb NOT NULL DEFAULT '{}'::jsonb,
    sa_datetime jsonb NOT NULL DEFAULT '{}'::jsonb,
    sa_bool jsonb NOT NULL DEFAULT '{}'::jsonb,
    sa_wire jsonb NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (tenant_id, run_id)
);

ALTER TABLE dexzd_visibility_runs ADD COLUMN IF NOT EXISTS partition_id integer NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS dexzd_visibility_partition_idx ON dexzd_visibility_runs
    (tenant_id, partition_id, run_id);
CREATE INDEX IF NOT EXISTS dexzd_visibility_default_idx ON dexzd_visibility_runs
    (tenant_id, is_running DESC, close_time_ns DESC NULLS LAST, start_time_ns DESC, run_id COLLATE "C");
CREATE INDEX IF NOT EXISTS dexzd_visibility_flow_type_idx ON dexzd_visibility_runs
    (tenant_id, flow_type, is_running DESC, close_time_ns DESC NULLS LAST, start_time_ns DESC, run_id COLLATE "C");
CREATE INDEX IF NOT EXISTS dexzd_visibility_workflow_id_idx ON dexzd_visibility_runs
    (tenant_id, workflow_id);
CREATE TABLE IF NOT EXISTS dexzd_visibility_progress (
    partition_id integer PRIMARY KEY,
    applied_position bigint NOT NULL,
    applied_node_id text,
    applied_incarnation bigint,
    applied_seq bigint
);

ALTER TABLE dexzd_visibility_progress ADD COLUMN IF NOT EXISTS applied_node_id text;
ALTER TABLE dexzd_visibility_progress ADD COLUMN IF NOT EXISTS applied_incarnation bigint;
ALTER TABLE dexzd_visibility_progress ADD COLUMN IF NOT EXISTS applied_seq bigint;

CREATE TABLE IF NOT EXISTS dexzd_visibility_skipped_documents (
    tenant_id text NOT NULL,
    partition_id integer NOT NULL,
    run_id text NOT NULL,
    reason text NOT NULL,
    PRIMARY KEY (tenant_id, run_id)
);
