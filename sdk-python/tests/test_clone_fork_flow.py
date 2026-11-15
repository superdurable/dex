# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
from datetime import datetime, timedelta, timezone
from typing import Any, cast
from uuid import UUID

import grpc
import pytest
from google.protobuf import any_pb2
from google.rpc import status_pb2
from grpc_status import rpc_status

from dex import (
    AsyncClient,
    BlobCache,
    Client,
    CloneForkFlowOptions,
    CloneForkFlowResult,
    CloneForkFlowUnsupportedError,
    FlowAlreadyStartedError,
    FlowConfig,
    FlowNotFoundError,
    IdReusePolicy,
    SubFlowResetPolicy,
    TimeTravelOptions,
    TimeTravelStepMethod,
    TimeTravelType,
)
from dex._history_point_mapping import (
    map_clone_fork_flow_request,
    map_time_travel_request,
)
from dex.dexpb import dex_pb2 as pb
from dex.flow import Registry
from test_hard_delete_attributes import ProtocolService, SecretFlow
from test_hard_delete_attributes import _options as client_options
from test_hard_delete_attributes import _start_flow_server as start_client_server


class CloneForkService(ProtocolService):
    def __init__(self, current_protocol_version: int) -> None:
        super().__init__(current_protocol_version)
        self.clone_requests: list[pb.CloneForkFlowRequest] = []
        self.reset_requests: list[pb.ResetFlowRequest] = []
        self.clone_failure_sub_status: int | None = None

    def CloneForkFlow(  # noqa: N802
        self,
        request: pb.CloneForkFlowRequest,
        context: grpc.ServicerContext,
    ) -> pb.CloneForkFlowResponse:
        self.calls.append(f"CloneForkFlow:{request.flow_id}")
        self.clone_requests.append(request)
        if self.clone_failure_sub_status is not None:
            packed = any_pb2.Any()
            packed.Pack(
                pb.ServiceErrorResponse(
                    detail="clone fork failed",
                    sub_status=cast(Any, self.clone_failure_sub_status),
                )
            )
            context.abort_with_status(
                rpc_status.to_status(
                    status_pb2.Status(
                        code=grpc.StatusCode.NOT_FOUND.value[0],
                        message="clone fork failed",
                        details=[packed],
                    )
                )
            )
        return pb.CloneForkFlowResponse(
            run_id="clone-run",
            source_run_id="source-run",
            source_run_attempt=2,
            source_event_id=17,
        )

    def ResetFlow(  # noqa: N802
        self,
        request: pb.ResetFlowRequest,
        context: grpc.ServicerContext,
    ) -> pb.ResetFlowResponse:
        self.reset_requests.append(request)
        return pb.ResetFlowResponse(run_id="run-1", run_attempt=2)


async def _with_client(
    client_type: type[Client] | type[AsyncClient],
    service: CloneForkService,
    flow: SecretFlow,
    operation: Any,
) -> None:
    server, port = start_client_server(service)
    client = client_type(
        Registry([flow]), cast(BlobCache, object()), client_options(port)
    )
    try:
        await operation(client)
    finally:
        if isinstance(client, AsyncClient):
            await client.close()
        else:
            client.close()
        server.stop(grace=None).wait(timeout=5)


async def _clone(
    client: Client | AsyncClient,
    flow: SecretFlow,
    source_flow_id: str,
    flow_id: str,
    options: CloneForkFlowOptions = CloneForkFlowOptions(),
) -> CloneForkFlowResult:
    if isinstance(client, AsyncClient):
        return await client.clone_fork_flow(flow, source_flow_id, flow_id, options)
    return client.clone_fork_flow(flow, source_flow_id, flow_id, options)


