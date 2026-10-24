// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;

use tokio_postgres::Client;
use tokio_postgres::types::ToSql;

use crate::error::{AttributeStoreError, database_error};
use crate::map_table::{MapTableSchema, go_list};
use crate::row_key::RowKeyTemplate;
use crate::settings::AttributeStoreSettings;
use crate::statements::fencing_table_ddl;
use crate::value_conversion::ColumnSchema;

/// Casts every information_schema domain column so tokio-postgres decodes it as text or int8.
const DESCRIBE_COLUMNS_QUERY: &str = "SELECT column_name::text, data_type::text, udt_name::text, \
is_nullable::text, character_maximum_length::int8, numeric_precision::int8, numeric_scale::int8, \
column_default::text, is_identity::text, is_generated::text
FROM information_schema.columns
WHERE table_schema = $1 AND table_name = $2
ORDER BY ordinal_position";

const DESCRIBE_PRIMARY_KEYS_QUERY: &str = "SELECT kcu.column_name::text
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
  ON tc.constraint_name = kcu.constraint_name
 AND tc.table_schema = kcu.table_schema
 AND tc.table_name = kcu.table_name
WHERE tc.constraint_type = 'PRIMARY KEY'
  AND tc.table_schema = $1 AND tc.table_name = $2
ORDER BY kcu.ordinal_position";

const FENCING_STRING_COLUMNS: [&str; 3] = ["store_name", "cluster_id", "scope"];

/// A schema-qualified Postgres table.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct TableReference {
    pub(crate) namespace: String,
    pub(crate) table: String,
}

/// The store table snapshot. Without a rowKey the whole Flow ID is stored in `flow_id_column`.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct TableSchema {
    pub(crate) reference: TableReference,
    pub(crate) flow_id_column: Option<String>,
    pub(crate) row_key: Option<RowKeyTemplate>,
    pub(crate) columns: BTreeMap<String, ColumnSchema>,
    pub(crate) map_tables: BTreeMap<String, MapTableSchema>,
}

/// The configured tables of one store, resolved once at startup.
#[derive(Clone, Debug)]
pub(crate) struct ResolvedTables {
    pub(crate) table: TableReference,
    pub(crate) map_tables: BTreeMap<String, TableReference>,
    pub(crate) fencing_table: TableReference,
}

/// Runs metadata queries for one store on one connection.
pub(crate) struct SchemaDescriber<'a> {
    pub(crate) client: &'a Client,
    pub(crate) settings: &'a AttributeStoreSettings,
}

impl TableReference {
    /// Returns the quoted `"schema"."table"` form used in SQL and error messages.
    pub(crate) fn qualified(&self) -> String {
        format!(
            "{}.{}",
            quote_identifier(&self.namespace),
            quote_identifier(&self.table)
        )
    }
}

impl TableSchema {
    /// Returns the primary-key columns in rowKey order, or the single Flow ID column.
    pub(crate) fn key_columns(&self) -> Vec<String> {
        match (&self.row_key, &self.flow_id_column) {
            (Some(row_key), _) => row_key.columns(),
            (None, Some(flow_id_column)) => vec![flow_id_column.clone()],
            (None, None) => Vec::new(),
        }
    }

    /// Returns the value of each key column for a Flow ID, or None when it does not match the rowKey.
    pub(crate) fn key_values(&self, flow_id: &str) -> Option<Vec<String>> {
        match &self.row_key {
            Some(row_key) => row_key.match_flow_id(flow_id),
            None => Some(vec![flow_id.to_owned()]),
        }
    }

    pub(crate) fn is_key_column(&self, name: &str) -> bool {
        self.key_columns().iter().any(|column| column == name)
    }
}

