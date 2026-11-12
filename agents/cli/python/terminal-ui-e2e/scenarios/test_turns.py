# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Turns in the terminal UI: streaming, markdown, approvals, questions, queueing, cancel, resize."""

from __future__ import annotations

import pytest

from dex_agents_cli_terminal_ui_e2e.chat_run import (
    ChatRun,
    ChatRunFactory,
    resume_id_after_exit,
    session_id_on_screen,
)
from dex_agents_cli_terminal_ui_e2e.command_targets import TerminalUiCommand
from dex_agents_cli_terminal_ui_e2e.terminal_session import TerminalSession

READY_STATUS = "Enter send"
FILLER = " ".join(f"word{index:02d}" for index in range(40))
TABLE_REPLY = (
    f"STREAM-START {FILLER} STREAM-MIDDLE\n\n"
    "| name | qty |\n|---|---|\n| milk | 2 |\n| eggs | 12 |\n\nSTREAM-END"
)


def start_chat(run: ChatRun, columns: int = 80, lines: int = 24) -> TerminalSession:
    """Start the command and wait for the ready input."""
    session = run.spawn(columns=columns, lines=lines)
    session.wait_for_display(READY_STATUS, timeout_seconds=90)
    return session


def slow_text(marker: str, word_count: int = 40) -> str:
    """Return a reply that streams for a while: a start word, filler, and an end word."""
    filler = " ".join(f"filler{index:02d}" for index in range(word_count))
    return f"{marker}-START {filler} {marker}-END"


def latest_prompts(run: ChatRun) -> list[str]:
    """Return the latest user prompt of every model request, oldest first."""
    return [str(entry.get("prompt")) for entry in run.model_requests()]


@pytest.mark.needs_slow_streams
def test_streaming_reply_renders_markdown_table(
    command: TerminalUiCommand, chat_runs: ChatRunFactory
) -> None:
    run = chat_runs(
        [
            {
                "prompt": "stream a table",
                "steps": [{"text": TABLE_REPLY, "stream_delay_seconds": 0.1}],
            }
        ]
    )
    session = start_chat(run)
    session.submit("stream a table")
    session.wait_for_text("STREAM-START", timeout_seconds=60)
    assert not any(
        "STREAM-END" in line for line in session.screen.all_lines()
    ), "the whole reply arrived at once; nothing streamed"
    session.wait_for_display(READY_STATUS, timeout_seconds=60)
    # While it streams, the unfinished block shows raw markdown; the finished one is rendered.
    lines = session.screen.display()
    assert any("STREAM-END" in line for line in lines), session.describe()
    header = next((line for line in lines if "name" in line and "qty" in line), None)
    assert header is not None and "|" not in header, session.describe()
    assert any(line.strip().startswith("────") for line in lines), session.describe()
    assert any("eggs" in line and "12" in line and "|" not in line for line in lines)
    assert not any("|---|" in line for line in lines), "the table stayed raw markdown"


def test_write_approval_shows_diff_then_rejection_writes_nothing(
    command: TerminalUiCommand, chat_runs: ChatRunFactory
) -> None:
    tools = command.case.tools
    run = chat_runs([])
    workspace = run.workspace.resolve()
    run.write_turns(
        [
            {
                "prompt": "write the approved file",
                "steps": [
                    {
                        "tool_calls": [
                            {
                                "name": tools.write_tool,
                                "arguments": tools.write_arguments(
                                    workspace, "approved.txt", "hello\nworld\n"
                                ),
                            }
                        ]
                    },
                    {"text": "APPROVED-DONE"},
                ],
            },
            {
                "prompt": "write the rejected file",
                "steps": [
                    {
                        "tool_calls": [
                            {
                                "name": tools.write_tool,
                                "arguments": tools.write_arguments(
                                    workspace, "rejected.txt", "never written\n"
                                ),
                            }
                        ]
                    },
                    {"text": "REJECTED-DONE"},
                ],
            },
        ]
    )
    # Tall enough for the whole approval: some harnesses take absolute paths.
    session = start_chat(run, lines=40)
    session.submit("write the approved file")
    session.wait_for_display(f"Approve {tools.write_tool}?", timeout_seconds=90)
    display = session.screen.display()
    assert any("hello" in line for line in display), session.describe()
    if command.has_write_diff:
        assert any("+2 -0" in line for line in display), session.describe()
        assert any("+ hello" in line for line in display), session.describe()
        assert any("+ world" in line for line in display), session.describe()
    assert not (run.workspace / "approved.txt").exists(), "written before the approval"
    session.type_text("y")
    session.wait_for_text("APPROVED-DONE", timeout_seconds=90)
    assert (run.workspace / "approved.txt").read_text(encoding="utf-8") == "hello\nworld\n"
    session.wait_until(
        lambda screen: any(
            "\u2713" in line and tools.write_tool in line for line in screen.display()
        ),
        f"a finished {tools.write_tool} row",
        timeout_seconds=30,
    )
    session.wait_for_display(READY_STATUS)

    session.submit("write the rejected file")
    session.wait_for_display(f"Approve {tools.write_tool}?", timeout_seconds=90)
    session.type_text("n")
    session.wait_for_text("REJECTED-DONE", timeout_seconds=90)
    assert not (run.workspace / "rejected.txt").exists(), "a rejected write ran"
    session.wait_for_display(READY_STATUS)
    session.press("ctrl_d")
    assert session.wait_for_exit() == 0, session.describe()


