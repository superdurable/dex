# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

"""Real-server tests of adaptive pull Worker slot streams (min_open_slots).

Each test starts its own dex-server-zd, so it can set caps and restart it.
The design's test numbers refer to docs/design/adaptive-worker-slot-streams.md.
"""

from __future__ import annotations

import logging
import os
import threading
import time
import uuid
from collections.abc import Callable, Iterator
from datetime import timedelta
from pathlib import Path
from typing import Any
from unittest.mock import patch

import grpc
import pytest

from dex import Client, PullRegistrationError, PullSlotStreamState, PullWorkerOptions
from dex._pull_slot_streams import _PullSlotSettings
from dex.dexpb import dex_pb2_grpc

from .adaptive_slot_harness import (
    MODE_BLOCK,
    MODE_QUICK,
    MODE_STUBBORN,
    RPC_ONLY,
    STEP_AND_RPC,
    STUBBORN_RPC_KEY,
    AdaptiveSlotFlow,
    AdaptiveSlotHandlers,
    AdaptiveSlotParkedFlow,
    AdaptiveSlotTestServer,
    AdaptiveSlotWorkerRun,
    BlockingRegisterStub,
    CapturedLogs,
    ProxyRegister,
    PullSlotCompetitor,
    PullSlotProxy,
    RegisterSendBarrier,
    ServerLimits,
    SlotUsageSampler,
    SnapshotInvariantSampler,
    eventually,
    never,
    new_client,
    require_flows_completed,
    start_flows,
    step_registers,
    windows_to_floor,
)

_HOUR = 3600.0


class AdaptiveSlotHarness:
    """Own every server, proxy, Worker, and client of one test, and clean them up in reverse."""

    def __init__(self, directory: Path) -> None:
        self._directory = directory
        self._cleanups: list[Callable[[], None]] = []
        self._servers: list[AdaptiveSlotTestServer] = []

    def close(self) -> None:
        # pytest shows teardown output only for a failed test.
        for server in self._servers:
            print(f"dex-server-zd log tail:\n{server.log_tail()}")
        failures: list[BaseException] = []
        for cleanup in reversed(self._cleanups):
            try:
                cleanup()
            except BaseException as failure:
                failures.append(failure)
        if failures:
            raise failures[0]

    def _path(self, name: str) -> Path:
        path = self._directory / f"{name}-{uuid.uuid4().hex[:8]}"
        path.mkdir(parents=True)
        return path

    def server(self, limits: ServerLimits = ServerLimits()) -> AdaptiveSlotTestServer:
        server = AdaptiveSlotTestServer(self._path("server"), limits)
        self._servers.append(server)
        self._cleanups.append(server.stop)
        return server

    def proxy(self, upstream_address: str) -> PullSlotProxy:
        proxy = PullSlotProxy(upstream_address)
        self._cleanups.append(proxy.close)
        return proxy

    def worker(
        self,
        address: str,
        handlers: AdaptiveSlotHandlers,
        pull: PullWorkerOptions,
        shrink_window_seconds: float,
        settings: _PullSlotSettings | None = None,
        is_parked_only: bool = False,
    ) -> AdaptiveSlotWorkerRun:
        flows: list[Any] = [AdaptiveSlotParkedFlow(handlers)]
        if not is_parked_only:
            flows.insert(0, AdaptiveSlotFlow(handlers))
        run = AdaptiveSlotWorkerRun(
            address,
            handlers,
            flows,
            pull,
            settings or _PullSlotSettings(shrink_window_seconds=shrink_window_seconds),
            self._path("cache"),
        )
        self._cleanups.append(run.stop)
        return run

    def client(self, address: str, run: AdaptiveSlotWorkerRun) -> Client:
        client = new_client(address, run)
        self._cleanups.append(client.close)
        return client

    def competitor(
        self, address: str, app_worker_id: str, loops: int
    ) -> PullSlotCompetitor:
        competitor = PullSlotCompetitor(address, app_worker_id, loops)
        self._cleanups.append(competitor.stop)
        return competitor

    def logs(self) -> CapturedLogs:
        logs = CapturedLogs()
        logger = logging.getLogger("dex")
        previous_level = logger.level
        logger.addHandler(logs)
        logger.setLevel(logging.INFO)

        def remove() -> None:
            logger.removeHandler(logs)
            logger.setLevel(previous_level)

        self._cleanups.append(remove)
        return logs


@pytest.fixture
def harness(tmp_path: Path) -> Iterator[AdaptiveSlotHarness]:
    scenario = AdaptiveSlotHarness(tmp_path)
    try:
        yield scenario
    finally:
        scenario.close()


def app_id(prefix: str) -> str:
    return f"{prefix}-{uuid.uuid4().hex[:8]}"


def require_registered_streams(
    proxy: PullSlotProxy, app_worker_id: str, step_streams: int, rpc_only_streams: int
) -> None:
    eventually(
        lambda: proxy.registered_streams(app_worker_id, STEP_AND_RPC) == step_streams
        and proxy.registered_streams(app_worker_id, RPC_ONLY) == rpc_only_streams,
        20,
        lambda: (
            f"server streams of {app_worker_id}: want {step_streams} Step-and-RPC and "
            f"{rpc_only_streams} RPC-only, have "
            f"{proxy.registered_streams(app_worker_id, STEP_AND_RPC)} and "
            f"{proxy.registered_streams(app_worker_id, RPC_ONLY)}\n"
            + proxy.describe(app_worker_id)
        ),
    )


def require_slot_state(
    run: AdaptiveSlotWorkerRun,
    description: str,
    condition: Callable[[PullSlotStreamState], bool],
    timeout: float = 30,
) -> None:
    last: list[PullSlotStreamState] = [run.state()]

    def is_reached() -> bool:
        last[0] = run.state()
        return condition(last[0])

    eventually(
        is_reached,
        timeout,
        lambda: f"slot streams never reached {description}: {last[0]}",
    )


def require_no_permanent_registration_error(run: AdaptiveSlotWorkerRun) -> None:
    registration = run.worker.pull_registration_state()
    assert registration.is_ready
    for queue_state in registration.queues:
        assert (
            queue_state.last_permanent_error is None
        ), f"slot kind {queue_state.queue} recorded a permanent error"
    assert (
        not run.started.is_set()
    ), f"start returned during a soft rejection: {run.failure}"


def count_register_batches(registers: list[ProxyRegister], window: float) -> int:
    """Group Registers that arrive within half a window of a batch's first one."""
    batches = 0
    batch_start = 0.0
    for register in registers:
        if batches == 0 or register.at - batch_start > window / 2:
            batches += 1
            batch_start = register.at
    return batches


def require_register_batches_per_window(
    registers: list[ProxyRegister], window: float, batch_size: int
) -> None:
    batch_start = registers[0].at
    batch_count = 0
    for register in registers:
        if register.at - batch_start > window / 2:
            assert (
                register.at - batch_start >= window * 0.8
            ), "two growth batches in one window"
            batch_start = register.at
            batch_count = 0
        batch_count += 1
        assert batch_count <= batch_size


def require_attempts_were_first(handlers: AdaptiveSlotHandlers) -> None:
    for entry in handlers.drain_entries():
        assert entry.attempt == 1, f"a Step spent a retry: {entry}"


class RpcInThread:
    """Invoke one RPC on another thread and keep its result."""

    def __init__(self, invoke: Callable[[], object]) -> None:
        self.result: object = None
        self.failure: BaseException | None = None
        self._done = threading.Event()
        self._thread = threading.Thread(target=self._run, args=(invoke,), daemon=True)
        self._thread.start()

    def _run(self, invoke: Callable[[], object]) -> None:
        try:
            self.result = invoke()
        except BaseException as failure:
            self.failure = failure
        finally:
            self._done.set()

    def wait(self, timeout: float) -> bool:
        return self._done.wait(timeout)


