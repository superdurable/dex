// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_visibility_query::{FieldRegistry, parse_and_check};
use tokio_postgres::Client;

use super::{VisibilityDialect, translate_query_for_dialect};

pub(super) async fn assert_paradedb_system_indexes_for_test(client: &Client) {
    client
        .batch_execute(
            "INSERT INTO dexzd_visibility_runs
                (tenant_id, run_id, workflow_id, workflow_type, flow_type,
                 execution_status, start_time_ns, version)
             SELECT 'index-tenant', 'run-' || number, 'workflow-' || number, 'Engine',
                    CASE WHEN number <= 2 THEN 'RareFlow' || number ELSE 'CommonFlow' END,
                    'Running', number, 1
             FROM generate_series(1, 20000) AS number;
             ANALYZE dexzd_visibility_runs;",
        )
        .await
        .expect("seed and analyze selective system-field fixtures");

    for (field, column, first, second, index_name) in [
        (
            "FlowType",
            "flow_type",
            "RareFlow1",
            "RareFlow2",
            "dexzd_visibility_flow_type_idx",
        ),
        (
            "WorkflowId",
            "workflow_id",
            "workflow-1",
            "workflow-2",
            "dexzd_visibility_workflow_id_idx",
        ),
        (
            "RunId",
            "run_id",
            "run-1",
            "run-2",
            "dexzd_visibility_runs_pkey",
        ),
    ] {
        for (predicate, expected_count) in [
            (format!("{field} = '{first}'"), 1),
            (format!("{field} IN ('{first}', '{second}')"), 2),
        ] {
            let query = parse_and_check(&predicate, &FieldRegistry::new()).expect(&predicate);
            let statement = translate_query_for_dialect(
                &query,
                "index-tenant",
                100,
                None,
                VisibilityDialect::ParadeDb,
                &[],
            )
            .expect("translate indexed system predicate");
            let plan = client
                .query(
                    &format!("EXPLAIN (ANALYZE, COSTS OFF) {}", statement.sql),
                    &statement.parameter_refs(),
                )
                .await
                .expect("explain indexed system predicate")
                .iter()
                .map(|row| row.get::<_, String>(0))
                .collect::<Vec<_>>()
                .join("\n");
            println!("ParadeDB system index EXPLAIN for {predicate}:\n{plan}");
            assert!(plan.contains(index_name), "{predicate}:\n{plan}");
            assert!(
                plan.lines().any(|line| {
                    line.contains("Index Cond:") && line.contains(&format!("{column} ="))
                }),
                "{predicate} must constrain the indexed system column:\n{plan}",
            );
            let result = client
                .query(&statement.sql, &statement.parameter_refs())
                .await
                .expect("execute indexed system predicate");
            assert_eq!(result.len(), expected_count, "{predicate}");
        }
    }
    client
        .batch_execute("TRUNCATE dexzd_visibility_runs; ANALYZE dexzd_visibility_runs;")
        .await
        .expect("remove isolated index fixtures before evaluator parity");
}
