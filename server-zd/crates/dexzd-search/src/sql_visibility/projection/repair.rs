// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::PartitionId;
use dexzd_protocol::dexzd::v1::LogPosition;
use tokio_postgres::types::{ToSql, Type};

use super::changes::SearchDocumentChange;
use super::projector::{
    ProjectionError, ProjectionMetricRecorder, apply_changes, database_error, lock_progress,
    operation_timeout, prepare_attribute_slots, should_retry,
};
use super::skipped_document::{SkippedSearchDocument, record_skipped_documents};
use crate::sql_visibility::search_backend::{
    DATABASE_OPERATION_TIMEOUT, SqlVisibilitySearchBackend,
};

/// Current committed documents to repair while preserving one partition's live projection progress.
pub struct PartitionDocumentRepairs {
    /// Partition owning these documents.
    pub partition_id: PartitionId,
    /// Last live-frame position projected by the current owner, or None before its first frame.
    pub previous_position: Option<LogPosition>,
    /// Complete versioned documents from one consistent committed view; purged runs are omitted.
    pub changes: Vec<SearchDocumentChange>,
}

pub(super) struct BorrowedPartitionDocumentRepairs<'a> {
    pub(super) partition_id: PartitionId,
    pub(super) previous_position: Option<&'a LogPosition>,
    pub(super) changes: &'a [SearchDocumentChange],
}

pub(super) async fn repair_documents(
    backend: &SqlVisibilitySearchBackend,
    metric_recorder: Option<&dyn ProjectionMetricRecorder>,
    partitions: &[PartitionDocumentRepairs],
    owner_epoch: u64,
) -> Result<(), ProjectionError> {
    let borrowed: Vec<_> = partitions
        .iter()
        .map(|partition| BorrowedPartitionDocumentRepairs {
            partition_id: partition.partition_id,
            previous_position: partition.previous_position.as_ref(),
            changes: &partition.changes,
        })
        .collect();
    repair_borrowed_documents(backend, metric_recorder, &borrowed, owner_epoch).await
}

