// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

//! Worker runs, Flow helpers, and assertions shared by the adaptive slot stream tests.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex, mpsc};
use std::thread;
use std::time::{Duration, Instant};

use dex_sdk::{
    FlowStatus, PullSlotStreamState, PullWorkerOptions, Registry, SdkResult, Worker, WorkerOptions,
};
use tempfile::TempDir;
use tokio::runtime::Runtime;

use crate::adaptive_slot_harness::{
    ProxyEvent, PullSlotProxy, RPC_ONLY, STEP_AND_RPC, TestClient, eventually, test_cache,
};
use crate::adaptive_slot_workflow::{
    AdaptiveSlotFlow, AdaptiveSlotHandlers, AdaptiveSlotParkedFlow, MODE_STUBBORN,
};

pub(crate) fn test_runtime() -> Arc<Runtime> {
    Arc::new(
        tokio::runtime::Builder::new_multi_thread()
            .worker_threads(4)
            .enable_all()
            .build()
            .expect("build test runtime"),
    )
}

pub(crate) fn app_worker_id(prefix: &str) -> String {
    format!(
        "{prefix}-{}",
        &uuid::Uuid::new_v4().simple().to_string()[..8]
    )
}

/// Owns a started Worker and its Start result.
pub(crate) struct AdaptiveSlotWorkerRun {
    pub(crate) worker: Arc<Worker>,
    pub(crate) registry: Registry,
    handlers: Arc<AdaptiveSlotHandlers>,
    runtime: Arc<Runtime>,
    started: mpsc::Receiver<SdkResult<()>>,
    result: Option<SdkResult<()>>,
    thread: Option<thread::JoinHandle<()>>,
    _cache_directory: TempDir,
}

impl AdaptiveSlotWorkerRun {
    /// Starts a Worker with both test Flows.
    pub(crate) fn start(
        runtime: &Arc<Runtime>,
        address: &str,
        handlers: &Arc<AdaptiveSlotHandlers>,
        pull: PullWorkerOptions,
    ) -> Self {
        let registry = Registry::new()
            .register(AdaptiveSlotFlow::new(handlers))
            .and_then(|registry| registry.register(AdaptiveSlotParkedFlow::new(handlers)))
            .expect("register adaptive slot Flows");
        Self::start_with_registry(runtime, address, handlers, registry, pull)
    }

    /// Starts a Worker that serves only the parked Flow, so it never takes the other Flow's Steps.
    pub(crate) fn start_parked_only(
        runtime: &Arc<Runtime>,
        address: &str,
        handlers: &Arc<AdaptiveSlotHandlers>,
        pull: PullWorkerOptions,
    ) -> Self {
        let registry = Registry::new()
            .register(AdaptiveSlotParkedFlow::new(handlers))
            .expect("register parked Flow");
        Self::start_with_registry(runtime, address, handlers, registry, pull)
    }

    fn start_with_registry(
        runtime: &Arc<Runtime>,
        address: &str,
        handlers: &Arc<AdaptiveSlotHandlers>,
        registry: Registry,
        pull: PullWorkerOptions,
    ) -> Self {
        let (cache, cache_directory) = test_cache();
        let worker = Arc::new(Worker::new(
            registry.clone(),
            cache,
            WorkerOptions::new().server_address(address).pull(pull),
        ));
        let (result_sender, started) = mpsc::channel();
        let running = Arc::clone(&worker);
        let thread = thread::Builder::new()
            .name("adaptive-slot-worker".to_string())
            .spawn(move || {
                let result = running.start();
                result_sender
                    .send(result)
                    .expect("the run keeps the receiver until it joins this thread");
            })
            .expect("start Worker thread");
        Self {
            worker,
            registry,
            handlers: Arc::clone(handlers),
            runtime: Arc::clone(runtime),
            started,
            result: None,
            thread: Some(thread),
            _cache_directory: cache_directory,
        }
    }

    pub(crate) fn state(&self) -> PullSlotStreamState {
        self.worker.pull_slot_stream_state()
    }

