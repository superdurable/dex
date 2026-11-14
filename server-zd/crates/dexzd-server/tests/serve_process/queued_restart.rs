// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Queued Steps survive a SIGKILL restart or hot standby failover (plan TR1, TB2, TB3, TB26).

use std::collections::BTreeMap;
use std::fs;
use std::net::SocketAddr;
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use dexzd_api::HOT_STANDBY_HEALTH_SERVICE_NAME;
use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::worker_task_service_client::WorkerTaskServiceClient;
use dexzd_protocol::dex::{
    CloseDecision, CloseDecisionType, FlowConfig, FlowStartOptions, FlowStatus,
    GetFlowStateRequest, InvokeExecuteMethodResponse, StartFlowRequest, StepDecision,
    StepDurability, StepMethodInvocationPhase, StepMethodPendingReason, StepOptions,
    WaitForFlowRequest, WorkerTarget,
};
use s3local_server::SignatureVerification;
use tempfile::TempDir;
use tonic::transport::Channel;
use tonic_health::pb::HealthCheckRequest;
use tonic_health::pb::health_check_response::ServingStatus;
use tonic_health::pb::health_client::HealthClient;

use super::harness::{PullWorker, S3Emulator, ServeConfig, ServeProcess, free_loopback_address};
use super::metrics::try_scrape;
use super::{FLOW_TYPE, PRIMARY_BUCKET, SHUTDOWN_BUDGET, string_value};

/// Flows of each Step kind; together they make a backlog across the 16 partitions.
const FLOWS_PER_KIND: usize = 12;
const STEP_TYPE: &str = "QueuedStep";
const LOCK_KEY: &str = "balance";
/// Short in-memory waits, so attempts reach the durable queue quickly.
const MATCHING_SETTINGS: &str =
    "matching:\n  stickyWait: 50ms\n  syncMatchWait: 50ms\n  headsResyncInterval: 0s\n";
/// Keeps attempts in the in-memory sync wait for the whole test.
const SYNC_WAIT_SETTINGS: &str =
    "matching:\n  stickyWait: 50ms\n  syncMatchWait: 1h\n  headsResyncInterval: 0s\n";
const QUEUED_BUDGET: Duration = Duration::from_secs(30);
const COMPLETION_WAIT_SECONDS: i32 = 30;
/// A killed active's lease; the hot standby takes over after it expires.
const ACTIVE_LEASE_DURATION: &str = "6s";
const TAKEOVER_BUDGET: Duration = Duration::from_secs(30);
const HOT_STANDBY_BUDGET: Duration = Duration::from_secs(30);
const POLL_INTERVAL: Duration = Duration::from_millis(50);
const HOT_TAKEOVER_LOG_LINE: &str = "serving from the hot standby's local store";

#[derive(Clone, Copy, Debug)]
enum QueuedStepKind {
    Sync,
    Async,
    SyncWithExecuteLock,
}

const STEP_KINDS: [QueuedStepKind; 3] = [
    QueuedStepKind::Sync,
    QueuedStepKind::Async,
    QueuedStepKind::SyncWithExecuteLock,
];

/// TR1 and TB2: SIGKILL with queued SYNC, ASYNC, and locked Steps, restart on the same data.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn queued_steps_complete_on_attempt_one_after_a_sigkill_restart() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let data_directory = directory.path().join("data");
    let first_metrics = free_loopback_address();
    let first_config =
        write_dev_config(directory.path(), "first", first_metrics, MATCHING_SETTINGS);
    let mut first = ServeProcess::spawn_dev(
        &data_directory,
        &first_config,
        free_loopback_address(),
        directory.path().join("first.log"),
    );
    let channel = first.wait_until_serving().await;
    let flow_ids = start_queued_flows(&channel, first_metrics, "restart", true).await;

    let killed = first.kill().await;
    assert!(!killed.success(), "SIGKILL stops the first process");
    let second_metrics = free_loopback_address();
    let second_config = write_dev_config(
        directory.path(),
        "second",
        second_metrics,
        MATCHING_SETTINGS,
    );
    let mut second = ServeProcess::spawn_dev(
        &data_directory,
        &second_config,
        free_loopback_address(),
        directory.path().join("second.log"),
    );
    let channel = second.wait_until_serving().await;
    // A restart rebuilds the queue from the index.
    wait_for_queue_entries(second_metrics, flow_ids.len()).await;
    complete_queued_flows(&channel, second_metrics, &flow_ids).await;
    let stopped = second.terminate(SHUTDOWN_BUDGET).await;
    assert!(stopped.success(), "{}", second.log());
}

