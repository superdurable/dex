# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The command with Deep Agents against the scripted Responses API: chat, tools, and settings."""

from __future__ import annotations

import contextlib
import hashlib
import io
from collections import Counter
from pathlib import Path
from typing import Any

import pexpect
import pytest
from deepagents import create_deep_agent
from langchain.agents.middleware import AgentMiddleware

from dex_agents_cli_core import session_flow_type
from dex_agents_cli_testing import scenarios
from dex_agents_cli_testing.commands import (
    APPROVE_PROMPT,
    CommandHome,
    call,
    expect,
    read_session_id,
    session_client,
    wait_for_exit,
)
from dex_agents_cli_testing.fake_connectors_mcp import (
    COLLIDING_READ_FILE_OUTPUT,
    PREFIXED_READ_FILE_OUTPUT,
)
from dex_agents_cli_testing.fixtures import FakeConnectorsMcp
from dex_agents_deepagents_cli import (
    DEEP_AGENTS_TOOL_RULES,
    DEEPAGENTS_CLI_FLOW_TYPE,
    build_deep_agent,
)
from dex_agents_deepagents_cli.harness import deep_agents_chat_model
from tests.conftest import MOCK_API_KEY

DEFAULTS: dict[str, object] = {}
CLI_CORE_REPLACED_TOOLS = {"list_directory", "search_files", "run_shell_command"}
DEEP_AGENTS_LOCAL_TOOLS = set(DEEP_AGENTS_TOOL_RULES) - {"write_todos"}
_MIDDLEWARE_SEARCH_DEPTH = 50


async def test_a_turn_answers_and_resume_continues_the_session(command_home: CommandHome) -> None:
    await scenarios.chat_turn_and_resume(command_home, DEFAULTS)
    requests = command_home.model_api.model_requests()
    assert requests[0].body["model"] == "gpt-5.2"
    assert {request.headers.get("authorization") for request in requests} == {
        f"Bearer {MOCK_API_KEY}"
    }


async def test_file_tools_read_write_and_edit_workspace_files_without_approval(
    command_home: CommandHome,
) -> None:
    command_home.write_config(scenarios.NO_CONNECTORS)
    workspace = _workspace(command_home)
    (workspace / "notes.txt").write_text("buy milk\n")
    child = _spawn(command_home)
    await expect(child, "> ")
    child.sendline(call("read_file", {"file_path": str(workspace / "notes.txt")}))
    await expect(child, "buy milk")
    await expect(child, "> ")

    child.sendline(call("write_file", {"file_path": str(workspace / "todo.txt"), "content": "a"}))
    await expect(child, "write_file: Updated file")
    await expect(child, "> ")
    assert (workspace / "todo.txt").read_text() == "a"

    child.sendline(
        call(
            "edit_file",
            {"file_path": str(workspace / "todo.txt"), "old_string": "a", "new_string": "call mom"},
        )
    )
    await expect(child, "edit_file: ")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert "[approval]" not in _transcript(child)
    assert (workspace / "todo.txt").read_text() == "call mom"


async def test_execute_runs_a_command_in_the_workspace_without_approval(
    command_home: CommandHome,
) -> None:
    command_home.write_config(scenarios.NO_CONNECTORS)
    workspace = _workspace(command_home)
    child = _spawn(command_home)
    await expect(child, "> ")
    child.sendline(call("execute", {"command": "echo from-execute > shell.txt; echo ran"}))
    await expect(child, "execute: ")
    await expect(child, "ran")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert "[approval]" not in _transcript(child)
    assert (workspace / "shell.txt").read_text().strip() == "from-execute"


async def test_a_file_tool_refuses_a_path_outside_the_workspace(
    command_home: CommandHome, tmp_path: Path
) -> None:
    command_home.write_config(scenarios.NO_CONNECTORS)
    _workspace(command_home)
    outside = tmp_path / "outside.txt"
    child = _spawn(command_home)
    await expect(child, "> ")
    child.sendline(call("write_file", {"file_path": str(outside), "content": "never"}))
    await expect(child, "outside the workspace")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert not outside.exists()