    pub(crate) fn wait_registered(&self, timeout: Duration) {
        let result = self.runtime.block_on(async {
            tokio::time::timeout(timeout, self.worker.wait_pull_registered()).await
        });
        match result {
            Ok(Ok(())) => {}
            Ok(Err(error)) => panic!("Worker readiness failed: {error}"),
            Err(_) => panic!("Worker did not become ready: {:?}", self.state()),
        }
    }

    /// Waits for Start to return and returns its result.
    pub(crate) fn await_start_result(&mut self, timeout: Duration) -> SdkResult<()> {
        if let Some(result) = &self.result {
            return result.clone();
        }
        let result = self
            .started
            .recv_timeout(timeout)
            .expect("Start did not return");
        self.result = Some(result.clone());
        result
    }

    /// Requests Stop and waits for Start to return.
    pub(crate) fn stop(&mut self, timeout: Duration) -> SdkResult<()> {
        self.worker.stop();
        self.await_start_result(timeout)
    }

    /// Panics when the Worker lost readiness, recorded a permanent error, or Start returned.
    pub(crate) fn require_no_permanent_registration_error(&mut self) {
        let registration = self.worker.pull_registration_state();
        assert!(registration.is_ready, "readiness dropped: {registration:?}");
        for queue in &registration.queues {
            assert!(
                queue.last_permanent_error.is_none(),
                "slot kind {} recorded a permanent error: {:?}",
                queue.queue,
                queue.last_permanent_error
            );
        }
        if self.result.is_none()
            && let Ok(result) = self.started.try_recv()
        {
            self.result = Some(result.clone());
            panic!("Start returned during a soft rejection: {result:?}");
        }
    }
}

impl Drop for AdaptiveSlotWorkerRun {
    fn drop(&mut self) {
        // A failed test may leave handlers that ignore cancellation; Stop waits for every handler.
        self.handlers.release_all_stubborn();
        self.handlers.release();
        self.worker.stop();
        if let Some(thread) = self.thread.take()
            && thread.join().is_err()
            && !thread::panicking()
        {
            panic!("the Worker thread panicked");
        }
    }
}

/// Polls the snapshot until `condition` holds; panics with the last snapshot after `timeout`.
pub(crate) fn require_slot_state(
    run: &AdaptiveSlotWorkerRun,
    description: &str,
    timeout: Duration,
    condition: impl Fn(&PullSlotStreamState) -> bool,
) {
    let mut last = run.state();
    let is_reached = eventually(timeout, || {
        last = run.state();
        condition(&last)
    });
    assert!(
        is_reached,
        "slot streams never reached {description}: {last:?}"
    );
}

/// Polls the proxy until the server holds exactly these registered streams of the Worker.
pub(crate) fn require_registered_streams(
    proxy: &PullSlotProxy,
    app_worker_id: &str,
    step_streams: usize,
    rpc_only_streams: usize,
) {
    let is_reached = eventually(Duration::from_secs(20), || {
        proxy.registered_streams(app_worker_id, STEP_AND_RPC) == step_streams
            && proxy.registered_streams(app_worker_id, RPC_ONLY) == rpc_only_streams
    });
    assert!(
        is_reached,
        "server streams of {app_worker_id}: want {step_streams} Step-and-RPC and {rpc_only_streams} RPC-only, have {} and {}\n{}",
        proxy.registered_streams(app_worker_id, STEP_AND_RPC),
        proxy.registered_streams(app_worker_id, RPC_ONLY),
        proxy.describe(app_worker_id)
    );
}

pub(crate) fn start_flows(
    client: &TestClient,
    handlers: &Arc<AdaptiveSlotHandlers>,
    mode: &str,
    count: usize,
) -> Vec<String> {
    let flow = AdaptiveSlotFlow::new(handlers);
    (0..count)
        .map(|_| {
            let flow_id = format!("adaptive-slot-{}", uuid::Uuid::new_v4());
            client
                .client
                .start_flow(&flow, &flow_id, mode.to_string())
                .expect("start adaptive slot Flow");
            flow_id
        })
        .collect()
}

