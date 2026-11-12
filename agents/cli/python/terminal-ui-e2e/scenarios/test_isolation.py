# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The terminal UI owns the terminal: no output leaks onto it, and a lost Worker host is shown."""

from __future__ import annotations

import os
import signal
import subprocess
import time

from dex_agents_cli_terminal_ui_e2e.chat_run import ChatRun, ChatRunFactory, session_id_on_screen
from dex_agents_cli_terminal_ui_e2e.command_targets import TerminalUiCommand
from dex_agents_cli_terminal_ui_e2e.output_canary import CANARY_TEXT
from dex_agents_cli_terminal_ui_e2e.terminal_session import TerminalSession

READY_STATUS = "Enter send"
CANARY_ROLES = ("launcher", "worker-host")
# The stdout of dexcli dev, which a command that starts it must keep off the terminal.
DEXCLI_DEV_OUTPUT = ("Dex development environment is ready", "Press Ctrl+C to stop.")
# The tool's own output is the tool result, which the terminal UI may show; the
# controlling terminal and a child that outlives the tool are not.
CANARY_SCRIPT = (
    "echo canary script ran\n"
    f"(echo {CANARY_TEXT} shell tty > /dev/tty) 2>/dev/null\n"
    f"(sleep 1; echo {CANARY_TEXT} shell background fd 2 >&2) &\n"
    "exit 0\n"
)


def test_no_output_reaches_the_terminal(
    command: TerminalUiCommand, chat_runs: ChatRunFactory
) -> None:
    tools = command.case.tools
    run = chat_runs(
        [
            {
                "prompt": "run the canary script",
                "steps": [
                    {
                        "tool_calls": [
                            {"name": tools.shell_tool, "arguments": {"command": "sh canary.sh"}}
                        ]
                    },
                    # No {tool_results}: the reply must not carry the canary text itself.
                    {"text": "SHELL-CANARY-DONE"},
                ],
            }
        ],
        own_dev_server=True,
    )
    (run.workspace / "canary.sh").write_text(CANARY_SCRIPT, encoding="utf-8")
    run.write_config(shell_allow=("sh canary.sh",))
    session = run.spawn(has_canary=True)
    session.wait_for_display(READY_STATUS, timeout_seconds=180)
    (run.canary_dir / "trigger").write_text("", encoding="utf-8")
    session.wait_until(lambda _: _canary_roles_written(run) == set(CANARY_ROLES), "canaries")

    session.submit("run the canary script")
    session.wait_until(
        lambda screen: any(
            "SHELL-CANARY-DONE" in line or f"Approve {tools.shell_tool}?" in line
            for line in screen.display()
        ),
        "the shell reply or an approval",
        timeout_seconds=90,
    )
    if any(f"Approve {tools.shell_tool}?" in line for line in session.screen.display()):
        session.type_text("y")
        session.wait_for_text("SHELL-CANARY-DONE", timeout_seconds=90)
    session.pump(2.0)
    _assert_nothing_leaked(session)
    session.press("ctrl_d")
    assert session.wait_for_exit(timeout_seconds=90) == 0, session.describe()
    _assert_nothing_leaked(session)
    log_text = run.command_log_text()
    for role in CANARY_ROLES:
        for fd in (1, 2):
            assert f"{CANARY_TEXT} {role} fd {fd}" in log_text, f"{role} fd {fd} left no log line"
            assert f"{CANARY_TEXT} {role} child fd {fd}" in log_text


def test_killed_worker_host_is_shown(command: TerminalUiCommand, chat_runs: ChatRunFactory) -> None:
    run = chat_runs([])
    session = run.spawn()
    session.wait_for_display(READY_STATUS, timeout_seconds=90)
    session_id = session_id_on_screen(session)
    worker_host_pid = _worker_host_pid(session)
    os.kill(worker_host_pid, signal.SIGKILL)
    session.wait_for_text("The Worker stopped (exit", timeout_seconds=60)
    session.wait_for_text(session_id, timeout_seconds=10)
    session.type_text("x")
    exit_code = session.wait_for_exit(timeout_seconds=60)
    assert exit_code == 1, session.describe()
    output = session.screen.output_text()
    assert f"--resume {session_id}" in output, output[-800:]


def _assert_nothing_leaked(session: TerminalSession) -> None:
    output = session.screen.output_text()
    leaked = [line for line in output.splitlines() if CANARY_TEXT in line]
    leaked += [text for text in DEXCLI_DEV_OUTPUT if text in output]
    assert not leaked, f"reached the terminal: {leaked}\n{session.describe()}"


def _canary_roles_written(run: ChatRun) -> set[str]:
    return {path.read_text(encoding="utf-8") for path in run.canary_dir.glob("*.written")}


def _worker_host_pid(session: TerminalSession) -> int:
    """Return the launcher's child that runs the Worker host, found by its parent PID."""
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        listing = subprocess.run(
            ["ps", "-A", "-o", "pid=,ppid=,command="],
            capture_output=True,
            text=True,
            check=True,
        ).stdout
        for line in listing.splitlines():
            parts = line.split(None, 2)
            if len(parts) == 3 and int(parts[1]) == session.pid and "__worker-host" in parts[2]:
                return int(parts[0])
        session.pump(0.5)
    raise AssertionError(f"no Worker host child of {session.pid}")
