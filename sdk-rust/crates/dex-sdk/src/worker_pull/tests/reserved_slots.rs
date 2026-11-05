// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;
use dex_protocol::dex::InvokeExecuteMethodRequest;
use std::sync::atomic::AtomicUsize;

type RequestEvent = Result<Option<PollWorkerTaskRequest>, Status>;

// Exceeds tonic's default 4 MiB decode limit, so the Worker fails to decode the task frame.
const OVERSIZED_TASK_ID_BYTES: usize = 5 * 1024 * 1024;

struct SlotConnection {
    register: PollWorkerTaskRegister,
    responses: mpsc::Sender<Result<PollWorkerTaskResponse, Status>>,
    requests: mpsc::Receiver<RequestEvent>,
}

impl SlotConnection {
    async fn registered(&self, limit: u32) {
        self.responses
            .send(Ok(PollWorkerTaskResponse {
                frame: Some(ResponseFrame::Registered(PollWorkerTaskRegistered {
                    supports_unknown_name_rejection: true,
                    max_concurrent_tasks_per_app_worker: limit,
                })),
            }))
            .await
            .expect("send registration");
    }

    async fn assign_step(&self, task_id: &str) {
        self.responses
            .send(Ok(PollWorkerTaskResponse {
                frame: Some(ResponseFrame::Task(PollWorkerTask {
                    task_id: task_id.to_string(),
                    request: Some(TaskRequest::Execute(InvokeExecuteMethodRequest {
                        flow_type: "SlotFlow".to_string(),
                        step_type: "SlotStep".to_string(),
                        context: Some(ProtoContext {
                            flow_id: uuid::Uuid::new_v4().to_string(),
                            ..Default::default()
                        }),
                        step_input: Some(crate::value_mapper::encode(&()).expect("Step input")),
                        ..Default::default()
                    })),
                    ..Default::default()
                })),
            }))
            .await
            .expect("assign Step");
    }

    async fn assign_large_rpc(&self) {
        self.responses
            .send(Ok(PollWorkerTaskResponse {
                frame: Some(ResponseFrame::Task(PollWorkerTask {
                    task_id: "large-rpc".to_string(),
                    request: Some(TaskRequest::Rpc(InvokeWorkerRpcRequest {
                        flow_type: "SlotFlow".to_string(),
                        rpc_name: "get_large_slot_value".to_string(),
                        context: Some(ProtoContext {
                            flow_id: uuid::Uuid::new_v4().to_string(),
                            ..Default::default()
                        }),
                        input: Some(crate::value_mapper::encode(&()).expect("RPC input")),
                        ..Default::default()
                    })),
                    ..Default::default()
                })),
            }))
            .await
            .expect("assign large RPC");
    }

    async fn assign_rpc(&self) {
        self.responses
            .send(Ok(PollWorkerTaskResponse {
                frame: Some(ResponseFrame::Task(PollWorkerTask {
                    task_id: "rpc".to_string(),
                    request: Some(TaskRequest::Rpc(InvokeWorkerRpcRequest {
                        flow_type: "SlotFlow".to_string(),
                        rpc_name: "get_slot_value".to_string(),
                        context: Some(ProtoContext {
                            flow_id: uuid::Uuid::new_v4().to_string(),
                            ..Default::default()
                        }),
                        input: Some(crate::value_mapper::encode(&()).expect("RPC input")),
                        ..Default::default()
                    })),
                    ..Default::default()
                })),
            }))
            .await
            .expect("assign RPC");
    }

    async fn assign_oversized_task(&self) {
        self.responses
            .send(Ok(PollWorkerTaskResponse {
                frame: Some(ResponseFrame::Task(PollWorkerTask {
                    task_id: "x".repeat(OVERSIZED_TASK_ID_BYTES),
                    ..Default::default()
                })),
            }))
            .await
            .expect("assign oversized task");
    }

    async fn assign_unknown_step(&self, task_id: String) -> Result<(), Status> {
        self.responses
            .send(Ok(PollWorkerTaskResponse {
                frame: Some(ResponseFrame::Task(PollWorkerTask {
                    task_id,
                    request: Some(TaskRequest::Execute(InvokeExecuteMethodRequest {
                        flow_type: "SlotFlow".to_string(),
                        step_type: "MissingStep".to_string(),
                        ..Default::default()
                    })),
                    ..Default::default()
                })),
            }))
            .await
            .map_err(|_| Status::unavailable("slot connection closed"))
    }

    async fn reset(&mut self) -> Status {
        self.next_request()
            .await
            .expect_err("stream reset, never a clean half-close")
    }

