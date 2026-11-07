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
        continue_as_new_threshold: Optional positive history-event threshold that
            requests continue-as-new.
        continue_as_new_page_size_bytes: Optional positive history page-size budget
            in bytes used by continue-as-new decisions.
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
            ``start_flow`` and ``update_flow_config`` raise ``DexServiceError``
            with code ``grpc.StatusCode.INVALID_ARGUMENT`` for any other value. A
            Step that starts a SubFlow with such an override is an invalid Worker
            response: the Step attempt fails and Dex retries it under the Step's
            retry policy. Servers older than server-zd 0.1.21 ignore the field.

            Example:
                >>> client.update_flow_config(
                ...     "order-42", FlowConfig(history_window_events=50_000)
                ... )

            .. versionadded:: 1.38.0
    """

    active_step_search_mode: ActiveStepSearchMode | None = None
    continue_as_new_threshold: int | None = None
    continue_as_new_page_size_bytes: int | None = None
    step_durability: StepDurability | None = None
    attribute_store_names: list[str] | None = None
    history_window_events: int | None = None
