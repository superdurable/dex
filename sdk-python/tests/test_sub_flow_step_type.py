# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
import logging
from datetime import timedelta
from typing import Any, cast

import pytest

from dex import (
    AsyncContext,
    Context,
    Flow,
    Registry,
    Step,
    StepDecision,
    StepList,
    SubFlow,
    Timer,
    Wait,
    graceful_complete,
)
from dex._async_worker_dispatcher import AsyncWorkerDispatcher
from dex._server_protocol import NegotiatedServerProtocol
from dex._value_mapper import ValueMapper
from dex._worker_dispatcher import WorkerDispatcher
from dex.dexpb import dex_pb2 as pb

DASHED_STEP_TYPE = "b-C"


class ChildStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete()


class ChildFlow(Flow[None]):
    def __init__(self) -> None:
        self.child = ChildStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.child)


CHILD_FLOW = ChildFlow()


class StartChildStep(Step[str]):
    def __init__(self, step_type: str) -> None:
        self._step_type = step_type

    def get_step_type(self) -> str:
        return self._step_type

    def wait_for(self, context: Context, input: str) -> Wait:
        if input == "timer":
            return Wait.until(Timer.by_duration(timedelta(seconds=1)))
        return Wait.until(SubFlow.run(CHILD_FLOW, None))

    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete()


class AsyncStartChildStep(Step[str]):
    def __init__(self, step_type: str) -> None:
        self._step_type = step_type

    def get_step_type(self) -> str:
        return self._step_type

    async def wait_for(self, context: AsyncContext, input: str) -> Wait:  # type: ignore[override]
        return Wait.until(SubFlow.run(CHILD_FLOW, None))

    async def execute(self, context: AsyncContext, input: str) -> StepDecision:  # type: ignore[override]
        return graceful_complete()


class ParentFlow(Flow[str]):
    def __init__(self, step: Step[str]) -> None:
        self.start = step

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.start)


class PassthroughHydrator:
    @staticmethod
    def wait_for_request(
        request: pb.InvokeWaitForMethodRequest,
    ) -> pb.InvokeWaitForMethodRequest:
        return request


class AsyncPassthroughHydrator:
    @staticmethod
    async def wait_for_request(
        request: pb.InvokeWaitForMethodRequest,
    ) -> pb.InvokeWaitForMethodRequest:
        return request


def _server_protocol() -> NegotiatedServerProtocol:
    server_protocol = NegotiatedServerProtocol()
    server_protocol.record_version(3)
    return server_protocol


def _request(
    values: ValueMapper, step_type: str, input: str
) -> pb.InvokeWaitForMethodRequest:
    return pb.InvokeWaitForMethodRequest(
        flow_type="ParentFlow",
        step_type=step_type,
        step_input=values.encode(input, values.codec(str)),
    )


def _wait_for(step_type: str, input: str) -> pb.InvokeWaitForMethodResponse:
    registry = Registry((ParentFlow(StartChildStep(step_type)), CHILD_FLOW))
    values = ValueMapper(registry.codec_registry)
    dispatcher = WorkerDispatcher(
        registry, values, cast(Any, PassthroughHydrator()), _server_protocol()
    )
    outputs = list(dispatcher.invoke_wait_for(_request(values, step_type, input)))
    return outputs[-1].result


async def _async_wait_for(step_type: str, input: str) -> pb.InvokeWaitForMethodResponse:
    registry = Registry(
        (ParentFlow(AsyncStartChildStep(step_type)), CHILD_FLOW),
        allow_async_handlers=True,
    )
    values = ValueMapper(registry.codec_registry)
    dispatcher = AsyncWorkerDispatcher(
        registry, values, cast(Any, AsyncPassthroughHydrator()), _server_protocol()
    )
    outputs = [
        output
        async for output in dispatcher.invoke_wait_for(
            _request(values, step_type, input)
        )
    ]
    return outputs[-1].result


def test_dashed_step_type_sends_sub_flow_wait_unchanged(
    caplog: pytest.LogCaptureFixture,
) -> None:
    with caplog.at_level(logging.WARNING, logger="dex._worker_dispatcher"):
        response = _wait_for(DASHED_STEP_TYPE, "sub-flow")
    sub_flows = response.waiting_condition.sub_flow_conditions
    assert [sub_flow.sub_flow_type for sub_flow in sub_flows] == ["ChildFlow"]
    assert any(
        'Step type "b-C" contains "-"' in record.getMessage()
        for record in caplog.records
    )


def test_dashed_step_type_can_wait_without_sub_flows(
    caplog: pytest.LogCaptureFixture,
) -> None:
    with caplog.at_level(logging.WARNING, logger="dex._worker_dispatcher"):
        response = _wait_for(DASHED_STEP_TYPE, "timer")
    assert len(response.waiting_condition.timer_conditions) == 1
    assert not response.waiting_condition.sub_flow_conditions
    assert not caplog.records


def test_dash_free_step_type_starts_sub_flows() -> None:
    response = _wait_for("StartChild", "sub-flow")
    sub_flows = response.waiting_condition.sub_flow_conditions
    assert [sub_flow.sub_flow_type for sub_flow in sub_flows] == ["ChildFlow"]


def test_async_dashed_step_type_sends_sub_flow_wait_unchanged() -> None:
    response = asyncio.run(_async_wait_for(DASHED_STEP_TYPE, "sub-flow"))
    sub_flows = response.waiting_condition.sub_flow_conditions
    assert [sub_flow.sub_flow_type for sub_flow in sub_flows] == ["ChildFlow"]
