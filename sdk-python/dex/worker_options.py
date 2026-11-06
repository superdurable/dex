# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import os
from dataclasses import dataclass, field
from datetime import timedelta

from dex.credential import Credential
from dex.flow_service_tls_options import FlowServiceTlsOptions


@dataclass(frozen=True)
class PullWorkerOptions:
    """Configure outbound task polling for a Worker or AsyncWorker.

    Pass this to ``WorkerOptions(pull=PullWorkerOptions())``. Each stream runs
    one task at a time.
    Handlers may run concurrently and must synchronize shared state.

    Attributes:
        max_concurrent_tasks: Execution slots, each owning one long-lived poll
            stream. Zero uses the CPU count. Negative values are invalid. Start
            fails if this exceeds the Server's nonzero per-Worker limit reported
            by matching.maxConcurrentTasksCap.
        reserved_rpc_tasks: Slots accepting only RPCs. The remaining slots accept
            Steps and RPCs. Steps are never declined for capacity. None reserves
            one slot when the resolved task limit is at least two, otherwise zero.
            Zero disables reservation. Values must be nonnegative and smaller
            than the resolved task limit. With one slot and no explicit reserve,
            start warns that RPCs may wait behind long-running Steps.
        idle_streams_per_queue: Deprecated; accepted for source compatibility and
            has no effect. The Worker opens exactly max_concurrent_tasks streams.
            Zero remains valid; negative values remain invalid.
        app_worker_id: Unique process identity for sticky routing. Empty generates
            an identity with eight random base32 characters. A supplied value
            must contain 1-63 ASCII letters, digits, dots, underscores, or hyphens,
            differ from every registered Flow type, and change after restart.
        stop_drain_timeout: Grace period for active tasks on stop. Zero uses 30
            seconds. Idle streams half-close immediately. Busy streams send final
            output before half-closing. Tasks arriving after stop begins are dropped.
            On timeout a busy stream is reset, its handler is canceled, and stop
            waits for the handler to return. Negative values are invalid.
            Busy Reconnect waits this long for server EOF after
            the final frame. During Stop, half-closed streams use the remaining
            drain deadline.

        stop_on_permanent_stream_rejection: Whether the first permanent rejection
            leaving a slot kind without a registered stream drains the Worker and
            raises PullRegistrationError from start. Defaults to False, which
            retries indefinitely. Applies before and after initial readiness.

    Raises:
        ValueError: If a numeric option is negative, the reserve reaches the task
            limit, or app_worker_id is invalid.

    Examples:
        >>> pull = PullWorkerOptions(max_concurrent_tasks=8, reserved_rpc_tasks=2)
        >>> worker = Worker(registry, cache, WorkerOptions(pull=pull))
    """

    max_concurrent_tasks: int = 0
    idle_streams_per_queue: int = 0
    app_worker_id: str = ""
    stop_drain_timeout: timedelta = timedelta(0)
    stop_on_permanent_stream_rejection: bool = False
    reserved_rpc_tasks: int | None = None

    def __post_init__(self) -> None:
        if self.max_concurrent_tasks < 0:
            raise ValueError("max_concurrent_tasks must not be negative")
        task_limit = self.max_concurrent_tasks or os.cpu_count() or 1
        if self.reserved_rpc_tasks is not None and (
            self.reserved_rpc_tasks < 0 or self.reserved_rpc_tasks >= task_limit
        ):
            raise ValueError(
                "reserved_rpc_tasks must be nonnegative and less than max_concurrent_tasks"
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
    """

    server_address: str = "localhost:8801"
    attribute_index_sync_timeout: timedelta = timedelta(minutes=2)
    pull: PullWorkerOptions = field(default_factory=PullWorkerOptions)
    credential: Credential | None = None
    flow_service_tls: FlowServiceTlsOptions | None = None

    def __post_init__(self) -> None:
        if self.pull is None:
            raise ValueError(
                "push Workers are no longer supported; use PullWorkerOptions() instead of pull=None"
            )
        if self.attribute_index_sync_timeout <= timedelta(0):
            raise ValueError("attribute_index_sync_timeout must be positive")
