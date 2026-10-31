// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

#![cfg(test)]

use super::prefix_successor;

#[test]
fn indexed_text_system_predicates_keep_dialect_collations() {
    use dexzd_visibility_query::{FieldRegistry, parse_and_check};

    use super::super::{SqlValue, VisibilityDialect, translate_query_for_dialect};

    for (field, column) in [
        ("FlowType", "flow_type"),
        ("WorkflowId", "workflow_id"),
        ("RunId", "run_id"),
    ] {
        for dialect in [VisibilityDialect::ParadeDb, VisibilityDialect::Postgres] {
            for (suffix, expected_predicate) in [
                ("= 'é'", "{left} = $2::text"),
                ("IN ('é', 'é')", "({left} = $2::text OR {left} = $3::text)"),
                ("!= 'é'", "NOT (({column}) COLLATE \"C\" = $2::text)"),
                (
                    "NOT IN ('é', 'é')",
                    "NOT (({column}) COLLATE \"C\" = $2::text OR ({column}) COLLATE \"C\" = $3::text)",
                ),
                ("> 'é'", "({column}) COLLATE \"C\" > $2::text"),
                (">= 'é'", "({column}) COLLATE \"C\" >= $2::text"),
                ("< 'é'", "({column}) COLLATE \"C\" < $2::text"),
                ("<= 'é'", "({column}) COLLATE \"C\" <= $2::text"),
                (
                    "BETWEEN 'e' AND 'é'",
                    "(({column}) COLLATE \"C\" >= $2::text) AND (({column}) COLLATE \"C\" <= $3::text)",
                ),
                ("IS NULL", "{column} IS NULL"),
                ("IS NOT NULL", "{column} IS NOT NULL"),
                (
                    "STARTS_WITH 'é'",
                    "left({column}, char_length($2::text)) = $2::text",
                ),
            ] {
                let input = format!("{field} {suffix}");
                let query = parse_and_check(&input, &FieldRegistry::new()).expect(&input);
                let statement =
                    translate_query_for_dialect(&query, "tenant", 10, None, dialect, &[])
                        .expect(&input);
                let left = if dialect == VisibilityDialect::ParadeDb {
                    column.to_owned()
                } else {
                    format!("({column}) COLLATE \"C\"")
                };
                let expected_predicate = if dialect == VisibilityDialect::Postgres
                    && suffix == "STARTS_WITH 'é'"
                {
                    "(({column}) COLLATE \"C\" >= $2::text AND ({column}) COLLATE \"C\" < $3::text AND left({column}, char_length($2::text)) = $2::text)"
                } else {
                    expected_predicate
                };
                let expected_predicate = expected_predicate
                    .replace("{left}", &left)
                    .replace("{column}", column);
                let expected_predicate = if dialect == VisibilityDialect::Postgres
                    && suffix == "IS NULL"
                {
                    format!("NOT ({column} IS NOT NULL)")
                } else if dialect == VisibilityDialect::Postgres && suffix == "NOT IN ('é', 'é')"
                {
                    format!(
                        "NOT ((({column}) COLLATE \"C\" = $2::text OR ({column}) COLLATE \"C\" = $3::text))"
                    )
                } else {
                    expected_predicate
                };
                assert_eq!(
                    statement.sql,
                    format!(
                        "SELECT {} FROM dexzd_visibility_runs WHERE tenant_id = $1::text AND ({expected_predicate}) {} LIMIT ${}::bigint",
                        super::SELECT_COLUMNS,
                        super::DEFAULT_ORDER,
                        statement.parameters.len(),
                    ),
                    "{dialect:?}: {input}",
                );
                assert_eq!(
                    statement.parameters.first(),
                    Some(&SqlValue::Text("tenant".into()))
                );
                assert_eq!(statement.parameters.last(), Some(&SqlValue::Int(10)));
            }
        }
    }
}

#[test]
fn prefix_successor_handles_unicode_boundaries_and_carry() {
    assert_eq!(prefix_successor(""), None);
    assert_eq!(prefix_successor("\u{d7ff}"), Some("\u{e000}".into()));
    assert_eq!(prefix_successor("\u{10ffff}"), None);
    assert_eq!(prefix_successor("a\u{10ffff}"), Some("b".into()));
    assert_eq!(
        prefix_successor("ab\u{10ffff}\u{10ffff}"),
        Some("ac".into())
    );
}
