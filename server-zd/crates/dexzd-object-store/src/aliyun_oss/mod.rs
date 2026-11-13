// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Linearizable ETag replacement of mutable keys on stores with only create-only writes.

mod bucket_checks;
mod reclamation_schedule;
mod startup_probes;
mod version_body;
mod version_keys;
mod version_listing;
mod version_reads;
mod version_reclamation;
mod version_writes;

use std::path::Path;
use std::sync::Arc;
use std::time::Duration;

use async_trait::async_trait;
use bytes::Bytes;
use dexzd_metrics::{NodeMetrics, VersionedListingStaleCounters};

use crate::file_download::TemporaryDownloadFile;
use crate::{
    ByteRange, ObjectContent, ObjectListing, ObjectMetadata, ObjectStore, ObjectStoreError,
    ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult, STARTUP_PROBE_OBJECT_KEY_PREFIX,
    WriteCondition, WriteOutcome,
};
pub use bucket_checks::{
    AliyunOssBucketCheckError, check_aliyun_oss_create_only_writes,
    check_no_data_of_another_provider,
};
pub use startup_probes::STALE_STARTUP_PROBE_AGE;
use startup_probes::{purge_startup_probe, read_store_time, remove_startup_probes_written_before};
pub use version_keys::VERSION_OBJECT_KEY_SUFFIX_BYTES;
use version_keys::{LogicalKeyLayout, logical_key_layout};
use version_listing::list_logical_keys;
use version_reads::{get_versioned, get_versioned_range, head_versioned};
pub use version_reclamation::MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION;
use version_reclamation::VersionReclaimer;
use version_writes::VersionedObjectWriter;

/// Chooses the ID of each new version.
pub(crate) trait VersionIdSource: Send + Sync {
    /// Returns a new ID, distinct from every ID returned before.
    fn next_version_id(&self) -> u128;
}

/// Draws 128-bit version IDs from a cryptographically secure generator.
struct RandomVersionIdSource;

impl VersionIdSource for RandomVersionIdSource {
    fn next_version_id(&self) -> u128 {
        rand::random()
    }
}

/// Numbers version IDs for simulations: the seed in the high 64 bits, a counter below.
#[cfg(feature = "test-util")]
struct SeededVersionIdSourceForTest {
    seed: u64,
    issued_ids: std::sync::atomic::AtomicU64,
}

#[cfg(feature = "test-util")]
impl VersionIdSource for SeededVersionIdSourceForTest {
    fn next_version_id(&self) -> u128 {
        let sequence = self
            .issued_ids
            .fetch_add(1, std::sync::atomic::Ordering::SeqCst);
        (u128::from(self.seed) << 64) | u128::from(sequence)
    }
}

