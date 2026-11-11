# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Write the Dex Agents conformance suite from the Python core.

Run it from the Python core, so it imports that core's ``dex_agents``:

    cd agents/python/core
    uv run --frozen python ../../conformance/generate.py            # values, names, texts, graph
    uv run --frozen python ../../conformance/generate.py --check    # fail if any file is stale
    DEXCLI=... uv run --frozen python ../../conformance/generate.py --transcripts

``--transcripts`` also runs every scenario in ``transcripts/`` on the Python
core with a private Dex engine and rewrites each scenario's ``expected``.
"""

from __future__ import annotations

import argparse
import asyncio
import hashlib
import json
import sys
from collections.abc import Callable, Mapping, Sequence
from dataclasses import replace
from pathlib import Path
from types import NoneType, UnionType
from typing import Any, Union, get_args, get_origin

from dex import (
    Attribute,
    AttributeMap,
    Channel,
    ChannelMap,
    CodecRegistry,
    Registry,
    Stream,
)
from dex.codec import Codec, Value, WireKind
from mcp_types import CallToolResult

from dex_agents import (
    APPROVAL_DEADLINE_PASSED,
    BLANK_TOOL_TASK_ID,
    BUILT_IN_TOOL_DEFINITIONS,
    CONTEXT_SUMMARY_TRANSCRIPT_HEADER,
    DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    DEFAULT_MAX_DURABLE_WAIT,
    DEFAULT_MESSAGE_CHUNK_MAX_BYTES,
    DEFAULT_MESSAGE_CHUNK_SIZE,
    DEFAULT_MESSAGE_WINDOW_SIZE,
    DEFAULT_REJECTION_TOOL_RESULT,
    DEFAULT_RETAINED_MESSAGE_LIMIT,
    INVALID_TOOL_TASK_POLL_INTERVAL,
    MAX_SESSION_SETTINGS_BYTES,
    MESSAGE_ROLLOVER_MAX_BYTES_PER_COMMIT,
    MESSAGE_ROLLOVER_MAX_CHUNKS_PER_COMMIT,
    MODEL_CALL_REFUSED_ERROR_PREFIX,
    NO_PLAN_NOTE,
    OUTCOME_UNKNOWN_TOOL_RESULT,
    PLANNING_TURN_INSTRUCTION,
    PLANNING_TURN_TOOL_REJECTION,
    QUESTION_DEADLINE_PASSED,
    QUESTION_DECLINED_TOOL_RESULT,
    SESSION_SETTINGS_SIZE_TOLERANCE_BYTES,
    TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT,
    TOOL_CALL_NOT_RUN_ERROR_PREFIX,
    TOOL_TASK_ABANDONED_ERROR,
    TURN_CANCELLED_NOTE_HEADER,
    TURN_FAILURE_NOTE_FOOTER,
    TURN_FAILURE_NOTE_HEADER,
    TURN_STEERED_NOTE_HEADER,
    WRITE_TODOS_CALLED_MORE_THAN_ONCE,
    AgentEvent,
    AgentEventType,
    AgentEventWriter,
    AgentImageReference,
    AgentInterrupt,
    AgentInterruptResolution,
    AgentMessage,
    AgentMessageChunk,
    AgentMessageChunkRange,
    AgentMessageIndex,
    AgentMessagesPage,
    AgentPlan,
    AgentPlanTask,
    AgentSessionStartInput,
    AgentSessionStatus,
    AgentSnapshot,
    AgentState,
    AgentToolCall,
    AgentUsage,
    AgentUserMessage,
    AnswerQuestionRequest,
    BuiltInTool,
    CancelTurnRequest,
    CompactContextInput,
    ContextSummary,
    ContextSummaryRequest,
    DeleteQueuedMessageRequest,
    ExecutePlanRequest,
    InteractionMode,
    InterruptKind,
    ListAgentMessagesRequest,
    MessageRole,
    PendingUserMessage,
    PlanExecutionRequest,
    PlanStatus,
    PlanTaskStatus,
    QuestionAnswer,
    QuestionResolution,
    ResolveInterruptRequest,
    SteerUserMessageRequest,
    ToolCallCancellation,
    ToolCallCancellationReason,
    ToolCallCompleted,
    ToolCallStatus,
    ToolEffect,
    ToolExecutionError,
    TurnCancellationRequest,
    TurnControlKind,
    TurnControlRequest,
    TurnReplySequence,
    TurnSessionSettings,
    UpdateSessionSettingsRequest,
    build_context_summary_prompt,
    build_plan_execution_message,
    build_plan_note,
    build_turn_failure_note,
    describe_tool_call_outcome,
    message_sequence_key,
    plan_execution_key,
    session_settings_size,
)
from dex_agents.built_in_tools import (
    BuiltInToolArgumentsError,
    build_durable_wait_completed_output,
    build_plan_cleared_output,
    build_plan_updated_output,
    build_question_answered_output,
    parse_ask_user_arguments,
    parse_durable_wait_arguments,
    parse_write_todos_arguments,
)
from dex_agents.builtin_loop import (
    CONTEXT_SUMMARY_INSTRUCTIONS,
    CONTEXT_SUMMARY_NOTE_HEADER,
    CONTEXT_SUMMARY_REQUEST,
    DEFAULT_MAX_MODEL_CALLS_PER_TURN,
    TOOL_CALL_LIMIT_REACHED_RESULT,
    AgentDefinition,
    build_context_summary_note,
    build_context_summary_omission_note,
    builtin_agent_session_flow,
)
from dex_agents.builtin_loop.runner import (
    _read_tool_source_envelope,
    _write_tool_source_envelope,
    build_unknown_tool_error,
)
from dex_agents.mcp._resource_and_prompt_tools import (
    build_resource_and_prompt_tools,
    read_server_name,
    run_resource_and_prompt_tool,
)
from dex_agents.mcp._tools import render_tool_result_text, serialize_tool_output_json
from dex_agents.runner import build_tool_call_note
from dex_agents.session_flow import (
    ALL_PENDING_MESSAGES,
    MESSAGE_PAGE_LIMIT,
    MISSING_TOOL_TASK_POLLER,
    SNAPSHOT_MESSAGE_LIMIT,
)
from dex_agents.testing import ScriptedModelClient, ScriptedModelReply

SUITE_DIR = Path(__file__).resolve().parent
VALUES_DIR = SUITE_DIR / "values"
NAMES_FILE = SUITE_DIR / "names.json"
TEXTS_FILE = SUITE_DIR / "texts.json"
SESSION_GRAPH_FILE = SUITE_DIR / "session_graph.json"
#: Each core ships the graph, so an application can render it without this suite.
SESSION_GRAPH_CORE_COPIES = (
    SUITE_DIR.parent / "python/core/src/dex_agents/session_flow_graph.json",
    SUITE_DIR.parent / "typescript/core/src/agent-session-flow-graph.json",
)
SESSION_GRAPH_RENDERINGS_FILE = SUITE_DIR / "session_graph_renderings.json"
TRANSCRIPTS_DIR = SUITE_DIR / "transcripts"

#: Flow type of the session Flow the manifest describes; applications choose their own.
MANIFEST_FLOW_TYPE = "ConformanceAgent"

# Text that pins canonical escaping: quotes, backslash, control characters,
# non-ASCII, an astral character, and the HTML characters Go escapes by default.
TRICKY_TEXT = 'Grüße "quoted" \\ back\nline\ttab ☕ 😀 </script> & <ok>'
# Settings that pin nesting, every JSON kind, and key order.
FULL_SESSION_SETTINGS: dict[str, Any] = {
    "model": "gpt-5-mini",
    "persona": TRICKY_TEXT,
    "temperature": 0.25,
    "max_turns": 12,
    "tools": ["lookup_order", "send_email"],
    "credential": {"name": "support-openai"},
    "verbose": False,
    "fallback_model": None,
}
CREATED_AT = "2026-11-02T09:30:00.000Z"
LATER = "2026-11-02T09:45:30.125Z"
CHART_IMAGE = AgentImageReference(
    uri="s3://agent-media/sessions/sha256/" + "4f" * 32,
    mime_type="image/png",
    sha256="4f" * 32,
    size_bytes=68,
)
PHOTO_IMAGE = AgentImageReference(
    uri="s3://agent-media/sessions/sha256/" + "a7" * 32,
    mime_type="image/jpeg",
    sha256="a7" * 32,
    size_bytes=3_750_000,
)


# ---------------------------------------------------------------------------
# Value codecs and type names
# ---------------------------------------------------------------------------

CODECS = CodecRegistry()

# Language-neutral type name -> Python type hint. Scalars use the wire names.
VALUE_TYPES: dict[str, object] = {
    "AgentImageReference": AgentImageReference,
    "AgentSessionStartInput": AgentSessionStartInput,
    "AgentState": AgentState,
    "AgentMessage": AgentMessage,
    "QuestionAnswer": QuestionAnswer,
    "AgentToolCall": AgentToolCall,
    "AgentInterrupt": AgentInterrupt,
    "AgentInterruptList": list[AgentInterrupt],
    "AgentInterruptResolution": AgentInterruptResolution,
    "ResolveInterruptRequest": ResolveInterruptRequest,
    "AnswerQuestionRequest": AnswerQuestionRequest,
    "AgentUsage": AgentUsage,
    "AgentUserMessage": AgentUserMessage,
    "PendingUserMessage": PendingUserMessage,
    "SteerUserMessageRequest": SteerUserMessageRequest,
    "DeleteQueuedMessageRequest": DeleteQueuedMessageRequest,
    "CancelTurnRequest": CancelTurnRequest,
    "TurnControlRequest": TurnControlRequest,
    "TurnCancellationRequest": TurnCancellationRequest,
    "SessionSettings": dict[str, Any],
    "UpdateSessionSettingsRequest": UpdateSessionSettingsRequest,
    "TurnSessionSettings": TurnSessionSettings,
    "ToolCallCancellation": ToolCallCancellation,
    "ToolCallCompleted": ToolCallCompleted,
    "AgentPlanTask": AgentPlanTask,
    "AgentPlan": AgentPlan,
    "ExecutePlanRequest": ExecutePlanRequest,
    "PlanExecutionRequest": PlanExecutionRequest,
    "ContextSummary": ContextSummary,
    "AgentMessageChunk": AgentMessageChunk,
    "AgentMessageIndex": AgentMessageIndex,
    "TurnReplySequence": TurnReplySequence,
    "CompactContextInput": CompactContextInput,
    "TurnFailureRouteInput": CompactContextInput | None,
    "AgentEvent": AgentEvent,
    "AgentSnapshot": AgentSnapshot,
    "ListAgentMessagesRequest": ListAgentMessagesRequest,
    "AgentMessagesPage": AgentMessagesPage,
    "string": str,
    "int64": int,
    "bool": bool,
    "null": NoneType,
}


def codec_for(type_name: str) -> Codec[Any]:
    """Return the Python core's codec for a language-neutral type name."""
    return CODECS.resolve(VALUE_TYPES[type_name])


