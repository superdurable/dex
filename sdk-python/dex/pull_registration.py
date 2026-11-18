# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import asyncio
import threading
import time
from concurrent.futures import Future
from dataclasses import dataclass


class PullRegistrationError(RuntimeError):
    """Identify a permanent rejection leaving a slot kind without a registered stream.

    Raised by ``wait_pull_registered`` before initial readiness, or by ``start``
    when ``stop_on_permanent_stream_rejection`` is enabled. Inspect ``queue``
    and ``error`` for the rejected slot kind and original gRPC error. Permanent codes
    are INVALID_ARGUMENT, RESOURCE_EXHAUSTED, UNIMPLEMENTED, PERMISSION_DENIED,
    and UNAUTHENTICATED.

    A stream whose Registered acknowledgement names a different task queue
    than ``PullWorkerOptions.task_queue`` is also a permanent rejection. Its
    ``error`` is a ``RuntimeError`` that names both task queues.

    Attributes:
        queue: Rejected slot kind: ``STEP_AND_RPC`` or ``RPC_ONLY``.
            The field name is retained for source compatibility.
        error: Original permanent error, also exposed as ``__cause__``: a gRPC
            transport error, or a ``RuntimeError`` for a task queue mismatch.
        task_queue: The task queue the Worker requested; empty for the default
            task queue.

            .. versionadded:: 1.45.1
    """

    def __init__(self, queue: str, error: BaseException, task_queue: str = "") -> None:
        """Create a slot kind registration rejection.

        Args:
            queue: Rejected slot kind label, retained in the ``queue`` field.
            error: Original permanent error.
            task_queue: Requested task queue; empty for the default task queue.

                .. versionadded:: 1.45.1
        """
        task_queue_label = f" on task queue {task_queue!r}" if task_queue else ""
        super().__init__(
            f"pull Worker slot kind {queue!r}{task_queue_label} registration "
            f"rejected: {error}"
        )
        self.queue = queue
        self.error = error
        self.task_queue = task_queue
        self.__cause__ = error


@dataclass(frozen=True)
class PullQueueRegistrationState:
    """Describe one execution slot kind in an immutable readiness snapshot.

    Attributes:
        queue: ``STEP_AND_RPC`` or ``RPC_ONLY``; the field name is retained.
        has_registered_stream: Whether a stream is currently registered.
        last_permanent_error: Last permanent error, or ``None``: a gRPC
            transport error, or a ``RuntimeError`` when the Server registered
            the stream under a different task queue. A new Registered
            acknowledgement clears it. Treat errors as immutable.
        task_queue: The task queue the Worker polls, from
            ``PullWorkerOptions.task_queue``. Empty means the default task
            queue. ``queue`` is the slot kind; ``task_queue`` is the routing
            queue.

            .. versionadded:: 1.45.1
    """

    queue: str
    has_registered_stream: bool
    last_permanent_error: BaseException | None
    task_queue: str = ""


@dataclass(frozen=True)
class PullRegistrationState:
    """Describe pull registration readiness independently of the Worker lifecycle.

    Obtain snapshots with ``worker.pull_registration_state()``. After every required slot kind
    has registered once, transient failures, reconnects, and stop keep readiness.
    A permanent error with no registered stream makes ``is_ready`` false until
    re-registration. When the first registration reports a Server per-Worker
    limit below ``max_concurrent_tasks``, ``is_ready`` stays false permanently.

    .. versionchanged:: 1.45.5
       That permanent failure applies to an explicit ``max_concurrent_tasks``
       of a fixed Worker. An unset ``max_concurrent_tasks``, or any value of an
       adaptive Worker, is clamped to the limit instead. When the clamp leaves
       no room for an RPC-only slot, ``queues`` lists only ``STEP_AND_RPC``
       and readiness no longer waits for ``RPC_ONLY``. See
       ``PullWorkerOptions.min_open_slots``.

    Attributes:
        is_ready: Whether STEP_AND_RPC and, when reserved, RPC_ONLY registered
            once, the first registration passed the Server per-Worker limit
            check, and no required slot kind is permanently
            rejected without a registered stream.
        queues: Immutable snapshots ordered STEP_AND_RPC then RPC_ONLY when reserved.

    Examples:
        >>> state = worker.pull_registration_state()
        >>> is_ready = state.is_ready
    """

    is_ready: bool = False
    queues: tuple[PullQueueRegistrationState, ...] = ()


