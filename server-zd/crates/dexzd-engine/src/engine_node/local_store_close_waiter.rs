// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Waits until no handle keeps a local store open, so another process may reopen its directory.

use std::path::PathBuf;
use std::sync::{Arc, Mutex, Weak};
use std::time::Duration;

use dexzd_storage::is_directory_open_in_this_process;
use tokio::time::Instant;

use crate::commit::EngineLocalStore;
use crate::recovery::LocalStoreConfig;

/// How often a wait for a closed store checks it again.
const STORE_CLOSE_POLL: Duration = Duration::from_millis(10);

/// Waits until a node's or a hot standby's local store has closed, so its directory reopens.
///
/// Take it before a shutdown, then await [`Self::wait_until_closed`] after it.
#[derive(Clone)]
pub struct LocalStoreCloseWaiter {
    /// The newest store; a hot standby's restore replaces it.
    store: Arc<Mutex<Option<Weak<EngineLocalStore>>>>,
    /// A RocksDB store's directory, whose lock outlives the last handle until the store closes.
    directory: Option<PathBuf>,
}

impl LocalStoreCloseWaiter {
    /// Returns a waiter that watches whatever store `store` names when it waits, and the
    /// directory of the store that `config` names.
    pub(crate) fn new(
        store: Arc<Mutex<Option<Weak<EngineLocalStore>>>>,
        config: &LocalStoreConfig,
    ) -> Self {
        let directory = match config {
            LocalStoreConfig::RocksDb { directory, .. } => Some(directory.clone()),
            _ => None,
        };
        Self { store, directory }
    }

    /// Returns a waiter of whatever store this process has open in `directory`.
    #[cfg(feature = "test-util")]
    pub fn for_directory_for_test(directory: PathBuf) -> Self {
        Self {
            store: Arc::new(Mutex::new(None)),
            directory: Some(directory),
        }
    }

    /// Returns a waiter of `store`, which `config` names.
    pub(crate) fn for_store(store: &Arc<EngineLocalStore>, config: &LocalStoreConfig) -> Self {
        Self::new(Arc::new(Mutex::new(Some(Arc::downgrade(store)))), config)
    }

    /// Waits until no handle keeps the store open, for at most `budget`.
    ///
    /// Returns whether the store closed, and `true` when no store was ever opened. Returns
    /// `false` when a handle still lives after `budget`; reopening the directory then fails with
    /// [`crate::EngineStartError::LocalStoreUnavailable`] until it closes.
    pub async fn wait_until_closed(&self, budget: Duration) -> bool {
        self.wait_until_closed_by(Instant::now() + budget).await
    }

    /// Returns whether the store closed by `deadline`; see [`Self::wait_until_closed`].
    pub(crate) async fn wait_until_closed_by(&self, deadline: Instant) -> bool {
        let store = self
            .store
            .lock()
            .expect("store handle lock is not poisoned")
            .clone();
        // The last handle's drop closes the database after the count reached zero.
        let is_open = || {
            store.as_ref().is_some_and(|store| store.strong_count() > 0)
                || self
                    .directory
                    .as_deref()
                    .is_some_and(is_directory_open_in_this_process)
        };
        while is_open() && Instant::now() < deadline {
            tokio::time::sleep(STORE_CLOSE_POLL).await;
        }
        !is_open()
    }
}
