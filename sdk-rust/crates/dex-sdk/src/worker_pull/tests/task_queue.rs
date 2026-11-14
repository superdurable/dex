// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::convert::Infallible;
use std::task::Poll;

use dex_protocol::dex::ServerInfo;
use tonic::codegen::{BoxFuture, Service, http};
use tonic::server::{NamedService, UnaryService};

use super::*;
use crate::{ClientOptions, StartFlowOptions};

/// Echoes `registered_task_queue` in Registered and reports each requested task queue.
#[derive(Clone)]
struct TaskQueueEchoService {
    registered_task_queue: String,
    requested_task_queues: mpsc::Sender<String>,
    half_closed: mpsc::Sender<()>,
}

#[tonic::async_trait]
impl WorkerTaskService for TaskQueueEchoService {
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
            return Err(Status::invalid_argument("missing Register"));
        };
        self.requested_task_queues
            .send(register.task_queue)
            .await
            .map_err(|error| Status::internal(error.to_string()))?;
        let (sender, receiver) = mpsc::channel(8);
        sender
            .send(Ok(PollWorkerTaskResponse {
                frame: Some(ResponseFrame::Registered(PollWorkerTaskRegistered {
                    task_queue: self.registered_task_queue.clone(),
                    ..PollWorkerTaskRegistered::default()
                })),
            }))
            .await
            .map_err(|error| Status::internal(error.to_string()))?;
        let half_closed = self.half_closed.clone();
        // The test runtime owns cancellation of this deliberately detached mock stream reader.
        let _stream_reader = tokio::spawn(async move {
            while let Ok(Some(_)) = incoming.message().await {}
            if let Err(error) = half_closed.send(()).await {
                eprintln!("report task queue stream half-close: {error}");
            }
            drop(sender);
        });
        Ok(Response::new(ReceiverStream::new(receiver)))
    }
}

struct EchoServer {
    address: std::net::SocketAddr,
    requested_task_queues: mpsc::Receiver<String>,
    half_closed: mpsc::Receiver<()>,
    stop: watch::Sender<bool>,
    server: JoinHandle<Result<(), tonic::transport::Error>>,
}

async fn start_echo_server(registered_task_queue: &str) -> EchoServer {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind task queue service");
    let address = listener.local_addr().expect("service address");
    let (requested_sender, requested_task_queues) = mpsc::channel(8);
    let (half_closed_sender, half_closed) = mpsc::channel(8);
    let (stop, mut shutdown) = watch::channel(false);
    let server = tokio::spawn(
        Server::builder()
            .add_service(WorkerTaskServiceServer::new(TaskQueueEchoService {
                registered_task_queue: registered_task_queue.to_string(),
                requested_task_queues: requested_sender,
                half_closed: half_closed_sender,
            }))
            .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async move {
                while !*shutdown.borrow() && shutdown.changed().await.is_ok() {}
            }),
    );
    EchoServer {
        address,
        requested_task_queues,
        half_closed,
        stop,
        server,
    }
}

fn pull_worker(address: std::net::SocketAddr, options: &PullWorkerOptions) -> PullWorker {
    let channel = outbound_task_channel(
        Endpoint::from_shared(format!("http://{address}")).expect("endpoint"),
        None,
    );
    let (cache, _directory) = test_cache();
    let dispatcher = WorkerDispatcher::new(
        Registry::new(),
        ValueHydrator::new(FlowServiceClient::new(channel.clone()), cache),
        crate::server_protocol::NegotiatedServerProtocolVersion::default(),
    );
    PullWorker::new(options, &dispatcher, channel).expect("pull Worker")
}