@dataclass(frozen=True)
class PullSlotStreamState:
    """Describe a pull Worker's slot streams in an immutable snapshot.

    Obtain it with ``worker.pull_slot_stream_state()``, including concurrently
    with ``start``, ``stop``, and running handlers. It is an independent copy;
    changing the Worker later does not change it. Every count is a number of
    slot streams or handlers, in the same unit as
    ``PullWorkerOptions.max_concurrent_tasks``.

    ``open``, ``registering``, ``busy``, and ``idle`` count Step-and-RPC streams
    only, the streams that accept both Steps and RPCs. ``rpc_only_open`` counts
    the reserved RPC-only streams. While every stream is registered and no
    handler outlives its stream, an adaptive Worker, whose
    ``PullWorkerOptions.min_open_slots`` is below ``max_concurrent_tasks``,
    keeps ``open`` between ``floor`` and ``effective_ceiling``, and a fixed
    Worker keeps ``open`` equal to ``effective_ceiling``. Reconnects, orphaned
    handlers, and a lowered Server cap move ``open`` outside that range until
    the Worker converges again.

    ``open`` of zero with ``orphaned_handlers`` above zero means that the Worker
    holds no live Step-and-RPC stream and waits for running handlers to return,
    for example after a Server restart. ``pull_registration_state`` does not
    report that gap, because readiness stays latched across reconnects.

    Attributes:
        is_adaptive: Whether the Worker varies its open Step-and-RPC streams,
            which is when ``min_open_slots`` is below ``max_concurrent_tasks``.
        open: Step-and-RPC streams that are registered or registering and not
            closing.
        registering: Step-and-RPC streams that wait for the Server's
            acknowledgement, including a reconnect that waits for its backoff.
        busy: Step-and-RPC streams that run a task.
        idle: Registered Step-and-RPC streams that wait for a task.
        orphaned_handlers: Step-and-RPC handlers that still run after their
            stream ended. Each one keeps its slot, so the Worker registers no
            stream for it until it returns. A synchronous handler on the thread
            pool is never interrupted, so it counts here until it returns.
        orphaned_rpc_only_handlers: RPC-only handlers that still run after their
            stream ended.
        floor: Step-and-RPC streams the Worker keeps open at rest:
            ``min_open_slots`` minus the reserved RPC slots, limited by
            ``effective_ceiling``.
        effective_ceiling: The most Step-and-RPC streams and orphaned handlers
            the Worker may hold: ``total_capacity`` minus the RPC-only slots, or
            minus the RPC-only streams and handlers when they exceed them.
        growth_target: The Step-and-RPC stream count that growth may reach. It
            equals ``effective_ceiling``, or less for the rest of a shrink
            window after the Server rejected a growth stream at its stream
            limit.
        total_capacity: The Worker's slot count: ``max_concurrent_tasks``,
            limited by the per-Worker cap that the Server reported in its last
            acknowledgement.
        rpc_only_open: RPC-only streams that are registered or registering and
            not closing.
        rpc_only_slots: RPC-only streams the Worker keeps: ``reserved_rpc_tasks``,
            or fewer when the Server cap leaves no room for them next to one
            Step-and-RPC stream.
        streams_grown: Step-and-RPC streams opened by growth since the Worker
            started.
        streams_shrunk: Streams that the Worker half-closed because it had more
            than it needed.
        grow_rejected_at_cap: Registrations that the Server rejected with
            RESOURCE_EXHAUSTED and that the adaptive Worker treated as a soft
            limit instead of a permanent rejection.
        shrink_race_drops: Tasks that arrived on a stream after the Worker
            decided to close it. The Server returns each one as not delivered
            and dispatches it again without spending a retry.

    Examples:
        >>> state = worker.pull_slot_stream_state()
        >>> print(
        ...     f"open={state.open} busy={state.busy} idle={state.idle} "
        ...     f"orphaned={state.orphaned_handlers} capacity={state.total_capacity}"
        ... )

    .. versionadded:: 1.45.5
    """

    is_adaptive: bool = False
    open: int = 0
    registering: int = 0
    busy: int = 0
    idle: int = 0
    orphaned_handlers: int = 0
    orphaned_rpc_only_handlers: int = 0
    floor: int = 0
    effective_ceiling: int = 0
    growth_target: int = 0
    total_capacity: int = 0
    rpc_only_open: int = 0
    rpc_only_slots: int = 0
    streams_grown: int = 0
    streams_shrunk: int = 0
    grow_rejected_at_cap: int = 0
    shrink_race_drops: int = 0


