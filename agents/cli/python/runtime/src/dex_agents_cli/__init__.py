# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The shared runtime of the Dex Agents CLI commands.

A harness package implements ``AgentHarness`` and calls ``main`` with it.
Import this package before any module that imports gRPC, such as ``dex``.
"""

import os

# gRPC's fork handlers print into every shell command the local tools fork; the CLI never
# forks a gRPC user. The variable only takes effect before gRPC is first imported.
os.environ.setdefault("GRPC_ENABLE_FORK_SUPPORT", "false")

# isort: split

from dex_agents_cli.cli import main
from dex_agents_cli.config import (
    AUTO,
    ConfigError,
    ConnectorEntry,
    EffectiveConfig,
    TurnConfigResolution,
    canonical_operation_name,
    is_shell_command_allowed,
    parse_config_yaml,
    resolve_config,
    resolve_turn_config,
)
from dex_agents_cli.connectors import CONNECTORS_TOOL_SOURCE_NAME, ConnectorsMcpToolSource
from dex_agents_cli.harness import AgentHarness, HarnessBuildContext, LocalToolGroup
from dex_agents_cli.local_tools import LOCAL_TOOL_SOURCE_NAME, LocalToolSource
from dex_agents_cli.turns import (
    SESSION_SETTINGS_REJECTED_PREFIX,
    SessionSettingsRejectedError,
    TurnConfigResolver,
    current_turn_config,
    with_turn_config,
)

__all__ = [
    "AUTO",
    "CONNECTORS_TOOL_SOURCE_NAME",
    "LOCAL_TOOL_SOURCE_NAME",
    "SESSION_SETTINGS_REJECTED_PREFIX",
    "AgentHarness",
    "ConfigError",
    "ConnectorEntry",
    "ConnectorsMcpToolSource",
    "EffectiveConfig",
    "HarnessBuildContext",
    "LocalToolGroup",
    "LocalToolSource",
    "SessionSettingsRejectedError",
    "TurnConfigResolution",
    "TurnConfigResolver",
    "canonical_operation_name",
    "current_turn_config",
    "is_shell_command_allowed",
    "main",
    "parse_config_yaml",
    "resolve_config",
    "resolve_turn_config",
    "with_turn_config",
]
