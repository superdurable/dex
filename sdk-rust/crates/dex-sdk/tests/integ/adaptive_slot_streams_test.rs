// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

//! Real-server tests of adaptive pull Worker slot streams, one dex-server-zd process per test.
//! Design test numbers refer to docs/design/adaptive-worker-slot-streams.md.

use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{Duration, Instant};

use dex_sdk::{GrpcCode, PullWorkerOptions};
use tokio::sync::{Semaphore, watch};

use crate::adaptive_slot_harness::{
    AdaptiveSlotTestServer, PullSlotCompetitor, PullSlotProxy, RPC_ONLY, STEP_AND_RPC,
    ServerLimits, captured_logs, eventually, never, step_events, wait_until_true,
};
use crate::adaptive_slot_support::{
    AdaptiveSlotWorkerRun, SlotUsageSampler, app_worker_id, await_rpc_after_restart,
    count_register_batches, invoke_blocking_rpc, occupy_every_slot, require_flows_completed,
    require_register_batches_per_window, require_registered_streams, require_slot_state,
    start_flows, start_flows_concurrently, start_parked_flow, test_runtime, windows_to_floor,
};
use crate::adaptive_slot_workflow::{
    AdaptiveSlotFlow, AdaptiveSlotHandlers, AdaptiveSlotParkedFlow, MODE_BLOCK, MODE_QUICK,
    MODE_STUBBORN, STUBBORN_RPC_KEY,
};

const SECOND: Duration = Duration::from_secs(1);

fn require_single_attempts(handlers: &AdaptiveSlotHandlers) {
    for entry in handlers.drain_entries() {
        assert_eq!(1, entry.attempt, "Flow {} spent a retry", entry.flow_id);
    }
}

/// Design Tests 2, 3, 4, 5, 9, and 11.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_grow_and_shrink() {
    let runtime = test_runtime();
    let server = AdaptiveSlotTestServer::start(ServerLimits::default());
    let proxy = PullSlotProxy::start(&runtime, &server.address);
    let handlers = AdaptiveSlotHandlers::new(false);
    let app_worker_id = app_worker_id("adaptive-grow");
    let run = AdaptiveSlotWorkerRun::start(
        &runtime,
        &proxy.address,
        &handlers,
        PullWorkerOptions::new()
            .max_concurrent_tasks(16)
            .min_open_slots(3)
            .app_worker_id(&app_worker_id)
            .shrink_window_for_test(SECOND),
    );
    run.wait_registered(Duration::from_secs(60));

    // Test 2: an idle adaptive Worker holds exactly L + R streams.
    require_registered_streams(&proxy, &app_worker_id, 2, 1);
    let state = run.state();
    assert!(state.is_adaptive);
    assert_eq!(2, state.open);
    assert_eq!(1, state.rpc_only_open);
    assert_eq!(2, state.floor);
    assert_eq!(15, state.effective_ceiling);
    assert_eq!(16, state.total_capacity);
    assert_eq!(1, state.rpc_only_slots);

    // Test 3: a burst of N long Steps with L < N <= E grows to at least N and at most min(E, 2N).
    let client = Arc::new(server.new_client(&run.registry));
    let mut flow_ids = start_flows(&client, &handlers, MODE_BLOCK, 5);
    handlers.await_entries(5);
    require_slot_state(&run, "five busy Steps", 30 * SECOND, |state| {
        state.busy == 5 && state.registering == 0
    });
    let state = run.state();
    assert!((5..=10).contains(&state.open), "{state:?}");

    // Test 9: growth up to E keeps the RPC-only stream at R, and an RPC runs while every Step slot is busy.
    flow_ids.extend(start_flows(&client, &handlers, MODE_BLOCK, 10));
    handlers.await_entries(10);
    require_slot_state(&run, "fifteen busy Steps", 30 * SECOND, |state| {
        state.busy == 15 && state.open == 15 && state.registering == 0
    });
    require_registered_streams(&proxy, &app_worker_id, 15, 1);
    assert_eq!(1, run.state().rpc_only_open);
    let status: String = client
        .client
        .invoke_rpc_without_input(&flow_ids[0], AdaptiveSlotFlow::GET_SLOT_STATUS)
        .expect("RPC while every Step slot is busy");
    assert_eq!("ok", status);
    handlers.release();
    require_flows_completed(&client, &flow_ids);

    // Tests 4 and 5: streams return to the floor while short Steps keep arriving, and no Step spends a retry.
    let is_trickling = Arc::new(AtomicBool::new(true));
    let trickle = {
        let client = Arc::clone(&client);
        let handlers = Arc::clone(&handlers);
        let is_trickling = Arc::clone(&is_trickling);
        thread::spawn(move || {
            let mut trickle_ids = Vec::new();
            while is_trickling.load(Ordering::Acquire) {
                trickle_ids.extend(start_flows(&client, &handlers, MODE_QUICK, 1));
                thread::sleep(Duration::from_millis(100));
            }
            trickle_ids
        })
    };
    let shrink_deadline = Duration::from_secs(windows_to_floor(15, 2) + 5);
    let is_at_floor = eventually(shrink_deadline, || run.state().open == 2);
    is_trickling.store(false, Ordering::Release);
    let trickle_ids = trickle.join().expect("join trickle");
    assert!(
        is_at_floor,
        "streams did not return to the floor: {:?}",
        run.state()
    );
    assert!(!trickle_ids.is_empty());
    require_flows_completed(&client, &trickle_ids);
    require_single_attempts(&handlers);
    let state = run.state();
    assert!(state.streams_grown >= 13, "{state:?}");
    assert!(state.streams_shrunk >= 13, "{state:?}");

    // Test 11 and invariant 5: the count stays at the floor, and no stream closed on purpose
    // reopens, for longer than two maximum reconnect backoffs (2 x 7.5 s).
    require_registered_streams(&proxy, &app_worker_id, 2, 1);
    let since = Instant::now();
    assert!(
        never(16 * SECOND, || run.state().open != 2
            || !proxy.registers_since(&app_worker_id, since).is_empty()),
        "streams rose above the floor at rest, or a stream closed on purpose registered again\n{}",
        proxy.describe(&app_worker_id)
    );
}

/// The RPC variant of design Test 3, where S + R serve the burst.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_rpc_burst() {
    let runtime = test_runtime();
    let server = AdaptiveSlotTestServer::start(ServerLimits::default());
    let handlers = AdaptiveSlotHandlers::new(false);
    let run = AdaptiveSlotWorkerRun::start(
        &runtime,
        &server.address,
        &handlers,
        PullWorkerOptions::new()
            .max_concurrent_tasks(8)
            .min_open_slots(2)
            .app_worker_id(app_worker_id("adaptive-rpc"))
            .shrink_window_for_test(Duration::from_secs(3600)),
    );
    run.wait_registered(Duration::from_secs(60));
    let client = Arc::new(server.new_client(&run.registry));
    let parked_id = start_parked_flow(&client, &handlers);
    const RPC_COUNT: usize = 6;
    let results: Vec<_> = (0..RPC_COUNT)
        .map(|_| invoke_blocking_rpc(&client, &parked_id))
        .collect();
    handlers.await_entries(RPC_COUNT);
    // E = 7 and R = 1: S + R >= min(N, E + R) and S <= min(E, 2N).
    let state = run.state();
    assert!(state.open + state.rpc_only_open >= RPC_COUNT, "{state:?}");
    assert!(state.open <= 7, "{state:?}");
    assert_eq!(1, state.rpc_only_open);
    handlers.release();
    for result in results {
        result
            .recv_timeout(Duration::from_secs(60))
            .expect("RPC returned")
            .expect("RPC succeeded");
    }
}

