# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""ADK tools for the tools of Dex tool sources, offered only inside a Dex ``RunModel``."""

from __future__ import annotations

import contextlib
from collections.abc import Iterator
from contextvars import ContextVar
from typing import Any

from google.adk.agents.readonly_context import ReadonlyContext
from google.adk.tools import BaseTool, ToolContext
from google.adk.tools.base_toolset import BaseToolset
from google.genai import types

from dex_agents.stepwise import FrameworkToolSourceRouter, SourceToolDefinition, SourceToolListing

# The listing of the RunModel that runs now, with the router that listed it.
_ACTIVE_SOURCE_TOOLS: ContextVar[tuple[FrameworkToolSourceRouter, SourceToolListing] | None] = (
    ContextVar("dex_agents_google_adk_active_source_tools", default=None)
)


class SourceTool(BaseTool):
    """One tool of a Dex tool source, as the model sees it; ADK never runs it."""

    def __init__(self, definition: SourceToolDefinition) -> None:
        super().__init__(name=definition.name, description=definition.description)
        self._input_schema = dict(definition.input_schema)

    def _get_declaration(self) -> types.FunctionDeclaration:
        return types.FunctionDeclaration(
            name=self.name,
            description=self.description,
            parameters_json_schema=self._input_schema,
        )

    async def run_async(self, *, args: dict[str, Any], tool_context: ToolContext) -> Any:
        raise RuntimeError(f"tool source tool {self.name} runs only in its own Dex tool Step")


class SourceToolset(BaseToolset):
    """List one runner's source tools for the ``RunModel`` that this runner runs now."""

    def __init__(self, router: FrameworkToolSourceRouter) -> None:
        super().__init__()
        self._router = router

    async def get_tools(self, readonly_context: ReadonlyContext | None = None) -> list[BaseTool]:
        active = _ACTIVE_SOURCE_TOOLS.get()
        if active is None or active[0] is not self._router:
            return []
        return [SourceTool(definition) for definition in active[1].tools]

    async def close(self) -> None:
        return None


@contextlib.contextmanager
def offer_source_tools(
    router: FrameworkToolSourceRouter, listing: SourceToolListing
) -> Iterator[None]:
    """Let this runner's ``SourceToolset`` list ``listing`` while the block runs."""
    token = _ACTIVE_SOURCE_TOOLS.set((router, listing))
    try:
        yield
    finally:
        _ACTIVE_SOURCE_TOOLS.reset(token)
