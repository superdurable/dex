// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

#![cfg(test)]

use std::collections::BTreeMap;
use std::time::{SystemTime, UNIX_EPOCH};

use dexzd_keyspace::TenantId;
use dexzd_protocol::dex::IndexType;
use dexzd_protocol::dexzd::v1::{AttributeIndexRegistry, AttributeIndexRegistration};
use dexzd_visibility_query::{FieldRegistry, SearchDocument, SearchValue, compare_documents, evaluate, parse_and_check};
use tokio_postgres::NoTls;

use crate::sql_visibility::{SearchRow, VisibilityDialect, assign_attribute_slots, ensure_schema_for_dialect, translate_query_for_dialect};
use crate::sql_visibility::slots::registry::initialize_attribute_slots;
use crate::sql_visibility::slots::backfill::backfill_next_batch;

struct MixedDocument {
    fields: BTreeMap<String, SearchValue>,
    index_types: Vec<(String, IndexType)>,
}

impl MixedDocument {
    fn new(run_id: &str) -> Self {
        Self { fields: [("RunId", SearchValue::String(run_id.into())), ("WorkflowId", SearchValue::String(run_id.into())), ("WorkflowType", SearchValue::String("Engine".into())), ("FlowType", SearchValue::String("MixedTypeFlow".into())), ("ExecutionStatus", SearchValue::String("Running".into())), ("StartTime", SearchValue::Datetime(1))].into_iter().map(|(key, value)| (key.to_owned(), value)).collect(), index_types: Vec::new() }
    }

    fn with(mut self, key: &str, index_type: IndexType, value: SearchValue) -> Self {
        self.fields.insert(key.into(), value);
        self.index_types.push((key.into(), index_type));
        self
    }

    fn run_id(&self) -> &str {
        match self.fields.get("RunId").unwrap() { SearchValue::String(run_id) => run_id, _ => panic!("test runs have string IDs") }
    }
}

impl SearchDocument for MixedDocument {
    fn field_value(&self, field: &str) -> Option<SearchValue> { self.fields.get(field).cloned() }
}

fn documents() -> Vec<MixedDocument> {
    vec![
        MixedDocument::new("mixed-array")
            .with("keyword", IndexType::KeywordArray, SearchValue::StringArray(vec!["red".into(), "blue".into()]))
            .with("tags", IndexType::Keyword, SearchValue::String("scalar".into()))
            .with("number", IndexType::Double, SearchValue::Double(1.5))
            .with("double", IndexType::Int, SearchValue::Int(7))
            .with("flag", IndexType::Keyword, SearchValue::String("true".into()))
            .with("datetime", IndexType::Int, SearchValue::Int(1))
            .with("text", IndexType::KeywordArray, SearchValue::StringArray(vec!["rust".into()])),
        MixedDocument::new("native")
            .with("keyword", IndexType::Keyword, SearchValue::String("red".into()))
            .with("tags", IndexType::KeywordArray, SearchValue::StringArray(vec!["scalar".into()]))
            .with("number", IndexType::Int, SearchValue::Int(7))
            .with("double", IndexType::Double, SearchValue::Double(7.0))
            .with("flag", IndexType::Bool, SearchValue::Bool(true))
            .with("datetime", IndexType::Datetime, SearchValue::Datetime(1))
            .with("text", IndexType::Text, SearchValue::String("rust engineer".into())),
        MixedDocument::new("missing"),
        MixedDocument::new("numeric-equal-wrong-type")
            .with("number", IndexType::Double, SearchValue::Double(7.0))
            .with("double", IndexType::Int, SearchValue::Int(7))
            .with("tags", IndexType::Keyword, SearchValue::String("other".into())),
        MixedDocument::new("string-compatible")
            .with("keyword", IndexType::Text, SearchValue::String("red".into()))
            .with("text", IndexType::Keyword, SearchValue::String("rust engineer".into()))
            .with("tags", IndexType::KeywordArray, SearchValue::StringArray(Vec::new())),
        MixedDocument::new("oversized-mixed-array")
            .with("keyword", IndexType::KeywordArray, SearchValue::StringArray(std::iter::once("red".to_owned()).chain(std::iter::repeat_n("x".repeat(100), 49)).collect())),
    ]
}

