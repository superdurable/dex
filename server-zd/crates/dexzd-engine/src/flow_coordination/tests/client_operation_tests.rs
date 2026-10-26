// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::atomic::Ordering;

use dexzd_apply::{ApplyOutcome, read_model::get_search_document};
use dexzd_protocol::dex::{
    ActiveStepSearchMode, AttributeWrite, ChannelMessage, ChannelMessageDeletion, ErrorSubStatus,
    FlowConfig, FlowResetType, GetAttributesRequest, GetFlowStateRequest, InvokeRpcRequest,
    InvokeWorkerRpcResponse, PublishToChannelRequest, ResetFlowRequest, StepDecision,
    StepDurability, StepMovement, StopFlowRequest, WorkerErrorResponse, WorkerTarget,
};
use dexzd_protocol::dexzd::v1::flow_command;

use crate::flow_coordination::tests::fixtures::{
    TENANT, flow, set_attribute_request, start_flow, start_request, string_value,
};
use crate::flow_coordination::tests::model_applier::not_found_reply;
use crate::flow_coordination::tests::test_support::FlowCoordinationHarness;
use crate::flow_coordination::{
    FlowCoordinationConfig, FlowCoordinationError, RpcInvokeError, WorkerRpcResult, grpc_code,
};

#[tokio::test(start_paused = true)]
async fn start_flow_proposes_a_complete_start_command_and_returns_the_run_id() {
    let harness = FlowCoordinationHarness::new();
    let mut request = start_request("order-1");
    request.flow_timeout_seconds = 30;

    let response = harness
        .drive(harness.registry.start_flow(TENANT, request.clone()))
        .await
        .unwrap();

    let submitted = harness.submitted();
    assert_eq!(submitted.len(), 1);
    let Some(flow_command::Command::FlowStart(start)) = submitted[0].command.clone() else {
        panic!("expected FlowStart");
    };
    assert_eq!(
        dexzd_keyspace::RunId::from_slice(&start.new_run_id)
            .unwrap()
            .to_string(),
        response.run_id
    );
    assert!(submitted[0].run_id.is_empty());
    assert_eq!(start.request_fingerprint.len(), 16);
    assert_eq!(
        start.resolved_timeout_policy, 1,
        "positive timeout defaults to FAIL"
    );
    assert_eq!(
        start.worker_target,
        Some(WorkerTarget {
            is_pull_mode: true,
            ..WorkerTarget::default()
        })
    );
    assert_eq!(
        start.settings.unwrap().history_window_semantic_events,
        10_000
    );
    assert_eq!(start.request.unwrap(), request);
}

