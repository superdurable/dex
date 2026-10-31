# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Read a Flow's Step history, so tests can assert the Step shape of a turn."""

from __future__ import annotations

import asyncio
import json
from dataclasses import dataclass
from pathlib import Path

from dex_agents.testing.processes import resolve_dexcli_path

#: History event type of a completed ``wait_for`` call.
WAIT_FOR_COMPLETED_EVENT = "StepWaitForCompleted"
#: History event type of a completed ``execute`` call.
EXECUTE_COMPLETED_EVENT = "StepExecuteCompleted"


@dataclass(frozen=True, kw_only=True)
class StepHistoryEntry:
    """Describe one completed Step method in a Flow's history.

    Attributes:
        event_type: ``WAIT_FOR_COMPLETED_EVENT`` or ``EXECUTE_COMPLETED_EVENT``.
        step_type: Step type, such as ``"RunModel"``.
        step_execution_id: Step execution ID, such as ``"RunModel-2"``.
        final_attempt: One-based attempt that completed the method.
    """

    event_type: str
    step_type: str
    step_execution_id: str
    final_attempt: int


async def read_step_history(
    server_address: str,
    flow_id: str,
    *,
    dexcli: Path | None = None,
) -> list[StepHistoryEntry]:
    """Return every completed Step method of a Flow's current run, in history order.

    The function runs ``dexcli flow history --all``. Use it to assert that a
    turn produced the expected Steps, such as ``RunModel``, ``CallTool``,
    ``ResumeTurn``, and ``RunModel``. Steps that are still waiting or running
    are not included.

    Example:
        >>> history = await read_step_history(stack.engine.address, "session-1")
        >>> executed = [entry.step_type for entry in history
        ...             if entry.event_type == EXECUTE_COMPLETED_EVENT]

    Args:
        server_address: Dex Server address.
        flow_id: Flow ID to read.
        dexcli: ``dexcli`` executable; ``None`` uses ``resolve_dexcli_path()``.

    Returns:
        One entry per completed ``wait_for`` or ``execute`` method.

    Raises:
        RuntimeError: If ``dexcli`` fails.
    """
    process = await asyncio.create_subprocess_exec(
        str(dexcli or resolve_dexcli_path()),
        "flow",
        "history",
        flow_id,
        "--server",
        server_address,
        "--all",
        "--output",
        "json",
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    stdout, stderr = await process.communicate()
    if process.returncode != 0:
        raise RuntimeError(f"dexcli flow history failed: {stderr.decode(errors='replace')}")
    entries: list[StepHistoryEntry] = []
    for event in json.loads(stdout)["events"]:
        if event["type"] not in (WAIT_FOR_COMPLETED_EVENT, EXECUTE_COMPLETED_EVENT):
            continue
        step_context = event["payload"]["context"]
        entries.append(
            StepHistoryEntry(
                event_type=event["type"],
                step_type=step_context["stepType"],
                step_execution_id=step_context["stepExecutionId"],
                final_attempt=int(step_context["finalAttempt"]),
            )
        )
    return entries
