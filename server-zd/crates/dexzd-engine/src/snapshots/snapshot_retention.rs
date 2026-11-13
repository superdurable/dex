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
use uuid::Uuid;

use crate::snapshots::SnapshotError;
use crate::snapshots::ownership_fence::OwnershipFence;

const LISTING_PAGE_SIZE: u32 = 1000;
const LISTING_PAGES_PER_RUN: usize = 10;
const CANDIDATES_PER_RUN: usize = 64;

/// After this, an eventually consistent refresh starts no new page and resumes in the next run.
const EVENTUAL_LISTING_DURATION_PER_RUN: Duration = Duration::from_secs(10);

/// Keeps the newest snapshots of this node across every lineage and deletes the rest.
pub(crate) struct SnapshotRetention {
    pub(crate) object_store: Arc<dyn ObjectStore>,
    pub(crate) node_id: String,
    pub(crate) retained_count: usize,
    pub(crate) deletion_delay: Duration,
    /// When eventual, a manifest leaves the catalog only once a HEAD misses it.
    pub(crate) listing_consistency: ListingConsistency,
}

/// This process's upload lineage and newest reserved number; every manifest in it is read by key.
pub(crate) struct ReservedSnapshotLineage {
    pub(crate) lineage_id: Uuid,
    pub(crate) last_reserved_snapshot_number: u64,
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
    /// Reserved manifest numbers up to this one, in this lineage, were read by key.
    reserved_manifests_read_through: Option<(Uuid, u64)>,
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
}

