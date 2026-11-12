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
import contextlib
import functools
import json
import os
import shutil
import threading
import uuid
from collections.abc import AsyncIterator, Iterator
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

import pytest

from dex_agents import (
    MAX_APPROVAL_SUMMARY_BYTES,
    AgentInterrupt,
    AgentInterruptDisplay,
    AgentSessionClient,
    AgentSnapshot,
    MessageRole,
    ToolEffect,
    ToolExecutionError,
)
from dex_agents.runner import OUTCOME_UNKNOWN_TOOL_RESULT
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    AgentTestStack,
    wait_for_agent_snapshot,
    wait_until,
)
from dex_agents_cli_core.local_tools import BrowserSessions
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


async def start_turn(
    sessions: AgentSessionClient, message: str, session_settings: dict[str, Any] | None = None
) -> str:
    session_id = f"local-tools-{uuid.uuid4().hex[:8]}"
    await sessions.start_session(session_id, message, session_settings=session_settings)
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


async def test_links_that_stay_inside_work_and_links_that_leave_fail(
    local_tools_stack: LocalToolsStack,
) -> None:
    workspace, outside = local_tools_stack.workspace, local_tools_stack.outside
    (workspace / "tools" / "bin").mkdir(parents=True, exist_ok=True)
    (workspace / "tools" / "bin" / "tool").write_text("internal tool")
    (workspace / "links").mkdir(exist_ok=True)
    (workspace / "links" / "bin").symlink_to("../tools/bin", target_is_directory=True)
    (workspace / "links" / "absolute.txt").symlink_to(workspace / "tools" / "bin" / "tool")
    (outside / "leaked.txt").write_text("leaked")
    (workspace / "links" / "leaving.txt").symlink_to("../../outside/leaked.txt")
    calls: list[list[Any]] = [
        ["read_file", {"path": "links/bin/tool"}],
        ["read_file", {"path": "links/absolute.txt"}],
        ["list_directory", {"path": "links/bin"}],
        ["read_file", {"path": "links/leaving.txt"}],
    ]
    sessions = local_tools_stack.sessions
    session_id = await start_turn(sessions, "calls " + json.dumps(calls))
    outputs = tool_outputs(await wait_for_turn_end(sessions, session_id))
    assert outputs[0] == ("read_file", "internal tool")
    assert outputs[1] == ("read_file", "internal tool")
    assert outputs[2] == ("list_directory", "tool")
    assert "outside the workspace" in outputs[3][1]


async def test_hard_links_and_swapped_symbolic_links_never_reach_outside(
    local_tools_stack: LocalToolsStack,
) -> None:
    workspace, outside = local_tools_stack.workspace, local_tools_stack.outside
    sessions = local_tools_stack.sessions
    (outside / "hard-secret.txt").write_text("hard secret")
    os.link(outside / "hard-secret.txt", workspace / "hard.txt")
    (outside / "target.txt").write_text("original")
    (workspace / "link.txt").symlink_to(outside / "target.txt")
    swap = workspace / "swap"
    swap.mkdir()
    (swap / "note.txt").write_text("inside note")
    before = await start_turn(sessions, 'call read_file {"path": "swap/note.txt"}')
    assert tool_outputs(await wait_for_turn_end(sessions, before)) == [("read_file", "inside note")]

    # The directory becomes a link to outside between two calls.
    shutil.rmtree(swap)
    swap.symlink_to(outside, target_is_directory=True)
    (outside / "note.txt").write_text("outside note")
    calls: list[list[Any]] = [
        ["read_file", {"path": "hard.txt"}],
        ["read_file", {"path": "swap/note.txt"}],
        ["write_file", {"path": "swap/new.txt", "content": "x"}],
        ["write_file", {"path": "link.txt", "content": "replaced"}],
    ]
    session_id = await start_turn(sessions, "calls " + json.dumps(calls))
    for _ in range(2):
        interrupt = await wait_for_approval(sessions, session_id, "write_file")
        assert await sessions.resolve_interrupt(session_id, interrupt.interrupt_id, approved=True)
    outputs = dict(enumerate(tool_outputs(await wait_for_turn_end(sessions, session_id))))
    assert "other hard links" in outputs[0][1]
    assert "outside the workspace" in outputs[1][1]
    assert "outside the workspace" in outputs[2][1]
    assert "outside the workspace" in outputs[3][1]
    assert not (outside / "new.txt").exists()
    # Nothing is written through a link at the name.
    assert (outside / "target.txt").read_text() == "original"
    assert (workspace / "link.txt").is_symlink()


