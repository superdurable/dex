// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use dexzd_apply::{ApplyEffect, ApplyOutcome, FlowStateReader, StateWrite, WaitableChange};
use dexzd_keyspace::row_keys::history_record_key;
use dexzd_keyspace::{ColumnFamily, RunId, RunTable, ValueId, ValueReference, run_key};
use dexzd_protocol::dex::{
    AttributeMatch, AttributeMatchOperator, ErrorSubStatus, FlowClosedHistoryEvent,
    FlowHistoryEvent, FlowStatus, InvokeRpcRequest, StopFlowRequest, StopType, Value,
    WaitForAttributeRequest, WaitForFlowRequest, WaitForHistoryEventRequest,
    WaitForStepCompletionRequest, flow_history_event, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributeRecord, FlowCloseRecord, FlowCommand, FlowStopCommand, HistoryRecord,
    PendingFlowClose, RunRecord, StepCounterRecord, StepExecutionPhase, StepExecutionRecord,
    StoredStepCompletionOutput, StoredValue, StoredValueId, StoredValueReference, flow_command,
    stored_value,
};
use prost::Message;

use crate::flow_coordination::tests::fixtures::{
    TENANT, flow, int_value, set_attribute_request, start_flow, string_value,
};
use crate::flow_coordination::tests::test_support::FlowCoordinationHarness;
use crate::flow_coordination::{
    CommitUrgency, CommittedCommand, CommittedFlowCommand, FlowCoordinationConfig,
    FlowCoordinationError, grpc_code,
};

fn wait_for_flow_request(flow_id: &str, wait_time_seconds: i32) -> WaitForFlowRequest {
    WaitForFlowRequest {
        flow_id: flow_id.to_owned(),
        wait_time_seconds,
        needs_results: true,
        ..WaitForFlowRequest::default()
    }
}

fn stop_command(flow_id: &str, run_id: dexzd_keyspace::RunId) -> FlowCommand {
    FlowCommand {
        flow_id: flow_id.to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        command: Some(flow_command::Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                flow_id: flow_id.to_owned(),
                stop_type: 2,
                ..StopFlowRequest::default()
            }),
        })),
        ..FlowCommand::default()
    }
}

fn attribute_wait(
    flow_id: &str,
    operator: AttributeMatchOperator,
    operand: i64,
) -> WaitForAttributeRequest {
    WaitForAttributeRequest {
        flow_id: flow_id.to_owned(),
        r#match: Some(AttributeMatch {
            key: "count".to_owned(),
            operator: operator as i32,
            operand: Some(int_value(operand)),
        }),
        ..WaitForAttributeRequest::default()
    }
}

async fn assert_pending<F: std::future::Future>(future: std::pin::Pin<&mut F>) {
    tokio::select! {
        _ = future => panic!("the long poll must still wait"),
        () = tokio::time::sleep(Duration::from_millis(10)) => {}
    }
}

#[tokio::test(start_paused = true)]
async fn wait_for_flow_wakes_only_on_the_committed_close() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    let wait = harness
        .registry
        .wait_for_flow(TENANT, wait_for_flow_request("order-1", 30));
    tokio::pin!(wait);
    assert_pending(wait.as_mut()).await;
    assert_eq!(harness.registry.long_poll_waiter_count(), 1);

    harness
        .registry
        .propose(
            flow("order-1"),
            stop_command("order-1", run_id),
            CommitUrgency::Batched,
        )
        .await
        .unwrap();
    assert_pending(wait.as_mut()).await;

    harness.commit_all();
    let result = wait.await.unwrap();
    assert_eq!(result.flow_status, FlowStatus::Terminated as i32);
    assert_eq!(harness.registry.long_poll_waiter_count(), 0);
}

