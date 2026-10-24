// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;
use std::fmt;
use std::str::FromStr;
use std::time::Duration;

use dexzd_protocol::dex::Value;

use crate::error::AttributeStoreError;

/// Default fencing table name, resolved against the connection's current schema.
pub const DEFAULT_FENCING_TABLE_NAME: &str = "dexzd_attribute_sync_fencing";

/// Default interval between periodic schema refreshes, before jitter.
pub const DEFAULT_SCHEMA_SYNC_INTERVAL: Duration = Duration::from_secs(60);

/// Default bound on one write transaction, applied on both server and client.
pub const DEFAULT_TRANSACTION_TIMEOUT: Duration = Duration::from_secs(5);

/// The largest Postgres `statement_timeout`, in milliseconds.
const MAXIMUM_TRANSACTION_TIMEOUT: Duration = Duration::from_millis(2_147_483_647);

/// One Postgres Attribute Store, matching one Go `attributeStore.stores` entry.
#[derive(Clone)]
pub struct AttributeStoreSettings {
    /// The store name that Flows select. Must not be empty.
    pub name: String,
    /// The Postgres connection string. It may contain credentials and is redacted from errors.
    pub dsn: String,
    /// The store table as `table` or `schema.table`; a bare table uses the current schema.
    pub table_name: String,
    /// Splits each Flow ID into primary-key columns, such as `order-{customer_id}-{order_id}`.
    pub row_key: Option<String>,
    /// Maps Attribute names and AttributeMap instance fields to column names.
    pub column_naming: ColumnNaming,
    /// Writes each instance of the named AttributeMaps as one row of its own table.
    pub attribute_maps: BTreeMap<String, AttributeMapTableSettings>,
    /// The Dex-owned fencing table as `table` or `schema.table`. Default `dexzd_attribute_sync_fencing`.
    pub fencing_table_name: String,
    /// Interval between periodic schema refreshes; each wait receives 90–110% jitter. Default 1m.
    pub schema_sync_interval: Duration,
    /// Bounds the connection wait plus transaction, and each statement. Default 5s; at most 2147483647ms.
    pub transaction_timeout: Duration,
}

/// The column naming rule, matching Go's `columnNaming` values.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum ColumnNaming {
    /// Go's empty value: each name is its column name.
    #[default]
    AsIs,
    /// Go's `snake_case`: `DeploymentURL` becomes `deployment_url`.
    SnakeCase,
}

/// A `columnNaming` configuration value other than empty or `snake_case`.
#[derive(Debug, thiserror::Error)]
#[error("unsupported columnNaming {0:?}")]
pub struct UnsupportedColumnNaming(pub String);

/// The table that stores one AttributeMap's instances.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AttributeMapTableSettings {
    /// The instance table as `table` or `schema.table`.
    pub table_name: String,
    /// The column that stores the unescaped instance key.
    pub instance_key_column: String,
}

/// Identifies the writer of a transaction; a smaller token for the same cluster and scope is fenced.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct FencingToken {
    /// The cluster ID; tokens are compared only within one cluster.
    pub cluster_id: String,
    /// The writer scope within the cluster, such as `node:<node_id>`.
    pub scope: String,
    /// The token, which must fit in a Postgres bigint.
    pub token: u64,
}

/// One Attribute write, keyed like Go's `AttributeSyncItem.key`.
#[derive(Clone, Debug, PartialEq)]
pub struct AttributeSyncItem {
    /// The Attribute name, or `<map>/<escaped instance>` for an AttributeMap instance.
    pub key: String,
    /// The latest value; `None` is a null write.
    pub value: Option<Value>,
}

impl AttributeStoreSettings {
    /// Returns settings with the given destination and the default options.
    pub fn new(
        name: impl Into<String>,
        dsn: impl Into<String>,
        table_name: impl Into<String>,
    ) -> Self {
        Self {
            name: name.into(),
            dsn: dsn.into(),
            table_name: table_name.into(),
            row_key: None,
            column_naming: ColumnNaming::AsIs,
            attribute_maps: BTreeMap::new(),
            fencing_table_name: DEFAULT_FENCING_TABLE_NAME.to_owned(),
            schema_sync_interval: DEFAULT_SCHEMA_SYNC_INTERVAL,
            transaction_timeout: DEFAULT_TRANSACTION_TIMEOUT,
        }
    }

