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
The one exception is ``last-prompt`` entries, which it neither stores nor
hands back, including ones that adapter 0.4.0 stored. With a media store,
stored entries hold image references instead of image bytes; see
``transcript_images``.
"""

from __future__ import annotations

import json
import uuid
from collections.abc import Callable, Iterable, Mapping, Sequence
from dataclasses import asdict, dataclass, field
from typing import Any

from dex_agents import AgentImageReference, AgentMessage, FrameworkMessage, MessageRole
from dex_agents_claude_agent_sdk.transcript_images import (
    decode_image_reference,
    image_block,
    image_references_in,
)

#: ``framework_item`` ``kind`` of a user message the session stored before its turn ran.
USER_PROMPT_ITEM_KIND = "user_prompt"
#: ``framework_item`` ``kind`` of a message that carries Claude Code transcript entries.
CLAUDE_TRANSCRIPT_ITEM_KIND = "claude_transcript"
# turn_entries holds the turn's unstored entries, none once the session stores each segment.
RUN_STATE_FORMAT_VERSION = 1
# The TypeScript adapter's namespace, so both adapters derive the same entry uuids.
_UNFINISHED_TOOL_RESULT_UUID_NAMESPACE = uuid.UUID("6f0d3c1e8a2b4f57a9d4c3e2b1a09876")
# The session fields of an assistant entry, which the CLI writes on every entry.
_ENVELOPE_FIELD_NAMES = ("userType", "entrypoint", "cwd", "sessionId", "version", "gitBranch")
# Claude Code's entry that names the leaf a resume continues from.
LEAF_POINTER_ENTRY_TYPE = "last-prompt"

TranscriptEntry = dict[str, Any]


@dataclass(frozen=True)
class ClaudeSessionTranscript:
    session_id: str | None
    entries: list[TranscriptEntry] = field(default_factory=list)


@dataclass(frozen=True)
class SuspendedClaudeTurn:
    session_id: str
    turn_entries: list[TranscriptEntry]


def encode_user_prompt_item(text: str, images: Sequence[AgentImageReference] = ()) -> str:
    item: dict[str, Any] = {"kind": USER_PROMPT_ITEM_KIND, "text": text}
    if images:
        item["images"] = [asdict(image) for image in images]
    return json.dumps(item, ensure_ascii=False)


def build_turn_prompt_content(
    messages: Sequence[AgentMessage], turn_id: str
) -> str | list[dict[str, Any]]:
    """Return the turn's prompt; its image blocks hold references until the runner loads them."""
    # Prompts and turn-failure notes after the last transcript never reached the CLI.
    unsent_prompts: list[tuple[str, list[AgentImageReference]]] = []
    for message in messages:
        item = json.loads(message.framework_item)
        if item.get("kind") == CLAUDE_TRANSCRIPT_ITEM_KIND:
            unsent_prompts = []
        elif item.get("kind") == USER_PROMPT_ITEM_KIND:
            images = [decode_image_reference(fields) for fields in item.get("images", [])]
            unsent_prompts.append((item["text"], images))
    current_prompt = messages[-1] if messages else None
    if (
        current_prompt is None
        or current_prompt.role is not MessageRole.USER
        or current_prompt.turn_id != turn_id
        or not unsent_prompts
    ):
        raise RuntimeError(f"turn {turn_id} does not end with its stored prompt")
    if len(unsent_prompts) == 1 and not unsent_prompts[0][1]:
        return unsent_prompts[0][0]
    return [
        block
        for text, images in unsent_prompts
        for block in [{"type": "text", "text": text}, *(image_block(image) for image in images)]
    ]


def load_session_transcript(messages: Iterable[AgentMessage]) -> ClaudeSessionTranscript:
    session_id: str | None = None
    entries: list[TranscriptEntry] = []
    for message in messages:
        item = json.loads(message.framework_item)
        if item.get("kind") != CLAUDE_TRANSCRIPT_ITEM_KIND:
            continue
        session_id = item["session_id"]
        entries.extend(item["entries"])
    return ClaudeSessionTranscript(session_id, without_leaf_pointers(entries))


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
    return SuspendedClaudeTurn(state["session_id"], without_leaf_pointers(state["turn_entries"]))


