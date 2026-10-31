# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""An agent tool whose nested agent stops for approval only at run time records ``failed``."""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from agents import Agent, RunConfig, function_tool

from dex_agents import (
    AgentEventWriter,
    AgentToolCall,
    DeferredToolExecution,
    ToolCallStatus,
    ToolEffect,
    ToolExecutionError,
)
from dex_agents.testing import InvocationLog
from dex_agents_openai_agents import OpenAIAgentsRunner
from tests.scripted_model import CommandScriptedModel


async def _record_heartbeat() -> None:
    return None


async def test_nested_approval_added_after_construction_records_failed(tmp_path: Path) -> None:
    model = CommandScriptedModel(tmp_path, InvocationLog(tmp_path / "invocations.log"))
    refunds: list[str] = []

    @function_tool
    def issue_refund(order: str) -> str:
        """Refund an order."""
        refunds.append(order)
        return f"refunded {order}"

    billing = Agent(name="Billing", model=model, tools=[issue_refund])
    outer = Agent(
        name="Outer",
        model=model,
        tools=[billing.as_tool(tool_name="billing", tool_description="Billing agent.")],
    )
    runner = OpenAIAgentsRunner(outer, run_config=RunConfig(tracing_disabled=True))
    # Agents used as tools are not copied, so this change reaches the runner.
    issue_refund.needs_approval = True
    arguments = json.dumps({"input": 'call issue_refund {"order": "42"}'})
    execution = DeferredToolExecution(
        flow_id="session-1",
        tool_call=AgentToolCall(
            tool_call_id="call-1",
            turn_id="turn-1",
            tool_name="billing",
            arguments=arguments,
            effect=ToolEffect.NON_IDEMPOTENT_WRITE,
            requires_approval=False,
            status=ToolCallStatus.APPROVED,
        ),
        idempotency_key="session-1/turn-1/call-1",
        attempt=1,
        events=AgentEventWriter(lambda event: None, "turn-1", 1),
        heartbeat=_record_heartbeat,
    )

    with pytest.raises(ToolExecutionError, match="stopped for a nested approval of issue_refund"):
        await runner.execute_deferred_tool_call(execution)
    assert refunds == []
