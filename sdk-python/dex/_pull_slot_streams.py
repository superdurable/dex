# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
import enum
import logging
import os
import threading
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import TYPE_CHECKING

import grpc

from dex.dexpb import dex_pb2 as pb
from dex.pull_registration import PullSlotStreamState
from dex.worker_options import PullWorkerOptions, _default_reserved_rpc_tasks

if TYPE_CHECKING:
    from dex._pull_worker import PullStream, PullWorkerRuntime

_LOGGER = logging.getLogger(__name__)

# W, the shrink window of decision D6.
_DEFAULT_SHRINK_WINDOW_SECONDS = 30.0

STEP_AND_RPC_QUEUE = "STEP_AND_RPC"
RPC_ONLY_QUEUE = "RPC_ONLY"


class _SlotPhase(enum.IntEnum):
    """The phase of one slot stream attempt; untracked attempts count in no total."""

    UNTRACKED = 0
    REGISTERING = 1
    IDLE = 2
    BUSY = 3
    # A stream draining after the Server's Reconnect; it takes no task.
    RECONNECTING = 4


_PHASE_COUNT = len(_SlotPhase)


@dataclass
class _PullSlotAttempt:
    """One registration attempt of a slot stream; the slot lock guards it."""

    phase: _SlotPhase = _SlotPhase.UNTRACKED
    idle_sequence: int = 0
    is_growth: bool = False
    is_opened_after_readiness: bool = False
    has_ended: bool = False
    has_received_registered: bool = False
    # Set under the lock just before the Register write; until then close_surplus may revoke the attempt.
    is_register_sent: bool = False
    is_orphaned: bool = False
    is_closed_on_purpose: bool = False
    is_soft_rejected: bool = False
    is_reopen_granted: bool = False


@dataclass
class _PullSlotSettings:
    """Slot stream timings and test hooks; only tests change them."""

    shrink_window_seconds: float = _DEFAULT_SHRINK_WINDOW_SECONDS
    # Runs after a slot stream opens and before its Register is checked and sent.
    before_register_send_for_test: Callable[[PullStream], Awaitable[None]] | None = None


class _SlotAttemptRevokedError(Exception):
    """Ends a poll whose attempt lost its slot before its Register left."""


