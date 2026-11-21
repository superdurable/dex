# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""RunModel backs off on transient model API errors and fails at once on other 4xx."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta
from email.utils import format_datetime

import pytest

from dex_agents import ModelRetryPolicy, RunModelLimit
from dex_agents.model_retry import (
    ModelCallDecisionKind,
    ModelErrorKind,
    classify_model_error,
    decide_model_call_retry,
)
from dex_agents.testing import EXECUTE_COMPLETED_EVENT
from tests.conftest import ScriptedAgentHarness
from tests.scripted_agent import RUN_MODEL_LIMIT_ENVIRONMENT_VARIABLE


class FakeStatusError(Exception):
    def __init__(self, status_code: int, headers: dict[str, str] | None = None) -> None:
        super().__init__(f"Error code: {status_code}")
        self.status_code = status_code
        self.headers = headers or {}


class FakeResponse:
    def __init__(self, status_code: int, headers: dict[str, str]) -> None:
        self.status_code = status_code
        self.headers = headers


class TransportError(Exception):
    pass


class FakeConnectError(TransportError):
    """Like httpx's ``ConnectError``, which subclasses ``TransportError``."""


@pytest.mark.parametrize("status", [408, 409, 429, 500, 502, 503, 529])
def test_retryable_statuses_are_transient(status: int) -> None:
    assert classify_model_error(FakeStatusError(status)).kind is ModelErrorKind.TRANSIENT


@pytest.mark.parametrize("status", [400, 401, 403, 404, 413, 422])
def test_other_4xx_statuses_are_permanent(status: int) -> None:
    assert classify_model_error(FakeStatusError(status)).kind is ModelErrorKind.PERMANENT


def test_connection_errors_and_cause_chains_are_classified() -> None:
    assert classify_model_error(ConnectionResetError()).kind is ModelErrorKind.TRANSIENT
    assert classify_model_error(TimeoutError()).kind is ModelErrorKind.TRANSIENT
    assert classify_model_error(FakeConnectError()).kind is ModelErrorKind.TRANSIENT
    try:
        try:
            raise FakeStatusError(503)
        except FakeStatusError as error:
            raise RuntimeError("the graph failed") from error
    except RuntimeError as wrapped:
        assert classify_model_error(wrapped).kind is ModelErrorKind.TRANSIENT
    response_error = Exception("google style")
    response_error.response = FakeResponse(400, {})  # type: ignore[attr-defined]
    assert classify_model_error(response_error).kind is ModelErrorKind.PERMANENT
    assert classify_model_error(RuntimeError("a bug")).kind is ModelErrorKind.UNKNOWN


def test_retry_after_is_read_in_seconds_milliseconds_or_a_date() -> None:
    seconds = classify_model_error(FakeStatusError(429, {"Retry-After": "7"}))
    assert seconds.retry_after_seconds == 7
    milliseconds = classify_model_error(FakeStatusError(429, {"retry-after-ms": "2500"}))
    assert milliseconds.retry_after_seconds == 2.5
    date = format_datetime(datetime.now(UTC) + timedelta(seconds=20), usegmt=True)
    from_date = classify_model_error(FakeStatusError(503, {"retry-after": date}))
    assert from_date.retry_after_seconds is not None and 15 < from_date.retry_after_seconds <= 20


def test_the_default_policy_waits_from_1_to_30_seconds_about_two_minutes_in_all() -> None:
    retry = ModelRetryPolicy()
    assert retry == RunModelLimit().retry
    assert retry.initial_interval == timedelta(seconds=1)
    assert retry.maximum_interval == timedelta(seconds=30)
    assert retry.total_duration == timedelta(minutes=2)
    # 1 + 2 + 4 + 8 + 16 + 30 + 30 = 91 seconds; one more wait passes 120.
    assert retry.transient_retries == 7
    assert ModelRetryPolicy(total_duration=timedelta(0)).transient_retries == 0


