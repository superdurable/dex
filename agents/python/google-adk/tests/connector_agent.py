# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""An ADK agent whose tool source offers the fake ledger Connector's operations.

The model reads ``calls <json>`` from the latest user message, a JSON list of
``[tool_name, arguments]`` pairs, and calls them in one response. It calls
only tools declared in its request; any other name makes it answer
``unbound <name>``. Once results arrive it answers one ``<tool_name>:
<result>`` line per result. A ``hold:<gate>`` word before ``calls`` makes that
answer wait at a ``FileGate``, so a test can read the turn's records before the
turn ends.

The agent also has its own tool, ``lookup``. The session policy for
``policy_note`` claims ``READ`` and asks for approval; only the approval
applies, because the effect class comes from the manifest.
"""

from __future__ import annotations

import json
import os
from collections.abc import AsyncGenerator, Sequence
from pathlib import Path
from typing import Any

from google.adk.agents import LlmAgent
from google.adk.models.base_llm import BaseLlm
from google.adk.models.llm_request import LlmRequest
from google.adk.models.llm_response import LlmResponse
from google.genai import types

from dex_agents import AgentSessionFlow, ToolEffect, ToolPolicy
from dex_agents.connectors import ConnectorToolSource, connector_tool
from dex_agents.testing import FileGate
from dex_agents_google_adk import adk_agent_session_flow
from tests.fake_ledger import (
    LEDGER_CONNECTION,
    STATE_DIR_ENVIRONMENT_VARIABLE,
    CreatePayoutOperation,
    FakeLedgerProvider,
    GetBalanceOperation,
    PostNoteOperation,
)

CONNECTOR_AGENT_FLOW_TYPE = "AdkConnectorAgent"
CONNECTOR_TOOL_POLICIES = {
    "lookup": ToolPolicy(ToolEffect.READ),
    "policy_note": ToolPolicy(ToolEffect.READ, requires_approval=True),
}


class ConnectorScriptedModel(BaseLlm):
    """Call the tools the user message names, if they are declared, then report their results."""

    model: str = "scripted-connector-model"
    state_dir: str

    async def generate_content_async(
        self, llm_request: LlmRequest, stream: bool = False
    ) -> AsyncGenerator[LlmResponse, None]:
        contents = llm_request.contents
        prompt_index = max(
            index
            for index, content in enumerate(contents)
            if content.role == "user" and any(part.text for part in content.parts or [])
        )
        prompt = "".join(part.text or "" for part in contents[prompt_index].parts or [])
        hold = None
        if prompt.startswith("hold:"):
            hold_word, prompt = prompt.split(" ", 1)
            hold = hold_word.removeprefix("hold:")
        results = [
            f"{response.name}: {_describe_response(response.response or {})}"
            for content in contents[prompt_index + 1 :]
            for part in content.parts or []
            if (response := part.function_response) is not None
        ]
        if results:
            if hold is not None:
                gate = FileGate(Path(self.state_dir), hold)
                gate.mark_reached()
                await gate.wait_for_release()
            yield _text_response("\n".join(results))
            return
        if not prompt.startswith("calls "):
            yield _text_response(f"said: {prompt}")
            return
        requested: list[Any] = json.loads(prompt.removeprefix("calls "))
        unbound = [name for name, _ in requested if name not in llm_request.tools_dict]
        if unbound:
            yield _text_response(f"unbound {', '.join(unbound)}")
            return
        yield _function_call_response(requested)


def lookup(query: str) -> str:
    """Look up documents that match a query."""
    return f"found {query}"


def build_connector_source(state_dir: Path) -> ConnectorToolSource:
    provider = FakeLedgerProvider(state_dir)
    payouts = CreatePayoutOperation(provider)
    notes = PostNoteOperation(provider)
    return ConnectorToolSource(
        [
            connector_tool(GetBalanceOperation(provider), LEDGER_CONNECTION, name="get_balance"),
            connector_tool(
                payouts, LEDGER_CONNECTION, name="create_payout", requires_approval=False
            ),
            connector_tool(payouts, LEDGER_CONNECTION, name="approved_payout"),
            connector_tool(notes, LEDGER_CONNECTION, name="post_note"),
            connector_tool(notes, LEDGER_CONNECTION, name="policy_note"),
        ]
    )


def build_connector_agent(state_dir: Path) -> LlmAgent:
    return LlmAgent(
        name="connector",
        model=ConnectorScriptedModel(state_dir=str(state_dir)),
        instruction="You are a scripted test agent.",
        tools=[lookup],
    )


def build_connector_agent_flow(state_dir: Path) -> AgentSessionFlow:
    return adk_agent_session_flow(
        build_connector_agent(state_dir),
        flow_type=CONNECTOR_AGENT_FLOW_TYPE,
        tool_policies=CONNECTOR_TOOL_POLICIES,
        tool_sources=[build_connector_source(state_dir)],
    )


def build_connector_agent_flow_from_environment() -> AgentSessionFlow:
    return build_connector_agent_flow(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))


def _function_call_response(requests: Sequence[tuple[str, dict[str, Any]]]) -> LlmResponse:
    return LlmResponse(
        content=types.Content(
            role="model",
            parts=[
                types.Part(function_call=types.FunctionCall(name=name, args=arguments))
                for name, arguments in requests
            ],
        ),
        finish_reason=types.FinishReason.STOP,
    )


def _text_response(text: str) -> LlmResponse:
    return LlmResponse(
        content=types.Content(role="model", parts=[types.Part(text=text)]),
        finish_reason=types.FinishReason.STOP,
    )


def _describe_response(response: dict[str, Any]) -> str:
    for key in ("result", "error"):
        if isinstance(response.get(key), str):
            return str(response[key])
    return json.dumps(response, separators=(",", ":"))
