# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Transcripts that adapter 0.4.0 stored with ``last-prompt`` entries resume without them.

These drive the same calls ``run_model_step`` makes to rebuild a segment's
transcript from committed messages and run state; no CLI run can store a
leaf pointer any more.
"""

from __future__ import annotations

import json
from typing import Any

from claude_agent_sdk import SessionKey

from dex_agents import AgentMessage, MessageRole
from dex_agents_claude_agent_sdk.session_store import TurnSegmentSessionStore
from dex_agents_claude_agent_sdk.transcript import (
    CLAUDE_TRANSCRIPT_ITEM_KIND,
    decode_run_state,
    encode_run_state,
    load_session_transcript,
    project_turn_entries,
)

SESSION_ID = "6f0c5f8e-2f5d-4f4e-9b1e-2d6a0c1f9a11"


async def test_committed_messages_and_run_state_resume_without_leaf_pointers() -> None:
    committed_entries = [
        _entry("user", "u1", None),
        _entry("assistant", "a1", "u1"),
        {"type": "last-prompt", "leafUuid": "u1", "sessionId": SESSION_ID},
        _entry("user", "u2", "a1"),
        _entry("assistant", "a2", "u2"),
    ]
    stored_message = AgentMessage(
        sequence=1,
        turn_id="turn-1",
        role=MessageRole.ASSISTANT,
        framework_item=json.dumps(
            {
                "kind": CLAUDE_TRANSCRIPT_ITEM_KIND,
                "session_id": SESSION_ID,
                "entries": committed_entries,
            }
        ),
        created_at="2026-11-10T00:00:00Z",
    )
    run_state = encode_run_state(
        SESSION_ID,
        [
            _entry("user", "u3", "a2"),
            {"type": "last-prompt", "leafUuid": "a2", "sessionId": SESSION_ID},
            _entry("assistant", "a3", "u3"),
        ],
    )

    committed = load_session_transcript([stored_message])
    suspended = decode_run_state(run_state)
    store = TurnSegmentSessionStore(SESSION_ID, [*committed.entries, *suspended.turn_entries])
    replayed = await store.load(SessionKey(project_key="workspace", session_id=SESSION_ID))

    assert replayed is not None
    assert [entry["uuid"] for entry in replayed] == ["u1", "a1", "u2", "a2", "u3", "a3"]
    # Entries of the suspended turn are stored again when it completes; the pointer must not return.
    stored_again = project_turn_entries(SESSION_ID, suspended.turn_entries, lambda name: name)
    assert all(
        entry["type"] != "last-prompt"
        for message in stored_again
        for entry in json.loads(message.framework_item)["entries"]
    )


def _entry(entry_type: str, entry_uuid: str, parent_uuid: str | None) -> dict[str, Any]:
    message: dict[str, Any] = (
        {"role": "user", "content": f"prompt {entry_uuid}"}
        if entry_type == "user"
        else {
            "id": f"msg_{entry_uuid}",
            "role": "assistant",
            "content": [{"type": "text", "text": f"answer {entry_uuid}"}],
        }
    )
    return {
        "type": entry_type,
        "uuid": entry_uuid,
        "parentUuid": parent_uuid,
        "sessionId": SESSION_ID,
        "message": message,
    }