def without_leaf_pointers(entries: Iterable[TranscriptEntry]) -> list[TranscriptEntry]:
    """Drop ``last-prompt`` entries: a stored leaf pointer would resume at a stale message."""
    return [entry for entry in entries if entry.get("type") != LEAF_POINTER_ENTRY_TYPE]


def project_turn_entries(
    session_id: str,
    turn_entries: Sequence[TranscriptEntry],
    policy_tool_name: Callable[[str], str],
    stored_turn_entries: Sequence[TranscriptEntry] = (),
) -> list[FrameworkMessage]:
    # Each assistant API message and each tool result becomes one message;
    # other entries ride along with the next one, or with the last one.
    # stored_turn_entries name the tools of results whose tool use the session already stored.
    tool_names_by_id: dict[str, str] = {
        tool_use.tool_use_id: policy_tool_name(tool_use.cli_tool_name)
        for tool_use in _main_tool_uses(stored_turn_entries)
    }
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
        # The images the model saw in this result; stored blocks hold only references.
        images=tuple(image_references_in(single_result.get("content"))),
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


def answered_tool_use_ids(entries: Iterable[Mapping[str, Any]]) -> frozenset[str]:
    """Return the IDs of the tool uses that have a ``tool_result`` in ``entries``."""
    return frozenset(
        block["tool_use_id"]
        for entry in entries
        for block in _content_blocks(entry)
        if block.get("type") == "tool_result" and isinstance(block.get("tool_use_id"), str)
    )


@dataclass(frozen=True)
class UnfinishedToolResult:
    """Content of a tool result that the adapter records for a tool use the CLI left without one."""

    text: str
    is_error: bool
    images: Sequence[AgentImageReference] = ()


@dataclass(frozen=True)
class TranscriptToolUse:
    """A main-conversation tool use of a turn transcript."""

    tool_use_id: str
    cli_tool_name: str


@dataclass(frozen=True)
class _MainToolUse(TranscriptToolUse):
    assistant_uuid: str | None
    timestamp: object
    envelope: Mapping[str, Any]


def build_unfinished_tool_result_entries(
    session_id: str,
    turn_entries: Sequence[TranscriptEntry],
    describe_result: Callable[[TranscriptToolUse], UnfinishedToolResult],
) -> list[TranscriptEntry]:
    """Return one CLI-shaped tool result entry per unanswered tool use; rebuilding is exact."""
    answered_ids = answered_tool_use_ids(turn_entries)
    unanswered_tool_uses = [
        tool_use
        for tool_use in _main_tool_uses(turn_entries)
        if tool_use.tool_use_id not in answered_ids
    ]
    if not unanswered_tool_uses:
        return []
    conversation_entries = [
        entry
        for entry in turn_entries
        if entry.get("type") in ("user", "assistant") and isinstance(entry.get("uuid"), str)
    ]
    timestamps = [
        entry["timestamp"] for entry in turn_entries if isinstance(entry.get("timestamp"), str)
    ]
    timestamp = max(timestamps) if timestamps else None
    # The CLI chains a deferred call's result to the last conversation entry, skipping attachments.
    parent_uuid: object = conversation_entries[-1]["uuid"] if conversation_entries else None
    result_entries: list[TranscriptEntry] = []
    for tool_use in unanswered_tool_uses:
        entry_uuid = _name_based_uuid(f"{session_id}/{tool_use.tool_use_id}")
        result_entries.append(
            _build_tool_result_entry(
                tool_use, describe_result(tool_use), entry_uuid, parent_uuid, timestamp
            )
        )
        parent_uuid = entry_uuid
    return result_entries


@dataclass(frozen=True)
class ResumeTranscript:
    """The transcript a segment's CLI resumes from, and the answers the session has not stored."""

    entries: list[TranscriptEntry]
    new_answer_entries: list[TranscriptEntry]


