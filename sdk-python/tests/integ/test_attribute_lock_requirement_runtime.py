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
import os
import queue
import threading
from collections.abc import Callable, Iterator
from contextlib import ExitStack, contextmanager
from dataclasses import replace
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
    BlobCacheConfig,
    Channel,
    ChannelMap,
    Client,
    ClientOptions,
    Context,
    Flow,
    FlowDefinitionError,
    FlowResult,
    FlowStatus,
    FlowTimeoutHandlerOptions,
    PersistenceSchema,
    PullWorkerOptions,
    Registry,
    RetryPolicy,
    RPCInvokeOptions,
    RpcLockConflictError,
    RPCResult,
    StartFlowOptions,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    StepOptions,
    SubFlow,
    Wait,
    WaitForFailurePolicy,
    Worker,
    WorkerInvocationError,
    WorkerOptions,
    dead_end,
    force_complete,
    go_to,
    go_to_many,
    graceful_complete,
    open_blob_cache,
    rpc,
)

from .shared import unique_id

WAIT_TIMEOUT = timedelta(seconds=30)
MISSING_LOCK_ERROR_TYPE = "dex.runtime_errors.MissingAttributeLockError"
# Needs percent-encoding, so the test covers escaped map lock keys.
SPACED_INSTANCE = "item a"

COORDINATION = Attribute("py-lock-req-coordination", bool)
TOTAL = Attribute("py-lock-req-total", int, writes_require_lock=True)
ITEMS = AttributeMap("py-lock-req-items", int, writes_require_lock=True)
SHARED = AttributeMap("py-lock-req-shared", int, writes_require_lock=COORDINATION)
FREE = Attribute("py-lock-req-free", int)
REPLIES = Channel("py-lock-req-replies", str, publishes_require_lock=COORDINATION)
EVENTS = ChannelMap("py-lock-req-events", str, publishes_require_lock=COORDINATION)
FREE_CHANNEL = Channel("py-lock-req-free-channel", str)
SCHEMA = PersistenceSchema.of(
    COORDINATION, TOTAL, ITEMS, SHARED, FREE, REPLIES, EVENTS, FREE_CHANNEL
)


class PyLockReqRecoveryStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        recovery = context.recovery_error
        assert recovery is not None
        return graceful_complete(f"{recovery.error_type}: {recovery.detail}")


def _options(*locks: Any) -> StepOptions:
    """Run one attempt, hold ``locks`` in execute, and recover from an execute failure."""
    return StepOptions(
        wait_for_retry=RetryPolicy(maximum_attempts=1),
        execute_retry=RetryPolicy(maximum_attempts=1),
        execute_lock_attributes=tuple(locks),
    ).on_execute_failure_proceed_to(PyLockReqRecoveryStep)


class PyLockReqUnlockedTotalStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _options()

    def execute(self, context: Context, input: None) -> StepDecision:
        TOTAL.set(context, 1)
        return graceful_complete("written")


def _wait_for_options(execute_locks: Any, wait_for_locks: Any) -> StepOptions:
    """Proceed to execute after a failed wait_for, so execute reports the failure."""
    return replace(
        _options(*execute_locks),
        wait_for_failure=WaitForFailurePolicy.PROCEED,
        wait_for_lock_attributes=tuple(wait_for_locks),
    )


def _wait_for_result(context: Context | AsyncContext) -> StepDecision:
    """Complete with the wait_for failure, or ``"written"`` when wait_for succeeded."""
    if not context.wait_for_method_failed():
        return graceful_complete("written")
    recovery = context.recovery_error
    assert recovery is not None
    return graceful_complete(f"{recovery.error_type}: {recovery.detail}")


class PyLockReqWaitForTotalStep(Step[None]):
    """Registers the lock for execute only, so its wait_for write fails."""

    def get_step_options(self) -> StepOptions | None:
        return _wait_for_options((TOTAL.lock(),), ())

    def wait_for(self, context: Context, input: None) -> Wait:
        TOTAL.set(context, 1)
        return Wait.skip_immediately()

    def execute(self, context: Context, input: None) -> StepDecision:
        return _wait_for_result(context)


class PyLockReqLockedWaitForTotalStep(Step[None]):
    """Registers the lock for wait_for, so its wait_for write commits."""

    def get_step_options(self) -> StepOptions | None:
        return _wait_for_options((), (TOTAL.lock(),))

    def wait_for(self, context: Context, input: None) -> Wait:
        TOTAL.set(context, 1)
        return Wait.skip_immediately()

    def execute(self, context: Context, input: None) -> StepDecision:
        return _wait_for_result(context)


class PyLockReqLockedTotalStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _options(TOTAL.lock())

    def execute(self, context: Context, input: None) -> StepDecision:
        TOTAL.set(context, 1)
        return graceful_complete("written")


class PyLockReqDeleteTotalStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _options()

    def execute(self, context: Context, input: None) -> StepDecision:
        TOTAL.delete(context)
        return graceful_complete("written")


class PyLockReqLockedItemStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _options(ITEMS.lock(SPACED_INSTANCE))

    def execute(self, context: Context, input: None) -> StepDecision:
        ITEMS.set(context, SPACED_INSTANCE, 1)
        return graceful_complete("written")


class PyLockReqOtherItemStep(Step[None]):
    """Holds one instance's lock and writes another instance."""

    def get_step_options(self) -> StepOptions | None:
        return _options(ITEMS.lock(SPACED_INSTANCE))

    def execute(self, context: Context, input: None) -> StepDecision:
        ITEMS.set(context, SPACED_INSTANCE, 1)
        ITEMS.set(context, "item b", 1)
        return graceful_complete("written")


class PyLockReqLockedSharedStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _options(COORDINATION.lock())

    def execute(self, context: Context, input: None) -> StepDecision:
        SHARED.set(context, "x", 1)
        EVENTS.publish(context, "x", "shared written")
        return graceful_complete("written")


class PyLockReqUnlockedSharedStep(Step[None]):
    """Holds the written instance's own lock, not the declared coordination lock."""

    def get_step_options(self) -> StepOptions | None:
        return _options(SHARED.lock("x"))

    def execute(self, context: Context, input: None) -> StepDecision:
        SHARED.set(context, "x", 1)
        return graceful_complete("written")


class PyLockReqUndeclaredStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _options()

    def execute(self, context: Context, input: None) -> StepDecision:
        FREE.set(context, 1)
        FREE_CHANNEL.publish(context, "free")
        return graceful_complete("written")


class PyLockReqDispatchStep(Step[str]):
    def __init__(self, targets: dict[str, Step[None]]) -> None:
        self._targets = targets

    def execute(self, context: Context, input: str) -> StepDecision:
        if input == "movement-lock-total":
            # A per-movement override does not count, even when it names the required lock.
            return go_to_many(
                StepMovement(
                    self._targets["unlocked-total"], None, _options(TOTAL.lock())
                )
            )
        return go_to(self._targets[input], None)


