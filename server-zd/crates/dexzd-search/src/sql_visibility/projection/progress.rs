// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Per-partition projection progress rows and the node-epoch fence that guards every write.

use std::collections::BTreeMap;

use dexzd_keyspace::PartitionId;
use dexzd_protocol::dexzd::v1::LogPosition;
use tokio_postgres::Transaction;
use tokio_postgres::types::{ToSql, Type};

use super::projector::{
    PartitionProjectionBatch, ProjectionError, ProjectionProgress, database_error,
};

const PROGRESS_COLUMNS: &str = "partition_id, applied_node_id, applied_incarnation, applied_seq, applied_position, applied_epoch, outbox_through_seq";

pub(super) async fn read_progress_rows(
    client: &tokio_postgres::Client,
) -> Result<BTreeMap<PartitionId, ProjectionProgress>, ProjectionError> {
    let rows = client
        .query_typed(
            &format!("SELECT {PROGRESS_COLUMNS} FROM dexzd_visibility_progress"),
            &[],
        )
        .await
        .map_err(|source| database_error("read progress from", source))?;
    rows.iter()
        .map(|row| {
            let partition_number: i32 = row
                .try_get(0)
                .map_err(|source| database_error("decode progress partition from", source))?;
            let partition_id = u16::try_from(partition_number).map_err(|_| {
                ProjectionError::InvalidState(format!(
                    "SQL visibility progress has invalid partition {partition_number}"
                ))
            })?;
            Ok((PartitionId(partition_id), progress_from_row(row)?))
        })
        .collect()
}

/// Creates missing progress rows, locks every row in partition order, and checks the epoch fence.
///
/// Returns [`ProjectionError::Fenced`] when a row records a newer epoch than `epoch`.
pub(super) async fn lock_progress(
    transaction: &Transaction<'_>,
    partition_ids: &[PartitionId],
    epoch: u64,
) -> Result<(), ProjectionError> {
    let partition_numbers: Vec<i32> = partition_ids
        .iter()
        .map(|partition| i32::from(partition.0))
        .collect();
    transaction
        .execute_typed(
            "INSERT INTO dexzd_visibility_progress (partition_id, applied_position) SELECT partition_id, 0 FROM unnest($1::integer[]) AS partition_id ON CONFLICT (partition_id) DO NOTHING",
            &[(&partition_numbers as &(dyn ToSql + Sync), Type::INT4_ARRAY)],
        )
        .await
        .map_err(|source| database_error("initialize progress for", source))?;
    let rows = transaction
        .query_typed(
            "SELECT partition_id, applied_epoch FROM dexzd_visibility_progress WHERE partition_id = ANY($1::integer[]) ORDER BY partition_id FOR UPDATE",
            &[(&partition_numbers as &(dyn ToSql + Sync), Type::INT4_ARRAY)],
        )
        .await
        .map_err(|source| database_error("lock progress for", source))?;
    for row in rows {
        let partition_number: i32 = row
            .try_get(0)
            .map_err(|source| database_error("decode progress partition from", source))?;
        let stored_epoch: Option<i64> = row
            .try_get(1)
            .map_err(|source| database_error("decode progress epoch from", source))?;
        let Some(stored_epoch) = stored_epoch else {
            continue;
        };
        let stored_epoch = u64::try_from(stored_epoch).map_err(|_| {
            ProjectionError::InvalidState("invalid SQL visibility progress epoch".into())
        })?;
        if stored_epoch > epoch {
            return Err(ProjectionError::Fenced {
                partition_id: PartitionId(u16::try_from(partition_number).map_err(|_| {
                    ProjectionError::InvalidState(
                        "invalid SQL visibility progress partition".into(),
                    )
                })?),
                stored_epoch,
                epoch,
            });
        }
    }
    Ok(())
}

