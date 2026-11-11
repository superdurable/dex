// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;
use std::sync::atomic::{AtomicI64, AtomicU64, Ordering};
use std::time::Duration;

use bytes::Bytes;
use dexzd_api::{FlowDefinitions, FlowDefinitionsConfig};
use dexzd_clock::WallClock;
use dexzd_keyspace::TenantId;
use dexzd_object_store::{
    FaultAction, FaultInjector, InMemoryObjectStore, ObjectStore, ObjectStoreOperation,
    WriteCondition,
};
use dexzd_protocol::dex::{
    ErrorSubStatus, FlowDefinitionUpload, FlowDefinitionUploadOutcome, GetFlowDefinitionsRequest,
    ListFlowDefinitionsRequest, ServiceErrorResponse, UploadFlowDefinitionsRequest,
};
use dexzd_protocol::dexzd::v1::FlowDefinitionRecord;
use prost::Message;
use tonic::Code;

const NOW_UNIX_MICROS: i64 = 1_800_000_000_000_000;
const TYPES_PREFIX: &str = "registry/flow-definitions/0/types/";

struct FixedWallClock(AtomicI64);

impl WallClock for FixedWallClock {
    fn now_unix_micros(&self) -> i64 {
        self.0.load(Ordering::SeqCst)
    }
}

