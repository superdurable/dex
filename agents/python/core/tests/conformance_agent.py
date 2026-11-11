# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The Stepwise agent that runs the conformance suite's scripted-turn transcripts.

Every core builds the same agent from ``agents/conformance/transcripts``. The
model answers from the scenarios' ``replies``: the key is the request's last
user text, and the reply index advances by each reply's tool call count as
the tool results arrive. ``start_report`` starts a remote task that the first
poll finishes. Every model request is appended to ``model-requests.jsonl``.
"""

from __future__ import annotations

import json
import os
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from dex_agents import (
    AgentSessionFlow,
    BuiltInTool,
    CompactionPolicy,
    DeferredToolExecution,
    DeferredToolOutcome,
    ToolEffect,
    ToolExecutionResult,
    ToolTaskPoll,
    ToolTaskPollOutcome,
    ToolTaskStarted,
)
from dex_agents.builtin_loop import (
    AgentDefinition,
    BuiltinLoopAgentRunner,
    ModelOutputItem,
    ModelRequest,
    ToolResultItem,
    UserTextItem,
    builtin_agent_session_options,
    function_tool,
)
from dex_agents.testing import ScriptedModelClient, ScriptedModelReply, ScriptedToolCall

TRANSCRIPTS_DIR = Path(__file__).resolve().parents[3] / "conformance" / "transcripts"
STATE_DIR_ENVIRONMENT_VARIABLE = "CONFORMANCE_AGENT_STATE_DIR"
MODEL_REQUEST_LOG_NAME = "model-requests.jsonl"
INSTRUCTIONS = "You are the conformance agent."
#: Flow type of each agent profile a scenario names.
FLOW_TYPES = {"chat": "ConformanceAgent", "compacting": "ConformanceCompactingAgent"}
COMPACTION_POLICY = CompactionPolicy(
    context_window_tokens=1000, trigger_ratio=0.3, retained_ratio=0.15
)


def read_scenarios() -> list[dict[str, Any]]:
    return [
        json.loads(path.read_text(encoding="utf-8"))
        for path in sorted(TRANSCRIPTS_DIR.glob("*.json"))
    ]


def read_replies() -> dict[str, list[dict[str, Any]]]:
    replies: dict[str, list[dict[str, Any]]] = {}
    for scenario in read_scenarios():
        for user_text, scripted in scenario["replies"].items():
            if user_text in replies:
                raise ValueError(f"two scenarios script the user text {user_text!r}")
            replies[user_text] = scripted
    return replies


class ConformanceRunner(BuiltinLoopAgentRunner):
    """Stepwise, plus ``start_report``, which starts a task the first poll finishes."""

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        if call.tool_call.tool_name == "start_report":
            return ToolTaskStarted(
                task_id=f"task-{call.tool_call.tool_call_id}", poll_interval_seconds=1
            )
        return await super().execute_deferred_tool_call(call)

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        topic = json.loads(poll.tool_call.arguments)["topic"]
        return ToolExecutionResult(output=f"report on {topic}")


def build_conformance_flows(state_dir: Path) -> list[AgentSessionFlow]:
    replies = read_replies()

    @function_tool(effect=ToolEffect.READ)
    def lookup_order(query: str) -> str:
        """Look up an order."""
        return f"order {query}: shipped"

    @function_tool(effect=ToolEffect.NON_IDEMPOTENT_WRITE)
    def send_email(to: str, body: str) -> dict[str, str]:
        """Send an email."""
        return {"sent_to": to, "body": body}

    @function_tool(effect=ToolEffect.READ)
    def start_report(topic: str) -> str:
        """Start building a report."""
        raise AssertionError("ConformanceRunner starts the report task")

    def respond(request: ModelRequest) -> ScriptedModelReply:
        log_request(state_dir, request)
        user_text = ScriptedModelClient.user_texts(request)[-1]
        scripted = replies[user_text]
        result_count = len(ScriptedModelClient.tool_results(request))
        index = 0
        while result_count > 0:
            result_count -= len(scripted[index].get("tool_calls", []))
            index += 1
        return scripted_reply(scripted[index])

    flows = []
    for profile, flow_type in FLOW_TYPES.items():
        definition = AgentDefinition(
            name=f"conformance-{profile}",
            instructions=INSTRUCTIONS,
            model=ScriptedModelClient(respond),
            tools=[lookup_order, send_email, start_report],
            built_in_tools=frozenset(BuiltInTool),
            plan_mode_enabled=True,
            compaction=COMPACTION_POLICY if profile == "compacting" else None,
        )
        flows.append(
            AgentSessionFlow(
                ConformanceRunner(definition),
                flow_type=flow_type,
                options=builtin_agent_session_options(definition),
            )
        )
    return flows


def build_conformance_flows_from_environment() -> list[AgentSessionFlow]:
    return build_conformance_flows(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))


def scripted_reply(scripted: Mapping[str, Any]) -> ScriptedModelReply:
    return ScriptedModelReply(
        text=scripted.get("text", ""),
        reasoning=scripted.get("reasoning"),
        tool_calls=[
            ScriptedToolCall(tool_name=call["tool_name"], arguments=call["arguments"])
            for call in scripted.get("tool_calls", [])
        ],
    )


def describe_model_request(request: ModelRequest) -> dict[str, Any]:
    """Return the language-neutral form of a model request that transcripts store."""
    items: list[dict[str, Any]] = []
    for item in request.input_items:
        if isinstance(item, UserTextItem):
            items.append({"type": "user_text", "text": item.text})
        elif isinstance(item, ToolResultItem):
            items.append(
                {
                    "type": "tool_result",
                    "tool_call_id": item.tool_call_id,
                    "tool_name": item.tool_name,
                    "output": item.output,
                }
            )
        else:
            assert isinstance(item, ModelOutputItem)
            items.append(
                {
                    "type": "model_output",
                    "provider": item.provider,
                    "api": item.api,
                    "item": item.item,
                    "text": item.text,
                    "tool_call_ids": [call.tool_call_id for call in item.tool_calls],
                }
            )
    tool_choice = request.tool_choice
    return {
        "flow_id": request.metadata.flow_id,
        "step_execution_id": request.metadata.step_execution_id,
        "turn_id": request.metadata.turn_id,
        "attempt": request.metadata.attempt,
        "instructions": request.instructions,
        "tools": [
            {
                "name": tool.name,
                "description": tool.description,
                "parameters_json": json.dumps(
                    tool.parameters, ensure_ascii=False, separators=(",", ":")
                ),
            }
            for tool in request.tools
        ],
        "tool_choice": tool_choice
        if isinstance(tool_choice, str)
        else {"tool_name": tool_choice.tool_name},
        "items": items,
    }


def log_request(state_dir: Path, request: ModelRequest) -> None:
    with (state_dir / MODEL_REQUEST_LOG_NAME).open("a", encoding="utf-8") as log:
        log.write(json.dumps(describe_model_request(request), ensure_ascii=False) + "\n")


def read_model_requests(state_dir: Path, flow_id: str) -> list[dict[str, Any]]:
    path = state_dir / MODEL_REQUEST_LOG_NAME
    if not path.exists():
        return []
    requests = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line]
    return [request for request in requests if request.pop("flow_id") == flow_id]
