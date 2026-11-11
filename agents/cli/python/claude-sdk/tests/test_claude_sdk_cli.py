# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The command with the bundled Claude Code CLI: chat, connectors, Claude Code tools, and auth."""

from __future__ import annotations

import json
import os
import signal
from pathlib import Path
from typing import Any

from dex_agents_claude_sdk_cli import CLAUDE_SDK_CLI_FLOW_TYPE
from dex_agents_cli import session_flow_type
from tests.conftest import (
    MOCK_API_KEY,
    CliHome,
    FakeConnectorsMcp,
    expect,
    read_session_id,
    session_client,
    wait_for_exit,
)

NO_CONNECTORS: dict[str, Any] = {"connectors": {"enabled": []}}
APPROVE_PROMPT = "Approve? [y/N, or n <reason>]"


def call(tool_name: str, tool_input: dict[str, Any]) -> str:
    """Return the scripted model's command to call one tool."""
    return f"call {tool_name} {json.dumps(tool_input)}"


def offered_tool_names(cli_home: CliHome) -> set[str]:
    """Return the tool names of the latest model request."""
    return {str(tool["name"]) for tool in cli_home.model_requests()[-1].get("tools") or []}


async def test_a_turn_streams_the_answer_and_resume_continues_the_session(
    cli_home: CliHome,
) -> None:
    cli_home.write_config(NO_CONNECTORS)
    first = cli_home.spawn()
    session_id = await read_session_id(first)
    await expect(first, "> ")
    first.sendline("say hello")
    await expect(first, "said: hello")
    await expect(first, "> ")
    first.sendeof()
    assert await wait_for_exit(first) == 0

    second = cli_home.spawn("--resume", session_id)
    assert await read_session_id(second) == session_id
    await expect(second, "> ")
    second.sendline("recall")
    await expect(second, "history: say hello")
    second.sendeof()
    assert await wait_for_exit(second) == 0
    request = cli_home.model_requests()[0]
    assert request["model"] == "claude-opus-5-5"
    assert {request.api_key for request in cli_home.messages_api.requests} == {MOCK_API_KEY}


async def test_a_connector_read_runs_at_once_and_a_write_runs_once_after_approval(
    cli_home: CliHome, fake_mcp: FakeConnectorsMcp
) -> None:
    cli_home.write_config({"connectors": {"mcp": fake_mcp.url}})
    child = cli_home.spawn()
    await expect(child, "> ")
    child.sendline(call("hackernews_get_top_stories", {}))
    await expect(child, "hackernews_get_top_stories: 1. Durable agents; 2. Local first")
    await expect(child, "> ")
    assert fake_mcp.runs("hackernews_get_top_stories") == 1

    child.sendline(call("slack_post_thread_reply", {"thread": "T1", "text": "on it"}))
    await expect(child, '[approval] slack_post_thread_reply {"thread": "T1"')
    await expect(child, APPROVE_PROMPT)
    assert fake_mcp.runs("slack_post_thread_reply") == 0
    child.sendline("y")
    await expect(child, "slack_post_thread_reply: posted on it in T1")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert fake_mcp.runs("slack_post_thread_reply") == 1


async def test_read_runs_at_once_and_write_and_bash_run_after_approval(
    cli_home: CliHome,
) -> None:
    cli_home.write_config(NO_CONNECTORS)
    cli_home.workspace.mkdir()
    (cli_home.workspace / "notes.txt").write_text("buy milk\n")
    child = cli_home.spawn()
    await expect(child, "> ")
    child.sendline(call("Read", {"file_path": str(cli_home.workspace / "notes.txt")}))
    await expect(child, "buy milk")
    await expect(child, "> ")
    assert "[approval]" not in (child.before or "")
    assert {"Read", "Grep", "Glob", "Write", "Edit", "Bash", "WebFetch"} <= offered_tool_names(
        cli_home
    )

    todo_path = str(cli_home.workspace / "todo.txt")
    child.sendline(call("Write", {"file_path": todo_path, "content": "call mom"}))
    await expect(child, f"[approval] Write {json.dumps({'file_path': todo_path})[:-1]}")
    await expect(child, APPROVE_PROMPT)
    assert not (cli_home.workspace / "todo.txt").exists()
    child.sendline("y")
    await expect(child, "Write: ")
    await expect(child, "> ")
    assert (cli_home.workspace / "todo.txt").read_text() == "call mom"

    child.sendline(call("Bash", {"command": "echo ran > bash.txt"}))
    await expect(child, '[approval] Bash {"command": "echo ran > bash.txt"}')
    await expect(child, APPROVE_PROMPT)
    assert not (cli_home.workspace / "bash.txt").exists()
    child.sendline("y")
    await expect(child, "Bash: ")
    await expect(child, "> ")
    assert (cli_home.workspace / "bash.txt").read_text() == "ran\n"

    child.sendline(call("Bash", {"command": "echo no > rejected.txt"}))
    await expect(child, "[approval] Bash")
    child.sendline("n not now")
    await expect(child, "Bash: ")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert not (cli_home.workspace / "rejected.txt").exists()


