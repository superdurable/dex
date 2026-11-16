# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
from typing import Any, cast

import pytest

from dex import (
    Channel,
    ConditionCombination,
    Context,
    Flow,
    GracefulCompletionConditionUnsupportedError,
    InvalidStepResultError,
    PersistenceSchema,
    Registry,
    Step,
    StepDecision,
    StepList,
    Wait,
    dead_end,
    graceful_completion_requested,
)
from dex._async_worker_dispatcher import AsyncWorkerDispatcher
from dex._server_protocol import NegotiatedServerProtocol
from dex._value_mapper import ValueMapper
from dex._worker_dispatcher import WorkerDispatcher
from dex.dexpb import dex_pb2 as pb

MESSAGES = Channel("messages", str)


class GracefulWaitStep(Step[str]):
    def wait_for(self, context: Context, input: str) -> Wait:
        if input == "one":
            return Wait.any_of(
                MESSAGES.for_one(),
                graceful_completion_requested(condition_id="graceful"),
            )
        if input == "two":
            return Wait.any_of(
                graceful_completion_requested(),
                graceful_completion_requested(),
            )
        if input == "combination":
            return Wait.any_combination_of(
                ConditionCombination.of(MESSAGES.for_one(condition_id="message")),
                ConditionCombination.of(
                    graceful_completion_requested(condition_id="graceful")
                ),
            )
        return Wait.until(MESSAGES.for_one())

    def execute(self, context: Context, input: str) -> StepDecision:
        GracefulConditionFlow.observed.append(
            context.is_graceful_completion_condition_completed()
        )
        return dead_end()


class GracefulConditionFlow(Flow[str]):
    observed: list[bool] = []

    def __init__(self) -> None:
        self.wait_step = GracefulWaitStep()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.wait_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(MESSAGES)


class PassthroughHydrator:
    @staticmethod
    def wait_for_request(
        request: pb.InvokeWaitForMethodRequest,
    ) -> pb.InvokeWaitForMethodRequest:
        return request

    @staticmethod
    def execute_request(
        request: pb.InvokeExecuteMethodRequest,
    ) -> pb.InvokeExecuteMethodRequest:
        return request


class AsyncPassthroughHydrator:
    @staticmethod
    async def wait_for_request(
        request: pb.InvokeWaitForMethodRequest,
    ) -> pb.InvokeWaitForMethodRequest:
        return request


def _server_protocol(version: int) -> NegotiatedServerProtocol:
    server_protocol = NegotiatedServerProtocol()
    server_protocol.record_version(version)
    return server_protocol


def _dispatcher(protocol_version: int) -> tuple[WorkerDispatcher, ValueMapper]:
    registry = Registry((GracefulConditionFlow(),))
    values = ValueMapper(registry.codec_registry)
    dispatcher = WorkerDispatcher(
        registry,
        values,
        cast(Any, PassthroughHydrator()),
        _server_protocol(protocol_version),
    )
    return dispatcher, values


def _wait_for_request(values: ValueMapper, input: str) -> pb.InvokeWaitForMethodRequest:
    return pb.InvokeWaitForMethodRequest(
        flow_type="GracefulConditionFlow",
        step_type="GracefulWaitStep",
        step_input=values.encode(input, values.codec(str)),
    )


def _wait_for_result(
    dispatcher: WorkerDispatcher, values: ValueMapper, input: str
) -> pb.InvokeWaitForMethodResponse:
    outputs = list(dispatcher.invoke_wait_for(_wait_for_request(values, input)))
    return outputs[-1].result


def test_graceful_completion_condition_maps_on_protocol_7() -> None:
    dispatcher, values = _dispatcher(7)

    waiting = _wait_for_result(dispatcher, values, "one").waiting_condition

    assert waiting.waiting_condition_type == pb.WAITING_CONDITION_TYPE_ANY_COMPLETED
    assert [
        condition.condition_id for condition in waiting.graceful_completion_conditions
    ] == ["graceful"]
    assert len(waiting.channel_conditions) == 1


def test_graceful_completion_condition_maps_in_any_combination() -> None:
    dispatcher, values = _dispatcher(7)

    waiting = _wait_for_result(dispatcher, values, "combination").waiting_condition

    assert [list(group.condition_ids) for group in waiting.condition_combinations] == [
        ["message"],
        ["graceful"],
    ]
    assert waiting.graceful_completion_conditions[0].condition_id == "graceful"


@pytest.mark.parametrize("input", ["one", "combination"])
def test_wait_for_fails_below_protocol_7_without_a_wait(input: str) -> None:
    dispatcher, values = _dispatcher(6)

    with pytest.raises(GracefulCompletionConditionUnsupportedError) as failure:
        _wait_for_result(dispatcher, values, input)

    assert failure.value.negotiated_server_protocol == 6
    assert failure.value.required_server_protocol == 7
    assert "server-zd 0.1.34" in str(failure.value)


def test_wait_without_graceful_completion_condition_maps_below_protocol_7() -> None:
    dispatcher, values = _dispatcher(6)

    waiting = _wait_for_result(dispatcher, values, "plain").waiting_condition

    assert len(waiting.channel_conditions) == 1
    assert not waiting.graceful_completion_conditions


def test_second_graceful_completion_condition_fails_wait_for() -> None:
    dispatcher, values = _dispatcher(7)

    with pytest.raises(
        InvalidStepResultError,
        match="a Wait accepts at most one graceful completion condition",
    ):
        _wait_for_result(dispatcher, values, "two")


def test_async_wait_for_fails_below_protocol_7() -> None:
    asyncio.run(_async_wait_for_fails_below_protocol_7())


async def _async_wait_for_fails_below_protocol_7() -> None:
    registry = Registry((GracefulConditionFlow(),), allow_async_handlers=True)
    values = ValueMapper(registry.codec_registry)
    dispatcher = AsyncWorkerDispatcher(
        registry, values, cast(Any, AsyncPassthroughHydrator()), _server_protocol(6)
    )
    with pytest.raises(GracefulCompletionConditionUnsupportedError):
        async for _ in dispatcher.invoke_wait_for(_wait_for_request(values, "one")):
            pass


@pytest.mark.parametrize(
    "status,expected",
    [
        (pb.CONDITION_STATUS_COMPLETED, True),
        (pb.CONDITION_STATUS_WAITING, False),
        (None, False),
    ],
)
def test_execute_reads_graceful_completion_result(
    status: int | None, expected: bool
) -> None:
    dispatcher, values = _dispatcher(7)
    results = pb.ConditionResults()
    if status is not None:
        results.graceful_completion_results.add(
            condition_id="graceful", condition_status=cast(Any, status)
        )
    GracefulConditionFlow.observed.clear()

    list(
        dispatcher.invoke_execute(
            pb.InvokeExecuteMethodRequest(
                flow_type="GracefulConditionFlow",
                step_type="GracefulWaitStep",
                step_input=values.encode("one", values.codec(str)),
                condition_results=results,
            )
        )
    )

    assert GracefulConditionFlow.observed == [expected]
