// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! GetFlowState reports a Step that waits for a lock an admitted transactional RPC holds.

use std::time::Duration;

use dexzd_protocol::dex::{
    ChannelCondition, ChannelMessage, GetFlowStateRequest, InvokeRpcRequest,
    InvokeWaitForMethodResponse, InvokeWorkerRpcResponse, PublishToChannelRequest,
    StepMethodInvocationPhase, StepMethodPendingReason, StepOptions, Value, WaitingCondition,
    WaitingConditionType, poll_worker_task, poll_worker_task_frame,
};

use crate::harness;

const FLOW_ID: &str = "rpc-lock-state";
const FLOW_TYPE: &str = "LockStateFlow";

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_step_waiting_for_a_live_rpc_lock_reports_waiting_for_locks() {
    let server = harness::DevServer::start_with_config_overrides(Some(
        "matching:\n  stickyWait: 1ms\n  syncMatchWait: 50ms\n  headsResyncInterval: 0s\n",
    ))
    .await;
    let mut client = server.flow_client();
    let mut worker = harness::ScriptedWorker::start_reserved_slot(
        server.worker_client(),
        FLOW_TYPE,
        "rpc-worker",
    )
    .await;
    harness::start_pull_flow(
        &mut client,
        FLOW_ID,
        FLOW_TYPE,
        "WaitStep",
        Value::default(),
        StepOptions {
            execute_lock_attribute_keys: vec!["balance".to_owned()],
            ..StepOptions::default()
        },
    )
    .await;
    let wait_task = worker.next_task().await;
    worker.reply(
        wait_task.task_id,
        harness::wait_for_result(InvokeWaitForMethodResponse {
            waiting_condition: Some(WaitingCondition {
                waiting_condition_type: WaitingConditionType::AllCompleted as i32,
                channel_conditions: vec![ChannelCondition {
                    condition_id: "release".to_owned(),
                    channel_name: "release".to_owned(),
                    ..ChannelCondition::default()
                }],
                ..WaitingCondition::default()
            }),
            ..InvokeWaitForMethodResponse::default()
        }),
    );
    harness::wait_until_step_waits(&mut client, FLOW_ID, "WaitStep-1").await;
    // The admitted RPC holds balance until its Worker answers.
    let mut rpc_client = client.clone();
    let rpc = tokio::spawn(async move {
        rpc_client
            .invoke_rpc(InvokeRpcRequest {
                flow_id: FLOW_ID.to_owned(),
                rpc_name: "updateBalance".to_owned(),
                lock_attribute_keys: vec!["balance".to_owned()],
                timeout_seconds: 30,
                request_id: "rpc-lock-state-1".to_owned(),
                ..InvokeRpcRequest::default()
            })
            .await
    });
    let rpc_task = worker.next_task().await;
    assert!(matches!(
        rpc_task.request,
        Some(poll_worker_task::Request::Rpc(_))
    ));
    let runner = harness::ScriptedWorker::start_reserved_slot(
        server.worker_client(),
        FLOW_TYPE,
        "step-worker",
    )
    .await;
    client
        .publish_to_channel(PublishToChannelRequest {
            flow_id: FLOW_ID.to_owned(),
            messages: vec![ChannelMessage {
                channel_name: "release".to_owned(),
                value: Some(Value::default()),
                ..ChannelMessage::default()
            }],
            ..PublishToChannelRequest::default()
        })
        .await
        .expect("publish");
    let invocation = tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            let state = client
                .get_flow_state(GetFlowStateRequest {
                    flow_id: FLOW_ID.to_owned(),
                    ..GetFlowStateRequest::default()
                })
                .await
                .expect("state")
                .into_inner();
            if let Some(invocation) = state
                .active_step_executions
                .iter()
                .find_map(|step| step.execute_invocation)
            {
                break invocation;
            }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .expect("Execute becomes Pending");
    worker.reply(
        rpc_task.task_id,
        poll_worker_task_frame::Output::Rpc(InvokeWorkerRpcResponse::default()),
    );
    rpc.await.expect("rpc task").expect("rpc result");
    runner.stop().await;
    worker.stop().await;
    server.shutdown().await;
    assert_eq!(invocation.phase, StepMethodInvocationPhase::Pending as i32);
    assert_eq!(
        invocation.pending_reason,
        StepMethodPendingReason::WaitingForLocks as i32,
        "an admitted RPC still owns balance: {invocation:?}"
    );
}
