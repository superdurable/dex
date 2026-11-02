// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Public calls that race the start of a `serve` process.

use std::net::SocketAddr;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use s3mock_server::SignatureVerification;
use tempfile::TempDir;
use tokio::task::JoinSet;
use tokio::time::Instant;
use tokio_util::sync::CancellationToken;
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

/// Once a process serves, no public call sent afterwards answers that the node is starting.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn no_public_call_answers_starting_once_the_process_serves() {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
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
    let first_served_at = calls
        .iter()
        .filter(|call| call.answer == CallAnswer::Served)
        .map(|call| call.answered_at)
        .min()
        .expect("the callers were served")
        .min(reported_serving_at);
    let late_refusals: Vec<_> = calls
        .iter()
        .filter(|call| call.answer == CallAnswer::Starting && call.sent_at > first_served_at)
        .map(|call| call.sent_at - first_served_at)
        .collect();
    assert!(
        late_refusals.is_empty(),
        "{} calls sent after the process first served answered {STARTING_REFUSAL:?}, sent this long after: {late_refusals:?}",
        late_refusals.len()
    );
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

    /// Waits until every caller was served `count` times.
    async fn wait_until_each_was_served(&self, count: usize) {
        let deadline = Instant::now() + SERVED_CALLS_BUDGET;
        while self
            .served_counts
            .iter()
            .any(|served| served.load(Ordering::SeqCst) < count)
        {
            assert!(
                Instant::now() < deadline,
                "not every caller was served {count} times within {SERVED_CALLS_BUDGET:?}"
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