    async fn next_request(&mut self) -> RequestEvent {
        timeout(Duration::from_secs(5), self.requests.recv())
            .await
            .expect("request event deadline")
            .expect("request event")
    }

    async fn final_frame(&mut self, task_id: &str) {
        let request = self
            .next_request()
            .await
            .expect("stream healthy")
            .expect("final output before EOF");
        assert!(
            matches!(request.frame, Some(RequestFrame::TaskFrame(frame)) if frame.task_id == task_id)
        );
    }

    async fn half_closed(&mut self) {
        assert!(
            self.next_request()
                .await
                .expect("clean half-close")
                .is_none()
        );
    }
}

#[derive(Clone)]
struct SlotService {
    connections: mpsc::Sender<SlotConnection>,
    is_reading_allowed: watch::Receiver<bool>,
}

#[tonic::async_trait]
impl WorkerTaskService for SlotService {
    type PollWorkerTaskStream = ReceiverStream<Result<PollWorkerTaskResponse, Status>>;

    async fn poll_worker_task(
        &self,
        request: Request<tonic::Streaming<PollWorkerTaskRequest>>,
    ) -> Result<Response<Self::PollWorkerTaskStream>, Status> {
        let mut incoming = request.into_inner();
        let Some(PollWorkerTaskRequest {
            frame: Some(RequestFrame::Register(register)),
        }) = incoming.message().await?
        else {
            return Err(Status::invalid_argument("missing registration"));
        };
        let (responses, receiver) = mpsc::channel(16);
        let (requests, request_events) = mpsc::channel(16);
        self.connections
            .send(SlotConnection {
                register,
                responses,
                requests: request_events,
            })
            .await
            .map_err(|error| Status::internal(error.to_string()))?;
        let mut is_reading_allowed = self.is_reading_allowed.clone();
        // The mock server runtime owns this reader and cancels it when the fixture is dropped.
        let _stream_reader = tokio::spawn(async move {
            loop {
                if is_reading_allowed
                    .wait_for(|is_allowed| *is_allowed)
                    .await
                    .is_err()
                {
                    break;
                }
                // Pausing drops the pending read, so the Server stops consuming request data.
                let event = tokio::select! {
                    event = incoming.message() => event,
                    paused = is_reading_allowed.wait_for(|is_allowed| !*is_allowed) => {
                        if paused.is_err() {
                            break;
                        }
                        continue;
                    }
                };
                let has_ended = !matches!(event, Ok(Some(_)));
                if requests.send(event).await.is_err() || has_ended {
                    break;
                }
            }
        });
        Ok(Response::new(ReceiverStream::new(receiver)))
    }
}

struct SlotHarness {
    connections: mpsc::Receiver<SlotConnection>,
    worker: Arc<PullWorker>,
    running: JoinHandle<SdkResult<()>>,
    shutdown: watch::Sender<bool>,
    server: JoinHandle<Result<(), tonic::transport::Error>>,
    server_stop: watch::Sender<bool>,
    _server_reads: watch::Sender<bool>,
    _cache_directory: tempfile::TempDir,
}

impl SlotHarness {
    async fn new(options: PullWorkerOptions, registry: Registry) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind slot server");
        let address = listener.local_addr().expect("slot server address");
        let (connections, received) = mpsc::channel(16);
        let (server_stop, mut server_shutdown) = watch::channel(false);
        let (server_reads, is_reading_allowed) = watch::channel(true);
        let server = tokio::spawn(
            Server::builder()
                .add_service(WorkerTaskServiceServer::new(SlotService {
                    connections,
                    is_reading_allowed,
                }))
                .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async move {
                    while !*server_shutdown.borrow() && server_shutdown.changed().await.is_ok() {}
                }),
        );
        let channel = outbound_task_channel(
            Endpoint::from_shared(format!("http://{address}")).expect("endpoint"),
            None,
        );
        let (cache, cache_directory) = test_cache();
        let dispatcher = WorkerDispatcher::new(
            registry,
            ValueHydrator::new(FlowServiceClient::new(channel.clone()), cache),
            crate::server_protocol::NegotiatedServerProtocolVersion::default(),
        );
        let worker =
            Arc::new(PullWorker::new(&options, &dispatcher, channel).expect("slot Worker"));
        let (shutdown, stopped) = watch::channel(false);
        let running_worker = Arc::clone(&worker);
        let running = tokio::spawn(async move { running_worker.run(stopped).await });
        Self {
            connections: received,
            worker,
            running,
            shutdown,
            server,
            server_stop,
            _server_reads: server_reads,
            _cache_directory: cache_directory,
        }
    }

    async fn connection(&mut self) -> SlotConnection {
        timeout(Duration::from_secs(5), self.connections.recv())
            .await
            .expect("slot opened")
            .expect("slot connection")
    }

    fn stop(&self) {
        self.worker.stop_accepting_tasks();
        self.shutdown.send_replace(true);
    }

    async fn finish(self) -> SdkResult<()> {
        let result = timeout(Duration::from_secs(5), self.running)
            .await
            .expect("Worker finished")
            .expect("join Worker");
        self.server_stop.send_replace(true);
        timeout(Duration::from_secs(5), self.server)
            .await
            .expect("server stopped")
            .expect("join server")
            .expect("server result");
        result
    }
}