#[tokio::test(flavor = "multi_thread")]
async fn mismatched_task_queue_echo_is_a_permanent_rejection() {
    let mut server = start_echo_server("").await;
    let worker = pull_worker(
        server.address,
        &PullWorkerOptions::new()
            .max_concurrent_tasks(1)
            .task_queue("blue")
            .stop_on_permanent_stream_rejection(true),
    );
    let registration = Arc::clone(&worker.registration);
    let (_stop, shutdown) = watch::channel(false);
    let running = tokio::spawn(async move { worker.run(shutdown).await });
    let requested = timeout(Duration::from_secs(5), server.requested_task_queues.recv())
        .await
        .expect("Register deadline")
        .expect("requested task queue");
    assert_eq!(requested, "blue");
    let error = timeout(Duration::from_secs(5), registration.wait())
        .await
        .expect("rejection deadline")
        .expect_err("a mismatched echo never becomes ready");
    let rejection = error.pull_registration_error().expect("typed rejection");
    assert_eq!(rejection.status.code(), Code::FailedPrecondition);
    assert_eq!(rejection.task_queue, "blue");
    assert!(
        rejection
            .status
            .message()
            .contains("Server registered task queue \"\" instead of \"blue\""),
        "{rejection}"
    );
    timeout(Duration::from_secs(5), server.half_closed.recv())
        .await
        .expect("half-close deadline")
        .expect("the Worker half-closed the mismatched stream");
    let snapshot = registration.snapshot();
    assert!(!snapshot.is_ready);
    assert_eq!(snapshot.queues[0].task_queue, "blue");
    assert_eq!(
        snapshot.queues[0]
            .last_permanent_error
            .as_ref()
            .map(Status::code),
        Some(Code::FailedPrecondition)
    );
    let result = timeout(Duration::from_secs(5), running)
        .await
        .expect("permanent rejection stops the Worker")
        .expect("Worker task");
    assert!(
        result
            .expect_err("stop on permanent rejection")
            .pull_registration_error()
            .is_some_and(|rejection| rejection.task_queue == "blue")
    );
    server.stop.send_replace(true);
    server
        .server
        .await
        .expect("server task")
        .expect("server shutdown");
}

#[tokio::test(flavor = "multi_thread")]
async fn matching_task_queue_echo_registers() {
    for task_queue in ["", "blue"] {
        let mut server = start_echo_server(task_queue).await;
        let worker = pull_worker(
            server.address,
            &PullWorkerOptions::new()
                .max_concurrent_tasks(1)
                .task_queue(task_queue),
        );
        let registration = Arc::clone(&worker.registration);
        let (stop, shutdown) = watch::channel(false);
        let running = tokio::spawn(async move { worker.run(shutdown).await });
        let requested = timeout(Duration::from_secs(5), server.requested_task_queues.recv())
            .await
            .expect("Register deadline")
            .expect("requested task queue");
        assert_eq!(requested, task_queue);
        timeout(Duration::from_secs(5), registration.wait())
            .await
            .expect("readiness deadline")
            .expect("ready");
        let snapshot = registration.snapshot();
        assert!(snapshot.is_ready);
        assert_eq!(snapshot.queues[0].task_queue, task_queue);
        stop.send_replace(true);
        timeout(Duration::from_secs(5), running)
            .await
            .expect("stop deadline")
            .expect("Worker task")
            .expect("clean stop");
        server.stop.send_replace(true);
        server
            .server
            .await
            .expect("server task")
            .expect("server shutdown");
    }
}

#[test]
fn worker_construction_rejects_invalid_and_reserved_task_queues() {
    let (cache, _directory) = test_cache();
    let registry = Registry::new()
        .register(RegisteredFlow)
        .expect("register Flow");
    for task_queue in ["dex.default", "dex.blue", "bad name", &"q".repeat(64)] {
        let error = Worker::try_new(
            registry.clone(),
            Arc::clone(&cache),
            WorkerOptions::new().pull(PullWorkerOptions::new().task_queue(task_queue)),
        )
        .err()
        .expect("invalid Worker task queue");
        assert!(
            matches!(error, SdkError::FlowDefinition { .. }),
            "{task_queue}: {error}"
        );
        assert!(error.to_string().contains("task queue"), "{error}");
    }
    Worker::try_new(
        registry,
        cache,
        WorkerOptions::new().pull(PullWorkerOptions::new().task_queue("blue")),
    )
    .expect("valid Worker task queue");
}

/// Serves only GetServerInfo with protocol 4; every other FlowService call is unimplemented.
#[derive(Clone)]
struct ProtocolFourFlowService;

impl NamedService for ProtocolFourFlowService {
    const NAME: &'static str = "dex.FlowService";
}