fn registry() -> AttributeIndexRegistry {
    AttributeIndexRegistry { version: 1, registrations: [("keyword", IndexType::Keyword), ("tags", IndexType::KeywordArray), ("number", IndexType::Int), ("double", IndexType::Double), ("flag", IndexType::Bool), ("datetime", IndexType::Datetime), ("text", IndexType::Text)].into_iter().map(|(key, index_type)| AttributeIndexRegistration { tenant_id: 1, index_key: key.into(), index_type: index_type as i32, ..Default::default() }).collect() }
}

fn fields() -> FieldRegistry {
    let mut fields = FieldRegistry::new();
    for entry in registry().registrations { fields.insert(entry.index_key, IndexType::try_from(entry.index_type).unwrap()); }
    fields
}

fn assert_builtin_reference(documents: &[MixedDocument], fields: &FieldRegistry) {
    let mixed = &documents[0];
    for (input, expected) in [("tags = 'scalar'", true), ("keyword = 'red'", true), ("number = 1", false), ("number != 1", true), ("double = 7.0", false), ("double != 7.0", true), ("number IS NULL", false), ("number IS NOT NULL", true), ("NOT number = '7.5'", true)] {
        assert_eq!(evaluate(&parse_and_check(input, fields).unwrap(), mixed), expected, "builtin reference: {input}");
    }
}

#[tokio::test]
async fn postgres_mixed_values_match_builtin_with_the_authorized_keyword_array_exception() {
    let documents = documents();
    let fields = fields();
    assert_builtin_reference(&documents, &fields);
    let Ok(database_url) = std::env::var("DEX_ZD_POSTGRES_URL") else { return; };
    let (mut client, connection) = tokio_postgres::connect(&database_url, NoTls).await.unwrap();
    let driver = tokio::spawn(connection);
    let nonce = SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos();
    let schema = format!("dexzd_mixed_{}_{nonce}", std::process::id());
    client.batch_execute(&format!("CREATE SCHEMA {schema}; SET search_path={schema},public")).await.unwrap();
    ensure_schema_for_dialect(&mut client, VisibilityDialect::Postgres).await.unwrap();
    let rows = documents.iter().map(|document| SearchRow::from_document(TenantId(1), 1, document, &document.index_types).unwrap()).collect::<Vec<_>>();
    for chunk in crate::sql_visibility::upsert::upsert_statements_with_slots(&rows, Some(&[])).unwrap() { client.execute_typed(&chunk.statement.sql, &chunk.statement.typed_parameter_refs()).await.unwrap(); }
    let registry = registry();
    let assignments = assign_attribute_slots(&registry).unwrap();
    for should_use_slots in [false, true] {
        if should_use_slots {
            initialize_attribute_slots(&mut client, registry.version, &assignments).await.unwrap();
            while backfill_next_batch(&mut client).await.unwrap() {}
        }
        for (input, has_keyword_array_exception) in [
            ("", false), ("ORDER BY StartTime ASC", false), ("ORDER BY CloseTime DESC", false),
            ("keyword = 'red'", true), ("keyword IN ('red', 'blue')", true),
            ("keyword != 'red'", false), ("keyword NOT IN ('red', 'blue')", false), ("NOT (keyword = 'red')", false), ("NOT (keyword != 'red')", false),
            ("keyword STARTS_WITH 'r'", false), ("keyword < 'z'", false), ("keyword IS NULL", false), ("keyword IS NOT NULL", false),
            ("tags = 'scalar'", false), ("tags != 'scalar'", false), ("tags IN ('scalar', 'other')", false), ("tags NOT IN ('scalar')", false), ("tags IS NULL", false), ("tags IS NOT NULL", false),
            ("number = 7", false), ("number != 7", false), ("number >= 0", false), ("number BETWEEN 0 AND 10", false), ("number = '7.5'", false), ("NOT number = '7.5'", false), ("number IS NULL", false), ("number IS NOT NULL", false),
            ("double = 7.0", false), ("double != 7.0", false), ("double IS NULL", false), ("double IS NOT NULL", false),
            ("flag = true", false), ("flag != true", false), ("flag IS NULL", false), ("flag IS NOT NULL", false),
            ("text = 'rust'", false), ("text != 'rust'", false), ("text IS NULL", false), ("text IS NOT NULL", false),
            ("datetime >= '1970-01-01T00:00:00Z'", false), ("datetime IS NULL", false), ("datetime IS NOT NULL", false),
            ("NOT (number = 7 OR tags = 'scalar')", false),
        ] {
            let query = parse_and_check(input, &fields).unwrap();
            let mut expected = documents.iter().filter(|document| evaluate(&query, *document)).collect::<Vec<_>>();
            let builtin_count = expected.len();
            if has_keyword_array_exception { expected.retain(|document| !matches!(document.fields.get("keyword"), Some(SearchValue::StringArray(_)))); assert!(expected.len() < builtin_count, "the explicitly accepted difference is exercised"); }
            expected.sort_by(|left, right| compare_documents(&query, *left, *right));
            let expected = expected.iter().map(|document| document.run_id().to_owned()).collect::<Vec<_>>();
            let statement = translate_query_for_dialect(&query, "1", 100, None, VisibilityDialect::Postgres, if should_use_slots { &assignments } else { &[] }).unwrap();
            let actual = client.query_typed(&statement.sql, &statement.typed_parameter_refs()).await.unwrap().iter().map(|row| row.get::<_, String>("run_id")).collect::<Vec<_>>();
            assert_eq!(actual, expected, "{input}; ready slots: {should_use_slots}");
        }
    }
    client.batch_execute(&format!("DROP SCHEMA {schema} CASCADE")).await.unwrap();
    drop(client);
    driver.await.unwrap().unwrap();
}

