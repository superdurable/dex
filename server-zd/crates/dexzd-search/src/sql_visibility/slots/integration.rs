// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

#![cfg(test)]

use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use async_trait::async_trait;
use dexzd_keyspace::{PartitionId, RunId, TenantId};
use dexzd_postgres_connection::ConnectionSettings;
use dexzd_protocol::dex::{self, IndexType};
use dexzd_protocol::dexzd::v1::{
    AttributeIndexRegistration, AttributeIndexRegistry, LogPosition, SearchAttributeValue,
    SearchDocumentRecord,
};
use dexzd_visibility_query::{FieldRegistry, parse_and_check};
use prost::Message;
use tokio_postgres::{Client, NoTls};

use super::backfill::backfill_next_batch;
use super::registry::{initialize_attribute_slots, load_committed_attribute_slot_snapshot};
use super::{AttributeIndexRegistryReader, assign_attribute_slots};
use crate::sql_visibility::{
    SchemaError, SearchDocumentChange, SqlVisibilityProjector, SqlVisibilitySearchBackend,
    VisibilityDialect, ensure_schema_for_dialect, translate_query_for_dialect,
};
use crate::{SearchBackend, SearchError, SearchRequest};

struct RegistrySnapshot(AttributeIndexRegistry);

#[async_trait]
impl AttributeIndexRegistryReader for RegistrySnapshot {
    async fn read_attribute_index_registry(&self) -> Result<AttributeIndexRegistry, SearchError> {
        Ok(self.0.clone())
    }
}

fn registry() -> AttributeIndexRegistry {
    AttributeIndexRegistry {
        version: 1,
        registrations: [
            ("keyword", IndexType::Keyword),
            ("tags", IndexType::KeywordArray),
            ("text", IndexType::Text),
            ("number", IndexType::Int),
            ("double", IndexType::Double),
            ("datetime", IndexType::Datetime),
            ("flag", IndexType::Bool),
        ]
        .into_iter()
        .map(|(key, index_type)| AttributeIndexRegistration {
            tenant_id: 1,
            index_key: key.into(),
            index_type: index_type as i32,
            ..Default::default()
        })
        .collect(),
    }
}

async fn query_ids(
    client: &Client,
    fields: &FieldRegistry,
    assignments: &[super::AttributeSlot],
    input: &str,
) -> Vec<String> {
    let query = parse_and_check(input, fields).unwrap();
    let statement = translate_query_for_dialect(
        &query,
        "1",
        100_000,
        None,
        VisibilityDialect::Postgres,
        assignments,
    )
    .unwrap();
    client
        .query_typed(&statement.sql, &statement.typed_parameter_refs())
        .await
        .unwrap()
        .into_iter()
        .map(|row| row.get("run_id"))
        .collect()
}

