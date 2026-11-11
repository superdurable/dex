# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The ``dex-agents-google-adk`` command: a Google ADK agent in the terminal.

It builds the agent session Flow with the Google ADK adapter and
runs it with the shared Dex Agents CLI core. See the package README.
"""

import dex_agents_cli_core  # noqa: F401  # first: it configures gRPC before anything imports it

# isort: split

import sys

from dex_agents_cli_core import ConfigError, command_name, run_agent_cli
from dex_agents_google_adk_cli.harness import (
    DEFAULT_GOOGLE_ADK_MODEL,
    DEFAULT_INSTRUCTIONS,
    GOOGLE_ADK_CLI_FLOW_TYPE,
    GoogleAdkHarness,
    validate_google_adk_section,
)

__all__ = [
    "DEFAULT_GOOGLE_ADK_MODEL",
    "DEFAULT_INSTRUCTIONS",
    "GOOGLE_ADK_CLI_FLOW_TYPE",
    "GoogleAdkHarness",
    "main",
    "run",
    "validate_google_adk_section",
]


def main(argv: list[str] | None = None) -> int:
    """Run the command and return its exit code.

    Args:
        argv: The arguments without the program name; ``None`` reads ``sys.argv``.

    Returns:
        0 on success, 1 for a failure at run time, and 2 for a usage or
        configuration error, such as a key in the ``google_adk`` section.
    """
    harness = GoogleAdkHarness()
    try:
        return run_agent_cli(harness, argv)
    except ConfigError as error:
        print(f"{command_name(harness)}: {error}", file=sys.stderr)
        return 2


def run() -> None:
    """Run the command from its console script and exit with its code."""
    raise SystemExit(main())