def build_resume_transcript(
    session_id: str | None,
    entries: Sequence[TranscriptEntry],
    ended_turn_entries: Sequence[TranscriptEntry],
    answer_text: str,
) -> ResumeTranscript:
    """Answer every ended turn's unanswered tool use in place; see "Ended turns" in the README."""
    if session_id is None:
        return ResumeTranscript(list(entries), [])
    answered_ids = answered_tool_use_ids(entries)
    new_answer_entries = [
        _build_tool_result_entry(
            tool_use,
            UnfinishedToolResult(answer_text, is_error=True),
            _ended_turn_answer_uuid(session_id, tool_use.tool_use_id),
            tool_use.assistant_uuid,
            tool_use.timestamp,
        )
        for tool_use in _main_tool_uses(ended_turn_entries)
        if tool_use.tool_use_id not in answered_ids
    ]
    stored_answer_entries = [entry for entry in entries if _is_ended_turn_answer(session_id, entry)]
    if not new_answer_entries and not stored_answer_entries:
        return ResumeTranscript(list(entries), [])
    return ResumeTranscript(
        _place_ended_turn_answers(entries, [*stored_answer_entries, *new_answer_entries]),
        new_answer_entries,
    )


def _place_ended_turn_answers(
    entries: Sequence[TranscriptEntry], answer_entries: Sequence[TranscriptEntry]
) -> list[TranscriptEntry]:
    # Moves each answer after its tool use and drops the answered calls' deferral attachments.
    answer_uuids = {_value_key(_field(entry, "uuid")) for entry in answer_entries}
    answered_ids = answered_tool_use_ids(answer_entries)
    answers_by_assistant_uuid: dict[tuple[Any, ...], list[TranscriptEntry]] = {}
    for answer in answer_entries:
        answers_by_assistant_uuid.setdefault(
            _value_key(_field(answer, "sourceToolAssistantUUID")), []
        ).append(answer)
    parent_of_dropped_uuid: dict[tuple[Any, ...], object] = {
        _value_key(_field(entry, "uuid")): _field(entry, "parentUuid")
        for entry in entries
        if _is_deferral_attachment_of(entry, answered_ids)
    }
    last_answer_uuid_by_assistant_uuid: dict[tuple[Any, ...], object] = {}
    placed_entries: list[TranscriptEntry] = []
    for entry in entries:
        entry_uuid_key = _value_key(_field(entry, "uuid"))
        if entry_uuid_key in answer_uuids or entry_uuid_key in parent_of_dropped_uuid:
            continue
        original_parent_uuid = _field(entry, "parentUuid")
        parent_uuid = original_parent_uuid
        visited_keys: set[tuple[Any, ...]] = set()
        while (parent_key := _value_key(parent_uuid)) in parent_of_dropped_uuid:
            if parent_key in visited_keys:
                break
            visited_keys.add(parent_key)
            parent_uuid = parent_of_dropped_uuid[parent_key]
        # Later blocks and results of the reply stay; what continued the conversation follows.
        is_continuation = entry.get("type") != "assistant" and all(
            block.get("type") != "tool_result" for block in _content_blocks(entry)
        )
        if is_continuation and _value_key(parent_uuid) in last_answer_uuid_by_assistant_uuid:
            parent_uuid = last_answer_uuid_by_assistant_uuid[_value_key(parent_uuid)]
        placed_entries.append(
            entry
            if _value_key(parent_uuid) == _value_key(original_parent_uuid)
            else {**entry, "parentUuid": parent_uuid}
        )
        entry_uuid = entry.get("uuid")
        answers = (
            answers_by_assistant_uuid.get(_value_key(entry_uuid))
            if isinstance(entry_uuid, str)
            else None
        )
        if answers is not None:
            answer_parent_uuid: object = entry_uuid
            for answer in answers:
                placed_entries.append({**answer, "parentUuid": answer_parent_uuid})
                answer_parent_uuid = answer["uuid"]
            last_answer_uuid_by_assistant_uuid[_value_key(entry_uuid)] = answer_parent_uuid
    return placed_entries


def _is_deferral_attachment_of(entry: Mapping[str, Any], tool_use_ids: frozenset[str]) -> bool:
    attachment = entry.get("attachment")
    return (
        entry.get("type") == "attachment"
        and isinstance(attachment, Mapping)
        and attachment.get("type") == "hook_deferred_tool"
        and _js_string(_field(attachment, "toolUseID")) in tool_use_ids
    )


