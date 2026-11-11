# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Connector tools next to Deep Agents' own: prefixed on a name collision, writes approved."""

from __future__ import annotations

import dataclasses
from collections.abc import Collection, Sequence

from dex_agents import (
    AgentToolCall,
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    DeferredToolOutcome,
    ToolCallStepOptions,
    ToolEffect,
    ToolPolicy,
    ToolTaskCancellation,
    ToolTaskPoll,
    ToolTaskPollOutcome,
)
from dex_agents.builtin_loop import SourceToolDefinition, ToolSource

#: The prefix of a connector tool whose name a Deep Agents tool already uses.
COLLIDING_CONNECTOR_TOOL_PREFIX = "connector_"


class PrefixedConnectorToolSource:
    """Offer a connectors tool source's tools beside Deep Agents' tools.

    A connector tool keeps its name unless a Deep Agents tool has it; then
    the model sees it as ``connector_<name>``, and ``approval.always`` names
    it that way. The source still runs the connector operation it listed.
    Every call that is not a read waits for approval.
    """

    def __init__(self, source: ToolSource, reserved_tool_names: Collection[str]) -> None:
        """Wrap one connectors tool source.

        Args:
            source: The CLI core's connectors tool source.
            reserved_tool_names: The Deep Agents tool names. They are fixed, so
                every Worker prefixes the same tools.
        """
        self._source = source
        self._reserved_tool_names = frozenset(reserved_tool_names)

    @property
    def source_name(self) -> str:
        """Return the wrapped source's name, so stored calls reach it on any Worker."""
        return self._source.source_name

    async def list_tools(self) -> Sequence[SourceToolDefinition]:
        """Return the source's listing, each colliding name prefixed."""
        return [self._agent_visible(tool) for tool in await self._source.list_tools()]

    def cached_tools(self) -> Sequence[SourceToolDefinition]:
        """Return the source's last listing, each colliding name prefixed."""
        read_cache = getattr(self._source, "cached_tools", None)
        cached: Sequence[SourceToolDefinition] = read_cache() if callable(read_cache) else ()
        return [self._agent_visible(tool) for tool in cached]

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: SourceToolDefinition,
    ) -> DeferredToolCall:
        """Build the source's call under the agent's name, with approval for every write."""
        built = self._source.deferred_tool_call(
            turn,
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            listed_tool=listed_tool,
        )
        if built.policy.effect is ToolEffect.READ or built.policy.is_approval_required:
            return built
        return dataclasses.replace(
            built, policy=ToolPolicy(built.policy.effect, requires_approval=True)
        )

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        """Run one call through the source, which reads the operation from its own reference."""
        return await self._source.execute_deferred_tool_call(call)

    async def poll_tool_task(self, poll: ToolTaskPoll) -> ToolTaskPollOutcome:
        """Poll one of the source's tool tasks."""
        return await self._source.poll_tool_task(poll)

    async def cancel_tool_task(self, cancellation: ToolTaskCancellation) -> None:
        """Cancel one of the source's abandoned tool tasks, when the source can."""
        cancel = getattr(self._source, "cancel_tool_task", None)
        if callable(cancel):
            await cancel(cancellation)

    def tool_call_step_options(self, tool_call: AgentToolCall) -> ToolCallStepOptions | None:
        """Return the source's options for one call's tool Step, or ``None`` for the defaults."""
        provide = getattr(self._source, "tool_call_step_options", None)
        options: ToolCallStepOptions | None = provide(tool_call) if callable(provide) else None
        return options

    def _agent_visible(self, tool: SourceToolDefinition) -> SourceToolDefinition:
        if tool.name not in self._reserved_tool_names:
            return tool
        if not dataclasses.is_dataclass(tool) or isinstance(tool, type):
            raise TypeError(f"cannot rename the listed tool {tool.name}: it is not a dataclass")
        renamed: SourceToolDefinition = dataclasses.replace(
            tool, name=f"{COLLIDING_CONNECTOR_TOOL_PREFIX}{tool.name}"
        )
        return renamed
