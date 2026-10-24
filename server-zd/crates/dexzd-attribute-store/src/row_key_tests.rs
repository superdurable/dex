// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;
use std::time::Duration;

use crate::row_key::{RowKeyTemplate, snake_case};
use crate::settings::{AttributeMapTableSettings, AttributeStoreSettings, ColumnNaming};
use crate::store::jitter_interval;

#[test]
fn row_key_parse_failures_match_go_messages() {
    for (template, message) in [
        ("order-{customer_id}{order_id}", "separated by literal text"),
        ("order-{customer_id", "unterminated placeholder"),
        ("order-{customer_id}-{customer_id}", "more than once"),
        ("order-{*}", "at least one column"),
        ("order-}{customer_id}", "unmatched"),
        ("order-{}-{order_id}", "empty placeholder"),
        ("order-{customer{id}", "unterminated placeholder"),
        ("plain-literal", "at least one column"),
    ] {
        let error = RowKeyTemplate::parse(template).expect_err(template);
        assert!(error.contains(message), "{template}: {error}");
    }
}

#[test]
fn row_key_matches_flow_ids_like_go() {
    let orders = RowKeyTemplate::parse("order-{customer_id}-{order_id}").expect("valid template");
    assert_eq!(orders.columns(), ["customer_id", "order_id"]);
    assert_eq!(
        orders.match_flow_id("order-c1-o-2-x"),
        Some(vec!["c1".to_owned(), "o-2-x".to_owned()])
    );
    assert_eq!(
        orders.match_flow_id("order-c1-o-7"),
        Some(vec!["c1".to_owned(), "o-7".to_owned()])
    );
    assert_eq!(orders.match_flow_id("invoice-c1-o-3"), None);
    assert_eq!(orders.match_flow_id("order-c1-"), None);
    assert_eq!(orders.match_flow_id("order--o1"), None);
    assert_eq!(orders.match_flow_id("order-c1"), None);

    let onboarding = RowKeyTemplate::parse("onboarding-{customer_id}-{*}").expect("valid template");
    assert_eq!(onboarding.columns(), ["customer_id"]);
    assert_eq!(
        onboarding.match_flow_id("onboarding-c1-request-7"),
        Some(vec!["c1".to_owned()])
    );

    let suffixed = RowKeyTemplate::parse("{tenant}/run-{run_id}.v1").expect("valid template");
    assert_eq!(
        suffixed.match_flow_id("acme/run-42.v1"),
        Some(vec!["acme".to_owned(), "42".to_owned()])
    );
    assert_eq!(suffixed.match_flow_id("acme/run-42.v1x"), None);
    assert_eq!(suffixed.match_flow_id("acme/run-42"), None);

    let unicode = RowKeyTemplate::parse("顧客-{id}").expect("valid template");
    assert_eq!(
        unicode.match_flow_id("顧客-七"),
        Some(vec!["七".to_owned()])
    );
}

#[test]
fn snake_case_matches_go() {
    for (name, expected) in [
        ("DeploymentURL", "deployment_url"),
        ("URLPath", "url_path"),
        ("S3Bucket", "s3_bucket"),
        ("OrderStatus", "order_status"),
        ("ShippingURL", "shipping_url"),
        ("ItemCount", "item_count"),
        ("AddedByAccountID", "added_by_account_id"),
        ("GroupID", "group_id"),
        ("CustomerID", "customer_id"),
        ("DisplayName", "display_name"),
        ("shipping_url", "shipping_url"),
        ("status", "status"),
        ("ID", "id"),
        ("HTTPServerURL", "http_server_url"),
        ("Version2Name", "version2_name"),
        ("A", "a"),
        ("", ""),
        ("already_Snake", "already_snake"),
        ("ÀBc", "à_bc"),
    ] {
        assert_eq!(snake_case(name), expected, "{name}");
    }
    assert_eq!(ColumnNaming::AsIs.column_name("DisplayName"), "DisplayName");
    assert_eq!(
        ColumnNaming::SnakeCase.column_name("DisplayName"),
        "display_name"
    );
}