class PyLockReqStepFlow(Flow[str]):
    def __init__(self) -> None:
        self.targets: dict[str, Step[None]] = {
            "unlocked-total": PyLockReqUnlockedTotalStep(),
            "wait-for-total": PyLockReqWaitForTotalStep(),
            "locked-wait-for-total": PyLockReqLockedWaitForTotalStep(),
            "locked-total": PyLockReqLockedTotalStep(),
            "delete-total": PyLockReqDeleteTotalStep(),
            "locked-item": PyLockReqLockedItemStep(),
            "other-item": PyLockReqOtherItemStep(),
            "locked-shared": PyLockReqLockedSharedStep(),
            "unlocked-shared": PyLockReqUnlockedSharedStep(),
            "undeclared": PyLockReqUndeclaredStep(),
        }
        self.recovery = PyLockReqRecoveryStep()
        self.dispatch = PyLockReqDispatchStep(self.targets)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.dispatch).other_steps(
            *self.targets.values(), self.recovery
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return SCHEMA


class PyLockReqRpcFlow(Flow[None]):
    """Has no Steps, so it stays open for RPC cases."""

    def get_steps(self) -> StepList[None]:
        return StepList.empty()

    def get_persistence_schema(self) -> PersistenceSchema:
        return SCHEMA

    @rpc
    def publish_reply_unlocked(self, context: Context, reply: str) -> None:
        # The rejected publish must also discard this undeclared write.
        FREE.set(context, 1)
        REPLIES.publish(context, reply)

    @rpc
    def count_replies(self, context: Context) -> RPCResult[int]:
        return RPCResult(REPLIES.size(context))

    @rpc(lock_attributes=(COORDINATION.lock(),))
    def publish_reply_locked(self, context: Context, reply: str) -> None:
        REPLIES.publish(context, reply)

    @rpc
    def set_item(self, context: Context, instance: str) -> None:
        ITEMS.set(context, instance, 1)

    @rpc(lock_attributes=(ITEMS.lock(SPACED_INSTANCE),))
    def set_item_locked(self, context: Context, instance: str) -> None:
        ITEMS.set(context, instance, 1)

    @rpc
    def set_free(self, context: Context) -> None:
        FREE.set(context, 1)
        FREE_CHANNEL.publish(context, "free")

    @rpc
    def publish_event_unlocked(self, context: Context, instance: str) -> None:
        # The rejected publish must also discard this undeclared write.
        FREE.set(context, 1)
        EVENTS.publish(context, instance, "event")

    @rpc
    def count_events(self, context: Context, instance: str) -> RPCResult[int]:
        return RPCResult(EVENTS.size(context, instance))


class PyLockReqTimeoutFlow(Flow[None]):
    """Writes a declared Attribute from its unchecked timeout handler."""

    def get_steps(self) -> StepList[None]:
        return StepList.empty()

    def get_persistence_schema(self) -> PersistenceSchema:
        return SCHEMA

    def handle_timeout(self, context: Context) -> StepDecision:
        TOTAL.set(context, 5)
        return force_complete("expired")


class PyLockReqAsyncStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return _options()

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> StepDecision:
        await asyncio.sleep(0)
        TOTAL.set(context, 1)
        return graceful_complete("written")


class PyLockReqAsyncWaitForStep(Step[None]):
    """Runs a real ``async def`` wait_for that writes without its lock."""

    def get_step_options(self) -> StepOptions | None:
        return _wait_for_options((TOTAL.lock(),), ())

    async def wait_for(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> Wait:
        await asyncio.sleep(0)
        TOTAL.set(context, 1)
        return Wait.skip_immediately()

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> StepDecision:
        return _wait_for_result(context)


class PyLockReqAsyncDispatchStep(Step[str]):
    def __init__(self, targets: dict[str, Step[None]]) -> None:
        self._targets = targets

    def execute(self, context: Context, input: str) -> StepDecision:
        return go_to(self._targets[input], None)


class PyLockReqAsyncStepFlow(Flow[str]):
    """Runs real ``async def`` Step methods on an AsyncWorker."""

    def __init__(self) -> None:
        self.targets: dict[str, Step[None]] = {
            "execute": PyLockReqAsyncStep(),
            "wait-for": PyLockReqAsyncWaitForStep(),
        }
        self.recovery = PyLockReqRecoveryStep()
        self.dispatch = PyLockReqAsyncDispatchStep(self.targets)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.dispatch).other_steps(
            *self.targets.values(), self.recovery
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return SCHEMA


class PyLockReqAsyncRpcFlow(Flow[None]):
    """Has no Steps and serves real ``async def`` RPCs on an AsyncWorker."""

    def get_steps(self) -> StepList[None]:
        return StepList.empty()

    def get_persistence_schema(self) -> PersistenceSchema:
        return SCHEMA

    @rpc
    async def async_publish_unlocked(self, context: Context, reply: str) -> None:
        await asyncio.sleep(0)
        REPLIES.publish(context, reply)

    @rpc(lock_attributes=(COORDINATION.lock(),))
    async def async_publish_locked(self, context: Context, reply: str) -> None:
        await asyncio.sleep(0)
        REPLIES.publish(context, reply)


STEP_FLOW = PyLockReqStepFlow()
RPC_FLOW = PyLockReqRpcFlow()
ASYNC_STEP_FLOW = PyLockReqAsyncStepFlow()
ASYNC_RPC_FLOW = PyLockReqAsyncRpcFlow()
TIMEOUT_FLOW = PyLockReqTimeoutFlow()


def _server_address() -> str:
    return os.environ.get("DEX_SERVER_ADDRESS", "127.0.0.1:8801")


def _registry(is_async: bool) -> Registry:
    if is_async:
        return Registry(
            (STEP_FLOW, RPC_FLOW, ASYNC_STEP_FLOW, ASYNC_RPC_FLOW),
            allow_async_handlers=True,
        )
    return Registry((STEP_FLOW, RPC_FLOW, TIMEOUT_FLOW))


def _worker_options(task_queue: str) -> WorkerOptions:
    return WorkerOptions(
        server_address=_server_address(),
        pull=PullWorkerOptions(max_concurrent_tasks=2, task_queue=task_queue),
    )


@contextmanager
def _sync_worker(task_queue: str, registry: Registry) -> Iterator[Worker]:
    with TemporaryDirectory(prefix="dex-python-lock-req-cache-") as cache_directory:
        cache = open_blob_cache(BlobCacheConfig(cache_directory, 16 * 1024 * 1024))
        try:
            worker = Worker(registry, cache, _worker_options(task_queue))
            failures: list[BaseException] = []

            def run_worker() -> None:
                try:
                    worker.start()
                except BaseException as error:
                    failures.append(error)

            thread: threading.Thread | None = None
            try:
                started_thread = threading.Thread(
                    target=run_worker, name=f"lock-req-{task_queue}", daemon=True
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
                        assert (
                            not thread.is_alive()
                        ), "lock requirement Worker did not stop"
            if failures:
                raise RuntimeError("lock requirement Worker failed") from failures[0]
        finally:
            cache.close()


@contextmanager
def _async_worker(task_queue: str, registry: Registry) -> Iterator[AsyncWorker]:
    with TemporaryDirectory(prefix="dex-python-lock-req-async-") as cache_directory:
        cache = open_blob_cache(BlobCacheConfig(cache_directory, 16 * 1024 * 1024))
        try:
            loop = asyncio.new_event_loop()
            loop_thread: threading.Thread | None = None
            try:
                started_thread = threading.Thread(
                    target=loop.run_forever,
                    name=f"lock-req-async-{task_queue}",
                    daemon=True,
                )
                started_thread.start()
                loop_thread = started_thread

                async def create_worker() -> AsyncWorker:
                    return AsyncWorker(registry, cache, _worker_options(task_queue))

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
                    assert is_loop_stopped, "lock requirement event loop did not stop"
        finally:
            cache.close()


@contextmanager
def _client(registry: Registry) -> Iterator[Client]:
    with TemporaryDirectory(prefix="dex-python-lock-req-client-") as cache_directory:
        cache = open_blob_cache(BlobCacheConfig(cache_directory, 16 * 1024 * 1024))
        try:
            client = Client(registry, cache, ClientOptions(_server_address()))
            try:
                yield client
            finally:
                client.close()
        finally:
            cache.close()


@contextmanager
def _harness_with_registries(
    worker_registry: Registry, client_registry: Registry, is_async: bool
) -> Iterator[tuple[Client, str]]:
    task_queue = f"py-lock-req-{uuid4().hex[:12]}"
    with ExitStack() as stack:
        if is_async:
            stack.enter_context(_async_worker(task_queue, worker_registry))
        else:
            stack.enter_context(_sync_worker(task_queue, worker_registry))
        yield stack.enter_context(_client(client_registry)), task_queue


@contextmanager
def _harness(is_async: bool) -> Iterator[tuple[Client, str]]:
    with _harness_with_registries(
        _registry(is_async), _registry(is_async), is_async
    ) as harness:
        yield harness


def _run_flow(
    client: Client, flow: Flow[Any], task_queue: str, input: object
) -> tuple[str, FlowResult]:
    flow_id = unique_id("py-lock-req")
    client.start_flow(flow, flow_id, input, StartFlowOptions(task_queue=task_queue))
    return flow_id, client.wait_for_flow(flow_id, WAIT_TIMEOUT)


def _start_rpc_flow(client: Client, task_queue: str, flow: Flow[None]) -> str:
    flow_id = unique_id("py-lock-req-rpc")
    client.start_flow(flow, flow_id, None, StartFlowOptions(task_queue=task_queue))
    return flow_id


def _is_set(client: Client, flow_id: str, key: Any) -> bool:
    return client.get_attributes(flow_id, key).get(key) is not None


def _single_output(result: FlowResult) -> str:
    assert result.status is FlowStatus.COMPLETED, result.error_message
    return result.single_output(str)


# Case, expected detail fragment (None when the Step succeeds), and written keys.
STEP_CASES: list[tuple[str, str | None, list[Any]]] = [
    (
        "unlocked-total",
        "Step 'PyLockReqUnlockedTotalStep' execute wrote Attribute "
        "'py-lock-req-total' without the required lock of Attribute "
        "'py-lock-req-total'",
        [TOTAL],
    ),
    ("movement-lock-total", "wrote Attribute 'py-lock-req-total'", [TOTAL]),
    ("locked-total", None, [TOTAL]),
    (
        "locked-item",
        None,
        [ITEMS.instance(SPACED_INSTANCE)],
    ),
    (
        "other-item",
        "wrote AttributeMap 'py-lock-req-items' instance 'item b' without the "
        "required lock of AttributeMap 'py-lock-req-items' instance 'item b'",
        [ITEMS.instance(SPACED_INSTANCE), ITEMS.instance("item b")],
    ),
    ("locked-shared", None, [SHARED.instance("x")]),
    (
        "unlocked-shared",
        "wrote AttributeMap 'py-lock-req-shared' instance 'x' without the "
        "required lock of Attribute 'py-lock-req-coordination'",
        [SHARED.instance("x")],
    ),
    ("undeclared", None, [FREE]),
]


def test_declared_lock_requirements_on_steps() -> None:
    with _harness(is_async=False) as (client, task_queue):
        for name, missing, written in STEP_CASES:
            flow_id, result = _run_flow(client, STEP_FLOW, task_queue, name)
            output = _single_output(result)
            if missing is None:
                assert output == "written", name
            else:
                assert output.startswith(f"{MISSING_LOCK_ERROR_TYPE}: "), output
                assert missing in output, output
            for key in written:
                assert _is_set(client, flow_id, key) is (missing is None), name

        flow_id, result = _run_flow(client, STEP_FLOW, task_queue, "delete-total")
        output = _single_output(result)
        assert "wrote Attribute 'py-lock-req-total'" in output, output

        flow_id, result = _run_flow(client, STEP_FLOW, task_queue, "wait-for-total")
        output = _single_output(result)
        assert output.startswith(f"{MISSING_LOCK_ERROR_TYPE}: "), output
        assert (
            "Step 'PyLockReqWaitForTotalStep' wait_for wrote Attribute "
            "'py-lock-req-total'" in output
        ), output
        assert not _is_set(
            client, flow_id, TOTAL
        ), "the failed wait_for write is discarded"

        flow_id, result = _run_flow(
            client, STEP_FLOW, task_queue, "locked-wait-for-total"
        )
        assert _single_output(result) == "written"
        assert _is_set(client, flow_id, TOTAL)


def test_timeout_handler_is_not_checked() -> None:
    with _harness(is_async=False) as (client, task_queue):
        flow_id = unique_id("py-lock-req-timeout")
        client.start_flow(
            TIMEOUT_FLOW,
            flow_id,
            None,
            StartFlowOptions(task_queue=task_queue, timeout=timedelta(seconds=1)),
        )
        result = client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert _single_output(result) == "expired"
        assert client.get_attributes(flow_id, TOTAL).get(TOTAL) == 5


def _assert_missing_lock(
    raised: pytest.ExceptionInfo[WorkerInvocationError], fragment: str
) -> None:
    assert raised.value.worker_error_type == MISSING_LOCK_ERROR_TYPE
    assert fragment in (raised.value.worker_error_detail or "")


def test_declared_lock_requirements_on_rpcs() -> None:
    with _harness(is_async=False) as (client, task_queue):
        flow_id = _start_rpc_flow(client, task_queue, RPC_FLOW)
        client.invoke_rpc(RPC_FLOW.publish_reply_locked, flow_id, "hi")
        assert (
            client.invoke_rpc(RPC_FLOW.count_replies, flow_id) == 1
        ), "the locked publish commits"
        with pytest.raises(WorkerInvocationError) as raised:
            client.invoke_rpc(RPC_FLOW.publish_reply_unlocked, flow_id, "hi")
        _assert_missing_lock(
            raised,
            "RPC 'publish_reply_unlocked' published to Channel 'py-lock-req-replies' "
            "without the required lock of Attribute 'py-lock-req-coordination'",
        )
        assert (
            client.invoke_rpc(RPC_FLOW.count_replies, flow_id) == 1
        ), "the rejected publish is discarded"
        assert not _is_set(
            client, flow_id, FREE
        ), "the other buffered write is discarded"

        flow_id = _start_rpc_flow(client, task_queue, RPC_FLOW)
        with pytest.raises(WorkerInvocationError) as raised:
            client.invoke_rpc(RPC_FLOW.set_item, flow_id, SPACED_INSTANCE)
        _assert_missing_lock(
            raised, "wrote AttributeMap 'py-lock-req-items' instance 'item a'"
        )
        assert not _is_set(client, flow_id, ITEMS.instance(SPACED_INSTANCE))

        with pytest.raises(WorkerInvocationError) as raised:
            client.invoke_rpc(
                RPC_FLOW.set_item,
                flow_id,
                SPACED_INSTANCE,
                options=RPCInvokeOptions(
                    lock_attribute_map_instances=(ITEMS.lock(SPACED_INSTANCE),)
                ),
            )
        _assert_missing_lock(
            raised, "wrote AttributeMap 'py-lock-req-items' instance 'item a'"
        )
        assert not _is_set(
            client, flow_id, ITEMS.instance(SPACED_INSTANCE)
        ), "a call-time lock does not satisfy the declared requirement"

        client.invoke_rpc(RPC_FLOW.set_item_locked, flow_id, SPACED_INSTANCE)
        assert _is_set(client, flow_id, ITEMS.instance(SPACED_INSTANCE))

        client.invoke_rpc(RPC_FLOW.set_free, flow_id)
        assert _is_set(client, flow_id, FREE)

        flow_id = _start_rpc_flow(client, task_queue, RPC_FLOW)
        with pytest.raises(WorkerInvocationError) as raised:
            client.invoke_rpc(RPC_FLOW.publish_event_unlocked, flow_id, SPACED_INSTANCE)
        _assert_missing_lock(
            raised,
            "published to ChannelMap 'py-lock-req-events' instance 'item a' without "
            "the required lock of Attribute 'py-lock-req-coordination'",
        )
        assert (
            client.invoke_rpc(RPC_FLOW.count_events, flow_id, SPACED_INSTANCE) == 0
        ), "the rejected publish is discarded"
        assert not _is_set(
            client, flow_id, FREE
        ), "the other buffered write is discarded"


def test_declared_lock_requirements_on_async_worker() -> None:
    with _harness(is_async=True) as (client, task_queue):
        flow_id, result = _run_flow(client, ASYNC_STEP_FLOW, task_queue, "execute")
        output = _single_output(result)
        assert output.startswith(f"{MISSING_LOCK_ERROR_TYPE}: "), output
        assert "Step 'PyLockReqAsyncStep' execute wrote" in output, output
        assert not _is_set(client, flow_id, TOTAL)

        flow_id, result = _run_flow(client, ASYNC_STEP_FLOW, task_queue, "wait-for")
        output = _single_output(result)
        assert output.startswith(f"{MISSING_LOCK_ERROR_TYPE}: "), output
        assert "Step 'PyLockReqAsyncWaitForStep' wait_for wrote" in output, output
        assert not _is_set(client, flow_id, TOTAL)

        # The synchronous Client invokes the async handlers by their registered RPC names.
        unlocked = cast(
            Callable[[Context, str], None], ASYNC_RPC_FLOW.async_publish_unlocked
        )
        locked = cast(
            Callable[[Context, str], None], ASYNC_RPC_FLOW.async_publish_locked
        )
        flow_id = _start_rpc_flow(client, task_queue, ASYNC_RPC_FLOW)
        with pytest.raises(WorkerInvocationError) as raised:
            client.invoke_rpc(unlocked, flow_id, "hi")
        _assert_missing_lock(raised, "published to Channel 'py-lock-req-replies'")
        client.invoke_rpc(locked, flow_id, "hi")


class _SnapshotControl:
    """Changes Step options after registration and pauses a Step in execute."""

    def __init__(self) -> None:
        self.options_changed = False
        self._entered: queue.Queue[str] = queue.Queue()
        self._release = threading.Event()

    def step_options(self) -> StepOptions:
        """Hold the total lock until the options change after registration."""
        locks = () if self.options_changed else (TOTAL.lock(),)
        return StepOptions(
            execute_retry=RetryPolicy(maximum_attempts=1),
            execute_lock_attributes=locks,
        )

    def pause(self, flow_id: str) -> None:
        self._entered.put(flow_id)
        if not self._release.wait(timeout=45):
            raise RuntimeError("the paused Step was not released")

    def arm(self) -> None:
        self._release.clear()

    def wait_entered(self) -> str:
        return self._entered.get(timeout=30)

    def release(self) -> None:
        self._release.set()

    def reset(self) -> None:
        self.options_changed = False
        self._release.clear()
        self._entered = queue.Queue()


SNAPSHOT = _SnapshotControl()


class PyLockSnapGatedStep(Step[None]):
    """Writes the total after the test releases it; only registration sees its lock."""

    def get_step_options(self) -> StepOptions | None:
        return SNAPSHOT.step_options()

    def execute(self, context: Context, input: None) -> StepDecision:
        SNAPSHOT.pause(context.flow_id)
        TOTAL.set(context, 1)
        return graceful_complete("written")


class PyLockSnapAsyncGatedStep(Step[None]):
    def get_step_options(self) -> StepOptions | None:
        return SNAPSHOT.step_options()

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: None
    ) -> StepDecision:
        await asyncio.to_thread(SNAPSHOT.pause, context.flow_id)
        TOTAL.set(context, 1)
        return graceful_complete("written")


class PyLockSnapFailingStep(Step[None]):
    """Fails once, so its execute failure route moves to the gated Step."""

    def __init__(self, recovery: Step[None]) -> None:
        self._recovery = recovery

    def get_step_options(self) -> StepOptions | None:
        return StepOptions(
            execute_retry=RetryPolicy(maximum_attempts=1)
        ).on_execute_failure_proceed_to(self._recovery)

    def execute(self, context: Context, input: None) -> StepDecision:
        raise RuntimeError("route to the execute failure target")


class PyLockSnapStartFlow(Flow[None]):
    """Starts in the gated Step, as a Flow or as a SubFlow."""

    def __init__(self) -> None:
        self.gated = PyLockSnapGatedStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.gated)

    def get_persistence_schema(self) -> PersistenceSchema:
        return SCHEMA

    @rpc(lock_attributes=(TOTAL.lock(),))
    def set_total_locked(self, context: Context, value: int) -> None:
        TOTAL.set(context, value)


class PyLockSnapSubFlowStep(Step[None]):
    def __init__(self, child: PyLockSnapStartFlow) -> None:
        self._child = child

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(SubFlow.run(self._child, None))

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("sub-flow done")


class PyLockSnapDispatchStep(Step[str]):
    def __init__(self, targets: dict[str, Step[None]]) -> None:
        self._targets = targets

    def execute(self, context: Context, input: str) -> StepDecision:
        if input == "timeout":
            # The Flow stays open until its timeout handler runs.
            return dead_end()
        return go_to(self._targets[input], None)


class PyLockSnapRouteFlow(Flow[str]):
    """Moves to the gated Step by movement, failure route, timeout failure, or SubFlow."""

    def __init__(self, child: PyLockSnapStartFlow) -> None:
        self.gated = PyLockSnapGatedStep()
        self.failing = PyLockSnapFailingStep(self.gated)
        self.sub_flow = PyLockSnapSubFlowStep(child)
        self.dispatch = PyLockSnapDispatchStep(
            {
                "movement": self.gated,
                "execute-failure": self.failing,
                "sub-flow": self.sub_flow,
            }
        )

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.dispatch).other_steps(
            self.gated, self.failing, self.sub_flow
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return SCHEMA

    def handle_timeout(self, context: Context) -> StepDecision:
        raise RuntimeError("route to the timeout handler failure target")

    @rpc(lock_attributes=(TOTAL.lock(),))
    def set_total_locked(self, context: Context, value: int) -> None:
        TOTAL.set(context, value)


class PyLockSnapAsyncRouteFlow(Flow[str]):
    """Moves to an ``async def`` gated Step on an AsyncWorker."""

    def __init__(self) -> None:
        self.gated = PyLockSnapAsyncGatedStep()
        self.dispatch = PyLockSnapDispatchStep({"movement": self.gated})

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.dispatch).other_steps(self.gated)

    def get_persistence_schema(self) -> PersistenceSchema:
        return SCHEMA

    @rpc(lock_attributes=(TOTAL.lock(),))
    def set_total_locked(self, context: Context, value: int) -> None:
        TOTAL.set(context, value)


