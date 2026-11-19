# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Stand-in ADK tools that let a resumed invocation replay calls whose tool a deploy removed."""

from __future__ import annotations

import contextlib
from collections.abc import Collection, Iterator
from contextvars import ContextVar
from typing import Any

from google.adk.agents import BaseAgent, LlmAgent
from google.adk.agents.readonly_context import ReadonlyContext
from google.adk.tools import BaseTool, ToolContext
from google.adk.tools.base_toolset import BaseToolset

#: Model-visible result of a new call to a stand-in; the model is never offered one.
PAUSED_CALL_TOOL_NOT_OFFERED_RESULT = (
    "Tool {tool_name} is not available now; it only answers a call made before a deploy."
)

# The paused calls' tool names of the RunModel that runs now, with the runner's toolset.
_ACTIVE_PAUSED_CALL_TOOLS: ContextVar[tuple[PausedCallToolset, frozenset[str]] | None] = ContextVar(
    "dex_agents_google_adk_active_paused_call_tools", default=None
)


class PausedCallTool(BaseTool):
    """Keep a paused call's tool name known to ADK; undeclared, so the model never sees it."""

    def __init__(self, name: str) -> None:
        super().__init__(name=name, description="")

    async def run_async(self, *, args: dict[str, Any], tool_context: ToolContext) -> Any:
        raise RuntimeError(f"tool {self.name} only replays a recorded call")


class PausedCallToolset(BaseToolset):
    """List a stand-in per paused call's tool, only inside this runner's resuming ``RunModel``."""

    async def get_tools(self, readonly_context: ReadonlyContext | None = None) -> list[BaseTool]:
        active = _ACTIVE_PAUSED_CALL_TOOLS.get()
        if active is None or active[0] is not self:
            return []
        return [PausedCallTool(tool_name) for tool_name in sorted(active[1])]

    async def close(self) -> None:
        return None


def install_paused_call_toolset(root_agent: BaseAgent) -> PausedCallToolset:
    """Put one ``PausedCallToolset`` first in each ``LlmAgent``: real tools replace stand-ins."""
    toolset = PausedCallToolset()
    for agent in _llm_agents(root_agent):
        agent.tools = [toolset, *agent.tools]
    return toolset


@contextlib.contextmanager
def offer_paused_call_tools(
    toolset: PausedCallToolset, tool_names: Collection[str]
) -> Iterator[None]:
    """Let ``toolset`` list stand-ins named ``tool_names`` while the block runs."""
    token = _ACTIVE_PAUSED_CALL_TOOLS.set((toolset, frozenset(tool_names)))
    try:
        yield
    finally:
        _ACTIVE_PAUSED_CALL_TOOLS.reset(token)


def _llm_agents(agent: BaseAgent) -> Iterator[LlmAgent]:
    if isinstance(agent, LlmAgent):
        yield agent
    for sub_agent in agent.sub_agents:
        yield from _llm_agents(sub_agent)
