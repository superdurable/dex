// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Background deletion of superseded versions after a confirmed write. Listing discovery keeps
//! tombstones; HEAD probing deletes from the oldest version up.

use std::collections::{BTreeMap, VecDeque};
use std::sync::Arc;
use std::time::Duration;

use bytes::Bytes;
use dexzd_metrics::{VersionedListingStaleCounters, VersionedListingStaleSite};
use tokio::sync::mpsc;
use tokio::sync::mpsc::error::TrySendError;
use tokio::task::JoinHandle;

use crate::aliyun_oss::version_discovery::VersionDiscovery;
use crate::aliyun_oss::version_keys::{
    older_versions_prefix, version_anchor_key, version_generation, version_object_key,
    version_prefix,
};
use crate::aliyun_oss::version_reads::{read_listed_header, record_stale_version_listing};
use crate::{ObjectStore, ObjectStoreOperation, STARTUP_PROBE_OBJECT_KEY_PREFIX, WriteCondition};

/// How long a version must have been superseded before reclamation deletes it, in production.
///
/// The age is measured between last-write times that the store records, so host clock skew
/// does not shorten it. It makes it unlikely that a writer confirms its version only after the
/// next generation was reclaimed, which leaves its outcome unknown.
///
/// It also keeps replacements safe while listings lag behind writes by less than this age.
///
/// With [`VersionDiscovery::HeadProbing`], replacements stay safe at any age; it only decides
/// how long superseded versions stay.
pub const MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION: Duration = Duration::from_secs(10 * 60);

/// Pending requests beyond this are dropped; a later confirmed write reclaims the same versions.
const RECLAMATION_QUEUE_CAPACITY: usize = 256;

/// Versions deleted per batch request, the limit of [`ObjectStore::delete_many`].
const RECLAMATION_PAGE_KEYS: u32 = 1000;

/// Oldest versions that one bottom-up pass considers; the next pass continues above them.
const MAX_BOTTOM_UP_DELETIONS_PER_PASS: usize = 1000;

/// Keys whose deleted generations a process remembers; more forget the first key.
const MAX_REMEMBERED_RECLAIMED_KEYS: usize = 4096;

/// Body of an anchor; Tencent COS cannot create an empty object exclusively.
const ANCHOR_BODY: &[u8] = b"anchor";

/// One confirmed write whose older versions may be reclaimed.
#[derive(Debug)]
struct ReclamationRequest {
    logical_key: String,
    confirmed_generation: u64,
}

