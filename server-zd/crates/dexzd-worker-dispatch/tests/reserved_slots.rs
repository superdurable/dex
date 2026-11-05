// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Reserved-slot pull Workers over raw protocol streams: registration, slot reservation,
//! hand-out, clean half-close, capacity protocol errors, and the unsupported type cache.

use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime};

use dexzd_clock::SystemWallClock;
use dexzd_cluster_metadata::LeaseValidityDeadline;
use dexzd_metrics::NodeMetrics;
use dexzd_protocol::dex::{
    self, DispatchTaskErrorReason, InvokeExecuteMethodRequest, InvokeWaitForMethodOutput,
    InvokeWaitForMethodRequest, InvokeWaitForMethodResponse, InvokeWorkerRpcRequest,
    InvokeWorkerRpcResponse, PollWorkerSlotKind, PollWorkerTaskFrame, PollWorkerTaskRegister,
    PollWorkerTaskReject, PollWorkerTaskRejectReason, PollWorkerTaskRequest,
    PollWorkerTaskResponse, StepMethodHeartbeat, poll_worker_task, poll_worker_task_frame,
    poll_worker_task_request, poll_worker_task_response,
    worker_task_service_client::WorkerTaskServiceClient,
    worker_task_service_server::WorkerTaskServiceServer,
};
use dexzd_worker_dispatch::{
    DispatchEvent, DispatchOutcome, DispatchRequest, DispatchTaskPayload, MatchingConfig,
    StepStreamRequest, StepStreamReservation, StepTaskEvent, StepTaskHandle, StepTaskSendFailure,
    WorkerDispatch,
};
use tokio::sync::mpsc;
use tokio::task::JoinHandle;
use tokio_stream::wrappers::{TcpListenerStream, UnboundedReceiverStream};
use tonic::transport::{Channel, Server};
use tonic::{Code, Request, Streaming};

struct TestServer {
    channel: Channel,
    address: std::net::SocketAddr,
    task: JoinHandle<()>,
}

impl Drop for TestServer {
    fn drop(&mut self) {
        self.task.abort();
    }
}

/// One Worker stream; dropping `sender` cleanly half-closes it.
struct Slot {
    sender: Option<mpsc::UnboundedSender<PollWorkerTaskRequest>>,
    stream: Option<Streaming<PollWorkerTaskResponse>>,
    /// Forwards a dedicated connection; aborting it breaks the stream like a crashed Worker.
    connection: Option<JoinHandle<()>>,
}

impl Slot {
    async fn next(&mut self) -> Result<PollWorkerTaskResponse, tonic::Status> {
        tokio::time::timeout(
            Duration::from_secs(3),
            self.stream.as_mut().expect("open stream").message(),
        )
        .await
        .expect("a response arrives")
        .map(|message| message.expect("the stream continues"))
    }

    async fn task(&mut self) -> dex::PollWorkerTask {
        loop {
            match self.next().await.expect("a task").frame {
                Some(poll_worker_task_response::Frame::Task(task)) => return task,
                Some(poll_worker_task_response::Frame::Ping(_)) => {}
                other => panic!("expected a task, got {other:?}"),
            }
        }
    }

    fn send(&self, frame: poll_worker_task_request::Frame) {
        self.sender
            .as_ref()
            .expect("the request side is open")
            .send(PollWorkerTaskRequest { frame: Some(frame) })
            .expect("the stream is open");
    }

    fn heartbeat(&self, task_id: &str) {
        self.send(poll_worker_task_request::Frame::TaskFrame(
            PollWorkerTaskFrame {
                task_id: task_id.to_owned(),
                output: Some(poll_worker_task_frame::Output::WaitFor(
                    InvokeWaitForMethodOutput {
                        output: Some(dex::invoke_wait_for_method_output::Output::Heartbeat(
                            StepMethodHeartbeat::default(),
                        )),
                    },
                )),
            },
        ));
    }

    fn result(&self, task_id: &str) {
        self.send(poll_worker_task_request::Frame::TaskFrame(
            PollWorkerTaskFrame {
                task_id: task_id.to_owned(),
                output: Some(poll_worker_task_frame::Output::WaitFor(
                    InvokeWaitForMethodOutput {
                        output: Some(dex::invoke_wait_for_method_output::Output::Result(
                            InvokeWaitForMethodResponse::default(),
                        )),
                    },
                )),
            },
        ));
    }

    fn reject(&self, task_id: &str, reason: PollWorkerTaskRejectReason) {
        self.send(poll_worker_task_request::Frame::Reject(
            PollWorkerTaskReject {
                task_id: task_id.to_owned(),
                reason: reason as i32,
            },
        ));
    }

    /// Cleanly half-closes the request side and keeps reading.
    fn half_close(&mut self) {
        self.sender = None;
    }

    /// Drops the response side, so the client resets the HTTP/2 stream with RST_STREAM.
    fn cancel(&mut self) {
        self.stream = None;
    }