impl<B> Service<http::Request<B>> for ProtocolFourFlowService
where
    B: tonic::codegen::Body + Send + 'static,
    B::Error: Into<tonic::codegen::StdError> + Send + 'static,
{
    type Response = http::Response<tonic::body::Body>;
    type Error = Infallible;
    type Future = BoxFuture<Self::Response, Self::Error>;

    fn poll_ready(&mut self, _: &mut std::task::Context<'_>) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, request: http::Request<B>) -> Self::Future {
        Box::pin(async move {
            if request.uri().path() == "/dex.FlowService/GetServerInfo" {
                let mut grpc = tonic::server::Grpc::new(tonic_prost::ProstCodec::default());
                Ok(grpc.unary(ProtocolFourServerInfo, request).await)
            } else {
                Ok(Status::unimplemented("protocol 4 test Server").into_http())
            }
        })
    }
}

struct ProtocolFourServerInfo;

impl UnaryService<()> for ProtocolFourServerInfo {
    type Response = ServerInfo;
    type Future = BoxFuture<Response<Self::Response>, Status>;

    fn call(&mut self, _: Request<()>) -> Self::Future {
        Box::pin(async {
            Ok(Response::new(ServerInfo {
                server_version: "protocol-four".to_string(),
                minimum_supported_protocol_version: 4,
                current_protocol_version: 4,
                ..ServerInfo::default()
            }))
        })
    }
}

#[test]
fn named_task_queues_fail_closed_on_a_protocol_four_server() {
    let runtime = tokio::runtime::Runtime::new().expect("test runtime");
    let listener = runtime
        .block_on(TcpListener::bind("127.0.0.1:0"))
        .expect("bind protocol 4 Server");
    let address = listener.local_addr().expect("Server address");
    let (stop, mut shutdown) = watch::channel(false);
    let server = runtime.spawn(
        Server::builder()
            .add_service(ProtocolFourFlowService)
            .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async move {
                while !*shutdown.borrow() && shutdown.changed().await.is_ok() {}
            }),
    );
    let (cache, _directory) = test_cache();
    let registry = Registry::new()
        .register(RegisteredFlow)
        .expect("register Flow");
    let worker = Worker::try_new(
        registry.clone(),
        Arc::clone(&cache),
        WorkerOptions::new()
            .server_address(address.to_string())
            .pull(PullWorkerOptions::new().task_queue("blue")),
    )
    .expect("Worker");
    let error = worker
        .start()
        .expect_err("protocol 4 cannot serve a named task queue");
    assert!(
        error
            .to_string()
            .contains("pull Worker with task queue \"blue\" requires Server protocol version 5"),
        "{error}"
    );
    let default_worker = Worker::try_new(
        registry.clone(),
        Arc::clone(&cache),
        WorkerOptions::new().server_address(address.to_string()),
    )
    .expect("default-queue Worker");
    let default_error = default_worker
        .start()
        .expect_err("the test Server rejects Attribute-index synchronization");
    assert!(
        !default_error.to_string().contains("task queue"),
        "the default task queue passes the protocol check: {default_error}"
    );
    let client = crate::Client::try_new(
        registry,
        cache,
        ClientOptions::new().server_address(address.to_string()),
    )
    .expect("Client");
    let invalid = client
        .start_flow_with_options(
            &RegisteredFlow,
            "task-queue-invalid",
            (),
            StartFlowOptions::new().task_queue("bad name"),
        )
        .expect_err("invalid task queue");
    assert!(
        matches!(invalid, SdkError::InvalidArgument { .. }),
        "{invalid}"
    );
    for task_queue in ["blue", "dex.default"] {
        let old_server = client
            .start_flow_with_options(
                &RegisteredFlow,
                "task-queue-old-server",
                (),
                StartFlowOptions::new().task_queue(task_queue),
            )
            .expect_err("protocol 4 cannot start on a named task queue");
        assert!(
            matches!(old_server, SdkError::Service { .. }),
            "{old_server}"
        );
        assert!(
            old_server
                .to_string()
                .contains("requires Server protocol version 5"),
            "{old_server}"
        );
    }
    stop.send_replace(true);
    runtime
        .block_on(server)
        .expect("Server task")
        .expect("Server shutdown");
}
