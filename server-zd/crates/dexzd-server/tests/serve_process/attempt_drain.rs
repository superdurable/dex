// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::net::SocketAddr;
use std::path::PathBuf;
use std::time::Duration;

use dexzd_protocol::dex::admin_service_client::AdminServiceClient;
use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::worker_task_service_client::WorkerTaskServiceClient;
use dexzd_protocol::dex::{
    AttributeWrite, CloseDecision, CloseDecisionType, DescribeActiveNodeRequest,
    ExecuteMethodFailurePolicy, FlowConfig, FlowStartOptions, FlowStatus, GetFlowStateRequest,
    GetHistoryEventsRequest, InvokeExecuteMethodOutput, InvokeExecuteMethodRequest,
    InvokeExecuteMethodResponse, PollWorkerSlotKind, PollWorkerTask, PollWorkerTaskFrame,
    PollWorkerTaskRegister, PollWorkerTaskRequest, PollWorkerTaskResponse, RetryPolicy,
    SetAttributesRequest, StartFlowRequest, StepAttemptProgress, StepDecision,
    StepDownActiveNodeRequest, StepDurability, StepMethodHeartbeat, StepMovement, StepOptions,
    WaitForFlowRequest, WorkerErrorResponse, WorkerTarget, flow_history_event,
    invoke_execute_method_output, poll_worker_task, poll_worker_task_frame,
    poll_worker_task_request, poll_worker_task_response,
    step_down_active_node_response::LeaseHolderAfterStepDown,
};
use s3mock_server::SignatureVerification;
use tempfile::TempDir;
use tokio::sync::mpsc;
use tokio::time::Instant;
use tokio_stream::wrappers::UnboundedReceiverStream;
use tonic::Streaming;
use tonic::transport::Channel;

use crate::harness::{S3Emulator, ServeConfig, ServeProcess, free_loopback_address};
use crate::{PRIMARY_BUCKET, string_value};

