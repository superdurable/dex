# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The commands the terminal UI scenarios run, and what each one supports."""

from __future__ import annotations

from dataclasses import dataclass

from dex_agents_cli_testing.e2e.command_cases import (
    CLI_CORE_TOOLS,
    COMMAND_CASES,
    CommandCase,
    Provider,
)


@dataclass(frozen=True)
class TerminalUiCommand:
    """One command and its terminal UI features.

    Attributes:
        case: The command, its build, tools, and provider.
        has_ask_user: Whether its agent has the ``ask_user`` tool.
        has_slow_streams: Whether the mock model API can stream its replies
            slowly, which the scenarios that act during a turn need.
        has_write_diff: Whether its write approval shows a diff: the CLI
            core's file tools describe their effect, a harness's own do not.
    """

    case: CommandCase
    has_ask_user: bool
    has_slow_streams: bool
    has_write_diff: bool

    @property
    def name(self) -> str:
        """Return the case name, such as ``py-openai-sdk``."""
        return self.case.name


def _target(case: CommandCase) -> TerminalUiCommand:
    return TerminalUiCommand(
        case=case,
        has_ask_user=case.package_dir_name == "builtin",
        # Only the OpenAI Responses and Chat Completions stubs stream slowly.
        has_slow_streams=case.provider is Provider.OPENAI,
        has_write_diff=case.tools is CLI_CORE_TOOLS,
    )


#: Every command with a terminal UI. Pi keeps the plain chat, so it is not here.
TERMINAL_UI_COMMANDS: dict[str, TerminalUiCommand] = {
    case.name: _target(case) for case in COMMAND_CASES if case.package_dir_name != "pi"
}