def type_name_of(type_hint: object) -> str:
    """Return the language-neutral name of a Step, RPC, or persistence type hint."""
    for name, candidate in VALUE_TYPES.items():
        if candidate == type_hint:
            return name
    if type_hint is None:
        return "null"
    origin = get_origin(type_hint)
    if origin in (Union, UnionType) and set(get_args(type_hint)) == {CompactContextInput, NoneType}:
        return "TurnFailureRouteInput"
    raise TypeError(f"no conformance type name for {type_hint!r}")


def wire_text(value: Value) -> str:
    """Render a Value's data as the text a golden case stores."""
    if value.kind is WireKind.BOOL:
        return "true" if value.data else "false"
    if value.kind is WireKind.INT64:
        return str(value.data)
    return str(value.data)


def encode(type_name: str, value: object) -> str:
    return wire_text(codec_for(type_name).encode(value))


def wire_kind(type_name: str) -> str:
    return codec_for(type_name).wire_kind.value


def reencode(type_name: str, wire: str) -> str:
    """Decode wire text with the Python core and encode it again."""
    codec = codec_for(type_name)
    kind = codec.wire_kind
    data: str | bool | int
    if kind is WireKind.BOOL:
        data = wire == "true"
    elif kind is WireKind.INT64:
        data = int(wire)
    else:
        data = wire
    return canonicalize_wire_text(
        type_name, wire_text(codec.encode(codec.decode(Value(kind, data))))
    )


#: Value types with a string field that holds JSON text the contract fixes only as JSON.
JSON_TEXT_FIELDS: dict[str, tuple[str, ...]] = {"AgentEvent": ("payload",)}


def canonicalize_json(value: Any) -> str:
    """Serialize parsed JSON in the suite's canonical form."""
    return json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False)


def canonicalize_wire_text(type_name: str, text: str) -> str:
    """Return a value's canonical text: JSON parsed and re-serialized, scalars unchanged.

    ``AgentEvent.payload`` is JSON text whose bytes each core writes differently, so it is
    canonicalized too. Other strings, such as tool output, stay byte for byte.
    """
    if wire_kind(type_name) != "json":
        return text
    value = json.loads(text)
    for field_name in JSON_TEXT_FIELDS.get(type_name, ()):
        value[field_name] = canonicalize_json(json.loads(value[field_name]))
    return canonicalize_json(value)


# ---------------------------------------------------------------------------
# Golden values
# ---------------------------------------------------------------------------


class ValueCases:
    """Collect the cases of one value type."""

    def __init__(self, type_name: str, description: str) -> None:
        self.type_name = type_name
        self.description = description
        self.cases: list[dict[str, str]] = []

    def value(self, name: str, description: str, value: object) -> ValueCases:
        """Add a value the Python core writes: its encoding and that encoding's canonical form."""
        text = encode(self.type_name, value)
        self.cases.append(
            {
                "name": name,
                "description": description,
                "wire": text,
                "canonical": canonicalize_wire_text(self.type_name, text),
            }
        )
        return self

    def wire(self, name: str, description: str, wire: object) -> ValueCases:
        """Add a value as another writer stored it; canonical is its decode and re-encode."""
        text = wire if isinstance(wire, str) else json.dumps(wire, separators=(",", ":"))
        self.cases.append(
            {
                "name": name,
                "description": description,
                "wire": text,
                "canonical": reencode(self.type_name, text),
            }
        )
        return self

    def document(self) -> dict[str, Any]:
        return {
            "type": self.type_name,
            "wire_kind": wire_kind(self.type_name),
            "description": self.description,
            "cases": self.cases,
        }


def full_tool_call(**changes: Any) -> AgentToolCall:
    record = AgentToolCall(
        tool_call_id="call-7",
        turn_id="turn-3",
        tool_name="transfer_funds",
        arguments='{"amount": 5, "memo": "Grüße"}',
        effect=ToolEffect.NON_IDEMPOTENT_WRITE,
        requires_approval=True,
        status=ToolCallStatus.AWAITING_ANSWER,
        output=TRICKY_TEXT,
        error="the bank was down",
        rejection_message="not today",
        deferral_order=2,
        tool_source_reference='{"server":"bank","tool":"transfer"}',
        trace_context="00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
        wait_until=LATER,
        input_continuation='{"amount":5}',
        input_answers=[
            QuestionAnswer(
                interrupt_id="call-7-input-1",
                question="Which account?",
                answer="checking",
                resolution=QuestionResolution.ANSWERED,
            ),
            QuestionAnswer(
                interrupt_id="call-7-input-2",
                question="Confirm?",
                answer="",
                resolution=QuestionResolution.DECLINED,
            ),
        ],
        input_question="Which currency?",
        input_choices=["EUR", "USD"],
        input_allows_free_text=False,
        cancellation_reason=ToolCallCancellationReason.STEERED,
        task_id="task-42",
        task_poll_interval_seconds=5,
        task_answer_pending=True,
        images=[CHART_IMAGE],
    )
    return replace(record, **changes)


def minimal_tool_call(**changes: Any) -> AgentToolCall:
    record = AgentToolCall(
        tool_call_id="call-1",
        turn_id="turn-1",
        tool_name="lookup_order",
        arguments="{}",
        effect=ToolEffect.READ,
        requires_approval=False,
        status=ToolCallStatus.APPROVED,
    )
    return replace(record, **changes)


def approval_interrupt() -> AgentInterrupt:
    return AgentInterrupt(
        interrupt_id="call-2",
        kind=InterruptKind.TOOL_APPROVAL,
        tool_call_id="call-2",
        tool_name="send_email",
        arguments='{"to": "ops@example.com"}',
        created_at=CREATED_AT,
    )


def question_interrupt() -> AgentInterrupt:
    return AgentInterrupt(
        interrupt_id="call-7-input-3",
        kind=InterruptKind.QUESTION,
        tool_call_id="call-7",
        tool_name="transfer_funds",
        arguments='{"amount": 5}',
        created_at=CREATED_AT,
        deadline=LATER,
        question="Which currency?",
        choices=["EUR", "USD"],
        allows_free_text=False,
    )


def full_state() -> AgentState:
    return AgentState(
        status=AgentSessionStatus.RUNNING_TOOLS,
        turn_count=3,
        active_turn_id="turn-4",
        next_message_sequence=12,
        last_turn_error="RuntimeError: the model call failed",
        interaction_mode=InteractionMode.PLANNING,
        accepts_turn_control=True,
    )


def tool_message() -> AgentMessage:
    return AgentMessage(
        sequence=7,
        turn_id="turn-3",
        role=MessageRole.TOOL,
        text=None,
        tool_call_id="call-7",
        tool_name="transfer_funds",
        tool_arguments='{"amount": 5}',
        tool_output=TRICKY_TEXT,
        framework_item='{"type":"tool_result","call_id":"call-7","output":"moved 5"}',
        created_at=CREATED_AT,
    )


def user_message(sequence: int = 1) -> AgentMessage:
    return AgentMessage(
        sequence=sequence,
        turn_id="turn-1",
        role=MessageRole.USER,
        text="Where is order 7?",
        framework_item='{"type":"user_text","text":"Where is order 7?"}',
        created_at=CREATED_AT,
    )


