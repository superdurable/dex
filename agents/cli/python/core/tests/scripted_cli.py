# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The test command: ``python -m tests.scripted_cli`` runs the CLI core with the scripted harness.

``DEX_AGENTS_TEST_WITHOUT_IMAGES=1`` runs a harness whose runner takes no images.
``DEX_AGENTS_TEST_FORCED_SUMMARY=on`` or ``off`` runs a harness that ignores
``HarnessContext.is_session_summary_indexed``; ``misreported-on`` or
``misreported-off`` declares that summary while its options report the opposite.
``DEX_AGENTS_TEST_LEASE_GATE_DIR`` holds each lease release before it leaves, and each
lease acquire after it answered, until the test writes ``release.go`` or ``acquire.go`` there.
"""

import asyncio
import os
from pathlib import Path
from typing import Any

from dex_agents import AgentSessionClient
from dex_agents_cli_core import run_agent_cli
from tests.scripted_harness import (
    FORCED_SUMMARY_ENVIRONMENT_VARIABLE,
    WITHOUT_IMAGES_ENVIRONMENT_VARIABLE,
    ScriptedHarness,
)

#: The directory of the lease gate markers; unset runs without a gate.
LEASE_GATE_ENVIRONMENT_VARIABLE = "DEX_AGENTS_TEST_LEASE_GATE_DIR"


async def _pass_gate(directory: Path, name: str) -> None:
    (directory / f"{name}.reached").touch()
    while not (directory / f"{name}.go").exists():
        await asyncio.sleep(0.02)


def _install_lease_gate(directory: Path) -> None:
    acquire = AgentSessionClient.acquire_session_terminal_lease
    release = AgentSessionClient.release_session_terminal_lease

    async def gated_acquire(self: AgentSessionClient, *arguments: Any) -> Any:
        result = await acquire(self, *arguments)
        await _pass_gate(directory, "acquire")
        return result

    async def gated_release(self: AgentSessionClient, *arguments: Any) -> Any:
        await _pass_gate(directory, "release")
        return await release(self, *arguments)

    AgentSessionClient.acquire_session_terminal_lease = gated_acquire  # type: ignore[method-assign, assignment]
    AgentSessionClient.release_session_terminal_lease = gated_release  # type: ignore[method-assign, assignment]


if __name__ == "__main__":
    gate = os.environ.get(LEASE_GATE_ENVIRONMENT_VARIABLE)
    if gate:
        _install_lease_gate(Path(gate))
    supports_images = os.environ.get(WITHOUT_IMAGES_ENVIRONMENT_VARIABLE) != "1"
    forced = os.environ.get(FORCED_SUMMARY_ENVIRONMENT_VARIABLE)
    raise SystemExit(
        run_agent_cli(
            ScriptedHarness(
                supports_images=supports_images,
                forced_summary_indexed=None if forced is None else forced.endswith("on"),
                is_summary_misreported=forced is not None and forced.startswith("misreported"),
            )
        )
    )
