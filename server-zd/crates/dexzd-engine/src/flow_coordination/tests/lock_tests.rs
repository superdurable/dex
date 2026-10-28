// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;
use std::sync::atomic::Ordering;
use std::time::Duration;

use dexzd_apply::FlowStateReader;
use dexzd_keyspace::{ColumnFamily, RunId, RunTable, run_key};
use dexzd_protocol::dex::{
    AttributeWrite, ErrorSubStatus, InvokeExecuteMethodResponse, InvokeRpcRequest,
    InvokeWorkerRpcResponse, RetryPolicy, StepOptions, WorkerErrorResponse,
};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, LockRecord, LockWaiterRecord, RetryBackoffTimerId, StepAttemptFailedCommand,
    StepAttemptFailureKind, StepAttemptStartedCommand, StepExecutionRecord, StepInvocationKey,
    StepInvocationPhase, StepMethod, StepMethodCompletedCommand, TimerId, TimersFiredCommand,
    flow_command::Command, step_method_completed_command, timer_id,
};
use prost::Message;
use tokio::sync::Notify;

use crate::flow_coordination::tests::fixtures::{
    TENANT, flow, locking_attempt_command, locking_invocation, set_attribute_request, start_flow,
    start_request, string_value,
};
use crate::flow_coordination::tests::test_support::FlowCoordinationHarness;
use crate::flow_coordination::{
    CommitUrgency, FlowCoordinationConfig, FlowCoordinationError, PendingInvocation,
    WorkerRpcResult, grpc_code,
};

fn rpc_request(flow_id: &str, lock_keys: &[&str]) -> InvokeRpcRequest {
    InvokeRpcRequest {
        flow_id: flow_id.to_owned(),
        rpc_name: "approveOrder".to_owned(),
        request_id: "rpc-1".to_owned(),
        lock_attribute_keys: lock_keys.iter().map(|key| (*key).to_owned()).collect(),
        ..InvokeRpcRequest::default()
    }
}

fn output_result(text: &str) -> WorkerRpcResult {
    WorkerRpcResult {
        response: InvokeWorkerRpcResponse {
            output: Some(string_value(text)),
            ..InvokeWorkerRpcResponse::default()
        },
        app_worker_id: "worker-1".to_owned(),
        pull_routing: None,
    }
}

fn assert_aborted(error: &FlowCoordinationError) {
    assert_eq!(error.grpc_code(), grpc_code::ABORTED, "{error:?}");
    assert_eq!(
        error.sub_status(),
        ErrorSubStatus::WorkerApiError,
        "the Go SDK maps this pair to RPCLockConflictError"
    );
}

fn seed_lock_record(
    harness: &FlowCoordinationHarness,
    run_id: RunId,
    lock_key: &str,
    record: &LockRecord,
) {
    let context = harness.registry.flow_context(&flow("order-1"));
    harness.store.put(
        ColumnFamily::Flow,
        run_key(
            context.flow_hash,
            run_id,
            RunTable::LockTable,
            lock_key.as_bytes(),
        ),
        record.encode_to_vec(),
    );
}

fn pending(
    run_id: RunId,
    invocation: &StepInvocationKey,
    lock_keys: &[String],
    pending_since_seq: u64,
) -> PendingInvocation {
    PendingInvocation {
        run_id,
        invocation: invocation.clone(),
        lock_keys: lock_keys.to_vec(),
        pending_since_seq,
    }
}

fn waiter_invocation(step_execution_id: &str) -> StepInvocationKey {
    StepInvocationKey {
        step_execution_id: step_execution_id.to_owned(),
        method: StepMethod::Execute as i32,
    }
}

#[tokio::test(start_paused = true)]
async fn rpc_conflicts_with_a_speculative_step_lock_holder() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    harness
        .registry
        .propose(
            flow("order-1"),
            locking_attempt_command("order-1", run_id, "balance"),
            CommitUrgency::Batched,
        )
        .await
        .unwrap();

    let error = harness
        .drive(
            harness
                .registry
                .invoke_rpc(TENANT, rpc_request("order-1", &["balance"])),
        )
        .await
        .unwrap_err();
    assert_aborted(&error);
    assert!(
        harness.rpc_invoker.invocations.lock().unwrap().is_empty(),
        "the Worker is never called on a conflict"
    );
}