def await_rpc_after_restart(rpc_call: RpcInThread) -> None:
    """Wait for the RPC that the restart interrupted; its result depends on client retries."""
    assert rpc_call.wait(30), "the RPC interrupted by the restart did not return"
    print(f"RPC interrupted by the restart returned: {rpc_call.failure}")


def occupy_every_slot(
    run: AdaptiveSlotWorkerRun,
    client: Client,
    flow: AdaptiveSlotFlow,
    parked: AdaptiveSlotParkedFlow,
    parked_id: str,
) -> tuple[list[str], RpcInThread]:
    """Start E stubborn Steps, then one stubborn RPC that can only land on the RPC-only stream."""
    ceiling = run.state().effective_ceiling
    flow_ids = start_flows(client, flow, MODE_STUBBORN, ceiling)
    run.handlers.await_entries(ceiling)
    require_slot_state(
        run,
        "every Step-and-RPC slot busy",
        lambda state: state.busy == ceiling and state.open == ceiling,
    )
    rpc_call = RpcInThread(
        lambda: client.invoke_rpc(parked.block_until_released, parked_id)
    )
    entry = run.handlers.await_entries(1)[0]
    assert entry.kind == "rpc"
    return flow_ids, rpc_call


def start_parked_flow(client: Client, parked: AdaptiveSlotParkedFlow) -> str:
    parked_id = app_id("adaptive-parked")
    client.start_flow(parked, parked_id, None)
    return parked_id


def flow_of(run: AdaptiveSlotWorkerRun) -> AdaptiveSlotFlow:
    for flow in run.flows:
        if isinstance(flow, AdaptiveSlotFlow):
            return flow
    raise AssertionError("the Worker serves no adaptive slot Flow")


def parked_of(run: AdaptiveSlotWorkerRun) -> AdaptiveSlotParkedFlow:
    for flow in run.flows:
        if isinstance(flow, AdaptiveSlotParkedFlow):
            return flow
    raise AssertionError("the Worker serves no parked Flow")


def test_adaptive_grow_shrink(harness: AdaptiveSlotHarness) -> None:
    """Design Tests 2, 3, 4, 5, 9, and 11.

    A burst of N long Steps with L < N <= E grows to at least N and at most
    min(E, 2N). After load stops, streams return to the floor while short Steps
    keep arriving, and no stream closed on purpose reopens for longer than two
    maximum reconnect backoffs (2 x 7.5 s).
    """
    server = harness.server()
    proxy = harness.proxy(server.address)
    handlers = AdaptiveSlotHandlers()
    worker_id = app_id("adaptive-grow")
    run = harness.worker(
        proxy.address,
        handlers,
        PullWorkerOptions(
            max_concurrent_tasks=16, min_open_slots=3, app_worker_id=worker_id
        ),
        1.0,
    )
    run.wait_registered()

    # Test 2: an idle adaptive Worker holds exactly L + R streams.
    require_registered_streams(proxy, worker_id, 2, 1)
    state = run.state()
    assert state.is_adaptive
    assert (state.open, state.rpc_only_open, state.floor) == (2, 1, 2)
    assert (state.effective_ceiling, state.total_capacity, state.rpc_only_slots) == (
        15,
        16,
        1,
    )

    # Test 3: a burst of N long Steps grows open streams to [N, min(E, 2N)].
    client = harness.client(server.address, run)
    flow = flow_of(run)
    flow_ids = start_flows(client, flow, MODE_BLOCK, 5)
    entries = handlers.await_entries(5)
    require_slot_state(
        run,
        "five busy Steps",
        lambda state: state.busy == 5 and state.registering == 0,
    )
    state = run.state()
    assert 5 <= state.open <= 10

    # Test 9: RPC-only streams stay at R, and an RPC runs with every Step slot busy.
    flow_ids += start_flows(client, flow, MODE_BLOCK, 10)
    entries += handlers.await_entries(10)
    require_slot_state(
        run,
        "fifteen busy Steps",
        lambda state: state.busy == 15 and state.open == 15 and state.registering == 0,
    )
    require_registered_streams(proxy, worker_id, 15, 1)
    assert run.state().rpc_only_open == 1
    assert client.invoke_rpc(flow.get_slot_status, flow_ids[0]) == "ok"
    handlers.release.set()
    require_flows_completed(client, flow_ids)

    # Tests 4 and 5: back to the floor under a trickle, with no retry spent.
    trickle_ids: list[str] = []
    stop_trickle = threading.Event()

    def trickle() -> None:
        while not stop_trickle.is_set():
            trickle_ids.extend(start_flows(client, flow, MODE_QUICK, 1))
            stop_trickle.wait(0.1)

    trickle_thread = threading.Thread(target=trickle, daemon=True)
    trickle_thread.start()
    try:
        eventually(
            lambda: run.state().open == 2,
            windows_to_floor(15, 2) + 5,
            lambda: f"streams did not return to the floor: {run.state()}",
        )
    finally:
        stop_trickle.set()
        trickle_thread.join(timeout=10)
    assert trickle_ids
    require_flows_completed(client, trickle_ids)
    entries += handlers.drain_entries()
    for entry in entries:
        assert entry.attempt == 1, f"a Step spent a retry: {entry}"
    state = run.state()
    assert state.streams_grown >= 13
    assert state.streams_shrunk >= 13
    print(f"shrink race drops: {state.shrink_race_drops}")

    # Test 11 and invariant 5: no shrunk stream reopens within two maximum backoffs.
    require_registered_streams(proxy, worker_id, 2, 1)
    since = time.monotonic()
    never(
        lambda: run.state().open != 2
        or len(proxy.registers_since(worker_id, since)) != 0,
        16,
        "streams rose above the floor at rest, or a stream closed on purpose registered again",
        interval=0.05,
    )


def test_adaptive_rpc_burst(harness: AdaptiveSlotHarness) -> None:
    """The RPC variant of design Test 3, where S + R serve the burst."""
    server = harness.server()
    handlers = AdaptiveSlotHandlers()
    run = harness.worker(
        server.address,
        handlers,
        PullWorkerOptions(
            max_concurrent_tasks=8,
            min_open_slots=2,
            app_worker_id=app_id("adaptive-rpc"),
        ),
        _HOUR,
    )
    run.wait_registered()
    client = harness.client(server.address, run)
    parked = parked_of(run)
    parked_id = start_parked_flow(client, parked)
    rpc_count = 6
    rpc_calls = [
        RpcInThread(lambda: client.invoke_rpc(parked.block_until_released, parked_id))
        for _ in range(rpc_count)
    ]
    handlers.await_entries(rpc_count)
    # E = 7, R = 1: S + R >= min(N, 8), S <= min(7, 2N).
    state = run.state()
    assert state.open + state.rpc_only_open >= rpc_count
    assert state.open <= 7
    assert state.rpc_only_open == 1
    handlers.release.set()
    for rpc_call in rpc_calls:
        assert rpc_call.wait(60)
        assert rpc_call.failure is None, rpc_call.failure
        assert rpc_call.result == "released"