def full_plan() -> AgentPlan:
    return AgentPlan(
        revision=3,
        status=PlanStatus.ACTIVE,
        tasks=[
            AgentPlanTask(content="Look up the order", status=PlanTaskStatus.COMPLETED),
            AgentPlanTask(content="Refund the order", status=PlanTaskStatus.IN_PROGRESS),
            AgentPlanTask(content="Tell the user ☕", status=PlanTaskStatus.PENDING),
        ],
        updated_at=LATER,
    )


def full_summary() -> ContextSummary:
    return ContextSummary(
        generation=2,
        first_sequence=1,
        last_sequence=40,
        text="The user asked about order 7.\nIt shipped.",
        created_at=LATER,
    )


def summary_prompt_cases() -> list[tuple[ContextSummary | None, list[AgentMessage], int | None]]:
    """Return adapter summary prompts: whole, oldest left out, and one message shortened."""
    multi_call = AgentMessage(
        sequence=3,
        turn_id="turn-1",
        role=MessageRole.ASSISTANT,
        framework_item='{"type":"response","calls":["a","b"],"note":"日本語 ☕"}',
        created_at=CREATED_AT,
    )
    long_answer = AgentMessage(
        sequence=8,
        turn_id="turn-3",
        role=MessageRole.ASSISTANT,
        text="日本語 ☕ " + "x" * 4000,
        framework_item='{"type":"message"}',
        created_at=CREATED_AT,
    )
    messages = [user_message(), multi_call, tool_message()]
    return [
        (None, messages, None),
        (full_summary(), messages, 220),
        (None, [user_message(), long_answer], 600),
    ]


def summary_prompt_request(
    previous: ContextSummary | None, messages: list[AgentMessage], budget: int | None
) -> ContextSummaryRequest:
    return ContextSummaryRequest(
        flow_id="session-1",
        turn_id="turn-4",
        attempt=1,
        previous_summary=previous,
        messages=messages,
        through_sequence=messages[-1].sequence,
        events=AgentEventWriter(lambda event: None, "turn-4", 1),
        heartbeat=_no_heartbeat,
        input_token_budget=budget,
    )


async def _no_heartbeat() -> None:
    return None


def full_usage() -> AgentUsage:
    return AgentUsage(
        model_calls=4,
        input_tokens=1200,
        output_tokens=340,
        cached_input_tokens=800,
        reasoning_tokens=96,
    )


RELEASED_STATE = {
    "status": "running_model",
    "turn_count": 2,
    "active_turn_id": "turn-3",
    "next_message_sequence": 5,
    "last_turn_error": None,
}
RELEASED_TOOL_CALL = {
    "tool_call_id": "call-1",
    "turn_id": "turn-1",
    "tool_name": "send_email",
    "arguments": '{"to":"ops"}',
    "effect": "unclassified",
    "requires_approval": True,
    "status": "awaiting_approval",
    "output": None,
    "error": None,
    "rejection_message": None,
    "deferral_order": 0,
}
RELEASED_INTERRUPT = {
    "interrupt_id": "call-1",
    "kind": "tool_approval",
    "tool_call_id": "call-1",
    "tool_name": "send_email",
    "arguments": '{"to":"ops"}',
    "created_at": CREATED_AT,
    "deadline": None,
}
RELEASED_MESSAGE = {
    "sequence": 1,
    "turn_id": "turn-1",
    "role": "user",
    "text": "hello",
    "tool_call_id": None,
    "tool_name": None,
    "tool_arguments": None,
    "tool_output": None,
    "framework_item": '{"role":"user","content":"hello"}',
    "created_at": CREATED_AT,
}
RELEASED_USAGE = {
    "model_calls": 1,
    "input_tokens": 10,
    "output_tokens": 5,
    "cached_input_tokens": 0,
    "reasoning_tokens": 0,
}


