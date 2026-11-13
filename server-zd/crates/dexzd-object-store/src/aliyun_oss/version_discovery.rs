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
use std::time::Duration;

use dexzd_metrics::{VersionedListingStaleCounters, VersionedListingStaleSite};
use tokio::time::Instant;

use crate::aliyun_oss::version_body::VERSION_HEADER_LENGTH;
use crate::aliyun_oss::version_keys::{
    malformed_version_key, version_generation, version_object_key, version_prefix,
};
use crate::aliyun_oss::version_reads::{
    MAX_NEWEST_VERSION_LOOKUPS, NewestVersion, VersionReadExtent, decode_header, inner_error,
    is_listed_object, read_newest_listed_version, record_stale_version_listing,
};
use crate::{ObjectMetadata, ObjectStore, ObjectStoreError, ObjectStoreErrorKind};
use crate::{ObjectStoreOperation, ObjectStoreResult};

/// Mutable keys whose newest generation is remembered; more evict the oldest observation.
const MAX_GENERATION_HINTS: usize = 4096;

/// How the versioned layer finds the newest version of a mutable key.
///
/// Both strategies use the same physical layout, so processes with different strategies can
/// share a bucket, including during a rolling upgrade between them. Every process that shares
/// the bucket must reclaim with the same minimum superseded age, because
/// [`VersionDiscovery::HeadProbing`] trusts a lower bound for half that age.
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
    /// A listing, or the newest generation this process saw recently, gives a lower bound.
    /// HEAD requests of the following generations then find the newest one, and a read of the
    /// next generation confirms each create.
    ///
    /// It needs strongly consistent HEAD and GET requests, and listings that lag behind writes
    /// by less than the minimum superseded age. Tencent COS needs it, because its listings can
    /// repeat a listing from seconds earlier. It costs one more HEAD for most reads.
    HeadProbing,
}

/// Finds the newest version of mutable keys with the strategy chosen at construction.
pub(super) struct NewestVersionLocator {
    pub(super) inner: Arc<dyn ObjectStore>,
    pub(super) discovery: VersionDiscovery,
    /// A hint younger than this cannot name a reclaimed generation; half the superseded age.
    hint_lifetime: Duration,
    hints: Mutex<GenerationHints>,
    listing_stale: VersionedListingStaleCounters,
}

/// The newest generation seen of each recently read or written mutable key.
#[derive(Default)]
struct GenerationHints {
    by_key: BTreeMap<String, GenerationHint>,
}

#[derive(Clone)]
struct GenerationHint {
    generation: u64,
    /// The version object's metadata, unknown after this process created it.
    metadata: Option<ObjectMetadata>,
    /// A time before the generation was seen as the newest.
    observed_at: Instant,
}

/// What HEAD requests past a lower bound found.
enum ProbedGenerations {
    /// The key has no version: the lower bound is zero and generation 1 is missing.
    Absent,
    /// The newest generation and its metadata.
    Newest(u64, ObjectMetadata),
    /// The lower bound has no newer generation and is gone itself.
    LowerBoundGone,
}

/// Where a head-probing lookup starts.
struct LowerBound {
    generation: u64,
    metadata: Option<ObjectMetadata>,
    is_listed: bool,
    /// A time before `generation` was the newest; probes are valid only for a while after it.
    observed_at: Instant,
}