/// Design Tests 6 and 18.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_soft_rejection_at_node_limit() {
    let runtime = test_runtime();
    let server = AdaptiveSlotTestServer::start(ServerLimits {
        max_concurrent_tasks_cap: Some(20),
        max_streams: Some(20),
    });
    let proxy = PullSlotProxy::start(&runtime, &server.address);
    let fixed_handlers = AdaptiveSlotHandlers::new(false);
    let mut fixed = AdaptiveSlotWorkerRun::start_parked_only(
        &runtime,
        &server.address,
        &fixed_handlers,
        PullWorkerOptions::new()
            .max_concurrent_tasks(14)
            .app_worker_id(app_worker_id("fixed-filler")),
    );
    fixed.wait_registered(Duration::from_secs(60));
    let handlers = AdaptiveSlotHandlers::new(false);
    let app_worker_id = app_worker_id("adaptive-limit");
    let window = 2 * SECOND;
    let mut run = AdaptiveSlotWorkerRun::start(
        &runtime,
        &proxy.address,
        &handlers,
        PullWorkerOptions::new()
            .max_concurrent_tasks(12)
            .min_open_slots(3)
            .app_worker_id(&app_worker_id)
            .shrink_window_for_test(window),
    );
    run.wait_registered(Duration::from_secs(60));
    require_registered_streams(&proxy, &app_worker_id, 2, 1);
    let client = server.new_client(&run.registry);
    let flow_ids = start_flows(&client, &handlers, MODE_BLOCK, 10);

    // 14 + 3 streams leave room for 3: growth reaches S = 5 and a partly rejected batch clamps G to S + H.
    require_slot_state(
        &run,
        "growth clamped at the node limit",
        30 * SECOND,
        |state| {
            state.open == 5
                && state.busy == 5
                && state.registering == 0
                && state.growth_target == 5
                && state.grow_rejected_at_cap > 0
        },
    );
    let state = run.state();
    assert_eq!(11, state.effective_ceiling);
    assert_eq!(state.open + state.orphaned_handlers, state.growth_target);
    run.require_no_permanent_registration_error();

    // At most one growth batch per window while rejections continue: a batch is min(E - S, S) = 5.
    let since = Instant::now();
    assert!(
        eventually(6 * window, || count_register_batches(
            &proxy.registers_since(&app_worker_id, since),
            window
        ) >= 3),
        "the Worker did not retry growth once per window\n{}",
        proxy.describe(&app_worker_id)
    );
    require_register_batches_per_window(&proxy.registers_since(&app_worker_id, since), window, 5);
    run.require_no_permanent_registration_error();

    // Room returns: growth resumes after the next window, and G returns to E.
    fixed.stop(30 * SECOND).expect("stop the filler Worker");
    require_slot_state(
        &run,
        "growth after the node limit cleared",
        30 * SECOND,
        |state| {
            state.busy == 10
                && state.registering == 0
                && state.growth_target == state.effective_ceiling
        },
    );
    handlers.release();
    require_flows_completed(&client, &flow_ids);
    require_single_attempts(&handlers);
}

/// Design Test 19.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_floor_retry_latch() {
    let runtime = test_runtime();
    let server = AdaptiveSlotTestServer::start(ServerLimits {
        max_concurrent_tasks_cap: Some(20),
        max_streams: Some(20),
    });
    let proxy = PullSlotProxy::start(&runtime, &server.address);
    let handlers = AdaptiveSlotHandlers::new(false);
    let app_worker_id = app_worker_id("adaptive-latch");
    let window = SECOND;
    let mut run = AdaptiveSlotWorkerRun::start(
        &runtime,
        &proxy.address,
        &handlers,
        PullWorkerOptions::new()
            .max_concurrent_tasks(12)
            .reserved_rpc_tasks(1)
            .min_open_slots(3)
            .app_worker_id(&app_worker_id)
            .shrink_window_for_test(window),
    );
    run.wait_registered(Duration::from_secs(60));
    require_registered_streams(&proxy, &app_worker_id, 2, 1);
    let mut competitor = PullSlotCompetitor::start(
        &runtime,
        &server.address,
        &crate::adaptive_slot_support::app_worker_id("competitor"),
        18,
    );
    assert!(eventually(20 * SECOND, || competitor.held_streams() == 17));

    // Resetting a floor stream frees a node slot; the competitor takes it, so the reconnect is rejected softly.
    let since = Instant::now();
    assert!(proxy.reset_registered_stream(&app_worker_id, STEP_AND_RPC));
    require_slot_state(&run, "the reset stream ended", 5 * SECOND, |state| {
        state.open - state.registering == 1
    });
    // One reconnect after the reset, then one retry per window: wait for three window retries.
    let violation = Mutex::new(String::new());
    assert!(
        eventually(6 * window, || {
            let state = run.state();
            if state.open - state.registering > 1 || !run.worker.pull_registration_state().is_ready
            {
                *violation.lock().expect("violation lock") = format!(
                    "a stream registered at the node limit, or readiness dropped: {state:?}"
                );
                return true;
            }
            step_events(&proxy.registers_since(&app_worker_id, since)).len() >= 4
        }),
        "the Worker did not retry at each window\n{}",
        proxy.describe(&app_worker_id)
    );
    assert_eq!("", *violation.lock().expect("violation lock"));
    let registers = step_events(&proxy.registers_since(&app_worker_id, since));
    for pair in registers[1..].windows(2) {
        assert!(
            pair[1].at.duration_since(pair[0].at) >= window * 8 / 10,
            "a Register followed a rejected stream's end at once\n{}",
            proxy.describe(&app_worker_id)
        );
    }
    assert_eq!(1, proxy.registered_streams(&app_worker_id, STEP_AND_RPC));
    assert!(run.state().grow_rejected_at_cap >= 2);
    run.require_no_permanent_registration_error();

    // A competitor stream that ends does not clear this Worker's latch.
    let before_close = step_events(&proxy.registers_since(&app_worker_id, since));
    let last_register_before_close = before_close.last().expect("a Register").at;
    let competitor_closed_at = Instant::now();
    competitor.close_one_stream();
    assert!(
        eventually(3 * window, || !step_events(
            &proxy.registers_since(&app_worker_id, competitor_closed_at)
        )
        .is_empty()),
        "the Worker stopped retrying at the window"
    );
    let next_register =
        step_events(&proxy.registers_since(&app_worker_id, competitor_closed_at))[0].at;
    assert!(
        next_register.duration_since(last_register_before_close) >= window * 8 / 10,
        "the competitor's close triggered a Register"
    );

    // When the competitor leaves, the floor comes back within one window.
    competitor.stop();
    assert!(eventually(2 * window + 3 * SECOND, || {
        let state = run.state();
        proxy.registered_streams(&app_worker_id, STEP_AND_RPC) == 2
            && state.open == 2
            && state.registering == 0
    }));
    assert!(run.worker.pull_registration_state().is_ready);
}

