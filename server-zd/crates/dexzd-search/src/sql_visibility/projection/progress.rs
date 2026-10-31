// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::PartitionId;
use dexzd_protocol::dexzd::v1::LogPosition;
use tokio_postgres::Transaction;
use tokio_postgres::types::{ToSql, Type};

use super::projector::{ProjectionError, database_error};

pub(super) async fn lock_progress(
    transaction: &Transaction<'_>,
    partition_id: PartitionId,
    owner_epoch: u64,
) -> Result<Option<LogPosition>, ProjectionError> {
    let partition_number = i32::from(partition_id.0);
    transaction
        .execute_typed(
            "INSERT INTO dexzd_visibility_progress (partition_id, applied_position) VALUES ($1::integer, 0) ON CONFLICT (partition_id) DO NOTHING",
            &[(&partition_number as &(dyn ToSql + Sync), Type::INT4)],
        )
        .await
        .map_err(|source| database_error("initialize progress for", source))?;
    let row = transaction
        .query_typed_one(
            "SELECT applied_node_id, applied_incarnation, applied_seq, applied_position, owner_epoch FROM dexzd_visibility_progress WHERE partition_id = $1::integer FOR UPDATE",
            &[(&partition_number as &(dyn ToSql + Sync), Type::INT4)],
        )
        .await
        .map_err(|source| database_error("lock progress for", source))?;
    let stored_epoch: i64 = row.get("owner_epoch");
    let offered_epoch = i64::try_from(owner_epoch)
        .map_err(|_| ProjectionError::InvalidState("owner epoch exceeds bigint".into()))?;
    if stored_epoch > offered_epoch {
        return Err(ProjectionError::OwnerFenced {
            partition_id,
            stored_epoch,
            offered_epoch,
        });
    }
    if stored_epoch != offered_epoch {
        transaction.execute_typed("UPDATE dexzd_visibility_progress SET owner_epoch = $2::bigint WHERE partition_id = $1::integer", &[(&partition_number as &(dyn ToSql + Sync), Type::INT4), (&offered_epoch, Type::INT8)]).await.map_err(|source| database_error("claim projection owner in", source))?;
    }
    position_from_row(&row)
}

pub(super) async fn update_progress(
    transaction: &Transaction<'_>,
    partition_id: PartitionId,
    position: &LogPosition,
) -> Result<(), ProjectionError> {
    let sequence = i64::try_from(position.seq)
        .map_err(|_| ProjectionError::InvalidState("log sequence exceeds bigint".into()))?;
    let partition_number = i32::from(partition_id.0);
    let incarnation = i64::from(position.incarnation);
    transaction
        .execute_typed(
            "UPDATE dexzd_visibility_progress SET applied_position = $2::bigint, applied_node_id = $3::text, applied_incarnation = $4::bigint, applied_seq = $5::bigint WHERE partition_id = $1::integer",
            &[
                (&partition_number as &(dyn ToSql + Sync), Type::INT4),
                (&sequence, Type::INT8),
                (&position.node_id, Type::TEXT),
                (&incarnation, Type::INT8),
                (&sequence, Type::INT8),
            ],
        )
        .await
        .map_err(|source| database_error("update progress for", source))?;
    Ok(())
}

pub(super) fn position_from_row(
    row: &tokio_postgres::Row,
) -> Result<Option<LogPosition>, ProjectionError> {
    let node_id: Option<String> = row
        .try_get(0)
        .map_err(|source| database_error("decode progress node from", source))?;
    let incarnation: Option<i64> = row
        .try_get(1)
        .map_err(|source| database_error("decode progress incarnation from", source))?;
    let sequence: Option<i64> = row
        .try_get(2)
        .map_err(|source| database_error("decode progress sequence from", source))?;
    let legacy_sequence: i64 = row
        .try_get(3)
        .map_err(|source| database_error("decode legacy progress from", source))?;
    match (node_id, incarnation, sequence) {
        (None, None, None) if legacy_sequence == 0 => Ok(None),
        (Some(node_id), Some(incarnation), Some(sequence)) => Ok(Some(LogPosition {
            node_id,
            incarnation: u32::try_from(incarnation).map_err(|_| {
                ProjectionError::InvalidState("invalid SQL visibility progress incarnation".into())
            })?,
            seq: u64::try_from(sequence).map_err(|_| {
                ProjectionError::InvalidState("invalid SQL visibility progress sequence".into())
            })?,
        })),
        _ => Err(ProjectionError::InvalidState(
            "incomplete SQL visibility projection position".into(),
        )),
    }
}

pub(super) fn validate_position(position: &LogPosition) -> Result<(), ProjectionError> {
    if position.node_id.is_empty() || position.seq == 0 {
        return Err(ProjectionError::InvalidState("invalid log position".into()));
    }
    i64::try_from(position.seq)
        .map(|_| ())
        .map_err(|_| ProjectionError::InvalidState("log sequence exceeds bigint".into()))
}

pub(super) fn same_stream(left: &LogPosition, right: &LogPosition) -> bool {
    left.node_id == right.node_id && left.incarnation == right.incarnation
}

pub(super) fn is_replay(stored: Option<&LogPosition>, offered: &LogPosition) -> bool {
    stored.is_some_and(|stored| same_stream(stored, offered) && stored.seq >= offered.seq)
}
