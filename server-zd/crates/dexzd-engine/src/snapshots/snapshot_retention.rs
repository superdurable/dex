// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;
use std::time::Duration;

use dexzd_object_store::{ListingConsistency, ObjectMetadata, ObjectStore};
use dexzd_protocol::dexzd::v1::SnapshotManifest;
use prost::Message;
use tokio::sync::Mutex;
use tokio::time::Instant;
use uuid::Uuid;

use crate::snapshots::SnapshotError;
use crate::snapshots::ownership_fence::OwnershipFence;

use eventual_protection::EventualProtection;

mod eventual_protection;

const LISTING_PAGE_SIZE: u32 = 1000;
const LISTING_PAGES_PER_RUN: usize = 10;
const CANDIDATES_PER_RUN: usize = 64;

/// After this, an eventually consistent refresh starts no new page and resumes in the next run.
const EVENTUAL_LISTING_DURATION_PER_RUN: Duration = Duration::from_secs(10);

/// Manifest GETs and HEADs of one eventually consistent run; later runs read the rest first.
const EVENTUAL_MANIFEST_READS_PER_RUN: usize = 256;

/// Keeps the newest snapshots of this node across every lineage and deletes the rest.
pub(crate) struct SnapshotRetention {
    pub(crate) object_store: Arc<dyn ObjectStore>,
    pub(crate) node_id: String,
    pub(crate) retained_count: usize,
    pub(crate) deletion_delay: Duration,
    /// When eventual, a manifest leaves the catalog only once a HEAD misses it.
    pub(crate) listing_consistency: ListingConsistency,
}

/// Decoded snapshot manifests of this node by object key.
///
/// Manifests never change, so each one is read at most once per process.
#[derive(Default)]
pub(crate) struct SnapshotCatalog {
    pub(crate) operation_lock: Arc<Mutex<()>>,
    pub(crate) has_prepared_snapshot_lineage: bool,
    manifests: BTreeMap<String, SnapshotManifest>,
    listing_start_after: Option<String>,
    listed_manifest_keys: BTreeSet<String>,
    listed_files: BTreeMap<String, ObjectMetadata>,
    inserted_manifest_keys: BTreeSet<String>,
    orphan_first_seen: BTreeMap<String, OrphanMark>,
    completed_scan_number: u64,
    candidate_cursor: Option<String>,
    /// What retention keeps when listings are eventually consistent; strong listings leave it empty.
    protection: EventualProtection,
    /// Listed manifests an eventual run has not read yet; no deletion happens until it is empty.
    unread_listed_manifest_keys: BTreeSet<String>,
    /// Known manifests a completed eventual listing lacked and no HEAD checked yet.
    unchecked_unlisted_manifest_keys: BTreeSet<String>,
}

/// How many more manifest GETs and HEADs one eventual retention run may send.
struct ManifestReadBudget {
    remaining_reads: usize,
}

impl ManifestReadBudget {
    /// Returns whether one more read fits, and counts it when it does.
    fn try_spend_read(&mut self) -> bool {
        let has_remaining_read = self.remaining_reads > 0;
        self.remaining_reads = self.remaining_reads.saturating_sub(1);
        has_remaining_read
    }
}

struct OrphanMark {
    etag: String,
    first_seen_unix_micros: i64,
    scan_number: u64,
}

impl SnapshotCatalog {
    /// Adds a manifest that this process just wrote.
    pub(crate) fn insert(&mut self, key: String, manifest: SnapshotManifest) {
        self.inserted_manifest_keys.insert(key.clone());
        self.manifests.insert(key, manifest);
    }

    /// Adds a manifest that this process just wrote and counts it among its own snapshots.
    pub(crate) fn insert_own(&mut self, key: String, manifest: SnapshotManifest) {
        self.protection.record_own_manifest(key.clone());
        self.insert(key, manifest);
    }

    /// Records a manifest write whose upload failed; the files it lists stay until it is resolved.
    pub(crate) fn record_failed_manifest_upload(
        &mut self,
        key: String,
        manifest: &SnapshotManifest,
        attempt_ended_at: Instant,
    ) {
        self.protection
            .record_unresolved_upload(key, manifest, attempt_ended_at);
    }

    /// Records manifests that a strong read showed pointed, such as pointers a snapshot replaced.
    pub(crate) fn record_pointed_manifests(&mut self, keys: impl IntoIterator<Item = String>) {
        self.protection.record_pointed_manifests(keys);
    }
}