    /// Breaks the connection as a crashed Worker would; the server never sees a half-close.
    fn reset(&mut self) {
        self.connection
            .take()
            .expect("only a slot on its own connection can break it")
            .abort();
    }
}

async fn start_server(dispatch: WorkerDispatch) -> TestServer {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind");
    let address = listener.local_addr().expect("address");
    let task = tokio::spawn(async move {
        Server::builder()
            .add_service(WorkerTaskServiceServer::new(dispatch.service_for_tenant(0)))
            .serve_with_incoming(TcpListenerStream::new(listener))
            .await
            .expect("serve");
    });
    let channel = Channel::from_shared(format!("http://{address}"))
        .expect("uri")
        .connect()
        .await
        .expect("connect");
    TestServer {
        channel,
        address,
        task,
    }
}

/// Returns a channel on its own connection to `server` and the task that forwards it.
async fn own_connection(server: &TestServer) -> (Channel, JoinHandle<()>) {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind");
    let address = listener.local_addr().expect("address");
    let target = server.address;
    let forwarder = tokio::spawn(async move {
        let (mut inbound, _) = listener.accept().await.expect("accept");
        let mut outbound = tokio::net::TcpStream::connect(target)
            .await
            .expect("connect");
        if let Err(error) = tokio::io::copy_bidirectional(&mut inbound, &mut outbound).await {
            tracing::debug!(%error, "forwarded connection ended");
        }
    });
    let channel = Channel::from_shared(format!("http://{address}"))
        .expect("uri")
        .connect()
        .await
        .expect("connect");
    (channel, forwarder)
}

/// Registers a slot on its own connection, so the test can break it.
async fn register_breakable(server: &TestServer, registration: PollWorkerTaskRegister) -> Slot {
    let (channel, connection) = own_connection(server).await;
    let mut slot = register(&channel, registration).await;
    slot.connection = Some(connection);
    slot
}

fn config() -> MatchingConfig {
    MatchingConfig {
        dispatch_wait: Duration::from_millis(400),
        sticky_wait: Duration::from_millis(100),
        ..MatchingConfig::default()
    }
}

fn reserved(
    worker_id: &str,
    flow_types: &[&str],
    slot_kind: PollWorkerSlotKind,
) -> PollWorkerTaskRegister {
    PollWorkerTaskRegister {
        app_worker_id: worker_id.to_owned(),
        flow_types: flow_types
            .iter()
            .map(|flow_type| (*flow_type).to_owned())
            .collect(),
        supports_reserved_slot_dispatch: true,
        slot_kind: slot_kind as i32,
        ..PollWorkerTaskRegister::default()
    }
}

fn legacy(queue: &str, worker_id: &str) -> PollWorkerTaskRegister {
    PollWorkerTaskRegister {
        queue: queue.to_owned(),
        app_worker_id: worker_id.to_owned(),
        flow_types: vec!["Flow".to_owned()],
        ..PollWorkerTaskRegister::default()
    }
}

async fn open(
    channel: &Channel,
    registration: PollWorkerTaskRegister,
) -> Result<(Slot, dex::PollWorkerTaskRegistered), tonic::Status> {
    let (sender, receiver) = mpsc::unbounded_channel();
    sender
        .send(PollWorkerTaskRequest {
            frame: Some(poll_worker_task_request::Frame::Register(registration)),
        })
        .expect("send registration");
    let mut client = WorkerTaskServiceClient::new(channel.clone());
    let response = client
        .poll_worker_task(Request::new(UnboundedReceiverStream::new(receiver)))
        .await?;
    let mut slot = Slot {
        sender: Some(sender),
        stream: Some(response.into_inner()),
        connection: None,
    };
    match slot.next().await?.frame {
        Some(poll_worker_task_response::Frame::Registered(registered)) => Ok((slot, registered)),
        other => panic!("expected the registration reply, got {other:?}"),
    }
}

async fn register(channel: &Channel, registration: PollWorkerTaskRegister) -> Slot {
    open(channel, registration)
        .await
        .expect("registration succeeds")
        .0
}

fn step_request(flow_type: &str, step_type: &str, sticky: Option<&str>) -> StepStreamRequest {
    StepStreamRequest {
        tenant_id: 0,
        flow_id: "flow-1".to_owned(),
        flow_type: flow_type.to_owned(),
        step_type: step_type.to_owned(),
        sticky_app_worker_id: sticky.map(str::to_owned),
        search_started_at: Instant::now(),
    }
}

fn wait_for_payload(flow_type: &str, step_type: &str) -> DispatchTaskPayload {
    DispatchTaskPayload::WaitFor(InvokeWaitForMethodRequest {
        flow_type: flow_type.to_owned(),
        step_type: step_type.to_owned(),
        ..InvokeWaitForMethodRequest::default()
    })
}

