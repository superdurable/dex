# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Every Pydantic AI model call and every tool call of a turn is its own Step."""

from __future__ import annotations

import json

from pydantic_ai import ModelMessagesTypeAdapter, ModelRequest, SystemPromptPart, UserPromptPart

from dex_agents import AgentSessionStatus, MessageRole, ToolCallStatus, ToolEffect
from dex_agents.testing import wait_for_agent_snapshot, wait_until
from tests.conftest import ScriptedAgentHarness
from tests.scripted_agent import SYSTEM_PROMPT


async def test_read_tool_turn_runs_model_tool_and_resume_steps_and_replays(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    prompt = 'call lookup {"query": "docs"}'
    await harness.sessions.start_session(session_id, prompt)
    first = await harness.wait_for_turns(session_id, 1)

    assert [(message.role, message.text, message.tool_name) for message in first.messages] == [
        (MessageRole.USER, prompt, None),
        (MessageRole.USER, None, None),
        (MessageRole.ASSISTANT, None, "lookup"),
        (MessageRole.TOOL, None, "lookup"),
        (MessageRole.ASSISTANT, "lookup: found docs", None),
    ]
    tool_request, tool_result = first.messages[2], first.messages[3]
    assert tool_request.tool_call_id == tool_result.tool_call_id == "call-1-0-0"
    assert json.loads(tool_request.tool_arguments or "") == {"query": "docs"}
    assert tool_result.tool_output == "found docs"
    assert first.usage.model_calls == 2
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "ResumeTurn",
        "RunModel",
    ]
    assert harness.invocations.names() == ["model:1:1", "lookup", "model:1:2"]

    replayed = [
        model_message
        for message in first.messages
        for model_message in ModelMessagesTypeAdapter.validate_json(message.framework_item)
    ]
    opening_request = replayed[1]
    assert isinstance(opening_request, ModelRequest)
    assert [type(part) for part in opening_request.parts] == [SystemPromptPart, UserPromptPart]

    assert await harness.sessions.send_user_message(session_id, "recall")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == f"history: {prompt}; system: {SYSTEM_PROMPT}"
    assert [(message.turn_id, message.role) for message in second.messages[5:]] == [
        ("turn-2", MessageRole.USER),
        ("turn-2", MessageRole.ASSISTANT),
    ]
    assert harness.invocations.names()[3:] == ["model:2:1"]
    streamed = [
        payload["text"] for payload in await harness.list_event_payloads(session_id, "text_delta")
    ]
    assert streamed == ["lookup: found docs", second.messages[-1].text]


async def test_parallel_tool_calls_run_as_parallel_steps(harness: ScriptedAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call lookup {"query": "a", "gate": "lookup-gate"}'
        ' && call charge {"amount": 2, "gate": "charge-gate"}',
    )
    lookup_gate = harness.gate("lookup-gate")
    charge_gate = harness.gate("charge-gate")
    # Both tools wait at their gates at the same time, so their Steps run in parallel.
    await lookup_gate.wait_until_reached()
    await charge_gate.wait_until_reached()
    running = await harness.sessions.get_agent_snapshot(session_id)
    assert running.state is not None
    assert running.state.status is AgentSessionStatus.RUNNING_TOOLS
    assert [(call.tool_name, call.deferral_order) for call in running.tool_calls] == [
        ("lookup", 0),
        ("charge", 1),
    ]

    lookup_gate.release()
    charge_gate.release()
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == (
        f"lookup: found a; charge: charged 2 for deps-{session_id} "
        f"with key {session_id}/turn-1/call-1-0-1"
    )
    executed = await harness.executed_step_types(session_id)
    assert executed.count("CallTool") == 2
    assert executed.count("RunModel") == 2
    assert executed[-2:] == ["ResumeTurn", "RunModel"]