/// An [`ObjectStore`] for a store whose only conditional write is create-only, such as
/// Aliyun OSS with `x-oss-forbid-overwrite`.
///
/// Mutable object keys (see [`crate::is_mutable_object_key`]) get linearizable
/// [`WriteCondition::CreateOnly`] and [`WriteCondition::ReplaceIfEtagMatches`] writes. Each
/// logical key `K` is stored as create-only versions at `K.v/<u64::MAX - generation>`, so a
/// one-key listing finds the newest version. A read returns only the object that listing named,
/// by its physical ETag. A replacement requires the ETag to name the newest version, creates
/// the next generation, and confirms it, so one writer wins each generation. Each version body
/// starts with a header that names the version and its predecessor; ETags have the form
/// `"g<generation>-<version ID>"`. A delete writes a tombstone version, except on startup probe
/// keys, whose versions are deleted. A background task deletes superseded versions, see
/// [`MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION`].
///
/// Every other key is stored at the same physical key and passed to the inner store. Such a
/// key rejects [`WriteCondition::ReplaceIfEtagMatches`] with
/// [`ObjectStoreErrorKind::Permanent`], and
/// [`ObjectStore::supports_replace_if_etag_matches_on_every_key`] returns `false`. Keys that
/// name a version object, such as `manifest.v/...`, are rejected as well.
///
/// The physical layout differs from every other provider, so data cannot move between them.
///
/// ```no_run
/// # async fn example(inner: std::sync::Arc<dyn dexzd_object_store::ObjectStore>) {
/// use bytes::Bytes;
/// use dexzd_metrics::NodeMetrics;
/// use dexzd_object_store::{
///     AliyunOssObjectStore, MANIFEST_OBJECT_KEY, MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION,
///     ObjectStore, WriteCondition, WriteOutcome,
/// };
///
/// let store = AliyunOssObjectStore::new(
///     inner,
///     MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION,
///     NodeMetrics::unregistered(),
/// );
/// let created = store
///     .put(MANIFEST_OBJECT_KEY, Bytes::from_static(b"v1"), WriteCondition::CreateOnly)
///     .await
///     .expect("the store is reachable");
/// let WriteOutcome::Written { etag } = created else {
///     return;
/// };
/// let replaced = store
///     .put(
///         MANIFEST_OBJECT_KEY,
///         Bytes::from_static(b"v2"),
///         WriteCondition::ReplaceIfEtagMatches { etag },
///     )
///     .await
///     .expect("the store is reachable");
/// assert!(matches!(replaced, WriteOutcome::Written { .. }));
/// # }
/// ```
pub struct AliyunOssObjectStore {
    inner: Arc<dyn ObjectStore>,
    version_ids: Box<dyn VersionIdSource>,
    reclaimer: VersionReclaimer,
    listing_stale: VersionedListingStaleCounters,
}

impl AliyunOssObjectStore {
    /// Wraps `inner`, which must honor [`WriteCondition::CreateOnly`] atomically and list
    /// keys consistently right after each write.
    ///
    /// The store never sends [`WriteCondition::ReplaceIfEtagMatches`] to `inner`. It starts its
    /// version reclamation task, which stops when the store is dropped.
    ///
    /// Reclamation deletes a version only after it was superseded for
    /// `minimum_superseded_age_before_reclamation`; production passes
    /// [`MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION`]. `metrics` counts listings that miss a
    /// version this store just created.
    ///
    /// # Panics
    ///
    /// Panics when called outside a Tokio runtime, or when the minimum age is zero.
    pub fn new(
        inner: Arc<dyn ObjectStore>,
        minimum_superseded_age_before_reclamation: Duration,
        metrics: NodeMetrics,
    ) -> Self {
        Self::with_version_id_source(
            inner,
            Box::new(RandomVersionIdSource),
            minimum_superseded_age_before_reclamation,
            metrics,
        )
    }

    /// Wraps `inner` like [`Self::new`], deriving version IDs from `seed` instead of a random
    /// generator, so a deterministic simulation writes the same bodies on every run.
    ///
    /// Stores that share an inner store must use different seeds; IDs of different seeds
    /// never collide.
    ///
    /// # Panics
    ///
    /// Panics when called outside a Tokio runtime, or when the minimum age is zero.
    #[cfg(feature = "test-util")]
    pub fn with_seeded_version_ids_for_test(
        inner: Arc<dyn ObjectStore>,
        seed: u64,
        minimum_superseded_age_before_reclamation: Duration,
        metrics: NodeMetrics,
    ) -> Self {
        Self::with_version_id_source(
            inner,
            Box::new(SeededVersionIdSourceForTest {
                seed,
                issued_ids: std::sync::atomic::AtomicU64::new(0),
            }),
            minimum_superseded_age_before_reclamation,
            metrics,
        )
    }

    fn with_version_id_source(
        inner: Arc<dyn ObjectStore>,
        version_ids: Box<dyn VersionIdSource>,
        minimum_superseded_age_before_reclamation: Duration,
        metrics: NodeMetrics,
    ) -> Self {
        assert!(
            !minimum_superseded_age_before_reclamation.is_zero(),
            "reclaiming a version as soon as it is superseded is unsafe"
        );
        let listing_stale = metrics.object_store_versioned_listing_stale;
        Self {
            reclaimer: VersionReclaimer::start(
                Arc::clone(&inner),
                minimum_superseded_age_before_reclamation,
                listing_stale.clone(),
            ),
            inner,
            version_ids,
            listing_stale,
        }
    }

