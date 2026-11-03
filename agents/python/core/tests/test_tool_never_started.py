# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0


"""Which failed tool calls the session records as never run."""

from __future__ import annotations

from dataclasses import replace
from types import SimpleNamespace
from typing import Any, cast

import pytest
from dex import Context, RecoveryErrorInfo, StepAttemptProgress

from dex_agents import AgentToolCall, ToolCallStatus, ToolEffect
from dex_agents.session_flow import _is_tool_never_started

APPROVED_CALL = AgentToolCall(
    tool_call_id="call-1",
    turn_id="turn-1",
    tool_name="publish",
    arguments="{}",
    effect=ToolEffect.NON_IDEMPOTENT_WRITE,
    requires_approval=False,
    status=ToolCallStatus.APPROVED,
)


def _failed(progress: StepAttemptProgress) -> Context:
    error = RecoveryErrorInfo("lost", "FLOW_ERROR_TYPE_WORKER_API_FAIL", progress)
    return cast(Context, cast(Any, SimpleNamespace(recovery_error=error)))


@pytest.mark.parametrize(
    ("progress", "is_stamped", "is_never_started"),
    [
        (StepAttemptProgress.NOT_ASSIGNED, True, True),
        (StepAttemptProgress.ASSIGNED, True, True),
        (StepAttemptProgress.STARTED, True, False),
        (StepAttemptProgress.UNSPECIFIED, True, False),
        # A release before 0.2.0 ran the tool without a mark, so ASSIGNED may mean it ran.
        (StepAttemptProgress.ASSIGNED, False, False),
        (StepAttemptProgress.NOT_ASSIGNED, False, True),
    ],
)
def test_only_a_stamped_call_counts_assigned_as_never_started(
    progress: StepAttemptProgress, is_stamped: bool, is_never_started: bool
) -> None:
    call = replace(APPROVED_CALL, marks_attempt_started=is_stamped)
    assert _is_tool_never_started(_failed(progress), call) is is_never_started


def test_a_call_waiting_for_its_task_is_never_counted_as_not_run() -> None:
    call = replace(
        APPROVED_CALL,
        status=ToolCallStatus.WAITING_FOR_TASK,
        task_id="task-1",
        marks_attempt_started=True,
    )
    assert not _is_tool_never_started(_failed(StepAttemptProgress.NOT_ASSIGNED), call)