struct SlotFlow {
    step: SlotStep,
    large_result_returned: Arc<Notify>,
}

struct SlotStep {
    calls: Arc<AtomicUsize>,
    started: Arc<Notify>,
    release: Arc<(Mutex<bool>, Condvar)>,
    should_wait_for_cancellation: bool,
}

impl crate::Step for SlotStep {
    type Input = ();
    fn execute(&self, context: &mut Context, _: ()) -> HandlerResult<crate::StepDecision> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        self.started.notify_one();
        if self.should_wait_for_cancellation {
            context.wait_for_cancellation();
        } else {
            let (lock, changed) = &*self.release;
            let mut is_released = lock.lock().expect("slot release lock");
            while !*is_released {
                is_released = changed.wait(is_released).expect("slot release lock");
            }
        }
        Ok(crate::StepDecision::graceful_complete(()))
    }
}

impl Flow for SlotFlow {
    type StartInput = ();
    fn steps(&self) -> crate::StepList<'_, ()> {
        crate::StepList::start(&self.step)
    }
    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(Rpc::new("get_slot_value"), Self::get_slot_value)
            .function_without_input(Rpc::new("get_large_slot_value"), Self::get_large_slot_value)
    }
}

impl SlotFlow {
    fn get_slot_value(&self, _: &mut Context) -> HandlerResult<RpcResult<String>> {
        Ok(RpcResult::new("value".to_string()))
    }

    fn get_large_slot_value(&self, _: &mut Context) -> HandlerResult<RpcResult<String>> {
        self.large_result_returned.notify_one();
        // Exceeds the HTTP/2 flow-control windows, so a paused Server holds part of the result.
        Ok(RpcResult::new("x".repeat(3_000_000)))
    }
}

