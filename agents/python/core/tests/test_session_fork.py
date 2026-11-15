# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Session forks: the rules of agents/conformance/session_fork.json, and forks on an engine.

The twin of the TypeScript core's test/integ/session-fork.integration.test.ts.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

from dex_agents import (
    AGENT_STATE,
    SESSION_LAST_TERMINAL_ENVIRONMENT,
    SESSION_TERMINAL_LEASE,
    AcquireSessionTerminalLeaseRequest,
    AgentSessionForkError,
    AgentSessionForkPoint,
    AgentSessionStatus,
    AgentSessionTurnStart,
    MessageRole,
    plan_agent_session_fork_point,
)
from tests.conftest import ScriptedAgentHarness

CASES: dict[str, Any] = json.loads(
    (Path(__file__).resolve().parents[3] / "conformance" / "session_fork.json").read_text(
        encoding="utf-8"
    )
)
SOURCE_SESSION_ID = "source-1"


@pytest.mark.parametrize("case", CASES["points"], ids=lambda case: case["name"])
def test_the_fork_point_follows_the_conformance_suite(case: dict[str, Any]) -> None:
    state = case["state"]
    expected = case["expected"]

    def plan() -> AgentSessionForkPoint:
        return plan_agent_session_fork_point(
            SOURCE_SESSION_ID,
            case["after_message"],
            state["next_message_sequence"],
            state["active_turn_id"],
            AgentSessionStatus(state["status"]),
            [
                AgentSessionTurnStart(start["sequence"], start["step_execution_id"])
                for start in case["turn_starts"]
            ],
        )

    if "error" in expected:
        with pytest.raises(AgentSessionForkError) as raised:
            plan()
        assert raised.value.code == expected["error"]
        assert str(raised.value) == CASES["errors"][expected["error"]].format(**expected["values"])
        return
    assert plan() == AgentSessionForkPoint(expected["after_message"], expected["step_execution_id"])


async def _session_with_two_turns(harness: ScriptedAgentHarness) -> str:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)
    assert await harness.sessions.send_user_message(session_id, "say hello")
    await harness.wait_for_turns(session_id, 1)
    assert await harness.sessions.send_user_message(session_id, "say again")
    await harness.wait_for_turns(session_id, 2)
    return session_id


async def test_a_fork_after_an_earlier_turn_continues_on_its_own_while_the_source_keeps_working(
    harness: ScriptedAgentHarness,
) -> None:
    source_id = await _session_with_two_turns(harness)
    lease = await harness.sessions.acquire_session_terminal_lease(
        source_id,
        AcquireSessionTerminalLeaseRequest(
            holder_id="holder-a",
            host_name="host-a",
            process_id=7,
            ttl_seconds=600,
            environment={"host_name": "host-a", "workspace": "/work"},
        ),
    )
    assert lease.is_acquired
    fork_id = harness.new_session_id()

    fork = await harness.sessions.fork_session(source_id, fork_id, after_message=2)

    assert (fork.session_id, fork.after_message) == (fork_id, 2)
    stored = await harness.stack.client.get_attributes(
        fork_id, AGENT_STATE, SESSION_TERMINAL_LEASE, SESSION_LAST_TERMINAL_ENVIRONMENT
    )
    assert stored.get(SESSION_TERMINAL_LEASE) is None
    assert stored.get(SESSION_LAST_TERMINAL_ENVIRONMENT) is None
    state = stored[AGENT_STATE]
    assert (state.next_message_sequence, state.turn_count, state.active_turn_id) == (3, 1, None)

    assert await harness.sessions.send_user_message(fork_id, "recall")
    forked = await harness.wait_for_turns(fork_id, 2)
    assert [(message.role, message.text) for message in forked.messages] == [
        (MessageRole.USER, "say hello"),
        (MessageRole.ASSISTANT, "said: hello"),
        (MessageRole.USER, "recall"),
        (MessageRole.ASSISTANT, "history: say hello"),
    ]

    assert await harness.sessions.send_user_message(source_id, "recall")
    source = await harness.wait_for_turns(source_id, 3)
    assert source.messages[-1].text == "history: say hello | say again"
    source_lease = await harness.stack.client.get_attributes(source_id, SESSION_TERMINAL_LEASE)
    assert source_lease.get(SESSION_TERMINAL_LEASE) == lease.lease


async def test_a_fork_of_the_current_state_keeps_every_message_and_takes_its_task_queue(
    harness: ScriptedAgentHarness,
) -> None:
    source_id = await _session_with_two_turns(harness)
    fork_id = harness.new_session_id()

    fork = await harness.sessions.fork_session(source_id, fork_id, task_queue="s-fork-test")

    assert fork.after_message == 4
    flow_state = await harness.stack.client.get_flow_state(fork_id)
    assert flow_state.task_queue == "s-fork-test"
    stored = await harness.stack.client.get_attributes(fork_id, AGENT_STATE)
    assert stored[AGENT_STATE].next_message_sequence == 5


async def test_a_fork_inside_a_turn_or_during_a_running_turn_is_refused(
    harness: ScriptedAgentHarness,
) -> None:
    source_id = await _session_with_two_turns(harness)

    with pytest.raises(AgentSessionForkError) as inside:
        await harness.sessions.fork_session(source_id, harness.new_session_id(), after_message=1)
    assert inside.value.code == "inside_turn"

    gate = harness.gate("fork-gate")
    assert await harness.sessions.send_user_message(source_id, "stall fork-gate")
    await gate.wait_until_reached()
    with pytest.raises(AgentSessionForkError) as running:
        await harness.sessions.fork_session(source_id, harness.new_session_id())
    assert running.value.code == "turn_in_progress"

    fork_id = harness.new_session_id()
    fork = await harness.sessions.fork_session(source_id, fork_id, after_message=4)
    assert fork.after_message == 4
    gate.release()
    await harness.wait_for_turns(source_id, 3)
