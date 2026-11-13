// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Finding the newest version of a mutable key: by listing, or by HEAD requests past a lower bound.

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

use dexzd_metrics::{VersionedListingStaleCounters, VersionedListingStaleSite};

use crate::aliyun_oss::version_body::{VERSION_HEADER_LENGTH, VERSION_HEADER_RANGE, VersionHeader};
use crate::aliyun_oss::version_keys::{
    malformed_version_key, version_anchor_key, version_generation, version_object_key,
    version_prefix,
};
use crate::aliyun_oss::version_reads::{
    MAX_NEWEST_VERSION_LOOKUPS, NewestVersion, VersionReadExtent, decode_header, inner_error,
    is_listed_object, read_newest_listed_version, read_version_header,
    record_stale_version_listing,
};
use crate::{
    ObjectMetadata, ObjectStore, ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation,
    ObjectStoreResult,
};

/// Mutable keys whose newest generation is remembered; more evict the oldest observation.
const MAX_GENERATION_HINTS: usize = 4096;

/// How the versioned layer finds the newest version of a mutable key.
///
/// The two strategies also reclaim superseded versions differently, so every process that
/// shares a prefix must use the same one; [`crate::check_version_discovery_marker`] checks
/// that at startup. The server uses [`VersionDiscovery::Listing`] for Aliyun OSS by default and
/// [`VersionDiscovery::HeadProbing`] for Tencent COS.
///
/// ```no_run
/// # async fn example(inner: std::sync::Arc<dyn dexzd_object_store::ObjectStore>) {
/// use dexzd_metrics::NodeMetrics;
/// use dexzd_object_store::{
///     AliyunOssObjectStore, MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION, VersionDiscovery,
/// };
///
/// let store = AliyunOssObjectStore::with_version_discovery(
///     inner,
///     MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION,
///     NodeMetrics::unregistered(),
///     VersionDiscovery::HeadProbing,
/// );
/// # drop(store);
/// # }
/// ```
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[non_exhaustive]
pub enum VersionDiscovery {
    /// A one-key listing names the newest version, and a listing after each create confirms
    /// it.
    ///
    /// It needs listings that show every write at once. Aliyun OSS listings did so in every
    /// test, but Aliyun documents strong consistency only for single-object operations.
    Listing,
    /// HEAD requests past a lower bound find the newest version, and reads of the neighboring
    /// generations confirm it and each create. Reclamation deletes versions from the oldest up.
    ///
    /// It needs strongly consistent HEAD, GET, create-only, and DELETE requests. The versions it
    /// reads and the outcomes of conditional writes never depend on listings or on any clock. A
    /// listing that lags behind writes adds requests, and one that stays stale through every
    /// attempt makes a read fail transiently. Use it only on a prefix without versions written
    /// by [`VersionDiscovery::Listing`]. Tencent COS needs it, because its listings can repeat a
    /// listing from seconds earlier.
    HeadProbing,
}

/// Finds the newest version of mutable keys with the strategy chosen at construction.
pub(super) struct NewestVersionLocator {
    pub(super) inner: Arc<dyn ObjectStore>,
    pub(super) discovery: VersionDiscovery,
    hints: Mutex<GenerationHints>,
    listing_stale: VersionedListingStaleCounters,
}

/// The newest version seen of each recently read or written mutable key.
#[derive(Default)]
struct GenerationHints {
    by_key: BTreeMap<String, GenerationHint>,
    next_sequence: u64,
}

/// A version that this process found to be the first object ever stored at its generation.
#[derive(Clone)]
struct GenerationHint {
    generation: u64,
    version_id: u128,
    /// The version object's metadata, unknown after this process created it.
    metadata: Option<ObjectMetadata>,
    /// When the hint was remembered, in remembering order; the lowest is evicted first.
    sequence: u64,
}

/// Where a HEAD-probing read attempt starts.
enum LowerBound {
    /// No version is known; the attempt starts at generation 1.
    Unknown,
    /// A version this process found to be the first object at its generation.
    Hint(GenerationHint),
    /// The newest version a listing showed; it existed when the listing was made.
    Listed {
        generation: u64,
        metadata: ObjectMetadata,
    },
}