#[tokio::test(start_paused = true)]
async fn rpc_conflicts_with_another_rpc_until_the_worker_finishes_after_the_caller_drops() {
    let harness = Arc::new(FlowCoordinationHarness::new());
    start_flow(&harness, "order-1").await.unwrap();
    let gate = Arc::new(Notify::new());
    *harness.rpc_invoker.gate.lock().unwrap() = Some(Arc::clone(&gate));
    harness
        .rpc_invoker
        .results
        .lock()
        .unwrap()
        .push_back(Ok(output_result("first")));

    let mut holder = Box::pin(
        harness
            .registry
            .invoke_rpc(TENANT, rpc_request("order-1", &["balance"])),
    );
    tokio::select! {
        _ = &mut holder => panic!("the first RPC waits at the gate"),
        () = harness.rpc_invoker.entered.notified() => {}
    }

    let error = harness
        .registry
        .invoke_rpc(
            TENANT,
            InvokeRpcRequest {
                request_id: "conflicting-rpc".to_owned(),
                ..rpc_request("order-1", &["other", "balance"])
            },
        )
        .await
        .unwrap_err();
    assert_aborted(&error);

    let unrelated_gate = Arc::new(Notify::new());
    *harness.rpc_invoker.gate.lock().unwrap() = Some(Arc::clone(&unrelated_gate));
    let mut unrelated = Box::pin(harness.registry.invoke_rpc(
        TENANT,
        InvokeRpcRequest {
            request_id: "unrelated-rpc".to_owned(),
            ..rpc_request("order-1", &["unrelated"])
        },
    ));
    tokio::select! {
        _ = &mut unrelated => panic!("waits at the gate too"),
        () = harness.rpc_invoker.entered.notified() => {}
    }

    drop(holder);
    let error = harness
        .registry
        .invoke_rpc(
            TENANT,
            InvokeRpcRequest {
                request_id: "retry-before-worker-finishes".to_owned(),
                ..rpc_request("order-1", &["balance"])
            },
        )
        .await
        .unwrap_err();
    assert_aborted(&error);
    *harness.rpc_invoker.gate.lock().unwrap() = None;
    gate.notify_one();
    let response = harness
        .drive(
            harness
                .registry
                .invoke_rpc(TENANT, rpc_request("order-1", &["balance"])),
        )
        .await
        .unwrap();
    assert_eq!(response.output, Some(string_value("first")));
    harness
        .rpc_invoker
        .results
        .lock()
        .unwrap()
        .push_back(Ok(output_result("unrelated")));
    unrelated_gate.notify_one();
    let unrelated_response = harness.drive(unrelated).await.unwrap();
    assert_eq!(unrelated_response.output, Some(string_value("unrelated")));
    harness
        .rpc_invoker
        .results
        .lock()
        .unwrap()
        .push_back(Ok(output_result("second")));
    let response = harness
        .drive(harness.registry.invoke_rpc(
            TENANT,
            InvokeRpcRequest {
                request_id: "after-worker-finishes".to_owned(),
                ..rpc_request("order-1", &["balance"])
            },
        ))
        .await
        .unwrap();
    assert_eq!(response.output, Some(string_value("second")));
}

#[tokio::test(start_paused = true)]
async fn rpc_ignores_queued_step_waiters_but_invocations_respect_fifo() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    seed_lock_record(
        &harness,
        run_id,
        "balance",
        &LockRecord {
            holder: None,
            holder_attempt: 0,
            waiters: vec![LockWaiterRecord {
                invocation: Some(waiter_invocation("Earlier-1")),
                pending_since_seq: 5,
            }],
        },
    );
    let address = flow("order-1");
    let later = waiter_invocation("Later-1");
    let lock_keys = vec!["balance".to_owned()];

    assert!(
        !harness
            .registry
            .can_start_invocation(&address, &pending(run_id, &later, &lock_keys, 9))
            .unwrap(),
        "a later waiter cannot pass an earlier one"
    );
    assert!(
        harness
            .registry
            .can_start_invocation(
                &address,
                &pending(run_id, &waiter_invocation("Earlier-1"), &lock_keys, 5)
            )
            .unwrap()
    );

    harness
        .rpc_invoker
        .results
        .lock()
        .unwrap()
        .push_back(Ok(output_result("read")));
    let response = harness
        .drive(
            harness
                .registry
                .invoke_rpc(TENANT, rpc_request("order-1", &["balance"])),
        )
        .await
        .unwrap();
    assert_eq!(response.output, Some(string_value("read")));
}

