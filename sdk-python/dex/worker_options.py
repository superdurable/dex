# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import os
from collections.abc import Sequence
from dataclasses import dataclass, field
from datetime import timedelta

from dex._message_size import validate_grpc_max_message_bytes
from dex._task_queue_names import registered_task_queue_name_error
from dex.credential import Credential
from dex.flow_definition import FlowDefinitionDocument
from dex.flow_service_tls_options import FlowServiceTlsOptions


def _default_reserved_rpc_tasks(max_concurrent_tasks: int, min_open_slots: int) -> int:
    # One RPC-only slot only when the ceiling and the floor both leave room for it.
    return int(max_concurrent_tasks >= 2 and min_open_slots >= 2)


@dataclass(frozen=True)
class PullWorkerOptions:
    """Configure outbound task polling for a Worker or AsyncWorker.

    Pass this to ``WorkerOptions(pull=PullWorkerOptions())``. Each stream runs
    one task at a time.
    Handlers may run concurrently and must synchronize shared state.

    The Server queues Steps until a supporting Worker has a free slot; waiting
    does not consume a Step retry. Reserve RPC slots when long Steps could occupy
    all shared slots. RPC_ONLY slots remain unavailable to Steps even when idle.
    Readiness requires one registered STEP_AND_RPC stream and, when reserved,
    one RPC_ONLY stream. It does not require every execution slot to register.
    Stop preserves latched registration readiness, so combine readiness checks
    with application lifecycle state before routing work to a stopping process.

    .. versionchanged:: 1.35.0
       Each slot owns one stream. RPC reservation uses RPC_ONLY streams instead
       of declining Steps. idle_streams_per_queue is deprecated and ignored.
       Start fails above matching.maxConcurrentTasksCap. Tasks received during
       stop return to Dex without consuming a retry.

    .. versionchanged:: 1.45.5
       With ``min_open_slots`` below ``max_concurrent_tasks`` the Worker is
       adaptive. It keeps between ``min_open_slots`` and
       ``max_concurrent_tasks`` slot streams open, so an idle Worker holds fewer
       Server streams. With ``min_open_slots`` unset or equal to
       ``max_concurrent_tasks`` the Worker is fixed and keeps every slot open,
       as before.

    Attributes:
        max_concurrent_tasks: The most tasks the Worker runs at once, including
            the reserved RPC slots. Each running task holds one long-lived poll
            stream. Zero uses the CPU count. Negative values are invalid. Start
            fails if an explicit value of a fixed Worker exceeds the Server's
            nonzero per-Worker limit reported by matching.maxConcurrentTasksCap.

            .. versionchanged:: 1.45.5
               This is the ceiling of the slot streams. A fixed Worker, whose
               ``min_open_slots`` is unset or equal to it, keeps this many
               streams open. An adaptive Worker, whose ``min_open_slots`` is
               below it, opens streams up to it only while every open
               Step-and-RPC stream is busy. Two cases clamp the value to the
               Server cap at the first registration, with a WARNING log that
               names both values, instead of failing start: a fixed Worker with
               this option unset, whose CPU-count default exceeds the cap, and
               an adaptive Worker, with this option set or unset. A fixed Worker
               with this option unset therefore opens one stream first, learns
               the cap from its registration, and then opens the others, which
               adds one round trip to start. A fixed Worker with an explicit
               value opens all its streams at once and still fails start above
               the cap. After a clamp, the ``reserved_rpc_tasks`` and
               ``min_open_slots`` defaults come from the clamped value. In an
               adaptive Worker, a lower cap reported on a reconnect lowers its
               capacity: it half-closes the surplus streams, RPC-only streams
               first, and keeps running. A fixed Worker keeps the earlier
               behavior.
        reserved_rpc_tasks: Slots accepting only RPCs. The remaining slots accept
            Steps and RPCs. Steps are never declined for capacity. None reserves
            one slot when the resolved task limit is at least two, otherwise zero.
            Zero disables reservation. Values must be nonnegative and smaller
            than the resolved task limit. With one slot and no explicit reserve,
            start warns that RPCs may wait behind long-running Steps.

            .. versionchanged:: 1.45.5
               None also reserves zero when ``min_open_slots`` is 1, and start
               then warns that RPCs may wait behind long-running Steps.
        idle_streams_per_queue: Deprecated; accepted for source compatibility and
            has no effect. ``max_concurrent_tasks`` and ``min_open_slots``
            determine the number of slot streams. Zero remains valid; negative
            values remain invalid.
        app_worker_id: Unique process identity for sticky routing. Empty generates
            an identity with eight random base32 characters. A supplied value
            must contain 1-63 ASCII letters, digits, dots, underscores, or hyphens,
            differ from every registered Flow type, and change after restart.
        stop_drain_timeout: Grace period for active tasks on stop. Zero uses 30
            seconds. Idle streams half-close immediately. Busy streams send final
            output before half-closing. Tasks arriving after stop begins are dropped.
            Dropped unstarted tasks return to Dex without consuming a retry.
            On timeout a busy stream is reset, its handler is canceled, and stop
            waits for the handler to return. Negative values are invalid.
            Busy Reconnect waits this long for server EOF after
            the final frame. During Stop, half-closed streams use the remaining
            drain deadline. A stream awaiting final-output confirmation gets up
            to five additional seconds. Synchronous handlers cannot be interrupted
            forcibly; an unresponsive handler can delay stop indefinitely.

        stop_on_permanent_stream_rejection: Whether the first permanent rejection
            leaving a slot kind without a registered stream drains the Worker and
            raises PullRegistrationError from start. Defaults to False, which
            retries indefinitely. Applies before and after initial readiness.
        task_queue: The named task queue this Worker polls. The empty default
            polls the default task queue, which serves every Flow started
            without a task queue.

            A Flow started with ``StartFlowOptions(task_queue=...)`` runs its
            Steps and RPCs only on Workers that poll that task queue and
            register its Flow type. A Worker polls exactly one task queue. To
            serve several task queues from one process, construct one Worker
            for each task queue, each with its own ``app_worker_id``.

            A name has 1 to 63 ASCII letters, digits, dots, underscores, or
            hyphens. Names starting with ``dex.`` are reserved and invalid
            here, including ``dex.default``; leave the field empty to poll the
            default task queue.

            A named task queue requires Dex Server protocol version 5 or later.
            Against an older Server, ``start`` raises ``RuntimeError`` before it
            synchronizes Attribute indexes or opens task streams. Each stream
            also checks that the Server registered it under this task queue.
            On a mismatch the Worker closes the stream without running any
            task it received and records a permanent rejection: see
            ``PullRegistrationError`` and ``pull_registration_state``.

            .. versionadded:: 1.45.1
        min_open_slots: The fewest slot streams the Worker keeps open. It uses
            the same unit as ``max_concurrent_tasks``, so it counts the reserved
            RPC-only slots too. Zero, the default, uses the resolved
            ``max_concurrent_tasks``: every slot keeps a stream, the Worker is
            fixed, and it behaves as earlier releases did. Negative values are
            invalid.

            A value below ``max_concurrent_tasks`` makes the Worker adaptive.
            Use it when idle Workers should hold fewer Server streams, such as a
            large fleet or a high ``max_concurrent_tasks``. An adaptive Worker
            starts with ``min_open_slots - reserved_rpc_tasks`` Step-and-RPC
            streams and opens the RPC-only streams after the first registration.
            When a task takes its last idle Step-and-RPC stream, it doubles its
            Step-and-RPC streams, one batch at a time, up to
            ``max_concurrent_tasks``. Every 30 seconds it half-closes half of
            the idle streams above the window's peak busy count plus 25%
            headroom. It never goes below the floor and never closes its last
            idle stream. Its RPC-only streams stay at ``reserved_rpc_tasks``.

            A burst larger than the open streams waits a few registration round
            trips while the Worker grows. A task that reaches a stream while it
            closes returns to the Server without spending a Step retry, and the
            Server dispatches it again. When the Server rejects a stream at its
            stream limit (RESOURCE_EXHAUSTED), an adaptive Worker treats the
            rejection of a growth stream, or of a reconnect after readiness, as
            a soft limit. It records no ``PullRegistrationError``. After a soft
            rejection, the Worker waits until another of its registered streams
            ends or the next 30-second shrink window starts, and then retries. A
            growth batch that keeps being rejected grows at most once per
            window. Use ``Worker.pull_slot_stream_state`` to observe the
            streams.

            A handler that keeps running after its stream ends, such as a
            synchronous handler during a Server restart, keeps its slot. The
            Worker registers no stream for that slot until the handler returns.

            Construction raises ``ValueError`` unless
            ``reserved_rpc_tasks + 1 <= min_open_slots <= max_concurrent_tasks``
            with the resolved values. When ``reserved_rpc_tasks`` is None, its
            default reserves one slot only when both ``max_concurrent_tasks``
            and ``min_open_slots`` are at least two. Start fails at the first
            registration when the Server cap lowers ``max_concurrent_tasks``
            below ``min_open_slots`` or to ``reserved_rpc_tasks`` or less; the
            error names the values and the cap.

            .. versionadded:: 1.45.5

    Raises:
        ValueError: If a numeric option is negative, the reserve reaches the task
            limit, ``min_open_slots`` is outside
            ``reserved_rpc_tasks + 1 <= min_open_slots <= max_concurrent_tasks``,
            app_worker_id is invalid, or task_queue is invalid.

    Examples:
        >>> pull = PullWorkerOptions(max_concurrent_tasks=8, reserved_rpc_tasks=2)
        >>> worker = Worker(registry, cache, WorkerOptions(pull=pull))
        >>> gpu_worker = Worker(
        ...     registry,
        ...     cache,
        ...     WorkerOptions(pull=PullWorkerOptions(task_queue="gpu")),
        ... )
        >>> # Between 8 and 64 slot streams, one of them reserved for RPCs.
        >>> adaptive = PullWorkerOptions(max_concurrent_tasks=64, min_open_slots=8)
        >>> adaptive_worker = Worker(registry, cache, WorkerOptions(pull=adaptive))
    """

    max_concurrent_tasks: int = 0
    idle_streams_per_queue: int = 0
    app_worker_id: str = ""
    stop_drain_timeout: timedelta = timedelta(0)
    stop_on_permanent_stream_rejection: bool = False
    reserved_rpc_tasks: int | None = None
    task_queue: str = ""
    min_open_slots: int = 0

    def __post_init__(self) -> None:
        task_queue_error = registered_task_queue_name_error(self.task_queue)
        if task_queue_error is not None:
            raise ValueError(
                f"task_queue {self.task_queue!r} is invalid: {task_queue_error}"
            )
        if self.max_concurrent_tasks < 0:
            raise ValueError("max_concurrent_tasks must not be negative")
        if self.min_open_slots < 0:
            raise ValueError("min_open_slots must not be negative")
        task_limit = self.max_concurrent_tasks or os.cpu_count() or 1
        open_slot_floor = self.min_open_slots or task_limit
        if open_slot_floor > task_limit:
            raise ValueError(
                f"min_open_slots {open_slot_floor} must not exceed "
                f"max_concurrent_tasks {task_limit}"
            )
        if self.reserved_rpc_tasks is not None and (
            self.reserved_rpc_tasks < 0 or self.reserved_rpc_tasks >= task_limit
        ):
            raise ValueError(
                "reserved_rpc_tasks must be nonnegative and less than max_concurrent_tasks"
            )
        reserved_rpc_tasks = (
            self.reserved_rpc_tasks
            if self.reserved_rpc_tasks is not None
            else _default_reserved_rpc_tasks(task_limit, open_slot_floor)
        )
        if reserved_rpc_tasks + 1 > open_slot_floor:
            raise ValueError(
                f"min_open_slots {open_slot_floor} must be at least "
                f"reserved_rpc_tasks {reserved_rpc_tasks} plus one Step-and-RPC slot"
            )
        if self.idle_streams_per_queue < 0:
            raise ValueError("idle_streams_per_queue must not be negative")
        if self.stop_drain_timeout < timedelta(0):
            raise ValueError("stop_drain_timeout must not be negative")
        if self.app_worker_id and (
            len(self.app_worker_id) > 63
            or not self.app_worker_id.isascii()
            or any(
                not (character.isalnum() or character in "._-")
                for character in self.app_worker_id
            )
        ):
            raise ValueError(
                "app_worker_id must contain 1-63 ASCII letters, digits, dots, underscores, or hyphens"
            )


