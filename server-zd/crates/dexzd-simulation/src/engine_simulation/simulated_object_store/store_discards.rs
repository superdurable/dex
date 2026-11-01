// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Whether a start that discarded its existing local store had a reason to.

use std::sync::Arc;

use dexzd_engine::EngineLocalStore;
use dexzd_protocol::dexzd::v1::Manifest;

use crate::engine_simulation::simulated_node::NODE_ID;
use crate::engine_simulation::simulated_object_store::closed_stream_replays::lowest_applied_position;
use crate::invariants::InvariantViolation;

/// Checks that a start discarded its store `existing` only behind cleanup or with a snapshot.
///
/// Returns whether the start discarded `existing` instead of serving from it.
pub(crate) fn has_discarded_existing_store(
    manifest: Option<&Manifest>,
    existing: Option<&Arc<EngineLocalStore>>,
    serving_store: &Arc<EngineLocalStore>,
) -> Result<bool, InvariantViolation> {
    let Some(existing) = existing.filter(|existing| !Arc::ptr_eq(existing, serving_store)) else {
        return Ok(false);
    };
    let Some(manifest) = manifest else {
        return Err(InvariantViolation::new(
            "store-discarded-without-gap",
            "a start discarded its store without reading the manifest".to_owned(),
        ));
    };
    let applied = lowest_applied_position(existing);
    let is_explained = match applied {
        None => has_complete_node_snapshot(manifest) || resumes_behind_cleanup(manifest, None),
        Some(applied) => resumes_behind_cleanup(manifest, Some(applied)),
    };
    if is_explained {
        return Ok(true);
    }
    Err(InvariantViolation::new(
        "store-discarded-without-gap",
        format!("a start discarded a store at {applied:?} that the retained log could update"),
    ))
}

/// Returns whether every partition records one snapshot of this node, as recovery requires.
fn has_complete_node_snapshot(manifest: &Manifest) -> bool {
    let Some(first) = manifest
        .partitions
        .first()
        .and_then(|partition| partition.snapshot.as_ref())
    else {
        return false;
    };
    first.node_id == NODE_ID
        && manifest.partitions.iter().all(|partition| {
            partition.snapshot.as_ref() == Some(first) && partition.owner_node_id == NODE_ID
        })
}

/// Returns whether log cleanup deleted a batch that a store at `applied` still needs.
fn resumes_behind_cleanup(manifest: &Manifest, applied: Option<(u32, u64)>) -> bool {
    let segments = manifest
        .partitions
        .first()
        .map(|partition| partition.segments.as_slice())
        .unwrap_or_default();
    if let Some((incarnation, _)) = applied
        && !segments
            .iter()
            .any(|segment| segment.incarnation == incarnation)
    {
        return true;
    }
    segments
        .iter()
        .filter(|segment| segment.node_id == NODE_ID)
        .any(|segment| {
            let resumes_at = match applied {
                Some((incarnation, _)) if incarnation > segment.incarnation => return false,
                Some((incarnation, seq)) if incarnation == segment.incarnation => seq + 1,
                _ => 1,
            };
            resumes_at < segment.start_seq
        })
}