async def test_approval_always_makes_writes_wait_and_rejected_writes_never_run(
    command_home: CommandHome, fake_mcp: FakeConnectorsMcp
) -> None:
    command_home.write_config(
        {"approval": {"always": ["write_file"]}, "connectors": {"mcp": fake_mcp.url}}
    )
    workspace = _workspace(command_home)
    child = _spawn(command_home)
    await expect(child, "> ")
    child.sendline(call("write_file", {"file_path": str(workspace / "a.txt"), "content": "yes"}))
    await expect(child, "[approval] write_file")
    await expect(child, APPROVE_PROMPT)
    assert not (workspace / "a.txt").exists()
    child.sendline("y")
    await expect(child, "write_file: Updated file")
    await expect(child, "> ")
    assert (workspace / "a.txt").read_text() == "yes"

    child.sendline(call("write_file", {"file_path": str(workspace / "b.txt"), "content": "no"}))
    await expect(child, "[approval] write_file")
    await expect(child, APPROVE_PROMPT)
    child.sendline("n not now")
    await expect(child, "write_file: ")
    await expect(child, "> ")

    child.sendline(call("slack_post_thread_reply", {"thread": "T1", "text": "never"}))
    await expect(child, "[approval] slack_post_thread_reply {")
    await expect(child, APPROVE_PROMPT)
    child.sendline("n not now")
    await expect(child, "slack_post_thread_reply: ")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert not (workspace / "b.txt").exists()
    assert fake_mcp.runs("slack_post_thread_reply") == 0


async def test_a_connector_read_runs_at_once_and_a_write_runs_once_after_approval(
    command_home: CommandHome, fake_mcp: FakeConnectorsMcp
) -> None:
    await scenarios.connector_read_and_approved_write(command_home, DEFAULTS, fake_mcp)


async def test_a_connector_tool_named_like_a_deep_agents_tool_gets_a_prefix(
    command_home: CommandHome, fake_mcp_with_colliding_tools: FakeConnectorsMcp
) -> None:
    fake_mcp = fake_mcp_with_colliding_tools
    command_home.write_config({"connectors": {"mcp": fake_mcp.url}})
    workspace = _workspace(command_home)
    (workspace / "notes.txt").write_text("buy milk\n")
    child = _spawn(command_home)
    await expect(child, "> ")
    # The server also lists connector_read_file, so the renamed read_file takes the next name.
    child.sendline(call("connector_connector_read_file", {"file_path": "remote.txt"}))
    await expect(child, f": {COLLIDING_READ_FILE_OUTPUT}: remote.txt")
    await expect(child, "> ")
    child.sendline(call("connector_read_file", {"file_path": "archived.txt"}))
    await expect(child, f"connector_read_file: {PREFIXED_READ_FILE_OUTPUT}: archived.txt")
    await expect(child, "> ")
    child.sendline(call("read_file", {"file_path": str(workspace / "notes.txt")}))
    await expect(child, "buy milk")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert fake_mcp.runs("read_file") == 1
    assert fake_mcp.runs("connector_read_file") == 1
    names = Counter(_offered_tool_names(command_home))
    assert names["read_file"] == 1
    assert names["connector_read_file"] == 1
    assert names["connector_connector_read_file"] == 1
    assert max(names.values()) == 1


async def test_session_settings_change_the_model(command_home: CommandHome, tmp_path: Path) -> None:
    flow_type = session_flow_type(
        DEEPAGENTS_CLI_FLOW_TYPE, command_home.config_path, command_home.workspace
    )
    await scenarios.session_settings_change_the_model(
        command_home, DEFAULTS, flow_type, "openai:gpt-5-mini", "gpt-5-mini", tmp_path / "cache"
    )


async def test_a_reasoning_effort_fails_the_turn_because_the_command_declares_none(
    command_home: CommandHome, tmp_path: Path
) -> None:
    flow_type = session_flow_type(
        DEEPAGENTS_CLI_FLOW_TYPE, command_home.config_path, command_home.workspace
    )
    await scenarios.session_settings_reject_an_undeclared_reasoning_effort(
        command_home, DEFAULTS, flow_type, tmp_path / "cache", "low"
    )


def test_a_key_in_the_deepagents_section_is_a_configuration_error(
    command_home: CommandHome,
) -> None:
    scenarios.harness_section_key_is_a_configuration_error(command_home, DEFAULTS, "deepagents")