/// Design Test 14.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_cap_of_one() {
    captured_logs::install();
    let runtime = test_runtime();
    let cpu_count = std::thread::available_parallelism()
        .expect("read CPU count")
        .get();

    // Fixed with an unset ceiling probes and clamps (F4).
    {
        let server = AdaptiveSlotTestServer::start(ServerLimits::cap(1));
        let proxy = PullSlotProxy::start(&runtime, &server.address);
        let release_acknowledgements = proxy.hold_acknowledgements();
        let app_worker_id = app_worker_id("fixed-probe");
        let run = AdaptiveSlotWorkerRun::start(
            &runtime,
            &proxy.address,
            &AdaptiveSlotHandlers::new(false),
            PullWorkerOptions::new().app_worker_id(&app_worker_id),
        );
        // F4: only the probe stream registers before the first acknowledgement.
        let origin = Instant::now() - Duration::from_secs(3600);
        assert!(eventually(10 * SECOND, || proxy
            .registers_since(&app_worker_id, origin)
            .len()
            == 1));
        assert!(
            never(Duration::from_millis(300), || proxy
                .registers_since(&app_worker_id, origin)
                .len()
                > 1),
            "a stream registered before the probe's acknowledgement"
        );
        release_acknowledgements();
        require_cap_of_one_worker_serves(&server, &proxy, &run, &app_worker_id);
        if cpu_count > 1 {
            assert_eq!(
                1,
                captured_logs::warnings_with(
                    &app_worker_id,
                    &["clamped max_concurrent_tasks", &cpu_count.to_string(), "1"]
                )
            );
        }
        assert!(!run.state().is_adaptive);
    }

    // Adaptive with a floor of one, with the ceiling set or not.
    for max_concurrent_tasks in [Some(4), None] {
        let server = AdaptiveSlotTestServer::start(ServerLimits::cap(1));
        let proxy = PullSlotProxy::start(&runtime, &server.address);
        let app_worker_id = app_worker_id("adaptive-one");
        let mut options = PullWorkerOptions::new()
            .min_open_slots(1)
            .app_worker_id(&app_worker_id);
        match max_concurrent_tasks {
            Some(value) => options = options.max_concurrent_tasks(value),
            None if cpu_count < 2 => options = options.max_concurrent_tasks(2),
            None => {}
        }
        let run = AdaptiveSlotWorkerRun::start(
            &runtime,
            &proxy.address,
            &AdaptiveSlotHandlers::new(false),
            options,
        );
        require_cap_of_one_worker_serves(&server, &proxy, &run, &app_worker_id);
        assert!(run.state().is_adaptive);
        assert_eq!(
            1,
            captured_logs::warnings_with(&app_worker_id, &["clamped max_concurrent_tasks", "1"])
        );
        let origin = Instant::now() - Duration::from_secs(3600);
        for register in proxy.registers_since(&app_worker_id, origin) {
            assert_eq!(STEP_AND_RPC, register.slot_kind);
        }
    }

    // An explicit reserved RPC slot fails start, in either mode.
    let fixed_options = if cpu_count < 2 {
        PullWorkerOptions::new()
            .max_concurrent_tasks(2)
            .min_open_slots(2)
            .reserved_rpc_tasks(1)
    } else {
        PullWorkerOptions::new().reserved_rpc_tasks(1)
    };
    let adaptive_options = PullWorkerOptions::new()
        .max_concurrent_tasks(4)
        .min_open_slots(2)
        .reserved_rpc_tasks(1);
    for (mode, options) in [("fixed", fixed_options), ("adaptive", adaptive_options)] {
        let server = AdaptiveSlotTestServer::start(ServerLimits::cap(1));
        let mut run = AdaptiveSlotWorkerRun::start(
            &runtime,
            &server.address,
            &AdaptiveSlotHandlers::new(false),
            options.app_worker_id(app_worker_id("reserved-one")),
        );
        let error = run
            .await_start_result(30 * SECOND)
            .expect_err("start fails")
            .to_string();
        if mode == "fixed" && cpu_count < 2 {
            assert!(
                error.contains(
                    "max_concurrent_tasks 2 exceeds Server matching.maxConcurrentTasksCap 1"
                ),
                "{error}"
            );
            continue;
        }
        assert!(
            error.contains("reserved_rpc_tasks 1 must be less than the slot ceiling 1"),
            "{mode}: {error}"
        );
        assert!(
            error.contains("matching.maxConcurrentTasksCap 1"),
            "{mode}: {error}"
        );
    }

    // Fixed with an explicit ceiling above the cap fails start.
    let server = AdaptiveSlotTestServer::start(ServerLimits::cap(1));
    let mut run = AdaptiveSlotWorkerRun::start(
        &runtime,
        &server.address,
        &AdaptiveSlotHandlers::new(false),
        PullWorkerOptions::new()
            .max_concurrent_tasks(2)
            .app_worker_id(app_worker_id("fixed-two")),
    );
    let error = run
        .await_start_result(30 * SECOND)
        .expect_err("start fails")
        .to_string();
    assert!(
        error.contains("max_concurrent_tasks 2 exceeds Server matching.maxConcurrentTasksCap 1"),
        "{error}"
    );
}

fn require_cap_of_one_worker_serves(
    server: &AdaptiveSlotTestServer,
    proxy: &PullSlotProxy,
    run: &AdaptiveSlotWorkerRun,
    app_worker_id: &str,
) {
    run.wait_registered(Duration::from_secs(60));
    require_slot_state(
        run,
        "one Step-and-RPC stream at cap 1",
        30 * SECOND,
        |state| {
            state.total_capacity == 1
                && state.rpc_only_slots == 0
                && state.open == 1
                && state.rpc_only_open == 0
                && state.registering == 0
        },
    );
    require_registered_streams(proxy, app_worker_id, 1, 0);
    let registration = run.worker.pull_registration_state();
    assert!(registration.is_ready);
    assert_eq!(1, registration.queues.len(), "{registration:?}");
    let handlers = AdaptiveSlotHandlers::new(false);
    let client = server.new_client(&run.registry);
    let flow_ids = start_flows(&client, &handlers, MODE_QUICK, 2);
    require_flows_completed(&client, &flow_ids);
    let parked_id = start_parked_flow(&client, &handlers);
    let status: String = client
        .client
        .invoke_rpc_without_input(&parked_id, AdaptiveSlotParkedFlow::GET_SLOT_STATUS)
        .expect("RPC at cap 1");
    assert_eq!("ok", status);
}

/// Design Tests 20a and 20b.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_ceiling_above_cap() {
    captured_logs::install();
    let runtime = test_runtime();

    // 20a: adaptive mode clamps.
    {
        let server = AdaptiveSlotTestServer::start(ServerLimits::default());
        let handlers = AdaptiveSlotHandlers::new(false);
        let app_worker_id = app_worker_id("adaptive-300");
        let run = AdaptiveSlotWorkerRun::start(
            &runtime,
            &server.address,
            &handlers,
            PullWorkerOptions::new()
                .max_concurrent_tasks(300)
                .min_open_slots(8)
                .app_worker_id(&app_worker_id)
                .shrink_window_for_test(Duration::from_secs(3600)),
        );
        run.wait_registered(Duration::from_secs(120));
        assert_eq!(
            1,
            captured_logs::warnings_with(
                &app_worker_id,
                &["clamped max_concurrent_tasks", "300", "256"]
            )
        );
        assert_eq!(
            1,
            captured_logs::warnings_with(&app_worker_id, &["clamped max_concurrent_tasks"])
        );
        assert_eq!(256, run.state().total_capacity);
        let mut sampler = SlotUsageSampler::start(&run.worker);
        let client = Arc::new(server.new_client(&run.registry));
        let flow_ids = start_flows_concurrently(&client, &handlers, MODE_BLOCK, 400);
        require_slot_state(
            &run,
            "every slot busy at the clamped ceiling",
            90 * SECOND,
            |state| state.busy == 255 && state.open + state.rpc_only_open == 256,
        );
        assert_eq!(0, run.state().grow_rejected_at_cap);
        handlers.release();
        require_flows_completed(&client, &flow_ids);
        sampler.require_within_capacity();
        require_single_attempts(&handlers);
    }

    // 20b: fixed mode fails start.
    let server = AdaptiveSlotTestServer::start(ServerLimits::default());
    let mut run = AdaptiveSlotWorkerRun::start(
        &runtime,
        &server.address,
        &AdaptiveSlotHandlers::new(false),
        PullWorkerOptions::new()
            .max_concurrent_tasks(300)
            .app_worker_id(app_worker_id("fixed-300")),
    );
    let error = run
        .await_start_result(30 * SECOND)
        .expect_err("start fails")
        .to_string();
    assert!(
        error
            .contains("max_concurrent_tasks 300 exceeds Server matching.maxConcurrentTasksCap 256"),
        "{error}"
    );
}