def build_value_documents() -> list[dict[str, Any]]:
    documents: list[ValueCases] = []

    documents.append(
        ValueCases(
            "AgentImageReference",
            "One element of AgentUserMessage.images, AgentMessage.images, and "
            "AgentToolCall.images.",
        )
        .value("png", "A PNG image.", CHART_IMAGE)
        .value("jpeg-at-limit", "A JPEG image of MAX_IMAGE_BYTES.", PHOTO_IMAGE)
        .wire(
            "without-type",
            "A reference without type reads as an image.",
            {
                "uri": CHART_IMAGE.uri,
                "mime_type": "image/png",
                "sha256": CHART_IMAGE.sha256,
                "size_bytes": 68,
            },
        )
    )

    documents.append(
        ValueCases(
            "AgentSessionStartInput", "Input of StartAgentSession, the session Flow's start Step."
        )
        .value(
            "full",
            "With an initial message and session settings.",
            AgentSessionStartInput(
                initial_message=TRICKY_TEXT, session_settings=FULL_SESSION_SETTINGS
            ),
        )
        .value("defaults", "Without an initial message or settings.", AgentSessionStartInput())
        .wire(
            "released-0.1.0",
            "As dex-agents 0.1.0 wrote it, without session_settings.",
            {"initial_message": "hello"},
        )
    )

    state = ValueCases("AgentState", "The AgentState Attribute.")
    state.value("full", "Every field set away from its default.", full_state())
    state.value(
        "defaults",
        "Every optional field at its default.",
        AgentState(status=AgentSessionStatus.WAITING_FOR_USER_MESSAGE),
    )
    for status in AgentSessionStatus:
        state.value(
            f"status-{status}",
            f"Status {status}.",
            AgentState(
                status=status, turn_count=1, active_turn_id="turn-2", next_message_sequence=3
            ),
        )
    for mode in InteractionMode:
        state.value(
            f"interaction-mode-{mode}",
            f"Interaction mode {mode}.",
            AgentState(status=AgentSessionStatus.RUNNING_MODEL, interaction_mode=mode),
        )
    state.wire("released-0.1.0", "As dex-agents 0.1.0 wrote it.", RELEASED_STATE)
    state.wire(
        "explicit-null-interaction-mode",
        "An explicit null interaction_mode reads as chat.",
        {**RELEASED_STATE, "interaction_mode": None},
    )
    documents.append(state)

    messages = ValueCases("AgentMessage", "One AgentMessages map instance.")
    messages.value("user", "A user message.", user_message())
    messages.value(
        "assistant",
        "An assistant message with display text.",
        AgentMessage(
            sequence=2,
            turn_id="turn-1",
            role=MessageRole.ASSISTANT,
            text=TRICKY_TEXT,
            framework_item='{"type":"message","text":"hi"}',
            created_at=CREATED_AT,
        ),
    )
    messages.value("tool", "A tool result with every tool field set.", tool_message())
    messages.value(
        "user-with-images",
        "A user message with two images.",
        replace(user_message(), images=[CHART_IMAGE, PHOTO_IMAGE]),
    )
    messages.value(
        "tool-with-image",
        "A tool result that returned an image.",
        replace(tool_message(), images=[CHART_IMAGE]),
    )
    messages.wire("released-0.1.0", "As dex-agents 0.1.0 wrote it.", RELEASED_MESSAGE)
    documents.append(messages)

    answers = ValueCases("QuestionAnswer", "One element of AgentToolCall.input_answers.")
    for resolution in QuestionResolution:
        answers.value(
            f"resolution-{resolution}",
            f"Resolution {resolution}.",
            QuestionAnswer(
                interrupt_id="call-7-input-1",
                question="Which account?",
                answer="checking" if resolution is QuestionResolution.ANSWERED else "",
                resolution=resolution,
            ),
        )
    answers.wire(
        "without-resolution",
        "Written before resolution existed; reads as answered.",
        {"interrupt_id": "call-7-input-1", "question": "Which account?", "answer": "savings"},
    )
    documents.append(answers)

    tool_calls = ValueCases("AgentToolCall", "One AgentToolCalls map instance.")
    tool_calls.value("full", "Every optional field set.", full_tool_call())
    tool_calls.value("defaults", "Every optional field at its default.", minimal_tool_call())
    for tool_call_status in ToolCallStatus:
        tool_calls.value(
            f"status-{tool_call_status}",
            f"Status {tool_call_status}.",
            minimal_tool_call(status=tool_call_status),
        )
    for effect in ToolEffect:
        tool_calls.value(f"effect-{effect}", f"Effect {effect}.", minimal_tool_call(effect=effect))
    for reason in ToolCallCancellationReason:
        tool_calls.value(
            f"cancelled-{reason}",
            f"A call abandoned with cancellation reason {reason}.",
            minimal_tool_call(
                status=ToolCallStatus.REJECTED,
                cancellation_reason=reason,
                rejection_message=TOOL_CALL_CANCELLED_BY_STEERING_RESULT
                if reason is ToolCallCancellationReason.STEERED
                else TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT,
            ),
        )
    tool_calls.wire("released-0.1.0", "As dex-agents 0.1.0 wrote it.", RELEASED_TOOL_CALL)
    documents.append(tool_calls)

    interrupts = ValueCases("AgentInterrupt", "One AgentInterrupt, as in PendingInterrupts.")
    interrupts.value("tool-approval", "A tool approval with defaults.", approval_interrupt())
    interrupts.value("question", "A question with every field set.", question_interrupt())
    interrupts.wire("released-0.1.0", "As dex-agents 0.1.0 wrote it.", RELEASED_INTERRUPT)
    documents.append(interrupts)

    documents.append(
        ValueCases("AgentInterruptList", "The PendingInterrupts Attribute.")
        .value("empty", "No pending interrupt.", [])
        .value(
            "both-kinds",
            "An approval and a question.",
            [approval_interrupt(), question_interrupt()],
        )
        .wire("released-0.1.0", "As dex-agents 0.1.0 wrote it.", [RELEASED_INTERRUPT])
    )

    documents.append(
        ValueCases("AgentInterruptResolution", "One InterruptResolutions Channel message.")
        .value("approved", "An approval.", AgentInterruptResolution(approved=True))
        .value(
            "rejected",
            "A rejection with a message.",
            AgentInterruptResolution(approved=False, rejection_message=TRICKY_TEXT),
        )
        .value(
            "answered",
            "A question answer.",
            AgentInterruptResolution(approved=True, answer="checking"),
        )
        .wire(
            "released-0.1.0",
            "As dex-agents 0.1.0 wrote it.",
            {"approved": False, "rejection_message": "not today"},
        )
    )

    documents.append(
        ValueCases("ResolveInterruptRequest", "Input of the resolve_interrupt RPC.")
        .value(
            "approve", "An approval.", ResolveInterruptRequest(interrupt_id="call-2", approved=True)
        )
        .value(
            "reject",
            "A rejection with a message.",
            ResolveInterruptRequest(
                interrupt_id="call-2", approved=False, rejection_message="not today"
            ),
        )
    )

    documents.append(
        ValueCases("AnswerQuestionRequest", "Input of the answer_question RPC.").value(
            "full",
            "An answer.",
            AnswerQuestionRequest(interrupt_id="call-7-input-1", answer=TRICKY_TEXT),
        )
    )

    documents.append(
        ValueCases("AgentUsage", "The AgentUsage Attribute and usage_recorded payloads.")
        .value("full", "Every counter set.", full_usage())
        .value("defaults", "Every counter zero.", AgentUsage())
        .wire("released-0.1.0", "As dex-agents 0.1.0 wrote it.", RELEASED_USAGE)
    )

    documents.append(
        ValueCases(
            "AgentUserMessage",
            "QueuedUserMessages and SteeredUserMessages Channel messages, and the "
            "send_user_message RPC input.",
        )
        .value("full", "A plan-mode message.", AgentUserMessage(text=TRICKY_TEXT, plan_mode=True))
        .value("defaults", "An ordinary message.", AgentUserMessage(text="hello"))
        .value(
            "images",
            "A message with two images.",
            AgentUserMessage(text="What is in these?", images=[CHART_IMAGE, PHOTO_IMAGE]),
        )
        .wire("released-0.1.0", "As dex-agents 0.1.0 wrote it.", {"text": "hello"})
        .wire(
            "explicit-null-plan-mode",
            "An explicit null plan_mode reads as false.",
            {"text": "hello", "plan_mode": None},
        )
    )

    documents.append(
        ValueCases("PendingUserMessage", "One element of AgentSnapshot.queued_messages.").value(
            "full",
            "A pending message with its Channel message ID.",
            PendingUserMessage(
                message_id="msg-3",
                message=AgentUserMessage(text="later", plan_mode=True, images=[CHART_IMAGE]),
            ),
        )
    )

    documents.append(
        ValueCases("SteerUserMessageRequest", "Input of the steer_user_message RPC.")
        .value(
            "by-text", "A new steered message.", SteerUserMessageRequest(text="stop and summarize")
        )
        .value(
            "by-message-id", "Steer a queued message.", SteerUserMessageRequest(message_id="msg-3")
        )
    )

    documents.append(
        ValueCases("DeleteQueuedMessageRequest", "Input of the delete_queued_message RPC.").value(
            "full", "A message ID.", DeleteQueuedMessageRequest(message_id="msg-3")
        )
    )

    documents.append(
        ValueCases("CancelTurnRequest", "Input of the cancel_turn RPC.")
        .value("full", "Cancel a named turn.", CancelTurnRequest(turn_id="turn-4"))
        .value("defaults", "Cancel the active turn.", CancelTurnRequest())
    )

    documents.append(
        ValueCases(
            "SessionSettings",
            "The SessionSettings Attribute: one JSON object.",
        )
        .value("full", "Every JSON kind, nested.", FULL_SESSION_SETTINGS)
        .value("model-only", "Only a model name.", {"model": "gpt-5-mini"})
    )
    documents.append(
        ValueCases(
            "TurnSessionSettings",
            "The TurnSessionSettings Attribute: the settings one turn started with, "
            "tagged with that turn.",
        ).value(
            "full",
            "Every JSON kind, nested.",
            TurnSessionSettings(turn_id="turn-4", session_settings=FULL_SESSION_SETTINGS),
        )
    )
    documents.append(
        ValueCases("UpdateSessionSettingsRequest", "Input of the update_session_settings RPC.")
        .value(
            "full",
            "New settings.",
            UpdateSessionSettingsRequest(session_settings=FULL_SESSION_SETTINGS),
        )
        .value("empty", "Clear the settings.", UpdateSessionSettingsRequest(session_settings={}))
        .value(
            "null",
            "No settings: the RPC returns false and changes nothing.",
            UpdateSessionSettingsRequest(session_settings=None),
        )
        .wire(
            "without-session-settings",
            "A request without the field reads as null settings.",
            {},
        )
    )

    turn_control = ValueCases("TurnControlRequest", "One TurnControlRequests Channel message.")
    for kind in TurnControlKind:
        turn_control.value(f"kind-{kind}", f"Kind {kind}.", TurnControlRequest(kind=kind))
    documents.append(turn_control)

    documents.append(
        ValueCases("TurnCancellationRequest", "The TurnCancellationRequest Attribute.").value(
            "full",
            "A request for one turn.",
            TurnCancellationRequest(turn_id="turn-4", requested_at=CREATED_AT),
        )
    )

    cancellations = ValueCases("ToolCallCancellation", "One ToolCallCancellations Channel message.")
    for reason in ToolCallCancellationReason:
        cancellations.value(
            f"reason-{reason}", f"Reason {reason}.", ToolCallCancellation(reason=reason)
        )
    documents.append(cancellations)

    documents.append(
        ValueCases("ToolCallCompleted", "One ToolCallResults Channel message.").value(
            "full", "A tool call ID.", ToolCallCompleted(tool_call_id="call-7")
        )
    )

    plan_tasks = ValueCases("AgentPlanTask", "One element of AgentPlan.tasks.")
    for task_status in PlanTaskStatus:
        plan_tasks.value(
            f"status-{task_status}",
            f"Status {task_status}.",
            AgentPlanTask(content="Refund the order", status=task_status),
        )
    documents.append(plan_tasks)

    plans = ValueCases("AgentPlan", "The AgentPlan Attribute.")
    plans.value("full", "Tasks in every status.", full_plan())
    for plan_status in PlanStatus:
        plans.value(
            f"status-{plan_status}",
            f"Status {plan_status}.",
            AgentPlan(
                revision=1,
                status=plan_status,
                tasks=[]
                if plan_status is PlanStatus.CLEARED
                else [AgentPlanTask(content="Refund the order", status=PlanTaskStatus.PENDING)],
                updated_at=CREATED_AT,
            ),
        )
    documents.append(plans)

    documents.append(
        ValueCases("ExecutePlanRequest", "Input of the execute_plan RPC.").value(
            "full", "A plan revision.", ExecutePlanRequest(revision=3)
        )
    )
    documents.append(
        ValueCases(
            "PlanExecutionRequest",
            "PlanExecutions Channel messages and the PendingPlanExecution Attribute.",
        ).value("full", "A plan revision.", PlanExecutionRequest(revision=3))
    )

    documents.append(
        ValueCases("ContextSummary", "The ContextSummary Attribute.").value(
            "full", "A second-generation summary.", full_summary()
        )
    )
    documents.append(
        ValueCases(
            "AgentMessageChunk", "One AgentMessageChunks map instance: an immutable archive chunk."
        ).value(
            "two-messages",
            "A chunk of two messages, keyed by its first sequence.",
            AgentMessageChunk(messages=[user_message(2), tool_message()]),
        )
    )
    documents.append(
        ValueCases("AgentMessageIndex", "The AgentMessageIndex Attribute.")
        .value(
            "full",
            "Retention deleted the first chunk, a byte-capped chunk is shorter, and an oversized "
            "message 80 sits alone among the window's messages.",
            AgentMessageIndex(
                chunks=[
                    AgentMessageChunkRange(first_sequence=33, last_sequence=64),
                    AgentMessageChunkRange(first_sequence=65, last_sequence=71),
                    AgentMessageChunkRange(first_sequence=72, last_sequence=72),
                    AgentMessageChunkRange(first_sequence=80, last_sequence=80),
                ],
                first_sequence=33,
                window_first_sequence=73,
            ),
        )
        .value(
            "defaults",
            "Nothing archived yet.",
            AgentMessageIndex(chunks=[], first_sequence=1, window_first_sequence=1),
        )
    )
    documents.append(
        ValueCases("TurnReplySequence", "The TurnReplySequence Attribute.").value(
            "full",
            "Turn 3's replies start after its two user messages, 10 and 11.",
            TurnReplySequence(turn_id="turn-3", sequence=12),
        )
    )
    documents.append(
        ValueCases("CompactContextInput", "Input of CompactContext.").value(
            "full", "A cutoff sequence.", CompactContextInput(through_sequence=40)
        )
    )
    documents.append(
        ValueCases(
            "TurnFailureRouteInput",
            "Input of RunModel, CompactContext, and RecordTurnFailure: a CompactContextInput or "
            "JSON null.",
        )
        .value("null", "RunModel's input, and the failure route of RunModel.", None)
        .value(
            "compact-context",
            "The failure route of CompactContext.",
            CompactContextInput(through_sequence=40),
        )
    )

    documents.append(build_event_cases())

    snapshots = ValueCases("AgentSnapshot", "Output of the get_agent_snapshot RPC.")
    snapshots.value(
        "full",
        "Every field set.",
        AgentSnapshot(
            state=full_state(),
            messages=[user_message(), tool_message()],
            pending_interrupts=[approval_interrupt(), question_interrupt()],
            tool_calls=[full_tool_call(), minimal_tool_call()],
            usage=full_usage(),
            queued_message_count=1,
            plan=full_plan(),
            queued_messages=[
                PendingUserMessage(message_id="msg-3", message=AgentUserMessage(text="later"))
            ],
            steered_messages=[
                PendingUserMessage(message_id="msg-4", message=AgentUserMessage(text="stop"))
            ],
            context_summary=full_summary(),
            session_settings={"model": "gpt-5-mini"},
        ),
    )
    snapshots.value("defaults", "Before StartAgentSession commits.", AgentSnapshot(state=None))
    snapshots.wire(
        "released-0.1.0",
        "As dex-agents 0.1.0 returned it.",
        {
            "state": RELEASED_STATE,
            "messages": [RELEASED_MESSAGE],
            "pending_interrupts": [RELEASED_INTERRUPT],
            "tool_calls": [RELEASED_TOOL_CALL],
            "usage": RELEASED_USAGE,
            "queued_message_count": 0,
        },
    )
    documents.append(snapshots)

    documents.append(
        ValueCases("ListAgentMessagesRequest", "Input of the list_agent_messages RPC.")
        .value(
            "full",
            "A page before a sequence.",
            ListAgentMessagesRequest(before_sequence=40, limit=20),
        )
        .value("defaults", "The newest page.", ListAgentMessagesRequest())
        .wire("without-limit", "A request without limit reads limit 50.", {"before_sequence": None})
    )
    documents.append(
        ValueCases("AgentMessagesPage", "Output of the list_agent_messages RPC.")
        .value(
            "full",
            "A page with an earlier page.",
            AgentMessagesPage(messages=[user_message(2), tool_message()], next_before_sequence=2),
        )
        .value(
            "last", "The oldest page.", AgentMessagesPage(messages=[], next_before_sequence=None)
        )
        .value(
            "after-retention",
            "The oldest kept page after retention deleted messages 1 through 32.",
            AgentMessagesPage(
                messages=[user_message(33)], next_before_sequence=None, deleted_before_sequence=33
            ),
        )
        .wire(
            "released-0.1.0",
            "As dex-agents 0.1.0 wrote it, without deleted_before_sequence.",
            {"messages": [], "next_before_sequence": None},
        )
    )

    documents.append(
        ValueCases(
            "string",
            "String Values: FrameworkRunState, AgentTraceContext, the tool call ID input of "
            "the tool Steps and CancelToolTask, and the turn ID input of FinishTurnCancellation.",
        )
        .value("tool-call-id", "A tool call ID.", "call-7")
        .value("turn-id", "A turn ID.", "turn-3")
        .value("framework-run-state", "Opaque framework JSON.", '{"model_calls":2}')
        .value("non-ascii", "Text with every escape-sensitive character.", TRICKY_TEXT)
    )
    documents.append(
        ValueCases("int64", "The expected result count input of ResumeTurn.").value(
            "count", "Three results.", 3
        )
    )
    documents.append(
        ValueCases("bool", "Output of the boolean RPCs.")
        .value("true", "Accepted.", True)
        .value("false", "Refused.", False)
    )
    documents.append(
        ValueCases("null", "Input of AwaitUserMessage and ApplyTurnControl.").value(
            "null", "No input.", None
        )
    )

    return [document.document() for document in documents]


