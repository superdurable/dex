# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Stepwise agent with a scripted model, driven by commands in the user text.

The model reads the current turn's user text:

* ``say <text>`` answers ``said: <text>``.
* ``recall`` answers with the earlier user texts and the replayed item counts.
* ``lookup <query>`` calls ``lookup_order``, then answers with its result.
* ``lookup-gated <gate> <query>`` does the same; the tool waits at the gate.
* ``lookup-then-stall <gate> <query>`` waits at the gate in the second call.
* ``lookup-then-fail <query>`` raises in the second call.
* ``publish <message>`` calls ``publish_note``, which needs approval.
* ``slow-write <gate> <message>`` calls ``slow_write``, which waits at the gate.
* ``ask <question> | <choice> ...`` calls ``ask_user``; ``ask-strict`` disallows
  free text.
* ``ask-raw <json>`` calls ``ask_user`` with raw arguments.
* ``wait <seconds> <reason>`` calls ``durable_wait``.
* ``transfer <amount> [gate]`` calls ``transfer_funds``, which asks which account.
* ``transfer-pair <amount> <amount>`` calls ``transfer_funds`` twice in one response.
* ``loop <obey|defy> <query>`` calls ``lookup_order`` while tools are offered.
  Once none are, ``obey`` answers and ``defy`` asks for a tool anyway.
* ``collide`` calls ``transfer_funds`` as ``pay`` and ``publish_note`` as
  ``pay-input-1``, the ID that ``pay``'s question would take.

``STEPWISE_AGENT_MAX_MODEL_CALLS`` sets ``max_model_calls_per_turn``, and
``STEPWISE_AGENT_UNFINISHED_BUILDER`` set to ``raise`` or ``malformed`` breaks
``build_unfinished_turn_messages``.

