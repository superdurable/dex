# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Plans: write_todos replaces the plan, plan mode drafts it, and execute_plan runs one revision."""

from __future__ import annotations

import asyncio
import json
from pathlib import Path
from typing import Any

import pytest
from dex.codec import CodecRegistry, Value, WireKind

from dex_agents import (
    NO_PLAN_NOTE,
    PLANNING_TURN_INSTRUCTION,
    PLANNING_TURN_TOOL_REJECTION,
    WRITE_TODOS_CALLED_MORE_THAN_ONCE,
    AgentPlan,
    AgentPlanTask,
    AgentSessionFlow,
    AgentSessionOptions,
    AgentSessionStatus,
    AgentSnapshot,
    AgentState,
    AgentUserMessage,
    BuiltInTool,
    InteractionMode,
    MessageRole,
    PlanStatus,
    PlanTaskStatus,
    build_plan_execution_message,
)
from dex_agents.stepwise import AgentDefinition
from dex_agents.testing import ScriptedModelClient, ScriptedModelReply
from tests.conftest import ScriptedAgentHarness
from tests.plan_agent import (
    APPROVED_PLAN_PREFIX,
    PLANNING_TOOL_CHOICE_ENVIRONMENT_VARIABLE,
    USER_MESSAGE_GATE_ENVIRONMENT_VARIABLE,
    USER_MESSAGE_GATE_NAME,
    read_model_requests,
)
from tests.scripted_agent import BUILT_IN_TOOLS_ENVIRONMENT_VARIABLE, ScriptedRunner

FAILED = "The tool call failed: "


async def test_write_todos_replaces_the_plan_and_each_call_of_a_double_write_fails(
    plan_harness: ScriptedAgentHarness,
) -> None:
    await plan_harness.stack.start_worker()
    session_id = plan_harness.new_session_id()
    first = [
        {"content": "Look up the order", "status": "in_progress"},
        {"content": "Refund it", "status": "pending"},
    ]
    await plan_harness.sessions.start_session(session_id, f"todos {json.dumps(first)}")
    snapshot = await plan_harness.wait_for_turns(session_id, 1)

    assert snapshot.plan == AgentPlan(
        revision=1,
        status=PlanStatus.ACTIVE,
        tasks=[
            AgentPlanTask(content="Look up the order", status=PlanTaskStatus.IN_PROGRESS),
            AgentPlanTask(content="Refund it", status=PlanTaskStatus.PENDING),
        ],
        updated_at=snapshot.plan.updated_at if snapshot.plan else "",
    )
    assert snapshot.state is not None
    assert snapshot.state.interaction_mode is InteractionMode.CHAT
    assert snapshot.messages[-1].text == (
        'write_todos: {"status":"updated","revision":1,"task_count":2}'
    )
    # No tool Step runs: RunModel records the call and still moves to ResumeTurn.
    assert await plan_harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "ResumeTurn",
        "RunModel",
    ]

    done = [{"content": task["content"], "status": "completed"} for task in first]
    snapshot = await _send_and_wait(plan_harness, session_id, f"todos {json.dumps(done)}", 2)
    assert snapshot.plan is not None
    assert (snapshot.plan.revision, snapshot.plan.status) == (2, PlanStatus.COMPLETED)
    second_turn_input = _requests(plan_harness, session_id, "turn-2")[0]["user_texts"]
    assert second_turn_input[-2] == (
        "[Dex] The current plan, revision 1, is active:\n"
        "- [in_progress] Look up the order\n"
        "- [pending] Refund it"
    )

    snapshot = await _send_and_wait(plan_harness, session_id, f"todos-twice {json.dumps(first)}", 3)
    assert _turn_tool_outputs(snapshot, "turn-3") == [
        FAILED + WRITE_TODOS_CALLED_MORE_THAN_ONCE,
        FAILED + WRITE_TODOS_CALLED_MORE_THAN_ONCE,
    ]
    assert snapshot.plan is not None and snapshot.plan.revision == 2

    invalid_arguments = [
        (
            '{"todos": [{"content": " ", "status": "pending"}]}',
            "todos[0].content must be a non-empty string",
        ),
        (
            '{"todos": [{"content": "a", "status": "done"}]}',
            "todos[0].status must be pending, in_progress, or completed",
        ),
        (
            '{"todos": [{"content": "a", "status": "pending", "owner": "me"}]}',
            "todos[0] has unknown field owner",
        ),
        ('{"todos": "a"}', "todos must be a list"),
        ('{"tasks": []}', "unknown argument tasks"),
        # NaN is not JSON, as JavaScript's parser holds too.
        ('{"todos": NaN}', "arguments must be a JSON object"),
        # Unknown names sort by code point: U+FFFF before U+1F600.
        ('{"todos": [], "\U0001f600": 1, "\uffff": 2}', "unknown argument \uffff"),
    ]
    for offset, (arguments, reason) in enumerate(invalid_arguments):
        turn = 4 + offset
        snapshot = await _send_and_wait(plan_harness, session_id, f"todos-raw {arguments}", turn)
        assert _turn_tool_outputs(snapshot, f"turn-{turn}") == [f"{FAILED}invalid todos: {reason}"]
    assert snapshot.plan is not None and snapshot.plan.revision == 2

    snapshot = await _send_and_wait(plan_harness, session_id, "todos []", 11)
    assert snapshot.plan is not None
    assert (snapshot.plan.revision, snapshot.plan.status, snapshot.plan.tasks) == (
        3,
        PlanStatus.CLEARED,
        [],
    )
    assert _turn_tool_outputs(snapshot, "turn-11") == ['{"status":"cleared","revision":3}']
    assert [payload for payload in await _payloads(plan_harness, session_id, "plan_updated")] == [
        {"revision": 1, "status": "active", "task_count": 2},
        {"revision": 2, "status": "completed", "task_count": 2},
        {"revision": 3, "status": "cleared", "task_count": 0},
    ]