/// Starts `count` Flows from eight threads at once.
pub(crate) fn start_flows_concurrently(
    client: &Arc<TestClient>,
    handlers: &Arc<AdaptiveSlotHandlers>,
    mode: &str,
    count: usize,
) -> Vec<String> {
    let flow_ids: Vec<String> = (0..count)
        .map(|_| format!("adaptive-slot-{}", uuid::Uuid::new_v4()))
        .collect();
    let chunk_size = count.div_ceil(8).max(1);
    let starters: Vec<_> = flow_ids
        .chunks(chunk_size)
        .map(|chunk| {
            let chunk = chunk.to_vec();
            let client = Arc::clone(client);
            let flow = AdaptiveSlotFlow::new(handlers);
            let mode = mode.to_string();
            thread::spawn(move || {
                for flow_id in chunk {
                    client
                        .client
                        .start_flow(&flow, &flow_id, mode.clone())
                        .expect("start adaptive slot Flow");
                }
            })
        })
        .collect();
    for starter in starters {
        starter.join().expect("join Flow starter");
    }
    flow_ids
}

pub(crate) fn require_flows_completed(client: &TestClient, flow_ids: &[String]) {
    for flow_id in flow_ids {
        let result = client
            .client
            .wait_for_flow_with_timeout(flow_id, Duration::from_secs(120))
            .unwrap_or_else(|error| panic!("wait for Flow {flow_id}: {error}"));
        assert_eq!(FlowStatus::Completed, result.status(), "Flow {flow_id}");
    }
}

pub(crate) fn start_parked_flow(
    client: &TestClient,
    handlers: &Arc<AdaptiveSlotHandlers>,
) -> String {
    let flow_id = format!("adaptive-slot-parked-{}", uuid::Uuid::new_v4());
    client
        .client
        .start_flow(&AdaptiveSlotParkedFlow::new(handlers), &flow_id, ())
        .expect("start parked Flow");
    flow_id
}

/// Invokes the blocking RPC on a thread; the receiver gets its result.
pub(crate) fn invoke_blocking_rpc(
    client: &Arc<TestClient>,
    parked_id: &str,
) -> mpsc::Receiver<SdkResult<String>> {
    let (sender, receiver) = mpsc::channel();
    let client = Arc::clone(client);
    let parked_id = parked_id.to_string();
    thread::spawn(move || {
        let result = client
            .client
            .invoke_rpc_without_input(&parked_id, AdaptiveSlotParkedFlow::BLOCK_UNTIL_RELEASED);
        match sender.send(result) {
            Ok(()) => {}
            // The test stopped waiting for this RPC, which a restart may have interrupted.
            Err(_unsent) => {}
        }
    });
    receiver
}

/// Waits for the RPC that a restart interrupted; its result depends on client retries.
pub(crate) fn await_rpc_after_restart(rpc_result: &mpsc::Receiver<SdkResult<String>>) {
    let result = rpc_result.recv_timeout(Duration::from_secs(60));
    assert!(
        result.is_ok(),
        "the RPC interrupted by the restart did not return"
    );
}

/// Starts E stubborn Steps, then one stubborn RPC that can only land on the RPC-only stream.
pub(crate) fn occupy_every_slot(
    run: &AdaptiveSlotWorkerRun,
    client: &Arc<TestClient>,
    handlers: &Arc<AdaptiveSlotHandlers>,
    parked_id: &str,
) -> (Vec<String>, mpsc::Receiver<SdkResult<String>>) {
    let ceiling = run.state().effective_ceiling;
    let flow_ids = start_flows(client, handlers, MODE_STUBBORN, ceiling);
    handlers.await_entries(ceiling);
    require_slot_state(
        run,
        "every Step-and-RPC slot busy",
        Duration::from_secs(30),
        |state| state.busy == ceiling && state.open == ceiling,
    );
    let rpc_result = invoke_blocking_rpc(client, parked_id);
    let entry = handlers.await_entries(1).remove(0);
    assert_eq!("rpc", entry.kind);
    (flow_ids, rpc_result)
}