/// Design Test 17a.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_cap_drop_to_one() {
    let runtime = test_runtime();
    let mut server = AdaptiveSlotTestServer::start(ServerLimits::cap(4));
    let proxy = PullSlotProxy::start(&runtime, &server.address);
    let handlers = AdaptiveSlotHandlers::new(true);
    let app_worker_id = app_worker_id("adaptive-drop");
    let mut run = AdaptiveSlotWorkerRun::start(
        &runtime,
        &proxy.address,
        &handlers,
        PullWorkerOptions::new()
            .max_concurrent_tasks(4)
            .min_open_slots(2)
            .app_worker_id(&app_worker_id)
            .stop_on_permanent_stream_rejection(true)
            .shrink_window_for_test(SECOND),
    );
    run.wait_registered(Duration::from_secs(60));
    let client = Arc::new(server.new_client(&run.registry));
    let parked_id = start_parked_flow(&client, &handlers);
    let (flow_ids, rpc_result) = occupy_every_slot(&run, &client, &handlers, &parked_id);

    // The restart lowers the cap to 1; the proxy lets the RPC-only stream register first.
    server.kill();
    proxy.hold_step_registers_until_rpc_only_acknowledged();
    server.restart(ServerLimits::cap(1));
    proxy.set_upstream(&server.address);
    let mut released: Vec<&str> = flow_ids.iter().map(String::as_str).collect();
    released.push(STUBBORN_RPC_KEY);
    handlers.release_stubborn(&released);
    await_rpc_after_restart(&rpc_result);

    require_slot_state(
        &run,
        "one Step-and-RPC stream at cap 1",
        30 * SECOND,
        |state| {
            state.total_capacity == 1
                && state.rpc_only_slots == 0
                && state.rpc_only_open == 0
                && state.open == 1
                && state.registering == 0
                && state.orphaned_handlers == 0
                && state.orphaned_rpc_only_handlers == 0
        },
    );
    assert!(eventually(10 * SECOND, || {
        proxy.registered_streams(&app_worker_id, STEP_AND_RPC) == 1
            && proxy.registered_streams(&app_worker_id, RPC_ONLY) == 0
    }));
    assert!(
        run.state().grow_rejected_at_cap >= 1,
        "the Step-and-RPC Register was not rejected at the cap\n{}",
        proxy.describe(&app_worker_id)
    );
    run.require_no_permanent_registration_error();

    // Steps and RPCs keep running on the one stream.
    let restarted_client = server.new_client(&run.registry);
    require_flows_completed(&restarted_client, &flow_ids);
    let quick_ids = start_flows(&restarted_client, &handlers, MODE_QUICK, 2);
    require_flows_completed(&restarted_client, &quick_ids);
    let status: String = restarted_client
        .client
        .invoke_rpc_without_input(&parked_id, AdaptiveSlotParkedFlow::GET_SLOT_STATUS)
        .expect("RPC at cap 1");
    assert_eq!("ok", status);
}

/// Design Test 17b: fixed mode keeps today's behavior.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn fixed_slot_streams_cap_drop_to_one() {
    let runtime = test_runtime();
    for should_stop in [true, false] {
        let mut server = AdaptiveSlotTestServer::start(ServerLimits::cap(4));
        let proxy = PullSlotProxy::start(&runtime, &server.address);
        let app_worker_id = app_worker_id("fixed-drop");
        let mut run = AdaptiveSlotWorkerRun::start(
            &runtime,
            &proxy.address,
            &AdaptiveSlotHandlers::new(false),
            PullWorkerOptions::new()
                .max_concurrent_tasks(4)
                .app_worker_id(&app_worker_id)
                .stop_on_permanent_stream_rejection(should_stop)
                .shrink_window_for_test(SECOND),
        );
        run.wait_registered(Duration::from_secs(60));
        require_registered_streams(&proxy, &app_worker_id, 3, 1);
        server.kill();
        proxy.hold_step_registers_until_rpc_only_acknowledged();
        let restarted_at = Instant::now();
        server.restart(ServerLimits::cap(1));
        proxy.set_upstream(&server.address);
        if should_stop {
            let error = run
                .await_start_result(45 * SECOND)
                .expect_err("Start stops on the permanent rejection");
            let rejection = error
                .pull_registration_error()
                .unwrap_or_else(|| panic!("not a registration error: {error}"));
            assert_eq!("STEP_AND_RPC", rejection.queue);
            assert_eq!(GrpcCode::ResourceExhausted, rejection.status.code());
            continue;
        }
        let step_queue = || {
            run.worker
                .pull_registration_state()
                .queues
                .into_iter()
                .find(|queue| queue.queue == "STEP_AND_RPC")
                .expect("the Step-and-RPC slot kind")
        };
        assert!(
            eventually(45 * SECOND, || {
                let queue = step_queue();
                !queue.has_registered_stream
                    && queue
                        .last_permanent_error
                        .is_some_and(|status| status.code() == GrpcCode::ResourceExhausted)
            }),
            "{}",
            proxy.describe(&app_worker_id)
        );
        assert!(!run.worker.pull_registration_state().is_ready);
        // Each of the three Step-and-RPC runners sends its first Register after its own transient
        // backoff, so wait until every one of them met the cap.
        assert!(
            eventually(45 * SECOND, || step_events(
                &proxy.rejections_since(&app_worker_id, restarted_at)
            )
            .len()
                >= 3),
            "{}",
            proxy.describe(&app_worker_id)
        );
        // Today's permanent-error backoff is one minute, so no Step-and-RPC stream registers again soon.
        let rejected_at = Instant::now();
        assert!(
            never(5 * SECOND, || !step_events(
                &proxy.registers_since(&app_worker_id, rejected_at)
            )
            .is_empty()),
            "a Step-and-RPC stream retried before the permanent-error backoff\n{}",
            proxy.describe(&app_worker_id)
        );
        assert!(!run.state().is_adaptive);
        assert_eq!(0, run.state().grow_rejected_at_cap);
    }
}

/// An adaptive Worker whose every slot runs a handler that ignores cancellation.
struct OrphanedHandlerScenario {
    server: AdaptiveSlotTestServer,
    proxy: PullSlotProxy,
    run: AdaptiveSlotWorkerRun,
    handlers: Arc<AdaptiveSlotHandlers>,
    app_worker_id: String,
    flow_ids: Vec<String>,
    rpc_result: std::sync::mpsc::Receiver<dex_sdk::SdkResult<String>>,
    sampler: SlotUsageSampler,
}