/// Expected values are the output of Go's `snakeCase` with Go 1.26 (Unicode 15.0.0).
#[test]
fn snake_case_uses_go_general_categories_for_non_ascii_names() {
    for (name, expected) in [
        ("a٣B", "a٣_b"),
        ("ªA", "ªa"),
        ("ºA", "ºa"),
        ("①A", "①a"),
        ("ⅰA", "ⅰa"),
        ("ⅠA", "ⅰa"),
        ("aⅠ", "aⅰ"),
        ("ⒶBc", "ⓐbc"),
        ("AⒷc", "aⓑc"),
        ("aⒶ", "aⓐ"),
        ("ⓐA", "ⓐa"),
        ("ʰA", "ʰa"),
        ("aǅ", "aǆ"),
        ("ǅA", "ǆa"),
        ("a²B", "a²b"),
        ("a໒B", "a໒_b"),
        ("ÀÉtat", "à_état"),
        ("ÉtéÀ", "été_à"),
        ("ßA", "ß_a"),
        ("ĸA", "ĸ_a"),
        ("ΣΑΣ", "σασ"),
        ("ÅngströmΩ", "ångström_ω"),
        ("𝐀𝐁c", "𝐀_𝐁c"),
        ("a𝟘B", "a𝟘_b"),
        ("Ⅻb", "ⅻb"),
    ] {
        assert_eq!(snake_case(name), expected, "{name}");
    }
}

#[test]
fn column_naming_parses_go_config_values() {
    assert_eq!(
        "".parse::<ColumnNaming>().expect("empty is valid"),
        ColumnNaming::AsIs
    );
    assert_eq!(
        "snake_case"
            .parse::<ColumnNaming>()
            .expect("snake_case is valid"),
        ColumnNaming::SnakeCase
    );
    let error = "camelCase"
        .parse::<ColumnNaming>()
        .expect_err("camelCase is invalid");
    assert!(
        error.to_string().contains("unsupported columnNaming"),
        "{error}"
    );
}

#[test]
fn settings_validation_matches_go_messages() {
    let valid = AttributeStoreSettings::new("orders", "host=localhost", "public.orders");
    valid.validate().expect("valid settings");
    let failures: Vec<(AttributeStoreSettings, &str)> = vec![
        (
            AttributeStoreSettings::new("", "host=localhost", "orders"),
            "name must not be empty",
        ),
        (
            AttributeStoreSettings::new("orders", "", "orders"),
            "requires dsn",
        ),
        (
            AttributeStoreSettings::new("orders", "host=localhost", ""),
            "requires tableName",
        ),
        (
            AttributeStoreSettings::new("orders", "host=localhost", "a.b.c"),
            "must contain at most 2 identifiers",
        ),
        (
            AttributeStoreSettings::new("orders", "host=localhost", ".orders"),
            "contains an invalid identifier",
        ),
        (
            with_map(valid.clone(), "Members/All", "members", "account_id"),
            "contain no '/'",
        ),
        (
            with_map(valid.clone(), "Members", "", "account_id"),
            "requires tableName",
        ),
        (
            with_map(valid.clone(), "Members", "members", ""),
            "requires tableName and instanceKeyColumn",
        ),
        (
            with_map(valid.clone(), "", "members", "account_id"),
            "must be non-empty",
        ),
        (
            AttributeStoreSettings {
                fencing_table_name: "a.b.c".to_owned(),
                ..valid.clone()
            },
            "invalid fencingTableName",
        ),
        (
            AttributeStoreSettings {
                schema_sync_interval: Duration::ZERO,
                ..valid.clone()
            },
            "schemaSyncInterval must be positive",
        ),
        (
            AttributeStoreSettings {
                transaction_timeout: Duration::ZERO,
                ..valid.clone()
            },
            "transactionTimeout must be positive",
        ),
        (
            AttributeStoreSettings {
                transaction_timeout: Duration::from_millis(2_147_483_648),
                ..valid.clone()
            },
            "transactionTimeout must not exceed",
        ),
    ];
    for (settings, message) in failures {
        let error = settings.validate().expect_err(message).to_string();
        assert!(error.contains(message), "{message}: {error}");
        assert!(!error.contains("host=localhost"), "{error}");
    }
}

#[test]
fn settings_debug_redacts_the_dsn() {
    let settings = AttributeStoreSettings::new("orders", "postgres://dex:secret@db/dex", "orders");
    let debug = format!("{settings:?}");
    assert!(!debug.contains("secret"), "{debug}");
    assert!(debug.contains("[REDACTED]"), "{debug}");
}

#[test]
fn jitter_interval_uses_go_range() {
    let interval = Duration::from_secs(60);
    assert_eq!(jitter_interval(interval, 0.0), Duration::from_secs(54));
    assert_eq!(jitter_interval(interval, 0.5), Duration::from_secs(60));
    assert_eq!(jitter_interval(interval, 1.0), Duration::from_secs(66));
}

fn with_map(
    mut settings: AttributeStoreSettings,
    map_name: &str,
    table_name: &str,
    instance_key_column: &str,
) -> AttributeStoreSettings {
    settings.attribute_maps = BTreeMap::from([(
        map_name.to_owned(),
        AttributeMapTableSettings {
            table_name: table_name.to_owned(),
            instance_key_column: instance_key_column.to_owned(),
        },
    )]);
    settings
}
