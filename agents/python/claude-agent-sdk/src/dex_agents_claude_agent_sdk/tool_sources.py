# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Tool sources served by the custom tool server, and routing of their stored calls."""

from __future__ import annotations

import asyncio
import json
import logging
from collections.abc import Collection, Mapping, Sequence
from dataclasses import dataclass, replace
from datetime import timedelta
from typing import Any

from dex_agents import AgentToolCall, AgentTurnContext, DeferredToolCall
from dex_agents.stepwise import SourceToolDefinition, ToolSource

_LOGGER = logging.getLogger(__name__)
_TOOL_SOURCE_METHODS = (
    "list_tools",
    "deferred_tool_call",
    "execute_deferred_tool_call",
    "poll_tool_task",
)
# The adapter's envelope around a source's tool_source_reference: the building source's name.
_TOOL_SOURCE_NAME_KEY = "claude_tool_source"
_TOOL_SOURCE_REFERENCE_KEY = "reference"


@dataclass(frozen=True)
class ListedSourceTool:
    """One tool that a source listed for this turn segment."""

    source: ToolSource
    definition: SourceToolDefinition

    def build_deferred_tool_call(
        self, turn: AgentTurnContext, tool_call_id: str, arguments: str
    ) -> DeferredToolCall:
        built = self.source.deferred_tool_call(
            turn,
            tool_call_id=tool_call_id,
            tool_name=self.definition.name,
            arguments=arguments,
            listed_tool=self.definition,
        )
        envelope = {
            _TOOL_SOURCE_NAME_KEY: self.source.source_name,
            _TOOL_SOURCE_REFERENCE_KEY: built.tool_source_reference,
        }
        return replace(built, tool_source_reference=json.dumps(envelope))


class ToolSourceRouter:
    def __init__(
        self,
        tool_sources: Sequence[ToolSource],
        listing_timeout: timedelta,
        reserved_tool_names: Collection[str],
    ) -> None:
        for source in tool_sources:
            missing = [
                name for name in _TOOL_SOURCE_METHODS if not callable(getattr(source, name, None))
            ]
            if missing:
                raise TypeError(f"tool source {source!r} has no {missing[0]} method")
            source_name = getattr(source, "source_name", None)
            if not isinstance(source_name, str) or not source_name.strip():
                raise TypeError(f"tool source {source!r} needs a non-blank source_name")
        self._sources_by_name = {source.source_name: source for source in tool_sources}
        if len(self._sources_by_name) != len(tool_sources):
            raise ValueError("two tool sources share a source_name")
        if listing_timeout <= timedelta(0):
            raise ValueError("tool_source_listing_timeout must be positive")
        self._tool_sources = tuple(tool_sources)
        self._listing_timeout = listing_timeout
        self._reserved_tool_names = frozenset(reserved_tool_names)
        declared: set[str] = set()
        for source in self._tool_sources:
            read_names = getattr(source, "declared_tool_names", None)
            names = sorted(read_names()) if callable(read_names) else []
            for tool_name in names:
                self._refuse_tool_name(tool_name, declared)
            declared.update(names)

    async def list_source_tools(self) -> dict[str, ListedSourceTool]:
        listed: dict[str, ListedSourceTool] = {}
        for index, source in enumerate(self._tool_sources):
            for definition in await self._list_one_source(index, source):
                self._refuse_tool_name(definition.name, listed)
                listed[definition.name] = ListedSourceTool(source, definition)
        return listed

    def route(self, record: AgentToolCall) -> tuple[ToolSource, str | None] | None:
        envelope = _read_tool_source_envelope(record.tool_source_reference)
        if envelope is None:
            return None
        source_name, reference = envelope
        source = self._sources_by_name.get(source_name)
        if source is None:
            raise LookupError(
                f"tool source {source_name}, which built the call of tool "
                f"{record.tool_name}, is not configured on this Worker"
            )
        return source, reference

    async def _list_one_source(
        self, index: int, source: ToolSource
    ) -> tuple[SourceToolDefinition, ...]:
        listing = asyncio.ensure_future(source.list_tools())
        # A listing that outlives this segment keeps running; nobody else reads its error.
        listing.add_done_callback(_consume_exception)
        timeout = self._listing_timeout.total_seconds()
        try:
            return tuple(await asyncio.wait_for(asyncio.shield(listing), timeout))
        except TimeoutError:
            _LOGGER.warning("tool source %d listed nothing within %ss", index, timeout)
        except Exception as error:
            _LOGGER.warning("tool source %d could not list its tools: %s", index, error)
        return ()

    def _refuse_tool_name(self, tool_name: str, other_source_tool_names: Collection[str]) -> None:
        if tool_name in self._reserved_tool_names:
            raise ValueError(
                f"tool {tool_name} of a tool source uses the name of a custom tool "
                "or a Claude Code tool"
            )
        if tool_name in other_source_tool_names:
            raise ValueError(f"two tool sources list a tool named {tool_name}")


def _read_tool_source_envelope(tool_source_reference: str | None) -> tuple[str, str | None] | None:
    if tool_source_reference is None:
        return None
    try:
        envelope: Any = json.loads(tool_source_reference)
    except ValueError:
        return None
    if not isinstance(envelope, Mapping):
        return None
    source_name = envelope.get(_TOOL_SOURCE_NAME_KEY)
    reference = envelope.get(_TOOL_SOURCE_REFERENCE_KEY)
    if not isinstance(source_name, str) or (
        reference is not None and not isinstance(reference, str)
    ):
        return None
    return source_name, reference


def _consume_exception(listing: asyncio.Future[Any]) -> None:
    if not listing.cancelled():
        listing.exception()