async def test_search_files_never_reads_outside_while_a_directory_is_swapped(
    local_tools_stack: LocalToolsStack,
) -> None:
    workspace, outside = local_tools_stack.workspace, local_tools_stack.outside
    sessions = local_tools_stack.sessions
    (outside / "swapped").mkdir(exist_ok=True)
    (outside / "swapped" / "leak.txt").write_text("needle outside")
    racing = workspace / "racing"
    stop = threading.Event()

    def swap_back_and_forth() -> None:
        while not stop.is_set():
            shutil.rmtree(racing, ignore_errors=True)
            with contextlib.suppress(OSError):
                racing.unlink()
            racing.mkdir()
            (racing / "inside.txt").write_text("needle inside")
            shutil.rmtree(racing, ignore_errors=True)
            racing.symlink_to(outside / "swapped", target_is_directory=True)
            racing.unlink()

    swapper = threading.Thread(target=swap_back_and_forth, daemon=True)
    swapper.start()
    try:
        outputs: list[str] = []
        for _ in range(5):
            session_id = await start_turn(
                sessions, 'call search_files {"pattern": "needle", "path": "."}'
            )
            interrupt = await wait_for_approval(sessions, session_id, "search_files")
            assert await sessions.resolve_interrupt(
                session_id, interrupt.interrupt_id, approved=True
            )
            outputs.extend(
                output for _, output in tool_outputs(await wait_for_turn_end(sessions, session_id))
            )
    finally:
        stop.set()
        swapper.join()
    assert outputs
    assert not any("needle outside" in output for output in outputs), outputs


async def test_a_case_variant_path_never_leaves_the_workspace(
    local_tools_stack: LocalToolsStack,
) -> None:
    workspace, outside = local_tools_stack.workspace, local_tools_stack.outside
    variant = Path(str(workspace).swapcase())
    if not variant.exists():
        pytest.skip("the filesystem is case-sensitive, so no case variant names the workspace")
    (workspace / "Case.txt").write_text("inside case")
    (outside / "secret.txt").write_text("secret")
    calls: list[list[Any]] = [
        ["read_file", {"path": "CASE.TXT"}],
        ["read_file", {"path": str(variant / "Case.txt")}],
        ["read_file", {"path": f"{variant}/../outside/secret.txt"}],
    ]
    sessions = local_tools_stack.sessions
    session_id = await start_turn(sessions, "calls " + json.dumps(calls))
    outputs = tool_outputs(await wait_for_turn_end(sessions, session_id))
    assert outputs[0] == ("read_file", "inside case")
    assert outputs[1] == ("read_file", "inside case")
    assert "outside the workspace" in outputs[2][1]


async def test_a_replacement_tool_sees_its_turns_narrowed_settings_in_its_own_step(
    local_tools_stack: LocalToolsStack,
) -> None:
    sessions = local_tools_stack.sessions
    narrowed = await start_turn(
        sessions,
        "call report_shell_allow {}",
        {"local_tools": {"shell": {"allow": ["echo allowed"]}}},
    )
    outputs = tool_outputs(await wait_for_turn_end(sessions, narrowed))
    assert outputs == [("report_shell_allow", '{"shell": true, "allow": ["echo allowed"]}')]
    switched_off = await start_turn(
        sessions, "call report_shell_allow {}", {"local_tools": {"shell": {"enabled": False}}}
    )
    outputs = tool_outputs(await wait_for_turn_end(sessions, switched_off))
    assert outputs == [
        (
            "report_shell_allow",
            '{"shell": false, "allow": ["echo allowed", "' + KILLED_COMMAND + '"]}',
        )
    ]


