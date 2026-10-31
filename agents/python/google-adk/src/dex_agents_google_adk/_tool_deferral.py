# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The ADK plugin that turns every tool call into a Dex tool call Step."""

from __future__ import annotations

import inspect
import json
from collections.abc import AsyncIterator, Callable, Iterator
from typing import Any

from google.adk.agents import BaseAgent, LlmAgent
from google.adk.agents.callback_context import CallbackContext
from google.adk.agents.invocation_context import InvocationContext
from google.adk.agents.readonly_context import ReadonlyContext
from google.adk.code_executors import BuiltInCodeExecutor
from google.adk.models import LlmRequest, LlmResponse
from google.adk.plugins import BasePlugin
from google.adk.tools import BaseTool, FunctionTool, ToolContext
from google.adk.tools.agent_tool import AgentTool
from google.adk.tools.base_toolset import BaseToolset
from google.adk.tools.exit_loop_tool import exit_loop
from pydantic_core import to_jsonable_python

from dex_agents import (
    AgentToolCall,
    AgentTurnContext,
    DeferredToolCall,
    ToolCallStatus,
    ToolEffect,
    ToolPolicy,
    describe_tool_call_outcome,
)

TOOL_DEFERRAL_PLUGIN_NAME = "dex_tool_deferral"
DEFERRAL_CONFIRMATION_HINT = (
    "The Dex agent session runs this tool call in its own durable Step and resumes the turn "
    "with the recorded result."
)
DEFERRED_TOOL_RESPONSE = {"error": "This tool call is deferred to a durable Dex Step."}
# ADK tools that steer the ADK invocation itself, so they must run inside it.
ADK_CONTROL_TOOL_CLASS_NAMES = frozenset(
    {
        "FinishTaskTool",
        "SetModelResponseTool",
        "TransferToAgentTool",
        "_SingleTurnAgentTool",
        "_TaskAgentTool",
    }
)
ADK_CONTROL_TOOL_NAMES = frozenset(
    {"exit_loop", "finish_task", "set_model_response", "transfer_to_agent"}
)
# ADK tools that only read ADK services.
ADK_READ_TOOL_CLASS_NAMES = frozenset({"LoadArtifactsTool", "LoadMemoryTool", "PreloadMemoryTool"})


