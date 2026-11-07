# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A scripted model with a context window, and the session scenarios that need one.

A framework adapter wraps ``ContextWindowModelScript`` in the framework's own
model type, then drives ``ContextWindowSessionScenario``. Every adapter so
proves the same thing: a session that would exceed a small context window keeps
going, the model input shrinks, and the record stays readable.

Each user message is ``turn <tag>``. The model calls the ``lookup`` tool once
with the tag, then answers ``ContextWindowModelScript.answer_text(tag)``.
Like a provider, it refuses a request larger than the window.
"""

from __future__ import annotations

import json
import os
import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any
from uuid import uuid4

from dex_agents.client import AgentSessionClient
from dex_agents.compaction import (
    CONTEXT_SUMMARY_INSTRUCTIONS,
    CONTEXT_SUMMARY_NOTE_HEADER,
    CompactionPolicy,
)
from dex_agents.persistence import DEFAULT_RETAINED_MESSAGE_LIMIT
from dex_agents.session_flow import AgentSessionFlow
from dex_agents.testing.coordination import FileGate
from dex_agents.testing.history import EXECUTE_COMPLETED_EVENT
from dex_agents.testing.polling import wait_for_agent_snapshot
from dex_agents.testing.stack import AgentTestStack
from dex_agents.values import AgentMessage, AgentSessionStatus, AgentSnapshot

#: Environment variable holding the JSON of ``ContextWindowSettings``.
CONTEXT_WINDOW_SETTINGS_ENVIRONMENT_VARIABLE = "DEX_AGENTS_CONTEXT_WINDOW_SETTINGS"
#: Tool that the scripted model calls once per turn.
CONTEXT_WINDOW_TOOL_NAME = "lookup"
#: Start of the error that the scripted model raises for an oversized request.
CONTEXT_WINDOW_EXCEEDED_ERROR = "scripted model: input exceeds the context window"
#: Gate that holds a summary call of a range that names ``gate-summary``.
SUMMARY_GATE_NAME = "summary"

_REQUEST_LOG_NAME = "model-requests.jsonl"
_SUMMARY_GATE_TAG = "gate-summary"
_TURN_COMMAND = re.compile(r"turn ([A-Za-z0-9-]+)")
_PROVIDER_CHARACTERS_PER_TOKEN = 4
_SUMMARY_TAG_LIMIT = 10


@dataclass(frozen=True, kw_only=True)
class ContextWindowSettings:
    """Size the scripted model and the session of one test.

    A Worker child reads them from ``CONTEXT_WINDOW_SETTINGS_ENVIRONMENT_VARIABLE``.

    Attributes:
        window_tokens: Context window of the scripted model, at four
            characters per token.
        is_compaction_enabled: Whether the session gets a ``CompactionPolicy``.
        tool_output_characters: Size of each tool result.
        answer_characters: Size of each model answer.
        message_window_size: ``AgentSessionOptions.message_window_size``.
        message_chunk_size: ``AgentSessionOptions.message_chunk_size``.
        retained_message_limit: ``AgentSessionOptions.retained_message_limit``.
    """

    window_tokens: int = 6000
    is_compaction_enabled: bool = True
    tool_output_characters: int = 2000
    answer_characters: int = 1000
    message_window_size: int = 8
    message_chunk_size: int = 4
    retained_message_limit: int | None = DEFAULT_RETAINED_MESSAGE_LIMIT

    def to_environment(self) -> dict[str, str]:
        """Return the Worker environment that carries these settings.

        Returns:
            One entry under ``CONTEXT_WINDOW_SETTINGS_ENVIRONMENT_VARIABLE``.
        """
        return {CONTEXT_WINDOW_SETTINGS_ENVIRONMENT_VARIABLE: json.dumps(self.__dict__)}

    @classmethod
    def from_environment(cls) -> ContextWindowSettings:
        """Read the settings a test gave this Worker.

        Returns:
            The test's settings, or the defaults outside a Worker child.
        """
        value = os.environ.get(CONTEXT_WINDOW_SETTINGS_ENVIRONMENT_VARIABLE)
        return cls() if value is None else cls(**json.loads(value))

    def session_options(self) -> dict[str, Any]:
        """Return the ``AgentSessionOptions`` keywords for these settings.

        Returns:
            The compaction policy and message storage options.
        """
        return {
            "compaction": CompactionPolicy(context_window_tokens=self.window_tokens)
            if self.is_compaction_enabled
            else None,
            "message_window_size": self.message_window_size,
            "message_chunk_size": self.message_chunk_size,
            "retained_message_limit": self.retained_message_limit,
        }


@dataclass(frozen=True, kw_only=True)
class ContextWindowReply:
    """Say what the model does for one request.

    Attributes:
        text: The answer, or ``None`` when the model calls the tool.
        tool_tag: The ``tag`` argument of the ``lookup`` call, or ``None``.
    """

    text: str | None = None
    tool_tag: str | None = None


class ContextWindowModelScript:
    """Answer model requests of a framework adapter, and log every request.

    The script holds no state between calls, so it survives a Worker restart.
    """

    def __init__(self, state_dir: Path, settings: ContextWindowSettings) -> None:
        """Bind the script to a test's state directory.

        Args:
            state_dir: Directory shared with the test.
            settings: Sizes of the test.
        """
        self._state_dir = state_dir
        self._settings = settings

    def tool_output(self, tag: str) -> str:
        """Return what the ``lookup`` tool answers.

        Args:
            tag: The tool call's ``tag`` argument.

        Returns:
            Text of ``tool_output_characters`` that names the tag.
        """
        return f"lookup-body-{tag}:" + "y" * self._settings.tool_output_characters

    def answer_text(self, tag: str) -> str:
        """Return the answer of the turn with this tag.

        Args:
            tag: The tag of the user message.

        Returns:
            Text of ``answer_characters`` that names the tag.
        """
        return f"answer-body-{tag}:" + "x" * self._settings.answer_characters

    async def reply(
        self, *, instructions: str | None, input_text: str, has_tool_result: bool
    ) -> ContextWindowReply:
        """Answer one model request, as a provider would see it.

        Args:
            instructions: The system prompt of the request.
            input_text: The whole request as text, in the framework's format.
            has_tool_result: Whether the running turn already has its result.

        Returns:
            The tool call or the answer.

        Raises:
            ValueError: If the request exceeds the window, or names no turn.
        """
        is_summary = bool(instructions) and CONTEXT_SUMMARY_INSTRUCTIONS in str(instructions)
        request_characters = len(input_text) + len(instructions or "")
        self._log_request(is_summary, input_text, request_characters)
        window_characters = self._settings.window_tokens * _PROVIDER_CHARACTERS_PER_TOKEN
        if request_characters > window_characters:
            raise ValueError(
                f"{CONTEXT_WINDOW_EXCEEDED_ERROR}: {request_characters} characters, "
                f"limit {window_characters}"
            )
        tags = _TURN_COMMAND.findall(input_text)
        if is_summary:
            return ContextWindowReply(text=await self._summarize(input_text, tags))
        if not tags:
            raise ValueError("the scripted model needs a turn command")
        tag = tags[-1]
        if has_tool_result:
            return ContextWindowReply(text=self.answer_text(tag))
        return ContextWindowReply(tool_tag=tag)

    async def _summarize(self, input_text: str, tags: Sequence[str]) -> str:
        if _SUMMARY_GATE_TAG in tags:
            gate = FileGate(self._state_dir, SUMMARY_GATE_NAME)
            gate.mark_reached()
            await gate.wait_for_release()
        kept = list(dict.fromkeys(tags))[-_SUMMARY_TAG_LIMIT:]
        return f"summary of turns: {' '.join(kept)}"

    def _log_request(self, is_summary: bool, input_text: str, characters: int) -> None:
        entry = {
            "kind": "summary" if is_summary else "model",
            "characters": characters,
            "input": input_text,
        }
        with (self._state_dir / _REQUEST_LOG_NAME).open("a", encoding="utf-8") as log:
            log.write(json.dumps(entry) + "\n")


def read_context_window_requests(state_dir: Path) -> list[Mapping[str, Any]]:
    """Return every request the scripted model saw, in order.

    Args:
        state_dir: The test's state directory.

    Returns:
        Entries with ``kind`` (``"model"`` or ``"summary"``), ``characters``,
        and ``input``.
    """
    path = state_dir / _REQUEST_LOG_NAME
    if not path.exists():
        return []
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines()]


@dataclass
class ContextWindowSessionScenario:
    """Drive an adapter's session past a context window, and check what happened.

    Attributes:
        stack: The test's engine and Workers.
        flow: The adapter's session Flow, as the test process builds it.
        state_dir: The directory shared with the Workers.
        settings: Sizes of the test.
    """

    stack: AgentTestStack
    flow: AgentSessionFlow
    state_dir: Path
    settings: ContextWindowSettings
    sessions: AgentSessionClient = field(init=False)
    session_id: str = field(default_factory=lambda: f"session-{uuid4().hex}")

    def __post_init__(self) -> None:
        self.sessions = self.stack.sessions(self.flow)

    def requests(self, kind: str) -> list[Mapping[str, Any]]:
        """Return the logged requests of one kind.

        Args:
            kind: ``"model"`` or ``"summary"``.

        Returns:
            The requests in order.
        """
        return [e for e in read_context_window_requests(self.state_dir) if e["kind"] == kind]

    async def run_turns(self, tags: Sequence[str], *, is_first: bool = True) -> AgentSnapshot:
        """Run one turn per tag and wait until the session finished them.

        Args:
            tags: The turns' tags.
            is_first: Whether the first tag starts the session.

        Returns:
            The snapshot after the last turn.
        """
        before = 0
        commands = [f"turn {tag}" for tag in tags]
        if is_first:
            await self.sessions.start_session(self.session_id, commands[0])
            commands = commands[1:]
        else:
            snapshot = await self.sessions.get_agent_snapshot(self.session_id)
            before = 0 if snapshot.state is None else snapshot.state.turn_count
        for command in commands:
            assert await self.sessions.send_user_message(self.session_id, command)
        return await self.wait_for_turns(before + len(tags))

    async def wait_for_turns(self, turn_count: int) -> AgentSnapshot:
        """Wait until the session finished a number of turns.

        Args:
            turn_count: Turns that must have ended.

        Returns:
            The snapshot.
        """
        return await wait_for_agent_snapshot(
            self.sessions,
            self.session_id,
            lambda snapshot: snapshot
            if snapshot.state is not None
            and snapshot.state.turn_count >= turn_count
            and snapshot.state.active_turn_id is None
            else None,
            description=f"{turn_count} finished turn(s)",
        )

    async def stored_messages(self) -> list[AgentMessage]:
        """Read every message history still holds through the public RPC.

        Returns:
            The messages in sequence order.
        """
        messages: list[AgentMessage] = []
        before: int | None = None
        while True:
            page = await self.sessions.list_agent_messages(
                self.session_id, before_sequence=before, limit=200
            )
            messages[:0] = page.messages
            if page.next_before_sequence is None:
                return messages
            before = page.next_before_sequence

    async def compact_context_count(self) -> int:
        """Count the finished ``CompactContext`` Steps of the session.

        Returns:
            The count.
        """
        history = await self.stack.read_step_history(self.session_id)
        return sum(
            entry.step_type == "CompactContext" and entry.event_type == EXECUTE_COMPLETED_EVENT
            for entry in history
        )

    async def assert_outlives_the_window(self, turn_count: int = 12) -> AgentSnapshot:
        """Check the whole contract on a session longer than the window.

        Args:
            turn_count: Turns to run.

        Returns:
            The final snapshot.
        """
        tags = [f"t{number}" for number in range(1, turn_count + 1)]
        await self.stack.start_worker()
        snapshot = await self.run_turns(tags)

        assert snapshot.state is not None
        assert snapshot.state.last_turn_error is None, snapshot.state.last_turn_error
        assert snapshot.state.status is AgentSessionStatus.WAITING_FOR_USER_MESSAGE
        assert snapshot.state.turn_count == turn_count
        summary = snapshot.context_summary
        assert summary is not None
        assert summary.text.startswith("summary of turns:")
        assert await self.compact_context_count() >= 1

        limit = self.settings.window_tokens * _PROVIDER_CHARACTERS_PER_TOKEN
        model_requests = self.requests("model")
        assert all(request["characters"] <= limit for request in model_requests)
        all_requests = read_context_window_requests(self.state_dir)
        for index, request in enumerate(all_requests):
            if request["kind"] == "summary":
                assert all_requests[index + 1]["characters"] < all_requests[index - 1]["characters"]
        last_input = model_requests[-1]["input"]
        assert CONTEXT_SUMMARY_NOTE_HEADER in last_input
        # The scripted summary is one line; an adapter may append data lines it keeps out of input.
        assert summary.text.splitlines()[0] in last_input
        # Turn 1 is behind the summary, so its bodies left the model input.
        assert "answer-body-t1:" not in last_input
        assert "lookup-body-t1:" not in last_input

        messages = await self.stored_messages()
        assert [message.sequence for message in messages] == list(range(1, len(messages) + 1))
        assert messages[0].text == "turn t1"
        assert not any(
            CONTEXT_SUMMARY_NOTE_HEADER in message.framework_item for message in messages
        )
        assert any("answer-body-t1:" in (message.text or "") for message in messages)
        summary_calls = len(self.requests("summary"))
        assert summary_calls == await self.compact_context_count()
        assert snapshot.usage.model_calls == len(model_requests) + summary_calls
        return snapshot

    async def assert_retention_follows_the_summary(self, turn_count: int = 24) -> None:
        """Check that retention deletes only chunks behind the summary.

        The test's settings need a small ``retained_message_limit``.

        Args:
            turn_count: Turns to run.
        """
        await self.stack.start_worker()
        snapshot = await self.run_turns([f"t{number}" for number in range(1, turn_count + 1)])
        assert snapshot.state is not None and snapshot.state.last_turn_error is None
        summary = snapshot.context_summary
        assert summary is not None
        page = await self.sessions.list_agent_messages(self.session_id, limit=200)
        deleted_before = page.deleted_before_sequence
        assert deleted_before is not None and deleted_before > 1
        assert deleted_before - 1 <= summary.last_sequence
        messages = await self.stored_messages()
        assert messages[0].sequence == deleted_before
        assert [message.sequence for message in messages] == list(
            range(deleted_before, deleted_before + len(messages))
        )
        limit = self.settings.retained_message_limit
        assert limit is not None and len(messages) >= limit
        assert messages[-1].text is not None and messages[-1].text.startswith(
            f"answer-body-t{turn_count}:"
        )

    async def assert_fails_at_the_window_without_a_policy(self, turn_count: int = 12) -> None:
        """Check that a session without a policy is unchanged and hits the window.

        Args:
            turn_count: Turns to run; the window must end the session first.
        """
        await self.stack.start_worker()
        snapshot = await self.run_turns([f"t{number}" for number in range(1, turn_count + 1)])
        assert snapshot.state is not None
        assert CONTEXT_WINDOW_EXCEEDED_ERROR in (snapshot.state.last_turn_error or "")
        assert snapshot.context_summary is None
        assert await self.compact_context_count() == 0
        assert not self.requests("summary")
        assert not any(
            CONTEXT_SUMMARY_NOTE_HEADER in request["input"] for request in self.requests("model")
        )

    async def assert_starts_compacting_after_an_upgrade(self) -> None:
        """Check that a session started without a policy compacts once a Worker has one."""
        worker = await self.stack.start_worker(
            ContextWindowSettings(
                **{**self.settings.__dict__, "is_compaction_enabled": False}
            ).to_environment()
        )
        await self.run_turns(["t1", "t2", "t3"])
        assert not self.requests("summary")
        worker.kill()

        await self.stack.start_worker()
        snapshot = await self.run_turns([f"t{n}" for n in range(4, 13)], is_first=False)

        assert snapshot.state is not None and snapshot.state.last_turn_error is None
        assert snapshot.context_summary is not None
        messages = await self.stored_messages()
        assert messages[0].text == "turn t1"

    async def assert_worker_kill_reruns_only_the_summary(self) -> None:
        """Check SIGKILL during ``CompactContext``: one more summary call, one summary."""
        worker = await self.stack.start_worker()
        tags = ["gate-summary", *[f"t{number}" for number in range(2, 13)]]
        commands = [f"turn {tag}" for tag in tags]
        await self.sessions.start_session(self.session_id, commands[0])
        for command in commands[1:]:
            assert await self.sessions.send_user_message(self.session_id, command)
        gate = FileGate(self.state_dir, SUMMARY_GATE_NAME)
        await gate.wait_until_reached()
        compacting = await self.sessions.get_agent_snapshot(self.session_id)
        assert compacting.state is not None
        assert compacting.state.status is AgentSessionStatus.COMPACTING_CONTEXT
        assert compacting.context_summary is None
        model_calls_before_kill = len(self.requests("model"))

        worker.kill()
        gate.release()
        await self.stack.start_worker()
        snapshot = await self.wait_for_turns(len(tags))

        assert gate.reached_count() == 2
        assert snapshot.state is not None and snapshot.state.last_turn_error is None
        assert snapshot.context_summary is not None
        assert snapshot.context_summary.generation >= 1
        model_requests = self.requests("model")
        # The model call that started compaction never ran, so the kill repeats no model call.
        assert model_calls_before_kill < len(model_requests)
        summary_calls = len(self.requests("summary"))
        assert await self.compact_context_count() == summary_calls - 1
        assert snapshot.usage.model_calls == len(model_requests) + summary_calls - 1