pub(super) async fn repair_borrowed_documents(
    backend: &SqlVisibilitySearchBackend,
    metric_recorder: Option<&dyn ProjectionMetricRecorder>,
    partitions: &[BorrowedPartitionDocumentRepairs<'_>],
    owner_epoch: u64,
) -> Result<(), ProjectionError> {
    if partitions.is_empty() {
        return Ok(());
    }
    let mut has_retried = false;
    loop {
        let mut slot = backend.lock_next_slot().await;
        if slot.is_disconnected() {
            slot.reconnect(backend.configuration()).await?;
        }
        let result = tokio::time::timeout(
            DATABASE_OPERATION_TIMEOUT,
            repair_transaction(&mut slot.client, backend, partitions, owner_epoch),
        )
        .await;
        let result = match result {
            Ok(result) => result,
            Err(_) => {
                slot.invalidate();
                Err(operation_timeout("repair documents in"))
            }
        };
        if should_retry(&result, has_retried) {
            has_retried = true;
            slot.reconnect(backend.configuration()).await?;
            continue;
        }
        drop(slot);
        return result
            .map(|skipped| record_skipped_documents(skipped, metric_recorder))
            .map_err(|error| error.redact_credentials(backend.configuration()));
    }
}

async fn repair_transaction(
    client: &mut tokio_postgres::Client,
    backend: &SqlVisibilitySearchBackend,
    partitions: &[BorrowedPartitionDocumentRepairs<'_>],
    owner_epoch: u64,
) -> Result<Vec<SkippedSearchDocument>, ProjectionError> {
    let transaction = client
        .transaction()
        .await
        .map_err(|source| database_error("begin repair in", source))?;
    let assignments = prepare_attribute_slots(&transaction, backend).await?;
    let mut ordered: Vec<_> = partitions.iter().collect();
    ordered.sort_by_key(|partition| partition.partition_id);
    for partition in ordered {
        let stored = lock_progress(&transaction, partition.partition_id, owner_epoch).await?;
        if stored.as_ref() != partition.previous_position {
            return Err(ProjectionError::RepairPositionMismatch {
                partition_id: partition.partition_id,
                stored,
                expected: partition.previous_position.cloned(),
            });
        }
    }
    let changes: Vec<_> = partitions
        .iter()
        .flat_map(|partition| {
            partition
                .changes
                .iter()
                .map(move |change| (partition.partition_id, change))
        })
        .collect();
    let skipped = apply_changes(
        &transaction,
        &changes,
        assignments
            .as_ref()
            .map(|snapshot| snapshot.assignments.as_slice()),
    )
    .await?;
    transaction
        .commit()
        .await
        .map_err(|source| database_error("commit repair in", source))?;
    Ok(skipped)
}

/// One projected run's identity and committed document version.
pub struct ProjectedRunVersion {
    /// Tenant owning this SQL row.
    pub tenant_id: dexzd_keyspace::TenantId,
    /// Run represented by this SQL row.
    pub run_id: dexzd_keyspace::RunId,
    /// Last committed document version projected for the run.
    pub version: u64,
}

pub(super) async fn read_run_versions(
    backend: &SqlVisibilitySearchBackend,
    partition: PartitionId,
    after: Option<(&str, &str)>,
    limit: u32,
) -> Result<Vec<ProjectedRunVersion>, ProjectionError> {
    let mut slot = backend.lock_next_slot().await;
    if slot.is_disconnected() {
        slot.reconnect(backend.configuration()).await?;
    }
    let partition_number = i32::from(partition.0);
    let (tenant, run) = after.unwrap_or(("", ""));
    let limit = i64::from(limit);
    let result = tokio::time::timeout(DATABASE_OPERATION_TIMEOUT, slot.client.query_typed(
        "SELECT tenant_id, run_id, version FROM dexzd_visibility_runs WHERE partition_id = $1::integer AND (tenant_id COLLATE \"C\", run_id COLLATE \"C\") > ($2::text, $3::text) ORDER BY tenant_id COLLATE \"C\", run_id COLLATE \"C\" LIMIT $4::bigint",
        &[(&partition_number as &(dyn ToSql + Sync), Type::INT4), (&tenant, Type::TEXT), (&run, Type::TEXT), (&limit, Type::INT8)],
    )).await;
    let rows = match result {
        Ok(result) => result.map_err(|source| database_error("scan run versions in", source)),
        Err(_) => {
            slot.invalidate();
            Err(operation_timeout("scan run versions in"))
        }
    }
    .map_err(|error| error.redact_credentials(backend.configuration()))?;
    rows.into_iter()
        .map(|row| {
            let tenant: String = row.get("tenant_id");
            let run: String = row.get("run_id");
            let version: i64 = row.get("version");
            Ok(ProjectedRunVersion {
                tenant_id: dexzd_keyspace::TenantId(tenant.parse().map_err(|error| {
                    ProjectionError::InvalidState(format!(
                        "invalid projected tenant {tenant}: {error}"
                    ))
                })?),
                run_id: run.parse().map_err(|error| {
                    ProjectionError::InvalidState(format!("invalid projected run {run}: {error}"))
                })?,
                version: u64::try_from(version).map_err(|error| {
                    ProjectionError::InvalidState(format!(
                        "invalid projected version {version}: {error}"
                    ))
                })?,
            })
        })
        .collect()
}

pub(super) async fn clear_empty_partition(
    backend: &SqlVisibilitySearchBackend,
    partition: PartitionId,
    owner_epoch: u64,
) -> Result<(), ProjectionError> {
    let mut slot = backend.lock_next_slot().await;
    if slot.is_disconnected() {
        slot.reconnect(backend.configuration()).await?;
    }
    let partition_number = i32::from(partition.0);
    let result = tokio::time::timeout(DATABASE_OPERATION_TIMEOUT, async {
        let transaction = slot
            .client
            .transaction()
            .await
            .map_err(|source| database_error("begin empty partition rebuild in", source))?;
        lock_progress(&transaction, partition, owner_epoch).await?;
        transaction.execute_typed("DELETE FROM dexzd_visibility_skipped_documents WHERE partition_id = $1::integer", &[(&partition_number as &(dyn ToSql + Sync), Type::INT4)]).await.map_err(|source| database_error("clear empty partition skipped documents in", source))?;
        transaction
            .execute_typed(
                "DELETE FROM dexzd_visibility_runs WHERE partition_id = $1::integer",
                &[(&partition_number as &(dyn ToSql + Sync), Type::INT4)],
            )
            .await
            .map_err(|source| database_error("clear empty partition documents in", source))?;
        transaction
            .execute_typed(
                "UPDATE dexzd_visibility_progress SET applied_node_id = NULL, applied_incarnation = NULL, applied_seq = NULL, applied_position = 0 WHERE partition_id = $1::integer",
                &[(&partition_number as &(dyn ToSql + Sync), Type::INT4)],
            )
            .await
            .map_err(|source| database_error("clear empty partition progress in", source))?;
        transaction
            .commit()
            .await
            .map_err(|source| database_error("commit empty partition rebuild in", source))
    })
    .await;
    match result {
        Ok(result) => result,
        Err(_) => {
            slot.invalidate();
            Err(operation_timeout("clear empty partition in"))
        }
    }
    .map_err(|error| error.redact_credentials(backend.configuration()))
}
