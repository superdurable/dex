# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""RunModelLimit reaches the Server, and the retry budget moves with it."""

from __future__ import annotations

import asyncio
import json
from datetime import timedelta
from typing import Any

import pytest

from dex_agents import AgentSessionOptions, RunModelLimit
from dex_agents.testing import EXECUTE_COMPLETED_EVENT, resolve_dexcli_path
from tests.conftest import ScriptedAgentHarness
from tests.scripted_agent import RUN_MODEL_LIMIT_ENVIRONMENT_VARIABLE

# Past RunModel's two-minute heartbeat timeout.
SILENT_CALL_SECONDS = 135


async def test_the_default_limit_is_what_every_session_had_before(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say hello")
    await harness.wait_for_turns(session_id, 1)

    (options,) = await _run_model_method_options(harness, session_id)
    assert options["timeoutSeconds"] == 900
    assert options["heartbeatTimeoutSeconds"] == 120
    assert options["retryPolicy"]["maximumAttempts"] == 3
    assert options["retryPolicy"]["totalDurationSeconds"] == 1800


async def test_every_run_model_of_a_session_gets_the_limit_and_the_budget_moves_with_it(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker({RUN_MODEL_LIMIT_ENVIRONMENT_VARIABLE: "3600,4"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call lookup {"query": "x"}')
    await harness.wait_for_turns(session_id, 1)
    await harness.sessions.send_user_message(session_id, "say later")
    await harness.wait_for_turns(session_id, 2)

    every_options = await _run_model_method_options(harness, session_id)
    assert len(every_options) == 3
    for options in every_options:
        assert options["timeoutSeconds"] == 3600
        assert options["heartbeatTimeoutSeconds"] == 120
        assert options["retryPolicy"]["maximumAttempts"] == 4
        assert options["retryPolicy"]["totalDurationSeconds"] == 3 * 3600


async def test_a_call_past_the_limit_ends_its_attempt_and_the_budget_stops_the_retries(
    harness: ScriptedAgentHarness,
) -> None:
    # A 10-second budget: attempt 2 ends past it, so the third attempt never starts.
    await harness.stack.start_worker({RUN_MODEL_LIMIT_ENVIRONMENT_VARIABLE: "5,3"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall never-released")

    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error
    assert harness.gate("never-released").reached_count() == 2
    assert harness.invocations.count("model cancelled at never-released") == 2


async def test_two_attempts_retry_a_call_that_timed_out(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker({RUN_MODEL_LIMIT_ENVIRONMENT_VARIABLE: "3,2"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall never-released")

    await harness.wait_for_turns(session_id, 1)

    assert harness.gate("never-released").reached_count() == 2


async def test_one_attempt_means_a_failed_call_is_not_repeated(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker({RUN_MODEL_LIMIT_ENVIRONMENT_VARIABLE: "2,1"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall never-released")

    await harness.wait_for_turns(session_id, 1)

    assert harness.gate("never-released").reached_count() == 1


async def test_a_silent_call_past_the_heartbeat_timeout_stays_on_its_first_attempt(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall silent")
    gate = harness.gate("silent")
    await gate.wait_until_reached()

    await asyncio.sleep(SILENT_CALL_SECONDS)
    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None and snapshot.state.last_turn_error is None
    history = await harness.stack.read_step_history(session_id)
    (run_model,) = [
        entry
        for entry in history
        if entry.step_type == "RunModel" and entry.event_type == EXECUTE_COMPLETED_EVENT
    ]
    assert run_model.final_attempt == 1


@pytest.mark.parametrize(
    ("method_timeout", "maximum_attempts", "message"),
    [
        (timedelta(0), 3, "whole number of seconds"),
        (timedelta(seconds=1.5), 3, "whole number of seconds"),
        (timedelta(minutes=15), 0, "maximum_attempts must be positive"),
        (timedelta(days=30_000), 3, "too long"),
    ],
)
def test_an_invalid_limit_is_rejected_at_startup(
    method_timeout: timedelta, maximum_attempts: int, message: str
) -> None:
    with pytest.raises(ValueError, match=message):
        RunModelLimit(method_timeout=method_timeout, maximum_attempts=maximum_attempts)


def test_the_default_options_keep_the_default_limit() -> None:
    limit = AgentSessionOptions().run_model_limit
    assert limit == RunModelLimit(method_timeout=timedelta(minutes=15), maximum_attempts=3)
    assert limit.total_duration == timedelta(minutes=30)


async def _run_model_method_options(
    harness: ScriptedAgentHarness, session_id: str
) -> list[dict[str, Any]]:
    process = await asyncio.create_subprocess_exec(
        str(resolve_dexcli_path()),
        "flow",
        "history",
        session_id,
        "--server",
        harness.stack.engine.address,
        "--all",
        "--output",
        "json",
        stdout=asyncio.subprocess.PIPE,
    )
    stdout, _ = await process.communicate()
    return [
        event["payload"]["context"]["methodOptions"]
        for event in json.loads(stdout)["events"]
        if event["type"] == EXECUTE_COMPLETED_EVENT
        and event["payload"]["context"]["stepType"] == "RunModel"
    ]
