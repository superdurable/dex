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
from typing import Any, cast
from uuid import uuid4

import pytest

from dex import (
    AsyncContext,
    AsyncWorker,
    Attribute,
    AttributeMap,
    AttributeMapNotLoadedError,
    BlobCacheConfig,
    Client,
    ClientOptions,
    Context,
    Flow,
    FlowResult,
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
STRICT_ERROR_TYPE = "dex.runtime_errors.UnlockedAttributeReadModifyWriteError"

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
    ENTRIES.set(context, instance, ENTRIES.get(context, instance) + 1)


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


class PyLockCheckExactInstanceStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _single_attempt_options(
            execute_load_attribute_map_instances=(ENTRIES.load("e1"),)
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        _increment_entry(context, "e1")
        return graceful_complete()


class PyLockCheckReadThenDeleteStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _single_attempt_options()

    def execute(self, context: Context, input: None) -> StepDecision:
        COUNTER.get(context)
        COUNTER.delete(context)
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


class PyLockCheckUnloadedMapReadStep(Step[None]):
    """Reads an instance it did not load; that failed read is not a snapshot read."""

    def get_step_options(self) -> StepOptions | None:
        return _single_attempt_options()

    def execute(self, context: Context, input: None) -> StepDecision:
        try:
            ENTRIES.get(context, "e1")
        except AttributeMapNotLoadedError:
            pass
        else:
            raise AssertionError("expected AttributeMapNotLoadedError")
        ENTRIES.set(context, "e1", 1)
        return graceful_complete()


class PyLockCheckHandlerErrorStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _single_attempt_options()

    def execute(self, context: Context, input: None) -> StepDecision:
        _increment(context)
        raise RuntimeError("lock check handler error")


class PyLockCheckRecoveryStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        recovery = context.recovery_error
        assert recovery is not None
        return graceful_complete(recovery.error_type)


class PyLockCheckRetriedStep(Step[None]):
    """Fails in strict mode on both attempts, then routes to the recovery Step."""

    def get_step_options(self) -> StepOptions | None:
        return StepOptions(
            execute_retry=RetryPolicy(
                initial_interval=timedelta(seconds=1),
                maximum_interval=timedelta(seconds=1),
                maximum_attempts=2,
            )
        ).on_execute_failure_proceed_to(PyLockCheckRecoveryStep)

    def execute(self, context: Context, input: None) -> StepDecision:
        _increment(context)
        return graceful_complete("first attempt succeeded")


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
            "unlocked-exact-instance": PyLockCheckExactInstanceStep(),
            "read-then-delete": PyLockCheckReadThenDeleteStep(),
            "locked": PyLockCheckLockedStep(),
            "locked-map": PyLockCheckLockedMapStep(),
            "single-writer": PyLockCheckSingleWriterStep(),
            "read-or-write-only": PyLockCheckReadOrWriteOnlyStep(),
            "unloaded-map-read": PyLockCheckUnloadedMapReadStep(),
            "handler-error": PyLockCheckHandlerErrorStep(),
            "retried": PyLockCheckRetriedStep(),
        }
        self.recovery = PyLockCheckRecoveryStep()
        self.dispatch = PyLockCheckDispatchStep(self.targets)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.dispatch).other_steps(
            *self.targets.values(), self.recovery
        )

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

    @rpc(load_attribute_maps=(ENTRIES,))
    def increment_entries_except_e2(
        self, context: Context, instances: list[str]
    ) -> None:
        for instance in instances:
            _increment_entry(context, instance)
        ENTRIES.declare_single_writer(context, "e2")


class PyLockCheckAsyncUnlockedStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _single_attempt_options()

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> StepDecision:
        await asyncio.sleep(0)
        _increment(context)
        return graceful_complete()


class PyLockCheckAsyncStepFlow(Flow[None]):
    """Runs a real ``async def`` Step on an AsyncWorker."""

    def __init__(self) -> None:
        self.step = PyLockCheckAsyncUnlockedStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(COUNTER, OTHER, ENTRIES)