fn slot_flow(is_released: bool, should_wait_for_cancellation: bool) -> SlotFlow {
    SlotFlow {
        step: SlotStep {
            calls: Arc::new(AtomicUsize::new(0)),
            started: Arc::new(Notify::new()),
            release: Arc::new((Mutex::new(is_released), Condvar::new())),
            should_wait_for_cancellation,
        },
        large_result_returned: Arc::new(Notify::new()),
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn registers_exactly_one_stream_per_slot_and_required_kinds() {
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new()
            .max_concurrent_tasks(4)
            .reserved_rpc_tasks(2)
            .app_worker_id("slot-worker"),
        Registry::new().register(slot_flow(true, false)).unwrap(),
    )
    .await;
    let mut connections = Vec::new();
    for _ in 0..4 {
        connections.push(harness.connection().await);
    }
    assert_eq!(connections.iter().filter(|connection| connection.register.slot_kind == PollWorkerSlotKind::RpcOnly as i32).count(), 2);
    for connection in &connections {
        assert!(connection.register.supports_reserved_slot_dispatch);
        assert!(connection.register.queue.is_empty());
        assert_eq!(connection.register.app_worker_id, "slot-worker");
        assert_eq!(connection.register.flow_types, ["SlotFlow"]);
    }
    let general = connections
        .iter()
        .find(|connection| connection.register.slot_kind == PollWorkerSlotKind::StepAndRpc as i32)
        .unwrap();
    general.registered(4).await;
    tokio::task::yield_now().await;
    assert!(!harness.worker.registration.snapshot().is_ready);
    for connection in connections
        .iter()
        .filter(|connection| connection.register.slot_kind == PollWorkerSlotKind::RpcOnly as i32)
    {
        connection.registered(4).await;
    }
    timeout(Duration::from_secs(5), harness.worker.registration.wait())
        .await
        .unwrap()
        .unwrap();
    assert!(harness.connections.try_recv().is_err());
    harness.stop();
    for connection in &mut connections {
        connection.half_closed().await;
    }
    drop(connections);
    harness.finish().await.unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn executes_every_assignment_without_capacity_rejections() {
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new().max_concurrent_tasks(2),
        Registry::new().register(slot_flow(true, false)).unwrap(),
    )
    .await;
    let first = harness.connection().await;
    let second = harness.connection().await;
    let (mut general, mut rpc_only) =
        if first.register.slot_kind == PollWorkerSlotKind::RpcOnly as i32 {
            (second, first)
        } else {
            (first, second)
        };
    general.registered(0).await;
    rpc_only.registered(0).await;
    general.assign_step("step").await;
    rpc_only.assign_rpc().await;
    general.final_frame("step").await;
    rpc_only.final_frame("rpc").await;
    // Even a misrouted Step must run: slot eligibility is the Server's responsibility after hand-out.
    rpc_only.assign_step("misrouted-step").await;
    rpc_only.final_frame("misrouted-step").await;
    harness.stop();
    general.half_closed().await;
    rpc_only.half_closed().await;
    drop((general, rpc_only));
    harness.finish().await.unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn registration_limit_fails_start_without_background_retry() {
    let flow = slot_flow(true, false);
    let calls = Arc::clone(&flow.step.calls);
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new().max_concurrent_tasks(3),
        Registry::new().register(flow).unwrap(),
    )
    .await;
    let mut connections = Vec::new();
    for _ in 0..3 {
        connections.push(harness.connection().await);
    }
    connections[0].registered(2).await;
    // A task sent right after Registered must come back without consuming an attempt.
    connections[0].assign_step("after-limit").await;
    let failure = timeout(Duration::from_secs(5), harness.worker.registration.wait())
        .await
        .unwrap()
        .unwrap_err();
    assert!(failure.to_string().contains("max_concurrent_tasks 3"));
    assert!(
        failure
            .to_string()
            .contains("matching.maxConcurrentTasksCap 2")
    );
    let SdkError::Service { service } = &failure else {
        panic!("expected a Service error, got {failure:?}");
    };
    assert_eq!(service.code(), Code::FailedPrecondition);
    for connection in &mut connections {
        connection.half_closed().await;
    }
    assert!(!harness.worker.registration.snapshot().is_ready);
    assert_eq!(calls.load(Ordering::SeqCst), 0);
    drop(connections);
    let failure = harness.finish().await.unwrap_err();
    assert!(
        failure
            .to_string()
            .contains("matching.maxConcurrentTasksCap 2")
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn lower_limit_after_reconnect_keeps_the_worker_serving() {
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new()
            .max_concurrent_tasks(2)
            .reserved_rpc_tasks(0),
        Registry::new().register(slot_flow(true, false)).unwrap(),
    )
    .await;
    let first = harness.connection().await;
    let mut second = harness.connection().await;
    first.registered(2).await;
    second.registered(2).await;
    timeout(Duration::from_secs(5), harness.worker.registration.wait())
        .await
        .unwrap()
        .unwrap();
    first
        .responses
        .send(Err(Status::unavailable("connection broke")))
        .await
        .unwrap();
    let mut replacement = harness.connection().await;
    replacement.registered(1).await;
    replacement.assign_step("after-lower-limit").await;
    replacement.final_frame("after-lower-limit").await;
    assert!(harness.worker.registration.snapshot().is_ready);
    harness.stop();
    replacement.half_closed().await;
    second.half_closed().await;
    drop((first, second, replacement));
    harness.finish().await.unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn stop_finishes_busy_output_half_closes_idle_and_drops_late_tasks() {
    let flow = slot_flow(false, false);
    let calls = Arc::clone(&flow.step.calls);
    let started = Arc::clone(&flow.step.started);
    let release = Arc::clone(&flow.step.release);
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new()
            .max_concurrent_tasks(2)
            .reserved_rpc_tasks(0),
        Registry::new().register(flow).unwrap(),
    )
    .await;
    let mut busy = harness.connection().await;
    let mut idle = harness.connection().await;
    busy.registered(0).await;
    idle.registered(0).await;
    busy.assign_step("running").await;
    timeout(Duration::from_secs(5), started.notified())
        .await
        .unwrap();
    harness.stop();
    busy.assign_step("late-before-half-close").await;
    idle.half_closed().await;
    idle.assign_step("late-after-half-close").await;
    assert!(busy.requests.try_recv().is_err());
    *release.0.lock().unwrap() = true;
    release.1.notify_all();
    busy.final_frame("running").await;
    busy.half_closed().await;
    assert_eq!(calls.load(Ordering::SeqCst), 1);
    drop((busy, idle));
    harness.finish().await.unwrap();
    assert_eq!(calls.load(Ordering::SeqCst), 1);
}

#[tokio::test(flavor = "multi_thread")]
async fn stop_timeout_resets_busy_stream_instead_of_half_closing() {
    let flow = slot_flow(false, false);
    let started = Arc::clone(&flow.step.started);
    let release = Arc::clone(&flow.step.release);
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new()
            .max_concurrent_tasks(1)
            .stop_drain_timeout(Duration::from_millis(50)),
        Registry::new().register(flow).unwrap(),
    )
    .await;
    let mut busy = harness.connection().await;
    busy.registered(0).await;
    busy.assign_step("slow").await;
    timeout(Duration::from_secs(5), started.notified())
        .await
        .unwrap();
    harness.stop();
    let failure = busy
        .next_request()
        .await
        .expect_err("busy stream reset, never clean EOF");
    assert_ne!(failure.code(), Code::Ok);
    assert!(!harness.running.is_finished());
    *release.0.lock().unwrap() = true;
    release.1.notify_all();
    drop(busy);
    harness.finish().await.unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn broken_stream_cancels_handler_and_never_moves_its_output_to_replacement() {
    let flow = slot_flow(false, true);
    let started = Arc::clone(&flow.step.started);
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new().max_concurrent_tasks(1),
        Registry::new().register(flow).unwrap(),
    )
    .await;
    let previous = harness.connection().await;
    previous.registered(0).await;
    previous.assign_step("broken-connection-task").await;
    timeout(Duration::from_secs(5), started.notified())
        .await
        .unwrap();
    previous
        .responses
        .send(Err(Status::unavailable("connection broke")))
        .await
        .unwrap();
    let mut replacement = harness.connection().await;
    replacement.registered(0).await;
    replacement.assign_rpc().await;
    replacement.final_frame("rpc").await;
    harness.stop();
    replacement.half_closed().await;
    drop((previous, replacement));
    harness.finish().await.unwrap();
}

#[test]
#[expect(deprecated, reason = "verify retained public compatibility APIs")]
fn deprecated_idle_setting_accepts_zero_and_decline_counter_stays_zero() {
    let (cache, _directory) = test_cache();
    let worker = Worker::try_new(
        Registry::new(),
        cache,
        WorkerOptions::new().pull(
            PullWorkerOptions::new()
                .max_concurrent_tasks(2)
                .idle_streams_per_queue(0),
        ),
    )
    .unwrap();
    assert_eq!(worker.pull_reserved_rpc_declines(), 0);
    worker.stop();
    assert_eq!(worker.pull_reserved_rpc_declines(), 0);
}

#[tokio::test(flavor = "multi_thread")]
async fn undecodable_task_frame_on_busy_slot_resets_the_stream() {
    let flow = slot_flow(false, true);
    let calls = Arc::clone(&flow.step.calls);
    let started = Arc::clone(&flow.step.started);
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new().max_concurrent_tasks(1),
        Registry::new().register(flow).unwrap(),
    )
    .await;
    let mut busy = harness.connection().await;
    busy.registered(0).await;
    busy.assign_step("running").await;
    timeout(Duration::from_secs(5), started.notified())
        .await
        .unwrap();
    busy.assign_oversized_task().await;
    busy.reset().await;
    let mut replacement = harness.connection().await;
    replacement.registered(0).await;
    harness.stop();
    // The cancelled handler's output never reaches the replacement connection.
    replacement.half_closed().await;
    assert_eq!(calls.load(Ordering::SeqCst), 1);
    drop((busy, replacement));
    harness.finish().await.unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn undecodable_task_frame_on_idle_slot_resets_the_stream() {
    let flow = slot_flow(true, false);
    let calls = Arc::clone(&flow.step.calls);
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new().max_concurrent_tasks(1),
        Registry::new().register(flow).unwrap(),
    )
    .await;
    let mut idle = harness.connection().await;
    idle.registered(0).await;
    idle.assign_oversized_task().await;
    idle.reset().await;
    let mut replacement = harness.connection().await;
    replacement.registered(0).await;
    replacement.assign_step("after-reset").await;
    replacement.final_frame("after-reset").await;
    harness.stop();
    replacement.half_closed().await;
    assert_eq!(calls.load(Ordering::SeqCst), 1);
    drop((idle, replacement));
    harness.finish().await.unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn next_task_right_after_final_output_runs_unless_stop_wins() {
    let flow = slot_flow(true, false);
    let calls = Arc::clone(&flow.step.calls);
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new().max_concurrent_tasks(1),
        Registry::new().register(flow).unwrap(),
    )
    .await;
    let mut slot = harness.connection().await;
    slot.registered(0).await;
    // Each assignment follows the previous final output immediately, before local cleanup ends.
    for index in 0..20 {
        let task_id = format!("reused-{index}");
        slot.assign_step(&task_id).await;
        slot.final_frame(&task_id).await;
    }
    assert_eq!(calls.load(Ordering::SeqCst), 20);
    tokio::join!(async { harness.stop() }, slot.assign_step("racing-stop"));
    match slot.next_request().await.expect("no reset") {
        None => assert_eq!(calls.load(Ordering::SeqCst), 20),
        Some(request) => {
            assert!(
                matches!(request.frame, Some(RequestFrame::TaskFrame(frame)) if frame.task_id == "racing-stop")
            );
            slot.half_closed().await;
            assert_eq!(calls.load(Ordering::SeqCst), 21);
        }
    }
    drop(slot);
    harness.finish().await.unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn next_task_waits_for_held_cleanup_of_the_previous_task() {
    let flow = slot_flow(true, false);
    let calls = Arc::clone(&flow.step.calls);
    let started = Arc::clone(&flow.step.started);
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new().max_concurrent_tasks(1),
        Registry::new().register(flow).unwrap(),
    )
    .await;
    let mut slot = harness.connection().await;
    slot.registered(0).await;
    let cleanup = crate::worker_pull::task_cleanup_barrier::hold("held-cleanup");
    slot.assign_step("held-cleanup").await;
    slot.final_frame("held-cleanup").await;
    timeout(Duration::from_secs(5), started.notified())
        .await
        .unwrap();
    // Dex reuses the slot while the previous task's local cleanup is still held open.
    slot.assign_step("after-held-cleanup").await;
    assert!(
        timeout(Duration::from_millis(200), started.notified())
            .await
            .is_err(),
        "the next task waits for the previous task's cleanup"
    );
    cleanup.notify_one();
    slot.final_frame("after-held-cleanup").await;
    assert_eq!(calls.load(Ordering::SeqCst), 2);
    harness.stop();
    slot.half_closed().await;
    drop(slot);
    harness.finish().await.unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn stop_during_held_cleanup_drops_the_pending_next_task() {
    let flow = slot_flow(true, false);
    let calls = Arc::clone(&flow.step.calls);
    let started = Arc::clone(&flow.step.started);
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new().max_concurrent_tasks(1),
        Registry::new().register(flow).unwrap(),
    )
    .await;
    let mut slot = harness.connection().await;
    slot.registered(0).await;
    let cleanup = crate::worker_pull::task_cleanup_barrier::hold("held-before-stop");
    slot.assign_step("held-before-stop").await;
    slot.final_frame("held-before-stop").await;
    timeout(Duration::from_secs(5), started.notified())
        .await
        .unwrap();
    slot.assign_step("pending-at-stop").await;
    assert!(
        timeout(Duration::from_millis(200), started.notified())
            .await
            .is_err(),
        "the pending task waits for the previous task's cleanup"
    );
    harness.stop();
    cleanup.notify_one();
    // The pending task is dropped without output or rejection, then the slot half-closes.
    slot.half_closed().await;
    assert_eq!(calls.load(Ordering::SeqCst), 1);
    drop(slot);
    harness.finish().await.unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn malformed_task_deadline_resets_the_stream_without_running_the_handler() {
    let flow = slot_flow(true, false);
    let calls = Arc::clone(&flow.step.calls);
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new()
            .max_concurrent_tasks(1)
            .stop_drain_timeout(Duration::from_millis(200)),
        Registry::new().register(flow).unwrap(),
    )
    .await;
    let mut malformed = harness.connection().await;
    malformed.registered(0).await;
    malformed
        .responses
        .send(Ok(PollWorkerTaskResponse {
            frame: Some(ResponseFrame::Task(PollWorkerTask {
                task_id: "malformed-deadline".to_string(),
                deadline: Some(prost_types::Timestamp {
                    seconds: i64::MAX,
                    nanos: -1,
                }),
                request: Some(TaskRequest::Execute(InvokeExecuteMethodRequest {
                    flow_type: "SlotFlow".to_string(),
                    step_type: "SlotStep".to_string(),
                    context: Some(ProtoContext {
                        flow_id: uuid::Uuid::new_v4().to_string(),
                        ..Default::default()
                    }),
                    step_input: Some(crate::value_mapper::encode(&()).expect("Step input")),
                    ..Default::default()
                })),
            })),
        }))
        .await
        .unwrap();
    malformed.reset().await;
    assert_eq!(calls.load(Ordering::SeqCst), 0);
    // The slot's capacity returns on a replacement connection.
    let mut replacement = harness.connection().await;
    replacement.registered(0).await;
    replacement.assign_step("after-malformed").await;
    replacement.final_frame("after-malformed").await;
    assert_eq!(calls.load(Ordering::SeqCst), 1);
    harness.stop();
    replacement.half_closed().await;
    drop((malformed, replacement));
    harness.finish().await.unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn idle_slot_drops_task_after_stop_begins_and_before_half_close() {
    let flow = slot_flow(true, false);
    let calls = Arc::clone(&flow.step.calls);
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new().max_concurrent_tasks(1),
        Registry::new().register(flow).unwrap(),
    )
    .await;
    let mut idle = harness.connection().await;
    idle.registered(0).await;
    harness.worker.stop_accepting_tasks();
    idle.assign_step("late-before-half-close").await;
    idle.assign_unknown_step("late-unknown".to_string())
        .await
        .unwrap();
    harness.shutdown.send_replace(true);
    // Neither output nor rejection is sent for a dropped task.
    idle.half_closed().await;
    assert_eq!(calls.load(Ordering::SeqCst), 0);
    drop(idle);
    harness.finish().await.unwrap();
}

