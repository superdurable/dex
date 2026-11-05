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
        // The mock server runtime owns this reader and cancels it when the fixture is dropped.
        let _stream_reader = tokio::spawn(async move {
            loop {
                let event = incoming.message().await;
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
        let server = tokio::spawn(
            Server::builder()
                .add_service(WorkerTaskServiceServer::new(SlotService { connections }))
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
        RpcList::new().function_without_input(Rpc::new("get_slot_value"), Self::get_slot_value)
    }
}

impl SlotFlow {
    fn get_slot_value(&self, _: &mut Context) -> HandlerResult<RpcResult<String>> {
        Ok(RpcResult::new("value".to_string()))
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
    let mut harness = SlotHarness::new(
        PullWorkerOptions::new().max_concurrent_tasks(3),
        Registry::new(),
    )
    .await;
    let mut connections = Vec::new();
    for _ in 0..3 {
        connections.push(harness.connection().await);
    }
    connections[0].registered(2).await;
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
    drop(connections);
    let failure = harness.finish().await.unwrap_err();
    assert!(
        failure
            .to_string()
            .contains("matching.maxConcurrentTasksCap 2")
    );
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