#[tokio::test]
async fn postgres_backfill_plans_mixed_types_and_schema_isolation() {
    let Ok(database_url) = std::env::var("DEX_ZD_POSTGRES_URL") else {
        return;
    };
    let (mut client, connection) = tokio_postgres::connect(&database_url, NoTls).await.unwrap();
    let connection_task = tokio::spawn(connection);
    let nonce = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_nanos();
    let schema = format!("dexzd_slots_{}_{nonce}", std::process::id());
    client
        .batch_execute(&format!(
            "CREATE SCHEMA {schema}; SET search_path={schema},public"
        ))
        .await
        .unwrap();
    ensure_schema_for_dialect(&mut client, VisibilityDialect::Postgres)
        .await
        .unwrap();
    let mismatch = ensure_schema_for_dialect(&mut client, VisibilityDialect::ParadeDb)
        .await
        .unwrap_err();
    assert!(matches!(mismatch, SchemaError::DialectMismatch { .. }));
    let fresh_schema = format!("{schema}_fresh");
    client
        .batch_execute(&format!(
            "CREATE SCHEMA {fresh_schema}; SET search_path={fresh_schema},{schema},public"
        ))
        .await
        .unwrap();
    ensure_schema_for_dialect(&mut client, VisibilityDialect::Postgres)
        .await
        .unwrap();
    assert!(
        client
            .query_one(
                "SELECT to_regclass($1::text) IS NOT NULL",
                &[&format!("{fresh_schema}.dexzd_visibility_runs")]
            )
            .await
            .unwrap()
            .get::<_, bool>(0)
    );
    client
        .batch_execute(&format!(
            "SET search_path={schema},public; DROP SCHEMA {fresh_schema} CASCADE"
        ))
        .await
        .unwrap();
    let extensions: Vec<String> = client
        .query(
            "SELECT extname FROM pg_extension WHERE extname NOT IN ('plpgsql')",
            &[],
        )
        .await
        .unwrap()
        .iter()
        .map(|row| row.get(0))
        .collect();
    assert!(
        extensions.is_empty(),
        "vanilla PostgreSQL has no search extensions: {extensions:?}"
    );
    client.batch_execute("INSERT INTO dexzd_visibility_runs (tenant_id, partition_id, run_id, workflow_id, workflow_type, flow_type, execution_status, start_time_ns, version, sa_keyword, sa_text, sa_number, sa_number_types, sa_datetime, sa_bool) SELECT '1', position % 2, lpad(position::text, 8, '0'), 'flow-' || position, 'Engine', CASE WHEN position % 5000 = 0 THEN 'RareFlow' ELSE 'TypicalFlow' END, 'Running', position, 1, jsonb_build_object('keyword', CASE WHEN position % 5000 = 0 THEN 'rare' ELSE 'typical' END, 'tags', ARRAY[CASE WHEN position % 5000 = 0 THEN 'rare' ELSE 'typical' END]), jsonb_build_object('text', CASE WHEN position % 5000 = 0 THEN 'rare rust engineer' ELSE 'typical text' END), jsonb_build_object('number', position, 'double', position::double precision), jsonb_build_object('number', 4, 'double', 5), jsonb_build_object('datetime', position), jsonb_build_object('flag', position % 2 = 0) FROM generate_series(1, 20000) AS position").await.unwrap();
    let registry = registry();
    let assignments = assign_attribute_slots(&registry).unwrap();
    let mut fields = FieldRegistry::new();
    for registration in &registry.registrations {
        fields.insert(
            registration.index_key.clone(),
            IndexType::try_from(registration.index_type).unwrap(),
        );
    }
    let predicates = [
        "keyword = 'rare'",
        "keyword != 'rare'",
        "keyword IN ('rare', 'missing')",
        "keyword NOT IN ('rare')",
        "keyword STARTS_WITH 'ra'",
        "keyword IS NULL",
        "keyword IS NOT NULL",
        "number >= 19995",
        "number BETWEEN 19995 AND 20000",
        "tags = 'rare'",
        "tags != 'rare'",
        "tags IN ('rare')",
        "text = 'rust engineer'",
        "text != 'rust'",
        "text = '---'",
        "text IS NULL",
        "text IS NOT NULL",
        "flag = true",
        "datetime > '1970-01-01T00:00:00Z'",
    ];
    let mut before = Vec::new();
    for input in predicates {
        before.push(query_ids(&client, &fields, &[], input).await);
    }
    assert!(read_ready_slots(&mut client).await.unwrap().is_empty());
    client.execute("INSERT INTO dexzd_visibility_attribute_slots (tenant_id, index_key, slot_column, state) VALUES ('1', 'keyword', 'keyword_01', 'backfilling')", &[]).await.unwrap();
    assert!(read_ready_slots(&mut client).await.unwrap().is_empty());
    let mut configuration: tokio_postgres::Config = database_url.parse().unwrap();
    configuration.options(format!("-c search_path={schema},public"));
    let (mut second, second_driver) = configuration.connect(NoTls).await.unwrap();
    let second_task = tokio::spawn(second_driver);
    initialize_attribute_slots(&mut client, registry.version, &assignments, true)
        .await
        .unwrap();
    let first_backfill = finish_backfill(&mut client);
    let second_backfill = finish_backfill(&mut second);
    tokio::time::timeout(Duration::from_secs(90), async {
        tokio::join!(first_backfill, second_backfill);
    })
    .await
    .unwrap();
    let mut ready = read_ready_slots(&mut client).await.unwrap();
    let mut expected_assignments = assignments.clone();
    ready.sort_by(|left, right| left.index_key.cmp(&right.index_key));
    expected_assignments.sort_by(|left, right| left.index_key.cmp(&right.index_key));
    assert_eq!(ready, expected_assignments);
    for (input, expected) in predicates.into_iter().zip(before) {
        assert_eq!(
            query_ids(&client, &fields, &ready, input).await,
            expected,
            "{input}"
        );
    }
    client
        .batch_execute("ANALYZE dexzd_visibility_runs")
        .await
        .unwrap();
    for (input, index) in [
        ("FlowType = 'RareFlow'", "flow_type"),
        ("WorkflowId = 'flow-20000'", "workflow_id"),
        ("RunId = '00020000'", "runs_pkey"),
        ("keyword = 'rare'", "keyword_01"),
        ("number >= 19995", "int_01"),
        ("tags = 'rare'", "keyword_array_01"),
        ("text = 'rust engineer'", "text_terms_01"),
    ] {
        let query = parse_and_check(input, &fields).unwrap();
        let mut statement = translate_query_for_dialect(
            &query,
            "1",
            100,
            None,
            VisibilityDialect::Postgres,
            &ready,
        )
        .unwrap();
        statement.sql = format!("EXPLAIN (FORMAT TEXT) {}", statement.sql);
        let plan = client
            .query_typed(&statement.sql, &statement.typed_parameter_refs())
            .await
            .unwrap()
            .iter()
            .map(|row| row.get::<_, String>(0))
            .collect::<Vec<_>>()
            .join("\n");
        assert!(
            plan.contains(&if index == "runs_pkey" {
                "dexzd_visibility_runs_pkey".to_owned()
            } else {
                format!("dexzd_visibility_{index}_idx")
            }),
            "{input}: {plan}"
        );
    }
    let text_hashes = super::text_term_hashes("rust engineer");
    client
        .execute(
            "UPDATE dexzd_visibility_runs SET text_terms_01 = $1::text[] WHERE run_id = '00000001'",
            &[&text_hashes],
        )
        .await
        .unwrap();
    assert!(
        !query_ids(&client, &fields, &ready, "text = 'rust engineer'")
            .await
            .contains(&"00000001".into()),
        "hash candidates require the exact text recheck"
    );
    // Mixed stored types follow the builtin evaluator rather than SQL casts.
    client.batch_execute("UPDATE dexzd_visibility_runs SET sa_keyword = jsonb_build_object('keyword', ARRAY['red','blue']), sa_number = jsonb_build_object('number', 7, 'double', 9), int_01 = NULL, double_01 = NULL, keyword_01 = NULL WHERE run_id = '00000001'; UPDATE dexzd_visibility_attribute_slots SET state = 'backfilling', backfill_partition_id = NULL, backfill_after_run_id = ''").await.unwrap();
    finish_backfill(&mut client).await;
    for input in [
        "keyword = '[\"red\", \"blue\"]'",
        "keyword IS NOT NULL",
        "number = 7",
        "double = 9",
        "double > 8",
    ] {
        assert_eq!(
            query_ids(&client, &fields, &ready, input).await,
            query_ids(&client, &fields, &[], input).await,
            "{input}"
        );
    }
    client.batch_execute("UPDATE dexzd_visibility_runs SET sa_number = jsonb_build_object('number', 1.5), int_01 = NULL WHERE run_id = '00000001'; UPDATE dexzd_visibility_attribute_slots SET state = 'backfilling', backfill_partition_id = NULL, backfill_after_run_id = '' WHERE index_key = 'number'").await.unwrap();
    finish_backfill(&mut client).await;
    assert!(
        !query_ids(&client, &fields, &ready, "number = 2")
            .await
            .contains(&"00000001".into())
    );
    client.batch_execute("UPDATE dexzd_visibility_runs SET sa_keyword = jsonb_build_object('tags', 'rare'), keyword_01 = NULL, keyword_array_01 = NULL WHERE run_id = '00000001'; UPDATE dexzd_visibility_attribute_slots SET state = 'backfilling', backfill_partition_id = NULL, backfill_after_run_id = '' WHERE index_key IN ('keyword', 'tags')").await.unwrap();
    finish_backfill(&mut client).await;
    assert!(
        query_ids(&client, &fields, &ready, "tags = 'rare'")
            .await
            .contains(&"00000001".into())
    );
    for input in ["tags IS NULL", "tags IS NOT NULL"] {
        assert_eq!(
            query_ids(&client, &fields, &ready, input).await,
            query_ids(&client, &fields, &[], input).await
        );
    }
    verify_projection_skip(&configuration, registry).await;
    client
        .batch_execute(&format!("DROP SCHEMA {schema} CASCADE"))
        .await
        .unwrap();
    drop(second);
    second_task.await.unwrap().unwrap();
    drop(client);
    connection_task.await.unwrap().unwrap();
}