SNAPSHOT_START_FLOW = PyLockSnapStartFlow()
SNAPSHOT_ROUTE_FLOW = PyLockSnapRouteFlow(SNAPSHOT_START_FLOW)
SNAPSHOT_ASYNC_FLOW = PyLockSnapAsyncRouteFlow()


@contextmanager
def _snapshot_harness(is_async: bool) -> Iterator[tuple[Client, str]]:
    """Build both registries, then change the Step options for every later read."""
    SNAPSHOT.reset()
    flows: tuple[Flow[Any], ...] = (
        (SNAPSHOT_ASYNC_FLOW,)
        if is_async
        else (SNAPSHOT_START_FLOW, SNAPSHOT_ROUTE_FLOW)
    )
    worker_registry = Registry(flows, allow_async_handlers=is_async)
    client_registry = Registry(flows, allow_async_handlers=is_async)
    try:
        with _harness_with_registries(
            worker_registry, client_registry, is_async
        ) as harness:
            SNAPSHOT.options_changed = True
            try:
                yield harness
            finally:
                SNAPSHOT.release()
    finally:
        SNAPSHOT.reset()


def _assert_step_holds_registered_lock(
    client: Client,
    set_total_locked: Callable[[Context, int], None],
    start: Callable[[], object],
) -> None:
    """Pause the gated Step, expect a lock conflict, then release it and expect its write."""
    SNAPSHOT.arm()
    start()
    paused_flow_id = SNAPSHOT.wait_entered()
    try:
        with pytest.raises(RpcLockConflictError):
            client.invoke_rpc(set_total_locked, paused_flow_id, 2)
    finally:
        SNAPSHOT.release()
    result = client.wait_for_flow(paused_flow_id, WAIT_TIMEOUT)
    assert _single_output(result) == "written"
    assert client.get_attributes(paused_flow_id, TOTAL).get(TOTAL) == 1


