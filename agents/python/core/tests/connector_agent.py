# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Stepwise agent whose tools are the fake ledger Connector's operations.

The scripted model reads ``calls <json>`` from the user text, a JSON list of
``[tool_name, arguments]`` pairs, and calls them in one response with IDs
``call-0``, ``call-1``, and so on. Once results arrive it answers one
``<tool_name>: <output>`` line per result. A ``hold:<gate>`` word before
``calls`` makes that answer wait at a ``FileGate``, so a test can read the
turn's records before the turn ends.

``post_note_retried`` is ``postNote`` under a manifest that claims
``retry: safe``, so the fenced write runs in ``CallTool`` with retries.
``payout_checked`` is ``createPayout`` with an approval predicate: amounts
above 50 wait for approval. ``CONNECTOR_AGENT_MAX_RESULT_BYTES`` bounds results,
and ``CONNECTOR_AGENT_DROPPED_TOOLS``, a comma-separated list, leaves tools out,
as a later deploy that removed them would.
"""

from __future__ import annotations

import copy
import json
import os
from pathlib import Path
from typing import Any

from dex_connectors.sdk import ConnectionRef

from dex_agents import AgentSessionFlow
from dex_agents.connectors import ConnectorToolSource, connector_tool
from dex_agents.stepwise import (
    AgentDefinition,
    ModelRequest,
    ScriptedModelClient,
    ScriptedModelReply,
    ScriptedToolCall,
    stepwise_agent_session_flow,
)
from dex_agents.testing import FileGate
from tests.fake_ledger import (
    STATE_DIR_ENVIRONMENT_VARIABLE,
    CreatePayoutOperation,
    FakeLedgerProvider,
    GetBalanceOperation,
    PostNoteOperation,
)

CONNECTOR_AGENT_FLOW_TYPE = "ConnectorAgent"
MAX_RESULT_BYTES_ENVIRONMENT_VARIABLE = "CONNECTOR_AGENT_MAX_RESULT_BYTES"
DROPPED_TOOLS_ENVIRONMENT_VARIABLE = "CONNECTOR_AGENT_DROPPED_TOOLS"
CONNECTION = ConnectionRef("fake-ledger", "test-ledger")
_MANIFEST_PATH = Path(__file__).with_name("fake_ledger") / "dex-connector.json"


def read_fake_ledger_manifest() -> dict[str, Any]:
    manifest: dict[str, Any] = json.loads(_MANIFEST_PATH.read_text())
    return manifest


def retried_note_manifest() -> dict[str, Any]:
    manifest = copy.deepcopy(read_fake_ledger_manifest())
    for factory in manifest["factories"]:
        if factory["operationId"] == "postNote":
            factory["agent"]["retry"] = "safe"
    return manifest


def build_connector_source(
    state_dir: Path,
    max_result_bytes: int | None = None,
    dropped_tools: frozenset[str] = frozenset(),
) -> ConnectorToolSource:
    provider = FakeLedgerProvider(state_dir)
    payouts = CreatePayoutOperation(provider)
    notes = PostNoteOperation(provider)
    tools = [
        connector_tool(GetBalanceOperation(provider), CONNECTION, name="get_balance"),
        connector_tool(payouts, CONNECTION, name="create_payout", requires_approval=False),
        connector_tool(payouts, CONNECTION, name="approved_payout"),
        connector_tool(
            payouts,
            CONNECTION,
            name="payout_checked",
            requires_approval=lambda arguments: arguments["amount"] > 50,
        ),
        connector_tool(notes, CONNECTION, name="post_note"),
        connector_tool(
            notes, CONNECTION, name="post_note_retried", manifest=retried_note_manifest()
        ),
    ]
    tools = [tool for tool in tools if tool.name not in dropped_tools]
    if max_result_bytes is None:
        return ConnectorToolSource(tools)
    return ConnectorToolSource(tools, max_result_bytes=max_result_bytes)


def build_connector_agent_flow(
    state_dir: Path,
    max_result_bytes: int | None = None,
    dropped_tools: frozenset[str] = frozenset(),
) -> AgentSessionFlow:
    async def reply(request: ModelRequest) -> ScriptedModelReply:
        command = ScriptedModelClient.user_texts(request)[-1]
        words = command.split(" ", 2)
        hold = words[0].removeprefix("hold:") if words[0].startswith("hold:") else None
        if hold is not None:
            command = command.split(" ", 1)[1]
        results = ScriptedModelClient.tool_results(request)
        if results:
            if hold is not None:
                gate = FileGate(state_dir, hold)
                gate.mark_reached()
                await gate.wait_for_release()
            return ScriptedModelReply(
                text="\n".join(f"{result.tool_name}: {result.output}" for result in results)
            )
        if not command.startswith("calls "):
            return ScriptedModelReply(text=f"said: {command}")
        return ScriptedModelReply(
            tool_calls=[
                ScriptedToolCall(
                    tool_name=name, arguments=json.dumps(arguments), tool_call_id=f"call-{index}"
                )
                for index, (name, arguments) in enumerate(
                    json.loads(command.removeprefix("calls "))
                )
            ]
        )

    definition = AgentDefinition(
        name="connector-agent",
        instructions="You are a scripted test agent.",
        model=ScriptedModelClient(reply),
        tool_sources=[build_connector_source(state_dir, max_result_bytes, dropped_tools)],
    )
    return stepwise_agent_session_flow(definition, flow_type=CONNECTOR_AGENT_FLOW_TYPE)


def build_connector_agent_flow_from_environment() -> AgentSessionFlow:
    limit = os.environ.get(MAX_RESULT_BYTES_ENVIRONMENT_VARIABLE)
    dropped = os.environ.get(DROPPED_TOOLS_ENVIRONMENT_VARIABLE, "")
    return build_connector_agent_flow(
        Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]),
        int(limit) if limit else None,
        frozenset(name for name in dropped.split(",") if name),
    )
