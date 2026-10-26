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
    ErrorSubStatus, FlowConfig, FlowResult, FlowStatus, InvokeExecuteMethodResponse,
    InvokeRpcRequest, InvokeRpcResponse, InvokeWaitForMethodResponse, InvokeWorkerRpcResponse,
    SkipTimerRequest, StartFlowRequest, StepDecision, StepOptions, StopFlowRequest, StopType,
    SubFlowCondition, Value, WaitingCondition, WaitingConditionType, WorkerTarget,
};
use dexzd_protocol::dexzd::v1::{
    AttributeSyncAckCommand, AttributesSetCommand, ChildFlowCompletedMessage,
    ChildFlowStartMessage, CrossFlowEndpoint, FlowCommand, FlowStopCommand, InboxMessageCommand,
    OutboxRecord, RunRecord, StepAttemptStartedCommand, StepInvocationKey, StepMethod,
    StepMethodCompletedCommand, flow_command, inbox_message_command, outbox_record,
    step_method_completed_command,
};
use prost::Message;
use tokio::sync::Notify;
use tokio::task::JoinHandle;

use crate::runtime::tests::fixtures::{
    TENANT, flow, set_attribute_request, start_request, string_value,
};
use crate::runtime::tests::test_support::RuntimeHarness;
use crate::runtime::{CommitUrgency, RuntimeConfig, RuntimeError, WorkerRpcResult};

