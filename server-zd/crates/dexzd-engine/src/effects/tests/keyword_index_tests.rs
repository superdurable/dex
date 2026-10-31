// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::atomic::Ordering;
use std::sync::{Arc, Mutex};

use dexzd_protocol::dex::{
    AttributeWrite, ExecuteMethodFailurePolicy, FlowErrorType, FlowStatus, IndexConfig, IndexType,
    InvokeWaitForMethodResponse, StepOptions, WaitForMethodFailurePolicy,
};
use dexzd_protocol::dexzd::v1::{StepAttemptFailureKind, flow_command};
use dexzd_request_validation::{KeywordValueLimit, ValidationConfig};

use super::{
    DurableState, EffectHarness, is_attempt_failed, is_method_completed, settings, start_request,
    string_value,
};
use crate::effects::{EffectsConfig, MethodRequest, NodeMetaUploadLedger};

#[tokio::test(start_paused = true)]
async fn oversized_indexed_worker_results_fail_without_completion_or_retry() {
    for is_execute in [false, true] {
        for should_proceed in [false, true] {
            let harness = harness_with_keyword_limit();
            harness.rebuild_all_partitions().await;
            let mut request = start_request("keyword-result");
            if should_proceed {
                request.step_options = Some(StepOptions {
                    wait_for_failure_policy: WaitForMethodFailurePolicy::ProceedOnFailure as i32,
                    execute_failure_policy: ExecuteMethodFailurePolicy::ProceedToConfiguredStep
                        as i32,
                    execute_failure_proceed_step_type: "RecoverStep".to_owned(),
                    ..StepOptions::default()
                });
            }
            let run_id = harness.start_flow(request, settings());
            let dispatch = harness.dispatcher.next_dispatch().await;
            let completed_before = if is_execute {
                dispatch.complete_wait_for(InvokeWaitForMethodResponse::default());
                let execute = harness.dispatcher.next_dispatch().await;
                let mut response = super::dispatch_tests::force_complete();
                response.upsert_attributes = vec![oversized_keyword()];
                execute.complete_execute(response);
                1
            } else {
                dispatch.complete_wait_for(InvokeWaitForMethodResponse {
                    upsert_attributes: vec![oversized_keyword()],
                    ..Default::default()
                });
                0
            };
            harness.settle().await;
            let failures = harness
                .flow_effect_operations
                .commands_matching(is_attempt_failed);
            assert_eq!(failures.len(), 1);
            let Some(flow_command::Command::StepAttemptFailed(failure)) =
                &failures[0].command.command
            else {
                panic!("expected failure");
            };
            assert_eq!(failure.kind, StepAttemptFailureKind::ValueTooLarge as i32);
            assert!(failure.is_non_retryable);
            let expected_message = "indexed Attribute \"status\" has a Keyword value or KeywordArray element of 1025 bytes; maximum is 1024 bytes";
            assert_eq!(failure.server_detail, expected_message);
            assert_eq!(
                harness
                    .flow_effect_operations
                    .commands_matching(is_method_completed)
                    .len(),
                completed_before
            );
            if should_proceed {
                assert_eq!(
                    harness.run_record("keyword-result", run_id).status,
                    FlowStatus::Running as i32
                );
                let recovery = harness.dispatcher.next_dispatch().await;
                assert_eq!(recovery.request.flow_id, "keyword-result");
                match recovery.method_request() {
                    MethodRequest::WaitFor(request) if is_execute => {
                        assert_eq!(request.step_type, "RecoverStep")
                    }
                    MethodRequest::Execute(request) if !is_execute => {
                        assert_eq!(request.step_type, "ChargeStep");
                        let recovery_error = request
                            .context
                            .as_ref()
                            .unwrap()
                            .recovery_error
                            .as_ref()
                            .unwrap();
                        assert_eq!(recovery_error.detail, expected_message);
                        assert_eq!(recovery_error.error_type, "ValueTooLarge");
                    }
                    other => panic!("unexpected recovery: {other:?}"),
                }
            } else {
                let close = harness.run_record("keyword-result", run_id).close.unwrap();
                assert_eq!(close.error_type, FlowErrorType::WorkerApiFail as i32);
                assert_eq!(close.error_message, expected_message);
                assert_eq!(
                    harness.run_record("keyword-result", run_id).status,
                    FlowStatus::Failed as i32
                );
                assert_eq!(
                    harness.dispatcher.dispatch_count.load(Ordering::SeqCst),
                    if is_execute { 2 } else { 1 }
                );
            }
            harness.shutdown().await;
        }
    }
}

fn harness_with_keyword_limit() -> EffectHarness {
    let durable = DurableState::new();
    let ledger = Arc::new(NodeMetaUploadLedger::new(
        durable.local_store.clone(),
        Arc::new(Mutex::new(())),
    ));
    EffectHarness::with_validation(
        durable,
        EffectsConfig::default(),
        ledger,
        ValidationConfig {
            has_blob_store: true,
            keyword_value_limit: Some(KeywordValueLimit::postgres()),
            ..ValidationConfig::default()
        },
    )
}

fn oversized_keyword() -> AttributeWrite {
    AttributeWrite {
        key: "status".to_owned(),
        value: Some(string_value(&"x".repeat(1025))),
        index_config: Some(IndexConfig {
            enable: true,
            r#type: IndexType::Keyword as i32,
            ..Default::default()
        }),
        ..Default::default()
    }
}
