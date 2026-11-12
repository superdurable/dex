# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Sessions in the terminal UI: --resume with its history, /history, and the /sessions picker."""

from __future__ import annotations

from dex_agents_cli_terminal_ui_e2e.chat_run import (
    ChatRun,
    ChatRunFactory,
    resume_id_after_exit,
    session_id_on_screen,
)
from dex_agents_cli_terminal_ui_e2e.command_targets import TerminalUiCommand
from dex_agents_cli_terminal_ui_e2e.terminal_session import TerminalSession

READY_STATUS = "Enter send"
# A turn with more messages than the history page of 50, so the turn before it is earlier.
READ_CALL_COUNT = 60


def start_chat(run: ChatRun, *arguments: str) -> TerminalSession:
    """Start the command and wait for the ready input."""
    session = run.spawn(*arguments)
    session.wait_for_display(READY_STATUS, timeout_seconds=90)
    return session


def run_turn(session: TerminalSession, prompt: str, marker: str) -> None:
    """Send one prompt and wait until its reply marker shows and the input is ready again."""
    session.submit(prompt)
    session.wait_for_text(marker, timeout_seconds=120)
    session.wait_for_display(READY_STATUS, timeout_seconds=60)


def exit_chat(session: TerminalSession) -> str | None:
    """Exit with Ctrl-D; return the session ID of the resume line."""
    session.press("ctrl_d")
    assert session.wait_for_exit() == 0, session.describe()
    return resume_id_after_exit(session)


def test_resume_shows_history_and_history_pager(
    command: TerminalUiCommand, chat_runs: ChatRunFactory
) -> None:
    tools = command.case.tools
    run = chat_runs([])
    read_call = {
        "name": tools.read_tool,
        "arguments": tools.read_arguments(run.workspace.resolve(), "notes.txt"),
    }
    run.write_turns(
        [
            {"prompt": "resume first turn", "steps": [{"text": "FIRST-DONE"}]},
            {
                "prompt": "resume second turn",
                "steps": [{"tool_calls": [read_call] * READ_CALL_COUNT}, {"text": "SECOND-DONE"}],
            },
            {
                "prompt": "resume third turn",
                "steps": [{"tool_calls": [read_call]}, {"text": "THIRD-DONE"}],
            },
        ]
    )
    first = start_chat(run)
    session_id = session_id_on_screen(first)
    run_turn(first, "resume first turn", "FIRST-DONE")
    run_turn(first, "resume second turn", "SECOND-DONE")
    run_turn(first, "resume third turn", "THIRD-DONE")
    assert exit_chat(first) == session_id

    resumed = start_chat(run, "--resume", session_id)
    resumed.wait_for_text("THIRD-DONE", timeout_seconds=60)
    # The transcript scrolls inside the terminal UI; page up to its top.
    for _ in range(15):
        if any("earlier messages: /history" in line for line in resumed.screen.display()):
            break
        resumed.press("page_up")
        resumed.pump(0.3)
    lines = resumed.screen.all_lines()
    assert any("session:" in line and session_id in line for line in lines), resumed.describe()
    assert any("resume third turn" in line for line in lines), resumed.describe()
    history_tool_rows = [line for line in lines if tools.read_tool in line and "·" in line]
    assert history_tool_rows, resumed.describe()
    assert not any("✓" in line for line in history_tool_rows), "history tool rows show a status"
    resumed.wait_for_display("earlier messages: /history", timeout_seconds=10)
    assert not any("FIRST-DONE" in line for line in resumed.screen.all_lines())

    resumed.submit("/history")
    resumed.wait_for_display("Earlier messages")
    # The pager opens at its end, next to the chat. g goes to its top; k there reads a page more.
    for _ in range(20):
        if any("FIRST-DONE" in line for line in resumed.screen.display()):
            break
        resumed.type_text("g")
        resumed.type_text("k")
        resumed.pump(0.5)
    resumed.wait_for_display("FIRST-DONE", timeout_seconds=10)
    assert any("resume first turn" in line for line in resumed.screen.display())
    assert any("resume second turn" in line for line in resumed.screen.display())
    resumed.type_text("q")
    resumed.wait_for_display_gone("Earlier messages")
    resumed.wait_for_display(READY_STATUS)
    assert exit_chat(resumed) == session_id


def test_sessions_picker_switches_session(
    command: TerminalUiCommand, chat_runs: ChatRunFactory
) -> None:
    run = chat_runs(
        [
            {"prompt": "alpha session turn", "steps": [{"text": "ALPHA-DONE"}]},
            {"prompt": "beta session turn", "steps": [{"text": "BETA-DONE"}]},
        ]
    )
    alpha = start_chat(run)
    alpha_id = session_id_on_screen(alpha)
    run_turn(alpha, "alpha session turn", "ALPHA-DONE")
    exit_chat(alpha)

    beta = start_chat(run)
    beta_id = session_id_on_screen(beta)
    assert beta_id != alpha_id
    run_turn(beta, "beta session turn", "BETA-DONE")
    beta.submit("/sessions")
    beta.wait_for_display(alpha_id, timeout_seconds=60)
    for _ in range(40):
        selected = [line for line in beta.screen.display() if "▸" in line]
        if any(alpha_id in line for line in selected):
            break
        beta.press("down")
        beta.pump(0.2)
    else:
        raise AssertionError(f"could not select {alpha_id}\n{beta.describe()}")
    beta.press("enter")
    beta.wait_for_text(f"── session {alpha_id} ──", timeout_seconds=60)
    beta.wait_for_text("ALPHA-DONE", timeout_seconds=60)
    lines = beta.screen.all_lines()
    separator = max(i for i, line in enumerate(lines) if f"── session {alpha_id} ──" in line)
    assert any("ALPHA-DONE" in line for line in lines[separator:]), beta.describe()
    beta.wait_for_display(READY_STATUS)
    assert exit_chat(beta) == alpha_id
