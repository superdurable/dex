# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A copy of the application's agent graph whose function tools stop the SDK for Dex Steps."""

from __future__ import annotations

import copy
import dataclasses
import json
import weakref
from collections.abc import Iterator, Mapping
from contextvars import ContextVar
from dataclasses import dataclass, field
from typing import Any, cast

from agents import (
    Agent,
    CodeInterpreterTool,
    FileSearchTool,
    FunctionTool,
    Handoff,
    HostedMCPTool,
    ImageGenerationTool,
    ProgrammaticToolCallingTool,
    RunContextWrapper,
    ShellTool,
    ToolApprovalItem,
    ToolSearchTool,
    WebSearchTool,
)
from agents._public_agent import get_public_agent
from agents.function_schema import function_schema
from agents.tool import Tool, set_function_tool_failure_error_function
from agents.tool_context import ToolContext
from agents.util._approvals import evaluate_function_tool_approval
from pydantic import BaseModel, TypeAdapter

from dex_agents import (
    AgentToolCall,
    AgentTurnContext,
    ToolCallStatus,
    ToolEffect,
    ToolPolicy,
    describe_tool_call_outcome,
)

PROVIDER_HOSTED_TOOL_TYPES = (
    FileSearchTool,
    WebSearchTool,
    HostedMCPTool,
    ImageGenerationTool,
    CodeInterpreterTool,
    ToolSearchTool,
    ProgrammaticToolCallingTool,
)
# The SDK reads this context flag to treat an output as a handled tool failure.
_SDK_HANDLED_FAILURE_ATTRIBUTE = "_function_tool_default_failure_handled"


@dataclass(frozen=True, eq=False)
class DeferrableFunctionTool:
    """One application function tool, the gated copy the SDK sees, and the Step copy."""

    qualified_name: str
    name: str
    namespace: str | None
    original_tool: FunctionTool
    owner_agent: Agent[Any]
    gated_tool: FunctionTool
    deferred_execution_tool: FunctionTool
    framework_effect: ToolEffect | None
    arguments_model: type[BaseModel] | None

    @property
    def output_type_adapter(self) -> TypeAdapter[Any] | None:
        return self.original_tool._output_type_adapter

    @property
    def has_output_schema(self) -> bool:
        return (
            self.original_tool._output_type_adapter is not None
            or self.original_tool.output_json_schema is not None
        )


@dataclass(eq=False)
class ActiveTurn:
    """The model call whose framework run is in progress in the current task."""

    turn: AgentTurnContext
    application_approval_call_ids: set[str] = field(default_factory=set)
    evaluated_call_ids: set[str] = field(default_factory=set)
    tools_by_call_id: dict[str, DeferrableFunctionTool] = field(default_factory=dict)

    def resolve_tool_call_policy(
        self, tool: DeferrableFunctionTool, tool_call_id: str
    ) -> ToolPolicy:
        policy = self.turn.resolve_tool_policy(tool.qualified_name, tool.framework_effect)
        # The SDK asks for approval without the gate when it cannot parse the arguments.
        is_application_approval_required = (
            tool_call_id in self.application_approval_call_ids
            or tool_call_id not in self.evaluated_call_ids
        )
        return ToolPolicy(
            policy.effect, policy.is_approval_required or is_application_approval_required
        )


ACTIVE_TURN: ContextVar[ActiveTurn | None] = ContextVar(
    "dex_openai_agents_active_turn", default=None
)


