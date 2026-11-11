# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""FrameworkToolSourceRouter without an engine: collisions, listing, envelope, and routing."""

from __future__ import annotations

import json
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from datetime import timedelta
from typing import Any, cast

import pytest

from dex_agents import (
    AgentEventWriter,
    AgentToolCall,
    AgentTurnContext,
    DeferredToolCall,
    DeferredToolExecution,
    ToolCallStatus,
    ToolCallStepOptions,
    ToolEffect,
    ToolExecutionResult,
    ToolPolicy,
)
from dex_agents.builtin_loop import FrameworkToolSourceRouter


@dataclass(frozen=True)
class _Definition:
    name: str
    description: str
    input_schema: Mapping[str, Any]


class _FakeSource:
    def __init__(
        self,
        source_name: str,
        names: Sequence[str],
        *,
        declares: bool = True,
        is_down: bool = False,
    ) -> None:
        self.source_name = source_name
        self.listed = [_Definition(name, f"{name} tool", {"type": "object"}) for name in names]
        self._declares = declares
        self._is_down = is_down
        self.executions: list[DeferredToolExecution] = []

    async def list_tools(self) -> Sequence[_Definition]:
        if self._is_down:
            raise ConnectionError("down")
        return self.listed

    def cached_tools(self) -> Sequence[_Definition]:
        return [_Definition("vanished", "gone from the live listing", {"type": "object"})]

    def declared_tool_names(self) -> frozenset[str]:
        return frozenset(tool.name for tool in self.listed) if self._declares else frozenset()

    def deferred_tool_call(
        self,
        turn: AgentTurnContext,
        *,
        tool_call_id: str,
        tool_name: str,
        arguments: str,
        listed_tool: Any,
    ) -> DeferredToolCall:
        return DeferredToolCall(
            tool_call_id=tool_call_id,
            tool_name=tool_name,
            arguments=arguments,
            policy=ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False),
            tool_source_reference=f"own-{tool_name}",
            ordered=True,
        )

    def tool_call_step_options(self, tool_call: AgentToolCall) -> ToolCallStepOptions:
        assert tool_call.tool_source_reference == f"own-{tool_call.tool_name}"
        return ToolCallStepOptions(execute_method_timeout=timedelta(seconds=7))

    async def execute_deferred_tool_call(self, call: DeferredToolExecution) -> ToolExecutionResult:
        self.executions.append(call)
        return ToolExecutionResult(output="ran")

    async def poll_tool_task(self, poll: Any) -> Any:
        raise LookupError("no tasks")


def test_declared_names_that_repeat_a_framework_tool_are_refused() -> None:
    with pytest.raises(ValueError, match="uses the name of a framework tool"):
        FrameworkToolSourceRouter([_FakeSource("a", ["lookup"])], reserved_tool_names={"lookup"})


def test_declared_names_that_repeat_another_source_are_refused() -> None:
    with pytest.raises(ValueError, match="two tool sources list a tool named pay"):
        FrameworkToolSourceRouter([_FakeSource("a", ["pay"]), _FakeSource("b", ["pay"])])


def test_two_sources_with_one_source_name_are_refused() -> None:
    with pytest.raises(ValueError, match="share a source_name"):
        FrameworkToolSourceRouter([_FakeSource("a", ["x"]), _FakeSource("a", ["y"])])


def test_a_source_without_a_tool_source_method_is_refused() -> None:
    with pytest.raises(TypeError, match="has no list_tools method"):
        FrameworkToolSourceRouter([cast(Any, object())])


async def test_listed_names_that_collide_at_run_time_are_refused() -> None:
    router = FrameworkToolSourceRouter([_FakeSource("a", ["late"], declares=False)])
    with pytest.raises(ValueError, match="uses the name of a framework tool"):
        await router.list_source_tools(_turn(), reserved_tool_names={"late"})


async def test_a_down_source_offers_no_tools() -> None:
    router = FrameworkToolSourceRouter(
        [_FakeSource("a", ["pay"], is_down=True), _FakeSource("b", ["read"])]
    )
    listing = await router.list_source_tools(_turn())
    assert [tool.name for tool in listing.tools] == ["read"]


