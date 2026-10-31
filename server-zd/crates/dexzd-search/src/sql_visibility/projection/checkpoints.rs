// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;

use dexzd_keyspace::PartitionId;
use dexzd_protocol::dexzd::v1::LogPosition;
use tokio_postgres::types::{ToSql, Type};

use super::progress::{is_replay, position_from_row, validate_position};
use super::projector::{ProjectionError, database_error, operation_timeout, should_retry};
use crate::sql_visibility::search_backend::{
    DATABASE_OPERATION_TIMEOUT, SqlVisibilitySearchBackend,
};

/// One partition's durable progress after retained frames and pending work permit advancement.
pub struct PartitionCheckpoint {
    /// Partition whose progress advances.
    pub partition_id: PartitionId,
    /// Expected SQL position before this checkpoint, or None for an empty projection.
    pub previous_position: Option<LogPosition>,
    /// Committed position covered by this checkpoint.
    pub applied_position: LogPosition,
}

pub(super) async fn checkpoint_partitions(
    backend: &SqlVisibilitySearchBackend,
    checkpoints: &[PartitionCheckpoint],
    owner_epoch: u64,
) -> Result<(), ProjectionError> {
    if checkpoints.is_empty() {
        return Ok(());
    }
    let mut has_retried = false;
    loop {
        let mut slot = backend.lock_next_slot().await;
        if slot.is_disconnected() {
            slot.reconnect(backend.configuration()).await?;
        }
        let result = match tokio::time::timeout(
            DATABASE_OPERATION_TIMEOUT,
            checkpoint_transaction(&mut slot.client, checkpoints, owner_epoch),
        )
        .await
        {
            Ok(result) => result,
            Err(_) => {
                slot.invalidate();
                Err(operation_timeout("checkpoint partitions in"))
            }
        };
        if should_retry(&result, has_retried) {
            has_retried = true;
            slot.reconnect(backend.configuration()).await?;
            continue;
        }
        return result.map_err(|error| error.redact_credentials(backend.configuration()));
    }
}

async fn checkpoint_transaction(
    client: &mut tokio_postgres::Client,
    checkpoints: &[PartitionCheckpoint],
    owner_epoch: u64,
) -> Result<(), ProjectionError> {
    let offered: BTreeMap<_, _> = checkpoints
        .iter()
        .map(|checkpoint| (checkpoint.partition_id, checkpoint))
        .collect();
    if offered.len() != checkpoints.len() {
        return Err(ProjectionError::InvalidState(
            "duplicate partition checkpoint".into(),
        ));
    }
    for checkpoint in checkpoints {
        validate_position(&checkpoint.applied_position)?;
    }
    let owner_epoch = i64::try_from(owner_epoch)
        .map_err(|_| ProjectionError::InvalidState("owner epoch exceeds bigint".into()))?;
    let partitions: Vec<i32> = offered
        .keys()
        .map(|partition| i32::from(partition.0))
        .collect();
    let transaction = client
        .transaction()
        .await
        .map_err(|source| database_error("begin partition checkpoints in", source))?;
    transaction.execute_typed("INSERT INTO dexzd_visibility_progress (partition_id, applied_position) SELECT partition_id, 0 FROM unnest($1::integer[]) AS partition_id ON CONFLICT (partition_id) DO NOTHING", &[(&partitions as &(dyn ToSql + Sync), Type::INT4_ARRAY)]).await.map_err(|source| database_error("initialize partition checkpoints in", source))?;
    let rows = transaction.query_typed("SELECT applied_node_id, applied_incarnation, applied_seq, applied_position, partition_id, owner_epoch FROM dexzd_visibility_progress WHERE partition_id = ANY($1::integer[]) ORDER BY partition_id FOR UPDATE", &[(&partitions as &(dyn ToSql + Sync), Type::INT4_ARRAY)]).await.map_err(|source| database_error("lock partition checkpoints in", source))?;
    let mut nodes = Vec::with_capacity(rows.len());
    let mut incarnations = Vec::with_capacity(rows.len());
    let mut sequences = Vec::with_capacity(rows.len());
    for row in rows {
        let partition_number: i32 = row.get("partition_id");
        let partition = PartitionId(
            u16::try_from(partition_number).expect("SQL partition was requested as u16"),
        );
        let checkpoint = offered[&partition];
        let stored_epoch: i64 = row.get("owner_epoch");
        if stored_epoch > owner_epoch {
            return Err(ProjectionError::OwnerFenced {
                partition_id: partition,
                stored_epoch,
                offered_epoch: owner_epoch,
            });
        }
        let stored = position_from_row(&row)?;
        let position = if is_replay(stored.as_ref(), &checkpoint.applied_position) {
            stored.expect("a replay has a stored position")
        } else {
            if stored.as_ref() != checkpoint.previous_position.as_ref() {
                return Err(ProjectionError::PositionMismatch {
                    partition_id: partition,
                    stored,
                    expected: checkpoint.previous_position.clone(),
                    offered: checkpoint.applied_position.clone(),
                });
            }
            checkpoint.applied_position.clone()
        };
        nodes.push(position.node_id);
        incarnations.push(i64::from(position.incarnation));
        sequences.push(i64::try_from(position.seq).expect("validated SQL position fits bigint"));
    }
    transaction.execute_typed("UPDATE dexzd_visibility_progress AS progress SET applied_node_id = offered.node_id, applied_incarnation = offered.incarnation, applied_seq = offered.sequence, applied_position = offered.sequence, owner_epoch = $5::bigint FROM unnest($1::integer[], $2::text[], $3::bigint[], $4::bigint[]) AS offered(partition_id, node_id, incarnation, sequence) WHERE progress.partition_id = offered.partition_id", &[(&partitions as &(dyn ToSql + Sync), Type::INT4_ARRAY), (&nodes, Type::TEXT_ARRAY), (&incarnations, Type::INT8_ARRAY), (&sequences, Type::INT8_ARRAY), (&owner_epoch, Type::INT8)]).await.map_err(|source| database_error("advance partition checkpoints in", source))?;
    transaction
        .commit()
        .await
        .map_err(|source| database_error("commit partition checkpoints in", source))
}
