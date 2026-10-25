// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;

use tokio_postgres::types::{ToSql, Type};

use crate::schema::{TableReference, quote_identifier};
use crate::value_conversion::SqlArgument;

/// One parameterized statement and the table it writes, for error messages.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct SqlStatement {
    pub(crate) query: String,
    pub(crate) arguments: Vec<SqlArgument>,
    pub(crate) table: String,
}

impl SqlStatement {
    /// Returns the arguments with their declared types for `execute_typed`.
    pub(crate) fn typed_arguments(&self) -> Vec<(&(dyn ToSql + Sync), Type)> {
        self.arguments
            .iter()
            .map(|argument| (argument as &(dyn ToSql + Sync), argument.declared_type()))
            .collect()
    }
}

/// Go's `buildKeyedUpsert`: a column without a value is written as DEFAULT.
pub(crate) fn build_keyed_upsert(
    reference: &TableReference,
    key_columns: &[String],
    key_values: &[String],
    column_names: &[String],
    values: &BTreeMap<String, SqlArgument>,
) -> SqlStatement {
    let quoted_keys = key_columns
        .iter()
        .map(|column| quote_identifier(column))
        .collect::<Vec<_>>();
    let mut columns = quoted_keys.clone();
    let mut placeholders = Vec::with_capacity(key_columns.len() + column_names.len());
    let mut arguments = Vec::with_capacity(key_columns.len() + column_names.len());
    let mut updates = Vec::with_capacity(column_names.len());
    for key_value in key_values {
        arguments.push(SqlArgument::Text(key_value.clone()));
        placeholders.push(format!("${}", arguments.len()));
    }
    for name in column_names {
        let quoted = quote_identifier(name);
        columns.push(quoted.clone());
        match values.get(name) {
            Some(argument) => {
                arguments.push(argument.clone());
                placeholders.push(format!("${}", arguments.len()));
            }
            None => placeholders.push("DEFAULT".to_owned()),
        }
        updates.push(format!("{quoted} = EXCLUDED.{quoted}"));
    }
    let table = reference.qualified();
    let mut query = format!(
        "INSERT INTO {table} ({}) VALUES ({}) ON CONFLICT ({}) ",
        columns.join(", "),
        placeholders.join(", "),
        quoted_keys.join(", ")
    );
    if updates.is_empty() {
        query.push_str("DO NOTHING");
    } else {
        query.push_str("DO UPDATE SET ");
        query.push_str(&updates.join(", "));
    }
    SqlStatement {
        query,
        arguments,
        table,
    }
}

/// Go's `buildKeyedDelete`: deletes the one row with `key_values`.
pub(crate) fn build_keyed_delete(
    reference: &TableReference,
    key_columns: &[String],
    key_values: &[String],
) -> SqlStatement {
    let conditions = key_columns
        .iter()
        .enumerate()
        .map(|(index, column)| format!("{} = ${}", quote_identifier(column), index + 1))
        .collect::<Vec<_>>();
    let table = reference.qualified();
    SqlStatement {
        query: format!("DELETE FROM {table} WHERE {}", conditions.join(" AND ")),
        arguments: key_values.iter().cloned().map(SqlArgument::Text).collect(),
        table,
    }
}

/// Records the token unless a newer one exists for the same store, cluster, and scope; zero rows means fenced.
pub(crate) fn build_fencing_statement(
    reference: &TableReference,
    store_name: &str,
    cluster_id: &str,
    scope: &str,
    token: i64,
) -> SqlStatement {
    let table = reference.qualified();
    SqlStatement {
        query: format!(
            "INSERT INTO {table} AS existing (store_name, cluster_id, scope, fencing_token, updated_at) \
             VALUES ($1, $2, $3, $4, now()) ON CONFLICT (store_name, cluster_id, scope) \
             DO UPDATE SET fencing_token = EXCLUDED.fencing_token, updated_at = now() \
             WHERE existing.fencing_token <= EXCLUDED.fencing_token"
        ),
        arguments: vec![
            SqlArgument::Text(store_name.to_owned()),
            SqlArgument::Text(cluster_id.to_owned()),
            SqlArgument::Text(scope.to_owned()),
            SqlArgument::Text(token.to_string()),
        ],
        table,
    }
}

/// Returns the statement that creates the fencing table; the table owner runs it before starting Dex.
pub(crate) fn fencing_table_ddl(reference: &TableReference) -> String {
    format!(
        "CREATE TABLE {} (store_name text NOT NULL, cluster_id text NOT NULL, scope text NOT NULL, \
         fencing_token bigint NOT NULL, updated_at timestamptz NOT NULL, \
         PRIMARY KEY (store_name, cluster_id, scope));",
        reference.qualified()
    )
}