fn flow_definitions(store: Arc<InMemoryObjectStore>) -> FlowDefinitions {
    FlowDefinitions::new(
        store,
        Arc::new(FixedWallClock(AtomicI64::new(NOW_UNIX_MICROS))),
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

fn request(definitions: Vec<FlowDefinitionUpload>) -> UploadFlowDefinitionsRequest {
    UploadFlowDefinitionsRequest {
        definitions,
        uploader: "rust-test/1.0".to_owned(),
        force: false,
    }
}

async fn upload_outcome(
    definitions: &FlowDefinitions,
    upload: FlowDefinitionUpload,
) -> FlowDefinitionUploadOutcome {
    let response = definitions
        .upload_flow_definitions(TenantId::DEFAULT, request(vec![upload]))
        .await
        .expect("the upload succeeds");
    response.results[0].outcome()
}

async fn stored_content(definitions: &FlowDefinitions, flow_type: &str) -> Vec<u8> {
    let response = definitions
        .get_flow_definitions(
            TenantId::DEFAULT,
            GetFlowDefinitionsRequest {
                flow_types: vec![flow_type.to_owned()],
            },
        )
        .await
        .expect("the Get succeeds");
    response.definitions[0].content.clone()
}

#[tokio::test]
async fn replace_rule_writes_only_for_created_replaced_and_forced_uploads() {
    let store = Arc::new(InMemoryObjectStore::new());
    let definitions = flow_definitions(store.clone());
    let puts = || store.call_count(ObjectStoreOperation::Put, TYPES_PREFIX);

    assert_eq!(
        upload_outcome(&definitions, upload("OrderFlow", 1, 100)).await,
        FlowDefinitionUploadOutcome::Created
    );
    assert_eq!(puts(), 1);
    assert_eq!(
        upload_outcome(&definitions, upload("OrderFlow", 1, 50)).await,
        FlowDefinitionUploadOutcome::Unchanged
    );
    assert_eq!(
        upload_outcome(&definitions, upload("OrderFlow", 2, 100)).await,
        FlowDefinitionUploadOutcome::KeptExisting
    );
    assert_eq!(
        upload_outcome(&definitions, upload("OrderFlow", 2, 99)).await,
        FlowDefinitionUploadOutcome::KeptExisting
    );
    assert_eq!(puts(), 1, "UNCHANGED and KEPT_EXISTING write nothing");
    assert_eq!(
        upload_outcome(&definitions, upload("OrderFlow", 2, 101)).await,
        FlowDefinitionUploadOutcome::Replaced
    );
    assert_eq!(puts(), 2);
    assert_eq!(
        stored_content(&definitions, "OrderFlow").await,
        document("OrderFlow", 2)
    );

    let mut forced = request(vec![upload("OrderFlow", 1, 10)]);
    forced.force = true;
    let response = definitions
        .upload_flow_definitions(TenantId::DEFAULT, forced.clone())
        .await
        .unwrap();
    assert_eq!(
        response.results[0].outcome(),
        FlowDefinitionUploadOutcome::Replaced
    );
    assert_eq!(response.results[0].stored_generated_at.unwrap().seconds, 10);
    let response = definitions
        .upload_flow_definitions(TenantId::DEFAULT, forced)
        .await
        .unwrap();
    assert_eq!(
        response.results[0].outcome(),
        FlowDefinitionUploadOutcome::Unchanged
    );
    assert_eq!(puts(), 3);
}

#[tokio::test]
async fn kept_existing_reports_the_stored_digest_and_time() {
    let store = Arc::new(InMemoryObjectStore::new());
    let definitions = flow_definitions(store);
    let created = definitions
        .upload_flow_definitions(TenantId::DEFAULT, request(vec![upload("F", 1, 200)]))
        .await
        .unwrap();
    let kept = definitions
        .upload_flow_definitions(TenantId::DEFAULT, request(vec![upload("F", 2, 100)]))
        .await
        .unwrap();
    assert_eq!(
        kept.results[0].stored_digest,
        created.results[0].stored_digest
    );
    assert_eq!(kept.results[0].stored_generated_at.unwrap().seconds, 200);
    assert_eq!(created.results[0].stored_digest.len(), 64);
}

/// Delays the first reads of each key so that concurrent uploads read before any of them writes.
struct DelayFirstReads {
    reads: AtomicU64,
    delayed_reads: u64,
}

impl FaultInjector for DelayFirstReads {
    fn action(&self, operation: ObjectStoreOperation, _key: &str) -> FaultAction {
        if operation == ObjectStoreOperation::Get
            && self.reads.fetch_add(1, Ordering::SeqCst) < self.delayed_reads
        {
            return FaultAction::Delay(Duration::from_millis(10));
        }
        FaultAction::Proceed
    }
}

#[tokio::test(start_paused = true)]
async fn concurrent_uploads_retry_conflicts_and_keep_the_newest_document() {
    let store = Arc::new(InMemoryObjectStore::with_injector(Arc::new(
        DelayFirstReads {
            reads: AtomicU64::new(0),
            delayed_reads: 3,
        },
    )));
    let definitions = Arc::new(flow_definitions(store.clone()));
    let mut handles = Vec::new();
    for revision in 1..=3 {
        let definitions = definitions.clone();
        let handle = tokio::spawn(async move {
            definitions
                .upload_flow_definitions(
                    TenantId::DEFAULT,
                    request(vec![upload("RaceFlow", revision, i64::from(revision))]),
                )
                .await
        });
        handles.push(handle);
    }
    let mut outcomes = Vec::new();
    for handle in handles {
        let response = handle.await.unwrap().unwrap();
        outcomes.push(response.results[0].clone());
    }
    assert_eq!(
        stored_content(&definitions, "RaceFlow").await,
        document("RaceFlow", 3)
    );
    let newest = &outcomes[2];
    assert!(matches!(
        newest.outcome(),
        FlowDefinitionUploadOutcome::Created | FlowDefinitionUploadOutcome::Replaced
    ));
    for result in &outcomes {
        if result.outcome() == FlowDefinitionUploadOutcome::KeptExisting {
            assert!(result.stored_generated_at.unwrap().seconds > 0);
        }
    }
    assert!(
        store.call_count(ObjectStoreOperation::Put, TYPES_PREFIX) >= 2,
        "at least one writer lost a conditional write and retried"
    );
}

/// Applies the first write, then reports an unknown outcome.
struct FirstWriteOutcomeUnknown(AtomicU64);

impl FaultInjector for FirstWriteOutcomeUnknown {
    fn action(&self, operation: ObjectStoreOperation, _key: &str) -> FaultAction {
        if operation == ObjectStoreOperation::Put && self.0.fetch_add(1, Ordering::SeqCst) == 0 {
            return FaultAction::ApplyThenOutcomeUnknown;
        }
        FaultAction::Proceed
    }
}

#[tokio::test]
async fn an_unknown_write_outcome_is_resolved_by_reading_the_object() {
    let store = Arc::new(InMemoryObjectStore::with_injector(Arc::new(
        FirstWriteOutcomeUnknown(AtomicU64::new(0)),
    )));
    let definitions = flow_definitions(store.clone());
    assert_eq!(
        upload_outcome(&definitions, upload("F", 1, 1)).await,
        FlowDefinitionUploadOutcome::Created
    );
    assert_eq!(store.call_count(ObjectStoreOperation::Put, TYPES_PREFIX), 1);
}

#[tokio::test]
async fn list_pages_through_decoded_flow_types() {
    let store = Arc::new(InMemoryObjectStore::new());
    let definitions = flow_definitions(store.clone());
    let flow_types = ["a/b", "Alpha", "beta.v2", "é", "pct%2Fname", "."];
    definitions
        .upload_flow_definitions(
            TenantId::DEFAULT,
            request(
                flow_types
                    .iter()
                    .map(|flow_type| upload(flow_type, 1, 1))
                    .collect(),
            ),
        )
        .await
        .unwrap();
    assert!(
        store
            .get(&format!("{TYPES_PREFIX}a%2Fb"))
            .await
            .unwrap()
            .is_some()
    );

    let mut listed = Vec::new();
    let mut next_page_token = String::new();
    loop {
        let page = definitions
            .list_flow_definitions(
                TenantId::DEFAULT,
                ListFlowDefinitionsRequest {
                    page_size: 3,
                    next_page_token,
                },
            )
            .await
            .unwrap();
        assert!(page.flow_types.len() <= 3);
        listed.extend(page.flow_types);
        if page.next_page_token.is_empty() {
            break;
        }
        next_page_token = page.next_page_token;
    }
    listed.sort();
    let mut expected: Vec<String> = flow_types
        .iter()
        .map(|flow_type| (*flow_type).to_owned())
        .collect();
    expected.sort();
    assert_eq!(listed, expected);

    let error = definitions
        .list_flow_definitions(
            TenantId::DEFAULT,
            ListFlowDefinitionsRequest {
                page_size: 0,
                next_page_token: "../other".to_owned(),
            },
        )
        .await
        .unwrap_err()
        .into_status();
    assert_eq!(error.code(), Code::InvalidArgument);
}

#[tokio::test]
async fn get_omits_missing_types_and_rejects_oversized_responses() {
    let store = Arc::new(InMemoryObjectStore::new());
    let config = FlowDefinitionsConfig {
        max_response_bytes: 300,
        ..FlowDefinitionsConfig::default()
    };
    let definitions = FlowDefinitions::new(
        store,
        Arc::new(FixedWallClock(AtomicI64::new(NOW_UNIX_MICROS))),
        &config,
    );
    definitions
        .upload_flow_definitions(
            TenantId::DEFAULT,
            request(vec![upload("A", 1, 1), upload("B", 1, 1)]),
        )
        .await
        .unwrap();
    let response = definitions
        .get_flow_definitions(
            TenantId::DEFAULT,
            GetFlowDefinitionsRequest {
                flow_types: vec!["Missing".to_owned(), "A".to_owned(), "A".to_owned()],
            },
        )
        .await
        .unwrap();
    assert_eq!(response.definitions.len(), 1);
    let info = response.definitions[0].info.as_ref().unwrap();
    assert_eq!(info.flow_type, "A");
    assert_eq!(info.uploader, "rust-test/1.0");
    assert_eq!(
        info.uploaded_at.unwrap().seconds,
        NOW_UNIX_MICROS / 1_000_000
    );

    let status = definitions
        .get_flow_definitions(
            TenantId::DEFAULT,
            GetFlowDefinitionsRequest {
                flow_types: vec!["A".to_owned(), "B".to_owned()],
            },
        )
        .await
        .unwrap_err()
        .into_status();
    assert_eq!(status.code(), Code::ResourceExhausted);
    assert_eq!(
        sub_status(&status),
        ErrorSubStatus::FlowDefinitionResponseTooLarge
    );
}

#[tokio::test]
async fn invalid_uploads_store_nothing() {
    let store = Arc::new(InMemoryObjectStore::new());
    let definitions = flow_definitions(store.clone());
    let mut future = upload("Later", 1, 0);
    future.generated_at = Some(prost_types::Timestamp {
        seconds: NOW_UNIX_MICROS / 1_000_000 + 24 * 60 * 60 + 1,
        nanos: 0,
    });
    let mut mismatch = upload("Named", 1, 1);
    mismatch.flow_type = "Other".to_owned();
    let mut year_10000 = upload("Year10000", 1, 0);
    year_10000.generated_at = Some(prost_types::Timestamp {
        seconds: 253_402_300_800,
        nanos: 0,
    });
    let mut before_year_1 = upload("BeforeYear1", 1, 0);
    before_year_1.generated_at = Some(prost_types::Timestamp {
        seconds: -62_135_596_801,
        nanos: 0,
    });
    for definitions_batch in [
        vec![upload("Valid", 1, 1), year_10000],
        vec![upload("Valid", 1, 1), before_year_1],
        vec![upload("Valid", 1, 1), future],
        vec![upload("Valid", 1, 1), mismatch],
        vec![upload("Valid", 1, 1), upload("Valid", 2, 2)],
    ] {
        let status = definitions
            .upload_flow_definitions(TenantId::DEFAULT, request(definitions_batch))
            .await
            .unwrap_err()
            .into_status();
        assert_eq!(status.code(), Code::InvalidArgument);
        assert_eq!(sub_status(&status), ErrorSubStatus::FlowDefinitionInvalid);
    }
    assert_eq!(store.call_count(ObjectStoreOperation::Put, TYPES_PREFIX), 0);
}

#[derive(Clone, PartialEq, Message)]
struct StatusDetails {
    #[prost(int32, tag = "1")]
    code: i32,
    #[prost(string, tag = "2")]
    message: String,
    #[prost(message, repeated, tag = "3")]
    details: Vec<prost_types::Any>,
}

fn sub_status(status: &tonic::Status) -> ErrorSubStatus {
    let envelope = StatusDetails::decode(status.details()).expect("status details decode");
    ServiceErrorResponse::decode(envelope.details[0].value.as_slice())
        .expect("ServiceErrorResponse decodes")
        .sub_status()
}

fn config_with(change: impl FnOnce(&mut FlowDefinitionsConfig)) -> FlowDefinitionsConfig {
    let mut config = FlowDefinitionsConfig::default();
    change(&mut config);
    config
}

fn flow_definitions_with_config(
    store: Arc<InMemoryObjectStore>,
    config: &FlowDefinitionsConfig,
) -> FlowDefinitions {
    FlowDefinitions::new(
        store,
        Arc::new(FixedWallClock(AtomicI64::new(NOW_UNIX_MICROS))),
        config,
    )
}

async fn rewrite_stored_record(
    store: &InMemoryObjectStore,
    key: &str,
    change: impl FnOnce(&mut FlowDefinitionRecord),
) {
    let object = store
        .get(key)
        .await
        .expect("the object reads")
        .expect("the object exists");
    let mut record = FlowDefinitionRecord::decode(object.bytes).expect("the record decodes");
    change(&mut record);
    store
        .put(
            key,
            Bytes::from(record.encode_to_vec()),
            WriteCondition::Unconditional,
        )
        .await
        .expect("the object writes");
}

#[tokio::test]
async fn corrupt_records_fail_as_internal_instead_of_unchanged() {
    let store = Arc::new(InMemoryObjectStore::new());
    let definitions = flow_definitions(store.clone());
    definitions
        .upload_flow_definitions(
            TenantId::DEFAULT,
            request(vec![upload("WrongDigest", 1, 1), upload("WrongType", 1, 1)]),
        )
        .await
        .unwrap();
    rewrite_stored_record(&store, &format!("{TYPES_PREFIX}WrongDigest"), |record| {
        record.content = document("WrongDigest", 2);
    })
    .await;
    rewrite_stored_record(&store, &format!("{TYPES_PREFIX}WrongType"), |record| {
        record.flow_type = "OtherType".to_owned();
    })
    .await;
    for flow_type in ["WrongDigest", "WrongType"] {
        let status = definitions
            .upload_flow_definitions(TenantId::DEFAULT, request(vec![upload(flow_type, 1, 1)]))
            .await
            .unwrap_err()
            .into_status();
        assert_eq!(status.code(), Code::Internal, "{flow_type}: {status:?}");
        assert_eq!(sub_status(&status), ErrorSubStatus::Uncategorized);
        let status = definitions
            .get_flow_definitions(
                TenantId::DEFAULT,
                GetFlowDefinitionsRequest {
                    flow_types: vec![flow_type.to_owned()],
                },
            )
            .await
            .unwrap_err()
            .into_status();
        assert_eq!(status.code(), Code::Internal, "{flow_type}: {status:?}");
    }
}

#[tokio::test]
async fn flow_types_whose_full_key_is_too_long_are_rejected_before_writing() {
    let store = Arc::new(InMemoryObjectStore::new());
    // 900 prefix bytes and the 34-byte registry path leave 90 bytes for the encoded Flow type.
    let config = config_with(|config| config.object_key_prefix_bytes = 900);
    let definitions = flow_definitions_with_config(store.clone(), &config);
    let fitting = "/".repeat(30);
    let response = definitions
        .upload_flow_definitions(TenantId::DEFAULT, request(vec![upload(&fitting, 1, 1)]))
        .await
        .unwrap();
    assert_eq!(
        response.results[0].outcome(),
        FlowDefinitionUploadOutcome::Created
    );
    let too_long = "/".repeat(31);
    let status = definitions
        .upload_flow_definitions(TenantId::DEFAULT, request(vec![upload(&too_long, 1, 1)]))
        .await
        .unwrap_err()
        .into_status();
    assert_eq!(status.code(), Code::InvalidArgument);
    assert_eq!(sub_status(&status), ErrorSubStatus::FlowDefinitionInvalid);
    let status = definitions
        .get_flow_definitions(
            TenantId::DEFAULT,
            GetFlowDefinitionsRequest {
                flow_types: vec![too_long],
            },
        )
        .await
        .unwrap_err()
        .into_status();
    assert_eq!(status.code(), Code::InvalidArgument);
    assert_eq!(store.call_count(ObjectStoreOperation::Put, TYPES_PREFIX), 1);
}

#[tokio::test]
async fn list_skips_foreign_and_non_canonical_keys_on_every_page() {
    let store = Arc::new(InMemoryObjectStore::new());
    let definitions = flow_definitions(store.clone());
    definitions
        .upload_flow_definitions(
            TenantId::DEFAULT,
            request(vec![upload("Alpha", 1, 1), upload("zeta", 1, 1)]),
        )
        .await
        .unwrap();
    // "foo/bar" sorts after "Alpha", so with one key per page it ends a page.
    for foreign in ["%", "%FF", "%41", "foo/bar", "foo/zzz"] {
        store
            .put(
                &format!("{TYPES_PREFIX}{foreign}"),
                Bytes::from_static(b"foreign"),
                WriteCondition::Unconditional,
            )
            .await
            .unwrap();
    }
    for page_size in [1, 2, 1000] {
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
                .unwrap();
            listed.extend(page.flow_types);
            if page.next_page_token.is_empty() {
                break;
            }
            next_page_token = page.next_page_token;
        }
        assert_eq!(listed, vec!["Alpha".to_owned(), "zeta".to_owned()]);
    }
    for token in ["%", "%FF", "foo/bar", "not base64!"] {
        let status = definitions
            .list_flow_definitions(
                TenantId::DEFAULT,
                ListFlowDefinitionsRequest {
                    page_size: 1,
                    next_page_token: token.to_owned(),
                },
            )
            .await
            .unwrap_err()
            .into_status();
        assert_eq!(status.code(), Code::InvalidArgument, "{token}");
    }
}

#[tokio::test]
async fn get_stops_reading_once_the_response_budget_is_spent() {
    let store = Arc::new(InMemoryObjectStore::new());
    let writer = flow_definitions(store.clone());
    let mut large = upload("Large", 1, 1);
    large.content = format!(
        r#"{{"schemaVersion":"2.0","flow":{{"name":"Large"}},"pad":"{}"}}"#,
        "x".repeat(2_000)
    )
    .into_bytes();
    writer
        .upload_flow_definitions(
            TenantId::DEFAULT,
            request(vec![upload("Small", 1, 1), large, upload("Later", 1, 1)]),
        )
        .await
        .unwrap();
    let config = config_with(|config| config.max_response_bytes = 1_000);
    let reader = flow_definitions_with_config(store.clone(), &config);
    let reads_before = store.call_count(ObjectStoreOperation::Get, TYPES_PREFIX);
    let status = reader
        .get_flow_definitions(
            TenantId::DEFAULT,
            GetFlowDefinitionsRequest {
                flow_types: vec!["Small".to_owned(), "Large".to_owned(), "Later".to_owned()],
            },
        )
        .await
        .unwrap_err()
        .into_status();
    assert_eq!(status.code(), Code::ResourceExhausted);
    assert_eq!(
        sub_status(&status),
        ErrorSubStatus::FlowDefinitionResponseTooLarge
    );
    assert_eq!(
        store.call_count(ObjectStoreOperation::Get, TYPES_PREFIX) - reads_before,
        2,
        "the read of Later never starts"
    );
    let response = reader
        .get_flow_definitions(
            TenantId::DEFAULT,
            GetFlowDefinitionsRequest {
                flow_types: vec!["Small".to_owned(), "Later".to_owned()],
            },
        )
        .await
        .unwrap();
    assert_eq!(response.definitions.len(), 2);
}

#[tokio::test]
async fn list_pages_past_a_foreign_key_longer_than_any_flow_definition_key() {
    let store = Arc::new(InMemoryObjectStore::new());
    let definitions = flow_definitions(store.clone());
    definitions
        .upload_flow_definitions(
            TenantId::DEFAULT,
            request(vec![upload("Alpha", 1, 1), upload("zeta", 1, 1)]),
        )
        .await
        .unwrap();
    // "long/..." sorts between "Alpha" and "zeta", so it ends the second one-key page.
    let long_foreign_key = format!("{TYPES_PREFIX}long/{}", "x".repeat(2_000));
    store
        .put(
            &long_foreign_key,
            Bytes::from_static(b"foreign"),
            WriteCondition::Unconditional,
        )
        .await
        .unwrap();
    let mut listed = Vec::new();
    let mut next_page_token = String::new();
    let mut pages = 0;
    loop {
        let page = definitions
            .list_flow_definitions(
                TenantId::DEFAULT,
                ListFlowDefinitionsRequest {
                    page_size: 1,
                    next_page_token,
                },
            )
            .await
            .unwrap();
        pages += 1;
        listed.extend(page.flow_types);
        if page.next_page_token.is_empty() {
            break;
        }
        next_page_token = page.next_page_token;
    }
    assert_eq!(listed, vec!["Alpha".to_owned(), "zeta".to_owned()]);
    assert!(pages >= 3, "the long foreign key ended a page");
}

#[tokio::test]
async fn a_stored_object_above_the_read_cap_fails_upload_as_internal() {
    let store = Arc::new(InMemoryObjectStore::new());
    let writer = flow_definitions(store.clone());
    let mut large = upload("Large", 1, 1);
    large.content = format!(
        r#"{{"schemaVersion":"2.0","flow":{{"name":"Large"}},"pad":"{}"}}"#,
        "x".repeat(2_000)
    )
    .into_bytes();
    writer
        .upload_flow_definitions(TenantId::DEFAULT, request(vec![large]))
        .await
        .unwrap();
    let config = config_with(|config| config.max_response_bytes = 1_000);
    let reader = flow_definitions_with_config(store.clone(), &config);
    let status = reader
        .upload_flow_definitions(TenantId::DEFAULT, request(vec![upload("Large", 2, 2)]))
        .await
        .unwrap_err()
        .into_status();
    assert_eq!(status.code(), Code::Internal, "{status:?}");
    let status = reader
        .get_flow_definitions(
            TenantId::DEFAULT,
            GetFlowDefinitionsRequest {
                flow_types: vec!["Large".to_owned()],
            },
        )
        .await
        .unwrap_err()
        .into_status();
    assert_eq!(status.code(), Code::ResourceExhausted);
    assert_eq!(
        sub_status(&status),
        ErrorSubStatus::FlowDefinitionResponseTooLarge
    );
}