async def test_a_plan_mode_message_drafts_with_only_write_todos_and_execute_plan_runs_it(
    plan_harness: ScriptedAgentHarness,
) -> None:
    await plan_harness.stack.start_worker()
    session_id = plan_harness.new_session_id()
    await plan_harness.sessions.start_session(session_id)
    assert await plan_harness.sessions.send_user_message(
        session_id, "plan Look up the order | Publish the note", plan_mode=True
    )
    snapshot = await plan_harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.interaction_mode is InteractionMode.PLANNING
    assert snapshot.plan is not None
    assert (snapshot.plan.revision, snapshot.plan.status) == (1, PlanStatus.DRAFT)
    assert [task.status for task in snapshot.plan.tasks] == [PlanTaskStatus.PENDING] * 2
    assert snapshot.messages[-1].text == "drafted 2 tasks"
    drafting, presenting = _requests(plan_harness, session_id, "turn-1")
    # The default planning tool choice is auto: the model may also answer in text.
    assert (drafting["tools"], drafting["tool_choice"]) == (["write_todos"], "auto")
    assert drafting["user_texts"][-2:] == [
        f"{NO_PLAN_NOTE}\n{PLANNING_TURN_INSTRUCTION}",
        "plan Look up the order | Publish the note",
    ]
    assert (presenting["tools"], presenting["tool_choice"]) == (["write_todos"], "auto")
    assert presenting["user_texts"][-2] == (
        "[Dex] The current plan, revision 1, is draft:\n"
        "- [pending] Look up the order\n"
        "- [pending] Publish the note\n"
        f"{PLANNING_TURN_INSTRUCTION}"
    )

    # Feedback on the draft is a planning turn too, and may revise it without being forced.
    snapshot = await _send_and_wait(
        plan_harness, session_id, "revise Look up the order | Publish the note | finish up", 2
    )
    assert snapshot.plan is not None
    assert (snapshot.plan.revision, snapshot.plan.status) == (2, PlanStatus.DRAFT)
    revising = _requests(plan_harness, session_id, "turn-2")[0]
    assert (revising["tools"], revising["tool_choice"]) == (["write_todos"], "auto")

    assert not await plan_harness.sessions.execute_plan(session_id, 1)
    assert not await plan_harness.sessions.execute_plan(session_id, 3)
    assert await plan_harness.sessions.execute_plan(session_id, 2)
    snapshot = await plan_harness.wait_for_turns(session_id, 3)

    assert snapshot.state is not None
    assert snapshot.state.interaction_mode is InteractionMode.EXECUTING
    executing = _requests(plan_harness, session_id, "turn-3")[0]
    assert executing["tools"] == ["lookup_order", "publish_note", "ask_user", "write_todos"]
    assert executing["tool_choice"] == "auto"
    assert executing["user_texts"][-1] == build_plan_execution_message(2)
    assert executing["user_texts"][-1] == (
        "[Dex] The user approved plan revision 2. "
        "Execute it and keep the task statuses current with write_todos."
    )
    assert executing["user_texts"][-2].startswith("[Dex] The current plan, revision 2, is active:")
    third_turn_events = [
        event.type
        for event in await plan_harness.agent_events(session_id)
        if event.turn_id == "turn-3"
    ]
    assert third_turn_events[:3] == ["turn_started", "plan_execution_started", "plan_updated"]
    assert await _payloads(plan_harness, session_id, "plan_execution_started") == [{"revision": 2}]
    # The approval marked the draft active, then the execution completed every task.
    assert (await _payloads(plan_harness, session_id, "plan_updated"))[-2:] == [
        {"revision": 2, "status": "active", "task_count": 3},
        {"revision": 3, "status": "completed", "task_count": 3},
    ]
    assert plan_harness.invocations.count("lookup_order") == 1
    # The execution marked every task completed, so nothing is left to execute.
    assert snapshot.plan is not None
    assert (snapshot.plan.revision, snapshot.plan.status) == (3, PlanStatus.COMPLETED)
    assert not await plan_harness.sessions.execute_plan(session_id, 3)


