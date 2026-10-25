// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use dexzd_clock::WallClock;
use dexzd_object_store::ObjectStore;
use tokio::sync::watch;
use uuid::Uuid;

use crate::lease::{LeaseConfig, LeaseHandle, LeaseObservers, acquire_lease_inner};
use crate::{MetadataError, NodeConfig};

/// Stores used while acquiring a lease and after acquisition.
pub struct LeaseObjectStores {
    pub(crate) acquisition: Arc<dyn ObjectStore>,
    pub(crate) renewal: Arc<dyn ObjectStore>,
}

impl LeaseObjectStores {
    /// Uses `acquisition` for startup reads and `renewal` for renewal and release.
    pub fn new(acquisition: Arc<dyn ObjectStore>, renewal: Arc<dyn ObjectStore>) -> Self {
        Self {
            acquisition,
            renewal,
        }
    }

    pub(crate) fn shared(store: Arc<dyn ObjectStore>) -> Self {
        Self::new(store.clone(), store)
    }
}

impl LeaseConfig {
    /// Acquires the lease, or returns `None` after cancellation and release of any written lease.
    pub async fn acquire_until_cancelled(
        &self,
        store: Arc<dyn ObjectStore>,
        clock: Arc<dyn WallClock>,
        node: &NodeConfig,
        cancellation: &mut watch::Receiver<bool>,
    ) -> Result<Option<LeaseHandle>, MetadataError> {
        self.acquire_until_cancelled_with_holder_process_id(
            store,
            clock,
            node,
            cancellation,
            Uuid::new_v4().to_string(),
        )
        .await
    }

    /// Acquires the lease like [`Self::acquire_until_cancelled`], naming `holder_process_id` as
    /// its holder.
    pub async fn acquire_until_cancelled_with_holder_process_id(
        &self,
        store: Arc<dyn ObjectStore>,
        clock: Arc<dyn WallClock>,
        node: &NodeConfig,
        cancellation: &mut watch::Receiver<bool>,
        holder_process_id: String,
    ) -> Result<Option<LeaseHandle>, MetadataError> {
        acquire_lease_inner(
            LeaseObjectStores::shared(store),
            clock,
            node,
            self,
            Some(cancellation),
            holder_process_id,
            LeaseObservers::default(),
        )
        .await
    }

    /// Acquires a process-specific lease that reports to `observers`.
    pub async fn acquire_until_cancelled_with_observers(
        &self,
        store: Arc<dyn ObjectStore>,
        clock: Arc<dyn WallClock>,
        node: &NodeConfig,
        cancellation: &mut watch::Receiver<bool>,
        holder_process_id: String,
        observers: LeaseObservers,
    ) -> Result<Option<LeaseHandle>, MetadataError> {
        acquire_lease_inner(
            LeaseObjectStores::shared(store),
            clock,
            node,
            self,
            Some(cancellation),
            holder_process_id,
            observers,
        )
        .await
    }

    /// Acquires with `stores.acquisition` for startup and `stores.renewal` for renewal and release.
    pub async fn acquire_until_cancelled_with_observers_and_stores(
        &self,
        stores: LeaseObjectStores,
        clock: Arc<dyn WallClock>,
        node: &NodeConfig,
        cancellation: &mut watch::Receiver<bool>,
        holder_process_id: String,
        observers: LeaseObservers,
    ) -> Result<Option<LeaseHandle>, MetadataError> {
        acquire_lease_inner(
            stores,
            clock,
            node,
            self,
            Some(cancellation),
            holder_process_id,
            observers,
        )
        .await
    }
}
