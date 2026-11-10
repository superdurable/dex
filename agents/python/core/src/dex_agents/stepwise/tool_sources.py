# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Tool sources: tools that a Stepwise agent lists at run time, such as an MCP server's tools."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from typing import Any, Protocol

from dex_agents.runner import (
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    DeferredToolOutcome,
    ToolTaskPoll,
    ToolTaskPollOutcome,
)


class SourceToolDefinition(Protocol):
    """Describe one tool that a ``ToolSource`` lists, as the model sees it.

    ``McpTool`` satisfies it. The runner reads only these three attributes.

    Attributes:
        name: Tool name the model calls. It must not repeat a function tool,
            an enabled built-in tool, or another source's tool.
        description: Tool description for the model.
        input_schema: JSON Schema of the tool's arguments object.
    """

    @property
    def name(self) -> str:
        """Return the tool name the model calls."""
        ...

    @property
    def description(self) -> str:
        """Return the tool description for the model."""
        ...

    @property
    def input_schema(self) -> Mapping[str, Any]:
        """Return the JSON Schema of the tool's arguments object."""
        ...


class ToolSource(Protocol):
    """Offer tools that a Stepwise agent lists at run time and runs one tool Step per call.

    Put a source in ``AgentDefinition.tool_sources``. ``McpToolSource``
    implements it. In every ``RunModel`` of a chat or executing turn, the
    runner awaits ``list_tools`` and offers the listed tools after the
    function tools and before the built-in tools, waiting at most
    ``AgentDefinition.tool_source_listing_timeout``. A source whose listing
    raises or takes longer offers no tools in that model call, so a down
    server never fails a turn; cache the listing per Worker and keep it
    cheap, as ``McpToolSource`` does. A planning turn and a turn past its
    model call limit list nothing. Framework adapters that take
    ``tool_sources`` serve them through ``FrameworkToolSourceRouter``.

    When the model calls a listed tool, the runner returns the source's
    ``deferred_tool_call``, so the source picks the policy, the effect class,
    and the ``tool_source_reference``. The runner passes the tool definition
    it offered as ``listed_tool``, so a refresh during the model call cannot
    change it. The runner stores the source's ``source_name`` with that
    reference, so the tool Step and ``PollToolTask`` reach the same source,
    with the source's own reference, on any Worker, without a listing, and
    whatever the order of ``tool_sources``. Keep ``source_name`` stable across
    deploys: a stored call whose source name no Worker has raises
    ``LookupError``, so its effect class decides its outcome. The call then
    runs in its own tool Step through
    ``execute_deferred_tool_call``, after an approval when its policy needs
    one, and a tool task that it starts is polled through ``poll_tool_task``.
    A name that no listing offered fails as an unknown tool without an
    approval; it never reaches a source.

    A source may also define optional methods. ``declared_tool_names()``
    returns names it always lists, so ``AgentDefinition`` refuses a
    collision when it is built. ``cached_tools()`` returns its last listing
    without I/O, so ``estimate_input_tokens`` counts its tools before this
    runner has listed them. ``async cancel_tool_task(cancellation)`` cancels a
    task that ``cancel_turn`` abandoned, as ``ToolTaskCanceller`` describes; a
    source without it leaves the task running. A fourth,
    ``tool_call_step_options(tool_call)``, returns ``ToolCallStepOptions`` for
    one call's tool Step, or ``None`` for the defaults;
    ``ConnectorToolSource`` uses it for each operation's timeout and retry
    policy.

    Example:
        >>> tools = McpToolSource([McpStdioServerConfig(name="files", command="mcp-files")])
        >>> agent = AgentDefinition(
        ...     name="files",
        ...     instructions="You manage reports.",
        ...     model=LiteLLMModelClient("openai/gpt-5-mini"),
        ...     tool_sources=[tools],
        ... )

    .. versionchanged:: 0.4.0
        A source may define ``tool_call_step_options``.
    """

    @property
    def source_name(self) -> str:
        """Return the source's stable, non-blank name, unique within one ``AgentDefinition``.

        Stored calls are routed by it, so keep it across deploys.
        """
        ...

    async def list_tools(self) -> Sequence[SourceToolDefinition]:
        """Return the tools to offer now.

        Returns:
            The tools, in the order the model sees them.

        Raises:
            Exception: A listing failure fails the ``RunModel`` attempt, which
                retries.
        """
        ...

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: SourceToolDefinition,
    ) -> DeferredToolCall:
        """Build the ``DeferredToolCall`` for one model call of a listed tool.

        Args:
            turn: The ``RunModel`` context. Resolve the policy with
                ``turn.resolve_tool_policy``, so session policies win.
            tool_call_id: Model tool call ID.
            tool_name: The listed tool name the model called.
            arguments: Tool arguments as JSON text.
            listed_tool: The definition this source listed for this model
                call. Build from it, not from a cache a refresh may change.

        Returns:
            The deferred call.
        """
        ...

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        """Run one call of a listed tool in its tool Step.

        Args:
            call: The approved call.

        Returns:
            The tool's outcome.

        Raises:
            ToolExecutionError: A failure the model sees.
        """
        ...

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        """Poll a tool task that ``execute_deferred_tool_call`` started, once.

        Args:
            poll: The call's record.

        Returns:
            The outcome of this poll.
        """
        ...
