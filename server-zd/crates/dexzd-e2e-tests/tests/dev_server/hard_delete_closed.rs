// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_protocol::dex::{
    AttributeWrite, ErrorSubStatus, GetAttributesRequest, GetFlowStateRequest,
    GetFlowSummaryRequest, SetAttributesRequest, StepOptions, StopFlowRequest, StopType, Value,
    value,
};
use s3mock::{Durability, EtagMode, ListObjectsRequest, Store, StoreConfig};

use crate::harness::{DevServer, service_error, start_pull_flow};
use crate::string_value;

const FLOW_ID: &str = "closed-hard-delete-flow";

fn null_write(key: &str, is_hard_delete_on_null: bool) -> AttributeWrite {
    AttributeWrite {
        key: key.to_owned(),
        value: Some(Value {
            kind: Some(value::Kind::NullValue(0)),
        }),
        is_hard_delete_on_null,
        ..AttributeWrite::default()
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_closed_run_erases_only_hard_delete_attributes_and_deletes_the_object() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let started = start_pull_flow(
        &mut flow_client,
        FLOW_ID,
        "ClosedSecretFlow",
        "IdleStep",
        string_value("idle"),
        StepOptions::default(),
    )
    .await;
    flow_client
        .set_attributes(SetAttributesRequest {
            flow_id: FLOW_ID.to_owned(),
            run_id: started.run_id.clone(),
            attributes: vec![
                AttributeWrite {
                    key: "secret".to_owned(),
                    value: Some(string_value("credential")),
                    is_hard_delete_on_null: true,
                    ..AttributeWrite::default()
                },
                AttributeWrite {
                    key: "plain".to_owned(),
                    value: Some(string_value("visible")),
                    ..AttributeWrite::default()
                },
            ],
            request_id: "set-secret".to_owned(),
            ..SetAttributesRequest::default()
        })
        .await
        .expect("initial Attributes are stored");
    let before = flow_client
        .get_attributes(GetAttributesRequest {
            flow_id: FLOW_ID.to_owned(),
            run_id: started.run_id.clone(),
            all_keys: true,
            ..GetAttributesRequest::default()
        })
        .await
        .expect("GetAttributes loads the hard-delete object")
        .into_inner();
    assert_eq!(before.attributes.len(), 2);

    flow_client
        .stop_flow(StopFlowRequest {
            flow_id: FLOW_ID.to_owned(),
            run_id: started.run_id.clone(),
            stop_type: StopType::Terminate as i32,
            ..StopFlowRequest::default()
        })
        .await
        .expect("StopFlow closes the run");
    let summary_before = flow_client
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: FLOW_ID.to_owned(),
            run_id: started.run_id.clone(),
        })
        .await
        .expect("GetFlowSummary reads the closed run")
        .into_inner();

    for (index, attributes) in [
        vec![AttributeWrite {
            key: "plain".to_owned(),
            value: Some(string_value("changed")),
            ..AttributeWrite::default()
        }],
        vec![null_write("plain", false)],
        vec![null_write("secret", true), null_write("plain", false)],
    ]
    .into_iter()
    .enumerate()
    {
        let status = flow_client
            .set_attributes(SetAttributesRequest {
                flow_id: FLOW_ID.to_owned(),
                run_id: started.run_id.clone(),
                attributes,
                request_id: format!("closed-rejection-{index}"),
                ..SetAttributesRequest::default()
            })
            .await
            .expect_err("a closed run rejects other writes");
        assert_eq!(status.code(), tonic::Code::NotFound, "{status}");
        assert_eq!(
            service_error(&status).sub_status,
            ErrorSubStatus::FlowNotExists as i32
        );
    }

    let erase = SetAttributesRequest {
        flow_id: FLOW_ID.to_owned(),
        run_id: started.run_id.clone(),
        attributes: vec![null_write("secret", true)],
        request_id: "erase-secret".to_owned(),
        ..SetAttributesRequest::default()
    };
    flow_client
        .set_attributes(erase.clone())
        .await
        .expect("the closed run accepts a hard-delete erase");
    flow_client
        .set_attributes(erase)
        .await
        .expect("the same request ID returns the recorded reply");
    let after = flow_client
        .get_attributes(GetAttributesRequest {
            flow_id: FLOW_ID.to_owned(),
            run_id: started.run_id.clone(),
            all_keys: true,
            ..GetAttributesRequest::default()
        })
        .await
        .expect("GetAttributes reads the closed run")
        .into_inner();
    assert_eq!(after.attributes.len(), 1);
    assert_eq!(after.attributes[0].key, "plain");
    let state = flow_client
        .get_flow_state(GetFlowStateRequest {
            flow_id: FLOW_ID.to_owned(),
            run_id: started.run_id.clone(),
        })
        .await
        .expect("GetFlowState reads the closed run")
        .into_inner();
    assert_eq!(state.attributes.len(), 1);
    assert_eq!(state.attributes[0].key, "plain");
    let summary_after = flow_client
        .get_flow_summary(GetFlowSummaryRequest {
            flow_id: FLOW_ID.to_owned(),
            run_id: started.run_id,
        })
        .await
        .expect("GetFlowSummary reads the closed run")
        .into_inner();
    assert_eq!(summary_after.flow_status, summary_before.flow_status);
    assert_eq!(summary_after.close_time, summary_before.close_time);

    let data_directory = server.shutdown_retaining_data_directory().await;
    let store = Store::open(&StoreConfig {
        root_directory: data_directory.path().join("s3"),
        durability: Durability::ProcessCrashSafe,
        etag_mode: EtagMode::Md5,
    })
    .expect("the embedded object store reopens");
    let objects = store
        .list_objects(
            "dex-dev",
            &ListObjectsRequest {
                prefix: "hard-delete/".to_owned(),
                delimiter: String::new(),
                max_keys: 100,
                continuation_token: None,
                start_after: None,
            },
        )
        .expect("the hard-delete objects can be listed");
    assert!(objects.objects.is_empty(), "the secret object was deleted");
}
