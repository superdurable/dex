# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The transcript a resumed CLI starts from when a refused sibling follows the deferral."""

from __future__ import annotations

from typing import Any

from dex_agents_claude_agent_sdk.transcript import with_pending_deferral_last

REPLY_MESSAGE_ID = "msg_reply"


def _prompt(uuid: str) -> dict[str, Any]:
    return {
        "type": "user",
        "uuid": uuid,
        "parentUuid": None,
        "message": {"role": "user", "content": "refund order A-1001"},
    }


def _tool_use(
    uuid: str, parent_uuid: str, tool_use_id: str, message_id: str = REPLY_MESSAGE_ID
) -> dict[str, Any]:
    return {
        "type": "assistant",
        "uuid": uuid,
        "parentUuid": parent_uuid,
        "message": {
            "id": message_id,
            "role": "assistant",
            "content": [
                {"type": "tool_use", "id": tool_use_id, "name": "mcp__tools__w", "input": {}}
            ],
        },
    }


def _deferral(uuid: str, parent_uuid: str, tool_use_id: str) -> dict[str, Any]:
    return {
        "type": "attachment",
        "uuid": uuid,
        "parentUuid": parent_uuid,
        "attachment": {"type": "hook_deferred_tool", "toolUseID": tool_use_id},
    }


def _tool_result(uuid: str, parent_uuid: str, tool_use_id: str, source_uuid: str) -> dict[str, Any]:
    return {
        "type": "user",
        "uuid": uuid,
        "parentUuid": parent_uuid,
        "sourceToolAssistantUUID": source_uuid,
        "message": {
            "role": "user",
            "content": [
                {
                    "type": "tool_result",
                    "tool_use_id": tool_use_id,
                    "content": "refused",
                    "is_error": True,
                }
            ],
        },
    }


def _chain(entries: list[dict[str, Any]]) -> list[tuple[Any, Any]]:
    return [(entry.get("uuid"), entry.get("parentUuid")) for entry in entries]


def test_a_pending_deferral_moves_after_its_replys_refused_sibling() -> None:
    # The shape a live model's stream leaves: the deferral is written before the second tool use.
    entries = [
        _prompt("p"),
        _tool_use("a1", "p", "toolu_refund"),
        _deferral("d1", "a1", "toolu_refund"),
        _tool_use("a2", "d1", "toolu_email"),
        _tool_result("r2", "a2", "toolu_email", "a2"),
        {"type": "cost-state", "sessionId": "s"},
    ]
    resumed = with_pending_deferral_last(entries)
    assert _chain(resumed) == [
        ("p", None),
        ("a1", "p"),
        ("a2", "a1"),
        ("r2", "a2"),
        (None, None),
        ("d1", "r2"),
    ]
    # The stored entries never change.
    assert entries[3]["parentUuid"] == "d1"


def test_other_transcripts_are_kept() -> None:
    deferral_last = [
        _prompt("p"),
        _tool_use("a1", "p", "toolu_refund"),
        _tool_use("a2", "a1", "toolu_email"),
        _tool_result("r2", "a2", "toolu_email", "a2"),
        _deferral("d1", "r2", "toolu_refund"),
    ]
    only_attachments_after = [
        _prompt("p"),
        _tool_use("a1", "p", "toolu_refund"),
        _deferral("d1", "a1", "toolu_refund"),
        {
            "type": "attachment",
            "uuid": "s1",
            "parentUuid": "d1",
            "attachment": {"type": "prompt_snapshot"},
        },
    ]
    answered = [
        _prompt("p"),
        _tool_use("a1", "p", "toolu_refund"),
        _deferral("d1", "a1", "toolu_refund"),
        _tool_use("a2", "d1", "toolu_email"),
        _tool_result("r2", "a2", "toolu_email", "a2"),
        _tool_result("r1", "a1", "toolu_refund", "a1"),
    ]
    later_reply = [
        _prompt("p"),
        _tool_use("a1", "p", "toolu_refund"),
        _deferral("d1", "a1", "toolu_refund"),
        _tool_use("a2", "d1", "toolu_email", "msg_other"),
    ]
    meta_after = [
        _prompt("p"),
        _tool_use("a1", "p", "toolu_refund"),
        _deferral("d1", "a1", "toolu_refund"),
        _tool_use("a2", "d1", "toolu_email"),
        {
            "type": "user",
            "uuid": "m1",
            "parentUuid": "a2",
            "isMeta": True,
            "message": {"role": "user", "content": "note"},
        },
    ]
    for entries in (deferral_last, only_attachments_after, answered, later_reply, meta_after):
        assert with_pending_deferral_last(entries) == entries