@pytest.mark.parametrize("client_type", [Client, AsyncClient])
def test_clone_fork_flow_maps_defaults_and_response(
    client_type: type[Client] | type[AsyncClient],
) -> None:
    service = CloneForkService(current_protocol_version=6)
    flow = SecretFlow()

    async def operation(client: Client | AsyncClient) -> None:
        result = await _clone(client, flow, "source", "clone")
        assert result == CloneForkFlowResult("clone-run", "source-run", 2, 17)
        await _clone(client, flow, "source", "clone-2")

    asyncio.run(_with_client(client_type, service, flow, operation))
    request = service.clone_requests[0]
    assert request.source_flow_id == "source"
    assert request.flow_id == "clone"
    assert request.source_run_id == ""
    UUID(request.request_id)
    assert request.request_id != service.clone_requests[1].request_id
    assert request.point_type == pb.FLOW_CLONE_FORK_POINT_TYPE_CURRENT
    assert request.step_method == pb.FLOW_RESET_STEP_METHOD_UNSPECIFIED
    assert request.sub_flow_reset_policy == pb.SUB_FLOW_RESET_POLICY_UNSPECIFIED
    assert request.id_reuse_policy == pb.ID_REUSE_POLICY_UNSPECIFIED
    assert not request.HasField("flow_config_override")
    assert not request.HasField("flow_already_started_options")
    assert list(request.attributes) == []
    assert request.flow_timeout_seconds == 0
    assert request.run_attempt == 0
    assert request.event_id == 0


@pytest.mark.parametrize("client_type", [Client, AsyncClient])
def test_clone_fork_flow_maps_every_option(
    client_type: type[Client] | type[AsyncClient],
) -> None:
    service = CloneForkService(current_protocol_version=6)
    flow = SecretFlow()
    options = CloneForkFlowOptions(
        type=TimeTravelType.EVENT_ID,
        event_id=42,
        source_run_id="source-run-9",
        source_run_attempt=3,
        id_reuse_policy=IdReusePolicy.ALLOW_TERMINATE_IF_RUNNING,
        ignore_already_started=True,
        request_id="branch-request",
        config_override=FlowConfig(history_window_events=64),
        task_queue="tenant-b",
        timeout=timedelta(seconds=90),
        reason="branch the session",
        sub_flow_reset_policy=SubFlowResetPolicy.ATTACH_ELSE_FAIL,
    ).with_attribute(flow.api_key, "copied")

    async def operation(client: Client | AsyncClient) -> None:
        await _clone(client, flow, "source", "clone", options)

    asyncio.run(_with_client(client_type, service, flow, operation))
    request = service.clone_requests[0]
    assert request.source_run_id == "source-run-9"
    assert request.run_attempt == 3
    assert request.point_type == pb.FLOW_CLONE_FORK_POINT_TYPE_EVENT_ID
    assert request.event_id == 42
    assert request.id_reuse_policy == pb.ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING
    assert request.flow_already_started_options.ignore_already_started_error
    assert request.request_id == "branch-request"
    override = request.flow_config_override
    assert override.history_window_events == 64
    assert not override.HasField("step_durability")
    assert override.worker_target.task_queue == "tenant-b"
    assert override.worker_target.is_pull_mode
    assert [write.key for write in request.attributes] == ["api-key"]
    assert request.attributes[0].is_hard_delete_on_null
    assert request.flow_timeout_seconds == 90
    assert request.reason == "branch the session"
    assert request.sub_flow_reset_policy == pb.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_FAIL


@pytest.mark.parametrize(
    ("options", "point_type", "step_method"),
    [
        (CloneForkFlowOptions(), pb.FLOW_CLONE_FORK_POINT_TYPE_CURRENT, 0),
        (
            CloneForkFlowOptions(type=TimeTravelType.BEGINNING, source_run_attempt=1),
            pb.FLOW_CLONE_FORK_POINT_TYPE_BEGINNING,
            0,
        ),
        (
            CloneForkFlowOptions(
                type=TimeTravelType.HISTORY_EVENT_TIME,
                history_event_time=datetime(2026, 11, 15, 10, 30, tzinfo=timezone.utc),
            ),
            pb.FLOW_CLONE_FORK_POINT_TYPE_HISTORY_EVENT_TIME,
            0,
        ),
        (
            CloneForkFlowOptions(type=TimeTravelType.STEP_TYPE, step_type="Charge"),
            pb.FLOW_CLONE_FORK_POINT_TYPE_STEP_TYPE,
            0,
        ),
        (
            CloneForkFlowOptions(
                type=TimeTravelType.STEP_EXECUTION_ID,
                step_execution_id="Charge-2",
                step_method=TimeTravelStepMethod.WAIT_FOR,
            ),
            pb.FLOW_CLONE_FORK_POINT_TYPE_STEP_EXECUTION_ID,
            pb.FLOW_RESET_STEP_METHOD_WAIT_FOR,
        ),
        (
            CloneForkFlowOptions(type=TimeTravelType.EVENT_ID, event_id=7),
            pb.FLOW_CLONE_FORK_POINT_TYPE_EVENT_ID,
            0,
        ),
    ],
)
def test_clone_fork_flow_maps_point_types(
    options: CloneForkFlowOptions, point_type: int, step_method: int
) -> None:
    request = map_clone_fork_flow_request("source", "clone", options, None, [], 0)
    assert request.point_type == point_type
    assert request.step_method == step_method
    assert request.step_type == (options.step_type or "")
    assert request.step_execution_id == (options.step_execution_id or "")
    assert request.event_id == (options.event_id or 0)
    assert request.run_attempt == options.source_run_attempt
    if options.type is TimeTravelType.HISTORY_EVENT_TIME:
        assert request.history_event_time == "2026-11-15T10:30:00+00:00"
    else:
        assert request.history_event_time == ""


