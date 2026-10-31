# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Agent graphs that the adapter refuses because a tool call would escape its Step."""

from __future__ import annotations

from pathlib import Path

import pytest
from agents import Agent, ShellCommandRequest, ShellTool, function_tool, handoff
from agents.mcp import MCPServerStdio
from agents.sandbox import SandboxAgent

from dex_agents import ToolEffect, ToolPolicy
from dex_agents.testing import InvocationLog
from dex_agents_openai_agents import openai_agents_session_flow
from tests.scripted_model import CommandScriptedModel


@pytest.fixture
def model(tmp_path: Path) -> CommandScriptedModel:
    return CommandScriptedModel(tmp_path, InvocationLog(tmp_path / "invocations.log"))


def _run_shell(request: ShellCommandRequest) -> str:
    return "ran"


def test_local_shell_tool_is_refused_unless_at_least_once_tools_are_allowed(
    model: CommandScriptedModel,
) -> None:
    agent = Agent(name="Operator", model=model, tools=[ShellTool(executor=_run_shell)])

    with pytest.raises(ValueError, match="ShellTool 'shell' runs inside the framework loop"):
        openai_agents_session_flow(agent, flow_type="OperatorAgent")

    flow = openai_agents_session_flow(
        agent,
        flow_type="OperatorAgent",
        tool_policies={"shell": ToolPolicy(ToolEffect.READ)},
        allow_at_least_once_tools=True,
    )
    assert flow.get_flow_type() == "OperatorAgent"


def test_mcp_servers_are_refused_and_take_no_policy(model: CommandScriptedModel) -> None:
    files = MCPServerStdio(params={"command": "files-server"}, name="files")
    agent = Agent(name="Librarian", model=model, mcp_servers=[files])

    with pytest.raises(ValueError, match="agent 'Librarian' lists tools at run time"):
        openai_agents_session_flow(agent, flow_type="LibrarianAgent")
    with pytest.raises(ValueError, match="tool_policies names 'read_file'"):
        openai_agents_session_flow(
            agent,
            flow_type="LibrarianAgent",
            tool_policies={"read_file": ToolPolicy(ToolEffect.READ)},
            allow_at_least_once_tools=True,
        )
    flow = openai_agents_session_flow(
        agent, flow_type="LibrarianAgent", allow_at_least_once_tools=True
    )
    assert flow.get_flow_type() == "LibrarianAgent"


def test_sandbox_agent_capabilities_are_refused(model: CommandScriptedModel) -> None:
    triage = Agent(name="Triage", model=model, handoffs=[SandboxAgent(name="Coder", model=model)])

    with pytest.raises(ValueError, match="agent 'Coder' lists tools at run time"):
        openai_agents_session_flow(triage, flow_type="TriageAgent")


def test_agent_tool_whose_nested_tool_needs_approval_is_refused(
    model: CommandScriptedModel,
) -> None:
    @function_tool(needs_approval=True)
    def issue_refund(order: str) -> str:
        """Refund an order."""
        return order

    billing = Agent(name="Billing", model=model, tools=[issue_refund])
    outer = Agent(
        name="Outer",
        model=model,
        tools=[billing.as_tool(tool_name="billing", tool_description="Billing agent.")],
    )

    with pytest.raises(ValueError, match="whose tool 'issue_refund' needs approval"):
        openai_agents_session_flow(outer, flow_type="OuterAgent")


def test_policy_for_an_unknown_tool_is_refused(model: CommandScriptedModel) -> None:
    @function_tool
    def lookup(query: str) -> str:
        """Search."""
        return query

    agent = Agent(name="Search", model=model, tools=[lookup])

    with pytest.raises(ValueError, match="tool_policies names 'lookpu'"):
        openai_agents_session_flow(
            agent, flow_type="SearchAgent", tool_policies={"lookpu": ToolPolicy(ToolEffect.READ)}
        )


def test_function_tool_names_must_be_unique_across_handoffs(model: CommandScriptedModel) -> None:
    @function_tool(name_override="lookup")
    def lookup_orders(query: str) -> str:
        """Search orders."""
        return query

    @function_tool(name_override="lookup")
    def lookup_invoices(query: str) -> str:
        """Search invoices."""
        return query

    billing = Agent(name="Billing", model=model, tools=[lookup_invoices])
    triage = Agent(name="Triage", model=model, tools=[lookup_orders], handoffs=[handoff(billing)])

    with pytest.raises(ValueError, match="two different function tools are named 'lookup'"):
        openai_agents_session_flow(triage, flow_type="TriageAgent")
