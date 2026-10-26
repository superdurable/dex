// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Drops pending entries of Attribute Stores the configuration no longer names, so closes finalize.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;

use dexzd_apply::FlowStateReader;
use dexzd_keyspace::row_keys::{
    attribute_sync_pending_store_prefix, decode_attribute_sync_pending_index_key,
    partition_index_range, prefix_range,
};
use dexzd_keyspace::{ColumnFamily, IndexKind, PartitionId};
use dexzd_protocol::dexzd::v1::AttributeSyncAckCommand;
use tokio_util::sync::CancellationToken;

use super::ack_proposer::PendingAck;
use super::pending_entries::read_pending_store_work;
use crate::effects::{EffectError, FlowEffectOperations};

/// Drop acks of one unconfigured store in one partition.
pub(super) struct UnconfiguredStoreDrops {
    pub(super) store_name: String,
    pub(super) acks: Vec<PendingAck>,
}

/// Reads the pending runs of every unconfigured store in each partition of `reads` with one
/// committed view, limited to a partition's selected store names when set.
///
/// Returns one drop ack per pending run, by partition and store, or the read failure's description.
/// A cancelled `stop` ends the read early with the partitions read so far.
pub(super) async fn read_unconfigured_store_drops(
    flow_effect_operations: Arc<dyn FlowEffectOperations>,
    reads: Vec<(PartitionId, Option<BTreeSet<String>>)>,
    configured_store_names: BTreeSet<String>,
    stop: CancellationToken,
) -> Result<BTreeMap<PartitionId, Vec<UnconfiguredStoreDrops>>, String> {
    tokio::task::spawn_blocking(move || {
        let reader = flow_effect_operations
            .committed_state_reader()
            .map_err(|error| error.to_string())?;
        let mut drops = BTreeMap::new();
        for (partition, selected_store_names) in reads {
            if stop.is_cancelled() {
                break;
            }
            let partition_drops = read_drops(
                reader.as_ref(),
                partition,
                &configured_store_names,
                selected_store_names.as_ref(),
            )
            .map_err(|error| error.to_string())?;
            if !partition_drops.is_empty() {
                drops.insert(partition, partition_drops);
            }
        }
        Ok(drops)
    })
    .await
    .map_err(|error| error.to_string())
    .and_then(|read| read)
}

fn read_drops(
    reader: &dyn FlowStateReader,
    partition: PartitionId,
    configured_store_names: &BTreeSet<String>,
    selected_store_names: Option<&BTreeSet<String>>,
) -> Result<Vec<UnconfiguredStoreDrops>, EffectError> {
    let mut drops = Vec::new();
    for store_name in read_pending_store_names(reader, partition)? {
        let is_selected =
            selected_store_names.is_none_or(|selected| selected.contains(&store_name));
        if configured_store_names.contains(&store_name) || !is_selected {
            continue;
        }
        let work = read_pending_store_work(reader, partition, &store_name)?;
        let mut acks = Vec::new();
        for run_entries in work.flows.into_iter().flatten() {
            tracing::error!(
                store_name,
                flow_id = %run_entries.flow.flow_id,
                run_id = %run_entries.run.run_id,
                dropped_entry_count = run_entries.entries.len(),
                "the Attribute Store is not configured; dropping the pending entries"
            );
            let acked_seq = run_entries
                .entries
                .iter()
                .map(|entry| entry.written_seq)
                .max()
                .unwrap_or(0);
            acks.push(PendingAck {
                flow: run_entries.flow,
                run: run_entries.run,
                ack: AttributeSyncAckCommand {
                    store_name: store_name.clone(),
                    acked_seq,
                    attribute_keys: Vec::new(),
                    is_dropped: true,
                },
            });
        }
        if !acks.is_empty() {
            drops.push(UnconfiguredStoreDrops { store_name, acks });
        }
    }
    Ok(drops)
}

/// Returns each store name in the partition's pending index, seeking past each store's keys.
fn read_pending_store_names(
    reader: &dyn FlowStateReader,
    partition: PartitionId,
) -> Result<BTreeSet<String>, EffectError> {
    let (mut start, end) = partition_index_range(partition, IndexKind::AttributeSyncPending);
    let mut store_names = BTreeSet::new();
    loop {
        let Some((key, _)) = reader
            .scan(ColumnFamily::PartitionIndex, &start, &end, 1)?
            .into_iter()
            .next()
        else {
            return Ok(store_names);
        };
        let entry = decode_attribute_sync_pending_index_key(&key).ok_or_else(|| {
            EffectError::CorruptState {
                table: "AttributeSyncPending index",
                key: crate::effects::hex(&key),
                detail: "malformed key".to_owned(),
            }
        })?;
        let (_, store_end) = prefix_range(&attribute_sync_pending_store_prefix(
            partition,
            &entry.store_name,
        ));
        start = store_end;
        store_names.insert(entry.store_name);
    }
}
