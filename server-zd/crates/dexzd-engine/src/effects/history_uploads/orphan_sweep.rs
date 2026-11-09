// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Periodic sweep of the `history/` prefix for objects of runs that no longer exist.
//!
//! A purge deletes a run's history objects before its RunPurge commits, but a write that object
//! storage accepted earlier can still land afterwards. Run IDs are never reused, so such an
//! object is only a leak: no read ever returns it. The sweep deletes it.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;
use std::time::Duration;

use dexzd_history_segment::{HISTORY_SEGMENT_OBJECT_KEY_PREFIX, parse_history_object_key};
use dexzd_keyspace::PartitionId;
use dexzd_object_store::ObjectMetadata;

use super::until_stopped;
use crate::effects::state_reads::{RunLocation, read_run_record};
use crate::effects::{EffectContext, EffectError};

/// Objects younger than this stay until a later sweep, so the sweep never races a write whose
/// run a newer owner created after this node's view.
const ORPHAN_MIN_AGE: Duration = Duration::from_secs(60 * 60);
/// Keys one listing page returns.
const LISTING_PAGE_KEYS: u32 = 1000;

/// Sweeps every `history_orphan_sweep_interval`, starting one interval after startup, until
/// shutdown.
pub(crate) async fn run_history_orphan_sweep(context: Arc<EffectContext>) {
    let interval = context.config.history_orphan_sweep_interval;
    if interval.is_zero() {
        return;
    }
    let mut ticker = tokio::time::interval_at(tokio::time::Instant::now() + interval, interval);
    ticker.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    loop {
        // Biased for reproducible simulation; shutdown fires once.
        tokio::select! {
            biased;
            () = context.shutdown.cancelled() => return,
            _ = ticker.tick() => {}
        }
        match sweep_orphan_history_objects(&context).await {
            Ok(deleted_count) => {
                tracing::debug!(deleted_count, "swept orphan history objects");
            }
            Err(EffectError::Stopped) => return,
            Err(error) => {
                tracing::warn!(%error, "sweeping orphan history objects failed; retrying at the next sweep");
            }
        }
    }
}

/// Lists the whole `history/` prefix once and deletes each object at least an hour old whose run
/// is in a partition this node owns and has no run record in committed state.
///
/// Returns how many objects it deleted, or the first failure; the next sweep starts over.
pub(crate) async fn sweep_orphan_history_objects(
    context: &EffectContext,
) -> Result<u64, EffectError> {
    let mut start_after: Option<String> = None;
    let mut deleted_count = 0_u64;
    loop {
        let listing = until_stopped(
            context,
            context.history_object_store.list(
                HISTORY_SEGMENT_OBJECT_KEY_PREFIX,
                start_after.as_deref(),
                LISTING_PAGE_KEYS,
            ),
        )
        .await?;
        let orphan_keys = orphan_keys(context, &listing.objects)?;
        if !orphan_keys.is_empty() {
            until_stopped(
                context,
                context.history_object_store.delete_many(&orphan_keys),
            )
            .await?;
            let count = u64::try_from(orphan_keys.len()).expect("a page length fits in u64");
            context.metrics.history_orphan_objects_deleted.inc_by(count);
            tracing::info!(
                deleted_count = count,
                "deleted history objects of runs that no longer exist"
            );
            deleted_count += count;
        }
        match listing.next_start_after {
            Some(next) => start_after = Some(next),
            None => return Ok(deleted_count),
        }
    }
}

/// Returns the keys among `objects` that the sweep deletes, reading run records from one
/// committed view.
fn orphan_keys(
    context: &EffectContext,
    objects: &[ObjectMetadata],
) -> Result<Vec<String>, EffectError> {
    let owned_partitions: BTreeSet<PartitionId> = context
        .task_queue_memory()
        .owned_partition_list()
        .into_iter()
        .collect();
    let reader = context.flow_effect_operations.committed_state_reader()?;
    let min_age_micros = i64::try_from(ORPHAN_MIN_AGE.as_micros()).expect("an hour fits in i64");
    let now_micros = context.clock.now_unix_micros();
    let mut is_run_present: BTreeMap<RunLocation, bool> = BTreeMap::new();
    let mut keys = Vec::new();
    for object in objects {
        let Some(object_run) = parse_history_object_key(&object.key) else {
            tracing::warn!(
                key = object.key,
                "skipping an object under history/ with an unknown key"
            );
            continue;
        };
        if !owned_partitions.contains(&context.hasher.partition_of(object_run.flow_hash)) {
            continue;
        }
        // A store that reports no write time never lets the sweep delete.
        let is_old_enough = object
            .last_modified_unix_micros
            .is_some_and(|written_at| now_micros.saturating_sub(written_at) >= min_age_micros);
        if !is_old_enough {
            continue;
        }
        let run = RunLocation {
            flow_hash: object_run.flow_hash,
            run_id: object_run.run_id,
        };
        let is_present = match is_run_present.get(&run) {
            Some(is_present) => *is_present,
            None => {
                let is_present = read_run_record(reader.as_ref(), run)?.is_some();
                is_run_present.insert(run, is_present);
                is_present
            }
        };
        if !is_present {
            keys.push(object.key.clone());
        }
    }
    Ok(keys)
}
