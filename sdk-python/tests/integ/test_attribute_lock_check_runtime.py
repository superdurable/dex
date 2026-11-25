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

import asyncio
import logging
import os
import threading
from collections.abc import Callable, Iterator
from contextlib import ExitStack, contextmanager
from dataclasses import dataclass
from datetime import timedelta
from tempfile import TemporaryDirectory
from typing import Any
from uuid import uuid4

import pytest

from dex import (
    AsyncWorker,
    Attribute,
    AttributeMap,
    BlobCacheConfig,
    Client,
    ClientOptions,
    Context,
    Flow,
    FlowStatus,
    PersistenceSchema,
    PullWorkerOptions,
    Registry,
    RetryPolicy,
    RPCInvokeOptions,
    StartFlowOptions,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    Wait,
    Worker,
    WorkerInvocationError,
    WorkerOptions,
    go_to,
    graceful_complete,
    open_blob_cache,
    rpc,
)

from .shared import unique_id

WAIT_TIMEOUT = timedelta(seconds=30)
LOCK_CHECK_LOGGER = "dex._attribute_lock_check"

COUNTER = Attribute("py-lock-check-counter", int)
OTHER = Attribute("py-lock-check-other", int)
ENTRIES = AttributeMap("py-lock-check-entries", int)

def _single_attempt_options(**overrides: Any) -> StepOptions:
    return StepOptions(
        wait_for_retry=RetryPolicy(maximum_attempts=1),
        execute_retry=RetryPolicy(maximum_attempts=1),
        **overrides,
    )

def _increment(context: Context) -> None:
    COUNTER.set(context, COUNTER.get(context) + 1)

def _increment_entry(context: Context, instance: str) -> None:
    ENTRIES.set(context, instance, (ENTRIES.get(context, instance) or 0) + 1)

class PyLockCheckUnlockedStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _single_attempt_options()

    def execute(self, context: Context, input: None) -> StepDecision:
        _increment(context)
        return graceful_complete()

class PyLockCheckUnlockedWaitForStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _single_attempt_options()

    def wait_for(self, context: Context, input: None) -> Wait:
        _increment(context)
        return Wait.skip_immediately()

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete()

class PyLockCheckUnlockedMapStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _single_attempt_options(execute_load_attribute_maps=(ENTRIES,))

    def execute(self, context: Context, input: None) -> StepDecision:
        _increment_entry(context, "e1")
        return graceful_complete()

class PyLockCheckLockedStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _single_attempt_options(execute_lock_attributes=(COUNTER.lock(),))

    def execute(self, context: Context, input: None) -> StepDecision:
        _increment(context)
        return graceful_complete()

class PyLockCheckLockedMapStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _single_attempt_options(
            execute_lock_attributes=(ENTRIES.lock("e1"),),
            execute_load_attribute_map_instances=(ENTRIES.load("e1"),),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        _increment_entry(context, "e1")
        return graceful_complete()

class PyLockCheckSingleWriterStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _single_attempt_options(execute_load_attribute_maps=(ENTRIES,))

    def execute(self, context: Context, input: None) -> StepDecision:
        _increment(context)
        _increment_entry(context, "e1")
        # The declarations follow the accesses: their order does not matter.
        COUNTER.declare_single_writer(context)
        ENTRIES.declare_single_writer(context, "e1")
        return graceful_complete()

class PyLockCheckReadOrWriteOnlyStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _single_attempt_options()

    def execute(self, context: Context, input: None) -> StepDecision:
        # Reading one Attribute to compute a write of another is not a read-modify-write.
        OTHER.set(context, COUNTER.get(context) + 10)
        # A read after the invocation's own write sees that write, not the snapshot.
        OTHER.set(context, OTHER.get(context) + 1)
        return graceful_complete()

class PyLockCheckDispatchStep(Step[str]):
    def __init__(self, targets: dict[str, Step[None]]) -> None:
        self._targets = targets

    def execute(self, context: Context, input: str) -> StepDecision:
        return go_to(self._targets[input], None)

class PyLockCheckStepFlow(Flow[str]):
    def __init__(self) -> None:
        self.targets: dict[str, Step[None]] = {
            "unlocked": PyLockCheckUnlockedStep(),
            "unlocked-wait-for": PyLockCheckUnlockedWaitForStep(),
            "unlocked-map": PyLockCheckUnlockedMapStep(),
            "locked": PyLockCheckLockedStep(),
            "locked-map": PyLockCheckLockedMapStep(),
            "single-writer": PyLockCheckSingleWriterStep(),
            "read-or-write-only": PyLockCheckReadOrWriteOnlyStep(),
        }
        self.dispatch = PyLockCheckDispatchStep(self.targets)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.dispatch).other_steps(*self.targets.values())

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(COUNTER, OTHER, ENTRIES)