async def test_a_planning_turn_that_answers_in_text_ends_without_a_draft(
    plan_harness: ScriptedAgentHarness,
) -> None:
    await plan_harness.stack.start_worker()
    session_id = plan_harness.new_session_id()
    await plan_harness.sessions.start_session(session_id)
    snapshot = await _send_and_wait(
        plan_harness, session_id, "Which database do you use?", 1, plan_mode=True
    )

    assert snapshot.state is not None
    assert snapshot.state.interaction_mode is InteractionMode.PLANNING
    assert snapshot.state.last_turn_error is None
    assert snapshot.plan is None
    assert snapshot.messages[-1].text == "said: Which database do you use?"
    assert not await plan_harness.sessions.execute_plan(session_id, 1)
    snapshot = await _send_and_wait(
        plan_harness, session_id, "plan Look up the order", 2, plan_mode=True
    )
    assert snapshot.plan is not None
    assert (snapshot.plan.revision, snapshot.plan.status) == (1, PlanStatus.DRAFT)


async def test_a_required_planning_tool_choice_forces_write_todos_until_a_draft_exists(
    plan_harness: ScriptedAgentHarness,
) -> None:
    await plan_harness.stack.start_worker({PLANNING_TOOL_CHOICE_ENVIRONMENT_VARIABLE: "required"})
    session_id = plan_harness.new_session_id()
    await plan_harness.sessions.start_session(session_id)
    await _send_and_wait(plan_harness, session_id, "plan Look up the order", 1, plan_mode=True)
    await _send_and_wait(plan_harness, session_id, "revise Look up the order | Refund it", 2)

    drafting, presenting = _requests(plan_harness, session_id, "turn-1")
    assert (drafting["tools"], drafting["tool_choice"]) == (["write_todos"], "write_todos")
    assert (presenting["tools"], presenting["tool_choice"]) == (["write_todos"], "auto")
    # A draft exists, so feedback on it is not forced.
    revising = _requests(plan_harness, session_id, "turn-2")[0]
    assert (revising["tools"], revising["tool_choice"]) == (["write_todos"], "auto")


