// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use async_trait::async_trait;

/// Describes the active server process and makes it give up its node lease, so a standby process can take over.
///
/// The server implements this trait over its node lease and serve loop and injects it into
/// [`crate::DexAdminService::authenticated_server`].
#[async_trait]
pub trait ActiveNodeAdministration: Send + Sync + 'static {
    /// Reads the node lease that this process holds, without changing anything.
    ///
    /// Returns the node ID, the node epoch, and the holder process ID of that lease. Returns
    /// [`ActiveNodeDescriptionError::NotActive`] when this process does not hold the node lease, for
    /// example while it starts, stands by, or steps down.
    async fn describe_active_node(
        &self,
    ) -> Result<ActiveNodeDescription, ActiveNodeDescriptionError>;

    /// Stops serving, drains in-flight commits, releases the node lease, then follows as a standby.
    ///
    /// When `expected_node_epoch` is set, compare it with this process's node epoch under the same
    /// guard that starts the step down. On a mismatch, return
    /// [`ActiveNodeStepDownError::NodeEpochMismatch`] and change nothing.
    ///
    /// After the release, the process waits for the reported hold-off before it competes for the
    /// node lease again. Meanwhile it watches the lease and returns as soon as it sees another holder,
    /// with [`LeaseHolderAfterStepDown::TakenOverByAnotherProcess`]. When the hold-off ends, it makes
    /// one acquisition attempt. Success returns [`LeaseHolderAfterStepDown::TakenBackByThisProcess`]
    /// with the new node epoch. Finding another holder returns `TakenOverByAnotherProcess`. Return
    /// [`LeaseHolderAfterStepDown::Unknown`] when neither is confirmed, for example after a failed
    /// lease read. So a call lasts at most the commit drain bound, the release, the hold-off, and
    /// one lease acquisition attempt.
    ///
    /// Stepping down must not send GOAWAY on, or close, the connection that carries the admin call,
    /// so the caller receives the result. The handler runs this future in a task of its own, so a
    /// client deadline or disconnect never drops a started step down.
    ///
    /// Returns [`ActiveNodeStepDownError::NotActive`], `NodeEpochMismatch`, or `AlreadySteppingDown`
    /// without changing anything. Returns `LeaseReleaseUnconfirmed` after serving stopped when the
    /// release write was not confirmed: the process stops renewing the lease, which may stay held
    /// until it expires, and continues as a standby. Returns `Internal` for any other failure: the
    /// process has stopped serving, never serves again, stops renewing the lease, and exits after
    /// returning the error, as after losing the lease.
    async fn step_down_active_node(
        &self,
        expected_node_epoch: Option<u64>,
    ) -> Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError>;
}

/// The node lease that the active server process holds.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ActiveNodeDescription {
    /// Node ID that the process serves.
    pub node_id: String,
    /// Node epoch that the process raised when it acquired the node lease.
    pub node_epoch: u64,
    /// Random ID that the process recorded as the node lease holder.
    pub holder_process_id: String,
}

/// Why this process cannot describe the active process.
#[derive(Clone, Debug, PartialEq, Eq, thiserror::Error)]
#[non_exhaustive]
pub enum ActiveNodeDescriptionError {
    /// This process does not hold the node lease, for example while it starts, stands by, or steps down.
    #[error("this server process is not the active process of its node ID: {detail}")]
    NotActive {
        /// What this process is doing instead.
        detail: String,
    },
}

/// What a completed step down released and which process held the node lease afterward.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ActiveNodeStepDownOutcome {
    /// Node ID whose active process stepped down.
    pub node_id: String,
    /// Node epoch of the released node lease.
    pub released_node_epoch: u64,
    /// Wait before the stepped-down process competes for the node lease again.
    pub lease_reacquisition_hold_off: Duration,
    /// Which process held the node lease when the step down returned.
    pub lease_holder_after_step_down: LeaseHolderAfterStepDown,
}

/// Which process held the node lease when a step down returned.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum LeaseHolderAfterStepDown {
    /// Another process of the same node ID acquired the node lease.
    TakenOverByAnotherProcess(NodeLeaseHolder),
    /// No other process acquired the lease within the hold-off, so this process took it back.
    TakenBackByThisProcess(NodeLeaseHolder),
    /// Neither a takeover nor a take-back was confirmed before the step down returned.
    Unknown {
        /// What could not be confirmed.
        detail: String,
    },
}

/// The process that holds a node lease.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NodeLeaseHolder {
    /// Node epoch that the process raised when it acquired the node lease.
    pub node_epoch: u64,
    /// Random ID that the process recorded as the node lease holder.
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
    /// The request's expected node epoch differs from the node epoch this process holds.
    #[error(
        "the active server process holds node epoch {active_node_epoch}, not the expected node epoch {expected_node_epoch}"
    )]
    NodeEpochMismatch {
        /// Node epoch that the request expected.
        expected_node_epoch: u64,
        /// Node epoch of the node lease that this process holds.
        active_node_epoch: u64,
    },
    /// An earlier step down of this process has not finished.
    #[error("a step down of this server process is already in progress")]
    AlreadySteppingDown,
    /// Serving stopped, but the release write was not confirmed.
    #[error("the node lease release was not confirmed: {detail}")]
    LeaseReleaseUnconfirmed {
        /// Why the release was not confirmed.
        detail: String,
    },
    /// The step down failed for another reason, and the process stops.
    #[error("the step down failed: {detail}")]
    Internal {
        /// What failed.
        detail: String,
    },
}
