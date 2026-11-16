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
from types import SimpleNamespace
from typing import Any, cast

import grpc
import pytest
from dex import DexServiceError, ErrorSubStatus

from dex_agents import (
    AGENT_PLAN,
    AGENT_STATE,
    SESSION_LAST_TERMINAL_ENVIRONMENT,
    SESSION_TERMINAL_LEASE,
    AcquireSessionTerminalLeaseRequest,
    AgentEvent,
    AgentSessionForkError,
    AgentSessionForkingFlow,
    AgentSessionForkPoint,
    AgentSessionStatus,
    AgentSessionTurnStart,
    AgentState,
    InteractionMode,
    MessageRole,
    PlanStatus,
    fork_agent_session,
    plan_agent_session_fork_point,
)
from dex_agents.testing import wait_for_agent_snapshot
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
            case.get("waiting_step_execution_id"),
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


async def test_a_turn_that_steering_started_has_no_fork_point_before_it(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    source_id = harness.new_session_id()
    await harness.sessions.start_session(
        source_id, "stall-then-call model-gate order-1 | launch notes"
    )
    gate = harness.gate("model-gate")
    await gate.wait_until_reached()
    assert await harness.sessions.steer_user_message(source_id, text="say steered")
    gate.release()
    snapshot = await harness.wait_for_turns(source_id, 2)
    steered_start = next(
        message.sequence for message in snapshot.messages if message.turn_id == "turn-2"
    )

    with pytest.raises(AgentSessionForkError) as steered:
        await harness.sessions.fork_session(
            source_id, harness.new_session_id(), after_message=steered_start - 1
        )
    assert steered.value.code == "steered_turn"

    fork = await harness.sessions.fork_session(source_id, harness.new_session_id())
    assert fork.after_message == len(snapshot.messages)


async def test_a_fork_after_a_cancelled_turn_keeps_the_cancelled_turn_and_continues(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    source_id = harness.new_session_id()
    await harness.sessions.start_session(source_id, "publish launch notes")
    await wait_for_agent_snapshot(
        harness.sessions,
        source_id,
        lambda snapshot: next(iter(snapshot.pending_interrupts), None),
        description="a pending interrupt",
    )
    with pytest.raises(AgentSessionForkError) as waiting:
        await harness.sessions.fork_session(source_id, harness.new_session_id())
    assert waiting.value.code == "turn_in_progress"
    assert await harness.sessions.cancel_turn(source_id, "turn-1")
    cancelled = await harness.wait_for_turns(source_id, 1)
    after_cancelled = len(cancelled.messages)
    assert await harness.sessions.send_user_message(source_id, "say later")
    await harness.wait_for_turns(source_id, 2)

    fork_id = harness.new_session_id()
    fork = await harness.sessions.fork_session(source_id, fork_id, after_message=after_cancelled)

    assert fork.after_message == after_cancelled
    assert await harness.sessions.send_user_message(fork_id, "say forked")
    forked = await harness.wait_for_turns(fork_id, 2)
    assert forked.messages[after_cancelled].text == "say forked"
    assert forked.messages[-1].text == "said: forked"
    assert [message.text for message in forked.messages[:after_cancelled]] == [
        message.text for message in cancelled.messages
    ]


async def test_a_fork_before_a_plan_execution_keeps_the_draft_plan(
    plan_harness: ScriptedAgentHarness,
) -> None:
    harness = plan_harness
    await harness.stack.start_worker()
    source_id = harness.new_session_id()
    await harness.sessions.start_session(source_id)
    assert await harness.sessions.send_user_message(
        source_id, "plan Look up the order | Publish the note", plan_mode=True
    )
    drafted = await harness.wait_for_turns(source_id, 1)
    after_planning = len(drafted.messages)
    assert await harness.sessions.execute_plan(source_id, 1)
    await harness.wait_for_turns(source_id, 2)

    fork_id = harness.new_session_id()
    fork = await harness.sessions.fork_session(source_id, fork_id, after_message=after_planning)

    assert fork.after_message == after_planning
    stored = await harness.stack.client.get_attributes(fork_id, AGENT_STATE, AGENT_PLAN)
    assert stored[AGENT_STATE].next_message_sequence == after_planning + 1
    plan = stored[AGENT_PLAN]
    assert (plan.revision, plan.status) == (1, PlanStatus.DRAFT)
    assert await harness.sessions.execute_plan(fork_id, 1)
    executed = await harness.wait_for_turns(fork_id, 2)
    assert executed.state is not None
    assert executed.state.interaction_mode is InteractionMode.EXECUTING


class _ScriptedForkClient:
    """A Client stand-in whose source is idle and whose clone fork answers ``clone_failure``."""

    def __init__(self, clone_failure: Exception | None, events: list[AgentEvent]) -> None:
        self._clone_failure = clone_failure
        self._events = events
        self.clone_calls = 0

    async def get_attributes(self, flow_id: str, *definitions: Any) -> dict[Any, Any]:
        return {
            AGENT_STATE: AgentState(
                status=AgentSessionStatus.WAITING_FOR_USER_MESSAGE,
                turn_count=3,
                next_message_sequence=7,
            )
        }

    async def get_flow_state(self, flow_id: str) -> Any:
        return SimpleNamespace(
            active_step_executions=[
                SimpleNamespace(
                    step_type="AwaitUserMessage", step_execution_id="AwaitUserMessage-4"
                )
            ],
            task_queue="",
        )

    async def list_stream_messages(self, *arguments: Any) -> Any:
        return SimpleNamespace(
            messages=[SimpleNamespace(value=event) for event in self._events], next_page_token=""
        )

    async def clone_fork_flow(self, *arguments: Any) -> Any:
        self.clone_calls += 1
        if self._clone_failure is not None:
            raise self._clone_failure
        raise AssertionError("the test expects a refusal")


def _turn_started(sequence: int, step_execution_id: str) -> AgentEvent:
    return AgentEvent(
        type="turn_started",
        turn_id=None,
        attempt=1,
        payload=json.dumps({"sequence": sequence}),
        step_execution_id=step_execution_id,
    )


async def test_a_point_outside_the_history_window_is_refused_with_the_server_detail() -> None:
    detail = "the point is outside the history window"
    client = _ScriptedForkClient(
        DexServiceError(
            grpc.StatusCode.FAILED_PRECONDITION,
            ErrorSubStatus.UNCATEGORIZED,
            detail,
            "clone_fork_flow",
            "fork-1",
        ),
        [_turn_started(5, "AwaitUserMessage-3")],
    )

    with pytest.raises(AgentSessionForkError) as refused:
        await fork_agent_session(
            cast(Any, client),
            AgentSessionForkingFlow("T"),
            SOURCE_SESSION_ID,
            "fork-1",
            after_message=4,
        )

    assert refused.value.code == "point_refused"
    assert str(refused.value) == CASES["errors"]["point_refused"].format(
        session_id=SOURCE_SESSION_ID, after_message=4, detail=detail
    )


async def test_events_trimmed_past_the_message_refuse_the_fork_before_any_copy() -> None:
    client = _ScriptedForkClient(None, [_turn_started(5, "AwaitUserMessage-3")])

    with pytest.raises(AgentSessionForkError) as refused:
        await fork_agent_session(
            cast(Any, client),
            AgentSessionForkingFlow("T"),
            SOURCE_SESSION_ID,
            "fork-1",
            after_message=2,
        )

    assert refused.value.code == "point_unavailable"
    assert client.clone_calls == 0
