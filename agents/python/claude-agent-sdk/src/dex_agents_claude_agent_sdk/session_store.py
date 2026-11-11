# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A ``SessionStore`` that serves committed Dex state and collects one segment's entries."""

from __future__ import annotations

from collections.abc import Sequence
from typing import cast

from claude_agent_sdk import SessionKey, SessionStore, SessionStoreEntry

from dex_agents_claude_agent_sdk.transcript import LEAF_POINTER_ENTRY_TYPE, TranscriptEntry
from dex_agents_claude_agent_sdk.transcript_images import TranscriptImageCodec


class TurnSegmentSessionStore(SessionStore):
    # The Protocol's own defaults tell the SDK these optional methods are unimplemented.
    list_sessions = SessionStore.list_sessions
    list_session_summaries = SessionStore.list_session_summaries
    delete = SessionStore.delete
    list_subkeys = SessionStore.list_subkeys

    def __init__(
        self,
        session_id: str | None,
        committed_entries: Sequence[TranscriptEntry],
        image_codec: TranscriptImageCodec | None = None,
    ) -> None:
        self._resumed_session_id = session_id
        # Committed entries hold image references; the CLI reads them with the bytes restored.
        self._image_codec = image_codec
        self._committed_entries = list(committed_entries)
        self._known_entry_uuids = {
            entry["uuid"] for entry in committed_entries if isinstance(entry.get("uuid"), str)
        }
        self._appended_entries_by_session: dict[str, list[TranscriptEntry]] = {}

    async def append(self, key: SessionKey, entries: list[SessionStoreEntry]) -> None:
        # Subagent transcripts are not stored; resume needs only the main transcript.
        if key.get("subpath"):
            return
        session_entries = self._appended_entries_by_session.setdefault(key["session_id"], [])
        for entry in entries:
            # A leaf pointer from an earlier segment would resume a later one at a stale message.
            if entry.get("type") == LEAF_POINTER_ENTRY_TYPE:
                continue
            entry_uuid = entry.get("uuid")
            # Retried mirror batches may overlap; the SDK asks stores to dedupe by uuid.
            if isinstance(entry_uuid, str) and entry_uuid in self._known_entry_uuids:
                continue
            # Stripped before the uuid counts as known, so a failed upload's retry stores the entry.
            stored_entry = (
                dict(entry)
                if self._image_codec is None
                else await self._image_codec.strip_image_bytes(entry)
            )
            if isinstance(entry_uuid, str):
                self._known_entry_uuids.add(entry_uuid)
            session_entries.append(stored_entry)

    async def load(self, key: SessionKey) -> list[SessionStoreEntry] | None:
        if key.get("subpath") or key["session_id"] != self._resumed_session_id:
            return None
        if not self._committed_entries:
            return None
        entries = [cast(SessionStoreEntry, dict(entry)) for entry in self._committed_entries]
        if self._image_codec is None:
            return entries
        restored: list[SessionStoreEntry] = await self._image_codec.restore_image_bytes(entries)
        return restored

    def take_segment_transcript(self) -> tuple[str, list[TranscriptEntry]]:
        if not self._appended_entries_by_session:
            raise RuntimeError(
                "the Claude Code CLI mirrored no transcript entries; the adapter needs "
                "session mirroring (--session-mirror) to store the conversation"
            )
        if len(self._appended_entries_by_session) > 1:
            session_ids = ", ".join(sorted(self._appended_entries_by_session))
            raise RuntimeError(f"the Claude Code CLI wrote more than one session: {session_ids}")
        ((session_id, entries),) = self._appended_entries_by_session.items()
        if self._resumed_session_id is not None and session_id != self._resumed_session_id:
            raise RuntimeError(
                f"the Claude Code CLI resumed session {self._resumed_session_id} "
                f"but wrote session {session_id}"
            )
        return session_id, entries
