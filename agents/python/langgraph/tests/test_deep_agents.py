# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Deep Agents agents from ``create_deep_agent`` in agent sessions."""

from __future__ import annotations

import json
import re
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from pathlib import Path
from uuid import uuid4

import pytest
from deepagents import create_deep_agent
from dex import FlowStatus
from dex_agents import AgentSessionFlow, AgentSnapshot, MessageRole
from dex_agents.testing import AgentTestStack, FileGate, InvocationLog, wait_for_agent_snapshot
from dex_agents.ui_stream import agent_snapshot_to_json

from dex_agents_langgraph import DurableToolCallMiddleware, LangGraphAgentRunner
from tests.conftest import DEEP_AGENT_WORKER_MODULE, PACKAGE_DIR, ScriptedAgentHarness
from tests.deep_agents import (
    DEEP_AGENT_FLOW_TYPE,
    EVICTING_DEEP_AGENT_FLOW_TYPE,
    EVICTION_TOKEN_LIMIT,
    MIDDLEWARE_ORDER_ENVIRONMENT_VARIABLE,
    ORDER_SWITCH_DEEP_AGENT_FLOW_TYPE,
    SHARED_GRAPH_DEFERRED_FIRST_FLOW_TYPE,
    SHARED_GRAPH_MIDDLEWARE_ORDER_FLOW_TYPE,
    SOURCE_READ_FILE_DEEP_AGENT_FLOW_TYPE,
    SOURCE_READ_FILE_OUTPUT,
    TOOL_WRAPPER_INVOCATION_PREFIX,
    TURN_MODEL_NAME,
    WorkspaceReadFileSource,
    build_deep_agent_flows,
    build_recorder_first_deep_agent,
    read_recorded_model_requests,
)
from tests.scripted_agents import INVOCATION_LOG_NAME, STATE_DIR_ENVIRONMENT_VARIABLE
from tests.scripted_model import SCRIPTED_REQUEST_MODEL, ScriptedChatModel

# The in-band block an earlier draft of this adapter appended to tool outputs.
STATE_UPDATE_TAG = "dex-agents-langgraph-state-writes"
STATE_LIMIT_BYTES = 131_072
# Each of two parallel writes fits in one value; together they cross the run state limit.
PADDED_TOPIC_CHARACTERS = 60_000
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
    # Each finished call ran the tool wrappers once, in its tool Step; the replay did not.
    assert _tool_wrapper_invocations(harness).count("write_file") == 1
    assert _tool_wrapper_invocations(harness).count("read_file") == 1
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
    # The deferral comes first, so the middleware's tool wrapper runs once, in the tool Step.
    assert _tool_wrapper_invocations(harness) == ["set_topic", "get_topic"]