async def test_a_resolved_model_that_cannot_force_a_tool_choice_fails_the_planning_turn(
    plan_harness: ScriptedAgentHarness,
) -> None:
    await plan_harness.stack.start_worker({PLANNING_TOOL_CHOICE_ENVIRONMENT_VARIABLE: "required"})
    session_id = plan_harness.new_session_id()
    await plan_harness.sessions.start_session(
        session_id, session_settings={"refuses_forced_tool_choice": True}
    )
    assert await plan_harness.sessions.send_user_message(
        session_id, "plan Look up the order", plan_mode=True
    )
    snapshot = await plan_harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None and snapshot.state.last_turn_error is not None
    assert "planning_tool_choice='required' needs a model" in snapshot.state.last_turn_error
    # The check runs before the request, so the model never sees a forced choice.
    assert _requests(plan_harness, session_id, "turn-1") == []


async def test_a_planning_turn_rejects_every_tool_but_write_todos(
    plan_harness: ScriptedAgentHarness,
) -> None:
    await plan_harness.stack.start_worker()
    session_id = plan_harness.new_session_id()
    await plan_harness.sessions.start_session(session_id)
    assert await plan_harness.sessions.send_user_message(
        session_id, "plan-sneak Look up the order", plan_mode=True
    )
    snapshot = await plan_harness.wait_for_turns(session_id, 1)

    assert _turn_tool_outputs(snapshot, "turn-1") == [
        PLANNING_TURN_TOOL_REJECTION,
        '{"status":"updated","revision":1,"task_count":1}',
    ]
    assert plan_harness.invocations.count("lookup_order") == 0
    finished = await _payloads(plan_harness, session_id, "tool_call_finished")
    assert [(payload["tool_name"], payload["status"]) for payload in finished] == [
        ("lookup_order", "rejected"),
        ("write_todos", "succeeded"),
    ]


async def test_concurrent_execute_plan_calls_for_one_revision_accept_exactly_one(
    plan_harness: ScriptedAgentHarness,
) -> None:
    await plan_harness.stack.start_worker()
    session_id = plan_harness.new_session_id()
    await plan_harness.sessions.start_session(session_id)

    for revision in range(1, 6):
        gate_name = f"execution-{revision}"
        snapshot = await _send_and_wait(
            plan_harness, session_id, f"plan stall {gate_name}", 2 * revision - 1, plan_mode=True
        )
        assert snapshot.plan is not None and snapshot.plan.revision == revision
        results = await asyncio.gather(
            plan_harness.sessions.execute_plan(session_id, revision),
            plan_harness.sessions.execute_plan(session_id, revision),
        )
        assert sorted(results) == [False, True]
        gate = plan_harness.gate(gate_name)
        await gate.wait_until_reached()
        assert not await plan_harness.sessions.execute_plan(session_id, revision)
        gate.release()
        await plan_harness.wait_for_turns(session_id, 2 * revision)

    assert await _payloads(plan_harness, session_id, "plan_execution_started") == [
        {"revision": revision} for revision in range(1, 6)
    ]
    assert plan_harness.invocations.count("lookup_order") == 5