@pytest.mark.parametrize(
    "arguments",
    [
        {"initial_interval": timedelta(0)},
        {"initial_interval": timedelta(seconds=1.5)},
        {"maximum_interval": timedelta(milliseconds=500)},
        {"initial_interval": timedelta(seconds=10), "maximum_interval": timedelta(seconds=5)},
        {"total_duration": timedelta(seconds=-1)},
    ],
)
def test_an_invalid_policy_is_rejected(arguments: dict[str, timedelta]) -> None:
    with pytest.raises(ValueError):
        ModelRetryPolicy(**arguments)


def test_a_transient_error_waits_with_jitter_and_at_least_retry_after() -> None:
    retry = ModelRetryPolicy()

    def waits(random: float) -> list[int]:
        decisions = [
            decide_model_call_retry(FakeStatusError(503), attempt, 3, retry, random)
            for attempt in range(1, 9)
        ]
        assert all(d.kind is ModelCallDecisionKind.RETRY_AFTER for d in decisions)
        return [d.after_seconds for d in decisions]

    assert waits(0.999) == [1, 2, 4, 8, 16, 30, 30, 30]
    assert waits(0) == [1, 1, 2, 4, 8, 15, 15, 15]
    limited = FakeStatusError(429, {"retry-after": "12"})
    assert decide_model_call_retry(limited, 1, 3, retry, 0).after_seconds == 12


def test_a_permanent_error_fails_the_turn_and_an_unknown_one_goes_to_the_attempt_limit() -> None:
    retry = ModelRetryPolicy()
    fail = ModelCallDecisionKind.FAIL_TURN
    reraise = ModelCallDecisionKind.RERAISE
    assert decide_model_call_retry(FakeStatusError(401), 1, 3, retry).kind is fail
    assert decide_model_call_retry(RuntimeError(), 2, 3, retry).kind is reraise
    assert decide_model_call_retry(RuntimeError(), 3, 3, retry).kind is reraise
    assert decide_model_call_retry(FakeStatusError(503), 1, 1, retry).kind is reraise


async def test_a_20_second_outage_no_longer_fails_the_turn(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "outage 20 503")

    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None and snapshot.state.last_turn_error is None
    calls = [line for line in harness.invocations.names() if line.startswith("outage:")]
    # Waits of 0.5-1, 1-2, 2-4, 4-8, and 8-16 seconds reach 20 seconds within six or seven calls.
    assert 5 <= len(calls) <= 9


async def test_a_400_fails_the_turn_without_a_retry(harness: ScriptedAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "outage 600 400")

    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error == "Error code: 400"
    calls = [line for line in harness.invocations.names() if line.startswith("outage:")]
    assert len(calls) == 1
    history = await harness.stack.read_step_history(session_id)
    run_model = [
        entry.final_attempt
        for entry in history
        if entry.step_type == "RunModel" and entry.event_type == EXECUTE_COMPLETED_EVENT
    ]
    assert run_model == [1]


async def test_one_attempt_still_disables_every_retry(harness: ScriptedAgentHarness) -> None:
    await harness.stack.start_worker({RUN_MODEL_LIMIT_ENVIRONMENT_VARIABLE: "900,1"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "outage 600 503")

    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error == "Error code: 503"
    calls = [line for line in harness.invocations.names() if line.startswith("outage:")]
    assert len(calls) == 1


async def test_an_error_that_is_not_a_model_api_error_keeps_three_attempts(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "fail")

    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error == "scripted failure"
    history = await harness.stack.read_step_history(session_id)
    assert [entry.step_type for entry in history].count("RecordTurnFailure") >= 1
    run_model = [
        entry.final_attempt
        for entry in history
        if entry.step_type == "RunModel" and entry.event_type == EXECUTE_COMPLETED_EVENT
    ]
    # The third attempt ends the turn itself.
    assert run_model == [3]
