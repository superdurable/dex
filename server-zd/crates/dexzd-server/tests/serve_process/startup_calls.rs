// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Public calls that race the start of a serving period: at process start and at a take-back.

use std::net::SocketAddr;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

use dexzd_protocol::dex::admin_service_client::AdminServiceClient;
use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::step_down_active_node_response::LeaseHolderAfterStepDown;
use dexzd_protocol::dex::worker_task_service_client::WorkerTaskServiceClient;
use dexzd_protocol::dex::{
    PollWorkerTaskRegister, PollWorkerTaskRequest, PollWorkerTaskResponse,
    StepDownActiveNodeRequest, poll_worker_task_request, poll_worker_task_response,
};
use s3mock_server::SignatureVerification;
use tempfile::TempDir;
use tokio::sync::mpsc;
use tokio::task::JoinSet;
use tokio::time::Instant;
use tokio_stream::StreamExt;
use tokio_stream::wrappers::UnboundedReceiverStream;
use tokio_util::sync::CancellationToken;
use tonic::Streaming;
use tonic::transport::Channel;

use super::harness::{S3Emulator, ServeConfig, ServeProcess, free_loopback_address};
use super::{PRIMARY_BUCKET, SHUTDOWN_BUDGET};

/// What a public service answers before its serving period activates it.
const STARTING_REFUSAL: &str = "the node is starting";
/// Each call opens a connection, so some land on the startup instance while it retires.
const FRESH_CONNECTION_CALLERS: usize = 4;
/// Each keeps the connection that the startup instance accepted until that instance closes it.
const KEPT_CONNECTION_CALLERS: usize = 4;
/// Served calls per caller, so every caller outlives the startup instance's retirement.
const SERVED_CALLS_PER_CALLER: usize = 20;
const CALL_TIMEOUT: Duration = Duration::from_secs(1);
const CALL_INTERVAL: Duration = Duration::from_millis(10);
const SERVED_CALLS_BUDGET: Duration = Duration::from_secs(30);
const SERVED_CALLS_POLL_INTERVAL: Duration = Duration::from_millis(50);
const WORKER_FLOW_TYPE: &str = "StartupWorkerFlow";
const WORKER_REGISTRATION_BUDGET: Duration = Duration::from_secs(30);
/// Longer than a standby connection's 3 s age and 1 s grace, and a retirement's 4 s budget.
const WORKER_STREAM_HOLD: Duration = Duration::from_secs(6);

/// Once a process serves, no public call sent afterwards answers that the node is starting.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn no_public_call_answers_starting_once_the_process_serves() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let (primary, address, config) = serve_configuration(&directory).await;
    let stop_calling = CancellationToken::new();
    let callers = StartupCallers::start(address, &stop_calling);
    let mut process = ServeProcess::spawn(&config, address, directory.path().join("serve.log"));
    process.wait_until_serving().await;
    let reported_serving_at = Instant::now();
    callers
        .wait_until_each_was_served(SERVED_CALLS_PER_CALLER)
        .await;
    stop_calling.cancel();
    let calls = callers.finish().await;
    let first_served_at = first_served_call_sent_since(&calls, None).min(reported_serving_at);
    assert_no_starting_refusal_after(&calls, first_served_at);
    let status = process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", process.log());
    primary.shutdown().await;
    eprintln!(
        "no_public_call_answers_starting_once_the_process_serves: {} calls, {} refused as starting before the process served",
        calls.len(),
        calls
            .iter()
            .filter(|call| call.answer == CallAnswer::Starting)
            .count()
    );
}

/// Once a take-back serves, no public call sent afterwards answers that the node is starting.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn no_public_call_answers_starting_once_a_take_back_serves() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let (primary, address, config) = serve_configuration(&directory).await;
    let mut process = ServeProcess::spawn(&config, address, directory.path().join("serve.log"));
    process.wait_until_serving().await;
    let stop_calling = CancellationToken::new();
    let callers = StartupCallers::start(address, &stop_calling);
    // The kept connections now belong to the first serving period's instance.
    callers
        .wait_until_each_was_served(SERVED_CALLS_PER_CALLER)
        .await;
    let outcome = AdminServiceClient::new(direct_channel(address))
        .step_down_active_node(StepDownActiveNodeRequest {
            expected_node_id: None,
            expected_node_epoch: None,
        })
        .await
        .expect("the active steps down")
        .into_inner();
    let answered_at = Instant::now();
    assert!(
        matches!(
            outcome.lease_holder_after_step_down,
            Some(LeaseHolderAfterStepDown::TakenBackByThisProcess(_))
        ),
        "without a standby the process takes the lease back: {outcome:?}"
    );
    callers
        .wait_until_each_was_served(SERVED_CALLS_PER_CALLER)
        .await;
    stop_calling.cancel();
    let calls = callers.finish().await;
    // The take-back answer precedes the swap to the new serving instance.
    let served_again_at = first_served_call_sent_since(&calls, Some(answered_at));
    assert_no_starting_refusal_after(&calls, served_again_at);
    let status = process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", process.log());
    primary.shutdown().await;
    eprintln!(
        "no_public_call_answers_starting_once_a_take_back_serves: {} calls across the take-back",
        calls.len()
    );
}