/// Records `epoch` on every batch's row, raises its outbox progress, and sets finished views' positions.
pub(super) async fn update_progress(
    transaction: &Transaction<'_>,
    epoch: u64,
    batches: &[PartitionProjectionBatch],
) -> Result<(), ProjectionError> {
    let epoch = to_bigint(epoch, "node epoch")?;
    let mut partitions = Vec::with_capacity(batches.len());
    let mut through_seqs = Vec::with_capacity(batches.len());
    let mut node_ids = Vec::with_capacity(batches.len());
    let mut incarnations = Vec::with_capacity(batches.len());
    let mut sequences = Vec::with_capacity(batches.len());
    for batch in batches {
        partitions.push(i32::from(batch.partition_id.0));
        through_seqs.push(to_bigint(batch.outbox_through_seq, "outbox sequence")?);
        let position = batch.applied_position.as_ref();
        node_ids.push(position.map(|position| position.node_id.clone()));
        incarnations.push(position.map(|position| i64::from(position.incarnation)));
        sequences.push(
            position
                .map(|position| to_bigint(position.seq, "log sequence"))
                .transpose()?,
        );
    }
    transaction
        .execute_typed(
            "UPDATE dexzd_visibility_progress AS progress SET applied_epoch = $2::bigint, outbox_through_seq = GREATEST(COALESCE(progress.outbox_through_seq, 0), batch.through_seq), applied_node_id = COALESCE(batch.node_id, progress.applied_node_id), applied_incarnation = COALESCE(batch.incarnation, progress.applied_incarnation), applied_seq = COALESCE(batch.seq, progress.applied_seq), applied_position = COALESCE(batch.seq, progress.applied_position) FROM unnest($1::integer[], $3::bigint[], $4::text[], $5::bigint[], $6::bigint[]) AS batch(partition_id, through_seq, node_id, incarnation, seq) WHERE progress.partition_id = batch.partition_id",
            &[
                (&partitions as &(dyn ToSql + Sync), Type::INT4_ARRAY),
                (&epoch, Type::INT8),
                (&through_seqs, Type::INT8_ARRAY),
                (&node_ids, Type::TEXT_ARRAY),
                (&incarnations, Type::INT8_ARRAY),
                (&sequences, Type::INT8_ARRAY),
            ],
        )
        .await
        .map_err(|source| database_error("update progress for", source))?;
    Ok(())
}

pub(super) fn validate_position(position: &LogPosition) -> Result<(), ProjectionError> {
    if position.node_id.is_empty() || position.seq == 0 {
        return Err(ProjectionError::InvalidState("invalid log position".into()));
    }
    to_bigint(position.seq, "log sequence").map(|_| ())
}

fn progress_from_row(row: &tokio_postgres::Row) -> Result<ProjectionProgress, ProjectionError> {
    let node_id: Option<String> = row
        .try_get(1)
        .map_err(|source| database_error("decode progress node from", source))?;
    let incarnation: Option<i64> = row
        .try_get(2)
        .map_err(|source| database_error("decode progress incarnation from", source))?;
    let sequence: Option<i64> = row
        .try_get(3)
        .map_err(|source| database_error("decode progress sequence from", source))?;
    let legacy_sequence: i64 = row
        .try_get(4)
        .map_err(|source| database_error("decode legacy progress from", source))?;
    let applied_epoch: Option<i64> = row
        .try_get(5)
        .map_err(|source| database_error("decode progress epoch from", source))?;
    let outbox_through_seq: Option<i64> = row
        .try_get(6)
        .map_err(|source| database_error("decode outbox progress from", source))?;
    let applied_position = match (node_id, incarnation, sequence) {
        (None, None, None) if legacy_sequence == 0 => None,
        (Some(node_id), Some(incarnation), Some(sequence)) => Some(LogPosition {
            node_id,
            incarnation: u32::try_from(incarnation).map_err(|_| {
                ProjectionError::InvalidState("invalid SQL visibility progress incarnation".into())
            })?,
            seq: u64::try_from(sequence).map_err(|_| {
                ProjectionError::InvalidState("invalid SQL visibility progress sequence".into())
            })?,
        }),
        _ => {
            return Err(ProjectionError::InvalidState(
                "incomplete SQL visibility projection position".into(),
            ));
        }
    };
    Ok(ProjectionProgress {
        applied_position,
        applied_epoch: applied_epoch
            .map(|epoch| {
                u64::try_from(epoch).map_err(|_| {
                    ProjectionError::InvalidState("invalid SQL visibility progress epoch".into())
                })
            })
            .transpose()?,
        outbox_through_seq: outbox_through_seq
            .map(|seq| {
                u64::try_from(seq).map_err(|_| {
                    ProjectionError::InvalidState("invalid SQL visibility outbox progress".into())
                })
            })
            .transpose()?
            .unwrap_or(0),
    })
}

fn to_bigint(value: u64, what: &str) -> Result<i64, ProjectionError> {
    i64::try_from(value)
        .map_err(|_| ProjectionError::InvalidState(format!("{what} {value} exceeds bigint")))
}
