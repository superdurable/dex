# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from datetime import timedelta
from typing import Any, cast

import pytest

from dex import (
    FlowDefinitionError,
    Registry,
    StepDecision,
    StepList,
    StepRef,
    StepTarget,
    go_to,
)
from dex._server_protocol import NegotiatedServerProtocol
from dex._value_mapper import ValueMapper
from dex._worker_dispatcher import WorkerDispatcher
from dex.dexpb import dex_pb2 as pb

from .environment import DexDevTestEnvironment
from .shared import unique_id
from .step_instances_flow import (
    FailingInstanceStep,
    InstanceRecoveryFlow,
    StepInstancesFlow,
    TypedInstanceStep,
)


def test_generic_step_instances_route_typed_inputs() -> None:
    flow = StepInstancesFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("step-instances")
        environment.client.start_flow(flow, flow_id, 0)
        result = environment.client.wait_for_flow(flow_id, timedelta(seconds=30))
        assert result.single_output(int) == 2
        assert flow.observed == [0, "first", 1, "second", 2]


@pytest.mark.parametrize("reference", [False, True])
def test_execute_failure_routes_by_instance_or_reference(reference: bool) -> None:
    flow = InstanceRecoveryFlow(reference)
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("instance-recovery")
        environment.client.start_flow(flow, flow_id, None)
        result = environment.client.wait_for_flow(flow_id, timedelta(seconds=30))
        assert result.single_output(str) == "recovered"


def test_registration_rejects_duplicate_step_types() -> None:
    class DuplicateTypesFlow(StepInstancesFlow):
        def get_steps(self) -> StepList[int]:
            return super().get_steps().other_steps(self.numbers)

    with pytest.raises(FlowDefinitionError, match="duplicate Step Numbers"):
        Registry((DuplicateTypesFlow(),))


@pytest.mark.parametrize("target", [StepRef("Missing"), TypedInstanceStep])
def test_registration_validates_declared_failure_targets(target: object) -> None:
    class InvalidFailureFlow(StepInstancesFlow):
        def get_steps(self) -> StepList[int]:
            return (
                super()
                .get_steps()
                .other_steps(FailingInstanceStep(cast(StepTarget[Any], target)))
            )

    message = "not registered: Missing" if isinstance(target, StepRef) else "ambiguous"
    with pytest.raises(FlowDefinitionError, match=message):
        Registry((InvalidFailureFlow(),))


def test_runtime_targets_reject_unknown_ambiguous_and_unregistered_instances() -> None:
    flow = StepInstancesFlow()
    registered = Registry((flow,))._registered_flows[flow.get_flow_type()]
    with pytest.raises(FlowDefinitionError, match="not registered: Missing"):
        registered.step_target(StepRef("Missing"))
    with pytest.raises(FlowDefinitionError, match="ambiguous"):
        registered.step_target(TypedInstanceStep)
    other_flow = StepInstancesFlow()
    with pytest.raises(FlowDefinitionError, match="instance is not registered"):
        registered.step_target(other_flow.numbers)
    assert registered.step_target(flow.numbers).name == "Numbers"
    assert registered.step_target(StepRef("Strings")).name == "Strings"


@pytest.mark.parametrize("target", [StepRef("Missing"), TypedInstanceStep])
def test_dispatch_reports_invalid_movement_targets(target: object) -> None:
    class InvalidMovementFlow(StepInstancesFlow):
        def _advance_number(self, input: int) -> StepDecision:
            return go_to(cast(StepTarget[int], target), input)

    class PassthroughHydrator:
        @staticmethod
        def execute_request(
            request: pb.InvokeExecuteMethodRequest,
        ) -> pb.InvokeExecuteMethodRequest:
            return request

    flow = InvalidMovementFlow()
    registry = Registry((flow,))
    values = ValueMapper(registry.codec_registry)
    dispatcher = WorkerDispatcher(
        registry, values, cast(Any, PassthroughHydrator()), NegotiatedServerProtocol()
    )
    message = "not registered: Missing" if isinstance(target, StepRef) else "ambiguous"
    with pytest.raises(FlowDefinitionError, match=message):
        list(
            dispatcher.invoke_execute(
                pb.InvokeExecuteMethodRequest(
                    flow_type=flow.get_flow_type(),
                    step_type="Numbers",
                    step_input=values.encode(0, values.codec(int)),
                )
            )
        )
