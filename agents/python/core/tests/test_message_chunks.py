# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The message window, archive chunks, and retention, on a real Dex Server."""

from __future__ import annotations

import asyncio
import contextlib
import json
from collections.abc import AsyncIterator
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from uuid import uuid4

import pytest_asyncio
from dex import CodecRegistry

from dex_agents import (
    AGENT_MESSAGE_CHUNKS,
    AGENT_MESSAGE_INDEX,
    AGENT_MESSAGES,
    AgentEventType,
    AgentMessage,
    AgentMessageChunk,
    AgentMessageChunkRange,
    AgentMessageIndex,
    AgentSessionClient,
    AgentSessionFlow,
    AgentSnapshot,
    MessageRole,
    message_sequence_key,
)
from dex_agents.stepwise import CONTEXT_SUMMARY_NOTE_HEADER
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    AgentTestStack,
    FileGate,
    wait_for_agent_snapshot,
)
from dex_agents.testing.processes import resolve_dexcli_path
from tests.compaction_agent import (
    MESSAGE_CHUNK_MAX_BYTES_ENVIRONMENT_VARIABLE,
    MESSAGE_CHUNK_SIZE_ENVIRONMENT_VARIABLE,
    MESSAGE_WINDOW_SIZE_ENVIRONMENT_VARIABLE,
    MODEL_GATE_NAME,
    RETAINED_MESSAGE_LIMIT_ENVIRONMENT_VARIABLE,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    WINDOW_TOKENS_ENVIRONMENT_VARIABLE,
    build_compaction_agent_flow,
    read_model_requests,
)
from tests.conftest import CORE_DIR

WORKER_MODULE = "tests.compaction_worker"
# A window of three and chunks of two roll over on every assistant message after the first two.
SMALL_WINDOW = {
    MESSAGE_WINDOW_SIZE_ENVIRONMENT_VARIABLE: "3",
    MESSAGE_CHUNK_SIZE_ENVIRONMENT_VARIABLE: "2",
}
SMALL_MAX_VALUE_BYTES = 8192
# The scripted model never compacts below this many tokens.
NO_COMPACTION = {WINDOW_TOKENS_ENVIRONMENT_VARIABLE: "1000000"}


@dataclass(frozen=True)
class StoredLayout:
    """Where one session's messages are, read with Client Attribute reads."""

    index: AgentMessageIndex | None
    window_sequences: list[int]
    chunks: dict[int, AgentMessageChunk]


