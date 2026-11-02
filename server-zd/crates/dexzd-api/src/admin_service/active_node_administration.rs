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

    /// Asks the serve loop to stop serving, drain in-flight commits, release the node lease, then follow as a standby.
    ///
    /// The node's lifecycle owner, the serve loop, performs the step down. This future only submits
    /// the request to it and awaits the result. So the serve loop orders a step down with a graceful
    /// SIGTERM shutdown, and dropping this future never cancels a submitted step down.
    ///
    /// The serve loop checks `precondition` under the same guard that starts the step down, with
    /// [`StepDownPrecondition::is_satisfied_by`]. On a mismatch, return
    /// [`ActiveNodeStepDownError::PreconditionMismatch`] and change nothing.
    ///
    /// After the release, the process waits for the reported hold-off before it competes for the
    /// node lease again. Meanwhile it watches the lease and returns as soon as it sees another holder,
    /// with [`LeaseHolderAfterStepDown::TakenOverByAnotherProcess`]. When the hold-off ends, it makes
    /// one acquisition attempt. Success returns [`LeaseHolderAfterStepDown::TakenBackByThisProcess`]
    /// with the new node epoch. Finding another holder returns `TakenOverByAnotherProcess`. Return
    /// [`LeaseHolderAfterStepDown::Unknown`] when neither is confirmed, for example after a failed
    /// lease read. So a call lasts at most the commit drain bound, the release, the hold-off, and
    /// one lease acquisition attempt. The exception is a stuck task of the stopped serving period
    /// that keeps the local store open for about a minute after the release: the process then
    /// returns `Internal` without competing for the lease, and exits.
    ///
    /// Stepping down must not send GOAWAY on, or close, the connection that carries the admin call,
    /// so the caller receives the result.
    ///
    /// Returns [`ActiveNodeStepDownError::NotActive`], `PreconditionMismatch`, or
    /// `AlreadySteppingDown` without changing anything. Returns `LeaseReleaseUnconfirmed` after
    /// serving stopped when the release write was not confirmed: the process stops renewing the
    /// lease, which may stay held until it expires, and continues as a standby. Returns `Internal`
    /// for any other failure. The process has then stopped serving and renewing the lease, and never
    /// serves again. After handing the error to this future, the serve loop waits a short grace
    /// period, so the handler can pass the error to the transport, and then exits the process, as
    /// after losing the lease.
    async fn step_down_active_node(
        &self,
        precondition: StepDownPrecondition,
    ) -> Result<ActiveNodeStepDownOutcome, ActiveNodeStepDownError>;
}

/// The node lease that a step down request expects the active process to hold.
///
/// A step down without a precondition steps down whichever process is active when it arrives.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct StepDownPrecondition {
    /// Node ID that the active process must serve; `None` skips the check. Never empty.
    pub expected_node_id: Option<String>,
    /// Node epoch of the lease that the active process must hold; `None` skips the check.
    pub expected_node_epoch: Option<u64>,
}

impl StepDownPrecondition {
    /// Returns true when an active process of `node_id` holding `node_epoch` matches every set field.
    pub fn is_satisfied_by(&self, node_id: &str, node_epoch: u64) -> bool {
        self.expected_node_id
            .as_deref()
            .is_none_or(|expected_node_id| expected_node_id == node_id)
            && self
                .expected_node_epoch
                .is_none_or(|expected_node_epoch| expected_node_epoch == node_epoch)
    }
}

impl std::fmt::Display for StepDownPrecondition {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match (&self.expected_node_id, self.expected_node_epoch) {
            (Some(node_id), Some(node_epoch)) => {
                write!(formatter, "node ID {node_id:?} at node epoch {node_epoch}")
            }
            (Some(node_id), None) => write!(formatter, "node ID {node_id:?}"),
            (None, Some(node_epoch)) => write!(formatter, "node epoch {node_epoch}"),
            (None, None) => write!(formatter, "any active process"),
        }
    }
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
    /// The request's precondition does not match the node lease this process holds.
    #[error(
        "the active server process serves node ID {active_node_id:?} at node epoch {active_node_epoch}, not the expected {expected}"
    )]
    PreconditionMismatch {
        /// What the request expected.
        expected: StepDownPrecondition,
        /// Node ID that this process serves.
        active_node_id: String,
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
