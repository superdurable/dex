# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The ``dex-agents-deepagents`` command: a Deep Agents agent in the terminal.

It builds the agent session Flow from ``create_deep_agent`` with the Dex
LangGraph adapter and runs it with the shared Dex Agents CLI runtime. See the
package README.
"""

import dex_agents_cli  # noqa: F401  # first: it configures gRPC before anything imports it

# isort: split

import sys

from dex_agents_cli import ConfigError, command_name, run_agent_cli
from dex_agents_deepagents_cli.harness import (
    DEEPAGENTS_CLI_FLOW_TYPE,
    DEFAULT_DEEPAGENTS_MODEL,
    DEFAULT_INSTRUCTIONS,
    WRITE_TODOS_TOOL_NAME,
    DeepAgentsHarness,
    TurnModelMiddleware,
    build_deep_agent,
    deep_agents_chat_model,
    validate_deepagents_section,
    write_todos_tool,
)

__all__ = [
    "DEEPAGENTS_CLI_FLOW_TYPE",
    "DEFAULT_DEEPAGENTS_MODEL",
    "DEFAULT_INSTRUCTIONS",
    "WRITE_TODOS_TOOL_NAME",
    "DeepAgentsHarness",
    "TurnModelMiddleware",
    "build_deep_agent",
    "deep_agents_chat_model",
    "main",
    "run",
    "validate_deepagents_section",
    "write_todos_tool",
]


def main(argv: list[str] | None = None) -> int:
    """Run the command and return its exit code.

    Args:
        argv: The arguments without the program name; ``None`` reads ``sys.argv``.

    Returns:
        0 on success, 1 for a failure at run time, and 2 for a usage or
        configuration error, such as a key in the ``deepagents`` section.
    """
    harness = DeepAgentsHarness()
    try:
        return run_agent_cli(harness, argv)
    except ConfigError as error:
        print(f"{command_name(harness)}: {error}", file=sys.stderr)
        return 2


def run() -> None:
    """Run the command from its console script and exit with its code."""
    raise SystemExit(main())