@dataclass
class ChunkHarness:
    stack: AgentTestStack
    flow: AgentSessionFlow
    sessions: AgentSessionClient
    state_dir: Path

    def new_session_id(self) -> str:
        return f"session-{uuid4().hex}"

    def model_inputs(self, turn_id: str) -> list[list[Any]]:
        return [
            entry["items"]
            for entry in read_model_requests(self.state_dir)
            if entry["kind"] == "model" and entry["turn_id"] == turn_id
        ]

    async def run_turns(self, session_id: str, commands: list[str]) -> AgentSnapshot:
        await self.sessions.start_session(session_id, commands[0])
        for command in commands[1:]:
            assert await self.sessions.send_user_message(session_id, command)
        return await self.wait_for_turns(session_id, len(commands))

    async def wait_for_turns(self, session_id: str, turn_count: int) -> AgentSnapshot:
        return await wait_for_agent_snapshot(
            self.sessions,
            session_id,
            lambda snapshot: snapshot
            if snapshot.state is not None
            and snapshot.state.turn_count >= turn_count
            and snapshot.state.active_turn_id is None
            else None,
            description=f"{turn_count} finished turn(s)",
        )

    async def pages(self, session_id: str, limit: int) -> list[list[int]]:
        """Walk ``list_agent_messages`` back from the newest page; return each page's sequences."""
        pages: list[list[int]] = []
        before: int | None = None
        while True:
            page = await self.sessions.list_agent_messages(
                session_id, before_sequence=before, limit=limit
            )
            pages.insert(0, [message.sequence for message in page.messages])
            if page.next_before_sequence is None:
                return pages
            before = page.next_before_sequence

    async def all_messages(self, session_id: str) -> list[AgentMessage]:
        messages: list[AgentMessage] = []
        before: int | None = None
        while True:
            page = await self.sessions.list_agent_messages(
                session_id, before_sequence=before, limit=200
            )
            messages[:0] = page.messages
            if page.next_before_sequence is None:
                return messages
            before = page.next_before_sequence

    async def read_layout(self, session_id: str, next_sequence: int) -> StoredLayout:
        """Read the index, which sequences have a window instance, and every chunk instance."""
        client = self.stack.client
        index = (await client.get_attributes(session_id, AGENT_MESSAGE_INDEX)).get(
            AGENT_MESSAGE_INDEX
        )
        window_reads = [
            AGENT_MESSAGES.instance(message_sequence_key(sequence))
            for sequence in range(1, next_sequence)
        ]
        chunk_reads = {
            sequence: AGENT_MESSAGE_CHUNKS.instance(message_sequence_key(sequence))
            for sequence in range(1, next_sequence)
        }
        window_values = await client.get_attributes(session_id, *window_reads)
        chunk_values = await client.get_attributes(session_id, *chunk_reads.values())
        return StoredLayout(
            index=index,
            window_sequences=[
                sequence
                for sequence, read in enumerate(window_reads, start=1)
                if window_values.get(read) is not None
            ],
            chunks={
                sequence: chunk
                for sequence, read in chunk_reads.items()
                if (chunk := chunk_values.get(read)) is not None
            },
        )

    async def turn_completed_counts(self, session_id: str) -> list[int]:
        page = await self.stack.client.list_stream_messages(
            session_id, self.flow.agent_events, 1000
        )
        return [
            json.loads(message.value.payload)["message_count"]
            for message in reversed(page.messages)
            if message.value.type == AgentEventType.TURN_COMPLETED
        ]

    async def written_keys(self, session_id: str) -> list[tuple[str, list[str]]]:
        """Return each Execute's Step execution ID and the Attribute keys it wrote or deleted."""
        process = await asyncio.create_subprocess_exec(
            str(resolve_dexcli_path()),
            "flow",
            "history",
            session_id,
            "--server",
            self.stack._engine.address,
            "--all",
            "--output",
            "json",
            stdout=asyncio.subprocess.PIPE,
        )
        stdout, _ = await process.communicate()
        assert process.returncode == 0
        return [
            (
                event["payload"]["context"]["stepExecutionId"],
                [write["key"] for write in event["payload"]["output"].get("upsertAttributes", [])],
            )
            for event in json.loads(stdout)["events"]
            if event["type"] == "StepExecuteCompleted"
        ]

    async def executed_step_types(self, session_id: str) -> list[str]:
        history = await self.stack.read_step_history(session_id)
        return [entry.step_type for entry in history if entry.event_type == EXECUTE_COMPLETED_EVENT]


@pytest_asyncio.fixture
async def chunk_harness(tmp_path: Path) -> AsyncIterator[ChunkHarness]:
    async with _chunk_harness(tmp_path, None) as harness:
        yield harness


@pytest_asyncio.fixture
async def small_value_chunk_harness(tmp_path: Path) -> AsyncIterator[ChunkHarness]:
    config_path = tmp_path / "server-zd.yaml"
    config_path.write_text(
        f"values:\n  maxValueBytes: {SMALL_MAX_VALUE_BYTES}\n  blobDbMinValueBytes: 4096\n"
        "dev:\n  durabilityMode: processCrashSafe\n"
    )
    async with _chunk_harness(tmp_path, config_path) as harness:
        yield harness


@contextlib.asynccontextmanager
async def _chunk_harness(
    tmp_path: Path, server_zd_config: Path | None
) -> AsyncIterator[ChunkHarness]:
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    flow = build_compaction_agent_flow(state_dir)
    async with AgentTestStack(
        tmp_path,
        [flow],
        WORKER_MODULE,
        worker_cwd=CORE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
        server_zd_config=server_zd_config,
    ) as stack:
        yield ChunkHarness(stack, flow, stack.sessions(flow), state_dir)


def _assert_consistent_layout(
    layout: StoredLayout, next_sequence: int, window_size: int, chunk_size: int = 2
) -> None:
    """Every stored message is in exactly one place, and the chunks match the index."""
    index = layout.index
    assert index is not None
    archived = [c for c in index.chunks if c.first_sequence < index.window_first_sequence]
    direct = [c for c in index.chunks if c.first_sequence >= index.window_first_sequence]
    direct_sequences = {c.first_sequence for c in direct}
    assert all(c.first_sequence == c.last_sequence for c in direct)
    assert layout.window_sequences == [
        sequence
        for sequence in range(index.window_first_sequence, next_sequence)
        if sequence not in direct_sequences
    ]
    assert len(layout.window_sequences) <= window_size
    expected_first = index.first_sequence
    for chunk_range in archived:
        assert chunk_range.first_sequence == expected_first
        assert 1 <= chunk_range.last_sequence - chunk_range.first_sequence + 1 <= chunk_size
        expected_first = chunk_range.last_sequence + 1
    assert expected_first == index.window_first_sequence
    assert sorted(layout.chunks) == [chunk_range.first_sequence for chunk_range in index.chunks]
    for chunk_range in index.chunks:
        stored = layout.chunks[chunk_range.first_sequence].messages
        assert [message.sequence for message in stored] == list(
            range(chunk_range.first_sequence, chunk_range.last_sequence + 1)
        )