class PullSlotStreams:
    """The slot stream state machine of one pull Worker.

    Every method runs on the Worker's event loop thread, except
    ``slot_stream_state``. Each method takes ``lock`` only in synchronous code,
    so no await, Register write, or other blocking I/O happens under it.
    """

    def __init__(self, runtime: PullWorkerRuntime, options: PullWorkerOptions) -> None:
        self._runtime = runtime
        self.lock = threading.Lock()
        self.settings = _PullSlotSettings()
        configured_max_concurrent_tasks = (
            options.max_concurrent_tasks or os.cpu_count() or 1
        )
        min_open_slots = options.min_open_slots or configured_max_concurrent_tasks
        reserved_rpc_tasks = (
            options.reserved_rpc_tasks
            if options.reserved_rpc_tasks is not None
            else _default_reserved_rpc_tasks(
                configured_max_concurrent_tasks, min_open_slots
            )
        )
        self.configured_max_concurrent_tasks = configured_max_concurrent_tasks
        self.max_concurrent_tasks = configured_max_concurrent_tasks
        self.min_open_slots = min_open_slots
        self.reserved_rpc_tasks = reserved_rpc_tasks
        self.is_max_concurrent_tasks_explicit = options.max_concurrent_tasks != 0
        self.is_min_open_slots_explicit = options.min_open_slots != 0
        self.is_reserved_rpc_tasks_explicit = options.reserved_rpc_tasks is not None
        self.is_adaptive = min_open_slots < configured_max_concurrent_tasks
        self.has_checked_registration_limit = False
        # C, from the last acknowledgement.
        self._total_capacity = configured_max_concurrent_tasks
        # R_eff.
        self._rpc_only_slots = reserved_rpc_tasks
        # L, min_open_slots minus R.
        self._step_floor = min_open_slots - reserved_rpc_tasks
        # L_eff.
        self._effective_floor = 0
        # E.
        self._effective_ceiling = 0
        # G, never above the effective ceiling.
        self._growth_target = 0
        self._step_phase_counts = [0] * _PHASE_COUNT
        self._rpc_only_phase_counts = [0] * _PHASE_COUNT
        self._orphaned_step_handlers = 0
        self._orphaned_rpc_only_handlers = 0
        self._is_growth_target_clamped = False
        self._has_grown_this_window = False
        # The retry latch that a soft rejection sets.
        self._is_reopen_deferred = False
        self._has_batch_rejection = False
        self._peak_busy = 0
        self._next_idle_sequence = 0
        self._shrink_window_handle: asyncio.TimerHandle | None = None
        self._shrink_window_generation = 0
        self._streams_grown = 0
        self._streams_shrunk = 0
        self._grow_rejected_at_cap = 0
        self._shrink_race_drops = 0
        self._recompute_slot_capacity_locked()

    def start_slot_streams(self) -> None:
        """Open the startup streams and start the shrink window of an adaptive Worker."""
        with self.lock:
            if self._runtime.is_stopping:
                return
            self._restart_shrink_window_locked()
            self._maybe_reopen_slot_streams_locked(None)

    def reconcile(self) -> None:
        """Re-evaluate the reopen rule; it opens nothing that the rule does not allow."""
        with self.lock:
            if self._runtime.is_stopping:
                return
            self._maybe_reopen_slot_streams_locked(None)

    def resolve_slot_ceiling_at_first_acknowledgement(
        self, limit: int
    ) -> tuple[bool, str | None]:
        """Apply D4 and F6 to the first acknowledgement.

        Returns:
            Whether this was the first acknowledgement, and a start failure
            message, or None when the Worker may keep running.
        """
        with self.lock:
            if self.has_checked_registration_limit:
                return False, None
            self.has_checked_registration_limit = True
            return True, self._resolve_slot_ceiling_locked(limit)

    def _resolve_slot_ceiling_locked(self, limit: int) -> str | None:
        has_limit = limit != 0
        if not self.is_adaptive and self.is_max_concurrent_tasks_explicit:
            if has_limit and self.max_concurrent_tasks > limit:
                return (
                    f"max_concurrent_tasks={self.max_concurrent_tasks} exceeds Server "
                    f"matching.maxConcurrentTasksCap={limit} "
                    "(max_concurrent_tasks_per_app_worker)"
                )
            return None
        ceiling = self.configured_max_concurrent_tasks
        if has_limit and ceiling > limit:
            ceiling = limit
            _LOGGER.warning(
                "clamped max_concurrent_tasks=%d to the Server "
                "matching.maxConcurrentTasksCap=%d",
                self.configured_max_concurrent_tasks,
                limit,
                extra={
                    "max_concurrent_tasks": self.configured_max_concurrent_tasks,
                    "max_concurrent_tasks_per_app_worker": limit,
                    "is_max_concurrent_tasks_explicit": self.is_max_concurrent_tasks_explicit,
                },
            )
        min_open_slots = (
            self.min_open_slots if self.is_min_open_slots_explicit else ceiling
        )
        reserved_rpc_tasks = (
            self.reserved_rpc_tasks
            if self.is_reserved_rpc_tasks_explicit
            else _default_reserved_rpc_tasks(ceiling, min_open_slots)
        )
        if reserved_rpc_tasks >= ceiling:
            return (
                f"reserved_rpc_tasks={reserved_rpc_tasks} must be less than the slot "
                f"ceiling {ceiling}, which is max_concurrent_tasks="
                f"{self.configured_max_concurrent_tasks} limited by Server "
                f"matching.maxConcurrentTasksCap={limit}"
            )
        if min_open_slots > ceiling:
            return (
                f"min_open_slots={min_open_slots} exceeds the slot ceiling {ceiling}, "
                f"which is max_concurrent_tasks={self.configured_max_concurrent_tasks} "
                f"limited by Server matching.maxConcurrentTasksCap={limit}"
            )
        self.max_concurrent_tasks = ceiling
        self.min_open_slots = min_open_slots
        self.reserved_rpc_tasks = reserved_rpc_tasks
        self._step_floor = min_open_slots - reserved_rpc_tasks
        self._total_capacity = ceiling
        if reserved_rpc_tasks == 0:
            self._runtime._remove_rpc_only_slot_kind()
        return None

    def record_slot_registered(self, stream: PullStream, limit: int) -> None:
        """The registered event of the state machine, for either stream kind."""
        with self.lock:
            attempt = stream.slot
            if attempt is None:
                return
            attempt.has_received_registered = True
            if attempt.phase == _SlotPhase.REGISTERING:
                self._set_slot_phase_locked(stream, _SlotPhase.IDLE)
            if self.is_adaptive:
                capacity = self.max_concurrent_tasks
                if limit != 0 and limit < capacity:
                    capacity = limit
                self._total_capacity = capacity
            rpc_only_slots = self.reserved_rpc_tasks
            if rpc_only_slots + 1 > self._total_capacity:
                rpc_only_slots = max(0, self._total_capacity - 1)
            if rpc_only_slots < self._rpc_only_slots:
                _LOGGER.warning(
                    "Server matching.maxConcurrentTasksCap=%d leaves %d RPC-only slots, "
                    "fewer than reserved_rpc_tasks=%d",
                    limit,
                    rpc_only_slots,
                    self.reserved_rpc_tasks,
                    extra={
                        "reserved_rpc_tasks": self.reserved_rpc_tasks,
                        "rpc_only_slots": rpc_only_slots,
                        "max_concurrent_tasks_per_app_worker": limit,
                    },
                )
            self._rpc_only_slots = rpc_only_slots
            self._close_surplus_slot_streams_locked()
            self._recompute_slot_capacity_locked()
            self._maybe_reopen_slot_streams_locked(None)
            if self._step_phase_counts[_SlotPhase.REGISTERING] == 0:
                self._settle_growth_batch_locked()

    def record_slot_task_started(self, stream: PullStream) -> None:
        """The task_frame event."""
        with self.lock:
            attempt = stream.slot
            if attempt is None or attempt.phase == _SlotPhase.UNTRACKED:
                return
            self._set_slot_phase_locked(stream, _SlotPhase.BUSY)
            if stream.slot_kind == pb.POLL_WORKER_SLOT_KIND_RPC_ONLY:
                return
            self._peak_busy = max(
                self._peak_busy, self._step_phase_counts[_SlotPhase.BUSY]
            )
            self._maybe_grow_slot_streams_locked()

    def record_slot_task_dropped(self, stream: PullStream) -> None:
        """Count a task dropped on a stream that the Worker chose to close."""
        with self.lock:
            attempt = stream.slot
            if (
                attempt is not None
                and attempt.is_closed_on_purpose
                and not self._runtime.is_stopping
            ):
                self._shrink_race_drops += 1

    def record_slot_reconnect_requested(self, stream: PullStream) -> None:
        """Keep a stream that the Server asked to reconnect out of the idle set."""
        with self.lock:
            attempt = stream.slot
            if attempt is not None and attempt.phase == _SlotPhase.IDLE:
                self._set_slot_phase_locked(stream, _SlotPhase.RECONNECTING)

    def record_slot_task_completed(self, stream: PullStream, is_draining: bool) -> bool:
        """The final_frame_sent event, or orphaned_handler_returned after the stream ended.

        Returns:
            True when the stream must half-close because its kind is in surplus
            after a lowered cap. False otherwise, including for an orphaned
            handler and an untracked stream.
        """
        with self.lock:
            attempt = stream.slot
            if attempt is None:
                return False
            if attempt.is_orphaned:
                attempt.is_orphaned = False
                if stream.slot_kind == pb.POLL_WORKER_SLOT_KIND_RPC_ONLY:
                    self._orphaned_rpc_only_handlers -= 1
                else:
                    self._orphaned_step_handlers -= 1
                self._recompute_slot_capacity_locked()
                self._maybe_reopen_slot_streams_locked(
                    self._reopen_candidate_locked(stream)
                )
                return False
            if attempt.phase != _SlotPhase.BUSY:
                return False
            if is_draining:
                self._set_slot_phase_locked(stream, _SlotPhase.RECONNECTING)
                return False
            self._set_slot_phase_locked(stream, _SlotPhase.IDLE)
            if stream.slot_kind == pb.POLL_WORKER_SLOT_KIND_RPC_ONLY:
                is_surplus = self._rpc_only_slot_usage_locked() > self._rpc_only_slots
            else:
                is_surplus = self._step_slot_usage_locked() > max(
                    0, self._total_capacity - self._rpc_only_slots
                )
            if not is_surplus:
                return False
            self._half_close_slot_stream_locked(stream)
            self._streams_shrunk += 1
            self._recompute_slot_capacity_locked()
            self._maybe_reopen_slot_streams_locked(None)
            return True

    def record_slot_stream_result(
        self,
        stream: PullStream,
        attempt: _PullSlotAttempt | None,
        code: grpc.StatusCode | None,
    ) -> bool:
        """End the attempt that a poll used and report whether its rejection was soft (F5).

        Returns:
            True when the attempt's RESOURCE_EXHAUSTED rejection is soft. The
            caller then records no permanent error, and its runner must not
            retry it. False otherwise, including when a reopen grant already
            replaced the attempt.
        """
        with self.lock:
            if attempt is None or stream.slot is not attempt:
                return False
            is_soft_rejection = (
                not self._runtime.is_stopping
                and self._is_soft_slot_rejection_locked(attempt, code)
            )
            if is_soft_rejection:
                self._record_slot_registration_rejected_locked(stream)
            self._record_slot_stream_ended_locked(stream)
            return is_soft_rejection

    def record_slot_stream_ended(self, stream: PullStream) -> None:
        """Record a stream end before its handler returns, so the handler counts as orphaned."""
        with self.lock:
            if stream.slot is None:
                return
            self._record_slot_stream_ended_locked(stream)

    def abandon_slot_attempt(self, stream: PullStream) -> None:
        """End an attempt whose runner exits before the attempt ends."""
        with self.lock:
            attempt = stream.slot
            if (
                attempt is None
                or attempt.phase == _SlotPhase.UNTRACKED
                or attempt.has_ended
            ):
                return
            attempt.is_closed_on_purpose = True
            self._record_slot_stream_ended_locked(stream)

    def take_reopen_grant(self, stream: PullStream) -> bool:
        """Report whether the reopen rule gave this runner the next attempt, and consume it.

        Returns:
            True when the runner may open its next attempt. A stream that the
            state machine does not track always keeps retrying.
        """
        with self.lock:
            attempt = stream.slot
            if attempt is None:
                return True
            is_granted = attempt.is_reopen_granted
            attempt.is_reopen_granted = False
            return is_granted

    def admit_slot_registration(self, stream: PullStream) -> bool:
        """Check an attempt before its runner opens a stream.

        ``mark_slot_registration_sending`` checks it again right before the
        Register write, because a lower cap may arrive in between.

        Returns:
            True when the attempt still fits, or the stream is untracked.
        """
        with self.lock:
            if stream.slot is None:
                return True
            return self._admit_slot_registration_locked(stream)

    def mark_slot_registration_sending(self, stream: PullStream) -> bool:
        """Re-check the attempt and mark it as sending; the caller writes the Register after.

        A cap drop between this check and the write reaches the Server, which
        enforces the cap; an adaptive Worker treats that rejection as soft.

        Returns:
            True when the caller may write the Register. False when the
            attempt no longer fits and was revoked.
        """
        with self.lock:
            attempt = stream.slot
            if attempt is None:
                return True
            if not self._admit_slot_registration_locked(stream):
                return False
            attempt.is_register_sent = True
            return True

    def stop_shrink_window(self) -> None:
        """Stop the shrink window timer at stop."""
        with self.lock:
            self._shrink_window_generation += 1
            if self._shrink_window_handle is not None:
                self._shrink_window_handle.cancel()
                self._shrink_window_handle = None

    def slot_stream_state(self) -> PullSlotStreamState:
        """Return an independent snapshot; safe from any thread."""
        with self.lock:
            counts = self._step_phase_counts
            return PullSlotStreamState(
                is_adaptive=self.is_adaptive,
                open=self._step_slot_open_locked(),
                registering=counts[_SlotPhase.REGISTERING],
                busy=counts[_SlotPhase.BUSY],
                idle=counts[_SlotPhase.IDLE],
                orphaned_handlers=self._orphaned_step_handlers,
                orphaned_rpc_only_handlers=self._orphaned_rpc_only_handlers,
                floor=self._effective_floor,
                effective_ceiling=self._effective_ceiling,
                growth_target=self._growth_target,
                total_capacity=self._total_capacity,
                rpc_only_open=self._rpc_only_slot_open_locked(),
                rpc_only_slots=self._rpc_only_slots,
                streams_grown=self._streams_grown,
                streams_shrunk=self._streams_shrunk,
                grow_rejected_at_cap=self._grow_rejected_at_cap,
                shrink_race_drops=self._shrink_race_drops,
            )

    def _expire_shrink_window(self, generation: int) -> None:
        # The every-W step of the state machine for one window generation.
        with self.lock:
            if (
                self._runtime.is_stopping
                or generation != self._shrink_window_generation
            ):
                return
            self._restart_shrink_window_locked()
            self._is_growth_target_clamped = False
            self._has_grown_this_window = False
            self._is_reopen_deferred = False
            self._recompute_slot_capacity_locked()
            self._maybe_reopen_slot_streams_locked(None)
            self._shrink_slot_streams_locked()
            self._peak_busy = self._step_phase_counts[_SlotPhase.BUSY]
            self._maybe_grow_slot_streams_locked()

    def _shrink_slot_streams_locked(self) -> None:
        # Close half the idle surplus above the window's peak plus headroom.
        peak_busy = self._peak_busy
        headroom = max(1, (peak_busy + 3) // 4)
        target = min(
            self._effective_ceiling, max(self._effective_floor, peak_busy + headroom)
        )
        open_streams = self._step_slot_open_locked()
        if open_streams <= target:
            return
        close_count = (open_streams - target + 1) // 2
        idle_streams = self._idle_step_streams_newest_first_locked()
        # The last idle stream stays open, and the floor stays open.
        close_count = min(
            close_count, len(idle_streams) - 1, open_streams - self._effective_floor
        )
        if close_count <= 0:
            return
        for stream in idle_streams[:close_count]:
            self._half_close_slot_stream_locked(stream)
        self._streams_shrunk += close_count
        _LOGGER.debug(
            "shrink pull slot streams from %d to %d, peak busy %d",
            open_streams,
            open_streams - close_count,
            peak_busy,
            extra={
                "from": open_streams,
                "to": open_streams - close_count,
                "peak_busy": peak_busy,
            },
        )
        self._recompute_slot_capacity_locked()

    def _close_surplus_slot_streams_locked(self) -> None:
        # Release capacity above a lowered cap; RPC-only surplus goes first. An
        # attempt whose Register has not left yet is revoked first, because it
        # costs the Server nothing.
        for slot_kind in (
            pb.POLL_WORKER_SLOT_KIND_RPC_ONLY,
            pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC,
        ):
            while self._is_slot_kind_over_quota_locked(slot_kind):
                pending = self._unsent_slot_attempt_locked(slot_kind)
                if pending is not None:
                    self._revoke_slot_attempt_locked(pending)
                    continue
                stream = self._newest_idle_stream_locked(slot_kind)
                if stream is None:
                    break
                self._half_close_slot_stream_locked(stream)
                self._streams_shrunk += 1

    def _admit_slot_registration_locked(self, stream: PullStream) -> bool:
        # A later cap drop meets a Server rejection, soft in adaptive mode.
        attempt = stream.slot
        if (
            attempt is None
            or self._runtime.is_stopping
            or attempt.phase != _SlotPhase.REGISTERING
            or attempt.has_ended
        ):
            return False
        if not self._is_slot_attempt_over_capacity_locked(stream):
            return True
        self._revoke_slot_attempt_locked(stream)
        self._recompute_slot_capacity_locked()
        self._maybe_reopen_slot_streams_locked(None)
        if self._step_phase_counts[_SlotPhase.REGISTERING] == 0:
            self._settle_growth_batch_locked()
        return False

    def _is_slot_attempt_over_capacity_locked(self, stream: PullStream) -> bool:
        # Checks one registering attempt, which the totals already count.
        if (
            self._step_slot_usage_locked() + self._rpc_only_slot_usage_locked()
            > self._total_capacity
        ):
            return True
        if stream.slot_kind == pb.POLL_WORKER_SLOT_KIND_RPC_ONLY:
            return self._rpc_only_slot_usage_locked() > self._rpc_only_slots
        # F5 gives the first Step-and-RPC stream any free slot, so only later ones need the Step quota.
        return (
            self._step_slot_open_locked() > 1
            and self._step_slot_usage_locked() > self._effective_ceiling
        )

    def _is_slot_kind_over_quota_locked(self, slot_kind: pb.PollWorkerSlotKind) -> bool:
        # Surplus of a kind after a lowered cap: RPC-only above R_eff, or Step-and-RPC above C - R_eff.
        if slot_kind == pb.POLL_WORKER_SLOT_KIND_RPC_ONLY:
            return self._rpc_only_slot_usage_locked() > self._rpc_only_slots
        return self._step_slot_usage_locked() > max(
            0, self._total_capacity - self._rpc_only_slots
        )

    def _revoke_slot_attempt_locked(self, stream: PullStream) -> None:
        # Take back an attempt whose Register has not left; its runner then exits.
        attempt = stream.slot
        if attempt is None:
            return
        attempt.is_closed_on_purpose = True
        attempt.has_ended = True
        self._set_slot_phase_locked(stream, _SlotPhase.UNTRACKED)

    def _is_soft_slot_rejection_locked(
        self, attempt: _PullSlotAttempt, code: grpc.StatusCode | None
    ) -> bool:
        # F5: only an adaptive Worker softens RESOURCE_EXHAUSTED, and only for a
        # growth stream or a reconnect after readiness that never registered.
        if (
            not self.is_adaptive
            or code is not grpc.StatusCode.RESOURCE_EXHAUSTED
            or attempt.has_received_registered
        ):
            return False
        if attempt.phase != _SlotPhase.REGISTERING:
            return False
        return attempt.is_growth or attempt.is_opened_after_readiness

    def _record_slot_registration_rejected_locked(self, stream: PullStream) -> None:
        # The soft registration_rejected event.
        attempt = stream.slot
        if attempt is None:
            return
        attempt.is_soft_rejected = True
        self._set_slot_phase_locked(stream, _SlotPhase.UNTRACKED)
        if attempt.is_growth:
            self._has_batch_rejection = True
        self._grow_rejected_at_cap += 1
        self._is_reopen_deferred = True
        self._recompute_slot_capacity_locked()
        if self._step_phase_counts[_SlotPhase.REGISTERING] == 0:
            self._settle_growth_batch_locked()

    def _record_slot_stream_ended_locked(self, stream: PullStream) -> None:
        # stream_ended; every end of an attempt passes here once.
        attempt = stream.slot
        if attempt is None or attempt.has_ended:
            return
        attempt.has_ended = True
        if attempt.phase != _SlotPhase.UNTRACKED:
            if attempt.phase == _SlotPhase.BUSY:
                attempt.is_orphaned = True
                if stream.slot_kind == pb.POLL_WORKER_SLOT_KIND_RPC_ONLY:
                    self._orphaned_rpc_only_handlers += 1
                else:
                    self._orphaned_step_handlers += 1
            self._set_slot_phase_locked(stream, _SlotPhase.UNTRACKED)
        if attempt.has_received_registered:
            self._is_reopen_deferred = False
        self._recompute_slot_capacity_locked()
        parked = None
        if not attempt.is_orphaned:
            parked = self._reopen_candidate_locked(stream)
        self._maybe_reopen_slot_streams_locked(parked)
        self._maybe_grow_slot_streams_locked()
        if self._step_phase_counts[_SlotPhase.REGISTERING] == 0:
            self._settle_growth_batch_locked()

    def _settle_growth_batch_locked(self) -> None:
        # Runs when no stream registers; a rejected batch clamps G until the window ends.
        if self._has_batch_rejection:
            self._has_batch_rejection = False
            self._growth_target = min(
                self._effective_ceiling, self._step_slot_usage_locked()
            )
            self._is_growth_target_clamped = True
            _LOGGER.info(
                "the Server rejected pull slot stream growth at its stream limit; "
                "holding growth at %d streams until the next shrink window",
                self._growth_target,
                extra={
                    "growth_target": self._growth_target,
                    "effective_ceiling": self._effective_ceiling,
                },
            )
        self._maybe_grow_slot_streams_locked()

    def _maybe_grow_slot_streams_locked(self) -> None:
        # Double the open streams when a hand-out took the last idle one.
        if (
            not self.is_adaptive
            or self._runtime.is_stopping
            or self._is_reopen_deferred
        ):
            return
        if self._is_growth_target_clamped and self._has_grown_this_window:
            return
        open_streams = self._step_slot_open_locked()
        usage = self._step_slot_usage_locked()
        if (
            self._step_phase_counts[_SlotPhase.IDLE] != 0
            or self._step_phase_counts[_SlotPhase.REGISTERING] != 0
            or usage >= self._growth_target
        ):
            return
        count = min(self._growth_target - usage, max(1, open_streams))
        _LOGGER.debug(
            "grow pull slot streams from %d to %d: no idle stream",
            open_streams,
            open_streams + count,
            extra={
                "from": open_streams,
                "to": open_streams + count,
                "reason": "no idle stream",
            },
        )
        for _ in range(count):
            self._open_slot_stream_locked(
                pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC, True, None
            )
        self._streams_grown += count
        self._has_grown_this_window = True
        self._restart_shrink_window_locked()

    def _maybe_reopen_slot_streams_locked(self, parked: PullStream | None) -> None:
        # Open streams within free capacity in F5 order. A parked runner of the
        # same kind is reused, so it keeps its reconnect backoff.
        if self._runtime.is_stopping or self._is_reopen_deferred:
            return
        step_open = self._step_slot_open_locked()
        free = max(
            0,
            self._total_capacity
            - (self._step_slot_usage_locked() + self._rpc_only_slot_usage_locked()),
        )
        if step_open == 0 and free > 0:
            self._open_slot_stream_locked(
                pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC, False, parked
            )
            step_open += 1
            free -= 1
        if self._is_cap_probe_pending_locked():
            return
        if self.has_checked_registration_limit or not self.is_adaptive:
            count = min(
                free, max(0, self._rpc_only_slots - self._rpc_only_slot_usage_locked())
            )
            for _ in range(count):
                self._open_slot_stream_locked(
                    pb.POLL_WORKER_SLOT_KIND_RPC_ONLY, False, parked
                )
            free -= count
        count = min(
            free,
            max(
                0,
                min(
                    self._effective_floor,
                    self._effective_ceiling - self._orphaned_step_handlers,
                )
                - step_open,
            ),
        )
        for _ in range(count):
            self._open_slot_stream_locked(
                pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC, False, parked
            )

    def _open_slot_stream_locked(
        self,
        slot_kind: pb.PollWorkerSlotKind,
        is_growth: bool,
        parked: PullStream | None,
    ) -> None:
        # Count a new attempt as registering and grant it to parked when possible.
        attempt = _PullSlotAttempt(
            is_growth=is_growth,
            is_opened_after_readiness=self._runtime.registration.has_registered_all,
        )
        if (
            not is_growth
            and parked is not None
            and parked.slot is not None
            and parked.slot_kind == slot_kind
            and not parked.slot.is_reopen_granted
        ):
            attempt.is_reopen_granted = True
            parked.slot = attempt
            self._set_slot_phase_locked(parked, _SlotPhase.REGISTERING)
            return
        stream = self._runtime._create_slot_stream(slot_kind)
        stream.slot = attempt
        self._set_slot_phase_locked(stream, _SlotPhase.REGISTERING)
        self._runtime._launch_slot_stream(stream)

    def _half_close_slot_stream_locked(self, stream: PullStream) -> None:
        # Take a stream out of the totals and half-close it; its later end changes no count.
        attempt = stream.slot
        if attempt is None:
            return
        self._set_slot_phase_locked(stream, _SlotPhase.UNTRACKED)
        attempt.is_closed_on_purpose = True
        stream._request_slot_half_close()

    def _restart_shrink_window_locked(self) -> None:
        # Start a new shrink window; an older window's timer then does nothing.
        loop = self._runtime._loop
        if (
            not self.is_adaptive
            or self.settings.shrink_window_seconds <= 0
            or loop is None
        ):
            return
        self._shrink_window_generation += 1
        generation = self._shrink_window_generation
        if self._shrink_window_handle is not None:
            self._shrink_window_handle.cancel()
        self._shrink_window_handle = loop.call_later(
            self.settings.shrink_window_seconds,
            self._expire_shrink_window,
            generation,
        )

    def _set_slot_phase_locked(self, stream: PullStream, phase: _SlotPhase) -> None:
        # Move an attempt between phases and keep the per-phase totals.
        attempt = stream.slot
        if attempt is None:
            return
        counts = self._step_phase_counts
        if stream.slot_kind == pb.POLL_WORKER_SLOT_KIND_RPC_ONLY:
            counts = self._rpc_only_phase_counts
        if attempt.phase != _SlotPhase.UNTRACKED:
            counts[attempt.phase] -= 1
        attempt.phase = phase
        if phase != _SlotPhase.UNTRACKED:
            counts[phase] += 1
        if phase == _SlotPhase.IDLE:
            self._next_idle_sequence += 1
            attempt.idle_sequence = self._next_idle_sequence

    def _recompute_slot_capacity_locked(self) -> None:
        # Derive E, L_eff, and G; RPC-only overflow takes Step capacity.
        rpc_only_reserve = max(self._rpc_only_slots, self._rpc_only_slot_usage_locked())
        self._effective_ceiling = max(0, self._total_capacity - rpc_only_reserve)
        self._effective_floor = max(0, min(self._step_floor, self._effective_ceiling))
        if self._is_growth_target_clamped:
            self._growth_target = min(self._growth_target, self._effective_ceiling)
        else:
            self._growth_target = self._effective_ceiling

    def _reopen_candidate_locked(self, stream: PullStream) -> PullStream | None:
        # The stream when its runner may serve the next attempt of its kind.
        attempt = stream.slot
        if (
            attempt is None
            or attempt.is_closed_on_purpose
            or attempt.is_soft_rejected
            or attempt.is_reopen_granted
        ):
            return None
        return stream

    def _idle_step_streams_newest_first_locked(self) -> list[PullStream]:
        idle_streams = [
            stream
            for stream in self._runtime._streams
            if stream.slot is not None
            and stream.slot_kind != pb.POLL_WORKER_SLOT_KIND_RPC_ONLY
            and stream.slot.phase == _SlotPhase.IDLE
        ]
        idle_streams.sort(key=_idle_sequence, reverse=True)
        return idle_streams

    def _unsent_slot_attempt_locked(
        self, slot_kind: pb.PollWorkerSlotKind
    ) -> PullStream | None:
        # A registering attempt of the kind whose Register has not left, or None.
        for stream in self._runtime._streams:
            attempt = stream.slot
            if (
                attempt is not None
                and stream.slot_kind == slot_kind
                and attempt.phase == _SlotPhase.REGISTERING
                and not attempt.is_register_sent
            ):
                return stream
        return None

    def _newest_idle_stream_locked(
        self, slot_kind: pb.PollWorkerSlotKind
    ) -> PullStream | None:
        newest: PullStream | None = None
        for stream in self._runtime._streams:
            attempt = stream.slot
            if (
                attempt is not None
                and stream.slot_kind == slot_kind
                and attempt.phase == _SlotPhase.IDLE
                and (newest is None or attempt.idle_sequence > _idle_sequence(newest))
            ):
                newest = stream
        return newest

    def _is_cap_probe_pending_locked(self) -> bool:
        # F4: a fixed Worker with an unset ceiling opens one stream until the first acknowledgement.
        return (
            not self.is_adaptive
            and not self.is_max_concurrent_tasks_explicit
            and not self.has_checked_registration_limit
        )

    def _step_slot_open_locked(self) -> int:
        # S.
        counts = self._step_phase_counts
        return (
            counts[_SlotPhase.REGISTERING]
            + counts[_SlotPhase.IDLE]
            + counts[_SlotPhase.BUSY]
            + counts[_SlotPhase.RECONNECTING]
        )

    def _step_slot_usage_locked(self) -> int:
        # S + H.
        return self._step_slot_open_locked() + self._orphaned_step_handlers

    def _rpc_only_slot_open_locked(self) -> int:
        # Sr.
        counts = self._rpc_only_phase_counts
        return (
            counts[_SlotPhase.REGISTERING]
            + counts[_SlotPhase.IDLE]
            + counts[_SlotPhase.BUSY]
            + counts[_SlotPhase.RECONNECTING]
        )

    def _rpc_only_slot_usage_locked(self) -> int:
        # Sr + Hr.
        return self._rpc_only_slot_open_locked() + self._orphaned_rpc_only_handlers


def _idle_sequence(stream: PullStream) -> int:
    attempt = stream.slot
    return 0 if attempt is None else attempt.idle_sequence
