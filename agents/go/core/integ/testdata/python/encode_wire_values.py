# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Print the Python core's encodings of wire values, payloads, and texts as one JSON object.

The Go parity test decodes and re-encodes each value and compares the bytes.
"""

from __future__ import annotations

import asyncio
import json

from dex.codec import CodecRegistry

from dex_agents import AgentEventWriter
from dex_agents.runner import (
    ToolExecutionError,
    DeferredToolCall,
    ToolPolicy,
    build_turn_failure_note,
    describe_tool_call_outcome,
)
from dex_agents.stepwise.model import ModelOutputItem, ModelToolCall, ToolResultItem, UserTextItem
from dex_agents.stepwise.function_tools import function_tool
from dex_agents.stepwise.transcript import encode_stepwise_message
from dex_agents.values import (
    AgentEvent,
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
    ContextSummary,
    InteractionMode,
    InterruptKind,
    ListAgentMessagesRequest,
    MessageRole,
    PendingUserMessage,
    PlanStatus,
    PlanTaskStatus,
    QuestionAnswer,
    QuestionResolution,
    ResolveInterruptRequest,
    ToolCallCancellationReason,
    ToolCallCompleted,
    ToolCallStatus,
    ToolEffect,
    TurnReplySequence,
)

del DeferredToolCall, ToolPolicy  # imported to fail fast if the public runner names move

STATE = AgentState(
    status=AgentSessionStatus.RUNNING_TOOLS,
    turn_count=2,
    active_turn_id="turn-3",
    next_message_sequence=9,
    last_turn_error=None,
    interaction_mode=InteractionMode.PLANNING,
    accepts_turn_control=True,
)
MESSAGE = AgentMessage(
    sequence=7,
    turn_id="turn-3",
    role=MessageRole.ASSISTANT,
    text="Ünïcode <&> \"quoted\"\nline",
    tool_call_id="call-1",
    tool_name="lookup_order",
    tool_arguments='{"order_id": "7"}',
    tool_output=None,
    framework_item='{"type":"user_text","text":"x"}',
    created_at="2026-11-01T12:00:00.123Z",
)
ANSWER = QuestionAnswer(
    interrupt_id="call-2-input-1", question="Which?", answer="that", resolution=QuestionResolution.DECLINED
)
TOOL_CALLS = [
    AgentToolCall(
        tool_call_id="call-1",
        turn_id="turn-3",
        tool_name="lookup_order",
        arguments='{"order_id": "7"}',
        effect=ToolEffect.READ,
        requires_approval=False,
        status=ToolCallStatus.SUCCEEDED,
        output="order 7 is shipped",
        deferral_order=0,
    ),
    AgentToolCall(
        tool_call_id="call-2",
        turn_id="turn-3",
        tool_name="refund_order",
        arguments="{}",
        effect=ToolEffect.NON_IDEMPOTENT_WRITE,
        requires_approval=True,
        status=ToolCallStatus.REJECTED,
        rejection_message="no",
        deferral_order=1,
        tool_source_reference="server/tool",
        trace_context="00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
        wait_until="2026-11-01T12:00:00.000Z",
        input_continuation="{}",
        input_answers=[ANSWER],
        input_question="Which?",
        input_choices=["this", "that"],
        input_allows_free_text=False,
        cancellation_reason=ToolCallCancellationReason.STEERED,
        task_id="task-1",
        task_poll_interval_seconds=5,
        task_answer_pending=True,
    ),
    AgentToolCall(
        tool_call_id="call-3",
        turn_id="turn-3",
        tool_name="charge_card",
        arguments='{"amount": 5}',
        effect=ToolEffect.UNCLASSIFIED,
        requires_approval=True,
        status=ToolCallStatus.OUTCOME_UNKNOWN,
        error="Worker lost",
        deferral_order=2,
    ),
    AgentToolCall(
        tool_call_id="call-4",
        turn_id="turn-3",
        tool_name="search",
        arguments="{}",
        effect=ToolEffect.IDEMPOTENT_WRITE,
        requires_approval=False,
        status=ToolCallStatus.FAILED,
        error="bad query",
        deferral_order=3,
    ),
]
INTERRUPT = AgentInterrupt(
    interrupt_id="call-2",
    kind=InterruptKind.TOOL_APPROVAL,
    tool_call_id="call-2",
    tool_name="refund_order",
    arguments="{}",
    created_at="2026-11-01T12:00:00.123Z",
    deadline="2026-11-02T12:00:00.123Z",
)
USAGE = AgentUsage(
    model_calls=2, input_tokens=20, output_tokens=10, cached_input_tokens=3, reasoning_tokens=4
)
PLAN = AgentPlan(
    revision=2,
    status=PlanStatus.ACTIVE,
    tasks=[AgentPlanTask(content="Look up", status=PlanTaskStatus.IN_PROGRESS)],
    updated_at="2026-11-01T12:00:00.123Z",
)
SUMMARY = ContextSummary(
    generation=1, first_sequence=1, last_sequence=4, text="earlier", created_at="2026-11-01T12:00:00.123Z"
)
EVENT = AgentEvent(
    type="tool_call_finished",
    turn_id="turn-3",
    attempt=2,
    payload='{"status": "succeeded"}',
    step_execution_id="CallTool-4",
)

VALUES: list[tuple[str, type, object]] = [
    (
        "AgentSessionStartInput",
        AgentSessionStartInput,
        AgentSessionStartInput(initial_message="hi", start_environment={"host_name": "h\u00f6st"}),
    ),
    ("AgentState", AgentState, STATE),
    ("AgentMessage", AgentMessage, MESSAGE),
    *((f"AgentToolCall/{call.tool_call_id}", AgentToolCall, call) for call in TOOL_CALLS),
    ("AgentMessageChunk", AgentMessageChunk, AgentMessageChunk(messages=[MESSAGE])),
    (
        "AgentMessageIndex",
        AgentMessageIndex,
        AgentMessageIndex(
            chunks=[
                AgentMessageChunkRange(first_sequence=33, last_sequence=64),
                AgentMessageChunkRange(first_sequence=65, last_sequence=96),
                AgentMessageChunkRange(first_sequence=99, last_sequence=99),
            ],
            first_sequence=33,
            window_first_sequence=97,
        ),
    ),
    (
        "TurnReplySequence",
        TurnReplySequence,
        TurnReplySequence(turn_id="turn-3", sequence=12),
    ),
    (
        "AgentMessageIndex/before-the-first-turn",
        AgentMessageIndex,
        AgentMessageIndex(chunks=[], first_sequence=1, window_first_sequence=1),
    ),
    ("AgentInterrupt", AgentInterrupt, INTERRUPT),
    ("PendingInterrupts", list[AgentInterrupt], [INTERRUPT]),
    (
        "AgentInterruptResolution",
        AgentInterruptResolution,
        AgentInterruptResolution(approved=False, rejection_message="no"),
    ),
    (
        "ResolveInterruptRequest",
        ResolveInterruptRequest,
        ResolveInterruptRequest(interrupt_id="call-2", approved=True),
    ),
    ("AgentUsage", AgentUsage, USAGE),
    ("AgentUserMessage", AgentUserMessage, AgentUserMessage(text="hello")),
    ("ToolCallCompleted", ToolCallCompleted, ToolCallCompleted(tool_call_id="call-1")),
    ("AgentEvent", AgentEvent, EVENT),
    ("ListAgentMessagesRequest", ListAgentMessagesRequest, ListAgentMessagesRequest(before_sequence=5)),
    (
        "AgentMessagesPage",
        AgentMessagesPage,
        AgentMessagesPage(messages=[MESSAGE], next_before_sequence=7),
    ),
    (
        "AgentMessagesPage/after-retention",
        AgentMessagesPage,
        AgentMessagesPage(messages=[MESSAGE], next_before_sequence=None, deleted_before_sequence=33),
    ),
    (
        "AgentSnapshot",
        AgentSnapshot,
        AgentSnapshot(
            state=STATE,
            messages=[MESSAGE],
            pending_interrupts=[INTERRUPT],
            tool_calls=TOOL_CALLS,
            usage=USAGE,
            queued_message_count=1,
            plan=PLAN,
            queued_messages=[
                PendingUserMessage(message_id="m-1", message=AgentUserMessage(text="queued"))
            ],
            context_summary=SUMMARY,
        ),
    ),
    ("AgentSnapshot/empty", AgentSnapshot, AgentSnapshot(state=None)),
]


def encode_values() -> dict[str, str]:
    registry = CodecRegistry()
    return {name: registry.resolve(type_hint).encode(value).data for name, type_hint, value in VALUES}


def event_payloads() -> list[dict[str, object]]:
    events: list[AgentEvent] = []
    writer = AgentEventWriter(events.append, "turn-1", 1, "RunModel-2")
    writer.write_event("turn_started", {"sequence": 1})
    writer.write_event(
        "usage_recorded",
        {
            "recorded": {"model_calls": 1, "input_tokens": 10, "output_tokens": 5,
                         "cached_input_tokens": 0, "reasoning_tokens": 0},
            "session": {"model_calls": 2, "input_tokens": 20, "output_tokens": 10,
                        "cached_input_tokens": 0, "reasoning_tokens": 0},
        },
    )
    writer.write_event(
        "interrupt_raised",
        {"interrupt_id": "call-2", "tool_call_id": "call-2", "tool_name": "refund_order",
         "deadline": None, "kind": "tool_approval", "question": None, "choices": [],
         "allows_free_text": True},
    )
    writer.write_event(
        "interrupt_resolved",
        {"interrupt_id": "call-2", "approved": False, "rejection_message": "Ünï <no>",
         "kind": "tool_approval", "answered": False, "cancellation_reason": None},
    )
    writer.write_text_delta("Ünïcode ✓ \"q\" \\ \t")
    return [
        {"type": event.type, "payload": event.payload, "attempt": event.attempt,
         "step_execution_id": event.step_execution_id}
        for event in events
    ]


def stepwise_items() -> list[str]:
    items = [
        UserTextItem(text="Ünïcode <&> \"q\""),
        ToolResultItem(tool_call_id="call-1", tool_name="lookup_order", output="ok ✓"),
        ModelOutputItem(provider="openai", api="responses", item='{"type":"message"}', text="hi"),
        ModelOutputItem(
            provider="dex-scripted",
            api="scripted",
            item='{"type":"function_call"}',
            tool_calls=[ModelToolCall(tool_call_id="c", tool_name="t", arguments='{"a": 1}')],
        ),
    ]
    return [encode_stepwise_message(item).framework_item for item in items]


SCHEMA = {
    "type": "object",
    "properties": {
        "order_id": {"type": "string"},
        "amount": {"type": "integer", "minimum": 1, "maximum": 100.5},
        "mode": {"enum": ["fast", "slow", 3]},
        "tags": {"type": "array", "items": {"type": ["string", "null"]}},
        "either": {"anyOf": [{"type": "string"}, {"type": "boolean"}]},
    },
    "required": ["order_id"],
    "additionalProperties": False,
}
INVALID_ARGUMENTS = [
    "",
    "not json",
    "[1]",
    '{"amount": 2}',
    '{"order_id": 7}',
    '{"order_id": "7", "amount": 0}',
    '{"order_id": "7", "amount": 101}',
    '{"order_id": "7", "amount": 1.5}',
    '{"order_id": "7", "amount": 2.0, "mode": "medium"}',
    '{"order_id": "7", "tags": ["a", 1]}',
    '{"order_id": "7", "either": 1}',
    '{"order_id": "7", "extra": true}',
    '{"order_id": "7", "mode": 3.0}',
]


async def schema_messages() -> list[str]:
    @function_tool(effect=ToolEffect.READ, name="checked", parameters_schema=SCHEMA)
    def checked(
        order_id: object = None,
        amount: object = None,
        mode: object = None,
        tags: object = None,
        either: object = None,
    ) -> str:
        return "ok"

    messages: list[str] = []
    for arguments in INVALID_ARGUMENTS:
        try:
            messages.append(await checked.invoke(arguments, None))  # type: ignore[arg-type]
        except ToolExecutionError as error:
            messages.append(str(error))
    return messages


class _DiscardedOutput:
    def write_text_delta(self, text: str) -> None:
        pass

    def write_reasoning_delta(self, text: str) -> None:
        pass


def anthropic_item() -> dict[str, object]:
    """Store an Anthropic response as the LiteLLM client does, and replay it."""
    from dex_agents.litellm import _chat_messages, _ChatMessageAssembler, _compact_json  # noqa: PLC0415
    from dex_agents.stepwise.model import ModelRequest, ModelRequestMetadata, ModelSettings  # noqa: PLC0415

    assembler = _ChatMessageAssembler()
    for chunk in [
        {"model": "claude-test", "choices": [{"delta": {"thinking_blocks": [
            {"type": "thinking", "thinking": "Check the order.", "signature": "sig=="}]}}]},
        {"choices": [{"delta": {"thinking_blocks": [{"type": "redacted_thinking", "data": "opaque=="}]}}]},
        {"choices": [{"delta": {"content": "Looking it up."}}]},
        {"choices": [{"delta": {"tool_calls": [{"index": 0, "id": "toolu_1", "function": {
            "name": "lookup_order", "arguments": '{"order_id": "7"}'}}]}, "finish_reason": "tool_calls"}]},
    ]:
        assembler.add_chunk(chunk, _DiscardedOutput())
    item = _compact_json(assembler.stored_message())
    replayed = _chat_messages(
        ModelRequest(
            instructions="",
            input_items=[ModelOutputItem(provider="anthropic", api="chat_completions", item=item)],
            tools=[],
            tool_choice="auto",
            settings=ModelSettings(),
            metadata=ModelRequestMetadata(flow_id="f", turn_id="t", step_execution_id="s", attempt=1),
        ),
        provider="anthropic",
        api="chat_completions",
    )
    return {"item": item, "replayed": replayed}


def main() -> None:
    print(
        json.dumps(
            {
                "values": encode_values(),
                "events": event_payloads(),
                "stepwise_items": stepwise_items(),
                "turn_failure_note": build_turn_failure_note(TOOL_CALLS),
                "outcomes": [describe_tool_call_outcome(call) for call in TOOL_CALLS],
                "anthropic": anthropic_item(),
                "schema": {"schema": SCHEMA, "arguments": INVALID_ARGUMENTS,
                           "messages": asyncio.run(schema_messages())},
            }
        )
    )


if __name__ == "__main__":
    main()
