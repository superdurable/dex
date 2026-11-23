# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from dataclasses import dataclass

from dex.attribute import AttributeLock, AttributeMapLoad
from dex.channel import ChannelMapLoad


@dataclass(frozen=True)
class RPCInvokeOptions:
    """Add runtime-selected map instances and a request ID to one RPC invocation.

    Dex unions these selections with the RPC decorator's fixed locks and state loads,
    then sorts and deduplicates their physical names. Locking and loading are
    independent. A read-modify-write handler must include the same AttributeMap
    instance in both collections.

    Set ``request_id`` when one logical call may be sent more than once, so that Dex
    applies the RPC's Flow state changes at most once. The options are immutable;
    reuse one instance for every attempt of the same call.

    Attributes:
        lock_attribute_map_instances: AttributeMap instance locks for this call.
        load_attribute_map_instances: Exact AttributeMap instance snapshots.
        load_channel_map_instances: Exact ChannelMap pending-message snapshots.
        request_id: Optional non-empty request ID that identifies this invocation
            for Dex request deduplication. ``None``, the default, sends a new
            SDK-generated UUID on every call, so every call runs as a new request.
            An empty string raises ``ValueError`` when the options are
            constructed. Dex Server sets no other length or character rule.

            Set a stable request ID when one logical call may be sent more than
            once. Examples are a retry after a call whose outcome is unknown, such
            as one that ended with a timeout or with UNAVAILABLE, and a Trigger
            that delivers each event at least once and retries a failed delivery.
            Derive the ID from the business event. When one event invokes several
            RPCs on the same Flow run, give each RPC its own ID, such as the event
            ID followed by the RPC name.

            Dex applies an RPC's Flow state changes at most once per request ID.
            Dex Server records the request ID and the reply when an attempt's
            result commits. What a later attempt with the same request ID on the
            same Flow run returns depends on the RPC and on that attempt:

            - A transactional or locking RPC, declared with ``is_transactional``
              or ``lock_attributes`` of ``@rpc``, returns the recorded reply
              before a Worker runs the handler. Concurrent calls with the same
              request ID share one handler execution and its result.
            - Any other RPC runs its handler on a Worker again. When this
              attempt's result changes Flow state or records an event, Dex
              discards it and returns the recorded reply. When this attempt's
              result changes no Flow state and records no event, Dex returns its
              new output without looking up the request ID.

            Use a transactional or locking RPC when every repeat must return the
            recorded reply. For example, a non-transactional handler that marks an
            event processed and returns ``"accepted"`` returns
            ``"already_processed"`` to a repeat that sees the mark and writes
            nothing. For every RPC, the handler's side effects outside Dex, such
            as an HTTP call, can repeat.

            A call that the Server fails before any commit records nothing, and a
            retry with the same request ID runs the RPC again. Such failures
            include ``WorkerInvocationError``, ``RpcLockConflictError``, and the
            RPC's handler timeout expiring while the handler runs.

            A call that ends on the caller's side, such as a timeout before the
            reply arrives, has an unknown outcome. The RPC may still commit after
            the call raises. Retry it with the same request ID: when the first
            call committed, the retry applies no changes again, and otherwise it
            runs the RPC. For a transactional or locking RPC, a retry joins the
            first call while it is in flight, or returns its recorded reply after
            it commits.

            Dex compares only the request ID, as it does for ``start_flow``. A
            call with a recorded request ID is a repeat even when its RPC name,
            input, or options differ. Dex answers it as described above and
            applies none of its Flow changes. Build every attempt's input from
            the stored event, so that a repeat carries the request the first call
            sent. Dex Server 0.1.36 and earlier compare the whole request instead.
            They reject a different request under a recorded request ID with
            ``DexServiceError`` whose ``code`` is ``grpc.StatusCode.INVALID_ARGUMENT``
            and whose ``detail`` says the request ID "was already used for a
            different request". To those servers, a Client with
            ``payload_encryption`` sends a different request on every attempt,
            because each attempt encrypts the input with a new nonce.

            Each Flow run keeps only its newest request IDs. The Server limit
            ``limits.maxRequestIdsPerRun`` sets how many, 1000 by default. Request
            IDs of the run's other recorded Client requests, such as Attribute
            writes, count toward the same limit. A call with an older, forgotten
            request ID runs as a new request. A new run of the same Flow ID starts
            with no recorded request IDs. Each kept request ID counts toward the
            run's Flow state budget, so keep request IDs short.

            After the Flow closes, a transactional or locking RPC with a recorded
            request ID still returns its recorded reply, until Dex deletes the
            closed run after its retention period. With a request ID that is not
            recorded, it raises ``FlowNotActiveOrNotFoundError``. A run over its
            Flow state budget, the Server limit ``limits.maxFlowStateBytes``,
            keeps no request ID for an RPC that changes no Flow state, so a retry
            of such an RPC runs again.

            .. versionadded:: 1.45.17

    Raises:
        ValueError: If ``request_id`` is an empty string.

    Examples:
        >>> options = RPCInvokeOptions(
        ...     lock_attribute_map_instances=(profiles.lock(partition),),
        ...     load_attribute_map_instances=(profiles.load(partition),),
        ... )
        >>> client.invoke_rpc(directory.upsert_customer_profile, flow_id, profile,
        ...                   options=options)

        Deliver an event to a transactional RPC, retrying while the outcome is
        unknown. Each retry for the same event adds ``event.delta`` at most once:

        >>> import time
        >>> import grpc
        >>> from dex import DexServiceError, RPCInvokeOptions
        >>> # increment_counter is declared with @rpc(is_transactional=True).
        >>> def deliver_counter_event(event: CounterEvent) -> int:
        ...     options = RPCInvokeOptions(request_id=f"{event.id}/increment_counter")
        ...     for attempt in range(1, 6):
        ...         try:
        ...             return client.invoke_rpc(
        ...                 counters.increment_counter,
        ...                 event.flow_id,
        ...                 event.delta,
        ...                 options=options,
        ...             )
        ...         except DexServiceError as error:
        ...             is_unknown_outcome = error.code in (
        ...                 grpc.StatusCode.UNAVAILABLE,
        ...                 grpc.StatusCode.DEADLINE_EXCEEDED,
        ...             )
        ...             if not is_unknown_outcome or attempt == 5:
        ...                 raise
        ...             time.sleep(attempt)
        ...     raise AssertionError("unreachable")
    """

    lock_attribute_map_instances: tuple[AttributeLock, ...] = ()
    load_attribute_map_instances: tuple[AttributeMapLoad, ...] = ()
    load_channel_map_instances: tuple[ChannelMapLoad, ...] = ()
    request_id: str | None = None

    def __post_init__(self) -> None:
        if self.request_id == "":
            raise ValueError("RPC invoke request_id must not be empty")
