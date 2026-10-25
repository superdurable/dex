// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The close barrier with the real apply: an admitted transactional RPC holds a natural close.

use std::sync::Arc;
use std::sync::atomic::Ordering;

use dexzd_apply::FlowStateReader;
use dexzd_keyspace::row_keys::{prefix_range, run_singleton_key};
use dexzd_keyspace::{ColumnFamily, RunId, RunTable, run_table_prefix};
use dexzd_protocol::dex::{
    AttributeStoreNames, AttributeSyncConfig, AttributeWrite, CloseDecision, CloseDecisionType,
    FlowConfig, FlowStatus, InvokeExecuteMethodResponse, InvokeRpcRequest, InvokeWorkerRpcResponse,
    StepDecision, StepOptions, WorkerTarget,
};
use dexzd_protocol::dexzd::v1::{
    AttributeSyncAckCommand, FlowCommand, RunRecord, StepAttemptStartedCommand, StepInvocationKey,
    StepMethod, StepMethodCompletedCommand, flow_command, step_method_completed_command,
};
use prost::Message;
use tokio::sync::Notify;

use crate::runtime::tests::fixtures::{TENANT, flow, start_request, string_value};
use crate::runtime::tests::test_support::RuntimeHarness;
use crate::runtime::{CommitUrgency, RuntimeConfig, WorkerRpcResult};

const STORE: &str = "primary";
const FLOW_ID: &str = "held-close";

fn synced(attribute_key: &str, text: &str) -> AttributeWrite {
    AttributeWrite {
        key: attribute_key.to_owned(),
        value: Some(string_value(text)),
        sync_config: Some(AttributeSyncConfig { enabled: true }),
        ..AttributeWrite::default()
    }
}

fn run_command(run_id: RunId, body: flow_command::Command) -> FlowCommand {
    FlowCommand {
        flow_id: FLOW_ID.to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        tenant_id: TENANT.0,
        command: Some(body),
        ..FlowCommand::default()
    }
}

fn charge_step() -> StepInvocationKey {
    StepInvocationKey {
        step_execution_id: "InitStep-1".to_owned(),
        method: StepMethod::Execute as i32,
    }
}

fn run_record(harness: &RuntimeHarness, run_id: RunId) -> RunRecord {
    let context = harness.registry.flow_context(&flow(FLOW_ID));
    let key = run_singleton_key(context.flow_hash, run_id, RunTable::RunRecord);
    let bytes = harness
        .store
        .view()
        .get(ColumnFamily::Flow, &key)
        .unwrap()
        .expect("the run record exists");
    RunRecord::decode(bytes.as_slice()).unwrap()
}

fn outbox_entry_count(harness: &RuntimeHarness, run_id: RunId) -> usize {
    let context = harness.registry.flow_context(&flow(FLOW_ID));
    let prefix = run_table_prefix(context.flow_hash, run_id, RunTable::AttributeSyncOutbox);
    let (start, end) = prefix_range(&prefix);
    harness
        .store
        .view()
        .scan(ColumnFamily::Flow, &start, &end, usize::MAX)
        .unwrap()
        .len()
}

async fn propose(harness: &RuntimeHarness, run_id: RunId, body: flow_command::Command) {
    let proposal = harness
        .registry
        .propose(
            flow(FLOW_ID),
            run_command(run_id, body),
            CommitUrgency::Prompt,
        )
        .await
        .expect("the command is proposed");
    harness.commit_all();
    proposal.ticket.wait().await.expect("the command commits");
}

fn ack(acked_seq: u64) -> flow_command::Command {
    flow_command::Command::AttributeSyncAck(AttributeSyncAckCommand {
        store_name: STORE.to_owned(),
        acked_seq,
        attribute_keys: Vec::new(),
        is_dropped: false,
    })
}

fn last_submitted_stamp(harness: &RuntimeHarness) -> bool {
    harness
        .submitted()
        .last()
        .expect("a command was submitted")
        .is_transactional_rpc_in_flight
}

/// Starts a run of InitStep (Execute only) that syncs `status` to the store, with real apply.
async fn start_synced_run() -> (RuntimeHarness, RunId) {
    let mut config = RuntimeConfig::default();
    config
        .validation
        .attribute_store_names
        .insert(STORE.to_owned());
    let harness = RuntimeHarness::with_config(&config);
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    let mut request = start_request(FLOW_ID);
    request.step_options = Some(StepOptions {
        skip_wait_for: true,
        ..StepOptions::default()
    });
    let options = request.flow_start_options.as_mut().unwrap();
    options.attributes = vec![synced("status", "opened")];
    options.flow_config_override = Some(FlowConfig {
        worker_target: Some(WorkerTarget {
            is_pull_mode: true,
            ..WorkerTarget::default()
        }),
        attribute_store_names: Some(AttributeStoreNames {
            names: vec![STORE.to_owned()],
        }),
        ..FlowConfig::default()
    });
    let started = harness
        .drive(harness.registry.start_flow(TENANT, request))
        .await
        .unwrap();
    let run_id: RunId = started.run_id.parse().unwrap();
    propose(&harness, run_id, start_execute()).await;
    (harness, run_id)
}

fn start_execute() -> flow_command::Command {
    flow_command::Command::StepAttemptStarted(StepAttemptStartedCommand {
        invocation: Some(charge_step()),
        attempt: 1,
        task_id: vec![1; 16],
        pull_routing: None,
    })
}