impl OrphanedHandlerScenario {
    fn start() -> Self {
        let runtime = test_runtime();
        let server = AdaptiveSlotTestServer::start(ServerLimits::cap(4));
        let proxy = PullSlotProxy::start(&runtime, &server.address);
        let handlers = AdaptiveSlotHandlers::new(true);
        let app_worker_id = app_worker_id("adaptive-orphan");
        let run = AdaptiveSlotWorkerRun::start(
            &runtime,
            &proxy.address,
            &handlers,
            PullWorkerOptions::new()
                .max_concurrent_tasks(4)
                .min_open_slots(2)
                .app_worker_id(&app_worker_id)
                .shrink_window_for_test(Duration::from_secs(3600)),
        );
        run.wait_registered(Duration::from_secs(60));
        let client = Arc::new(server.new_client(&run.registry));
        let parked_id = start_parked_flow(&client, &handlers);
        let (flow_ids, rpc_result) = occupy_every_slot(&run, &client, &handlers, &parked_id);
        let sampler = SlotUsageSampler::start(&run.worker);
        Self {
            server,
            proxy,
            run,
            handlers,
            app_worker_id,
            flow_ids,
            rpc_result,
            sampler,
        }
    }
}

/// Design Tests 15 and 8: the cap is unchanged across the restart.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_orphaned_handlers_across_restart() {
    let mut scenario = OrphanedHandlerScenario::start();
    let (proxy, handlers, app_worker_id) =
        (&scenario.proxy, &scenario.handlers, &scenario.app_worker_id);
    let killed_at = Instant::now();
    scenario.server.kill();
    scenario.server.restart(ServerLimits::cap(4));
    proxy.set_upstream(&scenario.server.address);
    // No early registration: every slot still has a running handler.
    require_slot_state(
        &scenario.run,
        "every handler orphaned",
        10 * SECOND,
        |state| {
            state.open == 0
                && state.rpc_only_open == 0
                && state.orphaned_handlers == 3
                && state.orphaned_rpc_only_handlers == 1
        },
    );
    assert!(
        never(SECOND, || !proxy
            .registers_since(app_worker_id, killed_at)
            .is_empty()
            || !scenario.run.worker.pull_registration_state().is_ready),
        "a stream registered while orphaned handlers filled the capacity, or readiness dropped"
    );

    // Freed capacity goes to one Step-and-RPC stream first, then to the RPC-only stream.
    handlers.release_stubborn(&[&scenario.flow_ids[0]]);
    assert!(eventually(10 * SECOND, || proxy
        .registered_streams(app_worker_id, STEP_AND_RPC)
        == 1));
    handlers.release_stubborn(&[STUBBORN_RPC_KEY]);
    assert!(eventually(10 * SECOND, || proxy
        .registered_streams(app_worker_id, RPC_ONLY)
        == 1));
    let registers = proxy.registers_since(app_worker_id, killed_at);
    assert_eq!(STEP_AND_RPC, registers[0].slot_kind);
    assert_eq!(RPC_ONLY, registers[registers.len() - 1].slot_kind);
    // Test 8: the Worker re-registers at most L + R streams first.
    assert!(registers.len() <= 2, "{}", proxy.describe(app_worker_id));
    let rest: Vec<&str> = scenario.flow_ids[1..].iter().map(String::as_str).collect();
    handlers.release_stubborn(&rest);
    await_rpc_after_restart(&scenario.rpc_result);
    let restarted_client = scenario.server.new_client(&scenario.run.registry);
    require_flows_completed(&restarted_client, &scenario.flow_ids);
    assert!(scenario.run.worker.pull_registration_state().is_ready);
    // With the cap unchanged, S + H + Sr + Hr <= C held at every sample.
    scenario.sampler.require_within_capacity();
}

/// Design Test 15: a restart that lowers the cap below the orphaned handler count (F5).
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_orphaned_handlers_with_lower_cap() {
    let mut scenario = OrphanedHandlerScenario::start();
    let (proxy, handlers, app_worker_id) =
        (&scenario.proxy, &scenario.handlers, &scenario.app_worker_id);
    scenario.server.kill();
    scenario.server.restart(ServerLimits::cap(2));
    proxy.set_upstream(&scenario.server.address);
    require_slot_state(
        &scenario.run,
        "every handler orphaned",
        10 * SECOND,
        |state| state.orphaned_handlers == 3 && state.orphaned_rpc_only_handlers == 1,
    );
    let released_at = Instant::now();
    handlers.release_stubborn(&[&scenario.flow_ids[0]]);
    // The first acknowledgement reveals cap 2 while usage is 4: the Worker registers nothing more until usage drops below 2.
    assert!(eventually(10 * SECOND, || proxy
        .acknowledgements_since(app_worker_id, released_at)
        .len()
        == 1));
    let acknowledged_at = proxy.acknowledgements_since(app_worker_id, released_at)[0].at;
    require_slot_state(
        &scenario.run,
        "capacity lowered to 2",
        10 * SECOND,
        |state| state.total_capacity == 2 && state.open == 0 && state.registering == 0,
    );
    handlers.release_stubborn(&[&scenario.flow_ids[1]]);
    require_slot_state(
        &scenario.run,
        "the second handler returned",
        10 * SECOND,
        |state| state.orphaned_handlers == 1,
    );
    // Usage is still 2 of 2 with one Step and one RPC-only handler running.
    assert!(
        never(SECOND, || !proxy
            .registers_since(app_worker_id, acknowledged_at + Duration::from_millis(1))
            .is_empty()),
        "a Register went out while usage reached the new cap\n{}",
        proxy.describe(app_worker_id)
    );
    handlers.release_stubborn(&[&scenario.flow_ids[2], STUBBORN_RPC_KEY]);
    require_slot_state(
        &scenario.run,
        "usage converged to the new cap",
        20 * SECOND,
        |state| {
            state.open == 1
                && state.rpc_only_open == 1
                && state.registering == 0
                && state.orphaned_handlers == 0
                && state.orphaned_rpc_only_handlers == 0
        },
    );
    let state = scenario.run.state();
    assert!(state.open + state.rpc_only_open <= state.total_capacity);
    require_registered_streams(proxy, app_worker_id, 1, 1);
    // F5 allows the transient excess, so this case asserts convergence instead of a bound at every sample.
    let samples_above_cap = scenario.sampler.stop_sampling().len();
    assert!(samples_above_cap <= 8, "{samples_above_cap}");
    await_rpc_after_restart(&scenario.rpc_result);
    let restarted_client = scenario.server.new_client(&scenario.run.registry);
    require_flows_completed(&restarted_client, &scenario.flow_ids);
}

/// Design Test 15: the failover lands while a growth batch is registering.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_failover_during_growth_batch() {
    let runtime = test_runtime();
    let mut server = AdaptiveSlotTestServer::start(ServerLimits::cap(8));
    let proxy = PullSlotProxy::start(&runtime, &server.address);
    let handlers = AdaptiveSlotHandlers::new(false);
    let app_worker_id = app_worker_id("adaptive-growth-failover");
    let mut run = AdaptiveSlotWorkerRun::start(
        &runtime,
        &proxy.address,
        &handlers,
        PullWorkerOptions::new()
            .max_concurrent_tasks(8)
            .min_open_slots(3)
            .app_worker_id(&app_worker_id)
            .shrink_window_for_test(Duration::from_secs(3600)),
    );
    run.wait_registered(Duration::from_secs(60));
    require_registered_streams(&proxy, &app_worker_id, 2, 1);
    let release_growth = proxy.hold_step_registers();
    let client = server.new_client(&run.registry);
    let flow_ids = start_flows(&client, &handlers, MODE_STUBBORN, 2);
    // Both floor streams are busy, so a growth batch of 2 waits at the proxy.
    assert!(eventually(20 * SECOND, || proxy.held_step_registers() == 2));
    require_slot_state(&run, "a growth batch registering", 30 * SECOND, |state| {
        state.busy == 2 && state.registering == 2
    });
    let mut sampler = SlotUsageSampler::start(&run.worker);
    let killed_at = Instant::now();
    server.kill();
    server.restart(ServerLimits::cap(8));
    proxy.set_upstream(&server.address);
    require_slot_state(&run, "the busy streams orphaned", 10 * SECOND, |state| {
        state.orphaned_handlers == 2 && state.registering == 2 && state.rpc_only_open == 1
    });
    require_registered_streams(&proxy, &app_worker_id, 0, 1);
    // The in-flight batch already holds the floor, so no other Step-and-RPC stream opens.
    assert!(step_events(&proxy.registers_since(&app_worker_id, killed_at)).is_empty());
    let released_at = Instant::now();
    release_growth();
    // The held batch registers on the new server; retried Steps may then grow the Worker further.
    assert!(
        eventually(20 * SECOND, || step_events(
            &proxy.acknowledgements_since(&app_worker_id, released_at)
        )
        .len()
            >= 2
            && proxy.registered_streams(&app_worker_id, STEP_AND_RPC) >= 2),
        "the growth batch did not register after the failover\n{}",
        proxy.describe(&app_worker_id)
    );
    let keys: Vec<&str> = flow_ids.iter().map(String::as_str).collect();
    handlers.release_stubborn(&keys);
    let restarted_client = server.new_client(&run.registry);
    require_flows_completed(&restarted_client, &flow_ids);
    require_slot_state(&run, "no orphaned handlers", 30 * SECOND, |state| {
        state.orphaned_handlers == 0 && state.registering == 0
    });
    sampler.require_within_capacity();
    run.require_no_permanent_registration_error();
}

