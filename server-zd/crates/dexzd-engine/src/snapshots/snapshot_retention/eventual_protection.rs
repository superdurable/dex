// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! What snapshot retention keeps when listings are eventually consistent.

use std::collections::{BTreeMap, BTreeSet};
use std::time::Duration;

use dexzd_object_store::ObjectMetadata;
use dexzd_protocol::dexzd::v1::SnapshotManifest;
use tokio::time::Instant;

/// Protection and candidate timers of one process on its monotonic clock; losing them only delays deletion.
#[derive(Default)]
pub(crate) struct EventualProtection {
    /// Manifests a strong read showed pointed; `Some` holds when they stopped being pointed.
    pointed_manifests: BTreeMap<String, Option<Instant>>,
    /// Manifests this process uploaded or sent a write for, by key.
    own_manifests: BTreeMap<String, OwnManifest>,
    next_upload_sequence: u64,
    /// When this process first saw each manifest unprotected.
    manifest_candidates: BTreeMap<String, Instant>,
    /// When this process first saw each file unprotected, and the ETag it saw.
    file_candidates: BTreeMap<String, FileCandidate>,
    /// The last file candidate checked, so the next run resumes after it.
    file_candidate_cursor: Option<String>,
}

struct OwnManifest {
    upload_sequence: u64,
    /// Present until a read finds a manifest whose write failed.
    unresolved: Option<UnresolvedUpload>,
}

struct UnresolvedUpload {
    file_keys: Vec<String>,
    attempt_ended_at: Instant,
}

struct FileCandidate {
    etag: String,
    nominated_at: Instant,
}

/// A file candidate that has been unprotected for the deletion delay.
pub(crate) struct MaturedFile {
    pub(crate) key: String,
    pub(crate) etag: String,
}

impl EventualProtection {
    /// Records manifests that a strong read showed pointed.
    pub(crate) fn record_pointed_manifests(&mut self, keys: impl IntoIterator<Item = String>) {
        for key in keys {
            self.pointed_manifests.insert(key, None);
        }
    }

    /// Records the current pointers and forgets manifests unpointed for at least `deletion_delay`.
    pub(crate) fn observe_pointed_manifests(
        &mut self,
        pointed_keys: &BTreeSet<&str>,
        now: Instant,
        deletion_delay: Duration,
    ) {
        for key in pointed_keys {
            self.pointed_manifests.insert((*key).to_owned(), None);
        }
        self.pointed_manifests.retain(|key, stopped_at| {
            if pointed_keys.contains(key.as_str()) {
                return true;
            }
            let stopped_at = stopped_at.get_or_insert(now);
            now.saturating_duration_since(*stopped_at) < deletion_delay
        });
    }

    /// Returns the manifests a strong read showed pointed within the deletion delay.
    pub(crate) fn pointed_manifest_keys(&self) -> impl Iterator<Item = &String> {
        self.pointed_manifests.keys()
    }

    /// Records a manifest that this process uploaded.
    pub(crate) fn record_own_manifest(&mut self, key: String) {
        let upload_sequence = self.take_upload_sequence();
        self.own_manifests.insert(
            key,
            OwnManifest {
                upload_sequence,
                unresolved: None,
            },
        );
    }

    /// Records a manifest write whose upload failed, so the manifest may exist or appear later.
    pub(crate) fn record_unresolved_upload(
        &mut self,
        key: String,
        manifest: &SnapshotManifest,
        attempt_ended_at: Instant,
    ) {
        let upload_sequence = self.take_upload_sequence();
        let file_keys = manifest
            .files
            .iter()
            .map(|file| file.object_key.clone())
            .collect();
        self.own_manifests.insert(
            key,
            OwnManifest {
                upload_sequence,
                unresolved: Some(UnresolvedUpload {
                    file_keys,
                    attempt_ended_at,
                }),
            },
        );
    }

    fn take_upload_sequence(&mut self) -> u64 {
        let upload_sequence = self.next_upload_sequence;
        self.next_upload_sequence += 1;
        upload_sequence
    }

    /// Returns the keys of manifest writes that no read has found yet.
    pub(crate) fn unresolved_manifest_keys(&self) -> Vec<String> {
        self.own_manifests
            .iter()
            .filter(|(_, own)| own.unresolved.is_some())
            .map(|(key, _)| key.clone())
            .collect()
    }

    /// Marks an unresolved manifest as found, so it counts as an uploaded one.
    pub(crate) fn resolve_found_manifest(&mut self, key: &str) {
        if let Some(own) = self.own_manifests.get_mut(key) {
            own.unresolved = None;
        }
    }

    /// Forgets an unresolved manifest once its upload ended at least `deletion_delay` ago.
    pub(crate) fn expire_missing_manifest(
        &mut self,
        key: &str,
        now: Instant,
        deletion_delay: Duration,
    ) {
        let is_expired = self
            .own_manifests
            .get(key)
            .and_then(|own| own.unresolved.as_ref())
            .is_some_and(|unresolved| {
                now.saturating_duration_since(unresolved.attempt_ended_at) >= deletion_delay
            });
        if is_expired {
            self.own_manifests.remove(key);
        }
    }