#[tokio::test]
async fn paradedb_mixed_numeric_and_scalar_array_errors_are_preexisting() {
    let documents = documents();
    let fields = fields();
    assert_builtin_reference(&documents, &fields);
    let Ok(database_url) = std::env::var("DEX_ZD_PARADEDB_URL") else { return; };
    let (mut client, connection) = tokio_postgres::connect(&database_url, NoTls).await.unwrap();
    let driver = tokio::spawn(connection);
    let nonce = SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos();
    let schema = format!("dexzd_legacy_mixed_{}_{nonce}", std::process::id());
    client.batch_execute(&format!("CREATE SCHEMA {schema}; SET search_path={schema},public,pdb")).await.unwrap();
    ensure_schema_for_dialect(&mut client, VisibilityDialect::ParadeDb).await.unwrap();
    let row = SearchRow::from_document(TenantId(1), 1, &documents[0], &documents[0].index_types).unwrap();
    let statement = crate::sql_visibility::upsert_statement(&row);
    client.execute_typed(&statement.sql, &statement.typed_parameter_refs()).await.unwrap();
    for input in ["number = 1", "tags = 'scalar'"] {
        let query = parse_and_check(input, &fields).unwrap();
        let statement = crate::sql_visibility::translate_query(&query, "1", 10, None).unwrap();
        let error = client.query_typed(&statement.sql, &statement.typed_parameter_refs()).await.expect_err("ParadeDB retains the legacy malformed-type query error");
        assert!(matches!(error.as_db_error().unwrap().code().code(), "22P02" | "22023"), "{input}: {error}");
    }
    client.batch_execute(&format!("DROP SCHEMA {schema} CASCADE")).await.unwrap();
    drop(client);
    driver.await.unwrap().unwrap();
}