class ToolDeferralPlugin(BasePlugin):
    """Defer every tool call except ADK control tools, through ADK tool confirmations.

    One instance serves one ``RunModel`` attempt. It must be the last plugin,
    so application plugins see each call first. A deferred call pauses the
    ADK invocation; ADK control tools run inline because they steer it.
    """

    def __init__(self, turn: AgentTurnContext, session_id: str, root_agent: BaseAgent) -> None:
        super().__init__(name=TOOL_DEFERRAL_PLUGIN_NAME)
        self._turn = turn
        self._session_id = session_id
        self._root_agent = root_agent
        self.deferred_tool_calls: list[DeferredToolCall] = []
        self.control_tool_names: dict[str, str] = {}
        self.model_call_count = 0

    async def before_model_callback(
        self, *, callback_context: CallbackContext, llm_request: LlmRequest
    ) -> LlmResponse | None:
        self.model_call_count += 1
        return None

    async def before_tool_callback(
        self,
        *,
        tool: BaseTool,
        tool_args: dict[str, Any],
        tool_context: ToolContext,
    ) -> dict[str, Any] | None:
        # ADK answers an unknown tool name with a bare BaseTool stand-in.
        if type(tool) is BaseTool:
            return None
        if tool_context.session.id != self._session_id:
            raise RuntimeError(
                f"tool {tool.name} runs in a nested ADK runner inside RunModel, "
                "where Dex cannot give it a Step"
            )
        tool_call_id = tool_context.function_call_id
        if not tool_call_id:
            raise RuntimeError(f"ADK called tool {tool.name} without a function call ID")
        record = self._turn.recorded_tool_call(tool_call_id)
        if record is not None:
            return recorded_tool_response(record)
        _raise_for_control_tool_beside_other_calls(tool_context)
        if is_adk_control_tool(tool):
            self._turn.events.write_tool_call_started(tool_call_id, tool.name)
            self.control_tool_names[tool_call_id] = tool.name
            return None
        policy = await self._resolve_policy(tool, tool_args, tool_context)
        await self._require_deferrable_tool(tool, policy, tool_context)
        callback_response = await run_agent_before_tool_callbacks(tool, tool_args, tool_context)
        if callback_response is not None:
            return callback_response
        tool_context.request_confirmation(hint=DEFERRAL_CONFIRMATION_HINT)
        tool_context.actions.skip_summarization = True
        self.deferred_tool_calls.append(
            DeferredToolCall(
                tool_call_id=tool_call_id,
                tool_name=tool.name,
                arguments=json.dumps(tool_args, ensure_ascii=False),
                policy=policy,
            )
        )
        return dict(DEFERRED_TOOL_RESPONSE)

    async def _resolve_policy(
        self,
        tool: BaseTool,
        tool_args: dict[str, Any],
        tool_context: ToolContext,
    ) -> ToolPolicy:
        policy = self._turn.resolve_tool_policy(tool.name, adk_framework_tool_effect(tool))
        if policy.is_approval_required:
            return policy
        if await tool.check_require_confirmation(tool_args, tool_context):
            return ToolPolicy(policy.effect, requires_approval=True)
        return policy

    async def _require_deferrable_tool(
        self, tool: BaseTool, policy: ToolPolicy, tool_context: ToolContext
    ) -> None:
        """Fail the model call when its tool Step could run the wrong tool or repeat a write."""
        lookup_context = tool_context.get_invocation_context().model_copy()
        found = await find_agent_tool(self._root_agent, tool.name, lookup_context)
        if found is None or not (
            found[0].name == tool_context.agent_name or is_same_function_tool(found[1], tool)
        ):
            raise RuntimeError(
                f"agent {tool_context.agent_name} called tool {tool.name}, but its tool Step "
                f"would run the {tool.name} of another agent; give tools distinct names"
            )
        if isinstance(tool, AgentTool) and policy.effect is ToolEffect.READ:
            inner_writes = [
                inner_tool.name
                async for inner_tool in _callable_agent_tools(tool.agent, lookup_context)
                if self._turn.resolve_tool_policy(
                    inner_tool.name, adk_framework_tool_effect(inner_tool)
                ).effect
                is not ToolEffect.READ
            ]
            if inner_writes:
                raise RuntimeError(
                    f"AgentTool {tool.name} has the read effect, but its agent calls "
                    f"{', '.join(sorted(inner_writes))}, which may write; a read tool Step can "
                    "retry, so give the AgentTool a write effect"
                )


def _raise_for_control_tool_beside_other_calls(tool_context: ToolContext) -> None:
    """Fail before ADK applies a control tool whose sibling calls still need Dex Steps."""
    sibling_names = next(
        (
            [function_call.name or "" for function_call in event.get_function_calls()]
            for event in reversed(tool_context.session.events)
            if any(
                function_call.id == tool_context.function_call_id
                for function_call in event.get_function_calls()
            )
        ),
        [],
    )
    agent = tool_context.get_invocation_context().agent
    control_names = set(ADK_CONTROL_TOOL_NAMES)
    if isinstance(agent, LlmAgent):
        control_names.update(
            sub_agent.name
            for sub_agent in agent.sub_agents
            if getattr(sub_agent, "mode", None) in ("task", "single_turn")
        )
    called_control_names = sorted(name for name in sibling_names if name in control_names)
    if called_control_names and any(name not in control_names for name in sibling_names):
        raise RuntimeError(
            f"the model called {', '.join(called_control_names)} together with tools that need "
            "Dex Steps; ADK applies a control tool in the same response, before those results "
            "exist, so Dex fails the turn instead"
        )


def is_adk_control_tool(tool: BaseTool) -> bool:
    """Report whether a tool steers the ADK invocation and must run inside it."""
    tool_class = type(tool)
    if (
        tool_class.__module__.startswith("google.adk.")
        and tool_class.__name__ in ADK_CONTROL_TOOL_CLASS_NAMES
    ):
        return True
    return isinstance(tool, FunctionTool) and tool.func is exit_loop