@pytest.mark.needs_ask_user
def test_ask_user_question_and_answer(
    command: TerminalUiCommand, chat_runs: ChatRunFactory
) -> None:
    run = chat_runs(
        [
            {
                "prompt": "ask me a question",
                "steps": [
                    {
                        "tool_calls": [
                            {
                                "name": "ask_user",
                                "arguments": {
                                    "question": "Which color do you want?",
                                    "choices": ["red", "blue"],
                                    "allows_free_text": False,
                                },
                            }
                        ]
                    },
                    {"text": "ASKED-DONE {tool_results}"},
                ],
            }
        ]
    )
    session = start_chat(run)
    session.submit("ask me a question")
    session.wait_for_display("Which color do you want?", timeout_seconds=90)
    assert any("blue" in line for line in session.screen.display()), session.describe()
    session.press("down")
    session.press("enter")
    session.wait_for_text("ASKED-DONE", timeout_seconds=90)
    reply = next(line for line in session.screen.all_lines() if "ASKED-DONE" in line)
    assert "blue" in reply, session.describe()


@pytest.mark.needs_slow_streams
def test_queue_then_steer_into_the_running_turn(
    command: TerminalUiCommand, chat_runs: ChatRunFactory
) -> None:
    tools = command.case.tools
    run = chat_runs([])
    # The long reply ends in a write; the steer ends the turn before the write runs.
    run.write_turns(
        [
            {
                "prompt": "start a long turn",
                "steps": [
                    {
                        "text": slow_text("LONG", 40),
                        "stream_delay_seconds": 0.25,
                        "tool_calls": [
                            {
                                "name": tools.write_tool,
                                "arguments": tools.write_arguments(
                                    run.workspace.resolve(), "unsteered.txt", "not steered\n"
                                ),
                            }
                        ],
                    },
                    {"text": "LONG-TURN-CONTINUED"},
                ],
            },
            {"prompt": "steer this in", "steps": [{"text": "STEERED-DONE"}]},
        ]
    )
    session = start_chat(run)
    session.submit("start a long turn")
    session.wait_for_text("LONG-START", timeout_seconds=60)
    session.submit("steer this in")
    session.wait_until(
        lambda screen: any(
            "queued" in line and "steer this in" in line for line in screen.display()
        ),
        "queued message row",
    )
    session.press("tab")
    session.wait_for_display("s steer")
    session.type_text("s")
    # The write may ask for a moment before the steer, at that Step boundary, abandons it.
    session.wait_for_text("STEERED-DONE", timeout_seconds=90)
    session.wait_for_display(READY_STATUS, timeout_seconds=60)
    lines = session.screen.all_lines()
    assert not any(f"Approve {tools.write_tool}?" in line for line in session.screen.display())
    assert not (run.workspace / "unsteered.txt").exists()
    assert not any("LONG-TURN-CONTINUED" in line for line in lines), session.describe()
    assert "steer this in" in latest_prompts(run), latest_prompts(run)
    assert not any("queued" in line for line in session.screen.display()), session.describe()


@pytest.mark.needs_slow_streams
def test_ctrl_c_cancels_turn_then_clears_draft_then_exits(
    command: TerminalUiCommand, chat_runs: ChatRunFactory
) -> None:
    run = chat_runs(
        [
            {
                "prompt": "start a turn to cancel",
                "steps": [{"text": slow_text("CANCEL", 80), "stream_delay_seconds": 0.25}],
            }
        ]
    )
    session = start_chat(run)
    session_id = session_id_on_screen(session)
    session.submit("start a turn to cancel")
    session.wait_for_text("CANCEL-START", timeout_seconds=60)
    session.press("ctrl_c")
    session.wait_for_text("turn cancelled", timeout_seconds=60)
    session.wait_for_display(READY_STATUS, timeout_seconds=60)
    assert session.is_running(), "one Ctrl-C during a turn exited"
    assert not any("CANCEL-END" in line for line in session.screen.all_lines())

    session.type_text("an unsent draft")
    session.wait_for_display("an unsent draft")
    session.press("ctrl_c")
    session.wait_for_display_gone("an unsent draft")
    assert session.is_running(), "Ctrl-C on a draft exited instead of clearing it"
    session.press("ctrl_c")
    assert session.wait_for_exit() == 0, session.describe()
    output = session.screen.output_text()
    assert f"Session {session_id} keeps running." in output, output[-800:]
    assert resume_id_after_exit(session) == session_id


@pytest.mark.needs_slow_streams
def test_resize_while_streaming(command: TerminalUiCommand, chat_runs: ChatRunFactory) -> None:
    run = chat_runs(
        [
            {
                "prompt": "stream while I resize",
                "steps": [{"text": slow_text("RESIZE", 40), "stream_delay_seconds": 0.15}],
            }
        ]
    )
    session = start_chat(run, columns=100)
    session.submit("stream while I resize")
    session.wait_for_text("RESIZE-START", timeout_seconds=60)
    session.resize(50, 24)
    session.wait_for_display(READY_STATUS, timeout_seconds=60)
    assert any("RESIZE-END" in line for line in session.screen.all_lines()), session.describe()
    _assert_frame_width(session, 50)
    session.resize(100, 30)
    session.pump(1.0)
    _assert_frame_width(session, 100)
    assert any("Ctrl-D exit" in line for line in session.screen.display()), session.describe()


def _assert_frame_width(session: TerminalSession, columns: int) -> None:
    separator = "─" * columns
    assert any(line == separator for line in session.screen.display()), session.describe()
