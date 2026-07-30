// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

mod config;
mod entry;
mod error;
mod format;
mod policy;
mod store;

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex, RwLock};

pub use config::BlobCacheConfig;
use config::validate_blob_id;
use entry::DiskEntry;
pub use error::BlobCacheError;
use format::{FileMetadata, calculate_metadata};
use policy::{DiskPolicyCallback, PolicyCache, new_policy};
use store::LocalFileStore;

pub struct BlobCache {
    config: BlobCacheConfig,
    store: LocalFileStore,
    policy: RwLock<Option<PolicyCache>>,
    lifecycle: RwLock<()>,
    commit: Mutex<()>,
    callback: DiskPolicyCallback,
    closed: AtomicBool,
}

impl BlobCache {
    pub fn open(config: BlobCacheConfig) -> Result<Self, BlobCacheError> {
        let store = LocalFileStore::new(config.directory())?;
        store.prepare()?;
        let callback = DiskPolicyCallback::new(store.clone());
        let policy = new_policy(&config, callback.clone())?;
        let cache = Self {
            config,
            store,
            policy: RwLock::new(Some(policy)),
            lifecycle: RwLock::new(()),
            commit: Mutex::new(()),
            callback,
            closed: AtomicBool::new(false),
        };
        if let Err(error) = cache.recover() {
            cache.close_policy_preserving_files();
            return Err(error);
        }
        Ok(cache)
    }

    pub fn get(&self, blob_id: &str) -> Result<Option<Vec<u8>>, BlobCacheError> {
        validate_blob_id(blob_id)?;
        let _lifecycle = self
            .lifecycle
            .read()
            .expect("blob cache lifecycle lock is poisoned");
        self.require_open()?;

        let entry = {
            let policy = self
                .policy
                .read()
                .expect("blob cache policy lock is poisoned");
            let policy = policy.as_ref().ok_or(BlobCacheError::Closed)?;
            policy.get(blob_id).map(|entry| Arc::clone(entry.value()))
        };
        let Some(entry) = entry else {
            return Ok(None);
        };
        let Some(lease) = entry.acquire_read() else {
            return Ok(None);
        };
        let result = self.store.read(&entry);
        drop(lease);
        match result {
            Ok(payload) => Ok(Some(payload)),
            Err(error) if error.is_missing_or_corrupt() => {
                self.invalidate_entry(&entry)?;
                Ok(None)
            }
            Err(error) => Err(error),
        }
    }

    pub fn put(&self, blob_id: &str, payload: &[u8]) -> Result<bool, BlobCacheError> {
        validate_blob_id(blob_id)?;
        let _lifecycle = self
            .lifecycle
            .read()
            .expect("blob cache lifecycle lock is poisoned");
        self.require_open()?;
        let _commit = self
            .commit
            .lock()
            .expect("blob cache commit lock is poisoned");
        self.callback.retry_cleanup()?;

        let metadata = calculate_metadata(blob_id, payload, self.store.path_for(blob_id))?;
        if metadata.size > self.config.max_bytes() {
            return Ok(false);
        }
        if let Some(reused) = self.reuse_existing(&metadata, payload)? {
            return Ok(reused);
        }

        let entry = Arc::new(DiskEntry::pending(metadata));
        self.callback.reset_error();
        let inserted = self.with_policy(|policy| {
            Ok(policy.insert(blob_id.to_owned(), Arc::clone(&entry), entry.metadata.size))
        })?;
        if !inserted {
            entry.begin_eviction();
            return Ok(false);
        }
        self.wait_for_policy()?;
        if let Some(error) = self.callback.take_error() {
            self.remove_candidate(&entry)?;
            return Err(BlobCacheError::Reconciliation(error));
        }
        if !self.policy_contains(blob_id, &entry)? {
            entry.begin_eviction();
            return Ok(false);
        }

        if let Err(error) = self.store.commit(&entry.metadata, payload) {
            self.remove_candidate(&entry)?;
            return Err(error);
        }
        if !entry.mark_ready() {
            return Err(BlobCacheError::Reconciliation(
                "admitted entry left pending state".to_owned(),
            ));
        }
        Ok(true)
    }

