# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Build the shared agent session Flow for one Claude Agent SDK agent."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from datetime import timedelta
from typing import Any

from claude_agent_sdk import SdkMcpTool

from dex_agents import (
    DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    AgentSessionFlow,
    AgentSessionOptions,
    ToolEffect,
    ToolPolicy,
)
from dex_agents_claude_agent_sdk.runner import (
    DEFAULT_CUSTOM_TOOL_SERVER_NAME,
    ClaudeAgentOptionsFactory,
    ClaudeAgentRunner,
)


def claude_agent_session_flow(
    options_factory: ClaudeAgentOptionsFactory,
    custom_tools: Sequence[SdkMcpTool[Any]] = (),
    *,
    flow_type: str,
    tool_policies: Mapping[str, ToolPolicy] | None = None,
    default_tool_effect: ToolEffect = ToolEffect.UNCLASSIFIED,
    interrupt_deadline: timedelta | None = None,
    event_stream_capacity_bytes: int = DEFAULT_AGENT_EVENTS_CAPACITY_BYTES,
    custom_tool_server_name: str = DEFAULT_CUSTOM_TOOL_SERVER_NAME,
) -> AgentSessionFlow:
    """Create the durable agent session Flow for one Claude Agent SDK agent.

    Call it once per agent at application startup and pass the same Flow to
    the Registry, the Worker, and every Client call. One Flow execution is
    one session; the Flow ID is the session ID. The Registry needs
    ``allow_async_handlers=True`` and an ``AsyncWorker`` serves it.

    The Claude Agent SDK is a whole-turn harness: one ``RunModel`` Step runs
    the Claude Code CLI until it answers or defers a tool call. Tool policies
    decide where each tool call runs. Custom tools that only read run inside
    the turn, because every deferral restarts the CLI. Every other custom tool
    call runs in its own tool Step, exactly once for single-attempt effects,
    after approval when its policy requires it. Tools that run inside the Claude Code CLI, such
    as ``Bash``, ``Write``, and ``Edit``, run inside the turn and wait for
    approval first when their policy requires it. The default effect,
    ``UNCLASSIFIED``, requires approval for every tool without a policy or MCP
    annotation, built-in tools included.

    Example:
        >>> @tool("lookup_order", "Look up an order", {"order_id": str},
        ...       annotations=ToolAnnotations(readOnlyHint=True))
        ... async def lookup_order(arguments):
        ...     text = await orders.describe(arguments["order_id"])
        ...     return {"content": [{"type": "text", "text": text}]}
        >>>
        >>> @tool("create_refund", "Refund an order", {"order_id": str})
        ... async def create_refund(arguments):
        ...     text = await payments.refund(arguments["order_id"])
        ...     return {"content": [{"type": "text", "text": text}]}
        >>>
        >>> flow = claude_agent_session_flow(
        ...     lambda context: ClaudeAgentOptions(
        ...         system_prompt="You resolve order problems.",
        ...         tools=["Read", "Grep"],
        ...         cwd=f"/srv/agents/{context.flow_id}",
        ...         setting_sources=[],
        ...     ),
        ...     [lookup_order, create_refund],
        ...     flow_type="SupportAgent",
        ...     tool_policies={
        ...         "create_refund": ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE),
        ...         "Read": ToolPolicy(ToolEffect.READ),
        ...         "Grep": ToolPolicy(ToolEffect.READ),
        ...     },
        ... )
        >>> registry = Registry([flow], allow_async_handlers=True)

    Args:
        options_factory: Returns the application's ``ClaudeAgentOptions`` for
            each turn segment. See ``ClaudeAgentRunner`` for the options the
            adapter sets and the ones it rejects.
        custom_tools: Tools created with ``claude_agent_sdk.tool``. The
            adapter serves them from an in-process MCP server.
        flow_type: Protocol Flow type, unique within the Registry, such as
            ``"SupportAgent"``. Keep it stable: running sessions resume by it.
        tool_policies: Policies by tool name: a custom tool's own name, a
            built-in name such as ``"Bash"``, or ``"mcp__<server>__<tool>"``
            for an external MCP tool. They override MCP annotations and
            ``default_tool_effect``. ``None`` means no explicit policies.
        default_tool_effect: Effect for a tool without a policy or MCP
            annotation. Defaults to ``UNCLASSIFIED``, so such calls run once
            and need approval.
        interrupt_deadline: How long an approval may stay pending before the
            session rejects the call. ``None``, the default, waits indefinitely.
        event_stream_capacity_bytes: Approximate byte budget of the
            ``AgentEvents`` Stream. Defaults to 10 MiB.
        custom_tool_server_name: Name of the in-process MCP server for custom
            tools. Defaults to ``"tools"``.

    Returns:
        An ``AgentSessionFlow`` that runs the agent with ``ClaudeAgentRunner``.

    Raises:
        ValueError: If an option is invalid, such as a duplicate custom tool
            name, a custom tool named like a Claude Code tool (see
            ``CLAUDE_CODE_BUILT_IN_TOOL_NAMES``), or a non-positive
            ``interrupt_deadline``.
    """
    return AgentSessionFlow(
        ClaudeAgentRunner(
            options_factory, custom_tools, custom_tool_server_name=custom_tool_server_name
        ),
        flow_type=flow_type,
        options=AgentSessionOptions(
            tool_policies=dict(tool_policies or {}),
            default_tool_effect=default_tool_effect,
            event_stream_capacity_bytes=event_stream_capacity_bytes,
            interrupt_deadline=interrupt_deadline,
        ),
    )