struct SkippedDocumentCount(AtomicUsize);

impl crate::sql_visibility::ProjectionMetricRecorder for SkippedDocumentCount {
    fn record_skipped_search_document(
        &self,
        _partition_id: PartitionId,
        _tenant_id: TenantId,
        _run_id: RunId,
    ) {
        self.0.fetch_add(1, Ordering::Relaxed);
    }
}

async fn verify_projection_skip(
    configuration: &tokio_postgres::Config,
    registry: AttributeIndexRegistry,
) {
    let backend = Arc::new(
        SqlVisibilitySearchBackend::connect_dialect_with_settings(
            &ConnectionSettings::from_config(configuration.clone()),
            1,
            None,
            Duration::from_secs(2),
            VisibilityDialect::Postgres,
            Some(Arc::new(RegistrySnapshot(registry))),
        )
        .await
        .unwrap(),
    );
    let metrics = Arc::new(SkippedDocumentCount(AtomicUsize::new(0)));
    let projector =
        SqlVisibilityProjector::new_with_metric_recorder(backend.clone(), metrics.clone());
    let mut previous = None;
    for (sequence, index_type, value, is_skipped) in [
        (1, IndexType::Keyword, "x".repeat(1025), true),
        (
            2,
            IndexType::KeywordArray,
            serde_json::to_string(&vec!["é".repeat(513)]).unwrap(),
            true,
        ),
        (3, IndexType::Keyword, "é".repeat(512), false),
    ] {
        let run_id = RunId([sequence as u8 + 22; 16]);
        let record = SearchDocumentRecord {
            version: 1,
            tenant_id: 1,
            run_id: run_id.as_bytes().to_vec(),
            flow_id: format!("keyword-boundary-{sequence}"),
            flow_type: "KeywordBoundaryFlow".into(),
            status: dex::FlowStatus::Running as i32,
            started_at_micros: 1,
            attributes: vec![SearchAttributeValue {
                index_key: if index_type == IndexType::Keyword {
                    "keyword"
                } else {
                    "tags"
                }
                .into(),
                index_type: index_type as i32,
                value: Some(dex::Value {
                    kind: Some(if index_type == IndexType::KeywordArray {
                        dex::value::Kind::ObjValue(dex::EncodedObject {
                            encoding: "json".into(),
                            payload: value.into_bytes(),
                        })
                    } else {
                        dex::value::Kind::StringValue(value)
                    }),
                }),
            }],
            ..Default::default()
        };
        let position = LogPosition {
            node_id: "slot-test".into(),
            incarnation: 1,
            seq: sequence,
        };
        projector
            .apply_batch(
                PartitionId(9),
                previous.as_ref(),
                &position,
                &[SearchDocumentChange::Upsert {
                    tenant_id: TenantId(1),
                    run_id,
                    encoded_document: record.encode_to_vec(),
                }],
            )
            .await
            .unwrap();
        assert_eq!(
            projector.read_progress(PartitionId(9)).await.unwrap(),
            Some(position.clone())
        );
        let slot = backend.lock_next_slot().await;
        let count: i64 = slot
            .client
            .query_one(
                "SELECT count(*) FROM dexzd_visibility_skipped_documents WHERE run_id = $1",
                &[&run_id.to_string()],
            )
            .await
            .unwrap()
            .get(0);
        assert_eq!(count, i64::from(is_skipped));
        previous = Some(position);
    }
    let run_id = RunId([31; 16]);
    let mixed_array = serde_json::to_string(&vec!["x".repeat(100); 50]).unwrap();
    let record = SearchDocumentRecord {
        version: 1,
        tenant_id: 1,
        run_id: run_id.as_bytes().to_vec(),
        flow_id: "large-mixed-array".into(),
        flow_type: "LargeMixedArrayFlow".into(),
        status: dex::FlowStatus::Running as i32,
        started_at_micros: 1,
        attributes: vec![SearchAttributeValue {
            index_key: "keyword".into(),
            index_type: IndexType::KeywordArray as i32,
            value: Some(dex::Value {
                kind: Some(dex::value::Kind::ObjValue(dex::EncodedObject {
                    encoding: "json".into(),
                    payload: mixed_array.into_bytes(),
                })),
            }),
        }],
        ..Default::default()
    };
    projector
        .apply_batch(
            PartitionId(9),
            previous.as_ref(),
            &LogPosition {
                node_id: "slot-test".into(),
                incarnation: 1,
                seq: 4,
            },
            &[SearchDocumentChange::Upsert {
                tenant_id: TenantId(1),
                run_id,
                encoded_document: record.encode_to_vec(),
            }],
        )
        .await
        .unwrap();
    let slot = backend.lock_next_slot().await;
    let preserved = slot.client.query_one("SELECT keyword_01, jsonb_array_length(sa_keyword -> 'keyword') FROM dexzd_visibility_runs WHERE run_id = $1", &[&run_id.to_string()]).await.unwrap();
    assert!(preserved.get::<_, Option<String>>(0).is_none());
    assert_eq!(preserved.get::<_, i32>(1), 50);
    drop(slot);
    let mut fields = FieldRegistry::new();
    fields.insert("keyword", IndexType::Keyword);
    let page = backend
        .search(SearchRequest {
            tenant_id: TenantId(1),
            query: "FlowType = 'LargeMixedArrayFlow'",
            page_size: 10,
            page_token: None,
            field_registry: &fields,
        })
        .await
        .unwrap();
    assert_eq!(page.entries.len(), 1);
    let unmatched = backend
        .search(SearchRequest {
            tenant_id: TenantId(1),
            query: "FlowType = 'LargeMixedArrayFlow' AND keyword STARTS_WITH '['",
            page_size: 10,
            page_token: None,
            field_registry: &fields,
        })
        .await
        .unwrap();
    assert!(unmatched.entries.is_empty());
    let unregistered_run = RunId([32; 16]);
    let unregistered = SearchDocumentRecord {
        version: 1,
        tenant_id: 1,
        run_id: unregistered_run.as_bytes().to_vec(),
        flow_id: "unregistered-oversized".into(),
        flow_type: "UnregisteredOversizedFlow".into(),
        status: dex::FlowStatus::Running as i32,
        started_at_micros: 1,
        attributes: vec![SearchAttributeValue {
            index_key: "unregistered".into(),
            index_type: IndexType::Keyword as i32,
            value: Some(dex::Value {
                kind: Some(dex::value::Kind::StringValue("x".repeat(1025))),
            }),
        }],
        ..Default::default()
    };
    projector
        .apply_batch(
            PartitionId(9),
            Some(&LogPosition {
                node_id: "slot-test".into(),
                incarnation: 1,
                seq: 4,
            }),
            &LogPosition {
                node_id: "slot-test".into(),
                incarnation: 1,
                seq: 5,
            },
            &[SearchDocumentChange::Upsert {
                tenant_id: TenantId(1),
                run_id: unregistered_run,
                encoded_document: unregistered.encode_to_vec(),
            }],
        )
        .await
        .unwrap();
    let preserved = backend
        .search(SearchRequest {
            tenant_id: TenantId(1),
            query: "FlowType = 'UnregisteredOversizedFlow'",
            page_size: 10,
            page_token: None,
            field_registry: &FieldRegistry::new(),
        })
        .await
        .unwrap();
    assert_eq!(preserved.entries.len(), 1);
    assert_eq!(metrics.0.load(Ordering::Relaxed), 2);
    let page = backend
        .search(SearchRequest {
            tenant_id: TenantId(1),
            query: "FlowType = 'KeywordBoundaryFlow'",
            page_size: 10,
            page_token: None,
            field_registry: &FieldRegistry::new(),
        })
        .await
        .unwrap();
    assert_eq!(page.entries.len(), 1);
}

async fn finish_backfill(client: &mut Client) {
    while backfill_next_batch(client).await.unwrap().did_advance {}
}

async fn read_ready_slots(client: &mut Client) -> Result<Vec<super::AttributeSlot>, SearchError> {
    Ok(load_committed_attribute_slot_snapshot(client).await?.ready)
}

#[path = "lifecycle_tests.rs"]
mod lifecycle;

#[path = "mixed_type_tests.rs"]
mod mixed_types;
