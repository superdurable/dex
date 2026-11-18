// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Stream events of the slot state machine: acknowledgements, tasks, rejections, and ends.

use dex_protocol::dex::PollWorkerSlotKind;
use tonic::Code;

use super::slot_stream_state::PullSlotStreamState;
use super::slot_streams::{SlotMachine, SlotPhase, default_reserved_rpc_tasks};

const RPC_ONLY_SLOT_KIND: &str = "RPC_ONLY";

impl SlotMachine<'_> {
    /// Applies D4 and F6 at the first acknowledgement; returns the start failure message.
    pub(super) fn resolve_ceiling_at_first_acknowledgement(
        &mut self,
        limit: u32,
    ) -> Result<(), String> {
        let has_limit = limit != 0;
        let config = &self.state.config;
        if !config.is_adaptive && config.is_max_concurrent_tasks_explicit {
            if has_limit && config.max_concurrent_tasks > limit as usize {
                return Err(format!(
                    "max_concurrent_tasks {} exceeds Server matching.maxConcurrentTasksCap {limit}",
                    config.max_concurrent_tasks
                ));
            }
            return Ok(());
        }
        let configured = config.configured_max_concurrent_tasks;
        let mut ceiling = configured;
        if has_limit && ceiling > limit as usize {
            ceiling = limit as usize;
            tracing::warn!(
                app_worker_id = %config.app_worker_id,
                max_concurrent_tasks = configured,
                max_concurrent_tasks_per_app_worker = limit,
                is_max_concurrent_tasks_explicit = config.is_max_concurrent_tasks_explicit,
                "clamped max_concurrent_tasks {configured} to the Server matching.maxConcurrentTasksCap {limit}"
            );
        }
        let min_open_slots = if config.is_min_open_slots_explicit {
            config.min_open_slots
        } else {
            ceiling
        };
        let reserved_rpc_tasks = if config.is_reserved_rpc_tasks_explicit {
            config.reserved_rpc_tasks
        } else {
            default_reserved_rpc_tasks(ceiling, min_open_slots)
        };
        if reserved_rpc_tasks >= ceiling {
            return Err(format!(
                "reserved_rpc_tasks {reserved_rpc_tasks} must be less than the slot ceiling {ceiling}, \
                 which is max_concurrent_tasks {configured} limited by Server matching.maxConcurrentTasksCap {limit}"
            ));
        }
        if min_open_slots > ceiling {
            return Err(format!(
                "min_open_slots {min_open_slots} exceeds the slot ceiling {ceiling}, \
                 which is max_concurrent_tasks {configured} limited by Server matching.maxConcurrentTasksCap {limit}"
            ));
        }
        let config = &mut self.state.config;
        config.max_concurrent_tasks = ceiling;
        config.min_open_slots = min_open_slots;
        config.reserved_rpc_tasks = reserved_rpc_tasks;
        self.state.slots.step_floor = min_open_slots - reserved_rpc_tasks;
        self.state.slots.total_capacity = ceiling;
        if reserved_rpc_tasks == 0 {
            self.registration.remove_slot_kind(RPC_ONLY_SLOT_KIND);
        }
        Ok(())
    }

    /// The registered event of the state machine.
    pub(super) fn registered(&mut self, stream_id: u64, limit: u32) {
        let Some(slot) = self.state.streams.get_mut(&stream_id) else {
            return;
        };
        slot.attempt.has_received_registered = true;
        if slot.attempt.phase == SlotPhase::Registering {
            self.set_phase(stream_id, SlotPhase::Idle);
        }
        if self.state.config.is_adaptive {
            let mut capacity = self.state.config.max_concurrent_tasks;
            if limit != 0 && (limit as usize) < capacity {
                capacity = limit as usize;
            }
            self.state.slots.total_capacity = capacity;
        }
        let reserved_rpc_tasks = self.state.config.reserved_rpc_tasks;
        let total_capacity = self.state.slots.total_capacity;
        let mut rpc_only_slots = reserved_rpc_tasks;
        if rpc_only_slots + 1 > total_capacity {
            rpc_only_slots = total_capacity.saturating_sub(1);
        }
        if rpc_only_slots < self.state.slots.rpc_only_slots {
            tracing::warn!(
                app_worker_id = %self.state.config.app_worker_id,
                reserved_rpc_tasks,
                rpc_only_slots,
                max_concurrent_tasks_per_app_worker = limit,
                "Server matching.maxConcurrentTasksCap leaves fewer RPC-only slots than reserved_rpc_tasks"
            );
        }
        self.state.slots.rpc_only_slots = rpc_only_slots;
        self.close_surplus();
        self.recompute_capacity();
        self.maybe_reopen(None);
        if self.state.slots.step_counts.registering == 0 {
            self.settle_growth_batch();
        }
    }

    /// The task_frame event.
    pub(super) fn task_started(&mut self, stream_id: u64) {
        let Some(slot) = self.state.streams.get(&stream_id) else {
            return;
        };
        if slot.attempt.phase == SlotPhase::Untracked {
            return;
        }
        let slot_kind = slot.slot_kind;
        self.set_phase(stream_id, SlotPhase::Busy);
        if slot_kind == PollWorkerSlotKind::RpcOnly {
            return;
        }
        let slots = &mut self.state.slots;
        slots.peak_busy = slots.peak_busy.max(slots.step_counts.busy);
        self.maybe_grow();
    }

    /// Counts a task dropped on a stream that the Worker chose to close.
    pub(super) fn task_dropped(&mut self, stream_id: u64) {
        let is_closed_on_purpose = self
            .state
            .streams
            .get(&stream_id)
            .is_some_and(|slot| slot.attempt.is_closed_on_purpose);
        if is_closed_on_purpose && !self.state.is_stopping {
            self.state.slots.shrink_race_drops += 1;
        }
    }

    /// Keeps a stream that the Server asked to reconnect out of the idle set.
    pub(super) fn reconnect_requested(&mut self, stream_id: u64) {
        if self
            .state
            .streams
            .get(&stream_id)
            .is_some_and(|slot| slot.attempt.phase == SlotPhase::Idle)
        {
            self.set_phase(stream_id, SlotPhase::Reconnecting);
        }
    }

    /// The final_frame_sent event, or orphaned_handler_returned after the stream ended.
    ///
    /// Returns true when the stream must half-close because it is surplus after a lowered cap.
    pub(super) fn task_completed(&mut self, stream_id: u64, is_draining: bool) -> bool {
        let Some(slot) = self.state.streams.get_mut(&stream_id) else {
            return false;
        };
        let slot_kind = slot.slot_kind;
        if slot.attempt.is_orphaned {
            slot.attempt.is_orphaned = false;
            if slot_kind == PollWorkerSlotKind::RpcOnly {
                self.state.slots.orphaned_rpc_only_handlers -= 1;
            } else {
                self.state.slots.orphaned_step_handlers -= 1;
            }
            self.recompute_capacity();
            let candidate = self.reopen_candidate(stream_id);
            self.maybe_reopen(candidate);
            return false;
        }
        if slot.attempt.phase != SlotPhase::Busy {
            return false;
        }
        if is_draining {
            self.set_phase(stream_id, SlotPhase::Reconnecting);
            return false;
        }
        self.set_phase(stream_id, SlotPhase::Idle);
        let slots = &self.state.slots;
        let is_surplus = if slot_kind == PollWorkerSlotKind::RpcOnly {
            self.rpc_only_usage() > slots.rpc_only_slots
        } else {
            self.step_usage() > slots.total_capacity.saturating_sub(slots.rpc_only_slots)
        };
        if !is_surplus {
            return false;
        }
        self.half_close(stream_id);
        self.state.slots.streams_shrunk += 1;
        self.recompute_capacity();
        self.maybe_reopen(None);
        true
    }

    /// Applies F5: only an adaptive Worker softens `RESOURCE_EXHAUSTED`, and only for a growth
    /// stream or a reconnect after readiness that never registered.
    pub(super) fn is_soft_rejection(&self, stream_id: u64, code: Code) -> bool {
        if !self.state.config.is_adaptive || code != Code::ResourceExhausted {
            return false;
        }
        let Some(slot) = self.state.streams.get(&stream_id) else {
            return false;
        };
        let attempt = &slot.attempt;
        if attempt.has_received_registered || attempt.phase != SlotPhase::Registering {
            return false;
        }
        attempt.is_growth || attempt.is_opened_after_readiness
    }

    /// The soft registration_rejected event.
    pub(super) fn registration_rejected(&mut self, stream_id: u64) {
        let Some(slot) = self.state.streams.get_mut(&stream_id) else {
            return;
        };
        slot.attempt.is_soft_rejected = true;
        let is_growth = slot.attempt.is_growth;
        self.set_phase(stream_id, SlotPhase::Untracked);
        let slots = &mut self.state.slots;
        if is_growth {
            slots.has_batch_rejection = true;
        }
        slots.grow_rejected_at_cap += 1;
        slots.is_reopen_deferred = true;
        self.recompute_capacity();
        if self.state.slots.step_counts.registering == 0 {
            self.settle_growth_batch();
        }
    }

    /// The stream_ended event; every end of an attempt passes here once.
    pub(super) fn stream_ended(&mut self, stream_id: u64) {
        let Some(slot) = self.state.streams.get_mut(&stream_id) else {
            return;
        };
        if slot.attempt.has_ended {
            return;
        }
        slot.attempt.has_ended = true;
        let phase = slot.attempt.phase;
        let slot_kind = slot.slot_kind;
        if phase != SlotPhase::Untracked {
            if phase == SlotPhase::Busy {
                slot.attempt.is_orphaned = true;
                if slot_kind == PollWorkerSlotKind::RpcOnly {
                    self.state.slots.orphaned_rpc_only_handlers += 1;
                } else {
                    self.state.slots.orphaned_step_handlers += 1;
                }
            }
            self.set_phase(stream_id, SlotPhase::Untracked);
        }
        let Some(slot) = self.state.streams.get(&stream_id) else {
            return;
        };
        let is_orphaned = slot.attempt.is_orphaned;
        if slot.attempt.has_received_registered {
            self.state.slots.is_reopen_deferred = false;
        }
        self.recompute_capacity();
        let parked = if is_orphaned {
            None
        } else {
            self.reopen_candidate(stream_id)
        };
        self.maybe_reopen(parked);
        self.maybe_grow();
        if self.state.slots.step_counts.registering == 0 {
            self.settle_growth_batch();
        }
    }

    /// Ends an attempt whose runner exits before the attempt ends.
    pub(super) fn abandon(&mut self, stream_id: u64) {
        let Some(slot) = self.state.streams.get_mut(&stream_id) else {
            return;
        };
        if slot.attempt.phase == SlotPhase::Untracked || slot.attempt.has_ended {
            return;
        }
        slot.attempt.is_closed_on_purpose = true;
        self.stream_ended(stream_id);
    }

    /// Checks an attempt before its runner opens a stream.
    ///
    /// Returns false when the attempt no longer fits and was revoked, or when it already ended.
    pub(super) fn admit(&mut self, stream_id: u64) -> bool {
        let Some(slot) = self.state.streams.get(&stream_id) else {
            return false;
        };
        if self.state.is_stopping
            || slot.attempt.phase != SlotPhase::Registering
            || slot.attempt.has_ended
        {
            return false;
        }
        if !self.is_attempt_over_capacity(stream_id) {
            return true;
        }
        self.revoke(stream_id);
        self.recompute_capacity();
        self.maybe_reopen(None);
        if self.state.slots.step_counts.registering == 0 {
            self.settle_growth_batch();
        }
        false
    }

    /// Re-checks the attempt and marks its Register as sent; the caller sends after the lock.
    ///
    /// A cap drop after this check reaches the Server, which enforces the cap; an adaptive Worker
    /// treats that rejection as soft. Returns false when the attempt no longer fits.
    pub(super) fn begin_register_send(&mut self, stream_id: u64) -> bool {
        if !self.admit(stream_id) {
            return false;
        }
        if let Some(slot) = self.state.streams.get_mut(&stream_id) {
            slot.attempt.is_register_sent = true;
        }
        true
    }

    /// Checks one registering attempt, which the totals already count.
    fn is_attempt_over_capacity(&self, stream_id: u64) -> bool {
        let slots = &self.state.slots;
        if self.step_usage() + self.rpc_only_usage() > slots.total_capacity {
            return true;
        }
        let is_rpc_only = self
            .state
            .streams
            .get(&stream_id)
            .is_some_and(|slot| slot.slot_kind == PollWorkerSlotKind::RpcOnly);
        if is_rpc_only {
            return self.rpc_only_usage() > slots.rpc_only_slots;
        }
        // F5 gives the first Step-and-RPC stream any free slot, so only later ones need the Step quota.
        self.step_open() > 1 && self.step_usage() > slots.effective_ceiling
    }

    /// Reports whether the runner of `stream_id` got the next attempt, and consumes that grant.
    pub(super) fn take_reopen_grant(&mut self, stream_id: u64) -> bool {
        let Some(slot) = self.state.streams.get_mut(&stream_id) else {
            return false;
        };
        std::mem::replace(&mut slot.attempt.is_reopen_granted, false)
    }

    /// Returns the public snapshot of the slot streams.
    pub(super) fn snapshot(&self) -> PullSlotStreamState {
        let slots = &self.state.slots;
        PullSlotStreamState {
            is_adaptive: self.state.config.is_adaptive,
            open: self.step_open(),
            registering: slots.step_counts.registering,
            busy: slots.step_counts.busy,
            idle: slots.step_counts.idle,
            orphaned_handlers: slots.orphaned_step_handlers,
            orphaned_rpc_only_handlers: slots.orphaned_rpc_only_handlers,
            floor: slots.effective_floor,
            effective_ceiling: slots.effective_ceiling,
            growth_target: slots.growth_target,
            total_capacity: slots.total_capacity,
            rpc_only_open: slots.rpc_only_counts.open(),
            rpc_only_slots: slots.rpc_only_slots,
            streams_grown: slots.streams_grown,
            streams_shrunk: slots.streams_shrunk,
            grow_rejected_at_cap: slots.grow_rejected_at_cap,
            shrink_race_drops: slots.shrink_race_drops,
        }
    }
}