def test_adaptive_soft_rejection_at_node_limit(harness: AdaptiveSlotHarness) -> None:
    """Design Tests 6 and 18.

    The fixed filler holds 14 streams and the adaptive floor 3, which leaves
    room for 3 more. Growth reaches S = 5, and the partly rejected batch clamps
    G to S + H.
    """
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=20, max_streams=20))
    proxy = harness.proxy(server.address)
    fixed_handlers = AdaptiveSlotHandlers()
    fixed = harness.worker(
        server.address,
        fixed_handlers,
        PullWorkerOptions(
            max_concurrent_tasks=14, app_worker_id=app_id("fixed-filler")
        ),
        _HOUR,
        is_parked_only=True,
    )
    fixed.wait_registered()
    handlers = AdaptiveSlotHandlers()
    worker_id = app_id("adaptive-limit")
    window = 2.0
    run = harness.worker(
        proxy.address,
        handlers,
        PullWorkerOptions(
            max_concurrent_tasks=12, min_open_slots=3, app_worker_id=worker_id
        ),
        window,
    )
    run.wait_registered()
    require_registered_streams(proxy, worker_id, 2, 1)
    client = harness.client(server.address, run)
    flow_ids = start_flows(client, flow_of(run), MODE_BLOCK, 10)

    # Room for 3 more: growth stops at S = 5, and the rejected batch clamps G.
    require_slot_state(
        run,
        "growth clamped at the node limit",
        lambda state: state.open == 5
        and state.busy == 5
        and state.registering == 0
        and state.growth_target == 5
        and state.grow_rejected_at_cap > 0,
    )
    state = run.state()
    assert state.effective_ceiling == 11
    assert state.open + state.orphaned_handlers == state.growth_target
    require_no_permanent_registration_error(run)

    # At most one growth batch per window while rejections continue: a batch is min(E - S, S) = 5.
    since = time.monotonic()
    eventually(
        lambda: count_register_batches(proxy.registers_since(worker_id, since), window)
        >= 3,
        6 * window,
        "the Worker did not retry growth once per window",
        interval=0.02,
    )
    require_register_batches_per_window(
        proxy.registers_since(worker_id, since), window, 5
    )
    require_no_permanent_registration_error(run)

    # Room returns: growth resumes after the next window, and G returns to E.
    fixed.stop()
    require_slot_state(
        run,
        "growth after the node limit cleared",
        lambda state: state.busy == 10
        and state.registering == 0
        and state.growth_target == state.effective_ceiling,
    )
    handlers.release.set()
    require_flows_completed(client, flow_ids)
    require_attempts_were_first(handlers)


def test_adaptive_floor_retry_latch(harness: AdaptiveSlotHarness) -> None:
    """Design Test 19."""
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=20, max_streams=20))
    proxy = harness.proxy(server.address)
    handlers = AdaptiveSlotHandlers()
    worker_id = app_id("adaptive-latch")
    window = 1.0
    run = harness.worker(
        proxy.address,
        handlers,
        PullWorkerOptions(
            max_concurrent_tasks=12,
            reserved_rpc_tasks=1,
            min_open_slots=3,
            app_worker_id=worker_id,
        ),
        window,
    )
    run.wait_registered()
    require_registered_streams(proxy, worker_id, 2, 1)
    competitor = harness.competitor(server.address, app_id("competitor"), 18)
    eventually(lambda: competitor.held_streams() == 17, 20)

    # Resetting a floor stream frees a node slot; the competitor takes it, so the reconnect is rejected softly.
    since = time.monotonic()
    assert proxy.reset_registered_stream(worker_id, STEP_AND_RPC)
    require_slot_state(
        run,
        "the reset stream ended",
        lambda state: state.open - state.registering == 1,
        timeout=5,
    )
    # One reconnect after the reset, then one retry per window: wait for three window retries.
    violations: list[str] = []

    def has_retried() -> bool:
        state = run.state()
        if (
            state.open - state.registering > 1
            or not run.worker.pull_registration_state().is_ready
        ):
            violations.append(
                f"a stream registered at the node limit, or readiness dropped: {state}"
            )
            return True
        return len(step_registers(proxy.registers_since(worker_id, since))) >= 4

    eventually(has_retried, 6 * window, "the Worker did not retry at each window")
    assert not violations, violations
    registers = step_registers(proxy.registers_since(worker_id, since))
    for index in range(1, len(registers) - 1):
        assert (
            registers[index + 1].at - registers[index].at >= window * 0.8
        ), "a Register followed a rejected stream's end at once"
    assert proxy.registered_streams(worker_id, STEP_AND_RPC) == 1
    assert run.state().grow_rejected_at_cap >= 2
    require_no_permanent_registration_error(run)

    # A competitor stream that ends does not clear this Worker's latch.
    before_close = step_registers(proxy.registers_since(worker_id, since))
    last_register_before_close = before_close[-1]
    competitor_closed_at = time.monotonic()
    competitor.close_one_stream()
    eventually(
        lambda: len(
            step_registers(proxy.registers_since(worker_id, competitor_closed_at))
        )
        >= 1,
        3 * window,
        "the Worker stopped retrying at the window",
    )
    next_register = step_registers(
        proxy.registers_since(worker_id, competitor_closed_at)
    )[0]
    assert (
        next_register.at - last_register_before_close.at >= window * 0.8
    ), "the competitor's close triggered a Register"

    # When the competitor leaves, the floor comes back within one window.
    competitor.stop()
    eventually(
        lambda: proxy.registered_streams(worker_id, STEP_AND_RPC) == 2
        and run.state().open == 2
        and run.state().registering == 0,
        2 * window + 3,
        lambda: f"the floor did not come back: {run.state()}",
    )
    assert run.worker.pull_registration_state().is_ready


def require_cap_of_one_worker_serves(
    harness: AdaptiveSlotHarness,
    server: AdaptiveSlotTestServer,
    proxy: PullSlotProxy,
    run: AdaptiveSlotWorkerRun,
    worker_id: str,
) -> None:
    run.wait_registered()
    require_slot_state(
        run,
        "one Step-and-RPC stream at cap 1",
        lambda state: state.total_capacity == 1
        and state.rpc_only_slots == 0
        and state.open == 1
        and state.rpc_only_open == 0
        and state.registering == 0,
    )
    require_registered_streams(proxy, worker_id, 1, 0)
    registration = run.worker.pull_registration_state()
    assert registration.is_ready
    assert len(registration.queues) == 1
    client = harness.client(server.address, run)
    flow_ids = start_flows(client, flow_of(run), MODE_QUICK, 2)
    require_flows_completed(client, flow_ids)
    parked = parked_of(run)
    parked_id = start_parked_flow(client, parked)
    assert client.invoke_rpc(parked.get_slot_status, parked_id) == "ok"


def test_cap_of_one_fixed_unset_ceiling_probes_and_clamps(
    harness: AdaptiveSlotHarness,
) -> None:
    """Design Test 14, fixed mode with an unset ceiling."""
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=1))
    proxy = harness.proxy(server.address)
    release_acknowledgements = proxy.hold_acknowledgements()
    logs = harness.logs()
    worker_id = app_id("fixed-probe")
    run = harness.worker(
        proxy.address,
        AdaptiveSlotHandlers(),
        PullWorkerOptions(app_worker_id=worker_id),
        _HOUR,
    )
    # F4: only the probe stream registers before the first acknowledgement.
    eventually(lambda: len(proxy.registers_since(worker_id, 0.0)) == 1, 10)
    never(
        lambda: len(proxy.registers_since(worker_id, 0.0)) > 1,
        0.3,
        "a stream registered before the probe's acknowledgement",
    )
    release_acknowledgements()
    require_cap_of_one_worker_serves(harness, server, proxy, run, worker_id)
    cpu_count = os.cpu_count() or 1
    if cpu_count > 1:
        assert logs.has_warning("clamped max_concurrent_tasks", str(cpu_count), "1")
    assert not run.state().is_adaptive


@pytest.mark.parametrize("max_concurrent_tasks", [4, 0])
def test_cap_of_one_adaptive_floor_of_one(
    harness: AdaptiveSlotHarness, max_concurrent_tasks: int
) -> None:
    """Design Test 14, adaptive mode with the ceiling set or not."""
    if max_concurrent_tasks == 0 and (os.cpu_count() or 1) < 2:
        max_concurrent_tasks = 2
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=1))
    proxy = harness.proxy(server.address)
    logs = harness.logs()
    worker_id = app_id("adaptive-one")
    run = harness.worker(
        proxy.address,
        AdaptiveSlotHandlers(),
        PullWorkerOptions(
            max_concurrent_tasks=max_concurrent_tasks,
            min_open_slots=1,
            app_worker_id=worker_id,
        ),
        _HOUR,
    )
    require_cap_of_one_worker_serves(harness, server, proxy, run, worker_id)
    assert run.state().is_adaptive
    assert logs.has_warning("clamped max_concurrent_tasks", "1")
    for register in proxy.registers_since(worker_id, 0.0):
        assert register.slot_kind == STEP_AND_RPC


