# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Serve ``ToolSource`` tools from a framework adapter's runner."""

from __future__ import annotations

import asyncio
import json
import logging
from collections.abc import Collection, Mapping, Sequence
from dataclasses import dataclass, replace
from datetime import timedelta
from typing import Any

from dex_agents.runner import (
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    DeferredToolOutcome,
    ToolCallStepOptions,
)
from dex_agents.stepwise.runner import DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT
from dex_agents.stepwise.tool_sources import SourceToolDefinition, ToolSource
from dex_agents.values import AgentToolCall

#: Key of the source name in the envelope that a ``FrameworkToolSourceRouter`` stores.
FRAMEWORK_TOOL_SOURCE_ENVELOPE_KEY = "dex_tool_source"

#: What the model reads for a new call of a tool that its source no longer lists; ``format`` it.
SOURCE_TOOL_NOT_OFFERED_RESULT = (
    "{tool_name} did not run: its tool source does not offer this tool now."
)

_TOOL_SOURCE_REFERENCE_KEY = "reference"
_TOOL_SOURCE_METHODS = (
    "list_tools",
    "deferred_tool_call",
    "execute_deferred_tool_call",
    "poll_tool_task",
)
_LOGGER = logging.getLogger(__name__)


@dataclass(frozen=True)
class _ListedSourceTool:
    source: ToolSource
    definition: SourceToolDefinition


class SourceToolListing:
    """Hold the source tools that one model call offers, and build their deferred calls.

    ``FrameworkToolSourceRouter.list_source_tools`` returns it. Offer
    ``tools`` to the model as the framework's native tools, whose bodies
    never run, and build every new call of one of them with
    ``deferred_tool_call``.

    A tool that this turn already called but that the live listing lacks is
    in ``replay_only_tools``. Offer it only where the framework needs the
    tool to resume with the recorded result. A new call of it must not run:
    answer it inside the framework with ``not_offered_result``, so it never
    reaches a tool Step under a new Call ID.

    .. versionadded:: 0.4.0
    """

    def __init__(
        self,
        listed_tools: Mapping[str, _ListedSourceTool],
        replay_only_tools: Mapping[str, _ListedSourceTool],
        envelope_key: str,
    ) -> None:
        """Create a listing; ``FrameworkToolSourceRouter.list_source_tools`` does this.

        Args:
            listed_tools: Each tool of the live listing by name, with its source.
            replay_only_tools: Each tool this turn called that the live listing
                lacks, from its source's ``cached_tools()``, by name.
            envelope_key: Envelope key of the router that built the listing.
        """
        self._listed_tools = dict(listed_tools)
        self._replay_only_tools = dict(replay_only_tools)
        self._envelope_key = envelope_key

    @property
    def tools(self) -> tuple[SourceToolDefinition, ...]:
        """Return the tools a new call may use, in source order and then in each source's order.

        Returns:
            Each tool's name, description, and JSON Schema of its arguments.
        """
        return tuple(listed.definition for listed in self._listed_tools.values())

    @property
    def replay_only_tools(self) -> tuple[SourceToolDefinition, ...]:
        """Return the tools this turn called that the live listing lacks.

        Offer them only when the framework must find the tool to resume a
        recorded call, as ADK and the OpenAI Agents SDK do.

        Returns:
            Each tool's definition from its source's ``cached_tools()``.
        """
        return tuple(listed.definition for listed in self._replay_only_tools.values())

    def has_tool(self, tool_name: str) -> bool:
        """Report whether a new call of a tool name goes to a source.

        Args:
            tool_name: The tool name the model called.

        Returns:
            ``True`` when the live listing has the tool.
        """
        return tool_name in self._listed_tools

    def is_replay_only(self, tool_name: str) -> bool:
        """Report whether a tool name is offered only to replay a recorded call.

        Args:
            tool_name: The tool name the model called.

        Returns:
            ``True`` when the tool is in ``replay_only_tools``. A new call of it
            gets ``not_offered_result`` and never runs.
        """
        return tool_name in self._replay_only_tools

    def not_offered_result(self, tool_name: str) -> str:
        """Return the text the model reads for a new call of a replay-only tool.

        Args:
            tool_name: The tool name the model called.

        Returns:
            ``SOURCE_TOOL_NOT_OFFERED_RESULT`` for the tool.
        """
        return SOURCE_TOOL_NOT_OFFERED_RESULT.format(tool_name=tool_name)

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
    ) -> DeferredToolCall | None:
        """Build the ``DeferredToolCall`` of one new model call of a listed tool.

        The source builds the call, so its effect class, approval, ordering,
        and ``tool_source_reference`` come from the source. A session
        ``tool_policies`` entry reaches the source through ``turn``; for a
        ``ConnectorToolSource`` it changes only the approval. The router
        stores the source's name with the reference, so the tool Step finds
        the same source on any Worker.

        Args:
            turn: The ``RunModel`` context.
            tool_call_id: Framework tool call ID. It must not be a call ID this
                turn already recorded.
            tool_name: The tool name the model called.
            arguments: Tool arguments as JSON text.

        Returns:
            The source's call, or ``None`` when no source lists the tool, so
            the adapter builds the call itself.

        Raises:
            ValueError: If the tool is replay-only, or this turn already
                recorded ``tool_call_id``. A new call under either must never
                reach a tool Step; answer it inside the framework instead.
        """
        if tool_name in self._replay_only_tools:
            raise ValueError(f"tool {tool_name} is offered only to replay its recorded calls")
        listed = self._listed_tools.get(tool_name)
        if listed is None:
            return None
        if turn.recorded_tool_call(tool_call_id) is not None:
            raise ValueError(f"tool call ID {tool_call_id} is already recorded in this turn")
        built = listed.source.deferred_tool_call(
            turn,
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            listed_tool=listed.definition,
        )
        envelope = {
            self._envelope_key: listed.source.source_name,
            _TOOL_SOURCE_REFERENCE_KEY: built.tool_source_reference,
        }
        return replace(built, tool_source_reference=json.dumps(envelope))