async def test_worker_killed_while_await_user_message_starts_an_approved_plan_runs_it_once(
    plan_harness: ScriptedAgentHarness,
) -> None:
    worker = await plan_harness.stack.start_worker(
        {USER_MESSAGE_GATE_ENVIRONMENT_VARIABLE: f"block:{APPROVED_PLAN_PREFIX}"}
    )
    session_id = await _start_with_draft(plan_harness, "plan Look up the order")
    assert await plan_harness.sessions.execute_plan(session_id, 1)
    gate = plan_harness.gate(USER_MESSAGE_GATE_NAME)
    await gate.wait_until_reached()

    worker.kill()
    gate.release()
    await plan_harness.stack.start_worker()
    snapshot = await plan_harness.wait_for_turns(session_id, 2)

    assert snapshot.state is not None
    assert snapshot.state.interaction_mode is InteractionMode.EXECUTING
    assert await _payloads(plan_harness, session_id, "plan_execution_started") == [{"revision": 1}]
    assert plan_harness.invocations.count("lookup_order") == 1
    assert len(_requests(plan_harness, session_id, "turn-2")) == 2
    executed = await plan_harness.executed_step_types(session_id)
    assert executed.count("AwaitUserMessage") == 2
    # The rerun cleared the pending approval, so the active plan can run again.
    assert snapshot.plan is not None and snapshot.plan.status is PlanStatus.ACTIVE
    assert await plan_harness.sessions.execute_plan(session_id, 1)
    await plan_harness.wait_for_turns(session_id, 3)
    assert len(await _payloads(plan_harness, session_id, "plan_execution_started")) == 2


async def test_an_approval_the_session_took_but_has_not_started_refuses_a_second_one(
    plan_harness: ScriptedAgentHarness,
) -> None:
    await plan_harness.stack.start_worker(
        {USER_MESSAGE_GATE_ENVIRONMENT_VARIABLE: f"fail:{APPROVED_PLAN_PREFIX}"}
    )
    session_id = await _start_with_draft(plan_harness, "plan Look up the order")
    assert await plan_harness.sessions.execute_plan(session_id, 1)
    gate = plan_harness.gate(USER_MESSAGE_GATE_NAME)
    await gate.wait_until_reached()

    # AwaitUserMessage consumed the approval, but its Execute has not committed.
    idle = await plan_harness.sessions.get_agent_snapshot(session_id)
    assert idle.state is not None
    assert idle.state.status is AgentSessionStatus.WAITING_FOR_USER_MESSAGE
    assert idle.state.active_turn_id is None
    assert not await plan_harness.sessions.execute_plan(session_id, 1)
    gate.release()
    await plan_harness.wait_for_turns(session_id, 2)

    snapshot = await _send_and_wait(plan_harness, session_id, "after the execution", 3)
    assert [
        message.text
        for message in snapshot.messages
        if message.role is MessageRole.USER and message.turn_id == "turn-3"
    ] == ["after the execution"]
    assert await _payloads(plan_harness, session_id, "plan_execution_started") == [{"revision": 1}]
    assert plan_harness.invocations.count("lookup_order") == 1


async def test_an_approval_that_a_queued_turn_makes_stale_never_runs(
    plan_harness: ScriptedAgentHarness,
) -> None:
    await plan_harness.stack.start_worker({USER_MESSAGE_GATE_ENVIRONMENT_VARIABLE: "fail:revise "})
    session_id = await _start_with_draft(plan_harness, "plan Look up the order")
    assert await plan_harness.sessions.send_user_message(
        session_id, "revise Look up the order | Publish the note"
    )
    gate = plan_harness.gate(USER_MESSAGE_GATE_NAME)
    await gate.wait_until_reached()

    # The session took the message first, so this approval waits behind that message's turn.
    assert await plan_harness.sessions.execute_plan(session_id, 1)
    gate.release()
    snapshot = await plan_harness.wait_for_turns(session_id, 2)

    assert snapshot.plan is not None
    assert (snapshot.plan.revision, snapshot.plan.status) == (2, PlanStatus.DRAFT)
    assert not await plan_harness.sessions.execute_plan(session_id, 1)
    assert await plan_harness.sessions.execute_plan(session_id, 2)
    await plan_harness.wait_for_turns(session_id, 3)
    assert await _payloads(plan_harness, session_id, "plan_execution_started") == [{"revision": 2}]


