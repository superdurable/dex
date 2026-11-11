# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The ``dex-agents-claude-sdk`` command: a Claude Agent SDK agent in the terminal.

It builds the agent session Flow with the Claude Agent SDK adapter and runs
it with the shared Dex Agents CLI runtime. See the package README.
"""

import dex_agents_cli  # noqa: F401  # first: it configures gRPC before anything imports it

# isort: split

import sys

from dex_agents_claude_sdk_cli.harness import (
    CLAUDE_CODE_LOCAL_TOOLS,
    CLAUDE_SDK_CLI_FLOW_TYPE,
    DEFAULT_CLAUDE_MODEL,
    DEFAULT_INSTRUCTIONS,
    ClaudeCodeLocalTools,
    ClaudeSdkHarness,
    validate_claude_section,
)
from dex_agents_cli import ConfigError, command_name, run_agent_cli

__all__ = [
    "CLAUDE_CODE_LOCAL_TOOLS",
    "CLAUDE_SDK_CLI_FLOW_TYPE",
    "DEFAULT_CLAUDE_MODEL",
    "DEFAULT_INSTRUCTIONS",
    "ClaudeCodeLocalTools",
    "ClaudeSdkHarness",
    "main",
    "run",
    "validate_claude_section",
]


def main(argv: list[str] | None = None) -> int:
    """Run the command and return its exit code.

    Args:
        argv: The arguments without the program name; ``None`` reads ``sys.argv``.

    Returns:
        0 on success, 1 for a failure at run time, and 2 for a usage or
        configuration error, such as a key in the ``claude`` section.
    """
    harness = ClaudeSdkHarness()
    try:
        return run_agent_cli(harness, argv)
    except ConfigError as error:
        print(f"{command_name(harness)}: {error}", file=sys.stderr)
        return 2


def run() -> None:
    """Run the command from its console script and exit with its code."""
    raise SystemExit(main())
