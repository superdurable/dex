# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Assertions on the Claude transcript entries that a session stored."""

from __future__ import annotations

import json
from collections.abc import Iterable, Sequence
from typing import Any, Protocol

from dex_agents import (
    TURN_CANCELLED_NOTE_HEADER,
    TURN_FAILURE_NOTE_HEADER,
    TURN_STEERED_NOTE_HEADER,
    AgentMessage,
    AgentMessageOrigin,
)
from dex_agents_claude_agent_sdk import CLAUDE_TRANSCRIPT_ITEM_KIND


class _MessageWithFrameworkItem(Protocol):
    @property
    def framework_item(self) -> str: ...


def assert_no_turn_note(messages: Sequence[AgentMessage]) -> None:
    """Assert that no stored message is a steered, cancelled, or failed turn's note."""
    for message in messages:
        assert message.origin not in (
            AgentMessageOrigin.TURN_STEERED_NOTE,
            AgentMessageOrigin.TURN_CANCELLED_NOTE,
        )
        for header in (
            TURN_STEERED_NOTE_HEADER,
            TURN_CANCELLED_NOTE_HEADER,
            TURN_FAILURE_NOTE_HEADER,
        ):
            assert not (message.text or "").startswith(header), f"a note was stored: {message.text}"


def stored_transcript_entries(
    messages: Iterable[_MessageWithFrameworkItem],
) -> list[dict[str, Any]]:
    """Return the Claude transcript entries of every message, in order."""
    entries: list[dict[str, Any]] = []
    for message in messages:
        item = json.loads(message.framework_item)
        if item.get("kind") == CLAUDE_TRANSCRIPT_ITEM_KIND:
            entries.extend(item.get("entries", []))
    return entries


def assert_unique_transcript_entries(messages: Sequence[AgentMessage]) -> None:
    """Assert that every stored transcript entry with a ``uuid`` is stored once."""
    uuids = [
        entry["uuid"]
        for entry in stored_transcript_entries(messages)
        if isinstance(entry.get("uuid"), str)
    ]
    assert uuids, "no transcript entry was stored"
    assert len(set(uuids)) == len(uuids), "a transcript entry was stored twice"


def deferrals_of(messages: Sequence[AgentMessage], tool_use_id: str) -> int:
    """Return how many stored deferral attachments name the tool use."""
    return sum(
        1
        for entry in stored_transcript_entries(messages)
        if isinstance(entry.get("attachment"), dict)
        and entry["attachment"].get("type") == "hook_deferred_tool"
        and entry["attachment"].get("toolUseID") == tool_use_id
    )


def results_of(messages: Sequence[AgentMessage], tool_use_id: str) -> int:
    """Return how many stored tool results answer the tool use."""
    return sum(
        1
        for entry in stored_transcript_entries(messages)
        for block in _content_blocks(entry)
        if block.get("type") == "tool_result" and block.get("tool_use_id") == tool_use_id
    )


def _content_blocks(entry: dict[str, Any]) -> list[dict[str, Any]]:
    message = entry.get("message")
    content = message.get("content") if isinstance(message, dict) else None
    return (
        [block for block in content if isinstance(block, dict)] if isinstance(content, list) else []
    )
