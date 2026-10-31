// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_protocol::dex::{
    AttributeWrite, ErrorSubStatus, IndexConfig, IndexType, InvokeRpcRequest,
    InvokeWorkerRpcResponse,
};
use dexzd_request_validation::KeywordValueLimit;

use super::fixtures::{TENANT, set_attribute_request, start_flow, start_request, string_value};
use super::test_support::FlowCoordinationHarness;
use crate::flow_coordination::{FlowCoordinationConfig, WorkerRpcResult, grpc_code};

#[tokio::test(start_paused = true)]
async fn oversized_indexed_client_writes_return_invalid_argument_without_proposals() {
    let mut config = FlowCoordinationConfig::default();
    config.validation.keyword_value_limit = Some(KeywordValueLimit::postgres());
    let harness = FlowCoordinationHarness::with_config(&config);
    let mut start = start_request("keyword-client");
    start.flow_start_options.as_mut().unwrap().attributes = vec![oversized_keyword()];
    let error = harness
        .registry
        .start_flow(TENANT, start)
        .await
        .unwrap_err();
    assert_eq!(error.grpc_code(), grpc_code::INVALID_ARGUMENT);
    assert_eq!(error.sub_status(), ErrorSubStatus::IndexedKeywordTooLong);
    assert!(harness.submitted().is_empty());
    start_flow(&harness, "keyword-client").await.unwrap();
    let proposal_count = harness.submitted().len();
    let mut set = set_attribute_request("keyword-client", "status", string_value(""));
    set.attributes = vec![oversized_keyword()];
    let error = harness
        .registry
        .set_attributes(TENANT, set)
        .await
        .unwrap_err();
    assert_eq!(error.grpc_code(), grpc_code::INVALID_ARGUMENT);
    assert_eq!(error.sub_status(), ErrorSubStatus::IndexedKeywordTooLong);
    assert_eq!(harness.submitted().len(), proposal_count);
    for is_transactional in [false, true] {
        harness
            .rpc_invoker
            .results
            .lock()
            .unwrap()
            .push_back(Ok(WorkerRpcResult {
                response: InvokeWorkerRpcResponse {
                    upsert_attributes: vec![oversized_keyword()],
                    ..Default::default()
                },
                app_worker_id: "keyword-worker".to_owned(),
                pull_routing: None,
            }));
        let error = harness
            .drive(harness.registry.invoke_rpc(
                TENANT,
                InvokeRpcRequest {
                    flow_id: "keyword-client".to_owned(),
                    rpc_name: "updateStatus".to_owned(),
                    request_id: format!("rpc-{is_transactional}"),
                    is_transactional,
                    ..Default::default()
                },
            ))
            .await
            .unwrap_err();
        assert_eq!(error.grpc_code(), grpc_code::INVALID_ARGUMENT);
        assert_eq!(error.sub_status(), ErrorSubStatus::IndexedKeywordTooLong);
        assert_eq!(harness.submitted().len(), proposal_count);
    }
}

fn oversized_keyword() -> AttributeWrite {
    AttributeWrite {
        key: "status".to_owned(),
        value: Some(string_value(&"x".repeat(1025))),
        index_config: Some(IndexConfig {
            enable: true,
            r#type: IndexType::Unspecified as i32,
            ..Default::default()
        }),
        ..Default::default()
    }
}