async def test_a_runner_without_plan_mode_writes_plans_but_refuses_plan_mode_messages(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker({BUILT_IN_TOOLS_ENVIRONMENT_VARIABLE: "write_todos"})
    session_id = harness.new_session_id()
    todos = json.dumps({"todos": [{"content": "Ship it", "status": "pending"}]})
    await harness.sessions.start_session(session_id, f"call write_todos {todos}")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.plan is not None
    assert (snapshot.plan.revision, snapshot.plan.status) == (1, PlanStatus.ACTIVE)
    assert snapshot.messages[-1].text == (
        'write_todos: {"status":"updated","revision":1,"task_count":1}'
    )
    assert "CallTool" not in await harness.executed_step_types(session_id)
    assert not await harness.sessions.send_user_message(session_id, "plan it", plan_mode=True)
    assert await harness.sessions.send_user_message(session_id, "say still chatting")
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert snapshot.messages[-1].text == "said: still chatting"
    assert snapshot.state is not None
    assert snapshot.state.interaction_mode is InteractionMode.CHAT


def test_values_without_the_plan_fields_or_with_null_ones_decode_to_the_defaults() -> None:
    codecs = CodecRegistry()

    def decode(value_type: type[Any], data: dict[str, Any]) -> Any:
        return codecs.resolve(value_type).decode(Value(kind=WireKind.JSON, data=json.dumps(data)))

    state = {"status": "waiting_for_user_message", "turn_count": 2, "next_message_sequence": 5}
    for extra in ({}, {"interaction_mode": None}):
        assert decode(AgentState, {**state, **extra}).interaction_mode is InteractionMode.CHAT
    for extra in ({}, {"plan_mode": None}):
        assert decode(AgentUserMessage, {"text": "hello", **extra}).plan_mode is False
    assert decode(AgentUserMessage, {"text": "hello", "plan_mode": True}).plan_mode is True


def test_plan_mode_needs_write_todos_and_a_runner_that_supports_it(tmp_path: Path) -> None:
    with pytest.raises(ValueError, match="requires the write_todos built-in tool"):
        AgentSessionOptions(plan_mode_enabled=True)
    with pytest.raises(ValueError, match="requires a runner that supports plan mode"):
        AgentSessionFlow(
            ScriptedRunner(tmp_path),
            flow_type="NoPlanMode",
            options=AgentSessionOptions(
                built_in_tools=frozenset({BuiltInTool.WRITE_TODOS}), plan_mode_enabled=True
            ),
        )
    model = ScriptedModelClient(lambda request: ScriptedModelReply(text="ok"))
    with pytest.raises(ValueError, match="requires the write_todos built-in tool"):
        AgentDefinition(name="agent", instructions="Help.", model=model, plan_mode_enabled=True)


async def _start_with_draft(harness: ScriptedAgentHarness, text: str) -> str:
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)
    assert await harness.sessions.send_user_message(session_id, text, plan_mode=True)
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.plan is not None and snapshot.plan.status is PlanStatus.DRAFT
    return session_id


async def _send_and_wait(
    harness: ScriptedAgentHarness,
    session_id: str,
    text: str,
    turn_count: int,
    *,
    plan_mode: bool = False,
) -> AgentSnapshot:
    assert await harness.sessions.send_user_message(session_id, text, plan_mode=plan_mode)
    return await harness.wait_for_turns(session_id, turn_count)


def _requests(harness: ScriptedAgentHarness, session_id: str, turn_id: str) -> list[dict[str, Any]]:
    return [
        request
        for request in read_model_requests(harness.state_dir)
        if request["flow_id"] == session_id and request["turn_id"] == turn_id
    ]


def _turn_tool_outputs(snapshot: AgentSnapshot, turn_id: str) -> list[str | None]:
    return [
        message.tool_output
        for message in snapshot.messages
        if message.role is MessageRole.TOOL and message.turn_id == turn_id
    ]


async def _payloads(
    harness: ScriptedAgentHarness, session_id: str, event_type: str
) -> list[dict[str, Any]]:
    return [
        json.loads(event.payload)
        for event in await harness.agent_events(session_id)
        if event.type == event_type
    ]
