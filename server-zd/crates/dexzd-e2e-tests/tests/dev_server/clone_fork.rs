// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! CloneForkFlow over gRPC: the new Flow runs the copied Step on its own task queue while the
//! source run keeps its running attempt.

use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    AttributeWrite, CloneForkFlowRequest, FlowConfig, FlowStartOptions, FlowStatus,
    GetAttributesRequest, InvokeExecuteMethodResponse, StartFlowRequest, StepOptions,
    WaitForFlowRequest, WorkerTarget,
};
use tonic::transport::Channel;

use crate::harness::{DevServer, ScriptedWorker, execute_request, execute_result, force_complete};
use crate::string_value;

const FLOW_TYPE: &str = "SessionFlow";
const STEP_TYPE: &str = "TurnStep";
const SOURCE_QUEUE: &str = "session-source";
const CLONE_QUEUE: &str = "session-clone";
/// How long the test waits to show that a Worker receives nothing.
const QUIET_WAIT: Duration = Duration::from_millis(700);

/// The clone fork of a run whose Step runs on the source's task queue dispatches that Step
/// again on the new task queue only; both runs then complete on their own.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_clone_fork_runs_the_copied_step_on_its_own_task_queue() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let mut source_worker = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        FLOW_TYPE,
        "source-worker",
        SOURCE_QUEUE,
    )
    .await;
    let mut clone_worker = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        FLOW_TYPE,
        "clone-worker",
        CLONE_QUEUE,
    )
    .await;
    flow_client
        .start_flow(StartFlowRequest {
            flow_id: "session-a".to_owned(),
            flow_type: FLOW_TYPE.to_owned(),
            start_step_type: STEP_TYPE.to_owned(),
            step_input: Some(string_value("first turn")),
            step_options: Some(StepOptions {
                skip_wait_for: true,
                ..StepOptions::default()
            }),
            flow_start_options: Some(FlowStartOptions {
                flow_config_override: Some(queue_config(SOURCE_QUEUE)),
                attributes: vec![AttributeWrite {
                    key: "transcript".to_owned(),
                    value: Some(string_value(&"t".repeat(300))),
                    ..AttributeWrite::default()
                }],
                ..FlowStartOptions::default()
            }),
            request_id: "session-a-start".to_owned(),
            ..StartFlowRequest::default()
        })
        .await
        .expect("StartFlow succeeds");
    let source_task = source_worker.next_task().await;
    assert_eq!(execute_request(&source_task).step_type, STEP_TYPE);

    let response = flow_client
        .clone_fork_flow(CloneForkFlowRequest {
            source_flow_id: "session-a".to_owned(),
            flow_id: "session-b".to_owned(),
            request_id: "fork-session-a".to_owned(),
            flow_config_override: Some(queue_config(CLONE_QUEUE)),
            attributes: vec![AttributeWrite {
                key: "lease".to_owned(),
                value: Some(string_value("released")),
                ..AttributeWrite::default()
            }],
            ..CloneForkFlowRequest::default()
        })
        .await
        .expect("CloneForkFlow succeeds")
        .into_inner();
    assert!(!response.run_id.is_empty());
    assert_eq!(response.source_run_attempt, 1);

    let clone_task = clone_worker.next_task().await;
    let clone_request = execute_request(&clone_task);
    assert_eq!(clone_request.step_type, STEP_TYPE);
    let context = clone_request
        .context
        .clone()
        .expect("a task has its context");
    assert_eq!(context.flow_id, "session-b");
    assert_eq!(context.run_id, response.run_id);
    assert!(
        source_worker.next_task_within(QUIET_WAIT).await.is_none(),
        "the source's Workers never run the new Flow's Step"
    );
    let attributes = flow_client
        .get_attributes(GetAttributesRequest {
            flow_id: "session-b".to_owned(),
            keys: vec!["transcript".to_owned(), "lease".to_owned()],
            ..GetAttributesRequest::default()
        })
        .await
        .expect("GetAttributes succeeds")
        .into_inner();
    assert_eq!(attributes.attributes.len(), 2, "{attributes:?}");

    clone_worker.reply(
        clone_task.task_id,
        execute_result(InvokeExecuteMethodResponse {
            step_decision: Some(force_complete(string_value("clone done"))),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    assert_eq!(
        wait_for_status(&mut flow_client, "session-b").await,
        FlowStatus::Completed
    );
    source_worker.reply(
        source_task.task_id,
        execute_result(InvokeExecuteMethodResponse {
            step_decision: Some(force_complete(string_value("source done"))),
            ..InvokeExecuteMethodResponse::default()
        }),
    );
    assert_eq!(
        wait_for_status(&mut flow_client, "session-a").await,
        FlowStatus::Completed
    );

    source_worker.stop().await;
    clone_worker.stop().await;
    server.shutdown().await;
}

fn queue_config(task_queue: &str) -> FlowConfig {
    FlowConfig {
        worker_target: Some(WorkerTarget {
            is_pull_mode: true,
            task_queue: task_queue.to_owned(),
        }),
        ..FlowConfig::default()
    }
}

async fn wait_for_status(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
) -> FlowStatus {
    let result = flow_client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 30,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect("WaitForFlow succeeds")
        .into_inner();
    FlowStatus::try_from(result.flow_status).expect("a known status")
}
