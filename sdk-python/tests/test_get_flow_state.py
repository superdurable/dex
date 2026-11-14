# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import asyncio
from datetime import datetime, timezone
from typing import Any, cast

import pytest
from google.protobuf import timestamp_pb2

from dex import (
    ActiveStepExecutionPhase,
    AsyncClient,
    BlobCache,
    Client,
    Flow,
    Registry,
    StepMethodInvocationPhase,
    StepMethodName,
    StepMethodPendingReason,
)
from dex.dexpb import dex_pb2 as pb


class GetFlowStateContractFlow(Flow[None]):
    pass


class GetFlowStateService:
    def __init__(self, response: pb.GetFlowStateResponse) -> None:
        self.response = response
        self.requests: list[pb.GetFlowStateRequest] = []

    def GetFlowState(self, request: pb.GetFlowStateRequest) -> Any:  # noqa: N802
        self.requests.append(request)
        return self.response


class AsyncGetFlowStateService(GetFlowStateService):
    async def GetFlowState(self, request: pb.GetFlowStateRequest) -> Any:  # noqa: N802
        self.requests.append(request)
        return self.response


def _client(response: pb.GetFlowStateResponse) -> tuple[Client, GetFlowStateService]:
    client = Client(Registry((GetFlowStateContractFlow(),)), cast(BlobCache, object()))
    service = GetFlowStateService(response)
    client._service = cast(Any, service)
    client._server_protocol.record_version(4)
    return client, service


def _timestamp(seconds: int) -> timestamp_pb2.Timestamp:
    return timestamp_pb2.Timestamp(seconds=seconds)


def _response() -> pb.GetFlowStateResponse:
    response = pb.GetFlowStateResponse(
        flow_config=pb.FlowConfig(
            worker_target=pb.WorkerTarget(is_pull_mode=True, task_queue="blue")
        )
    )
    response.active_step_executions.add(
        step_execution_id="ChargeStep-2",
        step_type="ChargeStep",
        phase=pb.ACTIVE_STEP_PHASE_ACTIVE,
        execute_invocation=pb.StepMethodInvocationState(
            phase=pb.STEP_METHOD_INVOCATION_PHASE_BACKOFF,
            attempt=3,
            backoff_fires_at_time=_timestamp(30),
        ),
        last_failure_info=pb.StepMethodFailure(
            backend_error="FLOW_ERROR_TYPE_WORKER_API_FAIL",
            attempt=2,
            details=pb.ServiceErrorResponse(
                detail="server detail",
                original_worker_error_type="ChargeDeclinedError",
                original_worker_error_detail="declined",
                original_worker_error_stack_trace="Traceback: declined",
            ),
        ),
    )
    response.active_step_executions.add(
        step_execution_id="multi-part-Step-1",
        step_type="multi-part-Step",
        phase=pb.ACTIVE_STEP_PHASE_ACTIVE,
        wait_for_invocation=pb.StepMethodInvocationState(
            phase=pb.STEP_METHOD_INVOCATION_PHASE_PENDING,
            pending_reason=pb.STEP_METHOD_PENDING_REASON_WAITING_FOR_LOCKS,
            attempt=1,
            pending_since_time=_timestamp(10),
        ),
        last_failure_info=pb.StepMethodFailure(
            backend_error="FLOW_ERROR_TYPE_WORKER_UNAVAILABLE",
            attempt=1,
            details=pb.ServiceErrorResponse(detail="no Worker"),
        ),
    )
    response.active_step_executions.add(
        step_execution_id="WaitStep-1",
        step_type="WaitStep",
        phase=pb.ACTIVE_STEP_PHASE_WAITING,
    )
    return response


def test_get_flow_state_maps_open_step_executions_and_task_queue() -> None:
    client, service = _client(_response())

    state = client.get_flow_state("order-42")

    assert [(request.flow_id, request.run_id) for request in service.requests] == [
        ("order-42", "")
    ]
    assert state.task_queue == "blue"
    retrying, pending, waiting = state.active_step_executions

    assert retrying.step_execution_id == "ChargeStep-2"
    assert retrying.step_type == "ChargeStep"
    assert retrying.execution_number == 2
    assert retrying.phase is ActiveStepExecutionPhase.ACTIVE
    assert retrying.invocation is not None
    assert retrying.invocation.method is StepMethodName.EXECUTE
    assert retrying.invocation.phase is StepMethodInvocationPhase.BACKOFF
    assert retrying.invocation.attempt == 3
    assert retrying.invocation.pending_reason is None
    assert retrying.invocation.pending_since is None
    assert retrying.invocation.attempt_started_at is None
    assert retrying.invocation.backoff_fires_at == datetime.fromtimestamp(
        30, timezone.utc
    )
    assert retrying.last_failure is not None
    assert retrying.last_failure.attempt == 2
    assert retrying.last_failure.error_type == "ChargeDeclinedError"
    assert retrying.last_failure.detail == "declined"
    assert retrying.last_failure.stack_trace == "Traceback: declined"
    assert retrying.last_failure.backend_error == "FLOW_ERROR_TYPE_WORKER_API_FAIL"

    assert pending.execution_number == 1
    assert pending.invocation is not None
    assert pending.invocation.method is StepMethodName.WAIT_FOR
    assert pending.invocation.phase is StepMethodInvocationPhase.PENDING
    assert pending.invocation.pending_reason is (
        StepMethodPendingReason.WAITING_FOR_LOCKS
    )
    assert pending.invocation.pending_since == datetime.fromtimestamp(10, timezone.utc)
    assert pending.last_failure is not None
    assert pending.last_failure.error_type == ""
    assert pending.last_failure.detail == "no Worker"

    assert waiting.phase is ActiveStepExecutionPhase.WAITING
    assert waiting.invocation is None
    assert waiting.last_failure is None


def test_get_flow_state_reports_default_task_queue_as_empty() -> None:
    client, _ = _client(pb.GetFlowStateResponse())

    state = client.get_flow_state("order-42")

    assert state.task_queue == ""
    assert list(state.active_step_executions) == []


@pytest.mark.parametrize(
    "step_execution_id", ["ChargeStep", "-1", "ChargeStep-0", "Step-x"]
)
def test_get_flow_state_rejects_a_malformed_step_execution_id(
    step_execution_id: str,
) -> None:
    response = pb.GetFlowStateResponse()
    response.active_step_executions.add(
        step_execution_id=step_execution_id, step_type="ChargeStep"
    )
    client, _ = _client(response)

    with pytest.raises(RuntimeError, match="malformed Step execution ID"):
        client.get_flow_state("order-42")


def test_get_flow_state_rejects_an_empty_flow_id_before_transport() -> None:
    client, service = _client(_response())

    with pytest.raises(ValueError):
        client.get_flow_state("")
    assert service.requests == []


def test_async_get_flow_state_maps_the_same_state() -> None:
    async def read() -> None:
        client = AsyncClient(
            Registry((GetFlowStateContractFlow(),)), cast(BlobCache, object())
        )
        service = AsyncGetFlowStateService(_response())
        client._service = cast(Any, service)
        client._server_protocol.record_version(4)
        state = await client.get_flow_state("order-42")
        assert state.task_queue == "blue"
        assert [step.step_execution_id for step in state.active_step_executions] == [
            "ChargeStep-2",
            "multi-part-Step-1",
            "WaitStep-1",
        ]
        await client.close()

    asyncio.run(read())
