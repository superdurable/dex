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

from dataclasses import dataclass
from typing import Optional

from dex.dex_api.models import WorkflowResetType


@dataclass
class ResetWorkflowTypeAndOptions:
    reset_type: WorkflowResetType
    reason: str
    history_event_id: Optional[int] = None
    history_event_time: Optional[str] = None
    state_id: Optional[str] = None
    state_execution_id: Optional[str] = None
    skip_signal_reapply: Optional[bool] = None


def reset_to_beginning(reason: str) -> ResetWorkflowTypeAndOptions:
    return ResetWorkflowTypeAndOptions(
        reset_type=WorkflowResetType.BEGINNING,
        reason=reason,
    )


def reset_to_history_event_id(
    history_event_id: int,
    reason: str,
) -> ResetWorkflowTypeAndOptions:
    return ResetWorkflowTypeAndOptions(
        reset_type=WorkflowResetType.HISTORY_EVENT_ID,
        history_event_id=history_event_id,
        reason=reason,
    )


def reset_to_history_event_time(
    history_event_time: str,
    reason: str,
) -> ResetWorkflowTypeAndOptions:
    return ResetWorkflowTypeAndOptions(
        reset_type=WorkflowResetType.HISTORY_EVENT_TIME,
        history_event_time=history_event_time,
        reason=reason,
    )


def reset_to_state_id(state_id: str, reason: str) -> ResetWorkflowTypeAndOptions:
    return ResetWorkflowTypeAndOptions(
        reset_type=WorkflowResetType.STATE_ID,
        state_id=state_id,
        reason=reason,
    )


def reset_to_state_execution_id(
    state_execution: str,
    reason: str,
) -> ResetWorkflowTypeAndOptions:
    return ResetWorkflowTypeAndOptions(
        reset_type=WorkflowResetType.STATE_EXECUTION_ID,
        state_execution_id=state_execution,
        reason=reason,
    )