class DeferrableAgentGraph:
    """Clone every agent reachable through handoffs, gating each function tool."""

    def __init__(self, starting_agent: Agent[Any], *, allow_at_least_once_tools: bool) -> None:
        self._allow_at_least_once_tools = allow_at_least_once_tools
        self._agent_clones: dict[int, Agent[Any]] = {}
        self._original_agents: list[Agent[Any]] = []
        self._tool_names_by_clone_id: dict[int, set[str]] = {}
        self._tools_by_original_id: dict[int, DeferrableFunctionTool] = {}
        self.tools_by_qualified_name: dict[str, DeferrableFunctionTool] = {}
        self.at_least_once_tool_names: set[str] = set()
        self.starting_agent = self._clone_agent(starting_agent)

    def find_interrupted_tool(
        self, active_turn: ActiveTurn, item: ToolApprovalItem
    ) -> DeferrableFunctionTool:
        call_id = item.call_id
        raw_type = (
            item.raw_item.get("type")
            if isinstance(item.raw_item, dict)
            else getattr(item.raw_item, "type", None)
        )
        if raw_type != "function_call" or not call_id:
            raise ValueError(
                f"tool {item.name!r} asked for approval, but only gated function tools can run "
                "in Dex Steps"
            )
        owner_tool_names = self._tool_names_by_clone_id.get(id(get_public_agent(item.agent)))
        tool = active_turn.tools_by_call_id.get(call_id) or self._find_tool_by_name(item)
        if tool is None or owner_tool_names is None or tool.qualified_name not in owner_tool_names:
            # A nested run, such as Agent.as_tool, stopped for its own tool.
            raise ValueError(
                f"tool {item.name!r} of agent {item.agent.name!r} asked for approval outside "
                "the gated agent graph"
            )
        return tool

    def validate_tool_policies(self, tool_policies: Mapping[str, ToolPolicy]) -> None:
        known_names = set(self.tools_by_qualified_name) | self.at_least_once_tool_names
        for tool_name in tool_policies:
            if tool_name not in known_names:
                raise ValueError(
                    f"tool_policies names {tool_name!r}, which is not a function tool of this "
                    "agent graph; tools listed at run time, such as MCP server tools, take no "
                    "policy"
                )

    def find_handoff_target_clone(self, agent: Any) -> Agent[Any]:
        clone = self._agent_clones.get(id(agent))
        if clone is not None:
            return clone
        raise RuntimeError(
            f"handoff returned agent {getattr(agent, 'name', agent)!r} outside the graph"
        )

    def _find_tool_by_name(self, item: ToolApprovalItem) -> DeferrableFunctionTool | None:
        candidates = [
            tool
            for tool in self.tools_by_qualified_name.values()
            if tool.name == item.tool_name
            and item.tool_namespace in (None, tool.namespace, tool.name)
        ]
        return candidates[0] if len(candidates) == 1 else None

    def _clone_agent(self, agent: Agent[Any]) -> Agent[Any]:
        existing = self._agent_clones.get(id(agent))
        if existing is not None:
            return existing
        self._require_static_tools(agent)
        wrapped_tools = [self._wrap_tool(agent, tool) for tool in agent.tools]
        clone = agent.clone(tools=wrapped_tools, handoffs=[])
        self._agent_clones[id(agent)] = clone
        self._original_agents.append(agent)
        self._tool_names_by_clone_id[id(clone)] = {
            tool.qualified_name for tool in wrapped_tools if isinstance(tool, FunctionTool)
        }
        clone.handoffs = [self._wrap_handoff(agent, handoff) for handoff in agent.handoffs]
        return clone

    def _require_static_tools(self, agent: Agent[Any]) -> None:
        if self._allow_at_least_once_tools:
            return
        lists_tools_at_run_time = (
            bool(agent.mcp_servers)
            or bool(getattr(agent, "capabilities", None))
            or type(agent).get_all_tools is not Agent.get_all_tools
            or type(agent).get_mcp_tools is not Agent.get_mcp_tools
        )
        if lists_tools_at_run_time:
            raise ValueError(
                f"agent {agent.name!r} lists tools at run time (mcp_servers or sandbox "
                "capabilities), which the adapter cannot gate, so they would run inside RunModel "
                "and repeat on a retry; pass allow_at_least_once_tools=True to accept that"
            )

    def _wrap_tool(self, owner_agent: Agent[Any], tool: Tool) -> Tool:
        if isinstance(tool, FunctionTool):
            return self._wrap_function_tool(owner_agent, tool).gated_tool
        if isinstance(tool, PROVIDER_HOSTED_TOOL_TYPES):
            return tool
        if isinstance(tool, ShellTool) and not _is_local_shell_environment(tool):
            return tool
        tool_name = _local_tool_name(tool)
        if not self._allow_at_least_once_tools:
            raise ValueError(
                f"{type(tool).__name__} {tool_name!r} runs inside the framework loop, which the "
                "adapter cannot gate, so it would repeat on a RunModel retry; call it from a "
                "function_tool, or pass allow_at_least_once_tools=True to accept that"
            )
        self.at_least_once_tool_names.add(tool_name)
        return tool

    def _wrap_function_tool(
        self, owner_agent: Agent[Any], tool: FunctionTool
    ) -> DeferrableFunctionTool:
        existing = self._tools_by_original_id.get(id(tool))
        if existing is not None:
            return existing
        qualified_name = tool.qualified_name
        if qualified_name in self.tools_by_qualified_name:
            raise ValueError(
                f"two different function tools are named {qualified_name!r}; tool call Steps "
                "find their tool by name, so names must be unique across the agent graph"
            )
        _reject_nested_approvals(tool)
        namespace = (
            qualified_name.removesuffix(f".{tool.name}") if qualified_name != tool.name else None
        )
        deferred_execution_tool = copy.copy(tool)
        # Tool Steps let exceptions reach Dex, which retries or records the outcome.
        set_function_tool_failure_error_function(deferred_execution_tool, None)
        gated_tool = copy.copy(tool)
        wrapped = DeferrableFunctionTool(
            qualified_name=qualified_name,
            name=tool.name,
            namespace=namespace,
            original_tool=tool,
            owner_agent=owner_agent,
            gated_tool=gated_tool,
            deferred_execution_tool=deferred_execution_tool,
            framework_effect=_framework_effect_from_mcp_annotations(tool),
            arguments_model=_arguments_model(tool),
        )
        gated_tool.needs_approval = _StopForDexStep(wrapped)
        gated_tool.on_invoke_tool = _RecordedToolResult(wrapped)
        # The tool call Step runs input guardrails before the body, so a resume never repeats them.
        gated_tool.tool_input_guardrails = None
        self._tools_by_original_id[id(tool)] = wrapped
        self.tools_by_qualified_name[qualified_name] = wrapped
        return wrapped

    def _wrap_handoff(
        self,
        owner_agent: Agent[Any],
        handoff: Agent[Any] | Handoff[Any, Any],
    ) -> Agent[Any] | Handoff[Any, Any]:
        if isinstance(handoff, Agent):
            return self._clone_agent(handoff)
        agent_ref = getattr(handoff, "_agent_ref", None)
        target = agent_ref() if agent_ref is not None else None
        if not isinstance(target, Agent):
            raise ValueError(
                f"agent {owner_agent.name!r} has handoff {handoff.tool_name!r} without a known "
                "target agent; create it with handoff(agent)"
            )
        target_clone = self._clone_agent(target)
        wrapped = dataclasses.replace(
            handoff, on_invoke_handoff=_HandoffTargetMapper(self, handoff)
        )
        # The SDK resolves RunState agents through this reference, so it must name the clone.
        wrapped._agent_ref = weakref.ref(target_clone)
        return wrapped


