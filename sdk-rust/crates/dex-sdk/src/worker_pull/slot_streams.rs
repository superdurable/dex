// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The slot stream state machine of one pull Worker: counts, growth, reopening, and shrinking.

use std::future::Future;
use std::pin::Pin;
use std::sync::Arc;
use std::time::Duration;

use dex_protocol::dex::PollWorkerSlotKind;
use tokio::time::Instant;

use super::{PullState, StreamSlot};
use crate::context::InvocationCancellation;
use crate::pull_registration::PullRegistration;

/// W, the shrink window of decision D6.
pub(super) const DEFAULT_SHRINK_WINDOW: Duration = Duration::from_secs(30);

/// A test hook that runs after a slot stream's admission and before its Register is sent.
pub(crate) type RegisterSendHook =
    Arc<dyn Fn() -> Pin<Box<dyn Future<Output = ()> + Send>> + Send + Sync>;

/// Slot stream timings and test hooks; only tests change them.
#[derive(Clone)]
pub(crate) struct PullSlotSettings {
    pub(crate) shrink_window: Duration,
    pub(crate) before_register_send: Option<RegisterSendHook>,
    /// A test latch that holds the Register in the request body after the stream opens.
    pub(crate) register_send_latch: Option<RegisterSendHook>,
}

impl Default for PullSlotSettings {
    fn default() -> Self {
        Self {
            shrink_window: DEFAULT_SHRINK_WINDOW,
            before_register_send: None,
            register_send_latch: None,
        }
    }
}

impl std::fmt::Debug for PullSlotSettings {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("PullSlotSettings")
            .field("shrink_window", &self.shrink_window)
            .field(
                "has_before_register_send",
                &self.before_register_send.is_some(),
            )
            .field(
                "has_register_send_latch",
                &self.register_send_latch.is_some(),
            )
            .finish()
    }
}

/// The phase of one attempt; an untracked attempt counts in no total.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) enum SlotPhase {
    Untracked,
    Registering,
    Idle,
    Busy,
    /// A stream draining after the Server's Reconnect; it takes no task.
    Reconnecting,
}

/// Per-phase totals of one slot kind.
#[derive(Clone, Copy, Debug, Default)]
pub(super) struct SlotPhaseCounts {
    pub(super) registering: usize,
    pub(super) idle: usize,
    pub(super) busy: usize,
    pub(super) reconnecting: usize,
}

impl SlotPhaseCounts {
    fn counter_mut(&mut self, phase: SlotPhase) -> Option<&mut usize> {
        match phase {
            SlotPhase::Untracked => None,
            SlotPhase::Registering => Some(&mut self.registering),
            SlotPhase::Idle => Some(&mut self.idle),
            SlotPhase::Busy => Some(&mut self.busy),
            SlotPhase::Reconnecting => Some(&mut self.reconnecting),
        }
    }

    /// Returns the streams of this kind that are registered or registering and not closing.
    pub(super) fn open(&self) -> usize {
        self.registering + self.idle + self.busy + self.reconnecting
    }
}

/// One registration attempt of a slot stream; the pull state lock guards it.
pub(super) struct SlotAttempt {
    pub(super) phase: SlotPhase,
    pub(super) idle_sequence: u64,
    pub(super) is_growth: bool,
    pub(super) is_opened_after_readiness: bool,
    pub(super) has_ended: bool,
    pub(super) has_received_registered: bool,
    /// Set just before the Register leaves; until then close_surplus may revoke the attempt.
    pub(super) is_register_sent: bool,
    pub(super) is_orphaned: bool,
    pub(super) is_closed_on_purpose: bool,
    pub(super) is_soft_rejected: bool,
    pub(super) is_reopen_granted: bool,
    /// Cancelled when the Worker half-closes this attempt's stream on purpose.
    pub(super) close_request: InvocationCancellation,
}

impl SlotAttempt {
    pub(super) fn new(is_growth: bool, is_opened_after_readiness: bool) -> Self {
        Self {
            phase: SlotPhase::Untracked,
            idle_sequence: 0,
            is_growth,
            is_opened_after_readiness,
            has_ended: false,
            has_received_registered: false,
            is_register_sent: false,
            is_orphaned: false,
            is_closed_on_purpose: false,
            is_soft_rejected: false,
            is_reopen_granted: false,
            close_request: InvocationCancellation::new(),
        }
    }
}

