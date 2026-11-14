# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

import os
import threading
from collections.abc import Iterator
from contextlib import ExitStack, contextmanager
from datetime import timedelta
from tempfile import TemporaryDirectory
from uuid import uuid4

import pytest

from dex import (
    BlobCacheConfig,
    Client,
    ClientOptions,
    Context,
    Flow,
    FlowStatus,
    LongPollTimeoutError,
    PullWorkerOptions,
    Registry,
    RPCResult,
    StartFlowOptions,
    Step,
    StepDecision,
    StepList,
    Worker,
    WorkerOptions,
    graceful_complete,
    open_blob_cache,
    rpc,
)

from .shared import unique_id

WAIT_TIMEOUT = timedelta(seconds=30)
UNSERVED_WAIT = timedelta(seconds=2)
DEFAULT_LABEL = "default-queue-worker"


class ReportWorkerLabelStep(Step[None]):
    def __init__(self, worker_label: str) -> None:
        self.worker_label = worker_label

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete(self.worker_label)


class NamedTaskQueueStepFlow(Flow[None]):
    """Completes with the label of the Worker that ran its Step."""

    def __init__(self, worker_label: str) -> None:
        self.report = ReportWorkerLabelStep(worker_label)

    def get_flow_type(self) -> str:
        return "PythonNamedTaskQueueStepFlow"

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.report)


class NamedTaskQueueRpcFlow(Flow[None]):
    """Stays open and answers an RPC with the label of the serving Worker."""

    def __init__(self, worker_label: str) -> None:
        self.worker_label = worker_label

    def get_flow_type(self) -> str:
        return "PythonNamedTaskQueueRpcFlow"

    @rpc
    def get_worker_label(self, context: Context) -> RPCResult[str]:
        return RPCResult(self.worker_label)


def _server_address() -> str:
    return os.environ.get("DEX_SERVER_ADDRESS", "127.0.0.1:8801")


def _unique_task_queue(prefix: str) -> str:
    return f"{prefix}-{uuid4().hex[:16]}"


@contextmanager
def _task_queue_worker(task_queue: str, worker_label: str) -> Iterator[Worker]:
    """Run one Worker that polls ``task_queue`` and reports ``worker_label``."""
    registry = Registry(
        (NamedTaskQueueStepFlow(worker_label), NamedTaskQueueRpcFlow(worker_label))
    )
    with TemporaryDirectory(prefix="dex-python-task-queue-cache-") as cache_directory:
        cache = open_blob_cache(BlobCacheConfig(cache_directory, 16 * 1024 * 1024))
        worker = Worker(
            registry,
            cache,
            WorkerOptions(
                server_address=_server_address(),
                pull=PullWorkerOptions(max_concurrent_tasks=2, task_queue=task_queue),
            ),
        )
        failures: list[BaseException] = []

        def run_worker() -> None:
            try:
                worker.start()
            except BaseException as error:
                failures.append(error)

        thread = threading.Thread(target=run_worker, name=f"worker-{worker_label}")
        thread.start()
        try:
            worker.wait_pull_registered(timeout=30)
            yield worker
        finally:
            worker.close()
            thread.join(timeout=10)
            cache.close()
        if thread.is_alive():
            raise RuntimeError(f"Worker {worker_label} did not stop")
        if failures:
            raise RuntimeError(f"Worker {worker_label} failed") from failures[0]


@contextmanager
def _client() -> Iterator[tuple[Client, NamedTaskQueueStepFlow, NamedTaskQueueRpcFlow]]:
    step_flow = NamedTaskQueueStepFlow("client")
    rpc_flow = NamedTaskQueueRpcFlow("client")
    with TemporaryDirectory(prefix="dex-python-task-queue-client-") as cache_directory:
        cache = open_blob_cache(BlobCacheConfig(cache_directory, 16 * 1024 * 1024))
        client = Client(
            Registry((step_flow, rpc_flow)), cache, ClientOptions(_server_address())
        )
        try:
            yield client, step_flow, rpc_flow
        finally:
            client.close()
            cache.close()


def _run_step_flow(
    client: Client, step_flow: NamedTaskQueueStepFlow, task_queue: str
) -> str:
    flow_id = unique_id("named-task-queue-step")
    client.start_flow(step_flow, flow_id, None, StartFlowOptions(task_queue=task_queue))
    result = client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status is FlowStatus.COMPLETED
    return result.single_output(str)


def _invoke_rpc_flow(
    client: Client, rpc_flow: NamedTaskQueueRpcFlow, task_queue: str
) -> str:
    flow_id = unique_id("named-task-queue-rpc")
    client.start_flow(rpc_flow, flow_id, None, StartFlowOptions(task_queue=task_queue))
    try:
        return client.invoke_rpc(rpc_flow.get_worker_label, flow_id)
    finally:
        client.stop_flow(flow_id)


def test_flows_reach_only_workers_of_their_task_queue() -> None:
    queue_a = _unique_task_queue("py-a")
    queue_b = _unique_task_queue("py-b")
    with ExitStack() as stack:
        worker_a = stack.enter_context(_task_queue_worker(queue_a, "worker-a"))
        stack.enter_context(_task_queue_worker(queue_b, "worker-b"))
        stack.enter_context(_task_queue_worker("", DEFAULT_LABEL))
        client, step_flow, rpc_flow = stack.enter_context(_client())

        state = worker_a.pull_registration_state()
        assert state.is_ready
        assert {entry.task_queue for entry in state.queues} == {queue_a}

        for _ in range(3):
            assert _run_step_flow(client, step_flow, queue_a) == "worker-a"
            assert _run_step_flow(client, step_flow, queue_b) == "worker-b"
        assert _run_step_flow(client, step_flow, "") == DEFAULT_LABEL
        assert _run_step_flow(client, step_flow, "dex.default") == DEFAULT_LABEL

        assert _invoke_rpc_flow(client, rpc_flow, queue_a) == "worker-a"
        assert _invoke_rpc_flow(client, rpc_flow, queue_b) == "worker-b"
        assert _invoke_rpc_flow(client, rpc_flow, "") == DEFAULT_LABEL


def test_flow_on_unserved_task_queue_waits_for_its_worker() -> None:
    queue_c = _unique_task_queue("py-c")
    with ExitStack() as stack:
        stack.enter_context(_task_queue_worker("", DEFAULT_LABEL))
        stack.enter_context(
            _task_queue_worker(_unique_task_queue("py-other"), "other-worker")
        )
        client, step_flow, _ = stack.enter_context(_client())
        flow_id = unique_id("named-task-queue-unserved")
        client.start_flow(
            step_flow, flow_id, None, StartFlowOptions(task_queue=queue_c)
        )
        with pytest.raises(LongPollTimeoutError):
            client.wait_for_flow(flow_id, UNSERVED_WAIT)

        stack.enter_context(_task_queue_worker(queue_c, "worker-c"))
        result = client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert result.status is FlowStatus.COMPLETED
        assert result.single_output(str) == "worker-c"
