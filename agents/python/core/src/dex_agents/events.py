# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Live event writing for the ``AgentEvents`` Stream."""

from __future__ import annotations

import asyncio
import json
import threading
from collections.abc import Callable, Mapping
from enum import StrEnum

from dex_agents.values import AgentEvent, ToolCallStatus


class AgentEventType(StrEnum):
    """Name every event the session writes to ``AgentEvents``.

    ``AgentEvent.type`` holds one of these values as a string. Payloads are
    JSON objects with snake_case keys.

    Attributes:
        TURN_STARTED: A user message started a turn. Payload ``{"sequence"}``,
            the user message sequence.
        TURN_COMPLETED: The turn produced its final answer. Payload
            ``{"message_count"}``, the number of messages the turn appended.
        TURN_FAILED: ``RecordTurnFailure`` ended the turn. Payload ``{"error"}``.
        TEXT_DELTA: Visible model text. Payload ``{"text"}``.
        REASONING_DELTA: A provider-authored reasoning summary. Payload ``{"text"}``.
        TOOL_CALL_STARTED: A tool started. Payload ``{"tool_call_id", "tool_name"}``.
        TOOL_CALL_FINISHED: A tool finished. Payload
            ``{"tool_call_id", "tool_name", "status"}``.
        TOOL_CALL_DEFERRED: The turn deferred a tool call to a Dex Step.
            Payload ``{"tool_call_id", "tool_name", "effect", "requires_approval"}``.
        INTERRUPT_RAISED: A tool call waits for approval. Payload
            ``{"interrupt_id", "tool_call_id", "tool_name", "deadline"}``.
        INTERRUPT_RESOLVED: An interrupt was answered or expired. Payload
            ``{"interrupt_id", "approved", "rejection_message"}``.
        USAGE_RECORDED: The session added segment usage. Payload
            ``{"recorded", "session"}``, each an ``AgentUsage`` object.
    """

    TURN_STARTED = "turn_started"
    TURN_COMPLETED = "turn_completed"
    TURN_FAILED = "turn_failed"
    TEXT_DELTA = "text_delta"
    REASONING_DELTA = "reasoning_delta"
    TOOL_CALL_STARTED = "tool_call_started"
    TOOL_CALL_FINISHED = "tool_call_finished"
    TOOL_CALL_DEFERRED = "tool_call_deferred"
    INTERRUPT_RAISED = "interrupt_raised"
    INTERRUPT_RESOLVED = "interrupt_resolved"
    USAGE_RECORDED = "usage_recorded"