@pytest.mark.parametrize(
    "pull",
    [
        PullWorkerOptions(max_concurrent_tasks=18, min_open_slots=1),
        PullWorkerOptions(),
    ],
    ids=["adaptive", "fixed-unset-ceiling"],
)
def test_cap_of_one_first_acknowledgement_snapshot_is_consistent(
    harness: AdaptiveSlotHarness, pull: PullWorkerOptions
) -> None:
    """A snapshot read during the first acknowledgement's clamp never mixes old and new capacities.

    Another thread reads the snapshot in a tight loop across the cap-1 first
    acknowledgement. Every sample must satisfy
    E == max(0, C - max(R_eff, Sr + Hr)) and 0 <= G <= E. The fixed case
    also drops its default RPC-only reserve from 1 to 0 at the clamp.
    """
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=1))
    proxy = harness.proxy(server.address)
    release_acknowledgements = proxy.hold_acknowledgements()
    worker_id = app_id("snapshot-consistency")
    run = harness.worker(
        proxy.address,
        AdaptiveSlotHandlers(),
        PullWorkerOptions(
            max_concurrent_tasks=pull.max_concurrent_tasks,
            min_open_slots=pull.min_open_slots,
            app_worker_id=worker_id,
        ),
        _HOUR,
    )
    sampler = SnapshotInvariantSampler(run)
    try:
        eventually(lambda: len(proxy.registers_since(worker_id, 0.0)) >= 1, 10)
        release_acknowledgements()
        run.wait_registered()
        require_slot_state(
            run,
            "one Step-and-RPC stream at cap 1",
            lambda state: state.total_capacity == 1
            and state.open == 1
            and state.registering == 0,
        )
    finally:
        violations = sampler.stop_sampling()
    assert not violations, f"inconsistent snapshots: {violations}"
    assert 1 in sampler.capacities
    print(f"snapshot samples: {sampler.sample_count}")


def test_first_acknowledgement_transition_holds_the_snapshot(
    harness: AdaptiveSlotHarness,
) -> None:
    """A snapshot waits for the whole first-acknowledgement transition.

    A test barrier pauses the transition under the slot lock, after the cap
    resolution and before the capacity update. A snapshot requested from
    another thread must block until the barrier opens, and then satisfy
    E == max(0, C - max(R_eff, Sr + Hr)) and 0 <= G <= E with the new cap.
    This guards against splitting that critical section again.
    """
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=1))
    has_entered = threading.Event()
    release = threading.Event()

    def pause_inside_first_acknowledgement() -> None:
        has_entered.set()
        assert release.wait(30), "the test never released the first acknowledgement"

    run = harness.worker(
        server.address,
        AdaptiveSlotHandlers(),
        PullWorkerOptions(
            max_concurrent_tasks=18,
            min_open_slots=1,
            app_worker_id=app_id("first-ack-barrier"),
        ),
        _HOUR,
        settings=_PullSlotSettings(
            shrink_window_seconds=_HOUR,
            inside_first_acknowledgement_for_test=pause_inside_first_acknowledgement,
        ),
    )
    try:
        assert has_entered.wait(30), "the first acknowledgement never arrived"
        snapshots: list[PullSlotStreamState] = []
        snapshot_taken = threading.Event()

        def take_snapshot() -> None:
            snapshots.append(run.state())
            snapshot_taken.set()

        snapshot_thread = threading.Thread(target=take_snapshot, daemon=True)
        snapshot_thread.start()
        # A bounded negative check: the snapshot must wait for the held slot lock.
        assert not snapshot_taken.wait(0.5), "a snapshot read the half-done transition"
    finally:
        release.set()
    assert snapshot_taken.wait(10), "the snapshot never returned"
    state = snapshots[0]
    rpc_only_usage = state.rpc_only_open + state.orphaned_rpc_only_handlers
    assert state.total_capacity == 1
    assert state.effective_ceiling == max(
        0, state.total_capacity - max(state.rpc_only_slots, rpc_only_usage)
    ), state
    assert 0 <= state.growth_target <= state.effective_ceiling, state
    run.wait_registered()


@pytest.mark.parametrize("mode", ["fixed", "adaptive"])
def test_cap_of_one_explicit_reserved_rpc_slot_fails_start(
    harness: AdaptiveSlotHarness, mode: str
) -> None:
    """Design Test 14: an explicit reserved RPC slot cannot fit a cap of 1."""
    options = {
        "fixed": PullWorkerOptions(reserved_rpc_tasks=1),
        "adaptive": PullWorkerOptions(
            max_concurrent_tasks=4, min_open_slots=2, reserved_rpc_tasks=1
        ),
    }
    if (os.cpu_count() or 1) < 2:
        options["fixed"] = PullWorkerOptions(
            max_concurrent_tasks=2, min_open_slots=2, reserved_rpc_tasks=1
        )
    pull = options[mode]
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=1))
    run = harness.worker(
        server.address,
        AdaptiveSlotHandlers(),
        PullWorkerOptions(
            max_concurrent_tasks=pull.max_concurrent_tasks,
            min_open_slots=pull.min_open_slots,
            reserved_rpc_tasks=pull.reserved_rpc_tasks,
            app_worker_id=app_id("reserved-one"),
        ),
        _HOUR,
    )
    failure = run.await_start_result(30)
    assert failure is not None
    if mode == "fixed" and pull.max_concurrent_tasks == 2:
        assert (
            "max_concurrent_tasks=2 exceeds Server matching.maxConcurrentTasksCap=1"
            in str(failure)
        )
        return
    assert "reserved_rpc_tasks=1 must be less than the slot ceiling 1" in str(failure)
    assert "matching.maxConcurrentTasksCap=1" in str(failure)


def test_cap_of_one_fixed_explicit_ceiling_above_cap_fails_start(
    harness: AdaptiveSlotHarness,
) -> None:
    """Design Test 14: a fixed explicit ceiling above the cap fails start as today."""
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=1))
    run = harness.worker(
        server.address,
        AdaptiveSlotHandlers(),
        PullWorkerOptions(max_concurrent_tasks=2, app_worker_id=app_id("fixed-two")),
        _HOUR,
    )
    failure = run.await_start_result(30)
    assert (
        "max_concurrent_tasks=2 exceeds Server matching.maxConcurrentTasksCap=1"
        in str(failure)
    )


def test_adaptive_ceiling_above_cap_clamps(harness: AdaptiveSlotHarness) -> None:
    """Design Test 20a."""
    server = harness.server()
    handlers = AdaptiveSlotHandlers()
    logs = harness.logs()
    run = harness.worker(
        server.address,
        handlers,
        PullWorkerOptions(
            max_concurrent_tasks=300,
            min_open_slots=8,
            app_worker_id=app_id("adaptive-300"),
        ),
        _HOUR,
    )
    run.wait_registered(120)
    assert logs.has_warning("clamped max_concurrent_tasks", "300", "256")
    assert logs.count_warnings("clamped max_concurrent_tasks") == 1
    assert run.state().total_capacity == 256
    sampler = SlotUsageSampler(run)
    client = harness.client(server.address, run)
    flow_ids = start_flows(client, flow_of(run), MODE_BLOCK, 400)
    require_slot_state(
        run,
        "every slot busy at the clamped ceiling",
        lambda state: state.busy == 255 and state.open + state.rpc_only_open == 256,
        timeout=120,
    )
    assert run.state().grow_rejected_at_cap == 0
    handlers.release.set()
    require_flows_completed(client, flow_ids, timeout=240)
    sampler.require_within_capacity()
    require_attempts_were_first(handlers)