#[tokio::test(start_paused = true)]
async fn wait_for_flow_returns_large_step_outputs_as_value_references() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    let context = harness.registry.flow_context(&flow("order-1"));
    let record_key = run_key(context.flow_hash, run_id, RunTable::RunRecord, &[]);
    let stored_record = harness
        .store
        .view()
        .get(ColumnFamily::Flow, &record_key)
        .unwrap()
        .unwrap();
    let mut record = RunRecord::decode(stored_record.as_slice()).unwrap();
    let value_id = ValueId {
        run_seq: 3,
        index_in_command: 0,
    };
    record.status = FlowStatus::Completed as i32;
    record.close = Some(FlowCloseRecord {
        results: vec![
            StoredStepCompletionOutput {
                completed_step_type: "SmallStep".to_owned(),
                completed_step_execution_id: "SmallStep-1".to_owned(),
                completed_step_output: Some(StoredValue {
                    kind: Some(stored_value::Kind::InlineValue(string_value("done"))),
                }),
            },
            StoredStepCompletionOutput {
                completed_step_type: "LargeStep".to_owned(),
                completed_step_execution_id: "LargeStep-1".to_owned(),
                completed_step_output: Some(StoredValue {
                    kind: Some(stored_value::Kind::TableValue(StoredValueReference {
                        value_id: Some(StoredValueId {
                            run_seq: value_id.run_seq,
                            index_in_command: value_id.index_in_command,
                        }),
                        size_bytes: 5 * 1024 * 1024,
                        is_string_value: true,
                    })),
                }),
            },
        ],
        ..FlowCloseRecord::default()
    });
    harness
        .store
        .put(ColumnFamily::Flow, record_key, record.encode_to_vec());

    let result = harness
        .registry
        .wait_for_flow(TENANT, wait_for_flow_request("order-1", 30))
        .await
        .unwrap();

    assert_eq!(result.flow_status, FlowStatus::Completed as i32);
    assert_eq!(
        result.results[0].completed_step_output,
        Some(string_value("done"))
    );
    assert_eq!(
        result.results[1].completed_step_output,
        Some(Value {
            kind: Some(value::Kind::InternalBlobIdForStringValue(
                ValueReference { run_id, value_id }.to_string()
            )),
        }),
        "the client loads large outputs with LoadBlobs"
    );
}

#[tokio::test(start_paused = true)]
async fn wait_for_flow_times_out_with_long_poll_time_out() {
    let harness = FlowCoordinationHarness::new();
    start_flow(&harness, "order-1").await.unwrap();
    let error = harness
        .registry
        .wait_for_flow(TENANT, wait_for_flow_request("order-1", 2))
        .await
        .unwrap_err();
    assert_eq!(error.grpc_code(), grpc_code::DEADLINE_EXCEEDED);
    assert_eq!(error.sub_status(), ErrorSubStatus::LongPollTimeOut);
    assert_eq!(error.to_string(), crate::WAIT_FOR_FLOW_TIMEOUT_MESSAGE);
    let error = harness
        .registry
        .wait_for_flow(TENANT, wait_for_flow_request("missing", 2))
        .await
        .unwrap_err();
    assert_eq!(error.sub_status(), ErrorSubStatus::FlowNotExists);
}

#[tokio::test(start_paused = true)]
async fn purging_run_is_not_found_by_each_wait_api_before_run_purge() {
    let harness = FlowCoordinationHarness::new();
    let closed_run = start_flow(&harness, "closed-flow").await.unwrap();
    let running_run = start_flow(&harness, "running-flow").await.unwrap();
    for (flow_id, run_id, status) in [
        ("closed-flow", closed_run, FlowStatus::Completed),
        ("running-flow", running_run, FlowStatus::Running),
    ] {
        let context = harness.registry.flow_context(&flow(flow_id));
        let record_key = run_key(context.flow_hash, run_id, RunTable::RunRecord, &[]);
        let stored_record = harness
            .store
            .view()
            .get(ColumnFamily::Flow, &record_key)
            .unwrap()
            .unwrap();
        let mut record = RunRecord::decode(stored_record.as_slice()).unwrap();
        record.status = status as i32;
        record.is_purging = true;
        harness
            .store
            .put(ColumnFamily::Flow, record_key, record.encode_to_vec());
    }
    let assert_not_found = |error: FlowCoordinationError| {
        assert_eq!(error.grpc_code(), grpc_code::NOT_FOUND, "{error:?}");
        assert_eq!(error.sub_status(), ErrorSubStatus::FlowNotExists);
    };
    assert_not_found(
        harness
            .registry
            .wait_for_flow(TENANT, wait_for_flow_request("closed-flow", 30))
            .await
            .unwrap_err(),
    );
    assert_not_found(
        harness
            .registry
            .wait_for_flow(
                TENANT,
                WaitForFlowRequest {
                    run_id: closed_run.to_string(),
                    ..wait_for_flow_request("closed-flow", 30)
                },
            )
            .await
            .unwrap_err(),
    );
    assert_not_found(
        harness
            .registry
            .wait_for_attribute(
                TENANT,
                attribute_wait("running-flow", AttributeMatchOperator::Equal, 1),
            )
            .await
            .unwrap_err(),
    );
    assert_not_found(
        harness
            .registry
            .wait_for_history_event(
                TENANT,
                WaitForHistoryEventRequest {
                    flow_id: "closed-flow".to_owned(),
                    run_id: closed_run.to_string(),
                    next_internal_event_id: 1,
                },
            )
            .await
            .unwrap_err(),
    );
}

