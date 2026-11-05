// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Step attempt hand-out to reserved Worker slots, through matching and the engine together.

use std::time::Duration;

use dexzd_protocol::dex::{
    FlowStatus, InvokeExecuteMethodResponse, PollWorkerTask, PollWorkerTaskRejectReason,
    RetryPolicy, StepOptions, WaitForFlowRequest,
};

use crate::harness::{
    DevServer, ScriptedWorker, execute_request, execute_result, force_complete, start_pull_flow,
};
use crate::string_value;

const FLOW_TYPE: &str = "ReservedSlotFlow";

/// Which of two Workers received a task.
enum Receiver {
    First,
    Second,
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn an_unsupported_step_runs_on_another_worker_as_the_same_attempt() {
    let server = DevServer::start().await;
    let mut first =
        ScriptedWorker::start_reserved_slot(server.worker_client(), FLOW_TYPE, "a").await;
    let mut second =
        ScriptedWorker::start_reserved_slot(server.worker_client(), FLOW_TYPE, "b").await;
    let flow_id = "reserved-unsupported";
    start_flow(&server, flow_id).await;

    let (receiver, declined) = next_task_of_either(&mut first, &mut second).await;
    assert_eq!(attempt(&declined), 1);
    let (decliner, mut runner) = match receiver {
        Receiver::First => (first, second),
        Receiver::Second => (second, first),
    };
    decliner.reject(
        declined.task_id.clone(),
        PollWorkerTaskRejectReason::UnknownStep,
    );
    let task = runner.next_task().await;
    assert_eq!(attempt(&task), 1, "a declined attempt keeps its number");
    assert_ne!(
        task.task_id, declined.task_id,
        "each hand-out has its own task ID"
    );
    runner.reply(task.task_id, complete_flow_output());
    assert_flow_completed(&server, flow_id).await;
    decliner.stop().await;
    runner.stop().await;
    server.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_clean_half_close_before_output_returns_the_attempt_to_another_worker() {
    let server = DevServer::start().await;
    let mut first =
        ScriptedWorker::start_reserved_slot(server.worker_client(), FLOW_TYPE, "a").await;
    let mut second =
        ScriptedWorker::start_reserved_slot(server.worker_client(), FLOW_TYPE, "b").await;
    let flow_id = "reserved-half-close";
    start_flow(&server, flow_id).await;

    let (receiver, abandoned) = next_task_of_either(&mut first, &mut second).await;
    assert_eq!(attempt(&abandoned), 1);
    let (closing, mut runner) = match receiver {
        Receiver::First => (first, second),
        Receiver::Second => (second, first),
    };
    closing.half_close().await;
    let task = runner.next_task().await;
    assert_eq!(
        attempt(&task),
        1,
        "a task without output when its stream half-closed was never run"
    );
    runner.reply(task.task_id, complete_flow_output());
    assert_flow_completed(&server, flow_id).await;
    runner.stop().await;
    server.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_reset_right_after_a_half_close_fails_the_attempt_and_retries_it() {
    let server = DevServer::start().await;
    let mut first =
        ScriptedWorker::start_reserved_slot(server.worker_client(), FLOW_TYPE, "a").await;
    let mut second =
        ScriptedWorker::start_reserved_slot(server.worker_client(), FLOW_TYPE, "b").await;
    let flow_id = "reserved-half-close-reset";
    start_pull_flow(
        &mut server.flow_client(),
        flow_id,
        FLOW_TYPE,
        "ShipStep",
        string_value("parcel"),
        StepOptions {
            skip_wait_for: true,
            execute_retry_policy: Some(RetryPolicy {
                initial_interval_seconds: 1,
                maximum_attempts: 2,
                ..RetryPolicy::default()
            }),
            ..StepOptions::default()
        },
    )
    .await;

    let (receiver, abandoned) = next_task_of_either(&mut first, &mut second).await;
    assert_eq!(attempt(&abandoned), 1);
    let (cancelling, mut runner) = match receiver {
        Receiver::First => (first, second),
        Receiver::Second => (second, first),
    };
    cancelling.half_close_then_reset().await;
    let task = runner.next_task().await;
    assert_eq!(
        attempt(&task),
        2,
        "a reset after hand-out means the Worker may have run the task"
    );
    runner.reply(task.task_id, complete_flow_output());
    assert_flow_completed(&server, flow_id).await;
    runner.stop().await;
    server.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_reserved_slot_gets_its_next_task_only_after_the_final_output() {
    let server = DevServer::start().await;
    let mut worker =
        ScriptedWorker::start_reserved_slot(server.worker_client(), FLOW_TYPE, "only").await;
    let flow_ids = ["reserved-serial-1", "reserved-serial-2"];
    for flow_id in flow_ids {
        start_flow(&server, flow_id).await;
    }

    let first_task = worker.next_task().await;
    assert!(
        worker
            .next_task_within(Duration::from_millis(500))
            .await
            .is_none(),
        "the slot is busy until the first task's final output"
    );
    let first_flow_id = execute_request(&first_task)
        .context
        .as_ref()
        .expect("the task has a context")
        .flow_id
        .clone();
    worker.reply(first_task.task_id, complete_flow_output());
    let second_task = worker.next_task().await;
    let second_flow_id = execute_request(&second_task)
        .context
        .as_ref()
        .expect("the task has a context")
        .flow_id
        .clone();
    assert_ne!(first_flow_id, second_flow_id);
    worker.reply(second_task.task_id, complete_flow_output());
    for flow_id in flow_ids {
        assert_flow_completed(&server, flow_id).await;
    }
    worker.stop().await;
    server.shutdown().await;
}

async fn start_flow(server: &DevServer, flow_id: &str) {
    start_pull_flow(
        &mut server.flow_client(),
        flow_id,
        FLOW_TYPE,
        "ShipStep",
        string_value("parcel"),
        StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        },
    )
    .await;
}

/// Returns the first task that either Worker receives, and which Worker received it.
async fn next_task_of_either(
    first: &mut ScriptedWorker,
    second: &mut ScriptedWorker,
) -> (Receiver, PollWorkerTask) {
    tokio::select! {
        task = first.next_task() => (Receiver::First, task),
        task = second.next_task() => (Receiver::Second, task),
    }
}

fn attempt(task: &PollWorkerTask) -> i32 {
    execute_request(task)
        .context
        .as_ref()
        .expect("the task has a context")
        .attempt
}

fn complete_flow_output() -> dexzd_protocol::dex::poll_worker_task_frame::Output {
    execute_result(InvokeExecuteMethodResponse {
        step_decision: Some(force_complete(string_value("shipped"))),
        ..InvokeExecuteMethodResponse::default()
    })
}

async fn assert_flow_completed(server: &DevServer, flow_id: &str) {
    let result = server
        .flow_client()
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 10,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect("WaitForFlow succeeds")
        .into_inner();
    assert_eq!(result.flow_status, FlowStatus::Completed as i32);
}
