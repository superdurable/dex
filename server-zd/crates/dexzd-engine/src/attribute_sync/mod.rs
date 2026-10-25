// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Owner-side Attribute Sync: writes pending entries' latest committed values, then acknowledges them.

mod ack_proposer;
mod pending_entries;
mod projector;
mod waker;

use std::sync::Arc;
use std::time::Duration;

use dexzd_clock::WallClock;
use dexzd_cluster_metadata::LeaseStatus;
use dexzd_keyspace::PartitionHasher;
use dexzd_protocol::dex::Value;
use futures::future::BoxFuture;
use tokio::sync::watch;

use crate::effects::EffectRuntime;

pub use projector::{AttributeSyncProjector, AttributeSyncProjectorHandle};
pub use waker::AttributeSyncWaker;

/// One Attribute to write to an Attribute Store.
#[derive(Debug, Clone, PartialEq)]
pub struct AttributeSyncItem {
    /// Attribute name, or `<AttributeMap name>/<escaped instance key>` for an AttributeMap instance.
    pub key: String,
    /// Latest committed value; `None` clears a main-row column or deletes an instance row.
    pub value: Option<Value>,
}

/// Ownership proof checked in every write transaction, so a former owner cannot overwrite newer values.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AttributeSyncFence {
    /// Canonical UUID of the cluster object; tokens compare only within one cluster.
    pub cluster_id: String,
    /// Owner scope, `node:<node ID>` for single-node clusters.
    pub scope: String,
    /// The owner's node epoch, which rises whenever a process takes the node lease.
    pub token: u64,
}

impl AttributeSyncFence {
    /// Returns the fence of a node that owns every partition of its cluster.
    pub fn for_node_owner(cluster_id: [u8; 16], node_id: &str, node_epoch: u64) -> Self {
        Self {
            cluster_id: uuid::Uuid::from_bytes(cluster_id).to_string(),
            scope: format!("node:{node_id}"),
            token: node_epoch,
        }
    }
}

/// Why an Attribute Store write did not commit.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum AttributeSyncWriteError {
    /// The store cannot serve writes, such as a lost connection or a connect timeout; every run of the store backs off.
    #[error("Attribute Store write failed and will be retried: {detail}")]
    Retryable {
        /// Failure description without credentials.
        detail: String,
    },
    /// The store refused this batch's content, such as a constraint violation; only this run backs off.
    #[error("Attribute Store refused a batch, which will be retried: {detail}")]
    RetryableForRun {
        /// Failure description without credentials.
        detail: String,
    },
    /// The transaction timed out; only this run backs off, then retries with half as many Attributes per transaction.
    #[error(
        "Attribute Store write timed out and will be retried in smaller transactions: {detail}"
    )]
    TimedOut {
        /// Failure description without credentials.
        detail: String,
    },
    /// The store cannot accept the write, such as a Flow ID not matching rowKey; entries are dropped.
    #[error("Attribute Store rejected the write: {detail}")]
    Rejected {
        /// Failure description without credentials.
        detail: String,
    },
    /// A newer owner raised the fencing token; the projector stops.
    #[error("Attribute Store write was fenced by a newer owner: {detail}")]
    Fenced {
        /// Failure description without credentials.
        detail: String,
    },
}

/// Writes one Flow's Attributes to a configured Attribute Store.
pub trait AttributeSyncWriter: Send + Sync + 'static {
    /// Writes `items` of `flow_id` to `store_name` in one transaction that also checks `fence`.
    ///
    /// The transaction ends within the transaction timeout; dropping the future rolls it back.
    ///
    /// Returns `Ok` once the transaction committed, or the [`AttributeSyncWriteError`] that
    /// decides whether the projector retries, drops the entries, or stops.
    fn write_flow_attributes<'a>(
        &'a self,
        store_name: &'a str,
        flow_id: &'a str,
        items: &'a [AttributeSyncItem],
        fence: &'a AttributeSyncFence,
    ) -> BoxFuture<'a, Result<(), AttributeSyncWriteError>>;
}

/// Backoff between failed writes and the deadline after which entries are dropped.
#[derive(Debug, Clone, PartialEq)]
pub struct AttributeSyncRetryPolicy {
    /// Delay after the first failed write of a store or run; `syncRetryPolicy.initialInterval`.
    pub initial_interval: Duration,
    /// Longest delay between attempts; `syncRetryPolicy.maximumInterval`.
    pub maximum_interval: Duration,
    /// Multiplies the delay after each failed write; positive and finite.
    pub backoff_coefficient: f64,
    /// Consecutive failed writes of one run's entries before they are dropped; 0 is unlimited.
    pub maximum_attempts: u32,
    /// Entries pending longer than this since their retry window started are dropped.
    pub total_duration: Duration,
}

/// Settings of the Attribute Sync projector, derived from the `attributeStore` section.
#[derive(Debug, Clone, PartialEq)]
pub struct AttributeSyncConfig {
    /// Configured Attribute Store names; entries of other stores stay pending.
    pub store_names: Vec<String>,
    /// Most Attributes of one run written in one transaction; values below 1 count as 1.
    pub batch_size: usize,
    /// Backoff and drop deadline of failed writes.
    pub retry_policy: AttributeSyncRetryPolicy,
    /// Longest a write transaction stays open; the writer enforces it.
    pub transaction_timeout: Duration,
    /// Lease validity reserved beyond the transaction timeout before a write may start.
    pub lease_margin: Duration,
}

/// Dependencies of the Attribute Sync projector, passed once to [`AttributeSyncProjector::start`].
pub struct AttributeSyncDependencies {
    /// Writes to the configured Attribute Stores.
    pub writer: Arc<dyn AttributeSyncWriter>,
    /// Committed state reads and ack proposals.
    pub runtime: Arc<dyn EffectRuntime>,
    /// The cluster's partition hasher.
    pub partition_hasher: PartitionHasher,
    /// Owner wall clock, compared with the committed retry window starts.
    pub clock: Arc<dyn WallClock>,
    /// Backlog, failure, and fencing metrics.
    pub metrics: Arc<dexzd_metrics::NodeMetrics>,
    /// This node's fencing token.
    pub fence: AttributeSyncFence,
    /// Validity of this node's lease; writes stop when it is lost or about to expire.
    pub lease_status: watch::Receiver<LeaseStatus>,
    /// The waker that the effect runner signals when apply marks entries.
    pub waker: AttributeSyncWaker,
}
