# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from dataclasses import dataclass, field
from typing import Any, Optional

from dex.dex_api.models import (
    IDReusePolicy,
    WorkflowRetryPolicy,
    WorkflowAlreadyStartedOptions,
    WorkflowConfig,
)
from dex.workflow_state import (
    WorkflowState,
    get_state_id_by_class,
    get_state_execution_id,
)


@dataclass
class WorkflowOptions:
    workflow_id_reuse_policy: Optional[IDReusePolicy] = None
    workflow_cron_schedule: Optional[str] = None
    workflow_start_delay_seconds: Optional[int] = None
    workflow_retry_policy: Optional[WorkflowRetryPolicy] = None
    workflow_already_started_options: Optional[WorkflowAlreadyStartedOptions] = None
    workflow_config_override: Optional[WorkflowConfig] = None
    initial_data_attributes: Optional[dict[str, Any]] = None
    _wait_for_completion_state_ids: list[str] = field(default_factory=list)
    _wait_for_completion_state_execution_ids: list[str] = field(default_factory=list)
    initial_search_attributes: Optional[dict[str, Any]] = None

    @property
    def wait_for_completion_state_ids(self) -> Optional[list[str]]:
        return self._wait_for_completion_state_ids

    @wait_for_completion_state_ids.setter
    def wait_for_completion_state_ids(self, *states: type[WorkflowState]):
        state_ids: list[str] = []
        for state in states:
            state_ids.append(get_state_id_by_class(state))
        self._wait_for_completion_state_ids = state_ids

    def add_wait_for_completion_state_ids(self, *states: type[WorkflowState]):
        for state in states:
            self._wait_for_completion_state_ids.append(get_state_id_by_class(state))

    @property
    def wait_for_completion_state_execution_ids(self) -> Optional[list[str]]:
        return self._wait_for_completion_state_execution_ids

    @wait_for_completion_state_execution_ids.setter
    def wait_for_completion_state_execution_ids(self, val):
        try:
            state, number = val
        except ValueError:
            raise ValueError(
                "Pass an iterable with two items: state: type[WorkflowState] and number: int"
            )
        else:
            state_id = get_state_execution_id(state, number)
            self._wait_for_completion_state_execution_ids = state_id

    def add_wait_for_completion_state_execution_id(
        self, state: type[WorkflowState], number: int
    ):
        state_id = get_state_execution_id(state, number)
        self._wait_for_completion_state_execution_ids.append(state_id)
