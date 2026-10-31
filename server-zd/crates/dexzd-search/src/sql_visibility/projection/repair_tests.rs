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
