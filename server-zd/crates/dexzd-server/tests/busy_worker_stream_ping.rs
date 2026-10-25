// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::{Duration, SystemTime};

use dexzd_clock::SystemWallClock;
use dexzd_protocol::dex::{
    self, InvokeWaitForMethodOutput, InvokeWaitForMethodRequest, InvokeWaitForMethodResponse,
    InvokeWorkerRpcRequest, InvokeWorkerRpcResponse, PollWorkerTaskFrame, PollWorkerTaskRegister,
    PollWorkerTaskRequest, PollWorkerTaskResponse, poll_worker_task_frame,
    poll_worker_task_request, poll_worker_task_response,
    worker_task_service_client::WorkerTaskServiceClient,
    worker_task_service_server::{WorkerTaskService, WorkerTaskServiceServer},
};
use dexzd_worker_dispatch::{
    DispatchEvent, DispatchOutcome, DispatchRequest, DispatchTaskPayload, MatchingConfig,
    WorkerDispatch,
};
use tokio::sync::mpsc;
use tokio::task::JoinHandle;
use tokio::time::Instant;
use tokio_stream::wrappers::{ReceiverStream, TcpListenerStream, UnboundedReceiverStream};
use tonic::transport::{Channel, Server};
use tonic::{Code, Request, Response, Status, Streaming};

struct TestServer {
    channel: Channel,
    task: JoinHandle<()>,
}

impl Drop for TestServer {
    fn drop(&mut self) {
        self.task.abort();
    }
}

#[derive(Clone)]
struct IdleResetProxy {
    upstream: Channel,
    idle_window: Duration,
    resets: Arc<AtomicUsize>,
    pings: Arc<AtomicUsize>,
    pongs: Arc<AtomicUsize>,
}

#[tonic::async_trait]
impl WorkerTaskService for IdleResetProxy {
    type PollWorkerTaskStream = ReceiverStream<Result<PollWorkerTaskResponse, Status>>;

    async fn poll_worker_task(
        &self,
        request: Request<Streaming<PollWorkerTaskRequest>>,
    ) -> Result<Response<Self::PollWorkerTaskStream>, Status> {
        let mut downstream = request.into_inner();
        let first = downstream
            .message()
            .await?
            .ok_or_else(|| Status::invalid_argument("missing registration"))?;
        let (upstream_sender, upstream_receiver) = mpsc::channel(16);
        upstream_sender
            .send(first)
            .await
            .map_err(|_| Status::unavailable("upstream request closed"))?;
        let mut upstream = WorkerTaskServiceClient::new(self.upstream.clone())
            .poll_worker_task(Request::new(ReceiverStream::new(upstream_receiver)))
            .await?
            .into_inner();
        let (downstream_sender, downstream_receiver) = mpsc::channel(16);
        let idle_window = self.idle_window;
        let resets = Arc::clone(&self.resets);
        let pings = Arc::clone(&self.pings);
        let pongs = Arc::clone(&self.pongs);
        // Closing either stream ends this test proxy's forwarder.
        let _forwarder = tokio::spawn(async move {
            let mut idle = Box::pin(tokio::time::sleep(idle_window));
            loop {
                tokio::select! {
                    message = downstream.message() => match message {
                        Ok(Some(frame)) => {
                            if matches!(frame.frame.as_ref(), Some(poll_worker_task_request::Frame::Pong(_))) {
                                pongs.fetch_add(1, Ordering::SeqCst);
                            }
                            if upstream_sender.send(frame).await.is_err() { return; }
                            idle.as_mut().reset(Instant::now() + idle_window);
                        }
                        Ok(None) | Err(_) => return,
                    },
                    message = upstream.message() => match message {
                        Ok(Some(frame)) => {
                            if matches!(frame.frame.as_ref(), Some(poll_worker_task_response::Frame::Ping(_))) {
                                pings.fetch_add(1, Ordering::SeqCst);
                            }
                            if downstream_sender.send(Ok(frame)).await.is_err() { return; }
                            idle.as_mut().reset(Instant::now() + idle_window);
                        }
                        Ok(None) | Err(_) => return,
                    },
                    () = idle.as_mut() => {
                        resets.fetch_add(1, Ordering::SeqCst);
                        if downstream_sender.send(Err(Status::unavailable("stream idle timeout"))).await.is_err() {
                            return;
                        }
                        return;
                    }
                }
            }
        });
        Ok(Response::new(ReceiverStream::new(downstream_receiver)))
    }
}

