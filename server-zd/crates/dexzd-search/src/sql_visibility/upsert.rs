// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};

use dexzd_visibility_query::SearchValue;
use thiserror::Error;

use super::slots::{AttributeSlot, all_slot_columns, slot_column_expressions};
use super::{SearchRow, SqlStatement, SqlValue};

// PostgreSQL permits 100 function arguments; each JSONB field supplies a key and value.
const MAX_JSONB_OBJECT_FIELDS: usize = 50;

const MAX_UPSERT_ROWS: usize = 100;
const MAX_BIND_PARAMETERS: usize = 65_535;

const FIXED_COLUMNS: &[(&str, &str)] = &[
    ("tenant_id", "text"),
    ("partition_id", "integer"),
    ("run_id", "text"),
    ("workflow_id", "text"),
    ("workflow_type", "text"),
    ("flow_type", "text"),
    ("execution_status", "text"),
    ("start_time_ns", "bigint"),
    ("close_time_ns", "bigint"),
    ("dex_parent_flow_id", "text"),
    ("active_step_types", "text[]"),
    ("dex_work_queue_permissions", "text[]"),
    ("version", "bigint"),
];
const FIXED_PARAMETER_COUNT: usize = FIXED_COLUMNS.len();

pub(super) struct UpsertChunk<'rows> {
    pub(super) statement: SqlStatement,
    pub(super) rows: Vec<&'rows SearchRow>,
}

#[derive(Debug, Error)]
#[error(
    "search row {run_id} in tenant {tenant_id} requires {parameter_count} bind parameters, exceeding {MAX_BIND_PARAMETERS}"
)]
pub(super) struct UpsertBatchError {
    tenant_id: String,
    run_id: String,
    parameter_count: usize,
}

/// Builds an idempotent upsert for a versioned search row.
#[must_use]
pub fn upsert_statement(row: &SearchRow) -> SqlStatement {
    build_upsert_statement(&[row], None)
}

#[cfg(test)]
pub(super) fn upsert_statements(
    rows: &[SearchRow],
) -> Result<Vec<UpsertChunk<'_>>, UpsertBatchError> {
    upsert_statements_with_slots(rows, None)
}

pub(super) fn upsert_statements_with_slots<'rows>(
    rows: &'rows [SearchRow],
    assignments: Option<&[AttributeSlot]>,
) -> Result<Vec<UpsertChunk<'rows>>, UpsertBatchError> {
    let mut statements = Vec::new();
    let mut chunk = Vec::new();
    let mut parameter_count = 0;
    for row in rows {
        let row_parameters = row_parameter_count(row) + assignments.map_or(0, |_| 56);
        if row_parameters > MAX_BIND_PARAMETERS {
            return Err(UpsertBatchError {
                tenant_id: row.tenant_id.clone(),
                run_id: row.run_id.clone(),
                parameter_count: row_parameters,
            });
        }
        if chunk.len() == MAX_UPSERT_ROWS || parameter_count + row_parameters > MAX_BIND_PARAMETERS
        {
            statements.push(build_deduplicated_upsert(&chunk, assignments));
            chunk.clear();
            parameter_count = 0;
        }
        chunk.push(row);
        parameter_count += row_parameters;
    }
    if !chunk.is_empty() {
        statements.push(build_deduplicated_upsert(&chunk, assignments));
    }
    Ok(statements)
}

fn build_deduplicated_upsert<'rows>(
    rows: &[&'rows SearchRow],
    assignments: Option<&[AttributeSlot]>,
) -> UpsertChunk<'rows> {
    let mut row_positions: BTreeMap<(&str, &str), usize> = BTreeMap::new();
    let mut unique_rows: Vec<&SearchRow> = Vec::new();
    for &row in rows {
        let key = (row.tenant_id.as_str(), row.run_id.as_str());
        if let Some(&position) = row_positions.get(&key) {
            if row.version > unique_rows[position].version {
                unique_rows[position] = row;
            }
        } else {
            row_positions.insert(key, unique_rows.len());
            unique_rows.push(row);
        }
    }
    UpsertChunk {
        statement: build_upsert_statement(&unique_rows, assignments),
        rows: unique_rows,
    }
}

