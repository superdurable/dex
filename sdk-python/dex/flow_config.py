# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from dataclasses import dataclass
from enum import Enum

from dex.step import StepDurability


class ActiveStepSearchMode(Enum):
    """Control which active Steps are included in Flow search indexing.

    Attributes:
        DEFAULT: Use the Dex server's current default policy.
        ALL: Index every active Step.
        WITH_WAIT_FOR: Index only active Steps that define ``wait_for``.
        DISABLED: Do not index active Steps.
    """

    DEFAULT = "default"
    ALL = "all"
    WITH_WAIT_FOR = "with_wait_for"
    DISABLED = "disabled"


@dataclass(frozen=True)
class FlowConfig:
    """Override server behavior for one Flow execution.

    At start time, every ``None`` field uses the registered or server default.
    Client configuration updates preserve existing values for ``None`` fields.

    Attribute Store projection is asynchronous. Failures never roll back Flow
    Attributes, and already queued projections retain their original target.

    Attributes:
        active_step_search_mode: Optional active-Step visibility indexing policy.
        step_durability: Optional default durability for Step handlers.
        attribute_store_names: Optional Server-configured Attribute Store names.
            ``None`` leaves the field absent; an empty list disables future projections.
        history_window_events: About how many of the newest history events the
            run keeps for history reads and Time Travel. ``None``, the default,
            uses the server's ``history.windowEvents`` setting, which defaults to
            10000.

            Raise it for a run that you inspect or travel back in often. Lower it
            for a run that writes many events and needs less disk. Dex prunes the
            oldest events in chunks, and only between commands, so a run keeps
            slightly more or fewer events than the value. A command that changes
            the Flow but records no event counts as one event. The first event of
            the run is always kept, so resetting to the beginning of the run always
            works. History reads no longer return pruned events, and Time Travel
            targets only commands inside the window.

            Set it at start through ``StartFlowOptions.config_override``, or change
            it on an active Flow with ``update_flow_config``. A smaller value takes
            effect at the run's next pruning. A larger value keeps more of the
            events recorded afterwards; pruned events never come back. Once set,
            the value can be changed but not cleared back to the server default:
            ``None`` in an update keeps the current value. A SubFlow inherits the
            value from its parent unless its own ``config_override`` sets it.

            The value must be positive and at most the server's
            ``history.maxWindowEvents`` setting, which defaults to 100000.
            The field is a signed 32-bit integer, so only 1 to 2147483647 can be
            sent. A value outside that range raises ``ValueError`` locally,
            before any request. ``start_flow`` and ``update_flow_config`` raise
            ``DexServiceError`` with code ``grpc.StatusCode.INVALID_ARGUMENT`` for
            zero, a negative value, or a value above the server limit. A
            Step that starts a SubFlow with such an override is an invalid Worker
            response: the Step attempt fails and Dex retries it under the Step's
            retry policy. Servers older than server-zd 0.1.21 ignore the field.

            Example:
                >>> client.update_flow_config(
                ...     "order-42", FlowConfig(history_window_events=50_000)
                ... )

            .. versionadded:: 1.38.0
        delete_expired_history_objects: Has no effect. The SDK does not send it
            to the server.

            server-zd 0.1.23 and later ignore the field for runs that start with
            them. Such a run, when it offloads its history, always deletes the
            objects of expired history segments about one hour after the history
            window passes them.

            A run that started under server-zd 0.1.22 still follows the value it
            recorded then. When that value is false, the run's expired history
            objects stay in object storage until its retention ends, and a run
            that never closes keeps them. To delete them sooner, set the field to
            true for that run with UpdateFlowConfig through an SDK older than
            1.40.0 or a direct gRPC request. See
            https://docs.superdurable.io/reference/server/configuration#history-offload.

            The field remains so existing code keeps working.

            .. versionadded:: 1.39.0

            .. deprecated:: 1.40.0
               The SDK no longer sends the field, and runs that start with
               server-zd 0.1.23 or later ignore it. Remove this field from
               your code.
        offload_history: Whether Dex offloads the run's history to object
            storage. ``None``, the default, means ``True``.

            An offloaded run uploads its history to object storage in segment
            objects. Once a segment is uploaded and a grace period has passed,
            Dex deletes its history records and superseded values from the
            local disk. History reads then fetch them from object storage. When
            the history window passes a whole segment, Dex deletes the
            segment's object about one hour later.

            A run that does not offload keeps its history on the local disk. It
            never writes a history object. The history window still applies:
            Dex prunes the oldest events and deletes the superseded values that
            the window passed.

            Leave it ``None`` for most runs. A long-running run then keeps only
            its newest history and current state on the local disk. Set it to
            ``False`` for many short runs that close soon after they start.
            Each offloaded run writes at least one history object when it
            closes. It deletes that object when its retention ends. Short runs
            at a high rate therefore add object storage requests, while their
            small history costs little local disk.
            A run that does not offload keeps every superseded value its history window
            covers on the local disk, with no byte limit; for a run that rewrites large
            values, keep offload or set a smaller history window.

            Set it only at start, through ``StartFlowOptions.config_override``,
            or for a SubFlow through its ``SubFlowOptions.config_override``. A
            SubFlow inherits the value from its parent unless its own override
            sets it. The run keeps the value for its whole life.
            ``update_flow_config`` with ``offload_history`` set raises
            ``DexServiceError`` with code ``grpc.StatusCode.INVALID_ARGUMENT``.
            Runs that started before server-zd 0.1.23 keep the history offload
            behavior that their server gave them. Servers older than server-zd
            0.1.23 ignore the field.

            Example:
                >>> client.start_flow(
                ...     notification_flow,
                ...     "notification-42",
                ...     None,
                ...     StartFlowOptions(
                ...         config_override=FlowConfig(offload_history=False)
                ...     ),
                ... )

            .. versionadded:: 1.40.0
    """

    active_step_search_mode: ActiveStepSearchMode | None = None
    step_durability: StepDurability | None = None
    attribute_store_names: list[str] | None = None
    history_window_events: int | None = None
    delete_expired_history_objects: bool | None = None
    offload_history: bool | None = None
