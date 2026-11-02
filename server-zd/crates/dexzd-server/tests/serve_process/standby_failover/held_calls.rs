// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Public calls that stay open at one process while it steps down or shuts down.

use std::net::SocketAddr;
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::worker_task_service_client::WorkerTaskServiceClient;
use dexzd_protocol::dex::{
    AttributeWrite, CloseDecision, CloseDecisionType, FlowConfig, FlowStartOptions,
    GetAttributesRequest, InvokeExecuteMethodOutput, InvokeExecuteMethodResponse, InvokeRpcRequest,
    InvokeWorkerRpcResponse, PollWorkerTaskFrame, PollWorkerTaskRegister, PollWorkerTaskRequest,
    PollWorkerTaskResponse, ReadStreamRequest, StartFlowRequest, StepDecision, StepOptions,
    WorkerTarget, invoke_execute_method_output, poll_worker_task, poll_worker_task_frame,
    poll_worker_task_request, poll_worker_task_response,
};
use tokio::sync::{mpsc, oneshot};
use tokio::task::JoinHandle;
use tokio::time::Instant;
use tokio_stream::StreamExt;
use tokio_stream::wrappers::UnboundedReceiverStream;
use tonic::Streaming;
use tonic::transport::Channel;

use crate::string_value;

/// Lets a held call reach the process before the test acts on it.
pub(super) const HELD_CALL_START_WINDOW: Duration = Duration::from_millis(300);
/// What a public call answers when a step down ends it.
pub(super) const STOPPED_SERVING_REFUSAL: &str = "stopped serving during the call";
/// A Flow type that no Worker serves, so its first Step stays pending.
const HELD_CALL_FLOW_TYPE: &str = "HeldCallFlow";
const HELD_CALL_STEP_TYPE: &str = "HeldCallStep";
const HARD_DELETE_ATTRIBUTE: &str = "secret";
/// Longer than any step down or shutdown, so only the stopping process ends the read.
const STREAM_WAIT_SECONDS: i32 = 30;
const START_ATTEMPTS: usize = 100;
const START_RETRY_INTERVAL: Duration = Duration::from_millis(100);
/// A Flow type whose only Worker answers its RPC when the test releases the answer.
const HELD_RPC_FLOW_TYPE: &str = "HeldRpcFlow";
const HELD_RPC_START_STEP_TYPE: &str = "HeldRpcStartStep";
const HELD_RPC_NAME: &str = "answerWhenReleased";
/// Longer than any step down, so only the Worker's answer ends the RPC.
const HELD_RPC_TIMEOUT_SECONDS: i32 = 30;

/// A public call that runs at one process until that process ends it.
pub(super) struct HeldCall {
    task: JoinHandle<(Result<(), tonic::Status>, Instant)>,
}

impl HeldCall {
    /// Starts a ReadStream at `address` that waits for a Stream message that never comes.
    pub(super) fn wait_for_stream_message(address: SocketAddr, flow_id: &str) -> Self {
        let request = ReadStreamRequest {
            flow_id: flow_id.to_owned(),
            flow_type: HELD_CALL_FLOW_TYPE.to_owned(),
            stream_name: "never-written".to_owned(),
            resume_token: String::new(),
            wait_time_seconds: STREAM_WAIT_SECONDS,
        };
        let mut client = direct_client(address);
        Self::spawn(async move { client.read_stream(request).await.map(drop) })
    }

    /// Starts a GetAttributes of `flow_id`'s hard-delete Attribute at `address`.
    ///
    /// A held hard-delete object read keeps the call open.
    pub(super) fn read_hard_delete_attribute(address: SocketAddr, flow_id: &str) -> Self {
        let request = GetAttributesRequest {
            flow_id: flow_id.to_owned(),
            keys: vec![HARD_DELETE_ATTRIBUTE.to_owned()],
            ..GetAttributesRequest::default()
        };
        let mut client = direct_client(address);
        Self::spawn(async move { client.get_attributes(request).await.map(drop) })
    }

    /// Starts a transactional InvokeRPC of `flow_id` at `address`; a step down drains its task.
    pub(super) fn invoke_held_rpc(address: SocketAddr, flow_id: &str) -> Self {
        let request = InvokeRpcRequest {
            flow_id: flow_id.to_owned(),
            rpc_name: HELD_RPC_NAME.to_owned(),
            timeout_seconds: HELD_RPC_TIMEOUT_SECONDS,
            request_id: format!("{flow_id}-rpc"),
            is_transactional: true,
            ..InvokeRpcRequest::default()
        };
        let mut client = direct_client(address);
        Self::spawn(async move { client.invoke_rpc(request).await.map(drop) })
    }

