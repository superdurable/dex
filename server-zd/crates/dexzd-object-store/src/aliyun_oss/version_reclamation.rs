// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Background deletion of superseded versions after a confirmed write; tombstones stay.

use std::sync::Arc;
use std::time::Duration;

use dexzd_metrics::{VersionedListingStaleCounters, VersionedListingStaleSite};
use tokio::sync::mpsc;
use tokio::sync::mpsc::error::TrySendError;
use tokio::task::JoinHandle;

use crate::aliyun_oss::version_keys::{version_generation, version_object_key, version_prefix};
use crate::aliyun_oss::version_reads::{read_listed_header, record_stale_version_listing};
use crate::{ObjectStore, ObjectStoreOperation, STARTUP_PROBE_OBJECT_KEY_PREFIX};

/// How long a version must have been superseded before reclamation deletes it, in production.
///
/// The age is measured between last-write times that the store records, so host clock skew
/// does not shorten it. It makes it unlikely that a writer confirms its version only after the
/// next generation was reclaimed, which leaves its outcome unknown.
///
/// It also keeps replacements safe while listings lag behind writes by less than this age.
pub const MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION: Duration = Duration::from_secs(10 * 60);

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

/// What a reclamation pass needs besides its request.
struct ReclamationContext {
    inner: Arc<dyn ObjectStore>,
    minimum_superseded_age: Duration,
    listing_stale: VersionedListingStaleCounters,
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
    /// It deletes a version only after it was superseded for `minimum_superseded_age`.
    ///
    /// # Panics
    ///
    /// Panics when called outside a Tokio runtime.
    pub(super) fn start(
        inner: Arc<dyn ObjectStore>,
        minimum_superseded_age: Duration,
        listing_stale: VersionedListingStaleCounters,
    ) -> Self {
        let (requests, receiver) = mpsc::channel(RECLAMATION_QUEUE_CAPACITY);
        let context = ReclamationContext {
            inner,
            minimum_superseded_age,
            listing_stale,
        };
        let task = tokio::spawn(run_reclamation(context, receiver));
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
    context: ReclamationContext,
    mut receiver: mpsc::Receiver<ReclamationRequest>,
) {
    while let Some(request) = receiver.recv().await {
        reclaim_superseded_versions(&context, &request).await;
    }
}

/// Deletes the versions of one key that `request` makes obsolete; failures only keep them.
async fn reclaim_superseded_versions(context: &ReclamationContext, request: &ReclamationRequest) {
    let inner = context.inner.as_ref();
    let logical_key = request.logical_key.as_str();
    let confirmed_generation = request.confirmed_generation;
    let prefix = version_prefix(logical_key);
    let minimum_age_micros = i64::try_from(context.minimum_superseded_age.as_micros())
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
                    record_missing_confirmed_version(context, request, Some(generation)).await;
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
            match read_listed_header(inner, ObjectStoreOperation::Delete, logical_key, &object)
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
        if confirmed_written_at.is_none() {
            // Only an empty first page leaves the reference unset here.
            record_missing_confirmed_version(context, request, None).await;
            return;
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

/// Counts a listing that missed the confirmed version of `request`, listing `listed_generation`
/// first, unless a newer writer may have reclaimed that version.
///
/// Only a version two or more generations newer can have reclaimed it.
async fn record_missing_confirmed_version(
    context: &ReclamationContext,
    request: &ReclamationRequest,
    listed_generation: Option<u64>,
) {
    let prefix = version_prefix(&request.logical_key);
    let newest_generation = match context.inner.list(&prefix, None, 1).await {
        Ok(listing) => listing
            .objects
            .first()
            .and_then(|newest| version_generation(&newest.key, &prefix)),
        Err(error) => {
            tracing::warn!(key = %request.logical_key, %error, "listing the newest version for reclamation failed");
            return;
        }
    };
    if newest_generation
        .is_some_and(|newest| newest >= request.confirmed_generation.saturating_add(2))
    {
        tracing::debug!(
            key = %request.logical_key,
            generation = request.confirmed_generation,
            ?newest_generation,
            "a newer writer may have reclaimed the confirmed version"
        );
        return;
    }
    record_stale_version_listing(
        &context.listing_stale,
        VersionedListingStaleSite::Reclamation,
        &request.logical_key,
        request.confirmed_generation,
        listed_generation,
    );
}