def _start_snapshot_flow(
    client: Client, flow: Flow[Any], task_queue: str, input: object
) -> Callable[[], object]:
    return lambda: client.start_flow(
        flow,
        unique_id("py-lock-snap"),
        input,
        StartFlowOptions(task_queue=task_queue),
    )


@pytest.mark.parametrize(
    "send_path",
    ["start", "movement", "execute-failure", "sub-flow", "timeout-handler-failure"],
)
def test_every_send_path_uses_the_registered_step_locks(send_path: str) -> None:
    with _snapshot_harness(is_async=False) as (client, task_queue):
        if send_path == "start":
            _assert_step_holds_registered_lock(
                client,
                SNAPSHOT_START_FLOW.set_total_locked,
                _start_snapshot_flow(client, SNAPSHOT_START_FLOW, task_queue, None),
            )
        elif send_path == "sub-flow":
            # The paused Step runs in the child Flow.
            _assert_step_holds_registered_lock(
                client,
                SNAPSHOT_START_FLOW.set_total_locked,
                _start_snapshot_flow(
                    client, SNAPSHOT_ROUTE_FLOW, task_queue, send_path
                ),
            )
        elif send_path == "timeout-handler-failure":
            timeout_options = StartFlowOptions(
                task_queue=task_queue,
                timeout=timedelta(seconds=1),
                timeout_handler_options=FlowTimeoutHandlerOptions(
                    retry=RetryPolicy(maximum_attempts=1)
                ).on_failure_proceed_to(SNAPSHOT_ROUTE_FLOW.gated),
            )
            _assert_step_holds_registered_lock(
                client,
                SNAPSHOT_ROUTE_FLOW.set_total_locked,
                lambda: client.start_flow(
                    SNAPSHOT_ROUTE_FLOW,
                    unique_id("py-lock-snap-timeout"),
                    "timeout",
                    timeout_options,
                ),
            )
        else:
            _assert_step_holds_registered_lock(
                client,
                SNAPSHOT_ROUTE_FLOW.set_total_locked,
                _start_snapshot_flow(
                    client, SNAPSHOT_ROUTE_FLOW, task_queue, send_path
                ),
            )