@dataclass(frozen=True)
class WorkerOptions:
    """Configure Worker networking, pull polling, and startup synchronization.

    ``attribute_index_sync_timeout`` is the RPC deadline used before the Worker
    opens its outbound task streams. It defaults to two minutes and must be
    positive.

    Attributes:
        server_address: The Dex FlowService target used for startup synchronization
            and blob hydration. Defaults to ``"localhost:8801"``.
        attribute_index_sync_timeout: Maximum duration for startup Attribute index
            synchronization. Defaults to two minutes and must be positive.
        pull: Outbound polling options. Defaults to ``PullWorkerOptions()``.
            ``None`` is invalid because push Workers are no longer supported.
            Workers connect to ``server_address`` without an inbound listener.
        flow_service_tls: TLS for all outbound FlowService and task connections.
            Defaults to ``None`` for plaintext. See ``FlowServiceTlsOptions``
            for trust, address validation, and a complete example.
        credential: Supplies the current bearer token for FlowService calls and
            each pull stream open. ``None`` sends no authorization metadata.
            Token errors fail calls before Dex is contacted. Enable TLS
            to protect bearer tokens in transit. A rotated token is used on the next call or reconnect.
            AsyncWorker accepts awaitable tokens and bounds pull stream token
            lookup to 30 seconds. Synchronous token lookup must return promptly.
        grpc_max_message_bytes: Largest gRPC message this Worker exchanges with Dex,
            in bytes, on its FlowService calls and pull streams. Zero, the default,
            receives up to ``DEFAULT_GRPC_MAX_MESSAGE_BYTES`` (32 MiB) and leaves
            sends unlimited, so the server's limit bounds them. A positive value
            limits both directions. A negative value raises ``ValueError``.

            .. versionadded:: 1.38.0
        flow_definition_paths: Flow Definition Graph (FDG 2.x) JSON files, or
            directories searched recursively for files whose extension is
            ``.json`` in any letter case, that the Worker uploads when it
            starts. Defaults to none. Each file's ``flow.name`` is its Flow
            type. Its top-level ``generatedAt`` is the generation time; without
            it the file's modification time is used. Either time is truncated to
            whole milliseconds. Files are read when the Worker is constructed.
            A JSON file in a directory that is not an FDG 2.x document, such as
            ``package.json``, is skipped and logged at INFO. See "Flow
            definitions" below.

            .. versionadded:: 1.45.0
        flow_definitions: In-memory FDG documents that the Worker uploads when
            it starts. Defaults to none. Use them when the application renders
            its graph in code. See ``FlowDefinitionDocument``.

            .. versionadded:: 1.45.0

    Flow definitions:
        Dex Web draws a run's graph from the Flow definition that the Server
        stores for its Flow type. A Worker configured with
        ``flow_definition_paths`` or ``flow_definitions`` uploads them once per
        start, after protocol negotiation and Attribute index synchronization
        and before it opens task streams. All upload requests share one deadline,
        ``attribute_index_sync_timeout``, and ``stop`` cancels an upload in
        flight. It uploads only definitions whose Flow
        type this Worker registers and logs the skipped ones at INFO level. It
        rewrites an absolute ``source.path`` inside each document to a path
        relative to the current working directory, or to the file name.

        The Server replaces a stored definition only with a different one
        generated later. The Worker logs created and replaced definitions at
        INFO, unchanged ones at DEBUG, and a kept newer definition at WARNING.
        Upload failures, including a Server without this API, are logged at
        WARNING and never stop the Worker from starting. Use
        ``Client.upload_flow_definitions`` with ``force=True`` to roll a
        definition back.

        The Worker constructor raises ``ValueError`` for a missing or unreadable
        path, an explicitly listed file or an in-memory document that is not a
        JSON object with a ``schemaVersion`` starting with ``"2."`` and a
        ``flow.name`` string, a ``generatedAt`` that is not strict RFC 3339
        (``T`` or ``t``, then ``Z``, ``z``, ``+HH:MM``, or ``-HH:MM``), also
        inside an in-memory document, an in-memory document whose ``flow.name`` differs from its Flow type, or
        two definitions for the same Flow type from any sources.

    Examples:
        >>> options = WorkerOptions(
        ...     server_address="dex.internal:8801",
        ...     flow_definition_paths=("build/flow-definitions",),
        ... )
        >>> worker = Worker(registry, cache, options)
    """

    server_address: str = "localhost:8801"
    attribute_index_sync_timeout: timedelta = timedelta(minutes=2)
    pull: PullWorkerOptions = field(default_factory=PullWorkerOptions)
    credential: Credential | None = None
    flow_service_tls: FlowServiceTlsOptions | None = None
    grpc_max_message_bytes: int = 0
    flow_definition_paths: Sequence[str | os.PathLike[str]] = ()
    flow_definitions: Sequence[FlowDefinitionDocument] = ()

    def __post_init__(self) -> None:
        validate_grpc_max_message_bytes(self.grpc_max_message_bytes)
        if isinstance(self.flow_definition_paths, (str, bytes, os.PathLike)):
            raise TypeError("flow_definition_paths must be a sequence of paths")
        object.__setattr__(
            self, "flow_definition_paths", tuple(self.flow_definition_paths)
        )
        object.__setattr__(self, "flow_definitions", tuple(self.flow_definitions))
        for document in self.flow_definitions:
            if not isinstance(document, FlowDefinitionDocument):
                raise TypeError(
                    "flow_definitions must contain FlowDefinitionDocument values"
                )
        if self.pull is None:
            raise ValueError(
                "push Workers are no longer supported; use PullWorkerOptions() instead of pull=None"
            )
        if self.attribute_index_sync_timeout <= timedelta(0):
            raise ValueError("attribute_index_sync_timeout must be positive")
