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

from dex_agents import AgentSessionFlow
from dex_agents.testing import run_agent_worker
from dex_agents_cli import (
    HarnessBuildContext,
    LocalToolGroup,
    LocalToolSource,
    TurnConfigResolver,
    resolve_config,
    with_turn_config,
)
from tests.scripted_harness import DEFAULT_SCRIPTED_MODEL, ScriptedHarness

CONFIG_ENVIRONMENT_VARIABLE = "DEX_AGENTS_TEST_CONFIG"
WORKSPACE_ENVIRONMENT_VARIABLE = "DEX_AGENTS_TEST_WORKSPACE"


def build_local_tools_flow(workspace: Path, values: dict[str, Any]) -> AgentSessionFlow:
    """Build the scripted harness's Flow with every local tool group."""
    harness = ScriptedHarness()
    config = resolve_config(values, harness.config_section, DEFAULT_SCRIPTED_MODEL)
    resolver = TurnConfigResolver(config)
    source = LocalToolSource(workspace, frozenset(LocalToolGroup), resolver)
    context = HarnessBuildContext(
        config=config,
        config_path=None,
        workspace=workspace,
        tool_sources=(source,),
        system_prompt_file_text=None,
        resolver=resolver,
    )
    return with_turn_config(harness.build_session_flow(context), resolver)


if __name__ == "__main__":
    run_agent_worker(
        [
            build_local_tools_flow(
                Path(os.environ[WORKSPACE_ENVIRONMENT_VARIABLE]),
                json.loads(os.environ[CONFIG_ENVIRONMENT_VARIABLE]),
            )
        ]
    )
