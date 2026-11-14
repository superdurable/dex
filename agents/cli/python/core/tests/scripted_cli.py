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
``DEX_AGENTS_CLI_TEST_LEASE_FAULT_DIR`` injects lease faults: while ``fail-acquire`` exists
there, every acquire fails before it is sent; when ``lose-next-acquire`` exists, the next
acquire is sent, the file is deleted, and its answer is lost as a ``TimeoutError``. A
``lose-next-acquire`` that holds ``then-fail-lease-reads`` then creates ``fail-lease-read``,
and while that file exists every Client read of ``SessionTerminalLease`` fails. A
``slow-next-acquire`` that holds a number of milliseconds is deleted, and the next acquire is
sent and answers that much later.
"""

import asyncio
import os
from pathlib import Path
from typing import Any

import grpc
from dex import AsyncClient, DexServiceError, ErrorSubStatus

from dex_agents import SESSION_TERMINAL_LEASE, AgentSessionClient
from dex_agents_cli_core import run_agent_cli
from tests.scripted_harness import (
    FORCED_SUMMARY_ENVIRONMENT_VARIABLE,
    WITHOUT_IMAGES_ENVIRONMENT_VARIABLE,
    ScriptedHarness,
)

#: The directory of the lease gate markers; unset runs without a gate.
LEASE_GATE_ENVIRONMENT_VARIABLE = "DEX_AGENTS_TEST_LEASE_GATE_DIR"
#: The directory of the lease fault markers; unset runs without faults.
LEASE_FAULT_ENVIRONMENT_VARIABLE = "DEX_AGENTS_CLI_TEST_LEASE_FAULT_DIR"
#: While this file exists in the fault directory, every acquire fails before it is sent.
FAIL_ACQUIRE_MARKER = "fail-acquire"
#: When this file exists in the fault directory, the next acquire's answer is lost.
LOSE_NEXT_ACQUIRE_MARKER = "lose-next-acquire"
#: The content of ``lose-next-acquire`` that also starts failing lease reads after the loss.
THEN_FAIL_LEASE_READS = "then-fail-lease-reads"
#: While this file exists in the fault directory, every Client read of the lease fails.
FAIL_LEASE_READ_MARKER = "fail-lease-read"
#: When this file exists, the next acquire answers the milliseconds it holds later.
SLOW_NEXT_ACQUIRE_MARKER = "slow-next-acquire"


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


def _injected_fault(detail: str) -> DexServiceError:
    return DexServiceError(
        grpc.StatusCode.UNAVAILABLE, ErrorSubStatus.UNCATEGORIZED, detail, "InvokeRPC", None
    )


def _install_lease_fault(directory: Path) -> None:
    acquire = AgentSessionClient.acquire_session_terminal_lease
    get_attributes = AsyncClient.get_attributes

    async def faulty_acquire(self: AgentSessionClient, *arguments: Any) -> Any:
        if (directory / FAIL_ACQUIRE_MARKER).exists():
            raise _injected_fault("injected lease fault: the acquire was not sent")
        slow = directory / SLOW_NEXT_ACQUIRE_MARKER
        if slow.exists():
            delay_seconds = int(slow.read_text().strip()) / 1000
            slow.unlink()
            result = await acquire(self, *arguments)
            await asyncio.sleep(delay_seconds)
            return result
        lost = directory / LOSE_NEXT_ACQUIRE_MARKER
        if lost.exists():
            is_failing_reads_next = lost.read_text().strip() == THEN_FAIL_LEASE_READS
            await acquire(self, *arguments)
            lost.unlink()
            if is_failing_reads_next:
                (directory / FAIL_LEASE_READ_MARKER).touch()
            raise TimeoutError("injected lease fault: the acquire's answer was lost")
        return await acquire(self, *arguments)

    async def faulty_get_attributes(self: AsyncClient, flow_id: str, *definitions: Any) -> Any:
        is_lease_read = any(
            getattr(definition, "name", None) == SESSION_TERMINAL_LEASE.name
            for definition in definitions
        )
        if is_lease_read and (directory / FAIL_LEASE_READ_MARKER).exists():
            raise _injected_fault("injected lease fault: the lease read failed")
        return await get_attributes(self, flow_id, *definitions)

    AgentSessionClient.acquire_session_terminal_lease = faulty_acquire  # type: ignore[method-assign, assignment]
    AsyncClient.get_attributes = faulty_get_attributes  # type: ignore[method-assign]


if __name__ == "__main__":
    gate = os.environ.get(LEASE_GATE_ENVIRONMENT_VARIABLE)
    if gate:
        _install_lease_gate(Path(gate))
    fault = os.environ.get(LEASE_FAULT_ENVIRONMENT_VARIABLE)
    if fault:
        _install_lease_fault(Path(fault))
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