async def test_deep_agents_tools_replace_the_cli_core_file_and_shell_tools(
    command_home: CommandHome,
) -> None:
    command_home.write_config({**scenarios.NO_CONNECTORS, "system_prompt": "Be brief."})
    child = _spawn(command_home)
    await expect(child, "> ")
    child.sendline("prompt")
    await expect(child, "prompt: Be brief.")
    await expect(child, "Your workspace is ")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    names = Counter(_offered_tool_names(command_home))
    assert set(DEEP_AGENTS_TOOL_RULES) | {"fetch_web_page", "browser_navigate"} <= set(names)
    assert not CLI_CORE_REPLACED_TOOLS & set(names)
    assert max(names.values()) == 1


async def test_switched_off_file_and_shell_tools_are_not_offered(
    command_home: CommandHome,
) -> None:
    command_home.write_config(
        {
            **scenarios.NO_CONNECTORS,
            "local_tools": {"files": False, "shell": {"enabled": False}},
        }
    )
    child = _spawn(command_home)
    await expect(child, "> ")
    child.sendline(call("read_file", {"file_path": "/etc/hosts"}))
    await expect(child, "unbound read_file")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    names = set(_offered_tool_names(command_home))
    assert "write_todos" in names
    assert not DEEP_AGENTS_LOCAL_TOOLS & names


async def test_write_todos_runs_at_once_and_updates_the_plan(command_home: CommandHome) -> None:
    command_home.write_config(scenarios.NO_CONNECTORS)
    child = _spawn(command_home)
    await expect(child, "> ")
    todos = [
        {"content": "read notes", "status": "in_progress"},
        {"content": "write summary", "status": "pending"},
    ]
    child.sendline(call("write_todos", {"todos": todos}))
    await expect(child, "write_todos: Updated todo list to")
    await expect(child, "write summary")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert "[approval]" not in _transcript(child)


async def test_task_runs_a_subagent_that_writes_a_workspace_file_in_one_tool_step(
    command_home: CommandHome,
) -> None:
    command_home.write_config(scenarios.NO_CONNECTORS)
    workspace = _workspace(command_home)
    target = workspace / "from-subagent.txt"
    subagent_prompt = call("write_file", {"file_path": str(target), "content": "delegated"})
    child = _spawn(command_home)
    await expect(child, "> ")
    child.sendline(
        call("task", {"description": subagent_prompt, "subagent_type": "general-purpose"})
    )
    await expect(child, "task: write_file: Updated file")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert "[approval]" not in _transcript(child)
    assert target.read_text() == "delegated"
    # The main agent's call and answer, and the subagent's call and answer inside the tool Step.
    requests = command_home.model_api.model_requests()
    assert len(requests) == 4
    # The subagent's own messages stay out of the main conversation.
    final_input = requests[-1].body["input"]
    outputs = [item for item in final_input if item.get("type") == "function_call_output"]
    assert len(outputs) == 1


async def test_task_waits_when_approval_always_names_a_tool_the_subagent_can_call(
    command_home: CommandHome,
) -> None:
    command_home.write_config({**scenarios.NO_CONNECTORS, "approval": {"always": ["read_file"]}})
    workspace = _workspace(command_home)
    (workspace / "notes.txt").write_text("buy milk\n")
    subagent_prompt = call("read_file", {"file_path": str(workspace / "notes.txt")})
    child = _spawn(command_home)
    await expect(child, "> ")
    child.sendline(
        call("task", {"description": subagent_prompt, "subagent_type": "general-purpose"})
    )
    await expect(child, "[approval] task")
    await expect(child, APPROVE_PROMPT)
    assert len(command_home.model_api.model_requests()) == 1
    child.sendline("n not now")
    await expect(child, "task: ")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    # The rejected call never ran its subagent.
    assert len(command_home.model_api.model_requests()) == 2


async def test_a_large_result_is_evicted_to_the_sessions_own_private_directory(
    command_home: CommandHome,
) -> None:
    command_home.write_config(scenarios.NO_CONNECTORS)
    _workspace(command_home)
    child = _spawn(command_home)
    session_id = await read_session_id(child)
    await expect(child, "> ")
    child.sendline(call("execute", {"command": "head -c 90000 /dev/zero | tr '\\0' x"}))
    await expect(child, "/large_tool_results/")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    artifacts = command_home.home / ".dex" / "agents" / "deepagents-artifacts"
    session_directory = artifacts / hashlib.sha256(session_id.encode()).hexdigest()
    [evicted] = list((session_directory / "large_tool_results").iterdir())
    assert evicted.read_text().count("x") >= 80_000
    assert [path.name for path in artifacts.iterdir()] == [session_directory.name]