fn build_upsert_statement(
    rows: &[&SearchRow],
    assignments: Option<&[AttributeSlot]>,
) -> SqlStatement {
    let mut parameters = Vec::new();
    let mut row_expressions = Vec::with_capacity(rows.len());
    for row in rows {
        row_expressions.push(row_values(row, &mut parameters));
    }
    let values = row_expressions.join(", ");
    let fixed_columns = FIXED_COLUMNS
        .iter()
        .map(|(column, _)| *column)
        .collect::<Vec<_>>()
        .join(", ");
    let slot_columns = assignments.map(|_| all_slot_columns()).unwrap_or_default();
    let extra_columns = slot_columns
        .iter()
        .map(|(column, _)| format!(", {column}"))
        .collect::<String>();
    let extra_updates = slot_columns
        .iter()
        .map(|(column, _)| format!(", {column} = EXCLUDED.{column}"))
        .collect::<String>();
    let base_columns = format!(
        "{fixed_columns}, sa_keyword, sa_text, sa_number, sa_number_types, sa_datetime, sa_bool, sa_wire"
    );
    let source = if let Some(assignments) = assignments {
        let tenants = rows
            .iter()
            .map(|row| row.tenant_id.as_str())
            .collect::<BTreeSet<_>>();
        let slot_values =
            slot_column_expressions(assignments, &tenants, &mut parameters).join(", ");
        format!(
            "SELECT {base_columns}, {slot_values} FROM (VALUES {values}) AS incoming({base_columns}) WHERE TRUE"
        )
    } else {
        format!("VALUES {values}")
    };
    let sql = format!(
        "INSERT INTO dexzd_visibility_runs ({fixed_columns}, sa_keyword, sa_text, sa_number, sa_number_types, sa_datetime, sa_bool, sa_wire{extra_columns}) {source} ON CONFLICT (tenant_id, run_id) DO UPDATE SET partition_id = EXCLUDED.partition_id, workflow_id = EXCLUDED.workflow_id, workflow_type = EXCLUDED.workflow_type, flow_type = EXCLUDED.flow_type, execution_status = EXCLUDED.execution_status, start_time_ns = EXCLUDED.start_time_ns, close_time_ns = EXCLUDED.close_time_ns, dex_parent_flow_id = EXCLUDED.dex_parent_flow_id, active_step_types = EXCLUDED.active_step_types, dex_work_queue_permissions = EXCLUDED.dex_work_queue_permissions, version = EXCLUDED.version, sa_keyword = EXCLUDED.sa_keyword, sa_text = EXCLUDED.sa_text, sa_number = EXCLUDED.sa_number, sa_number_types = EXCLUDED.sa_number_types, sa_datetime = EXCLUDED.sa_datetime, sa_bool = EXCLUDED.sa_bool, sa_wire = EXCLUDED.sa_wire{extra_updates} WHERE EXCLUDED.version > dexzd_visibility_runs.version"
    );
    SqlStatement { sql, parameters }
}

fn row_values(row: &SearchRow, parameters: &mut Vec<SqlValue>) -> String {
    let fixed_values: [SqlValue; FIXED_PARAMETER_COUNT] = [
        SqlValue::Text(row.tenant_id.clone()),
        SqlValue::Int32(row.partition_id),
        SqlValue::Text(row.run_id.clone()),
        SqlValue::Text(row.workflow_id.clone()),
        SqlValue::Text(row.workflow_type.clone()),
        SqlValue::Text(row.flow_type.clone()),
        SqlValue::Text(row.execution_status.clone()),
        SqlValue::Int(row.start_time_ns),
        SqlValue::OptionalInt(row.close_time_ns),
        SqlValue::OptionalText(row.dex_parent_flow_id.clone()),
        SqlValue::OptionalTextArray(row.active_step_types.clone()),
        SqlValue::OptionalTextArray(row.dex_work_queue_permissions.clone()),
        SqlValue::Int(row.version),
    ];
    let mut placeholders = Vec::with_capacity(fixed_values.len());
    for (value, (_, sql_type)) in fixed_values.into_iter().zip(FIXED_COLUMNS) {
        parameters.push(value);
        placeholders.push(format!("${}::{sql_type}", parameters.len()));
    }
    let keyword_bucket = bucket_expression(&row.sa_keyword, parameters);
    let text_bucket = bucket_expression(&row.sa_text, parameters);
    let number_bucket = bucket_expression(&row.sa_number, parameters);
    let number_types_bucket = bucket_expression(&row.sa_number_types, parameters);
    let datetime_bucket = bucket_expression(&row.sa_datetime, parameters);
    let bool_bucket = bucket_expression(&row.sa_bool, parameters);
    let wire_bucket = bucket_expression(&row.sa_wire, parameters);
    format!(
        "({}, {keyword_bucket}, {text_bucket}, {number_bucket}, {number_types_bucket}, {datetime_bucket}, {bool_bucket}, {wire_bucket})",
        placeholders.join(", ")
    )
}

fn row_parameter_count(row: &SearchRow) -> usize {
    FIXED_PARAMETER_COUNT
        + 2 * (row.sa_keyword.len()
            + row.sa_text.len()
            + row.sa_number.len()
            + row.sa_number_types.len()
            + row.sa_datetime.len()
            + row.sa_bool.len()
            + row.sa_wire.len())
}

fn bucket_expression(
    bucket: &BTreeMap<String, SearchValue>,
    parameters: &mut Vec<SqlValue>,
) -> String {
    if bucket.is_empty() {
        return "'{}'::jsonb".to_owned();
    }
    let mut fields = Vec::with_capacity(bucket.len());
    for (name, value) in bucket {
        parameters.push(SqlValue::Text(name.clone()));
        let key_placeholder = format!("${}::text", parameters.len());
        let (bind_value, sql_type) = match value {
            SearchValue::String(value) => (SqlValue::Text(value.clone()), "text"),
            SearchValue::StringArray(value) => (SqlValue::TextArray(value.clone()), "text[]"),
            SearchValue::Int(value) | SearchValue::Datetime(value) => {
                (SqlValue::Int(*value), "bigint")
            }
            SearchValue::Double(value) => (SqlValue::Double(*value), "double precision"),
            SearchValue::Bool(value) => (SqlValue::Bool(*value), "boolean"),
        };
        parameters.push(bind_value);
        let value_placeholder = format!("${}::{sql_type}", parameters.len());
        fields.push(format!("{key_placeholder}, to_jsonb({value_placeholder})"));
    }
    fields
        .chunks(MAX_JSONB_OBJECT_FIELDS)
        .map(|fields| format!("jsonb_build_object({})", fields.join(", ")))
        .collect::<Vec<_>>()
        .join(" || ")
}