#[tokio::test(start_paused = true)]
async fn waiting_polls_end_when_the_committed_run_starts_purging() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    let mut flow_wait = Box::pin(
        harness
            .registry
            .wait_for_flow(TENANT, wait_for_flow_request("order-1", 30)),
    );
    let mut attribute_wait = Box::pin(harness.registry.wait_for_attribute(
        TENANT,
        attribute_wait("order-1", AttributeMatchOperator::Equal, 1),
    ));
    let mut history_wait = Box::pin(harness.registry.wait_for_history_event(
        TENANT,
        WaitForHistoryEventRequest {
            flow_id: "order-1".to_owned(),
            run_id: run_id.to_string(),
            next_internal_event_id: 999,
        },
    ));
    assert_pending(flow_wait.as_mut()).await;
    assert_pending(attribute_wait.as_mut()).await;
    assert_pending(history_wait.as_mut()).await;
    assert_eq!(harness.registry.long_poll_waiter_count(), 3);

    let address = flow("order-1");
    let context = harness.registry.flow_context(&address);
    let record_key = run_key(context.flow_hash, run_id, RunTable::RunRecord, &[]);
    let stored_record = harness
        .store
        .view()
        .get(ColumnFamily::Flow, &record_key)
        .unwrap()
        .unwrap();
    let mut record = RunRecord::decode(stored_record.as_slice()).unwrap();
    record.status = FlowStatus::Completed as i32;
    record.is_purging = true;
    let encoded_record = record.encode_to_vec();
    harness.store.put(
        ColumnFamily::Flow,
        record_key.clone(),
        encoded_record.clone(),
    );
    harness.registry.apply_committed(CommittedFlowCommand {
        command: FlowCommand {
            tenant_id: TENANT.0,
            flow_id: "order-1".to_owned(),
            run_id: run_id.as_bytes().to_vec(),
            ..FlowCommand::default()
        },
        context,
        committed: CommittedCommand {
            outcome: ApplyOutcome {
                writes: vec![StateWrite::Put {
                    column_family: ColumnFamily::Flow,
                    key: record_key,
                    value: encoded_record,
                }],
                effects: Vec::new(),
                reply: Default::default(),
                run_id: Some(run_id),
            },
            position: Default::default(),
        },
    });
    let (flow_result, attribute_result, history_result) =
        tokio::time::timeout(Duration::from_millis(100), async {
            tokio::join!(flow_wait, attribute_wait, history_wait)
        })
        .await
        .expect("all registered polls wake promptly when the run starts purging");
    for error in [
        flow_result.unwrap_err(),
        attribute_result.unwrap_err(),
        history_result.unwrap_err(),
    ] {
        assert_eq!(error.grpc_code(), grpc_code::NOT_FOUND);
        assert_eq!(error.sub_status(), ErrorSubStatus::FlowNotExists);
    }
    assert_eq!(harness.registry.long_poll_waiter_count(), 0);
}

#[tokio::test(start_paused = true)]
async fn wait_for_attribute_returns_the_first_committed_match() {
    let harness = FlowCoordinationHarness::new();
    start_flow(&harness, "order-1").await.unwrap();
    let wait = harness.registry.wait_for_attribute(
        TENANT,
        attribute_wait("order-1", AttributeMatchOperator::GreaterThanOrEqual, 5),
    );
    tokio::pin!(wait);
    assert_pending(wait.as_mut()).await;

    harness
        .drive(harness.registry.set_attributes(
            TENANT,
            set_attribute_request("order-1", "count", int_value(3)),
        ))
        .await
        .unwrap();
    assert_pending(wait.as_mut()).await;
    let mut write = set_attribute_request("order-1", "count", int_value(7));
    write.request_id = "second".to_owned();
    harness
        .drive(harness.registry.set_attributes(TENANT, write))
        .await
        .unwrap();
    let response = wait.await.unwrap();
    assert_eq!(response.matched_value, Some(int_value(7)));

    let mismatched_type = harness.registry.wait_for_attribute(
        TENANT,
        WaitForAttributeRequest {
            request_timeout_seconds: 1,
            ..attribute_wait("order-1", AttributeMatchOperator::Equal, 7)
        },
    );
    assert!(mismatched_type.await.is_ok());
}