async def test_an_allow_listed_command_runs_at_once_in_the_workspace(cli_home: CliHome) -> None:
    cli_home.write_config(
        {**NO_CONNECTORS, "local_tools": {"shell": {"enabled": True, "allow": ["pwd -P"]}}}
    )
    child = cli_home.spawn()
    await expect(child, "> ")
    child.sendline(call("Bash", {"command": "pwd -P"}))
    await expect(child, f"Bash: {os.path.realpath(cli_home.workspace)}")
    await expect(child, "> ")
    assert "[approval]" not in (child.before or "")
    child.sendeof()
    assert await wait_for_exit(child) == 0


async def test_file_tools_refuse_paths_outside_the_workspace(
    cli_home: CliHome, tmp_path: Path
) -> None:
    secret = tmp_path / "secret.txt"
    secret.write_text("do not read")
    cli_home.write_config(NO_CONNECTORS)
    child = cli_home.spawn()
    await expect(child, "> ")
    child.sendline(call("Read", {"file_path": str(secret)}))
    await expect(child, "Read may only use paths inside the workspace")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert "do not read" not in json.dumps(cli_home.model_requests())


async def test_a_write_through_a_dangling_link_to_outside_is_refused(
    cli_home: CliHome, tmp_path: Path
) -> None:
    outside = tmp_path / "outside"
    outside.mkdir()
    cli_home.workspace.mkdir()
    (cli_home.workspace / "link.txt").symlink_to(outside / "created.txt")
    cli_home.write_config(NO_CONNECTORS)
    child = cli_home.spawn()
    await expect(child, "> ")
    child.sendline(
        call("Write", {"file_path": str(cli_home.workspace / "link.txt"), "content": "x"})
    )
    await expect(child, "Write may only use paths inside the workspace")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert "[approval]" not in (child.before or "")
    assert not (outside / "created.txt").exists()


async def test_glob_and_grep_run_at_once_inside_the_workspace_only(
    cli_home: CliHome, tmp_path: Path
) -> None:
    outside = tmp_path / "outside"
    outside.mkdir()
    (outside / "secret.txt").write_text("do not read")
    cli_home.workspace.mkdir()
    (cli_home.workspace / "notes.txt").write_text("buy milk\n")
    cli_home.write_config(NO_CONNECTORS)
    child = cli_home.spawn()
    await expect(child, "> ")
    child.sendline(call("Glob", {"pattern": "*.txt"}))
    await expect(child, "Glob: ")
    await expect(child, "notes.txt")
    await expect(child, "> ")
    child.sendline(call("Grep", {"pattern": "milk"}))
    await expect(child, "Grep: ")
    await expect(child, "notes.txt")
    await expect(child, "> ")
    for pattern in ("*/../../outside/secret.txt", "**/../../outside/secret.txt"):
        child.sendline(call("Glob", {"pattern": pattern}))
        await expect(child, "Glob may only use paths inside the workspace")
        await expect(child, "> ")
    child.sendline(call("Grep", {"pattern": "read", "glob": "../outside/*"}))
    await expect(child, "Grep may only use paths inside the workspace")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert "[approval]" not in (child.before or "")
    assert "do not read" not in json.dumps(cli_home.model_requests())


async def test_edit_waits_for_approval_and_runs_after_it(cli_home: CliHome) -> None:
    cli_home.workspace.mkdir()
    notes = cli_home.workspace / "notes.txt"
    notes.write_text("buy milk\n")
    cli_home.write_config(NO_CONNECTORS)
    child = cli_home.spawn()
    await expect(child, "> ")
    edit = {"file_path": str(notes), "old_string": "milk", "new_string": "bread"}
    child.sendline(f"{call('Read', {'file_path': str(notes)})} && {call('Edit', edit)}")
    await expect(child, "[approval] Edit")
    await expect(child, APPROVE_PROMPT)
    assert notes.read_text() == "buy milk\n"
    child.sendline("y")
    await expect(child, "Edit: ")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert notes.read_text() == "buy bread\n"


async def test_web_fetch_runs_without_approval(cli_home: CliHome) -> None:
    cli_home.write_config(NO_CONNECTORS)
    child = cli_home.spawn()
    await expect(child, "> ")
    # The refused proxy fails the fetch itself; the test checks only that no approval gates it.
    child.sendline(call("WebFetch", {"url": "https://example.com/", "prompt": "the title"}))
    await expect(child, "WebFetch: ")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert "[approval]" not in (child.before or "")
    assert "WebFetch" in offered_tool_names(cli_home)


async def test_an_allow_listed_background_command_still_waits_for_approval(
    cli_home: CliHome,
) -> None:
    cli_home.write_config(
        {**NO_CONNECTORS, "local_tools": {"shell": {"enabled": True, "allow": ["sleep 0"]}}}
    )
    child = cli_home.spawn()
    await expect(child, "> ")
    child.sendline(call("Bash", {"command": "sleep 0", "run_in_background": True}))
    await expect(child, "[approval] Bash")
    await expect(child, APPROVE_PROMPT)
    child.sendline("n not in the background")
    await expect(child, "Bash: ")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0


