# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The ``dex-agents-builtin`` command: a Dex built-in loop agent in the terminal.

It builds the agent session Flow with the Dex built-in agent loop on LiteLLM and
runs it with the shared Dex Agents CLI runtime. See the package README.
"""

import dex_agents_cli  # noqa: F401  # first: it configures gRPC before anything imports it

# isort: split

import sys

from dex_agents_builtin_cli.harness import (
    BUILTIN_CLI_FLOW_TYPE,
    DEFAULT_BUILTIN_MODEL,
    DEFAULT_INSTRUCTIONS,
    BuiltinHarness,
    validate_builtin_section,
)
from dex_agents_cli import ConfigError, command_name, run_agent_cli

__all__ = [
    "BUILTIN_CLI_FLOW_TYPE",
    "DEFAULT_BUILTIN_MODEL",
    "DEFAULT_INSTRUCTIONS",
    "BuiltinHarness",
    "main",
    "run",
    "validate_builtin_section",
]


def main(argv: list[str] | None = None) -> int:
    """Run the command and return its exit code.

    Args:
        argv: The arguments without the program name; ``None`` reads ``sys.argv``.

    Returns:
        0 on success, 1 for a failure at run time, and 2 for a usage or
        configuration error, such as a key in the ``builtin`` section.
    """
    harness = BuiltinHarness()
    try:
        return run_agent_cli(harness, argv)
    except ConfigError as error:
        print(f"{command_name(harness)}: {error}", file=sys.stderr)
        return 2


def run() -> None:
    """Run the command from its console script and exit with its code."""
    raise SystemExit(main())