    /// Applies Go's `AttributeStoreConfig.Validate` rules for one Postgres store, plus the zd-only settings.
    pub(crate) fn validate(&self) -> Result<(), AttributeStoreError> {
        let invalid = |reason: String| AttributeStoreError::InvalidSettings {
            store: self.name.clone(),
            reason,
        };
        if self.name.is_empty() {
            return Err(invalid("attribute store name must not be empty".to_owned()));
        }
        if self.dsn.is_empty() {
            return Err(invalid(format!(
                "attribute store {:?} requires dsn",
                self.name
            )));
        }
        if self.table_name.is_empty() {
            return Err(invalid(format!(
                "attribute store {:?} requires tableName",
                self.name
            )));
        }
        validate_table_name(&self.table_name).map_err(|reason| {
            invalid(format!(
                "attribute store {:?} has invalid tableName: {reason}",
                self.name
            ))
        })?;
        for (map_name, map_table) in &self.attribute_maps {
            validate_map_table(map_name, map_table).map_err(|reason| {
                invalid(format!(
                    "attribute store {:?} attributeMaps: {reason}",
                    self.name
                ))
            })?;
        }
        validate_table_name(&self.fencing_table_name).map_err(|reason| {
            invalid(format!(
                "attribute store {:?} has invalid fencingTableName: {reason}",
                self.name
            ))
        })?;
        if self.schema_sync_interval.is_zero() {
            return Err(invalid(
                "attribute store schemaSyncInterval must be positive".to_owned(),
            ));
        }
        if self.transaction_timeout.is_zero() {
            return Err(invalid(
                "attribute store transactionTimeout must be positive".to_owned(),
            ));
        }
        if self.transaction_timeout > MAXIMUM_TRANSACTION_TIMEOUT {
            return Err(invalid(
                "attribute store transactionTimeout must not exceed 2147483647ms".to_owned(),
            ));
        }
        Ok(())
    }
}

impl fmt::Debug for AttributeStoreSettings {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("AttributeStoreSettings")
            .field("name", &self.name)
            .field("dsn", &"[REDACTED]")
            .field("table_name", &self.table_name)
            .field("row_key", &self.row_key)
            .field("column_naming", &self.column_naming)
            .field("attribute_maps", &self.attribute_maps)
            .field("fencing_table_name", &self.fencing_table_name)
            .field("schema_sync_interval", &self.schema_sync_interval)
            .field("transaction_timeout", &self.transaction_timeout)
            .finish()
    }
}

impl FromStr for ColumnNaming {
    type Err = UnsupportedColumnNaming;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        match value {
            "" => Ok(Self::AsIs),
            "snake_case" => Ok(Self::SnakeCase),
            other => Err(UnsupportedColumnNaming(other.to_owned())),
        }
    }
}

fn validate_map_table(map_name: &str, map_table: &AttributeMapTableSettings) -> Result<(), String> {
    if map_name.is_empty() || map_name.contains('/') {
        return Err(format!(
            "AttributeMap name {map_name:?} must be non-empty and contain no '/'"
        ));
    }
    if map_table.table_name.is_empty() || map_table.instance_key_column.is_empty() {
        return Err(format!(
            "AttributeMap {map_name:?} requires tableName and instanceKeyColumn"
        ));
    }
    validate_table_name(&map_table.table_name)
        .map_err(|reason| format!("AttributeMap {map_name:?} has invalid tableName: {reason}"))
}

/// Go's `validateAttributeStoreTableName` for Postgres: at most two non-empty identifiers without NUL.
pub(crate) fn validate_table_name(table_name: &str) -> Result<(), String> {
    let parts = table_name.split('.').collect::<Vec<_>>();
    if parts.len() > 2 {
        return Err("must contain at most 2 identifiers".to_owned());
    }
    if parts
        .iter()
        .any(|part| part.is_empty() || part.contains('\0'))
    {
        return Err("contains an invalid identifier".to_owned());
    }
    Ok(())
}