def build_event_cases() -> ValueCases:
    """Return AgentEvent cases whose payload text the Python event writer produced."""
    written: list[AgentEvent] = []
    writer = AgentEventWriter(written.append, "turn-3", 2, "RunModel-4")
    interrupt = question_interrupt()
    usage_payload = {
        "model_calls": 1,
        "input_tokens": 10,
        "output_tokens": 5,
        "cached_input_tokens": 0,
        "reasoning_tokens": 0,
    }
    payloads: list[tuple[AgentEventType, Mapping[str, object]]] = [
        (AgentEventType.TURN_STARTED, {"sequence": 7}),
        (AgentEventType.TURN_COMPLETED, {"message_count": 3}),
        (AgentEventType.TURN_FAILED, {"error": "RuntimeError: the model call failed"}),
        (AgentEventType.TURN_STEERED, {"steered_message_count": 2, "message_count": 4}),
        (AgentEventType.TURN_CANCELLED, {"message_count": 1}),
        (AgentEventType.TEXT_DELTA, {"text": TRICKY_TEXT}),
        (AgentEventType.REASONING_DELTA, {"text": "thinking"}),
        (
            AgentEventType.TOOL_CALL_STARTED,
            {"tool_call_id": "call-7", "tool_name": "transfer_funds"},
        ),
        (
            AgentEventType.TOOL_CALL_FINISHED,
            {"tool_call_id": "call-7", "tool_name": "transfer_funds", "status": "succeeded"},
        ),
        (
            AgentEventType.TOOL_CALL_DEFERRED,
            {
                "tool_call_id": "call-7",
                "tool_name": "transfer_funds",
                "effect": "non_idempotent_write",
                "requires_approval": True,
            },
        ),
        (
            AgentEventType.INTERRUPT_RAISED,
            {
                "interrupt_id": interrupt.interrupt_id,
                "tool_call_id": interrupt.tool_call_id,
                "tool_name": interrupt.tool_name,
                "deadline": interrupt.deadline,
                "kind": str(interrupt.kind),
                "question": interrupt.question,
                "choices": list(interrupt.choices),
                "allows_free_text": interrupt.allows_free_text,
            },
        ),
        (
            AgentEventType.INTERRUPT_RESOLVED,
            {
                "interrupt_id": "call-2",
                "approved": False,
                "rejection_message": TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
                "kind": "tool_approval",
                "answered": False,
                "cancellation_reason": "steered",
            },
        ),
        (AgentEventType.USAGE_RECORDED, {"recorded": usage_payload, "session": usage_payload}),
        (
            AgentEventType.TIMER_STARTED,
            {"tool_call_id": "call-9", "wait_until": LATER, "reason": "the build runs"},
        ),
        (AgentEventType.PLAN_UPDATED, {"revision": 3, "status": "active", "task_count": 3}),
        (AgentEventType.PLAN_EXECUTION_STARTED, {"revision": 3}),
        (
            AgentEventType.TOOL_TASK_STARTED,
            {"tool_call_id": "call-8", "tool_name": "start_report", "task_id": "task-42"},
        ),
        (AgentEventType.COMPACTION_STARTED, {"through_sequence": 40}),
        (AgentEventType.COMPACTION_FINISHED, {"generation": 2, "through_sequence": 40}),
    ]
    for event_type, payload in payloads:
        writer.write_event(event_type, payload)
    cases = ValueCases(
        "AgentEvent",
        "One AgentEvents Stream message per event type. wire.payload is the Python event "
        "writer's text; canonical.payload is that JSON in canonical form.",
    )
    for event in written:
        cases.value(f"type-{event.type}", f"A {event.type} event.", event)
    cases.value(
        "outside-a-turn",
        "An event without a turn.",
        AgentEvent(type="openai_agents.handoff", turn_id=None, attempt=1, payload="{}"),
    )
    cases.wire(
        "without-step-execution-id",
        "Written before step_execution_id existed; reads as an empty string.",
        {"type": "turn_started", "turn_id": "turn-1", "attempt": 1, "payload": '{"sequence":1}'},
    )
    return cases


# ---------------------------------------------------------------------------
# Names manifest
# ---------------------------------------------------------------------------


def build_manifest_flow() -> Any:
    definition = AgentDefinition(
        name="conformance-manifest",
        instructions="Describe the session Flow.",
        model=ScriptedModelClient(lambda request: ScriptedModelReply(text="ok")),
    )
    return builtin_agent_session_flow(definition, flow_type=MANIFEST_FLOW_TYPE)


def build_session_flow_graph() -> dict[str, Any]:
    # This file runs as a script, so its sibling is not importable until now.
    sys.path.insert(0, str(SUITE_DIR))
    from session_graph import build_session_graph

    flow = build_manifest_flow()
    registry = Registry([flow], allow_async_handlers=True)
    return build_session_graph(
        flow, registry._registered_flows[MANIFEST_FLOW_TYPE], build_names()["persistence"]
    )