class PyLockCheckAsyncRpcFlow(Flow[None]):
    """Has no Steps and serves a real ``async def`` RPC on an AsyncWorker."""

    def get_steps(self) -> StepList[None]:
        return StepList.empty()

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(COUNTER, OTHER, ENTRIES)

    @rpc
    async def async_unlocked_increment(self, context: Context) -> None:
        await asyncio.sleep(0)
        _increment(context)


STEP_FLOW = PyLockCheckStepFlow()
RPC_FLOW = PyLockCheckRpcFlow()
ASYNC_STEP_FLOW = PyLockCheckAsyncStepFlow()
ASYNC_RPC_FLOW = PyLockCheckAsyncRpcFlow()


def _server_address() -> str:
    return os.environ.get("DEX_SERVER_ADDRESS", "127.0.0.1:8801")


def _registry(is_async: bool) -> Registry:
    if is_async:
        return Registry(
            (STEP_FLOW, RPC_FLOW, ASYNC_STEP_FLOW, ASYNC_RPC_FLOW),
            allow_async_handlers=True,
        )
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
        try:
            worker = Worker(
                _registry(False), cache, _worker_options(task_queue, is_strict)
            )
            failures: list[BaseException] = []

            def run_worker() -> None:
                try:
                    worker.start()
                except BaseException as error:
                    failures.append(error)

            thread: threading.Thread | None = None
            try:
                started_thread = threading.Thread(
                    target=run_worker, name=f"lock-check-{task_queue}", daemon=True
                )
                started_thread.start()
                thread = started_thread
                worker.wait_pull_registered(timeout=30)
                yield worker
            finally:
                try:
                    worker.close()
                finally:
                    if thread is not None:
                        thread.join(timeout=10)
                        assert not thread.is_alive(), "lock check Worker did not stop"
            if failures:
                raise RuntimeError("lock check Worker failed") from failures[0]
        finally:
            cache.close()