const FLOW_TYPE: &str = "AttemptDrainFlow";
const FLOW_ID: &str = "attempt-drain";
/// A second Flow whose Step waits in the durable task queue.
const QUEUED_FLOW_ID: &str = "attempt-drain-queued";
const FIRST_STEP: &str = "RunningStep";
const NEXT_STEP: &str = "NextStep";
const RECOVERY_STEP: &str = "RecoverStep";
const BUDGET: Duration = Duration::from_secs(45);
/// The longest s3mock delay; clearing the fault rules releases a held write earlier.
const HELD_WRITE_MILLISECONDS: u64 = 600_000;

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn sigterm_drains_ten_second_sync_attempt_without_retry() {
    survives_planned_stop(StepDurability::Sync, false).await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn sigterm_drains_ten_second_async_attempt_without_retry() {
    survives_planned_stop(StepDurability::Async, false).await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn step_down_drains_ten_second_sync_attempt_before_hot_takeover() {
    survives_planned_stop(StepDurability::Sync, true).await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn step_down_drains_ten_second_async_attempt_before_hot_takeover() {
    survives_planned_stop(StepDurability::Async, true).await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn expired_sync_drain_interrupts_and_retries_with_not_completed() {
    interrupted_after_timeout(StepDurability::Sync, "2s").await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn expired_async_drain_interrupts_and_retries_with_not_completed() {
    interrupted_after_timeout(StepDurability::Async, "2s").await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn zero_sync_drain_preserves_immediate_interruption() {
    interrupted_after_timeout(StepDurability::Sync, "0s").await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn zero_async_drain_preserves_immediate_interruption() {
    interrupted_after_timeout(StepDurability::Async, "0s").await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn drain_keeps_worker_stream_open_until_the_result_commits() {
    let fixture = DrainFixture::start().await;
    let mut active = fixture.spawn("active", None, false);
    let channel = active.wait_until_serving().await;
    let mut worker = DrainWorker::start(channel.clone()).await;
    start_flow(channel, StepDurability::Async).await;
    let task = worker.next_execute().await;
    fixture
        .emulator
        .set_fault_rules(&delayed_log_writes(2000))
        .await;
    let (status, ()) = tokio::join!(
        active.terminate(BUDGET),
        worker.complete_with_delayed_commit(&task)
    );
    assert_eq!(status.code(), Some(0), "{}", active.log());
    assert!(
        active
            .log()
            .contains("attempts_finished=1 attempts_running=0"),
        "{}",
        active.log()
    );
    fixture.emulator.clear_fault_rules().await;
    fixture.emulator.shutdown().await;
}

/// A Step queued at SIGTERM stays queued when a Worker slot frees during the drain; the
/// successor hands it out once.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn sigterm_leaves_a_queued_step_for_the_successor() {
    let fixture = DrainFixture::start().await;
    let metrics_address = free_loopback_address();
    let mut active = fixture.spawn_with_metrics("active", metrics_address);
    let channel = active.wait_until_serving().await;
    let mut worker = DrainWorker::start(channel.clone()).await;
    start_flow(channel.clone(), StepDurability::Sync).await;
    let running = worker.next_execute().await;
    // The Worker's only slot is busy, so this Step goes to the durable task queue.
    start_flow_with_id(channel, StepDurability::Sync, QUEUED_FLOW_ID).await;
    wait_for_metric_sample(metrics_address, "dexzd_step_task_queue_entries 1").await;

    let (status, ()) = tokio::join!(
        active.terminate(BUDGET),
        worker.complete_after(&running, Duration::from_secs(2), None),
    );
    assert_eq!(status.code(), Some(0), "{}", active.log());
    let active_log = active.log();
    assert!(active_log.contains("attempts_waited=1"), "{active_log}");
    assert!(active_log.contains("outcome=completed"), "{active_log}");
    // The freed slot gets no Claim while matching closes.
    worker.assert_no_task_until_closed().await;

    let mut successor = fixture.spawn("replacement", None, false);
    let successor_channel = successor.wait_until_serving().await;
    let mut successor_worker = DrainWorker::start(successor_channel.clone()).await;
    let mut queued_attempts = Vec::new();
    for _ in 0..2 {
        let task = successor_worker.next_execute().await;
        let request = execute_request(&task);
        let context = request.context.as_ref().expect("Execute carries context");
        if context.flow_id == QUEUED_FLOW_ID {
            assert_eq!(request.step_type, FIRST_STEP);
            queued_attempts.push(context.attempt);
        } else {
            assert_eq!(request.step_type, NEXT_STEP);
        }
        successor_worker.complete(&task, close_flow());
    }
    assert_eq!(
        queued_attempts,
        vec![1],
        "the queued Step runs once, on attempt 1"
    );
    assert_flow_completed(successor_channel.clone(), FLOW_ID).await;
    assert_flow_completed(successor_channel, QUEUED_FLOW_ID).await;
    let stopped = successor.terminate(BUDGET).await;
    assert_eq!(stopped.code(), Some(0), "{}", successor.log());
    fixture.emulator.shutdown().await;
}

/// Holds a SYNC start's commit until drain admission closed: the frame still goes out.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn sigterm_sends_the_task_of_a_sync_start_that_commits_during_the_drain() {
    let fixture = DrainFixture::start().await;
    let metrics_address = free_loopback_address();
    let mut active = fixture.spawn_with_metrics("active", metrics_address);
    let channel = active.wait_until_serving().await;
    start_flow(channel.clone(), StepDurability::Sync).await;
    // With no Worker, the hand-out waits in the matching queue.
    wait_for_queued_tasks(metrics_address, 1).await;
    fixture
        .emulator
        .set_fault_rules(&delayed_log_writes(HELD_WRITE_MILLISECONDS))
        .await;
    let mut worker = DrainWorker::start(channel).await;
    // The hand-out reserved the stream; its start cannot commit while log writes are held.
    wait_for_queued_tasks(metrics_address, 0).await;
    active.send_sigterm(BUDGET).await;
    // Admission is closed. The hand-out may register its attempt before or after this line.
    active
        .wait_for_log_line("Step attempt drain started", BUDGET)
        .await;
    let drain_started = active
        .log()
        .lines()
        .find(|line| line.contains("Step attempt drain started"))
        .expect("the drain logged its start")
        .to_owned();
    assert!(
        drain_started.ends_with("outstanding_handouts=1"),
        "{drain_started}"
    );
    fixture.emulator.clear_fault_rules().await;
    let task = worker.next_execute().await;
    let request = execute_request(&task);
    assert_eq!(request.step_type, FIRST_STEP);
    let step_execution_id = request
        .context
        .as_ref()
        .expect("Execute carries context")
        .step_execution_id
        .clone();
    assert_eq!(
        request
            .context
            .as_ref()
            .expect("Execute carries context")
            .attempt,
        1
    );
    worker.complete(&task, close_flow());
    assert_eq!(
        active.wait_for_exit(BUDGET).await.code(),
        Some(0),
        "{}",
        active.log()
    );
    let active_log = active.log();
    assert!(active_log.contains("outcome=completed"), "{active_log}");
    assert!(
        active_log.contains("attempts_waited=1 attempts_finished=1 attempts_running=0"),
        "{active_log}"
    );
    let mut successor = fixture.spawn("replacement", None, false);
    let successor_channel = successor.wait_until_serving().await;
    assert_completed(successor_channel.clone()).await;
    assert_first_step_completed_on_first_attempt(successor_channel, &step_execution_id).await;
    assert_eq!(
        successor.terminate(BUDGET).await.code(),
        Some(0),
        "{}",
        successor.log()
    );
    fixture.emulator.shutdown().await;
}

/// Holds the return's commit after a clean half-close in the drain: the attempt is not consumed.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn half_close_during_the_drain_returns_the_attempt_before_the_lease_release() {
    let fixture = DrainFixture::start().await;
    let mut active = fixture.spawn("active", None, false);
    let channel = active.wait_until_serving().await;
    let mut worker = DrainWorker::start_reserved_slot(channel.clone()).await;
    start_flow(channel, StepDurability::Sync).await;
    let task = worker.next_execute().await;
    let request = execute_request(&task);
    let step_execution_id = request
        .context
        .as_ref()
        .expect("Execute carries context")
        .step_execution_id
        .clone();
    assert_eq!(
        request
            .context
            .as_ref()
            .expect("Execute carries context")
            .attempt,
        1
    );
    // Longer than the teardown, so a return that the drain did not wait for is lost.
    fixture
        .emulator
        .set_fault_rules(&delayed_log_writes(8000))
        .await;
    active.send_sigterm(BUDGET).await;
    active
        .wait_for_log_line(
            "Step attempt drain started attempts_waited=1 outstanding_handouts=1",
            BUDGET,
        )
        .await;
    worker.half_close().await;
    assert_eq!(
        active.wait_for_exit(BUDGET).await.code(),
        Some(0),
        "{}",
        active.log()
    );
    let active_log = active.log();
    assert!(active_log.contains("outcome=completed"), "{active_log}");
    fixture.emulator.clear_fault_rules().await;
    let mut successor = fixture.spawn("replacement", None, false);
    let successor_channel = successor.wait_until_serving().await;
    let mut successor_worker = DrainWorker::start(successor_channel.clone()).await;
    let redispatched = successor_worker.next_execute().await;
    let context = execute_request(&redispatched)
        .context
        .as_ref()
        .expect("Execute carries context");
    assert_eq!(context.step_execution_id, step_execution_id);
    assert_eq!(
        context.attempt, 1,
        "a task without output when its stream half-closed was never run"
    );
    successor_worker.complete(&redispatched, close_flow());
    assert_completed(successor_channel.clone()).await;
    assert_first_step_completed_on_first_attempt(successor_channel, &step_execution_id).await;
    assert_eq!(
        successor.terminate(BUDGET).await.code(),
        Some(0),
        "{}",
        successor.log()
    );
    fixture.emulator.shutdown().await;
}

async fn survives_planned_stop(durability: StepDurability, is_step_down: bool) {
    let fixture = DrainFixture::start().await;
    let mut active = fixture.spawn("active", None, false);
    let channel = active.wait_until_serving().await;
    let mut standby = if is_step_down {
        let successor = fixture.spawn("standby", None, true);
        wait_for_hot_standby(&successor).await;
        Some(successor)
    } else {
        None
    };
    let mut worker = DrainWorker::start(channel.clone()).await;
    start_flow(channel.clone(), durability).await;
    let task = worker.next_execute().await;
    let request = execute_request(&task);
    assert_eq!(request.step_type, FIRST_STEP);
    assert_eq!(
        request
            .context
            .as_ref()
            .expect("Execute carries context")
            .attempt,
        1
    );
    let started = Instant::now();
    if let Some(successor) = &mut standby {
        let standby_address = successor.address();
        let mut admin = AdminServiceClient::new(channel.clone());
        let mut step_down_request = tonic::Request::new(StepDownActiveNodeRequest::default());
        step_down_request.set_timeout(BUDGET);
        let (answer, ()) = tokio::join!(
            admin.step_down_active_node(step_down_request),
            worker.complete_after(&task, Duration::from_secs(10), Some(standby_address)),
        );
        let answer = answer.expect("step-down succeeds").into_inner();
        assert!(matches!(
            answer.lease_holder_after_step_down,
            Some(LeaseHolderAfterStepDown::TakenOverByAnotherProcess(_))
        ));
        successor.wait_until_serving().await;
    } else {
        let (status, ()) = tokio::join!(
            active.terminate(BUDGET),
            worker.complete_after(&task, Duration::from_secs(10), None),
        );
        assert_eq!(status.code(), Some(0), "{}", active.log());
    }
    assert!(started.elapsed() >= Duration::from_secs(10));
    let active_log = active.log();
    assert!(active_log.contains("attempts_waited=1"), "{active_log}");
    assert!(
        active_log.contains("attempts_finished=1 attempts_running=0"),
        "{active_log}"
    );
    assert!(active_log.contains("outcome=completed"), "{active_log}");
    let mut successor = match standby {
        Some(successor) => successor,
        None => fixture.spawn("replacement", None, false),
    };
    let successor_channel = successor.wait_until_serving().await;
    let mut successor_worker = DrainWorker::start(successor_channel.clone()).await;
    let next = successor_worker.next_execute().await;
    assert_eq!(execute_request(&next).step_type, NEXT_STEP);
    assert_eq!(
        execute_request(&next)
            .context
            .as_ref()
            .expect("Execute carries context")
            .attempt,
        1
    );
    successor_worker.complete(&next, close_flow());
    assert_completed(successor_channel.clone()).await;
    let history = FlowServiceClient::new(successor_channel)
        .get_history_events(GetHistoryEventsRequest {
            flow_id: FLOW_ID.to_owned(),
            estimate_page_size: 100,
            ..Default::default()
        })
        .await
        .expect("history reads committed results")
        .into_inner();
    let completion = history
        .events
        .iter()
        .find_map(|event| match &event.payload {
            Some(flow_history_event::Payload::StepExecuteCompleted(completed))
                if completed
                    .context
                    .as_ref()
                    .expect("Execute carries context")
                    .step_type
                    == FIRST_STEP =>
            {
                Some(completed)
            }
            _ => None,
        })
        .expect("the drained Step completed in history");
    let context = completion
        .context
        .as_ref()
        .expect("Execute carries context");
    assert_eq!(context.final_attempt, 1);
    assert!(context.last_failure_info.is_none());
    assert_eq!(
        context.step_execution_id,
        request
            .context
            .as_ref()
            .expect("Execute carries context")
            .step_execution_id
    );
    assert_eq!(
        successor.terminate(BUDGET).await.code(),
        Some(0),
        "{}",
        successor.log()
    );
    if is_step_down {
        assert_eq!(
            active.terminate(BUDGET).await.code(),
            Some(0),
            "{}",
            active.log()
        );
    }
    fixture.emulator.shutdown().await;
}

async fn interrupted_after_timeout(durability: StepDurability, timeout: &str) {
    let fixture = DrainFixture::start().await;
    let mut active = fixture.spawn("active", Some(timeout), false);
    let channel = active.wait_until_serving().await;
    let mut worker = DrainWorker::start(channel.clone()).await;
    start_flow(channel.clone(), durability).await;
    let original = worker.next_execute().await;
    let original_context = execute_request(&original)
        .context
        .as_ref()
        .expect("Execute carries context");
    assert_eq!(original_context.attempt, 1);
    worker.heartbeat(&original);
    FlowServiceClient::new(channel)
        .set_attributes(SetAttributesRequest {
            flow_id: FLOW_ID.to_owned(),
            attributes: vec![AttributeWrite {
                key: "startCommitted".to_owned(),
                value: Some(string_value("yes")),
                ..Default::default()
            }],
            request_id: "commit-attempt-start-before-immediate-stop".to_owned(),
            ..Default::default()
        })
        .await
        .expect("a later committed command proves the ASYNC start is durable");
    let started = Instant::now();
    assert_eq!(
        active.terminate(BUDGET).await.code(),
        Some(0),
        "{}",
        active.log()
    );
    assert!(
        started.elapsed() < Duration::from_secs(10),
        "{}",
        active.log()
    );
    let active_log = active.log();
    if timeout == "2s" {
        assert!(started.elapsed() >= Duration::from_secs(2));
        assert!(active_log.contains("outcome=timed_out"), "{active_log}");
        assert!(
            active_log.contains("attempts_finished=0 attempts_running=1"),
            "{active_log}"
        );
    } else {
        assert!(
            active_log.contains("Step attempt drain disabled"),
            "{active_log}"
        );
        assert!(
            !active_log.contains("Step attempt drain ended"),
            "{active_log}"
        );
    }
    let mut replacement = fixture.spawn("replacement", None, false);
    let channel = replacement.wait_until_serving().await;
    let mut replacement_worker = DrainWorker::start(channel.clone()).await;
    let retried = replacement_worker.next_execute().await;
    let retry_context = execute_request(&retried)
        .context
        .as_ref()
        .expect("Execute carries context");
    assert_eq!(retry_context.attempt, 2);
    assert_eq!(
        retry_context.step_execution_id,
        original_context.step_execution_id
    );
    if timeout == "2s" {
        assert_eq!(
            retry_context.last_heartbeat_value,
            Some(string_value("draining"))
        );
    }
    let state = FlowServiceClient::new(channel.clone())
        .get_flow_state(GetFlowStateRequest {
            flow_id: FLOW_ID.to_owned(),
            ..Default::default()
        })
        .await
        .expect("read the committed interruption failure")
        .into_inner();
    let failure = state
        .active_step_executions
        .iter()
        .find(|step| step.step_execution_id == original_context.step_execution_id)
        .expect("the same Step is retrying")
        .last_failure_info
        .as_ref()
        .expect("takeover recorded the interruption");
    assert_eq!(failure.attempt, 1);
    assert!(
        failure
            .backend_error
            .contains("a server restart or failover interrupted the attempt"),
        "{failure:?}"
    );
    replacement_worker.output(
        &retried,
        poll_worker_task_frame::Output::WorkerError(WorkerErrorResponse {
            detail: "finish retry policy and enter recovery".to_owned(),
            error_type: "DrainTestFailure".to_owned(),
            ..Default::default()
        }),
    );
    let recovery = replacement_worker.next_execute().await;
    let recovery_request = execute_request(&recovery);
    assert_eq!(recovery_request.step_type, RECOVERY_STEP);
    assert_eq!(
        recovery_request
            .context
            .as_ref()
            .expect("the drain test fixture is valid and its operations succeed")
            .recovery_error
            .as_ref()
            .expect("the drain test fixture is valid and its operations succeed")
            .attempt_progress,
        StepAttemptProgress::NotCompleted as i32
    );
    replacement_worker.complete(&recovery, close_flow());
    assert_completed(channel).await;
    assert_eq!(
        replacement.terminate(BUDGET).await.code(),
        Some(0),
        "{}",
        replacement.log()
    );
    fixture.emulator.shutdown().await;
}

struct DrainFixture {
    emulator: S3Emulator,
    directory: TempDir,
}

impl DrainFixture {
    async fn start() -> Self {
        Self {
            emulator: S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await,
            directory: TempDir::new().expect("create a unique test directory"),
        }
    }

    fn spawn(&self, name: &str, timeout: Option<&str>, is_hot: bool) -> ServeProcess {
        let address = free_loopback_address();
        let config = self.write_config(name, timeout, is_hot, address);
        ServeProcess::spawn(
            &config,
            address,
            self.directory.path().join(format!("{name}.log")),
        )
    }

    fn spawn_with_metrics(&self, name: &str, metrics_address: SocketAddr) -> ServeProcess {
        let address = free_loopback_address();
        let config = self.write_config(name, None, false, address);
        let mut configuration = std::fs::read_to_string(&config)
            .expect("the drain test fixture is valid and its operations succeed");
        configuration.push_str(&format!("metrics:\n  listen: '{metrics_address}'\n"));
        std::fs::write(&config, configuration)
            .expect("the drain test fixture is valid and its operations succeed");
        ServeProcess::spawn(
            &config,
            address,
            self.directory.path().join(format!("{name}.log")),
        )
    }

    fn write_config(
        &self,
        name: &str,
        timeout: Option<&str>,
        is_hot: bool,
        address: std::net::SocketAddr,
    ) -> PathBuf {
        let path = self.directory.path().join(format!("{name}.yaml"));
        ServeConfig {
            primary_endpoint: &self.emulator.endpoint(),
            hard_delete_endpoint: None,
            listen: address,
            local_directory: &self.directory.path().join(format!("{name}-store")),
            lease_duration: "3s",
            fail_if_held: false,
            parade_db: None,
        }
        .write(&path);
        let mut configuration = std::fs::read_to_string(&path)
            .expect("the drain test fixture is valid and its operations succeed");
        if let Some(timeout) = timeout {
            configuration.push_str(&format!("lifecycle:\n  attemptDrainTimeout: {timeout}\n"));
        }
        if is_hot {
            configuration.push_str("standby:\n  mode: hot\n");
        }
        std::fs::write(&path, configuration)
            .expect("the drain test fixture is valid and its operations succeed");
        path
    }
}

struct DrainWorker {
    frames: mpsc::UnboundedSender<PollWorkerTaskRequest>,
    incoming: Streaming<PollWorkerTaskResponse>,
}

impl DrainWorker {
    async fn start(channel: Channel) -> Self {
        Self::register(channel, worker_registration()).await
    }

    /// Registers one reserved Step-and-RPC slot, whose clean half-close returns unrun tasks.
    async fn start_reserved_slot(channel: Channel) -> Self {
        Self::register(
            channel,
            PollWorkerTaskRegister {
                queue: String::new(),
                supports_reserved_slot_dispatch: true,
                slot_kind: PollWorkerSlotKind::StepAndRpc.into(),
                ..worker_registration()
            },
        )
        .await
    }

    async fn register(channel: Channel, registration: PollWorkerTaskRegister) -> Self {
        let (frames, receiver) = mpsc::unbounded_channel();
        frames
            .send(PollWorkerTaskRequest {
                frame: Some(poll_worker_task_request::Frame::Register(registration)),
            })
            .expect("the drain test fixture is valid and its operations succeed");
        let mut incoming = WorkerTaskServiceClient::new(channel)
            .poll_worker_task(UnboundedReceiverStream::new(receiver))
            .await
            .expect("the drain test fixture is valid and its operations succeed")
            .into_inner();
        assert!(matches!(
            incoming
                .message()
                .await
                .expect("the drain test fixture is valid and its operations succeed")
                .expect("the drain test fixture is valid and its operations succeed")
                .frame,
            Some(poll_worker_task_response::Frame::Registered(_))
        ));
        Self { frames, incoming }
    }

    async fn next_execute(&mut self) -> PollWorkerTask {
        tokio::time::timeout(BUDGET, async {
            loop {
                let response = self
                    .incoming
                    .message()
                    .await
                    .expect("the drain test fixture is valid and its operations succeed")
                    .expect("Worker stream stays open");
                match response.frame {
                    Some(poll_worker_task_response::Frame::Task(task)) => return task,
                    Some(poll_worker_task_response::Frame::Ping(_)) => self.pong(),
                    other => panic!("expected task or ping, got {other:?}"),
                }
            }
        })
        .await
        .expect("a Step reaches the Worker")
    }

    /// Reads the stream until the server ends it, asserting that no task arrives.
    async fn assert_no_task_until_closed(&mut self) {
        tokio::time::timeout(BUDGET, async {
            while let Ok(Some(response)) = self.incoming.message().await {
                assert!(
                    !matches!(
                        response.frame,
                        Some(poll_worker_task_response::Frame::Task(_))
                    ),
                    "a draining node hands out no queued task: {response:?}"
                );
            }
        })
        .await
        .expect("the stopped server ends the stream");
    }

    /// Half-closes the stream cleanly and waits until the server ends it.
    async fn half_close(self) {
        let Self {
            frames,
            mut incoming,
        } = self;
        drop(frames);
        tokio::time::timeout(BUDGET, async {
            while let Ok(Some(response)) = incoming.message().await {
                assert!(
                    matches!(
                        response.frame,
                        Some(poll_worker_task_response::Frame::Ping(_))
                    ),
                    "a half-closed stream gets no new task: {response:?}"
                );
            }
        })
        .await
        .expect("the server ends a half-closed stream");
    }

    async fn complete_after(
        &mut self,
        task: &PollWorkerTask,
        duration: Duration,
        standby: Option<SocketAddr>,
    ) {
        let deadline = Instant::now() + duration;
        let mut heartbeat = tokio::time::interval(Duration::from_millis(500));
        loop {
            tokio::select! {
                () = tokio::time::sleep_until(deadline) => break,
                _ = heartbeat.tick() => {
                    self.heartbeat(task);
                    if let Some(address) = standby {
                        let error = AdminServiceClient::new(direct_channel(address))
                            .describe_active_node(DescribeActiveNodeRequest::default()).await
                            .expect_err("the standby cannot take the lease before the running Step finishes");
                        assert_eq!(error.code(), tonic::Code::FailedPrecondition, "{error}");
                    }
                }
                response = self.incoming.message() => {
                    let response = response.expect("the drain test fixture is valid and its operations succeed").expect("a busy Worker stream stays open during drain");
                    assert!(matches!(response.frame, Some(poll_worker_task_response::Frame::Ping(_))),
                        "a running Worker must receive neither Reconnect nor new tasks: {response:?}");
                    self.pong();
                }
            }
        }
        self.complete(
            task,
            StepDecision {
                next_steps: vec![StepMovement {
                    step_type: NEXT_STEP.to_owned(),
                    step_options: Some(StepOptions {
                        skip_wait_for: true,
                        ..Default::default()
                    }),
                    ..Default::default()
                }],
                ..Default::default()
            },
        );
    }

    async fn complete_with_delayed_commit(&mut self, task: &PollWorkerTask) {
        self.complete(task, close_flow());
        assert!(
            tokio::time::timeout(Duration::from_secs(1), self.incoming.message())
                .await
                .is_err(),
            "Reconnect and stream closure must wait for the delayed completion commit"
        );
    }

    fn complete(&self, task: &PollWorkerTask, decision: StepDecision) {
        self.output(
            task,
            poll_worker_task_frame::Output::Execute(InvokeExecuteMethodOutput {
                output: Some(invoke_execute_method_output::Output::Result(
                    InvokeExecuteMethodResponse {
                        step_decision: Some(decision),
                        ..Default::default()
                    },
                )),
            }),
        );
    }

    fn heartbeat(&self, task: &PollWorkerTask) {
        self.output(
            task,
            poll_worker_task_frame::Output::Execute(InvokeExecuteMethodOutput {
                output: Some(invoke_execute_method_output::Output::Heartbeat(
                    StepMethodHeartbeat {
                        value: Some(string_value("draining")),
                    },
                )),
            }),
        );
    }

    fn output(&self, task: &PollWorkerTask, output: poll_worker_task_frame::Output) {
        self.frames
            .send(PollWorkerTaskRequest {
                frame: Some(poll_worker_task_request::Frame::TaskFrame(
                    PollWorkerTaskFrame {
                        task_id: task.task_id.clone(),
                        output: Some(output),
                    },
                )),
            })
            .expect("the drain test fixture is valid and its operations succeed");
    }

    fn pong(&self) {
        self.frames
            .send(PollWorkerTaskRequest {
                frame: Some(poll_worker_task_request::Frame::Pong(Default::default())),
            })
            .expect("the drain test fixture is valid and its operations succeed");
    }
}

fn worker_registration() -> PollWorkerTaskRegister {
    PollWorkerTaskRegister {
        queue: FLOW_TYPE.to_owned(),
        app_worker_id: "drain-worker".to_owned(),
        flow_types: vec![FLOW_TYPE.to_owned()],
        sdk_name: "dexzd-server-tests".to_owned(),
        sdk_version: "0".to_owned(),
        ..Default::default()
    }
}

async fn start_flow(channel: Channel, durability: StepDurability) {
    start_flow_with_id(channel, durability, FLOW_ID).await;
}

async fn start_flow_with_id(channel: Channel, durability: StepDurability, flow_id: &str) {
    FlowServiceClient::new(channel)
        .start_flow(StartFlowRequest {
            flow_id: flow_id.to_owned(),
            flow_type: FLOW_TYPE.to_owned(),
            start_step_type: FIRST_STEP.to_owned(),
            step_options: Some(StepOptions {
                skip_wait_for: true,
                execute_timeout_seconds: 60,
                heartbeat_timeout_seconds: 10,
                execute_durability_override: durability as i32,
                execute_retry_policy: Some(RetryPolicy {
                    initial_interval_seconds: 1,
                    maximum_attempts: 2,
                    ..Default::default()
                }),
                execute_failure_policy: ExecuteMethodFailurePolicy::ProceedToConfiguredStep as i32,
                execute_failure_proceed_step_type: RECOVERY_STEP.to_owned(),
                execute_failure_proceed_step_options: Some(Box::new(StepOptions {
                    skip_wait_for: true,
                    ..Default::default()
                })),
                ..Default::default()
            }),
            flow_start_options: Some(FlowStartOptions {
                flow_config_override: Some(FlowConfig {
                    worker_target: Some(WorkerTarget { is_pull_mode: true }),
                    ..Default::default()
                }),
                ..Default::default()
            }),
            request_id: format!("start-{flow_id}"),
            ..Default::default()
        })
        .await
        .expect("the drain test fixture is valid and its operations succeed");
}

fn execute_request(task: &PollWorkerTask) -> &InvokeExecuteMethodRequest {
    match task
        .request
        .as_ref()
        .expect("the drain test fixture is valid and its operations succeed")
    {
        poll_worker_task::Request::Execute(request) => request,
        other => panic!("expected Execute, got {other:?}"),
    }
}

fn close_flow() -> StepDecision {
    StepDecision {
        close_decision: Some(CloseDecision {
            close_decision_type: CloseDecisionType::ForceComplete as i32,
            ..Default::default()
        }),
        ..Default::default()
    }
}

async fn assert_completed(channel: Channel) {
    assert_flow_completed(channel, FLOW_ID).await;
}

async fn assert_flow_completed(channel: Channel, flow_id: &str) {
    let response = FlowServiceClient::new(channel)
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 30,
            ..Default::default()
        })
        .await
        .expect("the drain test fixture is valid and its operations succeed")
        .into_inner();
    assert_eq!(response.flow_status, FlowStatus::Completed as i32);
}

async fn wait_for_hot_standby(process: &ServeProcess) {
    let mut health =
        tonic_health::pb::health_client::HealthClient::new(direct_channel(process.address()));
    tokio::time::timeout(BUDGET, async {
        let mut polls = tokio::time::interval(Duration::from_millis(50));
        loop {
            polls.tick().await;
            if let Ok(response) = health
                .check(tonic_health::pb::HealthCheckRequest {
                    service: "dex.HotStandby".to_owned(),
                })
                .await
                && response.get_ref().status
                    == tonic_health::pb::health_check_response::ServingStatus::Serving as i32
            {
                return;
            }
        }
    })
    .await
    .unwrap_or_else(|_| panic!("standby never became hot: {}", process.log()));
}

fn direct_channel(address: std::net::SocketAddr) -> Channel {
    Channel::from_shared(format!("http://{address}"))
        .expect("the drain test fixture is valid and its operations succeed")
        .connect_lazy()
}

/// Waits until the matching queue gauge reports `expected` waiting tasks.
async fn wait_for_queued_tasks(metrics_address: SocketAddr, expected: u64) {
    let sample = format!("dexzd_matching_queued_tasks {expected}");
    wait_for_metric_sample(metrics_address, &sample).await;
}

async fn wait_for_metric_sample(metrics_address: SocketAddr, sample: &str) {
    tokio::time::timeout(BUDGET, async {
        let mut polls = tokio::time::interval(Duration::from_millis(20));
        loop {
            polls.tick().await;
            if let Ok(body) = crate::metrics::try_scrape(metrics_address, "/metrics").await
                && body.lines().any(|line| line.trim_end() == sample)
            {
                return;
            }
        }
    })
    .await
    .unwrap_or_else(|_| panic!("the metrics never reported {sample}"));
}

/// Returns s3mock fault rules that delay every log batch write by `milliseconds`.
fn delayed_log_writes(milliseconds: u64) -> String {
    format!(
        r#"[{{"operation":"PutObject","key_prefix":"{}/log/","action":{{"type":"delay","milliseconds":{milliseconds}}}}}]"#,
        crate::PRIMARY_PREFIX,
    )
}

/// Asserts that the first Step completed on attempt 1 without a recorded failure.
async fn assert_first_step_completed_on_first_attempt(channel: Channel, step_execution_id: &str) {
    let history = FlowServiceClient::new(channel)
        .get_history_events(GetHistoryEventsRequest {
            flow_id: FLOW_ID.to_owned(),
            estimate_page_size: 100,
            ..Default::default()
        })
        .await
        .expect("history reads committed results")
        .into_inner();
    let context = history
        .events
        .iter()
        .find_map(|event| match &event.payload {
            Some(flow_history_event::Payload::StepExecuteCompleted(completed)) => {
                completed.context.as_ref()
            }
            _ => None,
        })
        .expect("the first Step completed in history");
    assert_eq!(context.step_type, FIRST_STEP);
    assert_eq!(context.step_execution_id, step_execution_id);
    assert_eq!(context.final_attempt, 1);
    assert!(context.last_failure_info.is_none(), "{context:?}");
}