    fn spawn(call: impl Future<Output = Result<(), tonic::Status>> + Send + 'static) -> Self {
        let task = tokio::spawn(async move {
            let answer = call.await;
            (answer, Instant::now())
        });
        Self { task }
    }

    /// Waits for the answer; returns it and when it arrived.
    pub(super) async fn finish(self) -> (Result<(), tonic::Status>, Instant) {
        self.task.await.expect("a held call does not panic")
    }
}

/// Starts `flow_id` with a hard-delete Attribute through `channel`; no Worker ever runs it.
pub(super) async fn start_flow_with_hard_delete_attribute(channel: Channel, flow_id: &str) {
    let mut client = FlowServiceClient::new(channel);
    let request = StartFlowRequest {
        flow_id: flow_id.to_owned(),
        flow_type: HELD_CALL_FLOW_TYPE.to_owned(),
        start_step_type: HELD_CALL_STEP_TYPE.to_owned(),
        step_input: Some(string_value("held")),
        step_options: Some(StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        }),
        flow_start_options: Some(FlowStartOptions {
            attributes: vec![AttributeWrite {
                key: HARD_DELETE_ATTRIBUTE.to_owned(),
                value: Some(string_value("classified")),
                is_hard_delete_on_null: true,
                ..AttributeWrite::default()
            }],
            flow_config_override: Some(FlowConfig {
                worker_target: Some(WorkerTarget { is_pull_mode: true }),
                ..FlowConfig::default()
            }),
            ..FlowStartOptions::default()
        }),
        request_id: format!("{flow_id}-start"),
        ..StartFlowRequest::default()
    };
    start_flow(&mut client, request).await;
}

/// Starts `flow_id` through `channel` for a [`HeldRpcWorker`], which ends its first Step at once.
pub(super) async fn start_flow_for_held_rpc(channel: Channel, flow_id: &str) {
    let mut client = FlowServiceClient::new(channel);
    let request = StartFlowRequest {
        flow_id: flow_id.to_owned(),
        flow_type: HELD_RPC_FLOW_TYPE.to_owned(),
        start_step_type: HELD_RPC_START_STEP_TYPE.to_owned(),
        step_input: Some(string_value("held")),
        step_options: Some(StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        }),
        flow_start_options: Some(FlowStartOptions {
            flow_config_override: Some(FlowConfig {
                worker_target: Some(WorkerTarget { is_pull_mode: true }),
                ..FlowConfig::default()
            }),
            ..FlowStartOptions::default()
        }),
        request_id: format!("{flow_id}-start"),
        ..StartFlowRequest::default()
    };
    start_flow(&mut client, request).await;
}

/// Starts the Flow of `request`, retrying while the process cannot answer yet.
async fn start_flow(client: &mut FlowServiceClient<Channel>, request: StartFlowRequest) {
    let flow_id = request.flow_id.clone();
    for attempt in 1..=START_ATTEMPTS {
        match client.start_flow(request.clone()).await {
            Ok(_) => return,
            Err(status) => assert!(
                attempt < START_ATTEMPTS,
                "{flow_id} was never started: {status}"
            ),
        }
        tokio::time::sleep(START_RETRY_INTERVAL).await;
    }
}

/// A pull Worker of the held RPC Flow type that answers its one RPC only when released.
pub(super) struct HeldRpcWorker {
    rpc_arrived: oneshot::Receiver<()>,
    release: Option<oneshot::Sender<()>>,
    task: JoinHandle<()>,
}

