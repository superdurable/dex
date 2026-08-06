# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from dataclasses import dataclass
from enum import Enum

from dex.step import StepDurability
from dex.worker_options import WorkerTarget


class ActiveStepSearchMode(Enum):
    DEFAULT = "default"
    ALL = "all"


@dataclass(frozen=True)
class FlowConfig:
    active_step_search_mode: ActiveStepSearchMode | None = None
    continue_as_new_threshold: int | None = None
    continue_as_new_page_size_bytes: int | None = None
    step_durability: StepDurability | None = None
    worker_target: WorkerTarget | None = None