fn force_complete() -> flow_command::Command {
    flow_command::Command::StepMethodCompleted(StepMethodCompletedCommand {
        invocation: Some(charge_step()),
        attempt: 1,
        response: Some(step_method_completed_command::Response::Execute(
            InvokeExecuteMethodResponse {
                step_decision: Some(StepDecision {
                    close_decision: Some(CloseDecision {
                        close_decision_type: CloseDecisionType::ForceComplete as i32,
                        ..CloseDecision::default()
                    }),
                    ..StepDecision::default()
                }),
                ..InvokeExecuteMethodResponse::default()
            },
        )),
        app_worker_id: "worker-1".to_owned(),
        ..StepMethodCompletedCommand::default()
    })
}

fn transactional_rpc() -> InvokeRpcRequest {
    InvokeRpcRequest {
        flow_id: FLOW_ID.to_owned(),
        rpc_name: "approveOrder".to_owned(),
        request_id: "rpc-1".to_owned(),
        is_transactional: true,
        ..InvokeRpcRequest::default()
    }
}

/// Holds the next Worker RPC until the returned gate is notified.
fn hold_worker_rpcs(harness: &RuntimeHarness) -> Arc<Notify> {
    let gate = Arc::new(Notify::new());
    *harness.rpc_invoker.gate.lock().unwrap() = Some(Arc::clone(&gate));
    gate
}

#[tokio::test(start_paused = true)]
async fn a_delayed_admitted_rpc_commits_and_its_writes_hold_the_close_until_acked() {
    let (harness, run_id) = start_synced_run().await;

    // The Worker holds the admitted RPC until the test releases it.
    let gate = hold_worker_rpcs(&harness);
    harness
        .rpc_invoker
        .results
        .lock()
        .unwrap()
        .push_back(Ok(WorkerRpcResult {
            response: InvokeWorkerRpcResponse {
                output: Some(string_value("approved")),
                upsert_attributes: vec![synced("rpc", "written")],
                ..InvokeWorkerRpcResponse::default()
            },
            app_worker_id: "worker-1".to_owned(),
            pull_routing: None,
        }));
    let entered = harness.rpc_invoker.entered.notified();
    let registry = harness.registry.clone();
    let rpc = tokio::spawn(async move { registry.invoke_rpc(TENANT, transactional_rpc()).await });
    entered.await;

    propose(&harness, run_id, force_complete()).await;
    assert!(
        !last_submitted_stamp(&harness),
        "a close that already waits for the outbox keeps its bytes"
    );
    let deferred = run_record(&harness, run_id);
    assert!(deferred.pending_close.is_some());

    // The final ack of the Step's entry would close the run; the in-flight RPC holds it.
    propose(&harness, run_id, ack(deferred.next_run_seq - 1)).await;
    assert!(last_submitted_stamp(&harness));
    assert_eq!(outbox_entry_count(&harness, run_id), 0);
    assert_eq!(
        run_record(&harness, run_id).status,
        FlowStatus::Running as i32
    );

    gate.notify_one();
    let response = harness
        .drive(async { rpc.await.expect("the RPC task completes") })
        .await
        .expect("the admitted RPC commits");
    assert_eq!(response.output, Some(string_value("approved")));
    assert_eq!(
        outbox_entry_count(&harness, run_id),
        1,
        "the RPC's synced write joins the outbox"
    );
    let after_rpc = run_record(&harness, run_id);
    assert_eq!(after_rpc.status, FlowStatus::Running as i32);

    propose(&harness, run_id, ack(after_rpc.next_run_seq - 1)).await;
    assert!(!last_submitted_stamp(&harness));
    let closed = run_record(&harness, run_id);
    assert_eq!(closed.status, FlowStatus::Completed as i32);
    assert_eq!(closed.pending_close, None);
}

#[tokio::test(start_paused = true)]
async fn a_transactional_rpc_is_rejected_once_a_proposed_close_is_in_the_overlay() {
    let (harness, run_id) = start_synced_run().await;
    let proposal = harness
        .registry
        .propose(
            flow(FLOW_ID),
            run_command(run_id, force_complete()),
            CommitUrgency::Prompt,
        )
        .await
        .expect("the close is proposed");
    let error = harness
        .registry
        .invoke_rpc(TENANT, transactional_rpc())
        .await
        .expect_err("the uncommitted pending close rejects the RPC");
    assert_eq!(
        error.grpc_code(),
        crate::runtime::grpc_code::FAILED_PRECONDITION
    );
    assert!(harness.rpc_invoker.invocations.lock().unwrap().is_empty());
    harness.commit_all();
    proposal.ticket.wait().await.expect("the close commits");
}

#[tokio::test(start_paused = true)]
async fn the_rpc_count_drains_when_an_admitted_rpc_fails() {
    let (harness, _run_id) = start_synced_run().await;
    let gate = hold_worker_rpcs(&harness);
    harness
        .rpc_invoker
        .results
        .lock()
        .unwrap()
        .push_back(Err(crate::runtime::RpcInvokeError::DeadlineExceeded));
    let entered = harness.rpc_invoker.entered.notified();
    let registry = harness.registry.clone();
    let rpc = tokio::spawn(async move { registry.invoke_rpc(TENANT, transactional_rpc()).await });
    entered.await;
    let address = flow(FLOW_ID);
    let mut drained = Box::pin(harness.registry.wait_for_transactional_rpcs(&address));
    tokio::select! {
        _ = &mut drained => panic!("the admitted RPC is still in flight"),
        () = tokio::time::sleep(std::time::Duration::from_millis(10)) => {}
    }
    gate.notify_one();
    rpc.await
        .expect("the RPC task completes")
        .expect_err("the Worker failed the RPC");
    drained.await.expect("the count drains");
}
