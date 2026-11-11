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

    async def has_three_delivered_events() -> bool:
        return len(delivered) >= 3

    await wait_until("three delivered events", has_three_delivered_events)
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


async def test_a_loop_write_follows_earlier_writes_from_another_thread() -> None:
    delivered: list[AgentEvent] = []
    writer = AgentEventWriter(delivered.append, "turn-1", 1)

    # Joining on the loop thread keeps the loop from running the handed-over writes.
    thread = threading.Thread(target=writer.write_text_delta, args=("from a thread",))
    thread.start()
    thread.join()
    writer.write_text_delta("from the loop")

    async def has_two_delivered_events() -> bool:
        return len(delivered) >= 2

    await wait_until("two delivered events", has_two_delivered_events)
    assert [json.loads(event.payload)["text"] for event in delivered] == [
        "from a thread",
        "from the loop",
    ]


def test_a_thread_write_after_the_loop_closed_raises_and_queues_nothing() -> None:
    delivered: list[AgentEvent] = []

    async def create_writer() -> AgentEventWriter:
        return AgentEventWriter(delivered.append, "turn-1", 1)

    writer = asyncio.run(create_writer())
    errors: list[RuntimeError] = []

    def write_from_thread() -> None:
        try:
            writer.write_text_delta("after the loop closed")
        except RuntimeError as error:
            errors.append(error)

    for _ in range(3):
        thread = threading.Thread(target=write_from_thread)
        thread.start()
        thread.join()

    assert len(errors) == 3
    assert delivered == []
    assert len(writer._pending_events) == 0


def test_a_writer_created_outside_an_event_loop_writes_directly() -> None:
    delivered: list[AgentEvent] = []
    writer = AgentEventWriter(delivered.append, None, 1)

    writer.write_event("adapter.custom", {"value": 1})

    assert [(event.type, event.step_execution_id) for event in delivered] == [
        ("adapter.custom", "")
    ]