class _StopForDexStep:
    """Stop the SDK before every call, recording whether the application wants approval."""

    def __init__(self, tool: DeferrableFunctionTool) -> None:
        self._tool = tool

    async def __call__(
        self,
        run_context: RunContextWrapper[Any],
        arguments: dict[str, Any],
        tool_call_id: str,
    ) -> bool:
        active_turn = require_active_turn()
        # The SDK's own evaluation also requires approval when validation changes arguments.
        application_decision = await evaluate_function_tool_approval(
            self._tool.original_tool,
            run_context,
            json.dumps(arguments, ensure_ascii=False),
            tool_call_id,
        )
        active_turn.evaluated_call_ids.add(tool_call_id)
        active_turn.tools_by_call_id[tool_call_id] = self._tool
        if application_decision.outcome == "require_approval":
            active_turn.application_approval_call_ids.add(tool_call_id)
        return True


class _RecordedToolResult:
    """Return the Dex result of a tool call when the SDK resumes; never run the tool."""

    def __init__(self, tool: DeferrableFunctionTool) -> None:
        self._tool = tool

    async def __call__(self, context: ToolContext[Any], arguments: str) -> Any:
        active_turn = require_active_turn()
        record = active_turn.turn.recorded_tool_call(context.tool_call_id)
        if record is None:
            raise RuntimeError(
                f"tool call {context.tool_call_id} of {self._tool.qualified_name} has no Dex "
                "record; the adapter never runs a function tool inside RunModel"
            )
        return _build_recorded_tool_result(self._tool, record, context)