class _PullRegistration:
    def __init__(self, queues: tuple[str, ...], task_queue: str = "") -> None:
        self._lock = threading.Lock()
        self._queues = queues
        self._task_queue = task_queue
        self._counts = dict.fromkeys(queues, 0)
        self._registered: set[str] = set()
        self._errors: dict[str, BaseException] = {}
        self._initial_error: PullRegistrationError | None = None
        self._fatal_error_message: str | None = None
        self._has_registered_all = False
        self._is_stopping = False
        self._has_start_returned = False
        self._start_error: BaseException | None = None
        self._changed: Future[None] = Future()

    def record_registered_stream(self, queue: str) -> None:
        with self._lock:
            if queue not in self._counts:
                return
            self._counts[queue] += 1
            self._registered.add(queue)
            self._errors.pop(queue, None)
            self._has_registered_all = len(self._registered) == len(self._queues)
            self._notify()

    def record_stream_result(
        self, queue: str, was_registered: bool, error: BaseException | None
    ) -> PullRegistrationError | None:
        with self._lock:
            if queue not in self._counts:
                return None
            if was_registered:
                self._counts[queue] -= 1
            rejection = None
            if error is not None:
                self._errors[queue] = error
                if self._counts[queue] == 0:
                    rejection = PullRegistrationError(queue, error, self._task_queue)
                    if not self._has_registered_all and self._initial_error is None:
                        self._initial_error = rejection
            self._notify()
            return rejection

    def remove_slot_kind(self, queue: str) -> None:
        # The first acknowledgement may clamp the RPC-only reserve to zero.
        with self._lock:
            if queue not in self._queues:
                return
            self._queues = tuple(name for name in self._queues if name != queue)
            self._counts.pop(queue, None)
            self._registered.discard(queue)
            self._errors.pop(queue, None)
            self._has_registered_all = self._has_registered_all or (
                len(self._registered) == len(self._queues)
            )
            self._notify()

    @property
    def has_registered_all(self) -> bool:
        with self._lock:
            return self._has_registered_all

    def record_fatal_error(self, error: BaseException) -> None:
        with self._lock:
            self._fatal_error_message = str(error)
            self._notify()

    def record_stopping(self) -> None:
        with self._lock:
            self._is_stopping = True
            self._notify()

    def finish_start(self, error: BaseException | None) -> None:
        with self._lock:
            self._has_start_returned = True
            self._start_error = error
            self._notify()

    def snapshot(self) -> PullRegistrationState:
        with self._lock:
            queues = tuple(
                PullQueueRegistrationState(
                    queue,
                    self._counts[queue] > 0,
                    self._errors.get(queue),
                    self._task_queue,
                )
                for queue in self._queues
            )
            return PullRegistrationState(
                self._fatal_error_message is None
                and self._has_registered_all
                and not any(
                    not queue.has_registered_stream
                    and queue.last_permanent_error is not None
                    for queue in queues
                ),
                queues,
            )

    def wait(self, timeout: float | None) -> None:
        deadline = None if timeout is None else time.monotonic() + timeout
        while (changed := self._next_change()) is not None:
            changed.result(
                None if deadline is None else max(0, deadline - time.monotonic())
            )

    async def wait_async(self, timeout: float | None) -> None:
        if self._next_change() is not None:
            await asyncio.wait_for(self._wait_async(), timeout)

    async def _wait_async(self) -> None:
        while (changed := self._next_change()) is not None:
            await asyncio.shield(asyncio.wrap_future(changed))

    def _next_change(self) -> Future[None] | None:
        with self._lock:
            if self._fatal_error_message is not None:
                raise RuntimeError(self._fatal_error_message)
            if self._has_registered_all:
                return None
            if self._initial_error is not None:
                raise self._initial_error
            if self._has_start_returned:
                if self._start_error is not None:
                    raise self._start_error
                raise RuntimeError("pull Worker stopped before registration")
            if self._is_stopping:
                raise RuntimeError("pull Worker stopped before registration")
            return self._changed

    def _notify(self) -> None:
        changed = self._changed
        self._changed = Future()
        changed.set_result(None)
