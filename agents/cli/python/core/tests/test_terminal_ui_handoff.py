# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The terminal UI handoff on a pseudo-terminal: the real launcher and Worker host, a fake dexcli.

The fake ``dexcli`` is the contract host's ``tui`` role. It replays a
transcript and fails on any difference, so each exchange below is exactly
what contract 1 says. Faults are injected from outside: SIGSTOP holds the
Worker host while a request waits, SIGKILL ends it mid-request.
"""

from __future__ import annotations

import asyncio
import contextlib
import io
import json
import os
import re
import signal
import subprocess
import sys
import threading
import uuid
from collections.abc import Iterator, Mapping, Sequence
from dataclasses import asdict
from pathlib import Path
from typing import Any

import pexpect
import pytest

from dex_agents import LocalDirectoryMediaStore
from dex_agents_cli_core import cli, session_flow_type
from dex_agents_cli_core.environment import CommandEnvironment
from dex_agents_cli_core.harness import TerminalInterface
from dex_agents_cli_core.local_tools.shell import run_shell_command
from dex_agents_cli_core.terminal_ui import process_groups as process_groups_module
from dex_agents_cli_core.terminal_ui.contract import (
    AUTH_ENVIRONMENT_NAMES,
    TERMINAL_ENVIRONMENT_NAMES,
    candidate_session_ids,
)
from dex_agents_cli_core.terminal_ui.process_groups import (
    ToolProcessGroupRegistry,
    read_process_identity,
    stop_leftover_processes,
)
from tests.channel_closing_cli import CONTROL_CHANNEL_END_VARIABLE
from tests.conftest import (
    CliHome,
    FakeConnectorsMcp,
    config_yaml,
    expect,
    session_client,
    wait_for_exit,
)
from tests.fake_terminal_ui import (
    LAUNCH_IGNORE,
    TRANSCRIPTS_DIR,
    FakeTerminalUi,
    chat_transcript,
    insert_pause,
    is_group_alive,
    is_ready_step,
    load_transcript,
    new_key,
    replace_strings,
    run_record,
    wait_until_gone,
    with_fresh_keys,
    worker_host_pid,
)
from tests.noisy_cli import (
    BUILD_NOISE,
    GRANDCHILD_NOISE,
    IMPORT_NOISE,
    LAUNCHER_NOISE,
    LAUNCHER_NOISE_TRIGGER_VARIABLE,
    OPEN_DESCRIPTOR,
)
from tests.scripted_harness import SCRIPTED_FLOW_TYPE, ScriptedHarness
from tests.slow_worker_host_exit_cli import (
    DEXCLI_DEV_STOPPED_MARKER,
    MARKER_DIRECTORY_VARIABLE,
    WORKER_HOST_CLOSING_MARKER,
)
from tests.test_cli_chat import read_start_flow_input
from tests.test_cli_images import png_bytes

HARNESS = "scripted"
COMMAND = "dex-agents-scripted"
NO_CONNECTORS: dict[str, Any] = {"connectors": {"enabled": []}}
SHELL_CONFIG: dict[str, Any] = {
    **NO_CONNECTORS,
    "local_tools": {"shell": {"enabled": True, "allow": ["sleep 600"]}},
}
SECRET_SENTINEL = "sk-test-terminal-ui-sentinel"
STOPPED_HOST_MESSAGE = "The Worker host stopped."


def request(
    request_id: int, method: str, params: dict[str, Any], key: str | None = None
) -> dict[str, Any]:
    message: dict[str, Any] = {"type": "request", "id": request_id, "method": method}
    if key is not None:
        message["idempotency_key"] = key
    return {"from": "tui", "message": {**message, "params": params}}


def result(request_id: int, value: Mapping[str, Any]) -> dict[str, Any]:
    return {"from": "launcher", "message": {"type": "response", "id": request_id, "result": value}}


def error(request_id: int, code: str, message: str) -> dict[str, Any]:
    return {
        "from": "launcher",
        "message": {
            "type": "response",
            "id": request_id,
            "error": {"code": code, "message": message},
        },
    }


def pause(milliseconds: int) -> dict[str, Any]:
    return {"pause_ms": milliseconds, "role": "tui"}


@pytest.fixture
def screen() -> Iterator[io.StringIO]:
    yield io.StringIO()


def start_chat(
    cli_home: CliHome,
    fake: FakeTerminalUi,
    screen: io.StringIO,
    config: Mapping[str, Any] = NO_CONNECTORS,
    *arguments: str,
    command_module: str = "tests.scripted_cli",
    environment: Mapping[str, str] | None = None,
) -> pexpect.spawn:
    cli_home.write_config(config_yaml(config))
    cli_home.extra_environment.update(fake.environment())
    cli_home.extra_environment["OPENAI_API_KEY"] = SECRET_SENTINEL
    cli_home.extra_environment.update(environment or {})
    child = cli_home.spawn(*arguments, command_module=command_module)
    child.logfile_read = screen
    return child


async def read_session_id(child: pexpect.spawn) -> str:
    await asyncio.to_thread(child.expect, r"Session (\S+)\r?\n")
    match = child.match
    assert match is not None
    return str(match.group(1))


def resume_line(session_id: str) -> str:
    return f"Session {session_id} keeps running. Resume it with: {COMMAND} --resume {session_id}"


def assert_no_launcher_resume_line(screen: io.StringIO, session_id: str) -> None:
    # After ready, dexcli's exit summary is the only resume line: /new can switch sessions.
    assert resume_line(session_id) not in screen.getvalue()
    assert "keeps running. Resume it with" not in screen.getvalue()


def assert_replayed(fake: FakeTerminalUi, screen: io.StringIO) -> None:
    assert fake.mismatches() == []
    assert "closed unexpectedly" not in screen.getvalue()


def scripted_flow_type(cli_home: CliHome) -> str:
    workspace = cli_home.home / "dex-agent-workspace"
    return session_flow_type(SCRIPTED_FLOW_TYPE, cli_home.config_path, workspace)


async def test_a_chat_hands_off_and_relays_each_local_request(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO
) -> None:
    pictures = tmp_path / "pictures"
    pictures.mkdir()
    image = pictures / "screenshot.png"
    image.write_bytes(png_bytes())
    notes = tmp_path / "notes.txt"
    notes.write_text("not an image")
    reference = await LocalDirectoryMediaStore(cli_home.media_directory.absolute()).put_image(
        png_bytes()
    )
    session_key = new_key()
    (started_id, _) = candidate_session_ids(HARNESS, session_key)
    web_url = f"http://127.0.0.1:{cli_home.web_port}"
    page = f"{web_url}/v1/flows/{started_id}?view=session"
    steps = [
        request(1, "put_image", {"path": str(image)}, new_key()),
        result(1, {"image": asdict(reference), "name": "screenshot.png"}),
        request(2, "put_image", {"path": str(notes)}, new_key()),
        error(2, "not_an_image", "notes.txt is not a PNG, JPEG, GIF, or WebP image."),
        request(3, "start_session", {}, session_key),
        result(3, {"session_id": started_id}),
        request(4, "start_session", {}, session_key),
        result(4, {"session_id": started_id}),
        request(5, "get_request_result", {"idempotency_key": session_key}),
        result(5, {"state": "done", "result": {"session_id": started_id}}),
        request(6, "open_url", {"url": page}, new_key()),
        result(6, {}),
        request(
            7, "open_url", {"url": f"http://127.0.0.1:{cli_home.web_port}.evil.example/"}, new_key()
        ),
        error(7, "refused", "open_url only opens Dex Web pages."),
        request(8, "export_transcript", {}, new_key()),
        error(8, "unknown_method", "This command does not serve export_transcript."),
        {"from": "tui", "message": {"type": "future_message", "text": "skipped"}},
        request(9, "list_connectors", {}, new_key()),
        {
            "from": "launcher",
            "message": {"type": "response", "id": 9},
            "ignore": ["result", "error"],
        },
    ]
    transcript = chat_transcript(steps)
    # The scripted runner takes images, has no plan mode, and supports turn control.
    transcript["steps"][0]["ignore"].remove("capabilities")
    fake = FakeTerminalUi(tmp_path / "tui", transcript)
    child = start_chat(cli_home, fake, screen, environment={"DEX_AGENTS_OPEN_BROWSER": "1"})
    session_id = await read_session_id(child)
    assert await wait_for_exit(child) == 0
    assert_no_launcher_resume_line(screen, session_id)
    assert_replayed(fake, screen)
    assert cli_home.opened_urls.read_text().splitlines() == [page]
    for started in (started_id, session_id):
        flow_type, start_input = await read_start_flow_input(cli_home, started)
        assert flow_type == scripted_flow_type(cli_home)
        assert start_input["start_environment"]["workspace"].endswith("dex-agent-workspace")
    (start,) = [event for event in fake.events() if event["event"] == "start"]
    channel_fd = start["argv"][start["argv"].index("--channel-fd") + 1]
    assert start["argv"][-3:] == ["agents-tui", "--channel-fd", channel_fd]
    allowed = set(TERMINAL_ENVIRONMENT_NAMES) | set(AUTH_ENVIRONMENT_NAMES)
    # The wrapper's /bin/sh adds its own variables; macOS adds __CF_USER_TEXT_ENCODING.
    added = {"SHLVL", "PWD", "OLDPWD", "_", "__CF_USER_TEXT_ENCODING"}
    assert set(start["environment_names"]) - added <= allowed
    assert {"HOME", "TERM"} <= set(start["environment_names"])
    assert start["terminal"]["is_foreground"] is True
    assert SECRET_SENTINEL not in fake.record_path.read_text()
    assert list((cli_home.home / ".dex" / "agents" / "run").glob("*.json")) == []


async def test_nothing_the_worker_host_writes_reaches_the_terminal(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO
) -> None:
    fake = FakeTerminalUi(tmp_path / "tui", chat_transcript([pause(3000)]))
    trigger = tmp_path / "launcher-noise"
    child = start_chat(
        cli_home,
        fake,
        screen,
        command_module="tests.noisy_cli",
        environment={LAUNCHER_NOISE_TRIGGER_VARIABLE: str(trigger)},
    )
    session_id = await read_session_id(child)
    await fake.wait_for("sent", type="ready")
    trigger.touch()
    agents_directory = cli_home.home / ".dex" / "agents"
    host_pid = worker_host_pid(agents_directory)
    host_command = subprocess.run(
        ["ps", "-ww", "-o", "command=", "-p", str(host_pid)],
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    (control_fd,) = re.findall(r"__worker-host --control-fd (\d+)", host_command)
    assert await wait_for_exit(child) == 0
    assert_no_launcher_resume_line(screen, session_id)
    assert_replayed(fake, screen)
    shown = screen.getvalue()
    # Only the launcher's own import writes before the handoff; the Worker host's go to the log.
    assert shown.count(IMPORT_NOISE) == 1
    assert shown.index(IMPORT_NOISE) < shown.index(f"Session {session_id}")
    assert BUILD_NOISE not in shown
    assert GRANDCHILD_NOISE not in shown
    log = (cli_home.home / ".dex" / "agents" / f"{HARNESS}.log").read_text()
    assert f"{BUILD_NOISE} on stdout" in log
    assert f"{BUILD_NOISE} on stderr" in log
    assert log.count(GRANDCHILD_NOISE) == 2
    assert f"{OPEN_DESCRIPTOR} {control_fd}\n" not in log
    # The launcher's own fds 1 and 2 point at the log while dexcli owns the terminal.
    assert trigger.with_suffix(".done").exists()
    assert LAUNCHER_NOISE not in shown
    assert f"{LAUNCHER_NOISE} on stdout" in log
    assert f"{LAUNCHER_NOISE} on stderr" in log


@pytest.mark.parametrize("failure", ["timeout", "rejected", "closed"])
async def test_a_handoff_that_fails_before_ready_continues_in_plain_text_on_the_same_session(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO, failure: str
) -> None:
    launch_step = chat_transcript([])["steps"][0]
    transcripts = {
        "timeout": load_transcript("ready_timeout"),
        "rejected": {
            **chat_transcript([]),
            "steps": [
                launch_step,
                {
                    "from": "tui",
                    "message": {
                        "type": "launch_rejected",
                        "code": "auth_missing",
                        "message": "DEX_API_KEY is not set; dex.auth mode api_key needs it.",
                    },
                },
            ],
            "tui_exit": 2,
        },
        "closed": {**chat_transcript([]), "steps": [launch_step, {"close": "tui"}], "tui_exit": 1},
    }
    lines = {
        "timeout": "Plain chat: the terminal UI was not ready in 5 s.",
        "rejected": "Plain chat: DEX_API_KEY is not set; dex.auth mode api_key needs it.",
        "closed": "Plain chat: dexcli ",
    }
    fake = FakeTerminalUi(tmp_path / "tui", transcripts[failure])
    child = start_chat(cli_home, fake, screen)
    session_id = await read_session_id(child)
    await expect(child, lines[failure])
    assert await read_session_id(child) == session_id
    await expect(child, "> ")
    child.sendline("hello")
    await expect(child, "echo: hello")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert fake.mismatches() == []
    # The plain chat it fell back to prints the resume line itself.
    assert resume_line(session_id) in screen.getvalue()


NO_COMMON_CONTRACT: dict[str, Any] = {
    "dexcli_version": "9.0.0",
    "contracts": {"2": {"modes": {"chat": {"uses_methods": []}}, "capabilities": []}},
}
NO_CHAT_MODE: dict[str, Any] = {
    "dexcli_version": "1.60.0",
    "contracts": {"1": {"modes": {"sessions": {"uses_methods": []}}, "capabilities": []}},
}


@pytest.mark.parametrize(
    ("describe", "line"),
    [
        (None, "has no terminal UI (agents-tui --describe exited with code 2)"),
        (
            NO_COMMON_CONTRACT,
            "Plain chat: dexcli 9.0.0 speaks no terminal UI contract this command knows.",
        ),
        (NO_CHAT_MODE, "Plain chat: dexcli 1.60.0 has no chat terminal UI."),
    ],
    ids=["no-agents-tui", "no-common-contract", "mode-missing"],
)
async def test_a_dexcli_that_cannot_show_the_chat_means_the_plain_chat(
    cli_home: CliHome,
    tmp_path: Path,
    screen: io.StringIO,
    describe: Mapping[str, Any] | None,
    line: str,
) -> None:
    fake = FakeTerminalUi(tmp_path / "tui", describe=describe)
    environment: dict[str, str] = {}
    if describe is None:
        older = tmp_path / "older-dexcli"
        older.write_text("#!/bin/sh\necho 'unknown command' >&2\nexit 2\n")
        older.chmod(0o755)
        environment["DEXCLI"] = str(older)
    child = start_chat(cli_home, fake, screen, environment=environment)
    await expect(child, line)
    await read_session_id(child)
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert screen.getvalue().count("Session ") == 2
    assert not fake.was_run()


@pytest.mark.parametrize(
    ("arguments", "environment"),
    [
        (["--plain"], {}),
        ([], {"DEX_AGENTS_TERMINAL_UI": "0"}),
        ([], {"TERM": "dumb"}),
    ],
    ids=["plain-flag", "switch-off", "dumb-terminal"],
)
async def test_plain_choices_never_run_dexcli(
    cli_home: CliHome,
    tmp_path: Path,
    screen: io.StringIO,
    arguments: Sequence[str],
    environment: Mapping[str, str],
) -> None:
    fake = FakeTerminalUi(tmp_path / "tui")
    child = start_chat(cli_home, fake, screen, NO_CONNECTORS, *arguments, environment=environment)
    await read_session_id(child)
    await expect(child, "> ")
    child.sendline("hello")
    await expect(child, "echo: hello")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert fake.invocations() == []
    assert "Plain chat" not in screen.getvalue()


@pytest.mark.parametrize(
    ("platform", "terminal_type", "harness", "is_wanted"),
    [
        ("darwin", "xterm-256color", ScriptedHarness(), True),
        ("linux", "xterm", ScriptedHarness(), True),
        ("win32", "xterm-256color", ScriptedHarness(), False),
        ("darwin", None, ScriptedHarness(), False),
        (
            "darwin",
            "xterm",
            type(
                "PlainHarness", (ScriptedHarness,), {"terminal_interface": TerminalInterface.PLAIN}
            )(),
            False,
        ),
    ],
    ids=["darwin", "linux", "windows", "no-term", "plain-harness"],
)
def test_the_terminal_ui_is_chosen_before_any_probe(
    monkeypatch: pytest.MonkeyPatch,
    platform: str,
    terminal_type: str | None,
    harness: ScriptedHarness,
    is_wanted: bool,
) -> None:
    monkeypatch.setattr(sys, "platform", platform)
    monkeypatch.setattr(cli, "_is_interactive", lambda: True)
    variables = {"HOME": "/home/user", "DEX_AGENTS_TERMINAL_UI": "1"}
    if terminal_type is not None:
        variables["TERM"] = terminal_type
    environment = CommandEnvironment.from_environment(variables)
    arguments = cli._parse_arguments(harness, [])
    assert cli._is_terminal_ui_wanted(arguments, environment, harness) is is_wanted
    plain = cli._parse_arguments(harness, ["--plain"])
    assert cli._is_terminal_ui_wanted(plain, environment, harness) is False


@pytest.mark.parametrize("is_event_loop_blocked", [False, True], ids=["running", "loop-blocked"])
async def test_the_worker_host_and_its_shell_group_end_when_the_launcher_is_killed(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO, is_event_loop_blocked: bool
) -> None:
    fake = FakeTerminalUi(tmp_path / "tui", chat_transcript([pause(300_000)]))
    child = start_chat(cli_home, fake, screen, SHELL_CONFIG)
    session_id = await read_session_id(child)
    await fake.wait_for("sent", type="ready")
    agents_directory = cli_home.home / ".dex" / "agents"
    host_pid = worker_host_pid(agents_directory)
    try:
        async with session_client(
            cli_home.address, tmp_path / "client-cache", scripted_flow_type(cli_home)
        ) as sessions:
            await sessions.send_user_message(
                session_id, 'call run_shell_command {"command": "sleep 600"}'
            )
            tool_pgid = await wait_for_tool_group(agents_directory, host_pid)
            if is_event_loop_blocked:
                await sessions.start_session(f"blocked-{uuid.uuid4().hex[:8]}", "block")
                await asyncio.sleep(3)
        os.kill(child.pid, signal.SIGKILL)
        if sys.platform.startswith("linux"):
            # PR_SET_PDEATHSIG kills the Worker host at once; the next run stops its shell group.
            await wait_until_gone([host_pid], timeout=5)
            assert is_group_alive(tool_pgid)
            lines: list[str] = []
            await asyncio.to_thread(stop_leftover_processes, agents_directory / "run", lines.append)
            assert lines == ["Stopped 1 leftover tool process group."]
            await wait_until_gone([tool_pgid], timeout=5)
            return
        try:
            elapsed = await wait_until_gone([host_pid, tool_pgid], timeout=25)
        except AssertionError as error:
            details = describe_groups(agents_directory, host_pid)
            raise AssertionError(f"{error}\n{details}") from None
        assert elapsed < 20
    finally:
        with_suppressed_kill(child.pid)


def describe_groups(agents_directory: Path, host_pid: int) -> str:
    listing = subprocess.run(
        ["ps", "-A", "-o", "pid=,pgid=,ppid=,stat=,command="],
        capture_output=True,
        text=True,
        check=False,
    ).stdout.splitlines()
    processes = [line for line in listing if line.split()[1:2] == [str(host_pid)]]
    log = (agents_directory / f"{HARNESS}.log").read_text().splitlines()
    return "\n".join([*processes, *log[-40:]])


async def test_a_killed_worker_host_leaves_no_shell_group(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO
) -> None:
    stopped = {
        "from": "launcher",
        "message": {"type": "worker", "state": "stopped", "exit_code": 137},
    }
    fake = FakeTerminalUi(tmp_path / "tui", chat_transcript([stopped]))
    child = start_chat(cli_home, fake, screen, SHELL_CONFIG)
    session_id = await read_session_id(child)
    await fake.wait_for("sent", type="ready")
    agents_directory = cli_home.home / ".dex" / "agents"
    host_pid = worker_host_pid(agents_directory)
    async with session_client(
        cli_home.address, tmp_path / "client-cache", scripted_flow_type(cli_home)
    ) as sessions:
        await sessions.send_user_message(
            session_id, 'call run_shell_command {"command": "sleep 600"}'
        )
        tool_pgid = await wait_for_tool_group(agents_directory, host_pid)
    # The shell may have replaced itself with sleep since it registered; the check still matches.
    os.kill(host_pid, signal.SIGKILL)
    assert await wait_for_exit(child) == 1
    assert_no_launcher_resume_line(screen, session_id)
    assert_replayed(fake, screen)
    await wait_until_gone([tool_pgid], timeout=5)
    assert list((agents_directory / "run").glob("*.json")) == []


async def wait_for_tool_group(agents_directory: Path, host_pid: int) -> int:
    for _ in range(300):
        groups = run_record(agents_directory, host_pid)["tool_process_groups"]
        if groups:
            return int(groups[0]["pgid"])
        await asyncio.sleep(0.1)
    raise AssertionError("the shell command never registered its process group")


def with_suppressed_kill(pgid: int) -> None:
    # The group may hold only zombies by now, which macOS answers with EPERM.
    with contextlib.suppress(ProcessLookupError, PermissionError):
        os.killpg(pgid, signal.SIGKILL)


async def test_a_worker_host_killed_mid_request_answers_host_stopped(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO
) -> None:
    transcript = insert_pause(load_transcript("chat_host_stopped"), is_ready_step, 1500)
    fake = FakeTerminalUi(tmp_path / "tui", with_fresh_keys(transcript, HARNESS))
    child = start_chat(cli_home, fake, screen)
    session_id = await read_session_id(child)
    await fake.wait_for("sent", type="ready")
    host_pid = worker_host_pid(cli_home.home / ".dex" / "agents")
    os.kill(host_pid, signal.SIGSTOP)
    await fake.wait_for("sent", type="request", id=1)
    await asyncio.sleep(0.3)
    os.kill(host_pid, signal.SIGKILL)
    assert await wait_for_exit(child) == 1
    assert_no_launcher_resume_line(screen, session_id)
    assert_replayed(fake, screen)


async def test_a_lost_response_is_recovered_with_get_request_result(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO
) -> None:
    image = tmp_path / "screenshot.png"
    image.write_bytes(png_bytes())
    reference = await LocalDirectoryMediaStore(cli_home.media_directory.absolute()).put_image(
        png_bytes()
    )
    fixture = load_transcript("chat_put_image_cancelled_then_recovered")
    fixture_result = fixture["steps"][-1]["message"]["result"]["result"]
    transcript = replace_strings(
        with_fresh_keys(fixture, HARNESS),
        {"/home/user/pictures/screenshot.png": str(image)},
    )
    transcript["steps"][-1]["message"]["result"]["result"] = {
        **fixture_result,
        "image": asdict(reference),
    }
    transcript = insert_pause(transcript, is_ready_step, 1500)
    transcript = insert_pause(transcript, is_cancelled_response, 1500)
    fake = FakeTerminalUi(tmp_path / "tui", transcript)
    child = start_chat(cli_home, fake, screen)
    session_id = await read_session_id(child)
    await fake.wait_for("sent", type="ready")
    host_pid = worker_host_pid(cli_home.home / ".dex" / "agents")
    os.kill(host_pid, signal.SIGSTOP)
    await fake.wait_for("received", type="response", id=1, error_code="cancelled")
    os.kill(host_pid, signal.SIGCONT)
    assert await wait_for_exit(child) == 0
    assert_no_launcher_resume_line(screen, session_id)
    assert_replayed(fake, screen)


def is_cancelled_response(step: Mapping[str, Any]) -> bool:
    return bool(step.get("message", {}).get("error", {}).get("code") == "cancelled")


async def test_a_key_that_arrives_again_while_it_runs_shares_the_first_outcome(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO
) -> None:
    image = tmp_path / "screenshot.png"
    image.write_bytes(png_bytes())
    reference = await LocalDirectoryMediaStore(cli_home.media_directory.absolute()).put_image(
        png_bytes()
    )
    transcript = replace_strings(
        with_fresh_keys(load_transcript("chat_duplicate_key_while_running"), HARNESS),
        {"/home/user/pictures/screenshot.png": str(image)},
    )
    for step in transcript["steps"]:
        if step.get("message", {}).get("type") == "response":
            step["message"]["result"]["image"] = asdict(reference)
    transcript = insert_pause(transcript, is_ready_step, 1500)
    fake = FakeTerminalUi(tmp_path / "tui", transcript)
    child = start_chat(cli_home, fake, screen)
    session_id = await read_session_id(child)
    await fake.wait_for("sent", type="ready")
    host_pid = worker_host_pid(cli_home.home / ".dex" / "agents")
    os.kill(host_pid, signal.SIGSTOP)
    await fake.wait_for("sent", type="request", id=2)
    await asyncio.sleep(0.3)
    os.kill(host_pid, signal.SIGCONT)
    assert await wait_for_exit(child) == 0
    assert_no_launcher_resume_line(screen, session_id)
    assert_replayed(fake, screen)


async def test_get_request_result_reports_running_until_the_session_starts(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO
) -> None:
    transcript = with_fresh_keys(
        load_transcript("chat_get_request_result_running_then_done"), HARNESS
    )
    transcript = insert_pause(transcript, is_ready_step, 1500)
    transcript = insert_pause(transcript, lambda step: is_running_response(step, 3), 2500)
    fake = FakeTerminalUi(tmp_path / "tui", transcript)
    child = start_chat(cli_home, fake, screen)
    session_id = await read_session_id(child)
    await fake.wait_for("sent", type="ready")
    host_pid = worker_host_pid(cli_home.home / ".dex" / "agents")
    os.kill(host_pid, signal.SIGSTOP)
    await fake.wait_for("received", type="response", id=3)
    os.kill(host_pid, signal.SIGCONT)
    assert await wait_for_exit(child) == 0
    assert_no_launcher_resume_line(screen, session_id)
    assert_replayed(fake, screen)


def is_running_response(step: Mapping[str, Any], request_id: int) -> bool:
    message = step.get("message", {})
    return bool(message.get("id") == request_id and message.get("type") == "response")


async def test_a_resent_key_finds_its_session_and_a_collision_takes_the_long_id(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO
) -> None:
    collision = json.loads(
        (
            Path(__file__).resolve().parents[4] / "conformance" / "terminal_ui" / "session_ids.json"
        ).read_text()
    )["collisions"][0]
    first_key, second_key = collision["idempotency_keys"]
    first_short, _ = candidate_session_ids(HARNESS, first_key)
    _, second_long = candidate_session_ids(HARNESS, second_key)
    taken_key = new_key()
    first_run = FakeTerminalUi(
        tmp_path / "first",
        chat_transcript(
            [
                request(1, "start_session", {}, first_key),
                result(1, {"session_id": first_short}),
                request(2, "start_session", {}, second_key),
                result(2, {"session_id": second_long}),
                request(3, "start_session", {}, taken_key),
                error(
                    3,
                    "id_conflict",
                    "Other requests already started both session IDs this key derives.",
                ),
            ]
        ),
    )
    async with session_client(
        cli_home.address, tmp_path / "client-cache", scripted_flow_type(cli_home)
    ) as sessions:
        for taken in candidate_session_ids(HARNESS, taken_key):
            await sessions.start_session(taken)
    child = start_chat(cli_home, first_run, screen)
    assert await wait_for_exit(child) == 0
    assert_replayed(first_run, screen)
    # A new launcher remembers no key; the Dex server still knows which key started the session.
    second_run = FakeTerminalUi(
        tmp_path / "second",
        chat_transcript(
            [
                request(1, "start_session", {}, second_key),
                result(1, {"session_id": second_long}),
                request(2, "get_request_result", {"idempotency_key": first_key}),
                result(2, {"state": "unknown"}),
                request(3, "start_session", {}, first_key),
                result(3, {"session_id": first_short}),
            ]
        ),
    )
    second_screen = io.StringIO()
    child = start_chat(cli_home, second_run, second_screen)
    assert await wait_for_exit(child) == 0
    assert_replayed(second_run, second_screen)


async def test_a_fifth_waiting_request_is_busy(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO
) -> None:
    connectors_off = (
        "Connector tools are off: connectors.mcp is auto, which needs dex.server: auto."
    )
    transcript = with_fresh_keys(load_transcript("chat_unknown_method_and_busy"), HARNESS)
    for step in transcript["steps"]:
        message = step.get("message", {})
        if message.get("type") == "response" and message.get("id") in (2, 3, 4, 5):
            step["message"] = error(message["id"], "mcp_unavailable", connectors_off)["message"]
    transcript = insert_pause(transcript, is_ready_step, 1500)
    fake = FakeTerminalUi(tmp_path / "tui", transcript)
    config = {"dex": {"server": f"http://{cli_home.address}"}}
    child = start_chat(cli_home, fake, screen, config)
    session_id = await read_session_id(child)
    await fake.wait_for("sent", type="ready")
    host_pid = worker_host_pid(cli_home.home / ".dex" / "agents")
    os.kill(host_pid, signal.SIGSTOP)
    await fake.wait_for("received", type="response", id=7)
    os.kill(host_pid, signal.SIGCONT)
    assert await wait_for_exit(child) == 0
    assert_no_launcher_resume_line(screen, session_id)
    assert_replayed(fake, screen)


@pytest.mark.parametrize(
    ("terminal_ui_exit", "command_exit"), [(0, 0), (1, 1), (130, 130), (143, 1)]
)
async def test_the_command_exits_as_the_terminal_ui_did(
    cli_home: CliHome,
    tmp_path: Path,
    screen: io.StringIO,
    terminal_ui_exit: int,
    command_exit: int,
) -> None:
    fake = FakeTerminalUi(tmp_path / "tui", chat_transcript([], tui_exit=terminal_ui_exit))
    child = start_chat(cli_home, fake, screen)
    session_id = await read_session_id(child)
    assert await wait_for_exit(child) == command_exit
    assert_no_launcher_resume_line(screen, session_id)
    assert_replayed(fake, screen)
    assert list((cli_home.home / ".dex" / "agents" / "run").glob("*.json")) == []


async def test_sigterm_to_the_launcher_ends_the_chat_with_143(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO
) -> None:
    fake = FakeTerminalUi(tmp_path / "tui", chat_transcript([pause(300_000)]))
    child = start_chat(cli_home, fake, screen)
    session_id = await read_session_id(child)
    await fake.wait_for("sent", type="ready")
    host_pid = worker_host_pid(cli_home.home / ".dex" / "agents")
    os.kill(child.pid, signal.SIGTERM)
    assert await wait_for_exit(child) == 143
    assert_no_launcher_resume_line(screen, session_id)
    await wait_until_gone([host_pid], timeout=15)


async def test_a_killed_terminal_ui_restores_the_terminal_and_stops_the_worker_host(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO
) -> None:
    fake = FakeTerminalUi(tmp_path / "tui", chat_transcript([pause(300_000)]))
    child = start_chat(cli_home, fake, screen)
    session_id = await read_session_id(child)
    await fake.wait_for("sent", type="ready")
    host_pid = worker_host_pid(cli_home.home / ".dex" / "agents")
    tui_pids = subprocess.run(
        ["pgrep", "-f", str(fake.transcript_path)], capture_output=True, text=True, check=True
    ).stdout.split()
    for pid in tui_pids:
        os.kill(int(pid), signal.SIGKILL)
    await expect(child, "The chat closed unexpectedly (dexcli exit 137).")
    assert await wait_for_exit(child) == 1
    assert_no_launcher_resume_line(screen, session_id)
    assert "\x1b[?1049l\x1b[?2004l\x1b[?25h" in screen.getvalue()
    await wait_until_gone([host_pid], timeout=15)


def test_leftover_cleanup_checks_each_process_before_signaling(tmp_path: Path) -> None:
    run_directory = tmp_path / "run"
    run_directory.mkdir()
    lines: list[str] = []
    leftover_host = subprocess.Popen(["sleep", "600"], start_new_session=True)
    leftover_tool = subprocess.Popen(["sleep", "601"], start_new_session=True)
    reused = subprocess.Popen(["sleep", "602"], start_new_session=True)
    not_a_leader = subprocess.Popen(["sleep", "603"])
    try:
        host = read_process_identity(leftover_host.pid)
        tool = read_process_identity(leftover_tool.pid)
        reused_identity = read_process_identity(reused.pid)
        member = read_process_identity(not_a_leader.pid)
        assert host and tool and reused_identity and member
        gone_launcher = {
            "pid": 999_999,
            "pgid": 999_999,
            "started": "never",
            "command_sha256": "0" * 64,
        }
        write_record(run_directory / "a.json", gone_launcher, asdict(host), [asdict(tool)])
        forged = {**asdict(reused_identity), "started": "Thu Jan  1 00:00:00 1970"}
        write_record(run_directory / "b.json", gone_launcher, forged, [asdict(member)])
        alive_launcher = read_process_identity(os.getpid())
        assert alive_launcher
        write_record(run_directory / "c.json", asdict(alive_launcher), asdict(reused_identity), [])
        stop_leftover_processes(run_directory, lines.append, grace_seconds=0.5)
        assert leftover_host.wait(timeout=5) == -signal.SIGTERM
        assert leftover_tool.wait(timeout=5) == -signal.SIGTERM
        assert reused.poll() is None
        assert not_a_leader.poll() is None
        assert lines == [
            f"Stopped a leftover worker host (pid {leftover_host.pid}) and 1 tool process group."
        ]
        assert sorted(path.name for path in run_directory.iterdir()) == ["c.json"]
    finally:
        for process in (leftover_host, leftover_tool, reused, not_a_leader):
            process.kill()
            process.wait()


def write_record(
    path: Path,
    launcher: Mapping[str, Any],
    worker_host: Mapping[str, Any],
    tools: Sequence[Mapping[str, Any]],
) -> None:
    path.write_text(
        json.dumps(
            {"launcher": launcher, "worker_host": worker_host, "tool_process_groups": list(tools)}
        )
    )


#: Every conformance transcript a real launcher replays; sessions and connectors modes come later.
LAUNCHER_TRANSCRIPTS = sorted(
    path.stem
    for path in TRANSCRIPTS_DIR.glob("*.json")
    if "launcher" in json.loads(path.read_text())["real_roles"]
)
LAUNCHER_MODES_NOT_YET_SERVED = {"connectors_refresh", "sessions_resume_result"}


@pytest.mark.parametrize("name", LAUNCHER_TRANSCRIPTS)
async def test_the_launcher_replays_each_conformance_transcript(
    cli_home: CliHome,
    tmp_path: Path,
    screen: io.StringIO,
    fake_mcp: FakeConnectorsMcp,
    name: str,
) -> None:
    if name in LAUNCHER_MODES_NOT_YET_SERVED:
        pytest.skip("the sessions and connectors modes are not served yet")
    transcript = load_transcript(name)
    pictures = tmp_path / "pictures"
    pictures.mkdir()
    (pictures / "screenshot.png").write_bytes(png_bytes())
    (tmp_path / "notes.txt").write_text("not an image")
    aliases = {
        "/home/user/pictures/screenshot.png": str(pictures / "screenshot.png"),
        "/home/user/notes.txt": str(tmp_path / "notes.txt"),
        "http://127.0.0.1:8803/mcp": fake_mcp.url,
    }
    fake = FakeTerminalUi(tmp_path / "tui", transcript, aliases=aliases)
    config = {"connectors": {"mcp": fake_mcp.url}}
    browser = "1" if name == "chat_open_url" else "0"
    child = start_chat(
        cli_home, fake, screen, config, environment={"DEX_AGENTS_OPEN_BROWSER": browser}
    )
    session_id = await read_session_id(child)
    if name == "chat_worker_stopped":
        await fake.wait_for("sent", type="ready")
        os.kill(worker_host_pid(cli_home.home / ".dex" / "agents"), signal.SIGKILL)
    if name == "ready_timeout":
        await expect(child, "Plain chat: the terminal UI was not ready in 5 s.")
        await expect(child, "> ")
        child.sendeof()
        assert await wait_for_exit(child) == 0
    else:
        assert await wait_for_exit(child) == (1 if name == "chat_worker_stopped" else 0)
        assert_no_launcher_resume_line(screen, session_id)
    assert_replayed(fake, screen)


async def test_a_protocol_error_after_ready_fails_the_chat_even_when_dexcli_exits_0(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO
) -> None:
    steps = [{"from": "tui", "raw_line": "this is not JSON"}, {"close": "launcher"}]
    fake = FakeTerminalUi(tmp_path / "tui", chat_transcript(steps))
    child = start_chat(cli_home, fake, screen)
    session_id = await read_session_id(child)
    await expect(child, "The chat closed: dexcli sent a malformed line.")
    assert await wait_for_exit(child) == 1
    assert_no_launcher_resume_line(screen, session_id)
    assert fake.mismatches() == []


@pytest.mark.parametrize("is_group_broadcast", [False, True], ids=["launcher-only", "group"])
async def test_an_external_sigint_ends_the_chat_with_130(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO, is_group_broadcast: bool
) -> None:
    fake = FakeTerminalUi(tmp_path / "tui", chat_transcript([pause(300_000)]))
    child = start_chat(cli_home, fake, screen)
    session_id = await read_session_id(child)
    await fake.wait_for("sent", type="ready")
    host_pid = worker_host_pid(cli_home.home / ".dex" / "agents")
    if is_group_broadcast:
        os.killpg(child.pid, signal.SIGINT)
    else:
        os.kill(child.pid, signal.SIGINT)
    assert await wait_for_exit(child) == 130
    assert_no_launcher_resume_line(screen, session_id)
    assert "Plain chat" not in screen.getvalue()
    await wait_until_gone([host_pid], timeout=15)


async def test_a_worker_host_that_closes_its_channel_before_ready_means_the_plain_chat(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO
) -> None:
    fake = FakeTerminalUi(tmp_path / "tui")
    child = start_chat(
        cli_home,
        fake,
        screen,
        command_module="tests.channel_closing_cli",
        environment={CONTROL_CHANNEL_END_VARIABLE: "before-ready"},
    )
    await expect(
        child, "Plain chat: the Worker host closed its control channel before it was ready."
    )
    await read_session_id(child)
    await expect(child, "> ")
    child.sendeof()
    assert await wait_for_exit(child) == 0
    assert not fake.was_run()


async def test_a_worker_host_that_closes_its_channel_after_ready_answers_host_stopped(
    cli_home: CliHome, tmp_path: Path, screen: io.StringIO
) -> None:
    image = tmp_path / "screenshot.png"
    image.write_bytes(png_bytes())
    steps = [
        {
            "from": "launcher",
            "message": {"type": "worker", "state": "stopped", "exit_code": 137},
            "ignore": ["exit_code"],
        },
        request(1, "put_image", {"path": str(image)}, new_key()),
        error(1, "host_stopped", STOPPED_HOST_MESSAGE),
    ]
    fake = FakeTerminalUi(tmp_path / "tui", chat_transcript(steps))
    child = start_chat(
        cli_home,
        fake,
        screen,
        command_module="tests.channel_closing_cli",
        environment={CONTROL_CHANNEL_END_VARIABLE: "after-ready"},
    )
    session_id = await read_session_id(child)
    assert await wait_for_exit(child) == 1
    assert_no_launcher_resume_line(screen, session_id)
    assert_replayed(fake, screen)


async def test_a_shell_command_leaves_no_background_process_in_the_worker_host(
    tmp_path: Path,
) -> None:
    registry = ToolProcessGroupRegistry(tmp_path / "run" / "scripted-1.json", None, None)
    registered: list[int] = []
    register = registry.register

    def record_registration(pid: int) -> None:
        registered.append(pid)
        register(pid)

    registry.register = record_registration  # type: ignore[method-assign]
    output = await run_shell_command(
        "sleep 30 & echo started", tmp_path, 10, process_groups=registry
    )
    assert output.startswith("exit code 0\n--- stdout ---\nstarted")
    (pgid,) = registered
    await wait_until_gone([pgid], timeout=5)
    assert json.loads(registry.record_path.read_text())["tool_process_groups"] == []


def test_the_watchdog_never_signals_a_released_group(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    signaled_after_reap: list[int] = []
    reaped: set[int] = set()
    real_signal_group = process_groups_module._signal_group

    def checked_signal_group(pgid: int, signal_number: int) -> bool:
        if pgid in reaped:
            signaled_after_reap.append(pgid)
        return real_signal_group(pgid, signal_number)

    monkeypatch.setattr(process_groups_module, "_signal_group", checked_signal_group)
    for _ in range(20):
        registry = ToolProcessGroupRegistry(tmp_path / "run.json", None, None)
        process = subprocess.Popen(["sleep", "30"], start_new_session=True)
        registry.register(process.pid)
        stopper = threading.Thread(target=registry.stop_all, args=(0.05,))
        stopper.start()

        def reap(pid: int = process.pid) -> None:
            os.kill(pid, signal.SIGKILL)
            os.waitpid(pid, 0)
            reaped.add(pid)

        registry.release(process.pid, reap)
        process.returncode = -signal.SIGKILL
        stopper.join()
        registry.stop_all(0.01)
    assert signaled_after_reap == []


def test_the_package_imports_without_posix_terminal_modules() -> None:
    script = (
        "import sys\n"
        "sys.modules['termios'] = None\n"
        "import dex_agents_cli_core, dex_agents_cli_core.cli\n"
        "from dex_agents_cli_core.terminal_ui import "
        "child_process, launcher, process_groups, watchdog, worker_host\n"
        "print('imported')\n"
    )
    completed = subprocess.run(
        [sys.executable, "-c", script], capture_output=True, text=True, check=False
    )
    assert completed.stdout.strip() == "imported", completed.stderr


@pytest.mark.parametrize("signal_number", [signal.SIGTERM, signal.SIGHUP], ids=["term", "hup"])
@pytest.mark.parametrize("is_fallback", [False, True], ids=["finished", "fallback"])
async def test_the_command_handles_sigterm_and_sighup_again_after_the_handoff(
    cli_home: CliHome,
    tmp_path: Path,
    screen: io.StringIO,
    signal_number: int,
    is_fallback: bool,
) -> None:
    markers = tmp_path / "markers"
    markers.mkdir()
    launch_step = chat_transcript([])["steps"][0]
    rejected = {
        "from": "tui",
        "message": {
            "type": "launch_rejected",
            "code": "auth_missing",
            "message": "DEX_API_KEY is not set; dex.auth mode api_key needs it.",
        },
    }
    transcript = (
        {**chat_transcript([]), "steps": [launch_step, rejected], "tui_exit": 2}
        if is_fallback
        else chat_transcript([])
    )
    fake = FakeTerminalUi(tmp_path / "tui", transcript)
    child = start_chat(
        cli_home,
        fake,
        screen,
        command_module="tests.slow_worker_host_exit_cli",
        environment={MARKER_DIRECTORY_VARIABLE: str(markers)},
    )
    await read_session_id(child)
    host_pid = worker_host_pid(cli_home.home / ".dex" / "agents")
    # The handoff ended, and the launcher now waits for a Worker host that takes 30 s to stop.
    await wait_for_file(markers / WORKER_HOST_CLOSING_MARKER)
    os.kill(child.pid, signal_number)
    assert await wait_for_exit(child) == 128 + signal_number
    assert (markers / DEXCLI_DEV_STOPPED_MARKER).exists()
    await wait_until_gone([host_pid], timeout=10)


async def wait_for_file(path: Path, timeout: float = 60.0) -> None:
    loop = asyncio.get_running_loop()
    deadline = loop.time() + timeout
    while not path.exists():
        if loop.time() > deadline:
            raise AssertionError(f"{path} never appeared")
        await asyncio.sleep(0.05)


def test_launch_ignore_matches_the_conformance_launch_steps() -> None:
    launch_step = chat_transcript([])["steps"][0]
    assert launch_step["ignore"] == LAUNCH_IGNORE