async def test_an_approved_call_is_checked_again_by_the_worker_that_runs_it(
    cli_home: CliHome,
) -> None:
    cli_home.write_config(NO_CONNECTORS)
    first = cli_home.spawn()
    session_id = await read_session_id(first)
    await expect(first, "> ")
    todo_path = str(cli_home.workspace / "todo.txt")
    first.sendline(call("Write", {"file_path": todo_path, "content": "call mom"}))
    await expect(first, "[approval] Write")
    await expect(first, APPROVE_PROMPT)
    # Killed while it waits: the approval stays pending in the session.
    first.kill(signal.SIGKILL)
    first.close(force=True)

    # The file now switches the file tools off; the Worker that runs the approved call applies it.
    cli_home.write_config({**NO_CONNECTORS, "local_tools": {"files": False}})
    second = cli_home.spawn("--resume", session_id)
    await expect(second, "[approval] Write")
    await expect(second, APPROVE_PROMPT)
    second.sendline("y")
    await expect(second, "Write is switched off for this session; the call did not run")
    await expect(second, "> ")
    second.sendeof()
    assert await wait_for_exit(second) == 0
    assert not (cli_home.workspace / "todo.txt").exists()


async def test_session_settings_that_switch_the_shell_off_remove_bash(
    cli_home: CliHome, tmp_path: Path
) -> None:
    cli_home.write_config(NO_CONNECTORS)
    child = cli_home.spawn()
    session_id = await read_session_id(child)
    await expect(child, "> ")
    flow_type = session_flow_type(
        CLAUDE_SDK_CLI_FLOW_TYPE, cli_home.config_path, cli_home.workspace
    )
    async with session_client(cli_home.address, tmp_path / "client-cache", flow_type) as sessions:
        await sessions.update_session_settings(
            session_id, {"local_tools": {"shell": {"enabled": False}}}
        )
    child.sendline(call("Bash", {"command": "echo ran > bash.txt"}))
    await expect(child, "Bash: ")
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert "Bash" not in offered_tool_names(cli_home)
    assert "Read" in offered_tool_names(cli_home)
    assert not (cli_home.workspace / "bash.txt").exists()


async def test_with_a_key_the_claude_code_cli_uses_it(cli_home: CliHome) -> None:
    cli_home.write_config(NO_CONNECTORS)
    completed = cli_home.run(stdin="say keyed\n")
    assert completed.returncode == 0, completed.stderr
    assert "said: keyed" in completed.stdout
    assert {request.api_key for request in cli_home.messages_api.requests} == {MOCK_API_KEY}
    assert MOCK_API_KEY not in completed.stdout + completed.stderr
    assert MOCK_API_KEY not in _home_text(cli_home)


async def test_without_a_key_the_environment_and_claude_config_dir_pass_through(
    cli_home: CliHome, tmp_path: Path
) -> None:
    cli_home.write_config(NO_CONNECTORS)
    del cli_home.claude_environment["ANTHROPIC_API_KEY"]
    cli_home.claude_environment["ANTHROPIC_AUTH_TOKEN"] = "mock-login-token"
    claude_config_dir = tmp_path / "claude-config"
    cli_home.claude_environment["CLAUDE_CONFIG_DIR"] = str(claude_config_dir)
    completed = cli_home.run(stdin="say token\n")
    assert completed.returncode == 0, completed.stderr
    assert "said: token" in completed.stdout
    requests = cli_home.messages_api.requests
    assert {request.api_key for request in requests} == {None}
    assert {request.authorization for request in requests} == {"Bearer mock-login-token"}
    # The first segment writes Claude Code's own transcript where CLAUDE_CONFIG_DIR says.
    assert list((claude_config_dir / "projects").rglob("*.jsonl"))
    assert not (cli_home.home / ".claude" / "projects").exists()


async def test_without_claude_config_dir_the_cli_keeps_its_default(cli_home: CliHome) -> None:
    cli_home.write_config(NO_CONNECTORS)
    del cli_home.claude_environment["ANTHROPIC_API_KEY"]
    cli_home.claude_environment["ANTHROPIC_AUTH_TOKEN"] = "mock-login-token"
    completed = cli_home.run(stdin="say default\n")
    assert completed.returncode == 0, completed.stderr
    assert list((cli_home.home / ".claude" / "projects").rglob("*.jsonl"))


async def test_a_key_in_the_claude_section_is_a_configuration_error(cli_home: CliHome) -> None:
    cli_home.write_config({**NO_CONNECTORS, "claude": {"max_turns": 3}})
    completed = cli_home.run(stdin="say never\n")
    assert completed.returncode == 2
    assert 'schema at "/claude/max_turns"' in completed.stderr
    assert not cli_home.model_requests()


def _home_text(cli_home: CliHome) -> str:
    texts = []
    for path in (cli_home.home / ".dex").rglob("*"):
        if path.is_file() and path.suffix in (".yaml", ".json", ".log"):
            texts.append(path.read_text(errors="replace"))
    return "\n".join(texts)
