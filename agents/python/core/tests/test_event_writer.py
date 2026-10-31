# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""AgentEventWriter hands writes from other threads to its event loop."""

from __future__ import annotations

import asyncio
import json
import threading

from dex_agents import AgentEvent, AgentEventWriter
from dex_agents.testing import wait_until


async def test_writes_from_an_executor_thread_run_on_the_event_loop_thread() -> None:
    loop_thread_id = threading.get_ident()
    delivered: list[tuple[int, AgentEvent]] = []
    writer = AgentEventWriter(
        lambda event: delivered.append((threading.get_ident(), event)),
        "turn-1",
        2,
        "RunModel-1",
    )

    def write_from_thread() -> int:
        writer.write_text_delta("from a thread")
        writer.write_text_delta("in order")
        return threading.get_ident()

    worker_thread_id = await asyncio.to_thread(write_from_thread)
    writer.write_text_delta("from the loop")

    async def delivered_count() -> int:
        return len(delivered)

    await wait_until("three delivered events", delivered_count)
    assert worker_thread_id != loop_thread_id
    assert [thread_id for thread_id, _ in delivered] == [loop_thread_id] * 3
    assert [json.loads(event.payload)["text"] for _, event in delivered] == [
        "from a thread",
        "in order",
        "from the loop",
    ]
    assert {(event.turn_id, event.attempt, event.step_execution_id) for _, event in delivered} == {
        ("turn-1", 2, "RunModel-1")
    }


def test_a_writer_created_outside_an_event_loop_writes_directly() -> None:
    delivered: list[AgentEvent] = []
    writer = AgentEventWriter(delivered.append, None, 1)

    writer.write_event("adapter.custom", {"value": 1})

    assert [(event.type, event.step_execution_id) for event in delivered] == [
        ("adapter.custom", "")
    ]