async fn reserve(dispatch: &WorkerDispatch, request: StepStreamRequest) -> StepStreamReservation {
    tokio::time::timeout(
        Duration::from_secs(3),
        dispatch.reserve_step_stream(request),
    )
    .await
    .expect("the reservation ends")
}

/// Reserves a slot for a Step of `flow_type` and sends it as task `task_id`.
async fn hand_out(
    dispatch: &WorkerDispatch,
    flow_type: &str,
    step_type: &str,
    task_id: &str,
) -> (String, StepTaskHandle) {
    let StepStreamReservation::Reserved(stream) =
        reserve(dispatch, step_request(flow_type, step_type, None)).await
    else {
        panic!("a slot is idle");
    };
    let app_worker_id = stream.app_worker_id().to_owned();
    let handle = stream
        .send_task(
            task_id.to_owned(),
            wait_for_payload(flow_type, step_type),
            SystemTime::now() + Duration::from_secs(60),
            None,
        )
        .expect("the frame goes out");
    (app_worker_id, handle)
}

async fn final_event(handle: &mut StepTaskHandle) -> StepTaskEvent {
    loop {
        let event = tokio::time::timeout(Duration::from_secs(3), handle.next())
            .await
            .expect("an event arrives")
            .expect("the task has a final event");
        if !matches!(event, StepTaskEvent::Output(_)) {
            return event;
        }
    }
}

fn rpc_request(rpc_name: &str) -> DispatchRequest {
    DispatchRequest {
        tenant_id: 0,
        flow_id: "flow-1".to_owned(),
        flow_type: "Flow".to_owned(),
        sticky_app_worker_id: None,
        payload: DispatchTaskPayload::Rpc(InvokeWorkerRpcRequest {
            flow_type: "Flow".to_owned(),
            rpc_name: rpc_name.to_owned(),
            ..InvokeWorkerRpcRequest::default()
        }),
        deadline: SystemTime::now() + Duration::from_secs(10),
        sticky_wait: None,
        dispatch_wait: Some(Duration::from_secs(2)),
    }
}

fn rpc_result(task_id: &str) -> poll_worker_task_request::Frame {
    poll_worker_task_request::Frame::TaskFrame(PollWorkerTaskFrame {
        task_id: task_id.to_owned(),
        output: Some(poll_worker_task_frame::Output::Rpc(
            InvokeWorkerRpcResponse::default(),
        )),
    })
}

#[tokio::test]
async fn reserved_slot_registrations_leave_queue_empty_and_report_the_slot_limit() {
    let dispatch = WorkerDispatch::new(config(), Arc::new(SystemWallClock));
    let server = start_server(dispatch).await;
    let mut with_queue = reserved("worker", &["Flow"], PollWorkerSlotKind::StepAndRpc);
    with_queue.queue = "Flow".to_owned();
    let mut legacy_with_kind = legacy("Flow", "worker");
    legacy_with_kind.slot_kind = PollWorkerSlotKind::RpcOnly as i32;
    let mut unknown_kind = reserved("worker", &["Flow"], PollWorkerSlotKind::StepAndRpc);
    unknown_kind.slot_kind = 99;
    for invalid in [with_queue, legacy_with_kind, unknown_kind] {
        let error = open(&server.channel, invalid)
            .await
            .err()
            .expect("the registration is rejected");
        assert_eq!(error.code(), Code::InvalidArgument);
    }
    let (_slot, registered) = open(
        &server.channel,
        reserved("worker", &["Flow"], PollWorkerSlotKind::Unspecified),
    )
    .await
    .expect("a reserved slot registers");
    assert!(registered.supports_unknown_name_rejection);
    assert_eq!(registered.max_concurrent_tasks_per_app_worker, 256);
}

#[tokio::test]
async fn one_slot_serves_its_sticky_queue_and_every_flow_type_one_task_at_a_time() {
    let dispatch = WorkerDispatch::new(config(), Arc::new(SystemWallClock));
    let server = start_server(dispatch.clone()).await;
    let mut slot = register(
        &server.channel,
        reserved(
            "worker-a",
            &["OrderFlow", "RefundFlow"],
            PollWorkerSlotKind::StepAndRpc,
        ),
    )
    .await;
    let (app_worker_id, mut first) = hand_out(&dispatch, "OrderFlow", "Charge", "task-1").await;
    assert_eq!(app_worker_id, "worker-a");
    let task = slot.task().await;
    assert_eq!(task.task_id, "task-1");
    // The busy slot is reserved for its task; no other Flow type can take it.
    let pending = tokio::spawn({
        let dispatch = dispatch.clone();
        async move {
            reserve(
                &dispatch,
                step_request("RefundFlow", "Refund", Some("worker-a")),
            )
            .await
        }
    });
    tokio::time::sleep(Duration::from_millis(50)).await;
    assert!(!pending.is_finished(), "the only slot is busy");
    slot.result("task-1");
    assert!(matches!(
        final_event(&mut first).await,
        StepTaskEvent::Completed(DispatchOutcome::WorkerResult { .. })
    ));
    // After the final output the slot takes the next task at once, from the sticky queue.
    let StepStreamReservation::Reserved(stream) = pending.await.expect("join") else {
        panic!("the slot is idle again");
    };
    let mut second = stream
        .send_task(
            "task-2".to_owned(),
            wait_for_payload("RefundFlow", "Refund"),
            SystemTime::now() + Duration::from_secs(60),
            None,
        )
        .expect("the frame goes out");
    assert_eq!(slot.task().await.task_id, "task-2");
    slot.result("task-2");
    assert!(matches!(
        final_event(&mut second).await,
        StepTaskEvent::Completed(DispatchOutcome::WorkerResult { .. })
    ));
}