async def test_an_approval_waits_in_parallel_with_an_unapproved_read(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish {"message": "x"} && call lookup {"query": "y"}'
    )
    waiting = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot
        if [(call.tool_name, call.status) for call in snapshot.tool_calls]
        == [("publish", ToolCallStatus.AWAITING_APPROVAL), ("lookup", ToolCallStatus.SUCCEEDED)]
        else None,
        description="the read finished while the write awaits approval",
    )
    assert [interrupt.tool_name for interrupt in waiting.pending_interrupts] == ["publish"]
    assert harness.invocations.count("lookup") == 1
    assert harness.invocations.count("publish") == 0

    assert await harness.sessions.resolve_interrupt(
        session_id, waiting.pending_interrupts[0].interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published x; lookup: found y"
    executed = await harness.executed_step_types(session_id)
    assert executed.count("AwaitToolApproval") == 1
    assert executed.count("CallSingleAttemptTool") == 1
    assert executed.count("CallTool") == 1
    assert executed.count("RunModel") == 2


async def test_structured_output_completes_the_turn_through_the_output_tool(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "report weekly")
    snapshot = await harness.wait_for_turns(session_id, 1)

    final_call, final_return = snapshot.messages[-2], snapshot.messages[-1]
    assert (final_call.role, final_call.tool_name) == (MessageRole.ASSISTANT, "final_result")
    assert json.loads(final_call.tool_arguments or "") == {"title": "weekly", "items": ["a", "b"]}
    assert (final_return.role, final_return.tool_name) == (MessageRole.TOOL, "final_result")
    assert await harness.executed_step_types(session_id) == ["StartAgentSession", "RunModel"]
    assert harness.invocations.names() == ["model:1:1"]


async def test_a_final_result_beside_a_write_runs_the_write_before_the_answer(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish {"message": "news"} && report published'
    )
    interrupt = await harness.wait_for_pending_interrupt(session_id, "publish")
    assert harness.invocations.count("publish") == 0
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "publish: published news"
    assert [message.tool_name for message in snapshot.messages] == [
        None,
        None,
        "publish",
        "publish",
        None,
    ]
    assert harness.invocations.names() == ["model:1:1", "audit:publish", "publish", "model:1:2"]


async def test_mcp_tool_annotations_choose_the_tool_effect(harness: ScriptedAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call read_page {"url": "a"} && call upsert_note {"text": "b"}',
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "read_page: page a; upsert_note: noted b"
    deferred = await harness.list_event_payloads(session_id, "tool_call_deferred")
    assert [(payload["tool_name"], payload["effect"]) for payload in deferred] == [
        ("read_page", ToolEffect.READ.value),
        ("upsert_note", ToolEffect.IDEMPOTENT_WRITE.value),
    ]
    assert await harness.list_event_payloads(session_id, "interrupt_raised") == []
    assert (await harness.executed_step_types(session_id)).count("CallTool") == 2


async def test_tool_search_runs_in_its_own_step_and_reveals_a_deferred_loading_tool(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call search_tools {"queries": ["notify"]} then call notify {"text": "ship"}',
    )
    interrupt = await harness.wait_for_pending_interrupt(session_id, "notify")
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert [(call.tool_name, call.effect, call.status) for call in waiting.tool_calls] == [
        ("search_tools", ToolEffect.READ, ToolCallStatus.SUCCEEDED),
        ("notify", ToolEffect.UNCLASSIFIED, ToolCallStatus.AWAITING_APPROVAL),
    ]
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text is not None
    assert snapshot.messages[-1].text.endswith("notify: notified ship")
    assert harness.invocations.names() == [
        "model:1:1",
        "model:1:2",
        "notify",
        "model:1:3",
    ]
    executed = await harness.executed_step_types(session_id)
    assert executed.count("RunModel") == 3
    assert executed.count("CallTool") == 1
    assert executed.count("CallSingleAttemptTool") == 1


async def test_a_message_sent_during_a_turn_waits_in_the_queue(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall busy-gate")
    gate = harness.gate("busy-gate")
    await gate.wait_until_reached()

    assert await harness.sessions.send_user_message(session_id, "say later")

    async def queued_count() -> int:
        return (await harness.sessions.get_agent_snapshot(session_id)).queued_message_count

    assert await wait_until("one queued message", queued_count) == 1
    during = await harness.sessions.get_agent_snapshot(session_id)
    assert during.state is not None
    assert during.state.active_turn_id == "turn-1"
    assert during.state.status is AgentSessionStatus.RUNNING_MODEL

    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert [message.text for message in snapshot.messages if message.role is MessageRole.USER][
        -1
    ] == "say later"
    assert snapshot.messages[-1].text == "said: later"
    assert snapshot.queued_message_count == 0