async def test_a_session_policy_raises_a_local_tool_but_never_lowers_it(
    local_tools_stack: LocalToolsStack,
) -> None:
    sessions = local_tools_stack.sessions
    calls: list[list[Any]] = [
        ["edit_file", {"path": "policy.txt", "old_text": "a", "new_text": "b"}],
        ["search_files", {"pattern": "anything"}],
    ]
    session_id = await start_turn(sessions, "calls " + json.dumps(calls))

    def both_waiting(snapshot: AgentSnapshot) -> AgentSnapshot | None:
        return snapshot if len(snapshot.pending_interrupts) == 2 else None

    snapshot = await wait_for_agent_snapshot(
        sessions, session_id, both_waiting, description="two approvals"
    )
    effects = {
        call.tool_name: (call.effect, call.requires_approval) for call in snapshot.tool_calls
    }
    # TOOL_POLICIES lowers edit_file to an unapproved read and raises search_files to a write.
    assert effects == {
        "edit_file": (ToolEffect.NON_IDEMPOTENT_WRITE, True),
        "search_files": (ToolEffect.NON_IDEMPOTENT_WRITE, True),
    }
    for interrupt in snapshot.pending_interrupts:
        assert await sessions.resolve_interrupt(session_id, interrupt.interrupt_id, approved=False)
    await wait_for_turn_end(sessions, session_id)


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


async def approval_display_of(
    sessions: AgentSessionClient, tool_name: str, arguments: dict[str, Any]
) -> AgentInterruptDisplay | None:
    """Request one call, read its approval's display, and reject it."""
    session_id = await start_turn(sessions, f"call {tool_name} {json.dumps(arguments)}")
    interrupt = await wait_for_approval(sessions, session_id, tool_name)
    assert await sessions.resolve_interrupt(session_id, interrupt.interrupt_id, approved=False)
    await wait_for_turn_end(sessions, session_id)
    return interrupt.display


async def test_a_file_write_approval_carries_a_diff_against_the_file_now(
    local_tools_stack: LocalToolsStack,
) -> None:
    sessions, workspace = local_tools_stack.sessions, local_tools_stack.workspace
    (workspace / "display").mkdir()
    (workspace / "display" / "today.txt").write_text("one\ntwo\nthree\n")

    written = await approval_display_of(
        sessions, "write_file", {"path": "display/today.txt", "content": "one\nTWO\nthree\n"}
    )
    assert written == AgentInterruptDisplay(
        target="display/today.txt",
        summary="Writes 14 bytes to display/today.txt.",
        preview="--- display/today.txt (the file now)\n"
        "+++ display/today.txt (after this write)\n"
        "@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n",
    )
    created = await approval_display_of(
        sessions, "write_file", {"path": "display/new/draft.md", "content": "# Title\n"}
    )
    assert created == AgentInterruptDisplay(
        target="display/new/draft.md",
        summary="Writes 8 bytes to display/new/draft.md.",
        preview="--- /dev/null (no file now)\n+++ display/new/draft.md (after this write)\n"
        "@@ -0,0 +1 @@\n+# Title\n",
    )
    edited = await approval_display_of(
        sessions,
        "edit_file",
        {"path": "display/today.txt", "old_text": "three", "new_text": "3"},
    )
    assert edited == AgentInterruptDisplay(
        target="display/today.txt",
        summary="Replaces 5 bytes with 1 byte in display/today.txt.",
        preview="--- display/today.txt (the file now)\n"
        "+++ display/today.txt (after this write)\n"
        "@@ -1,3 +1,3 @@\n one\n two\n-three\n+3\n",
    )
    missing = await approval_display_of(
        sessions,
        "edit_file",
        {"path": "display/today.txt", "old_text": "absent", "new_text": "x"},
    )
    assert missing is not None
    assert missing.preview is None


async def test_a_shell_command_approval_carries_the_full_command_and_its_directory(
    local_tools_stack: LocalToolsStack,
) -> None:
    command = "npm test -- --watch=false"
    display = await approval_display_of(
        local_tools_stack.sessions, "run_shell_command", {"command": command}
    )
    workspace = str(local_tools_stack.workspace)
    assert display == AgentInterruptDisplay(
        target=workspace, summary=f"Runs a shell command in {workspace}: {command}"
    )