    pub fn delete(&self, blob_id: &str) -> Result<(), BlobCacheError> {
        validate_blob_id(blob_id)?;
        let _lifecycle = self
            .lifecycle
            .read()
            .expect("blob cache lifecycle lock is poisoned");
        self.require_open()?;
        let _commit = self
            .commit
            .lock()
            .expect("blob cache commit lock is poisoned");
        self.callback.retry_cleanup()?;

        let entry = self.policy_entry(blob_id)?;
        let Some(entry) = entry else {
            return self.store.remove(&self.store.path_for(blob_id));
        };
        entry.begin_eviction();
        if let Err(error) = self.store.remove(&entry.metadata.path) {
            entry.restore_ready();
            return Err(error);
        }
        self.with_policy(|policy| {
            policy.remove(&blob_id.to_owned());
            Ok(())
        })?;
        self.wait_for_policy()
    }

    pub fn delete_all(&self) -> Result<(), BlobCacheError> {
        let _lifecycle = self
            .lifecycle
            .write()
            .expect("blob cache lifecycle lock is poisoned");
        self.require_open()?;
        let _commit = self
            .commit
            .lock()
            .expect("blob cache commit lock is poisoned");

        self.callback.reset_error();
        self.with_policy(|policy| {
            policy
                .clear()
                .map_err(|error| BlobCacheError::Policy(error.to_string()))
        })?;
        self.wait_for_policy()?;
        if let Err(error) = self.store.purge() {
            self.callback.require_purge();
            return Err(error);
        }
        self.callback.reset_after_purge();
        Ok(())
    }

    pub fn close(&self) -> Result<(), BlobCacheError> {
        let _lifecycle = self
            .lifecycle
            .write()
            .expect("blob cache lifecycle lock is poisoned");
        if self.closed.load(Ordering::Acquire) {
            return Ok(());
        }
        let _commit = self
            .commit
            .lock()
            .expect("blob cache commit lock is poisoned");
        let cleanup_result = self.callback.retry_cleanup();
        self.close_policy_preserving_files();
        cleanup_result
    }

    fn recover(&self) -> Result<(), BlobCacheError> {
        let _commit = self
            .commit
            .lock()
            .expect("blob cache commit lock is poisoned");
        self.store.purge_temp().map_err(|error| {
            BlobCacheError::Reconciliation(format!("purge interrupted writes: {error}"))
        })?;
        let scan = self.store.scan().map_err(|error| {
            BlobCacheError::Reconciliation(format!("scan committed files: {error}"))
        })?;
        for path in scan.invalid_paths {
            self.store.remove(&path).map_err(|error| {
                BlobCacheError::Reconciliation(format!("remove invalid file: {error}"))
            })?;
        }
        for metadata in scan.entries {
            self.recover_entry(metadata)?;
        }
        Ok(())
    }

    fn recover_entry(&self, metadata: FileMetadata) -> Result<(), BlobCacheError> {
        if metadata.size > self.config.max_bytes() {
            return self.store.remove(&metadata.path).map_err(|error| {
                BlobCacheError::Reconciliation(format!("remove oversized file: {error}"))
            });
        }

        let entry = Arc::new(DiskEntry::ready(metadata));
        self.callback.reset_error();
        let inserted = self.with_policy(|policy| {
            Ok(policy.insert(
                entry.metadata.blob_id.clone(),
                Arc::clone(&entry),
                entry.metadata.size,
            ))
        })?;
        if !inserted {
            entry.begin_eviction();
            return self.store.remove(&entry.metadata.path).map_err(|error| {
                BlobCacheError::Reconciliation(format!("remove rejected file: {error}"))
            });
        }
        self.wait_for_policy()?;
        if let Some(error) = self.callback.take_error() {
            return Err(BlobCacheError::Reconciliation(error));
        }
        if self.policy_contains(&entry.metadata.blob_id, &entry)? {
            return Ok(());
        }
        self.store.remove(&entry.metadata.path).map_err(|error| {
            BlobCacheError::Reconciliation(format!("remove dropped file: {error}"))
        })
    }