impl SnapshotRetention {
    /// Deletes snapshots beyond the newest `retained_count` of every lineage combined, once
    /// the snapshot that displaced each one is older than the deletion delay.
    ///
    /// Snapshots in `referenced_keys` are always kept, and so is every file a kept snapshot
    /// references. A deleted snapshot's files go in the same pass, independent of process
    /// lifetime. Over an eventually consistent listing, `reserved_lineage` names the lineage
    /// whose manifests are read by key. Returns the kept snapshots, oldest first.
    pub(crate) async fn run_once<'catalog>(
        &self,
        catalog: &'catalog mut SnapshotCatalog,
        referenced_keys: &BTreeSet<String>,
        reserved_lineage: Option<&ReservedSnapshotLineage>,
        fence: &OwnershipFence,
        now_unix_micros: i64,
    ) -> Result<Vec<&'catalog SnapshotManifest>, SnapshotError> {
        if !self.refresh(catalog).await? {
            return Ok(Self::kept(catalog));
        }
        if !self.listing_consistency.is_strong() {
            self.read_unlisted_referenced_manifests(catalog, referenced_keys)
                .await?;
            if let Some(reserved_lineage) = reserved_lineage {
                self.read_unlisted_reserved_manifests(catalog, reserved_lineage)
                    .await?;
            }
        }
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
        let orphan_files = if self.listing_consistency.is_strong() {
            self.exclude_newly_referenced_files(catalog, unreferenced_files.into_iter().collect())
                .await?
        } else {
            self.exclude_files_unknown_manifests_may_use(
                catalog,
                unreferenced_files,
                reserved_lineage,
                now_unix_micros,
            )
        };
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

    /// Keeps each file that a known manifest uses or that a manifest retention would keep may use.
    fn exclude_files_unknown_manifests_may_use(
        &self,
        catalog: &SnapshotCatalog,
        unreferenced_files: BTreeSet<String>,
        reserved_lineage: Option<&ReservedSnapshotLineage>,
        now_unix_micros: i64,
    ) -> Vec<String> {
        let referenced_files: BTreeSet<&str> = catalog
            .manifests
            .values()
            .flat_map(|manifest| &manifest.files)
            .map(|file| file.object_key.as_str())
            .collect();
        unreferenced_files
            .into_iter()
            .filter(|key| {
                !referenced_files.contains(key.as_str())
                    && file_lineage(key).is_some_and(|lineage| {
                        reserved_lineage.is_some_and(|reserved| reserved.lineage_id == lineage)
                            || self.is_every_manifest_of_lineage_obsolete(
                                catalog,
                                lineage,
                                now_unix_micros,
                            )
                    })
            })
            .collect()
    }

    /// Returns whether retention deletes every manifest of `lineage`, including manifests no listing showed.
    ///
    /// One process writes each UUIDv7 lineage, so its manifests predate every later lineage's.
    /// `retained_count` known later manifests past the deletion delay therefore displace them all.
    fn is_every_manifest_of_lineage_obsolete(
        &self,
        catalog: &SnapshotCatalog,
        lineage: Uuid,
        now_unix_micros: i64,
    ) -> bool {
        let mut later_created_at: Vec<i64> = catalog
            .manifests
            .values()
            .filter(|manifest| {
                Uuid::from_slice(&manifest.lineage_id).is_ok_and(|other| other > lineage)
            })
            .map(|manifest| manifest.created_at_unix_micros)
            .collect();
        later_created_at.sort_unstable();
        let delay_micros = i64::try_from(self.deletion_delay.as_micros()).unwrap_or(i64::MAX);
        later_created_at
            .get(self.retained_count.max(1) - 1)
            .is_some_and(|created_at| now_unix_micros.saturating_sub(*created_at) >= delay_micros)
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

    /// Reconciles the catalog with a listing and reads only manifests it has not seen.
    async fn refresh(&self, catalog: &mut SnapshotCatalog) -> Result<bool, SnapshotError> {
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
        let unlisted_manifest_keys: Vec<String> = catalog
            .manifests
            .keys()
            .filter(|key| {
                !catalog.listed_manifest_keys.contains(*key)
                    && !catalog.inserted_manifest_keys.contains(*key)
            })
            .cloned()
            .collect();
        for key in unlisted_manifest_keys {
            // A stale listing can lack a manifest that still exists; files it uses must stay.
            if self.listing_consistency.is_strong() || self.object_store.head(&key).await?.is_none()
            {
                catalog.manifests.remove(&key);
            }
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
        Ok(true)
    }

    /// Reads referenced manifests of this node that the catalog lacks, as listings can hide them.
    async fn read_unlisted_referenced_manifests(
        &self,
        catalog: &mut SnapshotCatalog,
        referenced_keys: &BTreeSet<String>,
    ) -> Result<(), SnapshotError> {
        let node_prefix = format!("snapshot/{}/", self.node_id);
        for key in referenced_keys {
            if !key.starts_with(&node_prefix) || catalog.manifests.contains_key(key) {
                continue;
            }
            let Some(content) = self.object_store.get(key).await? else {
                continue;
            };
            let manifest = SnapshotManifest::decode(content.bytes)?;
            self.validate(key, &manifest)?;
            catalog.manifests.insert(key.clone(), manifest);
        }
        Ok(())
    }

    /// Reads the manifests of the reserved lineage that the catalog lacks, as listings can hide them.
    async fn read_unlisted_reserved_manifests(
        &self,
        catalog: &mut SnapshotCatalog,
        reserved_lineage: &ReservedSnapshotLineage,
    ) -> Result<(), SnapshotError> {
        let lineage = reserved_lineage.lineage_id;
        let first_unread_number = match catalog.reserved_manifests_read_through {
            Some((read_lineage, read_through)) if read_lineage == lineage => read_through + 1,
            _ => 1,
        };
        for snapshot_number in first_unread_number..=reserved_lineage.last_reserved_snapshot_number
        {
            let key = format!(
                "snapshot/{}/{lineage}/manifest/{snapshot_number:020}",
                self.node_id
            );
            if catalog.manifests.contains_key(&key) {
                continue;
            }
            // An upload with an unknown outcome can leave a manifest this process never recorded.
            let Some(content) = self.object_store.get(&key).await? else {
                continue;
            };
            let manifest = SnapshotManifest::decode(content.bytes)?;
            self.validate(&key, &manifest)?;
            catalog.manifests.insert(key, manifest);
        }
        catalog.reserved_manifests_read_through =
            Some((lineage, reserved_lineage.last_reserved_snapshot_number));
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

/// Returns the lineage of a snapshot file key, or `None` when the key names none.
fn file_lineage(key: &str) -> Option<Uuid> {
    key.split('/')
        .nth(2)
        .and_then(|lineage| Uuid::parse_str(lineage).ok())
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