/// TB26: SIGKILL while attempts wait in memory for a Worker; the restart dispatches them again.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn steps_in_the_sync_wait_complete_on_attempt_one_after_a_sigkill_restart() {
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let data_directory = directory.path().join("data");
    let first_metrics = free_loopback_address();
    let first_config =
        write_dev_config(directory.path(), "first", first_metrics, SYNC_WAIT_SETTINGS);
    let mut first = ServeProcess::spawn_dev(
        &data_directory,
        &first_config,
        free_loopback_address(),
        directory.path().join("first.log"),
    );
    let channel = first.wait_until_serving().await;
    let flow_ids = start_queued_flows(&channel, first_metrics, "sync-wait", false).await;

    let killed = first.kill().await;
    assert!(!killed.success(), "SIGKILL stops the first process");
    let second_metrics = free_loopback_address();
    let second_config = write_dev_config(
        directory.path(),
        "second",
        second_metrics,
        SYNC_WAIT_SETTINGS,
    );
    let mut second = ServeProcess::spawn_dev(
        &data_directory,
        &second_config,
        free_loopback_address(),
        directory.path().join("second.log"),
    );
    let channel = second.wait_until_serving().await;
    complete_queued_flows(&channel, second_metrics, &flow_ids).await;
    let stopped = second.terminate(SHUTDOWN_BUDGET).await;
    assert!(stopped.success(), "{}", second.log());
}

/// TB3: the same backlog survives a SIGKILL of the active and a takeover by a hot standby.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn queued_steps_complete_on_attempt_one_after_a_hot_standby_failover() {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let primary_endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let serve_config = |name: &str, listen: SocketAddr, metrics: SocketAddr| {
        let path = directory.path().join(format!("{name}.yaml"));
        ServeConfig {
            primary_endpoint: &primary_endpoint,
            hard_delete_endpoint: None,
            listen,
            local_directory: &directory.path().join(format!("{name}-store")),
            lease_duration: ACTIVE_LEASE_DURATION,
            fail_if_held: false,
            parade_db: None,
        }
        .write(&path);
        append_to_config(
            &path,
            &format!("metrics:\n  listen: '{metrics}'\n{MATCHING_SETTINGS}standby:\n  mode: hot\n"),
        );
        path
    };
    let active_address = free_loopback_address();
    let active_metrics = free_loopback_address();
    let mut active = ServeProcess::spawn(
        &serve_config("active", active_address, active_metrics),
        active_address,
        directory.path().join("active.log"),
    );
    let channel = active.wait_until_serving().await;
    let standby_address = free_loopback_address();
    let standby_metrics = free_loopback_address();
    let mut standby = ServeProcess::spawn(
        &serve_config("standby", standby_address, standby_metrics),
        standby_address,
        directory.path().join("standby.log"),
    );
    wait_for_hot_standby(&mut standby).await;
    let flow_ids = start_queued_flows(&channel, active_metrics, "failover", true).await;

    let killed = active.kill().await;
    assert!(!killed.success(), "SIGKILL stops the active");
    let channel = tokio::time::timeout(TAKEOVER_BUDGET, standby.wait_until_serving())
        .await
        .unwrap_or_else(|_| {
            panic!(
                "the hot standby did not serve within {TAKEOVER_BUDGET:?} of the kill:\n{}",
                standby.log()
            )
        });
    assert!(
        standby.log().contains(HOT_TAKEOVER_LOG_LINE),
        "the takeover served the hot standby's store:\n{}",
        standby.log()
    );
    wait_for_queue_entries(standby_metrics, flow_ids.len()).await;
    complete_queued_flows(&channel, standby_metrics, &flow_ids).await;
    let stopped = standby.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(stopped.code(), Some(0), "{}", standby.log());
    primary.shutdown().await;
}

