# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import os
import time

import grpc
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc as pb_grpc


def flow_service_client() -> pb_grpc.FlowServiceStub:
    server_address = os.environ.get("DEX_SERVER_ADDRESS", "127.0.0.1:8801")
    channel = grpc.insecure_channel(server_address)
    return pb_grpc.FlowServiceStub(channel)


def await_live_worker_failure(
    flow_id: str,
    run_id: str,
    *,
    timeout_seconds: float = 6.0,
) -> pb.StepMethodFailure:
    client = flow_service_client()
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        response = client.GetFlowState(
            pb.GetFlowStateRequest(flow_id=flow_id, run_id=run_id)
        )
        for step in response.active_step_executions:
            if step.HasField("last_failure_info") and step.last_failure_info.HasField(
                "details"
            ):
                return step.last_failure_info
        time.sleep(0.05)
    raise AssertionError("active Step did not expose retry failure")


def assert_worker_failure_stack_trace(
    failure: pb.StepMethodFailure,
    expected_detail: str,
) -> None:
    assert failure.attempt == 1
    assert failure.details.original_worker_error_detail == expected_detail
    stack_trace = failure.details.original_worker_error_stack_trace
    assert stack_trace
    assert expected_detail in stack_trace
