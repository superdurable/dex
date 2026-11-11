# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Stepwise tool sources: routing, policies, listing failures, and name collisions."""

from __future__ import annotations

import asyncio
import json
import stat
import sys
import time
from datetime import timedelta
from pathlib import Path
from typing import Any

import pytest

from dex_agents import (
    AgentEventWriter,
    AgentToolCall,
    AgentTurnContext,
    BuiltInTool,
    DeferredToolCall,
    DeferredToolExecution,
    ToolCallsRequested,
    ToolCallStatus,
    ToolEffect,
    ToolExecutionError,
    ToolExecutionResult,
    ToolTaskPoll,
    ToolTaskPollOutcome,
    TurnCompleted,
)
from dex_agents.builtin_loop import (
    AgentDefinition,
    BuiltinLoopAgentRunner,
    ModelOutputWriter,
    ModelRequest,
    ModelRequestMetadata,
    ModelResponse,
    ModelSettings,
    ScriptedModelClient,
    ScriptedModelReply,
    ScriptedToolCall,
    SourceToolDefinition,
)
from dex_agents.mcp import DEFAULT_MCP_SOURCE_NAME, McpStdioServerConfig, McpToolSource
from dex_agents.testing import ScriptedModelClient as ScriptedModelClientOfTesting
from dex_agents.testing import ScriptedModelReply as ScriptedModelReplyOfTesting
from tests.fake_mcp_server import INVOCATION_LOG_NAME, STATE_DIR_ENVIRONMENT_VARIABLE
from tests.mcp_stepwise_agent import take_note

CORE_DIR = Path(__file__).resolve().parents[1]


class _ListedTool:
    def __init__(self, name: str) -> None:
        self.name = name
        self.description = f"The {name} tool."
        self.input_schema: dict[str, Any] = {"type": "object", "properties": {}}