#[tokio::test(start_paused = true)]
async fn start_flow_persists_default_config_and_respects_explicit_overrides() {
    let mut config = FlowCoordinationConfig::default();
    let pull_target = WorkerTarget {
        is_pull_mode: true,
        ..WorkerTarget::default()
    };
    config.validation.default_flow_config.worker_target = Some(pull_target.clone());
    let expected_default = FlowConfig {
        active_step_search_mode: Some(ActiveStepSearchMode::Disabled as i32),
        continue_as_new_threshold: Some(100),
        step_durability: Some(StepDurability::Sync as i32),
        worker_target: Some(pull_target.clone()),
        ..FlowConfig::default()
    };
    let harness = FlowCoordinationHarness::with_config(&config);
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    let mut request = start_request("default-config");
    request.flow_start_options = None;
    harness
        .drive(harness.registry.start_flow(TENANT, request))
        .await
        .unwrap();
    let default_state = harness
        .registry
        .get_flow_state(
            TENANT,
            GetFlowStateRequest {
                flow_id: "default-config".to_owned(),
                ..GetFlowStateRequest::default()
            },
        )
        .await
        .unwrap();
    assert_eq!(default_state.flow_config, Some(expected_default.clone()));
    assert_eq!(default_state.active_step_executions.len(), 1);
    let submitted = harness.submitted();
    let Some(flow_command::Command::FlowStart(default_start)) = submitted[0].command.as_ref()
    else {
        panic!("expected FlowStart");
    };
    let default_run_id = dexzd_keyspace::RunId::from_slice(&default_start.new_run_id).unwrap();
    assert_eq!(default_start.effective_flow_config, Some(expected_default));
    let default_context = harness.registry.flow_context(&flow("default-config"));
    let default_document = get_search_document(
        &harness.store.view(),
        default_context.flow_hash,
        default_run_id,
    )
    .unwrap()
    .unwrap();
    assert!(default_document.active_step_types.is_empty());

    let mut overridden = start_request("overridden-config");
    overridden
        .flow_start_options
        .as_mut()
        .unwrap()
        .flow_config_override = Some(FlowConfig {
        active_step_search_mode: Some(ActiveStepSearchMode::EnabledForAll as i32),
        continue_as_new_threshold: Some(25),
        step_durability: Some(StepDurability::Async as i32),
        worker_target: Some(pull_target.clone()),
        ..FlowConfig::default()
    });
    harness
        .drive(harness.registry.start_flow(TENANT, overridden))
        .await
        .unwrap();
    let overridden_state = harness
        .registry
        .get_flow_state(
            TENANT,
            GetFlowStateRequest {
                flow_id: "overridden-config".to_owned(),
                ..GetFlowStateRequest::default()
            },
        )
        .await
        .unwrap();
    assert_eq!(
        overridden_state.flow_config,
        Some(FlowConfig {
            active_step_search_mode: Some(ActiveStepSearchMode::EnabledForAll as i32),
            continue_as_new_threshold: Some(25),
            step_durability: Some(StepDurability::Async as i32),
            worker_target: Some(pull_target),
            ..FlowConfig::default()
        })
    );
    let submitted = harness.submitted();
    let Some(flow_command::Command::FlowStart(overridden_start)) = submitted[1].command.as_ref()
    else {
        panic!("expected FlowStart");
    };
    let overridden_run_id =
        dexzd_keyspace::RunId::from_slice(&overridden_start.new_run_id).unwrap();
    let overridden_context = harness.registry.flow_context(&flow("overridden-config"));
    let overridden_document = get_search_document(
        &harness.store.view(),
        overridden_context.flow_hash,
        overridden_run_id,
    )
    .unwrap()
    .unwrap();
    assert_eq!(overridden_document.active_step_types, ["InitStep"]);
}

#[tokio::test(start_paused = true)]
async fn invalid_requests_are_rejected_before_proposing() {
    let harness = FlowCoordinationHarness::new();
    let mut request = start_request("order-1");
    request.request_id.clear();
    let error = harness
        .registry
        .start_flow(TENANT, request)
        .await
        .unwrap_err();
    assert_eq!(error.grpc_code(), grpc_code::INVALID_ARGUMENT);

    let mut push_target = start_request("order-2");
    push_target
        .flow_start_options
        .as_mut()
        .unwrap()
        .flow_config_override
        .as_mut()
        .unwrap()
        .worker_target = Some(WorkerTarget {
        address: "localhost:9000".to_owned(),
        ..WorkerTarget::default()
    });
    let error = harness
        .registry
        .start_flow(TENANT, push_target)
        .await
        .unwrap_err();
    assert_eq!(error.grpc_code(), grpc_code::INVALID_ARGUMENT);
    assert!(harness.submitted().is_empty());
}

#[tokio::test(start_paused = true)]
async fn writes_target_the_newest_run_and_a_missing_flow_is_not_found() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();

    harness
        .drive(harness.registry.set_attributes(
            TENANT,
            set_attribute_request("order-1", "status", string_value("paid")),
        ))
        .await
        .unwrap();
    let submitted = harness.submitted();
    assert_eq!(submitted[1].run_id, run_id.as_bytes().to_vec());
    let Some(flow_command::Command::AttributesSet(set)) = submitted[1].command.clone() else {
        panic!("expected AttributesSet");
    };
    assert_eq!(set.request_fingerprint.len(), 16);

    let error = harness
        .drive(harness.registry.set_attributes(
            TENANT,
            set_attribute_request("missing", "status", string_value("paid")),
        ))
        .await
        .unwrap_err();
    assert_eq!(error.grpc_code(), grpc_code::NOT_FOUND);
    assert_eq!(error.sub_status(), ErrorSubStatus::FlowNotExists);

    let error = harness
        .registry
        .stop_flow(
            TENANT,
            StopFlowRequest {
                flow_id: "order-1".to_owned(),
                run_id: "not-a-uuid".to_owned(),
                ..StopFlowRequest::default()
            },
        )
        .await
        .unwrap_err();
    assert_eq!(error.grpc_code(), grpc_code::INVALID_ARGUMENT);
}