@pytest.mark.parametrize(
    ("arguments", "message"),
    [
        ({"event_id": 3}, "cannot include another selector"),
        ({"source_run_attempt": 1}, "accepts only source_run_attempt 0"),
        (
            {"type": TimeTravelType.BEGINNING, "source_run_attempt": -1},
            "must not be negative",
        ),
        ({"type": TimeTravelType.EVENT_ID}, "selector must be set"),
        ({"type": TimeTravelType.EVENT_ID, "event_id": 0}, "positive integer"),
        ({"type": TimeTravelType.EVENT_ID, "event_id": -4}, "positive integer"),
        (
            {"type": TimeTravelType.EVENT_ID, "event_id": 4, "step_type": "A"},
            "cannot be combined",
        ),
        (
            {
                "type": TimeTravelType.STEP_TYPE,
                "step_type": "A",
                "step_method": TimeTravelStepMethod.EXECUTE,
            },
            "cannot be combined",
        ),
        (
            {"type": TimeTravelType.STEP_EXECUTION_ID, "step_execution_id": "A-1"},
            "WAIT_FOR or EXECUTE",
        ),
        ({"timeout": timedelta(seconds=-1)}, "must not be negative"),
        ({"request_id": ""}, "request_id must not be empty"),
        ({"task_queue": "dex.other"}, "reserved"),
    ],
)
def test_clone_fork_flow_options_reject_invalid_values(
    arguments: dict[str, Any], message: str
) -> None:
    with pytest.raises(ValueError, match=message):
        CloneForkFlowOptions(**arguments)


@pytest.mark.parametrize("client_type", [Client, AsyncClient])
def test_clone_fork_flow_validates_flow_ids(
    client_type: type[Client] | type[AsyncClient],
) -> None:
    service = CloneForkService(current_protocol_version=6)
    flow = SecretFlow()

    async def operation(client: Client | AsyncClient) -> None:
        with pytest.raises(ValueError, match="durable name is required"):
            await _clone(client, flow, "", "clone")
        with pytest.raises(ValueError, match="must differ from source_flow_id"):
            await _clone(client, flow, "same", "same")

    asyncio.run(_with_client(client_type, service, flow, operation))
    assert service.clone_requests == []


@pytest.mark.parametrize("client_type", [Client, AsyncClient])
def test_clone_fork_flow_refuses_servers_before_protocol_6(
    client_type: type[Client] | type[AsyncClient],
) -> None:
    service = CloneForkService(current_protocol_version=5)
    flow = SecretFlow()

    async def operation(client: Client | AsyncClient) -> None:
        with pytest.raises(CloneForkFlowUnsupportedError) as failure:
            await _clone(client, flow, "source", "clone")
        assert failure.value.negotiated_server_protocol == 5
        assert failure.value.required_server_protocol == 6
        assert "server-zd 0.1.33 or later" in str(failure.value)

    asyncio.run(_with_client(client_type, service, flow, operation))
    assert service.calls == ["GetServerInfo"]


