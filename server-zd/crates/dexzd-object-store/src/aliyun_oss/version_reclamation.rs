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

use crate::aliyun_oss::VersionIdSource;
use crate::aliyun_oss::reclamation_schedule::{
    DueReclamationPass, ReclamationSchedule, ReclamationTimings,
};
use crate::aliyun_oss::startup_probes::read_store_time_of_new_probe_version;
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
pub const MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION: Duration = Duration::from_secs(60 * 60);

/// OSS truncates last-write times to whole seconds, so a write may be up to this much later.
const STORE_TIMESTAMP_GRANULARITY_MICROS: i64 = 1_000_000;

/// Each key gets at most this many passes per minimum age, because a pass lists every kept version.
const MAXIMUM_PASSES_PER_MINIMUM_AGE: u32 = 12;

/// The first retry of a failed pass waits this fraction of the minimum age; retries double it.
const INITIAL_RETRY_BACKOFF_DIVISOR: u32 = 60;

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
    /// `version_ids` names the store clock probes of passes that run long after their write.
    ///
    /// # Panics
    ///
    /// Panics when called outside a Tokio runtime.
    pub(super) fn start(
        inner: Arc<dyn ObjectStore>,
        version_ids: Arc<dyn VersionIdSource>,
        minimum_superseded_age: Duration,
        listing_stale: VersionedListingStaleCounters,
    ) -> Self {
        let (requests, receiver) = mpsc::channel(RECLAMATION_QUEUE_CAPACITY);
        let task = VersionReclamationTask {
            inner,
            version_ids,
            minimum_superseded_age,
            listing_stale,
        };
        let task = tokio::spawn(task.run(receiver));
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

/// How one reclamation pass ended.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ReclamationPassOutcome {
    /// No version the pass kept can age into deletion.
    Finished,
    /// Versions kept as too young may be old enough at this local instant.
    FollowUpAt(Instant),
    /// A request failed or a listing missed a version; the pass is retried after a backoff.
    Failed,
    /// A newer writer owns the key's reclamation, or the store gives no last-write times.
    Abandoned,
}

/// The reclamation task: schedules passes and runs them one at a time.
struct VersionReclamationTask {
    inner: Arc<dyn ObjectStore>,
    version_ids: Arc<dyn VersionIdSource>,
    minimum_superseded_age: Duration,
    listing_stale: VersionedListingStaleCounters,
}

