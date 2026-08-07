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
    FlowErrorType,
    FlowStatus,
    FlowUncompletedError,
    LongPollTimeoutError,
    StartFlowOptions,
    StopFlowOptions,
    StopType,
)

from .empty_decision_flow import EmptyDecisionFlow
from .environment import DexDevTestEnvironment
from .force_fail_flow import ForceFailFlow
from .signal_flow import SignalFlow
from .state_failure_flow import StateFailureFlow
from .state_timeout_flow import StateTimeoutFlow
from .test_basic_runtime import unique_id


def test_flow_wait_timeout() -> None:
    flow = SignalFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("wait-timeout")
        environment.client.start_flow(flow, flow_id, 1)
        with pytest.raises(LongPollTimeoutError) as captured:
            environment.client.wait_for_flow(
                flow_id,
                int,
                timedelta(seconds=1),
            )
        assert captured.value.flow_id == flow_id


def test_flow_timeout() -> None:
    flow = SignalFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("flow-timeout")
        run_id = environment.client.start_flow(
            flow,
            flow_id,
            1,
            StartFlowOptions(timeout=timedelta(seconds=1)),
        )
        failure = wait_for_failure(environment, flow_id)
        assert_failure(failure, run_id, FlowStatus.TIMED_OUT, None, None)


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
        run_id = environment.client.start_flow(flow, flow_id, 1)
        environment.client.stop_flow(flow_id, StopFlowOptions(stop_type, reason))
        assert_failure(
            wait_for_failure(environment, flow_id),
            run_id,
            status,
            error_type,
            message,
        )


def test_force_fail_flow() -> None:
    flow = ForceFailFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("force-fail")
        run_id = environment.client.start_flow(flow, flow_id, 5)
        assert_failure(
            wait_for_failure(environment, flow_id),
            run_id,
            FlowStatus.FAILED,
            FlowErrorType.STEP_DECISION_FAILED,
            "a failing message",
        )


def test_worker_api_failure() -> None:
    flow = StateFailureFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("worker-api-failure")
        run_id = environment.client.start_flow(flow, flow_id, 5)
        failure = wait_for_failure(environment, flow_id)
        assert failure.run_id == run_id
        assert failure.status is FlowStatus.FAILED
        assert failure.error_type is FlowErrorType.WORKER_API_FAILED
        assert "test api failing" in str(failure)
        assert len(failure.results) == 0


def test_worker_api_timeout() -> None:
    flow = StateTimeoutFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("worker-api-timeout")
        run_id = environment.client.start_flow(flow, flow_id, 5)
        failure = wait_for_failure(environment, flow_id)
        assert failure.run_id == run_id
        assert failure.status is FlowStatus.FAILED
        assert failure.error_type is FlowErrorType.WORKER_API_FAILED
        assert "timeout" in str(failure).lower()
        assert len(failure.results) == 0


def test_empty_decision_fails_flow() -> None:
    flow = EmptyDecisionFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("empty-decision")
        run_id = environment.client.start_flow(flow, flow_id, 5)
        failure = wait_for_failure(environment, flow_id)
        assert failure.run_id == run_id
        assert failure.status is FlowStatus.FAILED
        assert failure.error_type is FlowErrorType.WORKER_API_FAILED
        assert "go_to_multi requires a movement" in str(failure)
        assert len(failure.results) == 0


def wait_for_failure(
    environment: DexDevTestEnvironment,
    flow_id: str,
) -> FlowUncompletedError:
    with pytest.raises(FlowUncompletedError) as captured:
        environment.client.wait_for_flow(
            flow_id,
            int,
            timedelta(seconds=15),
        )
    return captured.value


def assert_failure(
    failure: FlowUncompletedError,
    run_id: str,
    status: FlowStatus,
    error_type: FlowErrorType | None,
    message: str | None,
) -> None:
    assert failure.run_id == run_id
    assert failure.status is status
    assert failure.error_type is error_type
    assert failure.args[0] == message
    assert len(failure.results) == 0