#[tokio::test(start_paused = true)]
async fn error_replies_from_apply_become_flow_coordination_errors() {
    let harness = FlowCoordinationHarness::new();
    start_flow(&harness, "order-1").await.unwrap();
    for _ in 0..2 {
        harness
            .applier
            .scripted
            .lock()
            .unwrap()
            .push_back(Ok(ApplyOutcome::rejected(not_found_reply("order-1"))));
    }
    let error = harness
        .drive(harness.registry.stop_flow(
            TENANT,
            StopFlowRequest {
                flow_id: "order-1".to_owned(),
                ..StopFlowRequest::default()
            },
        ))
        .await
        .unwrap_err();
    assert_eq!(
        error,
        FlowCoordinationError::rejected(
            grpc_code::NOT_FOUND,
            ErrorSubStatus::FlowNotExists,
            "flow order-1 does not exist"
        )
    );
}

#[tokio::test(start_paused = true)]
async fn publish_replaces_message_ids_and_skips_empty_lists() {
    let harness = FlowCoordinationHarness::new();
    start_flow(&harness, "order-1").await.unwrap();
    harness
        .registry
        .publish_to_channel(
            TENANT,
            PublishToChannelRequest {
                flow_id: "never-started".to_owned(),
                ..PublishToChannelRequest::default()
            },
        )
        .await
        .unwrap();
    assert_eq!(
        harness.submitted().len(),
        1,
        "an empty publish proposes nothing"
    );

    harness
        .drive(harness.registry.publish_to_channel(
            TENANT,
            PublishToChannelRequest {
                flow_id: "order-1".to_owned(),
                messages: vec![ChannelMessage {
                    channel_name: "orders".to_owned(),
                    value: Some(string_value("hello")),
                    message_id: "client-chosen".to_owned(),
                }],
                ..PublishToChannelRequest::default()
            },
        ))
        .await
        .unwrap();
    let Some(flow_command::Command::ChannelPublish(publish)) =
        harness.submitted()[1].command.clone()
    else {
        panic!("expected ChannelPublish");
    };
    let message_id = &publish.request.unwrap().messages[0].message_id;
    assert_ne!(message_id, "client-chosen");
    assert_eq!(
        uuid::Uuid::parse_str(message_id).unwrap().get_version_num(),
        7
    );
}

#[tokio::test(start_paused = true)]
async fn reset_returns_the_unchanged_run_id() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    let response = harness
        .drive(harness.registry.reset_flow(
            TENANT,
            ResetFlowRequest {
                flow_id: "order-1".to_owned(),
                reset_type: 1,
                ..ResetFlowRequest::default()
            },
        ))
        .await
        .unwrap();
    assert_eq!(response.run_id, run_id.to_string());
    assert_eq!(response.run_attempt, 2);
}

