# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Dex Web as a second client of a command's session: messages and approvals."""

from __future__ import annotations

from dex_agents_cli_terminal_ui_e2e.chat_run import ChatRunFactory, session_id_on_screen
from dex_agents_cli_terminal_ui_e2e.command_targets import TerminalUiCommand
from dex_agents_cli_terminal_ui_e2e.dex_web_client import (
    SESSION_ACTION_PERMISSION,
    DexWebSessionClient,
)
from dex_agents_cli_testing.e2e.dex_server import DexServer

READY_STATUS = "Enter send"
OTHER_CLIENT_LABEL = "(another client)"


def test_dex_web_message_is_answered_and_shown_as_another_client(
    command: TerminalUiCommand, chat_runs: ChatRunFactory, shared_dex_server: DexServer
) -> None:
    run = chat_runs(
        [
            {"prompt": "hello from the terminal", "steps": [{"text": "TERMINAL-REPLY"}]},
            {"prompt": "hello from dex web", "steps": [{"text": "DEX-WEB-REPLY"}]},
        ]
    )
    session = run.spawn(columns=100, lines=40)
    session.wait_for_display(READY_STATUS, timeout_seconds=90)
    session.submit("hello from the terminal")
    session.wait_for_text("TERMINAL-REPLY", timeout_seconds=90)
    web = DexWebSessionClient(shared_dex_server.web_port, session_id_on_screen(session))

    refused = web.send_message("hello from dex web", permissions=[])
    assert refused.status == 403, refused

    sent = web.send_message("hello from dex web", permissions=[SESSION_ACTION_PERMISSION])
    assert sent.status == 200, sent
    assert sent.body.get("output") is True, sent
    session.wait_for_text("DEX-WEB-REPLY", timeout_seconds=90)
    shown = next(line for line in session.screen.all_lines() if "hello from dex web" in line)
    assert OTHER_CLIENT_LABEL in shown, session.describe()
    assert [str(entry.get("prompt")) for entry in run.model_requests()][-1] == "hello from dex web"
    session.wait_for_display(READY_STATUS)
    session.press("ctrl_d")
    assert session.wait_for_exit() == 0, session.describe()


def test_dex_web_answers_an_approval_of_a_local_write(
    command: TerminalUiCommand, chat_runs: ChatRunFactory, shared_dex_server: DexServer
) -> None:
    """Records what Working as allows today: Dex Web approves a local write of the session."""
    tools = command.case.tools
    run = chat_runs([])
    workspace = run.workspace.resolve()
    run.write_turns(
        [
            {
                "prompt": "write the file",
                "steps": [
                    {
                        "tool_calls": [
                            {
                                "name": tools.write_tool,
                                "arguments": tools.write_arguments(
                                    workspace, "from-web.txt", "approved in dex web\n"
                                ),
                            }
                        ]
                    },
                    {"text": "WEB-APPROVED-DONE"},
                ],
            }
        ]
    )
    session = run.spawn(columns=100, lines=40)
    session.wait_for_display(READY_STATUS, timeout_seconds=90)
    session.submit("write the file")
    session.wait_for_display(f"Approve {tools.write_tool}?", timeout_seconds=90)
    web = DexWebSessionClient(shared_dex_server.web_port, session_id_on_screen(session))
    interrupt = web.wait_for_pending_interrupt()
    assert not (run.workspace / "from-web.txt").exists(), "written before the approval"

    refused = web.resolve_interrupt(str(interrupt["interrupt_id"]), True, permissions=[])
    assert refused.status == 403, refused

    approved = web.resolve_interrupt(
        str(interrupt["interrupt_id"]), True, permissions=[SESSION_ACTION_PERMISSION]
    )
    assert approved.status == 200, approved
    session.wait_for_text("WEB-APPROVED-DONE", timeout_seconds=90)
    assert (run.workspace / "from-web.txt").read_text(encoding="utf-8") == "approved in dex web\n"
    session.wait_for_display_gone(f"Approve {tools.write_tool}?", timeout_seconds=30)
    session.wait_for_display(READY_STATUS)
    session.press("ctrl_d")
    assert session.wait_for_exit() == 0, session.describe()
