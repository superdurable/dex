// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Ports of the Go Attribute Store integration tests; they run only with `DEX_ZD_PARADEDB_URL`.

use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use dexzd_protocol::dex::Value;
use tokio::task::JoinHandle;
use tokio_postgres::{Client, NoTls};
use tokio_util::sync::CancellationToken;

use crate::error::{AttributeStoreError, AttributeStoreFailureScope};
use crate::schema::TableReference;
use crate::settings::{
    AttributeMapTableSettings, AttributeStoreSettings, AttributeSyncItem, ColumnNaming,
    FencingToken,
};
use crate::statements::fencing_table_ddl;
use crate::store::AttributeStore;
use crate::value_conversion_tests::{
    bool_value, double_value, int_value, null_value, object_value, string_value,
};

const DATABASE_URL_VARIABLE: &str = "DEX_ZD_PARADEDB_URL";
const FENCING_TABLE: &str = "fence";

struct TestDatabase {
    client: Client,
    connection_task: JoinHandle<()>,
    url: String,
    schema: String,
}

impl TestDatabase {
    async fn open(test_name: &str) -> Option<Self> {
        let url = std::env::var(DATABASE_URL_VARIABLE).ok()?;
        let client_and_task = connect_admin(&url).await;
        let schema = format!("das_{test_name}_{}", unique_suffix());
        let database = Self {
            client: client_and_task.0,
            connection_task: client_and_task.1,
            url,
            schema,
        };
        database.execute(&["CREATE SCHEMA {s}"]).await;
        Some(database)
    }

    /// Runs each statement with `{s}` replaced by the test schema.
    async fn execute(&self, statements: &[&str]) {
        for statement in statements {
            let statement = statement.replace("{s}", &self.schema);
            self.client
                .batch_execute(&statement)
                .await
                .unwrap_or_else(|error| panic!("{statement}: {error:?}"));
        }
    }

    async fn create_fencing_table(&self) {
        let ddl = fencing_table_ddl(&TableReference {
            namespace: self.schema.clone(),
            table: FENCING_TABLE.to_owned(),
        });
        self.client
            .batch_execute(&ddl)
            .await
            .expect("create the fencing table");
    }

    fn settings(&self, name: &str, table: &str) -> AttributeStoreSettings {
        let mut settings =
            AttributeStoreSettings::new(name, self.url.clone(), format!("{}.{table}", self.schema));
        settings.fencing_table_name = format!("{}.{FENCING_TABLE}", self.schema);
        settings
    }

    fn map_table(&self, table: &str, instance_key_column: &str) -> AttributeMapTableSettings {
        AttributeMapTableSettings {
            table_name: format!("{}.{table}", self.schema),
            instance_key_column: instance_key_column.to_owned(),
        }
    }

    async fn strings(&self, query: &str) -> Vec<String> {
        let query = query.replace("{s}", &self.schema);
        self.client
            .query(&query, &[])
            .await
            .unwrap_or_else(|error| panic!("{query}: {error:?}"))
            .iter()
            .map(|row| {
                row.get::<_, Option<String>>(0)
                    .unwrap_or_else(|| "<null>".to_owned())
            })
            .collect()
    }

    async fn string(&self, query: &str) -> String {
        let mut values = self.strings(query).await;
        assert_eq!(values.len(), 1, "{query} returned {values:?}");
        values.remove(0)
    }

    async fn drop_schema(self) {
        self.execute(&["DROP SCHEMA {s} CASCADE"]).await;
        self.connection_task.abort();
    }
}

async fn connect_admin(url: &str) -> (Client, JoinHandle<()>) {
    let (client, connection) = tokio_postgres::connect(url, NoTls)
        .await
        .expect("connect to the CI database");
    let connection_task = tokio::spawn(async move {
        if let Err(error) = connection.await {
            tracing::warn!(%error, "test database connection closed");
        }
    });
    (client, connection_task)
}

fn unique_suffix() -> String {
    let nanoseconds = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("wall clock after the Unix epoch")
        .as_nanos();
    format!("{}_{nanoseconds}", std::process::id())
}

fn fence(cluster_id: &str, token: u64) -> FencingToken {
    FencingToken {
        cluster_id: cluster_id.to_owned(),
        scope: "node:a".to_owned(),
        token,
    }
}

fn items(pairs: Vec<(&str, Value)>) -> Vec<AttributeSyncItem> {
    pairs
        .into_iter()
        .map(|(key, value)| AttributeSyncItem {
            key: key.to_owned(),
            value: Some(value),
        })
        .collect()
}

async fn connect(settings: AttributeStoreSettings) -> AttributeStore {
    match AttributeStore::connect_and_validate(settings).await {
        Ok(store) => store,
        Err(error) => panic!("connect the Attribute Store: {error}"),
    }
}

async fn connect_error(settings: AttributeStoreSettings) -> AttributeStoreError {
    match AttributeStore::connect_and_validate(settings).await {
        Ok(_) => panic!("startup must fail"),
        Err(error) => error,
    }
}