impl HeldRpcWorker {
    /// Registers at `address` and waits until the registration is confirmed.
    pub(super) async fn start(address: SocketAddr) -> Self {
        let channel = Channel::from_shared(format!("http://{address}"))
            .expect("the slot address is a valid URI")
            .connect_lazy();
        let (frames, outgoing) = mpsc::unbounded_channel();
        frames
            .send(PollWorkerTaskRequest {
                frame: Some(poll_worker_task_request::Frame::Register(
                    PollWorkerTaskRegister {
                        queue: HELD_RPC_FLOW_TYPE.to_owned(),
                        app_worker_id: "held-rpc-worker".to_owned(),
                        flow_types: vec![HELD_RPC_FLOW_TYPE.to_owned()],
                        sdk_name: "dexzd-server-tests".to_owned(),
                        sdk_version: "0".to_owned(),
                    },
                )),
            })
            .expect("the outgoing stream is open");
        let mut incoming = WorkerTaskServiceClient::new(channel)
            .poll_worker_task(UnboundedReceiverStream::new(outgoing))
            .await
            .expect("PollWorkerTask opens")
            .into_inner();
        let first = incoming
            .next()
            .await
            .expect("the server answers the registration")
            .expect("the registration succeeds");
        assert!(
            matches!(
                first.frame,
                Some(poll_worker_task_response::Frame::Registered(_))
            ),
            "the first frame is registered, got {first:?}"
        );
        let (rpc_arrived_sender, rpc_arrived) = oneshot::channel();
        let (release, released) = oneshot::channel();
        let task = tokio::spawn(serve_held_rpc_flow(
            incoming,
            frames,
            rpc_arrived_sender,
            released,
        ));
        Self {
            rpc_arrived,
            release: Some(release),
            task,
        }
    }

    /// Waits up to `budget` until the RPC reached this Worker.
    pub(super) async fn wait_for_rpc(&mut self, budget: Duration) {
        tokio::time::timeout(budget, &mut self.rpc_arrived)
            .await
            .unwrap_or_else(|_| panic!("the held RPC did not reach the Worker within {budget:?}"))
            .expect("the Worker reports the RPC before it stops");
    }

    /// Lets the Worker answer the RPC.
    pub(super) fn release_rpc(&mut self) {
        self.release
            .take()
            .expect("the RPC is released once")
            .send(())
            .expect("the Worker waits for the release while it holds the RPC");
    }

    /// Closes the Worker's stream.
    pub(super) async fn stop(self) {
        self.task.abort();
        if let Err(error) = self.task.await {
            assert!(error.is_cancelled(), "the Worker task panicked: {error}");
        }
    }
}

/// Ends the first Step without closing the Flow, and answers the RPC after `released`.
async fn serve_held_rpc_flow(
    mut incoming: Streaming<PollWorkerTaskResponse>,
    frames: mpsc::UnboundedSender<PollWorkerTaskRequest>,
    rpc_arrived: oneshot::Sender<()>,
    released: oneshot::Receiver<()>,
) {
    let mut rpc_arrived = Some(rpc_arrived);
    let mut released = Some(released);
    while let Some(Ok(response)) = incoming.next().await {
        let task = match response.frame {
            Some(poll_worker_task_response::Frame::Task(task)) => task,
            Some(poll_worker_task_response::Frame::Ping(_)) => {
                let pong = PollWorkerTaskRequest {
                    frame: Some(poll_worker_task_request::Frame::Pong(Default::default())),
                };
                if frames.send(pong).is_err() {
                    return;
                }
                continue;
            }
            _ => continue,
        };
        let output = match task.request {
            Some(poll_worker_task::Request::Execute(_)) => {
                poll_worker_task_frame::Output::Execute(InvokeExecuteMethodOutput {
                    output: Some(invoke_execute_method_output::Output::Result(
                        InvokeExecuteMethodResponse {
                            step_decision: Some(StepDecision {
                                close_decision: Some(CloseDecision {
                                    close_decision_type: CloseDecisionType::DeadEnd.into(),
                                    ..CloseDecision::default()
                                }),
                                ..StepDecision::default()
                            }),
                            ..InvokeExecuteMethodResponse::default()
                        },
                    )),
                })
            }
            Some(poll_worker_task::Request::Rpc(_)) => {
                if let Some(arrived) = rpc_arrived.take()
                    && arrived.send(()).is_err()
                {
                    return;
                }
                if let Some(released) = released.take()
                    && released.await.is_err()
                {
                    return;
                }
                poll_worker_task_frame::Output::Rpc(InvokeWorkerRpcResponse {
                    output: Some(string_value("released")),
                    ..InvokeWorkerRpcResponse::default()
                })
            }
            other => panic!("the held RPC Flow sends only Execute and RPC tasks, got {other:?}"),
        };
        let reply = PollWorkerTaskRequest {
            frame: Some(poll_worker_task_request::Frame::TaskFrame(
                PollWorkerTaskFrame {
                    task_id: task.task_id,
                    output: Some(output),
                },
            )),
        };
        if frames.send(reply).is_err() {
            return;
        }
    }
}

fn direct_client(address: SocketAddr) -> FlowServiceClient<Channel> {
    let channel = Channel::from_shared(format!("http://{address}"))
        .expect("the slot address is a valid URI")
        .connect_lazy();
    FlowServiceClient::new(channel)
}