async fn start_backend(dispatch: WorkerDispatch) -> TestServer {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind backend");
    let address = listener.local_addr().expect("backend address");
    let task = tokio::spawn(async move {
        Server::builder()
            .add_service(WorkerTaskServiceServer::new(dispatch.service_for_tenant(0)))
            .serve_with_incoming(TcpListenerStream::new(listener))
            .await
            .expect("serve backend");
    });
    let channel = Channel::from_shared(format!("http://{address}"))
        .expect("backend endpoint")
        .connect()
        .await
        .expect("connect backend");
    TestServer { channel, task }
}

async fn start_proxy(service: IdleResetProxy) -> TestServer {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind proxy");
    let address = listener.local_addr().expect("proxy address");
    let task = tokio::spawn(async move {
        Server::builder()
            .add_service(WorkerTaskServiceServer::new(service))
            .serve_with_incoming(TcpListenerStream::new(listener))
            .await
            .expect("serve proxy");
    });
    let channel = Channel::from_shared(format!("http://{address}"))
        .expect("proxy endpoint")
        .connect()
        .await
        .expect("connect proxy");
    TestServer { channel, task }
}

async fn register_worker(
    channel: &Channel,
) -> (
    mpsc::UnboundedSender<PollWorkerTaskRequest>,
    Streaming<PollWorkerTaskResponse>,
) {
    let (sender, receiver) = mpsc::unbounded_channel();
    sender
        .send(PollWorkerTaskRequest {
            frame: Some(poll_worker_task_request::Frame::Register(
                PollWorkerTaskRegister {
                    queue: "Flow".to_owned(),
                    app_worker_id: "proxy-worker".to_owned(),
                    flow_types: vec!["Flow".to_owned()],
                    ..Default::default()
                },
            )),
        })
        .expect("register sends");
    let mut stream = WorkerTaskServiceClient::new(channel.clone())
        .poll_worker_task(Request::new(UnboundedReceiverStream::new(receiver)))
        .await
        .expect("poll stream opens")
        .into_inner();
    assert!(matches!(
        stream
            .message()
            .await
            .expect("registration reads")
            .expect("registration exists")
            .frame,
        Some(poll_worker_task_response::Frame::Registered(_))
    ));
    (sender, stream)
}

async fn run_long_task(
    dispatch: &WorkerDispatch,
    sender: &mpsc::UnboundedSender<PollWorkerTaskRequest>,
    stream: &mut Streaming<PollWorkerTaskResponse>,
    is_rpc: bool,
) -> usize {
    let payload = if is_rpc {
        DispatchTaskPayload::Rpc(InvokeWorkerRpcRequest {
            flow_type: "Flow".to_owned(),
            rpc_name: "getResult".to_owned(),
            ..Default::default()
        })
    } else {
        DispatchTaskPayload::WaitFor(InvokeWaitForMethodRequest {
            flow_type: "Flow".to_owned(),
            step_type: "WorkStep".to_owned(),
            ..Default::default()
        })
    };
    let mut handle = dispatch
        .dispatch(DispatchRequest {
            tenant_id: 0,
            flow_id: format!("proxy-task-{is_rpc}"),
            flow_type: "Flow".to_owned(),
            sticky_app_worker_id: None,
            payload,
            deadline: SystemTime::now() + Duration::from_secs(10),
            sticky_wait: None,
            dispatch_wait: Some(Duration::from_secs(2)),
        })
        .expect("dispatch starts");
    let task_id = loop {
        let frame = tokio::time::timeout(Duration::from_secs(3), stream.message())
            .await
            .expect("assignment arrives")
            .expect("stream reads")
            .expect("stream remains open");
        match frame.frame.expect("frame exists") {
            poll_worker_task_response::Frame::Task(task) => break task.task_id,
            poll_worker_task_response::Frame::Ping(_) => {
                sender
                    .send(PollWorkerTaskRequest {
                        frame: Some(poll_worker_task_request::Frame::Pong(
                            dex::PollWorkerTaskPong {},
                        )),
                    })
                    .expect("Pong sends");
            }
            other => panic!("expected task, got {other:?}"),
        }
    };
    let deadline = Instant::now() + Duration::from_secs(2);
    let mut pings_during_task = 0;
    loop {
        tokio::select! {
            () = tokio::time::sleep_until(deadline) => break,
            message = stream.message() => {
                let frame = message.expect("stream reads").expect("stream remains open");
                match frame.frame.expect("frame exists") {
                    poll_worker_task_response::Frame::Ping(_) => {
                        pings_during_task += 1;
                        sender.send(PollWorkerTaskRequest { frame: Some(poll_worker_task_request::Frame::Pong(dex::PollWorkerTaskPong {})) }).expect("Pong sends");
                    }
                    other => panic!("unexpected busy frame: {other:?}"),
                }
            }
        }
    }
    assert!(pings_during_task > 0);
    let output = if is_rpc {
        poll_worker_task_frame::Output::Rpc(InvokeWorkerRpcResponse::default())
    } else {
        poll_worker_task_frame::Output::WaitFor(InvokeWaitForMethodOutput {
            output: Some(dex::invoke_wait_for_method_output::Output::Result(
                InvokeWaitForMethodResponse::default(),
            )),
        })
    };
    sender
        .send(PollWorkerTaskRequest {
            frame: Some(poll_worker_task_request::Frame::TaskFrame(
                PollWorkerTaskFrame {
                    task_id,
                    output: Some(output),
                },
            )),
        })
        .expect("result sends");
    loop {
        match tokio::time::timeout(Duration::from_secs(3), handle.next())
            .await
            .expect("dispatch completes")
            .expect("dispatch event exists")
        {
            DispatchEvent::Completed(DispatchOutcome::WorkerResult { frame, .. }) => {
                assert!(matches!(
                    (is_rpc, frame.output),
                    (true, Some(poll_worker_task_frame::Output::Rpc(_)))
                        | (false, Some(poll_worker_task_frame::Output::WaitFor(_)))
                ));
                break;
            }
            DispatchEvent::Completed(outcome) => panic!("unexpected outcome: {outcome:?}"),
            _ => {}
        }
    }
    pings_during_task
}

