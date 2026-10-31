// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use dexzd_keyspace::{PartitionId, RunId, TenantId};

use super::{assert_send, document, position, upsert};
use crate::sql_visibility::{
    PartitionDocumentRepairs, ProjectionError, SqlVisibilityProjector, SqlVisibilitySearchBackend,
};

#[cfg(test)]
pub(super) async fn verify_cross_partition_repairs_for_test(
    backend: &Arc<SqlVisibilitySearchBackend>,
) {
    let projector = SqlVisibilityProjector::new(backend.clone());
    let tenant_id = TenantId(27);
    let run_id = RunId([27; 16]);
    let other_run = RunId([28; 16]);
    projector
        .apply_batch(
            PartitionId(7),
            None,
            &position(1),
            &[upsert(tenant_id, run_id, &document(tenant_id, run_id, 1))],
        )
        .await
        .expect("initial live frame projects");
    assert_send(projector.repair_documents(&[]));
    projector
        .repair_documents(&[
            PartitionDocumentRepairs {
                partition_id: PartitionId(7),
                previous_position: Some(position(1)),
                changes: vec![upsert(tenant_id, run_id, &document(tenant_id, run_id, 4))],
            },
            PartitionDocumentRepairs {
                partition_id: PartitionId(8),
                previous_position: None,
                changes: vec![upsert(
                    tenant_id,
                    other_run,
                    &document(tenant_id, other_run, 6),
                )],
            },
        ])
        .await
        .expect("one transaction repairs multiple partitions");
    assert_eq!(
        projector
            .read_progress(PartitionId(7))
            .await
            .expect("read live progress"),
        Some(position(1))
    );
    assert_eq!(
        projector
            .read_progress(PartitionId(8))
            .await
            .expect("read initial progress"),
        None
    );
    let mismatched = projector
        .repair_documents(&[PartitionDocumentRepairs {
            partition_id: PartitionId(7),
            previous_position: Some(position(99)),
            changes: vec![upsert(tenant_id, run_id, &document(tenant_id, run_id, 8))],
        }])
        .await;
    assert!(matches!(
        mismatched,
        Err(ProjectionError::RepairPositionMismatch { .. })
    ));
    projector
        .apply_batch(
            PartitionId(7),
            Some(&position(1)),
            &position(2),
            &[upsert(tenant_id, run_id, &document(tenant_id, run_id, 2))],
        )
        .await
        .expect("older live document cannot overwrite repaired version");
    let slot = backend.lock_next_slot().await;
    let rows = slot.client.query("SELECT partition_id, version FROM dexzd_visibility_runs WHERE tenant_id = '27' ORDER BY partition_id", &[]).await.expect("read repaired document versions");
    assert_eq!(rows.len(), 2);
    assert_eq!(rows[0].get::<_, i32>(0), 7);
    assert_eq!(rows[0].get::<_, i64>(1), 4);
    assert_eq!(rows[1].get::<_, i32>(0), 8);
    assert_eq!(rows[1].get::<_, i64>(1), 6);
}

#[cfg(test)]
pub(super) async fn verify_owner_fencing_and_keyset_index_for_test(
    backend: &Arc<SqlVisibilitySearchBackend>,
) {
    let metrics = Arc::new(super::CountingProjectionMetrics::new(
        PartitionId(9),
        TenantId(29),
    ));
    let predecessor = SqlVisibilityProjector::new_with_metric_recorder_and_epoch(
        backend.clone(),
        metrics.clone(),
        1,
    );
    let successor =
        SqlVisibilityProjector::new_with_metric_recorder_and_epoch(backend.clone(), metrics, 2);
    let partition = PartitionId(9);
    let tenant = TenantId(29);
    let run = RunId([29; 16]);
    predecessor
        .repair_partition_documents(
            partition,
            None,
            &[upsert(tenant, run, &document(tenant, run, 1))],
        )
        .await
        .expect("owner-fencing scenario succeeds");
    successor
        .repair_partition_documents(
            partition,
            None,
            &[super::SearchDocumentChange::Delete {
                tenant_id: tenant,
                run_id: run,
            }],
        )
        .await
        .expect("owner-fencing scenario succeeds");
    assert!(matches!(
        predecessor
            .repair_partition_documents(
                partition,
                None,
                &[upsert(tenant, run, &document(tenant, run, 2))]
            )
            .await,
        Err(ProjectionError::OwnerFenced {
            partition_id: PartitionId(9),
            ..
        })
    ));
    assert!(matches!(
        predecessor
            .apply_batch(partition, None, &position(1), &[])
            .await,
        Err(ProjectionError::OwnerFenced { .. })
    ));
    assert!(matches!(
        predecessor.clear_empty_partition(partition).await,
        Err(ProjectionError::OwnerFenced { .. })
    ));
    assert!(
        successor
            .read_run_versions(partition, None, 256)
            .await
            .expect("owner-fencing scenario succeeds")
            .is_empty()
    );
    let mut slot = backend.lock_next_slot().await;
    let transaction = slot
        .client
        .transaction()
        .await
        .expect("owner-fencing scenario succeeds");
    transaction
        .batch_execute("SET LOCAL enable_seqscan = off")
        .await
        .expect("owner-fencing scenario succeeds");
    let rows = transaction.query("EXPLAIN SELECT tenant_id, run_id, version FROM dexzd_visibility_runs WHERE partition_id = 9 AND (tenant_id COLLATE \"C\", run_id COLLATE \"C\") > ('29', '') ORDER BY tenant_id COLLATE \"C\", run_id COLLATE \"C\" LIMIT 256", &[]).await.expect("owner-fencing scenario succeeds");
    let explanation = rows
        .iter()
        .map(|row| row.get::<_, String>(0))
        .collect::<Vec<_>>()
        .join("\n");
    assert!(
        explanation.contains("dexzd_visibility_partition_run_versions"),
        "{explanation}"
    );
    transaction
        .rollback()
        .await
        .expect("owner-fencing scenario succeeds");
}