#[tokio::test(flavor = "multi_thread")]
async fn stop_deadline_resets_a_slot_blocked_on_a_send() {
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new()
            .max_concurrent_tasks(1)
            .stop_drain_timeout(Duration::from_millis(200)),
        Registry::new().register(slot_flow(true, false)).unwrap(),
    )
    .await;
    let slot = harness.connection().await;
    slot.registered(0).await;
    let responses = slot.responses.clone();
    // Large rejections fill HTTP/2 flow control because this fake Server stops reading requests.
    let unknown_tasks = tokio::spawn(async move {
        let blocked = SlotConnection {
            register: PollWorkerTaskRegister::default(),
            responses,
            requests: mpsc::channel(1).1,
        };
        for index in 0..200 {
            let task_id = format!("{index}-{}", "x".repeat(64 * 1024));
            if blocked.assign_unknown_step(task_id).await.is_err() {
                return;
            }
        }
    });
    timeout(Duration::from_secs(5), async {
        while slot.requests.len() < slot.requests.max_capacity() {
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .expect("fake Server stopped reading");
    harness.stop();
    let result = timeout(Duration::from_secs(5), &mut harness.running)
        .await
        .expect("Stop returned after the drain deadline")
        .expect("join Worker");
    result.unwrap();
    unknown_tasks.abort();
}

#[tokio::test(flavor = "multi_thread")]
async fn stop_deadline_resets_half_closed_idle_slot_without_server_eof() {
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new()
            .max_concurrent_tasks(1)
            .stop_drain_timeout(Duration::from_millis(200)),
        Registry::new().register(slot_flow(true, false)).unwrap(),
    )
    .await;
    let mut idle = harness.connection().await;
    idle.registered(0).await;
    harness.stop();
    idle.half_closed().await;
    timeout(Duration::from_secs(5), &mut harness.running)
        .await
        .expect("Stop returned at the drain deadline")
        .expect("join Worker")
        .unwrap();
    timeout(Duration::from_secs(5), idle.responses.closed())
        .await
        .expect("the Worker reset the stream the Server never ended");
}

#[tokio::test(flavor = "multi_thread")]
async fn final_output_waits_flush_grace_beyond_stop_deadline_then_resets() {
    let flow = slot_flow(false, false);
    let started = Arc::clone(&flow.step.started);
    let release = Arc::clone(&flow.step.release);
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new()
            .max_concurrent_tasks(1)
            .stop_drain_timeout(Duration::from_millis(200)),
        Registry::new().register(flow).unwrap(),
    )
    .await;
    let mut busy = harness.connection().await;
    busy.registered(0).await;
    busy.assign_step("finishing").await;
    timeout(Duration::from_secs(5), started.notified())
        .await
        .unwrap();
    harness.stop();
    *release.0.lock().unwrap() = true;
    release.1.notify_all();
    busy.final_frame("finishing").await;
    busy.half_closed().await;
    assert!(
        timeout(Duration::from_secs(1), &mut harness.running)
            .await
            .is_err(),
        "the slot keeps waiting for Server EOF during the flush grace"
    );
    timeout(
        FINAL_OUTPUT_FLUSH_GRACE + Duration::from_secs(5),
        &mut harness.running,
    )
    .await
    .expect("Stop returned after the flush grace")
    .expect("join Worker")
    .unwrap();
    timeout(Duration::from_secs(5), busy.responses.closed())
        .await
        .expect("the Worker reset the stream after the flush grace");
}