def test_async_worker_movement_uses_the_registered_step_locks() -> None:
    with _snapshot_harness(is_async=True) as (client, task_queue):
        _assert_step_holds_registered_lock(
            client,
            SNAPSHOT_ASYNC_FLOW.set_total_locked,
            _start_snapshot_flow(client, SNAPSHOT_ASYNC_FLOW, task_queue, "movement"),
        )


class _SchemaFlow(Flow[None]):
    def __init__(self, schema: PersistenceSchema) -> None:
        self._schema = schema

    def get_steps(self) -> StepList[None]:
        return StepList.empty()

    def get_persistence_schema(self) -> PersistenceSchema:
        return self._schema


def test_registration_rejects_an_unregistered_lock_attribute() -> None:
    missing = Attribute("py-lock-req-missing", bool)
    with pytest.raises(FlowDefinitionError, match="py-lock-req-missing"):
        Registry(
            (
                _SchemaFlow(
                    PersistenceSchema.of(
                        Attribute("py-lock-req-a", int, writes_require_lock=missing)
                    )
                ),
            )
        )
    with pytest.raises(FlowDefinitionError, match="py-lock-req-missing"):
        Registry(
            (
                _SchemaFlow(
                    PersistenceSchema.of(
                        ChannelMap("py-lock-req-c", str, publishes_require_lock=missing)
                    )
                ),
            )
        )
    # An AttributeMap registered under the lock Attribute's name is not that Attribute.
    collision = Attribute("py-lock-req-map-lock", bool)
    with pytest.raises(FlowDefinitionError, match="py-lock-req-map-lock"):
        Registry(
            (
                _SchemaFlow(
                    PersistenceSchema.of(
                        AttributeMap("py-lock-req-map-lock", bool),
                        Channel("py-lock-req-c", str, publishes_require_lock=collision),
                    )
                ),
            )
        )


