// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_protocol::dex::{
    self, AttributeWrite, CloseDecision, CloseDecisionType, EncodedObject, ErrorSubStatus,
    FlowConfig, FlowStartOptions, IndexConfig, IndexType, InvokeExecuteMethodResponse,
    InvokeWaitForMethodResponse, InvokeWorkerRpcResponse, SetAttributesRequest, StartFlowRequest,
    StepDecision, SubFlowCondition, SubFlowOptions, Value, WaitingCondition, WaitingConditionType,
    WorkerTarget, value,
};
use dexzd_request_validation::{
    ClientRequest, KeywordValueLimit, ValidationConfig, ValidationError, WorkerResponse,
    validate_client_request, validate_worker_response,
};

#[derive(Debug, Clone, Copy)]
enum Ingress {
    StartFlow,
    SetAttributes,
    WaitFor,
    Execute,
    Rpc,
    SubFlow,
}

const INGRESSES: [Ingress; 6] = [
    Ingress::StartFlow,
    Ingress::SetAttributes,
    Ingress::WaitFor,
    Ingress::Execute,
    Ingress::Rpc,
    Ingress::SubFlow,
];

#[test]
fn every_attribute_ingress_enforces_boundaries_only_when_enabled() {
    for ingress in INGRESSES {
        for index_type in [
            IndexType::Keyword,
            IndexType::Unspecified,
            IndexType::KeywordArray,
        ] {
            for byte_count in [1024, 1025] {
                for limit in [None, Some(KeywordValueLimit::postgres())] {
                    let config = ValidationConfig {
                        keyword_value_limit: limit,
                        ..validation_config()
                    };
                    let write = indexed_write(byte_count, index_type);
                    let result = validate_ingress(ingress, write, &config);
                    if byte_count > 1024 && limit.is_some() {
                        let error = result.unwrap_err();
                        assert_eq!(
                            error.sub_status,
                            ErrorSubStatus::IndexedKeywordTooLong,
                            "{ingress:?} {index_type:?}"
                        );
                        assert!(error.message.contains("1025 bytes"));
                    } else {
                        result.unwrap_or_else(|error| {
                            panic!("{ingress:?} {index_type:?}: {error:?}")
                        });
                    }
                }
            }
        }
    }
}

#[test]
fn every_ingress_ignores_non_indexed_values_and_text_indexes() {
    let config = ValidationConfig {
        keyword_value_limit: Some(KeywordValueLimit::postgres()),
        ..validation_config()
    };
    for ingress in INGRESSES {
        let mut write = indexed_write(1025, IndexType::Keyword);
        write.index_config = None;
        validate_ingress(ingress, write, &config).unwrap();
        let mut write = indexed_write(1025, IndexType::Keyword);
        write.index_config.as_mut().unwrap().enable = false;
        validate_ingress(ingress, write, &config).unwrap();
        validate_ingress(ingress, indexed_write(1025, IndexType::Text), &config).unwrap();
    }
}

#[test]
fn keyword_arrays_measure_decoded_elements_and_keywords_measure_utf8_bytes() {
    let config = ValidationConfig {
        keyword_value_limit: Some(KeywordValueLimit::postgres()),
        ..validation_config()
    };
    for ingress in INGRESSES {
        for index_type in [
            IndexType::Keyword,
            IndexType::Unspecified,
            IndexType::KeywordArray,
        ] {
            let mut write = indexed_write(0, index_type);
            let text = "é".repeat(512);
            write.value = Some(Value {
                kind: Some(value::Kind::StringValue(
                    if index_type == IndexType::KeywordArray {
                        format!("[\"{}\",\"{}\"]", "\\u00e9".repeat(512), "x".repeat(1024))
                    } else {
                        text.clone()
                    },
                )),
            });
            validate_ingress(ingress, write.clone(), &config).unwrap();
            write.value = Some(Value {
                kind: Some(value::Kind::ObjValue(EncodedObject {
                    encoding: "json".to_owned(),
                    payload: if index_type == IndexType::KeywordArray {
                        serde_json::to_vec(&[format!("{text}x")]).unwrap()
                    } else {
                        format!("{text}x").into_bytes()
                    },
                })),
            });
            assert_eq!(
                validate_ingress(ingress, write, &config)
                    .unwrap_err()
                    .sub_status,
                ErrorSubStatus::IndexedKeywordTooLong
            );
        }
        let mut write = indexed_write(0, IndexType::KeywordArray);
        write.value = Some(Value {
            kind: Some(value::Kind::StringValue(
                serde_json::to_string(&vec!["x".repeat(1024); 4]).unwrap(),
            )),
        });
        validate_ingress(ingress, write, &config).unwrap();
    }
}

