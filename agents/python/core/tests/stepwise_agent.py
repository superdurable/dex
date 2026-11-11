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
* ``publish-as <tool_call_id> <message>`` does the same with a fixed tool call ID.
* ``slow-write <gate> <message>`` calls ``slow_write``, which waits at the gate.
* ``ask <question> | <choice> ...`` calls ``ask_user``; ``ask-strict`` disallows
  free text.
* ``ask-raw <json>`` calls ``ask_user`` with raw arguments.
* ``wait <seconds> <reason>`` calls ``durable_wait``.
* ``transfer <amount> [gate]`` calls ``transfer_funds``, which asks which account.
* ``transfer-late-ask <amount> <gate>`` does the same; the tool waits at the
  gate before it asks.
* ``transfer-pair <amount> <amount>`` calls ``transfer_funds`` twice in one response.
* ``loop <obey|defy> <query>`` calls ``lookup_order`` while tools are offered.
  Once none are, ``obey`` answers and ``defy`` asks for a tool anyway.
* ``collide`` calls ``transfer_funds`` as ``pay`` and ``publish_note`` as
  ``pay-input-1``, the ID that ``pay``'s question would take.
* ``stall-then-call <gate> <query> | <message>`` waits at the gate in the first
  call, then calls ``lookup_order`` and ``publish_note`` in one response.
* ``parallel <gate> <query> | <message>`` calls ``lookup_order``, which waits
  at the gate, and ``publish_note`` in one response.
* ``effect`` calls ``update_record``, which declares ``IDEMPOTENT_WRITE`` and
  answers with its ``ToolCallContext.effect``. A tool policy raises it to
  ``NON_IDEMPOTENT_WRITE`` without approval.
* ``send-unclassified`` calls ``send_record``, which declares
  ``IDEMPOTENT_WRITE``; a tool policy makes it ``UNCLASSIFIED`` without
  approval. It fails after sending, and its wrapper lets the error propagate
  unless ``ToolCallContext.effect`` is ``READ`` or ``IDEMPOTENT_WRITE``.
* ``old-shape`` calls ``get_order`` with the old ``{"id": "7"}`` shape, which
  ``prepare_arguments`` converts to ``order_id``; ``bad-shape`` calls it with
  ``{"bad": true}``, which ``prepare_arguments`` rejects.
* ``approve-old-shape`` calls ``cancel_order``, which needs approval, with the
  old ``{"id": "9"}`` shape.

``STEPWISE_AGENT_CANCEL_RPC_GATE`` makes ``cancel_turn`` wait at that gate
after it decided and before it commits, and
``STEPWISE_AGENT_FINISH_CANCELLATION_GATE`` makes ``FinishTurnCancellation``
block the Worker at that gate before it decides.
``STEPWISE_AGENT_MAX_MODEL_CALLS`` sets ``max_model_calls_per_turn``, and
``STEPWISE_AGENT_UNFINISHED_BUILDER`` set to ``raise`` or ``malformed`` breaks
``build_unfinished_turn_messages``; ``gate:<name>`` makes it wait at that gate,
blocking the Worker, before it returns the messages.

After its tool results arrive, the model answers ``<tool_name>: <output>``.

