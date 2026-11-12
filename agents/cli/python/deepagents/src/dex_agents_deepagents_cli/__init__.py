# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The ``dex-agents-deepagents`` command: a Deep Agents agent in the terminal.

It builds the agent session Flow from ``create_deep_agent`` with the Dex
LangGraph adapter and runs it with the shared Dex Agents CLI core. See the
package README.
"""

import dex_agents_cli_core  # noqa: F401  # first: it configures gRPC before anything imports it

# isort: split

import sys

from dex_agents_cli_core import ConfigError, command_name, run_agent_cli
from dex_agents_deepagents_cli.connector_tool_names import (
    COLLIDING_CONNECTOR_TOOL_PREFIX,
    PrefixedConnectorToolSource,
)
from dex_agents_deepagents_cli.harness import (
    DEEP_AGENTS_TOOL_RULES,
    DEEPAGENTS_CLI_FLOW_TYPE,
    DEFAULT_DEEPAGENTS_MODEL,
    DEFAULT_INSTRUCTIONS,
    EXECUTE_TOOL_NAME,
    TASK_TOOL_NAME,
    WRITE_TODOS_TOOL_NAME,
    DeepAgentsAgentRunner,
    DeepAgentsHarness,
    DeepAgentsToolRule,
    LocalToolSwitchesMiddleware,
    TurnChatModelSelector,
    TurnInstructionsMiddleware,
    build_deep_agent,
    deep_agents_chat_model,
    deep_agents_tool_sources,
    validate_deepagents_section,
)
from dex_agents_deepagents_cli.workspace_backend import (
    DEEP_AGENTS_ARTIFACT_PREFIXES,
    SESSION_ARTIFACTS_RETENTION,
    SessionArtifactsBackend,
    WorkspaceShellBackend,
    bind_artifacts_session,
    build_workspace_backend,
    default_artifacts_directory,
    ensure_artifacts_directory,
    remove_stale_session_artifacts,
)

__all__ = [
    "COLLIDING_CONNECTOR_TOOL_PREFIX",
    "DEEPAGENTS_CLI_FLOW_TYPE",
    "DEEP_AGENTS_ARTIFACT_PREFIXES",
    "DEEP_AGENTS_TOOL_RULES",
    "DEFAULT_DEEPAGENTS_MODEL",
    "DEFAULT_INSTRUCTIONS",
    "EXECUTE_TOOL_NAME",
    "SESSION_ARTIFACTS_RETENTION",
    "TASK_TOOL_NAME",
    "WRITE_TODOS_TOOL_NAME",
    "DeepAgentsAgentRunner",
    "DeepAgentsHarness",
    "DeepAgentsToolRule",
    "LocalToolSwitchesMiddleware",
    "PrefixedConnectorToolSource",
    "SessionArtifactsBackend",
    "TurnChatModelSelector",
    "TurnInstructionsMiddleware",
    "WorkspaceShellBackend",
    "bind_artifacts_session",
    "build_deep_agent",
    "build_workspace_backend",
    "deep_agents_chat_model",
    "deep_agents_tool_sources",
    "default_artifacts_directory",
    "ensure_artifacts_directory",
    "main",
    "remove_stale_session_artifacts",
    "run",
    "validate_deepagents_section",
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