def test_fixed_ceiling_above_cap_fails_start(harness: AdaptiveSlotHarness) -> None:
    """Design Test 20b."""
    server = harness.server()
    run = harness.worker(
        server.address,
        AdaptiveSlotHandlers(),
        PullWorkerOptions(max_concurrent_tasks=300, app_worker_id=app_id("fixed-300")),
        _HOUR,
    )
    failure = run.await_start_result(30)
    assert (
        "max_concurrent_tasks=300 exceeds Server matching.maxConcurrentTasksCap=256"
        in str(failure)
    )


def test_adaptive_cap_drop_to_one(harness: AdaptiveSlotHarness) -> None:
    """Design Test 17a."""
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=4))
    proxy = harness.proxy(server.address)
    handlers = AdaptiveSlotHandlers()
    handlers.is_rpc_stubborn = True
    worker_id = app_id("adaptive-drop")
    run = harness.worker(
        proxy.address,
        handlers,
        PullWorkerOptions(
            max_concurrent_tasks=4,
            min_open_slots=2,
            app_worker_id=worker_id,
            stop_on_permanent_stream_rejection=True,
        ),
        1.0,
    )
    run.wait_registered()
    client = harness.client(server.address, run)
    parked = parked_of(run)
    parked_id = start_parked_flow(client, parked)
    flow_ids, rpc_call = occupy_every_slot(run, client, flow_of(run), parked, parked_id)

    # The restart lowers the cap to 1; the proxy lets the RPC-only stream register first.
    server.kill()
    proxy.hold_step_registers_until_rpc_only_acknowledged()
    server.restart(ServerLimits(max_concurrent_tasks_cap=1))
    proxy.set_upstream(server.address)
    handlers.release_stubborn(*flow_ids, STUBBORN_RPC_KEY)
    await_rpc_after_restart(rpc_call)

    require_slot_state(
        run,
        "one Step-and-RPC stream at cap 1",
        lambda state: state.total_capacity == 1
        and state.rpc_only_slots == 0
        and state.rpc_only_open == 0
        and state.open == 1
        and state.registering == 0
        and state.orphaned_handlers == 0
        and state.orphaned_rpc_only_handlers == 0,
    )
    eventually(
        lambda: proxy.registered_streams(worker_id, STEP_AND_RPC) == 1
        and proxy.registered_streams(worker_id, RPC_ONLY) == 0,
        10,
    )
    assert (
        run.state().grow_rejected_at_cap >= 1
    ), "the Step-and-RPC Register was not rejected at the cap"
    require_no_permanent_registration_error(run)

    # Steps and RPCs keep running on the one stream.
    restarted_client = harness.client(server.address, run)
    require_flows_completed(restarted_client, flow_ids)
    quick_ids = start_flows(restarted_client, flow_of(run), MODE_QUICK, 2)
    require_flows_completed(restarted_client, quick_ids)
    assert restarted_client.invoke_rpc(parked.get_slot_status, parked_id) == "ok"


@pytest.mark.parametrize("should_stop", [True, False])
def test_fixed_cap_drop_to_one(harness: AdaptiveSlotHarness, should_stop: bool) -> None:
    """Design Test 17b: fixed mode keeps today's behavior."""
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=4))
    proxy = harness.proxy(server.address)
    worker_id = app_id("fixed-drop")
    run = harness.worker(
        proxy.address,
        AdaptiveSlotHandlers(),
        PullWorkerOptions(
            max_concurrent_tasks=4,
            app_worker_id=worker_id,
            stop_on_permanent_stream_rejection=should_stop,
        ),
        1.0,
    )
    run.wait_registered()
    require_registered_streams(proxy, worker_id, 3, 1)
    server.kill()
    proxy.hold_step_registers_until_rpc_only_acknowledged()
    server.restart(ServerLimits(max_concurrent_tasks_cap=1))
    proxy.set_upstream(server.address)
    if should_stop:
        failure = run.await_start_result(45)
        assert isinstance(failure, PullRegistrationError), failure
        assert failure.queue == "STEP_AND_RPC"
        assert isinstance(failure.error, grpc.RpcError)
        assert failure.error.code() is grpc.StatusCode.RESOURCE_EXHAUSTED
        return

    def is_rejected() -> bool:
        queue_state = run.worker.pull_registration_state().queues[0]
        error = queue_state.last_permanent_error
        return (
            not queue_state.has_registered_stream
            and isinstance(error, grpc.RpcError)
            and error.code() is grpc.StatusCode.RESOURCE_EXHAUSTED
        )

    eventually(is_rejected, 45)
    assert not run.worker.pull_registration_state().is_ready
    # Today's permanent-error backoff is one minute, so no Step-and-RPC stream registers again soon.
    rejected_at = time.monotonic()
    never(
        lambda: len(step_registers(proxy.registers_since(worker_id, rejected_at))) != 0,
        5,
        "a Step-and-RPC stream retried before the permanent-error backoff",
        interval=0.05,
    )
    assert not run.state().is_adaptive
    assert run.state().grow_rejected_at_cap == 0


class OrphanedHandlerScenario:
    """An adaptive Worker whose every slot runs a handler that ignores cancellation."""

    def __init__(self, harness: AdaptiveSlotHarness) -> None:
        self.server = harness.server(ServerLimits(max_concurrent_tasks_cap=4))
        self.proxy = harness.proxy(self.server.address)
        self.handlers = AdaptiveSlotHandlers()
        self.handlers.is_rpc_stubborn = True
        self.worker_id = app_id("adaptive-orphan")
        self.run = harness.worker(
            self.proxy.address,
            self.handlers,
            PullWorkerOptions(
                max_concurrent_tasks=4, min_open_slots=2, app_worker_id=self.worker_id
            ),
            _HOUR,
        )
        self.run.wait_registered()
        client = harness.client(self.server.address, self.run)
        parked = parked_of(self.run)
        parked_id = start_parked_flow(client, parked)
        self.flow_ids, self.rpc_call = occupy_every_slot(
            self.run, client, flow_of(self.run), parked, parked_id
        )
        self.sampler = SlotUsageSampler(self.run)


def test_orphaned_handlers_same_cap(harness: AdaptiveSlotHarness) -> None:
    """Design Tests 15 and 8 with the cap unchanged."""
    scenario = OrphanedHandlerScenario(harness)
    server, proxy, run = scenario.server, scenario.proxy, scenario.run
    handlers, worker_id, flow_ids = (
        scenario.handlers,
        scenario.worker_id,
        scenario.flow_ids,
    )
    killed_at = time.monotonic()
    server.kill()
    server.restart(ServerLimits(max_concurrent_tasks_cap=4))
    proxy.set_upstream(server.address)
    # No early registration: every slot still has a running handler.
    require_slot_state(
        run,
        "every handler orphaned",
        lambda state: state.open == 0
        and state.rpc_only_open == 0
        and state.orphaned_handlers == 3
        and state.orphaned_rpc_only_handlers == 1,
        timeout=10,
    )
    never(
        lambda: len(proxy.registers_since(worker_id, killed_at)) != 0
        or not run.worker.pull_registration_state().is_ready,
        1,
        "a stream registered while orphaned handlers filled the capacity, or readiness dropped",
    )

    # Freed capacity goes to one Step-and-RPC stream first, then to the RPC-only stream.
    handlers.release_stubborn(flow_ids[0])
    eventually(lambda: proxy.registered_streams(worker_id, STEP_AND_RPC) == 1, 10)
    handlers.release_stubborn(STUBBORN_RPC_KEY)
    eventually(lambda: proxy.registered_streams(worker_id, RPC_ONLY) == 1, 10)
    registers = proxy.registers_since(worker_id, killed_at)
    assert registers[0].slot_kind == STEP_AND_RPC
    assert registers[-1].slot_kind == RPC_ONLY
    # Test 8: the Worker re-registers at most L + R streams first.
    assert len(registers) <= 2
    handlers.release_stubborn(*flow_ids[1:])
    await_rpc_after_restart(scenario.rpc_call)
    restarted_client = harness.client(server.address, run)
    require_flows_completed(restarted_client, flow_ids)
    assert run.worker.pull_registration_state().is_ready
    # With the cap unchanged, S + H + Sr + Hr <= C held at every sample.
    scenario.sampler.require_within_capacity()


