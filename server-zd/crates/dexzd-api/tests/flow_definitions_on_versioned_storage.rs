// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Flow definition uploads, reads, and listings over `AliyunOssObjectStore`.

use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::Duration;

use dexzd_api::{FlowDefinitions, FlowDefinitionsConfig};
use dexzd_clock::WallClock;
use dexzd_keyspace::TenantId;
use dexzd_object_store::{
    AliyunOssObjectStore, FaultAction, FaultInjector, InMemoryObjectStore,
    InMemoryObjectStoreOptions, ObjectStore, ObjectStoreOperation,
};
use dexzd_protocol::dex::{
    FlowDefinitionUpload, FlowDefinitionUploadOutcome, GetFlowDefinitionsRequest,
    ListFlowDefinitionsRequest, UploadFlowDefinitionsRequest,
};

const TYPES_PREFIX: &str = "registry/flow-definitions/0/types/";

struct FixedWallClock;

impl WallClock for FixedWallClock {
    fn now_unix_micros(&self) -> i64 {
        1_800_000_000_000_000
    }
}

/// Delays every physical call a few pseudo-random milliseconds, so racing uploads interleave.
struct InterleavingDelays(AtomicU64);

impl FaultInjector for InterleavingDelays {
    fn action(&self, _operation: ObjectStoreOperation, _key: &str) -> FaultAction {
        let call = self.0.fetch_add(1, Ordering::SeqCst);
        FaultAction::Delay(Duration::from_millis(call.wrapping_mul(2_654_435_761) % 17))
    }
}

fn physical_store(injector: Option<Arc<dyn FaultInjector>>) -> Arc<InMemoryObjectStore> {
    Arc::new(InMemoryObjectStore::with_options(
        InMemoryObjectStoreOptions {
            injector,
            clock: None,
            rejects_replace_if_etag_matches: true,
        },
    ))
}

fn flow_definitions(physical: &Arc<InMemoryObjectStore>) -> FlowDefinitions {
    let adapter: Arc<dyn ObjectStore> = Arc::new(AliyunOssObjectStore::new(
        Arc::clone(physical) as Arc<dyn ObjectStore>,
        std::time::Duration::from_secs(60),
        dexzd_metrics::NodeMetrics::unregistered(),
    ));
    FlowDefinitions::new(
        adapter,
        Arc::new(FixedWallClock),
        &FlowDefinitionsConfig::default(),
    )
}