#[tokio::test(start_paused = true)]
async fn reset_replays_a_request_id_reused_after_rpcs_evicted_it() {
    let mut config = FlowCoordinationConfig::default();
    config.run_settings.request_dedup_capacity = 2;
    let harness = FlowCoordinationHarness::with_config(&config);
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    start_flow(&harness, "order-1").await.unwrap();
    let set_status = |status: &str| {
        let mut request = set_attribute_request("order-1", "status", string_value(status));
        request.request_id = "set-status".to_owned();
        request
    };
    harness
        .drive(harness.registry.set_attributes(TENANT, set_status("paid")))
        .await
        .unwrap();
    harness
        .rpc_invoker
        .results
        .lock()
        .unwrap()
        .push_back(Ok(WorkerRpcResult {
            response: InvokeWorkerRpcResponse {
                upsert_attributes: vec![AttributeWrite {
                    key: "approved".to_owned(),
                    value: Some(string_value("yes")),
                    ..AttributeWrite::default()
                }],
                ..InvokeWorkerRpcResponse::default()
            },
            app_worker_id: "worker-1".to_owned(),
            pull_routing: None,
        }));
    let mut rpc = rpc_request("order-1");
    rpc.is_transactional = true;
    harness
        .drive(harness.registry.invoke_rpc(TENANT, rpc))
        .await
        .unwrap();
    // With the RPC's entry, this third request ID evicts set-status from the capacity of two.
    harness
        .drive(harness.registry.set_attributes(
            TENANT,
            set_attribute_request("order-1", "note", string_value("fragile")),
        ))
        .await
        .unwrap();
    harness
        .drive(
            harness
                .registry
                .set_attributes(TENANT, set_status("shipped")),
        )
        .await
        .unwrap();
    let reused_request_time = harness.submitted().last().unwrap().proposed_at_micros;

    let response = harness
        .drive(harness.registry.reset_flow(
            TENANT,
            ResetFlowRequest {
                flow_id: "order-1".to_owned(),
                reset_type: FlowResetType::HistoryEventTime as i32,
                history_event_time: (i128::from(reused_request_time) * 1000).to_string(),
                ..ResetFlowRequest::default()
            },
        ))
        .await
        .unwrap();

    assert_eq!(response.run_attempt, 2);
    let attributes = harness
        .registry
        .get_attributes(
            TENANT,
            GetAttributesRequest {
                flow_id: "order-1".to_owned(),
                all_keys: true,
                ..GetAttributesRequest::default()
            },
        )
        .await
        .unwrap()
        .attributes;
    let status = attributes
        .iter()
        .find(|attribute| attribute.key == "status")
        .unwrap();
    assert_eq!(status.value, Some(string_value("shipped")));
    assert_eq!(attributes.len(), 3, "{attributes:?}");
}

fn rpc_request(flow_id: &str) -> InvokeRpcRequest {
    InvokeRpcRequest {
        flow_id: flow_id.to_owned(),
        rpc_name: "getOrder".to_owned(),
        request_id: "rpc-1".to_owned(),
        ..InvokeRpcRequest::default()
    }
}

#[tokio::test(start_paused = true)]
async fn read_only_rpc_is_not_logged_and_sees_committed_attributes() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    harness
        .drive(harness.registry.set_attributes(
            TENANT,
            set_attribute_request("order-1", "status", string_value("paid")),
        ))
        .await
        .unwrap();
    harness
        .rpc_invoker
        .results
        .lock()
        .unwrap()
        .push_back(Ok(WorkerRpcResult {
            response: InvokeWorkerRpcResponse {
                output: Some(string_value("paid order")),
                ..InvokeWorkerRpcResponse::default()
            },
            app_worker_id: "worker-1".to_owned(),
            pull_routing: None,
        }));
    let submitted_before = harness.submitted().len();

    let response = harness
        .registry
        .invoke_rpc(TENANT, rpc_request("order-1"))
        .await
        .unwrap();
    assert_eq!(response.output, Some(string_value("paid order")));
    assert_eq!(harness.submitted().len(), submitted_before);

    let invocation = harness.rpc_invoker.invocations.lock().unwrap()[0].clone();
    assert_eq!(invocation.run_id, run_id);
    assert_eq!(invocation.flow_type, "OrderFlow");
    let request = invocation.request;
    assert_eq!(request.rpc_name, "getOrder");
    assert_eq!(request.context.unwrap().run_id, run_id.to_string());
    assert_eq!(request.attributes.len(), 1);
    assert_eq!(request.attributes[0].key, "status");
}

#[tokio::test(start_paused = true)]
async fn rpc_with_effects_commits_and_returns_the_committed_output() {
    let harness = FlowCoordinationHarness::new();
    let run_id = start_flow(&harness, "order-1").await.unwrap();
    harness
        .rpc_invoker
        .results
        .lock()
        .unwrap()
        .push_back(Ok(WorkerRpcResult {
            response: InvokeWorkerRpcResponse {
                output: Some(string_value("approved")),
                upsert_attributes: vec![AttributeWrite {
                    key: "approved".to_owned(),
                    value: Some(string_value("yes")),
                    ..AttributeWrite::default()
                }],
                ..InvokeWorkerRpcResponse::default()
            },
            app_worker_id: "worker-1".to_owned(),
            pull_routing: None,
        }));
    let mut request = rpc_request("order-1");
    request.is_transactional = true;
    let response = harness
        .drive(harness.registry.invoke_rpc(TENANT, request))
        .await
        .unwrap();
    assert_eq!(response.output, Some(string_value("approved")));
    let submitted = harness.submitted();
    let last = submitted.last().unwrap();
    assert_eq!(last.run_id, run_id.as_bytes().to_vec());
    assert_eq!(
        last.expected_run_attempt, 1,
        "transactional RPCs pin the attempt"
    );
    let Some(flow_command::Command::RpcCommitted(rpc)) = last.command.clone() else {
        panic!("expected RpcCommitted");
    };
    assert_eq!(rpc.app_worker_id, "worker-1");
    assert_eq!(rpc.request_fingerprint.len(), 16);
}