#[test]
fn every_ingress_accepts_keyword_values_that_apply_does_not_index() {
    let config = ValidationConfig {
        keyword_value_limit: Some(KeywordValueLimit::postgres()),
        ..validation_config()
    };
    for ingress in INGRESSES {
        for index_type in [
            IndexType::Keyword,
            IndexType::Unspecified,
            IndexType::KeywordArray,
        ] {
            validate_ingress(ingress, indexed_write(300 * 1024, index_type), &config).unwrap();
            let mut write = indexed_write(0, index_type);
            write.value = Some(Value {
                kind: Some(value::Kind::ObjValue(EncodedObject {
                    encoding: "binary".to_owned(),
                    payload: vec![0xff; 1025],
                })),
            });
            validate_ingress(ingress, write, &config).unwrap();
        }
    }
}

fn validate_ingress(
    ingress: Ingress,
    write: AttributeWrite,
    config: &ValidationConfig,
) -> Result<(), ValidationError> {
    match ingress {
        Ingress::StartFlow => validate_client_request(
            ClientRequest::StartFlow(&StartFlowRequest {
                flow_id: "keyword-flow".to_owned(),
                flow_type: "KeywordFlow".to_owned(),
                request_id: "start".to_owned(),
                flow_start_options: Some(FlowStartOptions {
                    attributes: vec![write],
                    ..Default::default()
                }),
                ..Default::default()
            }),
            config,
        ),
        Ingress::SetAttributes => validate_client_request(
            ClientRequest::SetAttributes(&SetAttributesRequest {
                flow_id: "keyword-flow".to_owned(),
                request_id: "set".to_owned(),
                attributes: vec![write],
                ..Default::default()
            }),
            config,
        ),
        Ingress::WaitFor => validate_worker_response(
            WorkerResponse::WaitFor {
                response: Some(&InvokeWaitForMethodResponse {
                    upsert_attributes: vec![write],
                    ..Default::default()
                }),
                parent_flow_config: &config.default_flow_config,
            },
            config,
        ),
        Ingress::Execute => validate_worker_response(
            WorkerResponse::Execute(Some(&InvokeExecuteMethodResponse {
                upsert_attributes: vec![write],
                step_decision: Some(StepDecision {
                    close_decision: Some(CloseDecision {
                        close_decision_type: CloseDecisionType::ForceComplete as i32,
                        ..Default::default()
                    }),
                    ..Default::default()
                }),
                ..Default::default()
            })),
            config,
        ),
        Ingress::Rpc => validate_worker_response(
            WorkerResponse::Rpc(Some(&InvokeWorkerRpcResponse {
                upsert_attributes: vec![write],
                ..Default::default()
            })),
            config,
        ),
        Ingress::SubFlow => validate_worker_response(
            WorkerResponse::WaitFor {
                response: Some(&InvokeWaitForMethodResponse {
                    waiting_condition: Some(WaitingCondition {
                        waiting_condition_type: WaitingConditionType::AllCompleted as i32,
                        sub_flow_conditions: vec![SubFlowCondition {
                            sub_flow_type: "KeywordChildFlow".to_owned(),
                            start_step_type: "StartStep".to_owned(),
                            options: Some(SubFlowOptions {
                                attributes: vec![write],
                                ..Default::default()
                            }),
                            ..Default::default()
                        }],
                        ..Default::default()
                    }),
                    ..Default::default()
                }),
                parent_flow_config: &config.default_flow_config,
            },
            config,
        ),
    }
}

fn indexed_write(byte_count: usize, index_type: IndexType) -> AttributeWrite {
    let text = "x".repeat(byte_count);
    AttributeWrite {
        key: "status".to_owned(),
        value: Some(Value {
            kind: Some(value::Kind::StringValue(
                if index_type == IndexType::KeywordArray {
                    serde_json::to_string(&[text]).expect("string array serializes")
                } else {
                    text
                },
            )),
        }),
        index_config: Some(IndexConfig {
            enable: true,
            r#type: index_type as i32,
            ..dex::IndexConfig::default()
        }),
        ..AttributeWrite::default()
    }
}

fn validation_config() -> ValidationConfig {
    ValidationConfig {
        default_flow_config: FlowConfig {
            worker_target: Some(WorkerTarget { is_pull_mode: true }),
            ..ValidationConfig::default().default_flow_config
        },
        ..ValidationConfig::default()
    }
}
