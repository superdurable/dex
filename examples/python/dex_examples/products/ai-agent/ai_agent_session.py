# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""The AIAgentSession Flow: a Dex Agents Stepwise agent with the Worker's MCP tools.

The models the Worker allows, the system prompt, the context window, and the
MCP servers are Worker configuration, shared by every session of this Worker.
Each session picks one of the allowed models when it starts: its session
settings carry only the model name, and the Worker maps the name to a model
client. A "mock/..." model, such as "mock/dex", is a local demo model that
needs no provider key. It replies "Local demo response: <text>" and reads
slash commands, so the UI and the tests reach every feature: /tool <name>
<json>, /ask <question>, /ask-many <question>, /choose <question> | <choice> |
<choice>, /wait <seconds> <reason>, /slow <seconds> <text>, /plan-stop
<objective>, /plan-clear, and /model, which names the model.
"""

from __future__ import annotations

import asyncio
import json
import os
from collections.abc import Awaitable, Callable, Mapping, Sequence
from pathlib import Path

import yaml
from dex_agents import AgentSessionFlow, BuiltInTool, CompactionPolicy
from dex_agents.litellm import LiteLLMModelClient
from dex_agents.mcp import McpServerConfig, McpStdioServerConfig, McpStreamableHttpServerConfig
from dex_agents.mcp import McpToolSource
from dex_agents.stepwise import CONTEXT_SUMMARY_INSTRUCTIONS, AgentDefinition, ModelClient
from dex_agents.stepwise import ModelRequest
from dex_agents.stepwise import ScriptedModelClient, ScriptedModelReply, ScriptedToolCall
from dex_agents.stepwise import stepwise_agent_session_flow

AI_AGENT_SESSION_FLOW_TYPE = "AIAgentSession"
DEMO_MODEL = "mock/dex"
DEMO_MODEL_PREFIX = "mock/"
DEFAULT_SYSTEM_PROMPT = (
    "You are a helpful durable AI agent. Use tools when they help, explain "
    "important actions, and never claim a tool succeeded unless its result says so."
)
DEFAULT_CONTEXT_WINDOW_TOKENS = 32_000
MCP_SERVER_KEYS = frozenset(
    {"name", "transport", "command", "args", "cwd", "env", "url", "headers"}
    | {"trust_read_only_annotations"}
)


def build_ai_agent_session_flow(
    models: Sequence[str],
    system_prompt: str,
    context_window_tokens: int,
    mcp_servers: Sequence[McpServerConfig],
) -> AgentSessionFlow:
    """Builds the agent; ``models`` are the allowed model names, and the first is the default."""
    clients = {model: _model_client(model) for model in models}
    definition = AgentDefinition(
        name="ai-agent",
        instructions=system_prompt,
        model=clients[models[0]],
        # Settings hold only a model name; an unknown one gets the default, never a new client.
        resolve_model=lambda settings: clients.get(str(settings.get("model")), clients[models[0]]),
        tool_sources=[McpToolSource(mcp_servers, resource_and_prompt_tools_enabled=True)]
        if mcp_servers
        else [],
        built_in_tools=frozenset(BuiltInTool),
        plan_mode_enabled=True,
        compaction=CompactionPolicy(context_window_tokens=context_window_tokens),
    )
    return stepwise_agent_session_flow(definition, flow_type=AI_AGENT_SESSION_FLOW_TYPE)


def _model_client(model: str) -> ModelClient:
    if model.startswith(DEMO_MODEL_PREFIX):
        return ScriptedModelClient(_demo_model_reply(model))
    # LiteLLM reads the provider key from the Worker's environment, never from Flow state.
    return LiteLLMModelClient(model)


def _demo_model_reply(model: str) -> Callable[[ModelRequest], Awaitable[ScriptedModelReply]]:
    return lambda request: reply_as_demo_model(request, model)


def load_mcp_servers(path: Path | None) -> list[McpServerConfig]:
    """Reads trusted Worker MCP configuration; env and header values name environment variables."""
    servers: list[McpServerConfig] = []
    if path is None:
        return servers
    for entry in (yaml.safe_load(path.read_text()) or {}).get("servers", []):
        name = str(entry["name"])
        unknown_keys = sorted(set(entry) - MCP_SERVER_KEYS)
        if unknown_keys:
            # Per-tool "tools" settings were removed; Dex Agents picks retries and approval by effect.
            raise ValueError(f"MCP server {name!r} has unsupported keys {unknown_keys}")
        trusts = bool(entry.get("trust_read_only_annotations", False))
        if entry.get("transport") == "stdio":
            args = [str(argument) for argument in entry.get("args", [])]
            env = _values_from_environment(entry.get("env"))
            servers.append(
                McpStdioServerConfig(
                    name=name, command=str(entry["command"]), args=args, env=env,
                    cwd=entry.get("cwd"), trusts_tool_annotations=trusts,
                )
            )
        elif entry.get("transport") == "streamable_http":
            headers = _values_from_environment(entry.get("headers"))
            servers.append(
                McpStreamableHttpServerConfig(
                    name=name, url=str(entry["url"]), headers=headers,
                    trusts_tool_annotations=trusts,
                )
            )
        else:
            raise ValueError(f"MCP server {name!r} needs transport stdio or streamable_http")
    return servers


def _values_from_environment(names: Mapping[str, str] | None) -> dict[str, str] | None:
    if not names:
        return None
    missing = sorted(source for source in names.values() if source not in os.environ)
    if missing:
        raise ValueError(f"MCP configuration needs the environment variables {missing}")
    return {target: os.environ[source] for target, source in names.items()}


DEMO_REPLY_PREFIX = "Local demo response: "
APPROVED_PLAN_PREFIX = "[Dex] The user approved plan revision"
DEMO_COMMAND_USAGE = (
    "Usage: /tool <name> <json>, /ask <question>, /ask-many <question>, "
    "/choose <question> | <choice> | <choice>, /wait <seconds> <reason>, "
    "/slow <seconds> <text>, /plan-stop <objective>, /plan-clear, or /model."
)


async def reply_as_demo_model(request: ModelRequest, model: str = DEMO_MODEL) -> ScriptedModelReply:
    if request.instructions == CONTEXT_SUMMARY_INSTRUCTIONS:
        return ScriptedModelReply(text=f"Summary of {len(request.input_items)} earlier items.")
    user_texts = ScriptedModelClient.user_texts(request)
    objective = next((text for text in reversed(user_texts) if not text.startswith("[Dex]")), "")
    try:
        if {tool.name for tool in request.tools} == {"write_todos"}:
            rounds = _planning_rounds(objective)
        elif user_texts and user_texts[-1].startswith(APPROVED_PLAN_PREFIX):
            rounds = _plan_execution_rounds(objective)
        else:
            rounds = await _chat_rounds(objective)
    except ValueError:
        return ScriptedModelReply(text=DEMO_COMMAND_USAGE)
    results = ScriptedModelClient.tool_results(request)
    answered_count = len(results)
    for tool_calls in rounds:
        if answered_count == 0:
            return ScriptedModelReply(tool_calls=tool_calls)
        answered_count -= len(tool_calls)
    if not results:
        return ScriptedModelReply(text=DEMO_REPLY_PREFIX + (model if objective == "/model" else objective))
    outputs = "; ".join(_readable_tool_output(result.output) for result in results)
    return ScriptedModelReply(text=DEMO_REPLY_PREFIX + outputs)


def _planning_rounds(objective: str) -> list[list[ScriptedToolCall]]:
    if objective.startswith("/plan-clear"):
        return [[_call("write_todos", todos=[])]]
    if objective.startswith("/tool "):
        # A planning turn may only call write_todos; the session answers this call itself.
        return [[_tool_call_from_command(objective)]]
    return [[_call("write_todos", todos=_plan_tasks(objective))]]


def _plan_execution_rounds(objective: str) -> list[list[ScriptedToolCall]]:
    if objective.startswith("/plan-stop"):
        return []
    question = objective.removeprefix("/ask ").strip()
    rounds = [[_call("ask_user", question=question)]] if objective.startswith("/ask ") else []
    completed = [{**task, "status": "completed"} for task in _plan_tasks(objective)]
    return [*rounds, [_call("write_todos", todos=completed)]]


async def _chat_rounds(objective: str) -> list[list[ScriptedToolCall]]:
    command, _, argument = objective.partition(" ")
    if command == "/tool":
        return [[_tool_call_from_command(objective)]]
    if command == "/ask":
        return [[_call("ask_user", question=argument)]]
    if command == "/ask-many":
        pause = _call("durable_wait", duration_seconds=1, reason="a pause beside the question")
        return [[_call("ask_user", question=argument), pause]]
    if command == "/choose":
        question, *choices = [part.strip() for part in argument.split("|")]
        return [[_call("ask_user", question=question, choices=choices, allows_free_text=False)]]
    if command == "/wait":
        seconds, _, reason = argument.partition(" ")
        return [[_call("durable_wait", duration_seconds=int(seconds), reason=reason or "a wait")]]
    if command == "/slow":
        seconds, _, _ = argument.partition(" ")
        await asyncio.sleep(float(seconds))
    return []


def _plan_tasks(objective: str) -> list[dict[str, str]]:
    return [
        {"content": f"Work on: {objective}", "status": "pending"},
        {"content": "Report the result", "status": "pending"},
    ]


def _tool_call_from_command(command: str) -> ScriptedToolCall:
    _, tool_name, arguments = (command.split(" ", 2) + ["{}"])[:3]
    return ScriptedToolCall(tool_name=tool_name, arguments=arguments)


def _call(tool_name: str, **arguments: object) -> ScriptedToolCall:
    return ScriptedToolCall(tool_name=tool_name, arguments=arguments)


def _readable_tool_output(output: str) -> str:
    try:
        value = json.loads(output)
    except ValueError:
        return output
    return str(value["answer"]) if isinstance(value, dict) and "answer" in value else output