def test_orphaned_handlers_cap_lowered_below_orphans(
    harness: AdaptiveSlotHarness,
) -> None:
    """Design Test 15 with a restart that lowers the cap below the orphaned handler count.

    The first acknowledgement reveals cap 2 while usage is 4, so the Worker
    registers nothing more until usage drops below 2.
    """
    scenario = OrphanedHandlerScenario(harness)
    server, proxy, run = scenario.server, scenario.proxy, scenario.run
    handlers, worker_id, flow_ids = (
        scenario.handlers,
        scenario.worker_id,
        scenario.flow_ids,
    )
    server.kill()
    server.restart(ServerLimits(max_concurrent_tasks_cap=2))
    proxy.set_upstream(server.address)
    require_slot_state(
        run,
        "every handler orphaned",
        lambda state: state.orphaned_handlers == 3
        and state.orphaned_rpc_only_handlers == 1,
        timeout=10,
    )
    released_at = time.monotonic()
    handlers.release_stubborn(flow_ids[0])
    # The first acknowledgement reveals cap 2 with usage 4; no Register until usage drops.
    eventually(
        lambda: len(proxy.acknowledgements_since(worker_id, released_at)) == 1, 10
    )
    acknowledged_at = proxy.acknowledgements_since(worker_id, released_at)[0].at
    require_slot_state(
        run,
        "capacity lowered to 2",
        lambda state: state.total_capacity == 2
        and state.open == 0
        and state.registering == 0,
        timeout=10,
    )
    handlers.release_stubborn(flow_ids[1])
    require_slot_state(
        run,
        "the second handler returned",
        lambda state: state.orphaned_handlers == 1,
        timeout=10,
    )
    # Usage is still 2 of 2 with one Step and one RPC-only handler running.
    never(
        lambda: len(proxy.registers_since(worker_id, acknowledged_at + 0.001)) != 0,
        1,
        "a Register went out while usage reached the new cap",
    )
    handlers.release_stubborn(flow_ids[2], STUBBORN_RPC_KEY)
    require_slot_state(
        run,
        "usage converged to the new cap",
        lambda state: state.open == 1
        and state.rpc_only_open == 1
        and state.registering == 0
        and state.orphaned_handlers == 0
        and state.orphaned_rpc_only_handlers == 0,
        timeout=20,
    )
    state = run.state()
    assert state.open + state.rpc_only_open <= state.total_capacity
    require_registered_streams(proxy, worker_id, 1, 1)
    # F5 allows the transient excess, so this case asserts convergence instead of a bound at every sample.
    print(
        "samples above the new cap while old handlers ran: "
        f"{len(scenario.sampler.stop_sampling())}"
    )
    await_rpc_after_restart(scenario.rpc_call)
    restarted_client = harness.client(server.address, run)
    require_flows_completed(restarted_client, flow_ids)


def test_orphaned_handlers_failover_during_growth_batch(
    harness: AdaptiveSlotHarness,
) -> None:
    """Design Test 15, with the failover landing while a growth batch registers."""
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=8))
    proxy = harness.proxy(server.address)
    handlers = AdaptiveSlotHandlers()
    worker_id = app_id("adaptive-growth-failover")
    run = harness.worker(
        proxy.address,
        handlers,
        PullWorkerOptions(
            max_concurrent_tasks=8, min_open_slots=3, app_worker_id=worker_id
        ),
        _HOUR,
    )
    run.wait_registered()
    require_registered_streams(proxy, worker_id, 2, 1)
    release_growth = proxy.hold_step_registers()
    client = harness.client(server.address, run)
    flow_ids = start_flows(client, flow_of(run), MODE_STUBBORN, 2)
    # Both floor streams are busy, so a growth batch of 2 waits at the proxy.
    eventually(lambda: proxy.held_step_registers() == 2, 20)
    require_slot_state(
        run,
        "a growth batch registering",
        lambda state: state.busy == 2 and state.registering == 2,
    )
    sampler = SlotUsageSampler(run)
    killed_at = time.monotonic()
    server.kill()
    server.restart(ServerLimits(max_concurrent_tasks_cap=8))
    proxy.set_upstream(server.address)
    require_slot_state(
        run,
        "the busy streams orphaned",
        lambda state: state.orphaned_handlers == 2
        and state.registering == 2
        and state.rpc_only_open == 1,
        timeout=10,
    )
    require_registered_streams(proxy, worker_id, 0, 1)
    # The in-flight batch already holds the floor, so no other Step-and-RPC stream opens.
    assert not step_registers(proxy.registers_since(worker_id, killed_at))
    released_at = time.monotonic()
    release_growth()
    # The held batch registers on the new server; retried Steps may then grow the Worker further.
    eventually(
        lambda: len(
            step_registers(proxy.acknowledgements_since(worker_id, released_at))
        )
        >= 2
        and proxy.registered_streams(worker_id, STEP_AND_RPC) >= 2,
        20,
        "the growth batch did not register after the failover",
    )
    handlers.release_stubborn(*flow_ids)
    restarted_client = harness.client(server.address, run)
    require_flows_completed(restarted_client, flow_ids)
    require_slot_state(
        run,
        "no orphaned handlers",
        lambda state: state.orphaned_handlers == 0 and state.registering == 0,
    )
    sampler.require_within_capacity()
    require_no_permanent_registration_error(run)


def test_orphaned_handlers_failover_during_shrink_half_close(
    harness: AdaptiveSlotHarness,
) -> None:
    """Design Test 15, with the failover landing while a shrink half-close is in flight."""
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=8))
    proxy = harness.proxy(server.address)
    handlers = AdaptiveSlotHandlers()
    worker_id = app_id("adaptive-shrink-failover")
    run = harness.worker(
        proxy.address,
        handlers,
        PullWorkerOptions(
            max_concurrent_tasks=8, min_open_slots=3, app_worker_id=worker_id
        ),
        1.0,
    )
    run.wait_registered()
    client = harness.client(server.address, run)
    flow = flow_of(run)
    flow_ids = start_flows(client, flow, MODE_BLOCK, 3)
    # One handler ignores cancellation, so it outlives its stream in the crash.
    stubborn_ids = start_flows(client, flow, MODE_STUBBORN, 1)
    require_slot_state(
        run,
        "four busy Steps",
        lambda state: state.busy == 4 and state.registering == 0,
    )
    held_half_closes, release_half_closes = proxy.hold_half_closes()
    handlers.release.set()
    require_flows_completed(client, flow_ids)
    held_half_closes.get(timeout=20)
    assert run.state().busy == 1
    # The server still holds the half-closing stream when it crashes, and the stubborn handler still runs.
    sampler = SlotUsageSampler(run)
    server.kill()
    require_slot_state(
        run,
        "the stubborn handler orphaned",
        lambda state: state.orphaned_handlers == 1 and state.busy == 0,
        timeout=10,
    )
    server.restart(ServerLimits(max_concurrent_tasks_cap=8))
    # Registers that reached the dead server were retries of the same runners, so count from the switch.
    switched_at = time.monotonic()
    proxy.set_upstream(server.address)
    release_half_closes()
    require_registered_streams(proxy, worker_id, 2, 1)

    def is_shrunk_stream_reopened() -> bool:
        return (
            len(step_registers(proxy.registers_since(worker_id, switched_at))) > 2
            or proxy.registered_streams(worker_id, STEP_AND_RPC) > 2
        )

    never(
        is_shrunk_stream_reopened,
        3,
        "a stream closed by shrink reopened after the failover",
    )
    state = run.state()
    assert (
        state.orphaned_handlers == 1
    ), f"the orphaned handler left the capacity count early: {state}"
    assert state.open == 2
    # The returning handler frees a slot, but the floor is already open, so nothing reopens.
    handlers.release_stubborn(*stubborn_ids)
    require_slot_state(
        run,
        "the orphaned handler returned",
        lambda state: state.orphaned_handlers == 0
        and state.open == 2
        and state.rpc_only_open == 1
        and state.registering == 0,
    )
    never(
        is_shrunk_stream_reopened,
        2,
        "a stream reopened when the orphaned handler returned",
    )
    restarted_client = harness.client(server.address, run)
    require_flows_completed(restarted_client, stubborn_ids)
    sampler.require_within_capacity()
    require_no_permanent_registration_error(run)