impl VersionReclamationTask {
    async fn run(self, mut receiver: mpsc::Receiver<ReclamationRequest>) {
        let timings = ReclamationTimings {
            minimum_interval_between_passes: self.minimum_superseded_age
                / MAXIMUM_PASSES_PER_MINIMUM_AGE,
            initial_retry_backoff: self.minimum_superseded_age / INITIAL_RETRY_BACKOFF_DIVISOR,
            maximum_retry_backoff: self.minimum_superseded_age,
        };
        let mut schedule = ReclamationSchedule::new(timings, MAXIMUM_SCHEDULED_KEYS);
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
                match self
                    .reclaim_superseded_versions(&pass, Instant::now())
                    .await
                {
                    ReclamationPassOutcome::Finished | ReclamationPassOutcome::Abandoned => {}
                    ReclamationPassOutcome::FollowUpAt(try_at) => {
                        schedule.record_follow_up(pass, try_at);
                    }
                    ReclamationPassOutcome::Failed => {
                        schedule.record_failed_pass(pass, Instant::now());
                    }
                }
            }
        }
    }

    /// Deletes the versions of one key that `pass` makes obsolete; failures only keep them.
    ///
    /// A version is deleted when its successor was written at least the minimum age before the
    /// reference time. The reference is the confirmed version's last-write time, or a fresh
    /// store clock probe for a pass that runs long after the write. Returns how the pass ended.
    async fn reclaim_superseded_versions(
        &self,
        pass: &DueReclamationPass,
        started_at: Instant,
    ) -> ReclamationPassOutcome {
        let logical_key = pass.logical_key.as_str();
        let confirmed_generation = pass.confirmed_generation;
        let prefix = version_prefix(logical_key);
        let minimum_age_micros = i64::try_from(self.minimum_superseded_age.as_micros())
            .expect("the minimum superseded age fits in i64 microseconds");
        let probed_store_time = if pass.needs_store_time {
            match read_store_time_of_new_probe_version(
                self.inner.as_ref(),
                self.version_ids.as_ref(),
            )
            .await
            {
                Ok(store_time) => Some(store_time),
                Err(error) => {
                    tracing::warn!(key = %logical_key, %error, "reading the store clock for reclamation failed");
                    return ReclamationPassOutcome::Failed;
                }
            }
        } else {
            None
        };
        // Listing after the next generation starts at the confirmed version itself.
        let mut start_after = confirmed_generation
            .checked_add(1)
            .map(|newer_generation| version_object_key(logical_key, newer_generation));
        let mut reference_written_at = None;
        let mut newer_written_at = None;
        let mut oldest_young_successor_written_at: Option<i64> = None;
        let mut has_failed_request = false;
        loop {
            let listing = match self
                .inner
                .list(&prefix, start_after.as_deref(), RECLAMATION_PAGE_KEYS)
                .await
            {
                Ok(listing) => listing,
                Err(error) => {
                    tracing::warn!(key = %logical_key, %error, "listing versions for reclamation failed");
                    return ReclamationPassOutcome::Failed;
                }
            };
            let mut reclaimable_keys = Vec::new();
            for object in listing.objects {
                let Some(generation) = version_generation(&object.key, &prefix) else {
                    tracing::warn!(key = %object.key, "reclamation found a malformed version key");
                    return ReclamationPassOutcome::Abandoned;
                };
                let Some(reference_written_at) = reference_written_at else {
                    // The confirmed version is gone only after a newer writer reclaimed it.
                    if generation != confirmed_generation {
                        return self
                            .classify_missing_confirmed_version(pass, Some(generation))
                            .await;
                    }
                    let Some(written_at) = object.last_modified_unix_micros else {
                        return ReclamationPassOutcome::Abandoned;
                    };
                    // Both are store times that had passed before this pass deletes anything.
                    reference_written_at =
                        Some(probed_store_time.map_or(written_at, |probed| probed.max(written_at)));
                    newer_written_at = Some(written_at);
                    continue;
                };
                // The nearest newer stored version was written no earlier than this one's successor.
                let successor_written_at = newer_written_at;
                newer_written_at = object.last_modified_unix_micros;
                if generation.saturating_add(2) > confirmed_generation {
                    continue;
                }
                // A truncated last-write time may hide up to a second of the successor's age.
                let latest_successor_written_at = successor_written_at.map(|written_at| {
                    written_at.saturating_add(STORE_TIMESTAMP_GRANULARITY_MICROS)
                });
                let is_superseded_long_enough =
                    latest_successor_written_at.is_some_and(|written_at| {
                        written_at <= reference_written_at.saturating_sub(minimum_age_micros)
                    });
                if !is_superseded_long_enough {
                    if let Some(written_at) = latest_successor_written_at {
                        oldest_young_successor_written_at = Some(
                            oldest_young_successor_written_at
                                .map_or(written_at, |oldest| oldest.min(written_at)),
                        );
                    }
                    continue;
                }
                match read_listed_header(
                    self.inner.as_ref(),
                    ObjectStoreOperation::Delete,
                    logical_key,
                    &object,
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
                        has_failed_request = true;
                    }
                }
            }
            let Some(reference_written_at) = reference_written_at else {
                // Only an empty first page leaves the reference unset here.
                return self.classify_missing_confirmed_version(pass, None).await;
            };
            if !reclaimable_keys.is_empty()
                && let Err(error) = self.inner.delete_many(&reclaimable_keys).await
            {
                tracing::warn!(key = %logical_key, %error, "deleting superseded versions failed");
                has_failed_request = true;
            }
            match listing.next_start_after {
                Some(next) => start_after = Some(next),
                None if has_failed_request => return ReclamationPassOutcome::Failed,
                None => {
                    return oldest_young_successor_written_at.map_or(
                        ReclamationPassOutcome::Finished,
                        |successor_written_at| {
                            let wait_micros = successor_written_at
                                .saturating_add(minimum_age_micros)
                                .saturating_sub(reference_written_at);
                            let wait =
                                Duration::from_micros(u64::try_from(wait_micros).unwrap_or(0));
                            // Local time only schedules the next pass; it decides no deletion.
                            ReclamationPassOutcome::FollowUpAt(started_at + wait)
                        },
                    );
                }
            }
        }
    }

    /// Decides why the listing of `pass` missed its confirmed version, listing `listed_generation`
    /// first instead, and returns how the pass ends.
    ///
    /// A generation two or more above the confirmed one may have reclaimed it legitimately.
    /// Otherwise the version must still exist, so the listing was stale and the pass is retried.
    async fn classify_missing_confirmed_version(
        &self,
        pass: &DueReclamationPass,
        listed_generation: Option<u64>,
    ) -> ReclamationPassOutcome {
        let prefix = version_prefix(&pass.logical_key);
        let newest_generation = match self.inner.list(&prefix, None, 1).await {
            Ok(listing) => listing
                .objects
                .first()
                .and_then(|newest| version_generation(&newest.key, &prefix)),
            Err(error) => {
                tracing::warn!(key = %pass.logical_key, %error, "listing the newest version for reclamation failed");
                return ReclamationPassOutcome::Failed;
            }
        };
        if newest_generation
            .is_some_and(|newest| newest >= pass.confirmed_generation.saturating_add(2))
        {
            tracing::debug!(
                key = %pass.logical_key,
                generation = pass.confirmed_generation,
                ?newest_generation,
                "a newer writer may have reclaimed the confirmed version and owns reclamation now"
            );
            return ReclamationPassOutcome::Abandoned;
        }
        record_stale_version_listing(
            &self.listing_stale,
            VersionedListingStaleSite::Reclamation,
            &pass.logical_key,
            pass.confirmed_generation,
            listed_generation,
        );
        ReclamationPassOutcome::Failed
    }
}

/// Waits until `deadline`, or forever without one.
async fn sleep_until_deadline(deadline: Option<Instant>) {
    match deadline {
        Some(deadline) => tokio::time::sleep_until(deadline).await,
        None => std::future::pending().await,
    }
}