/// Writes a `dev` overlay with strict durability, a metrics listener, and short matching waits.
fn write_dev_config(
    directory: &Path,
    name: &str,
    metrics: SocketAddr,
    matching_settings: &str,
) -> std::path::PathBuf {
    let path = directory.join(format!("{name}.yaml"));
    // Relaxed object writes do not survive SIGKILL.
    fs::write(
        &path,
        format!(
            "cluster:\n  partitionCount: 16\ndev:\n  durabilityMode: strict\nmetrics:\n  listen: '{metrics}'\n{matching_settings}"
        ),
    )
    .expect("the dev configuration is written");
    path
}

fn append_to_config(path: &Path, settings: &str) {
    let mut contents = fs::read_to_string(path).expect("the configuration is readable");
    contents.push_str(settings);
    fs::write(path, contents).expect("the configuration is written");
}

async fn wait_for_hot_standby(standby: &mut ServeProcess) {
    let channel = Channel::from_shared(format!("http://{}", standby.address()))
        .expect("the standby address is a valid URI")
        .connect_lazy();
    let mut health = HealthClient::new(channel);
    let deadline = tokio::time::Instant::now() + HOT_STANDBY_BUDGET;
    loop {
        assert!(
            !standby.has_exited(),
            "the standby exited:\n{}",
            standby.log()
        );
        let check = health.check(HealthCheckRequest {
            service: HOT_STANDBY_HEALTH_SERVICE_NAME.to_owned(),
        });
        if let Ok(Ok(response)) = tokio::time::timeout(POLL_INTERVAL * 10, check).await
            && response.get_ref().status == ServingStatus::Serving as i32
        {
            return;
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "the standby was not a hot standby within {HOT_STANDBY_BUDGET:?}:\n{}",
            standby.log()
        );
        tokio::time::sleep(POLL_INTERVAL).await;
    }
}

/// Starts every Step kind without a Worker and checks each waits on attempt 1 without failing.
///
/// Returns the started Flow IDs.
async fn start_queued_flows(
    channel: &Channel,
    metrics: SocketAddr,
    prefix: &str,
    is_queued: bool,
) -> Vec<String> {
    let mut client = FlowServiceClient::new(channel.clone());
    let mut flow_ids = Vec::new();
    for kind in STEP_KINDS {
        for index in 0..FLOWS_PER_KIND {
            let flow_id = format!("{prefix}-{kind:?}-{index}");
            client
                .start_flow(queued_flow_request(&flow_id, kind))
                .await
                .unwrap_or_else(|status| panic!("{flow_id} starts: {status}"));
            flow_ids.push(flow_id);
        }
    }
    let expected_entries = if is_queued { flow_ids.len() } else { 0 };
    wait_for_queue_entries(metrics, expected_entries).await;
    for flow_id in &flow_ids {
        let state = client
            .get_flow_state(GetFlowStateRequest {
                flow_id: flow_id.clone(),
                ..GetFlowStateRequest::default()
            })
            .await
            .unwrap_or_else(|status| panic!("GetFlowState of {flow_id}: {status}"))
            .into_inner();
        assert_eq!(
            state.active_step_executions.len(),
            1,
            "{flow_id}: {state:?}"
        );
        let step = &state.active_step_executions[0];
        assert!(
            step.last_failure_info.is_none(),
            "{flow_id}: queueing is not a failure: {step:?}"
        );
        let invocation = step
            .execute_invocation
            .as_ref()
            .unwrap_or_else(|| panic!("{flow_id}: Execute is open: {step:?}"));
        assert_eq!(
            invocation.phase,
            StepMethodInvocationPhase::Pending as i32,
            "{flow_id}"
        );
        assert_eq!(
            invocation.pending_reason,
            StepMethodPendingReason::WaitingForWorker as i32,
            "{flow_id}"
        );
        assert_eq!(invocation.attempt, 1, "{flow_id}");
        assert!(invocation.pending_since_time.is_some(), "{flow_id}");
    }
    wait_for_queue_entries(metrics, expected_entries).await;
    flow_ids
}