#[tokio::test(start_paused = true)]
async fn transactional_rpc_with_a_missing_deletion_commits_none_of_its_writes() {
    let harness = FlowCoordinationHarness::new();
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    start_flow(&harness, "order-1").await.unwrap();
    let response = InvokeWorkerRpcResponse {
        upsert_attributes: vec![AttributeWrite {
            key: "approved".to_owned(),
            value: Some(string_value("yes")),
            ..AttributeWrite::default()
        }],
        delete_from_channel: vec![ChannelMessageDeletion {
            channel_name: "orders".to_owned(),
            message_id: "missing".to_owned(),
        }],
        ..InvokeWorkerRpcResponse::default()
    };
    let worker_result = WorkerRpcResult {
        response,
        app_worker_id: "worker-1".to_owned(),
        pull_routing: None,
    };
    harness
        .rpc_invoker
        .results
        .lock()
        .unwrap()
        .extend([Ok(worker_result.clone()), Ok(worker_result)]);
    let mut transactional_request = rpc_request("order-1");
    transactional_request.is_transactional = true;

    let error = harness
        .drive(harness.registry.invoke_rpc(TENANT, transactional_request))
        .await
        .unwrap_err();

    assert_eq!(error.grpc_code(), grpc_code::NOT_FOUND, "{error:?}");
    assert_eq!(error.sub_status(), ErrorSubStatus::ChannelMessageNotFound);
    let Some(flow_command::Command::RpcCommitted(rpc)) =
        harness.submitted().last().cloned().unwrap().command
    else {
        panic!("expected RpcCommitted");
    };
    assert!(rpc.is_transactional);
    assert!(attribute_keys(&harness, "order-1").await.is_empty());

    let mut non_transactional_request = rpc_request("order-1");
    non_transactional_request.request_id = "rpc-2".to_owned();
    harness
        .drive(
            harness
                .registry
                .invoke_rpc(TENANT, non_transactional_request),
        )
        .await
        .unwrap();
    assert_eq!(
        attribute_keys(&harness, "order-1").await,
        vec!["approved".to_owned()],
        "a non-transactional RPC ignores the missing deletion"
    );
}

async fn attribute_keys(harness: &FlowCoordinationHarness, flow_id: &str) -> Vec<String> {
    harness
        .registry
        .get_attributes(
            TENANT,
            GetAttributesRequest {
                flow_id: flow_id.to_owned(),
                all_keys: true,
                ..GetAttributesRequest::default()
            },
        )
        .await
        .unwrap()
        .attributes
        .into_iter()
        .map(|attribute| attribute.key)
        .collect()
}

#[tokio::test(start_paused = true)]
async fn rpc_channel_publications_get_new_message_ids_before_proposing() {
    let harness = FlowCoordinationHarness::new();
    start_flow(&harness, "order-1").await.unwrap();
    let publication = |message_id: &str| ChannelMessage {
        channel_name: "orders".to_owned(),
        value: Some(string_value("shipped")),
        message_id: message_id.to_owned(),
    };
    harness
        .rpc_invoker
        .results
        .lock()
        .unwrap()
        .push_back(Ok(WorkerRpcResult {
            response: InvokeWorkerRpcResponse {
                publish_to_channel: vec![publication(""), publication("worker-chosen")],
                ..InvokeWorkerRpcResponse::default()
            },
            app_worker_id: "worker-1".to_owned(),
            pull_routing: None,
        }));

    harness
        .drive(harness.registry.invoke_rpc(TENANT, rpc_request("order-1")))
        .await
        .unwrap();

    let last = harness.submitted().last().cloned().unwrap();
    let Some(flow_command::Command::RpcCommitted(rpc)) = last.command else {
        panic!("expected RpcCommitted");
    };
    let message_ids: Vec<uuid::Uuid> = rpc
        .response
        .unwrap()
        .publish_to_channel
        .iter()
        .map(|message| message.message_id.parse().unwrap())
        .collect();
    assert_eq!(message_ids.len(), 2);
    assert!(
        message_ids
            .iter()
            .all(|message_id| message_id.get_version_num() == 7),
        "{message_ids:?}"
    );
    assert_ne!(message_ids[0], message_ids[1]);
}