def _two_message_chunks(first_sequence: int, end_sequence: int) -> list[AgentMessageChunkRange]:
    return [
        AgentMessageChunkRange(first_sequence=first, last_sequence=first + 1)
        for first in range(first_sequence, end_sequence, 2)
    ]


async def test_messages_roll_over_into_many_chunks_and_a_killed_rollover_commits_once(
    chunk_harness: ChunkHarness,
) -> None:
    """A Worker killed in the model call whose commit rolls over reruns that Step only."""
    harness = chunk_harness
    worker = await harness.stack.start_worker({**SMALL_WINDOW, **NO_COMPACTION})
    session_id = harness.new_session_id()
    commands = [f"pad 10 t{turn}" for turn in range(1, 13)]
    commands[6] = "pad 10 t7-gate-model"
    await harness.sessions.start_session(session_id, commands[0])
    for command in commands[1:]:
        assert await harness.sessions.send_user_message(session_id, command)

    # Turn 7's answer is message 14; storing it moves messages 11 and 12 into a chunk.
    gate = FileGate(harness.state_dir, MODEL_GATE_NAME)
    await gate.wait_until_reached()
    before_kill = await harness.read_layout(session_id, 14)
    assert before_kill.index is not None
    assert before_kill.index.window_first_sequence == 11
    worker.kill()
    gate.release()
    await harness.stack.start_worker({**SMALL_WINDOW, **NO_COMPACTION})
    snapshot = await harness.wait_for_turns(session_id, len(commands))

    assert snapshot.state is not None
    assert snapshot.state.next_message_sequence == 25
    messages = await harness.all_messages(session_id)
    assert [message.sequence for message in messages] == list(range(1, 25))
    assert [message.text for message in messages if message.role is MessageRole.USER] == commands
    assert [message.text for message in messages if message.role is MessageRole.ASSISTANT] == [
        f"{command.removeprefix('pad 10 ')}:" + "x" * 10 for command in commands
    ]
    layout = await harness.read_layout(session_id, 25)
    _assert_consistent_layout(layout, 25, 3)
    assert layout.index == AgentMessageIndex(
        chunks=_two_message_chunks(1, 23),
        first_sequence=1,
        window_first_sequence=23,
    )
    assert len(layout.chunks) == 11
    # The killed turn's RunModel ran twice and committed once; every turn counts one reply.
    assert gate.reached_count() == 2
    assert await harness.turn_completed_counts(session_id) == [1] * 12
    # Without compaction the last model call received every message, from the chunks and the window.
    [last_input] = harness.model_inputs("turn-12")
    assert [item[1] for item in last_input if item[0] == "user"] == commands


