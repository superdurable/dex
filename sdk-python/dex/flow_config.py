# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dataclasses import dataclass
from enum import Enum

from dex.step import StepDurability
from dex.worker_options import WorkerTarget


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

    Every ``None`` field uses the registered or server default. Config may be set
    at start time or replaced on an active Flow through the Client.

    Attribute Store projection is asynchronous. Failures never roll back Flow
    Attributes, and already queued projections retain their original target.

    Attributes:
        active_step_search_mode: Optional active-Step visibility indexing policy.
        continue_as_new_threshold: Optional positive history-event threshold that
            requests continue-as-new.
        continue_as_new_page_size_bytes: Optional positive history page-size budget
            in bytes used by continue-as-new decisions.
        step_durability: Optional default durability for Step handlers.
        worker_target: Optional Worker endpoint for later handler calls.
        attribute_store_names: Optional Server-configured Attribute Store names.
            ``None`` leaves the field absent; an empty list disables future projections.
    """

    active_step_search_mode: ActiveStepSearchMode | None = None
    continue_as_new_threshold: int | None = None
    continue_as_new_page_size_bytes: int | None = None
    step_durability: StepDurability | None = None
    worker_target: WorkerTarget | None = None
    attribute_store_names: list[str] | None = None
