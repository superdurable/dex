# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The record a tool Step's failure route writes, by the recovery error's attempt progress."""

from __future__ import annotations

import json
from dataclasses import replace
from pathlib import Path
from typing import Any

import pytest
from dex import RecoveryErrorInfo, StepAttemptProgress
from dex.codec import CodecRegistry

from dex_agents import (
    OUTCOME_UNKNOWN_ERROR_MAX_BYTES,
    OUTCOME_UNKNOWN_ERROR_TRUNCATION_MARKER,
    OUTCOME_UNKNOWN_THROWN_ERROR_TOOL_RESULT,
    OUTCOME_UNKNOWN_TOOL_RESULT,
    TOOL_CALL_NOT_RUN_ERROR_PREFIX,
    TOOL_TASK_ABANDONED_ERROR,
    AgentToolCall,
    QuestionAnswer,
    ToolCallCancellationReason,
    ToolCallStatus,
    ToolEffect,
    describe_tool_call_outcome,
)
from dex_agents.session_flow import _recovered_tool_call, _tool_execution_error_status

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


TOOL_EXECUTION_ERROR_CASES: list[dict[str, Any]] = json.loads(
    (Path(__file__).resolve().parents[3] / "conformance" / "tool_execution_errors.json").read_text()
)["cases"]


@pytest.mark.parametrize(
    "case", TOOL_EXECUTION_ERROR_CASES, ids=[case["name"] for case in TOOL_EXECUTION_ERROR_CASES]
)
def test_the_status_a_tool_execution_error_records(case: dict[str, Any]) -> None:
    tool_call = replace(
        APPROVED_CALL,
        effect=ToolEffect(case["effect"]),
        tool_source_reference=case["toolSourceReference"],
    )
    assert _tool_execution_error_status(tool_call, case["attempt"]) is ToolCallStatus(
        case["status"]
    )


TOOL_STEP_FAILURE_CASES: list[dict[str, Any]] = json.loads(
    (Path(__file__).resolve().parents[3] / "conformance" / "tool_step_failures.json").read_text()
)["cases"]

THROWN = RecoveryErrorInfo(
    "side-effect service 400: charge already refunded",
    "builtins.RuntimeError",
    StepAttemptProgress.NOT_COMPLETED,
)


@pytest.mark.parametrize(
    "case", TOOL_STEP_FAILURE_CASES, ids=[case["name"] for case in TOOL_STEP_FAILURE_CASES]
)
def test_whether_a_failed_tool_steps_last_attempt_threw(case: dict[str, Any]) -> None:
    recovery_error = (
        None
        if case["recoveryError"] is None
        else RecoveryErrorInfo(
            case["recoveryError"]["detail"],
            case["recoveryError"]["errorType"],
            StepAttemptProgress.NOT_COMPLETED,
        )
    )

    recorded = _recovered_tool_call(APPROVED_CALL, recovery_error, ToolCallStatus.OUTCOME_UNKNOWN)

    assert recorded.status is ToolCallStatus.OUTCOME_UNKNOWN
    assert recorded.error == case["error"]
    assert recorded.tool_step_threw is case["toolStepThrew"]
    assert describe_tool_call_outcome(recorded) == (
        OUTCOME_UNKNOWN_THROWN_ERROR_TOOL_RESULT.format(error=case["error"])
        if case["toolStepThrew"]
        else OUTCOME_UNKNOWN_TOOL_RESULT
    )


def test_both_outcome_unknown_texts_start_with_the_same_words() -> None:
    # The reconcile example and the docs find these calls in the history by this prefix.
    prefix = "The tool call's outcome is unknown: "

    assert OUTCOME_UNKNOWN_TOOL_RESULT.startswith(prefix)
    assert OUTCOME_UNKNOWN_THROWN_ERROR_TOOL_RESULT.startswith(prefix)


def test_a_read_whose_attempts_all_raised_records_failed_without_the_flag() -> None:
    read_call = replace(APPROVED_CALL, effect=ToolEffect.READ)

    recorded = _recovered_tool_call(read_call, THROWN, ToolCallStatus.FAILED)

    assert recorded.status is ToolCallStatus.FAILED
    assert recorded.tool_step_threw is False
    assert describe_tool_call_outcome(recorded) == f"The tool call failed: {THROWN.detail}"


def test_a_call_whose_tool_never_ran_records_failed_without_the_flag() -> None:
    not_started = RecoveryErrorInfo(
        THROWN.detail, THROWN.error_type, StepAttemptProgress.NOT_STARTED
    )

    recorded = _recovered_tool_call(APPROVED_CALL, not_started, ToolCallStatus.OUTCOME_UNKNOWN)

    assert recorded.status is ToolCallStatus.FAILED
    assert recorded.tool_step_threw is False


def test_a_task_poll_that_raised_sets_the_flag_because_its_error_is_the_last_answer() -> None:
    waiting = replace(APPROVED_CALL, status=ToolCallStatus.WAITING_FOR_TASK, task_id="task-1")

    recorded = _recovered_tool_call(waiting, THROWN, ToolCallStatus.OUTCOME_UNKNOWN)

    assert recorded.tool_step_threw is True
    assert recorded.error == THROWN.detail


def test_an_abandoned_task_keeps_its_abandoned_error_and_the_fixed_text() -> None:
    abandoned = replace(
        APPROVED_CALL,
        status=ToolCallStatus.WAITING_FOR_TASK,
        task_id="task-1",
        cancellation_reason=ToolCallCancellationReason.TURN_CANCELLED,
    )

    recorded = _recovered_tool_call(abandoned, THROWN, ToolCallStatus.OUTCOME_UNKNOWN)

    assert recorded.error == TOOL_TASK_ABANDONED_ERROR
    assert recorded.tool_step_threw is False
    assert describe_tool_call_outcome(recorded) == OUTCOME_UNKNOWN_TOOL_RESULT


def test_the_flag_round_trips_and_encoding_omits_it_when_false() -> None:
    codec = CodecRegistry().resolve(AgentToolCall)
    recorded = _recovered_tool_call(APPROVED_CALL, THROWN, ToolCallStatus.OUTCOME_UNKNOWN)
    encoded = codec.encode(recorded)
    unflagged = codec.encode(replace(recorded, tool_step_threw=False))

    assert json.loads(str(encoded.data))["tool_step_threw"] is True
    assert codec.decode(encoded) == recorded
    assert "tool_step_threw" not in json.loads(str(unflagged.data))
    assert codec.decode(unflagged).tool_step_threw is False


def test_a_long_raised_error_is_cut_to_the_budget_and_the_record_keeps_it_whole() -> None:
    error = "side-effect service 400: " + "x" * OUTCOME_UNKNOWN_ERROR_MAX_BYTES
    long_error = RecoveryErrorInfo(error, THROWN.error_type, StepAttemptProgress.NOT_COMPLETED)

    recorded = _recovered_tool_call(APPROVED_CALL, long_error, ToolCallStatus.OUTCOME_UNKNOWN)

    assert recorded.error == error
    total_bytes = len(error.encode())
    marker = OUTCOME_UNKNOWN_ERROR_TRUNCATION_MARKER.format(
        omitted_bytes=total_bytes - OUTCOME_UNKNOWN_ERROR_MAX_BYTES, total_bytes=total_bytes
    )
    assert describe_tool_call_outcome(recorded) == OUTCOME_UNKNOWN_THROWN_ERROR_TOOL_RESULT.format(
        error=error[:OUTCOME_UNKNOWN_ERROR_MAX_BYTES] + marker
    )