impl SchemaDescriber<'_> {
    /// Resolves the store, AttributeMap, and fencing tables against the connection's current schema.
    pub(crate) async fn resolve_tables(&self) -> Result<ResolvedTables, AttributeStoreError> {
        let table = self
            .resolve_table_reference(&self.settings.table_name)
            .await?;
        let mut map_tables = BTreeMap::new();
        for (map_name, map_table) in &self.settings.attribute_maps {
            let reference = self.resolve_table_reference(&map_table.table_name).await?;
            map_tables.insert(map_name.clone(), reference);
        }
        let fencing_table = self
            .resolve_table_reference(&self.settings.fencing_table_name)
            .await?;
        Ok(ResolvedTables {
            table,
            map_tables,
            fencing_table,
        })
    }

    async fn resolve_table_reference(
        &self,
        table_name: &str,
    ) -> Result<TableReference, AttributeStoreError> {
        if let Some((namespace, table)) = table_name.split_once('.') {
            return Ok(TableReference {
                namespace: namespace.to_owned(),
                table: table.to_owned(),
            });
        }
        let row = self
            .client
            .query_typed_one("SELECT current_schema()::text", &[])
            .await
            .map_err(|error| self.database_error("resolve table namespace", &error))?;
        let namespace = row
            .try_get::<_, Option<String>>(0)
            .map_err(|error| self.database_error("resolve table namespace", &error))?
            .unwrap_or_default();
        if namespace.is_empty() {
            return Err(AttributeStoreError::InvalidTable {
                store: self.settings.name.clone(),
                table: table_name.to_owned(),
                reason: "database has no active namespace".to_owned(),
            });
        }
        Ok(TableReference {
            namespace,
            table: table_name.to_owned(),
        })
    }

    /// Go's `sqlStore.describe`: validates the store table and every AttributeMap table.
    pub(crate) async fn describe(
        &self,
        tables: &ResolvedTables,
        row_key: Option<&RowKeyTemplate>,
    ) -> Result<TableSchema, AttributeStoreError> {
        let columns = self.describe_columns(&tables.table, "").await?;
        let mut snapshot = TableSchema {
            reference: tables.table.clone(),
            flow_id_column: None,
            row_key: row_key.cloned(),
            columns,
            map_tables: BTreeMap::new(),
        };
        let invalid = |reason: String| self.invalid_table(&tables.table, reason);
        let primary_keys = self.describe_primary_keys(&tables.table, "").await?;
        match row_key {
            None => {
                if primary_keys.len() != 1 {
                    return Err(invalid(
                        "table must have exactly one primary-key column".to_owned(),
                    ));
                }
                let Some(primary_key) = snapshot.columns.get(&primary_keys[0]) else {
                    return Err(invalid("primary-key column was not described".to_owned()));
                };
                if !primary_key.accepts_string() {
                    return Err(invalid(format!(
                        "primary-key column {:?} cannot store FlowID strings",
                        primary_key.name
                    )));
                }
                snapshot.flow_id_column = Some(primary_key.name.clone());
            }
            Some(row_key) => {
                let expected = row_key.columns();
                if !has_same_columns(&primary_keys, &expected) {
                    return Err(invalid(format!(
                        "table primary key {} must be exactly the rowKey columns {}",
                        go_list(&primary_keys),
                        go_list(&expected)
                    )));
                }
                for name in &expected {
                    if !snapshot
                        .columns
                        .get(name)
                        .is_some_and(ColumnSchema::accepts_string)
                    {
                        return Err(invalid(format!(
                            "rowKey column {name:?} cannot store strings"
                        )));
                    }
                }
            }
        }
        for (name, column) in &snapshot.columns {
            if snapshot.is_key_column(name) {
                continue;
            }
            if !column.is_nullable && !column.has_default && !column.is_generated {
                return Err(invalid(format!(
                    "column {name:?} prevents partial row inserts"
                )));
            }
        }
        let parent_key_columns = snapshot.key_columns();
        for (map_name, reference) in &tables.map_tables {
            let context = format!("AttributeMap {map_name:?}: ");
            let columns = self.describe_columns(reference, &context).await?;
            let primary_keys = self.describe_primary_keys(reference, &context).await?;
            let instance_key_column = &self.settings.attribute_maps[map_name].instance_key_column;
            let map_table = MapTableSchema::new(
                reference.clone(),
                instance_key_column,
                &parent_key_columns,
                &primary_keys,
                columns,
            )
            .map_err(|reason| self.invalid_table(reference, format!("{context}{reason}")))?;
            snapshot.map_tables.insert(map_name.clone(), map_table);
        }
        Ok(snapshot)
    }

    /// Checks that the fencing table exists with the columns and primary key the fencing statement needs.
    pub(crate) async fn validate_fencing_table(
        &self,
        reference: &TableReference,
    ) -> Result<(), AttributeStoreError> {
        let invalid = |reason: String| AttributeStoreError::InvalidFencingTable {
            store: self.settings.name.clone(),
            table: reference.qualified(),
            reason,
            ddl: fencing_table_ddl(reference),
        };
        let columns = self.query_columns(reference, "fencing table ").await?;
        if columns.is_empty() {
            return Err(invalid("table does not exist or has no columns".to_owned()));
        }
        for name in FENCING_STRING_COLUMNS {
            if !columns.get(name).is_some_and(ColumnSchema::accepts_string) {
                return Err(invalid(format!(
                    "column {name:?} must exist and store strings"
                )));
            }
        }
        if columns
            .get("fencing_token")
            .is_none_or(|column| column.data_type != "bigint")
        {
            return Err(invalid(
                "column \"fencing_token\" must exist with type bigint".to_owned(),
            ));
        }
        if columns
            .get("updated_at")
            .is_none_or(|column| column.data_type != "timestamp with time zone")
        {
            return Err(invalid(
                "column \"updated_at\" must exist with type timestamp with time zone".to_owned(),
            ));
        }
        let fencing_columns = FENCING_STRING_COLUMNS.map(str::to_owned);
        let primary_keys = self
            .describe_primary_keys(reference, "fencing table ")
            .await?;
        if !has_same_columns(&primary_keys, &fencing_columns) {
            return Err(invalid(format!(
                "table primary key {} must be exactly the columns {}",
                go_list(&primary_keys),
                go_list(&fencing_columns)
            )));
        }
        for (name, column) in &columns {
            let is_written =
                fencing_columns.contains(name) || name == "fencing_token" || name == "updated_at";
            if !is_written && !column.is_nullable && !column.has_default && !column.is_generated {
                return Err(invalid(format!(
                    "column {name:?} prevents fencing row inserts"
                )));
            }
        }
        Ok(())
    }

    async fn describe_columns(
        &self,
        reference: &TableReference,
        context: &str,
    ) -> Result<BTreeMap<String, ColumnSchema>, AttributeStoreError> {
        let columns = self.query_columns(reference, context).await?;
        if columns.is_empty() {
            return Err(self.invalid_table(
                reference,
                format!("{context}table does not exist or has no columns"),
            ));
        }
        Ok(columns)
    }

    async fn query_columns(
        &self,
        reference: &TableReference,
        context: &str,
    ) -> Result<BTreeMap<String, ColumnSchema>, AttributeStoreError> {
        let operation = || {
            format!(
                "describe columns of {context}table {}",
                reference.qualified()
            )
        };
        let rows = self
            .client
            .query_typed(DESCRIBE_COLUMNS_QUERY, &table_parameters(reference))
            .await
            .map_err(|error| self.database_error(operation(), &error))?;
        let mut columns = BTreeMap::new();
        for row in rows {
            let read = || -> Result<ColumnSchema, tokio_postgres::Error> {
                let is_identity = row.try_get::<_, Option<String>>(8)?.unwrap_or_default();
                let is_generated = row.try_get::<_, Option<String>>(9)?.unwrap_or_default();
                Ok(ColumnSchema {
                    name: row.try_get(0)?,
                    data_type: row.try_get::<_, String>(1)?.to_lowercase(),
                    column_type: row
                        .try_get::<_, Option<String>>(2)?
                        .unwrap_or_default()
                        .to_lowercase(),
                    is_nullable: row.try_get::<_, String>(3)?.eq_ignore_ascii_case("YES"),
                    character_maximum: row.try_get(4)?,
                    numeric_precision: row.try_get(5)?,
                    numeric_scale: row.try_get(6)?,
                    has_default: row.try_get::<_, Option<String>>(7)?.is_some(),
                    is_generated: is_generated_column(&is_identity, &is_generated),
                })
            };
            let column = read().map_err(|error| self.database_error(operation(), &error))?;
            columns.insert(column.name.clone(), column);
        }
        Ok(columns)
    }

    async fn describe_primary_keys(
        &self,
        reference: &TableReference,
        context: &str,
    ) -> Result<Vec<String>, AttributeStoreError> {
        let operation = || {
            format!(
                "describe primary key of {context}table {}",
                reference.qualified()
            )
        };
        let rows = self
            .client
            .query_typed(DESCRIBE_PRIMARY_KEYS_QUERY, &table_parameters(reference))
            .await
            .map_err(|error| self.database_error(operation(), &error))?;
        rows.iter()
            .map(|row| row.try_get::<_, String>(0))
            .collect::<Result<_, _>>()
            .map_err(|error| self.database_error(operation(), &error))
    }

    fn invalid_table(&self, reference: &TableReference, reason: String) -> AttributeStoreError {
        AttributeStoreError::InvalidTable {
            store: self.settings.name.clone(),
            table: reference.qualified(),
            reason,
        }
    }

    fn database_error(
        &self,
        operation: impl Into<String>,
        error: &tokio_postgres::Error,
    ) -> AttributeStoreError {
        database_error(&self.settings.name, &self.settings.dsn, operation, error)
    }
}

/// Binds the schema and table as text that Postgres parses as `sql_identifier`.
fn table_parameters(
    reference: &TableReference,
) -> [(&(dyn ToSql + Sync), tokio_postgres::types::Type); 2] {
    [
        (
            &reference.namespace as &(dyn ToSql + Sync),
            tokio_postgres::types::Type::TEXT,
        ),
        (&reference.table, tokio_postgres::types::Type::TEXT),
    ]
}

/// Go's `isGeneratedColumn` for Postgres: identity and generated columns.
fn is_generated_column(is_identity: &str, is_generated: &str) -> bool {
    [is_identity, is_generated]
        .iter()
        .any(|value| value.eq_ignore_ascii_case("YES") || value.eq_ignore_ascii_case("ALWAYS"))
}

pub(crate) fn has_same_columns(actual: &[String], expected: &[String]) -> bool {
    let mut sorted_actual = actual.to_vec();
    let mut sorted_expected = expected.to_vec();
    sorted_actual.sort();
    sorted_expected.sort();
    sorted_actual == sorted_expected
}

pub(crate) fn quote_identifier(identifier: &str) -> String {
    format!("\"{}\"", identifier.replace('"', "\"\""))
}