async fn write(store: &AttributeStore, flow_id: &str, pairs: Vec<(&str, Value)>) {
    store
        .write_batch(flow_id, &items(pairs), &fence("cluster-a", 1))
        .await
        .unwrap_or_else(|error| panic!("write {flow_id}: {error}"));
}

async fn write_status(
    store: &AttributeStore,
    cluster_id: &str,
    token: u64,
    status: &str,
) -> Result<(), AttributeStoreError> {
    let batch = items(vec![("status", string_value(status))]);
    store
        .write_batch("o1", &batch, &fence(cluster_id, token))
        .await
}

#[tokio::test]
async fn postgres_main_row_writes_overwrites_and_nulls() {
    let Some(database) = TestDatabase::open("main").await else {
        return;
    };
    database
        .execute(&["CREATE TABLE {s}.flow_attributes (flow_id TEXT PRIMARY KEY, name VARCHAR(32), \
             count_value BIGINT, small_value INTEGER, ratio DOUBLE PRECISION, real_value REAL, amount NUMERIC, \
             active BOOLEAN, logged_at TIMESTAMPTZ, json_value JSONB, plain_json JSON, binary_value BYTEA, \
             nullable_value TEXT)"])
        .await;
    database.create_fencing_table().await;
    let mut settings = database.settings("reporting", "flow_attributes");
    settings.schema_sync_interval = Duration::from_millis(50);
    let store = Arc::new(connect(settings).await);
    write(
        &store,
        "postgres-flow",
        vec![
            ("name", string_value("old")),
            ("count_value", int_value(42)),
            ("small_value", int_value(7)),
            ("ratio", double_value(2.5)),
            ("real_value", double_value(0.1)),
            ("amount", double_value(0.1 + 0.2)),
            ("active", bool_value(true)),
            (
                "logged_at",
                object_value("json", r#""2026-08-11T15:30:00.123456789Z""#),
            ),
            ("json_value", object_value("json", r#"{"ok":true}"#)),
            ("plain_json", object_value("json", r#"{"b": 1,  "a":2}"#)),
            ("binary_value", object_value("proto", "bytes")),
            ("nullable_value", null_value()),
            ("missing", string_value("filtered")),
            ("small_value", int_value(i64::from(i32::MAX) + 1)),
            ("name", string_value("latest")),
        ],
    )
    .await;
    let row = database
        .client
        .query_one(
            &format!(
                "SELECT name, count_value, small_value, ratio, real_value, amount::text, active, \
                 logged_at = TIMESTAMPTZ '2026-08-11 15:30:00.123456+00', json_value::text, plain_json::text, \
                 binary_value, nullable_value FROM {}.flow_attributes WHERE flow_id = 'postgres-flow'",
                database.schema
            ),
            &[],
        )
        .await
        .expect("read the written row");
    assert_eq!(row.get::<_, String>(0), "latest");
    assert_eq!(row.get::<_, i64>(1), 42);
    assert_eq!(
        row.get::<_, Option<i32>>(2),
        None,
        "an out-of-range duplicate wins and is filtered"
    );
    assert_eq!(row.get::<_, f64>(3), 2.5);
    assert_eq!(row.get::<_, f32>(4), 0.1_f32);
    assert_eq!(row.get::<_, String>(5), "0.30000000000000004");
    assert!(row.get::<_, bool>(6));
    assert!(
        row.get::<_, bool>(7),
        "timestamps truncate to microseconds like pgx"
    );
    assert_eq!(row.get::<_, String>(8), r#"{"ok": true}"#);
    assert_eq!(row.get::<_, String>(9), r#"{"b": 1,  "a":2}"#);
    assert_eq!(row.get::<_, Vec<u8>>(10), b"bytes");
    assert_eq!(row.get::<_, Option<String>>(11), None);

    store
        .write_batch(
            "postgres-flow",
            &[AttributeSyncItem {
                key: "name".to_owned(),
                value: None,
            }],
            &fence("cluster-a", 1),
        )
        .await
        .expect("write a null");
    write(
        &store,
        "postgres-flow",
        vec![("count_value", null_value()), ("small_value", int_value(9))],
    )
    .await;
    assert_eq!(
        database
            .strings("SELECT concat_ws(',', coalesce(name, '<null>'), coalesce(count_value::text, '<null>'), small_value) FROM {s}.flow_attributes")
            .await,
        ["<null>,<null>,9"]
    );

    let previous = store.current_schema();
    database
        .execute(&["ALTER TABLE {s}.flow_attributes RENAME TO flow_attributes_hidden"])
        .await;
    let error = store
        .refresh_schema()
        .await
        .expect_err("the table is hidden");
    assert!(error.to_string().contains("does not exist"), "{error}");
    assert_eq!(
        *store.current_schema(),
        *previous,
        "a failed refresh keeps the snapshot"
    );
    database
        .execute(&[
            "ALTER TABLE {s}.flow_attributes_hidden RENAME TO flow_attributes",
            "ALTER TABLE {s}.flow_attributes ADD COLUMN late_column TEXT",
        ])
        .await;
    let cancel = CancellationToken::new();
    let refresher = store.spawn_schema_refresher(cancel.clone());
    let deadline = tokio::time::Instant::now() + Duration::from_secs(10);
    while !store.current_schema().columns.contains_key("late_column") {
        assert!(
            tokio::time::Instant::now() < deadline,
            "the periodic refresh finds late_column"
        );
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    cancel.cancel();
    refresher
        .await
        .expect("the refresher stops on cancellation");
    write(
        &store,
        "postgres-flow",
        vec![("late_column", string_value("available"))],
    )
    .await;
    assert_eq!(
        database
            .string("SELECT late_column FROM {s}.flow_attributes WHERE flow_id = 'postgres-flow'")
            .await,
        "available"
    );
    database.drop_schema().await;
}

#[tokio::test]
async fn postgres_startup_rejects_invalid_store_tables() {
    let Some(database) = TestDatabase::open("invalid").await else {
        return;
    };
    database
        .execute(&[
            "CREATE TABLE {s}.no_primary_key (flow_id TEXT)",
            "CREATE TABLE {s}.composite_primary_key (first_id TEXT, second_id TEXT, PRIMARY KEY (first_id, second_id))",
            "CREATE TABLE {s}.integer_primary_key (flow_id BIGINT PRIMARY KEY)",
            "CREATE TABLE {s}.required_column (flow_id TEXT PRIMARY KEY, required_value TEXT NOT NULL)",
            "CREATE TABLE {s}.valid_table (flow_id TEXT PRIMARY KEY, value TEXT)",
        ])
        .await;
    database.create_fencing_table().await;
    for (table, message) in [
        ("missing_table", "does not exist"),
        ("no_primary_key", "exactly one primary-key column"),
        ("composite_primary_key", "exactly one primary-key column"),
        ("integer_primary_key", "cannot store FlowID strings"),
        ("required_column", "prevents partial row inserts"),
    ] {
        let error = connect_error(database.settings("invalid", table)).await;
        assert!(
            matches!(error, AttributeStoreError::InvalidTable { .. }),
            "{table}: {error}"
        );
        assert!(error.to_string().contains(message), "{table}: {error}");
        assert!(!error.is_retryable(), "{table}: {error}");
    }
    let mut settings = database.settings("invalid", "valid_table");
    settings.row_key = Some("order-{customer_id".to_owned());
    let error = connect_error(settings).await;
    assert!(
        error.to_string().contains("unterminated placeholder"),
        "{error}"
    );
    let mut settings = database.settings("invalid", "valid_table");
    settings.row_key = Some(String::new());
    let store = connect(settings).await;
    assert_eq!(
        store.current_schema().key_values("o1"),
        Some(vec!["o1".to_owned()])
    );
    drop(store);

    let separator = if database.url.contains('?') { '&' } else { '?' };
    let mut settings = database.settings("invalid", "valid_table");
    settings.dsn = format!("{}{separator}options=-c%20search_path%3D", database.url);
    settings.attribute_maps = BTreeMap::from([(
        "Members".to_owned(),
        AttributeMapTableSettings {
            table_name: "members".to_owned(),
            instance_key_column: "account_id".to_owned(),
        },
    )]);
    let error = connect_error(settings).await;
    assert!(
        matches!(error, AttributeStoreError::InvalidTable { .. }),
        "{error}"
    );
    assert!(
        error
            .to_string()
            .contains(r#"AttributeMap "Members": database has no active namespace"#),
        "{error}"
    );
    database.drop_schema().await;
}

#[tokio::test]
async fn postgres_row_keys_split_flow_ids_into_snake_case_rows() {
    let Some(database) = TestDatabase::open("rowkey").await else {
        return;
    };
    database
        .execute(&[
            "CREATE TABLE {s}.row_key_orders (customer_id TEXT, order_id TEXT, order_status TEXT, shipping_url TEXT, \
             s3_bucket TEXT, url_path TEXT, item_count BIGINT, PRIMARY KEY (customer_id, order_id))",
            "CREATE TABLE {s}.row_key_customers (customer_id TEXT PRIMARY KEY, display_name TEXT, onboarding_status TEXT)",
            "CREATE TABLE {s}.row_key_integer_key (customer_id BIGINT, order_id TEXT, PRIMARY KEY (customer_id, order_id))",
        ])
        .await;
    database.create_fencing_table().await;
    for (table, row_key, message) in [
        (
            "row_key_customers",
            "order-{customer_id}-{order_id}",
            "must be exactly the rowKey columns",
        ),
        (
            "row_key_orders",
            "order-{customer_id}",
            "must be exactly the rowKey columns",
        ),
        (
            "row_key_integer_key",
            "order-{customer_id}-{order_id}",
            "cannot store strings",
        ),
        (
            "row_key_orders",
            "order-{customer_id}{order_id}",
            "separated by literal text",
        ),
        ("row_key_orders", "order-{*}", "at least one column"),
    ] {
        let mut settings = database.settings("invalid", table);
        settings.row_key = Some(row_key.to_owned());
        let error = connect_error(settings).await;
        assert!(error.to_string().contains(message), "{row_key}: {error}");
    }
    let store_for = |name: &str, table: &str, row_key: &str| {
        let mut settings = database.settings(name, table);
        settings.row_key = Some(row_key.to_owned());
        settings.column_naming = ColumnNaming::SnakeCase;
        settings
    };
    let orders = connect(store_for(
        "orders",
        "row_key_orders",
        "order-{customer_id}-{order_id}",
    ))
    .await;
    let customers = connect(store_for(
        "customers",
        "row_key_customers",
        "customer-{customer_id}",
    ))
    .await;
    let onboarding = connect(store_for(
        "onboarding",
        "row_key_customers",
        "onboarding-{customer_id}-{*}",
    ))
    .await;
    let order_row = "SELECT concat_ws('|', order_status, shipping_url, s3_bucket, url_path, item_count) \
                     FROM {s}.row_key_orders WHERE customer_id = 'c1' AND order_id = 'o-2-x'";

    write(
        &orders,
        "order-c1-o-2-x",
        vec![
            ("OrderStatus", string_value("PLACED")),
            ("ShippingURL", string_value("https://example.com/c1")),
            ("S3Bucket", string_value("bucket")),
            ("URLPath", string_value("/orders")),
            ("ItemCount", int_value(3)),
        ],
    )
    .await;
    assert_eq!(
        database.string(order_row).await,
        "PLACED|https://example.com/c1|bucket|/orders|3"
    );

    write(
        &orders,
        "order-c1-o-2-x",
        vec![
            ("CustomerID", string_value("c2")),
            ("ShippingURL", string_value("https://example.com/ignored")),
            (
                "shipping_url",
                string_value("https://example.com/also-ignored"),
            ),
            ("OrderStatus", string_value("SHIPPED")),
        ],
    )
    .await;
    assert_eq!(
        database.string(order_row).await,
        "SHIPPED|https://example.com/c1|bucket|/orders|3"
    );

    write(
        &orders,
        "invoice-c1-o-3",
        vec![("OrderStatus", string_value("PLACED"))],
    )
    .await;
    write(
        &orders,
        "order-c1-",
        vec![("OrderStatus", string_value("PLACED"))],
    )
    .await;
    assert_eq!(
        database
            .string("SELECT count(*)::text FROM {s}.row_key_orders")
            .await,
        "1"
    );

    write(
        &customers,
        "customer-c1",
        vec![("DisplayName", string_value("Ada"))],
    )
    .await;
    write(
        &onboarding,
        "onboarding-c1-request-7",
        vec![("OnboardingStatus", string_value("READY"))],
    )
    .await;
    write(
        &customers,
        "customer-c1",
        vec![("DisplayName", string_value("Ada Lovelace"))],
    )
    .await;
    assert_eq!(
        database
            .string("SELECT display_name || '|' || onboarding_status FROM {s}.row_key_customers WHERE customer_id = 'c1'")
            .await,
        "Ada Lovelace|READY"
    );
    database.drop_schema().await;
}

#[tokio::test]
async fn postgres_map_tables_replace_and_delete_instances() {
    let Some(database) = TestDatabase::open("maps").await else {
        return;
    };
    database
        .execute(&[
            "CREATE TABLE {s}.map_groups (group_id TEXT PRIMARY KEY, display_name TEXT)",
            "CREATE TABLE {s}.map_group_members (group_id TEXT, account_id TEXT, role TEXT, added_at TIMESTAMPTZ, \
             added_by_account_id TEXT, score DOUBLE PRECISION, profile JSONB, status VARCHAR(16) DEFAULT 'active', \
             PRIMARY KEY (group_id, account_id))",
            "CREATE TABLE {s}.map_group_labels (group_id TEXT, label_key TEXT, value TEXT, PRIMARY KEY (group_id, label_key))",
            "CREATE TABLE {s}.map_plain (flow_id TEXT PRIMARY KEY, note TEXT)",
            "CREATE TABLE {s}.map_plain_items (flow_id TEXT, item_id TEXT, value TEXT, PRIMARY KEY (flow_id, item_id))",
            "CREATE TABLE {s}.map_bad_key (account_id TEXT PRIMARY KEY, role TEXT)",
            "CREATE TABLE {s}.map_integer_key (group_id TEXT, account_id BIGINT, PRIMARY KEY (group_id, account_id))",
            "CREATE TABLE {s}.map_required_column (group_id TEXT, account_id TEXT, role TEXT NOT NULL, \
             PRIMARY KEY (group_id, account_id))",
        ])
        .await;
    database.create_fencing_table().await;
    for (map_name, table, instance_key_column, message) in [
        ("Members", "map_missing", "account_id", "does not exist"),
        (
            "Members",
            "map_bad_key",
            "account_id",
            "must be exactly the columns",
        ),
        (
            "Members",
            "map_integer_key",
            "account_id",
            "cannot store strings",
        ),
        (
            "Members",
            "map_required_column",
            "account_id",
            "prevents partial row inserts",
        ),
        (
            "Members",
            "map_group_members",
            "group_id",
            "also a primary-key column",
        ),
        (
            "Members/All",
            "map_group_members",
            "account_id",
            "contain no '/'",
        ),
    ] {
        let mut settings = database.settings("invalid", "map_groups");
        settings.row_key = Some("group-{group_id}".to_owned());
        settings.attribute_maps = BTreeMap::from([(
            map_name.to_owned(),
            database.map_table(table, instance_key_column),
        )]);
        let error = connect_error(settings).await;
        assert!(error.to_string().contains(message), "{table}: {error}");
    }
    let mut groups_settings = database.settings("groups", "map_groups");
    groups_settings.row_key = Some("group-{group_id}".to_owned());
    groups_settings.column_naming = ColumnNaming::SnakeCase;
    groups_settings.attribute_maps = BTreeMap::from([
        (
            "Members".to_owned(),
            database.map_table("map_group_members", "account_id"),
        ),
        (
            "Labels".to_owned(),
            database.map_table("map_group_labels", "label_key"),
        ),
    ]);
    let groups = connect(groups_settings).await;
    let mut plain_settings = database.settings("plain", "map_plain");
    plain_settings.attribute_maps = BTreeMap::from([(
        "Items".to_owned(),
        database.map_table("map_plain_items", "item_id"),
    )]);
    let plain = connect(plain_settings).await;
    let member = |account_id: &str| {
        format!(
            "SELECT concat_ws('|', coalesce(role, '<null>'), coalesce(added_at = TIMESTAMPTZ '2026-08-11 15:30:00+00', false), \
             coalesce(added_by_account_id, '<null>'), coalesce(score::text, '<null>'), coalesce(profile::text, '<null>'), \
             coalesce(status, '<null>')) FROM {{s}}.map_group_members WHERE group_id = 'g1' AND account_id = '{account_id}'"
        )
    };
    let member_ids =
        "SELECT account_id FROM {s}.map_group_members WHERE group_id = 'g1' ORDER BY account_id";
    let display_name = "SELECT display_name FROM {s}.map_groups WHERE group_id = 'g1'";

    write(
        &groups,
        "group-g1",
        vec![
            ("DisplayName", string_value("Core")),
            (
                "Members/account%2F1",
                object_value(
                    "json",
                    r#"{"Role":"ADMIN","AddedAt":"2026-08-11T15:30:00Z","AddedByAccountID":"account/1","Score":3,
                    "Profile":{"tags":["a"]},"Unknown":"skipped","GroupID":"g1","AccountID":"account/1"}"#,
                ),
            ),
            (
                "Members/account-2",
                object_value(
                    "json",
                    r#"{"Role":"MEMBER","AddedAt":"2026-08-11T15:31:00Z","Score":2.5,"Status":"suspended",
                    "Unknown":"skipped","GroupID":"other"}"#,
                ),
            ),
            ("Labels/color", string_value("blue")),
        ],
    )
    .await;
    assert_eq!(database.string(display_name).await, "Core");
    assert_eq!(
        database.string(&member("account/1")).await,
        r#"ADMIN|t|account/1|3|{"tags": ["a"]}|active"#
    );
    assert_eq!(
        database.string(&member("account-2")).await,
        "MEMBER|f|<null>|2.5|<null>|suspended"
    );
    assert_eq!(
        database
            .strings(
                "SELECT label_key || '=' || value FROM {s}.map_group_labels WHERE group_id = 'g1'"
            )
            .await,
        ["color=blue"]
    );
    assert_eq!(groups.missing_column_warning_count_for_test(), 1);

    write(
        &groups,
        "group-g1",
        vec![
            ("Members/account%2F1", null_value()),
            (
                "Members/account-2",
                object_value("json", r#"{"Role":"ADMIN"}"#),
            ),
            ("Members/account-3", object_value("json", "{}")),
            ("Members/%ZZ", object_value("json", r#"{"Role":"INVALID"}"#)),
            ("Labels/color", null_value()),
        ],
    )
    .await;
    assert_eq!(
        database.strings(member_ids).await,
        ["account-2", "account-3"]
    );
    assert_eq!(
        database.string(&member("account-2")).await,
        "ADMIN|f|<null>|<null>|<null>|active"
    );
    assert_eq!(
        database.string(&member("account-3")).await,
        "<null>|f|<null>|<null>|<null>|active"
    );
    assert!(
        database
            .strings("SELECT label_key FROM {s}.map_group_labels")
            .await
            .is_empty()
    );

    write(
        &groups,
        "group-g1",
        vec![
            (
                "Members/account-3",
                object_value("json", r#"{"Unknown":"skipped"}"#),
            ),
            (
                "Members/account-4",
                object_value("json", r#"{"Role":"MEMBER"}"#),
            ),
        ],
    )
    .await;
    assert_eq!(groups.missing_column_warning_count_for_test(), 1);
    assert_eq!(
        database.strings(member_ids).await,
        ["account-2", "account-3", "account-4"]
    );
    assert_eq!(database.string(display_name).await, "Core");

    database
        .execute(&["ALTER TABLE {s}.map_group_members DROP COLUMN added_by_account_id"])
        .await;
    let error = groups
        .write_batch(
            "group-g1",
            &items(vec![
                ("DisplayName", string_value("Rolled back")),
                (
                    "Members/account-5",
                    object_value("json", r#"{"AddedByAccountID":"x"}"#),
                ),
            ]),
            &fence("cluster-a", 1),
        )
        .await
        .expect_err("the stale snapshot writes a dropped column");
    assert!(error.is_retryable(), "{error}");
    assert_eq!(error.sqlstate(), Some("42703"), "{error}");
    assert_eq!(error.failure_scope(), AttributeStoreFailureScope::Batch);
    assert_eq!(database.string(display_name).await, "Core");
    assert_eq!(
        database.strings(member_ids).await,
        ["account-2", "account-3", "account-4"]
    );
    database
        .execute(&["ALTER TABLE {s}.map_group_members ADD COLUMN added_by_account_id TEXT"])
        .await;

    write(
        &plain,
        "plain-flow-1",
        vec![
            ("note", string_value("n")),
            ("Items/i1", string_value("v1")),
            (
                "Members/account-9",
                object_value("json", r#"{"Role":"ADMIN"}"#),
            ),
        ],
    )
    .await;
    assert_eq!(
        database
            .string("SELECT value FROM {s}.map_plain_items WHERE flow_id = 'plain-flow-1' AND item_id = 'i1'")
            .await,
        "v1"
    );
    assert_eq!(
        database
            .string("SELECT count(*)::text FROM {s}.map_plain")
            .await,
        "1"
    );
    assert_eq!(
        database.strings(member_ids).await,
        ["account-2", "account-3", "account-4"]
    );
    database.drop_schema().await;
}

#[tokio::test]
async fn postgres_unknown_columns_refresh_once() {
    let Some(database) = TestDatabase::open("refresh").await else {
        return;
    };
    database
        .execute(&[
            "CREATE TABLE {s}.refresh_orders (order_id TEXT PRIMARY KEY, status TEXT)",
            "CREATE TABLE {s}.refresh_order_items (order_id TEXT, item_id TEXT, PRIMARY KEY (order_id, item_id))",
        ])
        .await;
    database.create_fencing_table().await;
    let mut settings = database.settings("orders", "refresh_orders");
    settings.schema_sync_interval = Duration::from_secs(3_600);
    settings.column_naming = ColumnNaming::SnakeCase;
    settings.attribute_maps = BTreeMap::from([(
        "Items".to_owned(),
        database.map_table("refresh_order_items", "item_id"),
    )]);
    let store = connect(settings).await;
    write(&store, "o1", vec![("Status", string_value("PLACED"))]).await;

    // A refresh confirms Discount missing; it must not delay a column that a migration adds next.
    write(&store, "o1", vec![("Discount", int_value(5))]).await;
    database
        .execute(&[
            "ALTER TABLE {s}.refresh_orders ADD COLUMN shipping_note TEXT",
            "ALTER TABLE {s}.refresh_order_items ADD COLUMN quantity BIGINT",
        ])
        .await;
    write(
        &store,
        "o1",
        vec![
            ("ShippingNote", string_value("leave at door")),
            ("Items/i1", object_value("json", r#"{"Quantity":2}"#)),
        ],
    )
    .await;
    assert_eq!(
        database
            .string("SELECT shipping_note FROM {s}.refresh_orders WHERE order_id = 'o1'")
            .await,
        "leave at door"
    );
    assert_eq!(
        database
            .string("SELECT quantity::text FROM {s}.refresh_order_items WHERE order_id = 'o1' AND item_id = 'i1'")
            .await,
        "2"
    );

    // A column confirmed missing waits for the periodic refresh.
    database
        .execute(&["ALTER TABLE {s}.refresh_orders ADD COLUMN discount BIGINT"])
        .await;
    write(&store, "o1", vec![("Discount", int_value(7))]).await;
    let discount =
        "SELECT coalesce(discount::text, '<null>') FROM {s}.refresh_orders WHERE order_id = 'o1'";
    assert_eq!(database.string(discount).await, "<null>");
    store.refresh_schema().await.expect("refresh the schema");
    write(&store, "o1", vec![("Discount", int_value(7))]).await;
    assert_eq!(database.string(discount).await, "7");

    // A field an instance table does not project refreshes only the first time it appears.
    write(
        &store,
        "o1",
        vec![(
            "Items/i2",
            object_value("json", r#"{"Quantity":1,"Fingerprint":"a"}"#),
        )],
    )
    .await;
    let first_refresh = store.column_refresh_state_for_test().await.last_refresh;
    assert!(first_refresh.is_some());
    for _ in 0..3 {
        write(
            &store,
            "o1",
            vec![(
                "Items/i2",
                object_value("json", r#"{"Quantity":2,"Fingerprint":"b"}"#),
            )],
        )
        .await;
    }
    assert_eq!(
        store.column_refresh_state_for_test().await.last_refresh,
        first_refresh
    );
    assert_eq!(
        database
            .string("SELECT quantity::text FROM {s}.refresh_order_items WHERE order_id = 'o1' AND item_id = 'i2'")
            .await,
        "2"
    );
    database.drop_schema().await;
}

#[tokio::test]
async fn postgres_missing_fencing_table_reports_runnable_ddl() {
    let Some(database) = TestDatabase::open("fenceddl").await else {
        return;
    };
    database
        .execute(&[
            "CREATE TABLE {s}.orders (flow_id TEXT PRIMARY KEY, status TEXT)",
            "CREATE TABLE {s}.integer_fence (store_name TEXT, cluster_id TEXT, scope TEXT, fencing_token INTEGER, \
             updated_at TIMESTAMPTZ, PRIMARY KEY (store_name, cluster_id, scope))",
            "CREATE TABLE {s}.narrow_key_fence (store_name TEXT, cluster_id TEXT, scope TEXT, fencing_token BIGINT, \
             updated_at TIMESTAMPTZ, PRIMARY KEY (store_name, cluster_id))",
        ])
        .await;
    for (fencing_table, message) in [
        (
            "integer_fence",
            "\"fencing_token\" must exist with type bigint",
        ),
        (
            "narrow_key_fence",
            "primary key [store_name cluster_id] must be exactly",
        ),
    ] {
        let mut settings = database.settings("orders", "orders");
        settings.fencing_table_name = format!("{}.{fencing_table}", database.schema);
        let error = connect_error(settings).await;
        assert!(
            error.to_string().contains(message),
            "{fencing_table}: {error}"
        );
        assert!(error.to_string().contains("CREATE TABLE"), "{error}");
    }

    let separator = if database.url.contains('?') { '&' } else { '?' };
    let url_with_search_path = format!(
        "{}{separator}options=-c%20search_path%3D{}",
        database.url, database.schema
    );
    let settings = AttributeStoreSettings::new("orders", url_with_search_path, "orders");
    let error = connect_error(settings.clone()).await;
    let AttributeStoreError::InvalidFencingTable { table, ddl, .. } = &error else {
        panic!("expected a missing fencing table: {error}");
    };
    assert_eq!(
        *table,
        format!("\"{}\".\"dexzd_attribute_sync_fencing\"", database.schema)
    );
    assert!(error.to_string().contains(ddl.as_str()), "{error}");
    assert!(!error.is_retryable());
    database
        .client
        .batch_execute(ddl)
        .await
        .expect("the reported DDL runs");
    let store = connect(settings).await;
    write(&store, "o1", vec![("status", string_value("PLACED"))]).await;
    assert_eq!(
        database.string("SELECT status FROM {s}.orders").await,
        "PLACED"
    );
    database.drop_schema().await;
}

#[tokio::test]
async fn postgres_fencing_tokens_are_independent_per_cluster_and_store() {
    let Some(database) = TestDatabase::open("fencing").await else {
        return;
    };
    database
        .execute(&["CREATE TABLE {s}.orders (flow_id TEXT PRIMARY KEY, status TEXT, note TEXT)"])
        .await;
    database.create_fencing_table().await;
    let orders = connect(database.settings("orders", "orders")).await;
    let copy = connect(database.settings("orders-copy", "orders")).await;
    let status = "SELECT status FROM {s}.orders WHERE flow_id = 'o1'";

    write_status(&orders, "cluster-a", 1, "a1")
        .await
        .expect("first writer of cluster A");
    write_status(&orders, "cluster-b", 1, "b1")
        .await
        .expect("cluster B starts at token 1 too");
    write_status(&orders, "cluster-a", 5, "a5")
        .await
        .expect("a newer token for cluster A");
    let error = write_status(&orders, "cluster-a", 4, "a4")
        .await
        .expect_err("an older token is fenced");
    assert!(
        matches!(error, AttributeStoreError::Fenced { token: 4, .. }),
        "{error}"
    );
    assert!(!error.is_retryable());
    assert_eq!(
        database.string(status).await,
        "a5",
        "a fenced write rolls back"
    );
    write_status(&orders, "cluster-a", 5, "a5-again")
        .await
        .expect("an equal token is the same writer");
    write_status(&orders, "cluster-b", 2, "b2")
        .await
        .expect("cluster B is independent");
    write_status(&copy, "cluster-a", 1, "copy")
        .await
        .expect("another store is independent");
    assert_eq!(
        database
            .strings(
                "SELECT store_name || '/' || cluster_id || '/' || scope || '=' || fencing_token FROM {s}.fence \
                 ORDER BY store_name COLLATE \"C\", cluster_id COLLATE \"C\""
            )
            .await,
        [
            "orders/cluster-a/node:a=5",
            "orders/cluster-b/node:a=2",
            "orders-copy/cluster-a/node:a=1",
        ]
    );
    let error = orders
        .write_batch(
            "o1",
            &items(vec![("status", string_value("x"))]),
            &fence("cluster-a", u64::MAX),
        )
        .await
        .expect_err("the token exceeds bigint");
    assert!(
        matches!(error, AttributeStoreError::InvalidFencingToken { .. }),
        "{error}"
    );

    database
        .execute(&["DELETE FROM {s}.fence WHERE cluster_id = 'cluster-b'"])
        .await;
    write_status(&orders, "cluster-a", 5, "after-delete")
        .await
        .expect("cluster A is unaffected");
    let error = write_status(&orders, "cluster-a", 3, "stale")
        .await
        .expect_err("cluster A stays fenced");
    assert!(
        matches!(error, AttributeStoreError::Fenced { .. }),
        "{error}"
    );
    assert_eq!(database.string(status).await, "after-delete");
    database.drop_schema().await;
}

#[tokio::test]
async fn postgres_grants_only_role_can_write() {
    let Some(database) = TestDatabase::open("grants").await else {
        return;
    };
    let role = format!("das_writer_{}", unique_suffix());
    database
        .execute(&[
            "CREATE TABLE {s}.orders (flow_id TEXT PRIMARY KEY, status TEXT)",
            &format!("CREATE ROLE {role} LOGIN PASSWORD 'writer-password'"),
            &format!("GRANT USAGE ON SCHEMA {{s}} TO {role}"),
        ])
        .await;
    database.create_fencing_table().await;
    database
        .execute(&[&format!(
            "GRANT SELECT, INSERT, UPDATE ON {{s}}.orders, {{s}}.{FENCING_TABLE} TO {role}"
        )])
        .await;
    let admin_config = database
        .url
        .parse::<tokio_postgres::Config>()
        .expect("the CI URL parses");
    let host = match admin_config.get_hosts().first() {
        Some(tokio_postgres::config::Host::Tcp(host)) => host.clone(),
        _ => "localhost".to_owned(),
    };
    let port = admin_config.get_ports().first().copied().unwrap_or(5432);
    let dbname = admin_config.get_dbname().unwrap_or("postgres").to_owned();
    let dsn =
        format!("host={host} port={port} dbname={dbname} user={role} password=writer-password");
    let mut settings = database.settings("orders", "orders");
    settings.dsn = dsn;
    let store = connect(settings).await;
    write(&store, "o1", vec![("status", string_value("PLACED"))]).await;
    write(&store, "o1", vec![("status", string_value("SHIPPED"))]).await;
    let error = store
        .write_batch(
            "o1",
            &items(vec![("status", string_value("stale"))]),
            &fence("cluster-a", 0),
        )
        .await
        .expect_err("an older token is fenced");
    assert!(
        matches!(error, AttributeStoreError::Fenced { .. }),
        "{error}"
    );
    assert_eq!(
        database.string("SELECT status FROM {s}.orders").await,
        "SHIPPED"
    );
    drop(store);
    let schema = database.schema.clone();
    let client_and_task = connect_admin(&database.url).await;
    database.drop_schema().await;
    client_and_task
        .0
        .batch_execute(&format!("DROP OWNED BY {role}; DROP ROLE {role}"))
        .await
        .unwrap_or_else(|error| panic!("drop role {role} after schema {schema}: {error:?}"));
    client_and_task.1.abort();
}

#[tokio::test]
async fn postgres_transaction_timeout_abandons_blocked_writes() {
    let Some(database) = TestDatabase::open("timeout").await else {
        return;
    };
    database
        .execute(&["CREATE TABLE {s}.orders (flow_id TEXT PRIMARY KEY, status TEXT)"])
        .await;
    database.create_fencing_table().await;
    let mut settings = database.settings("orders", "orders");
    settings.transaction_timeout = Duration::from_millis(500);
    let store = connect(settings).await;
    write(&store, "o1", vec![("status", string_value("PLACED"))]).await;

    let (blocker, blocker_task) = connect_admin(&database.url).await;
    blocker
        .batch_execute(&format!(
            "BEGIN; SELECT 1 FROM {}.orders WHERE flow_id = 'o1' FOR UPDATE",
            database.schema
        ))
        .await
        .expect("lock the row");
    let started = tokio::time::Instant::now();
    let error = store
        .write_batch(
            "o1",
            &items(vec![("status", string_value("late"))]),
            &fence("cluster-a", 1),
        )
        .await
        .expect_err("the row lock outlasts the transaction timeout");
    assert!(error.is_retryable(), "{error}");
    assert!(error.is_transaction_timeout(), "{error}");
    assert_eq!(error.failure_scope(), AttributeStoreFailureScope::Batch);
    assert!(
        started.elapsed() < Duration::from_secs(3),
        "{:?}",
        started.elapsed()
    );
    blocker
        .batch_execute("ROLLBACK")
        .await
        .expect("release the row lock");
    blocker_task.abort();
    tokio::time::sleep(Duration::from_millis(500)).await;
    assert_eq!(
        database
            .string("SELECT status FROM {s}.orders WHERE flow_id = 'o1'")
            .await,
        "PLACED",
        "an abandoned transaction never commits"
    );
    write(&store, "o1", vec![("status", string_value("SHIPPED"))]).await;
    assert_eq!(
        database
            .string("SELECT status FROM {s}.orders WHERE flow_id = 'o1'")
            .await,
        "SHIPPED"
    );
    database.drop_schema().await;
}
