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
use tokio::time::Instant;

use crate::aliyun_oss::reclamation_schedule::{
    DueReclamationPass, ObservedStoreTime, ReclamationSchedule,
};
use crate::aliyun_oss::version_keys::{version_generation, version_object_key, version_prefix};
use crate::aliyun_oss::version_reads::{read_listed_header, record_stale_version_listing};
use crate::{ObjectStore, ObjectStoreOperation, STARTUP_PROBE_OBJECT_KEY_PREFIX};

/// How long a version must have been superseded before reclamation deletes it, in production.
///
/// The age is measured between last-write times that the store records, so host clock skew
/// does not shorten it. A follow-up pass adds monotonic local time, which skew cannot change. It
/// makes it unlikely that a writer confirms its version only after the
/// next generation was reclaimed, which leaves its outcome unknown.
///
/// It also keeps replacements safe while listings lag behind writes by less than this age.
pub const MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION: Duration = Duration::from_secs(60 * 60);

/// Each key gets at most this many passes per minimum age, because a pass lists every kept version.
const MAXIMUM_PASSES_PER_MINIMUM_AGE: u32 = 12;

/// Keys the schedule tracks; beyond it, the key with the latest deadline is forgotten.
const MAXIMUM_SCHEDULED_KEYS: usize = 4096;

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
/// Scheduled passes are lost with it; a later write of each key reclaims its versions.
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
        let task = tokio::spawn(run_reclamation(
            inner,
            receiver,
            minimum_superseded_age,
            listing_stale,
        ));
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
    minimum_superseded_age: Duration,
    listing_stale: VersionedListingStaleCounters,
) {
    let mut schedule = ReclamationSchedule::new(
        minimum_superseded_age / MAXIMUM_PASSES_PER_MINIMUM_AGE,
        MAXIMUM_SCHEDULED_KEYS,
    );
    loop {
        let next_deadline = schedule.next_deadline();
        tokio::select! {
            request = receiver.recv() => {
                let Some(request) = request else {
                    return;
                };
                schedule.record_confirmed_write(
                    &request.logical_key,
                    request.confirmed_generation,
                    Instant::now(),
                );
            }
            () = sleep_until_deadline(next_deadline) => {}
        }
        while let Some(pass) = schedule.take_due_pass(Instant::now()) {
            let follow_up = reclaim_superseded_versions(
                inner.as_ref(),
                minimum_superseded_age,
                &listing_stale,
                &pass,
                Instant::now(),
            )
            .await;
            if let Some(follow_up) = follow_up {
                schedule.record_follow_up(pass, follow_up.reference, follow_up.reclaimable_at);
            }
        }
    }
}

/// Waits until `deadline`, or forever without one.
async fn sleep_until_deadline(deadline: Option<Instant>) {
    match deadline {
        Some(deadline) => tokio::time::sleep_until(deadline).await,
        None => std::future::pending().await,
    }
}

/// A later pass that can delete versions an earlier pass kept because they were too young.
#[derive(Debug, Clone, Copy)]
struct ReclamationFollowUp {
    /// The store time that the earlier pass observed.
    reference: ObservedStoreTime,
    /// When the oldest kept version has been superseded for the minimum age.
    reclaimable_at: Instant,
}

/// Deletes the versions of one key that `pass` makes obsolete; failures only keep them.
/// A follow-up measures age from the earlier pass's store time plus local elapsed time.
///
/// Returns the follow-up that can delete the oldest version this pass kept as too young, or
/// `None` when no kept version can age into deletion or the pass stopped early.
async fn reclaim_superseded_versions(
    inner: &dyn ObjectStore,
    minimum_superseded_age: Duration,
    listing_stale: &VersionedListingStaleCounters,
    pass: &DueReclamationPass,
    started_at: Instant,
) -> Option<ReclamationFollowUp> {
    let logical_key = pass.logical_key.as_str();
    let confirmed_generation = pass.confirmed_generation;
    let prefix = version_prefix(logical_key);
    let minimum_age_micros = i64::try_from(minimum_superseded_age.as_micros())
        .expect("the minimum superseded age fits in i64 microseconds");
    // Listing after the next generation starts at the confirmed version itself.
    let mut start_after = confirmed_generation
        .checked_add(1)
        .map(|newer_generation| version_object_key(logical_key, newer_generation));
    let mut reference = None;
    let mut newer_written_at = None;
    let mut oldest_young_successor_written_at: Option<i64> = None;
    loop {
        let listing = match inner
            .list(&prefix, start_after.as_deref(), RECLAMATION_PAGE_KEYS)
            .await
        {
            Ok(listing) => listing,
            Err(error) => {
                tracing::warn!(key = %logical_key, %error, "listing versions for reclamation failed");
                return None;
            }
        };
        let mut reclaimable_keys = Vec::new();
        for object in listing.objects {
            let Some(generation) = version_generation(&object.key, &prefix) else {
                tracing::warn!(key = %object.key, "reclamation found a malformed version key");
                return None;
            };
            let Some(ObservedStoreTime {
                written_at_unix_micros: reference_written_at,
                ..
            }) = reference
            else {
                // The confirmed version is gone only after a newer writer reclaimed it.
                if generation != confirmed_generation {
                    record_stale_version_listing(
                        listing_stale,
                        VersionedListingStaleSite::Reclamation,
                        logical_key,
                        confirmed_generation,
                        Some(generation),
                    );
                    return None;
                }
                let written_at = object.last_modified_unix_micros?;
                let reference_written_at = pass.reference.map_or(written_at, |earlier| {
                    earlier.lower_bound_at(started_at).max(written_at)
                });
                reference = Some(ObservedStoreTime {
                    written_at_unix_micros: reference_written_at,
                    observed_at: started_at,
                });
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
                if let Some(written_at) = successor_written_at {
                    oldest_young_successor_written_at = Some(
                        oldest_young_successor_written_at
                            .map_or(written_at, |oldest| oldest.min(written_at)),
                    );
                }
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
        let Some(observed_reference) = reference else {
            // Only an empty first page leaves the reference unset here.
            record_stale_version_listing(
                listing_stale,
                VersionedListingStaleSite::Reclamation,
                logical_key,
                confirmed_generation,
                None,
            );
            return None;
        };
        if !reclaimable_keys.is_empty()
            && let Err(error) = inner.delete_many(&reclaimable_keys).await
        {
            tracing::warn!(key = %logical_key, %error, "deleting superseded versions failed");
        }
        match listing.next_start_after {
            Some(next) => start_after = Some(next),
            None => {
                return oldest_young_successor_written_at.map(|successor_written_at| {
                    follow_up_for(observed_reference, successor_written_at, minimum_age_micros)
                });
            }
        }
    }
}

/// Returns the follow-up at which a version whose successor was written at
/// `successor_written_at` has been superseded for the minimum age.
fn follow_up_for(
    reference: ObservedStoreTime,
    successor_written_at: i64,
    minimum_age_micros: i64,
) -> ReclamationFollowUp {
    let wait_micros = successor_written_at
        .saturating_add(minimum_age_micros)
        .saturating_sub(reference.written_at_unix_micros);
    let wait = Duration::from_micros(u64::try_from(wait_micros).unwrap_or(0));
    ReclamationFollowUp {
        reference,
        reclaimable_at: reference.observed_at + wait,
    }
}
