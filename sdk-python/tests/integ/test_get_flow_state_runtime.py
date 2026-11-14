# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
import os
import time
from collections.abc import Callable
from datetime import datetime, timedelta, timezone
from tempfile import TemporaryDirectory
from uuid import uuid4

import pytest

from dex import (
    ActiveStepExecution,
    ActiveStepExecutionPhase,
    AsyncClient,
    BlobCacheConfig,
    Client,
    ClientOptions,
    FlowNotFoundError,
    FlowStatus,
    Registry,
    StartFlowOptions,
    StepMethodInvocationPhase,
    StepMethodName,
    StopFlowOptions,
    StopType,
    open_blob_cache,
)

from .environment import DexDevTestEnvironment
from .get_flow_state_flow import (
    GET_FLOW_STATE_FAILURE_DETAIL,
    ChargeDeclinedError,
    GetFlowStateFailingFlow,
    GetFlowStateWaitingFlow,
)
from .shared import unique_id

WAIT_TIMEOUT = timedelta(seconds=30)
TERMINATE = StopFlowOptions(type=StopType.TERMINATE, reason="test cleanup")


def test_get_flow_state_reports_the_retrying_step_and_its_last_failure() -> None:
    flow = GetFlowStateFailingFlow()
    with DexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("get-flow-state-failing")
        started_at = datetime.now(timezone.utc)
        client.start_flow(flow, flow_id, None)

        step = _poll_active_step(
            client,
            flow_id,
            lambda candidate: candidate.last_failure is not None
            and candidate.invocation is not None
            and candidate.invocation.phase is StepMethodInvocationPhase.BACKOFF,
        )
        assert step.step_type == "GetFlowStateFailingStep"
        assert step.step_execution_id == "GetFlowStateFailingStep-1"
        assert step.execution_number == 1
        assert step.phase is ActiveStepExecutionPhase.ACTIVE
        invocation = step.invocation
        assert invocation is not None
        assert invocation.method is StepMethodName.EXECUTE
        assert invocation.attempt == 2
        assert invocation.pending_reason is None
        assert invocation.backoff_fires_at is not None
        assert invocation.backoff_fires_at > started_at
        failure = step.last_failure
        assert failure is not None
        assert failure.attempt == 1
        assert failure.error_type == (
            f"{ChargeDeclinedError.__module__}.{ChargeDeclinedError.__qualname__}"
        )
        assert failure.detail == GET_FLOW_STATE_FAILURE_DETAIL
        assert GET_FLOW_STATE_FAILURE_DETAIL in failure.stack_trace

        client.stop_flow(flow_id, TERMINATE)
        closed = client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert closed.status is FlowStatus.TERMINATED
        assert list(client.get_flow_state(flow_id).active_step_executions) == []


def test_get_flow_state_reports_no_failure_for_a_healthy_running_flow() -> None:
    flow = GetFlowStateWaitingFlow()
    with DexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("get-flow-state-waiting")
        client.start_flow(flow, flow_id, None)

        step = _poll_active_step(
            client,
            flow_id,
            lambda candidate: candidate.phase is ActiveStepExecutionPhase.WAITING,
        )
        assert step.step_type == "GetFlowStateWaitingStep"
        assert step.execution_number == 1
        assert step.invocation is None
        assert step.last_failure is None
        assert client.describe_flow(flow_id).status is FlowStatus.RUNNING

        client.stop_flow(flow_id, TERMINATE)


def test_get_flow_state_rejects_a_missing_flow() -> None:
    flow = GetFlowStateWaitingFlow()
    with DexDevTestEnvironment(flow) as environment:
        missing_flow_id = unique_id("get-flow-state-missing")
        with pytest.raises(FlowNotFoundError) as error:
            environment.client.get_flow_state(missing_flow_id)
        assert error.value.flow_id == missing_flow_id
        with pytest.raises(ValueError):
            environment.client.get_flow_state("")


def test_get_flow_state_reports_the_task_queue_without_a_worker() -> None:
    flow = GetFlowStateWaitingFlow()
    named_task_queue = f"py-state-{uuid4().hex[:16]}"
    with TemporaryDirectory(prefix="dex-python-flow-state-") as cache_directory:
        cache = open_blob_cache(BlobCacheConfig(cache_directory, 16 * 1024 * 1024))
        client = Client(Registry((flow,)), cache, ClientOptions(_server_address()))
        started: dict[str, str] = {}
        try:
            for task_queue, expected_task_queue in (
                (named_task_queue, named_task_queue),
                ("", ""),
                ("dex.default", ""),
            ):
                flow_id = unique_id("get-flow-state-task-queue")
                client.start_flow(
                    flow, flow_id, None, StartFlowOptions(task_queue=task_queue)
                )
                started[flow_id] = expected_task_queue
                assert client.get_flow_state(flow_id).task_queue == expected_task_queue
            async_task_queues = asyncio.run(
                _read_task_queues_async(flow, list(started))
            )
            assert async_task_queues == list(started.values())
        finally:
            for flow_id in started:
                client.stop_flow(flow_id, TERMINATE)
            client.close()
            cache.close()


async def _read_task_queues_async(
    flow: GetFlowStateWaitingFlow, flow_ids: list[str]
) -> list[str]:
    with TemporaryDirectory(prefix="dex-python-flow-state-async-") as cache_directory:
        cache = open_blob_cache(BlobCacheConfig(cache_directory, 16 * 1024 * 1024))
        client = AsyncClient(Registry((flow,)), cache, ClientOptions(_server_address()))
        try:
            return [
                (await client.get_flow_state(flow_id)).task_queue
                for flow_id in flow_ids
            ]
        finally:
            await client.close()
            cache.close()


def _poll_active_step(
    client: Client,
    flow_id: str,
    is_ready: Callable[[ActiveStepExecution], bool],
    timeout_seconds: float = 15,
) -> ActiveStepExecution:
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        state = client.get_flow_state(flow_id)
        for step in state.active_step_executions:
            if is_ready(step):
                assert len(state.active_step_executions) == 1
                return step
        time.sleep(0.05)
    raise AssertionError(
        f"Flow {flow_id} did not report the expected active Step within "
        f"{timeout_seconds} seconds"
    )


def _server_address() -> str:
    return os.environ.get("DEX_SERVER_ADDRESS", "127.0.0.1:8801")