#[tokio::test(start_paused = true)]
async fn worker_rpc_errors_map_to_client_errors() {
    let harness = FlowCoordinationHarness::new();
    start_flow(&harness, "order-1").await.unwrap();
    let worker_error = WorkerErrorResponse {
        detail: "order is locked".to_owned(),
        grpc_status: 9,
        ..WorkerErrorResponse::default()
    };
    harness.rpc_invoker.results.lock().unwrap().extend([
        Err(RpcInvokeError::WorkerError {
            response: worker_error.clone(),
        }),
        Err(RpcInvokeError::NoWorkerAvailable {
            detail: "no worker".to_owned(),
        }),
        Err(RpcInvokeError::DeadlineExceeded),
    ]);

    let error = harness
        .registry
        .invoke_rpc(TENANT, rpc_request("order-1"))
        .await
        .unwrap_err();
    assert_eq!(
        error,
        FlowCoordinationError::WorkerRpcFailed { worker_error }
    );
    assert_eq!(error.grpc_code(), grpc_code::FAILED_PRECONDITION);
    assert_eq!(error.sub_status(), ErrorSubStatus::WorkerApiError);

    let error = harness
        .registry
        .invoke_rpc(TENANT, rpc_request("order-1"))
        .await
        .unwrap_err();
    assert_eq!(error.grpc_code(), grpc_code::UNAVAILABLE);

    let error = harness
        .registry
        .invoke_rpc(TENANT, rpc_request("order-1"))
        .await
        .unwrap_err();
    assert_eq!(error.grpc_code(), grpc_code::DEADLINE_EXCEEDED);

    let error = harness
        .registry
        .invoke_rpc(TENANT, rpc_request("missing"))
        .await
        .unwrap_err();
    assert_eq!(error.sub_status(), ErrorSubStatus::FlowNotExists);
}

#[tokio::test(start_paused = true)]
async fn malformed_worker_rpc_step_types_return_go_internal_status_on_both_routes() {
    let harness = FlowCoordinationHarness::new();
    start_flow(&harness, "order-1").await.unwrap();
    for is_transactional in [false, true] {
        for (is_next_step, expected) in [
            (
                true,
                "next step at index 0: step type contains reserved character \"/\"",
            ),
            (
                false,
                "cancel step type at index 0: step type contains reserved character \":\"",
            ),
        ] {
            let decision = if is_next_step {
                StepDecision {
                    next_steps: vec![StepMovement {
                        step_type: "Next/Step".to_owned(),
                        ..StepMovement::default()
                    }],
                    ..StepDecision::default()
                }
            } else {
                StepDecision {
                    cancel_step_types: vec!["Old:Step".to_owned()],
                    ..StepDecision::default()
                }
            };
            harness
                .rpc_invoker
                .results
                .lock()
                .unwrap()
                .push_back(Ok(WorkerRpcResult {
                    response: InvokeWorkerRpcResponse {
                        step_decision: Some(decision),
                        ..InvokeWorkerRpcResponse::default()
                    },
                    app_worker_id: "worker-1".to_owned(),
                    pull_routing: None,
                }));
            let mut request = rpc_request("order-1");
            request.is_transactional = is_transactional;
            request.request_id = format!("rpc-{is_next_step}-{is_transactional}");
            let error = harness
                .registry
                .invoke_rpc(TENANT, request)
                .await
                .unwrap_err();
            assert_eq!(error.grpc_code(), grpc_code::INTERNAL, "{error:?}");
            assert_eq!(error.sub_status(), ErrorSubStatus::Uncategorized);
            assert_eq!(error.to_string(), expected);
        }
    }
    assert_eq!(harness.submitted().len(), 1);
}