def _is_ended_turn_answer(session_id: str, entry: Mapping[str, Any]) -> bool:
    # An answer's uuid names its tool use, so the answer is recognized among the stored entries.
    if entry.get("type") != "user" or not isinstance(entry.get("uuid"), str):
        return False
    blocks = _content_blocks(entry)
    if len(blocks) != 1 or blocks[0].get("type") != "tool_result":
        return False
    tool_use_id = _js_string(_field(blocks[0], "tool_use_id"))
    return tool_use_id is not None and entry["uuid"] == _ended_turn_answer_uuid(
        session_id, tool_use_id
    )


def _ended_turn_answer_uuid(session_id: str, tool_use_id: str) -> str:
    return _name_based_uuid(f"{session_id}/ended-turn/{tool_use_id}")


def _build_tool_result_entry(
    tool_use: _MainToolUse,
    result: UnfinishedToolResult,
    entry_uuid: str,
    parent_uuid: object,
    timestamp: object,
) -> TranscriptEntry:
    # The TypeScript adapter writes these keys in the same order, so both store the same JSON.
    content: str | list[dict[str, Any]] = (
        [{"type": "text", "text": result.text}, *(image_block(image) for image in result.images)]
        if result.images
        else result.text
    )
    tool_result: dict[str, Any] = {
        "tool_use_id": tool_use.tool_use_id,
        "type": "tool_result",
        "content": content,
    }
    if result.is_error:
        tool_result["is_error"] = True
    entry: TranscriptEntry = {
        "parentUuid": parent_uuid,
        "isSidechain": False,
        "type": "user",
        "message": {"role": "user", "content": [tool_result]},
        "uuid": entry_uuid,
    }
    if isinstance(timestamp, str):
        entry["timestamp"] = timestamp
    entry["toolUseResult"] = f"Error: {result.text}" if result.is_error else result.text
    entry["sourceToolAssistantUUID"] = tool_use.assistant_uuid
    entry.update(tool_use.envelope)
    return entry


def _main_tool_uses(entries: Iterable[Mapping[str, Any]]) -> list[_MainToolUse]:
    tool_uses: list[_MainToolUse] = []
    for entry in entries:
        if _principal_kind(entry) is not MessageRole.ASSISTANT:
            continue
        envelope = {name: entry[name] for name in _ENVELOPE_FIELD_NAMES if name in entry}
        assistant_uuid = entry.get("uuid") if isinstance(entry.get("uuid"), str) else None
        tool_uses.extend(
            _MainToolUse(
                tool_use_id=block["id"],
                cli_tool_name=_js_string(_field(block, "name")) or "",
                assistant_uuid=assistant_uuid,
                timestamp=_field(entry, "timestamp"),
                envelope=envelope,
            )
            for block in _content_blocks(entry)
            if block.get("type") == "tool_use" and isinstance(block.get("id"), str)
        )
    return tool_uses


def _name_based_uuid(name: str) -> str:
    # A name-based (version 5) UUID, so a rebuilt entry keeps its uuid.
    return str(uuid.uuid5(_UNFINISHED_TOOL_RESULT_UUID_NAMESPACE, name))


class _Missing:
    """A JSON field that is absent, which the TypeScript adapter reads as ``undefined``."""


_MISSING = _Missing()


def _field(entry: Mapping[str, Any], name: str) -> object:
    return entry.get(name, _MISSING)


def _value_key(value: object) -> tuple[Any, ...]:
    # Compares like the TypeScript adapter's Map and ===: strings by value, objects by identity.
    if value is _MISSING:
        return ("undefined",)
    if value is None:
        return ("null",)
    if isinstance(value, bool):
        return ("boolean", value)
    if isinstance(value, int | float):
        return ("number", value)
    if isinstance(value, str):
        return ("string", value)
    return ("object", id(value))


def _js_string(value: object) -> str | None:
    # JavaScript String() of a JSON value; None for a value whose text the adapter never compares.
    if isinstance(value, str):
        return value
    if value is _MISSING:
        return "undefined"
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, int):
        return str(value)
    return None


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