fn document(flow_type: &str, revision: u32) -> Vec<u8> {
    format!(r#"{{"schemaVersion":"2.0","flow":{{"name":"{flow_type}"}},"revision":{revision}}}"#)
        .into_bytes()
}

fn upload(flow_type: &str, revision: u32, generated_at_seconds: i64) -> FlowDefinitionUpload {
    FlowDefinitionUpload {
        flow_type: flow_type.to_owned(),
        content: document(flow_type, revision),
        generated_at: Some(prost_types::Timestamp {
            seconds: generated_at_seconds,
            nanos: 0,
        }),
    }
}

fn request(definitions: Vec<FlowDefinitionUpload>, force: bool) -> UploadFlowDefinitionsRequest {
    UploadFlowDefinitionsRequest {
        definitions,
        uploader: "rust-test/1.0".to_owned(),
        force,
    }
}

async fn upload_outcome(
    definitions: &FlowDefinitions,
    upload: FlowDefinitionUpload,
    force: bool,
) -> FlowDefinitionUploadOutcome {
    definitions
        .upload_flow_definitions(TenantId::DEFAULT, request(vec![upload], force))
        .await
        .expect("the upload succeeds")
        .results[0]
        .outcome()
}

async fn stored_content(definitions: &FlowDefinitions, flow_type: &str) -> Vec<u8> {
    definitions
        .get_flow_definitions(
            TenantId::DEFAULT,
            GetFlowDefinitionsRequest {
                flow_types: vec![flow_type.to_owned()],
            },
        )
        .await
        .expect("the Get succeeds")
        .definitions[0]
        .content
        .clone()
}

async fn listed_flow_types(definitions: &FlowDefinitions, page_size: i32) -> Vec<String> {
    let mut listed = Vec::new();
    let mut next_page_token = String::new();
    loop {
        let page = definitions
            .list_flow_definitions(
                TenantId::DEFAULT,
                ListFlowDefinitionsRequest {
                    page_size,
                    next_page_token,
                },
            )
            .await
            .expect("the listing succeeds");
        listed.extend(page.flow_types);
        if page.next_page_token.is_empty() {
            return listed;
        }
        next_page_token = page.next_page_token;
    }
}

#[tokio::test]
async fn uploads_replace_definitions_whose_flow_types_contain_dots() {
    let physical = physical_store(None);
    let definitions = flow_definitions(&physical);
    for flow_type in ["Order_Flow-v1.2", "ends.v", "v", "a/b"] {
        assert_eq!(
            upload_outcome(&definitions, upload(flow_type, 1, 100), false).await,
            FlowDefinitionUploadOutcome::Created
        );
        assert_eq!(
            upload_outcome(&definitions, upload(flow_type, 1, 50), false).await,
            FlowDefinitionUploadOutcome::Unchanged
        );
        assert_eq!(
            upload_outcome(&definitions, upload(flow_type, 2, 99), false).await,
            FlowDefinitionUploadOutcome::KeptExisting
        );
        assert_eq!(
            upload_outcome(&definitions, upload(flow_type, 2, 101), false).await,
            FlowDefinitionUploadOutcome::Replaced
        );
        assert_eq!(
            stored_content(&definitions, flow_type).await,
            document(flow_type, 2)
        );
        assert_eq!(
            upload_outcome(&definitions, upload(flow_type, 3, 10), true).await,
            FlowDefinitionUploadOutcome::Replaced
        );
        assert_eq!(
            stored_content(&definitions, flow_type).await,
            document(flow_type, 3)
        );
    }
    let physical_listing = physical
        .list(&format!("{TYPES_PREFIX}Order_Flow-v1.2.v/"), None, 10)
        .await
        .expect("the physical listing succeeds");
    assert_eq!(physical_listing.objects.len(), 3, "each write is a version");
    let mut listed = listed_flow_types(&definitions, 3).await;
    listed.sort();
    assert_eq!(listed, ["Order_Flow-v1.2", "a/b", "ends.v", "v"]);
}

#[tokio::test]
async fn many_definitions_list_across_pages() {
    let physical = physical_store(None);
    let definitions = flow_definitions(&physical);
    let mut expected = Vec::new();
    for batch in 0..6 {
        let uploads: Vec<FlowDefinitionUpload> = (0..40)
            .map(|index| {
                let flow_type = format!("Flow.{batch}.{index}");
                expected.push(flow_type.clone());
                upload(&flow_type, 1, 1)
            })
            .collect();
        definitions
            .upload_flow_definitions(TenantId::DEFAULT, request(uploads, false))
            .await
            .expect("the upload succeeds");
    }
    expected.sort();
    let mut listed = listed_flow_types(&definitions, 17).await;
    listed.sort();
    assert_eq!(listed, expected);
}

#[tokio::test(start_paused = true)]
async fn racing_uploads_keep_the_newest_document() {
    let physical = physical_store(Some(Arc::new(InterleavingDelays(AtomicU64::new(0)))));
    let definitions = Arc::new(flow_definitions(&physical));
    let mut uploads = Vec::new();
    for revision in 1..=6 {
        let definitions = Arc::clone(&definitions);
        let racing_upload = tokio::spawn(async move {
            definitions
                .upload_flow_definitions(
                    TenantId::DEFAULT,
                    request(
                        vec![upload("Race.Flow", revision, i64::from(revision))],
                        false,
                    ),
                )
                .await
        });
        uploads.push(racing_upload);
    }
    for racing_upload in uploads {
        racing_upload
            .await
            .expect("the upload task finishes")
            .expect("the upload succeeds");
    }
    assert_eq!(
        stored_content(&definitions, "Race.Flow").await,
        document("Race.Flow", 6)
    );
}

#[tokio::test]
async fn percent_encoded_flow_types_never_alias_a_version_prefix() {
    let physical = physical_store(None);
    let definitions = flow_definitions(&physical);
    let flow_types = ["a.v/b", "a%2Fb", ".", "..", "中文.v/类型", "name%252F"];
    for flow_type in flow_types {
        assert_eq!(
            upload_outcome(&definitions, upload(flow_type, 1, 100), false).await,
            FlowDefinitionUploadOutcome::Created
        );
        assert_eq!(
            upload_outcome(&definitions, upload(flow_type, 2, 101), false).await,
            FlowDefinitionUploadOutcome::Replaced
        );
        assert_eq!(
            stored_content(&definitions, flow_type).await,
            document(flow_type, 2)
        );
    }
    let mut listed = listed_flow_types(&definitions, 1).await;
    listed.sort();
    let mut expected = flow_types.map(str::to_owned).to_vec();
    expected.sort();
    assert_eq!(listed, expected);
}
