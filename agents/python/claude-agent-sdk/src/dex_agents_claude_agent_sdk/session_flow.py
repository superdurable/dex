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
    AgentTracingOptions,
    MediaStore,
    ModelSpendLimit,
    RunModelLimit,
    ToolEffect,
    ToolPolicy,
)
from dex_agents.builtin_loop import DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT, ToolSource
from dex_agents_claude_agent_sdk.runner import (
    DEFAULT_CUSTOM_TOOL_SERVER_NAME,
    ClaudeAgentOptionsFactory,
    ClaudeAgentRunner,
)
from dex_agents_claude_agent_sdk.tool_gate import ClaudeCodeToolPermission


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
    tracing: AgentTracingOptions | None = None,
    run_model_limit: RunModelLimit | None = None,
    spend_limit: ModelSpendLimit | None = None,
    tool_sources: Sequence[ToolSource] = (),
    tool_source_listing_timeout: timedelta = DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT,
    claude_code_tool_permission: ClaudeCodeToolPermission | None = None,
    media_store: MediaStore | None = None,
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

    Tool sources add tools that always run in their own tool Step, reads
    included. ``ConnectorToolSource`` offers reviewed Connector operations
    this way: each call gets the operation's effect class, approval, Call ID,
    timeout, and retry policy from its manifest, as in an agent on the built-in loop.

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
        tracing: OpenTelemetry GenAI span options, such as the tracer provider
            and agent name. ``None``, the default, uses ``AgentTracingOptions()``:
            spans go to the global tracer provider when the OpenTelemetry API
            is installed.
        run_model_limit: Method timeout and attempts of the ``RunModel`` Step
            that runs one turn segment. A segment makes several model calls and
            runs inline tools, so it can need longer than the default 15
            minutes. A failed segment reruns whole. ``None``, the default, uses
            ``RunModelLimit()``.
        spend_limit: The application's ``ModelSpendLimit``. It is checked once
            before each turn segment, and usage arrives when the segment ends,
            so one segment can overshoot it. Set ``max_budget_usd`` in the
            ``ClaudeAgentOptions`` too to bound a single segment. ``None``, the
            default, makes no checks.
        tool_sources: Tool sources, such as ``ConnectorToolSource``, served
            from the custom tool server. See ``ClaudeAgentRunner``. A
            ``tool_policies`` entry for a connector tool changes only its
            approval. Defaults to none.

            .. versionadded:: 0.4.0
        tool_source_listing_timeout: How long one ``RunModel`` attempt waits
            for each source's ``list_tools``. Defaults to 10 seconds.

            .. versionadded:: 0.4.0
        claude_code_tool_permission: Decides each new call of a tool that runs
            inside the Claude Code CLI, such as ``Bash``, from its input: a
            ``ToolPolicy`` for that call, ``ClaudeCodeToolDenial`` to refuse
            it, or ``None`` for ``tool_policies`` and ``default_tool_effect``.
            See ``ClaudeCodeToolPermission``. Defaults to none.

            .. versionadded:: 0.5.0
        media_store: The store that holds the session's images, such as
            ``S3MediaStore``; pass the store your clients upload to, on every
            Worker. With a store, the session accepts user messages with
            images, and the model sees them and tool result images as Claude
            ``image`` blocks; the session stores only references. See
            ``ClaudeAgentRunner``. ``None``, the default, refuses images.

            .. versionadded:: 0.5.0

    Returns:
        An ``AgentSessionFlow`` that runs the agent with ``ClaudeAgentRunner``.

    Raises:
        ValueError: If an option is invalid, such as a duplicate custom tool
            name, a custom tool named like a Claude Code tool (see
            ``CLAUDE_CODE_BUILT_IN_TOOL_NAMES``), a source tool that repeats a
            custom tool, a Claude Code tool, or another source's tool, or a
            non-positive ``interrupt_deadline``.
        TypeError: If a tool source lacks a ``ToolSource`` method or a
            non-blank ``source_name``.
    """
    return AgentSessionFlow(
        ClaudeAgentRunner(
            options_factory,
            custom_tools,
            custom_tool_server_name=custom_tool_server_name,
            tool_sources=tool_sources,
            tool_source_listing_timeout=tool_source_listing_timeout,
            claude_code_tool_permission=claude_code_tool_permission,
            media_store=media_store,
        ),
        flow_type=flow_type,
        options=AgentSessionOptions(
            tool_policies=dict(tool_policies or {}),
            default_tool_effect=default_tool_effect,
            event_stream_capacity_bytes=event_stream_capacity_bytes,
            interrupt_deadline=interrupt_deadline,
            tracing=AgentTracingOptions() if tracing is None else tracing,
            run_model_limit=RunModelLimit() if run_model_limit is None else run_model_limit,
            spend_limit=spend_limit,
        ),
    )