async def test_a_shell_command_approval_redacts_inline_credentials_and_cuts_a_long_command(
    local_tools_stack: LocalToolsStack,
) -> None:
    sessions = local_tools_stack.sessions
    workspace = str(local_tools_stack.workspace)
    secret_command = (
        'curl -H "Authorization: Bearer abc123456" https://ci:hunter2@api.example.com?token=t1'
    )
    session_id = await start_turn(
        sessions, "call run_shell_command " + json.dumps({"command": secret_command})
    )
    interrupt = await wait_for_approval(sessions, session_id, "run_shell_command")
    assert interrupt.display is not None
    assert interrupt.display.summary == (
        f"Runs a shell command in {workspace}: "
        'curl -H "Authorization: Bearer <redacted>" '
        "https://ci:<redacted>@api.example.com?token=<redacted>"
    )
    # The display redacts; the arguments keep the full command.
    assert json.loads(interrupt.arguments)["command"] == secret_command
    assert await sessions.resolve_interrupt(session_id, interrupt.interrupt_id, approved=False)
    await wait_for_turn_end(sessions, session_id)

    long_command = "echo " + "x" * (20 * 1024)
    display = await approval_display_of(sessions, "run_shell_command", {"command": long_command})
    assert display is not None
    assert len(display.summary.encode("utf-8")) == MAX_APPROVAL_SUMMARY_BYTES
    assert display.summary.endswith("…")


async def test_a_write_preview_is_left_out_when_too_large_secret_or_outside(
    local_tools_stack: LocalToolsStack,
) -> None:
    sessions, workspace = local_tools_stack.sessions, local_tools_stack.workspace
    outside = local_tools_stack.outside
    (outside / "preview-secret.txt").write_text("outside secret\n")
    (workspace / "preview-link.txt").symlink_to(outside / "preview-secret.txt")
    (workspace / ".env").write_text("API_KEY=old-value\n")

    huge = await approval_display_of(
        sessions, "write_file", {"path": "huge.txt", "content": "x" * (70 * 1024)}
    )
    assert huge == AgentInterruptDisplay(
        target="huge.txt", summary=f"Writes {70 * 1024} bytes to huge.txt."
    )
    many_lines = "\n".join(f"line {index}" for index in range(300))
    long = await approval_display_of(
        sessions, "write_file", {"path": "long.txt", "content": many_lines}
    )
    assert long is not None
    assert long.preview is None
    secret = await approval_display_of(
        sessions, "write_file", {"path": ".env", "content": "API_KEY=new-value\n"}
    )
    assert secret == AgentInterruptDisplay(target=".env", summary="Writes 18 bytes to .env.")
    linked = await approval_display_of(
        sessions, "write_file", {"path": "preview-link.txt", "content": "x"}
    )
    assert linked == AgentInterruptDisplay(
        target="preview-link.txt", summary="Writes 1 byte to preview-link.txt."
    )
    with_secret = await approval_display_of(
        sessions,
        "write_file",
        {"path": "notes/settings.json", "content": '{"credentials": "topsecret"}\n'},
    )
    assert with_secret is not None
    assert with_secret.preview is None
    secrets_file = await approval_display_of(
        sessions, "write_file", {"path": "config/secrets.yaml", "content": "a: 1\n"}
    )
    assert secrets_file is not None
    assert secrets_file.preview is None


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
    directory = tmp_path_factory.mktemp("browser-probe")
    (directory / "probe.html").write_text("<title>probe</title>")
    handler = functools.partial(SimpleHTTPRequestHandler, directory=str(directory))
    server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    browser = BrowserSessions()
    try:
        # Opening a page downloads the browser only when it is missing.
        await browser.open_page("probe", f"http://127.0.0.1:{server.server_address[1]}/probe.html")
    except Exception as error:
        pytest.skip(f"Playwright's headless browser is unavailable here: {error}")
    finally:
        server.shutdown()
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


async def test_the_browser_refuses_a_file_url(tmp_path: Path) -> None:
    (tmp_path / "secret.html").write_text("<p>OUTSIDE_SECRET_CANARY</p>")
    browser = BrowserSessions()
    try:
        with pytest.raises(ToolExecutionError, match="is not an http or https URL"):
            await browser.open_page("session-1", (tmp_path / "secret.html").as_uri())
    finally:
        await browser.close()


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