class PyLockCheckRpcFlow(Flow[None]):
    """Has no Steps, so it stays open for RPC cases."""

    def get_steps(self) -> StepList[None]:
        return StepList.empty()

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(COUNTER, OTHER, ENTRIES)

    @rpc
    def unlocked_increment(self, context: Context) -> None:
        _increment(context)

    @rpc(lock_attributes=(COUNTER.lock(),))
    def locked_increment(self, context: Context) -> None:
        _increment(context)

    @rpc(load_attribute_maps=(ENTRIES,))
    def registered_load_entry_increment(self, context: Context, instance: str) -> None:
        _increment_entry(context, instance)

    @rpc
    def runtime_selected_entry_increment(self, context: Context, instance: str) -> None:
        _increment_entry(context, instance)

STEP_FLOW = PyLockCheckStepFlow()
RPC_FLOW = PyLockCheckRpcFlow()

def _server_address() -> str:
    return os.environ.get("DEX_SERVER_ADDRESS", "127.0.0.1:8801")

def _registry() -> Registry:
    return Registry((STEP_FLOW, RPC_FLOW))

def _worker_options(task_queue: str, is_strict: bool) -> WorkerOptions:
    return WorkerOptions(
        server_address=_server_address(),
        pull=PullWorkerOptions(max_concurrent_tasks=2, task_queue=task_queue),
        fail_on_unlocked_attribute_read_modify_write=is_strict,
    )

@contextmanager
def _sync_worker(task_queue: str, is_strict: bool) -> Iterator[Worker]:
    with TemporaryDirectory(prefix="dex-python-lock-check-cache-") as cache_directory:
        cache = open_blob_cache(BlobCacheConfig(cache_directory, 16 * 1024 * 1024))
        worker = Worker(_registry(), cache, _worker_options(task_queue, is_strict))
        failures: list[BaseException] = []

        def run_worker() -> None:
            try:
                worker.start()
            except BaseException as error:
                failures.append(error)

        thread = threading.Thread(target=run_worker, name=f"lock-check-{task_queue}")
        thread.start()
        try:
            worker.wait_pull_registered(timeout=30)
            yield worker
        finally:
            worker.close()
            thread.join(timeout=10)
            cache.close()
        if failures:
            raise RuntimeError("lock check Worker failed") from failures[0]

@contextmanager
def _async_worker(task_queue: str, is_strict: bool) -> Iterator[AsyncWorker]:
    with TemporaryDirectory(prefix="dex-python-lock-check-async-") as cache_directory:
        cache = open_blob_cache(BlobCacheConfig(cache_directory, 16 * 1024 * 1024))
        loop = asyncio.new_event_loop()
        loop_thread = threading.Thread(
            target=loop.run_forever, name=f"lock-check-async-{task_queue}"
        )
        loop_thread.start()

        async def create_worker() -> AsyncWorker:
            return AsyncWorker(
                _registry(), cache, _worker_options(task_queue, is_strict)
            )

        worker = asyncio.run_coroutine_threadsafe(create_worker(), loop).result(
            timeout=20
        )
        serving = asyncio.run_coroutine_threadsafe(worker.start(), loop)
        try:
            asyncio.run_coroutine_threadsafe(
                worker.wait_pull_registered(timeout=30), loop
            ).result(timeout=40)
            yield worker
        finally:
            asyncio.run_coroutine_threadsafe(worker.close(), loop).result(timeout=20)
            serving.result(timeout=20)
            loop.call_soon_threadsafe(loop.stop)
            loop_thread.join(timeout=10)
            loop.close()
            cache.close()

@contextmanager
def _client() -> Iterator[Client]:
    with TemporaryDirectory(prefix="dex-python-lock-check-client-") as cache_directory:
        cache = open_blob_cache(BlobCacheConfig(cache_directory, 16 * 1024 * 1024))
        client = Client(_registry(), cache, ClientOptions(_server_address()))
        try:
            yield client
        finally:
            client.close()
            cache.close()

def _task_queue(prefix: str) -> str:
    return f"{prefix}-{uuid4().hex[:12]}"

@dataclass(frozen=True)
class _LockCheckWorkers:
    client: Client
    warn_queue: str
    warn_count: Callable[[], int]
    strict_queue: str
    strict_count: Callable[[], int]

@contextmanager
def _lock_check_workers(is_async: bool) -> Iterator[_LockCheckWorkers]:
    warn_queue = _task_queue("py-lock-warn")
    strict_queue = _task_queue("py-lock-strict")
    with ExitStack() as stack:
        warn_worker: Worker | AsyncWorker
        strict_worker: Worker | AsyncWorker
        if is_async:
            warn_worker = stack.enter_context(_async_worker(warn_queue, False))
            strict_worker = stack.enter_context(_async_worker(strict_queue, True))
        else:
            warn_worker = stack.enter_context(_sync_worker(warn_queue, False))
            strict_worker = stack.enter_context(_sync_worker(strict_queue, True))
        client = stack.enter_context(_client())
        yield _LockCheckWorkers(
            client,
            warn_queue,
            lambda: warn_worker.unlocked_attribute_read_modify_write_count,
            strict_queue,
            lambda: strict_worker.unlocked_attribute_read_modify_write_count,
        )

def _run_step_case(client: Client, task_queue: str, step_case: str) -> FlowStatus:
    flow_id = unique_id(f"py-lock-check-{step_case}")
    client.start_flow(
        STEP_FLOW, flow_id, step_case, StartFlowOptions(task_queue=task_queue)
    )
    return client.wait_for_flow(flow_id, WAIT_TIMEOUT).status

