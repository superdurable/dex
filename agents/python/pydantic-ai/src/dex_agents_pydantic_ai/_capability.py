# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Run capabilities that stop a model Step after one model call and run one tool in a tool Step."""

from __future__ import annotations

from dataclasses import dataclass, field, replace
from typing import Any

from pydantic_ai import (
    AgentStreamEvent,
    CallDeferred,
    ModelResponse,
    PartDeltaEvent,
    PartStartEvent,
    RunContext,
    TextPart,
    TextPartDelta,
    ThinkingPart,
    ThinkingPartDelta,
    ToolCallPart,
    ToolDefinition,
    UnexpectedModelBehavior,
)
from pydantic_ai.capabilities import AbstractCapability, CapabilityOrdering
from pydantic_ai.models import ModelRequestContext
from pydantic_ai.toolsets import AbstractToolset, ToolsetTool, WrapperToolset

from dex_agents import AgentTurnContext, ToolEffect, ToolPolicy

TOOL_SEARCH_TOOL_KIND = "tool-search"


@dataclass
class DexModelStepCapability(AbstractCapability[Any]):
    turn: AgentTurnContext
    tool_definitions: dict[str, ToolDefinition] = field(default_factory=dict)

    @classmethod
    def get_serialization_name(cls) -> str | None:
        return None

    def get_ordering(self) -> CapabilityOrdering:
        # Outside every other capability, including outermost ones such as tool search.
        return CapabilityOrdering(position="outermost", wraps=(AbstractCapability,))

    def resolve_tool_policy(self, tool_name: str) -> ToolPolicy:
        return self.turn.resolve_tool_policy(
            tool_name, framework_tool_effect(self.tool_definitions.get(tool_name))
        )

    def has_structured_result(self, tool_name: str) -> bool:
        tool_definition = self.tool_definitions.get(tool_name)
        return tool_definition is not None and tool_definition.tool_kind is not None

    def get_wrapper_toolset(self, toolset: AbstractToolset[Any]) -> AbstractToolset[Any]:
        return ExternalFunctionToolsToolset(toolset, self.tool_definitions)

    async def after_model_request(
        self,
        ctx: RunContext[Any],
        *,
        request_context: ModelRequestContext,
        response: ModelResponse,
    ) -> ModelResponse:
        output_tool_names = {
            tool_def.name for tool_def in request_context.model_request_parameters.output_tools
        }
        tool_calls = response.tool_calls
        is_output_call = [call.tool_name in output_tool_names for call in tool_calls]
        if not any(is_output_call) or all(is_output_call):
            return response
        # Pydantic AI would drop the other calls; they run first, and the model answers after.
        return replace(
            response,
            parts=[
                part
                for part in response.parts
                if not (isinstance(part, ToolCallPart) and part.tool_name in output_tool_names)
            ],
        )

    def listens_to(self, event: AgentStreamEvent) -> bool:
        return isinstance(event, PartStartEvent | PartDeltaEvent)

    async def on_event(self, ctx: RunContext[Any], *, event: AgentStreamEvent) -> None:
        events = self.turn.events
        if isinstance(event, PartStartEvent):
            if isinstance(event.part, TextPart):
                events.write_text_delta(event.part.content)
            elif isinstance(event.part, ThinkingPart):
                events.write_reasoning_delta(event.part.content)
        elif isinstance(event, PartDeltaEvent):
            if isinstance(event.delta, TextPartDelta):
                events.write_text_delta(event.delta.content_delta)
            elif isinstance(event.delta, ThinkingPartDelta) and event.delta.content_delta:
                events.write_reasoning_delta(event.delta.content_delta)


@dataclass
class ExternalFunctionToolsToolset(WrapperToolset[Any]):
    tool_definitions: dict[str, ToolDefinition]

    async def get_tools(self, ctx: RunContext[Any]) -> dict[str, ToolsetTool[Any]]:
        tools = await super().get_tools(ctx)
        self.tool_definitions.update((name, tool.tool_def) for name, tool in tools.items())
        # External tools are never executed, so the run ends right after the model call.
        return {
            name: replace(tool, tool_def=replace(tool.tool_def, kind="external"))
            if tool.tool_def.kind == "function"
            else tool
            for name, tool in tools.items()
        }


@dataclass
class DexToolStepCapability(AbstractCapability[Any]):
    tool_name: str
    refused_external_tool_call_ids: set[str] = field(default_factory=set)
    tool_body_errors: list[BaseException] = field(default_factory=list)

    def is_tool_retry_limit_error(self, error: UnexpectedModelBehavior) -> bool:
        # Pydantic AI raises the tool's own retry limit outside the tool body, with this text.
        if any(error is body_error for body_error in self.tool_body_errors):
            return False
        return str(error).startswith(f"Tool {self.tool_name!r} exceeded max retries count")

    @classmethod
    def get_serialization_name(cls) -> str | None:
        return None

    def get_ordering(self) -> CapabilityOrdering:
        return CapabilityOrdering(position="outermost")

    async def prepare_tools(
        self,
        ctx: RunContext[Any],
        tool_defs: list[ToolDefinition],
    ) -> list[ToolDefinition]:
        # A tool that tool search revealed in the turn is hidden again in this one-call run.
        return [
            replace(tool_def, defer_loading=False) if tool_def.name == self.tool_name else tool_def
            for tool_def in tool_defs
        ]

    def get_wrapper_toolset(self, toolset: AbstractToolset[Any]) -> AbstractToolset[Any]:
        return ToolBodyErrorRecordingToolset(toolset, self.tool_body_errors)

    async def after_tool_validate(
        self,
        ctx: RunContext[Any],
        *,
        call: ToolCallPart,
        tool_def: ToolDefinition,
        args: dict[str, Any],
    ) -> dict[str, Any]:
        # An external tool has no function; deferring it ends the run before anything runs.
        if tool_def.kind == "external":
            self.refused_external_tool_call_ids.add(call.tool_call_id)
            raise CallDeferred()
        return args


@dataclass
class ToolBodyErrorRecordingToolset(WrapperToolset[Any]):
    tool_body_errors: list[BaseException]

    async def call_tool(
        self,
        name: str,
        tool_args: dict[str, Any],
        ctx: RunContext[Any],
        tool: ToolsetTool[Any],
    ) -> Any:
        try:
            return await super().call_tool(name, tool_args, ctx, tool)
        except UnexpectedModelBehavior as error:
            # From the tool body, such as a sub-agent's model error; its work may have happened.
            self.tool_body_errors.append(error)
            raise


def framework_tool_effect(tool_definition: ToolDefinition | None) -> ToolEffect | None:
    if tool_definition is None:
        return None
    if tool_definition.tool_kind == TOOL_SEARCH_TOOL_KIND:
        return ToolEffect.READ
    annotations = (tool_definition.metadata or {}).get("annotations")
    if not isinstance(annotations, dict):
        return None
    if annotations.get("readOnlyHint") is True:
        return ToolEffect.READ
    if annotations.get("idempotentHint") is True:
        return ToolEffect.IDEMPOTENT_WRITE
    return None
