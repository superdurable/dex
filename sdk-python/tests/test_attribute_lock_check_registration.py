# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from typing import Any, cast

from dex import (
    Attribute,
    Context,
    Flow,
    PersistenceSchema,
    Registry,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    graceful_complete,
)
from dex._attribute_lock_check import UnlockedAttributeReadModifyWriteCheck
from dex._server_protocol import NegotiatedServerProtocol
from dex._value_mapper import ValueMapper
from dex._worker_dispatcher import WorkerDispatcher
from dex.dexpb import dex_pb2 as pb

COUNTER = Attribute("lock-check-counter", int)


class OptionsReadOnceStep(Step[int]):
    """Fails if Dex reads its options more than once."""

    def __init__(self) -> None:
        self.option_reads = 0
        self.options: StepOptions | None = None

    def get_step_options(self) -> StepOptions | None:
        self.option_reads += 1
        if self.option_reads > 1:
            raise RuntimeError("Step options were read more than once")
        return self.options

    def execute(self, context: Context, input: int) -> StepDecision:
        COUNTER.set(context, COUNTER.get(context) + input)
        return graceful_complete()


class OptionsReadOnceFlow(Flow[int]):
    """Builds its Step options while Dex reads the persistence schema."""

    def __init__(self) -> None:
        self.step = OptionsReadOnceStep()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.step)

    def get_persistence_schema(self) -> PersistenceSchema:
        self.step.options = StepOptions(execute_lock_attributes=(COUNTER.lock(),))
        return PersistenceSchema.of(COUNTER)


class PassthroughHydrator:
    @staticmethod
    def execute_request(
        request: pb.InvokeExecuteMethodRequest,
    ) -> pb.InvokeExecuteMethodRequest:
        return request


def test_registration_reads_step_options_once_and_check_uses_the_snapshot() -> None:
    flow = OptionsReadOnceFlow()
    registry = Registry((flow,))
    assert flow.step.option_reads == 1

    registered = registry._flow_by_type("OptionsReadOnceFlow")
    start_step = registered.start_step
    assert start_step is not None
    assert start_step.execute_lock_scope.lock_keys == frozenset({COUNTER.name})
    assert registered.steps[start_step.name] is start_step
    assert registered.steps_by_class[OptionsReadOnceStep] == (start_step,)

    # Strict mode raises on an unlocked read-modify-write, and a second options read raises too.
    check = UnlockedAttributeReadModifyWriteCheck(fails_invocation=True)
    values = ValueMapper(registry.codec_registry)
    dispatcher = WorkerDispatcher(
        registry,
        values,
        cast(Any, PassthroughHydrator()),
        NegotiatedServerProtocol(),
        check,
    )
    request = pb.InvokeExecuteMethodRequest(
        flow_type="OptionsReadOnceFlow",
        step_type=start_step.name,
        step_input=values.encode(1, values.codec(int)),
    )
    outputs = list(dispatcher.invoke_execute(request))

    assert [write.key for write in outputs[-1].result.upsert_attributes] == [
        COUNTER.name
    ]
    assert check.detection_count == 0
    assert flow.step.option_reads == 1