def test_fixed_mode_unchanged(harness: AdaptiveSlotHarness) -> None:
    """Design Tests 1 and 16."""
    server = harness.server()
    proxy = harness.proxy(server.address)
    release_acknowledgements = proxy.hold_acknowledgements()
    handlers = AdaptiveSlotHandlers()
    worker_id = app_id("fixed-parallel")
    run = harness.worker(
        proxy.address,
        handlers,
        PullWorkerOptions(max_concurrent_tasks=4, app_worker_id=worker_id),
        1.0,
    )
    # Test 16: every stream opens before the first acknowledgement arrives.
    eventually(lambda: len(proxy.registers_since(worker_id, 0.0)) == 4, 10)
    release_acknowledgements()
    run.wait_registered()
    for register in proxy.registers_since(worker_id, 0.0):
        assert register.at < proxy.first_acknowledgement_at()
    # Test 1: the count stays at the ceiling through load and idle periods.
    require_registered_streams(proxy, worker_id, 3, 1)
    client = harness.client(server.address, run)
    flow_ids = start_flows(client, flow_of(run), MODE_BLOCK, 3)
    handlers.await_entries(3)
    assert run.state().open == 3
    handlers.release.set()
    require_flows_completed(client, flow_ids)
    since = time.monotonic()
    never(
        lambda: run.state().open != 3
        or len(proxy.registers_since(worker_id, since)) != 0,
        3,
        "a fixed Worker changed its stream count at rest",
    )
    state = run.state()
    assert not state.is_adaptive
    assert (state.open, state.rpc_only_open) == (3, 1)
    assert state.streams_grown == 0
    assert state.streams_shrunk == 0
    assert not proxy.registers_since(worker_id, since)
    require_registered_streams(proxy, worker_id, 3, 1)


def test_adaptive_grant_revoked_after_cap_drop(harness: AdaptiveSlotHarness) -> None:
    """F5 for an attempt granted under the old cap.

    When another stream reports a lower cap during its backoff, it sends no
    Register above that cap.
    """
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=8))
    proxy = harness.proxy(server.address)
    worker_id = app_id("adaptive-revoke")
    run = harness.worker(
        proxy.address,
        AdaptiveSlotHandlers(),
        PullWorkerOptions(
            max_concurrent_tasks=8,
            min_open_slots=3,
            reserved_rpc_tasks=0,
            app_worker_id=worker_id,
        ),
        _HOUR,
    )
    run.wait_registered()
    require_registered_streams(proxy, worker_id, 3, 0)

    # Every floor stream breaks and gets a reconnect grant under the last known cap of 8.
    server.kill()
    proxy.fail_step_registers_from_now()
    server.restart(ServerLimits(max_concurrent_tasks_cap=2))
    proxy.set_upstream(server.address)
    # One runner registers and learns cap 2 while the other two wait in their backoff.
    proxy.pass_next_step_register()
    require_slot_state(
        run,
        "the lower cap is known",
        lambda state: state.total_capacity == 2,
    )
    proxy.stop_failing_step_registers()
    cap_known_at = time.monotonic()
    # One registered stream leaves room for exactly one more; the other old grant must be revoked.
    never(
        lambda: len(step_registers(proxy.registers_since(worker_id, cap_known_at))) > 1
        or len(proxy.rejections_since(worker_id, cap_known_at)) != 0,
        8,
        "a Register granted under the old cap went out above the new cap",
    )
    require_slot_state(
        run,
        "two streams at cap 2",
        lambda state: state.open == 2 and state.registering == 0,
    )
    require_registered_streams(proxy, worker_id, 2, 0)
    require_no_permanent_registration_error(run)


def test_adaptive_cap_drop_between_admission_and_send(
    harness: AdaptiveSlotHarness,
) -> None:
    """Admission is re-checked before the write; a later cap drop gets a soft Server rejection.

    The two held attempts are re-checked after cap 2 is known, and one is
    revoked without sending anything. A Register that passed its re-check just
    before the cap became known may still meet the cap once. That rejection is
    soft, and the retry latch retries it at the next window.
    """
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=8))
    proxy = harness.proxy(server.address)
    barrier = RegisterSendBarrier()
    worker_id = app_id("adaptive-barrier")
    run = harness.worker(
        proxy.address,
        AdaptiveSlotHandlers(),
        PullWorkerOptions(
            max_concurrent_tasks=8,
            min_open_slots=3,
            reserved_rpc_tasks=0,
            app_worker_id=worker_id,
        ),
        1.0,
        settings=_PullSlotSettings(
            shrink_window_seconds=1.0, before_register_send_for_test=barrier.hold
        ),
    )
    try:
        run.wait_registered()
        require_registered_streams(proxy, worker_id, 3, 0)

        # Every floor stream breaks; each runner is admitted under the old cap of 8 and waits before its write.
        barrier.is_armed.set()
        server.kill()
        eventually(
            lambda: barrier.held == 3,
            30,
            "the runners did not reach the write barrier",
        )
        server.restart(ServerLimits(max_concurrent_tasks_cap=2))
        proxy.set_upstream(server.address)
        # One runner writes, registers, and reports cap 2 while the other two stay admitted but unsent.
        barrier.grant_pass()
        require_slot_state(
            run,
            "the lower cap is known",
            lambda state: state.total_capacity == 2,
        )
        cap_known_at = time.monotonic()
        barrier.release()

        # The held attempts are re-checked under cap 2; one is revoked and sends nothing.
        def is_over_cap() -> bool:
            rejections = len(proxy.rejections_since(worker_id, cap_known_at))
            return (
                rejections > 1
                or len(step_registers(proxy.registers_since(worker_id, cap_known_at)))
                > 1 + rejections
                or proxy.registered_streams(worker_id, STEP_AND_RPC) > 2
            )

        never(
            is_over_cap,
            3,
            "a revoked attempt sent a Register, or Registers exceeded the new cap",
            interval=0.01,
        )
        require_registered_streams(proxy, worker_id, 2, 0)
        require_slot_state(
            run,
            "two streams at cap 2",
            lambda state: state.open == 2 and state.registering == 0,
        )
        rejections = len(proxy.rejections_since(worker_id, cap_known_at))
        if rejections > 0:
            assert (
                run.state().grow_rejected_at_cap >= rejections
            ), "the rejection was not handled as soft"
        require_no_permanent_registration_error(run)
    finally:
        barrier.release()


