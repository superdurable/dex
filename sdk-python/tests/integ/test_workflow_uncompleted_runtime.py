# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from datetime import timedelta

import pytest

from dex import (
    Context,
    Flow,
    FlowErrorType,
    FlowStatus,
    FlowResult,
    FlowTimeoutPolicy,
    LongPollTimeoutError,
    StepDecision,
    StartFlowOptions,
    StopFlowOptions,
    StopType,
    force_complete,
)

from .empty_decision_flow import EmptyDecisionFlow
from .environment import DexDevTestEnvironment
from .force_fail_flow import ForceFailFlow
from .shared import unique_id
from .signal_flow import SignalFlow
from .state_failure_flow import StateFailureFlow
from .state_timeout_flow import StateTimeoutFlow


def test_flow_wait_timeout() -> None:
    flow = SignalFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("wait-timeout")
        environment.client.start_flow(flow, flow_id, 1)
        with pytest.raises(LongPollTimeoutError) as captured:
            environment.client.wait_for_flow(
                flow_id,
                timedelta(seconds=1),
            )
        assert captured.value.flow_id == flow_id


def test_flow_timeout() -> None:
    flow = SignalFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("flow-timeout")
        environment.client.start_flow(
            flow,
            flow_id,
            1,
            StartFlowOptions(timeout=timedelta(seconds=1)),
        )
        failure = wait_for_failure(environment, flow_id)
        assert_failure(
            failure,
            FlowStatus.FAILED,
            FlowErrorType.FLOW_TIMEOUT,
            "Flow timed out after 1 seconds",
        )


class TimeoutHandlerFlow(Flow[None]):
    def handle_timeout(self, context: Context) -> StepDecision:
        del context
        return force_complete("expired")


def test_flow_timeout_handler() -> None:
    flow = TimeoutHandlerFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("flow-timeout-handler")
        environment.client.start_flow(
            flow,
            flow_id,
            None,
            StartFlowOptions(timeout=timedelta(seconds=1)),
        )
        assert (
            environment.client.wait_for_flow(
                flow_id,
                timedelta(seconds=15),
            ).single_output(str)
            == "expired"
        )


def test_flow_timeout_handler_cancel_override() -> None:
    flow = TimeoutHandlerFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("flow-timeout-handler-cancel")
        environment.client.start_flow(
            flow,
            flow_id,
            None,
            StartFlowOptions(
                timeout=timedelta(seconds=1),
                timeout_policy=FlowTimeoutPolicy.CANCEL,
            ),
        )
        assert_failure(
            wait_for_failure(environment, flow_id),
            FlowStatus.CANCELED,
            None,
            None,
        )


def test_flow_timeout_handler_requires_override() -> None:
    flow = SignalFlow()
    with DexDevTestEnvironment(flow) as environment:
        with pytest.raises(ValueError, match="requires a positive timeout"):
            environment.client.start_flow(
                flow,
                unique_id("flow-timeout-policy-without-timeout"),
                1,
                StartFlowOptions(timeout_policy=FlowTimeoutPolicy.CANCEL),
            )

        with pytest.raises(ValueError, match="has no handle_timeout override"):
            environment.client.start_flow(
                flow,
                unique_id("flow-timeout-handler-missing"),
                1,
                StartFlowOptions(
                    timeout=timedelta(seconds=1),
                    timeout_policy=FlowTimeoutPolicy.HANDLER,
                ),
            )


@pytest.mark.parametrize(
    ("stop_type", "reason", "status", "error_type", "message"),
    (
        (StopType.CANCEL, None, FlowStatus.CANCELED, None, None),
        (StopType.CANCEL, None, FlowStatus.CANCELED, None, None),
        (StopType.TERMINATE, "terminated", FlowStatus.TERMINATED, None, None),
        (
            StopType.FAIL,
            "fail by API",
            FlowStatus.FAILED,
            FlowErrorType.CLIENT_API_FAILED,
            "fail by API",
        ),
    ),
)
def test_stopped_flow(
    stop_type: StopType,
    reason: str | None,
    status: FlowStatus,
    error_type: FlowErrorType | None,
    message: str | None,
) -> None:
    flow = SignalFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("stopped")
        environment.client.start_flow(flow, flow_id, 1)
        environment.client.stop_flow(flow_id, StopFlowOptions(stop_type, reason))
        assert_failure(
            wait_for_failure(environment, flow_id),
            status,
            error_type,
            message,
        )


def test_force_fail_flow() -> None:
    flow = ForceFailFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("force-fail")
        environment.client.start_flow(flow, flow_id, 5)
        assert_failure(
            wait_for_failure(environment, flow_id),
            FlowStatus.FAILED,
            FlowErrorType.STEP_DECISION_FAILED,
            "a failing message",
        )


def test_worker_api_failure() -> None:
    flow = StateFailureFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("worker-api-failure")
        environment.client.start_flow(flow, flow_id, 5)
        failure = wait_for_failure(environment, flow_id)
        assert failure.status is FlowStatus.FAILED
        assert failure.error_type is FlowErrorType.WORKER_API_FAILED
        assert failure.error_message is not None
        assert "test api failing" in failure.error_message
        assert len(failure.completions) == 0


def test_worker_api_timeout() -> None:
    flow = StateTimeoutFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("worker-api-timeout")
        environment.client.start_flow(flow, flow_id, 5)
        failure = wait_for_failure(environment, flow_id)
        assert failure.status is FlowStatus.FAILED
        assert failure.error_type is FlowErrorType.WORKER_API_FAILED
        assert failure.error_message is not None
        assert "timeout" in failure.error_message.lower()
        assert len(failure.completions) == 0


def test_empty_decision_fails_flow() -> None:
    flow = EmptyDecisionFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("empty-decision")
        environment.client.start_flow(flow, flow_id, 5)
        failure = wait_for_failure(environment, flow_id)
        assert failure.status is FlowStatus.FAILED
        assert failure.error_type is FlowErrorType.WORKER_API_FAILED
        assert failure.error_message is not None
        assert "go_to_multi requires a movement" in failure.error_message
        assert len(failure.completions) == 0


def wait_for_failure(
    environment: DexDevTestEnvironment,
    flow_id: str,
) -> FlowResult:
    return environment.client.wait_for_flow(
        flow_id,
        timedelta(seconds=15),
    )


def assert_failure(
    failure: FlowResult,
    status: FlowStatus,
    error_type: FlowErrorType | None,
    message: str | None,
) -> None:
    assert failure.status is status
    assert failure.error_type is error_type
    assert failure.error_message == message
    assert len(failure.completions) == 0