@contextmanager
def _async_worker(task_queue: str, is_strict: bool) -> Iterator[AsyncWorker]:
    with TemporaryDirectory(prefix="dex-python-lock-check-async-") as cache_directory:
        cache = open_blob_cache(BlobCacheConfig(cache_directory, 16 * 1024 * 1024))
        try:
            loop = asyncio.new_event_loop()
            loop_thread: threading.Thread | None = None
            try:
                started_thread = threading.Thread(
                    target=loop.run_forever,
                    name=f"lock-check-async-{task_queue}",
                    daemon=True,
                )
                started_thread.start()
                loop_thread = started_thread

                async def create_worker() -> AsyncWorker:
                    return AsyncWorker(
                        _registry(True), cache, _worker_options(task_queue, is_strict)
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
                    try:
                        asyncio.run_coroutine_threadsafe(worker.close(), loop).result(
                            timeout=20
                        )
                    finally:
                        serving.result(timeout=20)
            finally:
                try:
                    if loop_thread is not None:
                        loop.call_soon_threadsafe(loop.stop)
                finally:
                    if loop_thread is not None:
                        loop_thread.join(timeout=10)
                    is_loop_stopped = loop_thread is None or not loop_thread.is_alive()
                    if is_loop_stopped:
                        loop.close()
                    assert is_loop_stopped, "lock check event loop did not stop"
        finally:
            cache.close()


@contextmanager
def _client(is_async: bool) -> Iterator[Client]:
    with TemporaryDirectory(prefix="dex-python-lock-check-client-") as cache_directory:
        cache = open_blob_cache(BlobCacheConfig(cache_directory, 16 * 1024 * 1024))
        try:
            client = Client(
                _registry(is_async), cache, ClientOptions(_server_address())
            )
            try:
                yield client
            finally:
                client.close()
        finally:
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
        client = stack.enter_context(_client(is_async))
        yield _LockCheckWorkers(
            client,
            warn_queue,
            lambda: warn_worker.unlocked_attribute_read_modify_write_count,
            strict_queue,
            lambda: strict_worker.unlocked_attribute_read_modify_write_count,
        )


def _run_flow(
    client: Client, flow: Flow[Any], task_queue: str, input: object
) -> tuple[str, FlowResult]:
    flow_id = unique_id("py-lock-check")
    client.start_flow(flow, flow_id, input, StartFlowOptions(task_queue=task_queue))
    return flow_id, client.wait_for_flow(flow_id, WAIT_TIMEOUT)


def _counter(client: Client, flow_id: str) -> int | None:
    return client.get_attributes(flow_id, COUNTER).get(COUNTER)


def _written_value(client: Client, flow_id: str, instance: str | None) -> int | None:
    """Read the counter for ``None``, or one ``ENTRIES`` instance."""
    if instance is None:
        return _counter(client, flow_id)
    entry = ENTRIES.instance(instance)
    return client.get_attributes(flow_id, entry).get(entry)


def _warning_count(
    caplog: pytest.LogCaptureFixture, handler: str, attribute: str
) -> int:
    return sum(
        f"handler={handler} attribute={attribute} " in record.getMessage()
        for record in caplog.records
        if record.name == LOCK_CHECK_LOGGER
    )


STEP_CASES: list[tuple[str, str | None, str | None, bool]] = [
    ("unlocked", "PyLockCheckUnlockedStep.execute", COUNTER.name, False),
    (
        "unlocked-wait-for",
        "PyLockCheckUnlockedWaitForStep.wait_for",
        COUNTER.name,
        False,
    ),
    ("unlocked-map", "PyLockCheckUnlockedMapStep.execute", ENTRIES.name, False),
    (
        "unlocked-exact-instance",
        "PyLockCheckExactInstanceStep.execute",
        ENTRIES.name,
        False,
    ),
    ("read-then-delete", "PyLockCheckReadThenDeleteStep.execute", COUNTER.name, False),
    ("locked", None, None, False),
    ("locked-map", None, None, False),
    ("single-writer", None, None, False),
    ("read-or-write-only", None, None, False),
    ("unloaded-map-read", None, None, False),
    ("handler-error", None, None, True),
]


def _check_step_cases(
    workers: _LockCheckWorkers, caplog: pytest.LogCaptureFixture
) -> None:
    for step_case, handler, attribute, is_failing in STEP_CASES:
        before = workers.warn_count()
        _, result = _run_flow(workers.client, STEP_FLOW, workers.warn_queue, step_case)
        expected = FlowStatus.FAILED if is_failing else FlowStatus.COMPLETED
        assert result.status is expected, step_case
        detections = workers.warn_count() - before
        if handler is not None and attribute is not None:
            assert detections == 1, step_case
            assert _warning_count(caplog, handler, attribute) == 1, step_case
        else:
            assert detections == 0, step_case

        before = workers.strict_count()
        _, result = _run_flow(
            workers.client, STEP_FLOW, workers.strict_queue, step_case
        )
        detections = workers.strict_count() - before
        if handler is not None or is_failing:
            assert result.status is FlowStatus.FAILED, step_case
        else:
            assert result.status is FlowStatus.COMPLETED, step_case
        assert detections == (1 if handler is not None else 0), step_case


def _check_warns_once_and_commits(
    workers: _LockCheckWorkers, caplog: pytest.LogCaptureFixture
) -> None:
    for _ in range(2):
        before = workers.warn_count()
        flow_id, result = _run_flow(
            workers.client, STEP_FLOW, workers.warn_queue, "unlocked"
        )
        assert result.status is FlowStatus.COMPLETED
        assert workers.warn_count() - before == 1
        assert _counter(workers.client, flow_id) == 1, "warning mode commits"
    assert _warning_count(caplog, "PyLockCheckUnlockedStep.execute", COUNTER.name) == 1


def _check_strict_retries_then_recovers(workers: _LockCheckWorkers) -> None:
    before = workers.strict_count()
    flow_id, result = _run_flow(
        workers.client, STEP_FLOW, workers.strict_queue, "retried"
    )
    assert result.status is FlowStatus.COMPLETED
    assert workers.strict_count() - before == 2
    assert result.single_output(str) == STRICT_ERROR_TYPE
    assert _counter(workers.client, flow_id) is None, "strict mode discards writes"


def _invoke_on_queue(
    client: Client, task_queue: str, invoke: Callable[[str], None]
) -> tuple[str, WorkerInvocationError | None]:
    """Invoke an RPC on a new RPC Flow and return the Flow ID and any Worker error."""
    flow_id = unique_id("py-lock-check-rpc")
    client.start_flow(RPC_FLOW, flow_id, None, StartFlowOptions(task_queue=task_queue))
    try:
        invoke(flow_id)
    except WorkerInvocationError as error:
        return flow_id, error
    finally:
        client.stop_flow(flow_id)
    return flow_id, None


def _rpc_cases(
    client: Client,
) -> list[tuple[str, str | None, str | None, str | None, Callable[[str], None]]]:
    """Return name, handler label, reported Attribute, written instance, and invoker."""
    return [
        (
            "unlocked",
            "rpc.unlocked_increment",
            COUNTER.name,
            None,
            lambda flow_id: client.invoke_rpc(RPC_FLOW.unlocked_increment, flow_id),
        ),
        (
            "registered-map-load",
            "rpc.registered_load_entry_increment",
            ENTRIES.name,
            "e1",
            lambda flow_id: client.invoke_rpc(
                RPC_FLOW.registered_load_entry_increment, flow_id, "e1"
            ),
        ),
        (
            "locked",
            None,
            None,
            None,
            lambda flow_id: client.invoke_rpc(RPC_FLOW.locked_increment, flow_id),
        ),
        (
            "runtime-selected-map-lock",
            None,
            None,
            "e1",
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
    for name, handler, attribute, written_instance, invoke in _rpc_cases(
        workers.client
    ):
        before = workers.warn_count()
        flow_id, error = _invoke_on_queue(workers.client, workers.warn_queue, invoke)
        assert error is None, name
        detections = workers.warn_count() - before
        if handler is not None and attribute is not None:
            assert detections == 1, name
            assert _warning_count(caplog, handler, attribute) == 1, name
        else:
            assert detections == 0, name
        assert _written_value(workers.client, flow_id, written_instance) == 1, name

        before = workers.strict_count()
        flow_id, error = _invoke_on_queue(workers.client, workers.strict_queue, invoke)
        if handler is None or attribute is None:
            assert error is None, name
            assert workers.strict_count() - before == 0, name
            assert _written_value(workers.client, flow_id, written_instance) == 1, name
            continue
        assert error is not None, name
        assert workers.strict_count() - before == 1, name
        assert error.worker_error_type == STRICT_ERROR_TYPE, name
        assert attribute in error.worker_error_detail, name
        assert _written_value(workers.client, flow_id, written_instance) is None, name


def _check_several_map_instances(
    workers: _LockCheckWorkers, caplog: pytest.LogCaptureFixture
) -> None:
    instances = ["e3", "e1", "e2"]

    def invoke(flow_id: str) -> None:
        workers.client.invoke_rpc(
            RPC_FLOW.increment_entries_except_e2, flow_id, instances
        )

    for _ in range(2):
        before = workers.warn_count()
        flow_id, error = _invoke_on_queue(workers.client, workers.warn_queue, invoke)
        assert error is None
        assert workers.warn_count() - before == 2
        for instance in instances:
            assert _written_value(workers.client, flow_id, instance) == 1, instance
    assert _warning_count(caplog, "rpc.increment_entries_except_e2", ENTRIES.name) == 1

    before = workers.strict_count()
    flow_id, error = _invoke_on_queue(workers.client, workers.strict_queue, invoke)
    assert error is not None
    assert workers.strict_count() - before == 2
    # Strict mode discards every write of the call, including the declared e2.
    for instance in instances:
        assert _written_value(workers.client, flow_id, instance) is None, instance
    detail = error.worker_error_detail
    assert "instance 'e2'" not in detail
    assert 0 <= detail.index("instance 'e1'") < detail.index("instance 'e3'")


def _check_async_handlers(
    workers: _LockCheckWorkers, caplog: pytest.LogCaptureFixture
) -> None:
    before = workers.warn_count()
    _, result = _run_flow(workers.client, ASYNC_STEP_FLOW, workers.warn_queue, None)
    assert result.status is FlowStatus.COMPLETED
    assert workers.warn_count() - before == 1
    assert (
        _warning_count(caplog, "PyLockCheckAsyncUnlockedStep.execute", COUNTER.name)
        == 1
    )

    before = workers.strict_count()
    _, result = _run_flow(workers.client, ASYNC_STEP_FLOW, workers.strict_queue, None)
    assert result.status is FlowStatus.FAILED
    assert workers.strict_count() - before == 1

    # The synchronous Client invokes the async handler by its registered RPC name.
    async_rpc = cast(Callable[[Context], None], ASYNC_RPC_FLOW.async_unlocked_increment)

    def invoke(flow_id: str) -> None:
        workers.client.invoke_rpc(async_rpc, flow_id)

    for task_queue, count in (
        (workers.warn_queue, workers.warn_count),
        (workers.strict_queue, workers.strict_count),
    ):
        before = count()
        flow_id = unique_id("py-lock-check-async-rpc")
        workers.client.start_flow(
            ASYNC_RPC_FLOW, flow_id, None, StartFlowOptions(task_queue=task_queue)
        )
        try:
            if task_queue == workers.strict_queue:
                with pytest.raises(WorkerInvocationError) as raised:
                    invoke(flow_id)
                assert raised.value.worker_error_type == STRICT_ERROR_TYPE
            else:
                invoke(flow_id)
        finally:
            workers.client.stop_flow(flow_id)
        assert count() - before == 1


def test_unlocked_read_modify_write_detection_with_worker(
    caplog: pytest.LogCaptureFixture,
) -> None:
    caplog.set_level(logging.WARNING, logger=LOCK_CHECK_LOGGER)
    with _lock_check_workers(is_async=False) as workers:
        _check_step_cases(workers, caplog)
        _check_rpc_cases(workers, caplog)
        _check_several_map_instances(workers, caplog)
        _check_strict_retries_then_recovers(workers)
    caplog.clear()
    with _lock_check_workers(is_async=False) as workers:
        _check_warns_once_and_commits(workers, caplog)


def test_unlocked_read_modify_write_detection_with_async_worker(
    caplog: pytest.LogCaptureFixture,
) -> None:
    caplog.set_level(logging.WARNING, logger=LOCK_CHECK_LOGGER)
    with _lock_check_workers(is_async=True) as workers:
        _check_step_cases(workers, caplog)
        _check_rpc_cases(workers, caplog)
        _check_async_handlers(workers, caplog)


class _FailingLogHandler(logging.Handler):
    def emit(self, record: logging.LogRecord) -> None:
        raise RuntimeError("lock check log handler failure")


def test_failing_log_handler_keeps_successful_invocation() -> None:
    logger = logging.getLogger(LOCK_CHECK_LOGGER)
    handler = _FailingLogHandler()
    logger.addHandler(handler)
    try:
        with _lock_check_workers(is_async=False) as workers:
            flow_id, result = _run_flow(
                workers.client, STEP_FLOW, workers.warn_queue, "unlocked"
            )
            assert result.status is FlowStatus.COMPLETED
            assert _counter(workers.client, flow_id) == 1
    finally:
        logger.removeHandler(handler)
