// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Waits until no handle keeps a local store open, so another process may reopen its directory.

use std::sync::{Arc, Mutex, Weak};
use std::time::Duration;

use tokio::time::Instant;

use crate::commit::EngineLocalStore;

/// How often a wait for a closed store checks it again.
const STORE_CLOSE_POLL: Duration = Duration::from_millis(10);

/// Waits until a node's or a hot standby's local store has closed, so its directory reopens.
///
/// Take it before a shutdown, then await [`Self::wait_until_closed`] after it.
#[derive(Clone)]
pub struct LocalStoreCloseWaiter {
    /// The newest store; a hot standby's restore replaces it.
    store: Arc<Mutex<Option<Weak<EngineLocalStore>>>>,
}

impl LocalStoreCloseWaiter {
    /// Returns a waiter that watches whatever store `store` names when it waits.
    pub(crate) fn new(store: Arc<Mutex<Option<Weak<EngineLocalStore>>>>) -> Self {
        Self { store }
    }

    /// Returns a waiter of `store`.
    pub(crate) fn for_store(store: &Arc<EngineLocalStore>) -> Self {
        Self::new(Arc::new(Mutex::new(Some(Arc::downgrade(store)))))
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
        let Some(store) = self
            .store
            .lock()
            .expect("store handle lock is not poisoned")
            .clone()
        else {
            return true;
        };
        while store.strong_count() > 0 && Instant::now() < deadline {
            tokio::time::sleep(STORE_CLOSE_POLL).await;
        }
        store.strong_count() == 0
    }
}
