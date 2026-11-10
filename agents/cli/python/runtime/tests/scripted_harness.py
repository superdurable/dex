# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Stepwise test harness whose scripted model obeys commands in the user's message.

The last user message picks the reply:

* ``tools``: lists the offered tool names, sorted.
* ``model``: names the turn's model; ``prompt``: shows the turn's instructions.
* ``history``: lists the earlier user messages.
* ``call <tool> <json arguments>``: calls one tool.
* ``calls <json list of [tool, arguments]>``: calls several tools in one response.
* ``ask``: asks the user a question with the ``ask_user`` built-in tool.
* ``slow``: waits five minutes before answering, so the turn can be cancelled.
* anything else: ``echo: <text>``.

After tool results it answers ``done: <tool>=<output> | ...``.
"""

from __future__ import annotations

import asyncio
import json
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from dex_agents import AgentSessionFlow, BuiltInTool
from dex_agents.stepwise import (
    AgentDefinition,
    InstructionContext,
    ModelRequest,
    ScriptedModelClient,
    ScriptedModelReply,
    ScriptedToolCall,
    stepwise_agent_session_flow,
)
from dex_agents_cli import (
    AgentHarness,
    HarnessBuildContext,
    LocalToolGroup,
    TurnConfigResolver,
    resolve_config,
)

SCRIPTED_FLOW_TYPE = "DexAgentsCliScriptedAgent"
DEFAULT_SCRIPTED_MODEL = "scripted-model"
DEFAULT_INSTRUCTIONS = "You are the scripted test agent."
_SLOW_REPLY_SECONDS = 300
_MAX_SHOWN_OUTPUT = 300


class ScriptedHarness:
    """The test harness: a Stepwise agent on a scripted model, with every local tool group."""

    command_name = "dex-agents-scripted"
    config_name = "scripted"
    config_section = "builtin"
    flow_type = SCRIPTED_FLOW_TYPE
    default_model = DEFAULT_SCRIPTED_MODEL
    replaced_local_tool_groups: frozenset[LocalToolGroup] = frozenset()

    def build_session_flow(self, context: HarnessBuildContext) -> AgentSessionFlow:
        models: dict[str, ScriptedModelClient] = {}

        def resolve_model(session_settings: Mapping[str, Any]) -> ScriptedModelClient:
            model_name = context.turn_config(session_settings).model
            if model_name not in models:
                models[model_name] = ScriptedModelClient(_responder(model_name))
            return models[model_name]

        def instructions(instruction_context: InstructionContext) -> str:
            turn_config = context.turn_config(instruction_context.session_settings)
            return context.instructions(turn_config) or DEFAULT_INSTRUCTIONS

        definition = AgentDefinition(
            name="scripted",
            instructions=instructions,
            model=resolve_model({}),
            resolve_model=resolve_model,
            tool_sources=list(context.tool_sources),
            built_in_tools=frozenset({BuiltInTool.ASK_USER}),
        )
        return stepwise_agent_session_flow(definition, flow_type=self.flow_type)


def build_session_flow_without_tools() -> AgentSessionFlow:
    """Build the harness's Flow for a test's Client, which only needs its type and RPCs."""
    config = resolve_config({}, ScriptedHarness.config_section, DEFAULT_SCRIPTED_MODEL)
    context = HarnessBuildContext(
        config=config,
        config_path=None,
        workspace=Path.cwd(),
        tool_sources=(),
        system_prompt_file_text=None,
        resolver=TurnConfigResolver(config),
    )
    return ScriptedHarness().build_session_flow(context)


def _responder(model_name: str) -> Any:
    async def respond(request: ModelRequest) -> ScriptedModelReply:
        results = ScriptedModelClient.tool_results(request)
        if results:
            shown = " | ".join(
                f"{result.tool_name}={result.output[:_MAX_SHOWN_OUTPUT]}" for result in results
            )
            return ScriptedModelReply(text=f"done: {shown}")
        texts = ScriptedModelClient.user_texts(request)
        text = texts[-1].strip() if texts else ""
        command, _, rest = text.partition(" ")
        match command:
            case "tools":
                names = sorted(tool.name for tool in request.tools)
                return ScriptedModelReply(text="tools: " + ",".join(names))
            case "model":
                return ScriptedModelReply(text=f"model: {model_name}")
            case "prompt":
                return ScriptedModelReply(text=f"prompt: {request.instructions}")
            case "history":
                return ScriptedModelReply(text="history: " + " | ".join(texts[:-1]))
            case "call":
                tool_name, _, arguments = rest.partition(" ")
                return ScriptedModelReply(
                    tool_calls=[
                        ScriptedToolCall(
                            tool_name=tool_name, arguments=json.loads(arguments or "{}")
                        )
                    ]
                )
            case "calls":
                return ScriptedModelReply(
                    tool_calls=[
                        ScriptedToolCall(tool_name=tool_name, arguments=arguments)
                        for tool_name, arguments in json.loads(rest)
                    ]
                )
            case "ask":
                question = {"question": "Which color?", "choices": ["red", "blue"]}
                return ScriptedModelReply(
                    tool_calls=[ScriptedToolCall(tool_name="ask_user", arguments=question)]
                )
            case "slow":
                await asyncio.sleep(_SLOW_REPLY_SECONDS)
                return ScriptedModelReply(text="slow reply")
        return ScriptedModelReply(text=f"echo: {text}")

    return respond


def _check_protocol(harness: AgentHarness) -> AgentHarness:
    return harness


_check_protocol(ScriptedHarness())