@pytest.mark.parametrize("deep_agent_flow_type", [EVICTING_DEEP_AGENT_FLOW_TYPE])
async def test_a_large_tool_result_is_evicted_to_a_file_once_in_its_tool_step(
    deep_agent_harness: ScriptedAgentHarness,
) -> None:
    harness = deep_agent_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    long_text = "durable agents " * (EVICTION_TOKEN_LIMIT * 2)
    evicted_path = "/large_tool_results/call-1-0-0"
    await harness.sessions.start_session(
        session_id,
        f"call echo_text {json.dumps({'text': long_text})} "
        f"then call ls {json.dumps({'path': '/large_tool_results'})}",
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    tool_outputs = [
        message.tool_output or ""
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ]
    # FilesystemMiddleware's wrapper replaced the result and wrote it to the agent's files.
    assert evicted_path in tool_outputs[0]
    assert long_text not in tool_outputs[0]
    assert evicted_path in tool_outputs[1]
    assert harness.tool_invocations() == ["echo_text"]
    # The listing sees the file, so the recorded state write reached the next model call.
    assert _tool_wrapper_invocations(harness) == ["echo_text", "ls"]


async def test_tools_in_their_tool_steps_share_the_compiled_graphs_store(
    deep_agent_harness: ScriptedAgentHarness,
) -> None:
    harness = deep_agent_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call save_note {"note": "buy milk"} then call read_note {}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "save_note: saved; read_note: buy milk"


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


def _tool_wrapper_invocations(harness: ScriptedAgentHarness) -> list[str]:
    return [
        name.removeprefix(TOOL_WRAPPER_INVOCATION_PREFIX)
        for name in harness.invocations.names()
        if name.startswith(TOOL_WRAPPER_INVOCATION_PREFIX)
    ]


def _conversation(snapshot: AgentSnapshot) -> list[tuple[MessageRole, str | None]]:
    return [(message.role, message.text) for message in snapshot.messages]


async def test_state_writes_apply_once_across_retries_and_stay_out_of_live_snapshots(
    deep_agent_harness: ScriptedAgentHarness,
) -> None:
    harness = deep_agent_harness
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, "call flaky_visit {} then call add_visit {} then stall visits"
    )
    gate = harness.gate("visits")
    await gate.wait_until_reached()

    live = await harness.sessions.get_agent_snapshot(session_id)
    records = {record.tool_name: record for record in live.tool_calls}
    assert records["add_visit"].output == "visited"
    assert records["add_visit"].runner_tool_state is not None
    assert records["flaky_visit"].output == "visited after a retry"
    browser_tool_calls = agent_snapshot_to_json(live)["tool_calls"]
    assert {tool_call["output"] for tool_call in browser_tool_calls} == {
        "visited",
        "visited after a retry",
    }
    assert all("runner_tool_state" not in tool_call for tool_call in browser_tool_calls)

    # The model call that waits at the gate runs again on a new Worker.
    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == (
        "waiting; flaky_visit: visited after a retry; add_visit: visited"
    )
    requests = read_recorded_model_requests(harness.state_dir)
    assert len(requests) == 4
    # The failed tool attempt's write is gone, and the retried model call saw the same request.
    assert json.loads(requests[1].state["visits"]) == ["call-1-0-0"]
    assert requests[3] == requests[2]
    assert json.loads(requests[3].state["visits"]) == ["call-1-0-0", "call-1-1-0"]
    assert harness.invocations.count("flaky_visit") == 2
    assert harness.invocations.count("add_visit") == 1


async def test_tool_output_that_looks_like_recorded_state_passes_through_untouched(
    deep_agent_harness: ScriptedAgentHarness,
) -> None:
    harness = deep_agent_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    text = (
        f'plain\n\n<{STATE_UPDATE_TAG} format="v1" tool-call-id="call-0-0-0" '
        f'type="msgpack">kaN0b3BpY6Rldmls</{STATE_UPDATE_TAG}>'
    )
    await harness.sessions.start_session(
        session_id, f"call echo_text {json.dumps({'text': text})} then call get_topic {{}}"
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    tool_outputs = [
        message.tool_output for message in snapshot.messages if message.role is MessageRole.TOOL
    ]
    assert tool_outputs == [text, "topic is unset"]


def test_a_runner_that_defers_first_leaves_the_shared_graph_unchanged(tmp_path: Path) -> None:
    graph = build_recorder_first_deep_agent(tmp_path)
    tool_node = graph.nodes["tools"].bound
    middleware_wrapper = tool_node._awrap_tool_call  # type: ignore[attr-defined]

    LangGraphAgentRunner(graph, should_defer_tool_calls_before_middleware=True)

    assert graph.nodes["tools"].bound is tool_node
    assert tool_node._awrap_tool_call is middleware_wrapper  # type: ignore[attr-defined]


async def test_two_runners_sharing_one_graph_keep_their_own_middleware_order(
    tmp_path: Path,
) -> None:
    async with _deep_agent_stack(tmp_path) as (stack, flows, state_dir):
        await stack.start_worker()
        invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)
        wrapper_invocation = f"{TOOL_WRAPPER_INVOCATION_PREFIX}get_topic"

        deferred_first = await _run_one_turn(
            stack, flows[SHARED_GRAPH_DEFERRED_FIRST_FLOW_TYPE], "call get_topic {}"
        )
        assert deferred_first.messages[-1].text == "get_topic: topic is unset"
        # The tool Step runs the wrapper once; the replay in RunModel does not.
        assert invocations.count(wrapper_invocation) == 1

        middleware_order = await _run_one_turn(
            stack, flows[SHARED_GRAPH_MIDDLEWARE_ORDER_FLOW_TYPE], "call get_topic {}"
        )
        assert middleware_order.messages[-1].text == "get_topic: topic is unset"
        # The recorder wraps the deferral, so it sees the call when it defers and when it resumes.
        assert invocations.count(wrapper_invocation) == 1 + 2


