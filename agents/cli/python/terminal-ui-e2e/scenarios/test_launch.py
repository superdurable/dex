# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Launch and exit: the first frame, the plain fallback, and the dexcli dev a command starts."""

from __future__ import annotations

import contextlib
import os
import re
import signal
import socket
import time
from pathlib import Path

import pytest

from dex_agents_cli_terminal_ui_e2e.chat_run import (
    ChatRunFactory,
    resume_id_after_exit,
    session_id_on_screen,
)
from dex_agents_cli_terminal_ui_e2e.command_targets import TerminalUiCommand
from dex_agents_cli_terminal_ui_e2e.terminal_screen import TerminalScreen

READY_STATUS = "Enter send"
INPUT_MARK = "\u203a"
_DEV_READY_NOTICE = re.compile(r"dexcli dev is ready \(pid (\d+)\)")
_SGR = re.compile(rb"\x1b\[([0-9;:]*)m")


@pytest.mark.parametrize(
    ("columns", "is_no_color"),
    [(80, False), (50, False), (80, True)],
    ids=["80-columns", "50-columns", "no-color"],
)
def test_first_frame_shows_logo_and_context(
    command: TerminalUiCommand, chat_runs: ChatRunFactory, columns: int, is_no_color: bool
) -> None:
    run = chat_runs([])
    session = run.spawn(
        columns=columns, extra_environment={"NO_COLOR": "1"} if is_no_color else None
    )
    session.wait_for_display(READY_STATUS, timeout_seconds=90)
    lines = session.screen.all_lines()
    assert any("Super Durable" in line for line in lines), session.describe()
    assert any("command:" in line and command.case.package_dir_name in line for line in lines)
    # At 50 columns a long session ID truncates the "(new)" mark.
    new_session_mark = "(new)" if columns >= 80 else ""
    assert any(
        "session:" in line and new_session_mark in line for line in lines
    ), session.describe()
    assert any(line.lstrip().startswith(INPUT_MARK) for line in session.screen.display())
    if is_no_color:
        assert not _color_parameters(session.screen), "NO_COLOR output has color"
    else:
        assert _color_parameters(session.screen), "the terminal UI drew no color"
    session_id = session_id_on_screen(session)
    session.press("ctrl_d")
    assert session.wait_for_exit() == 0, session.describe()
    assert resume_id_after_exit(session) == session_id


def test_started_dexcli_dev_stops_on_exit(
    command: TerminalUiCommand, chat_runs: ChatRunFactory
) -> None:
    run = chat_runs([], own_dev_server=True)
    session = run.spawn()
    dev_pid: int | None = None
    try:
        session.wait_for_display(READY_STATUS, timeout_seconds=180)
        session.wait_for_text("dexcli dev is ready", timeout_seconds=30)
        notice = " ".join(session.screen.all_lines())
        match = _DEV_READY_NOTICE.search(notice)
        assert match is not None, session.describe()
        dev_pid = int(match.group(1))
        dex_port = int(run.environment(run.binaries.dexcli, False)["DEX_AGENTS_DEX_PORT"])
        assert _is_listening(dex_port)
        session.press("ctrl_d")
        assert session.wait_for_exit(timeout_seconds=90) == 0, session.describe()
        output = session.screen.output_text()
        assert "Stopped the dexcli dev this command started." in output
        assert not _is_process_running(dev_pid), f"dexcli dev {dev_pid} still runs"
        assert not _is_listening(dex_port), f"port {dex_port} still answers"
        dev_pid = None
    finally:
        if dev_pid is not None and _is_process_running(dev_pid):
            os.kill(dev_pid, signal.SIGTERM)


def test_plain_chat_without_agents_tui(
    command: TerminalUiCommand, chat_runs: ChatRunFactory, stub_dexcli_without_terminal_ui: Path
) -> None:
    run = chat_runs([{"prompt": "plain fallback turn", "steps": [{"text": "PLAIN-FALLBACK-DONE"}]}])
    session = run.spawn(dexcli=stub_dexcli_without_terminal_ui)
    session.wait_for_output("Plain chat:", timeout_seconds=90)
    session.wait_for_output("\n> ", timeout_seconds=60)
    plain_line = next(
        line for line in session.screen.output_text().splitlines() if "Plain chat:" in line
    )
    # TERMINAL_UI_CONTRACT.md: the describe_failed line names the dexcli version it needs.
    assert "1.46.0" in plain_line, plain_line
    session.submit("plain fallback turn")
    session.wait_for_output("PLAIN-FALLBACK-DONE", timeout_seconds=90)
    output = session.screen.output_text()
    assert "Super Durable" not in output, "the terminal UI ran with a dexcli that lacks it"
    session.submit("/exit")
    assert session.wait_for_exit() == 0, session.describe()


def _color_parameters(screen: TerminalScreen) -> list[str]:
    colors = []
    for match in _SGR.finditer(bytes(screen.raw)):
        for parameter in re.split(rb"[;:]", match.group(1)):
            if parameter.isdigit() and (30 <= int(parameter) <= 49 or 90 <= int(parameter) <= 107):
                colors.append(parameter.decode())
    return colors


def _is_process_running(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    # An exited child of another process can linger as a zombie for a moment.
    time.sleep(0.5)
    with contextlib.suppress(ProcessLookupError):
        os.kill(pid, 0)
        return True
    return False


def _is_listening(port: int) -> bool:
    with contextlib.suppress(OSError), socket.create_connection(("127.0.0.1", port), 2):
        return True
    return False
