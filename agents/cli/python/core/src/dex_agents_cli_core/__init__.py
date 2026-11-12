# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The shared CLI core of the Dex Agents CLI commands.

A harness package implements ``AgentHarness`` and calls ``run_agent_cli`` with it.
Import this package before any module that imports gRPC, such as ``dex``.
"""

import os

# gRPC's fork handlers print into every shell command the local tools fork; the CLI never
# forks a gRPC user. The variable only takes effect before gRPC is first imported.
os.environ.setdefault("GRPC_ENABLE_FORK_SUPPORT", "false")

# isort: split

from dex_agents_cli_core.cli import command_name, run_agent_cli
from dex_agents_cli_core.config import (
    AUTO,
    ConfigError,
    ConnectorEntry,
    EffectiveConfig,
    MediaConfig,
    SessionsConfig,
    TurnConfigResolution,
    canonical_operation_name,
    is_session_summary_indexed,
    is_shell_command_allowed,
    parse_config_yaml,
    resolve_config,
    resolve_turn_config,
)
from dex_agents_cli_core.connectors import (
    CONNECTOR_TOOL_META_KEYS,
    CONNECTORS_TOOL_SOURCE_NAME,
    ConnectorsMcpToolSource,
    ConnectorToolMetaKeys,
)
from dex_agents_cli_core.harness import (
    SESSION_ACTION_PERMISSION,
    AgentHarness,
    HarnessContext,
    LocalToolGroup,
    SessionEnvironmentIdentity,
    current_session_environment_identity,
    namespace_session_flow_type,
    session_flow_type,
)
from dex_agents_cli_core.local_tools import LOCAL_TOOL_SOURCE_NAME, LocalToolSource
from dex_agents_cli_core.tool_policy import (
    is_local_tool_group_enabled,
    is_shell_command_approval_required,
    resolve_turn_tool_policy,
)
from dex_agents_cli_core.turns import (
    ResolvedTurn,
    TurnConfigResolver,
    current_turn_config,
    with_turn_config,
)

__all__ = [
    "AUTO",
    "CONNECTORS_TOOL_SOURCE_NAME",
    "CONNECTOR_TOOL_META_KEYS",
    "LOCAL_TOOL_SOURCE_NAME",
    "SESSION_ACTION_PERMISSION",
    "AgentHarness",
    "ConfigError",
    "ConnectorEntry",
    "ConnectorToolMetaKeys",
    "ConnectorsMcpToolSource",
    "EffectiveConfig",
    "HarnessContext",
    "LocalToolGroup",
    "LocalToolSource",
    "MediaConfig",
    "ResolvedTurn",
    "SessionEnvironmentIdentity",
    "SessionsConfig",
    "TurnConfigResolution",
    "TurnConfigResolver",
    "canonical_operation_name",
    "command_name",
    "current_session_environment_identity",
    "current_turn_config",
    "is_local_tool_group_enabled",
    "is_session_summary_indexed",
    "is_shell_command_allowed",
    "is_shell_command_approval_required",
    "namespace_session_flow_type",
    "parse_config_yaml",
    "resolve_config",
    "resolve_turn_config",
    "resolve_turn_tool_policy",
    "run_agent_cli",
    "session_flow_type",
    "with_turn_config",
]