/// Design Test 15: the failover lands while a shrink half-close is in flight, with a stubborn handler.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_failover_during_shrink_half_close() {
    let runtime = test_runtime();
    let mut server = AdaptiveSlotTestServer::start(ServerLimits::cap(8));
    let proxy = PullSlotProxy::start(&runtime, &server.address);
    let handlers = AdaptiveSlotHandlers::new(false);
    let app_worker_id = app_worker_id("adaptive-shrink-failover");
    let mut run = AdaptiveSlotWorkerRun::start(
        &runtime,
        &proxy.address,
        &handlers,
        PullWorkerOptions::new()
            .max_concurrent_tasks(8)
            .min_open_slots(3)
            .app_worker_id(&app_worker_id)
            .shrink_window_for_test(SECOND),
    );
    run.wait_registered(Duration::from_secs(60));
    let client = server.new_client(&run.registry);
    let flow_ids = start_flows(&client, &handlers, MODE_BLOCK, 3);
    // One handler ignores cancellation, so it outlives its stream in the crash.
    let stubborn_ids = start_flows(&client, &handlers, MODE_STUBBORN, 1);
    require_slot_state(&run, "four busy Steps", 30 * SECOND, |state| {
        state.busy == 4 && state.registering == 0
    });
    let release_half_closes = proxy.hold_half_closes();
    handlers.release();
    require_flows_completed(&client, &flow_ids);
    assert!(
        eventually(20 * SECOND, || proxy.held_half_closes() >= 1),
        "the Worker never shrank"
    );
    assert_eq!(1, run.state().busy);
    // The server still holds the half-closing stream when it crashes, and the stubborn handler still runs.
    let mut sampler = SlotUsageSampler::start(&run.worker);
    server.kill();
    require_slot_state(
        &run,
        "the stubborn handler orphaned",
        10 * SECOND,
        |state| state.orphaned_handlers == 1 && state.busy == 0,
    );
    server.restart(ServerLimits::cap(8));
    // Registers that reached the dead server were retries of the same runners, so count from the switch.
    let switched_at = Instant::now();
    proxy.set_upstream(&server.address);
    release_half_closes();
    require_registered_streams(&proxy, &app_worker_id, 2, 1);
    let is_shrunk_stream_reopened = || {
        step_events(&proxy.registers_since(&app_worker_id, switched_at)).len() > 2
            || proxy.registered_streams(&app_worker_id, STEP_AND_RPC) > 2
    };
    assert!(
        never(3 * SECOND, is_shrunk_stream_reopened),
        "a stream closed by shrink reopened after the failover\n{}",
        proxy.describe(&app_worker_id)
    );
    let state = run.state();
    assert_eq!(
        1, state.orphaned_handlers,
        "the orphaned handler left the capacity count early: {state:?}"
    );
    assert_eq!(2, state.open);
    // The returning handler frees a slot, but the floor is already open, so nothing reopens.
    let keys: Vec<&str> = stubborn_ids.iter().map(String::as_str).collect();
    handlers.release_stubborn(&keys);
    require_slot_state(
        &run,
        "the orphaned handler returned",
        30 * SECOND,
        |state| {
            state.orphaned_handlers == 0
                && state.open == 2
                && state.rpc_only_open == 1
                && state.registering == 0
        },
    );
    assert!(
        never(2 * SECOND, is_shrunk_stream_reopened),
        "a stream reopened when the orphaned handler returned"
    );
    let restarted_client = server.new_client(&run.registry);
    require_flows_completed(&restarted_client, &stubborn_ids);
    sampler.require_within_capacity();
    run.require_no_permanent_registration_error();
}

/// Design Tests 1 and 16.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn fixed_slot_streams_unchanged() {
    let runtime = test_runtime();
    let server = AdaptiveSlotTestServer::start(ServerLimits::default());
    let proxy = PullSlotProxy::start(&runtime, &server.address);
    let release_acknowledgements = proxy.hold_acknowledgements();
    let handlers = AdaptiveSlotHandlers::new(false);
    let app_worker_id = app_worker_id("fixed-parallel");
    let run = AdaptiveSlotWorkerRun::start(
        &runtime,
        &proxy.address,
        &handlers,
        PullWorkerOptions::new()
            .max_concurrent_tasks(4)
            .app_worker_id(&app_worker_id)
            .shrink_window_for_test(SECOND),
    );
    // Test 16: every stream opens before the first acknowledgement arrives.
    let origin = Instant::now() - Duration::from_secs(3600);
    assert!(eventually(10 * SECOND, || proxy
        .registers_since(&app_worker_id, origin)
        .len()
        == 4));
    release_acknowledgements();
    run.wait_registered(Duration::from_secs(60));
    let first_acknowledgement = proxy
        .first_acknowledgement_at()
        .expect("an acknowledgement");
    for register in proxy.registers_since(&app_worker_id, origin) {
        assert!(register.at < first_acknowledgement);
    }
    // Test 1: the count stays at the ceiling through load and idle periods.
    require_registered_streams(&proxy, &app_worker_id, 3, 1);
    let client = server.new_client(&run.registry);
    let flow_ids = start_flows(&client, &handlers, MODE_BLOCK, 3);
    handlers.await_entries(3);
    assert_eq!(3, run.state().open);
    handlers.release();
    require_flows_completed(&client, &flow_ids);
    let since = Instant::now();
    assert!(
        never(3 * SECOND, || run.state().open != 3
            || !proxy.registers_since(&app_worker_id, since).is_empty()),
        "a fixed Worker changed its stream count at rest"
    );
    let state = run.state();
    assert!(!state.is_adaptive);
    assert_eq!(3, state.open);
    assert_eq!(1, state.rpc_only_open);
    assert_eq!(0, state.streams_grown);
    assert_eq!(0, state.streams_shrunk);
    assert!(proxy.registers_since(&app_worker_id, since).is_empty());
    require_registered_streams(&proxy, &app_worker_id, 3, 1);
}