impl NewestVersionLocator {
    pub(super) fn new(
        inner: Arc<dyn ObjectStore>,
        discovery: VersionDiscovery,
        minimum_superseded_age: Duration,
        listing_stale: VersionedListingStaleCounters,
    ) -> Self {
        Self {
            inner,
            discovery,
            hint_lifetime: minimum_superseded_age / 2,
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
    /// Returns a transient error when the newest version changed during every attempt, or
    /// when a listed lower bound stayed gone.
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

    /// Remembers `generation` as the newest version of `logical_key`, seen after `observed_at`.
    pub(super) fn remember_created_generation(
        &self,
        logical_key: &str,
        generation: u64,
        observed_at: Instant,
    ) {
        if self.discovery == VersionDiscovery::HeadProbing {
            self.remember(
                logical_key,
                GenerationHint {
                    generation,
                    metadata: None,
                    observed_at,
                },
            );
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

    /// Returns whether a create that started at `read_started_at` was too quick to land after
    /// its generation was reclaimed, which takes at least the minimum superseded age.
    pub(super) fn is_too_recent_for_resurrection(&self, read_started_at: Instant) -> bool {
        read_started_at.elapsed() < self.hint_lifetime
    }

    async fn read_newest_probed_version(
        &self,
        operation: ObjectStoreOperation,
        logical_key: &str,
        extent: VersionReadExtent,
    ) -> ObjectStoreResult<Option<NewestVersion>> {
        for _ in 0..MAX_NEWEST_VERSION_LOOKUPS {
            let observed_at = Instant::now();
            let lower_bound = self
                .lower_bound(operation, logical_key, observed_at)
                .await?;
            let probed = self
                .probe_newer_generations(operation, logical_key, &lower_bound)
                .await?;
            // A slow probe may HEAD generations that reclamation removed after the lower bound.
            if !self.is_recent(lower_bound.observed_at) {
                continue;
            }
            let (generation, metadata) = match probed {
                ProbedGenerations::Absent => return Ok(None),
                ProbedGenerations::Newest(generation, metadata) => (generation, metadata),
                ProbedGenerations::LowerBoundGone => {
                    self.forget(logical_key);
                    continue;
                }
            };
            if lower_bound.is_listed && generation > lower_bound.generation {
                self.listing_stale
                    .record(VersionedListingStaleSite::NewestVersionLookup);
            }
            let physical_key = version_object_key(logical_key, generation);
            let content = match extent {
                VersionReadExtent::HeaderOnly => {
                    self.inner
                        .get_range(
                            &physical_key,
                            crate::aliyun_oss::version_body::VERSION_HEADER_RANGE,
                        )
                        .await
                }
                VersionReadExtent::WholeBody => self.inner.get(&physical_key).await,
            }
            .map_err(|error| inner_error(operation, logical_key, &error))?;
            // A lower bound that is gone came from a listing older than its deletion.
            let Some(content) = content.filter(|content| is_listed_object(&metadata, content))
            else {
                self.forget(logical_key);
                continue;
            };
            if !self.is_recent(lower_bound.observed_at) {
                continue;
            }
            let header = decode_header(operation, logical_key, &physical_key, &content)?;
            let payload = (extent == VersionReadExtent::WholeBody)
                .then(|| content.bytes.slice(VERSION_HEADER_LENGTH..));
            self.remember(
                logical_key,
                GenerationHint {
                    generation,
                    metadata: Some(metadata.clone()),
                    observed_at,
                },
            );
            return Ok(Some(NewestVersion {
                generation,
                header,
                payload,
                payload_size: content
                    .object_size
                    .saturating_sub(VERSION_HEADER_LENGTH as u64),
                last_modified_unix_micros: metadata.last_modified_unix_micros,
            }));
        }
        Err(ObjectStoreError::new(
            operation,
            logical_key,
            ObjectStoreErrorKind::Transient,
            "the newest version changed or its lower bound was gone during every read attempt",
        ))
    }

    /// Returns a generation that is at most the newest: a recent hint, or else the first entry
    /// of a one-key listing, or else zero.
    async fn lower_bound(
        &self,
        operation: ObjectStoreOperation,
        logical_key: &str,
        listed_at: Instant,
    ) -> ObjectStoreResult<LowerBound> {
        if let Some(hint) = self.recent_hint(logical_key) {
            return Ok(LowerBound {
                generation: hint.generation,
                metadata: hint.metadata,
                is_listed: false,
                observed_at: hint.observed_at,
            });
        }
        let prefix = version_prefix(logical_key);
        let listing = self
            .inner
            .list(&prefix, None, 1)
            .await
            .map_err(|error| inner_error(operation, logical_key, &error))?;
        let Some(listed) = listing.objects.into_iter().next() else {
            return Ok(LowerBound {
                generation: 0,
                metadata: None,
                is_listed: true,
                observed_at: listed_at,
            });
        };
        let generation = version_generation(&listed.key, &prefix)
            .ok_or_else(|| malformed_version_key(operation, logical_key, &listed.key))?;
        Ok(LowerBound {
            generation,
            metadata: Some(listed),
            is_listed: true,
            observed_at: listed_at,
        })
    }

    /// HEADs the generations after `lower_bound` until one is missing.
    ///
    /// Returns the last existing generation with its metadata, [`ProbedGenerations::Absent`]
    /// when the lower bound is zero and generation 1 is missing, and
    /// [`ProbedGenerations::LowerBoundGone`] when only the lower bound remains and it is missing.
    async fn probe_newer_generations(
        &self,
        operation: ObjectStoreOperation,
        logical_key: &str,
        lower_bound: &LowerBound,
    ) -> ObjectStoreResult<ProbedGenerations> {
        let mut newest = lower_bound.generation;
        let mut newest_metadata = lower_bound.metadata.clone();
        while let Some(next) = newest.checked_add(1) {
            let Some(metadata) = self.head_version(operation, logical_key, next).await? else {
                break;
            };
            newest = next;
            newest_metadata = Some(metadata);
        }
        if newest == 0 {
            return Ok(ProbedGenerations::Absent);
        }
        if let Some(metadata) = newest_metadata {
            return Ok(ProbedGenerations::Newest(newest, metadata));
        }
        // A hint from this process's create has no metadata yet.
        Ok(
            match self.head_version(operation, logical_key, newest).await? {
                Some(metadata) => ProbedGenerations::Newest(newest, metadata),
                None => ProbedGenerations::LowerBoundGone,
            },
        )
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

    /// Returns whether reclamation cannot yet remove a generation that was newest at `observed_at`.
    fn is_recent(&self, observed_at: Instant) -> bool {
        observed_at.elapsed() < self.hint_lifetime
    }

    fn recent_hint(&self, logical_key: &str) -> Option<GenerationHint> {
        let mut hints = self
            .hints
            .lock()
            .expect("generation hints are not poisoned");
        let hint = hints.by_key.get(logical_key)?.clone();
        if self.is_recent(hint.observed_at) {
            return Some(hint);
        }
        hints.by_key.remove(logical_key);
        None
    }

    fn remember(&self, logical_key: &str, hint: GenerationHint) {
        let mut hints = self
            .hints
            .lock()
            .expect("generation hints are not poisoned");
        let by_key = &mut hints.by_key;
        // Generations only grow, so a lower one comes from an older observation.
        if by_key
            .get(logical_key)
            .is_some_and(|known| known.generation > hint.generation)
        {
            return;
        }
        if by_key.len() >= MAX_GENERATION_HINTS && !by_key.contains_key(logical_key) {
            let oldest_key = by_key
                .iter()
                .min_by_key(|(_, known)| known.observed_at)
                .map(|(key, _)| key.clone());
            if let Some(oldest_key) = oldest_key {
                by_key.remove(&oldest_key);
            }
        }
        by_key.insert(logical_key.to_owned(), hint);
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