def _build_recorded_tool_result(
    tool: DeferrableFunctionTool,
    record: AgentToolCall,
    context: ToolContext[Any],
) -> Any:
    if record.status is not ToolCallStatus.SUCCEEDED and tool.has_output_schema:
        # Only successful outputs match the tool's output schema.
        setattr(context, _SDK_HANDLED_FAILURE_ATTRIBUTE, True)
    return describe_tool_call_outcome(record)


class _HandoffTargetMapper:
    """Run the application's handoff and continue with the target agent's clone."""

    def __init__(self, graph: DeferrableAgentGraph, handoff: Handoff[Any, Any]) -> None:
        self._graph = graph
        self._invoke_original_handoff = handoff.on_invoke_handoff

    async def __call__(self, context: RunContextWrapper[Any], arguments: str) -> Agent[Any]:
        target = await self._invoke_original_handoff(context, arguments)
        return self._graph.find_handoff_target_clone(target)


def require_active_turn() -> ActiveTurn:
    active_turn = ACTIVE_TURN.get()
    if active_turn is None:
        raise RuntimeError("an adapter-gated tool ran outside a Dex agent session model call")
    return active_turn


def _reject_nested_approvals(tool: FunctionTool) -> None:
    nested_agent = getattr(tool, "_agent_instance", None)
    if not getattr(tool, "_is_agent_tool", False) or not isinstance(nested_agent, Agent):
        return
    for agent in _iterate_nested_agents(nested_agent):
        for nested_tool in agent.tools:
            needs_approval = getattr(nested_tool, "needs_approval", False)
            if needs_approval is not False:
                raise ValueError(
                    f"agent tool {tool.name!r} runs agent {agent.name!r}, whose tool "
                    f"{getattr(nested_tool, 'name', nested_tool)!r} needs approval; a tool call "
                    "Step cannot stop for a nested approval, so set needs_approval on the agent "
                    "tool instead"
                )


def _iterate_nested_agents(nested_agent: Agent[Any]) -> Iterator[Agent[Any]]:
    pending = [nested_agent]
    seen: set[int] = set()
    while pending:
        agent = pending.pop()
        if id(agent) in seen:
            continue
        seen.add(id(agent))
        yield agent
        for handoff in agent.handoffs:
            agent_ref = getattr(handoff, "_agent_ref", None)
            target = handoff if isinstance(handoff, Agent) else (agent_ref() if agent_ref else None)
            if isinstance(target, Agent):
                pending.append(target)
        for nested_tool in agent.tools:
            inner_agent = getattr(nested_tool, "_agent_instance", None)
            if getattr(nested_tool, "_is_agent_tool", False) and isinstance(inner_agent, Agent):
                pending.append(inner_agent)


def _arguments_model(tool: FunctionTool) -> type[BaseModel] | None:
    try:
        wrapped_callable = tool.__wrapped__
    except AttributeError:
        return None
    try:
        return function_schema(
            wrapped_callable,
            name_override=tool.name,
            use_docstring_info=False,
            strict_json_schema=tool.strict_json_schema,
        ).params_pydantic_model
    except Exception:
        # The SDK validates arguments itself when the schema cannot be rebuilt here.
        return None


def _framework_effect_from_mcp_annotations(tool: FunctionTool) -> ToolEffect | None:
    annotations = tool.mcp_annotations
    if annotations is None:
        return None
    if getattr(annotations, "readOnlyHint", None) is True:
        return ToolEffect.READ
    if getattr(annotations, "idempotentHint", None) is True:
        return ToolEffect.IDEMPOTENT_WRITE
    return None


def _is_local_shell_environment(tool: ShellTool) -> bool:
    environment = cast(Mapping[str, Any], tool.environment or {})
    return environment.get("type") == "local"


def _local_tool_name(tool: Tool) -> str:
    name = getattr(tool, "name", None)
    if isinstance(name, str) and name:
        return name
    return type(tool).__name__
