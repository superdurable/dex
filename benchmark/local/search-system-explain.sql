\set ON_ERROR_STOP on
\echo rare-flow-type
EXPLAIN (ANALYZE, BUFFERS)
SELECT tenant_id, run_id, workflow_id, workflow_type, flow_type, execution_status, start_time_ns, close_time_ns, dex_parent_flow_id, active_step_types, dex_work_queue_permissions, version, sa_keyword::text, sa_text::text, sa_number::text, sa_number_types::text, sa_datetime::text, sa_bool::text, sa_wire::text
FROM dexzd_visibility_runs WHERE tenant_id = '0' AND flow_type COLLATE "C" = 'searchRareFlow'
ORDER BY is_running DESC, close_time_ns DESC NULLS LAST, start_time_ns DESC, run_id COLLATE "C" ASC LIMIT 21;
\echo start-time-desc
EXPLAIN (ANALYZE, BUFFERS)
SELECT tenant_id, run_id, workflow_id, workflow_type, flow_type, execution_status, start_time_ns, close_time_ns, dex_parent_flow_id, active_step_types, dex_work_queue_permissions, version, sa_keyword::text, sa_text::text, sa_number::text, sa_number_types::text, sa_datetime::text, sa_bool::text, sa_wire::text
FROM dexzd_visibility_runs WHERE tenant_id = '0'
ORDER BY start_time_ns DESC, run_id COLLATE "C" ASC LIMIT 21;

\echo rare-flow-type-default-collation-control
EXPLAIN (ANALYZE, BUFFERS)
SELECT tenant_id, run_id, workflow_id, workflow_type, flow_type, execution_status, start_time_ns, close_time_ns, dex_parent_flow_id, active_step_types, dex_work_queue_permissions, version, sa_keyword::text, sa_text::text, sa_number::text, sa_number_types::text, sa_datetime::text, sa_bool::text, sa_wire::text
FROM dexzd_visibility_runs WHERE tenant_id = '0' AND flow_type = 'searchRareFlow'
ORDER BY is_running DESC, close_time_ns DESC NULLS LAST, start_time_ns DESC, run_id COLLATE "C" ASC LIMIT 21;
