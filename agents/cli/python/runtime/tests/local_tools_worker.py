# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Worker child for the local tool tests: the scripted harness with the runtime's local tools.

It builds the Flow exactly as a command does, from the configuration in
``$DEX_AGENTS_TEST_CONFIG`` (JSON) and the workspace in ``$DEX_AGENTS_TEST_WORKSPACE``.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any

import dex_agents_cli  # noqa: F401  Imported first, before gRPC, as a command does.

# isort: split

from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents.testing import run_agent_worker
from dex_agents_cli import (
    HarnessContext,
    LocalToolGroup,
    LocalToolSource,
    TurnConfigResolver,
    resolve_config,
    with_turn_config,
)
from tests.scripted_harness import DEFAULT_SCRIPTED_MODEL, ScriptedHarness

CONFIG_ENVIRONMENT_VARIABLE = "DEX_AGENTS_TEST_CONFIG"
WORKSPACE_ENVIRONMENT_VARIABLE = "DEX_AGENTS_TEST_WORKSPACE"


#: Session policies that try to lower one local tool and raise another; only raising holds.
TOOL_POLICIES = {
    "edit_file": ToolPolicy(ToolEffect.READ),
    "search_files": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
}


def build_local_tools_flow(workspace: Path, values: dict[str, Any]) -> AgentSessionFlow:
    """Build the scripted harness's Flow with every local tool group and ``TOOL_POLICIES``."""
    harness = ScriptedHarness(TOOL_POLICIES, has_replacement_shell_tool=True)
    config = resolve_config(values, harness.config_section, DEFAULT_SCRIPTED_MODEL)
    resolver = TurnConfigResolver(config, None, lambda warnings: None)
    source = LocalToolSource(workspace, frozenset(LocalToolGroup), resolver)
    context = HarnessContext(
        flow_type=harness.flow_type,
        config=config,
        workspace=workspace,
        instructions=None,
        harness_options={},
        tool_sources=(source,),
        resolver=resolver,
    )
    return with_turn_config(harness.create_session_flow(context), resolver)


if __name__ == "__main__":
    run_agent_worker(
        [
            build_local_tools_flow(
                Path(os.environ[WORKSPACE_ENVIRONMENT_VARIABLE]),
                json.loads(os.environ[CONFIG_ENVIRONMENT_VARIABLE]),
            )
        ]
    )