async def test_the_source_builds_the_call_and_the_router_routes_it_back() -> None:
    source = _FakeSource("ledger", ["pay"])
    router = FrameworkToolSourceRouter([source])
    listing = await router.list_source_tools(_turn())
    call = listing.deferred_tool_call(
        _turn(), tool_call_id="c1", tool_name="pay", arguments='{"amount": 1}'
    )
    assert call is not None
    assert call.ordered
    assert call.policy == ToolPolicy(ToolEffect.NON_IDEMPOTENT_WRITE, requires_approval=False)
    assert json.loads(call.tool_source_reference or "") == {
        "dex_tool_source": "ledger",
        "reference": "own-pay",
    }
    unlisted = listing.deferred_tool_call(_turn(), tool_call_id="c2", tool_name="x", arguments="")
    assert unlisted is None
    record = _record("pay", call.tool_source_reference)
    assert router.is_source_tool_call(record)
    assert router.tool_call_step_options(record) == ToolCallStepOptions(
        execute_method_timeout=timedelta(seconds=7)
    )
    execution = DeferredToolExecution(
        flow_id="session-1",
        tool_call=record,
        idempotency_key="session-1/turn-1/c1",
        attempt=2,
        events=cast(AgentEventWriter, None),
        heartbeat=_heartbeat,
        heartbeat_checkpoint="marker",
    )
    assert await router.execute_deferred_tool_call(execution) == ToolExecutionResult(output="ran")
    [received] = source.executions
    assert received.tool_call.tool_source_reference == "own-pay"
    assert (received.attempt, received.heartbeat_checkpoint) == (2, "marker")


async def test_a_call_of_an_unconfigured_source_raises_lookup_error() -> None:
    router = FrameworkToolSourceRouter([_FakeSource("ledger", ["pay"])])
    record = _record("pay", json.dumps({"dex_tool_source": "gone", "reference": None}))
    with pytest.raises(LookupError, match="not configured on this Worker"):
        router.tool_call_step_options(record)
    assert not router.is_source_tool_call(_record("lookup", None))
    assert router.tool_call_step_options(_record("lookup", None)) is None


def test_cached_source_tools_read_each_cache_and_refuse_collisions() -> None:
    router = FrameworkToolSourceRouter([_FakeSource("ledger", ["pay"])])
    assert [tool.name for tool in router.cached_source_tools()] == ["vanished"]
    colliding = FrameworkToolSourceRouter(
        [_FakeSource("a", [], declares=False), _FakeSource("b", [], declares=False)]
    )
    with pytest.raises(ValueError, match="two tool sources list a tool named vanished"):
        colliding.cached_source_tools()


async def test_a_recorded_tool_missing_from_the_live_listing_comes_from_the_cache() -> None:
    router = FrameworkToolSourceRouter([_FakeSource("ledger", ["pay"])])
    reference = json.dumps({"dex_tool_source": "ledger", "reference": "own-vanished"})
    turn = _turn([_record("vanished", reference)])
    listing = await router.list_source_tools(turn)
    assert [tool.name for tool in listing.tools] == ["pay"]
    assert [tool.name for tool in listing.replay_only_tools] == ["vanished"]
    assert listing.is_replay_only("vanished") and not listing.has_tool("vanished")
    # A new call of the vanished tool never becomes a deferred call for its source.
    with pytest.raises(ValueError, match="offered only to replay"):
        listing.deferred_tool_call(turn, tool_call_id="c9", tool_name="vanished", arguments="{}")
    assert listing.not_offered_result("vanished") == (
        "vanished did not run: its tool source does not offer this tool now."
    )


async def test_a_new_call_under_a_recorded_call_id_is_refused() -> None:
    router = FrameworkToolSourceRouter([_FakeSource("ledger", ["pay"])])
    reference = json.dumps({"dex_tool_source": "ledger", "reference": "own-pay"})
    turn = _turn([_record("pay", reference)])
    listing = await router.list_source_tools(turn)
    with pytest.raises(ValueError, match="already recorded"):
        listing.deferred_tool_call(turn, tool_call_id="c1", tool_name="pay", arguments="{}")


async def _heartbeat() -> None:
    return None


def _record(tool_name: str, tool_source_reference: str | None) -> AgentToolCall:
    return AgentToolCall(
        tool_call_id="c1",
        turn_id="turn-1",
        tool_name=tool_name,
        arguments="{}",
        effect=ToolEffect.NON_IDEMPOTENT_WRITE,
        requires_approval=False,
        status=ToolCallStatus.SUCCEEDED,
        deferral_order=0,
        tool_source_reference=tool_source_reference,
    )


def _turn(records: Sequence[AgentToolCall] = ()) -> AgentTurnContext:
    return AgentTurnContext(
        flow_id="session-1",
        turn_id="turn-1",
        attempt=1,
        messages=(),
        framework_run_state=None,
        tool_calls={record.tool_call_id: record for record in records},
        tool_policies={},
        default_tool_effect=ToolEffect.UNCLASSIFIED,
        events=cast(AgentEventWriter, None),
        heartbeat=_heartbeat,
    )