def adk_framework_tool_effect(tool: BaseTool) -> ToolEffect | None:
    """Return the effect ADK metadata implies for a tool, or ``None`` when it implies none."""
    tool_class = type(tool)
    if (
        tool_class.__module__.startswith("google.adk.")
        and tool_class.__name__ in ADK_READ_TOOL_CLASS_NAMES
    ):
        return ToolEffect.READ
    annotations = getattr(getattr(tool, "raw_mcp_tool", None), "annotations", None)
    if getattr(annotations, "readOnlyHint", None) is True:
        return ToolEffect.READ
    if getattr(annotations, "idempotentHint", None) is True:
        return ToolEffect.IDEMPOTENT_WRITE
    return None


async def find_agent_tool(
    root_agent: BaseAgent, tool_name: str, invocation_context: InvocationContext
) -> tuple[LlmAgent, BaseTool] | None:
    """Return the first agent, depth first, with a tool of this name, and that tool.

    The search sets ``invocation_context.agent``; pass a context the caller
    does not otherwise use.
    """
    for agent in walk_agent_tree(root_agent):
        if not isinstance(agent, LlmAgent):
            continue
        invocation_context.agent = agent
        for tool in await agent.canonical_tools(ReadonlyContext(invocation_context)):
            if tool.name == tool_name:
                return agent, tool
    return None


def require_supported_agent_tree(root_agent: BaseAgent) -> None:
    """Fail at construction for ADK features that cannot run as Dex Steps.

    Raises:
        ValueError: If an agent, or an agent inside an ``AgentTool``, has a code
            executor other than ``BuiltInCodeExecutor``, or a sub-agent in
            ``task`` or ``single_turn`` mode sits under a root that is not an
            ``LlmAgent``.
    """
    for agent in walk_agent_tree(root_agent):
        if not isinstance(agent, LlmAgent):
            continue
        for tool_union in agent.tools:
            if isinstance(tool_union, AgentTool) and not is_adk_control_tool(tool_union):
                _require_no_code_executor_in_steps(tool_union.agent)
        code_executor = agent.code_executor
        if code_executor is not None and not isinstance(code_executor, BuiltInCodeExecutor):
            raise ValueError(
                f"agent {agent.name} runs code with {type(code_executor).__name__} inside "
                "RunModel, where a retry runs model-written code again; use "
                "BuiltInCodeExecutor or a tool that runs the code in its own Step"
            )
        mode = getattr(agent, "mode", None)
        if (
            agent is not root_agent
            and str(getattr(mode, "value", mode)) in ("task", "single_turn")
            and not isinstance(root_agent, LlmAgent)
        ):
            raise ValueError(
                f"sub-agent {agent.name} uses {getattr(mode, 'value', mode)} mode, which ADK "
                "runs only under a root LlmAgent"
            )


def _require_no_code_executor_in_steps(
    agent: BaseAgent, visited_agent_ids: set[int] | None = None
) -> None:
    """Fail for an AgentTool's agents that would rerun model-written code on a Step retry."""
    visited = visited_agent_ids if visited_agent_ids is not None else set()
    for inner_agent in walk_agent_tree(agent):
        if id(inner_agent) in visited or not isinstance(inner_agent, LlmAgent):
            continue
        visited.add(id(inner_agent))
        code_executor = inner_agent.code_executor
        if code_executor is not None and not isinstance(code_executor, BuiltInCodeExecutor):
            raise ValueError(
                f"agent {inner_agent.name} inside an AgentTool runs code with "
                f"{type(code_executor).__name__}, which a tool Step retry runs again; use "
                "BuiltInCodeExecutor"
            )
        for tool_union in inner_agent.tools:
            if isinstance(tool_union, AgentTool):
                _require_no_code_executor_in_steps(tool_union.agent, visited)


def require_distinct_tool_names(root_agent: BaseAgent) -> None:
    """Fail when two agents give one name to different tools, before any session runs.

    A tool Step finds its tool by name, so a shared name could run the wrong
    tool. Toolsets resolve their tools per call; ``RunModel`` checks those.

    Raises:
        ValueError: If two agents declare different tools with one name.
    """
    owners: dict[str, tuple[str, object]] = {}
    for agent in walk_agent_tree(root_agent):
        if not isinstance(agent, LlmAgent):
            continue
        for tool_union in agent.tools:
            if isinstance(tool_union, BaseToolset):
                continue
            tool = tool_union if isinstance(tool_union, BaseTool) else FunctionTool(tool_union)
            if is_adk_control_tool(tool):
                continue
            identity: object = tool.func if isinstance(tool, FunctionTool) else tool
            owner = owners.setdefault(tool.name, (agent.name, identity))
            if owner[1] is not identity:
                raise ValueError(
                    f"agents {owner[0]} and {agent.name} both have a tool named {tool.name}; "
                    "a Dex tool Step finds its tool by name, so give tools distinct names"
                )


