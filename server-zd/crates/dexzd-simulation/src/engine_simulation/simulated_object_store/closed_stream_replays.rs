// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Whether a start's replay of closed log streams reaches a batch that an EpochStart voided.

use dexzd_engine::EngineLocalStore;
use dexzd_storage::LocalReadView;

use crate::engine_simulation::simulated_node::{NODE_ID, PARTITION_COUNT};
use crate::engine_simulation::simulated_object_store::SharedObjectStorage;

/// Returns whether a start with `local_store`, or a lost store without snapshots, replays a
/// voided closed batch.
pub(crate) async fn replays_voided_closed_batch(
    storage: &SharedObjectStorage,
    local_store: Option<&EngineLocalStore>,
) -> bool {
    let manifest = dexzd_cluster_metadata::read_manifest(storage.store.as_ref())
        .await
        .expect("the shared in-memory store reads without faults");
    let Some(segments) = manifest
        .as_ref()
        .and_then(|manifest| manifest.partitions.first())
        .map(|partition| &partition.segments)
    else {
        return false;
    };
    let applied = local_store.and_then(lowest_applied_position);
    let observations = storage
        .observations
        .lock()
        .expect("storage observation lock is not poisoned");
    segments
        .iter()
        .filter(|segment| segment.node_id == NODE_ID && !segment.is_open)
        .any(|segment| {
            let first_seq = match applied {
                Some((incarnation, _)) if incarnation > segment.incarnation => return false,
                Some((incarnation, seq)) if incarnation == segment.incarnation => seq + 1,
                _ => 1,
            };
            first_seq <= segment.end_seq
                && observations
                    .log_batches
                    .has_voided_batch(segment.incarnation, first_seq..=segment.end_seq)
        })
}

/// Returns the lowest (incarnation, seq) every partition applied, or `None` when one applied
/// nothing.
fn lowest_applied_position(store: &EngineLocalStore) -> Option<(u32, u64)> {
    let metas = store
        .partition_metas()
        .expect("an in-memory store reads its partition metadata");
    if metas.len() < PARTITION_COUNT as usize {
        return None;
    }
    metas
        .iter()
        .map(|meta| {
            meta.applied
                .as_ref()
                .map(|applied| (applied.incarnation, applied.seq))
        })
        .collect::<Option<Vec<_>>>()?
        .into_iter()
        .min()
}
