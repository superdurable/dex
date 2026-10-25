// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! What one seed exercised and left behind, collected once the seed ends.

use std::collections::BTreeMap;

use dexzd_object_store::ObjectStore;

use crate::engine_simulation::engine_invariants::{pruned_semantic_events, run_count};
use crate::engine_simulation::simulated_clients::ClientRecords;
use crate::engine_simulation::simulated_node::{ClusterEnvironment, NodeProcess, copy_local_store};
use crate::engine_simulation::{EngineSimulationFinalState, EngineSimulationSummary};

/// Returns the trace, the serving process's local store, and every stored object.
pub(crate) async fn final_state(
    environment: &ClusterEnvironment,
    serving: Option<&NodeProcess>,
) -> EngineSimulationFinalState {
    let shared_store = &environment.storage.store;
    let mut objects = BTreeMap::new();
    let mut start_after = None;
    loop {
        let listing = shared_store
            .list("", start_after.as_deref(), 1_000)
            .await
            .expect("the shared in-memory store lists without faults");
        for object in listing.objects {
            let content = shared_store
                .get(&object.key)
                .await
                .expect("the shared in-memory store reads without faults");
            if let Some(content) = content {
                objects.insert(object.key, content.bytes);
            }
        }
        match listing.next_start_after {
            Some(next) => start_after = Some(next),
            None => break,
        }
    }
    EngineSimulationFinalState {
        trace_events: environment.trace.all_events(),
        local_store: serving.map(|serving| copy_local_store(&serving.local_store)),
        objects,
    }
}

/// Completes `summary` with what `environment` observed and the client `records`.
pub(crate) fn finish_summary(
    mut summary: EngineSimulationSummary,
    environment: &ClusterEnvironment,
    records: &ClientRecords,
) -> EngineSimulationSummary {
    summary.trace_hash = environment.trace.digest();
    summary.trace_events = environment.trace.event_count();
    summary.recent_events = environment.trace.recent_events();
    {
        let observations = environment
            .storage
            .observations
            .lock()
            .expect("storage observation lock is not poisoned");
        summary.injected_faults = observations
            .injected_faults
            .iter()
            .map(|((fault, effect), count)| (format!("{fault:?}/{effect:?}"), *count))
            .collect();
        summary.precondition_failed_puts = name_counts(&observations.precondition_failed_puts);
        summary.deleted_objects = name_counts(&observations.deleted_objects);
        summary
            .snapshot_reads_during_start
            .clone_from(&observations.snapshot_reads_during_start);
        summary.old_epoch_overlaps = observations.old_epoch.overlaps;
        summary.old_epoch_frames = observations.old_epoch.frames;
        summary.old_epoch_write_attempts = name_counts(&observations.old_epoch.write_attempts);
        summary.writes_landed_after_crash = name_counts(&observations.writes_landed_after_crash);
        summary.epoch_starts_below_landed_batches = observations.epoch_starts_below_landed_batches;
    }
    summary.rejected_by_kind = name_counts(&environment.committed_log.rejected_by_kind());
    summary.worker_behaviors = environment
        .workers
        .behavior_counts()
        .into_iter()
        .map(|(behavior, count)| (format!("{behavior:?}"), count))
        .collect();
    summary.applied_by_kind = environment
        .committed_log
        .applied_by_kind()
        .into_iter()
        .map(|(kind, count)| (kind.to_owned(), count))
        .collect();
    summary.replayed_frames = environment.committed_log.replayed_frames();
    summary.canonical_command_digest = environment.committed_log.canonical_command_digest();
    summary.step_failure_recoveries =
        name_counts(&environment.committed_log.step_failure_recoveries());
    summary.final_runs = environment.committed_log.with_shadow(run_count);
    summary.final_pruned_semantic_events = environment
        .committed_log
        .with_shadow(pruned_semantic_events);
    summary.acknowledged_writes = records.acknowledged_writes.len() as u64;
    summary.checked_reads = records.checked_reads;
    summary.checked_read_values = records.checked_read_values;
    summary.client_calls = records
        .outcomes
        .iter()
        .map(|((operation, outcome), count)| (format!("{operation} {outcome}"), *count))
        .collect();
    summary
}

fn name_counts(counts: &BTreeMap<&'static str, u64>) -> BTreeMap<String, u64> {
    counts
        .iter()
        .map(|(name, count)| ((*name).to_owned(), *count))
        .collect()
}