#[tokio::test(start_paused = true)]
async fn wait_for_attribute_rejects_values_above_the_inline_threshold() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    let context = harness.registry.flow_context(&flow("order-1"));
    harness.store.put(
        ColumnFamily::Flow,
        run_key(context.flow_hash, run_id, RunTable::Attribute, b"count"),
        AttributeRecord {
            value: Some(StoredValue {
                kind: Some(stored_value::Kind::TableValue(
                    StoredValueReference::default(),
                )),
            }),
            ..AttributeRecord::default()
        }
        .encode_to_vec(),
    );
    let error = harness
        .registry
        .wait_for_attribute(
            TENANT,
            attribute_wait("order-1", AttributeMatchOperator::Equal, 1),
        )
        .await
        .unwrap_err();
    assert_eq!(error.grpc_code(), grpc_code::FAILED_PRECONDITION);

    harness
        .drive(harness.registry.set_attributes(
            TENANT,
            set_attribute_request("order-1", "count", string_value("text")),
        ))
        .await
        .unwrap();
    let error = harness
        .registry
        .wait_for_attribute(
            TENANT,
            WaitForAttributeRequest {
                request_timeout_seconds: 2,
                ..attribute_wait("order-1", AttributeMatchOperator::Equal, 1)
            },
        )
        .await
        .unwrap_err();
    assert_eq!(
        error.sub_status(),
        ErrorSubStatus::RequestTimeout,
        "a type mismatch keeps waiting until the request times out"
    );
}

#[tokio::test(start_paused = true)]
async fn wait_for_step_completion_distinguishes_request_and_call_timeouts() {
    let config = FlowCoordinationConfig {
        long_poll_default_wait: Duration::from_secs(5),
        ..FlowCoordinationConfig::default()
    };
    let harness = FlowCoordinationHarness::with_config(&config);
    start_flow(&harness, "order-1").await.unwrap();
    let request = |request_timeout_seconds| WaitForStepCompletionRequest {
        flow_id: "order-1".to_owned(),
        step_type: "Approve".to_owned(),
        step_execution_number: "1".to_owned(),
        request_timeout_seconds,
        ..WaitForStepCompletionRequest::default()
    };
    let error = harness
        .registry
        .wait_for_step_completion(TENANT, request(2))
        .await
        .unwrap_err();
    assert_eq!(error.sub_status(), ErrorSubStatus::RequestTimeout);
    assert_eq!(
        error.to_string(),
        crate::STEP_COMPLETION_REQUEST_TIMEOUT_MESSAGE
    );
    let error = harness
        .registry
        .wait_for_step_completion(TENANT, request(0))
        .await
        .unwrap_err();
    assert_eq!(error.sub_status(), ErrorSubStatus::LongPollTimeOut);
    assert_eq!(error.to_string(), crate::WAIT_CALL_DEADLINE_MESSAGE);
}