/// A Worker stream on a connection kept across the start is accepted only where it stays open.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_worker_stream_kept_across_the_start_stays_open() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let (primary, address, config) = serve_configuration(&directory).await;
    // Connected before the process listens, it reaches the startup instance first.
    let kept_channel = Channel::from_shared(format!("http://{address}"))
        .expect("the server address is a valid URI")
        .connect_lazy();
    let registration = tokio::spawn(register_worker_until_accepted(kept_channel));
    let mut process = ServeProcess::spawn(&config, address, directory.path().join("serve.log"));
    process.wait_until_serving().await;
    let (mut incoming, frames) = tokio::time::timeout(WORKER_REGISTRATION_BUDGET, registration)
        .await
        .unwrap_or_else(|_| {
            panic!("the Worker did not register within {WORKER_REGISTRATION_BUDGET:?}")
        })
        .expect("the registration task does not panic");
    let registered_at = Instant::now();
    let held_until = registered_at + WORKER_STREAM_HOLD;
    loop {
        match tokio::time::timeout_at(held_until, incoming.next()).await {
            Err(_) => break,
            Ok(Some(Ok(response))) => answer_ping(&response, &frames),
            Ok(Some(Err(status))) => panic!(
                "the registered Worker stream was cut {:?} later: {status}",
                registered_at.elapsed()
            ),
            Ok(None) => panic!(
                "the registered Worker stream ended {:?} later",
                registered_at.elapsed()
            ),
        }
    }
    drop(frames);
    drop(incoming);
    let status = process.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(status.code(), Some(0), "{}", process.log());
    primary.shutdown().await;
}

/// Starts an object store and writes a single-process serve configuration; returns both and the address.
async fn serve_configuration(directory: &TempDir) -> (S3Emulator, SocketAddr, std::path::PathBuf) {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let endpoint = primary.endpoint();
    let address = free_loopback_address();
    let config = directory.path().join("serve.yaml");
    ServeConfig {
        primary_endpoint: &endpoint,
        hard_delete_endpoint: None,
        listen: address,
        local_directory: &directory.path().join("store"),
        lease_duration: "10s",
        fail_if_held: false,
        parade_db: None,
    }
    .write(&config);
    (primary, address, config)
}

/// Returns when the first served call sent at or after `since` was answered.
fn first_served_call_sent_since(calls: &[StartupCall], since: Option<Instant>) -> Instant {
    calls
        .iter()
        .filter(|call| {
            call.answer == CallAnswer::Served && since.is_none_or(|since| call.sent_at >= since)
        })
        .map(|call| call.answered_at)
        .min()
        .expect("the callers were served")
}

/// Panics if a call sent after `served_at` answered that the node is starting.
fn assert_no_starting_refusal_after(calls: &[StartupCall], served_at: Instant) {
    let late_refusals: Vec<_> = calls
        .iter()
        .filter(|call| call.answer == CallAnswer::Starting && call.sent_at > served_at)
        .map(|call| call.sent_at - served_at)
        .collect();
    assert!(
        late_refusals.is_empty(),
        "{} calls sent after the process served answered {STARTING_REFUSAL:?}, sent this long after: {late_refusals:?}",
        late_refusals.len()
    );
}

/// Registers a Worker over `channel`, retrying refusals; returns the registered stream and its outgoing frames.
async fn register_worker_until_accepted(
    channel: Channel,
) -> (
    Streaming<PollWorkerTaskResponse>,
    mpsc::UnboundedSender<PollWorkerTaskRequest>,
) {
    loop {
        let (frames, outgoing) = mpsc::unbounded_channel();
        frames
            .send(PollWorkerTaskRequest {
                frame: Some(poll_worker_task_request::Frame::Register(
                    PollWorkerTaskRegister {
                        queue: WORKER_FLOW_TYPE.to_owned(),
                        app_worker_id: "startup-worker".to_owned(),
                        flow_types: vec![WORKER_FLOW_TYPE.to_owned()],
                        sdk_name: "dexzd-server-tests".to_owned(),
                        sdk_version: "0".to_owned(),
                    },
                )),
            })
            .expect("the outgoing stream is open");
        let opened = WorkerTaskServiceClient::new(channel.clone())
            .poll_worker_task(UnboundedReceiverStream::new(outgoing))
            .await;
        if let Ok(response) = opened {
            let mut incoming = response.into_inner();
            if let Some(Ok(first)) = incoming.next().await
                && matches!(
                    first.frame,
                    Some(poll_worker_task_response::Frame::Registered(_))
                )
            {
                return (incoming, frames);
            }
        }
        // A process that is not listening yet, or not serving, refuses; a Worker retries.
        tokio::time::sleep(CALL_INTERVAL).await;
    }
}

