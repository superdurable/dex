\set ON_ERROR_STOP on
SELECT slot_column AS keyword_slot FROM dexzd_visibility_attribute_slots WHERE index_key = 'SearchField00' \gset
SELECT slot_column AS int_slot FROM dexzd_visibility_attribute_slots WHERE index_key = 'SearchField01' \gset
\echo rare-flow-type
EXPLAIN (ANALYZE, BUFFERS)
SELECT tenant_id, run_id, workflow_id, workflow_type, flow_type, execution_status, start_time_ns, close_time_ns, dex_parent_flow_id, active_step_types, dex_work_queue_permissions, version, sa_keyword::text, sa_text::text, sa_number::text, sa_number_types::text, sa_datetime::text, sa_bool::text, sa_wire::text
FROM dexzd_visibility_runs WHERE tenant_id = '0' AND flow_type = 'searchRareFlow'
ORDER BY is_running DESC, close_time_ns DESC NULLS LAST, start_time_ns DESC, run_id COLLATE "C" ASC LIMIT 21;
\echo keyword-equality
EXPLAIN (ANALYZE, BUFFERS)
SELECT tenant_id, run_id, workflow_id, workflow_type, flow_type, execution_status, start_time_ns, close_time_ns, dex_parent_flow_id, active_step_types, dex_work_queue_permissions, version, sa_keyword::text, sa_text::text, sa_number::text, sa_number_types::text, sa_datetime::text, sa_bool::text, sa_wire::text
FROM dexzd_visibility_runs WHERE tenant_id = '0' AND :"keyword_slot" = 'rare'
ORDER BY is_running DESC, close_time_ns DESC NULLS LAST, start_time_ns DESC, run_id COLLATE "C" ASC LIMIT 21;
\echo int-range
EXPLAIN (ANALYZE, BUFFERS)
SELECT tenant_id, run_id, workflow_id, workflow_type, flow_type, execution_status, start_time_ns, close_time_ns, dex_parent_flow_id, active_step_types, dex_work_queue_permissions, version, sa_keyword::text, sa_text::text, sa_number::text, sa_number_types::text, sa_datetime::text, sa_bool::text, sa_wire::text
FROM dexzd_visibility_runs WHERE tenant_id = '0' AND :"int_slot" BETWEEN 50000 AND 50099
ORDER BY is_running DESC, close_time_ns DESC NULLS LAST, start_time_ns DESC, run_id COLLATE "C" ASC LIMIT 21;
\echo start-time-desc
EXPLAIN (ANALYZE, BUFFERS)
SELECT tenant_id, run_id, workflow_id, workflow_type, flow_type, execution_status, start_time_ns, close_time_ns, dex_parent_flow_id, active_step_types, dex_work_queue_permissions, version, sa_keyword::text, sa_text::text, sa_number::text, sa_number_types::text, sa_datetime::text, sa_bool::text, sa_wire::text
FROM dexzd_visibility_runs WHERE tenant_id = '0'
ORDER BY start_time_ns DESC, run_id COLLATE "C" ASC LIMIT 21;
