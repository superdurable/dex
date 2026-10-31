// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_protocol::dex::IndexType;
use dexzd_protocol::dexzd::v1::{AttributeIndexRegistration, AttributeIndexRegistry};

use super::assign_attribute_slots;
use crate::sql_visibility::{VisibilityDialect, translate_query_for_dialect};
use dexzd_visibility_query::{FieldRegistry, parse_and_check};

fn registration(tenant_id: u64, key: &str, index_type: IndexType) -> AttributeIndexRegistration {
    AttributeIndexRegistration {
        tenant_id,
        index_key: key.into(),
        index_type: index_type as i32,
        ..Default::default()
    }
}

#[test]
fn append_keeps_assignments_and_system_fields_never_consume_slots() {
    let mut registry = AttributeIndexRegistry {
        version: 1,
        registrations: vec![
            registration(1, "DexWorkQueuePermissions", IndexType::KeywordArray),
            registration(1, "later_alphabetically", IndexType::Keyword),
            registration(1, "number", IndexType::Int),
            registration(2, "other_tenant", IndexType::Keyword),
        ],
    };
    let initial = assign_attribute_slots(&registry).unwrap();
    assert_eq!(initial[0].slot_column.as_str(), "keyword_01");
    assert_eq!(initial[1].slot_column.as_str(), "int_01");
    assert_eq!(initial[2].slot_column.as_str(), "keyword_01");
    registry
        .registrations
        .push(registration(1, "alphabetically_first", IndexType::Keyword));
    let appended = assign_attribute_slots(&registry).unwrap();
    assert_eq!(appended[..initial.len()], initial);
    assert_eq!(appended.last().unwrap().slot_column.as_str(), "keyword_02");
    registry.registrations.extend(
        (0..10)
            .map(|position| registration(1, &format!("overflow_{position}"), IndexType::Keyword)),
    );
    let error = assign_attribute_slots(&registry).unwrap_err().to_string();
    assert!(error.contains("capacity exceeded"));
    assert!(error.contains("later_alphabetically"));
    assert!(error.contains("overflow_9"));
}

#[test]
fn postgres_migration_and_queries_never_depend_on_extensions() {
    let migration = include_str!("../postgres.sql");
    for forbidden in ["EXTENSION", "pg_search", "pdb.", "btree_gin"] {
        assert!(!migration.contains(forbidden));
    }
    let registry = AttributeIndexRegistry {
        version: 1,
        registrations: vec![registration(1, "keyword", IndexType::Keyword)],
    };
    let assignments = assign_attribute_slots(&registry).unwrap();
    let mut fields = FieldRegistry::new();
    fields.insert("keyword", IndexType::Keyword);
    let query = parse_and_check("keyword STARTS_WITH 're'", &fields).unwrap();
    let statement = translate_query_for_dialect(
        &query,
        "1",
        10,
        None,
        VisibilityDialect::Postgres,
        &assignments,
    )
    .unwrap();
    assert!(statement.sql.contains("keyword_01"));
    assert!(statement.sql.contains(" >= "));
    assert!(statement.sql.contains(" < "));
    assert!(!statement.sql.contains("@@@"));
    assert!(!statement.sql.contains("coalesce"));
    let fallback =
        translate_query_for_dialect(&query, "1", 10, None, VisibilityDialect::Postgres, &[])
            .unwrap();
    assert!(fallback.sql.contains("sa_keyword"));
    assert!(!fallback.sql.contains("keyword_01"));
}