/// Answers a ping on a Worker stream, as a Worker does to keep it.
fn answer_ping(
    response: &PollWorkerTaskResponse,
    frames: &mpsc::UnboundedSender<PollWorkerTaskRequest>,
) {
    if matches!(
        response.frame,
        Some(poll_worker_task_response::Frame::Ping(_))
    ) {
        frames
            .send(PollWorkerTaskRequest {
                frame: Some(poll_worker_task_request::Frame::Pong(Default::default())),
            })
            .expect("the outgoing stream stays open while the test holds it");
    }
}

fn direct_channel(address: SocketAddr) -> Channel {
    Channel::from_shared(format!("http://{address}"))
        .expect("the server address is a valid URI")
        .connect_lazy()
}

/// How one GetServerInfo call ended.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum CallAnswer {
    Served,
    Starting,
    /// Any other failure, such as a refused connection before the process listens.
    OtherFailure,
}

struct StartupCall {
    sent_at: Instant,
    answered_at: Instant,
    answer: CallAnswer,
}

/// GetServerInfo callers that run until stopped, each counting its served calls.
struct StartupCallers {
    tasks: JoinSet<Vec<StartupCall>>,
    served_counts: Vec<Arc<AtomicUsize>>,
}

impl StartupCallers {
    fn start(address: SocketAddr, stop_calling: &CancellationToken) -> Self {
        let mut tasks = JoinSet::new();
        let mut served_counts = Vec::new();
        for caller in 0..FRESH_CONNECTION_CALLERS + KEPT_CONNECTION_CALLERS {
            let served_count = Arc::new(AtomicUsize::new(0));
            served_counts.push(served_count.clone());
            let is_keeping_connection = caller >= FRESH_CONNECTION_CALLERS;
            tasks.spawn(call_until_stopped(
                address,
                is_keeping_connection,
                served_count,
                stop_calling.clone(),
            ));
        }
        Self {
            tasks,
            served_counts,
        }
    }

    /// Waits until every caller was served `count` more times than when this wait started.
    async fn wait_until_each_was_served(&self, count: usize) {
        let targets: Vec<usize> = self
            .served_counts
            .iter()
            .map(|served| served.load(Ordering::SeqCst) + count)
            .collect();
        let deadline = Instant::now() + SERVED_CALLS_BUDGET;
        while self
            .served_counts
            .iter()
            .zip(&targets)
            .any(|(served, target)| served.load(Ordering::SeqCst) < *target)
        {
            assert!(
                Instant::now() < deadline,
                "not every caller was served {count} more times within {SERVED_CALLS_BUDGET:?}"
            );
            tokio::time::sleep(SERVED_CALLS_POLL_INTERVAL).await;
        }
    }

    async fn finish(mut self) -> Vec<StartupCall> {
        let mut calls = Vec::new();
        while let Some(caller_calls) = self.tasks.join_next().await {
            calls.extend(caller_calls.expect("a caller does not panic"));
        }
        calls
    }
}

/// Calls GetServerInfo at `address` until `stop_calling`, over one kept connection or a new one each time.
async fn call_until_stopped(
    address: SocketAddr,
    is_keeping_connection: bool,
    served_count: Arc<AtomicUsize>,
    stop_calling: CancellationToken,
) -> Vec<StartupCall> {
    let endpoint = Channel::from_shared(format!("http://{address}"))
        .expect("the server address is a valid URI");
    let kept_channel = endpoint.connect_lazy();
    let mut calls = Vec::new();
    while !stop_calling.is_cancelled() {
        let sent_at = Instant::now();
        let channel = if is_keeping_connection {
            Some(kept_channel.clone())
        } else {
            tokio::time::timeout(CALL_TIMEOUT, endpoint.connect())
                .await
                .ok()
                .and_then(Result::ok)
        };
        let answer = match channel {
            None => CallAnswer::OtherFailure,
            Some(channel) => {
                let mut client = FlowServiceClient::new(channel);
                match tokio::time::timeout(CALL_TIMEOUT, client.get_server_info(())).await {
                    Ok(Ok(_)) => CallAnswer::Served,
                    Ok(Err(status)) if status.message().contains(STARTING_REFUSAL) => {
                        CallAnswer::Starting
                    }
                    Ok(Err(_)) | Err(_) => CallAnswer::OtherFailure,
                }
            }
        };
        if answer == CallAnswer::Served {
            served_count.fetch_add(1, Ordering::SeqCst);
        }
        calls.push(StartupCall {
            sent_at,
            answered_at: Instant::now(),
            answer,
        });
        tokio::time::sleep(CALL_INTERVAL).await;
    }
    calls
}