/// The resolved slot configuration; the first acknowledgement may lower the ceiling.
pub(super) struct SlotConfig {
    /// T, the resolved ceiling.
    pub(super) max_concurrent_tasks: usize,
    pub(super) configured_max_concurrent_tasks: usize,
    pub(super) reserved_rpc_tasks: usize,
    pub(super) min_open_slots: usize,
    pub(super) is_max_concurrent_tasks_explicit: bool,
    pub(super) is_reserved_rpc_tasks_explicit: bool,
    pub(super) is_min_open_slots_explicit: bool,
    pub(super) is_adaptive: bool,
    pub(super) shrink_window: Duration,
    pub(super) app_worker_id: String,
}

/// The counters and targets of the state machine.
#[derive(Default)]
pub(super) struct SlotStreams {
    /// C, from the last acknowledgement.
    pub(super) total_capacity: usize,
    /// R_eff.
    pub(super) rpc_only_slots: usize,
    /// L, min_open_slots minus R.
    pub(super) step_floor: usize,
    /// L_eff.
    pub(super) effective_floor: usize,
    /// E.
    pub(super) effective_ceiling: usize,
    /// G, never above effective_ceiling.
    pub(super) growth_target: usize,
    pub(super) step_counts: SlotPhaseCounts,
    pub(super) rpc_only_counts: SlotPhaseCounts,
    pub(super) orphaned_step_handlers: usize,
    pub(super) orphaned_rpc_only_handlers: usize,
    pub(super) is_growth_target_clamped: bool,
    pub(super) has_grown_this_window: bool,
    /// The retry latch that a soft rejection sets.
    pub(super) is_reopen_deferred: bool,
    pub(super) has_batch_rejection: bool,
    pub(super) peak_busy: usize,
    pub(super) next_idle_sequence: u64,
    pub(super) shrink_window_deadline: Option<Instant>,
    pub(super) streams_grown: u64,
    pub(super) streams_shrunk: u64,
    pub(super) grow_rejected_at_cap: u64,
    pub(super) shrink_race_drops: u64,
}

/// Returns the default reserved RPC slot count: one when the ceiling and the floor both leave
/// room for it, otherwise zero.
pub(super) fn default_reserved_rpc_tasks(
    max_concurrent_tasks: usize,
    min_open_slots: usize,
) -> usize {
    usize::from(max_concurrent_tasks >= 2 && min_open_slots >= 2)
}

/// The state machine over the locked pull state; readiness comes from the registration.
pub(super) struct SlotMachine<'a> {
    pub(super) state: &'a mut PullState,
    pub(super) registration: &'a PullRegistration,
}