@pytest.mark.parametrize("client_type", [Client, AsyncClient])
def test_clone_fork_flow_translates_errors(
    client_type: type[Client] | type[AsyncClient],
) -> None:
    service = CloneForkService(current_protocol_version=6)
    flow = SecretFlow()

    async def operation(client: Client | AsyncClient) -> None:
        service.clone_failure_sub_status = pb.ERROR_SUB_STATUS_FLOW_NOT_EXISTS
        with pytest.raises(FlowNotFoundError) as not_found:
            await _clone(client, flow, "source", "clone")
        assert not_found.value.flow_id == "source"
        service.clone_failure_sub_status = pb.ERROR_SUB_STATUS_FLOW_ALREADY_STARTED
        with pytest.raises(FlowAlreadyStartedError) as already_started:
            await _clone(client, flow, "source", "clone")
        assert already_started.value.flow_id == "clone"

    asyncio.run(_with_client(client_type, service, flow, operation))


@pytest.mark.parametrize("client_type", [Client, AsyncClient])
def test_time_travel_sends_event_id_and_sub_flow_reset_policy(
    client_type: type[Client] | type[AsyncClient],
) -> None:
    service = CloneForkService(current_protocol_version=6)
    flow = SecretFlow()

    async def operation(client: Client | AsyncClient) -> None:
        options = TimeTravelOptions(
            type=TimeTravelType.EVENT_ID,
            event_id=12,
            sub_flow_reset_policy=SubFlowResetPolicy.ALWAYS_REJECT,
        )
        if isinstance(client, AsyncClient):
            await client.time_travel("flow-1", options)
            await client.time_travel(
                "flow-1", TimeTravelOptions(type=TimeTravelType.BEGINNING)
            )
        else:
            client.time_travel("flow-1", options)
            client.time_travel(
                "flow-1", TimeTravelOptions(type=TimeTravelType.BEGINNING)
            )

    asyncio.run(_with_client(client_type, service, flow, operation))
    by_event, beginning = service.reset_requests
    assert by_event.reset_type == pb.FLOW_RESET_TYPE_EVENT_ID
    assert by_event.event_id == 12
    assert by_event.sub_flow_reset_policy == pb.SUB_FLOW_RESET_POLICY_ALWAYS_REJECT
    assert by_event.step_method == pb.FLOW_RESET_STEP_METHOD_UNSPECIFIED
    assert beginning.sub_flow_reset_policy == pb.SUB_FLOW_RESET_POLICY_UNSPECIFIED
    assert beginning.event_id == 0


@pytest.mark.parametrize(
    ("options", "message"),
    [
        (
            TimeTravelOptions(type=TimeTravelType.CURRENT),
            "valid only for clone_fork_flow",
        ),
        (TimeTravelOptions(type=TimeTravelType.EVENT_ID), "selector must be set"),
        (
            TimeTravelOptions(type=TimeTravelType.EVENT_ID, event_id=0),
            "positive integer",
        ),
        (
            TimeTravelOptions(type=TimeTravelType.BEGINNING, event_id=5),
            "cannot include another selector",
        ),
    ],
)
def test_time_travel_rejects_invalid_event_id_and_current(
    options: TimeTravelOptions, message: str
) -> None:
    with pytest.raises(ValueError, match=message):
        map_time_travel_request("flow-1", options)


@pytest.mark.parametrize(
    ("policy", "expected"),
    [
        (SubFlowResetPolicy.DEFAULT, pb.SUB_FLOW_RESET_POLICY_UNSPECIFIED),
        (SubFlowResetPolicy.ALWAYS_RESTART, pb.SUB_FLOW_RESET_POLICY_ALWAYS_RESTART),
        (SubFlowResetPolicy.ALWAYS_FAIL, pb.SUB_FLOW_RESET_POLICY_ALWAYS_FAIL),
        (SubFlowResetPolicy.ALWAYS_REJECT, pb.SUB_FLOW_RESET_POLICY_ALWAYS_REJECT),
        (
            SubFlowResetPolicy.ATTACH_ELSE_RESTART,
            pb.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_RESTART,
        ),
        (
            SubFlowResetPolicy.ATTACH_ELSE_FAIL,
            pb.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_FAIL,
        ),
        (
            SubFlowResetPolicy.ATTACH_ELSE_REJECT,
            pb.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_REJECT,
        ),
    ],
)
def test_sub_flow_reset_policy_maps_every_value(
    policy: SubFlowResetPolicy, expected: int
) -> None:
    request = map_time_travel_request(
        "flow-1",
        TimeTravelOptions(type=TimeTravelType.BEGINNING, sub_flow_reset_policy=policy),
    )
    assert request.sub_flow_reset_policy == expected
