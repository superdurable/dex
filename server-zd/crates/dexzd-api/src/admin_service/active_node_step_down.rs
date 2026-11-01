// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use async_trait::async_trait;

/// Makes the active server process give up its node lease, so a standby process can take over.
#[async_trait]
pub trait ActiveNodeStepDown: Send + Sync + 'static {
    /// Stops serving, drains in-flight commits, releases the node lease, then follows as a standby.
    ///
    /// Dropping the future, as a cancelled call does, must not interrupt a started step down.
    ///
    /// Returns the outcome once the node lease is released, including a takeover by another process
    /// observed before returning. The process then waits for the reported hold-off before it competes
    /// for the lease again. Returns [`ActiveNodeStepDownError::NotActive`] when this process does not
    /// hold the node lease, [`ActiveNodeStepDownError::AlreadySteppingDown`] when an earlier step down
    /// has not finished, [`ActiveNodeStepDownError::LeaseReleaseUnconfirmed`] when the release could
    /// not be confirmed, and [`ActiveNodeStepDownError::Internal`] for any other failure.
    async fn step_down_active_node(
        &self,
    ) -> Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError>;
}

/// What a completed step down released and observed.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ActiveNodeStepDownOutcome {
    /// Node ID whose active process stepped down.
    pub node_id: String,
    /// Node epoch of the released node lease.
    pub released_node_epoch: u64,
    /// Another process that acquired the node lease before the step down returned.
    pub observed_takeover: Option<ActiveNodeTakeover>,
    /// Wait before the stepped-down process competes for the node lease again.
    pub lease_reacquisition_hold_off: Duration,
}

/// Another process of the same node ID that acquired the released node lease.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ActiveNodeTakeover {
    /// Node epoch that the new active process raised on acquisition.
    pub node_epoch: u64,
    /// Random ID that the new active process recorded as the lease holder.
    pub holder_process_id: String,
}

/// Why a step down did not start or did not finish.
#[derive(Clone, Debug, PartialEq, Eq, thiserror::Error)]
#[non_exhaustive]
pub enum ActiveNodeStepDownError {
    /// This process does not hold the node lease, for example while it starts, stands by, or stops.
    #[error("this server process is not the active process of its node ID: {detail}")]
    NotActive {
        /// What this process is doing instead.
        detail: String,
    },
    /// An earlier step down of this process has not finished.
    #[error("a step down of this server process is already in progress")]
    AlreadySteppingDown,
    /// Serving stopped, but the release write was not confirmed; the lease stays held until it expires.
    #[error("the node lease release was not confirmed: {detail}")]
    LeaseReleaseUnconfirmed {
        /// Why the release was not confirmed.
        detail: String,
    },
    /// The step down failed for another reason.
    #[error("the step down failed: {detail}")]
    Internal {
        /// What failed.
        detail: String,
    },
}
