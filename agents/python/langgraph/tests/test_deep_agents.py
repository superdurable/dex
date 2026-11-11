# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Deep Agents agents from ``create_deep_agent`` in agent sessions."""

from __future__ import annotations

from pathlib import Path

import pytest
from deepagents import create_deep_agent
from dex_agents import AgentSnapshot, MessageRole

from dex_agents_langgraph import DurableToolCallMiddleware, LangGraphAgentRunner
from tests.conftest import ScriptedAgentHarness
from tests.deep_agents import (
    SOURCE_READ_FILE_DEEP_AGENT_FLOW_TYPE,
    SOURCE_READ_FILE_OUTPUT,
    TOOL_WRAPPER_INVOCATION_PREFIX,
    TURN_MODEL_NAME,
    WorkspaceReadFileSource,
    read_recorded_model_requests,
)
from tests.scripted_agents import INVOCATION_LOG_NAME
from tests.scripted_model import SCRIPTED_REQUEST_MODEL, ScriptedChatModel

STATE_UPDATE_TAG = "dex-agents-langgraph-state-writes"
FILES_SCRIPT = (
    'call write_file {"file_path": "/notes.md", "content": "buy milk"} '
    'then call wait_at_gate {"gate": "files"} '
    'then call read_file {"file_path": "/notes.md"}'
)


async def test_a_file_written_in_a_turn_is_read_back_after_a_worker_restart(
    deep_agent_harness: ScriptedAgentHarness,
) -> None:
    harness = deep_agent_harness
    worker = await harness.stack.start_worker()
    restarted_session_id = harness.new_session_id()
    await harness.sessions.start_session(restarted_session_id, FILES_SCRIPT)
    gate = harness.gate("files")
    await gate.wait_until_reached()

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    restarted = await harness.wait_for_turns(restarted_session_id, 1)

    answer = restarted.messages[-1].text or ""
    assert answer.startswith("write_file: Updated file /notes.md; wait_at_gate: passed files; ")
    assert "read_file: " in answer
    assert "buy milk" in answer.split("read_file: ", 1)[1]
    executed = await harness.executed_step_types(restarted_session_id)
    assert executed.count("CallSingleAttemptTool") == 1
    # A session without the restart replays the same conversation.
    uninterrupted_session_id = harness.new_session_id()
    await harness.sessions.start_session(uninterrupted_session_id, FILES_SCRIPT)
    uninterrupted = await harness.wait_for_turns(uninterrupted_session_id, 1)
    assert _conversation(uninterrupted) == _conversation(restarted)
    assert all(STATE_UPDATE_TAG not in (text or "") for _, text in _conversation(restarted))


async def test_a_tool_that_returns_a_command_updates_state_for_a_later_tool(
    deep_agent_harness: ScriptedAgentHarness,
) -> None:
    harness = deep_agent_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call set_topic {"topic": "durability"} then call get_topic {}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == (
        "set_topic: topic set to durability; get_topic: topic is durability"
    )
    tool_outputs = [
        message.tool_output for message in snapshot.messages if message.role is MessageRole.TOOL
    ]
    assert tool_outputs == ["topic set to durability", "topic is durability"]
    assert harness.tool_invocations() == ["set_topic", "get_topic"]
    # The deferral comes before the middleware's tool wrapper, which therefore never runs.
    assert not [
        name
        for name in harness.invocations.names()
        if name.startswith(TOOL_WRAPPER_INVOCATION_PREFIX)
    ]


async def test_task_is_neither_offered_nor_run_when_excluded(
    deep_agent_harness: ScriptedAgentHarness,
) -> None:
    harness = deep_agent_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call task {"description": "research", "subagent_type": "general-purpose"}',
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == (
        "task: The tool call failed: the agent has no tool named task"
    )
    offered = read_recorded_model_requests(harness.state_dir)[0].tool_names
    assert "task" not in offered
    assert {"write_file", "read_file", "set_topic", "get_topic"} <= set(offered)


@pytest.mark.parametrize("deep_agent_flow_type", [SOURCE_READ_FILE_DEEP_AGENT_FLOW_TYPE])
async def test_a_tool_source_replaces_an_excluded_graph_tool_of_the_same_name(
    deep_agent_harness: ScriptedAgentHarness,
) -> None:
    harness = deep_agent_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call read_file {"file_path": "/notes.md"}')
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"read_file: {SOURCE_READ_FILE_OUTPUT}: /notes.md"
    offered = read_recorded_model_requests(harness.state_dir)[0].tool_names
    assert offered.count("read_file") == 1
    assert offered[-1] == "read_file"


def test_a_tool_source_cannot_reuse_a_graph_tool_name_unless_it_is_excluded(
    tmp_path: Path,
) -> None:
    model = ScriptedChatModel(invocation_log_path=str(tmp_path / INVOCATION_LOG_NAME))
    with pytest.raises(ValueError, match="read_file"):
        LangGraphAgentRunner(
            create_deep_agent(model, middleware=[DurableToolCallMiddleware()]),
            tool_sources=[WorkspaceReadFileSource()],
        )
    LangGraphAgentRunner(
        create_deep_agent(model, middleware=[DurableToolCallMiddleware()]),
        tool_sources=[WorkspaceReadFileSource()],
        excluded_tool_names=["read_file"],
    )


async def test_session_settings_pick_the_model_of_each_turn(
    deep_agent_harness: ScriptedAgentHarness,
) -> None:
    harness = deep_agent_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say first")
    await harness.wait_for_turns(session_id, 1)
    assert await harness.sessions.update_session_settings(session_id, {"model": TURN_MODEL_NAME})
    await harness.sessions.send_user_message(
        session_id, 'call get_topic {} then call wait_at_gate {"gate": "model"}'
    )
    harness.gate("model").release()
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert snapshot.messages[-1].text == "get_topic: topic is unset; wait_at_gate: passed model"
    models = [request.model for request in read_recorded_model_requests(harness.state_dir)]
    assert models == [SCRIPTED_REQUEST_MODEL, TURN_MODEL_NAME, TURN_MODEL_NAME, TURN_MODEL_NAME]


def _conversation(snapshot: AgentSnapshot) -> list[tuple[MessageRole, str | None]]:
    return [(message.role, message.text) for message in snapshot.messages]