    fn reuse_existing(
        &self,
        metadata: &FileMetadata,
        payload: &[u8],
    ) -> Result<Option<bool>, BlobCacheError> {
        let Some(entry) = self.policy_entry(&metadata.blob_id)? else {
            return Ok(None);
        };
        let Some(lease) = entry.acquire_read() else {
            return Ok(None);
        };
        let result = self.store.read(&entry);
        drop(lease);
        match result {
            Ok(existing) => {
                if entry.metadata.size != metadata.size
                    || entry.metadata.checksum != metadata.checksum
                    || existing != payload
                {
                    return Err(BlobCacheError::ContentMismatch(metadata.blob_id.clone()));
                }
                Ok(Some(true))
            }
            Err(error) if error.is_missing_or_corrupt() => {
                entry.begin_eviction();
                if let Err(remove_error) = self.store.remove(&entry.metadata.path) {
                    self.callback.add_cleanup_path(&entry.metadata.path);
                    self.remove_policy_key(&metadata.blob_id)?;
                    return Err(remove_error);
                }
                self.remove_policy_key(&metadata.blob_id)?;
                Ok(None)
            }
            Err(error) => Err(error),
        }
    }

    fn invalidate_entry(&self, entry: &Arc<DiskEntry>) -> Result<(), BlobCacheError> {
        let _commit = self
            .commit
            .lock()
            .expect("blob cache commit lock is poisoned");
        let current = self.policy_entry(&entry.metadata.blob_id)?;
        if !current
            .as_ref()
            .is_some_and(|current| Arc::ptr_eq(current, entry))
        {
            return Ok(());
        }
        entry.begin_eviction();
        let remove_result = self.store.remove(&entry.metadata.path);
        self.remove_policy_key(&entry.metadata.blob_id)?;
        if remove_result.is_err() {
            self.callback.add_cleanup_path(&entry.metadata.path);
        }
        remove_result
    }

    fn remove_candidate(&self, entry: &Arc<DiskEntry>) -> Result<(), BlobCacheError> {
        if self.policy_contains(&entry.metadata.blob_id, entry)? {
            self.remove_policy_key(&entry.metadata.blob_id)?;
        }
        entry.begin_eviction();
        Ok(())
    }

    fn remove_policy_key(&self, blob_id: &str) -> Result<(), BlobCacheError> {
        self.with_policy(|policy| {
            policy.remove(&blob_id.to_owned());
            Ok(())
        })?;
        self.wait_for_policy()
    }

    fn policy_entry(&self, blob_id: &str) -> Result<Option<Arc<DiskEntry>>, BlobCacheError> {
        self.with_policy(|policy| Ok(policy.get(blob_id).map(|entry| Arc::clone(entry.value()))))
    }

    fn policy_contains(
        &self,
        blob_id: &str,
        expected: &Arc<DiskEntry>,
    ) -> Result<bool, BlobCacheError> {
        self.with_policy(|policy| {
            Ok(policy
                .get(blob_id)
                .is_some_and(|current| Arc::ptr_eq(current.value(), expected)))
        })
    }

    fn wait_for_policy(&self) -> Result<(), BlobCacheError> {
        self.with_policy(|policy| {
            policy
                .wait()
                .map_err(|error| BlobCacheError::Policy(error.to_string()))
        })
    }

    fn with_policy<T>(
        &self,
        operation: impl FnOnce(&PolicyCache) -> Result<T, BlobCacheError>,
    ) -> Result<T, BlobCacheError> {
        let policy = self
            .policy
            .read()
            .expect("blob cache policy lock is poisoned");
        operation(policy.as_ref().ok_or(BlobCacheError::Closed)?)
    }

    fn require_open(&self) -> Result<(), BlobCacheError> {
        if self.closed.load(Ordering::Acquire) {
            Err(BlobCacheError::Closed)
        } else {
            Ok(())
        }
    }

    fn close_policy_preserving_files(&self) {
        self.callback.set_closing();
        self.closed.store(true, Ordering::Release);
        let policy = self
            .policy
            .write()
            .expect("blob cache policy lock is poisoned")
            .take();
        drop(policy);
    }
}

impl Drop for BlobCache {
    fn drop(&mut self) {
        self.callback.set_closing();
    }
}