class _InMemoryToolSource:
    """A tool source that lists fixed names and records what reaches it."""

    def __init__(
        self, *names: str, declared: frozenset[str] = frozenset(), source_name: str | None = None
    ) -> None:
        self.source_name = source_name or "memory:" + ",".join(names or sorted(declared))
        self._tools = [_ListedTool(name) for name in names]
        self._declared = declared
        self.is_listing_broken = False
        self.executed: list[AgentToolCall] = []

    async def list_tools(self) -> list[_ListedTool]:
        if self.is_listing_broken:
            raise RuntimeError("server unreachable")
        return self._tools

    def declared_tool_names(self) -> frozenset[str]:
        return self._declared

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: SourceToolDefinition,
    ) -> DeferredToolCall:
        assert listed_tool.name == tool_name
        return DeferredToolCall(
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            policy=turn.resolve_tool_policy(tool_name, ToolEffect.READ),
            tool_source_reference=f"own-reference-of-{tool_name}",
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        self.executed.append(call.tool_call)
        return ToolExecutionResult(output=f"ran {call.tool_call.tool_name}")

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        self.executed.append(poll.tool_call)
        return ToolExecutionResult(output=f"polled {poll.tool_call.tool_name}")


class _HangingToolSource(_InMemoryToolSource):
    async def list_tools(self) -> list[_ListedTool]:
        await asyncio.sleep(60)
        return []


def _calls(*tool_names: str) -> ScriptedModelClient:
    return ScriptedModelClient(
        lambda request: ScriptedModelReply(
            tool_calls=[
                ScriptedToolCall(tool_name=name, tool_call_id=f"call-{index}")
                for index, name in enumerate(tool_names)
            ]
        )
    )


def _answers() -> ScriptedModelClient:
    return ScriptedModelClient(lambda request: ScriptedModelReply(text="answered"))


def _turn_context() -> AgentTurnContext:
    async def no_heartbeat() -> None:
        return None

    return AgentTurnContext(
        flow_id="session-1",
        turn_id="turn-1",
        attempt=1,
        messages=[],
        framework_run_state=None,
        tool_calls={},
        tool_policies={},
        default_tool_effect=ToolEffect.UNCLASSIFIED,
        events=AgentEventWriter(lambda event: None, "turn-1", 1),
        heartbeat=no_heartbeat,
        enabled_built_in_tools=frozenset({BuiltInTool.ASK_USER}),
    )


def _record(call: DeferredToolCall) -> AgentToolCall:
    return AgentToolCall(
        tool_call_id=call.tool_call_id,
        turn_id="turn-1",
        tool_name=call.tool_name,
        arguments=call.arguments,
        effect=call.policy.effect,
        requires_approval=bool(call.policy.is_approval_required),
        status=ToolCallStatus.APPROVED,
        tool_source_reference=call.tool_source_reference,
    )


def _execution(record: AgentToolCall) -> DeferredToolExecution:
    async def heartbeat() -> None:
        return None

    return DeferredToolExecution(
        flow_id="session-1",
        tool_call=record,
        idempotency_key=f"session-1/turn-1/{record.tool_call_id}",
        attempt=1,
        events=AgentEventWriter(lambda event: None, "turn-1", 1),
        heartbeat=heartbeat,
        supports_tool_tasks=True,
    )


def _poll(record: AgentToolCall) -> ToolTaskPoll:
    async def heartbeat() -> None:
        return None

    return ToolTaskPoll(
        flow_id="session-1",
        tool_call=record,
        idempotency_key=f"session-1/turn-1/{record.tool_call_id}",
        attempt=1,
        input_answers=(),
        events=AgentEventWriter(lambda event: None, "turn-1", 1),
        heartbeat=heartbeat,
    )


def _launch_script(path: Path, log: Path, then: str) -> str:
    """Writes a server command that logs each launch, then runs ``then``."""
    path.write_text(f"#!/bin/sh\necho launched >> {log}\n{then}\n")
    path.chmod(path.stat().st_mode | stat.S_IEXEC)
    return str(path)


def _launches(log: Path) -> int:
    return len(log.read_text().splitlines()) if log.exists() else 0


async def test_an_unlisted_source_tool_fails_as_unknown_without_approval_and_never_runs(
    tmp_path: Path,
) -> None:
    files_command = tmp_path / "files-server"  # absent: the files server is down
    environment = {STATE_DIR_ENVIRONMENT_VARIABLE: str(tmp_path)}
    source = McpToolSource(
        [
            McpStdioServerConfig(
                name="other",
                command=sys.executable,
                args=["-m", "tests.fake_mcp_server", "stdio"],
                env=environment,
                cwd=str(CORE_DIR),
            ),
            McpStdioServerConfig(
                name="files", command=str(files_command), env=environment, cwd=str(CORE_DIR)
            ),
        ],
        connection_attempts=1,
    )
    model = ScriptedModelClient(
        lambda request: ScriptedModelReply(
            tool_calls=[
                ScriptedToolCall(
                    tool_name="files__send_message",
                    arguments={"text": "wire $1M"},
                    tool_call_id="call-1",
                )
            ]
        )
    )
    runner = BuiltinLoopAgentRunner(
        AgentDefinition(name="agent", instructions="Help.", model=model, tool_sources=[source])
    )

    outcome = await runner.run_model_step(_turn_context())

    assert isinstance(outcome, ToolCallsRequested)
    offered = {tool.name for tool in model.requests[0].tools}
    assert "other__send_message" in offered
    assert "files__send_message" not in offered
    (call,) = outcome.tool_calls
    # It can never run, so nobody is asked to approve it.
    assert call.policy.effect is ToolEffect.UNCLASSIFIED
    assert call.policy.is_approval_required is False
    assert call.tool_source_reference is None
    # The server comes back before the approved tool Step runs.
    _launch_script(
        files_command,
        tmp_path / "files-launches.log",
        f"exec {sys.executable} -m tests.fake_mcp_server stdio",
    )
    with pytest.raises(ToolExecutionError, match="^unknown tool files__send_message$"):
        await runner.execute_deferred_tool_call(_execution(_record(call)))
    log = tmp_path / INVOCATION_LOG_NAME
    assert "send_message" not in (log.read_text() if log.exists() else "")


async def test_a_call_reaches_the_source_that_built_it_without_a_listing() -> None:
    search = _InMemoryToolSource("search")
    fetch = _InMemoryToolSource("fetch")
    runner = BuiltinLoopAgentRunner(
        AgentDefinition(
            name="agent", instructions="Help.", model=_calls("fetch"), tool_sources=[search, fetch]
        )
    )
    outcome = await runner.run_model_step(_turn_context())
    assert isinstance(outcome, ToolCallsRequested)
    (call,) = outcome.tool_calls
    assert json.loads(call.tool_source_reference or "") == {
        "stepwise_tool_source": "memory:fetch",
        "reference": "own-reference-of-fetch",
    }

    # A new Worker runs the tool Step while neither source can list.
    search.is_listing_broken = fetch.is_listing_broken = True
    fresh_runner = BuiltinLoopAgentRunner(runner.definition)
    assert await fresh_runner.execute_deferred_tool_call(
        _execution(_record(call))
    ) == ToolExecutionResult(output="ran fetch")
    assert await fresh_runner.poll_tool_task(_poll(_record(call))) == ToolExecutionResult(
        output="polled fetch"
    )
    assert [record.tool_source_reference for record in fetch.executed] == [
        "own-reference-of-fetch"
    ] * 2
    assert search.executed == []


async def test_a_call_whose_source_cannot_be_told_is_retried_not_failed() -> None:
    runner = BuiltinLoopAgentRunner(
        AgentDefinition(
            name="agent",
            instructions="Help.",
            model=_answers(),
            tool_sources=[_InMemoryToolSource("search")],
        )
    )
    shrunk_runner = BuiltinLoopAgentRunner(runner.definition)
    moved = AgentToolCall(
        tool_call_id="call-1",
        turn_id="turn-1",
        tool_name="fetch",
        arguments="{}",
        effect=ToolEffect.UNCLASSIFIED,
        requires_approval=True,
        status=ToolCallStatus.APPROVED,
        tool_source_reference=json.dumps({"stepwise_tool_source": "memory:gone", "reference": "x"}),
    )
    # A LookupError is not a ToolExecutionError, so the effect class decides the outcome.
    with pytest.raises(LookupError, match="memory:gone, .* is not configured on this Worker"):
        await shrunk_runner.execute_deferred_tool_call(_execution(moved))
    several = BuiltinLoopAgentRunner(
        AgentDefinition(
            name="agent",
            instructions="Help.",
            model=_answers(),
            tool_sources=[_InMemoryToolSource("search"), _InMemoryToolSource("fetch")],
        )
    )
    foreign = AgentToolCall(
        tool_call_id="call-1",
        turn_id="turn-1",
        tool_name="fetch",
        arguments="{}",
        effect=ToolEffect.UNCLASSIFIED,
        requires_approval=True,
        status=ToolCallStatus.APPROVED,
        tool_source_reference=json.dumps({"mcp_server": "files", "mcp_tool": "fetch"}),
    )
    with pytest.raises(LookupError, match="cannot tell which tool source"):
        await several.execute_deferred_tool_call(_execution(foreign))
    with pytest.raises(LookupError):
        await several.poll_tool_task(_poll(foreign))


async def test_down_servers_offer_no_tools_without_failing_the_turn_or_being_retried(
    tmp_path: Path,
) -> None:
    refusing_log = tmp_path / "refusing.log"
    hanging_log = tmp_path / "hanging.log"
    source = McpToolSource(
        [
            McpStdioServerConfig(
                name="refusing",
                command=_launch_script(tmp_path / "refusing", refusing_log, "exit 1"),
            ),
            McpStdioServerConfig(
                name="hanging",
                command=_launch_script(tmp_path / "hanging", hanging_log, "exec sleep 60"),
            ),
        ],
        connection_timeout=timedelta(seconds=3),
        connection_attempts=1,
        listing_failure_backoff=timedelta(minutes=5),
    )
    model = _answers()
    runner = BuiltinLoopAgentRunner(
        AgentDefinition(
            name="agent",
            instructions="Help.",
            model=model,
            tools=[take_note],
            tool_sources=[source],
            tool_source_listing_timeout=timedelta(seconds=1),
        )
    )

    for _ in range(3):
        started = time.monotonic()
        assert isinstance(await runner.run_model_step(_turn_context()), TurnCompleted)
        assert time.monotonic() - started < 2.5
    deadline = time.monotonic() + 20
    # Wait until the hanging server's one listing timed out.
    while source._listings["hanging"].failure is None and time.monotonic() < deadline:
        await asyncio.sleep(0.1)
    started = time.monotonic()
    assert isinstance(await runner.run_model_step(_turn_context()), TurnCompleted)
    assert time.monotonic() - started < 0.5

    assert [[tool.name for tool in request.tools] for request in model.requests] == [
        ["take_note", "ask_user"]
    ] * 4
    assert _launches(refusing_log) == 1
    assert _launches(hanging_log) == 1


async def test_a_hanging_source_is_bounded_per_model_call() -> None:
    runner = BuiltinLoopAgentRunner(
        AgentDefinition(
            name="agent",
            instructions="Help.",
            model=_answers(),
            tool_sources=[_HangingToolSource()],
            tool_source_listing_timeout=timedelta(milliseconds=300),
        )
    )
    started = time.monotonic()
    assert isinstance(await runner.run_model_step(_turn_context()), TurnCompleted)
    assert time.monotonic() - started < 2


def test_two_sources_with_resource_and_prompt_tools_are_refused_when_built() -> None:
    def source(server_name: str) -> McpToolSource:
        return McpToolSource(
            [McpStdioServerConfig(name=server_name, command="mcp-files")],
            resource_and_prompt_tools_enabled=True,
            source_name=server_name,
        )

    with pytest.raises(ValueError, match="^two tool sources list a tool named mcp_get_prompt$"):
        AgentDefinition(
            name="agent",
            instructions="Help.",
            model=_answers(),
            tool_sources=[source("files"), source("tickets")],
        )
    with pytest.raises(
        ValueError, match="^tool ask_user uses the name of an enabled built-in tool$"
    ):
        AgentDefinition(
            name="agent",
            instructions="Help.",
            model=_answers(),
            tool_sources=[_InMemoryToolSource(declared=frozenset({"ask_user"}))],
            built_in_tools=frozenset({BuiltInTool.ASK_USER}),
        )
    AgentDefinition(
        name="agent", instructions="Help.", model=_answers(), tool_sources=[source("files")]
    )


@pytest.mark.parametrize(
    ("sources", "message"),
    [
        (
            [_InMemoryToolSource("ask_user")],
            "tool ask_user uses the name of an enabled built-in tool",
        ),
        (
            [_InMemoryToolSource("take_note")],
            "tool take_note of a tool source uses the name of a function tool",
        ),
        (
            [_InMemoryToolSource("search"), _InMemoryToolSource("search", source_name="mirror")],
            "two tool sources list a tool named search",
        ),
    ],
)
async def test_a_source_tool_name_collision_fails_the_model_step_before_any_model_call(
    sources: list[_InMemoryToolSource], message: str
) -> None:
    model = _answers()
    runner = BuiltinLoopAgentRunner(
        AgentDefinition(
            name="agent",
            instructions="Help.",
            model=model,
            tools=[take_note],
            tool_sources=sources,
            built_in_tools=frozenset({BuiltInTool.ASK_USER}),
        )
    )
    with pytest.raises(ValueError, match=f"^{message}$"):
        await runner.run_model_step(_turn_context())
    assert model.requests == ()


def test_a_tool_source_needs_every_method() -> None:
    with pytest.raises(TypeError, match="has no list_tools method"):
        AgentDefinition(
            name="agent",
            instructions="Help.",
            model=_answers(),
            tool_sources=[object()],  # type: ignore[list-item]
        )


async def test_resource_and_prompt_tools_retry_the_connection_and_say_why_they_failed(
    tmp_path: Path,
) -> None:
    log = tmp_path / "launches.log"
    source = McpToolSource(
        [
            McpStdioServerConfig(
                name="down", command=_launch_script(tmp_path / "down", log, "exit 1")
            )
        ],
        connection_attempts=2,
        resource_and_prompt_tools_enabled=True,
    )
    assert source.declared_tool_names() == {
        "mcp_list_resources_and_prompts",
        "mcp_read_resource",
        "mcp_get_prompt",
    }
    with pytest.raises(ToolExecutionError) as raised:
        await source.call_tool("mcp_read_resource", {"server": "down", "uri": "memo://greeting"})
    message = str(raised.value)
    assert message.startswith(
        "could not read from MCP server down, so mcp_read_resource did not finish: "
    )
    assert "TaskGroup" not in message
    assert _launches(log) == 2


class _EstimateRecordingModel:
    def __init__(self) -> None:
        self.estimated: list[ModelRequest] = []

    async def call_model(self, request: ModelRequest, output: ModelOutputWriter) -> ModelResponse:
        raise AssertionError("no model call")

    def estimate_input_tokens(self, request: ModelRequest) -> int:
        self.estimated.append(request)
        return 1


class _CachedToolSource(_InMemoryToolSource):
    def cached_tools(self) -> list[_ListedTool]:
        return self._tools


def test_the_estimate_counts_a_source_listing_cached_before_any_model_call() -> None:
    model = _EstimateRecordingModel()
    runner = BuiltinLoopAgentRunner(
        AgentDefinition(
            name="agent",
            instructions="Help.",
            model=model,
            tools=[take_note],
            tool_sources=[_CachedToolSource("search")],
        )
    )
    runner.estimate_input_tokens(_turn_context())
    assert [tool.name for tool in model.estimated[0].tools] == ["take_note", "search", "ask_user"]


async def test_the_demo_model_keeps_only_its_latest_requests() -> None:
    class Discard:
        def write_text_delta(self, text: str) -> None:
            return None

        def write_reasoning_delta(self, text: str) -> None:
            return None

    model = ScriptedModelClient(lambda request: ScriptedModelReply(text="hi"))
    requests = [
        ModelRequest(
            instructions=f"request {index}",
            input_items=[],
            tools=[],
            tool_choice="auto",
            settings=ModelSettings(),
            metadata=ModelRequestMetadata(
                flow_id="f", turn_id="turn-1", step_execution_id="s", attempt=1
            ),
        )
        for index in range(101)
    ]
    for request in requests:
        await model.call_model(request, Discard())
    assert len(model.requests) == 100
    assert model.requests[0].instructions == "request 1"
    silent = ScriptedModelClient(
        lambda request: ScriptedModelReply(text="hi"), max_recorded_requests=0
    )
    await silent.call_model(requests[0], Discard())
    assert silent.requests == ()
    assert ScriptedModelClient is ScriptedModelClientOfTesting
    assert ScriptedModelReply is ScriptedModelReplyOfTesting


def _stdio_server(name: str, state_dir: Path) -> McpStdioServerConfig:
    state_dir.mkdir(exist_ok=True)
    return McpStdioServerConfig(
        name=name,
        command=sys.executable,
        args=["-m", "tests.fake_mcp_server", "stdio"],
        env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
        cwd=str(CORE_DIR),
    )


async def test_a_stored_call_reaches_its_source_after_sources_are_reordered_or_added() -> None:
    search = _InMemoryToolSource("search")
    fetch = _InMemoryToolSource("fetch")
    runner = BuiltinLoopAgentRunner(
        AgentDefinition(
            name="agent", instructions="Help.", model=_calls("fetch"), tool_sources=[search, fetch]
        )
    )
    outcome = await runner.run_model_step(_turn_context())
    assert isinstance(outcome, ToolCallsRequested)
    (call,) = outcome.tool_calls

    for sources in ([fetch, search], [_InMemoryToolSource("crm"), search, fetch]):
        deployed = BuiltinLoopAgentRunner(
            AgentDefinition(
                name="agent", instructions="Help.", model=_answers(), tool_sources=sources
            )
        )
        await deployed.execute_deferred_tool_call(_execution(_record(call)))
        await deployed.poll_tool_task(_poll(_record(call)))
    assert search.executed == []
    assert [record.tool_name for record in fetch.executed] == ["fetch"] * 4


async def test_swapped_mcp_sources_still_run_the_call_on_its_server(tmp_path: Path) -> None:
    files = McpToolSource(
        [_stdio_server("files", tmp_path / "f")], connection_attempts=1, source_name="files"
    )
    other = McpToolSource(
        [_stdio_server("other", tmp_path / "o")], connection_attempts=1, source_name="other"
    )
    model = ScriptedModelClient(
        lambda request: ScriptedModelReply(
            tool_calls=[
                ScriptedToolCall(
                    tool_name="files__send_message", arguments={"text": "x"}, tool_call_id="c1"
                )
            ]
        )
    )
    runner = BuiltinLoopAgentRunner(
        AgentDefinition(
            name="agent", instructions="Help.", model=model, tool_sources=[files, other]
        )
    )
    outcome = await runner.run_model_step(_turn_context())
    assert isinstance(outcome, ToolCallsRequested)
    (call,) = outcome.tool_calls
    swapped = BuiltinLoopAgentRunner(
        AgentDefinition(
            name="agent", instructions="Help.", model=model, tool_sources=[other, files]
        )
    )
    result = await swapped.execute_deferred_tool_call(_execution(_record(call)))
    assert result == ToolExecutionResult(output="sent x")
    assert "send_message" in (tmp_path / "f" / INVOCATION_LOG_NAME).read_text()


async def test_a_refresh_during_the_model_call_does_not_fail_the_model_step(
    tmp_path: Path,
) -> None:
    source = McpToolSource([_stdio_server("files", tmp_path / "f")], connection_attempts=1)

    def respond(request: ModelRequest) -> ScriptedModelReply:
        # A background refresh finishes while the model runs and no longer offers the tool.
        source._tools = {}
        return ScriptedModelReply(
            tool_calls=[
                ScriptedToolCall(
                    tool_name="files__lookup", arguments={"query": "q"}, tool_call_id="c1"
                )
            ]
        )

    model = ScriptedModelClient(respond)
    runner = BuiltinLoopAgentRunner(
        AgentDefinition(name="agent", instructions="Help.", model=model, tool_sources=[source])
    )
    outcome = await runner.run_model_step(_turn_context())
    assert isinstance(outcome, ToolCallsRequested)
    (call,) = outcome.tool_calls
    assert call.policy.effect is ToolEffect.READ
    assert json.loads(json.loads(call.tool_source_reference or "")["reference"]) == {
        "mcp_server": "files",
        "mcp_tool": "lookup",
    }


def test_tool_source_names_must_be_unique_and_present() -> None:
    with pytest.raises(ValueError, match="^two tool sources are named mcp$"):
        AgentDefinition(
            name="agent",
            instructions="Help.",
            model=_answers(),
            tool_sources=[
                McpToolSource([McpStdioServerConfig(name="files", command="a")]),
                McpToolSource([McpStdioServerConfig(name="tickets", command="b")]),
            ],
        )
    renamed = McpToolSource(
        [McpStdioServerConfig(name="files", command="b")], source_name="archive"
    )
    assert renamed.source_name == "archive"
    with pytest.raises(TypeError, match="needs a non-blank source_name"):
        AgentDefinition(
            name="agent",
            instructions="Help.",
            model=_answers(),
            tool_sources=[_InMemoryToolSource("search", source_name=" ")],
        )


async def test_a_stored_call_still_routes_after_a_server_is_added_to_its_source(
    tmp_path: Path,
) -> None:
    model = ScriptedModelClient(
        lambda request: ScriptedModelReply(
            tool_calls=[
                ScriptedToolCall(
                    tool_name="files__send_message", arguments={"text": "x"}, tool_call_id="c1"
                )
            ]
        )
    )
    before = McpToolSource([_stdio_server("files", tmp_path / "f")], connection_attempts=1)
    assert before.source_name == DEFAULT_MCP_SOURCE_NAME == "mcp"
    runner = BuiltinLoopAgentRunner(
        AgentDefinition(name="agent", instructions="Help.", model=model, tool_sources=[before])
    )
    outcome = await runner.run_model_step(_turn_context())
    assert isinstance(outcome, ToolCallsRequested)
    (call,) = outcome.tool_calls

    # A deploy adds a server to the same source; the files server and tool are unchanged.
    after = McpToolSource(
        [_stdio_server("files", tmp_path / "f"), _stdio_server("crm", tmp_path / "c")],
        connection_attempts=1,
    )
    assert after.source_name == before.source_name
    deployed = BuiltinLoopAgentRunner(
        AgentDefinition(name="agent", instructions="Help.", model=model, tool_sources=[after])
    )
    result = await deployed.execute_deferred_tool_call(_execution(_record(call)))
    assert result == ToolExecutionResult(output="sent x")
    assert "send_message" in (tmp_path / "f" / INVOCATION_LOG_NAME).read_text()
