# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Multi-agent ADK trees: parallel branches, sub-agent modes, AgentTools, tool names."""

from __future__ import annotations

import json
from dataclasses import replace
from pathlib import Path

import pytest
from google.adk.agents import LlmAgent, SequentialAgent
from google.adk.code_executors import BuiltInCodeExecutor, UnsafeLocalCodeExecutor
from google.adk.tools.agent_tool import AgentTool
from google.genai import types

import dex_agents_google_adk.runner as adk_runner_module
from dex_agents import (
    AgentEventWriter,
    AgentMessage,
    AgentToolCall,
    AgentTurnContext,
    ToolCallsRequested,
    ToolCallStatus,
)
from dex_agents_google_adk import AdkAgentRunner
from dex_agents_google_adk._run_state import SuspendedAdkTurn
from tests.conftest import AdkAgentHarness
from tests.scripted_adk_agent import MODEL_CALL_LOG_NAME, STATE_DIR_ENVIRONMENT_VARIABLE
from tests.scripted_adk_topologies import (
    NESTED_PARALLEL_FLOW_TYPE,
    PARALLEL_OF_SEQUENTIAL_FLOW_TYPE,
    PARALLEL_RESEARCH_FLOW_TYPE,
    READ_AGENT_TOOL_FLOW_TYPE,
    SINGLE_TURN_DELEGATION_FLOW_TYPE,
    TASK_DELEGATION_FLOW_TYPE,
    TASK_QUESTION,
    TOOLSET_NAME_CLASH_FLOW_TYPE,
    TWO_SINGLE_TURN_DELEGATIONS_FLOW_TYPE,
    ScriptedBranchModel,
    build_parallel_research_flow,
)
from tests.scripted_adk_topologies import record_a as first_record_a