async def test_retention_deletes_only_fully_summarized_chunks_beyond_the_limit(
    chunk_harness: ChunkHarness,
) -> None:
    harness = chunk_harness
    # Without a summary nothing is deleted, however many messages are stored.
    unsummarized_worker = await harness.stack.start_worker(
        {**SMALL_WINDOW, **NO_COMPACTION, RETAINED_MESSAGE_LIMIT_ENVIRONMENT_VARIABLE: "4"}
    )
    unsummarized = harness.new_session_id()
    await harness.run_turns(unsummarized, [f"pad 10 u{turn}" for turn in range(1, 9)])
    layout = await harness.read_layout(unsummarized, 17)
    _assert_consistent_layout(layout, 17, 3)
    assert layout.index is not None and layout.index.first_sequence == 1
    assert (
        await harness.sessions.list_agent_messages(unsummarized)
    ).deleted_before_sequence is None
    assert [message.sequence for message in await harness.all_messages(unsummarized)] == list(
        range(1, 17)
    )

    unsummarized_worker.kill()
    # Each "pad 400" turn stores about 115 tokens, so a 600-token window compacts every few turns.
    await harness.stack.start_worker(
        {
            **SMALL_WINDOW,
            WINDOW_TOKENS_ENVIRONMENT_VARIABLE: "600",
            RETAINED_MESSAGE_LIMIT_ENVIRONMENT_VARIABLE: "4",
        }
    )
    session_id = harness.new_session_id()
    commands = [f"pad 400 t{turn}" for turn in range(1, 21)]
    snapshot = await harness.run_turns(session_id, commands)

    summary = snapshot.context_summary
    assert snapshot.state is not None and summary is not None
    next_sequence = snapshot.state.next_message_sequence
    assert next_sequence == 41
    layout = await harness.read_layout(session_id, next_sequence)
    _assert_consistent_layout(layout, next_sequence, 3)
    index = layout.index
    assert index is not None
    assert index.first_sequence > 1
    # Every deleted chunk was summarized, at least the limit stays, and the next chunk is needed.
    assert index.first_sequence - 1 <= summary.last_sequence
    assert next_sequence - index.first_sequence >= 4
    oldest = index.chunks[0]
    assert (
        oldest.last_sequence > summary.last_sequence
        or next_sequence - (oldest.last_sequence + 1) < 4
    )
    page = await harness.sessions.list_agent_messages(session_id, limit=200)
    assert (page.next_before_sequence, page.deleted_before_sequence) == (
        None,
        index.first_sequence,
    )
    messages = await harness.all_messages(session_id)
    assert [message.sequence for message in messages] == list(
        range(index.first_sequence, next_sequence)
    )
    assert snapshot.messages == messages
    assert snapshot.messages[-1].text == "t20:" + "x" * 400
    # Each model call got its own command last, after the summary or the first command.
    calls = [
        entry["items"]
        for entry in read_model_requests(harness.state_dir)
        if entry["kind"] == "model" and entry["items"][-1][1] in commands
    ]
    assert [items[-1][1] for items in calls] == commands
    assert all(
        items[0][1].startswith(CONTEXT_SUMMARY_NOTE_HEADER) or items[0][1] == commands[0]
        for items in calls
    )
    assert await harness.turn_completed_counts(session_id) == [1] * 20


async def test_pages_and_the_snapshot_read_across_the_window_and_the_chunks(
    chunk_harness: ChunkHarness,
) -> None:
    harness = chunk_harness
    await harness.stack.start_worker({**SMALL_WINDOW, **NO_COMPACTION})
    session_id = harness.new_session_id()
    commands = [f"pad 1 t{turn}" for turn in range(1, 102)]
    snapshot = await harness.run_turns(session_id, commands)

    assert snapshot.state is not None and snapshot.state.next_message_sequence == 203
    layout = await harness.read_layout(session_id, 203)
    _assert_consistent_layout(layout, 203, 3)
    # The snapshot keeps the newest 200 of 202, from the middle of the first chunk on.
    assert [message.sequence for message in snapshot.messages] == list(range(3, 203))
    for limit in (1, 2, 3, 5, 7, 200):
        pages = await harness.pages(session_id, limit)
        assert [sequence for page in pages for sequence in page] == list(range(1, 203))
        assert all(len(page) == limit for page in pages[1:])
    # Anchors in the window, at a chunk's first message, and inside a chunk.
    for before, expected in ((202, range(198, 202)), (101, range(97, 101)), (4, range(1, 4))):
        page = await harness.sessions.list_agent_messages(
            session_id, before_sequence=before, limit=4
        )
        assert [message.sequence for message in page.messages] == list(expected)
        assert page.next_before_sequence == (None if expected.start == 1 else expected.start)
    page = await harness.sessions.list_agent_messages(session_id, before_sequence=1, limit=4)
    assert (page.messages, page.next_before_sequence) == ([], None)