#[tokio::test(start_paused = true)]
async fn invocation_start_is_blocked_by_an_rpc_lock_and_proposes_once_free() {
    let harness = Arc::new(FlowCoordinationHarness::new());
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    let address = flow("order-1");
    let gate = Arc::new(Notify::new());
    *harness.rpc_invoker.gate.lock().unwrap() = Some(Arc::clone(&gate));
    harness
        .rpc_invoker
        .results
        .lock()
        .unwrap()
        .push_back(Ok(output_result("done")));
    let rpc = harness
        .registry
        .invoke_rpc(TENANT, rpc_request("order-1", &["balance"]));
    tokio::pin!(rpc);
    tokio::select! {
        _ = &mut rpc => panic!("waits at the gate"),
        () = harness.rpc_invoker.entered.notified() => {}
    }

    let lock_keys = vec!["balance".to_owned()];
    let invocation = locking_invocation("balance");
    let blocked = harness
        .registry
        .propose_invocation_start(
            &address,
            &pending(run_id, &invocation, &lock_keys, 1),
            locking_attempt_command("order-1", run_id, "balance"),
            CommitUrgency::Prompt,
        )
        .await
        .unwrap();
    assert!(blocked.is_none(), "an RPC holds the key");

    gate.notify_one();
    let response = harness.drive(rpc).await.unwrap();
    assert_eq!(response.output, Some(string_value("done")));

    let started = harness
        .registry
        .propose_invocation_start(
            &address,
            &pending(run_id, &invocation, &lock_keys, 1),
            locking_attempt_command("order-1", run_id, "balance"),
            CommitUrgency::Prompt,
        )
        .await
        .unwrap();
    assert!(started.is_some());
    assert!(
        !harness
            .registry
            .can_start_invocation(
                &address,
                &pending(run_id, &waiter_invocation("Other-1"), &lock_keys, 2)
            )
            .unwrap(),
        "the speculative holder blocks other invocations"
    );
}

#[tokio::test(start_paused = true)]
async fn stalled_worker_rpc_ends_at_its_deadline_and_releases_its_locks() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    *harness.rpc_invoker.gate.lock().unwrap() = Some(Arc::new(Notify::new()));
    let mut request = rpc_request("order-1", &["balance"]);
    request.timeout_seconds = 1;

    let error = tokio::time::timeout(
        Duration::from_secs(5),
        harness.registry.invoke_rpc(TENANT, request),
    )
    .await
    .expect("the RPC ends at its deadline")
    .unwrap_err();
    assert_eq!(error.grpc_code(), grpc_code::DEADLINE_EXCEEDED, "{error:?}");

    let lock_keys = vec!["balance".to_owned()];
    let started = harness
        .registry
        .propose_invocation_start(
            &flow("order-1"),
            &pending(run_id, &locking_invocation("balance"), &lock_keys, 1),
            locking_attempt_command("order-1", run_id, "balance"),
            CommitUrgency::Prompt,
        )
        .await
        .unwrap();
    assert!(started.is_some(), "the expired RPC released its lock");
}

#[tokio::test(start_paused = true)]
async fn equal_sequence_waiters_start_in_queue_order() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    let first = locking_invocation("balance");
    let second = waiter_invocation("Second-1");
    seed_lock_record(
        &harness,
        run_id,
        "balance",
        &LockRecord {
            holder: None,
            holder_attempt: 0,
            waiters: vec![
                LockWaiterRecord {
                    invocation: Some(first.clone()),
                    pending_since_seq: 7,
                },
                LockWaiterRecord {
                    invocation: Some(second.clone()),
                    pending_since_seq: 7,
                },
            ],
        },
    );
    let address = flow("order-1");
    let lock_keys = vec!["balance".to_owned()];

    let blocked = harness
        .registry
        .propose_invocation_start(
            &address,
            &pending(run_id, &second, &lock_keys, 7),
            locking_attempt_command("order-1", run_id, "balance"),
            CommitUrgency::Prompt,
        )
        .await
        .unwrap();
    assert!(
        blocked.is_none(),
        "the second queued waiter cannot pass the first"
    );
    let started = harness
        .registry
        .propose_invocation_start(
            &address,
            &pending(run_id, &first, &lock_keys, 7),
            locking_attempt_command("order-1", run_id, "balance"),
            CommitUrgency::Prompt,
        )
        .await
        .unwrap();
    assert!(started.is_some());
}