#[tokio::test]
async fn long_step_and_rpc_survive_per_stream_idle_reset_proxy() {
    let dispatch = WorkerDispatch::new(
        MatchingConfig {
            ping_interval: Duration::from_millis(200),
            ..Default::default()
        },
        Arc::new(SystemWallClock),
    );
    let backend = start_backend(dispatch.clone()).await;
    let proxy_service = IdleResetProxy {
        upstream: backend.channel.clone(),
        idle_window: Duration::from_millis(600),
        resets: Arc::new(AtomicUsize::new(0)),
        pings: Arc::new(AtomicUsize::new(0)),
        pongs: Arc::new(AtomicUsize::new(0)),
    };
    let proxy = start_proxy(proxy_service.clone()).await;
    let (sender, mut stream) = register_worker(&proxy.channel).await;
    let step_pings = run_long_task(&dispatch, &sender, &mut stream, false).await;
    let rpc_pings = run_long_task(&dispatch, &sender, &mut stream, true).await;
    assert!(step_pings >= 3);
    assert!(rpc_pings >= 3);
    assert_eq!(0, proxy_service.resets.load(Ordering::SeqCst));
    assert!(proxy_service.pings.load(Ordering::SeqCst) >= step_pings + rpc_pings);
    assert!(proxy_service.pongs.load(Ordering::SeqCst) >= step_pings + rpc_pings);
}

#[tokio::test]
async fn idle_reset_proxy_closes_stream_when_idle_window_is_shorter_than_ping_interval() {
    let dispatch = WorkerDispatch::new(
        MatchingConfig {
            ping_interval: Duration::from_millis(200),
            ..Default::default()
        },
        Arc::new(SystemWallClock),
    );
    let backend = start_backend(dispatch).await;
    let proxy_service = IdleResetProxy {
        upstream: backend.channel.clone(),
        idle_window: Duration::from_millis(80),
        resets: Arc::new(AtomicUsize::new(0)),
        pings: Arc::new(AtomicUsize::new(0)),
        pongs: Arc::new(AtomicUsize::new(0)),
    };
    let proxy = start_proxy(proxy_service.clone()).await;
    let (_sender, mut stream) = register_worker(&proxy.channel).await;
    let failure = tokio::time::timeout(Duration::from_secs(2), stream.message())
        .await
        .expect("proxy times out")
        .expect_err("proxy resets idle stream");
    assert_eq!(Code::Unavailable, failure.code());
    assert_eq!(1, proxy_service.resets.load(Ordering::SeqCst));
}
