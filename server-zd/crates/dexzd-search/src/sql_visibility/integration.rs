// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

#![cfg(test)]

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use async_trait::async_trait;
use dexzd_keyspace::{PartitionId, RunId, TenantId};
use dexzd_postgres_connection::ConnectionSettings;
use dexzd_protocol::dex::{self, FlowStatus, IndexType};
use dexzd_protocol::dexzd::v1::{LogPosition, SearchAttributeValue, SearchDocumentRecord};
use dexzd_visibility_query::{
    FieldRegistry, SearchDocument, SearchValue, compare_documents, evaluate, parse_and_check,
};
use prost::Message;
use tokio_postgres::NoTls;

use super::{
    PageCursor, ProjectionVisibilityWait, SearchDocumentChange, SearchRow, SqlVisibilityProjector,
    SqlVisibilitySearchBackend, VisibilityDialect, assign_attribute_slots,
    ensure_schema_for_dialect, tenant_column_value, translate_query_for_dialect,
};
use crate::{SearchBackend, SearchError, SearchRequest};

struct CountVisibilityWait(AtomicUsize);

struct NeverVisibilityWait;

#[async_trait]
impl ProjectionVisibilityWait for CountVisibilityWait {
    async fn wait_until_visible(&self, _request: SearchRequest<'_>) -> Result<(), SearchError> {
        self.0.fetch_add(1, Ordering::Relaxed);
        Ok(())
    }
}

#[async_trait]
impl ProjectionVisibilityWait for NeverVisibilityWait {
    async fn wait_until_visible(&self, _request: SearchRequest<'_>) -> Result<(), SearchError> {
        std::future::pending().await
    }
}

#[derive(Default)]
struct Document(BTreeMap<String, SearchValue>);

impl Document {
    fn with(mut self, field: &str, value: SearchValue) -> Self {
        self.0.insert(field.to_owned(), value);
        self
    }
}

impl SearchDocument for Document {
    fn field_value(&self, field: &str) -> Option<SearchValue> {
        self.0.get(field).cloned()
    }
}

#[tokio::test]
async fn paradedb_projection_and_search_use_direct_tls() {
    let Ok(database_url) = std::env::var("DEX_ZD_PARADEDB_TLS_URL") else {
        assert!(
            !matches!(
                std::env::var("DEX_ZD_REQUIRE_PARADEDB_TLS").as_deref(),
                Ok("1")
            ),
            "the direct TLS test requires DEX_ZD_PARADEDB_TLS_URL"
        );
        return;
    };
    let certificate_path = std::env::var("DEX_ZD_PARADEDB_TLS_CA_CERT")
        .expect("TLS ParadeDB test requires its CA certificate");
    let plaintext_configuration: tokio_postgres::Config =
        database_url.parse().expect("parse CI TLS ParadeDB URL");
    let plaintext_error = plaintext_configuration
        .connect(NoTls)
        .await
        .err()
        .expect("TLS-only ParadeDB must reject a plaintext startup");
    let database_error = plaintext_error
        .as_db_error()
        .expect("plaintext startup must receive a PostgreSQL rejection");
    assert_eq!(database_error.code().code(), "28000");
    assert!(database_error.message().contains("no encryption"));

    for (ssl_mode, partition_id, run_id) in [
        ("require", PartitionId(1), RunId([1; 16])),
        ("verify-full", PartitionId(2), RunId([2; 16])),
    ] {
        let root_certificate = if ssl_mode == "verify-full" {
            format!("&sslrootcert={certificate_path}")
        } else {
            String::new()
        };
        let dsn = format!(
            "{database_url}?sslmode={ssl_mode}&sslnegotiation=direct&connect_timeout=5{root_certificate}"
        );
        let settings =
            ConnectionSettings::parse_for_search(&dsn).expect("parse direct TLS ParadeDB DSN");
        let backend = Arc::new(
            SqlVisibilitySearchBackend::connect_with_settings(
                &settings,
                1,
                None,
                Duration::from_secs(2),
            )
            .await
            .expect("connect ParadeDB through direct TLS"),
        );
        let projector = SqlVisibilityProjector::new(backend.clone());
        let tenant_id = TenantId(17);
        let document = SearchDocumentRecord {
            version: 1,
            tenant_id: tenant_id.0,
            flow_id: format!("tls-flow-{run_id}"),
            run_id: run_id.as_bytes().to_vec(),
            flow_type: "TlsFlow".into(),
            status: FlowStatus::Running as i32,
            started_at_micros: 1_000_000,
            ..Default::default()
        };
        projector
            .apply_batch(
                partition_id,
                None,
                &LogPosition {
                    node_id: "tls-node".into(),
                    incarnation: 1,
                    seq: 1,
                },
                &[SearchDocumentChange::Upsert {
                    tenant_id,
                    run_id,
                    encoded_document: document.encode_to_vec(),
                }],
            )
            .await
            .expect("project a committed run over TLS");
        backend.lock_next_slot().await.invalidate();
        let page = backend
            .search(SearchRequest {
                tenant_id,
                query: "FlowType = 'TlsFlow'",
                page_size: 10,
                page_token: None,
                field_registry: &FieldRegistry::new(),
            })
            .await
            .expect("SearchFlows reconnects through direct TLS");
        assert!(
            page.entries
                .iter()
                .any(|entry| entry.run_id == run_id.to_string())
        );
    }
}