fn queued_flow_request(flow_id: &str, kind: QueuedStepKind) -> StartFlowRequest {
    let (durability, lock_keys) = match kind {
        QueuedStepKind::Sync => (StepDurability::Sync, Vec::new()),
        QueuedStepKind::Async => (StepDurability::Async, Vec::new()),
        QueuedStepKind::SyncWithExecuteLock => (StepDurability::Sync, vec![LOCK_KEY.to_owned()]),
    };
    StartFlowRequest {
        flow_id: flow_id.to_owned(),
        flow_type: FLOW_TYPE.to_owned(),
        start_step_type: STEP_TYPE.to_owned(),
        step_input: Some(string_value("queued")),
        step_options: Some(StepOptions {
            skip_wait_for: true,
            execute_durability_override: durability as i32,
            execute_lock_attribute_keys: lock_keys,
            ..StepOptions::default()
        }),
        flow_start_options: Some(FlowStartOptions {
            flow_config_override: Some(FlowConfig {
                worker_target: Some(WorkerTarget {
                    is_pull_mode: true,
                    ..WorkerTarget::default()
                }),
                ..FlowConfig::default()
            }),
            ..FlowStartOptions::default()
        }),
        request_id: format!("request-{flow_id}"),
        ..StartFlowRequest::default()
    }
}

/// Registers a Worker and checks every Flow completes with each Step delivered once, on attempt 1.
async fn complete_queued_flows(channel: &Channel, metrics: SocketAddr, flow_ids: &[String]) {
    let deliveries: Arc<Mutex<Vec<(String, String, i32)>>> = Arc::default();
    let recorded = Arc::clone(&deliveries);
    let worker = PullWorker::start(
        WorkerTaskServiceClient::new(channel.clone()),
        FLOW_TYPE,
        Box::new(move |request| {
            let context = request.context.clone().unwrap_or_default();
            recorded.lock().expect("lock").push((
                context.flow_id,
                context.step_execution_id,
                context.attempt,
            ));
            InvokeExecuteMethodResponse {
                step_decision: Some(StepDecision {
                    close_decision: Some(CloseDecision {
                        close_decision_type: CloseDecisionType::ForceComplete.into(),
                        close_input: Some(string_value("done")),
                        ..CloseDecision::default()
                    }),
                    ..StepDecision::default()
                }),
                ..InvokeExecuteMethodResponse::default()
            }
        }),
    )
    .await;
    let mut client = FlowServiceClient::new(channel.clone());
    for flow_id in flow_ids {
        let result = client
            .wait_for_flow(WaitForFlowRequest {
                flow_id: flow_id.clone(),
                wait_time_seconds: COMPLETION_WAIT_SECONDS,
                ..WaitForFlowRequest::default()
            })
            .await
            .unwrap_or_else(|status| panic!("WaitForFlow of {flow_id}: {status}"))
            .into_inner();
        assert_eq!(
            result.flow_status,
            FlowStatus::Completed as i32,
            "{flow_id}: {result:?}"
        );
    }
    wait_for_queue_entries(metrics, 0).await;
    worker.stop().await;
    let mut attempts_by_step: BTreeMap<(String, String), Vec<i32>> = BTreeMap::new();
    for (flow_id, step_execution_id, attempt) in deliveries.lock().expect("lock").drain(..) {
        attempts_by_step
            .entry((flow_id, step_execution_id))
            .or_default()
            .push(attempt);
    }
    assert_eq!(
        attempts_by_step.len(),
        flow_ids.len(),
        "every Flow's Step reached the Worker: {attempts_by_step:?}"
    );
    for (step, attempts) in &attempts_by_step {
        assert_eq!(
            attempts,
            &vec![1],
            "{step:?} reached the Worker once, on attempt 1"
        );
    }
}

/// Waits until the node's DispatchQueue row count is `count`.
async fn wait_for_queue_entries(metrics: SocketAddr, count: usize) {
    let deadline = tokio::time::Instant::now() + QUEUED_BUDGET;
    let mut last_seen = None;
    loop {
        if let Ok(body) = try_scrape(metrics, "/metrics").await {
            last_seen = queue_entries(&body);
            if last_seen == Some(count) {
                return;
            }
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "the queue did not hold {count} rows within {QUEUED_BUDGET:?}; last saw {last_seen:?}"
        );
        tokio::time::sleep(POLL_INTERVAL).await;
    }
}

fn queue_entries(body: &str) -> Option<usize> {
    body.lines()
        .find_map(|line| line.strip_prefix("dexzd_step_task_queue_entries "))
        .and_then(|value| value.trim().parse::<usize>().ok())
}
