// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;
use std::time::Duration;

use dexzd_protocol::dex::Value;

use crate::error::AttributeStoreError;
use crate::map_table::MapTableSchema;
use crate::row_key::RowKeyTemplate;
use crate::schema::{ResolvedTables, TableReference, TableSchema};
use crate::settings::{
    AttributeMapTableSettings, AttributeStoreSettings, ColumnNaming, FencingToken,
};
use crate::statements::{
    build_fencing_statement, build_keyed_delete, build_keyed_upsert, fencing_table_ddl,
};
use crate::store::{
    AttributeStore, jitter_interval, path_escape, path_unescape, timeout_milliseconds,
};
use crate::value_conversion::SqlArgument;
use crate::value_conversion_tests::{column, int_value, null_value, object_value, string_value};

fn reference(namespace: &str, table: &str) -> TableReference {
    TableReference {
        namespace: namespace.to_owned(),
        table: table.to_owned(),
    }
}

fn text(value: &str) -> SqlArgument {
    SqlArgument::Text(value.to_owned())
}

/// Callers spawn store futures on a multi-threaded runtime.
fn assert_send<T: Send>(_future: &T) {}

#[test]
fn upsert_quotes_identifiers_and_binds_values_like_go() {
    let values = BTreeMap::from([
        ("name".to_owned(), text("flow")),
        ("select".to_owned(), text("7")),
    ]);
    let statement = build_keyed_upsert(
        &reference("reporting", "flow attributes"),
        &["FlowID".to_owned()],
        &["flow-id".to_owned()],
        &["name".to_owned(), "select".to_owned()],
        &values,
    );
    assert_eq!(
        statement.query,
        r#"INSERT INTO "reporting"."flow attributes" ("FlowID", "name", "select") VALUES ($1, $2, $3) ON CONFLICT ("FlowID") DO UPDATE SET "name" = EXCLUDED."name", "select" = EXCLUDED."select""#
    );
    assert_eq!(
        statement.arguments,
        [text("flow-id"), text("flow"), text("7")]
    );
    assert_eq!(statement.table, r#""reporting"."flow attributes""#);

    let malicious = r#"profile"; DROP TABLE audit; --"#;
    let statement = build_keyed_upsert(
        &reference("public", "t"),
        &["flow_id".to_owned()],
        &["flow-value'; DROP TABLE flows; --".to_owned()],
        &[malicious.to_owned()],
        &BTreeMap::from([(
            malicious.to_owned(),
            text("attribute-value'; DROP TABLE values; --"),
        )]),
    );
    assert!(
        statement
            .query
            .contains(r#""profile""; DROP TABLE audit; --""#),
        "{}",
        statement.query
    );
    assert!(!statement.query.contains("attribute-value"));
    assert!(!statement.query.contains("flow-value"));
}

#[test]
fn instance_upsert_writes_defaults_and_key_only_rows_do_nothing() {
    let keys = ["group_id".to_owned(), "account_id".to_owned()];
    let key_values = ["g1".to_owned(), "a/1".to_owned()];
    let statement = build_keyed_upsert(
        &reference("public", "members"),
        &keys,
        &key_values,
        &["role".to_owned(), "status".to_owned()],
        &BTreeMap::from([("role".to_owned(), text("ADMIN"))]),
    );
    assert_eq!(
        statement.query,
        r#"INSERT INTO "public"."members" ("group_id", "account_id", "role", "status") VALUES ($1, $2, $3, DEFAULT) ON CONFLICT ("group_id", "account_id") DO UPDATE SET "role" = EXCLUDED."role", "status" = EXCLUDED."status""#
    );
    assert_eq!(
        statement.arguments,
        [text("g1"), text("a/1"), text("ADMIN")]
    );

    let statement = build_keyed_upsert(
        &reference("public", "members"),
        &keys,
        &key_values,
        &[],
        &BTreeMap::new(),
    );
    assert_eq!(
        statement.query,
        r#"INSERT INTO "public"."members" ("group_id", "account_id") VALUES ($1, $2) ON CONFLICT ("group_id", "account_id") DO NOTHING"#
    );

    let statement = build_keyed_delete(&reference("public", "members"), &keys, &key_values);
    assert_eq!(
        statement.query,
        r#"DELETE FROM "public"."members" WHERE "group_id" = $1 AND "account_id" = $2"#
    );
    assert_eq!(statement.arguments, [text("g1"), text("a/1")]);
}

#[test]
fn fencing_statement_and_ddl_text() {
    let fence = reference("ops", "dexzd_attribute_sync_fencing");
    let statement = build_fencing_statement(&fence, "orders", "0190-cluster", "node:a", 7);
    assert_eq!(
        statement.query,
        r#"INSERT INTO "ops"."dexzd_attribute_sync_fencing" AS existing (store_name, cluster_id, scope, fencing_token, updated_at) VALUES ($1, $2, $3, $4, now()) ON CONFLICT (store_name, cluster_id, scope) DO UPDATE SET fencing_token = EXCLUDED.fencing_token, updated_at = now() WHERE existing.fencing_token <= EXCLUDED.fencing_token"#
    );
    assert_eq!(
        statement.arguments,
        [
            text("orders"),
            text("0190-cluster"),
            text("node:a"),
            text("7")
        ]
    );
    assert_eq!(
        fencing_table_ddl(&fence),
        r#"CREATE TABLE "ops"."dexzd_attribute_sync_fencing" (store_name text NOT NULL, cluster_id text NOT NULL, scope text NOT NULL, fencing_token bigint NOT NULL, updated_at timestamptz NOT NULL, PRIMARY KEY (store_name, cluster_id, scope));"#
    );
}

#[test]
fn batch_statements_follow_go_order_and_filtering() {
    let mut settings = AttributeStoreSettings::new("groups", "host=localhost", "public.groups");
    settings.row_key = Some("group-{group_id}".to_owned());
    settings.column_naming = ColumnNaming::SnakeCase;
    settings.attribute_maps = BTreeMap::from([
        (
            "Members".to_owned(),
            AttributeMapTableSettings {
                table_name: "public.members".to_owned(),
                instance_key_column: "account_id".to_owned(),
            },
        ),
        (
            "Labels".to_owned(),
            AttributeMapTableSettings {
                table_name: "public.labels".to_owned(),
                instance_key_column: "label_key".to_owned(),
            },
        ),
    ]);
    let group_columns = [
        column("group_id", "text"),
        column("display_name", "text"),
        column("count_value", "bigint"),
    ];
    let member_columns = [
        column("group_id", "text"),
        column("account_id", "text"),
        column("role", "text"),
    ];
    let label_columns = [
        column("group_id", "text"),
        column("label_key", "text"),
        column("value", "text"),
    ];
    let by_name = |columns: &[crate::value_conversion::ColumnSchema]| {
        columns
            .iter()
            .map(|column| (column.name.clone(), column.clone()))
            .collect::<BTreeMap<_, _>>()
    };
    let group_key = ["group_id".to_owned()];
    let members = MapTableSchema::new(
        reference("public", "members"),
        "account_id",
        &group_key,
        &["group_id".to_owned(), "account_id".to_owned()],
        by_name(&member_columns),
    )
    .expect("valid members table");
    let labels = MapTableSchema::new(
        reference("public", "labels"),
        "label_key",
        &group_key,
        &["group_id".to_owned(), "label_key".to_owned()],
        by_name(&label_columns),
    )
    .expect("valid labels table");
    let snapshot = TableSchema {
        reference: reference("public", "groups"),
        flow_id_column: None,
        row_key: Some(RowKeyTemplate::parse("group-{group_id}").expect("valid template")),
        columns: by_name(&group_columns),
        map_tables: BTreeMap::from([
            ("Members".to_owned(), members),
            ("Labels".to_owned(), labels),
        ]),
    };
    let tables = ResolvedTables {
        table: reference("public", "groups"),
        map_tables: BTreeMap::new(),
        fencing_table: reference("public", "dexzd_attribute_sync_fencing"),
    };
    let store = AttributeStore::new_for_test(settings, tables, snapshot);
    let fence = FencingToken {
        cluster_id: "cluster-a".to_owned(),
        scope: "node:a".to_owned(),
        token: 1,
    };
    assert_send(&store.write_batch("group-g1", &[], &fence));
    assert_send(&store.refresh_schema());
    let snapshot = store.current_schema();
    assert_eq!(snapshot.key_values("other-g1"), None);
    let key_values = snapshot.key_values("group-g1").expect("matching Flow ID");
    let latest: BTreeMap<String, Value> = [
        ("DisplayName", string_value("Core")),
        ("CountValue", int_value(i64::MAX)),
        ("GroupID", string_value("g2")),
        ("Missing", string_value("filtered")),
        ("Shared", string_value("a")),
        ("shared", string_value("b")),
        (
            "Members/account%2F1",
            object_value("json", r#"{"Role":"ADMIN"}"#),
        ),
        ("Members/account-2", null_value()),
        ("Members/%ZZ", object_value("json", r#"{"Role":"INVALID"}"#)),
        ("Members/", object_value("json", "{}")),
        ("Labels/color", string_value("blue")),
        ("Unconfigured/instance", string_value("filtered")),
    ]
    .into_iter()
    .map(|(key, value)| (key.to_owned(), value))
    .collect();
    let statements = store.build_statements(&snapshot, &key_values, &latest);
    let queries = statements
        .iter()
        .map(|statement| statement.query.as_str())
        .collect::<Vec<_>>();
    assert_eq!(
        queries,
        [
            r#"INSERT INTO "public"."groups" ("group_id", "count_value", "display_name") VALUES ($1, $2, $3) ON CONFLICT ("group_id") DO UPDATE SET "count_value" = EXCLUDED."count_value", "display_name" = EXCLUDED."display_name""#,
            r#"INSERT INTO "public"."labels" ("group_id", "label_key", "value") VALUES ($1, $2, $3) ON CONFLICT ("group_id", "label_key") DO UPDATE SET "value" = EXCLUDED."value""#,
            r#"DELETE FROM "public"."members" WHERE "group_id" = $1 AND "account_id" = $2"#,
            r#"INSERT INTO "public"."members" ("group_id", "account_id", "role") VALUES ($1, $2, $3) ON CONFLICT ("group_id", "account_id") DO UPDATE SET "role" = EXCLUDED."role""#,
        ]
    );
    assert_eq!(
        statements[0].arguments,
        [text("g1"), text("9223372036854775807"), text("Core")]
    );
    assert_eq!(
        statements[1].arguments,
        [text("g1"), text("color"), text("blue")]
    );
    assert_eq!(statements[2].arguments, [text("g1"), text("account-2")]);
    assert_eq!(
        statements[3].arguments,
        [text("g1"), text("account/1"), text("ADMIN")]
    );

    let nothing = BTreeMap::from([("Missing".to_owned(), string_value("filtered"))]);
    assert!(
        store
            .build_statements(&snapshot, &key_values, &nothing)
            .is_empty()
    );
}

#[test]
fn instance_keys_unescape_like_go_path_unescape() {
    assert_eq!(path_unescape("account%2F1").as_deref(), Some("account/1"));
    assert_eq!(path_unescape("a+b").as_deref(), Some("a+b"));
    assert_eq!(path_unescape("%E4%B8%83").as_deref(), Some("七"));
    assert_eq!(path_unescape("%ZZ"), None);
    assert_eq!(path_unescape("%4"), None);
    assert_eq!(path_unescape("%"), None);
    assert_eq!(path_unescape("%FF"), None);
    assert_eq!(
        path_escape("account/1;a,b?c d"),
        "account%2F1%3Ba%2Cb%3Fc%20d"
    );
    assert_eq!(path_escape("a-b_c.d~e$&+:=@"), "a-b_c.d~e$&+:=@");
}

#[test]
fn transaction_timeouts_round_up_to_milliseconds() {
    assert_eq!(timeout_milliseconds(Duration::from_secs(5)), 5_000);
    assert_eq!(timeout_milliseconds(Duration::from_micros(1_500)), 2);
    assert_eq!(timeout_milliseconds(Duration::from_nanos(1)), 1);
    assert_eq!(timeout_milliseconds(Duration::MAX), 2_147_483_647);
    assert_eq!(jitter_interval(Duration::MAX, 0.99), Duration::MAX);
}

#[test]
fn errors_classify_retries_and_name_the_store() {
    let fenced = AttributeStoreError::Fenced {
        store: "orders".to_owned(),
        cluster_id: "c1".to_owned(),
        scope: "node:a".to_owned(),
        token: 3,
    };
    assert!(!fenced.is_retryable());
    assert!(fenced.to_string().contains("\"orders\""), "{fenced}");
    let database = AttributeStoreError::Database {
        store: "orders".to_owned(),
        operation: "write table \"public\".\"orders\"".to_owned(),
        message: "SQLSTATE 23514: check violation".to_owned(),
        sqlstate: Some("23514".to_owned()),
    };
    assert!(database.is_retryable());
    assert_eq!(database.sqlstate(), Some("23514"));
    let timeout = AttributeStoreError::Timeout {
        store: "orders".to_owned(),
        operation: "connect to Postgres".to_owned(),
        timeout: Duration::from_secs(5),
    };
    assert!(timeout.is_retryable());
    for error in [
        AttributeStoreError::InvalidFencingToken {
            store: "orders".to_owned(),
            token: u64::MAX,
        },
        AttributeStoreError::InvalidTable {
            store: "orders".to_owned(),
            table: "\"public\".\"orders\"".to_owned(),
            reason: "table does not exist or has no columns".to_owned(),
        },
        AttributeStoreError::InvalidSettings {
            store: "orders".to_owned(),
            reason: "attribute store \"orders\" requires dsn".to_owned(),
        },
    ] {
        assert!(!error.is_retryable(), "{error}");
    }
}