    /// Deletes every startup probe key whose newest object was written more than
    /// [`STALE_STARTUP_PROBE_AGE`] ago, by the store's clock.
    ///
    /// Such keys remain from processes that stopped during [`crate::startup_self_check`]. A
    /// running check holds its probe for at most [`crate::STARTUP_PROBE_MAXIMUM_LIFETIME`]. A
    /// process suspended in the middle of a check, for example by host sleep or `SIGSTOP`, can
    /// hold it longer. When the suspension outlasts [`STALE_STARTUP_PROBE_AGE`], the sweep may
    /// remove the probe, and the resumed check then fails or passes on a new probe; restart the
    /// server after such a failure. Probe keys are separate from leases and the manifest, so
    /// removing one never affects fencing. Only keys under `probe/` are ever deleted.
    ///
    /// The current time is the last-write time of a clock probe object this call writes and
    /// deletes, so the host clock does not matter. When another sweep removes that clock probe
    /// first, this sweep removes nothing and returns `0`; a clock probe left behind is removed
    /// once it is stale. Call it before the check. Returns the number of probe keys removed.
    /// Objects whose store reports no last-write time are kept.
    ///
    /// # Errors
    ///
    /// Returns the inner store's error when a write, listing, or delete fails; earlier deletes
    /// stay. Returns [`ObjectStoreErrorKind::Permanent`] when the store reports no last-write
    /// time for the new probe object.
    pub async fn remove_stale_startup_probes(&self) -> ObjectStoreResult<u64> {
        let clock_key = format!(
            "{STARTUP_PROBE_OBJECT_KEY_PREFIX}store-clock-{:032x}",
            self.version_ids.next_version_id()
        );
        let Some(store_now) = read_store_time(self, &clock_key).await? else {
            tracing::warn!(
                key = %clock_key,
                "another sweep removed this sweep's store clock probe; skipping this sweep"
            );
            return Ok(0);
        };
        let stale_age_micros = i64::try_from(STALE_STARTUP_PROBE_AGE.as_micros())
            .expect("the stale probe age fits in i64 microseconds");
        remove_startup_probes_written_before(
            self.inner.as_ref(),
            store_now.saturating_sub(stale_age_micros),
        )
        .await
    }

    fn writer(&self) -> VersionedObjectWriter<'_> {
        VersionedObjectWriter {
            inner: self.inner.as_ref(),
            version_ids: self.version_ids.as_ref(),
            reclaimer: &self.reclaimer,
            listing_stale: &self.listing_stale,
        }
    }
}

#[async_trait]
impl ObjectStore for AliyunOssObjectStore {
    async fn put(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        match logical_key_layout(ObjectStoreOperation::Put, key)? {
            LogicalKeyLayout::Versioned => self.writer().put(key, body, condition).await,
            LogicalKeyLayout::Plain => {
                reject_plain_replacement(key, &condition)?;
                self.inner.put(key, body, condition).await
            }
        }
    }

    async fn put_file(
        &self,
        key: &str,
        path: &Path,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        match logical_key_layout(ObjectStoreOperation::Put, key)? {
            LogicalKeyLayout::Versioned => {
                let body = tokio::fs::read(path).await.map_err(|error| {
                    ObjectStoreError::new(
                        ObjectStoreOperation::Put,
                        key,
                        ObjectStoreErrorKind::Permanent,
                        format!("reading {}: {error}", path.display()),
                    )
                })?;
                self.writer().put(key, Bytes::from(body), condition).await
            }
            LogicalKeyLayout::Plain => {
                reject_plain_replacement(key, &condition)?;
                self.inner.put_file(key, path, condition).await
            }
        }
    }

    async fn get(&self, key: &str) -> ObjectStoreResult<Option<ObjectContent>> {
        match logical_key_layout(ObjectStoreOperation::Get, key)? {
            LogicalKeyLayout::Versioned => get_versioned(self.inner.as_ref(), key).await,
            LogicalKeyLayout::Plain => self.inner.get(key).await,
        }
    }

