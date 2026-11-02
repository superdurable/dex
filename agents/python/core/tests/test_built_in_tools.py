# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Built-in tool names are reserved only when enabled; function tools check their arguments."""

from __future__ import annotations

import dataclasses
import enum
from datetime import timedelta
from typing import Annotated, Literal, TypedDict

import pytest

from dex_agents import (
    AgentEventWriter,
    AgentSessionOptions,
    AgentTurnContext,
    BuiltInTool,
    InterruptKind,
    ToolEffect,
    ToolExecutionError,
    ToolPolicy,
)
from dex_agents.stepwise import (
    AgentDefinition,
    StepwiseAgentRunner,
    ToolCallContext,
    function_tool,
)
from dex_agents.testing import ScriptedModelClient, ScriptedModelReply, wait_for_agent_snapshot
from tests.conftest import ScriptedAgentHarness


class Priority(enum.Enum):
    LOW = "low"
    HIGH = "high"


@dataclasses.dataclass
class Address:
    city: str
    zip_code: str | None = None


class Filters(TypedDict, total=False):
    status: str


@function_tool(effect=ToolEffect.IDEMPOTENT_WRITE)
async def create_ticket(
    title: Annotated[str, "Short summary"],
    priority: Priority,
    address: Address,
    tags: list[str],
    context: ToolCallContext,
    channel: Literal["email", "chat"] = "email",
    filters: Filters | None = None,
    retries: int = 0,
) -> dict[str, object]:
    """Create a support ticket."""
    return {
        "title": title,
        "priority": priority,
        "city": address.city,
        "tags": tags,
        "channel": channel,
        "key": context.idempotency_key,
    }


def test_enabled_built_in_names_are_reserved() -> None:
    with pytest.raises(ValueError, match="enabled built-in tool"):
        AgentSessionOptions(
            tool_policies={"ask_user": ToolPolicy(ToolEffect.READ)},
            built_in_tools=frozenset({BuiltInTool.ASK_USER}),
        )
    assert AgentSessionOptions(tool_policies={"ask_user": ToolPolicy(ToolEffect.READ)})

    @function_tool(effect=ToolEffect.READ, name="durable_wait")
    def application_wait() -> str:
        """An application tool that reuses a built-in name."""
        return "waited"

    model = ScriptedModelClient(lambda request: ScriptedModelReply(text="ok"))
    with pytest.raises(ValueError, match="enabled built-in tool"):
        AgentDefinition(
            name="agent",
            instructions="Help.",
            model=model,
            tools=[application_wait],
            built_in_tools=frozenset({BuiltInTool.DURABLE_WAIT}),
        )
    assert AgentDefinition(
        name="agent", instructions="Help.", model=model, tools=[application_wait]
    )


async def test_a_session_built_in_sharing_a_function_tool_name_fails_before_any_model_call() -> (
    None
):
    @function_tool(effect=ToolEffect.READ, name="ask_user")
    def application_ask_user() -> str:
        """An application tool that reuses a built-in name."""
        return "asked"

    model = ScriptedModelClient(lambda request: ScriptedModelReply(text="never"))
    runner = StepwiseAgentRunner(
        AgentDefinition(
            name="agent", instructions="Help.", model=model, tools=[application_ask_user]
        )
    )
    turn = AgentTurnContext(
        flow_id="session-1",
        turn_id="turn-1",
        attempt=1,
        messages=[],
        framework_run_state=None,
        tool_calls={},
        tool_policies={},
        default_tool_effect=ToolEffect.UNCLASSIFIED,
        events=AgentEventWriter(lambda event: None, "turn-1", 1),
        heartbeat=_no_heartbeat,
        enabled_built_in_tools=frozenset({BuiltInTool.ASK_USER}),
    )
    with pytest.raises(ValueError, match="tool ask_user uses the name of an enabled built-in tool"):
        await runner.run_model_step(turn)
    assert model.requests == ()