def build_session_graph_renderings(graph: Mapping[str, Any]) -> dict[str, Any]:
    cases = []
    for flow_type, action_permission in (
        ("SupportAgent", "support-agent.operate"),
        (TRICKY_TEXT + "\u2028\x7f\x01", "a.b-c1"),
    ):
        rendered = json.loads(json.dumps(graph))
        rendered["flow"]["name"] = flow_type
        for action in rendered["v2"]["actions"]:
            action["requiredPermission"] = action_permission
        cases.append(
            {
                "flow_type": flow_type,
                "action_permission": action_permission,
                "sha256": hashlib.sha256(render_json(rendered).encode()).hexdigest(),
            }
        )
    return {
        "description": "The SHA-256 of the UTF-8 graph text that every core must render.",
        "cases": cases,
    }


def persistence_kind(definition: object) -> str:
    # AttributeMap and ChannelMap are checked first; neither subclasses the other kinds.
    for kind, definition_class in (
        ("attribute_map", AttributeMap),
        ("channel_map", ChannelMap),
        ("attribute", Attribute),
        ("channel", Channel),
        ("stream", Stream),
    ):
        if isinstance(definition, definition_class):
            return kind
    raise TypeError(f"unknown persistence definition {definition!r}")


def build_names() -> dict[str, Any]:
    flow = build_manifest_flow()
    registry = Registry([flow], allow_async_handlers=True)
    registered = registry._registered_flows[MANIFEST_FLOW_TYPE]
    steps = [
        {
            "name": step.name,
            "input": step_input_type_name(step.name, step.input_type),
            "is_start_step": step.starting,
        }
        for step in registered.steps.values()
    ]
    rpcs = []
    for registered_rpc in sorted(registered.rpcs.values(), key=lambda entry: entry.name):
        hints = registered_rpc.method.__annotations__
        input_hint = hints.get("input")
        rpcs.append(
            {
                "name": registered_rpc.name,
                "input": None if input_hint is None else type_name_of(flow_type_hint(input_hint)),
                "output": type_name_of(rpc_output_hint(hints["return"])),
                "is_transactional": registered_rpc.options.is_transactional,
                "locks": sorted(registered_rpc.locks),
                "load_attribute_maps": sorted(
                    definition.name for definition in registered_rpc.options.load_attribute_maps
                ),
                "load_channels": sorted(
                    definition.name for definition in registered_rpc.options.load_channels
                ),
            }
        )
    persistence = [
        {
            "name": name,
            "kind": persistence_kind(definition),
            "value_type": type_name_of(definition.value_type),
        }
        for name, definition in sorted(registered.persistence.items())
    ]
    return {
        "description": (
            "Every name a session Flow registers or writes. Flow types are chosen by the "
            "application; the other names are fixed."
        ),
        "step_types": steps,
        "rpcs": rpcs,
        "persistence": persistence,
        "agent_events_default_capacity_bytes": DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
        "event_types": [str(event_type) for event_type in AgentEventType],
        "enums": {
            "AgentSessionStatus": [str(value) for value in AgentSessionStatus],
            "MessageRole": [str(value) for value in MessageRole],
            "ToolEffect": [str(value) for value in ToolEffect],
            "ToolCallStatus": [str(value) for value in ToolCallStatus],
            "InterruptKind": [str(value) for value in InterruptKind],
            "InteractionMode": [str(value) for value in InteractionMode],
            "PlanStatus": [str(value) for value in PlanStatus],
            "PlanTaskStatus": [str(value) for value in PlanTaskStatus],
            "QuestionResolution": [str(value) for value in QuestionResolution],
            "ToolCallCancellationReason": [str(value) for value in ToolCallCancellationReason],
            "TurnControlKind": [str(value) for value in TurnControlKind],
            "BuiltInTool": [str(value) for value in BuiltInTool],
        },
        "final_tool_call_statuses": [str(status) for status in ToolCallStatus if status.is_final],
        "keys": {
            "AgentMessages": {
                "format": "the message sequence as a 12-digit zero-padded decimal",
                "examples": {
                    str(sequence): message_sequence_key(sequence) for sequence in (1, 7, 123456)
                },
            },
            "PlanExecutions": {
                "format": "the plan revision as a decimal",
                "examples": {str(revision): plan_execution_key(revision) for revision in (1, 12)},
            },
            "AgentMessageChunks": {
                "format": "the chunk's first message sequence as a 12-digit zero-padded decimal",
                "examples": {
                    str(sequence): message_sequence_key(sequence) for sequence in (1, 33, 123457)
                },
            },
            "AgentToolCalls": {"format": "the tool call ID"},
            "InterruptResolutions": {"format": "the interrupt ID"},
            "ToolCallCancellations": {"format": "the tool call ID"},
        },
        "ids": {
            "turn_id": {"format": "turn-<n>, where n is turn_count + 1 when the turn starts"},
            "tool_approval_interrupt_id": {"format": "<tool_call_id>"},
            "ask_user_interrupt_id": {"format": "<tool_call_id>"},
            "tool_question_interrupt_id": {
                "format": "<tool_call_id>-input-<n>, where n is len(input_answers) + 1",
                "examples": {
                    "call-7 with 0 answers": "call-7-input-1",
                    "call-7 with 2 answers": "call-7-input-3",
                },
            },
        },
        "timestamps": {
            "format": "RFC 3339 in UTC with milliseconds and Z, such as 2026-11-02T09:30:00.000Z",
            "pattern": r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$",
            "fields": [
                "AgentMessage.created_at",
                "AgentInterrupt.created_at",
                "AgentInterrupt.deadline",
                "AgentToolCall.wait_until",
                "AgentPlan.updated_at",
                "ContextSummary.created_at",
                "TurnCancellationRequest.requested_at",
            ],
        },
        "mcp_resource_and_prompt_tool_names": [
            tool.name for tool in build_resource_and_prompt_tools(MCP_SERVER_NAMES)
        ],
        "stepwise_tool_source_reference": build_tool_source_envelope_cases(),
        "session_settings_sizes": build_session_settings_size_cases(),
        "limits": {
            "snapshot_message_limit": SNAPSHOT_MESSAGE_LIMIT,
            "list_agent_messages_default_limit": ListAgentMessagesRequest().limit,
            "list_agent_messages_maximum_limit": MESSAGE_PAGE_LIMIT,
            "default_max_durable_wait_seconds": int(DEFAULT_MAX_DURABLE_WAIT.total_seconds()),
            "turn_control_at_most": ALL_PENDING_MESSAGES,
            "stepwise_default_max_model_calls_per_turn": DEFAULT_MAX_MODEL_CALLS_PER_TURN,
            "max_session_settings_bytes": MAX_SESSION_SETTINGS_BYTES,
            "session_settings_size_tolerance_bytes": SESSION_SETTINGS_SIZE_TOLERANCE_BYTES,
            "default_message_window_size": DEFAULT_MESSAGE_WINDOW_SIZE,
            "default_message_chunk_size": DEFAULT_MESSAGE_CHUNK_SIZE,
            "default_message_chunk_max_bytes": DEFAULT_MESSAGE_CHUNK_MAX_BYTES,
            "message_rollover_max_chunks_per_commit": MESSAGE_ROLLOVER_MAX_CHUNKS_PER_COMMIT,
            "message_rollover_max_bytes_per_commit": MESSAGE_ROLLOVER_MAX_BYTES_PER_COMMIT,
            "default_retained_message_limit": DEFAULT_RETAINED_MESSAGE_LIMIT,
        },
    }


def build_session_settings_size_cases() -> dict[str, Any]:
    """Return settings with the size each core's limit check must compute.

    Each core measures its own compact JSON text, so a few inputs measure differently:
    Python writes floats in shortest form (``1.0``, ``1e+16``), TypeScript and Go write
    ``1`` and every digit, and Go escapes U+2028 and U+2029. ``bytes`` is every core's size
    unless ``bytes_by_core`` names one. A ``limit`` case stands for
    ``{"note": "x" * note_length}``, so the file stays small.
    """
    examples: list[tuple[str, dict[str, Any], dict[str, int]]] = [
        ("model-only", {"model": "gpt-5-mini"}, {}),
        ("non-ascii", {"persona": "Grüße ☕ 😀"}, {}),
        ("nested", {"credential": {"name": "support-openai"}, "tools": ["a", "b"], "n": 3}, {}),
        ("html-characters", {"s": "<>&"}, {}),
        ("whole-float", {"n": 1.0}, {"typescript": 7, "go": 7}),
        ("large-float", {"n": 1e16}, {"typescript": 23, "go": 23}),
        ("line-separator", {"s": "\u2028"}, {"go": 14}),
    ]
    at_the_limit = MAX_SESSION_SETTINGS_BYTES - len('{"note":""}')
    session_limit = MAX_SESSION_SETTINGS_BYTES + SESSION_SETTINGS_SIZE_TOLERANCE_BYTES
    cases: list[dict[str, Any]] = []
    for name, settings, other_cores in examples:
        size = session_settings_size(settings)
        case: dict[str, Any] = {"name": name, "settings": settings, "bytes": size}
        if other_cores:
            case["bytes_by_core"] = {"python": size, "typescript": size, "go": size, **other_cores}
        cases.append(case)
    return {
        "format": "UTF-8 bytes of compact JSON text, non-ASCII unescaped",
        "examples": cases,
        "limit": [
            {
                "name": name,
                "note_length": note_length,
                "bytes": session_settings_size({"note": "x" * note_length}),
                "is_accepted": session_settings_size({"note": "x" * note_length})
                <= MAX_SESSION_SETTINGS_BYTES,
                "is_accepted_by_session": session_settings_size({"note": "x" * note_length})
                <= session_limit,
            }
            for name, note_length in (
                ("at-the-limit", at_the_limit),
                ("over-the-limit", at_the_limit + 1),
                ("at-the-session-limit", at_the_limit + SESSION_SETTINGS_SIZE_TOLERANCE_BYTES),
                (
                    "over-the-session-limit",
                    at_the_limit + SESSION_SETTINGS_SIZE_TOLERANCE_BYTES + 1,
                ),
            )
        ],
    }


