# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Claude Code transcript entries as stored session messages and run state.

The Claude Code CLI writes one JSON object per transcript line. The adapter
stores those entries, unchanged, inside ``AgentMessage.framework_item`` and
inside ``FrameworkRunState``, and hands them back to the CLI on the next turn.
"""

from __future__ import annotations

import json
from collections.abc import Callable, Iterable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any

from dex_agents import AgentMessage, FrameworkMessage, MessageRole

#: ``framework_item`` ``kind`` of a user message the session stored before its turn ran.
USER_PROMPT_ITEM_KIND = "user_prompt"
#: ``framework_item`` ``kind`` of a message that carries Claude Code transcript entries.
CLAUDE_TRANSCRIPT_ITEM_KIND = "claude_transcript"
RUN_STATE_FORMAT_VERSION = 1

TranscriptEntry = dict[str, Any]


@dataclass(frozen=True)
class ClaudeSessionTranscript:
    session_id: str | None
    entries: list[TranscriptEntry] = field(default_factory=list)


@dataclass(frozen=True)
class SuspendedClaudeTurn:
    session_id: str
    turn_entries: list[TranscriptEntry]


def encode_user_prompt_item(text: str) -> str:
    return json.dumps({"kind": USER_PROMPT_ITEM_KIND, "text": text}, ensure_ascii=False)


def build_turn_prompt_content(
    messages: Sequence[AgentMessage], turn_id: str
) -> str | list[dict[str, str]]:
    # Prompts and turn-failure notes after the last transcript never reached the CLI.
    unsent_prompt_texts: list[str] = []
    for message in messages:
        item = json.loads(message.framework_item)
        if item.get("kind") == CLAUDE_TRANSCRIPT_ITEM_KIND:
            unsent_prompt_texts = []
        elif item.get("kind") == USER_PROMPT_ITEM_KIND:
            unsent_prompt_texts.append(item["text"])
    current_prompt = messages[-1] if messages else None
    if (
        current_prompt is None
        or current_prompt.role is not MessageRole.USER
        or current_prompt.turn_id != turn_id
        or not unsent_prompt_texts
    ):
        raise RuntimeError(f"turn {turn_id} does not end with its stored prompt")
    if len(unsent_prompt_texts) == 1:
        return unsent_prompt_texts[0]
    return [{"type": "text", "text": text} for text in unsent_prompt_texts]


def load_session_transcript(messages: Iterable[AgentMessage]) -> ClaudeSessionTranscript:
    session_id: str | None = None
    entries: list[TranscriptEntry] = []
    for message in messages:
        item = json.loads(message.framework_item)
        if item.get("kind") != CLAUDE_TRANSCRIPT_ITEM_KIND:
            continue
        session_id = item["session_id"]
        entries.extend(item["entries"])
    return ClaudeSessionTranscript(session_id, entries)


def encode_run_state(session_id: str, turn_entries: Sequence[TranscriptEntry]) -> str:
    return json.dumps(
        {
            "version": RUN_STATE_FORMAT_VERSION,
            "session_id": session_id,
            "turn_entries": list(turn_entries),
        },
        ensure_ascii=False,
    )


def decode_run_state(framework_run_state: str) -> SuspendedClaudeTurn:
    state = json.loads(framework_run_state)
    if state.get("version") != RUN_STATE_FORMAT_VERSION:
        raise ValueError(f"unsupported Claude run state version {state.get('version')!r}")
    return SuspendedClaudeTurn(state["session_id"], list(state["turn_entries"]))


def project_turn_entries(
    session_id: str,
    turn_entries: Sequence[TranscriptEntry],
    policy_tool_name: Callable[[str], str],
) -> list[FrameworkMessage]:
    # Each assistant API message and each tool result becomes one message;
    # other entries ride along with the next one, or with the last one.
    tool_names_by_id: dict[str, str] = {}
    groups: list[_TranscriptEntryGroup] = []
    leading_entries: list[TranscriptEntry] = []
    for entry in turn_entries:
        principal_kind = _principal_kind(entry)
        if principal_kind is None:
            leading_entries.append(entry)
            continue
        previous = groups[-1] if groups else None
        if (
            principal_kind is MessageRole.ASSISTANT
            and not leading_entries
            and previous is not None
            and previous.role is MessageRole.ASSISTANT
            and previous.message_id is not None
            and previous.message_id == _assistant_message_id(entry)
        ):
            previous.entries.append(entry)
            continue
        groups.append(
            _TranscriptEntryGroup(
                role=principal_kind,
                message_id=_assistant_message_id(entry),
                entries=[*leading_entries, entry],
            )
        )
        leading_entries = []
    if leading_entries:
        if groups:
            groups[-1].entries.extend(leading_entries)
        else:
            groups.append(_TranscriptEntryGroup(MessageRole.ASSISTANT, None, leading_entries))
    return [
        group.to_framework_message(session_id, tool_names_by_id, policy_tool_name)
        for group in groups
    ]


@dataclass
class _TranscriptEntryGroup:
    role: MessageRole
    message_id: str | None
    entries: list[TranscriptEntry]

    def to_framework_message(
        self,
        session_id: str,
        tool_names_by_id: dict[str, str],
        policy_tool_name: Callable[[str], str],
    ) -> FrameworkMessage:
        framework_item = json.dumps(
            {
                "kind": CLAUDE_TRANSCRIPT_ITEM_KIND,
                "session_id": session_id,
                "entries": self.entries,
            },
            ensure_ascii=False,
        )
        principal_blocks = [
            block
            for entry in self.entries
            if _principal_kind(entry) is self.role
            for block in _content_blocks(entry)
        ]
        if self.role is MessageRole.ASSISTANT:
            return _assistant_message(
                framework_item, principal_blocks, tool_names_by_id, policy_tool_name
            )
        return _tool_result_message(framework_item, principal_blocks, tool_names_by_id)


def _assistant_message(
    framework_item: str,
    blocks: list[Mapping[str, Any]],
    tool_names_by_id: dict[str, str],
    policy_tool_name: Callable[[str], str],
) -> FrameworkMessage:
    texts = [str(block.get("text", "")) for block in blocks if block.get("type") == "text"]
    tool_uses = [block for block in blocks if block.get("type") == "tool_use"]
    for tool_use in tool_uses:
        tool_names_by_id[str(tool_use["id"])] = policy_tool_name(str(tool_use["name"]))
    single_tool_use = tool_uses[0] if len(tool_uses) == 1 else None
    return FrameworkMessage(
        role=MessageRole.ASSISTANT,
        framework_item=framework_item,
        text="".join(texts) if texts else None,
        tool_call_id=None if single_tool_use is None else str(single_tool_use["id"]),
        tool_name=None if single_tool_use is None else tool_names_by_id[str(single_tool_use["id"])],
        tool_arguments=None
        if single_tool_use is None
        else json.dumps(single_tool_use.get("input", {}), ensure_ascii=False),
    )


def _tool_result_message(
    framework_item: str,
    blocks: list[Mapping[str, Any]],
    tool_names_by_id: dict[str, str],
) -> FrameworkMessage:
    tool_results = [block for block in blocks if block.get("type") == "tool_result"]
    single_result = tool_results[0] if len(tool_results) == 1 else None
    if single_result is None:
        return FrameworkMessage(role=MessageRole.TOOL, framework_item=framework_item)
    tool_call_id = str(single_result["tool_use_id"])
    return FrameworkMessage(
        role=MessageRole.TOOL,
        framework_item=framework_item,
        tool_call_id=tool_call_id,
        tool_name=tool_names_by_id.get(tool_call_id),
        tool_output=tool_result_text(single_result.get("content")),
    )


def tool_result_text(content: object) -> str:
    if content is None:
        return ""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts: list[str] = []
        for block in content:
            if isinstance(block, Mapping) and block.get("type") == "text":
                parts.append(str(block.get("text", "")))
            elif isinstance(block, Mapping):
                parts.append(f"[{block.get('type', 'unknown')} content omitted]")
        return "\n".join(parts)
    return json.dumps(content, ensure_ascii=False)


def _principal_kind(entry: Mapping[str, Any]) -> MessageRole | None:
    entry_type = entry.get("type")
    if entry_type not in ("assistant", "user"):
        return None
    if entry.get("isMeta") or entry.get("isSidechain") or entry.get("teamName"):
        return None
    if entry_type == "assistant":
        return MessageRole.ASSISTANT
    blocks = _content_blocks(entry)
    if any(block.get("type") == "tool_result" for block in blocks):
        return MessageRole.TOOL
    return None


def _content_blocks(entry: Mapping[str, Any]) -> list[Mapping[str, Any]]:
    message = entry.get("message")
    if not isinstance(message, Mapping):
        return []
    content = message.get("content")
    if not isinstance(content, list):
        return []
    return [block for block in content if isinstance(block, Mapping)]


def _assistant_message_id(entry: Mapping[str, Any]) -> str | None:
    if entry.get("type") != "assistant":
        return None
    message = entry.get("message")
    if not isinstance(message, Mapping):
        return None
    message_id = message.get("id")
    return message_id if isinstance(message_id, str) else None
