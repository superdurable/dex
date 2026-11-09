// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! History offload on the leader: uploads sealed history segments and acknowledges them in seq
//! order, rescans the upload backlog, deletes the objects of expired segments and of a purged
//! run, and sweeps objects of runs that no longer exist. It also proposes local history
//! cleanup.
//!
//! Apply decides each segment's range and content, so every owner of a partition writes the same
//! object at the same key. A repeated upload therefore finds an object with an equal content
//! checksum and acknowledges it without fencing. Uploads never block command commits: a failed
//! upload retries with backoff while the backlog gauges grow.
//!
//! Every object storage call ends when the node stops, so no history request outlives its owner.
//!
//! Partition rebuilds queue background backlog gauge publication when periodic rescanning is enabled.

mod expired_object_deletion;
mod local_cleanup;
mod orphan_sweep;
mod run_history_objects;
mod segment_upload;
mod upload_limiter;
mod upload_rescan;

use std::future::Future;

use dexzd_object_store::ObjectStoreResult;

pub(crate) use expired_object_deletion::delete_expired_history_objects;
pub(crate) use local_cleanup::clean_up_local_history;
pub(crate) use orphan_sweep::run_history_orphan_sweep;
pub(crate) use run_history_objects::delete_run_history_objects;
pub(crate) use segment_upload::upload_sealed_history_segments;
pub(crate) use upload_limiter::HistoryUploadLimiter;
pub use upload_limiter::HistoryUploadLimits;
pub(crate) use upload_rescan::{
    SealedSegmentCounter, request_partition_backlog_rescan, run_history_upload_rescan,
};

use crate::effects::{EffectContext, EffectError};

/// Runs one object storage call, abandoning it once the node stops.
///
/// Returns the call's result, with a failure as [`EffectError::ObjectStore`], or
/// [`EffectError::Stopped`] when shutdown came first.
async fn until_stopped<T>(
    context: &EffectContext,
    call: impl Future<Output = ObjectStoreResult<T>>,
) -> Result<T, EffectError> {
    // Biased for reproducible simulation; shutdown fires once.
    tokio::select! {
        biased;
        () = context.shutdown.cancelled() => Err(EffectError::Stopped),
        result = call => result.map_err(EffectError::ObjectStore),
    }
}