def test_adaptive_blocked_register_send(harness: AdaptiveSlotHarness) -> None:
    """One blocked Register write must not stop cap updates, other handlers, or stop."""
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=4))
    handlers = AdaptiveSlotHandlers()
    logs = harness.logs()
    real_stub_class = dex_pb2_grpc.WorkerTaskServiceStub
    stubs: list[BlockingRegisterStub] = []

    def blocking_stub(channel: Any) -> BlockingRegisterStub:
        stub = BlockingRegisterStub(real_stub_class(channel), 1)  # type: ignore[no-untyped-call]
        stubs.append(stub)
        return stub

    with patch(
        "dex._pull_worker.dex_pb2_grpc.WorkerTaskServiceStub", side_effect=blocking_stub
    ):
        run = harness.worker(
            server.address,
            handlers,
            PullWorkerOptions(
                max_concurrent_tasks=8,
                min_open_slots=3,
                app_worker_id=app_id("adaptive-blocked-send"),
                stop_drain_timeout=timedelta(seconds=1),
            ),
            _HOUR,
        )
        try:
            # The first Step-and-RPC Register blocks in its write; the second registers and reports cap 4.
            eventually(lambda: bool(stubs) and stubs[0].blocked_writes == 1, 20)
            require_slot_state(
                run,
                "a cap update on another stream",
                lambda state: state.total_capacity == 4 and state.idle >= 1,
                timeout=20,
            )
            client = harness.client(server.address, run)
            flow_ids = start_flows(client, flow_of(run), MODE_QUICK, 2)
            require_flows_completed(client, flow_ids)
            assert stubs[0].blocked_writes == 1, "the blocked write returned on its own"

            # Stop finishes within its drain timeout although one write never returns by itself.
            stopped = threading.Event()
            stop_started_at = time.monotonic()

            def stop() -> None:
                run.worker.stop()
                stopped.set()

            stop_thread = threading.Thread(target=stop, daemon=True)
            stop_thread.start()
            assert stopped.wait(
                15
            ), "stop did not finish while a Register write was blocked"
            assert time.monotonic() - stop_started_at < 10
            assert run.await_start_result(10) is None
            # A write that stop canceled is a normal shutdown, not a stream failure.
            assert (
                logs.count_records(logging.ERROR, "Worker task stream failed") == 0
            ), "stop logged its canceled Register write as a failure"
        finally:
            for stub in stubs:
                stub.release_all()


def test_adaptive_cap_drop_above_floor(harness: AdaptiveSlotHarness) -> None:
    """Design Test 7 for a new cap above the floor."""
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=8))
    proxy = harness.proxy(server.address)
    handlers = AdaptiveSlotHandlers()
    worker_id = app_id("adaptive-drop-above")
    run = harness.worker(
        proxy.address,
        handlers,
        PullWorkerOptions(
            max_concurrent_tasks=8, min_open_slots=3, app_worker_id=worker_id
        ),
        1.0,
    )
    run.wait_registered()
    client = harness.client(server.address, run)
    flow_ids = start_flows(client, flow_of(run), MODE_BLOCK, 6)
    require_slot_state(run, "six busy Steps", lambda state: state.busy == 6)
    sampler = SlotUsageSampler(run)
    server.kill()
    restarted_at = time.monotonic()
    server.restart(ServerLimits(max_concurrent_tasks_cap=4))
    proxy.set_upstream(server.address)
    # C = 4 and R = 1 leave E = 3; retried Steps grow back to E.
    require_slot_state(
        run,
        "growth up to the lower cap",
        lambda state: state.total_capacity == 4
        and state.effective_ceiling == 3
        and state.busy == 3
        and state.open == 3
        and state.rpc_only_open == 1
        and state.registering == 0,
    )
    require_registered_streams(proxy, worker_id, 3, 1)
    assert not proxy.rejections_since(worker_id, restarted_at)
    assert not proxy.worker_resets_since(
        worker_id, restarted_at
    ), "cap convergence reset a stream"
    require_no_permanent_registration_error(run)
    handlers.release.set()
    restarted_client = harness.client(server.address, run)
    require_flows_completed(restarted_client, flow_ids)
    sampler.require_within_capacity()
    assert not proxy.worker_resets_since(
        worker_id, restarted_at
    ), "cap convergence reset a stream"


def test_adaptive_cap_drop_below_floor(harness: AdaptiveSlotHarness) -> None:
    """Design Test 7 for a new cap below the floor."""
    server = harness.server(ServerLimits(max_concurrent_tasks_cap=8))
    proxy = harness.proxy(server.address)
    handlers = AdaptiveSlotHandlers()
    worker_id = app_id("adaptive-drop-below")
    run = harness.worker(
        proxy.address,
        handlers,
        PullWorkerOptions(
            max_concurrent_tasks=8,
            min_open_slots=5,
            app_worker_id=worker_id,
            stop_on_permanent_stream_rejection=True,
        ),
        1.0,
    )
    run.wait_registered()
    require_registered_streams(proxy, worker_id, 4, 1)
    server.kill()
    restarted_at = time.monotonic()
    server.restart(ServerLimits(max_concurrent_tasks_cap=3))
    proxy.set_upstream(server.address)
    # C = 3 and R = 1 leave E = 2, so L_eff = 2.
    require_slot_state(
        run,
        "the floor limited by the lower cap",
        lambda state: state.total_capacity == 3
        and state.floor == 2
        and state.open == 2
        and state.rpc_only_open == 1
        and state.registering == 0,
    )
    require_registered_streams(proxy, worker_id, 2, 1)
    assert not proxy.worker_resets_since(
        worker_id, restarted_at
    ), "cap convergence reset a stream"
    require_no_permanent_registration_error(run)
    restarted_client = harness.client(server.address, run)
    quick_ids = start_flows(restarted_client, flow_of(run), MODE_QUICK, 3)
    require_flows_completed(restarted_client, quick_ids)


def test_adaptive_graceful_stop_during_growth(harness: AdaptiveSlotHarness) -> None:
    """Design Test 12."""
    server = harness.server()
    handlers = AdaptiveSlotHandlers()
    run = harness.worker(
        server.address,
        handlers,
        PullWorkerOptions(
            max_concurrent_tasks=32,
            min_open_slots=3,
            app_worker_id=app_id("adaptive-stop"),
        ),
        1.0,
    )
    run.wait_registered()
    client = harness.client(server.address, run)
    flow_ids = start_flows(client, flow_of(run), MODE_BLOCK, 20)
    handlers.await_entries(4)
    stopped = threading.Event()

    def stop() -> None:
        run.worker.stop()
        stopped.set()

    stop_thread = threading.Thread(target=stop, daemon=True)
    stop_thread.start()
    eventually(lambda: run.worker._pull.is_stopping, 10, interval=0.001)
    # Busy handlers finish during the drain; tasks that never started return without spending a retry.
    handlers.release.set()
    assert stopped.wait(60), "stop did not finish"
    assert run.await_start_result(10) is None
    replacement = harness.worker(
        server.address,
        handlers,
        PullWorkerOptions(
            max_concurrent_tasks=8, app_worker_id=app_id("fixed-replacement")
        ),
        _HOUR,
    )
    replacement.wait_registered()
    require_flows_completed(client, flow_ids)
    require_attempts_were_first(handlers)


def test_adaptive_mixed_fleet(harness: AdaptiveSlotHarness) -> None:
    """Design Test 13 against one server."""
    server = harness.server()
    handlers = AdaptiveSlotHandlers()
    fixed = harness.worker(
        server.address,
        handlers,
        PullWorkerOptions(max_concurrent_tasks=4, app_worker_id=app_id("fixed-fleet")),
        _HOUR,
    )
    adaptive = harness.worker(
        server.address,
        handlers,
        PullWorkerOptions(
            max_concurrent_tasks=16,
            min_open_slots=2,
            app_worker_id=app_id("adaptive-fleet"),
        ),
        1.0,
    )
    fixed.wait_registered()
    adaptive.wait_registered()
    client = harness.client(server.address, adaptive)
    flow = flow_of(adaptive)
    quick_ids = start_flows(client, flow, MODE_QUICK, 10)
    require_flows_completed(client, quick_ids)
    # The fixed Worker leaves; its share moves to the adaptive Worker, which grows from its floor.
    fixed.stop()
    flow_ids = start_flows(client, flow, MODE_BLOCK, 8)
    handlers.await_entries(8)
    require_slot_state(
        adaptive,
        "the adaptive Worker grew to the load",
        lambda state: state.busy == 8 and 8 <= state.open <= 15,
    )
    handlers.release.set()
    require_flows_completed(client, flow_ids)
    require_attempts_were_first(handlers)
