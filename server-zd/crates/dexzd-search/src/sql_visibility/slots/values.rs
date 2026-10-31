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
use super::AttributeSlotColumn;
use super::slot_column::{INDEX_TYPES, slot_type};
use crate::sql_visibility::SqlValue;
use std::collections::BTreeSet;

pub(crate) fn all_slot_columns() -> Vec<(AttributeSlotColumn, &'static str)> {
    let mut columns = Vec::new();
    for index_type in INDEX_TYPES {
        let (_, capacity, sql_type) =
            slot_type(index_type).expect("enumerated slot types are specified");
        for position in 1..=capacity {
            columns.push((
                AttributeSlotColumn::for_registration(index_type, position)
                    .expect("positions are within slot capacity"),
                sql_type,
            ));
        }
    }
    columns
}

pub(crate) fn slot_column_expressions(
    assignments: &[AttributeSlot],
    tenants: &BTreeSet<&str>,
    parameters: &mut Vec<SqlValue>,
) -> Vec<String> {
    let tenant_assignments = assignments
        .iter()
        .map(|assignment| (assignment, assignment.tenant_id.to_string()))
        .filter(|(_, tenant)| tenants.contains(tenant.as_str()))
        .collect::<Vec<_>>();
    let mut expressions = Vec::new();
    for (column, sql_type) in all_slot_columns() {
        let mut branches = Vec::new();
        for (assignment, tenant) in &tenant_assignments {
            if assignment.slot_column != column {
                continue;
            }
            parameters.push(SqlValue::Text(tenant.clone()));
            let tenant_parameter = format!("${}::text", parameters.len());
            parameters.push(SqlValue::Text(assignment.index_key.clone()));
            let key = format!("${}::text", parameters.len());
            let bucket = match assignment.index_type {
                IndexType::Keyword | IndexType::KeywordArray => "incoming.sa_keyword",
                IndexType::Text => "incoming.sa_text",
                IndexType::Int | IndexType::Double => "incoming.sa_number",
                IndexType::Datetime => "incoming.sa_datetime",
                IndexType::Bool => "incoming.sa_bool",
                IndexType::Unspecified => unreachable!("slot assignments reject unspecified types"),
            };
            let value = slot_value_expression(assignment.index_type, bucket, &key);
            branches.push(format!(
                "WHEN incoming.tenant_id = {tenant_parameter} THEN {value}"
            ));
        }
        expressions.push(if branches.is_empty() {
            format!("NULL::{sql_type}")
        } else {
            format!("(CASE {} END)::{sql_type}", branches.join(" "))
        });
    }
    expressions
}

pub(crate) fn slot_value_expression(index_type: IndexType, bucket: &str, key: &str) -> String {
    let text = format!("({bucket} ->> {key})");
    let string = string_source(
        key,
        if bucket.starts_with("incoming.") {
            "incoming."
        } else {
            ""
        },
    );
    match index_type {
        IndexType::Keyword => {
            // Stored arrays cannot reproduce Keyword membership in one text slot; positive predicates intentionally do not match.
            format!("CASE WHEN octet_length({string}) <= 1024 THEN {string} END")
        }
        IndexType::KeywordArray => {
            let array = string_array_source(
                key,
                if bucket.starts_with("incoming.") {
                    "incoming."
                } else {
                    ""
                },
            );
            format!(
                "(SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM unnest(slot_values) AS element WHERE octet_length(element) > 1024) THEN slot_values END FROM (SELECT {array} AS slot_values) AS source)"
            )
        }
        IndexType::Int => {
            // Stored numeric types remain distinct, matching the builtin evaluator.
            format!(
                "CASE WHEN ({bucket}_types ->> {key})::integer = {} AND {text} ~ '^[+-]?[0-9]+$' THEN CASE WHEN {text}::numeric BETWEEN -9223372036854775808 AND 9223372036854775807 THEN {text}::bigint END END",
                IndexType::Int as i32
            )
        }
        IndexType::Double => format!(
            "CASE WHEN ({bucket}_types ->> {key})::integer = {} THEN {text}::double precision END",
            IndexType::Double as i32
        ),
        IndexType::Datetime => format!("{text}::bigint"),
        IndexType::Bool => format!("{text}::boolean"),
        IndexType::Text => format!(
            "CASE WHEN {string} IS NOT NULL THEN {} END",
            text_slot_expression(&string)
        ),
        IndexType::Unspecified => unreachable!("slot assignments reject unspecified types"),
    }
}

pub(crate) fn string_source(key: &str, prefix: &str) -> String {
    format!(
        "coalesce(CASE WHEN jsonb_typeof({prefix}sa_keyword -> {key}) = 'string' THEN {prefix}sa_keyword ->> {key} END, CASE WHEN jsonb_typeof({prefix}sa_text -> {key}) = 'string' THEN {prefix}sa_text ->> {key} END)"
    )
}

pub(crate) fn string_array_source(key: &str, prefix: &str) -> String {
    let string = string_source(key, prefix);
    format!(
        "CASE WHEN jsonb_typeof({prefix}sa_keyword -> {key}) = 'array' THEN ARRAY(SELECT jsonb_array_elements_text({prefix}sa_keyword -> {key})) WHEN {string} IS NOT NULL THEN ARRAY[{string}] END"
    )
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
