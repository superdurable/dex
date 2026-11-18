// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/// Independent snapshot of a pull Worker's slot streams.
///
/// Obtain it with [`crate::Worker::pull_slot_stream_state`], including concurrently with
/// [`crate::Worker::start`], [`crate::Worker::stop`], and running handlers. It is a copy;
/// changing it does not change the Worker. Every count is a number of slot streams or handlers,
/// in the same unit as [`crate::PullWorkerOptions::max_concurrent_tasks`].
///
/// [`Self::open`], [`Self::registering`], [`Self::busy`], and [`Self::idle`] count Step-and-RPC
/// streams only, the streams that accept both Steps and RPCs. [`Self::rpc_only_open`] counts the
/// reserved RPC-only streams. While every stream is registered and no handler outlives its stream,
/// an adaptive Worker, whose [`crate::PullWorkerOptions::min_open_slots`] is below
/// `max_concurrent_tasks`, keeps [`Self::open`] between [`Self::floor`] and
/// [`Self::effective_ceiling`]. A fixed Worker keeps [`Self::open`] equal to
/// [`Self::effective_ceiling`]. Reconnects, orphaned handlers, and a lowered Server cap move
/// [`Self::open`] outside that range until the Worker converges again.
///
/// An [`Self::open`] of zero with [`Self::orphaned_handlers`] above zero means that the Worker
/// holds no live Step-and-RPC stream and waits for running handlers to return, for example after
/// a Server restart. [`crate::Worker::pull_registration_state`] does not report that gap, because
/// readiness stays latched across reconnects.
///
/// New fields may be added in later releases, so the type cannot be built with a struct literal
/// outside the SDK.
///
/// Since 1.45.7.
///
/// ```no_run
/// # fn report(worker: &dex_sdk::Worker) {
/// let state = worker.pull_slot_stream_state();
/// println!(
///     "open={} busy={} idle={} orphaned={} capacity={}",
///     state.open, state.busy, state.idle, state.orphaned_handlers, state.total_capacity
/// );
/// # }
/// ```
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
#[non_exhaustive]
pub struct PullSlotStreamState {
    /// Whether the Worker varies its open Step-and-RPC streams, which is when
    /// [`crate::PullWorkerOptions::min_open_slots`] is below `max_concurrent_tasks`.
    pub is_adaptive: bool,
    /// The Step-and-RPC streams that are registered or registering and not closing.
    pub open: usize,
    /// The Step-and-RPC streams that wait for the Server's acknowledgement, including a reconnect
    /// that waits for its backoff.
    pub registering: usize,
    /// The Step-and-RPC streams that run a task.
    pub busy: usize,
    /// The registered Step-and-RPC streams that wait for a task.
    pub idle: usize,
    /// The Step-and-RPC handlers that still run after their stream ended.
    ///
    /// Each one keeps its slot, so the Worker does not register a stream for it until it returns.
    pub orphaned_handlers: usize,
    /// The RPC-only handlers that still run after their stream ended.
    pub orphaned_rpc_only_handlers: usize,
    /// The number of Step-and-RPC streams the Worker keeps open at rest.
    ///
    /// It is `min_open_slots` minus the reserved RPC slots, limited by [`Self::effective_ceiling`].
    pub floor: usize,
    /// The most Step-and-RPC streams and orphaned handlers the Worker may hold.
    ///
    /// It is [`Self::total_capacity`] minus the RPC-only slots, or minus the RPC-only streams and
    /// handlers when they exceed those slots.
    pub effective_ceiling: usize,
    /// The Step-and-RPC stream count that growth may reach.
    ///
    /// It equals [`Self::effective_ceiling`], or less for the rest of a shrink window after the
    /// Server rejected a growth stream at its stream limit.
    pub growth_target: usize,
    /// The Worker's slot count: `max_concurrent_tasks`, limited by the per-Worker cap that the
    /// Server reported in its last acknowledgement.
    pub total_capacity: usize,
    /// The RPC-only streams that are registered or registering and not closing.
    pub rpc_only_open: usize,
    /// The number of RPC-only streams the Worker keeps.
    ///
    /// It is `reserved_rpc_tasks`, or fewer when the Server cap leaves no room for them next to
    /// one Step-and-RPC stream.
    pub rpc_only_slots: usize,
    /// The Step-and-RPC streams opened by growth since the Worker started.
    pub streams_grown: u64,
    /// The streams that the Worker half-closed because it had more than it needed.
    pub streams_shrunk: u64,
    /// The registrations that the Server rejected with `RESOURCE_EXHAUSTED` and that the adaptive
    /// Worker treated as a soft limit instead of a permanent rejection.
    pub grow_rejected_at_cap: u64,
    /// The tasks that arrived on a stream after the Worker decided to close it.
    ///
    /// The Server returns each such task as not delivered and dispatches it again without spending
    /// a retry.
    pub shrink_race_drops: u64,
}
