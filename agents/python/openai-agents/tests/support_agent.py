# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A real OpenAI Agents SDK agent graph with a scripted model and logged tools.

The ``Assistant`` agent owns most tools, hands off to ``Billing``, which owns
``refund``, and asks ``Researcher`` through an agent tool. Every tool records
each real run in an ``InvocationLog``, so tests can prove a write ran exactly
once across Worker processes.
"""

from __future__ import annotations

import os
from datetime import timedelta
from pathlib import Path
from typing import Any

from agents import (
    Agent,
    RunConfig,
    RunContextWrapper,
    ToolGuardrailFunctionOutput,
    ToolInputGuardrailData,
    function_tool,
    handoff,
    tool_input_guardrail,
)
from pydantic import BaseModel

from dex_agents import AgentSessionFlow, ToolEffect, ToolExecutionError, ToolPolicy
from dex_agents.testing import FileGate, InvocationLog
from dex_agents_openai_agents import (
    get_current_deferred_tool_execution,
    openai_agents_session_flow,
)
from tests.scripted_model import CommandScriptedModel

SUPPORT_AGENT_FLOW_TYPE = "SupportAgent"
STATE_DIR_ENVIRONMENT_VARIABLE = "OPENAI_AGENTS_TEST_STATE_DIR"
INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE = "OPENAI_AGENTS_TEST_INTERRUPT_DEADLINE_SECONDS"
INVOCATION_LOG_NAME = "invocations.log"
IDEMPOTENCY_KEY_LOG_NAME = "idempotency-keys.log"
TOOL_POLICIES = {
    "lookup": ToolPolicy(ToolEffect.READ),
    "read_secret": ToolPolicy(ToolEffect.READ),
    "ask_researcher": ToolPolicy(ToolEffect.READ),
    "publish": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
    "charge": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE),
    "transfer": ToolPolicy(ToolEffect.IDEMPOTENT_WRITE),
    "slow_write": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
    "pay": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
    "refund": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
    "run_sql": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
}


class Receipt(BaseModel):
    receipt_id: str
    amount: int


def build_support_agent_flow(
    state_dir: Path, interrupt_deadline: timedelta | None = None
) -> AgentSessionFlow:
    invocations = InvocationLog(state_dir / INVOCATION_LOG_NAME)
    idempotency_keys = InvocationLog(state_dir / IDEMPOTENCY_KEY_LOG_NAME)
    model = CommandScriptedModel(state_dir, invocations)

    async def pass_gate(gate_name: str) -> None:
        if gate_name:
            gate = FileGate(state_dir, gate_name)
            gate.mark_reached()
            await gate.wait_for_release()

    @function_tool
    async def lookup(query: str, gate: str = "") -> str:
        """Search the knowledge base."""
        invocations.record("lookup")
        await pass_gate(gate)
        return f"found {query}"

    @function_tool
    async def publish(message: str) -> str:
        """Publish a message to every subscriber."""
        invocations.record("publish")
        return f"published {message}"

    @function_tool
    async def charge(amount: int, failed_attempts: int = 0, gate: str = "") -> str:
        """Charge the customer's card."""
        invocations.record("charge")
        execution = get_current_deferred_tool_execution()
        if execution is None:
            raise RuntimeError("charge must run in a tool call Step")
        idempotency_keys.record(f"charge {execution.idempotency_key} attempt {execution.attempt}")
        await pass_gate(gate)
        if execution.attempt <= failed_attempts:
            raise ConnectionError(f"charge attempt {execution.attempt} lost its connection")
        if amount <= 0:
            raise ToolExecutionError("amount must be positive")
        return f"charged {amount} with key {execution.idempotency_key}"

    @function_tool
    async def slow_write(message: str, gate: str) -> str:
        """Write a record slowly."""
        invocations.record("slow_write")
        await pass_gate(gate)
        return f"wrote {message}"

    @function_tool(needs_approval=True)
    def read_secret(name: str) -> str:
        """Read a secret value."""
        invocations.record("read_secret")
        return f"secret {name} is 42"

    async def is_large_transfer(
        context: RunContextWrapper[Any], arguments: dict[str, Any], tool_call_id: str
    ) -> bool:
        return int(arguments.get("amount", 0)) > 1000

    @function_tool(needs_approval=is_large_transfer)
    def transfer(to: str, amount: int = 5000) -> str:
        """Transfer money; the amount defaults to 5000."""
        invocations.record("transfer")
        return f"sent {amount} to {to}"

    @function_tool(output_type=Receipt)
    def pay(order: str, amount: int) -> Receipt:
        """Pay for an order and return the receipt."""
        invocations.record("pay")
        if amount <= 0:
            raise ToolExecutionError("card declined")
        return Receipt(receipt_id=f"receipt-{order}", amount=amount)

    @tool_input_guardrail
    def block_destructive_sql(data: ToolInputGuardrailData) -> ToolGuardrailFunctionOutput:
        arguments = data.context.tool_arguments or ""
        invocations.record("guardrail:run_sql")
        if "drop" in arguments:
            return ToolGuardrailFunctionOutput.reject_content("blocked: destructive SQL")
        if "truncate" in arguments:
            return ToolGuardrailFunctionOutput.raise_exception(output_info="destructive")
        return ToolGuardrailFunctionOutput.allow()

    @function_tool(tool_input_guardrails=[block_destructive_sql])
    def run_sql(sql: str) -> str:
        """Run one SQL statement."""
        invocations.record("run_sql")
        return f"ran {sql}"

    @function_tool
    def cite(source: str) -> str:
        """Cite a source."""
        invocations.record("cite")
        execution = get_current_deferred_tool_execution()
        key = execution.idempotency_key if execution is not None else "none"
        return f"cited {source} with key {key}"

    @function_tool
    def refund(order: str) -> str:
        """Refund an order."""
        invocations.record("refund")
        return f"refunded {order}"

    researcher = Agent(
        name="Researcher", instructions="Research the question.", model=model, tools=[cite]
    )
    billing = Agent(name="Billing", instructions="Handle billing.", model=model, tools=[refund])
    assistant = Agent(
        name="Assistant",
        instructions="Help the user.",
        model=model,
        tools=[
            lookup,
            publish,
            charge,
            slow_write,
            read_secret,
            transfer,
            pay,
            run_sql,
            researcher.as_tool(tool_name="ask_researcher", tool_description="Ask a researcher."),
        ],
        handoffs=[handoff(billing)],
    )
    return openai_agents_session_flow(
        assistant,
        flow_type=SUPPORT_AGENT_FLOW_TYPE,
        tool_policies=TOOL_POLICIES,
        interrupt_deadline=interrupt_deadline,
        run_config=RunConfig(tracing_disabled=True),
    )


def build_support_agent_flow_from_environment() -> AgentSessionFlow:
    deadline_seconds = os.environ.get(INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE)
    return build_support_agent_flow(
        Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]),
        None if deadline_seconds is None else timedelta(seconds=float(deadline_seconds)),
    )
