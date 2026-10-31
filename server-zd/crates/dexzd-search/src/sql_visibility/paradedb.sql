CREATE EXTENSION IF NOT EXISTS pg_search;

CREATE INDEX IF NOT EXISTS dexzd_visibility_bm25_idx ON dexzd_visibility_runs USING paradedb (
    (tenant_id::pdb.literal),
    (run_id::pdb.literal),
    (workflow_id::pdb.literal),
    (workflow_type::pdb.literal),
    (flow_type::pdb.literal),
    (execution_status::pdb.literal),
    (dex_parent_flow_id::pdb.literal),
    start_time_ns, close_time_ns,
    (sa_keyword::pdb.literal),
    (sa_text::pdb.whitespace),
    (sa_number::pdb.whitespace('columnar=true')),
    (sa_datetime::pdb.whitespace('columnar=true')),
    (sa_bool::pdb.whitespace('columnar=true'))
);