impl LowerBound {
    fn generation(&self) -> u64 {
        match self {
            Self::Unknown => 0,
            Self::Hint(hint) => hint.generation,
            Self::Listed { generation, .. } => *generation,
        }
    }
}

/// What one HEAD-probing read attempt found.
enum ProbedRead {
    /// The newest version, or `None` when the key has no version.
    Confirmed(Option<NewestVersion>),
    /// The attempt could not confirm a version; the next attempt starts from a listing.
    Unconfirmed,
}

impl NewestVersionLocator {
    pub(super) fn new(
        inner: Arc<dyn ObjectStore>,
        discovery: VersionDiscovery,
        listing_stale: VersionedListingStaleCounters,
    ) -> Self {
        Self {
            inner,
            discovery,
            hints: Mutex::new(GenerationHints::default()),
            listing_stale,
        }
    }

    /// Returns the inner store.
    pub(super) fn inner(&self) -> &dyn ObjectStore {
        self.inner.as_ref()
    }

    /// Reads the newest version, or `None` when the key has none.
    ///
    /// Returns a transient error when the newest version changed, or could not be confirmed,
    /// during every attempt.
    pub(super) async fn read_newest_version(
        &self,
        operation: ObjectStoreOperation,
        logical_key: &str,
        extent: VersionReadExtent,
    ) -> ObjectStoreResult<Option<NewestVersion>> {
        match self.discovery {
            VersionDiscovery::Listing => {
                read_newest_listed_version(self.inner(), operation, logical_key, extent).await
            }
            VersionDiscovery::HeadProbing => {
                self.read_newest_probed_version(operation, logical_key, extent)
                    .await
            }
        }
    }

    /// Remembers `generation` with `version_id`, which this process created and confirmed, as
    /// the newest version of `logical_key`.
    pub(super) fn remember_created_generation(
        &self,
        logical_key: &str,
        generation: u64,
        version_id: u128,
    ) {
        if self.discovery == VersionDiscovery::HeadProbing {
            self.remember(logical_key, generation, version_id, None);
        }
    }

    /// Forgets the newest generation of `logical_key`, whose versions were deleted.
    pub(super) fn forget(&self, logical_key: &str) {
        self.hints
            .lock()
            .expect("generation hints are not poisoned")
            .by_key
            .remove(logical_key);
    }

    /// Returns whether the anchor of `logical_key` exists, so generation 1 may have been reclaimed.
    pub(super) async fn is_anchored(
        &self,
        operation: ObjectStoreOperation,
        logical_key: &str,
    ) -> ObjectStoreResult<bool> {
        self.inner
            .head(&version_anchor_key(logical_key))
            .await
            .map(|anchor| anchor.is_some())
            .map_err(|error| inner_error(operation, logical_key, &error))
    }

    async fn read_newest_probed_version(
        &self,
        operation: ObjectStoreOperation,
        logical_key: &str,
        extent: VersionReadExtent,
    ) -> ObjectStoreResult<Option<NewestVersion>> {
        let mut may_use_hint = true;
        for _ in 0..MAX_NEWEST_VERSION_LOOKUPS {
            let lower_bound = match self.hint(logical_key).filter(|_| may_use_hint) {
                Some(hint) => LowerBound::Hint(hint),
                None => self.listed_lower_bound(operation, logical_key).await?,
            };
            let listed_generation = match &lower_bound {
                LowerBound::Listed { generation, .. } => Some(*generation),
                LowerBound::Unknown | LowerBound::Hint(_) => None,
            };
            let probed = self
                .read_probed_attempt(operation, logical_key, extent, lower_bound)
                .await?;
            let confirmed_generation = match &probed {
                ProbedRead::Confirmed(newest) => {
                    Some(newest.as_ref().map(|newest| newest.generation))
                }
                ProbedRead::Unconfirmed => None,
            };
            if listed_generation.is_some()
                && confirmed_generation.is_none_or(|confirmed| confirmed != listed_generation)
            {
                self.listing_stale
                    .record(VersionedListingStaleSite::NewestVersionLookup);
            }
            match probed {
                ProbedRead::Confirmed(newest) => return Ok(newest),
                ProbedRead::Unconfirmed => {
                    self.forget(logical_key);
                    may_use_hint = false;
                }
            }
        }
        Err(ObjectStoreError::new(
            operation,
            logical_key,
            ObjectStoreErrorKind::Transient,
            "the newest version changed or could not be confirmed during every read attempt",
        ))
    }