/// F5 for an attempt granted under the old cap: when another stream reports a lower cap during
/// its backoff, it sends no Register above that cap.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_grant_revoked_after_cap_drop() {
    let runtime = test_runtime();
    let mut server = AdaptiveSlotTestServer::start(ServerLimits::cap(8));
    let proxy = PullSlotProxy::start(&runtime, &server.address);
    let app_worker_id = app_worker_id("adaptive-revoke");
    let mut run = AdaptiveSlotWorkerRun::start(
        &runtime,
        &proxy.address,
        &AdaptiveSlotHandlers::new(false),
        PullWorkerOptions::new()
            .max_concurrent_tasks(8)
            .min_open_slots(3)
            .reserved_rpc_tasks(0)
            .app_worker_id(&app_worker_id)
            .shrink_window_for_test(Duration::from_secs(3600)),
    );
    run.wait_registered(Duration::from_secs(60));
    require_registered_streams(&proxy, &app_worker_id, 3, 0);

    // Every floor stream breaks and gets a reconnect grant under the last known cap of 8.
    server.kill();
    proxy.fail_step_registers_from_now();
    server.restart(ServerLimits::cap(2));
    proxy.set_upstream(&server.address);
    // One runner registers and learns cap 2 while the other two wait in their backoff.
    proxy.pass_next_step_register();
    require_slot_state(&run, "the lower cap is known", 30 * SECOND, |state| {
        state.total_capacity == 2
    });
    proxy.stop_failing_step_registers();
    let cap_known_at = Instant::now();
    // One registered stream leaves room for exactly one more; the other old grant must be revoked.
    assert!(
        never(8 * SECOND, || step_events(
            &proxy.registers_since(&app_worker_id, cap_known_at)
        )
        .len()
            > 1
            || !proxy
                .rejections_since(&app_worker_id, cap_known_at)
                .is_empty()),
        "a Register granted under the old cap went out above the new cap\n{}",
        proxy.describe(&app_worker_id)
    );
    require_slot_state(&run, "two streams at cap 2", 30 * SECOND, |state| {
        state.open == 2 && state.registering == 0
    });
    require_registered_streams(&proxy, &app_worker_id, 2, 0);
    run.require_no_permanent_registration_error();
}

/// Holds armed slot streams after their admission and before their Register is sent.
struct RegisterSendBarrier {
    is_armed: AtomicBool,
    held: AtomicUsize,
    passes: Semaphore,
    released: watch::Sender<bool>,
}

impl RegisterSendBarrier {
    async fn hold(self: Arc<Self>) {
        if !self.is_armed.load(Ordering::Acquire) {
            return;
        }
        self.held.fetch_add(1, Ordering::AcqRel);
        let mut released = self.released.subscribe();
        tokio::select! {
            permit = self.passes.acquire() => {
                permit.expect("the barrier keeps its semaphore open").forget();
            }
            _ = wait_until_true(&mut released) => {}
        }
        self.held.fetch_sub(1, Ordering::AcqRel);
    }
}

/// Admission is re-checked before the send; a cap drop after it gets a Server rejection, soft in
/// adaptive mode.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_cap_drop_between_admission_and_send() {
    let runtime = test_runtime();
    let mut server = AdaptiveSlotTestServer::start(ServerLimits::cap(8));
    let proxy = PullSlotProxy::start(&runtime, &server.address);
    let barrier = Arc::new(RegisterSendBarrier {
        is_armed: AtomicBool::new(false),
        held: AtomicUsize::new(0),
        passes: Semaphore::new(0),
        released: watch::channel(false).0,
    });
    let app_worker_id = app_worker_id("adaptive-barrier");
    let hook_barrier = Arc::clone(&barrier);
    let mut run = AdaptiveSlotWorkerRun::start(
        &runtime,
        &proxy.address,
        &AdaptiveSlotHandlers::new(false),
        PullWorkerOptions::new()
            .max_concurrent_tasks(8)
            .min_open_slots(3)
            .reserved_rpc_tasks(0)
            .app_worker_id(&app_worker_id)
            .shrink_window_for_test(SECOND)
            .before_register_send_for_test(move || Arc::clone(&hook_barrier).hold()),
    );
    run.wait_registered(Duration::from_secs(60));
    require_registered_streams(&proxy, &app_worker_id, 3, 0);

    // Every floor stream breaks; each runner is admitted under the old cap of 8 and waits before its send.
    barrier.is_armed.store(true, Ordering::Release);
    server.kill();
    assert!(
        eventually(30 * SECOND, || barrier.held.load(Ordering::Acquire) == 3),
        "the runners did not reach the send barrier"
    );
    server.restart(ServerLimits::cap(2));
    proxy.set_upstream(&server.address);
    // One runner sends, registers, and reports cap 2 while the other two stay admitted but unsent.
    barrier.passes.add_permits(1);
    require_slot_state(&run, "the lower cap is known", 30 * SECOND, |state| {
        state.total_capacity == 2
    });
    let cap_known_at = Instant::now();
    barrier.released.send_replace(true);
    // The two held attempts are re-checked after cap 2 is known: one is revoked and sends nothing.
    // A Register that passed its re-check just before the cap became known may still meet the cap
    // once; that rejection is soft, and the latch retries it at the next window.
    assert!(
        never(3 * SECOND, || {
            let rejections = proxy.rejections_since(&app_worker_id, cap_known_at).len();
            rejections > 1
                || step_events(&proxy.registers_since(&app_worker_id, cap_known_at)).len()
                    > 1 + rejections
                || proxy.registered_streams(&app_worker_id, STEP_AND_RPC) > 2
        }),
        "a revoked attempt sent a Register, or Registers exceeded the new cap\n{}",
        proxy.describe(&app_worker_id)
    );
    require_registered_streams(&proxy, &app_worker_id, 2, 0);
    require_slot_state(&run, "two streams at cap 2", 30 * SECOND, |state| {
        state.open == 2 && state.registering == 0
    });
    let rejections = proxy.rejections_since(&app_worker_id, cap_known_at).len() as u64;
    if rejections > 0 {
        assert!(
            run.state().grow_rejected_at_cap >= rejections,
            "the rejection was not handled as soft"
        );
    }
    run.require_no_permanent_registration_error();
}

/// One Register is blocked before it reaches the server. Cap updates and handlers on the other
/// streams must still progress, and Stop must still finish.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_blocked_register_send() {
    let runtime = test_runtime();
    let server = AdaptiveSlotTestServer::start(ServerLimits::cap(4));
    let proxy = PullSlotProxy::start(&runtime, &server.address);
    let release_blocked = proxy.block_next_step_registers(1);
    let handlers = AdaptiveSlotHandlers::new(false);
    let mut run = AdaptiveSlotWorkerRun::start(
        &runtime,
        &proxy.address,
        &handlers,
        PullWorkerOptions::new()
            .max_concurrent_tasks(8)
            .min_open_slots(3)
            .stop_drain_timeout(SECOND)
            .app_worker_id(app_worker_id("adaptive-blocked-send"))
            .shrink_window_for_test(Duration::from_secs(3600)),
    );

    // The first Step-and-RPC Register blocks; the second registers and reports cap 4.
    assert!(eventually(20 * SECOND, || proxy.blocked_registers() == 1));
    require_slot_state(
        &run,
        "a cap update on another stream",
        20 * SECOND,
        |state| state.total_capacity == 4 && state.idle >= 1,
    );
    let client = server.new_client(&run.registry);
    let flow_ids = start_flows(&client, &handlers, MODE_QUICK, 2);
    require_flows_completed(&client, &flow_ids);
    assert_eq!(1, proxy.blocked_registers());
    assert!(run.state().registering >= 1, "{:?}", run.state());

    // Stop finishes within its drain timeout although one Register never leaves by itself.
    let stop_started_at = Instant::now();
    run.stop(15 * SECOND)
        .expect("Start returns after Stop with a blocked Register");
    assert!(stop_started_at.elapsed() < 10 * SECOND);
    release_blocked();
}

