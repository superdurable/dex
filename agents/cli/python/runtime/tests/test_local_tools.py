# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Local tools in a real session: workspace confinement, approvals, the browser, and crashes."""

from __future__ import annotations

import asyncio
import json
import threading
import uuid
from collections.abc import AsyncIterator, Iterator
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

import pytest

from dex_agents import AgentInterrupt, AgentSessionClient, AgentSnapshot, MessageRole
from dex_agents.runner import OUTCOME_UNKNOWN_TOOL_RESULT
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    AgentTestStack,
    wait_for_agent_snapshot,
    wait_until,
)
from dex_agents_cli.local_tools import BrowserSessions
from tests.conftest import PACKAGE_DIR
from tests.local_tools_worker import (
    CONFIG_ENVIRONMENT_VARIABLE,
    WORKSPACE_ENVIRONMENT_VARIABLE,
)
from tests.scripted_harness import build_session_flow_without_tools

KILLED_COMMAND = "echo run >> runs.txt; touch started.flag; sleep 4"
CONFIG = {"local_tools": {"shell": {"enabled": True, "allow": ["echo allowed", KILLED_COMMAND]}}}


@dataclass(frozen=True)
class LocalToolsStack:
    stack: AgentTestStack
    sessions: AgentSessionClient
    workspace: Path
    outside: Path


@pytest.fixture(scope="module")
async def local_tools_stack(
    tmp_path_factory: pytest.TempPathFactory,
) -> AsyncIterator[LocalToolsStack]:
    root = tmp_path_factory.mktemp("local-tools")
    workspace = root / "workspace"
    outside = root / "outside"
    workspace.mkdir()
    outside.mkdir()
    flow = build_session_flow_without_tools()
    async with AgentTestStack(
        root / "stack",
        [flow],
        "tests.local_tools_worker",
        worker_cwd=PACKAGE_DIR,
        worker_env={
            CONFIG_ENVIRONMENT_VARIABLE: json.dumps(CONFIG),
            WORKSPACE_ENVIRONMENT_VARIABLE: str(workspace),
        },
    ) as stack:
        await stack.start_worker()
        yield LocalToolsStack(stack, stack.sessions(flow), workspace, outside)


async def start_turn(sessions: AgentSessionClient, message: str) -> str:
    session_id = f"local-tools-{uuid.uuid4().hex[:8]}"
    await sessions.start_session(session_id, message)
    return session_id


async def wait_for_turn_end(sessions: AgentSessionClient, session_id: str) -> AgentSnapshot:
    def finished(snapshot: AgentSnapshot) -> AgentSnapshot | None:
        state = snapshot.state
        is_done = state is not None and state.turn_count >= 1 and state.active_turn_id is None
        return snapshot if is_done else None

    return await wait_for_agent_snapshot(sessions, session_id, finished, description="turn end")


async def wait_for_approval(
    sessions: AgentSessionClient, session_id: str, tool_name: str
) -> AgentInterrupt:
    def pending(snapshot: AgentSnapshot) -> AgentInterrupt | None:
        return next((i for i in snapshot.pending_interrupts if i.tool_name == tool_name), None)

    return await wait_for_agent_snapshot(
        sessions, session_id, pending, description=f"approval of {tool_name}"
    )


def tool_outputs(snapshot: AgentSnapshot) -> list[tuple[str, str]]:
    return [
        (message.tool_name or "", message.tool_output or "")
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ]


async def executed_steps(stack: AgentTestStack, session_id: str, step_type: str) -> int:
    history = await stack.read_step_history(session_id)
    return sum(
        1
        for entry in history
        if entry.step_type == step_type and entry.event_type == EXECUTE_COMPLETED_EVENT
    )


async def test_paths_outside_the_workspace_are_refused(local_tools_stack: LocalToolsStack) -> None:
    workspace, outside = local_tools_stack.workspace, local_tools_stack.outside
    (outside / "secret.txt").write_text("secret")
    (workspace / "inside.txt").write_text("inside")
    (workspace / "escape").symlink_to(outside, target_is_directory=True)
    calls: list[list[Any]] = [
        ["read_file", {"path": "inside.txt"}],
        ["read_file", {"path": "../outside/secret.txt"}],
        ["read_file", {"path": "escape/secret.txt"}],
        ["read_file", {"path": str(outside / "secret.txt")}],
        ["list_directory", {"path": ".."}],
        ["write_file", {"path": "escape/planted.txt", "content": "x"}],
    ]
    sessions = local_tools_stack.sessions
    session_id = await start_turn(sessions, "calls " + json.dumps(calls))
    interrupt = await wait_for_approval(sessions, session_id, "write_file")
    assert await sessions.resolve_interrupt(session_id, interrupt.interrupt_id, approved=True)
    outputs = tool_outputs(await wait_for_turn_end(sessions, session_id))
    assert outputs[0] == ("read_file", "inside")
    for tool_name, output in outputs[1:]:
        assert "outside the workspace" in output, (tool_name, output)
    assert not (outside / "planted.txt").exists()


