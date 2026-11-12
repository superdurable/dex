// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Best-effort removal of superseded versions after a confirmed write.
//!
//! After a writer confirms generation `n`, a background task deletes generations `n - 2` and
//! below whose successor was written at least [`MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION`]
//! before generation `n`. It never deletes generation `n`, its predecessor, or a tombstone.
//! Because generation `n` exists, every deleted generation already has a successor, which is
//! what confirmation of a created version relies on.

use std::sync::Arc;
use std::time::Duration;

use tokio::sync::mpsc;
use tokio::sync::mpsc::error::TrySendError;
use tokio::task::JoinHandle;

use crate::aliyun_oss::version_keys::{version_generation, version_object_key, version_prefix};
use crate::aliyun_oss::version_reads::read_header_at;
use crate::{ObjectStore, ObjectStoreOperation, STARTUP_PROBE_OBJECT_KEY_PREFIX};

/// How long a version must have been superseded before reclamation deletes it.
///
/// The age is measured between last-write times that the store records, so host clock skew
/// does not shorten it. It makes it unlikely that a writer confirms its version only after the
/// next generation was reclaimed, which leaves its outcome unknown.
pub const MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION: Duration = Duration::from_secs(60);

/// Pending requests beyond this are dropped; a later confirmed write reclaims the same versions.
const RECLAMATION_QUEUE_CAPACITY: usize = 256;

/// Versions deleted per batch request, the limit of [`ObjectStore::delete_many`].
const RECLAMATION_PAGE_KEYS: u32 = 1000;

/// One confirmed write whose older versions may be reclaimed.
#[derive(Debug)]
struct ReclamationRequest {
    logical_key: String,
    confirmed_generation: u64,
}

/// Owns the background task that reclaims superseded versions.
///
/// Dropping the reclaimer aborts the task and any reclamation in progress.
pub(super) struct VersionReclaimer {
    requests: mpsc::Sender<ReclamationRequest>,
    task: JoinHandle<()>,
}

impl VersionReclaimer {
    /// Starts the reclamation task on the current Tokio runtime.
    ///
    /// # Panics
    ///
    /// Panics when called outside a Tokio runtime.
    pub(super) fn start(inner: Arc<dyn ObjectStore>) -> Self {
        let (requests, receiver) = mpsc::channel(RECLAMATION_QUEUE_CAPACITY);
        let task = tokio::spawn(run_reclamation(inner, receiver));
        Self { requests, task }
    }

    /// Queues reclamation of the versions that `confirmed_generation` of `logical_key` makes
    /// obsolete, without waiting.
    pub(super) fn reclaim_below(&self, logical_key: &str, confirmed_generation: u64) {
        // Probe keys are deleted whole, and generations below 3 have nothing to reclaim.
        if logical_key.starts_with(STARTUP_PROBE_OBJECT_KEY_PREFIX) || confirmed_generation < 3 {
            return;
        }
        let request = ReclamationRequest {
            logical_key: logical_key.to_owned(),
            confirmed_generation,
        };
        match self.requests.try_send(request) {
            Ok(()) => {}
            Err(TrySendError::Full(request)) => {
                tracing::debug!(
                    key = %request.logical_key,
                    generation = request.confirmed_generation,
                    "the version reclamation queue is full; a later write reclaims these versions"
                );
            }
            Err(TrySendError::Closed(request)) => {
                tracing::warn!(
                    key = %request.logical_key,
                    generation = request.confirmed_generation,
                    "the version reclamation task has stopped; superseded versions are kept"
                );
            }
        }
    }
}

impl Drop for VersionReclaimer {
    fn drop(&mut self) {
        self.task.abort();
    }
}

async fn run_reclamation(
    inner: Arc<dyn ObjectStore>,
    mut receiver: mpsc::Receiver<ReclamationRequest>,
) {
    while let Some(request) = receiver.recv().await {
        reclaim_superseded_versions(inner.as_ref(), &request).await;
    }
}

/// Deletes the versions of one key that `request` makes obsolete; failures only keep them.
async fn reclaim_superseded_versions(inner: &dyn ObjectStore, request: &ReclamationRequest) {
    let logical_key = request.logical_key.as_str();
    let confirmed_generation = request.confirmed_generation;
    let prefix = version_prefix(logical_key);
    let minimum_age_micros = i64::try_from(MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION.as_micros())
        .expect("the minimum superseded age fits in i64 microseconds");
    // Listing after the next generation starts at the confirmed version itself.
    let mut start_after = confirmed_generation
        .checked_add(1)
        .map(|newer_generation| version_object_key(logical_key, newer_generation));
    let mut confirmed_written_at = None;
    let mut newer_written_at = None;
    loop {
        let listing = match inner
            .list(&prefix, start_after.as_deref(), RECLAMATION_PAGE_KEYS)
            .await
        {
            Ok(listing) => listing,
            Err(error) => {
                tracing::warn!(key = %logical_key, %error, "listing versions for reclamation failed");
                return;
            }
        };
        let mut reclaimable_keys = Vec::new();
        for object in listing.objects {
            let Some(generation) = version_generation(&object.key, &prefix) else {
                tracing::warn!(key = %object.key, "reclamation found a malformed version key");
                return;
            };
            let Some(reference_written_at) = confirmed_written_at else {
                // The confirmed version is gone only after a newer writer reclaimed it.
                if generation != confirmed_generation {
                    return;
                }
                let Some(written_at) = object.last_modified_unix_micros else {
                    return;
                };
                confirmed_written_at = Some(written_at);
                newer_written_at = Some(written_at);
                continue;
            };
            // The nearest newer stored version was written no earlier than this one's successor.
            let successor_written_at = newer_written_at;
            newer_written_at = object.last_modified_unix_micros;
            if generation.saturating_add(2) > confirmed_generation {
                continue;
            }
            let is_superseded_long_enough = successor_written_at.is_some_and(|written_at| {
                written_at <= reference_written_at.saturating_sub(minimum_age_micros)
            });
            if !is_superseded_long_enough {
                continue;
            }
            match read_header_at(
                inner,
                ObjectStoreOperation::Delete,
                logical_key,
                &object.key,
            )
            .await
            {
                Ok(Some(header)) if !header.is_tombstone => reclaimable_keys.push(object.key),
                Ok(_) => {}
                Err(error) => {
                    tracing::warn!(
                        key = %object.key,
                        %error,
                        "reading a version header for reclamation failed; the version is kept"
                    );
                }
            }
        }
        if !reclaimable_keys.is_empty()
            && let Err(error) = inner.delete_many(&reclaimable_keys).await
        {
            tracing::warn!(key = %logical_key, %error, "deleting superseded versions failed");
        }
        match listing.next_start_after {
            Some(next) => start_after = Some(next),
            None => return,
        }
    }
}