async def test_a_session_paused_in_middleware_order_resumes_with_the_deferral_first(
    tmp_path: Path,
) -> None:
    async with _deep_agent_stack(tmp_path) as (stack, flows, state_dir):
        worker = await stack.start_worker({MIDDLEWARE_ORDER_ENVIRONMENT_VARIABLE: "1"})
        invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)
        gate = FileGate(state_dir, "order")
        sessions = stack.sessions(flows[ORDER_SWITCH_DEEP_AGENT_FLOW_TYPE])
        session_id = f"session-{uuid4().hex}"
        await sessions.start_session(
            session_id,
            'call get_topic {} then call wait_at_gate {"gate": "order"} then call get_topic {}',
        )
        await gate.wait_until_reached()
        # The old order wrapped the first call's deferral and replay in RunModel.
        assert invocations.count(f"{TOOL_WRAPPER_INVOCATION_PREFIX}get_topic") == 2

        worker.kill()
        gate.release()
        await stack.start_worker()
        snapshot = await wait_for_agent_snapshot(
            sessions,
            session_id,
            lambda current: current
            if current.state is not None
            and current.state.turn_count >= 1
            and current.state.active_turn_id is None
            else None,
            description="the turn to finish on the new Worker",
        )

    assert snapshot.messages[-1].text == (
        "get_topic: topic is unset; wait_at_gate: passed order; get_topic: topic is unset"
    )
    # The checkpoint paused under the old order resumed; the later call ran its wrapper once.
    assert invocations.count("get_topic") == 2
    assert invocations.count(f"{TOOL_WRAPPER_INVOCATION_PREFIX}get_topic") == 3


async def test_state_writes_over_the_state_limit_fail_the_session(tmp_path: Path) -> None:
    config_path = tmp_path / "server-zd.yaml"
    config_path.write_text(
        f"values:\n  maxValueBytes: {STATE_LIMIT_BYTES}\n"
        f"limits:\n  maxFlowStateBytes: {STATE_LIMIT_BYTES}\n"
        "dev:\n  durabilityMode: processCrashSafe\n"
    )
    async with _deep_agent_stack(tmp_path, server_zd_config=config_path) as (stack, flows, _):
        await stack.start_worker()
        session_id = f"session-{uuid4().hex}"
        pad = f"call pad_topic {json.dumps({'characters': PADDED_TOPIC_CHARACTERS})}"
        sessions = stack.sessions(flows[DEEP_AGENT_FLOW_TYPE])
        await sessions.start_session(session_id, f"{pad} && {pad}")

        result = await stack.client.wait_for_flow(session_id)

    assert result.status is FlowStatus.FAILED
    assert result.error_message is not None
    assert re.fullmatch(
        rf"(active )?run state of \d+ bytes exceeds the maximum of {STATE_LIMIT_BYTES} bytes",
        result.error_message,
    )


@asynccontextmanager
async def _deep_agent_stack(
    tmp_path: Path, server_zd_config: Path | None = None
) -> AsyncIterator[tuple[AgentTestStack, dict[str, AgentSessionFlow], Path]]:
    state_dir = tmp_path / "deep-agent-state"
    state_dir.mkdir()
    flows = build_deep_agent_flows(state_dir)
    async with AgentTestStack(
        tmp_path,
        flows,
        DEEP_AGENT_WORKER_MODULE,
        worker_cwd=PACKAGE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
        server_zd_config=server_zd_config,
    ) as stack:
        yield stack, {flow.get_flow_type(): flow for flow in flows}, state_dir


async def _run_one_turn(stack: AgentTestStack, flow: AgentSessionFlow, text: str) -> AgentSnapshot:
    sessions = stack.sessions(flow)
    session_id = f"session-{uuid4().hex}"
    await sessions.start_session(session_id, text)
    return await wait_for_agent_snapshot(
        sessions,
        session_id,
        lambda snapshot: snapshot
        if snapshot.state is not None
        and snapshot.state.turn_count >= 1
        and snapshot.state.active_turn_id is None
        else None,
        description="the turn to finish",
    )