/// What a reclamation pass needs besides its request.
struct ReclamationContext {
    inner: Arc<dyn ObjectStore>,
    discovery: VersionDiscovery,
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
        discovery: VersionDiscovery,
        minimum_superseded_age: Duration,
        listing_stale: VersionedListingStaleCounters,
    ) -> Self {
        let (requests, receiver) = mpsc::channel(RECLAMATION_QUEUE_CAPACITY);
        let context = ReclamationContext {
            inner,
            discovery,
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

/// The highest generation of each key that this process deleted bottom-up.
///
/// Every generation up to it is gone for good: the first object stored at a generation never
/// returns once deleted, and bottom-up deletion removed every older one first.
#[derive(Default)]
struct ReclaimedGenerations {
    deleted_through: BTreeMap<String, u64>,
}

impl ReclaimedGenerations {
    fn deleted_through(&self, logical_key: &str) -> u64 {
        self.deleted_through.get(logical_key).copied().unwrap_or(0)
    }

    fn record_deleted(&mut self, logical_key: &str, generation: u64) {
        if self.deleted_through.len() >= MAX_REMEMBERED_RECLAIMED_KEYS
            && !self.deleted_through.contains_key(logical_key)
        {
            self.deleted_through.pop_first();
        }
        self.deleted_through
            .insert(logical_key.to_owned(), generation);
    }
}

async fn run_reclamation(
    context: ReclamationContext,
    mut receiver: mpsc::Receiver<ReclamationRequest>,
) {
    let mut reclaimed = ReclaimedGenerations::default();
    while let Some(request) = receiver.recv().await {
        match context.discovery {
            VersionDiscovery::Listing => reclaim_superseded_versions(&context, &request).await,
            VersionDiscovery::HeadProbing => {
                reclaim_oldest_versions_first(&context, &mut reclaimed, &request).await;
            }
        }
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

/// Deletes the versions that `request` makes obsolete one at a time from the oldest up, so the
/// remaining generations stay consecutive; tombstones go too. The first failure ends the pass.
///
/// A listing names the candidates and their write times, and a HEAD reads the confirmed
/// version's time. A lagging listing only keeps versions longer: each deletion first checks that
/// the generation below it is gone, and generation 1 first gets the key's anchor.
async fn reclaim_oldest_versions_first(
    context: &ReclamationContext,
    reclaimed: &mut ReclaimedGenerations,
    request: &ReclamationRequest,
) {
    let inner = context.inner.as_ref();
    let logical_key = request.logical_key.as_str();
    let confirmed_generation = request.confirmed_generation;
    // The newest two generations stay, so a reader can always check the newest one.
    let Some(highest_reclaimable) = confirmed_generation.checked_sub(2).filter(|g| *g > 0) else {
        return;
    };
    let minimum_age_micros = i64::try_from(context.minimum_superseded_age.as_micros())
        .expect("the minimum superseded age fits in i64 microseconds");
    let confirmed = match inner
        .head(&version_object_key(logical_key, confirmed_generation))
        .await
    {
        Ok(confirmed) => confirmed,
        Err(error) => {
            tracing::warn!(key = %logical_key, %error, "reading the confirmed version for reclamation failed");
            return;
        }
    };
    // Only a writer two or more generations newer can have reclaimed the confirmed version.
    let Some(reference_written_at) = confirmed.and_then(|object| object.last_modified_unix_micros)
    else {
        return;
    };
    let Some(oldest_versions) = list_oldest_versions(
        context,
        logical_key,
        confirmed_generation,
        reference_written_at,
    )
    .await
    else {
        return;
    };
    let mut deleted_through = reclaimed.deleted_through(logical_key);
    for (generation, successor_written_at) in oldest_versions {
        if generation > highest_reclaimable {
            return;
        }
        // A lagging listing can still show a generation this process deleted.
        if generation <= deleted_through {
            continue;
        }
        let is_superseded_long_enough = successor_written_at.is_some_and(|written_at| {
            written_at <= reference_written_at.saturating_sub(minimum_age_micros)
        });
        if !is_superseded_long_enough
            || !is_generation_below_gone(context, logical_key, generation, deleted_through).await
        {
            return;
        }
        let physical_key = version_object_key(logical_key, generation);
        if let Err(error) = inner.delete(&physical_key).await {
            tracing::warn!(key = %physical_key, %error, "deleting a superseded version failed; newer versions are kept");
            return;
        }
        deleted_through = generation;
        reclaimed.record_deleted(logical_key, generation);
    }
}

/// Lists the versions older than `confirmed_generation`, written at `confirmed_written_at`, oldest
/// first, each with the write time of the nearest newer listed version, which is no earlier than
/// its successor's.
///
/// Returns at most [`MAX_BOTTOM_UP_DELETIONS_PER_PASS`] of the oldest ones, or `None` after a
/// failed listing or a malformed key.
async fn list_oldest_versions(
    context: &ReclamationContext,
    logical_key: &str,
    confirmed_generation: u64,
    confirmed_written_at: i64,
) -> Option<Vec<(u64, Option<i64>)>> {
    let prefix = version_prefix(logical_key);
    let confirmed_key = version_object_key(logical_key, confirmed_generation);
    let older_prefix = older_versions_prefix(logical_key, confirmed_generation);
    // Newest first, as listed; the front version's successor was dropped or is the confirmed one.
    let mut window: VecDeque<(u64, Option<i64>)> = VecDeque::new();
    let mut front_successor_written_at = Some(confirmed_written_at);
    let mut start_after = confirmed_key.clone();
    loop {
        let listing = match context
            .inner
            .list(&older_prefix, Some(&start_after), RECLAMATION_PAGE_KEYS)
            .await
        {
            Ok(listing) => listing,
            Err(error) => {
                tracing::warn!(key = %logical_key, %error, "listing versions for reclamation failed");
                return None;
            }
        };
        let mut last_key = None;
        for object in listing.objects {
            // A lagging listing may ignore `start_after`; versions not older are skipped.
            if object.key <= start_after {
                continue;
            }
            let Some(generation) = version_generation(&object.key, &prefix) else {
                tracing::warn!(key = %object.key, "reclamation found a malformed version key");
                return None;
            };
            last_key = Some(object.key.clone());
            window.push_back((generation, object.last_modified_unix_micros));
            if window.len() > MAX_BOTTOM_UP_DELETIONS_PER_PASS
                && let Some((_, written_at)) = window.pop_front()
            {
                front_successor_written_at = written_at;
            }
        }
        match (listing.next_start_after, last_key) {
            (Some(_), Some(last_key)) => start_after = last_key,
            _ => break,
        }
    }
    let mut successor_written_at = front_successor_written_at;
    let mut oldest_first = Vec::with_capacity(window.len());
    for (generation, written_at) in window {
        oldest_first.push((generation, successor_written_at));
        successor_written_at = written_at;
    }
    oldest_first.reverse();
    Some(oldest_first)
}

/// Returns whether every version below `generation` is gone, so deleting it leaves no gap.
///
/// Before generation 1 goes, this creates the key's anchor, which tells readers that the key
/// still has versions. A failure keeps the version.
async fn is_generation_below_gone(
    context: &ReclamationContext,
    logical_key: &str,
    generation: u64,
    deleted_through: u64,
) -> bool {
    let inner = context.inner.as_ref();
    if generation == 1 {
        let anchor_key = version_anchor_key(logical_key);
        return match inner
            .put(
                &anchor_key,
                Bytes::from_static(ANCHOR_BODY),
                WriteCondition::CreateOnly,
            )
            .await
        {
            Ok(_) => true,
            Err(error) => {
                tracing::warn!(key = %anchor_key, %error, "creating the anchor failed; generation 1 is kept");
                false
            }
        };
    }
    let below = generation - 1;
    if below <= deleted_through {
        return true;
    }
    match inner.head(&version_object_key(logical_key, below)).await {
        Ok(None) => true,
        Ok(Some(_)) => false,
        Err(error) => {
            tracing::warn!(key = %logical_key, generation = below, %error, "reading the version below a superseded one failed; it is kept");
            false
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
    // Without a generation two above the confirmed one, no newer writer can exist.
    let may_be_reclaimed_by_newer_writer =
        request
            .confirmed_generation
            .checked_add(2)
            .is_some_and(|reclaiming_generation| {
                newest_generation.is_some_and(|newest| newest >= reclaiming_generation)
            });
    if may_be_reclaimed_by_newer_writer {
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