def step_input_type_name(step_type: str, input_type: object) -> str:
    # RunModel always receives null. The Python core types it like its failure route's input.
    if step_type == "RunModel" and type_name_of(input_type) == "TurnFailureRouteInput":
        return "null"
    return type_name_of(input_type)


def flow_type_hint(hint: object) -> object:
    """Resolve a string annotation of the session Flow module."""
    if isinstance(hint, str):
        import dex_agents.session_flow as session_flow_module

        return eval(hint, vars(session_flow_module))
    return hint


def rpc_output_hint(hint: object) -> object:
    resolved = flow_type_hint(hint)
    (output,) = get_args(resolved)
    return output


# ---------------------------------------------------------------------------
# Model-visible texts
# ---------------------------------------------------------------------------


def final_record(status: ToolCallStatus, deferral_order: int, **changes: Any) -> AgentToolCall:
    return minimal_tool_call(
        tool_call_id=f"call-{deferral_order + 1}",
        tool_name=["lookup_order", "send_email", "transfer_funds", "publish_note"][
            deferral_order % 4
        ],
        arguments=['{"query": "7"}', '{"to": "ops"}', '{"amount": 5}', "{}"][deferral_order % 4],
        status=status,
        deferral_order=deferral_order,
        **changes,
    )


def note_records() -> list[AgentToolCall]:
    # Out of deferral order on purpose: notes list records by deferral_order.
    return [
        final_record(ToolCallStatus.REJECTED, 2, rejection_message=None),
        final_record(ToolCallStatus.SUCCEEDED, 0, output="found 7"),
        final_record(ToolCallStatus.OUTCOME_UNKNOWN, 3),
        final_record(ToolCallStatus.FAILED, 1, error="smtp down"),
    ]


def record_json(record: AgentToolCall) -> Any:
    return json.loads(encode("AgentToolCall", record))


def argument_errors(parse: Callable[[str], object], cases: Sequence[str]) -> list[dict[str, Any]]:
    entries: list[dict[str, Any]] = []
    for arguments in cases:
        try:
            parse(arguments)
        except BuiltInToolArgumentsError as error:
            entries.append({"arguments": arguments, "error": str(error)})
        else:
            entries.append({"arguments": arguments, "error": None})
    return entries


MCP_SERVER_NAMES = ("docs", "tickets")

#: MCP tool results as servers send them, for the text the model sees.
MCP_TOOL_RESULTS: tuple[dict[str, Any], ...] = (
    {"content": [], "structuredContent": {"temp": 21, "city": "Zürich", "10": 1, "9": 2.0}},
    {
        "content": [
            {"type": "text", "text": "Grüße ☕"},
            {"type": "image", "data": "aGk=", "mimeType": "image/png"},
        ]
    },
    {"content": [{"type": "text", "text": "first"}, {"type": "text", "text": "second"}]},
)


def mcp_argument_errors() -> list[dict[str, Any]]:
    """Return the model-visible error of each malformed MCP resource or prompt tool call."""
    cases: list[tuple[str, dict[str, Any]]] = [
        ("mcp_read_resource", {"server": 7}),
        ("mcp_read_resource", {"server": "docs", "uri": 7}),
        ("mcp_get_prompt", {"server": "docs", "name": None}),
        ("mcp_get_prompt", {"server": "docs", "name": "greet", "arguments": {"who": 1}}),
    ]
    entries: list[dict[str, Any]] = []
    for tool_name, arguments in cases:
        try:
            read_server_name(arguments)
            # Every case fails before the client is used.
            asyncio.run(run_resource_and_prompt_tool(None, tool_name, arguments))  # type: ignore[arg-type]
        except ToolExecutionError as error:
            entries.append({"tool_name": tool_name, "arguments": arguments, "error": str(error)})
        else:
            raise AssertionError(f"{tool_name} {arguments} did not fail")
    return entries


def build_tool_source_envelope_cases() -> dict[str, Any]:
    """Return the Stepwise tool source envelope: how each core writes it and what reads back."""
    cases = []
    for source_name, reference in (
        ("docs", '{"server":"docs","tool":"search"}'),
        ("tickets-☕", None),
    ):
        python_text = _write_tool_source_envelope(source_name, reference)
        assert _read_tool_source_envelope(python_text) == (source_name, reference)
        envelope = {"stepwise_tool_source": source_name, "reference": reference}
        cases.append(
            {
                "source_name": source_name,
                "reference": reference,
                "written": [
                    python_text,
                    json.dumps(envelope, ensure_ascii=False, separators=(",", ":")),
                ],
                "canonical": canonicalize_json(envelope),
            }
        )
    return {
        "description": (
            "AgentToolCall.tool_source_reference of a call that a Stepwise tool source "
            "built: JSON text naming the source and holding the source's own reference. "
            "Not model-visible, so it compares in canonical JSON. Every core reads every "
            "written form, and its own written form canonicalizes to canonical."
        ),
        "keys": {"source_name": "stepwise_tool_source", "reference": "reference"},
        "cases": cases,
    }