After its tool results arrive, the model answers ``<tool_name>: <output>``.
"""

from __future__ import annotations

import json
import os
from collections.abc import Sequence
from datetime import timedelta
from pathlib import Path
from typing import cast

from dex_agents import (
    AgentSessionFlow,
    AgentTurnContext,
    BuiltInTool,
    FrameworkMessage,
    ToolEffect,
    ToolInputRequested,
)
from dex_agents.stepwise import (
    DEFAULT_MAX_MODEL_CALLS_PER_TURN,
    AgentDefinition,
    ModelRequest,
    StepwiseAgentRunner,
    ToolCallContext,
    function_tool,
    stepwise_agent_session_flow,
    stepwise_agent_session_options,
)
from dex_agents.testing import (
    FileGate,
    InvocationLog,
    ScriptedModelClient,
    ScriptedModelReply,
    ScriptedToolCall,
)

STEPWISE_AGENT_FLOW_TYPE = "StepwiseScriptedAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "STEPWISE_AGENT_STATE_DIR"
INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE = "STEPWISE_AGENT_INTERRUPT_DEADLINE_SECONDS"
MAX_MODEL_CALLS_ENVIRONMENT_VARIABLE = "STEPWISE_AGENT_MAX_MODEL_CALLS"
UNFINISHED_BUILDER_ENVIRONMENT_VARIABLE = "STEPWISE_AGENT_UNFINISHED_BUILDER"
INVOCATION_LOG_NAME = "invocations.log"
INSTRUCTIONS = "You are a scripted test agent."


class BrokenUnfinishedTurnBuilderRunner(StepwiseAgentRunner):
    def __init__(self, definition: AgentDefinition, mode: str) -> None:
        super().__init__(definition)
        self._mode = mode

    def build_unfinished_turn_messages(self, turn: AgentTurnContext) -> Sequence[FrameworkMessage]:
        if self._mode == "raise":
            raise RuntimeError("scripted builder defect")
        return cast(Sequence[FrameworkMessage], [{"framework_item": "{}"}])


def build_stepwise_agent_flow(
    state_dir: Path,
    interrupt_deadline: timedelta | None = None,
    max_model_calls_per_turn: int = DEFAULT_MAX_MODEL_CALLS_PER_TURN,
    unfinished_builder_mode: str | None = None,
) -> AgentSessionFlow:
    invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)

    async def wait_at_gate(gate_name: str) -> None:
        gate = FileGate(state_dir, gate_name)
        gate.mark_reached()
        await gate.wait_for_release()

    @function_tool(effect=ToolEffect.READ)
    async def lookup_order(query: str, gate: str | None = None) -> str:
        """Look up an order."""
        invocations.record("lookup_order")
        if gate is not None:
            await wait_at_gate(gate)
        return f"found {query}"

    @function_tool(effect=ToolEffect.NON_IDEMPOTENT_WRITE)
    def publish_note(message: str) -> dict[str, str]:
        """Publish a note."""
        invocations.record("publish_note")
        return {"published": message}

    @function_tool(effect=ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False)
    async def slow_write(message: str, gate: str) -> str:
        """Write slowly."""
        invocations.record("slow_write")
        await wait_at_gate(gate)
        return f"wrote {message}"

    @function_tool(effect=ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False)
    async def transfer_funds(
        amount: int, context: ToolCallContext, gate: str | None = None
    ) -> str | ToolInputRequested:
        """Move money between accounts."""
        if not context.input_answers:
            invocations.record("transfer_funds:ask")
            return ToolInputRequested(
                question="Which account?",
                choices=["checking", "savings"],
                allows_free_text=False,
                continuation=json.dumps({"amount": amount}),
            )
        invocations.record("transfer_funds:run")
        if gate is not None:
            await wait_at_gate(gate)
        account = context.input_answers[-1].answer
        continued_amount = json.loads(context.input_continuation or "{}")["amount"]
        return f"moved {continued_amount} from {account}"

    async def respond(request: ModelRequest) -> ScriptedModelReply:
        command = ScriptedModelClient.user_texts(request)[-1]
        results = ScriptedModelClient.tool_results(request)
        turn_round = len(results) + 1
        invocations.record(f"model:{request.metadata.turn_id}:{turn_round}")
        verb, _, rest = command.partition(" ")
        if verb == "loop":
            mode, query = rest.split(" ", 1)
            if request.tools or mode == "defy":
                return _call("lookup_order", {"query": f"{query}-{turn_round}"})
            return ScriptedModelReply(text=f"stopped after {len(results)} lookups")
        if verb == "collide" and not results:
            return ScriptedModelReply(
                tool_calls=[
                    ScriptedToolCall(
                        tool_name="transfer_funds", arguments={"amount": 5}, tool_call_id="pay"
                    ),
                    ScriptedToolCall(
                        tool_name="publish_note",
                        arguments={"message": "receipt"},
                        tool_call_id="pay-input-1",
                    ),
                ]
            )
        if results:
            if verb == "lookup-then-stall" and turn_round == 2:
                await wait_at_gate(rest.split(" ", 1)[0])
            if verb == "lookup-then-fail":
                raise RuntimeError("scripted failure after the tool ran")
            return ScriptedModelReply(
                text="; ".join(f"{result.tool_name}: {result.output}" for result in results)
            )
        if verb == "say":
            return ScriptedModelReply(text=f"said: {rest}", reasoning="thinking about it")
        if verb == "recall":
            earlier = ScriptedModelClient.user_texts(request)[:-1]
            output_items = sum(1 for item in request.input_items if hasattr(item, "provider"))
            return ScriptedModelReply(
                text=f"history: {' | '.join(earlier)} ({output_items} model items)"
            )
        if verb in ("lookup", "lookup-then-stall", "lookup-then-fail", "lookup-gated"):
            query = rest
            arguments: dict[str, object] = {"query": query}
            if verb in ("lookup-then-stall", "lookup-gated"):
                gate, query = rest.split(" ", 1)
                arguments = {"query": query}
                if verb == "lookup-gated":
                    arguments["gate"] = gate
            return _call("lookup_order", arguments)
        if verb == "publish":
            return _call("publish_note", {"message": rest})
        if verb == "slow-write":
            gate, message = rest.split(" ", 1)
            return _call("slow_write", {"message": message, "gate": gate})
        if verb in ("ask", "ask-strict"):
            question, *choices = [part.strip() for part in rest.split("|")]
            ask_arguments: dict[str, object] = {"question": question}
            if choices:
                ask_arguments["choices"] = choices
            if verb == "ask-strict":
                ask_arguments["allows_free_text"] = False
            return _call(BuiltInTool.ASK_USER, ask_arguments, text="I need to ask you something.")
        if verb == "ask-raw":
            return ScriptedModelReply(
                tool_calls=[ScriptedToolCall(tool_name=BuiltInTool.ASK_USER, arguments=rest)]
            )
        if verb == "wait":
            seconds, reason = rest.split(" ", 1)
            return _call(
                BuiltInTool.DURABLE_WAIT, {"duration_seconds": int(seconds), "reason": reason}
            )
        if verb == "transfer-pair":
            return ScriptedModelReply(
                tool_calls=[
                    ScriptedToolCall(tool_name="transfer_funds", arguments={"amount": int(amount)})
                    for amount in rest.split(" ")
                ]
            )
        if verb == "transfer":
            amount, _, gate = rest.partition(" ")
            transfer_arguments: dict[str, object] = {"amount": int(amount)}
            if gate:
                transfer_arguments["gate"] = gate
            return _call("transfer_funds", transfer_arguments)
        raise ValueError(f"unknown scripted command {command!r}")

    definition = AgentDefinition(
        name="stepwise-test",
        instructions=INSTRUCTIONS,
        model=ScriptedModelClient(respond),
        tools=[lookup_order, publish_note, slow_write, transfer_funds],
        built_in_tools=frozenset({BuiltInTool.ASK_USER, BuiltInTool.DURABLE_WAIT}),
        interrupt_deadline=interrupt_deadline,
        max_model_calls_per_turn=max_model_calls_per_turn,
    )
    if unfinished_builder_mode is None:
        return stepwise_agent_session_flow(definition, flow_type=STEPWISE_AGENT_FLOW_TYPE)
    return AgentSessionFlow(
        BrokenUnfinishedTurnBuilderRunner(definition, unfinished_builder_mode),
        flow_type=STEPWISE_AGENT_FLOW_TYPE,
        options=stepwise_agent_session_options(definition),
    )


def build_stepwise_agent_flow_from_environment() -> AgentSessionFlow:
    deadline_seconds = os.environ.get(INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE)
    return build_stepwise_agent_flow(
        Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]),
        None if deadline_seconds is None else timedelta(seconds=float(deadline_seconds)),
        int(os.environ.get(MAX_MODEL_CALLS_ENVIRONMENT_VARIABLE, DEFAULT_MAX_MODEL_CALLS_PER_TURN)),
        os.environ.get(UNFINISHED_BUILDER_ENVIRONMENT_VARIABLE),
    )


def _call(tool_name: str, arguments: dict[str, object], text: str = "") -> ScriptedModelReply:
    return ScriptedModelReply(
        text=text, tool_calls=[ScriptedToolCall(tool_name=tool_name, arguments=arguments)]
    )