class FrameworkToolSourceRouter:
    """Serve ``ToolSource`` tools, such as ``ConnectorToolSource``, from a framework adapter.

    A framework adapter's runner owns one router for the sources the
    application passes as ``tool_sources``. The router does the work that is
    the same for every framework:

    * ``list_source_tools`` lists every source in each ``RunModel``. The
      adapter offers the listed tools to the model as native tools whose
      bodies never run.
    * ``SourceToolListing.deferred_tool_call`` builds the call of a listed
      tool through its source, so the effect class, approval, and write
      ordering come from the source, never from the framework.
    * ``execute_deferred_tool_call`` runs a call that a source built in its
      own tool Step, through that source, with the full
      ``DeferredToolExecution``: the Step context, the heartbeat checkpoint,
      and the attempt.
    * ``tool_call_step_options`` gives the call's tool Step the source's
      timeout and retry policy.

    Name collisions are refused. The constructor checks each source's
    ``declared_tool_names`` against ``reserved_tool_names`` and the other
    sources. ``list_source_tools`` checks the listed names the same way.

    The router keeps no session state, so one router serves every session of
    the runner, also concurrently.

    .. versionadded:: 0.4.0

    Example:
        >>> class MyRunner:
        ...     def __init__(self, agent, tool_sources):
        ...         self._sources = FrameworkToolSourceRouter(
        ...             tool_sources, reserved_tool_names=agent.tool_names)
        ...
        ...     async def run_model_step(self, turn):
        ...         listing = await self._sources.list_source_tools(turn)
        ...         tools = [native_tool(definition) for definition in listing.tools]
        ...         calls = run_framework_once(tools)
        ...         return ToolCallsRequested(framework_run_state=state, tool_calls=[
        ...             listing.deferred_tool_call(turn, tool_call_id=call.id,
        ...                                        tool_name=call.name, arguments=call.arguments)
        ...             or framework_deferred_call(turn, call)
        ...             for call in calls])
        ...
        ...     async def execute_deferred_tool_call(self, call):
        ...         if self._sources.is_source_tool_call(call.tool_call):
        ...             return await self._sources.execute_deferred_tool_call(call)
        ...         return await run_framework_tool(call)
        ...
        ...     def tool_call_step_options(self, tool_call):
        ...         return self._sources.tool_call_step_options(tool_call)
    """

    def __init__(
        self,
        tool_sources: Sequence[ToolSource],
        *,
        reserved_tool_names: Collection[str] = (),
        listing_timeout: timedelta = DEFAULT_TOOL_SOURCE_LISTING_TIMEOUT,
        envelope_key: str = FRAMEWORK_TOOL_SOURCE_ENVELOPE_KEY,
    ) -> None:
        """Check the sources and their declared tool names.

        Args:
            tool_sources: The sources, in the order their tools are offered.
            reserved_tool_names: Names of the framework's own tools, which no
                source tool may use. Pass every name the adapter knows when it
                is built; ``list_source_tools`` takes names known later.
            listing_timeout: How long one ``list_source_tools`` waits for each
                source. A source that fails or takes longer offers no tools in
                that model call. Defaults to 10 seconds.
            envelope_key: Key of the source name in the envelope stored as
                ``AgentToolCall.tool_source_reference``. Stored records depend
                on it, so keep the default.

        Raises:
            TypeError: If a source lacks a ``ToolSource`` method or a non-blank
                ``source_name``.
            ValueError: If two sources share a ``source_name``,
                ``listing_timeout`` is not positive, or a source's
                ``declared_tool_names`` repeat a reserved name or another
                source's tool.
        """
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
        self._reserved_tool_names = frozenset(reserved_tool_names)
        self._listing_timeout = listing_timeout
        self._envelope_key = envelope_key
        declared: set[str] = set()
        for source in self._tool_sources:
            names = sorted(_declared_tool_names(source))
            for tool_name in names:
                _refuse_tool_name(tool_name, self._reserved_tool_names, declared)
            declared.update(names)
        self._declared_tool_names = frozenset(declared)

    @property
    def tool_sources(self) -> tuple[ToolSource, ...]:
        """Return the sources, in the order their tools are offered.

        Returns:
            The sources passed to the constructor.
        """
        return self._tool_sources

    @property
    def declared_tool_names(self) -> frozenset[str]:
        """Return every tool name the sources declare they always list.

        Use it to accept a session ``tool_policies`` entry for a source tool,
        such as one that changes a connector tool's approval.

        Returns:
            The union of each source's ``declared_tool_names()``, or the empty
            set for sources without that method.
        """
        return self._declared_tool_names

    def cached_source_tools(self) -> tuple[SourceToolDefinition, ...]:
        """Return each source's ``cached_tools()``, without I/O.

        Use it where a framework fixes its tools before any model call, such
        as when an adapter copies the agent at construction. A source without
        ``cached_tools`` contributes nothing. ``ConnectorToolSource`` returns
        its whole, fixed listing.

        Returns:
            The cached tools, in source order and then in each source's order.

        Raises:
            ValueError: If a cached name repeats a reserved name or another
                source's tool.
        """
        cached: dict[str, SourceToolDefinition] = {}
        for source in self._tool_sources:
            read_cache = getattr(source, "cached_tools", None)
            for definition in read_cache() if callable(read_cache) else ():
                _refuse_tool_name(definition.name, self._reserved_tool_names, cached)
                cached[definition.name] = definition
        return tuple(cached.values())

    async def list_source_tools(
        self,
        turn: AgentTurnContext,
        *,
        reserved_tool_names: Collection[str] = (),
    ) -> SourceToolListing:
        """List every source for one model call.

        Each source's ``list_tools`` gets at most the listing timeout. A
        source whose listing raises or takes longer offers no tools in this
        call, so a down source never fails a turn. A tool that this turn
        already called through a source but that the live listing lacks goes
        to ``replay_only_tools``, from that source's ``cached_tools()`` when it
        has one, so the framework still finds the tool when it resumes with
        the recorded result. A new call of such a tool is refused.

        Args:
            turn: The ``RunModel`` context.
            reserved_tool_names: More framework tool names, known only now,
                that no source tool may use.

        Returns:
            The tools to offer in this model call.

        Raises:
            ValueError: If a listed name repeats a reserved name or another
                source's tool. No model call should be made.
        """
        reserved = self._reserved_tool_names | frozenset(reserved_tool_names)
        listed: dict[str, _ListedSourceTool] = {}
        for index, source in enumerate(self._tool_sources):
            for definition in await self._list_one_source(index, source):
                _refuse_tool_name(definition.name, reserved, listed)
                listed[definition.name] = _ListedSourceTool(source, definition)
        replay_only: dict[str, _ListedSourceTool] = {}
        for record in turn.tool_calls.values():
            if record.tool_name in listed or record.tool_name in replay_only:
                continue
            envelope = self._read_envelope(record.tool_source_reference)
            recorded_source = None if envelope is None else self._sources_by_name.get(envelope[0])
            cached = (
                None if recorded_source is None else _cached_tool(recorded_source, record.tool_name)
            )
            if recorded_source is not None and cached is not None:
                replay_only[cached.name] = _ListedSourceTool(recorded_source, cached)
        return SourceToolListing(listed, replay_only, self._envelope_key)

    def is_source_tool_call(self, tool_call: AgentToolCall) -> bool:
        """Report whether a source built a call, so the router runs it.

        Args:
            tool_call: A record of this session.

        Returns:
            ``True`` when the record carries this router's envelope.
        """
        return self._read_envelope(tool_call.tool_source_reference) is not None

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> DeferredToolOutcome:
        """Run one call that a source built, in its tool Step, through that source.

        The source receives the whole ``DeferredToolExecution``, with its own
        ``tool_source_reference`` in place of the envelope, so it keeps the
        Step context, the heartbeat checkpoint, and the attempt.

        Args:
            call: The approved call.

        Returns:
            The source's outcome.

        Raises:
            LookupError: If no source built the call, or the source that
                built it is not configured on this Worker. The call's effect
                class then decides its outcome.
            ToolExecutionError: The source raised it; the model sees it.
            ToolOutcomeUnknownError: The source cannot tell whether the call
                took effect.
            Exception: What the source raises otherwise, such as a retry.
        """
        source, reference = self._route(call.tool_call)
        return await source.execute_deferred_tool_call(
            replace(call, tool_call=replace(call.tool_call, tool_source_reference=reference))
        )

    def tool_call_step_options(self, tool_call: AgentToolCall) -> ToolCallStepOptions | None:
        """Return the source's options for one call's tool Step.

        A source that defines ``tool_call_step_options(tool_call)`` gives its
        calls their own timeout and retry policy, as ``ConnectorToolSource``
        does from each operation's manifest.

        Args:
            tool_call: The record whose tool Step starts.

        Returns:
            The source's options, or ``None`` for the defaults, also for a
            call that no source built.

        Raises:
            LookupError: If a source built the call but is not configured on
                this Worker. The session then uses the defaults.
        """
        if not self.is_source_tool_call(tool_call):
            return None
        source, reference = self._route(tool_call)
        provide = getattr(source, "tool_call_step_options", None)
        if provide is None:
            return None
        options = provide(replace(tool_call, tool_source_reference=reference))
        return options if isinstance(options, ToolCallStepOptions) else None

    def _route(self, tool_call: AgentToolCall) -> tuple[ToolSource, str | None]:
        envelope = self._read_envelope(tool_call.tool_source_reference)
        if envelope is None:
            raise LookupError(f"no tool source built the call of tool {tool_call.tool_name}")
        source_name, reference = envelope
        source = self._sources_by_name.get(source_name)
        if source is None:
            raise LookupError(
                f"tool source {source_name}, which built the call of tool "
                f"{tool_call.tool_name}, is not configured on this Worker"
            )
        return source, reference

    def _read_envelope(self, tool_source_reference: str | None) -> tuple[str, str | None] | None:
        if tool_source_reference is None:
            return None
        try:
            envelope: Any = json.loads(tool_source_reference)
        except ValueError:
            return None
        if not isinstance(envelope, Mapping):
            return None
        source_name = envelope.get(self._envelope_key)
        reference = envelope.get(_TOOL_SOURCE_REFERENCE_KEY)
        if not isinstance(source_name, str) or (
            reference is not None and not isinstance(reference, str)
        ):
            return None
        return source_name, reference

    async def _list_one_source(
        self, index: int, source: ToolSource
    ) -> tuple[SourceToolDefinition, ...]:
        listing = asyncio.ensure_future(source.list_tools())
        # A listing that outlives this call keeps running; nobody else reads its error.
        listing.add_done_callback(_consume_exception)
        timeout = self._listing_timeout.total_seconds()
        try:
            return tuple(await asyncio.wait_for(asyncio.shield(listing), timeout))
        except TimeoutError:
            _LOGGER.warning("tool source %d listed nothing within %ss", index, timeout)
        except Exception as error:
            _LOGGER.warning("tool source %d could not list its tools: %s", index, error)
        return ()


def _declared_tool_names(source: ToolSource) -> Collection[str]:
    read_names = getattr(source, "declared_tool_names", None)
    return read_names() if callable(read_names) else ()


def _cached_tool(source: ToolSource, tool_name: str) -> SourceToolDefinition | None:
    read_cache = getattr(source, "cached_tools", None)
    if not callable(read_cache):
        return None
    return next((tool for tool in read_cache() if tool.name == tool_name), None)


def _refuse_tool_name(
    tool_name: str,
    reserved_tool_names: Collection[str],
    other_source_tool_names: Collection[str],
) -> None:
    if tool_name in reserved_tool_names:
        raise ValueError(f"tool {tool_name} of a tool source uses the name of a framework tool")
    if tool_name in other_source_tool_names:
        raise ValueError(f"two tool sources list a tool named {tool_name}")


def _consume_exception(listing: asyncio.Future[Any]) -> None:
    if not listing.cancelled():
        listing.exception()