#[tokio::test]
async fn rpc_only_slots_never_take_steps_and_idle_slots_serve_rpcs_first() {
    let dispatch = WorkerDispatch::new(config(), Arc::new(SystemWallClock));
    let server = start_server(dispatch.clone()).await;
    let mut rpc_slot = register(
        &server.channel,
        reserved("worker-a", &["Flow"], PollWorkerSlotKind::RpcOnly),
    )
    .await;
    assert!(matches!(
        reserve(&dispatch, step_request("Flow", "Charge", None)).await,
        StepStreamReservation::NoWorker {
            reason: DispatchTaskErrorReason::NoWorkerAvailable,
            ..
        }
    ));
    let mut rpc = dispatch
        .dispatch(rpc_request("getStatus"))
        .expect("dispatch");
    let task = rpc_slot.task().await;
    rpc_slot.send(rpc_result(&task.task_id));
    loop {
        match rpc.next().await.expect("an event") {
            DispatchEvent::Completed(DispatchOutcome::WorkerResult { .. }) => break,
            DispatchEvent::Completed(other) => panic!("unexpected outcome {other:?}"),
            _ => {}
        }
    }
    // A Step waits first, then an RPC; the freed slot goes to the RPC (plan D56).
    let mut slot = register(
        &server.channel,
        reserved("worker-b", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let (_, mut busy) = hand_out(&dispatch, "Flow", "Charge", "busy").await;
    assert_eq!(slot.task().await.task_id, "busy");
    let step_waiter = tokio::spawn({
        let dispatch = dispatch.clone();
        async move {
            let mut request = step_request("Flow", "Charge", None);
            request.search_started_at = Instant::now() + Duration::from_secs(1);
            reserve(&dispatch, request).await
        }
    });
    tokio::time::sleep(Duration::from_millis(50)).await;
    // The RPC-only slot is busy too, so the RPC waits behind the Step.
    let _rpc_slot_task = dispatch.dispatch(rpc_request("hold")).expect("dispatch");
    let _held = rpc_slot.task().await;
    let mut late_rpc = dispatch
        .dispatch(rpc_request("getStatus"))
        .expect("dispatch");
    tokio::time::sleep(Duration::from_millis(50)).await;
    slot.result("busy");
    assert!(matches!(
        final_event(&mut busy).await,
        StepTaskEvent::Completed(DispatchOutcome::WorkerResult { .. })
    ));
    let task = slot.task().await;
    assert!(
        matches!(task.request, Some(poll_worker_task::Request::Rpc(_))),
        "the RPC waiter takes the freed slot first"
    );
    slot.send(rpc_result(&task.task_id));
    loop {
        if let DispatchEvent::Completed(outcome) = late_rpc.next().await.expect("an event") {
            assert!(matches!(outcome, DispatchOutcome::WorkerResult { .. }));
            break;
        }
    }
    assert!(matches!(
        step_waiter.await.expect("join"),
        StepStreamReservation::Reserved(_)
    ));
}

#[tokio::test]
async fn a_declined_step_type_skips_that_worker_until_its_streams_are_gone() {
    let dispatch = WorkerDispatch::new(config(), Arc::new(SystemWallClock));
    let server = start_server(dispatch.clone()).await;
    let mut old = register_breakable(
        &server,
        reserved("old-worker", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let (_, mut declined) = hand_out(&dispatch, "Flow", "NewStep", "task-1").await;
    old.task().await;
    old.reject("task-1", PollWorkerTaskRejectReason::UnknownStep);
    assert!(matches!(
        final_event(&mut declined).await,
        StepTaskEvent::Rejected {
            reason: PollWorkerTaskRejectReason::UnknownStep,
            is_reserved_slot: true
        }
    ));
    // The idle old Worker still takes other Step types, but never this one again.
    assert!(matches!(
        reserve(
            &dispatch,
            step_request("Flow", "NewStep", Some("old-worker"))
        )
        .await,
        StepStreamReservation::NoWorker {
            reason: DispatchTaskErrorReason::UnknownName,
            ..
        }
    ));
    let (app_worker_id, _other) = hand_out(&dispatch, "Flow", "OldStep", "task-2").await;
    assert_eq!(app_worker_id, "old-worker");
    let mut new = register(
        &server.channel,
        reserved("new-worker", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let (app_worker_id, _new_task) = hand_out(&dispatch, "Flow", "NewStep", "task-3").await;
    assert_eq!(app_worker_id, "new-worker");
    assert_eq!(new.task().await.task_id, "task-3");
    // Once all its streams are gone, the Worker ID is forgotten.
    old.reset();
    tokio::time::sleep(Duration::from_millis(100)).await;
    let _old_again = register(
        &server.channel,
        reserved("old-worker", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let (app_worker_id, _again) = hand_out(&dispatch, "Flow", "NewStep", "task-4").await;
    assert_eq!(app_worker_id, "old-worker");
}

#[tokio::test]
async fn a_clean_half_close_tells_whether_the_worker_could_have_run_the_task() {
    let metrics = NodeMetrics::unregistered();
    let dispatch =
        WorkerDispatch::new_with_metrics(config(), Arc::new(SystemWallClock), metrics.clone());
    let server = start_server(dispatch.clone()).await;
    // Sent, no output, END_STREAM alone: after the reset grace, the Worker never ran it.
    let mut slot = register(
        &server.channel,
        reserved("worker-a", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let (_, mut handle) = hand_out(&dispatch, "Flow", "Charge", "task-1").await;
    slot.task().await;
    slot.half_close();
    assert!(matches!(
        final_event(&mut handle).await,
        StepTaskEvent::NotDelivered
    ));
    // Output, then a half-close before the final output, breaks the protocol.
    let mut slot = register(
        &server.channel,
        reserved("worker-b", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let (_, mut handle) = hand_out(&dispatch, "Flow", "Charge", "task-2").await;
    slot.task().await;
    slot.heartbeat("task-2");
    slot.half_close();
    assert!(matches!(
        final_event(&mut handle).await,
        StepTaskEvent::Disconnected { .. }
    ));
    // A transport error means the Worker may have run the task.
    let mut slot = register_breakable(
        &server,
        reserved("worker-c", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let (_, mut handle) = hand_out(&dispatch, "Flow", "Charge", "task-3").await;
    slot.task().await;
    slot.reset();
    assert!(matches!(
        final_event(&mut handle).await,
        StepTaskEvent::Disconnected { .. }
    ));
    // A legacy stream's half-close stays a disconnect (plan D70).
    let mut slot = register(&server.channel, legacy("Flow", "worker-d")).await;
    let (_, mut handle) = hand_out(&dispatch, "Flow", "Charge", "task-4").await;
    slot.task().await;
    slot.half_close();
    assert!(matches!(
        final_event(&mut handle).await,
        StepTaskEvent::Disconnected { .. }
    ));
}

#[tokio::test]
async fn a_reset_right_after_a_half_close_fails_the_task_as_a_disconnect() {
    let dispatch = WorkerDispatch::new(config(), Arc::new(SystemWallClock));
    let server = start_server(dispatch.clone()).await;
    let mut slot = register(
        &server.channel,
        reserved("worker-a", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let (_, mut handle) = hand_out(&dispatch, "Flow", "Charge", "task-1").await;
    slot.task().await;
    // Some clients cancel a call with END_STREAM and then RST_STREAM.
    slot.half_close();
    tokio::time::sleep(Duration::from_millis(20)).await;
    slot.cancel();
    assert!(matches!(
        final_event(&mut handle).await,
        StepTaskEvent::Disconnected { .. }
    ));
    // Back to back, the server may see the reset first; it is still a disconnect.
    let mut slot = register(
        &server.channel,
        reserved("worker-b", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let (_, mut handle) = hand_out(&dispatch, "Flow", "Charge", "task-2").await;
    slot.task().await;
    slot.half_close();
    slot.cancel();
    assert!(matches!(
        final_event(&mut handle).await,
        StepTaskEvent::Disconnected { .. }
    ));
}

/// A search whose waits ended before it began, such as after a long lock wait, still tries
/// the idle slots once.
#[tokio::test]
async fn a_search_past_its_dispatch_wait_still_tries_the_idle_slots() {
    let dispatch = WorkerDispatch::new(config(), Arc::new(SystemWallClock));
    let server = start_server(dispatch.clone()).await;
    let _slot = register(
        &server.channel,
        reserved("worker-a", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let mut request = step_request("Flow", "Charge", None);
    request.search_started_at = Instant::now()
        .checked_sub(config().dispatch_wait * 2)
        .expect("the monotonic clock runs longer than two dispatch waits");
    assert!(matches!(
        reserve(&dispatch, request).await,
        StepStreamReservation::Reserved(_)
    ));
}

/// A slot reserved when matching starts shutting down reconnects once it is released.
#[tokio::test]
async fn a_slot_released_during_shutdown_reconnects() {
    let dispatch = WorkerDispatch::new(config(), Arc::new(SystemWallClock));
    let server = start_server(dispatch.clone()).await;
    let mut slot = register(
        &server.channel,
        reserved("worker-a", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let StepStreamReservation::Reserved(stream) =
        reserve(&dispatch, step_request("Flow", "Charge", None)).await
    else {
        panic!("a slot is idle");
    };
    dispatch.shutdown();
    // The stream sees the shutdown while its slot is still reserved.
    tokio::time::sleep(Duration::from_millis(100)).await;
    drop(stream);
    loop {
        match slot.next().await.expect("the stream continues").frame {
            Some(poll_worker_task_response::Frame::Reconnect(_)) => break,
            Some(poll_worker_task_response::Frame::Ping(_)) => {}
            other => panic!("expected a reconnect, got {other:?}"),
        }
    }
}

/// A slot reserved while the node lease was valid sends nothing once it is not (TA9); the
/// slot goes back to its queues.
#[tokio::test]
async fn a_reserved_slot_sends_no_task_once_the_node_lease_is_not_valid() {
    let lease_validity_deadline = LeaseValidityDeadline::disarmed();
    lease_validity_deadline.arm_until(Instant::now() + Duration::from_secs(60));
    let dispatch = WorkerDispatch::new_with_lease_validity_deadline(
        config(),
        Arc::new(SystemWallClock),
        NodeMetrics::unregistered(),
        Some(lease_validity_deadline.clone()),
    );
    let server = start_server(dispatch.clone()).await;
    let mut slot = register(
        &server.channel,
        reserved("worker-a", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let StepStreamReservation::Reserved(stream) =
        reserve(&dispatch, step_request("Flow", "Charge", None)).await
    else {
        panic!("a slot is idle");
    };
    lease_validity_deadline.arm_until(Instant::now());
    let sent = stream.send_task(
        "task-1".to_owned(),
        wait_for_payload("Flow", "Charge"),
        SystemTime::now() + Duration::from_secs(60),
        None,
    );
    assert!(matches!(sent, Err(StepTaskSendFailure::LeaseInvalid)));
    let next_frame = tokio::time::timeout(
        Duration::from_millis(200),
        slot.stream.as_mut().expect("open").message(),
    )
    .await;
    assert!(
        next_frame.is_err(),
        "the Worker gets no task: {next_frame:?}"
    );
    // The slot is idle again: with a valid lease, the next reservation takes it.
    lease_validity_deadline.arm_until(Instant::now() + Duration::from_secs(60));
    assert!(matches!(
        reserve(&dispatch, step_request("Flow", "Charge", None)).await,
        StepStreamReservation::Reserved(_)
    ));
}

#[tokio::test]
async fn a_slot_that_half_closed_while_reserved_receives_no_task() {
    let dispatch = WorkerDispatch::new(config(), Arc::new(SystemWallClock));
    let server = start_server(dispatch.clone()).await;
    let mut slot = register(
        &server.channel,
        reserved("worker-a", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let StepStreamReservation::Reserved(stream) =
        reserve(&dispatch, step_request("Flow", "Charge", None)).await
    else {
        panic!("a slot is idle");
    };
    // The start commits after the Worker half-closed: the frame must not go out.
    slot.half_close();
    tokio::time::sleep(Duration::from_millis(100)).await;
    let sent = stream.send_task(
        "task-1".to_owned(),
        wait_for_payload("Flow", "Charge"),
        SystemTime::now() + Duration::from_secs(60),
        None,
    );
    assert!(matches!(sent, Err(StepTaskSendFailure::NotDelivered)));
    // The server ends the stream without a task frame.
    let end = tokio::time::timeout(
        Duration::from_secs(3),
        slot.stream.as_mut().expect("open").message(),
    )
    .await
    .expect("the stream ends");
    assert!(matches!(end, Ok(None)), "got {end:?}");
}

#[tokio::test]
async fn capacity_rejections_end_reserved_slots_and_back_off_legacy_streams_for_steps() {
    let metrics = NodeMetrics::unregistered();
    let dispatch =
        WorkerDispatch::new_with_metrics(config(), Arc::new(SystemWallClock), metrics.clone());
    let server = start_server(dispatch.clone()).await;
    let mut slot = register(
        &server.channel,
        reserved("worker-a", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let (_, mut handle) = hand_out(&dispatch, "Flow", "Charge", "task-1").await;
    slot.task().await;
    slot.reject("task-1", PollWorkerTaskRejectReason::NoCapacity);
    assert!(matches!(
        final_event(&mut handle).await,
        StepTaskEvent::Rejected {
            reason: PollWorkerTaskRejectReason::NoCapacity,
            is_reserved_slot: true
        }
    ));
    let error = slot
        .next()
        .await
        .expect_err("the stream ends with an error");
    assert_eq!(error.code(), Code::InvalidArgument);
    // A legacy capacity rejection is normal; Steps skip the stream for a backoff, RPCs do not.
    let mut legacy_slot = register(&server.channel, legacy("Flow", "worker-b")).await;
    let (_, mut handle) = hand_out(&dispatch, "Flow", "Charge", "task-2").await;
    legacy_slot.task().await;
    legacy_slot.reject("task-2", PollWorkerTaskRejectReason::NoCapacity);
    assert!(matches!(
        final_event(&mut handle).await,
        StepTaskEvent::Rejected {
            reason: PollWorkerTaskRejectReason::NoCapacity,
            is_reserved_slot: false
        }
    ));
    let started = Instant::now();
    let step = tokio::spawn({
        let dispatch = dispatch.clone();
        async move { reserve(&dispatch, step_request("Flow", "Charge", None)).await }
    });
    let mut rpc = dispatch
        .dispatch(rpc_request("getStatus"))
        .expect("dispatch");
    let task = legacy_slot.task().await;
    assert!(matches!(
        task.request,
        Some(poll_worker_task::Request::Rpc(_))
    ));
    legacy_slot.send(rpc_result(&task.task_id));
    loop {
        if let DispatchEvent::Completed(outcome) = rpc.next().await.expect("an event") {
            assert!(matches!(outcome, DispatchOutcome::WorkerResult { .. }));
            break;
        }
    }
    assert!(matches!(
        step.await.expect("join"),
        StepStreamReservation::Reserved(_)
    ));
    assert!(
        started.elapsed() >= Duration::from_millis(250),
        "the Step waited for the stream's backoff"
    );
}

#[tokio::test]
async fn an_rpc_declined_for_capacity_on_a_reserved_slot_moves_to_another_slot() {
    let dispatch = WorkerDispatch::new(config(), Arc::new(SystemWallClock));
    let server = start_server(dispatch.clone()).await;
    let mut broken = register(
        &server.channel,
        reserved("worker-a", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let mut rpc = dispatch
        .dispatch(rpc_request("getStatus"))
        .expect("dispatch");
    let task = broken.task().await;
    let mut good = register(
        &server.channel,
        reserved("worker-b", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    broken.reject(&task.task_id, PollWorkerTaskRejectReason::NoCapacity);
    assert_eq!(
        broken.next().await.expect_err("the stream ends").code(),
        Code::InvalidArgument
    );
    let retried = good.task().await;
    assert_eq!(retried.task_id, task.task_id);
    good.send(rpc_result(&retried.task_id));
    loop {
        if let DispatchEvent::Completed(outcome) = rpc.next().await.expect("an event") {
            assert!(matches!(outcome, DispatchOutcome::WorkerResult { .. }));
            break;
        }
    }
}

#[tokio::test]
async fn an_execute_task_carries_the_engine_task_id_and_deadline() {
    let dispatch = WorkerDispatch::new(config(), Arc::new(SystemWallClock));
    let server = start_server(dispatch.clone()).await;
    let mut slot = register(
        &server.channel,
        reserved("worker-a", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let StepStreamReservation::Reserved(stream) =
        reserve(&dispatch, step_request("Flow", "Charge", None)).await
    else {
        panic!("a slot is idle");
    };
    let deadline = SystemTime::UNIX_EPOCH + Duration::from_secs(4_000_000_000);
    let _handle = stream
        .send_task(
            "0192f3a4-5b6c-7d8e-9fa0-b1c2d3e4f5a6".to_owned(),
            DispatchTaskPayload::Execute(InvokeExecuteMethodRequest {
                flow_type: "Flow".to_owned(),
                step_type: "Charge".to_owned(),
                ..InvokeExecuteMethodRequest::default()
            }),
            deadline,
            None,
        )
        .expect("the frame goes out");
    let task = slot.task().await;
    assert_eq!(task.task_id, "0192f3a4-5b6c-7d8e-9fa0-b1c2d3e4f5a6");
    assert_eq!(task.deadline.expect("deadline").seconds, 4_000_000_000);
}
/// A freed slot that woke an RPC waiter is that RPC's, even before the RPC runs (plan D56).
#[tokio::test]
async fn a_new_step_never_takes_a_slot_offered_to_a_waiting_rpc() {
    let metrics = NodeMetrics::unregistered();
    let dispatch =
        WorkerDispatch::new_with_metrics(config(), Arc::new(SystemWallClock), metrics.clone());
    let server = start_server(dispatch.clone()).await;
    let _slot = register(
        &server.channel,
        reserved("worker", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let StepStreamReservation::Reserved(held) =
        reserve(&dispatch, step_request("Flow", "Hold", None)).await
    else {
        panic!("idle slot");
    };
    let _rpc = dispatch.dispatch(rpc_request("getStatus")).unwrap();
    tokio::time::timeout(Duration::from_secs(1), async {
        while metrics.matching_queued_tasks.get() == 0 {
            tokio::task::yield_now().await;
        }
    })
    .await
    .unwrap();
    drop(held);
    // The RPC has been notified but has not been scheduled on this current-thread executor.
    let new_step = futures::FutureExt::now_or_never(
        dispatch.reserve_step_stream(step_request("Flow", "New", None)),
    );
    assert!(
        !matches!(new_step, Some(StepStreamReservation::Reserved(_))),
        "new Step stole the freed slot while a compatible RPC was already waiting"
    );
}

/// A waiter checks the unsupported type cache when a slot frees, not when it starts waiting.
#[tokio::test]
async fn an_evicted_decline_lets_its_waiter_take_the_next_idle_slot() {
    let metrics = NodeMetrics::unregistered();
    let mut matching = config();
    matching.dispatch_wait = Duration::from_secs(10);
    matching.unsupported_type_cache_entries = 1;
    let dispatch =
        WorkerDispatch::new_with_metrics(matching, Arc::new(SystemWallClock), metrics.clone());
    let server = start_server(dispatch.clone()).await;
    let mut slot = register(
        &server.channel,
        reserved("worker", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let (_, mut first) = hand_out(&dispatch, "Flow", "A", "a").await;
    slot.task().await;
    slot.reject("a", PollWorkerTaskRejectReason::UnknownStep);
    final_event(&mut first).await;
    let waiting = dispatch.reserve_step_stream(step_request("Flow", "A", None));
    tokio::pin!(waiting);
    assert!(futures::poll!(&mut waiting).is_pending());
    let (_, mut second) = hand_out(&dispatch, "Flow", "B", "b").await;
    slot.task().await;
    slot.reject("b", PollWorkerTaskRejectReason::UnknownStep);
    final_event(&mut second).await;
    tokio::time::timeout(Duration::from_secs(1), async {
        while metrics.matching_unsupported_type_evictions.get() == 0 {
            tokio::task::yield_now().await;
        }
    })
    .await
    .unwrap();
    // A different request can take and release the slot, ensuring another idle transition.
    let StepStreamReservation::Reserved(available) =
        reserve(&dispatch, step_request("Flow", "C", None)).await
    else {
        panic!("available worker");
    };
    drop(available);
    assert!(
        tokio::time::timeout(Duration::from_millis(150), &mut waiting)
            .await
            .is_ok(),
        "A's cached exclusion survived eviction and a subsequent idle transition"
    );
}

/// An evicted decline is probed again only after its Worker's slot next becomes idle (plan 3.5).
#[tokio::test]
async fn an_eviction_never_reprobes_a_slot_that_was_already_idle() {
    let mut matching = config();
    matching.unsupported_type_cache_entries = 1;
    let dispatch = WorkerDispatch::new(matching, Arc::new(SystemWallClock));
    let server = start_server(dispatch.clone()).await;
    let mut first = register(
        &server.channel,
        reserved("first", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let mut second = register(
        &server.channel,
        reserved("second", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let (worker_id, mut first_task) = hand_out(&dispatch, "Flow", "Missing", "first").await;
    assert_eq!(worker_id, "first");
    first.task().await;
    first.reject("first", PollWorkerTaskRejectReason::UnknownStep);
    final_event(&mut first_task).await;
    let (worker_id, mut second_task) = hand_out(&dispatch, "Flow", "Missing", "second").await;
    assert_eq!(worker_id, "second");
    second.task().await;
    second.reject("second", PollWorkerTaskRejectReason::UnknownStep);
    final_event(&mut second_task).await;
    let reprobe = futures::FutureExt::now_or_never(
        dispatch.reserve_step_stream(step_request("Flow", "Missing", None)),
    );
    assert!(
        !matches!(reprobe, Some(StepStreamReservation::Reserved(_))),
        "eviction immediately re-probes the already-idle first Worker, allowing a start/return loop"
    );
}

/// The handler may still run after output, so a later rejection ends the stream.
#[tokio::test]
async fn a_rejection_after_output_ends_the_stream_instead_of_freeing_the_slot() {
    let dispatch = WorkerDispatch::new(config(), Arc::new(SystemWallClock));
    let server = start_server(dispatch.clone()).await;
    let mut slot = register(
        &server.channel,
        reserved("worker", &["Flow"], PollWorkerSlotKind::StepAndRpc),
    )
    .await;
    let (_, mut running) = hand_out(&dispatch, "Flow", "Running", "running").await;
    slot.task().await;
    slot.heartbeat("running");
    slot.reject("running", PollWorkerTaskRejectReason::UnknownStep);
    assert!(matches!(
        final_event(&mut running).await,
        StepTaskEvent::Disconnected { .. }
    ));
    assert_eq!(
        slot.next().await.expect_err("the stream ends").code(),
        Code::InvalidArgument
    );
    let next = reserve(&dispatch, step_request("Flow", "Other", None)).await;
    assert!(
        !matches!(next, StepStreamReservation::Reserved(_)),
        "the slot was reused without a final output after it had already produced a heartbeat"
    );
}