    async fn get_range(
        &self,
        key: &str,
        range: ByteRange,
    ) -> ObjectStoreResult<Option<ObjectContent>> {
        match logical_key_layout(ObjectStoreOperation::Get, key)? {
            LogicalKeyLayout::Versioned => {
                get_versioned_range(self.inner.as_ref(), key, range).await
            }
            LogicalKeyLayout::Plain => self.inner.get_range(key, range).await,
        }
    }

    async fn get_to_file(&self, key: &str, path: &Path) -> ObjectStoreResult<Option<u64>> {
        match logical_key_layout(ObjectStoreOperation::Get, key)? {
            LogicalKeyLayout::Versioned => {
                let Some(content) = get_versioned(self.inner.as_ref(), key).await? else {
                    return Ok(None);
                };
                let mut download = TemporaryDownloadFile::create(key, path).await?;
                download.write_all(&content.bytes).await?;
                download.commit().await?;
                Ok(Some(content.object_size))
            }
            LogicalKeyLayout::Plain => self.inner.get_to_file(key, path).await,
        }
    }

    async fn head(&self, key: &str) -> ObjectStoreResult<Option<ObjectMetadata>> {
        match logical_key_layout(ObjectStoreOperation::Head, key)? {
            LogicalKeyLayout::Versioned => {
                head_versioned(self.inner.as_ref(), ObjectStoreOperation::Head, key).await
            }
            LogicalKeyLayout::Plain => self.inner.head(key).await,
        }
    }

    async fn delete(&self, key: &str) -> ObjectStoreResult<()> {
        match logical_key_layout(ObjectStoreOperation::Delete, key)? {
            LogicalKeyLayout::Versioned if key.starts_with(STARTUP_PROBE_OBJECT_KEY_PREFIX) => {
                purge_startup_probe(self.inner.as_ref(), key).await
            }
            LogicalKeyLayout::Versioned => self.writer().delete(key).await,
            LogicalKeyLayout::Plain => self.inner.delete(key).await,
        }
    }

    async fn delete_many(&self, keys: &[String]) -> ObjectStoreResult<()> {
        if keys.len() > 1000 {
            return Err(ObjectStoreError::new(
                ObjectStoreOperation::Delete,
                "",
                ObjectStoreErrorKind::Permanent,
                "delete_many accepts at most 1000 keys",
            ));
        }
        let mut plain_keys = Vec::new();
        let mut versioned_keys = Vec::new();
        for key in keys {
            match logical_key_layout(ObjectStoreOperation::Delete, key)? {
                LogicalKeyLayout::Plain => plain_keys.push(key.clone()),
                LogicalKeyLayout::Versioned => versioned_keys.push(key),
            }
        }
        if !plain_keys.is_empty() {
            self.inner.delete_many(&plain_keys).await?;
        }
        for key in versioned_keys {
            self.delete(key).await?;
        }
        Ok(())
    }

    async fn list(
        &self,
        prefix: &str,
        start_after: Option<&str>,
        max_keys: u32,
    ) -> ObjectStoreResult<ObjectListing> {
        list_logical_keys(self.inner.as_ref(), prefix, start_after, max_keys).await
    }

    fn supports_replace_if_etag_matches_on_every_key(&self) -> bool {
        false
    }
}

/// Rejects [`WriteCondition::ReplaceIfEtagMatches`] on a key that is not a mutable object key.
fn reject_plain_replacement(key: &str, condition: &WriteCondition) -> ObjectStoreResult<()> {
    if matches!(condition, WriteCondition::ReplaceIfEtagMatches { .. }) {
        return Err(ObjectStoreError::new(
            ObjectStoreOperation::Put,
            key,
            ObjectStoreErrorKind::Permanent,
            "ReplaceIfEtagMatches works only on mutable object keys; register the key in \
             dexzd-object-store before replacing it",
        ));
    }
    Ok(())
}