def walk_agent_tree(agent: BaseAgent) -> Iterator[BaseAgent]:
    yield agent
    for sub_agent in agent.sub_agents:
        yield from walk_agent_tree(sub_agent)


def is_same_function_tool(first: BaseTool, second: BaseTool) -> bool:
    if first is second:
        return True
    return (
        isinstance(first, FunctionTool)
        and isinstance(second, FunctionTool)
        and first.func is second.func
    )


async def _callable_agent_tools(
    agent: BaseAgent,
    invocation_context: InvocationContext,
    visited_agent_ids: set[int] | None = None,
) -> AsyncIterator[BaseTool]:
    """Yield every tool an agent tree can call, looking inside nested ``AgentTool``s."""
    visited = visited_agent_ids if visited_agent_ids is not None else set()
    for inner_agent in walk_agent_tree(agent):
        if id(inner_agent) in visited or not isinstance(inner_agent, LlmAgent):
            continue
        visited.add(id(inner_agent))
        invocation_context.agent = inner_agent
        for tool in await inner_agent.canonical_tools(ReadonlyContext(invocation_context)):
            if isinstance(tool, AgentTool) and not is_adk_control_tool(tool):
                async for nested_tool in _callable_agent_tools(
                    tool.agent, invocation_context, visited
                ):
                    yield nested_tool
            # Built-in model tools, such as google_search, have no declaration and never run here.
            elif not is_adk_control_tool(tool) and tool._get_declaration() is not None:
                yield tool


async def run_agent_before_tool_callbacks(
    tool: BaseTool,
    tool_args: dict[str, Any],
    tool_context: ToolContext,
) -> dict[str, Any] | None:
    """Run the calling agent's ``before_tool_callback`` chain, as ADK would before the tool.

    Returns:
        The first non-``None`` callback response, or ``None`` when every
        callback lets the call through.
    """
    agent = tool_context.get_invocation_context().agent
    if not isinstance(agent, LlmAgent):
        return None
    for callback in agent.canonical_before_tool_callbacks:
        response = _invoke_before_tool_callback(callback, tool, tool_args, tool_context)
        if inspect.isawaitable(response):
            response = await response
        if response is not None:
            return dict(response)
    return None


def _invoke_before_tool_callback(
    callback: Callable[..., Any],
    tool: BaseTool,
    tool_args: dict[str, Any],
    tool_context: ToolContext,
) -> Any:
    keyword_arguments = {"tool": tool, "args": tool_args, "tool_context": tool_context}
    try:
        inspect.signature(callback).bind(**keyword_arguments)
    except TypeError:
        # ADK passes positionally to callbacks that name their parameters differently.
        return callback(tool, tool_args, tool_context)
    except ValueError:
        pass
    return callback(**keyword_arguments)


def recorded_tool_response(record: AgentToolCall) -> dict[str, Any]:
    """Return the function response ADK gives the model for a final Dex record."""
    if record.status is ToolCallStatus.SUCCEEDED:
        return decode_tool_output(record.output or "")
    return {"error": describe_tool_call_outcome(record)}


def encode_tool_output(result: object) -> str:
    """Encode an ADK tool result as the record output text.

    Returns:
        A bare string result as is, unless it would read back as a JSON
        object; any other result as the JSON text of its ADK function response.
    """
    response = result if isinstance(result, dict) else {"result": result}
    jsonable_response = to_jsonable_python(response, fallback=str)
    result_text = jsonable_response.get("result")
    if (
        len(jsonable_response) == 1
        and isinstance(result_text, str)
        and not isinstance(_decode_json(result_text), dict)
    ):
        return result_text
    return json.dumps(jsonable_response, ensure_ascii=False)


def decode_tool_output(output: str) -> dict[str, Any]:
    """Decode record output text back into the ADK function response it encodes."""
    decoded = _decode_json(output)
    if isinstance(decoded, dict):
        return decoded
    return {"result": output}


def _decode_json(text: str) -> object:
    try:
        decoded: object = json.loads(text)
    except ValueError:
        return None
    return decoded