``AgentDefinition.resolve_model`` picks a scripted model named by the session
settings' ``"model"``, ``alpha`` or ``beta``, or ``default`` without one. Every
call records ``model-name:<turn_id>:<round>:<name>``, and ``which-model``
answers ``model <name>: <instructions>``. A ``"persona"`` setting adds ``Persona: <persona>.`` to
the instructions.
"""

from __future__ import annotations

import asyncio
import json
import os
import time
from collections.abc import Awaitable, Callable, Mapping, Sequence
from datetime import timedelta
from pathlib import Path
from typing import Any, cast

from dex import AsyncContext, Context, RPCResult, StepDecision, rpc

from dex_agents import (
    PENDING_INTERRUPTS,
    TURN_CANCELLATION_REQUEST,
    AgentSessionFlow,
    AgentSessionOptions,
    AgentTurnContext,
    BuiltInTool,
    CancelTurnRequest,
    FrameworkMessage,
    ToolEffect,
    ToolExecutionError,
    ToolInputRequested,
    ToolPolicy,
)
from dex_agents.builtin_loop import (
    DEFAULT_MAX_MODEL_CALLS_PER_TURN,
    AgentDefinition,
    BuiltinLoopAgentRunner,
    InstructionContext,
    ModelRequest,
    ToolCallContext,
    builtin_agent_session_flow,
    builtin_agent_session_options,
    function_tool,
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
CANCEL_RPC_GATE_ENVIRONMENT_VARIABLE = "STEPWISE_AGENT_CANCEL_RPC_GATE"
FINISH_CANCELLATION_GATE_ENVIRONMENT_VARIABLE = "STEPWISE_AGENT_FINISH_CANCELLATION_GATE"
INVOCATION_LOG_NAME = "invocations.log"
INSTRUCTIONS = "You are a scripted test agent."
MODEL_NAMES = ("default", "alpha", "beta")


class BrokenUnfinishedTurnBuilderRunner(BuiltinLoopAgentRunner):
    def __init__(self, definition: AgentDefinition, mode: str, state_dir: Path) -> None:
        super().__init__(definition)
        self._mode = mode
        self._state_dir = state_dir

    def build_unfinished_turn_messages(self, turn: AgentTurnContext) -> Sequence[FrameworkMessage]:
        if self._mode.startswith("gate:"):
            gate = FileGate(self._state_dir, self._mode.removeprefix("gate:"))
            gate.mark_reached()
            # The method is synchronous, so the Worker blocks here until the test kills it.
            while not gate.is_released():
                time.sleep(0.05)
            return super().build_unfinished_turn_messages(turn)
        if self._mode == "raise":
            raise RuntimeError("scripted builder defect")
        return cast(Sequence[FrameworkMessage], [{"framework_item": "{}"}])


class GatedCancellationSessionFlow(AgentSessionFlow):
    """Hold ``cancel_turn`` or ``FinishTurnCancellation`` at a gate, to land races exactly."""

    def __init__(
        self,
        runner: BuiltinLoopAgentRunner,
        *,
        options: AgentSessionOptions,
        state_dir: Path,
        cancel_rpc_gate: str | None,
        finish_cancellation_gate: str | None,
    ) -> None:
        super().__init__(runner, flow_type=STEPWISE_AGENT_FLOW_TYPE, options=options)
        self._state_dir = state_dir
        self._cancel_rpc_gate = cancel_rpc_gate
        self._finish_cancellation_gate = finish_cancellation_gate

    @rpc(
        name="cancel_turn",
        timeout=timedelta(minutes=2),
        lock_attributes=(TURN_CANCELLATION_REQUEST.lock(), PENDING_INTERRUPTS.lock()),
    )
    async def cancel_turn(  # type: ignore[override]
        self, context: Context, input: CancelTurnRequest
    ) -> RPCResult[bool]:
        result = super().cancel_turn(context, input)
        if self._cancel_rpc_gate is not None:
            # The RPC read its state; other Steps commit until the gate opens.
            gate = FileGate(self._state_dir, self._cancel_rpc_gate)
            gate.mark_reached()
            await gate.wait_for_release()
        return result

    def _finish_cancelled_turn(self, context: AsyncContext, turn_id: str) -> StepDecision:
        if self._finish_cancellation_gate is not None:
            gate = FileGate(self._state_dir, self._finish_cancellation_gate)
            gate.mark_reached()
            # The method is synchronous, so the Worker blocks here until the test kills it.
            while not gate.is_released():
                time.sleep(0.05)
        return super()._finish_cancelled_turn(context, turn_id)


def build_stepwise_agent_flow(
    state_dir: Path,
    interrupt_deadline: timedelta | None = None,
    max_model_calls_per_turn: int = DEFAULT_MAX_MODEL_CALLS_PER_TURN,
    unfinished_builder_mode: str | None = None,
    cancel_rpc_gate: str | None = None,
    finish_cancellation_gate: str | None = None,
) -> AgentSessionFlow:
    invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)

    async def wait_at_gate(gate_name: str) -> None:
        gate = FileGate(state_dir, gate_name)
        gate.mark_reached()
        try:
            await gate.wait_for_release()
        except asyncio.CancelledError:
            # cancel_turn cancelled this model call or tool Step.
            invocations.record(f"cancelled at {gate_name}")
            raise

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
        amount: int,
        context: ToolCallContext,
        gate: str | None = None,
        ask_gate: str | None = None,
    ) -> str | ToolInputRequested:
        """Move money between accounts."""
        if not context.input_answers:
            if ask_gate is not None:
                await wait_at_gate(ask_gate)
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

    @function_tool(effect=ToolEffect.IDEMPOTENT_WRITE)
    def update_record(context: ToolCallContext) -> str:
        """Update a record."""
        return f"effect {context.effect}"

    @function_tool(effect=ToolEffect.IDEMPOTENT_WRITE)
    def send_record(context: ToolCallContext) -> str:
        """Send a record."""
        try:
            raise ConnectionResetError("connection reset after send")
        except ConnectionResetError as error:
            if context.effect not in (ToolEffect.READ, ToolEffect.IDEMPOTENT_WRITE):
                raise
            raise ToolExecutionError(str(error)) from error

    @function_tool(effect=ToolEffect.READ, prepare_arguments=_prepare_order_arguments)
    def get_order(order_id: str) -> dict[str, str]:
        """Get an order."""
        return {"order_id": order_id}

    @function_tool(
        effect=ToolEffect.NON_IDEMPOTENT_WRITE, prepare_arguments=_prepare_order_arguments
    )
    def cancel_order(order_id: str) -> dict[str, str]:
        """Cancel an order."""
        return {"cancelled": order_id}

    async def respond(model_name: str, request: ModelRequest) -> ScriptedModelReply:
        command = ScriptedModelClient.user_texts(request)[-1]
        results = ScriptedModelClient.tool_results(request)
        turn_round = len(results) + 1
        invocations.record(f"model:{request.metadata.turn_id}:{turn_round}")
        invocations.record(f"model-name:{request.metadata.turn_id}:{turn_round}:{model_name}")
        verb, _, rest = command.partition(" ")
        if verb == "which-model":
            return ScriptedModelReply(text=f"model {model_name}: {request.instructions}")
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
        if verb in ("stall-then-call", "parallel") and not results:
            gate, rest = rest.split(" ", 1)
            query, message = [part.strip() for part in rest.split("|")]
            if verb == "stall-then-call":
                await wait_at_gate(gate)
            lookup_arguments: dict[str, object] = {"query": query}
            if verb == "parallel":
                lookup_arguments["gate"] = gate
            return ScriptedModelReply(
                tool_calls=[
                    ScriptedToolCall(tool_name="lookup_order", arguments=lookup_arguments),
                    ScriptedToolCall(tool_name="publish_note", arguments={"message": message}),
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
        if verb == "effect":
            return _call("update_record", {})
        if verb == "send-unclassified":
            return _call("send_record", {})
        if verb == "old-shape":
            return _call("get_order", {"id": "7"})
        if verb == "bad-shape":
            return _call("get_order", {"bad": True})
        if verb == "approve-old-shape":
            return _call("cancel_order", {"id": "9"})
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
        if verb == "publish-as":
            tool_call_id, message = rest.split(" ", 1)
            return ScriptedModelReply(
                tool_calls=[
                    ScriptedToolCall(
                        tool_name="publish_note",
                        arguments={"message": message},
                        tool_call_id=tool_call_id,
                    )
                ]
            )
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
        if verb == "transfer-late-ask":
            amount, ask_gate = rest.split(" ", 1)
            return _call("transfer_funds", {"amount": int(amount), "ask_gate": ask_gate})
        if verb == "transfer":
            amount, _, gate = rest.partition(" ")
            transfer_arguments: dict[str, object] = {"amount": int(amount)}
            if gate:
                transfer_arguments["gate"] = gate
            return _call("transfer_funds", transfer_arguments)
        raise ValueError(f"unknown scripted command {command!r}")

    models = {name: ScriptedModelClient(_named_respond(respond, name)) for name in MODEL_NAMES}

    def resolve_model(session_settings: Mapping[str, Any]) -> ScriptedModelClient:
        return models[str(session_settings.get("model", "default"))]

    def instructions(context: InstructionContext) -> str:
        persona = context.session_settings.get("persona")
        return INSTRUCTIONS if persona is None else f"{INSTRUCTIONS} Persona: {persona}."

    definition = AgentDefinition(
        name="stepwise-test",
        instructions=instructions,
        model=models["default"],
        resolve_model=resolve_model,
        tools=[
            lookup_order,
            publish_note,
            slow_write,
            transfer_funds,
            update_record,
            send_record,
            get_order,
            cancel_order,
        ],
        tool_policies={
            "update_record": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
            "send_record": ToolPolicy(ToolEffect.UNCLASSIFIED, requires_approval=False),
        },
        built_in_tools=frozenset({BuiltInTool.ASK_USER, BuiltInTool.DURABLE_WAIT}),
        interrupt_deadline=interrupt_deadline,
        max_model_calls_per_turn=max_model_calls_per_turn,
    )
    if cancel_rpc_gate is not None or finish_cancellation_gate is not None:
        return GatedCancellationSessionFlow(
            BuiltinLoopAgentRunner(definition),
            options=builtin_agent_session_options(definition),
            state_dir=state_dir,
            cancel_rpc_gate=cancel_rpc_gate,
            finish_cancellation_gate=finish_cancellation_gate,
        )
    if unfinished_builder_mode is None:
        return builtin_agent_session_flow(definition, flow_type=STEPWISE_AGENT_FLOW_TYPE)
    return AgentSessionFlow(
        BrokenUnfinishedTurnBuilderRunner(definition, unfinished_builder_mode, state_dir),
        flow_type=STEPWISE_AGENT_FLOW_TYPE,
        options=builtin_agent_session_options(definition),
    )


def build_stepwise_agent_flow_from_environment() -> AgentSessionFlow:
    deadline_seconds = os.environ.get(INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE)
    return build_stepwise_agent_flow(
        Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]),
        None if deadline_seconds is None else timedelta(seconds=float(deadline_seconds)),
        int(os.environ.get(MAX_MODEL_CALLS_ENVIRONMENT_VARIABLE, DEFAULT_MAX_MODEL_CALLS_PER_TURN)),
        os.environ.get(UNFINISHED_BUILDER_ENVIRONMENT_VARIABLE),
        os.environ.get(CANCEL_RPC_GATE_ENVIRONMENT_VARIABLE),
        os.environ.get(FINISH_CANCELLATION_GATE_ENVIRONMENT_VARIABLE),
    )


def _prepare_order_arguments(arguments: Any) -> Any:
    if "bad" in arguments:
        raise ValueError("unsupported argument shape")
    if "id" in arguments:
        return {"order_id": arguments["id"]}
    return arguments


def _named_respond(
    respond: Callable[[str, ModelRequest], Awaitable[ScriptedModelReply]], model_name: str
) -> Callable[[ModelRequest], Awaitable[ScriptedModelReply]]:
    return lambda request: respond(model_name, request)


def _call(tool_name: str, arguments: dict[str, object], text: str = "") -> ScriptedModelReply:
    return ScriptedModelReply(
        text=text, tool_calls=[ScriptedToolCall(tool_name=tool_name, arguments=arguments)]
    )
