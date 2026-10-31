# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""One-call construction of a Step-mode agent session Flow for an OpenAI Agents SDK agent."""

from __future__ import annotations

from collections.abc import Mapping
from datetime import timedelta
from typing import Any

from agents import Agent, RunConfig, RunHooks
from agents.run import DEFAULT_MAX_TURNS

from dex_agents import (
    DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    AgentSessionFlow,
    AgentSessionOptions,
    ToolEffect,
    ToolPolicy,
)
from dex_agents_openai_agents.runner import OpenAIAgentsRunner


def openai_agents_session_flow(
    agent: Agent[Any],
    *,
    flow_type: str,
    tool_policies: Mapping[str, ToolPolicy] | None = None,
    default_tool_effect: ToolEffect = ToolEffect.UNCLASSIFIED,
    interrupt_deadline: timedelta | None = None,
    event_stream_capacity_bytes: int = DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    context: Any = None,
    run_config: RunConfig | None = None,
    max_turns: int | None = DEFAULT_MAX_TURNS,
    hooks: RunHooks[Any] | None = None,
    allow_at_least_once_tools: bool = False,
) -> AgentSessionFlow:
    """Build the durable Step-mode agent session Flow for one OpenAI Agents SDK agent.

    Call it once per agent at application startup, and pass the returned Flow
    to the Registry, the Worker, and every Client call. One Flow execution is
    one agent session; the Flow ID is the session ID. The Registry needs
    ``allow_async_handlers=True`` and an ``AsyncWorker`` serves it. Give the
    Worker enough task slots for the largest batch of parallel tool calls.

    Every model call runs in a ``RunModel`` Step, and every function tool call,
    READ included, runs in its own Step: ``CallTool`` for ``READ`` and
    ``IDEMPOTENT_WRITE`` tools, ``CallSingleAttemptTool`` for the others, after
    ``AwaitToolApproval`` when the policy or the tool's own ``needs_approval``
    setting requires approval. Policies are keyed by the tool's qualified name,
    which is ``FunctionTool.qualified_name``: the tool name, or
    ``"<namespace>.<name>"`` for a namespaced tool.

    Example:
        >>> @function_tool
        ... def search_orders(customer_id: str) -> str: ...
        >>> @function_tool
        ... def create_refund(order_id: str) -> str: ...
        >>> agent = Agent(name="Support", instructions="Help the user.",
        ...               tools=[search_orders, create_refund])
        >>> flow = openai_agents_session_flow(
        ...     agent,
        ...     flow_type="SupportAgent",
        ...     tool_policies={
        ...         "search_orders": ToolPolicy(ToolEffect.READ),
        ...         "create_refund": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
        ...     },
        ... )
        >>> registry = Registry([flow], allow_async_handlers=True)

    Args:
        agent: The starting agent. The runner copies it and every agent it
            reaches through handoffs; later changes to those agents are ignored.
            Agents used as tools are not copied, so changes to them apply.
        flow_type: Protocol Flow type, unique within the Registry. Keep it
            stable: running sessions resume by it.
        tool_policies: Policies by qualified tool name. They override MCP
            annotations on converted MCP function tools and
            ``default_tool_effect``. Every name must be a function tool or an
            accepted at-least-once tool of the agent graph. Defaults to none.
        default_tool_effect: Effect for tools without a policy. Defaults to
            ``UNCLASSIFIED``: one attempt, after approval.
        interrupt_deadline: How long an approval may stay pending before the
            session rejects the call. ``None``, the default, waits indefinitely.
        event_stream_capacity_bytes: Byte budget of the ``AgentEvents`` Stream,
            shared by all sessions of the Flow type. Defaults to 10 MiB.
        context: The SDK run context for every run and tool call. See
            ``OpenAIAgentsRunner``. Defaults to ``None``.
        run_config: SDK run settings for every run. ``None`` uses the SDK defaults.
        max_turns: Maximum model calls in one session turn. ``None`` disables
            the limit. Defaults to the SDK default, 10.
        hooks: SDK run hooks. They run inside ``RunModel``.
        allow_at_least_once_tools: Accept tools the SDK runs inside
            ``RunModel``, such as ``Agent.mcp_servers`` tools; they repeat when
            ``RunModel`` retries. Defaults to ``False``. See ``OpenAIAgentsRunner``.

    Returns:
        An ``AgentSessionFlow`` that runs the agent with ``OpenAIAgentsRunner``.

    Raises:
        ValueError: If ``flow_type`` is blank, an option is not positive, two
            different function tools share a qualified name, a handoff has no
            known target agent, an agent tool's nested agent has a tool that
            needs approval, a policy names an unknown tool, or the graph has a
            tool the SDK cannot gate and ``allow_at_least_once_tools`` is
            ``False``.
    """
    options = AgentSessionOptions(
        tool_policies=dict(tool_policies or {}),
        default_tool_effect=default_tool_effect,
        event_stream_capacity_bytes=event_stream_capacity_bytes,
        interrupt_deadline=interrupt_deadline,
    )
    runner = OpenAIAgentsRunner(
        agent,
        context=context,
        run_config=run_config,
        max_turns=max_turns,
        hooks=hooks,
        allow_at_least_once_tools=allow_at_least_once_tools,
    )
    runner.validate_tool_policies(options.tool_policies)
    return AgentSessionFlow(runner, flow_type=flow_type, options=options)