async def test_parallel_agent_branches_each_get_their_own_tool_results(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    sessions = harness.sessions_for(PARALLEL_RESEARCH_FLOW_TYPE)
    await sessions.start_session(session_id, "first")
    first = await harness.wait_for_turns(session_id, 1, sessions)

    assert _branch_answers(first.messages) == [
        "record_a: recorded a: first; results seen: 1",
        "record_b: recorded b: first; results seen: 1",
    ]
    branches = {
        json.loads(message.framework_item)["author"]: json.loads(message.framework_item)["branch"]
        for message in first.messages
        if message.text and message.text.startswith("record_")
    }
    assert branches == {
        "researcher_a": "research.researcher_a",
        "researcher_b": "research.researcher_b",
    }
    executed = await harness.executed_step_types(session_id)
    assert executed.count("CallTool") == 2
    assert harness.invocations.count("record_a") == harness.invocations.count("record_b") == 1

    assert await sessions.send_user_message(session_id, "second")
    second = await harness.wait_for_turns(session_id, 2, sessions)
    assert _branch_answers(
        [message for message in second.messages if message.turn_id == "turn-2"]
    ) == [
        "record_a: recorded a: second; results seen: 2",
        "record_b: recorded b: second; results seen: 2",
    ]
    assert harness.invocations.count("record_b") == 2


async def test_single_turn_sub_agent_tool_steps_run_once_and_the_answer_returns(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    sessions = harness.sessions_for(SINGLE_TURN_DELEGATION_FLOW_TYPE)
    await sessions.start_session(session_id, "write the report")
    snapshot = await harness.wait_for_turns(session_id, 1, sessions)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == "helper: helper done: filed report"
    assert harness.invocations.count("file_report") == 1
    executed = await harness.executed_step_types(session_id)
    assert executed.count("CallSingleAttemptTool") == 1
    assert "CallTool" not in executed


async def test_task_sub_agent_receives_the_users_replies_on_later_turns(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    sessions = harness.sessions_for(TASK_DELEGATION_FLOW_TYPE)
    await sessions.start_session(session_id, "charge then ask")
    first = await harness.wait_for_turns(session_id, 1, sessions)
    assert first.messages[-1].text == TASK_QUESTION
    assert harness.invocations.count("file_charge") == 1

    assert await sessions.send_user_message(session_id, "maybe")
    second = await harness.wait_for_turns(session_id, 2, sessions)
    assert second.messages[-1].text == TASK_QUESTION

    # Turn 3 replays turn 2's stored reply, which must keep the task agent's isolation scope.
    assert await sessions.send_user_message(session_id, "yes")
    third = await harness.wait_for_turns(session_id, 3, sessions)
    assert third.state is not None
    assert third.state.last_turn_error is None
    assert third.messages[-1].text == (
        "approver: finished after charged report; replies: maybe, yes"
    )
    assert harness.invocations.count("file_charge") == 1


async def test_read_agent_tool_whose_agent_writes_fails_the_turn_before_any_tool_runs(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    sessions = harness.sessions_for(READ_AGENT_TOOL_FLOW_TYPE)
    await sessions.start_session(session_id, "draft it")
    failed = await harness.wait_for_turns(session_id, 1, sessions)

    assert failed.state is not None
    assert "AgentTool drafting_agent has the read effect" in (failed.state.last_turn_error or "")
    assert "save_draft" in (failed.state.last_turn_error or "")
    assert harness.invocations.count("save_draft") == 0
    assert "CallTool" not in await harness.executed_step_types(session_id)


def test_tools_that_share_a_name_across_agents_fail_at_construction() -> None:
    def record_a(note: str) -> str:
        """A second function that ADK also names record_a."""
        return note

    child = LlmAgent(
        name="child", model=ScriptedBranchModel(tool_name="record_a"), tools=[record_a]
    )
    root = LlmAgent(
        name="root",
        model=ScriptedBranchModel(tool_name="record_a"),
        tools=[first_record_a],
        sub_agents=[child],
    )
    with pytest.raises(ValueError, match="agents root and child both have a tool named record_a"):
        AdkAgentRunner(root)


def _branch_answers(messages: list[AgentMessage]) -> list[str]:
    return sorted(
        message.text
        for message in messages
        if message.text is not None and message.text.startswith("record_")
    )


async def test_tool_calls_in_branches_of_a_nested_parallel_agent_fail_before_any_tool_runs(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    sessions = harness.sessions_for(NESTED_PARALLEL_FLOW_TYPE)
    await sessions.start_session(session_id, "research")
    failed = await harness.wait_for_turns(session_id, 1, sessions)

    assert failed.state is not None
    assert "tool calls in several ADK branches" in (failed.state.last_turn_error or "")
    assert harness.invocations.count("record_a") == harness.invocations.count("record_b") == 0
    assert "CallTool" not in await harness.executed_step_types(session_id)
    assert [message.text for message in failed.messages] == ["research"]


async def test_tool_calls_in_a_root_parallel_agents_workflow_branch_fail_before_any_tool_runs(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    sessions = harness.sessions_for(PARALLEL_OF_SEQUENTIAL_FLOW_TYPE)
    await sessions.start_session(session_id, "fan out")
    failed = await harness.wait_for_turns(session_id, 1, sessions)

    assert failed.state is not None
    assert "tool calls in several ADK branches" in (failed.state.last_turn_error or "")
    assert "fanout.ordered" in (failed.state.last_turn_error or "")
    for tool_name in ("record_a", "record_b", "record_c"):
        assert harness.invocations.count(tool_name) == 0
    assert "CallTool" not in await harness.executed_step_types(session_id)
    assert [message.text for message in failed.messages] == ["fan out"]


async def test_two_single_turn_delegations_in_one_response_fail_before_any_tool_runs(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    sessions = harness.sessions_for(TWO_SINGLE_TURN_DELEGATIONS_FLOW_TYPE)
    await sessions.start_session(session_id, "file both")
    failed = await harness.wait_for_turns(session_id, 1, sessions)

    assert failed.state is not None
    assert "tool calls in several ADK branches" in (failed.state.last_turn_error or "")
    assert harness.invocations.count("file_report_one") == 0
    assert harness.invocations.count("file_report_two") == 0
    assert "CallTool" not in await harness.executed_step_types(session_id)


async def test_toolset_tool_named_like_another_agents_tool_fails_before_its_step_runs(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    sessions = harness.sessions_for(TOOLSET_NAME_CLASH_FLOW_TYPE)
    await sessions.start_session(
        session_id, 'call transfer_to_agent {"agent_name": "toolset_child"}'
    )
    failed = await harness.wait_for_turns(session_id, 1, sessions)

    assert failed.state is not None
    assert "would run the record_a of another agent" in (failed.state.last_turn_error or "")
    assert harness.invocations.count("record_a") == 0
    assert harness.invocations.count("record_a_from_toolset") == 0
    assert "CallTool" not in await harness.executed_step_types(session_id)


async def test_control_tool_beside_another_call_fails_before_adk_applies_it(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    sessions = harness.sessions_for(TOOLSET_NAME_CLASH_FLOW_TYPE)
    await sessions.start_session(
        session_id,
        'call record_a {"note": "x"} && call transfer_to_agent {"agent_name": "toolset_child"}',
    )
    failed = await harness.wait_for_turns(session_id, 1, sessions)

    assert failed.state is not None
    assert "called transfer_to_agent together with tools that need Dex Steps" in (
        failed.state.last_turn_error or ""
    )
    # Each of the three RunModel attempts made the root's model call and never transferred.
    assert harness.invocations.count(MODEL_CALL_LOG_NAME) == 3
    assert harness.invocations.count("record_a") == 0


def test_code_executors_that_run_inside_run_model_fail_at_construction() -> None:
    coder = LlmAgent(
        name="coder",
        model=ScriptedBranchModel(tool_name="record_a"),
        code_executor=UnsafeLocalCodeExecutor(),
    )
    with pytest.raises(ValueError, match="runs code with UnsafeLocalCodeExecutor inside RunModel"):
        AdkAgentRunner(coder)
    AdkAgentRunner(
        LlmAgent(
            name="gemini_coder",
            model=ScriptedBranchModel(tool_name="record_a"),
            code_executor=BuiltInCodeExecutor(),
        )
    )


def test_code_executors_inside_an_agent_tool_fail_at_construction() -> None:
    coder = LlmAgent(
        name="coder",
        description="Writes and runs code.",
        model=ScriptedBranchModel(tool_name="record_a"),
        code_executor=UnsafeLocalCodeExecutor(),
    )
    planner = LlmAgent(
        name="planner",
        model=ScriptedBranchModel(tool_name="coder"),
        tools=[AgentTool(coder)],
    )
    with pytest.raises(ValueError, match="agent coder inside an AgentTool runs code"):
        AdkAgentRunner(planner)


def test_single_turn_sub_agents_under_a_root_that_is_not_an_llm_agent_fail_at_construction() -> (
    None
):
    helper = LlmAgent(
        name="helper",
        mode="single_turn",
        description="Helps.",
        model=ScriptedBranchModel(tool_name="record_a"),
    )
    coordinator = LlmAgent(
        name="coordinator", model=ScriptedBranchModel(tool_name="record_a"), sub_agents=[helper]
    )
    with pytest.raises(ValueError, match="ADK runs only under a root LlmAgent"):
        AdkAgentRunner(SequentialAgent(name="pipeline", sub_agents=[coordinator]))


async def test_a_resumed_branch_that_never_gets_its_recorded_result_fails_the_model_step(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv(STATE_DIR_ENVIRONMENT_VARIABLE, str(tmp_path))

    def answer_every_branch_in_one_message(
        turn: AgentTurnContext, suspended: SuspendedAdkTurn
    ) -> list[types.Content]:
        messages = original_messages(turn, suspended)
        parts = [part for message in messages for part in message.parts or []]
        return [messages[0].model_copy(update={"parts": parts})]

    original_messages = adk_runner_module._confirmation_response_messages
    # ADK stamps one bundled answer with the first call's branch, so branch b misses its result.
    monkeypatch.setattr(
        adk_runner_module, "_confirmation_response_messages", answer_every_branch_in_one_message
    )
    runner = build_parallel_research_flow().runner
    user_message = runner.build_user_message("first")
    messages = [
        AgentMessage(
            sequence=1,
            turn_id="turn-1",
            role=user_message.role,
            text=user_message.text,
            framework_item=user_message.framework_item,
            created_at="2026-10-31T00:00:00+00:00",
        )
    ]
    requested = await runner.run_model_step(_turn_context(messages, None, {}))
    assert isinstance(requested, ToolCallsRequested)
    records = {
        call.tool_call_id: AgentToolCall(
            tool_call_id=call.tool_call_id,
            turn_id="turn-1",
            tool_name=call.tool_name,
            arguments=call.arguments,
            effect=call.policy.effect,
            requires_approval=False,
            status=ToolCallStatus.SUCCEEDED,
            output=f"recorded {call.tool_name}",
            deferral_order=index,
        )
        for index, call in enumerate(requested.tool_calls)
    }
    with pytest.raises(RuntimeError, match="without giving the recorded results of tool calls"):
        await runner.run_model_step(_turn_context(messages, requested.framework_run_state, records))


def _turn_context(
    messages: list[AgentMessage],
    framework_run_state: str | None,
    tool_calls: dict[str, AgentToolCall],
) -> AgentTurnContext:
    async def record_heartbeat() -> None:
        return None

    flow = build_parallel_research_flow()
    return AgentTurnContext(
        flow_id="session-without-engine",
        turn_id="turn-1",
        attempt=1,
        messages=messages,
        framework_run_state=framework_run_state,
        tool_calls={key: replace(value) for key, value in tool_calls.items()},
        tool_policies=flow.options.tool_policies,
        default_tool_effect=flow.options.default_tool_effect,
        events=AgentEventWriter(lambda event: None, "turn-1", 1, "RunModel-1"),
        heartbeat=record_heartbeat,
    )
