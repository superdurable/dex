# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A built-in loop test harness whose scripted model obeys commands in the user's message.

The last user message picks the reply:

* ``tools``: lists the offered tool names, sorted.
* ``cached``: lists the sources' cached tool names when the Flow was built, sorted.
* ``model``: names the turn's model; ``prompt``: shows the turn's instructions.
* ``history``: lists the earlier user messages.
* ``call <tool> <json arguments>``: calls one tool.
* ``calls <json list of [tool, arguments]>``: calls several tools in one response.
* ``ask``: asks the user a question with the ``ask_user`` built-in tool.
* ``slow``: waits five minutes before answering, so the turn can be cancelled.
* ``fail``: raises, so the model call fails and, after its retries, the turn.
* anything else: ``echo: <text>``.

After tool results it answers ``done: <tool>=<output> | ...``.
"""

from __future__ import annotations

import asyncio
import json
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from dex_agents import AgentSessionFlow, BuiltInTool, ToolEffect, ToolPolicy
from dex_agents.builtin_loop import (
    AgentDefinition,
    InstructionContext,
    ModelRequest,
    ScriptedModelClient,
    ScriptedModelReply,
    ScriptedToolCall,
    builtin_agent_session_flow,
    function_tool,
)
from dex_agents_cli import (
    AgentHarness,
    HarnessContext,
    LocalToolGroup,
    TurnConfigResolver,
    is_local_tool_group_enabled,
    resolve_config,
)

SCRIPTED_FLOW_TYPE = "DexAgentsCliScriptedAgent"
DEFAULT_SCRIPTED_MODEL = "scripted-model"
DEFAULT_INSTRUCTIONS = "You are the scripted test agent."
_SLOW_REPLY_SECONDS = 300
_MAX_SHOWN_OUTPUT = 300


class ScriptedHarness:
    """The test harness: a built-in loop agent on a scripted model, with every local tool group."""

    name = "scripted"
    config_section = "builtin"
    flow_type = SCRIPTED_FLOW_TYPE
    default_model = DEFAULT_SCRIPTED_MODEL
    replaced_local_tools: frozenset[LocalToolGroup] = frozenset()

    def __init__(
        self,
        tool_policies: Mapping[str, ToolPolicy] | None = None,
        *,
        has_replacement_shell_tool: bool = False,
        is_tool_set_fixed_at_build: bool = False,
    ) -> None:
        self._tool_policies = dict(tool_policies or {})
        self._has_replacement_shell_tool = has_replacement_shell_tool
        self.is_tool_set_fixed_at_build = is_tool_set_fixed_at_build

    def create_session_flow(self, context: HarnessContext) -> AgentSessionFlow:
        cached_at_build = sorted(
            tool.name
            for source in context.tool_sources
            for tool in getattr(source, "cached_tools", tuple)()
        )
        models: dict[str, ScriptedModelClient] = {}

        def resolve_model(session_settings: Mapping[str, Any]) -> ScriptedModelClient:
            model_name = context.resolve_turn(session_settings).model
            if model_name not in models:
                models[model_name] = ScriptedModelClient(_responder(model_name, cached_at_build))
            return models[model_name]

        def instructions(instruction_context: InstructionContext) -> str:
            turn = context.resolve_turn(instruction_context.session_settings)
            return turn.instructions or DEFAULT_INSTRUCTIONS

        @function_tool(effect=ToolEffect.READ)
        async def report_shell_allow() -> str:
            """Report the shell settings this tool's own Step sees, as a replacement would."""
            turn = context.resolve_tool_step_permissions()
            return json.dumps(
                {
                    "shell": is_local_tool_group_enabled(turn, LocalToolGroup.SHELL),
                    "allow": list(turn.local_tools.shell.allow),
                }
            )

        definition = AgentDefinition(
            name="scripted",
            tools=[report_shell_allow] if self._has_replacement_shell_tool else [],
            instructions=instructions,
            model=resolve_model({}),
            resolve_model=resolve_model,
            tool_sources=list(context.tool_sources),
            built_in_tools=frozenset({BuiltInTool.ASK_USER}),
            tool_policies=self._tool_policies,
        )
        return builtin_agent_session_flow(definition, flow_type=context.flow_type)


def build_session_flow_without_tools(flow_type: str = SCRIPTED_FLOW_TYPE) -> AgentSessionFlow:
    """Build the harness's Flow for a test's Client, which only needs its type and RPCs."""
    config = resolve_config({}, ScriptedHarness.config_section, DEFAULT_SCRIPTED_MODEL)
    context = HarnessContext(
        flow_type=flow_type,
        config=config,
        workspace=Path.cwd(),
        instructions=None,
        harness_options={},
        tool_sources=(),
        resolver=TurnConfigResolver(config, None, lambda warnings: None),
    )
    return ScriptedHarness().create_session_flow(context)


def _responder(model_name: str, cached_at_build: list[str]) -> Any:
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
            case "cached":
                return ScriptedModelReply(text="cached: " + ",".join(cached_at_build))
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
            case "fail":
                raise RuntimeError("the scripted model was told to fail")
            case "slow":
                await asyncio.sleep(_SLOW_REPLY_SECONDS)
                return ScriptedModelReply(text="slow reply")
        return ScriptedModelReply(text=f"echo: {text}")

    return respond


def _check_protocol(harness: AgentHarness) -> AgentHarness:
    return harness


_check_protocol(ScriptedHarness())
