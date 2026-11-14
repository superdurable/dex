// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Queued Steps of a named task queue reach only its Workers, also after a restart.

use std::net::SocketAddr;
use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{GetFlowStateRequest, PollWorkerTask, StepMethodPendingReason};
use tonic::transport::Channel;

use crate::harness::{DevServer, ScriptedWorker, execute_request, free_loopback_address};
use crate::named_task_queues::{assert_completed, complete_output, start_flow};
use crate::queued_state::wait_for_queue_entries;

const FLOW_TYPE: &str = "BacklogQueueFlow";
const QUEUE_A: &str = "queue-a";
const NAMED_FLOW_ID: &str = "queued-named";
const DEFAULT_FLOW_ID: &str = "queued-default";
/// How long a test waits to show that a Worker receives nothing.
const QUIET_WAIT: Duration = Duration::from_secs(1);

/// Without a connected Worker, both attempts go to the durable queue. A default Worker and a
/// Worker of another task queue take only their own rows; the named row waits for its Worker.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn queued_steps_of_a_named_task_queue_reach_only_its_workers() {
    let metrics = free_loopback_address();
    let server = DevServer::start_with_config_overrides(Some(&queued_settings(metrics))).await;
    let mut flow_client = server.flow_client();
    start_queued_flows(&mut flow_client, metrics).await;

    let mut default_worker =
        ScriptedWorker::start_reserved_slot(server.worker_client(), FLOW_TYPE, "default-worker")
            .await;
    let mut other_queue_worker = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        FLOW_TYPE,
        "queue-b-worker",
        "queue-b",
    )
    .await;
    let default_task = default_worker.next_task().await;
    assert_eq!(flow_id_of(&default_task), DEFAULT_FLOW_ID);
    default_worker.reply(default_task.task_id, complete_output());
    assert_completed(&mut flow_client, DEFAULT_FLOW_ID).await;
    assert!(default_worker.next_task_within(QUIET_WAIT).await.is_none());
    assert!(
        other_queue_worker
            .next_task_within(QUIET_WAIT)
            .await
            .is_none()
    );
    wait_for_queue_entries(metrics, 1).await;
    assert_eq!(
        execute_pending_reason(&mut flow_client, NAMED_FLOW_ID).await,
        StepMethodPendingReason::WaitingForWorker as i32,
        "Workers of other task queues never count as Workers of the named task queue"
    );

    let mut queue_worker = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        FLOW_TYPE,
        "queue-a-worker",
        QUEUE_A,
    )
    .await;
    let named_task = queue_worker.next_task().await;
    assert_eq!(flow_id_of(&named_task), NAMED_FLOW_ID);
    queue_worker.reply(named_task.task_id, complete_output());
    assert_completed(&mut flow_client, NAMED_FLOW_ID).await;
    wait_for_queue_entries(metrics, 0).await;
    default_worker.stop().await;
    other_queue_worker.stop().await;
    queue_worker.stop().await;
    server.shutdown().await;
}

/// A restart rediscovers the queued rows of the default and the named task queue, and each
/// runs on attempt 1 on a Worker of its task queue.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_restart_rediscovers_queued_steps_of_both_task_queue_kinds() {
    let metrics = free_loopback_address();
    let server = DevServer::start_with_config_overrides(Some(&queued_settings(metrics))).await;
    let mut flow_client = server.flow_client();
    start_queued_flows(&mut flow_client, metrics).await;

    let server = server.restart().await;
    let mut flow_client = server.flow_client();
    // The restarted node counts the rows of both index kinds again.
    wait_for_queue_entries(metrics, 2).await;
    let mut queue_worker = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        FLOW_TYPE,
        "queue-a-worker",
        QUEUE_A,
    )
    .await;
    let named_task = queue_worker.next_task().await;
    assert_eq!(flow_id_of(&named_task), NAMED_FLOW_ID);
    assert_eq!(attempt_of(&named_task), 1);
    let mut default_worker =
        ScriptedWorker::start_reserved_slot(server.worker_client(), FLOW_TYPE, "default-worker")
            .await;
    let default_task = default_worker.next_task().await;
    assert_eq!(flow_id_of(&default_task), DEFAULT_FLOW_ID);
    assert_eq!(attempt_of(&default_task), 1);
    queue_worker.reply(named_task.task_id, complete_output());
    default_worker.reply(default_task.task_id, complete_output());
    assert_completed(&mut flow_client, NAMED_FLOW_ID).await;
    assert_completed(&mut flow_client, DEFAULT_FLOW_ID).await;
    wait_for_queue_entries(metrics, 0).await;
    queue_worker.stop().await;
    default_worker.stop().await;
    server.shutdown().await;
}

/// Short in-memory waits and no periodic Heads, so only Queued refs and rebuild Heads reach
/// matching.
fn queued_settings(metrics: SocketAddr) -> String {
    format!(
        "metrics:\n  listen: '{metrics}'\nmatching:\n  stickyWait: 50ms\n  syncMatchWait: 50ms\n  headsResyncInterval: 0s\n"
    )
}

/// Starts one Flow on the named and one on the default task queue, and waits until both
/// attempts are queued.
async fn start_queued_flows(flow_client: &mut FlowServiceClient<Channel>, metrics: SocketAddr) {
    start_flow(flow_client, NAMED_FLOW_ID, FLOW_TYPE, QUEUE_A, true).await;
    start_flow(flow_client, DEFAULT_FLOW_ID, FLOW_TYPE, "", true).await;
    wait_for_queue_entries(metrics, 2).await;
}

async fn execute_pending_reason(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
) -> i32 {
    flow_client
        .get_flow_state(GetFlowStateRequest {
            flow_id: flow_id.to_owned(),
            ..GetFlowStateRequest::default()
        })
        .await
        .expect("GetFlowState succeeds")
        .into_inner()
        .active_step_executions
        .first()
        .and_then(|step| step.execute_invocation)
        .expect("the queued Step has an Execute invocation")
        .pending_reason
}

fn flow_id_of(task: &PollWorkerTask) -> &str {
    &execute_request(task)
        .context
        .as_ref()
        .expect("the task has a context")
        .flow_id
}

fn attempt_of(task: &PollWorkerTask) -> i32 {
    execute_request(task)
        .context
        .as_ref()
        .expect("the task has a context")
        .attempt
}