impl SlotMachine<'_> {
    /// Sets C, R_eff, and L from the configuration before any acknowledgement.
    pub(super) fn initialize_capacity(&mut self) {
        let config = &self.state.config;
        self.state.slots.total_capacity = config.max_concurrent_tasks;
        self.state.slots.rpc_only_slots = config.reserved_rpc_tasks;
        self.state.slots.step_floor = config.min_open_slots - config.reserved_rpc_tasks;
        self.recompute_capacity();
    }

    /// Opens the startup streams and starts the shrink window of an adaptive Worker.
    pub(super) fn start(&mut self) {
        if self.state.is_stopping {
            return;
        }
        self.restart_shrink_window();
        self.maybe_reopen(None);
    }

    /// Re-evaluates the reopen rule; it opens nothing that the rule does not allow.
    pub(super) fn reconcile(&mut self) {
        if self.state.is_stopping {
            return;
        }
        self.maybe_reopen(None);
    }

    /// Runs the every-W step when the window deadline has passed.
    pub(super) fn expire_shrink_window(&mut self, now: Instant) {
        if self.state.is_stopping
            || self
                .state
                .slots
                .shrink_window_deadline
                .is_none_or(|deadline| now < deadline)
        {
            return;
        }
        self.restart_shrink_window();
        let slots = &mut self.state.slots;
        slots.is_growth_target_clamped = false;
        slots.has_grown_this_window = false;
        slots.is_reopen_deferred = false;
        self.recompute_capacity();
        self.maybe_reopen(None);
        self.shrink();
        self.state.slots.peak_busy = self.state.slots.step_counts.busy;
        self.maybe_grow();
    }

    /// Marks the Worker stopping, stops the shrink window, and drops streams not yet spawned.
    pub(super) fn begin_stop(&mut self) {
        self.state.is_stopping = true;
        self.state.slots.shrink_window_deadline = None;
        let pending = std::mem::take(&mut self.state.pending_spawns);
        for stream_id in pending {
            self.set_phase(stream_id, SlotPhase::Untracked);
            self.state.streams.remove(&stream_id);
        }
    }

    /// Closes half the idle surplus above the window's peak plus headroom.
    fn shrink(&mut self) {
        let slots = &self.state.slots;
        let peak_busy = slots.peak_busy;
        let headroom = peak_busy.div_ceil(4).max(1);
        let target = slots
            .effective_ceiling
            .min(slots.effective_floor.max(peak_busy + headroom));
        let open = self.step_open();
        if open <= target {
            return;
        }
        let idle_streams = self.idle_step_streams_newest_first();
        if idle_streams.is_empty() {
            return;
        }
        // The last idle stream stays open, and the floor stays open.
        let close_count = (open - target)
            .div_ceil(2)
            .min(idle_streams.len() - 1)
            .min(open.saturating_sub(self.state.slots.effective_floor));
        if close_count == 0 {
            return;
        }
        for stream_id in &idle_streams[..close_count] {
            self.half_close(*stream_id);
        }
        self.state.slots.streams_shrunk += close_count as u64;
        tracing::debug!(
            app_worker_id = %self.state.config.app_worker_id,
            from = open,
            to = open - close_count,
            peak_busy,
            "shrink pull slot streams"
        );
        self.recompute_capacity();
    }

    /// Releases capacity above a lowered cap: RPC-only first, unsent attempts before idle streams.
    pub(super) fn close_surplus(&mut self) {
        for slot_kind in [PollWorkerSlotKind::RpcOnly, PollWorkerSlotKind::StepAndRpc] {
            while self.is_slot_kind_over_quota(slot_kind) {
                if let Some(pending) = self.unsent_attempt(slot_kind) {
                    self.revoke(pending);
                    continue;
                }
                let Some(stream_id) = self.newest_idle_stream(slot_kind) else {
                    break;
                };
                self.half_close(stream_id);
                self.state.slots.streams_shrunk += 1;
            }
        }
    }

    /// Runs when no stream registers; a rejected batch clamps G until the window ends.
    pub(super) fn settle_growth_batch(&mut self) {
        if self.state.slots.has_batch_rejection {
            let usage = self.step_usage();
            let slots = &mut self.state.slots;
            slots.has_batch_rejection = false;
            slots.growth_target = slots.effective_ceiling.min(usage);
            slots.is_growth_target_clamped = true;
            tracing::info!(
                app_worker_id = %self.state.config.app_worker_id,
                growth_target = slots.growth_target,
                effective_ceiling = slots.effective_ceiling,
                "the Server rejected pull slot stream growth at its stream limit; holding growth until the next shrink window"
            );
        }
        self.maybe_grow();
    }

    /// Doubles the open streams when a hand-out took the last idle one.
    pub(super) fn maybe_grow(&mut self) {
        let slots = &self.state.slots;
        if !self.state.config.is_adaptive || self.state.is_stopping || slots.is_reopen_deferred {
            return;
        }
        if slots.is_growth_target_clamped && slots.has_grown_this_window {
            return;
        }
        let open = self.step_open();
        let usage = self.step_usage();
        let slots = &self.state.slots;
        if slots.step_counts.idle != 0
            || slots.step_counts.registering != 0
            || usage >= slots.growth_target
        {
            return;
        }
        let count = (slots.growth_target - usage).min(open.max(1));
        tracing::debug!(
            app_worker_id = %self.state.config.app_worker_id,
            from = open,
            to = open + count,
            reason = "no idle stream",
            "grow pull slot streams"
        );
        for _ in 0..count {
            self.open_slot_stream(PollWorkerSlotKind::StepAndRpc, true, None);
        }
        self.state.slots.streams_grown += count as u64;
        self.state.slots.has_grown_this_window = true;
        self.restart_shrink_window();
    }

    /// Opens streams within free capacity in F5 order.
    ///
    /// A parked runner of the same kind is reused, so it keeps its reconnect backoff.
    pub(super) fn maybe_reopen(&mut self, parked: Option<u64>) {
        if self.state.is_stopping || self.state.slots.is_reopen_deferred {
            return;
        }
        let mut step_open = self.step_open();
        let mut free = self
            .state
            .slots
            .total_capacity
            .saturating_sub(self.step_usage() + self.rpc_only_usage());
        if step_open == 0 && free > 0 {
            self.open_slot_stream(PollWorkerSlotKind::StepAndRpc, false, parked);
            step_open += 1;
            free -= 1;
        }
        if self.is_cap_probe_pending() {
            return;
        }
        if self.state.has_checked_registration_limit || !self.state.config.is_adaptive {
            let count = free.min(
                self.state
                    .slots
                    .rpc_only_slots
                    .saturating_sub(self.rpc_only_usage()),
            );
            for _ in 0..count {
                self.open_slot_stream(PollWorkerSlotKind::RpcOnly, false, parked);
            }
            free -= count;
        }
        let slots = &self.state.slots;
        let step_target = slots.effective_floor.min(
            slots
                .effective_ceiling
                .saturating_sub(slots.orphaned_step_handlers),
        );
        let count = free.min(step_target.saturating_sub(step_open));
        for _ in 0..count {
            self.open_slot_stream(PollWorkerSlotKind::StepAndRpc, false, parked);
        }
    }

    /// Counts a new attempt as registering and grants it to the parked runner when possible.
    fn open_slot_stream(
        &mut self,
        slot_kind: PollWorkerSlotKind,
        is_growth: bool,
        parked: Option<u64>,
    ) {
        let mut attempt =
            SlotAttempt::new(is_growth, self.registration.has_registered_all_slot_kinds());
        if !is_growth
            && let Some(parked_id) = parked
            && let Some(slot) = self.state.streams.get_mut(&parked_id)
            && slot.slot_kind == slot_kind
            && !slot.attempt.is_reopen_granted
        {
            attempt.is_reopen_granted = true;
            slot.attempt = attempt;
            self.set_phase(parked_id, SlotPhase::Registering);
            return;
        }
        let stream_id = self.state.next_stream_id;
        self.state.next_stream_id += 1;
        self.state.streams.insert(
            stream_id,
            StreamSlot {
                is_busy: false,
                is_registered: false,
                is_draining: false,
                slot_kind,
                attempt,
            },
        );
        self.set_phase(stream_id, SlotPhase::Registering);
        self.state.pending_spawns.push(stream_id);
    }

    /// Takes a stream out of the totals and asks its runner to half-close it; its later end
    /// changes no count.
    pub(super) fn half_close(&mut self, stream_id: u64) {
        self.set_phase(stream_id, SlotPhase::Untracked);
        if let Some(slot) = self.state.streams.get_mut(&stream_id) {
            slot.attempt.is_closed_on_purpose = true;
            slot.attempt.close_request.cancel();
        }
    }

    /// Takes back an attempt whose Register has not left; its runner then exits.
    pub(super) fn revoke(&mut self, stream_id: u64) {
        if let Some(slot) = self.state.streams.get_mut(&stream_id) {
            slot.attempt.is_closed_on_purpose = true;
            slot.attempt.has_ended = true;
        }
        self.set_phase(stream_id, SlotPhase::Untracked);
    }

    /// Starts a new shrink window; an earlier deadline then does nothing.
    pub(super) fn restart_shrink_window(&mut self) {
        let shrink_window = self.state.config.shrink_window;
        if !self.state.config.is_adaptive || shrink_window.is_zero() {
            return;
        }
        self.state.slots.shrink_window_deadline = Some(Instant::now() + shrink_window);
    }

    /// Moves an attempt between phases and keeps the per-phase totals.
    pub(super) fn set_phase(&mut self, stream_id: u64, phase: SlotPhase) {
        let Some(slot) = self.state.streams.get_mut(&stream_id) else {
            return;
        };
        let slots = &mut self.state.slots;
        let counts = if slot.slot_kind == PollWorkerSlotKind::RpcOnly {
            &mut slots.rpc_only_counts
        } else {
            &mut slots.step_counts
        };
        if let Some(counter) = counts.counter_mut(slot.attempt.phase) {
            *counter -= 1;
        }
        slot.attempt.phase = phase;
        if let Some(counter) = counts.counter_mut(phase) {
            *counter += 1;
        }
        if phase == SlotPhase::Idle {
            slots.next_idle_sequence += 1;
            slot.attempt.idle_sequence = slots.next_idle_sequence;
        }
    }

    /// Derives E, L_eff, and G; RPC-only overflow takes Step capacity.
    pub(super) fn recompute_capacity(&mut self) {
        let rpc_only_reserve = self.state.slots.rpc_only_slots.max(self.rpc_only_usage());
        let slots = &mut self.state.slots;
        slots.effective_ceiling = slots.total_capacity.saturating_sub(rpc_only_reserve);
        slots.effective_floor = slots.step_floor.min(slots.effective_ceiling);
        slots.growth_target = if slots.is_growth_target_clamped {
            slots.growth_target.min(slots.effective_ceiling)
        } else {
            slots.effective_ceiling
        };
    }

    /// Returns `stream_id` when its runner may serve the next attempt of its kind.
    pub(super) fn reopen_candidate(&self, stream_id: u64) -> Option<u64> {
        let slot = self.state.streams.get(&stream_id)?;
        let attempt = &slot.attempt;
        if attempt.is_closed_on_purpose || attempt.is_soft_rejected || attempt.is_reopen_granted {
            return None;
        }
        Some(stream_id)
    }

    /// Lists idle Step-and-RPC streams, the most recently idle first.
    fn idle_step_streams_newest_first(&self) -> Vec<u64> {
        let mut idle_streams: Vec<(u64, u64)> = self
            .state
            .streams
            .iter()
            .filter(|(_, slot)| {
                slot.slot_kind != PollWorkerSlotKind::RpcOnly
                    && slot.attempt.phase == SlotPhase::Idle
            })
            .map(|(stream_id, slot)| (slot.attempt.idle_sequence, *stream_id))
            .collect();
        idle_streams.sort_unstable_by(|left, right| right.cmp(left));
        idle_streams
            .into_iter()
            .map(|(_, stream_id)| stream_id)
            .collect()
    }

    /// Returns a registering attempt of the kind whose Register has not left, or `None`.
    fn unsent_attempt(&self, slot_kind: PollWorkerSlotKind) -> Option<u64> {
        self.state
            .streams
            .iter()
            .filter(|(_, slot)| {
                slot.slot_kind == slot_kind
                    && slot.attempt.phase == SlotPhase::Registering
                    && !slot.attempt.is_register_sent
            })
            .map(|(stream_id, _)| *stream_id)
            .min()
    }

    /// Returns the most recently idle stream of the kind, or `None`.
    fn newest_idle_stream(&self, slot_kind: PollWorkerSlotKind) -> Option<u64> {
        self.state
            .streams
            .iter()
            .filter(|(_, slot)| {
                slot.slot_kind == slot_kind && slot.attempt.phase == SlotPhase::Idle
            })
            .max_by_key(|(_, slot)| slot.attempt.idle_sequence)
            .map(|(stream_id, _)| *stream_id)
    }

    /// Reports surplus of a kind after a lowered cap: RPC-only above R_eff, or Step-and-RPC
    /// above C - R_eff.
    pub(super) fn is_slot_kind_over_quota(&self, slot_kind: PollWorkerSlotKind) -> bool {
        let slots = &self.state.slots;
        if slot_kind == PollWorkerSlotKind::RpcOnly {
            return self.rpc_only_usage() > slots.rpc_only_slots;
        }
        self.step_usage() > slots.total_capacity.saturating_sub(slots.rpc_only_slots)
    }

    /// Reports F4: a fixed Worker with an unset ceiling opens one stream until the first
    /// acknowledgement.
    fn is_cap_probe_pending(&self) -> bool {
        let config = &self.state.config;
        !config.is_adaptive
            && !config.is_max_concurrent_tasks_explicit
            && !self.state.has_checked_registration_limit
    }

    /// Returns S.
    pub(super) fn step_open(&self) -> usize {
        self.state.slots.step_counts.open()
    }

    /// Returns S + H.
    pub(super) fn step_usage(&self) -> usize {
        self.step_open() + self.state.slots.orphaned_step_handlers
    }

    /// Returns Sr + Hr.
    pub(super) fn rpc_only_usage(&self) -> usize {
        self.state.slots.rpc_only_counts.open() + self.state.slots.orphaned_rpc_only_handlers
    }
}