/// Records every sample where S + H + Sr + Hr exceeds C.
pub(crate) struct SlotUsageSampler {
    is_stopped: Arc<AtomicBool>,
    violations: Arc<Mutex<Vec<PullSlotStreamState>>>,
    thread: Option<thread::JoinHandle<()>>,
}

impl SlotUsageSampler {
    pub(crate) fn start(worker: &Arc<Worker>) -> Self {
        let is_stopped = Arc::new(AtomicBool::new(false));
        let violations = Arc::new(Mutex::new(Vec::new()));
        let thread = {
            let worker = Arc::clone(worker);
            let is_stopped = Arc::clone(&is_stopped);
            let violations = Arc::clone(&violations);
            thread::spawn(move || {
                while !is_stopped.load(Ordering::Acquire) {
                    thread::sleep(Duration::from_millis(5));
                    let state = worker.pull_slot_stream_state();
                    let usage = state.open
                        + state.orphaned_handlers
                        + state.rpc_only_open
                        + state.orphaned_rpc_only_handlers;
                    let mut violations = violations.lock().expect("sampler lock");
                    if usage > state.total_capacity && violations.len() < 8 {
                        violations.push(state);
                    }
                }
            })
        };
        Self {
            is_stopped,
            violations,
            thread: Some(thread),
        }
    }

    /// Stops sampling and returns the samples above the capacity.
    pub(crate) fn stop_sampling(&mut self) -> Vec<PullSlotStreamState> {
        self.is_stopped.store(true, Ordering::Release);
        if let Some(thread) = self.thread.take() {
            thread.join().expect("join sampler");
        }
        self.violations.lock().expect("sampler lock").clone()
    }

    pub(crate) fn require_within_capacity(&mut self) {
        let violations = self.stop_sampling();
        assert!(
            violations.is_empty(),
            "slot usage exceeded the capacity: {violations:?}"
        );
    }
}

impl Drop for SlotUsageSampler {
    fn drop(&mut self) {
        self.is_stopped.store(true, Ordering::Release);
        if let Some(thread) = self.thread.take()
            && thread.join().is_err()
            && !thread::panicking()
        {
            panic!("the sampler thread panicked");
        }
    }
}

/// Invariant 5: ceil(log2(E - L + 1)) + 1 windows.
pub(crate) fn windows_to_floor(effective_ceiling: usize, floor: usize) -> u64 {
    let mut windows = 1;
    let mut span = 1;
    while span < effective_ceiling - floor + 1 {
        span *= 2;
        windows += 1;
    }
    windows
}

/// Groups Registers that arrive within half a window of a batch's first one.
pub(crate) fn count_register_batches(registers: &[ProxyEvent], window: Duration) -> usize {
    let mut batches = 0;
    let mut batch_start: Option<Instant> = None;
    for register in registers {
        if batch_start.is_none_or(|start| register.at.duration_since(start) > window / 2) {
            batches += 1;
            batch_start = Some(register.at);
        }
    }
    batches
}

/// Checks that Registers come in at most one batch of `batch_size` per window.
pub(crate) fn require_register_batches_per_window(
    registers: &[ProxyEvent],
    window: Duration,
    batch_size: usize,
) {
    let Some(first) = registers.first() else {
        return;
    };
    let mut batch_start = first.at;
    let mut batch_count = 0;
    for register in registers {
        let since_batch = register.at.duration_since(batch_start);
        if since_batch > window / 2 {
            assert!(
                since_batch >= window * 8 / 10,
                "two growth batches in one window"
            );
            batch_start = register.at;
            batch_count = 0;
        }
        batch_count += 1;
        assert!(batch_count <= batch_size, "a growth batch was too large");
    }
}