    /// Returns the first version of a one-key listing, or [`LowerBound::Unknown`] when it shows
    /// none.
    async fn listed_lower_bound(
        &self,
        operation: ObjectStoreOperation,
        logical_key: &str,
    ) -> ObjectStoreResult<LowerBound> {
        let prefix = version_prefix(logical_key);
        let listing = self
            .inner
            .list(&prefix, None, 1)
            .await
            .map_err(|error| inner_error(operation, logical_key, &error))?;
        let Some(listed) = listing.objects.into_iter().next() else {
            return Ok(LowerBound::Unknown);
        };
        // The anchor sorts after every version, so it comes first only without versions.
        if listed.key == version_anchor_key(logical_key) {
            return Ok(LowerBound::Unknown);
        }
        let generation = version_generation(&listed.key, &prefix)
            .ok_or_else(|| malformed_version_key(operation, logical_key, &listed.key))?;
        Ok(LowerBound::Listed {
            generation,
            metadata: listed,
        })
    }

    /// HEADs the generations after `lower_bound` until one is missing, then confirms the last
    /// one found as the newest version.
    ///
    /// The candidate must be the object that existed before the missing generation was read,
    /// and the first object ever stored at its generation. Bottom-up reclamation then keeps
    /// every generation above it, so the missing one was never written.
    async fn read_probed_attempt(
        &self,
        operation: ObjectStoreOperation,
        logical_key: &str,
        extent: VersionReadExtent,
        lower_bound: LowerBound,
    ) -> ObjectStoreResult<ProbedRead> {
        let mut newest = lower_bound.generation();
        let mut probed_metadata = None;
        while let Some(next) = newest.checked_add(1) {
            let Some(metadata) = self.head_version(operation, logical_key, next).await? else {
                break;
            };
            newest = next;
            probed_metadata = Some(metadata);
        }
        if newest == 0 {
            // Generation 1 is gone only after reclamation anchored the key.
            return Ok(if self.is_anchored(operation, logical_key).await? {
                ProbedRead::Unconfirmed
            } else {
                ProbedRead::Confirmed(None)
            });
        }
        let (metadata, known_first_version_id) = match (probed_metadata, lower_bound) {
            (Some(metadata), _) => (metadata, None),
            (None, LowerBound::Listed { metadata, .. }) => (metadata, None),
            (None, LowerBound::Hint(hint)) => {
                let metadata = match hint.metadata {
                    Some(metadata) => metadata,
                    // A hint from this process's create has no metadata yet.
                    None => match self.head_version(operation, logical_key, newest).await? {
                        Some(metadata) => metadata,
                        None => return Ok(ProbedRead::Unconfirmed),
                    },
                };
                (metadata, Some(hint.version_id))
            }
            (None, LowerBound::Unknown) => {
                unreachable!("probing from generation 0 either finds a version or returns")
            }
        };
        let physical_key = version_object_key(logical_key, newest);
        let content = match extent {
            VersionReadExtent::HeaderOnly => {
                self.inner
                    .get_range(&physical_key, VERSION_HEADER_RANGE)
                    .await
            }
            VersionReadExtent::WholeBody => self.inner.get(&physical_key).await,
        }
        .map_err(|error| inner_error(operation, logical_key, &error))?;
        // The same object before and after the missing generation was read existed in between.
        let Some(content) = content.filter(|content| is_listed_object(&metadata, content)) else {
            return Ok(ProbedRead::Unconfirmed);
        };
        let header = decode_header(operation, logical_key, &physical_key, &content)?;
        let is_first_version = match known_first_version_id {
            Some(version_id) => header.version_id == version_id,
            None => {
                self.is_first_version_at(operation, logical_key, newest, &header)
                    .await?
            }
        };
        if !is_first_version {
            return Ok(ProbedRead::Unconfirmed);
        }
        let payload = (extent == VersionReadExtent::WholeBody)
            .then(|| content.bytes.slice(VERSION_HEADER_LENGTH..));
        self.remember(
            logical_key,
            newest,
            header.version_id,
            Some(metadata.clone()),
        );
        Ok(ProbedRead::Confirmed(Some(NewestVersion {
            generation: newest,
            header,
            payload,
            payload_size: content
                .object_size
                .saturating_sub(VERSION_HEADER_LENGTH as u64),
            last_modified_unix_micros: metadata.last_modified_unix_micros,
        })))
    }