class _LateLockStep(Step[None]):
    """Returns options that use a lock Attribute the Flow builds in its schema callback."""

    def __init__(self, flow: _LateLockFlow) -> None:
        self._flow = flow
        self.option_reads = 0

    def get_step_options(self) -> StepOptions | None:
        self.option_reads += 1
        lock = self._flow.lock
        assert lock is not None, "Step options were read before the persistence schema"
        return StepOptions(execute_lock_attributes=(lock.lock(),))

    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to(self._flow.next_step, None)


class _LateLockNextStep(_LateLockStep):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("done")


class _LateLockFlow(Flow[None]):
    def __init__(self) -> None:
        self.lock: Attribute[bool] | None = None
        self.step = _LateLockStep(self)
        self.next_step = _LateLockNextStep(self)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.step).other_steps(self.next_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        self.lock = Attribute("py-lock-req-late-lock", bool)
        return PersistenceSchema.of(self.lock)


def test_registration_reads_step_options_once_after_the_schema() -> None:
    flow = _LateLockFlow()
    registry = Registry((flow,))
    assert flow.step.option_reads == 1
    assert flow.next_step.option_reads == 1
    # The Client start and the Worker movement send the options read at registration.
    with _harness_with_registries(registry, registry, is_async=False) as (
        client,
        task_queue,
    ):
        _, result = _run_flow(client, flow, task_queue, None)
        assert _single_output(result) == "done"
    assert flow.step.option_reads == 1
    assert flow.next_step.option_reads == 1


def test_an_attribute_map_cannot_be_a_lock_attribute() -> None:
    lock_map = AttributeMap("py-lock-req-lock-map", bool)
    with pytest.raises(TypeError, match="writes_require_lock"):
        Attribute("py-lock-req-a", int, writes_require_lock=cast(Any, lock_map))
    with pytest.raises(TypeError, match="publishes_require_lock"):
        Channel("py-lock-req-c", str, publishes_require_lock=cast(Any, lock_map))