#[tokio::test(start_paused = true)]
async fn update_class_requests_fail_during_terminal_cleanup_while_wait_for_flow_waits() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    let mut flow_wait = Box::pin(
        harness
            .registry
            .wait_for_flow(TENANT, wait_for_flow_request("order-1", 30)),
    );
    let mut unmatched_wait = Box::pin(harness.registry.wait_for_attribute(
        TENANT,
        attribute_wait("order-1", AttributeMatchOperator::Equal, 1),
    ));
    let mut matched_attribute_wait = Box::pin(harness.registry.wait_for_attribute(
        TENANT,
        attribute_wait("order-1", AttributeMatchOperator::Equal, 7),
    ));
    let mut step_wait = Box::pin(
        harness
            .registry
            .wait_for_step_completion(TENANT, approve_step_request()),
    );
    assert_pending(flow_wait.as_mut()).await;
    assert_pending(unmatched_wait.as_mut()).await;
    assert_pending(matched_attribute_wait.as_mut()).await;
    assert_pending(step_wait.as_mut()).await;

    let context = harness.registry.flow_context(&flow("order-1"));
    // The deciding command wrote the Attribute that one admitted wait expects.
    let attribute_key = run_key(context.flow_hash, run_id, RunTable::Attribute, b"count");
    let encoded_attribute = AttributeRecord {
        value: Some(StoredValue {
            kind: Some(stored_value::Kind::InlineValue(int_value(7))),
        }),
        ..AttributeRecord::default()
    }
    .encode_to_vec();
    harness.store.put(
        ColumnFamily::Flow,
        attribute_key.clone(),
        encoded_attribute.clone(),
    );
    let record_key = run_key(context.flow_hash, run_id, RunTable::RunRecord, &[]);
    let stored_record = harness
        .store
        .view()
        .get(ColumnFamily::Flow, &record_key)
        .unwrap()
        .unwrap();
    let mut record = RunRecord::decode(stored_record.as_slice()).unwrap();
    record.pending_close = Some(PendingFlowClose {
        status: FlowStatus::Completed as i32,
        ..PendingFlowClose::default()
    });
    let encoded_record = record.encode_to_vec();
    harness.store.put(
        ColumnFamily::Flow,
        record_key.clone(),
        encoded_record.clone(),
    );
    harness.registry.apply_committed(CommittedFlowCommand {
        command: FlowCommand {
            tenant_id: TENANT.0,
            flow_id: "order-1".to_owned(),
            run_id: run_id.as_bytes().to_vec(),
            ..FlowCommand::default()
        },
        context,
        committed: CommittedCommand {
            outcome: ApplyOutcome {
                writes: vec![
                    StateWrite::Put {
                        column_family: ColumnFamily::Flow,
                        key: record_key,
                        value: encoded_record,
                    },
                    StateWrite::Put {
                        column_family: ColumnFamily::Flow,
                        key: attribute_key,
                        value: encoded_attribute,
                    },
                ],
                effects: vec![ApplyEffect::WakeWaiters {
                    run_id,
                    change: WaitableChange::FlowCleanupStarted,
                }],
                reply: Default::default(),
                run_id: Some(run_id),
            },
            position: Default::default(),
        },
    });
    let (attribute_result, matched_result, step_result) =
        tokio::time::timeout(Duration::from_millis(100), async {
            tokio::join!(unmatched_wait, matched_attribute_wait, step_wait)
        })
        .await
        .expect("waiting update-class polls wake when terminal cleanup starts");
    assert_eq!(
        matched_result
            .expect("an admitted wait still returns its match")
            .matched_value,
        Some(int_value(7))
    );
    let errors = [
        attribute_result.unwrap_err(),
        step_result.map(drop).unwrap_err(),
    ];
    for error in errors {
        assert_eq!(error.grpc_code(), grpc_code::FAILED_PRECONDITION);
        assert_eq!(error.sub_status(), ErrorSubStatus::FlowClosing);
        assert_eq!(error.to_string(), dexzd_apply::TERMINAL_CLEANUP_MESSAGE);
    }
    assert_pending(flow_wait.as_mut()).await;
    let new_wait = harness
        .registry
        .wait_for_attribute(
            TENANT,
            attribute_wait("order-1", AttributeMatchOperator::Equal, 7),
        )
        .await
        .expect_err("a new wait is rejected during terminal cleanup, even when it matches");
    assert_eq!(new_wait.grpc_code(), grpc_code::FAILED_PRECONDITION);
    assert_eq!(new_wait.sub_status(), ErrorSubStatus::FlowClosing);

    let rpc_error = harness
        .registry
        .invoke_rpc(
            TENANT,
            InvokeRpcRequest {
                flow_id: "order-1".to_owned(),
                rpc_name: "approveOrder".to_owned(),
                request_id: "rpc-1".to_owned(),
                is_transactional: true,
                ..InvokeRpcRequest::default()
            },
        )
        .await
        .unwrap_err();
    assert_eq!(rpc_error.grpc_code(), grpc_code::FAILED_PRECONDITION);
    assert_eq!(rpc_error.sub_status(), ErrorSubStatus::FlowClosing);
    assert!(
        harness.rpc_invoker.invocations.lock().unwrap().is_empty(),
        "the Worker is not called"
    );
}