async def test_a_chunk_closes_at_its_byte_cap_and_a_larger_message_gets_a_chunk_alone(
    chunk_harness: ChunkHarness,
) -> None:
    harness = chunk_harness
    chunk_max_bytes = 4_000
    await harness.stack.start_worker(
        {
            MESSAGE_WINDOW_SIZE_ENVIRONMENT_VARIABLE: "8",
            MESSAGE_CHUNK_SIZE_ENVIRONMENT_VARIABLE: "8",
            MESSAGE_CHUNK_MAX_BYTES_ENVIRONMENT_VARIABLE: str(chunk_max_bytes),
            **NO_COMPACTION,
        }
    )
    session_id = harness.new_session_id()
    commands = [f"pad 600 t{turn}" for turn in range(1, 13)]
    commands[4] = "pad 6000 big"
    snapshot = await harness.run_turns(session_id, commands)

    assert snapshot.state is not None and snapshot.state.next_message_sequence == 25
    layout = await harness.read_layout(session_id, 25)
    _assert_consistent_layout(layout, 25, 8, chunk_size=8)
    index = layout.index
    assert index is not None and len(index.chunks) >= 3
    codec = CodecRegistry().resolve(AgentMessageChunk)
    sizes = {
        first: len(str(codec.encode(chunk).data).encode()) for first, chunk in layout.chunks.items()
    }
    # Each chunk is under the cap, except the oversized answer, which is alone in its chunk.
    big_answer = 10
    for first, chunk in layout.chunks.items():
        sequences = [message.sequence for message in chunk.messages]
        if big_answer in sequences:
            assert sequences == [big_answer]
            assert sizes[first] > chunk_max_bytes
        else:
            assert sizes[first] <= chunk_max_bytes
    assert any(len(chunk.messages) < 8 for chunk in layout.chunks.values())
    messages = await harness.all_messages(session_id)
    assert [message.sequence for message in messages] == list(range(1, 25))
    assert messages[big_answer - 1].text == "big:" + "x" * 6000
    # The oversized answer went straight into its own chunk; the window never held it.
    written = [key for _, keys in await harness.written_keys(session_id) for key in keys]
    assert f"AgentMessages/{message_sequence_key(big_answer)}" not in written
    assert written.count(f"AgentMessageChunks/{message_sequence_key(big_answer)}") == 1
    [last_input] = harness.model_inputs("turn-12")
    assert [item[1] for item in last_input if item[0] == "user"] == commands


async def test_the_index_is_written_only_when_chunks_are_written_or_deleted(
    chunk_harness: ChunkHarness,
) -> None:
    harness = chunk_harness
    await harness.stack.start_worker(
        {
            MESSAGE_WINDOW_SIZE_ENVIRONMENT_VARIABLE: "8",
            MESSAGE_CHUNK_SIZE_ENVIRONMENT_VARIABLE: "4",
            **NO_COMPACTION,
        }
    )
    session_id = harness.new_session_id()
    commands = [f"pad 10 t{turn}" for turn in range(1, 13)]
    await harness.run_turns(session_id, commands)

    written = await harness.written_keys(session_id)
    index_writes = [keys for _, keys in written if "AgentMessageIndex" in keys]
    assert all(any(key.startswith("AgentMessageChunks/") for key in keys) for keys in index_writes)
    # 24 messages in chunks of four: four chunks, so four of the 24 Steps rewrote the index.
    assert len(index_writes) == 4
    # Every Step that starts a turn records where its replies start, and nothing else does.
    reply_writers = [step for step, keys in written if "TurnReplySequence" in keys]
    assert reply_writers == ["StartAgentSession-1"] + [
        f"AwaitUserMessage-{turn}" for turn in range(1, 12)
    ]
    assert await harness.turn_completed_counts(session_id) == [1] * 12


async def test_a_message_too_large_for_the_server_fails_its_turn_and_the_session_continues(
    small_value_chunk_harness: ChunkHarness,
) -> None:
    """A message just under values.maxValueBytes once fit the window and wedged its rollover."""
    harness = small_value_chunk_harness
    await harness.stack.start_worker(
        {MESSAGE_CHUNK_MAX_BYTES_ENVIRONMENT_VARIABLE: "4000", **NO_COMPACTION}
    )
    session_id = harness.new_session_id()
    await harness.run_turns(session_id, ["pad 100 probe"])
    [_, probe_answer] = await harness.all_messages(session_id)
    # The answer grows by two bytes per padding character: once in text, once in framework_item.
    edge_bytes = SMALL_MAX_VALUE_BYTES - 19
    padding = 100 + (edge_bytes - _message_size(probe_answer)) // 2

    assert await harness.sessions.send_user_message(session_id, f"pad {padding} edge")
    failed = await harness.wait_for_turns(session_id, 2)
    assert failed.state is not None
    assert failed.state.last_turn_error is not None
    assert "bytes" in failed.state.last_turn_error
    assert await harness.sessions.send_user_message(session_id, "pad 10 after")
    after = await harness.wait_for_turns(session_id, 3)

    assert after.state is not None and after.state.last_turn_error is None
    assert after.messages[-1].text == "after:" + "x" * 10
    assert [message.text for message in after.messages if message.role is MessageRole.USER] == [
        "pad 100 probe",
        f"pad {padding} edge",
        "pad 10 after",
    ]


def _message_size(message: AgentMessage) -> int:
    return len(str(CodecRegistry().resolve(AgentMessage).encode(message).data).encode())
