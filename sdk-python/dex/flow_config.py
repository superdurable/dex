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
    DEFAULT = "default"
    ALL = "all"
    WITH_WAIT_FOR = "with_wait_for"
    DISABLED = "disabled"


@dataclass(frozen=True)
class FlowConfig:
    active_step_search_mode: ActiveStepSearchMode | None = None
    continue_as_new_threshold: int | None = None
    continue_as_new_page_size_bytes: int | None = None
    step_durability: StepDurability | None = None
    worker_target: WorkerTarget | None = None
