// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use aws_lc_rs::digest::{SHA256, digest};
use dexzd_protocol::dex::IndexType;

use super::AttributeSlot;
use crate::sql_visibility::{SearchRow, SqlValue};

pub(crate) fn all_slot_columns() -> Vec<(String, &'static str)> {
    [
        ("keyword", "text", 10),
        ("int", "bigint", 3),
        ("datetime", "bigint", 3),
        ("double", "double precision", 3),
        ("bool", "boolean", 3),
        ("keyword_array", "text[]", 3),
        ("text_terms", "text[]", 3),
    ]
    .into_iter()
    .flat_map(|(prefix, sql_type, count)| {
        (1..=count).map(move |position| (format!("{prefix}_{position:02}"), sql_type))
    })
    .collect()
}

pub(crate) fn row_slot_values(
    row: &SearchRow,
    assignments: &[AttributeSlot],
    buckets: [&str; 5],
    parameters: &mut Vec<SqlValue>,
) -> Vec<String> {
    let mut values = Vec::new();
    for (column, sql_type) in all_slot_columns() {
        let assignment = assignments.iter().find(|assignment| {
            assignment.tenant_id.to_string() == row.tenant_id && assignment.slot_column == column
        });
        let Some(assignment) = assignment else {
            values.push(format!("NULL::{sql_type}"));
            continue;
        };
        parameters.push(SqlValue::Text(assignment.index_key.clone()));
        let key = format!("${}::text", parameters.len());
        let bucket = match assignment.index_type {
            IndexType::Keyword | IndexType::KeywordArray => buckets[0],
            IndexType::Text => buckets[1],
            IndexType::Int | IndexType::Double => buckets[2],
            IndexType::Datetime => buckets[3],
            IndexType::Bool => buckets[4],
            IndexType::Unspecified => unreachable!("slot assignments reject unspecified types"),
        };
        values.push(slot_value_expression(
            assignment.index_type,
            &format!("({bucket})"),
            &key,
        ));
    }
    values
}

pub(crate) fn slot_value_expression(index_type: IndexType, bucket: &str, key: &str) -> String {
    let text = format!("({bucket} ->> {key})");
    match index_type {
        IndexType::Keyword => text,
        IndexType::KeywordArray => format!(
            "CASE WHEN jsonb_typeof({bucket} -> {key}) = 'array' THEN ARRAY(SELECT jsonb_array_elements_text({bucket} -> {key})) END"
        ),
        IndexType::Int => {
            // Historical cast-invalid values project NULL instead of failing the whole search query.
            format!(
                "CASE WHEN {text} ~ '^[+-]?[0-9]+$' THEN CASE WHEN {text}::numeric BETWEEN -9223372036854775808 AND 9223372036854775807 THEN {text}::bigint END END"
            )
        }
        IndexType::Double => format!("{text}::double precision"),
        IndexType::Datetime => format!("{text}::bigint"),
        IndexType::Bool => format!("{text}::boolean"),
        IndexType::Text => format!(
            "CASE WHEN {text} IS NOT NULL THEN {} END",
            text_slot_expression(&text)
        ),
        IndexType::Unspecified => unreachable!("slot assignments reject unspecified types"),
    }
}

pub(crate) fn text_term_hashes(value: &str) -> Vec<String> {
    let mut hashes = super::super::query::tokenize_text(value)
        .iter()
        .map(|term| {
            digest(&SHA256, term.as_bytes()).as_ref()[..16]
                .iter()
                .map(|byte| format!("{byte:02x}"))
                .collect()
        })
        .collect::<Vec<String>>();
    hashes.sort();
    hashes.dedup();
    hashes
}

pub(crate) fn text_slot_expression(expression: &str) -> String {
    // Match the database tokenizer used by the exact JSONB text predicate, including its locale.
    format!(
        "ARRAY(SELECT DISTINCT left(encode(sha256(convert_to(term, 'UTF8')), 'hex'), 32) FROM unnest(regexp_split_to_array(lower({expression}), '[^[:alnum:]]+')) AS term WHERE term <> '')"
    )
}