impl SnapshotRetention {
    /// Deletes snapshots beyond the newest `retained_count` of every lineage combined, once
    /// the snapshot that displaced each one is older than the deletion delay.
    ///
    /// Snapshots in `referenced_keys` are always kept, and so is every file a kept snapshot
    /// references. A deleted snapshot's files go in the same pass, independent of process
    /// lifetime. Over an eventually consistent listing, [`Self::run_eventual_once`] decides
    /// instead. Returns the kept snapshots, oldest first.
    pub(crate) async fn run_once<'catalog>(
        &self,
        catalog: &'catalog mut SnapshotCatalog,
        referenced_keys: &BTreeSet<String>,
        fence: &OwnershipFence,
        now_unix_micros: i64,
    ) -> Result<Vec<&'catalog SnapshotManifest>, SnapshotError> {
        if !self.listing_consistency.is_strong() {
            return self
                .run_eventual_once(catalog, referenced_keys, fence)
                .await;
        }
        if !self.list_snapshot_objects(catalog).await? {
            return Ok(Self::kept(catalog));
        }
        self.reconcile_strong_listing(catalog).await?;
        let mut unreferenced_files: BTreeSet<String> = self
            .find_orphan_files(catalog, now_unix_micros)
            .await?
            .into_iter()
            .collect();
        let obsolete_manifests: Vec<String> = self
            .find_obsolete(catalog, referenced_keys, now_unix_micros)
            .into_iter()
            .take(CANDIDATES_PER_RUN)
            .collect();
        catalog.listed_files.clear();
        if !obsolete_manifests.is_empty() {
            // Files follow the manifests, so retries find them through the listing after partial manifest deletion.
            for chunk in obsolete_manifests.chunks(LISTING_PAGE_SIZE as usize) {
                fence.ensure_current_lease().await?;
                self.object_store.delete_many(chunk).await?;
            }
            for key in &obsolete_manifests {
                if let Some(manifest) = catalog.manifests.remove(key) {
                    unreferenced_files
                        .extend(manifest.files.into_iter().map(|file| file.object_key));
                }
            }
            tracing::info!(
                manifests = obsolete_manifests.len(),
                "deleted snapshots beyond the retained count"
            );
        }
        let orphan_files = self
            .exclude_newly_referenced_files(catalog, unreferenced_files.into_iter().collect())
            .await?;
        for chunk in orphan_files.chunks(LISTING_PAGE_SIZE as usize) {
            fence.ensure_current_lease().await?;
            // An issued delete can finish after lease loss; successors upload into fresh lineages.
            self.object_store.delete_many(chunk).await?;
        }
        if !orphan_files.is_empty() {
            tracing::info!(
                files = orphan_files.len(),
                "deleted unreferenced snapshot files"
            );
        }
        Ok(Self::kept(catalog))
    }

    /// Deletes listed objects that stayed unprotected for the deletion delay on the monotonic clock.
    ///
    /// A run sends at most [`EVENTUAL_MANIFEST_READS_PER_RUN`] manifest GETs and HEADs. A run
    /// that leaves a read for a later run starts no timer and deletes nothing, so every object
    /// stays until the manifests that may list it are read. Returns the kept snapshots, oldest
    /// first.
    async fn run_eventual_once<'catalog>(
        &self,
        catalog: &'catalog mut SnapshotCatalog,
        referenced_keys: &BTreeSet<String>,
        fence: &OwnershipFence,
    ) -> Result<Vec<&'catalog SnapshotManifest>, SnapshotError> {
        let node_prefix = format!("snapshot/{}/", self.node_id);
        let pointed_keys: BTreeSet<&str> = referenced_keys
            .iter()
            .filter(|key| key.starts_with(&node_prefix))
            .map(String::as_str)
            .collect();
        catalog.protection.observe_pointed_manifests(
            &pointed_keys,
            Instant::now(),
            self.deletion_delay,
        );
        let has_queued_reads = !catalog.unread_listed_manifest_keys.is_empty()
            || !catalog.unchecked_unlisted_manifest_keys.is_empty();
        if !has_queued_reads {
            if !self.list_snapshot_objects(catalog).await? {
                return Ok(Self::kept(catalog));
            }
            Self::queue_eventual_manifest_reads(catalog);
        }
        let mut budget = ManifestReadBudget {
            remaining_reads: EVENTUAL_MANIFEST_READS_PER_RUN,
        };
        let has_read_protected = self.read_protected_manifests(catalog, &mut budget).await?;
        if !has_read_protected || !self.read_queued_manifests(catalog, &mut budget).await? {
            tracing::debug!(
                unread_manifests = catalog.unread_listed_manifest_keys.len(),
                unchecked_manifests = catalog.unchecked_unlisted_manifest_keys.len(),
                "snapshot retention continues manifest reads in the next run"
            );
            return Ok(Self::kept(catalog));
        }
        let now = Instant::now();
        let (protected_manifests, protected_files) = catalog
            .protection
            .protected_objects(&catalog.manifests, self.retained_count);
        let obsolete_manifests: Vec<String> = catalog
            .protection
            .matured_manifests(
                catalog.manifests.keys(),
                &protected_manifests,
                now,
                self.deletion_delay,
            )
            .into_iter()
            .take(CANDIDATES_PER_RUN)
            .collect();
        for chunk in obsolete_manifests.chunks(LISTING_PAGE_SIZE as usize) {
            fence.ensure_current_lease().await?;
            self.object_store.delete_many(chunk).await?;
        }
        for key in &obsolete_manifests {
            catalog.manifests.remove(key);
            catalog.protection.forget_deleted_manifest(key);
        }
        if !obsolete_manifests.is_empty() {
            tracing::info!(
                manifests = obsolete_manifests.len(),
                "deleted unprotected snapshots"
            );
        }
        catalog
            .protection
            .nominate_files(&catalog.listed_files, &protected_files, now);
        catalog.listed_files.clear();
        self.delete_matured_files(catalog, fence, now).await?;
        Ok(Self::kept(catalog))
    }

    fn kept(catalog: &SnapshotCatalog) -> Vec<&SnapshotManifest> {
        let mut kept: Vec<&SnapshotManifest> = catalog.manifests.values().collect();
        kept.sort_by(|left, right| creation_order(left).cmp(&creation_order(right)));
        kept
    }

    async fn exclude_newly_referenced_files(
        &self,
        catalog: &mut SnapshotCatalog,
        orphan_files: Vec<String>,
    ) -> Result<Vec<String>, SnapshotError> {
        let lineage_prefixes: BTreeSet<&str> = orphan_files
            .iter()
            .filter_map(|key| key.split_once("/files/").map(|(prefix, _)| prefix))
            .collect();
        let mut incomplete_lineages = BTreeSet::new();
        for lineage_prefix in lineage_prefixes {
            let manifest_prefix = format!("{lineage_prefix}/manifest/");
            let listing = self
                .object_store
                .list(&manifest_prefix, None, LISTING_PAGE_SIZE)
                .await?;
            if listing.next_start_after.is_some() {
                incomplete_lineages.insert(lineage_prefix.to_owned());
                continue;
            }
            for manifest_object in listing.objects {
                if catalog.manifests.contains_key(&manifest_object.key) {
                    continue;
                }
                let Some(content) = self.object_store.get(&manifest_object.key).await? else {
                    continue;
                };
                let manifest = SnapshotManifest::decode(content.bytes)?;
                self.validate(&manifest_object.key, &manifest)?;
                catalog.manifests.insert(manifest_object.key, manifest);
            }
        }
        let referenced_files: BTreeSet<&str> = catalog
            .manifests
            .values()
            .flat_map(|manifest| &manifest.files)
            .map(|file| file.object_key.as_str())
            .collect();
        Ok(orphan_files
            .into_iter()
            .filter(|key| {
                let lineage_prefix = key
                    .split_once("/files/")
                    .expect("listed snapshot file has a files segment")
                    .0;
                !incomplete_lineages.contains(lineage_prefix)
                    && !referenced_files.contains(key.as_str())
            })
            .collect())
    }

    async fn find_orphan_files(
        &self,
        catalog: &mut SnapshotCatalog,
        now_unix_micros: i64,
    ) -> Result<Vec<String>, SnapshotError> {
        let referenced_files: BTreeSet<&str> = catalog
            .manifests
            .values()
            .flat_map(|manifest| &manifest.files)
            .map(|file| file.object_key.as_str())
            .collect();
        catalog.orphan_first_seen.retain(|key, _| {
            catalog.listed_files.contains_key(key) && !referenced_files.contains(key.as_str())
        });
        let delay_micros = i64::try_from(self.deletion_delay.as_micros()).unwrap_or(i64::MAX);
        let mut orphan_files = Vec::new();
        let mut checked_candidates = 0;
        let mut last_candidate = None;
        for (key, listed_file) in &catalog.listed_files {
            if referenced_files.contains(key.as_str()) {
                continue;
            }
            let first_seen = catalog
                .orphan_first_seen
                .entry(key.clone())
                .or_insert_with(|| OrphanMark {
                    etag: listed_file.etag.clone(),
                    first_seen_unix_micros: now_unix_micros,
                    scan_number: catalog.completed_scan_number,
                });
            if first_seen.etag != listed_file.etag {
                *first_seen = OrphanMark {
                    etag: listed_file.etag.clone(),
                    first_seen_unix_micros: now_unix_micros,
                    scan_number: catalog.completed_scan_number,
                };
            }
            if first_seen.scan_number == catalog.completed_scan_number
                || now_unix_micros.saturating_sub(first_seen.first_seen_unix_micros) < delay_micros
            {
                continue;
            }
            let written_at = listed_file
                .last_modified_unix_micros
                .unwrap_or(first_seen.first_seen_unix_micros);
            if now_unix_micros.saturating_sub(written_at) < delay_micros {
                continue;
            }
            if catalog
                .candidate_cursor
                .as_ref()
                .is_some_and(|cursor| key <= cursor)
            {
                continue;
            }
            if checked_candidates == CANDIDATES_PER_RUN {
                break;
            }
            checked_candidates += 1;
            last_candidate = Some(key.clone());
            let Some(current_file) = self.object_store.head(key).await? else {
                continue;
            };
            if current_file.last_modified_unix_micros.is_none() {
                *first_seen = OrphanMark {
                    etag: current_file.etag,
                    first_seen_unix_micros: now_unix_micros,
                    scan_number: catalog.completed_scan_number,
                };
                continue;
            }
            let current_written_at = current_file
                .last_modified_unix_micros
                .expect("checked above");
            if current_file.etag == listed_file.etag
                && now_unix_micros.saturating_sub(current_written_at) >= delay_micros
            {
                orphan_files.push(key.clone());
            }
        }
        catalog.candidate_cursor = last_candidate;
        Ok(orphan_files)
    }

    /// Returns manifest keys to delete; files that only they reference are deleted after them.
    fn find_obsolete(
        &self,
        catalog: &SnapshotCatalog,
        referenced_keys: &BTreeSet<String>,
        now_unix_micros: i64,
    ) -> Vec<String> {
        let mut snapshots: Vec<(&String, &SnapshotManifest)> = catalog.manifests.iter().collect();
        snapshots.sort_by(|left, right| creation_order(left.1).cmp(&creation_order(right.1)));
        let delay_micros = i64::try_from(self.deletion_delay.as_micros()).unwrap_or(i64::MAX);
        let displaced_count = snapshots.len().saturating_sub(self.retained_count);
        let is_removable: Vec<bool> = snapshots
            .iter()
            .enumerate()
            .map(|(index, (key, _))| {
                index < displaced_count
                    && !referenced_keys.contains(*key)
                    && now_unix_micros.saturating_sub(
                        snapshots[index + self.retained_count]
                            .1
                            .created_at_unix_micros,
                    ) >= delay_micros
            })
            .collect();
        let removed: Vec<(&String, &SnapshotManifest)> = snapshots
            .iter()
            .zip(&is_removable)
            .filter(|(_, is_removable)| **is_removable)
            .map(|(snapshot, _)| *snapshot)
            .collect();
        removed.iter().map(|(key, _)| (*key).clone()).collect()
    }

    /// Lists this node's snapshot objects, resuming the previous run's listing.
    ///
    /// Returns whether the listing reached its end; its keys then wait in the catalog.
    async fn list_snapshot_objects(
        &self,
        catalog: &mut SnapshotCatalog,
    ) -> Result<bool, SnapshotError> {
        let prefix = format!("snapshot/{}/", self.node_id);
        let listing_deadline = tokio::time::Instant::now() + EVENTUAL_LISTING_DURATION_PER_RUN;
        for page_index in 0..LISTING_PAGES_PER_RUN {
            // An eventually consistent page can wait out replays, so a run bounds its pages by time.
            if page_index > 0
                && !self.listing_consistency.is_strong()
                && tokio::time::Instant::now() >= listing_deadline
            {
                break;
            }
            let page = self
                .object_store
                .list(
                    &prefix,
                    catalog.listing_start_after.as_deref(),
                    LISTING_PAGE_SIZE,
                )
                .await?;
            for object in page.objects {
                match object.key.split('/').nth(3) {
                    Some("manifest") => {
                        catalog.listed_manifest_keys.insert(object.key);
                    }
                    Some("files") => {
                        catalog.listed_files.insert(object.key.clone(), object);
                    }
                    _ => {}
                }
            }
            match page.next_start_after {
                Some(next) => catalog.listing_start_after = Some(next),
                None => {
                    catalog.listing_start_after = None;
                    break;
                }
            }
        }
        if catalog.listing_start_after.is_some() {
            return Ok(false);
        }
        catalog.completed_scan_number = catalog.completed_scan_number.saturating_add(1);
        Ok(true)
    }

    /// Drops manifests a completed strong listing lacks and reads the ones the catalog has not seen.
    async fn reconcile_strong_listing(
        &self,
        catalog: &mut SnapshotCatalog,
    ) -> Result<(), SnapshotError> {
        let unlisted_manifest_keys: Vec<String> =
            Self::unlisted_manifest_keys(catalog).cloned().collect();
        for key in unlisted_manifest_keys {
            catalog.manifests.remove(&key);
        }
        let listed_keys = std::mem::take(&mut catalog.listed_manifest_keys);
        for key in listed_keys {
            if catalog.manifests.contains_key(&key) {
                continue;
            }
            // A concurrent cleanup may have deleted it after the listing.
            let Some(content) = self.object_store.get(&key).await? else {
                continue;
            };
            let manifest = SnapshotManifest::decode(content.bytes)?;
            self.validate(&key, &manifest)?;
            catalog.manifests.insert(key, manifest);
        }
        catalog.inserted_manifest_keys.clear();
        Ok(())
    }

    /// Queues the manifest reads a completed eventual listing needs, so later runs can finish them.
    fn queue_eventual_manifest_reads(catalog: &mut SnapshotCatalog) {
        // A stale listing can lack a manifest that still exists; files it uses must stay.
        catalog.unchecked_unlisted_manifest_keys =
            Self::unlisted_manifest_keys(catalog).cloned().collect();
        let listed_keys = std::mem::take(&mut catalog.listed_manifest_keys);
        catalog.unread_listed_manifest_keys = listed_keys
            .into_iter()
            .filter(|key| !catalog.manifests.contains_key(key))
            .collect();
        catalog.inserted_manifest_keys.clear();
    }

    /// Returns known manifests the completed listing lacks that this process did not just write.
    fn unlisted_manifest_keys(catalog: &SnapshotCatalog) -> impl Iterator<Item = &String> {
        catalog.manifests.keys().filter(|key| {
            !catalog.listed_manifest_keys.contains(*key)
                && !catalog.inserted_manifest_keys.contains(*key)
        })
    }

    /// HEADs queued unlisted manifests, then reads queued listed ones, within `budget`.
    ///
    /// Returns whether no queued read remains.
    async fn read_queued_manifests(
        &self,
        catalog: &mut SnapshotCatalog,
        budget: &mut ManifestReadBudget,
    ) -> Result<bool, SnapshotError> {
        // A key leaves its queue only after its read succeeds, so a failed read is retried.
        while let Some(key) = catalog.unchecked_unlisted_manifest_keys.first().cloned() {
            if catalog.manifests.contains_key(&key) {
                if !budget.try_spend_read() {
                    return Ok(false);
                }
                if self.object_store.head(&key).await?.is_none() {
                    catalog.manifests.remove(&key);
                }
            }
            catalog.unchecked_unlisted_manifest_keys.remove(&key);
        }
        while let Some(key) = catalog.unread_listed_manifest_keys.first().cloned() {
            if !catalog.manifests.contains_key(&key) {
                if !budget.try_spend_read() {
                    return Ok(false);
                }
                // A concurrent cleanup may have deleted it after the listing.
                if let Some(manifest) = self.read_manifest(&key).await? {
                    catalog.manifests.insert(key.clone(), manifest);
                }
            }
            catalog.unread_listed_manifest_keys.remove(&key);
        }
        Ok(true)
    }

    /// Reads pointed manifests and unresolved manifest writes that the catalog lacks, as listings can hide them.
    ///
    /// Returns whether every such read fit in `budget`.
    async fn read_protected_manifests(
        &self,
        catalog: &mut SnapshotCatalog,
        budget: &mut ManifestReadBudget,
    ) -> Result<bool, SnapshotError> {
        let node_prefix = format!("snapshot/{}/", self.node_id);
        let pointed_keys: Vec<String> = catalog
            .protection
            .pointed_manifest_keys()
            .filter(|key| key.starts_with(&node_prefix) && !catalog.manifests.contains_key(*key))
            .cloned()
            .collect();
        for key in pointed_keys {
            if !budget.try_spend_read() {
                return Ok(false);
            }
            if let Some(manifest) = self.read_manifest(&key).await? {
                catalog.manifests.insert(key, manifest);
            }
        }
        for key in catalog.protection.unresolved_manifest_keys() {
            if !budget.try_spend_read() {
                return Ok(false);
            }
            // A write with a failed upload can land after the failure was reported.
            match self.read_manifest(&key).await? {
                Some(manifest) => {
                    catalog.protection.resolve_found_manifest(&key);
                    catalog.manifests.insert(key, manifest);
                }
                None => catalog.protection.expire_missing_manifest(
                    &key,
                    Instant::now(),
                    self.deletion_delay,
                ),
            }
        }
        Ok(true)
    }

    /// Returns the validated manifest at `key`, or `None` when it does not exist.
    async fn read_manifest(&self, key: &str) -> Result<Option<SnapshotManifest>, SnapshotError> {
        let Some(content) = self.object_store.get(key).await? else {
            return Ok(None);
        };
        let manifest = SnapshotManifest::decode(content.bytes)?;
        self.validate(key, &manifest)?;
        Ok(Some(manifest))
    }

    /// Deletes up to [`CANDIDATES_PER_RUN`] matured file candidates whose ETag a HEAD confirms.
    async fn delete_matured_files(
        &self,
        catalog: &mut SnapshotCatalog,
        fence: &OwnershipFence,
        now: Instant,
    ) -> Result<(), SnapshotError> {
        let manifest_files: BTreeSet<&str> = catalog
            .manifests
            .values()
            .flat_map(|manifest| &manifest.files)
            .map(|file| file.object_key.as_str())
            .collect();
        let matured = catalog.protection.matured_files(
            &manifest_files,
            now,
            self.deletion_delay,
            CANDIDATES_PER_RUN,
        );
        let mut orphan_files = Vec::new();
        for file in matured {
            match self.object_store.head(&file.key).await? {
                None => catalog.protection.forget_file(&file.key),
                Some(current) if current.etag != file.etag => {
                    catalog
                        .protection
                        .renominate_file(&file.key, current.etag, now);
                }
                Some(_) => orphan_files.push(file.key),
            }
        }
        for chunk in orphan_files.chunks(LISTING_PAGE_SIZE as usize) {
            fence.ensure_current_lease().await?;
            // An issued delete can finish after lease loss; successors upload into fresh lineages.
            self.object_store.delete_many(chunk).await?;
        }
        for key in &orphan_files {
            catalog.protection.forget_file(key);
        }
        if !orphan_files.is_empty() {
            tracing::info!(
                files = orphan_files.len(),
                "deleted unprotected snapshot files"
            );
        }
        Ok(())
    }

    /// Rejects a manifest whose deletion could remove objects outside this node's snapshots.
    fn validate(&self, key: &str, manifest: &SnapshotManifest) -> Result<(), SnapshotError> {
        let lineage = Uuid::from_slice(&manifest.lineage_id)
            .map_err(|error| SnapshotError::Invalid(format!("{key}: {error}")))?;
        let expected_key = format!(
            "snapshot/{}/{lineage}/manifest/{:020}",
            self.node_id, manifest.snapshot_number
        );
        let files_prefix = format!("snapshot/{}/", self.node_id);
        let has_foreign_file = manifest.files.iter().any(|file| {
            !file.object_key.starts_with(&files_prefix) || !file.object_key.contains("/files/")
        });
        if manifest.node_id != self.node_id || key != expected_key || has_foreign_file {
            return Err(SnapshotError::Invalid(format!(
                "snapshot manifest {key} does not match its node, lineage, number, or file keys"
            )));
        }
        Ok(())
    }
}

fn creation_order(manifest: &SnapshotManifest) -> (i64, &[u8], u64) {
    (
        manifest.created_at_unix_micros,
        &manifest.lineage_id,
        manifest.snapshot_number,
    )
}

#[cfg(test)]
mod tests;
