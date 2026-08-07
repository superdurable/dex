// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::collections::HashSet;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use stretto::{Cache, CacheCallback, Coster, DefaultKeyBuilder, Item, UpdateValidator};

use super::entry::DiskEntry;
use super::store::LocalFileStore;
use super::{BlobCacheConfig, BlobCacheError};

pub(crate) type PolicyCache = Cache<
    String,
    Arc<DiskEntry>,
    DefaultKeyBuilder<String>,
    DiskEntryCoster,
    RejectUpdates,
    DiskPolicyCallback,
>;

#[derive(Clone)]
pub(crate) struct DiskPolicyCallback {
    shared: Arc<CallbackShared>,
}

struct CallbackShared {
    store: LocalFileStore,
    state: Mutex<CallbackState>,
}

#[derive(Default)]
struct CallbackState {
    closing: bool,
    callback_error: Option<String>,
    cleanup_backlog: HashSet<PathBuf>,
    purge_required: bool,
}

impl DiskPolicyCallback {
    pub(crate) fn new(store: LocalFileStore) -> Self {
        Self {
            shared: Arc::new(CallbackShared {
                store,
                state: Mutex::new(CallbackState::default()),
            }),
        }
    }

    pub(crate) fn reset_error(&self) {
        self.shared
            .state
            .lock()
            .expect("blob cache callback mutex is poisoned")
            .callback_error = None;
    }

    pub(crate) fn take_error(&self) -> Option<String> {
        self.shared
            .state
            .lock()
            .expect("blob cache callback mutex is poisoned")
            .callback_error
            .take()
    }

    pub(crate) fn retry_cleanup(&self) -> Result<(), BlobCacheError> {
        let mut state = self
            .shared
            .state
            .lock()
            .expect("blob cache callback mutex is poisoned");
        if state.purge_required {
            self.shared.store.purge().map_err(|error| {
                BlobCacheError::Reconciliation(format!("retry cache purge: {error}"))
            })?;
            state.purge_required = false;
            state.cleanup_backlog.clear();
            return Ok(());
        }

        let paths: Vec<PathBuf> = state.cleanup_backlog.iter().cloned().collect();
        let mut failures = Vec::new();
        for path in paths {
            match self.shared.store.remove(&path) {
                Ok(()) => {
                    state.cleanup_backlog.remove(&path);
                }
                Err(error) => failures.push(error.to_string()),
            }
        }
        if failures.is_empty() {
            Ok(())
        } else {
            Err(BlobCacheError::Reconciliation(failures.join("; ")))
        }
    }

    pub(crate) fn add_cleanup_path(&self, path: &Path) {
        self.shared
            .state
            .lock()
            .expect("blob cache callback mutex is poisoned")
            .cleanup_backlog
            .insert(path.to_path_buf());
    }

    pub(crate) fn require_purge(&self) {
        self.shared
            .state
            .lock()
            .expect("blob cache callback mutex is poisoned")
            .purge_required = true;
    }

    pub(crate) fn reset_after_purge(&self) {
        let mut state = self
            .shared
            .state
            .lock()
            .expect("blob cache callback mutex is poisoned");
        state.callback_error = None;
        state.cleanup_backlog.clear();
        state.purge_required = false;
    }

    pub(crate) fn set_closing(&self) {
        self.shared
            .state
            .lock()
            .expect("blob cache callback mutex is poisoned")
            .closing = true;
    }

    fn handle_policy_removal(&self, entry: Arc<DiskEntry>) {
        if self
            .shared
            .state
            .lock()
            .expect("blob cache callback mutex is poisoned")
            .closing
        {
            return;
        }
        if !entry.begin_eviction() {
            return;
        }
        if let Err(error) = self.shared.store.remove(&entry.metadata.path) {
            let mut state = self
                .shared
                .state
                .lock()
                .expect("blob cache callback mutex is poisoned");
            state.cleanup_backlog.insert(entry.metadata.path.clone());
            let message = error.to_string();
            state.callback_error = Some(match state.callback_error.take() {
                Some(existing) => format!("{existing}; {message}"),
                None => message,
            });
        }
    }
}

impl CacheCallback for DiskPolicyCallback {
    type Value = Arc<DiskEntry>;

    fn on_exit(&self, _value: Option<Self::Value>) {}

    fn on_evict(&self, item: Item<Self::Value>) {
        if let Some(entry) = item.val {
            self.handle_policy_removal(entry);
        }
    }

    fn on_reject(&self, item: Item<Self::Value>) {
        if let Some(entry) = item.val {
            self.handle_policy_removal(entry);
        }
    }
}

pub(crate) struct DiskEntryCoster;

impl Coster for DiskEntryCoster {
    type Value = Arc<DiskEntry>;

    fn cost(&self, entry: &Self::Value) -> i64 {
        entry.metadata.size
    }
}

pub(crate) struct RejectUpdates;

impl UpdateValidator for RejectUpdates {
    type Value = Arc<DiskEntry>;

    fn should_update(&self, _previous: &Self::Value, _current: &Self::Value) -> bool {
        false
    }
}

pub(crate) fn new_policy(
    config: &BlobCacheConfig,
    callback: DiskPolicyCallback,
) -> Result<PolicyCache, BlobCacheError> {
    Cache::<String, Arc<DiskEntry>>::builder(config.frequency_counters(), config.max_bytes())
        .set_coster(DiskEntryCoster)
        .set_update_validator(RejectUpdates)
        .set_callback(callback)
        .set_ignore_internal_cost(true)
        .finalize()
        .map_err(|error| BlobCacheError::Policy(error.to_string()))
}
