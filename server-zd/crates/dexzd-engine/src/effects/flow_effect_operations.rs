// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! What the effect runner needs from the Flow runtime: proposals, lock-checked attempt starts,
//! and state reads. The runtime registry implements [`FlowEffectOperations`].

use dexzd_apply::FlowStateReader;
use dexzd_keyspace::RunId;
use dexzd_protocol::dex::PullRouting;
use dexzd_protocol::dexzd::v1::{CommandReply, FlowCommand, StepInvocationKey};
use futures::future::BoxFuture;

use crate::flow_coordination::{CommitError, CommitUrgency, FlowAddress};

/// A proposal or read the runtime could not serve.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum FlowEffectOperationError {
    /// Backpressure or an object storage outage; retry later.
    #[error("runtime is unavailable: {detail}")]
    Unavailable {
        /// What was unavailable.
        detail: String,
    },
    /// The node is stopping; effect work ends.
    #[error("node is stopping: {detail}")]
    NodeStopping {
        /// Why the node stops.
        detail: String,
    },
}

impl From<CommitError> for FlowEffectOperationError {
    fn from(error: CommitError) -> Self {
        match error {
            CommitError::Unavailable { detail } => Self::Unavailable { detail },
            CommitError::NodeStopping { detail } => Self::NodeStopping { detail },
        }
    }
}

/// Result of a lock-checked attempt start.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum InvocationStartOutcome {
    /// `StepAttemptStarted` was proposed; its `InvocationStarted` effect follows.
    Started,
    /// A lock key is held, or an earlier waiter queues on it; retry after locks are released.
    LocksBusy,
    /// The invocation is no longer pending with this attempt.
    Stale,
}

/// Flow runtime operations used by the effect runner.
pub trait FlowEffectOperations: Send + Sync + 'static {
    /// Proposes `command` to `flow` and waits until it is committed and applied.
    ///
    /// Returns apply's reply on committed state, or [`FlowEffectOperationError`] when the command was
    /// not committed.
    fn propose_committed<'a>(
        &'a self,
        flow: &'a FlowAddress,
        command: FlowCommand,
        urgency: CommitUrgency,
    ) -> BoxFuture<'a, Result<CommandReply, FlowEffectOperationError>>;

    /// Proposes `command` to `flow` and returns once it is applied to the speculative overlay.
    ///
    /// Returns the speculative reply, or [`FlowEffectOperationError`] when nothing was proposed.
    fn propose_speculative<'a>(
        &'a self,
        flow: &'a FlowAddress,
        command: FlowCommand,
        urgency: CommitUrgency,
    ) -> BoxFuture<'a, Result<CommandReply, FlowEffectOperationError>>;

    /// Waits until every command proposed to `flow` before the call commits.
    ///
    /// Returns [`FlowEffectOperationError::NodeStopping`] when the runtime halts while waiting.
    fn sync_barrier<'a>(
        &'a self,
        flow: &'a FlowAddress,
    ) -> BoxFuture<'a, Result<(), FlowEffectOperationError>>;

    /// Waits until no transactional or locking RPC that this node admitted for `flow` is in
    /// flight. A restarted node has none.
    ///
    /// Returns [`FlowEffectOperationError::NodeStopping`] when the runtime halts while waiting.
    fn wait_for_transactional_rpcs<'a>(
        &'a self,
        flow: &'a FlowAddress,
    ) -> BoxFuture<'a, Result<(), FlowEffectOperationError>>;

    /// Inside the Flow's actor: proposes `StepAttemptStarted` with `task_id` and `pull_routing`
    /// when every lock key of the invocation is free in the overlay lock table and the
    /// in-memory RPC locks, and no earlier waiter queues on any of its keys. The urgency
    /// follows the method's durability.
    ///
    /// Returns whether the attempt started, or [`FlowEffectOperationError`] when nothing was proposed.
    #[expect(
        clippy::too_many_arguments,
        reason = "A lock-checked start names its Flow, run, call, attempt, and dispatch."
    )]
    fn try_start_invocation<'a>(
        &'a self,
        flow: &'a FlowAddress,
        run_id: RunId,
        run_attempt: u32,
        invocation: &'a StepInvocationKey,
        attempt: u32,
        task_id: Vec<u8>,
        pull_routing: PullRouting,
    ) -> BoxFuture<'a, Result<InvocationStartOutcome, FlowEffectOperationError>>;

    /// Returns a read view of `flow`'s state, including uncommitted speculative commands when
    /// `should_include_speculative` is true.
    ///
    /// Returns [`FlowEffectOperationError`] when the local store cannot open a view.
    fn flow_state_reader(
        &self,
        flow: &FlowAddress,
        should_include_speculative: bool,
    ) -> Result<Box<dyn FlowStateReader + Send>, FlowEffectOperationError>;

    /// Returns a consistent read view of committed state of every partition.
    ///
    /// Returns [`FlowEffectOperationError`] when the local store cannot open a view.
    fn committed_state_reader(
        &self,
    ) -> Result<Box<dyn FlowStateReader + Send>, FlowEffectOperationError>;
}