/// Design Test 7: a cap above and below the floor, without orphaned handlers.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_cap_drop_without_orphans() {
    let runtime = test_runtime();

    // The new cap is above the floor.
    {
        let mut server = AdaptiveSlotTestServer::start(ServerLimits::cap(8));
        let proxy = PullSlotProxy::start(&runtime, &server.address);
        let handlers = AdaptiveSlotHandlers::new(false);
        let app_worker_id = app_worker_id("adaptive-drop-above");
        let mut run = AdaptiveSlotWorkerRun::start(
            &runtime,
            &proxy.address,
            &handlers,
            PullWorkerOptions::new()
                .max_concurrent_tasks(8)
                .min_open_slots(3)
                .app_worker_id(&app_worker_id)
                .shrink_window_for_test(SECOND),
        );
        run.wait_registered(Duration::from_secs(60));
        let client = server.new_client(&run.registry);
        let flow_ids = start_flows(&client, &handlers, MODE_BLOCK, 6);
        require_slot_state(&run, "six busy Steps", 30 * SECOND, |state| state.busy == 6);
        let mut sampler = SlotUsageSampler::start(&run.worker);
        server.kill();
        let restarted_at = Instant::now();
        server.restart(ServerLimits::cap(4));
        proxy.set_upstream(&server.address);
        // C = 4 and R = 1 leave E = 3; the retried Steps grow the Worker back to E.
        require_slot_state(&run, "growth up to the lower cap", 30 * SECOND, |state| {
            state.total_capacity == 4
                && state.effective_ceiling == 3
                && state.busy == 3
                && state.open == 3
                && state.rpc_only_open == 1
                && state.registering == 0
        });
        require_registered_streams(&proxy, &app_worker_id, 3, 1);
        assert!(
            proxy
                .rejections_since(&app_worker_id, restarted_at)
                .is_empty()
        );
        assert!(
            proxy
                .worker_resets_since(&app_worker_id, restarted_at)
                .is_empty(),
            "cap convergence reset a stream"
        );
        run.require_no_permanent_registration_error();
        handlers.release();
        let restarted_client = server.new_client(&run.registry);
        require_flows_completed(&restarted_client, &flow_ids);
        sampler.require_within_capacity();
        assert!(
            proxy
                .worker_resets_since(&app_worker_id, restarted_at)
                .is_empty(),
            "cap convergence reset a stream"
        );
    }

    // The new cap is below the floor.
    let mut server = AdaptiveSlotTestServer::start(ServerLimits::cap(8));
    let proxy = PullSlotProxy::start(&runtime, &server.address);
    let handlers = AdaptiveSlotHandlers::new(false);
    let app_worker_id = app_worker_id("adaptive-drop-below");
    let mut run = AdaptiveSlotWorkerRun::start(
        &runtime,
        &proxy.address,
        &handlers,
        PullWorkerOptions::new()
            .max_concurrent_tasks(8)
            .min_open_slots(5)
            .app_worker_id(&app_worker_id)
            .stop_on_permanent_stream_rejection(true)
            .shrink_window_for_test(SECOND),
    );
    run.wait_registered(Duration::from_secs(60));
    require_registered_streams(&proxy, &app_worker_id, 4, 1);
    server.kill();
    let restarted_at = Instant::now();
    server.restart(ServerLimits::cap(3));
    proxy.set_upstream(&server.address);
    // C = 3 and R = 1 leave E = 2, so the floor of 4 becomes L_eff = 2.
    require_slot_state(
        &run,
        "the floor limited by the lower cap",
        30 * SECOND,
        |state| {
            state.total_capacity == 3
                && state.floor == 2
                && state.open == 2
                && state.rpc_only_open == 1
                && state.registering == 0
        },
    );
    require_registered_streams(&proxy, &app_worker_id, 2, 1);
    assert!(
        proxy
            .worker_resets_since(&app_worker_id, restarted_at)
            .is_empty(),
        "cap convergence reset a stream"
    );
    run.require_no_permanent_registration_error();
    let restarted_client = server.new_client(&run.registry);
    let quick_ids = start_flows(&restarted_client, &handlers, MODE_QUICK, 3);
    require_flows_completed(&restarted_client, &quick_ids);
}

/// Design Test 12.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_graceful_stop_during_growth() {
    let runtime = test_runtime();
    let server = AdaptiveSlotTestServer::start(ServerLimits::default());
    let handlers = AdaptiveSlotHandlers::new(false);
    let mut run = AdaptiveSlotWorkerRun::start(
        &runtime,
        &server.address,
        &handlers,
        PullWorkerOptions::new()
            .max_concurrent_tasks(32)
            .min_open_slots(3)
            .app_worker_id(app_worker_id("adaptive-stop"))
            .shrink_window_for_test(SECOND),
    );
    run.wait_registered(Duration::from_secs(60));
    let client = Arc::new(server.new_client(&run.registry));
    let flow_ids = start_flows_concurrently(&client, &handlers, MODE_BLOCK, 20);
    handlers.await_entries(4);
    run.worker.stop();
    // Busy handlers finish during the drain; tasks that never started return without spending a retry.
    handlers.release();
    run.await_start_result(30 * SECOND)
        .expect("graceful stop during growth");
    let replacement = AdaptiveSlotWorkerRun::start(
        &runtime,
        &server.address,
        &handlers,
        PullWorkerOptions::new()
            .max_concurrent_tasks(8)
            .app_worker_id(app_worker_id("fixed-replacement")),
    );
    replacement.wait_registered(Duration::from_secs(60));
    require_flows_completed(&client, &flow_ids);
    require_single_attempts(&handlers);
}

/// Design Test 13 against one server.
#[test]
#[ignore = "requires DEX_SERVER_ZD_BINARY"]
fn adaptive_slot_streams_mixed_fleet() {
    let runtime = test_runtime();
    let server = AdaptiveSlotTestServer::start(ServerLimits::default());
    let handlers = AdaptiveSlotHandlers::new(false);
    let mut fixed = AdaptiveSlotWorkerRun::start(
        &runtime,
        &server.address,
        &handlers,
        PullWorkerOptions::new()
            .max_concurrent_tasks(4)
            .app_worker_id(app_worker_id("fixed-fleet")),
    );
    let adaptive = AdaptiveSlotWorkerRun::start(
        &runtime,
        &server.address,
        &handlers,
        PullWorkerOptions::new()
            .max_concurrent_tasks(16)
            .min_open_slots(2)
            .app_worker_id(app_worker_id("adaptive-fleet"))
            .shrink_window_for_test(SECOND),
    );
    fixed.wait_registered(Duration::from_secs(60));
    adaptive.wait_registered(Duration::from_secs(60));
    let client = server.new_client(&adaptive.registry);
    let quick_ids = start_flows(&client, &handlers, MODE_QUICK, 10);
    require_flows_completed(&client, &quick_ids);
    require_single_attempts(&handlers);
    // The fixed Worker leaves; its share moves to the adaptive Worker, which grows from its floor.
    fixed.stop(30 * SECOND).expect("stop the fixed Worker");
    let flow_ids = start_flows(&client, &handlers, MODE_BLOCK, 8);
    handlers.await_entries(8);
    require_slot_state(
        &adaptive,
        "the adaptive Worker grew to the load",
        30 * SECOND,
        |state| state.busy == 8 && (8..=15).contains(&state.open),
    );
    handlers.release();
    require_flows_completed(&client, &flow_ids);
    require_single_attempts(&handlers);
}
