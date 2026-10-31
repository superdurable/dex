# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Forwarding of streamed model text to the ``AgentEvents`` Stream."""

from __future__ import annotations

import asyncio
import contextlib
from datetime import timedelta
from enum import Enum
from typing import Any

from agents import RawResponsesStreamEvent, RunResultStreaming

from dex_agents import AgentEventWriter

# Each delta is one Stream message, so short token deltas are coalesced first.
DELTA_FLUSH_CHARACTERS = 64
# A model that pauses mid-answer still shows its text after this delay.
DELTA_FLUSH_INTERVAL = timedelta(milliseconds=100)
_TEXT_DELTA_EVENT_TYPE = "response.output_text.delta"
_REASONING_SUMMARY_DELTA_EVENT_TYPE = "response.reasoning_summary_text.delta"


class _DeltaKind(Enum):
    TEXT = "text"
    REASONING_SUMMARY = "reasoning_summary"


class _CoalescingDeltaWriter:
    def __init__(self, events: AgentEventWriter) -> None:
        self._events = events
        self._kind: _DeltaKind | None = None
        self._chunks: list[str] = []
        self._size = 0

    @property
    def has_buffered_text(self) -> bool:
        return bool(self._chunks)

    def add(self, kind: _DeltaKind, text: str) -> None:
        if kind is not self._kind:
            self.flush()
            self._kind = kind
        self._chunks.append(text)
        self._size += len(text)
        if self._size >= DELTA_FLUSH_CHARACTERS:
            self.flush()

    def flush(self) -> None:
        text = "".join(self._chunks)
        if self._kind is _DeltaKind.TEXT:
            self._events.write_text_delta(text)
        elif self._kind is _DeltaKind.REASONING_SUMMARY:
            self._events.write_reasoning_delta(text)
        self._chunks = []
        self._size = 0


async def forward_stream_events(result: RunResultStreaming, events: AgentEventWriter) -> None:
    writer = _CoalescingDeltaWriter(events)
    stream_events = result.stream_events()
    next_event: asyncio.Future[Any] | None = None
    try:
        while True:
            if next_event is None:
                next_event = asyncio.ensure_future(anext(stream_events))
            if writer.has_buffered_text:
                done, _ = await asyncio.wait(
                    {next_event}, timeout=DELTA_FLUSH_INTERVAL.total_seconds()
                )
                if not done:
                    writer.flush()
                    continue
            try:
                event = await next_event
            except StopAsyncIteration:
                break
            finally:
                next_event = None
            _forward_stream_event(writer, event)
    finally:
        if next_event is not None:
            next_event.cancel()
            with contextlib.suppress(asyncio.CancelledError, StopAsyncIteration):
                await next_event
        writer.flush()


def _forward_stream_event(writer: _CoalescingDeltaWriter, event: Any) -> None:
    if not isinstance(event, RawResponsesStreamEvent):
        writer.flush()
        return
    event_type = getattr(event.data, "type", None)
    delta = getattr(event.data, "delta", None)
    if event_type == _TEXT_DELTA_EVENT_TYPE and isinstance(delta, str):
        writer.add(_DeltaKind.TEXT, delta)
    elif event_type == _REASONING_SUMMARY_DELTA_EVENT_TYPE and isinstance(delta, str):
        writer.add(_DeltaKind.REASONING_SUMMARY, delta)
    else:
        writer.flush()