def build_texts() -> dict[str, Any]:
    records = note_records()
    plan = full_plan()
    constants = {
        "outcome_unknown_tool_result": OUTCOME_UNKNOWN_TOOL_RESULT,
        "default_rejection_tool_result": DEFAULT_REJECTION_TOOL_RESULT,
        "approval_deadline_passed": APPROVAL_DEADLINE_PASSED,
        "question_deadline_passed": QUESTION_DEADLINE_PASSED,
        "question_declined_tool_result": QUESTION_DECLINED_TOOL_RESULT,
        "turn_failure_note_header": TURN_FAILURE_NOTE_HEADER,
        "turn_failure_note_footer": TURN_FAILURE_NOTE_FOOTER,
        "turn_steered_note_header": TURN_STEERED_NOTE_HEADER,
        "turn_cancelled_note_header": TURN_CANCELLED_NOTE_HEADER,
        "tool_call_cancelled_by_steering_result": TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
        "tool_call_cancelled_by_turn_cancellation_result": (
            TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT
        ),
        "write_todos_called_more_than_once": WRITE_TODOS_CALLED_MORE_THAN_ONCE,
        "planning_turn_tool_rejection": PLANNING_TURN_TOOL_REJECTION,
        "no_plan_note": NO_PLAN_NOTE,
        "planning_turn_instruction": PLANNING_TURN_INSTRUCTION,
        "blank_tool_task_id": BLANK_TOOL_TASK_ID,
        "invalid_tool_task_poll_interval": INVALID_TOOL_TASK_POLL_INTERVAL,
        "tool_task_abandoned_error": TOOL_TASK_ABANDONED_ERROR,
        "tool_call_not_run_error_prefix": TOOL_CALL_NOT_RUN_ERROR_PREFIX,
        "model_call_refused_error_prefix": MODEL_CALL_REFUSED_ERROR_PREFIX,
        "stepwise_context_summary_note_header": CONTEXT_SUMMARY_NOTE_HEADER,
        "stepwise_context_summary_instructions": CONTEXT_SUMMARY_INSTRUCTIONS,
        "stepwise_context_summary_request": CONTEXT_SUMMARY_REQUEST,
        "adapter_context_summary_transcript_header": CONTEXT_SUMMARY_TRANSCRIPT_HEADER,
    }
    templates: dict[str, list[dict[str, Any]]] = {
        "tool_call_outcome": [
            {"input": {"record": record_json(record)}, "text": describe_tool_call_outcome(record)}
            for record in [
                final_record(ToolCallStatus.SUCCEEDED, 0, output=TRICKY_TEXT),
                final_record(ToolCallStatus.SUCCEEDED, 0, output=None),
                final_record(ToolCallStatus.FAILED, 0, error="smtp down"),
                final_record(ToolCallStatus.FAILED, 0, error=None),
                final_record(ToolCallStatus.REJECTED, 0, rejection_message="not today"),
                final_record(ToolCallStatus.REJECTED, 0, rejection_message=None),
                final_record(ToolCallStatus.OUTCOME_UNKNOWN, 0),
            ]
        ],
        "turn_failure_note": [
            {
                "input": {"records": [record_json(record) for record in records]},
                "text": build_turn_failure_note(records),
            }
        ],
        "turn_steered_note": [
            {
                "input": {"records": [record_json(record) for record in records[:2]]},
                "text": build_tool_call_note(TURN_STEERED_NOTE_HEADER, records[:2]),
            }
        ],
        "turn_cancelled_note": [
            {
                "input": {"records": [record_json(record) for record in records[2:]]},
                "text": build_tool_call_note(TURN_CANCELLED_NOTE_HEADER, records[2:]),
            }
        ],
        "plan_execution_message": [
            {"input": {"revision": revision}, "text": build_plan_execution_message(revision)}
            for revision in (1, 12)
        ],
        "plan_note": [
            {
                "input": {
                    "plan": None
                    if case_plan is None
                    else json.loads(encode("AgentPlan", case_plan)),
                    "interaction_mode": str(mode),
                },
                "text": build_plan_note(case_plan, mode),
            }
            for case_plan, mode in [
                (plan, InteractionMode.CHAT),
                (plan, InteractionMode.PLANNING),
                (plan, InteractionMode.EXECUTING),
                (None, InteractionMode.PLANNING),
                (None, InteractionMode.CHAT),
                (
                    AgentPlan(revision=4, status=PlanStatus.CLEARED, tasks=[], updated_at=LATER),
                    InteractionMode.PLANNING,
                ),
                (replace(plan, status=PlanStatus.DRAFT), InteractionMode.PLANNING),
            ]
        ],
        "question_answered_output": [
            {"input": {"answer": answer}, "text": build_question_answered_output(answer)}
            for answer in ("checking", TRICKY_TEXT)
        ],
        "durable_wait_completed_output": [
            {
                "input": {"waited_seconds": 30, "reason": reason},
                "text": build_durable_wait_completed_output(30, reason),
            }
            for reason in ("the build runs", TRICKY_TEXT)
        ],
        "plan_updated_output": [
            {
                "input": {"revision": 2, "task_count": 3},
                "text": build_plan_updated_output(2, 3),
            }
        ],
        "plan_cleared_output": [{"input": {"revision": 4}, "text": build_plan_cleared_output(4)}],
        "stepwise_tool_call_limit_reached_result": [
            {
                "input": {"max_model_calls_per_turn": 100},
                "text": TOOL_CALL_LIMIT_REACHED_RESULT.format(max_model_calls_per_turn=100),
            }
        ],
        "stepwise_context_summary_omission_note": [
            {"input": {"omitted_count": count}, "text": build_context_summary_omission_note(count)}
            for count in (1, 3)
        ],
        "stepwise_unknown_tool_error": [
            {"input": {"tool_name": name}, "text": build_unknown_tool_error(name)}
            for name in ("lookup_invoice", "mcp_docs_search")
        ],
        "mcp_tool_output_json": [
            {"input": {"value": value}, "text": serialize_tool_output_json(json.loads(value))}
            for value in (
                '{"zeta": 1, "alpha": [{"b": 2.0, "a": "Grüße"}], "mid": null}',
                '{"contents": [{"uri": "docs://readme", "text": "<ok> & \\"quoted\\""}]}',
                '{"b": 1, "10": 2, "9": 3, "\\ud83d\\ude00": 4, "\\ue000": 5, "a": {"2": 0}}',
            )
        ],
        "mcp_tool_result_text": [
            {
                "input": {"result": result},
                "text": render_tool_result_text(CallToolResult.model_validate(result)),
            }
            for result in MCP_TOOL_RESULTS
        ],
        "stepwise_context_summary_note": [
            {
                "input": {"summary": json.loads(encode("ContextSummary", full_summary()))},
                "text": build_context_summary_note(full_summary()),
            }
        ],
        "adapter_context_summary_prompt": [
            {
                "input": {
                    "previous_summary": None
                    if previous is None
                    else json.loads(encode("ContextSummary", previous)),
                    "messages": [json.loads(encode("AgentMessage", m)) for m in messages],
                    "input_token_budget": budget,
                },
                "text": build_context_summary_prompt(
                    summary_prompt_request(previous, messages, budget)
                ),
            }
            for previous, messages, budget in summary_prompt_cases()
        ],
    }
    max_wait = int(DEFAULT_MAX_DURABLE_WAIT.total_seconds())
    argument_validation = {
        "ask_user": argument_errors(
            parse_ask_user_arguments,
            [
                '{"question": "Which?", "choices": ["checking"], "allows_free_text": false}',
                '{"question": "Which account?", "choices": null, "allows_free_text": null}',
                "[]",
                "not json",
                '{"question": NaN}',
                '{"question": " "}',
                '{"question": "Q", "choices": ["a", ""]}',
                '{"question": "Q", "allows_free_text": "yes"}',
                '{"question": "Q", "zeta": 1, "alpha": 2}',
            ],
        ),
        "durable_wait": argument_errors(
            lambda arguments: parse_durable_wait_arguments(arguments, DEFAULT_MAX_DURABLE_WAIT),
            [
                '{"duration_seconds": 30, "reason": "the build runs"}',
                '{"duration_seconds": 2.0, "reason": "whole"}',
                '{"duration_seconds": 2.5, "reason": "fraction"}',
                '{"duration_seconds": true, "reason": "bool"}',
                '{"duration_seconds": 0, "reason": "zero"}',
                f'{{"duration_seconds": {max_wait + 1}, "reason": "too long"}}',
                '{"duration_seconds": 5}',
                '{"duration_seconds": 5, "reason": "x", "extra": 1}',
            ],
        ),
        "write_todos": argument_errors(
            parse_write_todos_arguments,
            [
                '{"todos": [{"content": "Refund", "status": "pending"}]}',
                '{"todos": []}',
                "[]",
                '{"todos": {}}',
                '{"todos": [1]}',
                '{"todos": [{"content": "x", "status": "pending", "owner": "me"}]}',
                '{"todos": [{"content": " ", "status": "pending"}]}',
                '{"todos": [{"content": "x", "status": "done"}]}',
                '{"todos": [], "plan": 1}',
            ],
        ),
    }
    return {
        "description": (
            "Every text a model sees that the session, not the model or a tool, wrote. "
            "Compare byte for byte."
        ),
        "constants": constants,
        "templates": templates,
        "built_in_tool_definitions": [
            {
                "name": str(definition.name),
                "description": definition.description,
                "parameters_json": definition.parameters_json,
            }
            for definition in BUILT_IN_TOOL_DEFINITIONS
        ],
        "built_in_tool_argument_errors": argument_validation,
        "mcp_resource_and_prompt_tools": {
            "server_names": list(MCP_SERVER_NAMES),
            "definitions": [
                {
                    "name": tool.name,
                    "description": tool.description,
                    "input_schema_json": json.dumps(
                        tool.input_schema, ensure_ascii=False, separators=(",", ":")
                    ),
                }
                for tool in build_resource_and_prompt_tools(MCP_SERVER_NAMES)
            ],
            "argument_errors": mcp_argument_errors(),
        },
        "language_specific": {
            "missing_tool_task_poller": {
                "python": MISSING_TOOL_TASK_POLLER,
                "note": "Names the runner method in each language, such as pollToolTask.",
            }
        },
    }


# ---------------------------------------------------------------------------
# Files
# ---------------------------------------------------------------------------


def render_json(document: Any) -> str:
    return json.dumps(document, indent=2, ensure_ascii=False) + "\n"


def build_files() -> dict[Path, str]:
    files = {
        VALUES_DIR / f"{document['type']}.json": render_json(document)
        for document in build_value_documents()
    }
    files[NAMES_FILE] = render_json(build_names())
    files[TEXTS_FILE] = render_json(build_texts())
    session_graph = build_session_flow_graph()
    for path in (SESSION_GRAPH_FILE, *SESSION_GRAPH_CORE_COPIES):
        files[path] = render_json(session_graph)
    renderings = build_session_graph_renderings(session_graph)
    files[SESSION_GRAPH_RENDERINGS_FILE] = render_json(renderings)
    return files


def stale_files(files: Mapping[Path, str]) -> list[str]:
    stale = [
        str(path.relative_to(SUITE_DIR.parent))
        for path, text in files.items()
        if not path.exists() or path.read_text(encoding="utf-8") != text
    ]
    stale.extend(
        str(path.relative_to(SUITE_DIR.parent))
        for path in sorted(VALUES_DIR.glob("*.json"))
        if path not in files
    )
    return stale


def main(arguments: Sequence[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--check", action="store_true", help="fail when a file is stale")
    parser.add_argument(
        "--transcripts",
        action="store_true",
        help="also run the scenarios on the Python core and rewrite their expected results",
    )
    options = parser.parse_args(arguments)
    files = build_files()
    if options.check:
        stale = stale_files(files)
        if stale:
            print("stale conformance files; run generate.py:", *stale, sep="\n  ")
            return 1
        return 0
    VALUES_DIR.mkdir(exist_ok=True)
    for path in VALUES_DIR.glob("*.json"):
        if path not in files:
            path.unlink()
    for path, text in files.items():
        path.write_text(text, encoding="utf-8")
    if options.transcripts:
        # The scenario runner lives in the Python core's tests package.
        sys.path.insert(0, str(Path.cwd()))
        from tests.conformance_transcripts import write_expected_transcripts

        asyncio.run(write_expected_transcripts(TRANSCRIPTS_DIR))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