    /// Returns the manifests that retention keeps and the files they list.
    ///
    /// Returns the pointed manifests, the manifests pointed within the deletion delay, and the
    /// newest `retained_count` uploaded manifests in `manifests`, plus every file they or an
    /// unresolved manifest write list.
    pub(crate) fn protected_objects(
        &mut self,
        manifests: &BTreeMap<String, SnapshotManifest>,
        retained_count: usize,
    ) -> (BTreeSet<String>, BTreeSet<String>) {
        self.own_manifests
            .retain(|key, own| own.unresolved.is_some() || manifests.contains_key(key));
        let mut uploaded: Vec<(u64, &String)> = self
            .own_manifests
            .iter()
            .filter(|(_, own)| own.unresolved.is_none())
            .map(|(key, own)| (own.upload_sequence, key))
            .collect();
        uploaded.sort_unstable();
        let mut protected_manifests: BTreeSet<String> =
            self.pointed_manifests.keys().cloned().collect();
        protected_manifests.extend(
            uploaded
                .iter()
                .rev()
                .take(retained_count)
                .map(|(_, key)| (*key).clone()),
        );
        let mut protected_files: BTreeSet<String> = protected_manifests
            .iter()
            .filter_map(|key| manifests.get(key))
            .flat_map(|manifest| &manifest.files)
            .map(|file| file.object_key.clone())
            .collect();
        protected_files.extend(
            self.own_manifests
                .values()
                .filter_map(|own| own.unresolved.as_ref())
                .flat_map(|unresolved| unresolved.file_keys.iter().cloned()),
        );
        (protected_manifests, protected_files)
    }

    /// Nominates every known unprotected manifest and returns those unprotected for the delay.
    pub(crate) fn matured_manifests<'manifests>(
        &mut self,
        manifest_keys: impl Iterator<Item = &'manifests String>,
        protected_manifests: &BTreeSet<String>,
        now: Instant,
        deletion_delay: Duration,
    ) -> Vec<String> {
        let candidates: BTreeSet<&String> = manifest_keys
            .filter(|key| !protected_manifests.contains(*key))
            .collect();
        self.manifest_candidates
            .retain(|key, _| candidates.contains(key));
        candidates
            .into_iter()
            .filter(|key| {
                let nominated_at = *self
                    .manifest_candidates
                    .entry((*key).clone())
                    .or_insert(now);
                now.saturating_duration_since(nominated_at) >= deletion_delay
            })
            .cloned()
            .collect()
    }

    /// Forgets the candidate timer of a manifest that retention deleted.
    pub(crate) fn forget_deleted_manifest(&mut self, key: &str) {
        self.manifest_candidates.remove(key);
        self.own_manifests.remove(key);
    }

    /// Nominates listed unprotected files and clears protected ones; a later listing's omission keeps a timer.
    pub(crate) fn nominate_files(
        &mut self,
        listed_files: &BTreeMap<String, ObjectMetadata>,
        protected_files: &BTreeSet<String>,
        now: Instant,
    ) {
        self.file_candidates
            .retain(|key, _| !protected_files.contains(key));
        for (key, listed_file) in listed_files {
            if protected_files.contains(key) {
                continue;
            }
            let candidate =
                self.file_candidates
                    .entry(key.clone())
                    .or_insert_with(|| FileCandidate {
                        etag: listed_file.etag.clone(),
                        nominated_at: now,
                    });
            if candidate.etag != listed_file.etag {
                *candidate = FileCandidate {
                    etag: listed_file.etag.clone(),
                    nominated_at: now,
                };
            }
        }
    }

    /// Returns up to `limit` files unprotected for the delay that no known manifest lists.
    ///
    /// Returns them in key order after the previous run's last one, and wraps at the end.
    pub(crate) fn matured_files(
        &mut self,
        manifest_files: &BTreeSet<&str>,
        now: Instant,
        deletion_delay: Duration,
        limit: usize,
    ) -> Vec<MaturedFile> {
        let matured: Vec<MaturedFile> = self
            .file_candidates
            .iter()
            .filter(|(key, _)| {
                self.file_candidate_cursor
                    .as_ref()
                    .is_none_or(|cursor| *key > cursor)
            })
            .filter(|(key, candidate)| {
                !manifest_files.contains(key.as_str())
                    && now.saturating_duration_since(candidate.nominated_at) >= deletion_delay
            })
            .take(limit)
            .map(|(key, candidate)| MaturedFile {
                key: key.clone(),
                etag: candidate.etag.clone(),
            })
            .collect();
        self.file_candidate_cursor = matured.last().map(|file| file.key.clone());
        matured
    }

    /// Restarts the timer of a file whose current ETag differs from the nominated one.
    pub(crate) fn renominate_file(&mut self, key: &str, etag: String, now: Instant) {
        self.file_candidates.insert(
            key.to_owned(),
            FileCandidate {
                etag,
                nominated_at: now,
            },
        );
    }

    /// Forgets the candidate timer of a file that retention deleted or found missing.
    pub(crate) fn forget_file(&mut self, key: &str) {
        self.file_candidates.remove(key);
    }
}