fn fields() -> (FieldRegistry, Vec<(String, IndexType)>) {
    let application_fields = vec![
        ("keyword".to_owned(), IndexType::Keyword),
        ("tags".to_owned(), IndexType::KeywordArray),
        ("text".to_owned(), IndexType::Text),
        ("number".to_owned(), IndexType::Int),
        ("double".to_owned(), IndexType::Double),
        ("flag".to_owned(), IndexType::Bool),
        ("datetime".to_owned(), IndexType::Datetime),
        ("nullable".to_owned(), IndexType::Keyword),
    ];
    let mut registry = FieldRegistry::new();
    for (field, index_type) in &application_fields {
        registry.insert(field.clone(), *index_type);
    }
    (registry, application_fields)
}

fn run(run_id: &str, workflow_id: &str, status: &str, start: i64, close: Option<i64>) -> Document {
    let mut document = Document::default()
        .with("RunId", SearchValue::String(run_id.into()))
        .with("WorkflowId", SearchValue::String(workflow_id.into()))
        .with("WorkflowType", SearchValue::String("Engine".into()))
        .with("FlowType", SearchValue::String("CheckoutFlow".into()))
        .with("ExecutionStatus", SearchValue::String(status.into()))
        .with("StartTime", SearchValue::Datetime(start));
    if let Some(close) = close {
        document = document.with("CloseTime", SearchValue::Datetime(close));
    }
    document
}

#[derive(Default)]
pub(super) struct TestAttributeRegistry(dexzd_protocol::dexzd::v1::AttributeIndexRegistry);

#[async_trait]
impl super::AttributeIndexRegistryReader for TestAttributeRegistry {
    async fn read_attribute_index_registry(
        &self,
    ) -> Result<dexzd_protocol::dexzd::v1::AttributeIndexRegistry, SearchError> {
        Ok(self.0.clone())
    }
}

#[tokio::test]
async fn paradedb_matches_builtin_evaluator() {
    let Ok(database_url) = std::env::var("DEX_ZD_PARADEDB_URL") else {
        return;
    };
    run_differential_for_test(&database_url, VisibilityDialect::ParadeDb, false).await;
}

#[tokio::test]
async fn postgres_matches_builtin_evaluator() {
    let Ok(database_url) = std::env::var("DEX_ZD_POSTGRES_URL") else {
        return;
    };
    for has_slots in [false, true] {
        run_differential_for_test(&database_url, VisibilityDialect::Postgres, has_slots).await;
    }
}