async def test_a_file_write_waits_for_approval_and_runs_once(
    local_tools_stack: LocalToolsStack,
) -> None:
    sessions = local_tools_stack.sessions
    message = 'call write_file {"path": "approved.txt", "content": "v1"}'
    session_id = await start_turn(sessions, message)
    interrupt = await wait_for_approval(sessions, session_id, "write_file")
    assert not (local_tools_stack.workspace / "approved.txt").exists()
    assert await sessions.resolve_interrupt(session_id, interrupt.interrupt_id, approved=True)
    outputs = tool_outputs(await wait_for_turn_end(sessions, session_id))
    assert outputs == [("write_file", "wrote 2 bytes to approved.txt")]
    assert (local_tools_stack.workspace / "approved.txt").read_text() == "v1"
    assert await executed_steps(local_tools_stack.stack, session_id, "CallTool") == 1


async def test_a_listed_shell_command_runs_at_once_and_another_waits_for_approval(
    local_tools_stack: LocalToolsStack,
) -> None:
    sessions = local_tools_stack.sessions
    listed = await start_turn(sessions, 'call run_shell_command {"command": "echo allowed"}')
    snapshot = await wait_for_turn_end(sessions, listed)
    assert tool_outputs(snapshot) == [("run_shell_command", "exit code 0\nstdout:\nallowed\n")]

    unlisted = await start_turn(sessions, 'call run_shell_command {"command": "echo allowed "}')
    interrupt = await wait_for_approval(sessions, unlisted, "run_shell_command")
    assert interrupt.arguments == '{"command": "echo allowed "}'
    assert await sessions.resolve_interrupt(unlisted, interrupt.interrupt_id, approved=True)
    outputs = tool_outputs(await wait_for_turn_end(sessions, unlisted))
    assert outputs == [("run_shell_command", "exit code 0\nstdout:\nallowed\n")]


async def test_a_shell_command_is_not_run_again_after_the_worker_dies_during_it(
    local_tools_stack: LocalToolsStack,
) -> None:
    stack, workspace = local_tools_stack.stack, local_tools_stack.workspace
    sessions = local_tools_stack.sessions
    message = "call run_shell_command " + json.dumps({"command": KILLED_COMMAND})
    session_id = await start_turn(sessions, message)

    async def has_started() -> bool:
        return (workspace / "started.flag").exists()

    await wait_until("the command started", has_started)
    stack.workers[-1].kill()
    await stack.start_worker()
    snapshot = await wait_for_turn_end(sessions, session_id)
    assert tool_outputs(snapshot) == [("run_shell_command", OUTCOME_UNKNOWN_TOOL_RESULT)]
    await asyncio.sleep(5)  # Longer than the command's sleep, so a rerun would have written.
    assert (workspace / "runs.txt").read_text() == "run\n"


@pytest.fixture(scope="module")
async def browser_available(tmp_path_factory: pytest.TempPathFactory) -> None:
    """Skip the browser tests only when the headless browser cannot be installed or started."""
    page = tmp_path_factory.mktemp("browser-probe") / "probe.html"
    page.write_text("<title>probe</title>")
    browser = BrowserSessions()
    try:
        # Opening a page downloads the browser only when it is missing.
        await browser.open_page("probe", page.as_uri())
    except Exception as error:
        pytest.skip(f"Playwright's headless browser is unavailable here: {error}")
    finally:
        await browser.close()


@pytest.fixture
def form_site() -> Iterator[tuple[str, list[str]]]:
    submissions: list[str] = []

    class FormSite(BaseHTTPRequestHandler):
        def do_GET(self) -> None:
            self._answer(
                "<html><head><title>Sign up</title></head><body><h1>Newsletter</h1>"
                '<form method="post" action="/submit"><label>Email '
                '<input name="email" value="me@example.com"></label>'
                '<button type="submit">Subscribe</button></form></body></html>'
            )

        def do_POST(self) -> None:
            length = int(self.headers.get("Content-Length", "0"))
            submissions.append(self.rfile.read(length).decode())
            self._answer("<html><head><title>Thanks</title></head><body><h1>Subscribed</h1>")

        def _answer(self, html: str) -> None:
            body = html.encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/html")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, format: str, *args: object) -> None:
            del format, args

    server = ThreadingHTTPServer(("127.0.0.1", 0), FormSite)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_address[1]}/form", submissions
    finally:
        server.shutdown()


async def test_browser_reads_run_at_once_and_a_form_submit_waits_for_approval(
    local_tools_stack: LocalToolsStack,
    browser_available: None,
    form_site: tuple[str, list[str]],
) -> None:
    url, submissions = form_site
    sessions = local_tools_stack.sessions
    session_id = await start_turn(sessions, "call browser_navigate " + json.dumps({"url": url}))
    opened = tool_outputs(await wait_for_turn_end(sessions, session_id))
    assert opened[0][0] == "browser_navigate"
    assert "title: Sign up" in opened[0][1]
    assert 'heading "Newsletter"' in opened[0][1]

    assert await sessions.send_user_message(
        session_id, 'call browser_submit_form {"selector": "role=button[name=\\"Subscribe\\"]"}'
    )
    interrupt = await wait_for_approval(sessions, session_id, "browser_submit_form")
    assert submissions == []
    assert await sessions.resolve_interrupt(session_id, interrupt.interrupt_id, approved=True)

    def second_turn_done(snapshot: AgentSnapshot) -> AgentSnapshot | None:
        state = snapshot.state
        return snapshot if state and state.turn_count >= 2 and not state.active_turn_id else None

    snapshot = await wait_for_agent_snapshot(
        sessions, session_id, second_turn_done, description="the submit turn"
    )
    submitted = tool_outputs(snapshot)[-1]
    assert submitted[0] == "browser_submit_form"
    assert "title: Thanks" in submitted[1]
    assert submissions == ["email=me%40example.com"]
