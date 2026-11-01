// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::fmt;
use std::sync::Arc;
use std::time::Duration;

use dexzd_clock::WallClock;
use dexzd_object_store::ObjectStore;
use dexzd_protocol::dexzd::v1::NodeLease;
use prost::Message;
use tokio::sync::watch;
use uuid::Uuid;

use crate::lease::{LeaseHandle, LeaseObservers, acquire_lease_inner};
use crate::{MetadataError, NodeConfig};

/// Timing and contention policy for a node lease.
#[derive(Debug, Clone)]
pub struct LeaseConfig {
    /// Lease duration; defaults to ten seconds.
    pub duration: Duration,
    /// Additional wait after an old lease expires before takeover.
    /// Defaults to one second.
    pub takeover_grace: Duration,
    /// Return immediately when another live process holds the lease.
    /// Defaults to false.
    pub fail_fast: bool,
    /// Defaults to false; when true, takes over a held lease at once. Set only when its holder provably stopped.
    pub should_take_over_held_lease: bool,
    /// Defaults to one second; must be positive. The longest wait between reads of a lease that
    /// another process holds. The wait is also at most a third of `duration`, minus jitter.
    pub held_lease_poll_limit: Duration,
    /// Defaults to `None`, which writes an available lease at once. Otherwise asked once for each
    /// released or expired lease version how long to wait first, so a readier process can take
    /// the lease; the lease is read again after the wait. It only reduces contention: the
    /// conditional lease write alone decides which process acquires.
    pub acquisition_delay: Option<Arc<dyn LeaseAcquisitionDelay>>,
}

impl Default for LeaseConfig {
    fn default() -> Self {
        Self {
            duration: Duration::from_secs(10),
            takeover_grace: Duration::from_secs(1),
            fail_fast: false,
            should_take_over_held_lease: false,
            held_lease_poll_limit: Duration::from_secs(1),
            acquisition_delay: None,
        }
    }
}

impl LeaseConfig {
    /// Returns whether `lease` is released or expired past the takeover grace at `now_unix_micros`.
    pub fn is_available_at(&self, lease: &NodeLease, now_unix_micros: i64) -> bool {
        let grace_micros = i64::try_from(self.takeover_grace.as_micros()).unwrap_or(i64::MAX);
        lease.expires_at_unix_micros.saturating_add(grace_micros) <= now_unix_micros
    }
}

/// Decides how long a process waits before it writes an available node lease.
pub trait LeaseAcquisitionDelay: Send + Sync + fmt::Debug {
    /// Returns the wait before writing an available lease; zero writes it at once.
    fn delay_before_acquiring(&self) -> Duration;
}

/// Reads the node lease of `node_id` without competing for it.
///
/// Returns `None` before any process has taken the lease.
pub async fn read_node_lease(
    store: &dyn ObjectStore,
    node_id: &str,
) -> Result<Option<NodeLease>, MetadataError> {
    let Some(content) = store.get(&format!("lease/{node_id}")).await? else {
        return Ok(None);
    };
    let lease = NodeLease::decode(content.bytes).map_err(|source| MetadataError::Decode {
        key: "lease",
        source,
    })?;
    Ok(Some(lease))
}

/// Sleeps for `delay`; returns `true` as soon as `cancellation` is set.
pub(crate) async fn sleep_unless_cancelled(
    delay: Duration,
    cancellation: Option<&mut watch::Receiver<bool>>,
) -> bool {
    let Some(cancellation) = cancellation else {
        tokio::time::sleep(delay).await;
        return false;
    };
    tokio::select! {
        () = tokio::time::sleep(delay) => false,
        changed = cancellation.changed() => {
            if changed.is_ok() && *cancellation.borrow() {
                return true;
            }
            if changed.is_err() {
                tokio::time::sleep(delay).await;
            }
            false
        }
    }
}

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
    /// A write whose outcome is still unknown at cancellation is not released; it expires after
    /// the lease duration.
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