class AgentEventWriter:
    """Write live events for one Step attempt to ``AgentEvents``.

    The session gives every runner call a writer bound to the current turn ID,
    Step execution ID, and Step attempt. Writes enqueue immediately and never
    block. They are best effort: a retry can repeat them and a consumer can miss
    them, so never put durable results only in events.

    Every method is safe to call from any thread, such as a synchronous tool
    running in an executor thread. A call from another thread hands the event
    to the event loop that created the writer, and the Stream receives it in
    call order. Events written after the runner call returns are dropped.

    Example:
        >>> async def run_turn(self, turn: AgentTurnContext) -> AgentTurnOutcome:
        ...     async for chunk in model.stream(...):
        ...         turn.events.write_text_delta(chunk.text)
    """

    def __init__(
        self,
        write_agent_event: Callable[[AgentEvent], None],
        turn_id: str | None,
        attempt: int,
        step_execution_id: str = "",
    ) -> None:
        """Create a writer that stamps every event with its turn and Step attempt.

        The session constructs writers. Construct one yourself only to test a
        runner without a Dex Server. A writer created while an event loop runs
        calls ``write_agent_event`` only on that loop's thread.

        Args:
            write_agent_event: Callable that appends one event to the Stream.
            turn_id: Turn stamped on every event, or ``None``.
            attempt: One-based Step attempt stamped on every event.
            step_execution_id: Dex Step execution ID stamped on every event.
        """
        self._write_agent_event = write_agent_event
        self._turn_id = turn_id
        self._attempt = attempt
        self._step_execution_id = step_execution_id
        try:
            self._loop: asyncio.AbstractEventLoop | None = asyncio.get_running_loop()
        except RuntimeError:
            self._loop = None
        self._loop_thread_id = threading.get_ident()

    @property
    def turn_id(self) -> str | None:
        """Return the turn ID stamped on every event.

        Returns:
            ``"turn-<n>"``, or ``None`` outside a turn.
        """
        return self._turn_id

    @property
    def attempt(self) -> int:
        """Return the Step attempt stamped on every event.

        Returns:
            The one-based attempt of the Step that owns this writer.
        """
        return self._attempt

    @property
    def step_execution_id(self) -> str:
        """Return the Dex Step execution ID stamped on every event.

        Returns:
            The ID shared by every attempt of the Step that owns this writer.
        """
        return self._step_execution_id

    def write_event(self, event_type: AgentEventType | str, payload: Mapping[str, object]) -> None:
        """Write one event with a JSON object payload.

        Prefer the typed methods below for the standard event types. Use this
        method for an adapter-specific type; give it an adapter prefix, such as
        ``"openai_agents.handoff"``, so it never collides with a standard type.

        Args:
            event_type: Event type stored in ``AgentEvent.type``.
            payload: JSON-compatible object stored as ``AgentEvent.payload`` text.

        Raises:
            TypeError: If ``payload`` is not JSON-serializable.
            RuntimeError: If called from another thread after the writer's
                event loop closed.
        """
        event = AgentEvent(
            type=str(event_type),
            turn_id=self._turn_id,
            attempt=self._attempt,
            payload=json.dumps(dict(payload), ensure_ascii=False, sort_keys=True),
            step_execution_id=self._step_execution_id,
        )
        if self._loop is None or threading.get_ident() == self._loop_thread_id:
            self._write_agent_event(event)
            return
        # The Dex Step output queue belongs to the event loop and is not thread-safe.
        self._loop.call_soon_threadsafe(self._write_agent_event, event)

    def write_text_delta(self, text: str) -> None:
        """Write a chunk of visible model text.

        Every call writes one Stream message. Batch very small chunks when the
        framework emits one per token.

        Args:
            text: Text chunk, written as ``{"text": text}``.
        """
        if text:
            self.write_event(AgentEventType.TEXT_DELTA, {"text": text})

    def write_reasoning_delta(self, text: str) -> None:
        """Write a chunk of a provider-authored reasoning summary.

        Call it only for an explicit provider reasoning-summary event. Never
        label ordinary model text as reasoning.

        Args:
            text: Reasoning summary chunk, written as ``{"text": text}``.
        """
        if text:
            self.write_event(AgentEventType.REASONING_DELTA, {"text": text})

    def write_tool_call_started(self, tool_call_id: str, tool_name: str) -> None:
        """Report that a tool started running.

        The session writes this event for deferred tool calls. A runner writes
        it for READ tools that run inside the turn.

        Args:
            tool_call_id: Framework tool call ID.
            tool_name: Tool name.
        """
        self.write_event(
            AgentEventType.TOOL_CALL_STARTED,
            {"tool_call_id": tool_call_id, "tool_name": tool_name},
        )

    def write_tool_call_finished(
        self,
        tool_call_id: str,
        tool_name: str,
        status: ToolCallStatus,
    ) -> None:
        """Report that a tool finished running.

        Args:
            tool_call_id: Framework tool call ID.
            tool_name: Tool name.
            status: ``SUCCEEDED`` or ``FAILED`` for a call that ran, or another
                final status the session recorded.
        """
        self.write_event(
            AgentEventType.TOOL_CALL_FINISHED,
            {"tool_call_id": tool_call_id, "tool_name": tool_name, "status": str(status)},
        )