async def test_session_settings_change_the_instructions(
    command_home: CommandHome, tmp_path: Path
) -> None:
    flow_type = session_flow_type(
        DEEPAGENTS_CLI_FLOW_TYPE, command_home.config_path, command_home.workspace
    )
    await scenarios.session_settings_change_the_instructions(
        command_home, DEFAULTS, flow_type, tmp_path / "cache"
    )


def test_the_graph_has_no_summarization_that_calls_a_model_on_its_own(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.setenv("OPENAI_API_KEY", MOCK_API_KEY)
    model = deep_agents_chat_model("openai:gpt-5.2")
    # The plain Deep Agents graph shows the search finds the summarization when it is there.
    assert "SummarizationMiddleware" in _model_node_middleware(create_deep_agent(model))
    assert "SummarizationMiddleware" not in _model_node_middleware(
        build_deep_agent("openai:gpt-5.2", tmp_path)
    )


def test_the_tool_rules_cover_every_tool_of_the_graph(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.setenv("OPENAI_API_KEY", MOCK_API_KEY)
    graph = build_deep_agent("openai:gpt-5.2", tmp_path)
    assert set(graph.nodes["tools"].bound.tools_by_name) == set(DEEP_AGENTS_TOOL_RULES)


async def test_a_model_from_the_session_settings_reaches_every_model_call(
    command_home: CommandHome, tmp_path: Path
) -> None:
    command_home.write_config(scenarios.NO_CONNECTORS)
    workspace = _workspace(command_home)
    (workspace / "notes.txt").write_text("buy milk\n")
    child = _spawn(command_home)
    session_id = await read_session_id(child)
    await expect(child, "> ")
    flow_type = session_flow_type(
        DEEPAGENTS_CLI_FLOW_TYPE, command_home.config_path, command_home.workspace
    )
    async with session_client(command_home.address, tmp_path / "cache", flow_type) as sessions:
        await sessions.update_session_settings(session_id, {"model": "openai:gpt-5-mini"})
    child.sendline(call("read_file", {"file_path": str(workspace / "notes.txt")}))
    await expect(child, "buy milk")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    # One request for the call and one for the answer, both with the session's model.
    models = [request.body["model"] for request in command_home.model_api.model_requests()]
    assert models == ["gpt-5-mini", "gpt-5-mini"]


def _workspace(home: CommandHome) -> Path:
    home.workspace.mkdir(parents=True, exist_ok=True)
    return home.workspace.resolve()


def _offered_tool_names(home: CommandHome) -> list[str]:
    tools = home.model_api.model_requests()[-1].body["tools"]
    return [str(tool["name"]) for tool in tools]


def _spawn(home: CommandHome) -> pexpect.spawn:
    child = home.spawn()
    child.logfile_read = io.StringIO()
    return child


def _transcript(child: pexpect.spawn) -> str:
    logged = child.logfile_read
    assert isinstance(logged, io.StringIO)
    return logged.getvalue()


def _model_node_middleware(graph: Any) -> set[str]:
    """Return the names of the middleware that the model node's wrappers capture."""
    names: set[str] = set()
    seen: set[int] = set()
    pending: list[tuple[Any, int]] = [(graph.nodes["model"].bound, 0)]
    while pending:
        value, depth = pending.pop()
        if id(value) in seen or depth > _MIDDLEWARE_SEARCH_DEPTH:
            continue
        seen.add(id(value))
        if isinstance(value, AgentMiddleware):
            names.add(value.name)
            continue
        children: list[Any] = [
            getattr(value, attribute, None)
            for attribute in ("func", "afunc", "__wrapped__", "__self__")
        ]
        for cell in getattr(value, "__closure__", None) or ():
            with contextlib.suppress(ValueError):
                children.append(cell.cell_contents)
        if isinstance(value, list | tuple):
            children.extend(value)
        pending.extend((child, depth + 1) for child in children if child is not None)
    return names