def _warning_logged(
    caplog: pytest.LogCaptureFixture, handler: str, attribute: str
) -> bool:
    return any(
        f"handler={handler} attribute={attribute} " in record.getMessage()
        for record in caplog.records
        if record.name == LOCK_CHECK_LOGGER
    )

STEP_CASES = [
    ("unlocked", "PyLockCheckUnlockedStep.execute", COUNTER.name),
    ("unlocked-wait-for", "PyLockCheckUnlockedWaitForStep.wait_for", COUNTER.name),
    ("unlocked-map", "PyLockCheckUnlockedMapStep.execute", ENTRIES.name),
    ("locked", None, None),
    ("locked-map", None, None),
    ("single-writer", None, None),
    ("read-or-write-only", None, None),
]

def _check_step_cases(
    workers: _LockCheckWorkers, caplog: pytest.LogCaptureFixture
) -> None:
    for step_case, handler, attribute in STEP_CASES:
        before = workers.warn_count()
        status = _run_step_case(workers.client, workers.warn_queue, step_case)
        assert status is FlowStatus.COMPLETED, step_case
        detections = workers.warn_count() - before
        if handler is not None and attribute is not None:
            assert detections == 1, step_case
            assert _warning_logged(caplog, handler, attribute), step_case
        else:
            assert detections == 0, step_case

        before = workers.strict_count()
        status = _run_step_case(workers.client, workers.strict_queue, step_case)
        detections = workers.strict_count() - before
        if handler is not None:
            assert status is FlowStatus.FAILED, step_case
            assert detections == 1, step_case
        else:
            assert status is FlowStatus.COMPLETED, step_case
            assert detections == 0, step_case

def _invoke_on_queue(
    client: Client, task_queue: str, invoke: Callable[[str], None]
) -> None:
    flow_id = unique_id("py-lock-check-rpc")
    client.start_flow(RPC_FLOW, flow_id, None, StartFlowOptions(task_queue=task_queue))
    try:
        invoke(flow_id)
    finally:
        client.stop_flow(flow_id)

def _rpc_cases(
    client: Client,
) -> list[tuple[str, str | None, str | None, Callable[[str], None]]]:
    return [
        (
            "unlocked",
            "rpc.unlocked_increment",
            COUNTER.name,
            lambda flow_id: client.invoke_rpc(RPC_FLOW.unlocked_increment, flow_id),
        ),
        (
            "registered-map-load",
            "rpc.registered_load_entry_increment",
            ENTRIES.name,
            lambda flow_id: client.invoke_rpc(
                RPC_FLOW.registered_load_entry_increment, flow_id, "e1"
            ),
        ),
        (
            "locked",
            None,
            None,
            lambda flow_id: client.invoke_rpc(RPC_FLOW.locked_increment, flow_id),
        ),
        (
            "runtime-selected-map-lock",
            None,
            None,
            lambda flow_id: client.invoke_rpc(
                RPC_FLOW.runtime_selected_entry_increment,
                flow_id,
                "e1",
                options=RPCInvokeOptions(
                    lock_attribute_map_instances=(ENTRIES.lock("e1"),),
                    load_attribute_map_instances=(ENTRIES.load("e1"),),
                ),
            ),
        ),
    ]

def _check_rpc_cases(
    workers: _LockCheckWorkers, caplog: pytest.LogCaptureFixture
) -> None:
    for name, handler, attribute, invoke in _rpc_cases(workers.client):
        before = workers.warn_count()
        _invoke_on_queue(workers.client, workers.warn_queue, invoke)
        detections = workers.warn_count() - before
        if handler is not None and attribute is not None:
            assert detections == 1, name
            assert _warning_logged(caplog, handler, attribute), name
        else:
            assert detections == 0, name

        before = workers.strict_count()
        if handler is None or attribute is None:
            _invoke_on_queue(workers.client, workers.strict_queue, invoke)
            assert workers.strict_count() - before == 0, name
            continue
        with pytest.raises(WorkerInvocationError) as raised:
            _invoke_on_queue(workers.client, workers.strict_queue, invoke)
        assert workers.strict_count() - before == 1, name
        assert raised.value.worker_error_type.endswith(
            "UnlockedAttributeReadModifyWriteError"
        ), name
        assert attribute in raised.value.worker_error_detail, name

def test_unlocked_read_modify_write_detection_with_worker(
    caplog: pytest.LogCaptureFixture,
) -> None:
    caplog.set_level(logging.WARNING, logger=LOCK_CHECK_LOGGER)
    with _lock_check_workers(is_async=False) as workers:
        _check_step_cases(workers, caplog)
        _check_rpc_cases(workers, caplog)

def test_unlocked_read_modify_write_detection_with_async_worker(
    caplog: pytest.LogCaptureFixture,
) -> None:
    caplog.set_level(logging.WARNING, logger=LOCK_CHECK_LOGGER)
    with _lock_check_workers(is_async=True) as workers:
        _check_step_cases(workers, caplog)
        _check_rpc_cases(workers, caplog)