fn put_approve_step(
    harness: &FlowCoordinationHarness,
    run_id: RunId,
    phase: StepExecutionPhase,
    completed_seq: u64,
) {
    let context = harness.registry.flow_context(&flow("order-1"));
    harness.store.put(
        ColumnFamily::Flow,
        run_key(context.flow_hash, run_id, RunTable::StepCounter, b"Approve"),
        StepCounterRecord {
            last_step_number: 1,
        }
        .encode_to_vec(),
    );
    harness.store.put(
        ColumnFamily::Flow,
        run_key(
            context.flow_hash,
            run_id,
            RunTable::StepExecution,
            b"Approve-1",
        ),
        StepExecutionRecord {
            step_execution_id: "Approve-1".to_owned(),
            phase: phase as i32,
            completed_seq,
            ..StepExecutionRecord::default()
        }
        .encode_to_vec(),
    );
}

/// Writes the history record at `seq`, optionally holding the FlowClosed event.
fn put_history_record(
    harness: &FlowCoordinationHarness,
    run_id: RunId,
    seq: u64,
    has_flow_closed: bool,
) {
    let context = harness.registry.flow_context(&flow("order-1"));
    let events = has_flow_closed
        .then(|| FlowHistoryEvent {
            payload: Some(flow_history_event::Payload::FlowClosed(
                FlowClosedHistoryEvent {
                    flow_status: FlowStatus::Terminated as i32,
                    ..FlowClosedHistoryEvent::default()
                },
            )),
            ..FlowHistoryEvent::default()
        })
        .into_iter()
        .collect();
    harness.store.put(
        ColumnFamily::History,
        history_record_key(context.flow_hash, run_id, seq),
        HistoryRecord {
            seq,
            events,
            ..HistoryRecord::default()
        }
        .encode_to_vec(),
    );
}

async fn wait_for_approve_step(
    harness: &FlowCoordinationHarness,
) -> Result<(), FlowCoordinationError> {
    harness
        .registry
        .wait_for_step_completion(TENANT, approve_step_request())
        .await
        .map(drop)
}

fn assert_closed_flow_error(result: Result<(), FlowCoordinationError>) {
    let error = result.unwrap_err();
    assert_eq!(error.grpc_code(), grpc_code::NOT_FOUND, "{error:?}");
    assert_eq!(error.sub_status(), ErrorSubStatus::FlowNotExists);
}

fn approve_step_request() -> WaitForStepCompletionRequest {
    WaitForStepCompletionRequest {
        flow_id: "order-1".to_owned(),
        step_type: "Approve".to_owned(),
        step_execution_number: "1".to_owned(),
        ..WaitForStepCompletionRequest::default()
    }
}

#[tokio::test(start_paused = true)]
async fn wait_for_step_completion_treats_completed_and_canceled_steps_as_completed() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    put_approve_step(&harness, run_id, StepExecutionPhase::Completed, 5);
    wait_for_approve_step(&harness).await.unwrap();
    put_approve_step(&harness, run_id, StepExecutionPhase::Canceled, 5);
    wait_for_approve_step(&harness).await.unwrap();
    put_approve_step(&harness, run_id, StepExecutionPhase::Failed, 5);
    assert_closed_flow_error(wait_for_approve_step(&harness).await);
}

#[tokio::test(start_paused = true)]
async fn wait_for_step_completion_reports_a_step_canceled_by_terminate_as_a_closed_flow() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    harness
        .drive(harness.registry.stop_flow(
            TENANT,
            StopFlowRequest {
                flow_id: "order-1".to_owned(),
                stop_type: StopType::Terminate as i32,
                ..StopFlowRequest::default()
            },
        ))
        .await
        .unwrap();
    put_approve_step(&harness, run_id, StepExecutionPhase::Canceled, 9);
    put_history_record(&harness, run_id, 9, true);
    assert_closed_flow_error(wait_for_approve_step(&harness).await);

    // A Step canceled by a decision before the terminate still counts as completed.
    put_history_record(&harness, run_id, 9, false);
    wait_for_approve_step(&harness).await.unwrap();
}