const STORE: &str = "primary";
const FLOW_ID: &str = "held-close";
/// The Flow timeout is the only timer of the timeout handler's Step execution.
const FLOW_TIMEOUT_STEP_EXECUTION_ID: &str = "sys:timeout_handler-1";

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
async fn admission_rejects_a_transactional_rpc_once_a_stop_closes_the_run_in_the_overlay() {
    // The pre-admission check reads only committed state, so this reproduces a StopFlow racing it.
    let (harness, run_id) = start_unsynced_run(execute_only_request()).await;
    let stop = flow_command::Command::FlowStop(FlowStopCommand {
        request: Some(StopFlowRequest {
            flow_id: FLOW_ID.to_owned(),
            stop_type: StopType::Terminate as i32,
            ..StopFlowRequest::default()
        }),
    });
    let proposal = harness
        .registry
        .propose(
            flow(FLOW_ID),
            run_command(run_id, stop),
            CommitUrgency::Prompt,
        )
        .await
        .expect("the stop is proposed");
    assert_eq!(
        run_record(&harness, run_id).status,
        FlowStatus::Running as i32
    );
    let error = harness
        .registry
        .invoke_rpc(TENANT, transactional_rpc())
        .await
        .expect_err("admission sees the stopped run");
    assert_eq!(error.grpc_code(), crate::runtime::grpc_code::NOT_FOUND);
    assert_eq!(error.sub_status(), ErrorSubStatus::FlowNotExists);
    assert!(harness.rpc_invoker.invocations.lock().unwrap().is_empty());
    harness.commit_all();
    proposal.ticket.wait().await.expect("the stop commits");
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

/// Starts `request` with real apply and no Attribute Store, so no close waits for an outbox.
async fn start_unsynced_run(request: StartFlowRequest) -> (RuntimeHarness, RunId) {
    let harness = RuntimeHarness::new();
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    let started = harness
        .drive(harness.registry.start_flow(TENANT, request))
        .await
        .unwrap();
    let run_id = started.run_id.parse().unwrap();
    (harness, run_id)
}

fn execute_only_request() -> StartFlowRequest {
    let mut request = start_request(FLOW_ID);
    request.step_options = Some(StepOptions {
        skip_wait_for: true,
        ..StepOptions::default()
    });
    request
}

/// Admits a transactional RPC whose Worker holds it until the returned gate is notified.
async fn admit_held_rpc(
    harness: &RuntimeHarness,
) -> (
    Arc<Notify>,
    JoinHandle<Result<InvokeRpcResponse, RuntimeError>>,
) {
    let gate = hold_worker_rpcs(harness);
    harness
        .rpc_invoker
        .results
        .lock()
        .unwrap()
        .push_back(Ok(WorkerRpcResult {
            response: InvokeWorkerRpcResponse {
                output: Some(string_value("approved")),
                ..InvokeWorkerRpcResponse::default()
            },
            app_worker_id: "worker-1".to_owned(),
            pull_routing: None,
        }));
    let entered = harness.rpc_invoker.entered.notified();
    let registry = harness.registry.clone();
    let rpc = tokio::spawn(async move { registry.invoke_rpc(TENANT, transactional_rpc()).await });
    entered.await;
    (gate, rpc)
}

/// Releases the held RPC; returns the output of its committed response.
async fn release_rpc(
    harness: &RuntimeHarness,
    gate: &Notify,
    rpc: JoinHandle<Result<InvokeRpcResponse, RuntimeError>>,
) -> Option<Value> {
    gate.notify_one();
    harness
        .drive(async { rpc.await.expect("the RPC task completes") })
        .await
        .expect("the admitted RPC commits")
        .output
}

fn apply_count(harness: &RuntimeHarness) -> usize {
    harness.applier.applied.lock().unwrap().len()
}

fn set_attribute(attribute_key: &str) -> flow_command::Command {
    flow_command::Command::AttributesSet(AttributesSetCommand {
        request: Some(set_attribute_request(
            FLOW_ID,
            attribute_key,
            string_value("set"),
        )),
        ..AttributesSetCommand::default()
    })
}

fn start_wait_for() -> flow_command::Command {
    flow_command::Command::StepAttemptStarted(StepAttemptStartedCommand {
        invocation: Some(StepInvocationKey {
            step_execution_id: "InitStep-1".to_owned(),
            method: StepMethod::WaitFor as i32,
        }),
        attempt: 1,
        task_id: vec![2; 16],
        pull_routing: None,
    })
}

/// InitStep's WaitFor result: wait until one child Flow completes.
fn wait_for_child() -> flow_command::Command {
    flow_command::Command::StepMethodCompleted(StepMethodCompletedCommand {
        invocation: Some(StepInvocationKey {
            step_execution_id: "InitStep-1".to_owned(),
            method: StepMethod::WaitFor as i32,
        }),
        attempt: 1,
        response: Some(step_method_completed_command::Response::WaitFor(
            InvokeWaitForMethodResponse {
                waiting_condition: Some(WaitingCondition {
                    waiting_condition_type: WaitingConditionType::AllCompleted as i32,
                    sub_flow_conditions: vec![SubFlowCondition {
                        condition_id: "child".to_owned(),
                        sub_flow_type: "ChildFlow".to_owned(),
                        start_step_type: "ChildStep".to_owned(),
                        step_options: Some(StepOptions {
                            skip_wait_for: true,
                            ..StepOptions::default()
                        }),
                        ..SubFlowCondition::default()
                    }],
                    ..WaitingCondition::default()
                }),
                ..InvokeWaitForMethodResponse::default()
            },
        )),
        app_worker_id: "worker-1".to_owned(),
        ..StepMethodCompletedCommand::default()
    })
}

fn outbox_child_start(harness: &RuntimeHarness, run_id: RunId) -> ChildFlowStartMessage {
    let context = harness.registry.flow_context(&flow(FLOW_ID));
    let prefix = run_table_prefix(context.flow_hash, run_id, RunTable::Outbox);
    let (start, end) = prefix_range(&prefix);
    let rows = harness
        .store
        .view()
        .scan(ColumnFamily::Flow, &start, &end, usize::MAX)
        .unwrap();
    let [(_, bytes)] = rows.as_slice() else {
        panic!("the parent outbox holds one message: {rows:?}");
    };
    let record = OutboxRecord::decode(bytes.as_slice()).unwrap();
    let Some(outbox_record::Payload::ChildStart(child_start)) = record.payload else {
        panic!("the parent outbox holds a child start");
    };
    child_start
}

/// The child's completion as the child's owner delivers it to the parent.
fn child_completed(child_start: ChildFlowStartMessage) -> flow_command::Command {
    let child_run_id = vec![5; 16];
    flow_command::Command::InboxMessage(InboxMessageCommand {
        message_id: vec![6; 16],
        source: Some(CrossFlowEndpoint {
            tenant_id: TENANT.0,
            flow_id: child_start.child_flow_id.clone(),
            run_id: child_run_id.clone(),
            run_attempt: 1,
        }),
        payload: Some(inbox_message_command::Payload::ChildCompleted(
            ChildFlowCompletedMessage {
                child_flow_id: child_start.child_flow_id,
                child_run_id,
                child_run_attempt: 1,
                result: Some(FlowResult {
                    flow_status: FlowStatus::Completed as i32,
                    ..FlowResult::default()
                }),
                parent: child_start.parent,
            },
        )),
        ..InboxMessageCommand::default()
    })
}

#[tokio::test(start_paused = true)]
async fn only_a_flow_with_an_admitted_rpc_dry_runs_its_commands() {
    let (harness, run_id) = start_unsynced_run(execute_only_request()).await;
    let before = apply_count(&harness);
    propose(&harness, run_id, set_attribute("first")).await;
    assert_eq!(
        apply_count(&harness) - before,
        2,
        "a speculative apply and the committed apply"
    );

    let (gate, rpc) = admit_held_rpc(&harness).await;
    let before = apply_count(&harness);
    propose(&harness, run_id, set_attribute("second")).await;
    assert_eq!(
        apply_count(&harness) - before,
        3,
        "the stamp's dry run adds one apply"
    );
    assert!(!last_submitted_stamp(&harness));
    assert_eq!(
        release_rpc(&harness, &gate, rpc).await,
        Some(string_value("approved"))
    );
}

#[tokio::test(start_paused = true)]
async fn a_skipped_flow_timeout_closes_only_after_an_admitted_rpc_commits() {
    let mut request = execute_only_request();
    request.flow_timeout_seconds = 60;
    let (harness, run_id) = start_unsynced_run(request).await;
    let (gate, rpc) = admit_held_rpc(&harness).await;

    harness
        .drive(harness.registry.skip_timer(
            TENANT,
            SkipTimerRequest {
                flow_id: FLOW_ID.to_owned(),
                step_execution_id: FLOW_TIMEOUT_STEP_EXECUTION_ID.to_owned(),
                timer_condition_index: Some(0),
                ..SkipTimerRequest::default()
            },
        ))
        .await
        .expect("the Flow timeout is skipped");
    assert!(
        last_submitted_stamp(&harness),
        "the skip would time the run out"
    );
    let held = run_record(&harness, run_id);
    assert_eq!(held.status, FlowStatus::Running as i32);
    assert!(held.pending_close.is_some());

    assert_eq!(
        release_rpc(&harness, &gate, rpc).await,
        Some(string_value("approved"))
    );
    let closed = run_record(&harness, run_id);
    assert_eq!(closed.status, FlowStatus::Failed as i32);
    assert_eq!(closed.pending_close, None);
}

#[tokio::test(start_paused = true)]
async fn a_child_result_wakes_the_last_step_whose_close_then_waits_for_an_admitted_rpc() {
    let (harness, run_id) = start_unsynced_run(start_request(FLOW_ID)).await;
    propose(&harness, run_id, start_wait_for()).await;
    propose(&harness, run_id, wait_for_child()).await;
    let child_start = outbox_child_start(&harness, run_id);
    let (gate, rpc) = admit_held_rpc(&harness).await;

    let before = apply_count(&harness);
    propose(&harness, run_id, child_completed(child_start)).await;
    assert_eq!(
        apply_count(&harness) - before,
        3,
        "the owner dry-runs the child result"
    );
    assert!(
        !last_submitted_stamp(&harness),
        "the child result moves the Step to Execute, which keeps the run open"
    );
    let woken = run_record(&harness, run_id);
    assert_eq!(woken.status, FlowStatus::Running as i32);
    assert_eq!(woken.pending_close, None);

    propose(&harness, run_id, start_execute()).await;
    propose(&harness, run_id, force_complete()).await;
    assert!(last_submitted_stamp(&harness));
    let held = run_record(&harness, run_id);
    assert_eq!(held.status, FlowStatus::Running as i32);
    assert!(held.pending_close.is_some());

    assert_eq!(
        release_rpc(&harness, &gate, rpc).await,
        Some(string_value("approved"))
    );
    let closed = run_record(&harness, run_id);
    assert_eq!(closed.status, FlowStatus::Completed as i32);
    assert_eq!(closed.pending_close, None);
}