#[tokio::test(start_paused = true)]
async fn retry_backoff_rejects_writing_rpcs_and_retains_the_snapshot_after_restart() {
    let harness = FlowCoordinationHarness::new();
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    let mut request = start_request("order-1");
    request.step_options = Some(StepOptions {
        skip_wait_for: true,
        execute_lock_attribute_keys: vec!["counter".to_owned()],
        execute_retry_policy: Some(RetryPolicy {
            maximum_attempts: 3,
            ..RetryPolicy::default()
        }),
        ..StepOptions::default()
    });
    request.flow_start_options.as_mut().unwrap().attributes = vec![AttributeWrite {
        key: "counter".to_owned(),
        value: Some(string_value("1")),
        ..AttributeWrite::default()
    }];
    let started = harness
        .drive(harness.registry.start_flow(TENANT, request))
        .await
        .unwrap();
    let run_id = started.run_id.parse().unwrap();
    let invocation = waiter_invocation("InitStep-1");
    propose_retry_command(
        &harness,
        run_id,
        Command::StepAttemptStarted(StepAttemptStartedCommand {
            invocation: Some(invocation.clone()),
            attempt: 1,
            task_id: vec![1; 16],
            pull_routing: None,
        }),
    )
    .await;
    let first_snapshot = read_retry_step(&harness, run_id)
        .invocation
        .unwrap()
        .snapshot
        .unwrap();
    propose_retry_command(
        &harness,
        run_id,
        Command::StepAttemptFailed(StepAttemptFailedCommand {
            invocation: Some(invocation.clone()),
            attempt: 1,
            kind: StepAttemptFailureKind::WorkerError as i32,
            worker_error: Some(WorkerErrorResponse::default()),
            ..StepAttemptFailedCommand::default()
        }),
    )
    .await;
    assert_eq!(
        read_retry_step(&harness, run_id).invocation.unwrap().phase,
        StepInvocationPhase::Backoff as i32
    );

    let restarted = FlowCoordinationHarness::with_store(
        &FlowCoordinationConfig::default(),
        harness.store.clone(),
    );
    restarted
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    for owner in [&harness, &restarted] {
        let mut result = output_result("updated");
        result.response.upsert_attributes = vec![AttributeWrite {
            key: "counter".to_owned(),
            value: Some(string_value("3")),
            ..AttributeWrite::default()
        }];
        owner
            .rpc_invoker
            .results
            .lock()
            .unwrap()
            .push_back(Ok(result));
        let error = owner
            .drive(
                owner
                    .registry
                    .invoke_rpc(TENANT, rpc_request("order-1", &["counter"])),
            )
            .await
            .unwrap_err();
        assert_aborted(&error);
        assert!(owner.rpc_invoker.invocations.lock().unwrap().is_empty());
    }
    restarted
        .drive(restarted.registry.set_attributes(
            TENANT,
            set_attribute_request("order-1", "note", string_value("late")),
        ))
        .await
        .unwrap();
    propose_retry_command(
        &restarted,
        run_id,
        Command::TimersFired(TimersFiredCommand {
            timers: vec![TimerId {
                run_attempt: 1,
                kind: Some(timer_id::Kind::RetryBackoff(RetryBackoffTimerId {
                    invocation: Some(invocation.clone()),
                    next_attempt: 2,
                })),
            }],
        }),
    )
    .await;
    assert!(
        restarted
            .registry
            .can_start_invocation(
                &flow("order-1"),
                &pending(run_id, &invocation, &["counter".to_owned()], u64::MAX)
            )
            .unwrap()
    );
    propose_retry_command(
        &restarted,
        run_id,
        Command::StepAttemptStarted(StepAttemptStartedCommand {
            invocation: Some(invocation.clone()),
            attempt: 2,
            task_id: vec![2; 16],
            pull_routing: None,
        }),
    )
    .await;
    assert_eq!(
        read_retry_step(&restarted, run_id)
            .invocation
            .unwrap()
            .snapshot,
        Some(first_snapshot)
    );
    propose_retry_command(
        &restarted,
        run_id,
        Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(invocation),
            attempt: 2,
            response: Some(step_method_completed_command::Response::Execute(
                InvokeExecuteMethodResponse {
                    upsert_attributes: vec![AttributeWrite {
                        key: "counter".to_owned(),
                        value: Some(string_value("2")),
                        ..AttributeWrite::default()
                    }],
                    ..InvokeExecuteMethodResponse::default()
                },
            )),
            ..StepMethodCompletedCommand::default()
        }),
    )
    .await;
    restarted
        .drive(
            restarted
                .registry
                .invoke_rpc(TENANT, rpc_request("order-1", &["counter"])),
        )
        .await
        .unwrap();
    assert_eq!(restarted.rpc_invoker.invocations.lock().unwrap().len(), 1);
}

async fn propose_retry_command(harness: &FlowCoordinationHarness, run_id: RunId, body: Command) {
    let proposal = harness
        .registry
        .propose(
            flow("order-1"),
            FlowCommand {
                flow_id: "order-1".to_owned(),
                run_id: run_id.as_bytes().to_vec(),
                tenant_id: TENANT.0,
                command: Some(body),
                ..FlowCommand::default()
            },
            CommitUrgency::Prompt,
        )
        .await
        .unwrap();
    harness.commit_all();
    proposal.ticket.wait().await.unwrap();
}

fn read_retry_step(harness: &FlowCoordinationHarness, run_id: RunId) -> StepExecutionRecord {
    let context = harness.registry.flow_context(&flow("order-1"));
    let bytes = harness
        .store
        .view()
        .get(
            ColumnFamily::Flow,
            &run_key(
                context.flow_hash,
                run_id,
                RunTable::StepExecution,
                b"InitStep-1",
            ),
        )
        .unwrap()
        .unwrap();
    StepExecutionRecord::decode(bytes.as_slice()).unwrap()
}
