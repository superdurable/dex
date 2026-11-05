# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The record a tool Step's failure route writes, by the recovery error's attempt progress."""

from __future__ import annotations

from dataclasses import replace

import pytest
from dex import RecoveryErrorInfo, StepAttemptProgress

from dex_agents import (
    TOOL_CALL_NOT_RUN_ERROR_PREFIX,
    TOOL_TASK_ABANDONED_ERROR,
    AgentToolCall,
    QuestionAnswer,
    ToolCallCancellationReason,
    ToolCallStatus,
    ToolEffect,
)
from dex_agents.session_flow import _recovered_tool_call

APPROVED_CALL = AgentToolCall(
    tool_call_id="call-1",
    turn_id="turn-1",
    tool_name="publish",
    arguments="{}",
    effect=ToolEffect.NON_IDEMPOTENT_WRITE,
    requires_approval=False,
    status=ToolCallStatus.APPROVED,
)


def _failure(progress: StepAttemptProgress) -> RecoveryErrorInfo:
    return RecoveryErrorInfo("worker lost", "FLOW_ERROR_TYPE_WORKER_API_FAIL", progress)


@pytest.mark.parametrize("status", [ToolCallStatus.OUTCOME_UNKNOWN, ToolCallStatus.FAILED])
def test_a_call_whose_tool_step_never_started_records_failed_and_safe_to_call_again(
    status: ToolCallStatus,
) -> None:
    recorded = _recovered_tool_call(
        APPROVED_CALL, _failure(StepAttemptProgress.NOT_STARTED), status
    )

    assert recorded.status is ToolCallStatus.FAILED
    assert recorded.error == f"{TOOL_CALL_NOT_RUN_ERROR_PREFIX}: worker lost"


@pytest.mark.parametrize(
    "progress", [StepAttemptProgress.NOT_COMPLETED, StepAttemptProgress.UNSPECIFIED]
)
def test_a_call_whose_tool_may_have_run_keeps_the_status_of_its_effect(
    progress: StepAttemptProgress,
) -> None:
    recorded = _recovered_tool_call(
        APPROVED_CALL, _failure(progress), ToolCallStatus.OUTCOME_UNKNOWN
    )

    assert recorded.status is ToolCallStatus.OUTCOME_UNKNOWN
    assert recorded.error == "worker lost"


def test_a_failure_without_a_recovery_error_keeps_outcome_unknown() -> None:
    recorded = _recovered_tool_call(APPROVED_CALL, None, ToolCallStatus.OUTCOME_UNKNOWN)

    assert recorded.status is ToolCallStatus.OUTCOME_UNKNOWN
    assert recorded.error == "unknown failure"


def test_a_task_poll_that_never_started_keeps_outcome_unknown_because_the_task_ran() -> None:
    waiting = replace(APPROVED_CALL, status=ToolCallStatus.WAITING_FOR_TASK, task_id="task-1")

    recorded = _recovered_tool_call(
        waiting, _failure(StepAttemptProgress.NOT_STARTED), ToolCallStatus.OUTCOME_UNKNOWN
    )

    assert recorded.status is ToolCallStatus.OUTCOME_UNKNOWN
    assert recorded.error == "worker lost"


def test_an_abandoned_task_whose_cancellation_never_started_keeps_its_abandoned_error() -> None:
    abandoned = replace(
        APPROVED_CALL,
        status=ToolCallStatus.WAITING_FOR_TASK,
        task_id="task-1",
        cancellation_reason=ToolCallCancellationReason.TURN_CANCELLED,
    )

    recorded = _recovered_tool_call(
        abandoned, _failure(StepAttemptProgress.NOT_STARTED), ToolCallStatus.OUTCOME_UNKNOWN
    )

    assert recorded.status is ToolCallStatus.OUTCOME_UNKNOWN
    assert recorded.error == TOOL_TASK_ABANDONED_ERROR


def test_a_call_that_asked_a_question_already_ran_its_tool() -> None:
    answered = replace(
        APPROVED_CALL,
        input_answers=[
            QuestionAnswer(interrupt_id="call-1-input-1", question="Which list?", answer="news")
        ],
    )

    recorded = _recovered_tool_call(
        answered, _failure(StepAttemptProgress.NOT_STARTED), ToolCallStatus.OUTCOME_UNKNOWN
    )

    assert recorded.status is ToolCallStatus.OUTCOME_UNKNOWN
    assert recorded.error == "worker lost"