async fn run_differential_for_test(
    database_url: &str,
    dialect: VisibilityDialect,
    has_slots: bool,
) {
    let (_, application_fields) = fields();
    let registry_reader = Arc::new(TestAttributeRegistry(
        dexzd_protocol::dexzd::v1::AttributeIndexRegistry {
            version: 1,
            registrations: [1, 3]
                .into_iter()
                .flat_map(|tenant_id| {
                    application_fields.iter().map(move |(key, index_type)| {
                        dexzd_protocol::dexzd::v1::AttributeIndexRegistration {
                            tenant_id,
                            index_key: key.clone(),
                            index_type: *index_type as i32,
                            ..Default::default()
                        }
                    })
                })
                .collect(),
        },
    ));
    let assignments = if has_slots {
        assign_attribute_slots(&registry_reader.0).expect("assign test slots")
    } else {
        Vec::new()
    };
    let translate_query = |query: &dexzd_visibility_query::Query,
                           tenant: &str,
                           limit: i64,
                           cursor: Option<&PageCursor>| {
        translate_query_for_dialect(query, tenant, limit, cursor, dialect, &assignments)
    };
    let upsert_statement = |row: &SearchRow| {
        if dialect == VisibilityDialect::Postgres {
            super::upsert::upsert_statements_with_slots(
                std::slice::from_ref(row),
                Some(&assignments),
            )
            .expect("build slotted upsert")
            .remove(0)
            .statement
        } else {
            super::upsert_statement(row)
        }
    };
    let (mut client, connection) = tokio_postgres::connect(database_url, NoTls)
        .await
        .expect("connect to CI SQL visibility database");
    let connection_task = tokio::spawn(connection);
    if dialect == VisibilityDialect::ParadeDb {
        client
            .batch_execute("CREATE EXTENSION IF NOT EXISTS pg_search")
            .await
            .expect("install pg_search");
    }
    let nonce = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("wall clock after Unix epoch")
        .as_nanos();
    let schema_name = format!("dexzd_search_{}_{}", std::process::id(), nonce);
    client
        .batch_execute(&format!(
            "CREATE SCHEMA {schema_name}; SET search_path TO {schema_name}, public, pdb;"
        ))
        .await
        .expect("create isolated schema");
    ensure_schema_for_dialect(&mut client, dialect)
        .await
        .expect("record visibility schema version");
    if dialect == VisibilityDialect::ParadeDb {
        super::index_tests::assert_paradedb_system_indexes_for_test(&client).await;
    }

    let mut documents = vec![
        run("r1", "flow-a", "Running", 1_000_000_000, None)
            .with("keyword", SearchValue::String("red".into()))
            .with("tags", SearchValue::StringArray(vec!["charge".into()]))
            .with("text", SearchValue::String("Senior Rust Engineer".into()))
            .with("number", SearchValue::Int(2))
            .with("double", SearchValue::Double(1.5))
            .with("flag", SearchValue::Bool(true))
            .with("datetime", SearchValue::Datetime(1_000_000_000)),
        run(
            "r2",
            "flow-b",
            "Completed",
            2_000_000_000,
            Some(5_000_000_000),
        )
        .with("keyword", SearchValue::String("blue".into()))
        .with("tags", SearchValue::StringArray(vec!["ship".into()]))
        .with("text", SearchValue::String("Principal Go Engineer".into()))
        .with("number", SearchValue::Int(7))
        .with("double", SearchValue::Double(3.5))
        .with("flag", SearchValue::Bool(false))
        .with("datetime", SearchValue::Datetime(2_000_000_000)),
        run("r3", "flow-c", "Failed", 3_000_000_000, Some(4_000_000_000))
            .with("keyword", SearchValue::String("red".into()))
            .with(
                "tags",
                SearchValue::StringArray(vec!["charge".into(), "ship".into()]),
            )
            .with("text", SearchValue::String("Rust Backend".into()))
            .with("number", SearchValue::Int(5))
            .with("double", SearchValue::Double(2.5))
            .with("flag", SearchValue::Bool(true)),
        run(
            "r4",
            "flow-d",
            "Completed",
            4_000_000_000,
            Some(6_000_000_000),
        )
        .with("tags", SearchValue::StringArray(Vec::new())),
        run(
            "r5",
            "flow-e",
            "Completed",
            5_000_000_000,
            Some(7_000_000_000),
        )
        .with("number", SearchValue::Int(9_007_199_254_740_993))
        .with("double", SearchValue::Double(0.25)),
        run(
            "r6",
            "flow-f",
            "Completed",
            6_000_000_000,
            Some(8_000_000_000),
        )
        .with("number", SearchValue::Int(-2))
        .with("double", SearchValue::Double(-3.0)),
        run(
            "r7",
            "flow-g",
            "Completed",
            7_000_000_000,
            Some(9_000_000_000),
        )
        .with("number", SearchValue::Int(1000)),
        run(
            "r8",
            "flow-h",
            "Completed",
            8_000_000_000,
            Some(10_000_000_000),
        )
        .with("number", SearchValue::Int(3)),
        run(
            "r9",
            "flow-i",
            "Completed",
            9_000_000_000,
            Some(14_000_000_000),
        )
        .with("number", SearchValue::Int(9_007_199_254_740_992)),
        run(
            "r10",
            "flow-j",
            "Completed",
            10_000_000_000,
            Some(15_000_000_000),
        )
        .with("number", SearchValue::Int(-9_007_199_254_740_992)),
        run(
            "r11",
            "flow-k",
            "Completed",
            11_000_000_000,
            Some(16_000_000_000),
        )
        .with("number", SearchValue::Int(i64::MAX - 1)),
        run(
            "r12",
            "flow-l",
            "Completed",
            12_000_000_000,
            Some(17_000_000_000),
        )
        .with("number", SearchValue::Int(i64::MAX)),
        run(
            "r13",
            "flow-m",
            "Completed",
            13_000_000_000,
            Some(18_000_000_000),
        )
        .with("number", SearchValue::Int(i64::MAX - 2)),
        run("r-é", "flow-é", "Running", 14_000_000_000, None)
            .with("FlowType", SearchValue::String("ÉclairFlow".into())),
        run("r-é", "flow-é", "Running", 15_000_000_000, None)
            .with("FlowType", SearchValue::String("ÉclairFlow".into())),
        run("r-中", "flow-中", "Running", 16_000_000_000, None)
            .with("FlowType", SearchValue::String("订单Flow".into())),
    ];
    let (registry, application_fields) = fields();
    let parity_tenant_id = TenantId(3);
    let parity_tenant_column = tenant_column_value(parity_tenant_id);
    let mut rows = BTreeMap::new();
    for document in &documents {
        let row = SearchRow::from_document(parity_tenant_id, 1, document, &application_fields)
            .expect("map document");
        let statement = upsert_statement(&row);
        client
            .execute(&statement.sql, &statement.parameter_refs())
            .await
            .expect("insert run");
        rows.insert(row.run_id.clone(), row);
    }
    let other_tenant = run(
        "other",
        "flow-a",
        "Completed",
        1_000_000_000,
        Some(2_000_000_000),
    );
    let other_row = SearchRow::from_document(TenantId(4), 1, &other_tenant, &application_fields)
        .expect("map other tenant");
    let statement = upsert_statement(&other_row);
    client
        .execute(&statement.sql, &statement.parameter_refs())
        .await
        .expect("insert other tenant");

    let mut stale_row = rows.get("r1").expect("versioned row").clone();
    stale_row
        .sa_keyword
        .insert("keyword".into(), SearchValue::String("stale".into()));
    for version in [1, 0] {
        stale_row.version = version;
        let statement = upsert_statement(&stale_row);
        client
            .execute(&statement.sql, &statement.parameter_refs())
            .await
            .expect("stale upsert");
    }
    let keyword: String = client.query_one(
        "SELECT sa_keyword ->> 'keyword' FROM dexzd_visibility_runs WHERE tenant_id = $1::text AND run_id = 'r1'",
        &[&parity_tenant_column],
    ).await.expect("read guarded row").get(0);
    assert_eq!(keyword, "red");

    for input in [
        "",
        "WorkflowId = 'flow-a'",
        "WorkflowId != 'flow-a'",
        "WorkflowId IN ('flow-a', 'flow-c')",
        "WorkflowId STARTS_WITH 'flow-'",
        "FlowType = 'ÉclairFlow'",
        "FlowType IN ('ÉclairFlow', '订单Flow')",
        "FlowType > 'Z'",
        "FlowType STARTS_WITH 'É'",
        "WorkflowId = 'flow-é'",
        "WorkflowId IN ('flow-é', 'flow-中')",
        "WorkflowId > 'flow-z'",
        "WorkflowId STARTS_WITH 'flow-é'",
        "RunId = 'r-é'",
        "RunId IN ('r-é', 'r-中')",
        "RunId > 'r-z'",
        "RunId STARTS_WITH 'r-é'",
        "keyword = 'red'",
        "keyword STARTS_WITH 'r'",
        "keyword NOT IN ('red')",
        "NOT (keyword = 'red')",
        "tags = 'charge'",
        "tags IN ('ship')",
        "tags != 'charge'",
        "tags IS NOT NULL",
        "number >= 5",
        "number = '7'",
        "number IN ('2', 7)",
        "number BETWEEN '3' AND '+6'",
        "number != '5'",
        "number = '7.0'",
        "number = '1e3'",
        "number = '9007199254740993'",
        "number = '9007199254740993.0'",
        "number = '9007199254740992.5'",
        "number = '-9007199254740992.5'",
        "number = '7.5'",
        "number IN ('7', '7.5')",
        "number > '7.5'",
        "number <= '-1.5'",
        "number BETWEEN '1.5' AND '3.5'",
        "number >= '9223372036854775806.5'",
        "number < '9223372036854775806.5'",
        "number BETWEEN '9223372036854775806.5' AND '9223372036854775807'",
        "number != '7.5'",
        "number NOT IN ('7.5')",
        "NOT number = '7.5'",
        "double < 3.0",
        "double = '1.5'",
        "double IN ('1.5', 3.5)",
        "double BETWEEN '2' AND '3.0'",
        "double > '2E0'",
        "double = '0x1p-2'",
        "double = '-0X1.8p1'",
        "flag = true",
        "datetime >= '1970-01-01T00:00:02Z'",
        "datetime IS NULL",
        "text = 'rust'",
        "text = 'rust engineer'",
        "text != 'rust'",
        "CloseTime IS NULL",
        "CloseTime IS NOT NULL",
        "ExecutionStatus = 'ContinuedAsNew'",
        "WorkflowType = 'Engine'",
        "(keyword = 'red' OR flag = false) AND number IS NOT NULL",
        "FlowType = 'CheckoutFlow' ORDER BY StartTime ASC",
        "FlowType = 'CheckoutFlow' ORDER BY CloseTime DESC",
    ] {
        let query = parse_and_check(input, &registry).expect("checked query");
        let mut expected: Vec<&Document> = documents
            .iter()
            .filter(|document| evaluate(&query, *document))
            .collect();
        expected.sort_by(|left, right| compare_documents(&query, *left, *right));
        let expected_ids: Vec<String> = expected
            .iter()
            .map(|document| match document.field_value("RunId") {
                Some(SearchValue::String(run_id)) => run_id,
                _ => panic!("test run must have an ID"),
            })
            .collect();
        let statement = translate_query(&query, &parity_tenant_column, 100, None)
            .expect("translate checked query");
        let actual = client
            .query(&statement.sql, &statement.parameter_refs())
            .await
            .expect(input);
        let actual_ids: Vec<String> = actual
            .iter()
            .map(|row| row.get::<_, String>("run_id"))
            .collect();
        assert_eq!(actual_ids, expected_ids, "{input}");
        println!("SQL visibility differential case passed: {input}");
    }

    for (input, expected_ids) in [
        ("number = '7'", vec!["r2"]),
        ("number IN ('2', 7)", vec!["r1", "r2"]),
        ("number BETWEEN '3' AND '+6'", vec!["r8", "r3"]),
        ("double = '1.5'", vec!["r1"]),
        ("double IN ('1.5', 3.5)", vec!["r1", "r2"]),
        ("double BETWEEN '2' AND '3.0'", vec!["r3"]),
        ("double > '2E0'", vec!["r2", "r3"]),
    ] {
        let query = parse_and_check(input, &registry).expect("quoted numeric query");
        let statement = translate_query(&query, &parity_tenant_column, 100, None)
            .expect("translate quoted numeric query");
        let actual_ids: Vec<String> = client
            .query(&statement.sql, &statement.parameter_refs())
            .await
            .expect(input)
            .iter()
            .map(|row| row.get::<_, String>("run_id"))
            .collect();
        assert_eq!(actual_ids, expected_ids, "{input}");
        println!("SQL visibility differential case passed: {input}");
    }

    for (input, expected_ids) in [
        ("number = '7.0'", vec!["r2"]),
        ("number = '1e3'", vec!["r7"]),
        ("number = '9007199254740993'", vec!["r5"]),
        ("number = '9007199254740993.0'", vec!["r5"]),
        ("number = '9007199254740992.5'", vec!["r9"]),
        ("number = '-9007199254740992.5'", vec!["r10"]),
        ("number = '7.5'", vec![]),
        ("number IN ('7', '7.5')", vec!["r2"]),
        (
            "number > '7.5'",
            vec!["r5", "r7", "r9", "r11", "r12", "r13"],
        ),
        ("number <= '-1.5'", vec!["r6", "r10"]),
        ("number BETWEEN '1.5' AND '3.5'", vec!["r1", "r8"]),
        ("number >= '9223372036854775806.5'", vec!["r11", "r12"]),
        (
            "number < '9223372036854775806.5'",
            vec!["r1", "r2", "r3", "r5", "r6", "r7", "r8", "r9", "r10", "r13"],
        ),
        (
            "number BETWEEN '9223372036854775806.5' AND '9223372036854775807'",
            vec!["r11", "r12"],
        ),
        (
            "number != '7.5'",
            vec![
                "r1", "r2", "r3", "r5", "r6", "r7", "r8", "r9", "r10", "r11", "r12", "r13",
            ],
        ),
        ("double = '0x1p-2'", vec!["r5"]),
        ("double = '-0X1.8p1'", vec!["r6"]),
    ] {
        let query = parse_and_check(input, &registry).expect("quoted numeric query");
        let statement = translate_query(&query, &parity_tenant_column, 100, None)
            .expect("translate quoted numeric query");
        let actual_ids: BTreeSet<String> = client
            .query(&statement.sql, &statement.parameter_refs())
            .await
            .expect(input)
            .iter()
            .map(|row| row.get("run_id"))
            .collect();
        let expected_ids: BTreeSet<String> = expected_ids.into_iter().map(str::to_owned).collect();
        assert_eq!(actual_ids, expected_ids, "{input}");
        println!("SQL visibility differential case passed: {input}");
    }
    for input in [
        "number = '9223372036854775808'",
        "number = '0x1p2'",
        "number = 'Inf'",
        "number = '1_0'",
        "number = ' 7'",
        "number = '7d'",
        "double = 'Infinity'",
        "double = 'NaN'",
        "double = '1e999'",
        "double = '0x1p1024'",
        "double = '1_0'",
        "double = ' 1'",
        "double = '1f'",
    ] {
        let error = parse_and_check(input, &registry).expect_err(input);
        assert!(error.message.contains("literal"), "{input}: {error}");
    }

    for document in [
        run("running-tie-a", "flow-a", "Running", 1_000_000_000, None),
        run("running-tie-b", "flow-b", "Running", 1_000_000_000, None),
        run(
            "closed-tie",
            "flow-tie",
            "Completed",
            2_000_000_000,
            Some(5_000_000_000),
        ),
        run("closed-null-a", "flow-a", "Completed", 2_000_000_000, None)
            .with("tags", SearchValue::StringArray(Vec::new())),
        run("closed-null-b", "flow-b", "Failed", 2_000_000_000, None)
            .with("keyword", SearchValue::String("red".into())),
    ] {
        let row = SearchRow::from_document(parity_tenant_id, 1, &document, &application_fields)
            .expect("map pagination document");
        let statement = upsert_statement(&row);
        client
            .execute(&statement.sql, &statement.parameter_refs())
            .await
            .expect("insert pagination run");
        rows.insert(row.run_id.clone(), row);
        documents.push(document);
    }

    for input in [
        "",
        "ORDER BY StartTime ASC",
        "ORDER BY CloseTime DESC",
        "tags IS NOT NULL",
        "keyword = 'red'",
    ] {
        let query = parse_and_check(input, &registry).expect("ordered query");
        let mut expected: Vec<&Document> = documents
            .iter()
            .filter(|document| evaluate(&query, *document))
            .collect();
        expected.sort_by(|left, right| compare_documents(&query, *left, *right));
        let expected_ids: Vec<String> = expected
            .iter()
            .map(|document| match document.field_value("RunId") {
                Some(SearchValue::String(run_id)) => run_id,
                _ => panic!("test run must have an ID"),
            })
            .collect();
        for page_size in [1_usize, 2, 3] {
            let mut cursor = None;
            let mut actual_ids = Vec::new();
            loop {
                let statement = translate_query(
                    &query,
                    &parity_tenant_column,
                    page_size as i64,
                    cursor.as_ref(),
                )
                .expect("ordered page");
                let page = client
                    .query(&statement.sql, &statement.parameter_refs())
                    .await
                    .expect("ordered page SQL");
                actual_ids.extend(page.iter().map(|row| row.get::<_, String>("run_id")));
                if page.len() < page_size {
                    break;
                }
                let last_id: String = page.last().expect("full page has final row").get("run_id");
                cursor = Some(PageCursor::from_row(
                    rows.get(&last_id).expect("cursor row"),
                ));
            }
            assert_eq!(actual_ids, expected_ids, "{input}, page size {page_size}");
            println!(
                "SQL visibility differential paging passed: {input:?}, page_size={page_size}, rows={}",
                actual_ids.len()
            );
        }
    }

    let tenant_id = TenantId(1);
    let first_run_id = "00000000-0000-0000-0000-000000000001";
    let second_run_id = "00000000-0000-0000-0000-000000000002";
    for document in [
        run(first_run_id, "first", "Running", 1_000_000_000, None)
            .with("keyword", SearchValue::String("red".into()))
            .with("tags", SearchValue::StringArray(vec!["urgent".into()]))
            .with("text", SearchValue::String("Rust Engineer".into()))
            .with("number", SearchValue::Int(7))
            .with("double", SearchValue::Double(2.0))
            .with("flag", SearchValue::Bool(true))
            .with("datetime", SearchValue::Datetime(1_000_000_000)),
        run(
            second_run_id,
            "second",
            "Completed",
            2_000_000_000,
            Some(3_000_000_000),
        )
        .with("keyword", SearchValue::String("blue".into()))
        .with("number", SearchValue::Int(9))
        .with("datetime", SearchValue::Datetime(2_000_000_000)),
    ] {
        let row = SearchRow::from_document(tenant_id, 1, &document, &application_fields)
            .expect("map backend fixture");
        let row = if row.run_id == first_run_id {
            row.with_wire_attributes(&[
                SearchAttributeValue {
                    index_key: "datetime".into(),
                    index_type: IndexType::Datetime.into(),
                    value: Some(dex::Value {
                        kind: Some(dex::value::Kind::StringValue(
                            "1970-01-01T01:00:01+01:00".into(),
                        )),
                    }),
                },
                SearchAttributeValue {
                    index_key: "nullable".into(),
                    index_type: IndexType::Keyword.into(),
                    value: Some(dex::Value {
                        kind: Some(dex::value::Kind::NullValue(0)),
                    }),
                },
            ])
            .expect("preserve backend wire attributes")
        } else {
            row
        };
        let statement = upsert_statement(&row);
        client
            .execute(&statement.sql, &statement.parameter_refs())
            .await
            .expect("insert backend fixture");
    }
    let mut configuration: tokio_postgres::Config =
        database_url.parse().expect("parse CI SQL visibility URL");
    configuration.options(format!("-c search_path={schema_name},public,pdb"));
    configuration.application_name(&schema_name);
    let visibility_wait = Arc::new(CountVisibilityWait(AtomicUsize::new(0)));
    let backend = SqlVisibilitySearchBackend::connect_dialect_with_settings(
        &ConnectionSettings::from_config(configuration.clone()),
        2,
        Some(visibility_wait.clone()),
        Duration::from_secs(2),
        dialect,
        Some(registry_reader.clone()),
    )
    .await
    .expect("connect backend pool");
    let first_page = backend
        .search(SearchRequest {
            tenant_id: TenantId(1),
            query: "WorkflowType = 'Engine'",
            page_size: 1,
            page_token: None,
            field_registry: &registry,
        })
        .await
        .expect("first backend page");
    assert_eq!(first_page.entries.len(), 1);
    assert_eq!(first_page.entries[0].run_id, first_run_id);
    let returned_attributes: BTreeMap<_, _> = first_page.entries[0]
        .indexed_attributes
        .iter()
        .map(|attribute| (attribute.key.as_str(), attribute.value.as_ref()))
        .collect();
    for attribute in [
        "FlowType", "keyword", "tags", "text", "number", "double", "flag", "datetime", "nullable",
    ] {
        assert!(
            returned_attributes
                .get(attribute)
                .and_then(|value| *value)
                .is_some(),
            "{attribute}"
        );
    }
    use dexzd_protocol::dex::value::Kind;
    assert!(matches!(
        returned_attributes["keyword"].and_then(|value| value.kind.as_ref()),
        Some(Kind::StringValue(value)) if value == "red"
    ));
    assert!(matches!(
        returned_attributes["text"].and_then(|value| value.kind.as_ref()),
        Some(Kind::StringValue(value)) if value == "Rust Engineer"
    ));
    match returned_attributes["tags"].and_then(|value| value.kind.as_ref()) {
        Some(Kind::ObjValue(value)) => {
            let tags: Vec<String> =
                serde_json::from_slice(&value.payload).expect("decode returned tags");
            assert_eq!(tags, ["urgent"]);
        }
        _ => panic!("missing returned tags"),
    }
    assert!(matches!(
        returned_attributes["number"].and_then(|value| value.kind.as_ref()),
        Some(Kind::IntValue(7))
    ));
    assert!(matches!(
        returned_attributes["double"].and_then(|value| value.kind.as_ref()),
        Some(Kind::DoubleValue(2.0))
    ));
    assert!(matches!(
        returned_attributes["flag"].and_then(|value| value.kind.as_ref()),
        Some(Kind::BoolValue(true))
    ));
    assert!(matches!(
        returned_attributes["datetime"].and_then(|value| value.kind.as_ref()),
        Some(Kind::StringValue(value)) if value == "1970-01-01T01:00:01+01:00"
    ));
    assert!(matches!(
        returned_attributes["nullable"].and_then(|value| value.kind.as_ref()),
        Some(Kind::NullValue(0))
    ));
    let token = first_page.next_page_token.expect("second backend page");
    let second_page = backend
        .search(SearchRequest {
            tenant_id: TenantId(1),
            query: "WorkflowType = 'Engine'",
            page_size: 1,
            page_token: Some(&token),
            field_registry: &registry,
        })
        .await
        .expect("second backend page");
    assert_eq!(second_page.entries[0].run_id, second_run_id);
    assert!(second_page.next_page_token.is_none());
    let second_datetime = second_page.entries[0]
        .indexed_attributes
        .iter()
        .find(|attribute| attribute.key == "datetime")
        .and_then(|attribute| attribute.value.as_ref())
        .and_then(|value| value.kind.as_ref());
    assert!(matches!(
        second_datetime,
        Some(Kind::IntValue(2_000_000_000))
    ));
    assert_eq!(visibility_wait.0.load(Ordering::Relaxed), 2);
    for (query, expected_run_id) in [
        ("keyword = 'red'", first_run_id),
        ("number >= 8", second_run_id),
        ("CloseTime IS NULL", first_run_id),
    ] {
        let page = backend
            .search(SearchRequest {
                tenant_id: TenantId(1),
                query,
                page_size: 10,
                page_token: None,
                field_registry: &registry,
            })
            .await
            .expect("filtered backend page");
        assert_eq!(page.entries.len(), 1, "{query}");
        assert_eq!(page.entries[0].run_id, expected_run_id, "{query}");
    }
    for page_size in [0, usize::MAX] {
        let page = backend
            .search(SearchRequest {
                tenant_id: TenantId(1),
                query: "",
                page_size,
                page_token: None,
                field_registry: &registry,
            })
            .await
            .expect("bounded backend page");
        assert_eq!(page.entries.len(), 2);
        assert!(page.next_page_token.is_none());
    }
    for changed_query in ["", "WorkflowType = 'Engine' ORDER BY StartTime ASC"] {
        let error = backend
            .search(SearchRequest {
                tenant_id: TenantId(1),
                query: changed_query,
                page_size: 1,
                page_token: Some(&token),
                field_registry: &registry,
            })
            .await
            .expect_err("changed query token");
        assert!(matches!(error, SearchError::InvalidArgument(_)));
    }
    let replacement = if token.ends_with('0') { '1' } else { '0' };
    let tampered = format!("{}{replacement}", &token[..token.len() - 1]);
    let error = backend
        .search(SearchRequest {
            tenant_id: TenantId(1),
            query: "WorkflowType = 'Engine'",
            page_size: 1,
            page_token: Some(&tampered),
            field_registry: &registry,
        })
        .await
        .expect_err("tampered token");
    assert!(matches!(error, SearchError::InvalidArgument(_)));
    let error = backend
        .search(SearchRequest {
            tenant_id: TenantId(2),
            query: "WorkflowType = 'Engine'",
            page_size: 1,
            page_token: Some(&token),
            field_registry: &registry,
        })
        .await
        .expect_err("cross-tenant token");
    assert!(matches!(error, SearchError::InvalidArgument(_)));
    let other_tenant = backend
        .search(SearchRequest {
            tenant_id: TenantId(2),
            query: "",
            page_size: 10,
            page_token: None,
            field_registry: &registry,
        })
        .await
        .expect("other tenant page");
    assert!(other_tenant.entries.is_empty());

    let stalled_backend = SqlVisibilitySearchBackend::connect_dialect_with_settings(
        &ConnectionSettings::from_config(configuration.clone()),
        1,
        Some(Arc::new(NeverVisibilityWait)),
        Duration::from_millis(50),
        dialect,
        Some(registry_reader.clone()),
    )
    .await
    .expect("connect stalled backend");
    let stalled_error = stalled_backend
        .search(SearchRequest {
            tenant_id,
            query: "",
            page_size: 1,
            page_token: None,
            field_registry: &registry,
        })
        .await
        .expect_err("stalled visibility wait");
    assert!(
        matches!(stalled_error, SearchError::Unavailable(message) if message.contains("timed out"))
    );
    drop(stalled_backend);

    let tied_run_id = "00000000-0000-0000-0000-000000000003";
    let tied_document = run(
        tied_run_id,
        "third",
        "Completed",
        2_000_000_000,
        Some(3_000_000_000),
    );
    let tied_row = SearchRow::from_document(tenant_id, 1, &tied_document, &application_fields)
        .expect("map tied backend run");
    let tied_statement = upsert_statement(&tied_row);
    client
        .execute(&tied_statement.sql, &tied_statement.parameter_refs())
        .await
        .expect("insert tied backend run");
    let ordered_query = "ORDER BY CloseTime ASC";
    let mut ordered_token = None;
    let mut ordered_ids = Vec::new();
    loop {
        let page = backend
            .search(SearchRequest {
                tenant_id,
                query: ordered_query,
                page_size: 1,
                page_token: ordered_token.as_deref(),
                field_registry: &registry,
            })
            .await
            .expect("ordered backend page");
        ordered_ids.extend(page.entries.into_iter().map(|entry| entry.run_id));
        ordered_token = page.next_page_token;
        if ordered_token.is_none() {
            break;
        }
    }
    assert_eq!(ordered_ids, [second_run_id, tied_run_id, first_run_id]);

    let terminated = client
        .query(
            "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = $1::text AND pid <> pg_backend_pid()",
            &[&schema_name],
        )
        .await
        .expect("terminate backend pool connections");
    assert!(terminated.len() >= 2);
    assert!(terminated.iter().all(|row| row.get::<_, bool>(0)));
    let recovered = backend
        .search(SearchRequest {
            tenant_id,
            query: "",
            page_size: 10,
            page_token: None,
            field_registry: &registry,
        })
        .await
        .expect("reconnect terminated backend connection");
    assert_eq!(recovered.entries.len(), 3);

    super::projection::run_projector_scenarios_for_test(database_url, &client, nonce, dialect)
        .await;

    client
        .batch_execute("DROP TABLE dexzd_visibility_runs")
        .await
        .expect("remove visibility table");
    let database_error = backend
        .search(SearchRequest {
            tenant_id,
            query: "",
            page_size: 1,
            page_token: None,
            field_registry: &registry,
        })
        .await
        .expect_err("missing visibility table");
    assert!(matches!(database_error, SearchError::Internal(message) if message.contains("42P01")));
    drop(backend);

    client
        .batch_execute(&format!("DROP SCHEMA {schema_name} CASCADE"))
        .await
        .expect("drop isolated schema");
    drop(client);
    connection_task
        .await
        .expect("connection task")
        .expect("connection shutdown");
}