#[tokio::test(flavor = "multi_thread")]
async fn final_output_held_by_backpressure_before_stop_keeps_the_flush_grace() {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind slot server");
    let address = listener.local_addr().expect("slot server address");
    let (connections, mut opened) = mpsc::channel(4);
    let (server_reads, is_reading_allowed) = watch::channel(true);
    let (server_stop, mut server_shutdown) = watch::channel(false);
    let server = tokio::spawn(
        Server::builder()
            .add_service(WorkerTaskServiceServer::new(SlotService {
                connections,
                is_reading_allowed,
            }))
            .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async move {
                while !*server_shutdown.borrow() && server_shutdown.changed().await.is_ok() {}
            }),
    );
    let channel = outbound_task_channel(
        Endpoint::from_shared(format!("http://{address}")).expect("endpoint"),
        None,
    );
    let flow = slot_flow(true, false);
    let large_result_returned = Arc::clone(&flow.large_result_returned);
    let (cache, _directory) = test_cache();
    let dispatcher = WorkerDispatcher::new(
        Registry::new().register(flow).unwrap(),
        ValueHydrator::new(FlowServiceClient::new(channel.clone()), cache),
        crate::server_protocol::NegotiatedServerProtocolVersion::default(),
    );
    let shared = Arc::new(PullShared {
        registration: Arc::new(PullRegistration::new(&["STEP_AND_RPC".to_string()])),
        state: Mutex::new(PullState {
            streams: HashMap::from([(
                0,
                StreamSlot {
                    is_busy: false,
                    is_registered: false,
                    is_draining: false,
                },
            )]),
            busy_tasks: 0,
            is_stopping: false,
            logged_unsupported: false,
        }),
        notify: Notify::new(),
        max_concurrent_tasks: 1,
        has_checked_registration_limit: Mutex::new(false),
        startup_error: Mutex::new(None),
        task_start_gate: Arc::new(Mutex::new(false)),
    });
    let (stop, stop_receiver) = watch::channel(StopSignal {
        phase: 0,
        deadline: None,
    });
    let mut stream = PullStream {
        channel,
        credential: None,
        dispatcher,
        app_worker_id: "slot-worker".to_string(),
        flow_types: vec!["SlotFlow".to_string()],
        queue: "STEP_AND_RPC".to_string(),
        slot_kind: PollWorkerSlotKind::StepAndRpc,
        stream_id: 0,
        shared: Arc::clone(&shared),
        stop: stop_receiver,
        stop_drain_timeout: Duration::from_millis(200),
        is_registered: false,
        has_recorded_result: false,
    };
    let mut polling = tokio::spawn(async move { stream.poll().await });
    let mut slot = timeout(Duration::from_secs(5), opened.recv())
        .await
        .expect("slot opened")
        .expect("slot connection");
    slot.registered(0).await;
    server_reads.send_replace(false);
    slot.assign_large_rpc().await;
    timeout(Duration::from_secs(5), large_result_returned.notified())
        .await
        .unwrap();
    timeout(Duration::from_secs(5), async {
        while shared.state.lock().expect("pull state lock").busy_tasks != 0 {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .expect("local cleanup finished");
    // The slot is idle locally, but the Server has not read the final output yet.
    let stop_deadline = Instant::now() + Duration::from_millis(200);
    stop.send_replace(StopSignal {
        phase: DRAINING,
        deadline: Some(stop_deadline),
    });
    assert!(
        timeout(Duration::from_secs(1), &mut polling).await.is_err(),
        "the slot waits past the ordinary drain deadline"
    );
    stop.send_replace(StopSignal {
        phase: FORCED,
        deadline: Some(stop_deadline),
    });
    server_reads.send_replace(true);
    let request = slot
        .next_request()
        .await
        .expect("final output, not a reset")
        .expect("final output before EOF");
    let Some(RequestFrame::TaskFrame(frame)) = request.frame else {
        panic!("expected the final RPC output");
    };
    assert_eq!(frame.task_id, "large-rpc");
    assert!(matches!(frame.output, Some(Output::Rpc(_))));
    slot.half_closed().await;
    drop(slot);
    let (was_registered, _, result) = timeout(Duration::from_secs(5), polling)
        .await
        .expect("slot closed after Server EOF")
        .expect("join slot");
    assert!(was_registered);
    result.expect("clean close after Server EOF");
    server_stop.send_replace(true);
    timeout(Duration::from_secs(5), server)
        .await
        .expect("server stopped")
        .expect("join server")
        .expect("server result");
}