def test_invalid_session_options_are_refused() -> None:
    with pytest.raises(ValueError):
        AgentSessionOptions(built_in_tools=frozenset({"write_todos"}))  # type: ignore[arg-type]
    with pytest.raises(ValueError, match="whole number of seconds"):
        AgentSessionOptions(max_durable_wait=timedelta(milliseconds=1500))
    assert AgentSessionOptions(built_in_tools=frozenset({"ask_user"})).built_in_tools == {  # type: ignore[arg-type]
        BuiltInTool.ASK_USER
    }


async def test_a_disabled_built_in_name_is_an_ordinary_tool(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call ask_user {"question": "Ready?"}')
    interrupt = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(iter(snapshot.pending_interrupts), None),
        description="the approval of an ordinary ask_user tool",
    )
    assert interrupt.kind is InterruptKind.TOOL_APPROVAL
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "ask_user: The tool call failed: unknown tool ask_user"
    executed = await harness.executed_step_types(session_id)
    assert "CallSingleAttemptTool" in executed
    assert "AwaitQuestionAnswer" not in executed


def test_function_tool_schemas_come_from_type_hints() -> None:
    assert create_ticket.name == "create_ticket"
    assert create_ticket.description == "Create a support ticket."
    assert create_ticket.policy == ToolPolicy(ToolEffect.IDEMPOTENT_WRITE, False)
    assert create_ticket.parameters == {
        "type": "object",
        "properties": {
            "title": {"type": "string", "description": "Short summary"},
            "priority": {"enum": ["low", "high"]},
            "address": {
                "type": "object",
                "properties": {
                    "city": {"type": "string"},
                    "zip_code": {"anyOf": [{"type": "string"}, {"type": "null"}]},
                },
                "required": ["city"],
                "additionalProperties": False,
            },
            "tags": {"type": "array", "items": {"type": "string"}},
            "channel": {"enum": ["email", "chat"]},
            "filters": {
                "anyOf": [
                    {
                        "type": "object",
                        "properties": {"status": {"type": "string"}},
                        "required": [],
                        "additionalProperties": False,
                    },
                    {"type": "null"},
                ]
            },
            "retries": {"type": "integer"},
        },
        "required": ["title", "priority", "address", "tags"],
        "additionalProperties": False,
    }


async def test_function_tools_check_and_convert_their_arguments() -> None:
    context = ToolCallContext(
        flow_id="session-1",
        turn_id="turn-1",
        tool_call_id="call-1",
        idempotency_key="session-1/turn-1/call-1",
        attempt=1,
        events=AgentEventWriter(lambda event: None, "turn-1", 1),
        heartbeat=_no_heartbeat,
    )
    output = await create_ticket.invoke(
        '{"title": "Broken", "priority": "high", "address": {"city": "Oslo"},'
        ' "tags": ["a"], "retries": 2.0}',
        context,
    )
    assert output == (
        '{"title": "Broken", "priority": "high", "city": "Oslo", "tags": ["a"],'
        ' "channel": "email", "key": "session-1/turn-1/call-1"}'
    )
    for arguments, problem in [
        ('{"title": "x"}', "arguments.priority is required"),
        (
            '{"title": "x", "priority": "urgent", "address": {"city": "Oslo"}, "tags": []}',
            'arguments.priority must be one of ["low", "high"]',
        ),
        (
            '{"title": "x", "priority": "low", "address": {"city": 7}, "tags": []}',
            "arguments.address.city must be string",
        ),
        (
            '{"title": "x", "priority": "low", "address": {"city": "a"}, "tags": [], "extra": 1}',
            "arguments.extra is not allowed",
        ),
        ("[1]", "expected a JSON object"),
        ('{"title": ', "arguments are not valid JSON"),
    ]:
        with pytest.raises(
            ToolExecutionError, match="invalid arguments for create_ticket"
        ) as error:
            await create_ticket.invoke(arguments, context)
        assert problem in str(error.value)


async def _no_heartbeat() -> None:
    return None