    /// Returns whether the version with `header` at `generation`, read just before, is the first
    /// object ever stored at that generation rather than a recreated reclaimed one.
    ///
    /// Generation 1 is the first while the key has no anchor. A later generation is the first
    /// when its predecessor still holds the version it names: reclamation deletes generations
    /// from the oldest up, so a reclaimed generation's predecessor is gone.
    async fn is_first_version_at(
        &self,
        operation: ObjectStoreOperation,
        logical_key: &str,
        generation: u64,
        header: &VersionHeader,
    ) -> ObjectStoreResult<bool> {
        let Some(previous_generation) = generation.checked_sub(1).filter(|previous| *previous > 0)
        else {
            return Ok(!self.is_anchored(operation, logical_key).await?);
        };
        let Some(predecessor_id) = header.predecessor_id else {
            return Ok(false);
        };
        let predecessor = read_version_header(
            self.inner.as_ref(),
            operation,
            logical_key,
            previous_generation,
        )
        .await?;
        Ok(predecessor.is_some_and(|predecessor| predecessor.version_id == predecessor_id))
    }

    async fn head_version(
        &self,
        operation: ObjectStoreOperation,
        logical_key: &str,
        generation: u64,
    ) -> ObjectStoreResult<Option<ObjectMetadata>> {
        self.inner
            .head(&version_object_key(logical_key, generation))
            .await
            .map_err(|error| inner_error(operation, logical_key, &error))
    }

    fn hint(&self, logical_key: &str) -> Option<GenerationHint> {
        self.hints
            .lock()
            .expect("generation hints are not poisoned")
            .by_key
            .get(logical_key)
            .cloned()
    }

    fn remember(
        &self,
        logical_key: &str,
        generation: u64,
        version_id: u128,
        metadata: Option<ObjectMetadata>,
    ) {
        let mut hints = self
            .hints
            .lock()
            .expect("generation hints are not poisoned");
        let sequence = hints.next_sequence;
        hints.next_sequence += 1;
        let by_key = &mut hints.by_key;
        // Generations only grow, so a lower one comes from an older observation.
        if by_key
            .get(logical_key)
            .is_some_and(|known| known.generation > generation)
        {
            return;
        }
        if by_key.len() >= MAX_GENERATION_HINTS && !by_key.contains_key(logical_key) {
            let oldest_key = by_key
                .iter()
                .min_by_key(|(_, known)| known.sequence)
                .map(|(key, _)| key.clone());
            if let Some(oldest_key) = oldest_key {
                by_key.remove(&oldest_key);
            }
        }
        by_key.insert(
            logical_key.to_owned(),
            GenerationHint {
                generation,
                version_id,
                metadata,
                sequence,
            },
        );
    }

    /// Counts and logs a listing that did not show `expected_generation`, created by this process.
    pub(super) fn record_stale_listing(
        &self,
        site: VersionedListingStaleSite,
        logical_key: &str,
        expected_generation: u64,
        listed_generation: Option<u64>,
    ) {
        record_stale_version_listing(
            &self.listing_stale,
            site,
            logical_key,
            expected_generation,
            listed_generation,
        );
    }
}