#[tokio::test(start_paused = true)]
async fn wait_for_step_completion_on_a_closed_flow_reports_a_missing_flow() {
    let harness = FlowCoordinationHarness::new();
    start_flow(&harness, "order-1").await.unwrap();
    let wait = harness
        .registry
        .wait_for_step_completion(TENANT, approve_step_request());
    tokio::pin!(wait);
    assert_pending(wait.as_mut()).await;
    harness
        .drive(harness.registry.stop_flow(
            TENANT,
            StopFlowRequest {
                flow_id: "order-1".to_owned(),
                ..StopFlowRequest::default()
            },
        ))
        .await
        .unwrap();
    assert_closed_flow_error(wait.await.map(drop));
}

#[tokio::test(start_paused = true)]
async fn wait_for_history_event_reports_available_events_and_closed_runs() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    let request = |next_internal_event_id| WaitForHistoryEventRequest {
        flow_id: "order-1".to_owned(),
        run_id: run_id.to_string(),
        next_internal_event_id,
    };
    let available = harness
        .registry
        .wait_for_history_event(TENANT, request(1))
        .await
        .unwrap();
    assert!(available.event_available);
    assert_eq!(available.available_internal_event_id, 1);

    let wait = harness.registry.wait_for_history_event(TENANT, request(5));
    tokio::pin!(wait);
    assert_pending(wait.as_mut()).await;
    harness
        .drive(harness.registry.stop_flow(
            TENANT,
            StopFlowRequest {
                flow_id: "order-1".to_owned(),
                ..StopFlowRequest::default()
            },
        ))
        .await
        .unwrap();
    let closed = wait.await.unwrap();
    assert!(!closed.event_available);
    assert_eq!(closed.flow_status, FlowStatus::Canceled as i32);
}

#[tokio::test(start_paused = true)]
async fn waiter_limits_reject_registrations_and_free_slots_on_drop() {
    let config = FlowCoordinationConfig {
        max_long_poll_waiters_per_flow: 1,
        max_long_poll_waiters_per_node: 2,
        ..FlowCoordinationConfig::default()
    };
    let harness = FlowCoordinationHarness::with_config(&config);
    start_flow(&harness, "order-1").await.unwrap();
    start_flow(&harness, "order-2").await.unwrap();
    start_flow(&harness, "order-3").await.unwrap();

    let first = harness
        .registry
        .wait_for_flow(TENANT, wait_for_flow_request("order-1", 30));
    tokio::pin!(first);
    assert_pending(first.as_mut()).await;
    let error = harness
        .registry
        .wait_for_flow(TENANT, wait_for_flow_request("order-1", 30))
        .await
        .unwrap_err();
    assert_eq!(error.grpc_code(), grpc_code::RESOURCE_EXHAUSTED);

    let mut second = Box::pin(
        harness
            .registry
            .wait_for_flow(TENANT, wait_for_flow_request("order-2", 30)),
    );
    assert_pending(second.as_mut()).await;
    let error = harness
        .registry
        .wait_for_flow(TENANT, wait_for_flow_request("order-3", 30))
        .await
        .unwrap_err();
    assert_eq!(
        error.grpc_code(),
        grpc_code::RESOURCE_EXHAUSTED,
        "node limit"
    );

    drop(second);
    assert_eq!(harness.registry.long_poll_waiter_count(), 1);
    let mut third = Box::pin(
        harness
            .registry
            .wait_for_flow(TENANT, wait_for_flow_request("order-3", 30)),
    );
    assert_pending(third.as_mut()).await;
}

#[tokio::test(start_paused = true)]
async fn closing_or_halting_ends_waiting_long_polls() {
    let harness = FlowCoordinationHarness::new();
    start_flow(&harness, "order-1").await.unwrap();
    let wait = harness
        .registry
        .wait_for_flow(TENANT, wait_for_flow_request("order-1", 30));
    tokio::pin!(wait);
    assert_pending(wait.as_mut()).await;
    harness.registry.close_long_polls();
    let error = wait.await.unwrap_err();
    assert!(
        matches!(error, FlowCoordinationError::Unavailable { .. }),
        "{error:?}"
    );

    let halted = FlowCoordinationHarness::new();
    start_flow(&halted, "order-1").await.unwrap();
    let wait = halted
        .registry
        .wait_for_flow(TENANT, wait_for_flow_request("order-1", 30));
    tokio::pin!(wait);
    assert_pending(wait.as_mut()).await;
    halted.registry.halt("test halt".to_owned());
    let error = wait.await.unwrap_err();
    assert!(
        matches!(error, FlowCoordinationError::NodeStopping { .. }),
        "{error:?}"
    );
}
