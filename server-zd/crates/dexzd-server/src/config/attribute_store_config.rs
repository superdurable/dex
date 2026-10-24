// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;
use std::fmt;
use std::time::Duration;

use serde::{Deserialize, Deserializer, Serialize, Serializer};

use super::ConfigError;

/// Default `attributeStore.schemaSyncInterval`, also used for an explicit zero.
const DEFAULT_SCHEMA_SYNC_INTERVAL: Duration = Duration::from_secs(60);
/// Default `attributeStore.syncBatchSize`, also used for an explicit zero.
const DEFAULT_SYNC_BATCH_SIZE: u32 = 100;
/// Default `attributeStore.syncAttemptTimeout`, also used for an explicit zero.
const DEFAULT_SYNC_ATTEMPT_TIMEOUT: Duration = Duration::from_secs(30);
/// Default `attributeStore.syncRetryPolicy.initialInterval`, also used for an explicit zero.
const DEFAULT_RETRY_INITIAL_INTERVAL: Duration = Duration::from_millis(100);
/// Default `attributeStore.syncRetryPolicy.backoffCoefficient`, also used for an explicit zero.
const DEFAULT_RETRY_BACKOFF_COEFFICIENT: f64 = 2.0;
/// Default `attributeStore.syncRetryPolicy.maximumInterval`, also used for an explicit zero.
const DEFAULT_RETRY_MAXIMUM_INTERVAL: Duration = Duration::from_secs(30);
/// Default `attributeStore.syncRetryPolicy.totalDuration`, also used for an explicit zero.
const DEFAULT_RETRY_TOTAL_DURATION: Duration = Duration::from_secs(60 * 60);
/// Default `attributeStore.stores.<name>.fencingTableName`.
const DEFAULT_FENCING_TABLE_NAME: &str = "dexzd_attribute_sync_fencing";

/// Synchronizes Attributes written with SyncToAttributeStore into PostgreSQL tables.
///
/// The keys match the Go Dex Server's `attributeStore` section. As in Go, an explicit zero for a
/// duration, the batch size, or a retry policy field means its default.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct AttributeStoreConfig {
    /// Defaults to empty, which disables Attribute synchronization and rejects every
    /// FlowConfig.attributeStoreNames entry as an unknown Attribute Store.
    /// Maps each Attribute Store name that FlowConfig.attributeStoreNames may list to its
    /// PostgreSQL destination. Names must be non-empty. Startup connects to every store and checks
    /// its tables, and any failure fails startup. Immutable after startup.
    pub stores: BTreeMap<String, AttributeStoreEntryConfig>,
    /// Defaults to 1m; an explicit zero also means 1m. Interval between table schema refreshes.
    /// Each wait is jittered uniformly to 90-110% of the interval. A write that names an unknown
    /// column refreshes the schema at once.
    #[serde(with = "humantime_serde")]
    pub schema_sync_interval: Duration,
    /// Defaults to 100; an explicit zero also means 100. Most Attributes of one run that one
    /// PostgreSQL transaction writes. A run with more pending Attributes uses several transactions.
    pub sync_batch_size: u32,
    /// Defaults to 30s; an explicit zero also means 30s. Upper bound of one write transaction.
    /// The effective timeout is the smaller of this value and one eighth of lease.duration, so a
    /// transaction always ends while this node still holds its lease. Writes start only while more
    /// than this timeout plus a quarter of lease.duration of lease validity remains.
    #[serde(with = "humantime_serde")]
    pub sync_attempt_timeout: Duration,
    /// Retry backoff and drop deadline of failed writes. Defaults to 100ms initial interval, 30s
    /// maximum interval, coefficient 2, unlimited attempts, and a 1h total duration. `null` means
    /// every default, as in Go.
    #[serde(deserialize_with = "deserialize_null_as_default")]
    pub sync_retry_policy: AttributeStoreRetryPolicyConfig,
}

impl Default for AttributeStoreConfig {
    fn default() -> Self {
        Self {
            stores: BTreeMap::new(),
            schema_sync_interval: DEFAULT_SCHEMA_SYNC_INTERVAL,
            sync_batch_size: DEFAULT_SYNC_BATCH_SIZE,
            sync_attempt_timeout: DEFAULT_SYNC_ATTEMPT_TIMEOUT,
            sync_retry_policy: AttributeStoreRetryPolicyConfig::default(),
        }
    }
}

/// Backoff between failed Attribute Store writes and the deadline after which entries are dropped.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct AttributeStoreRetryPolicyConfig {
    /// Defaults to 100ms; an explicit zero also means 100ms. Delay after the first failed write of
    /// a store; later delays grow by backoffCoefficient up to maximumInterval.
    #[serde(with = "humantime_serde")]
    pub initial_interval: Duration,
    /// Defaults to 2; an explicit zero also means 2. Multiplies the delay after each failed write.
    /// Must be positive and finite.
    pub backoff_coefficient: f64,
    /// Defaults to 30s; an explicit zero also means 30s. Longest delay between attempts; must not be
    /// less than initialInterval.
    #[serde(with = "humantime_serde")]
    pub maximum_interval: Duration,
    /// Defaults to 0, unlimited. Consecutive failed writes of one run's entries, including the first,
    /// after which the entries are dropped. The count lives in memory, so a restart counts again.
    pub maximum_attempts: u32,
    /// Defaults to 1h; an explicit zero also means 1h. Entries still pending this long after they
    /// were first marked, as recorded by the committed log, are dropped with an error log and the
    /// dexzd_attribute_sync_dropped_entries_total metric, like Go skipping a batch. Later writes of
    /// the same Attributes sync normally. Values such as 168h are valid.
    #[serde(with = "humantime_serde")]
    pub total_duration: Duration,
}

impl Default for AttributeStoreRetryPolicyConfig {
    fn default() -> Self {
        Self {
            initial_interval: DEFAULT_RETRY_INITIAL_INTERVAL,
            backoff_coefficient: DEFAULT_RETRY_BACKOFF_COEFFICIENT,
            maximum_interval: DEFAULT_RETRY_MAXIMUM_INTERVAL,
            maximum_attempts: 0,
            total_duration: DEFAULT_RETRY_TOTAL_DURATION,
        }
    }
}

/// Selects the Attribute Store backend; dex-server-zd supports only `postgres`.
#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum AttributeStoreType {
    /// Default; invalid for a configured store.
    #[default]
    #[serde(rename = "")]
    Unset,
    /// PostgreSQL, including Amazon RDS for PostgreSQL.
    Postgres,
    /// Accepted by the Go Dex Server only; fails validation here.
    Mysql,
    /// Accepted by the Go Dex Server only; fails validation here.
    Databricks,
    /// Accepted by the Go Dex Server only; fails validation here.
    Snowflake,
    /// Accepted by the Go Dex Server only; fails validation here.
    Mongodb,
}

impl AttributeStoreType {
    fn yaml_name(self) -> &'static str {
        match self {
            Self::Unset => "",
            Self::Postgres => "postgres",
            Self::Mysql => "mysql",
            Self::Databricks => "databricks",
            Self::Snowflake => "snowflake",
            Self::Mongodb => "mongodb",
        }
    }
}

/// Maps Attribute names, and the fields of AttributeMap instance values, to column names.
#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
pub enum AttributeStoreColumnNaming {
    /// Default, written as an empty string; each name is used exactly.
    #[default]
    #[serde(rename = "")]
    Exact,
    /// Each name is converted to snake_case, such as deployment_url for DeploymentURL.
    #[serde(rename = "snake_case")]
    SnakeCase,
}

/// One PostgreSQL destination of Attribute synchronization.
#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct AttributeStoreEntryConfig {
    /// Defaults to empty, which is invalid. Must be postgres. The Go Dex Server's mysql, databricks,
    /// snowflake, and mongodb fail validation; keep those stores on the Go Dex Server. Immutable
    /// after startup.
    #[serde(rename = "type")]
    pub store_type: AttributeStoreType,
    /// Defaults to empty. PostgreSQL connection string, such as
    /// `host=db.example.com user=dex dbname=app sslmode=verify-full sslrootcert=/etc/ssl/rds.pem`.
    /// When empty, loading reads DEX_ZD_ATTRIBUTE_STORE_DSN_<NAME>, where NAME is the store name in
    /// upper case with every character other than A-Z and 0-9 replaced by an underscore. A store
    /// without a DSN fails validation. Serialization and logs redact non-empty values.
    #[serde(serialize_with = "serialize_redacted_dsn")]
    pub dsn: String,
    /// Defaults to empty, which is invalid. Main table as table or schema.table. Its primary key holds
    /// the Flow ID, or the rowKey columns. Immutable after startup.
    pub table_name: String,
    /// Defaults to empty and must stay empty. Accepted for Go configuration compatibility; only Go
    /// warehouse stores use it.
    pub flow_id_column: String,
    /// Defaults to empty and must stay empty. Accepted for Go configuration compatibility; only Go
    /// MongoDB stores use it.
    pub database_name: String,
    /// Defaults to empty and must stay empty. Accepted for Go configuration compatibility; only Go
    /// MongoDB stores use it.
    pub collection_name: String,
    /// Defaults to empty, which stores the whole Flow ID in the table's single primary-key column.
    /// Splits each Flow ID into primary-key columns, such as `release-{project_id}-{release_id}`.
    /// Each {column} placeholder captures up to the next literal text, the last placeholder captures
    /// the rest, and {*} captures without a column. A Flow ID that does not match drops its writes
    /// with an error log. Immutable after startup.
    pub row_key: String,
    /// Defaults to empty, which uses each Attribute name exactly as the column name; snake_case
    /// uses its snake_case form. Immutable after startup.
    pub column_naming: AttributeStoreColumnNaming,
    /// Defaults to empty, which writes AttributeMap instances like other Attributes. Writes each
    /// instance of the named AttributeMaps as one row of its own table, in the same transaction as
    /// the run's other writes. Deleting an instance deletes its row. Immutable after startup.
    pub attribute_maps: BTreeMap<String, AttributeStoreMapTableConfig>,
    /// Defaults to dexzd_attribute_sync_fencing. Table, as table or schema.table, that holds one
    /// fencing token row per store, cluster, and owner scope. Every write transaction raises its
    /// row to this node's epoch, so a node that lost ownership cannot overwrite newer values. One
    /// table can serve every store and cluster in a database. dex-server-zd never creates or alters
    /// it: create it before startup and grant SELECT, INSERT, and UPDATE. Startup fails with the
    /// CREATE TABLE statement when it is missing. Immutable after startup.
    pub fencing_table_name: String,
}

impl Default for AttributeStoreEntryConfig {
    fn default() -> Self {
        Self {
            store_type: AttributeStoreType::Unset,
            dsn: String::new(),
            table_name: String::new(),
            flow_id_column: String::new(),
            database_name: String::new(),
            collection_name: String::new(),
            row_key: String::new(),
            column_naming: AttributeStoreColumnNaming::Exact,
            attribute_maps: BTreeMap::new(),
            fencing_table_name: DEFAULT_FENCING_TABLE_NAME.to_owned(),
        }
    }
}

impl fmt::Debug for AttributeStoreEntryConfig {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("AttributeStoreEntryConfig")
            .field("store_type", &self.store_type)
            .field("dsn", &"[redacted]")
            .field("table_name", &self.table_name)
            .field("flow_id_column", &self.flow_id_column)
            .field("database_name", &self.database_name)
            .field("collection_name", &self.collection_name)
            .field("row_key", &self.row_key)
            .field("column_naming", &self.column_naming)
            .field("attribute_maps", &self.attribute_maps)
            .field("fencing_table_name", &self.fencing_table_name)
            .finish()
    }
}

/// The table that holds one AttributeMap's instances.
#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct AttributeStoreMapTableConfig {
    /// Defaults to empty, which is invalid. Instance table as table or schema.table. Its primary key
    /// must be exactly the store's primary-key columns plus instanceKeyColumn. Immutable after
    /// startup.
    pub table_name: String,
    /// Defaults to empty, which is invalid. Column that stores the unescaped instance key, such as
    /// account-7 for the instance written as Members/account-7. Immutable after startup.
    pub instance_key_column: String,
}

impl AttributeStoreConfig {
    /// Returns the schema refresh interval, replacing an explicit zero with the default.
    pub fn effective_schema_sync_interval(&self) -> Duration {
        non_zero_duration_or(self.schema_sync_interval, DEFAULT_SCHEMA_SYNC_INTERVAL)
    }

    /// Returns the per-transaction Attribute limit, replacing an explicit zero with the default.
    pub fn effective_sync_batch_size(&self) -> u32 {
        if self.sync_batch_size == 0 {
            DEFAULT_SYNC_BATCH_SIZE
        } else {
            self.sync_batch_size
        }
    }

    /// Returns the transaction timeout bound, replacing an explicit zero with the default.
    pub fn effective_sync_attempt_timeout(&self) -> Duration {
        non_zero_duration_or(self.sync_attempt_timeout, DEFAULT_SYNC_ATTEMPT_TIMEOUT)
    }

    /// Returns the retry policy with every explicit zero replaced by its default.
    pub fn effective_sync_retry_policy(&self) -> AttributeStoreRetryPolicyConfig {
        let policy = &self.sync_retry_policy;
        AttributeStoreRetryPolicyConfig {
            initial_interval: non_zero_duration_or(
                policy.initial_interval,
                DEFAULT_RETRY_INITIAL_INTERVAL,
            ),
            backoff_coefficient: if policy.backoff_coefficient == 0.0 {
                DEFAULT_RETRY_BACKOFF_COEFFICIENT
            } else {
                policy.backoff_coefficient
            },
            maximum_interval: non_zero_duration_or(
                policy.maximum_interval,
                DEFAULT_RETRY_MAXIMUM_INTERVAL,
            ),
            maximum_attempts: policy.maximum_attempts,
            total_duration: non_zero_duration_or(
                policy.total_duration,
                DEFAULT_RETRY_TOTAL_DURATION,
            ),
        }
    }

    /// Reads DEX_ZD_ATTRIBUTE_STORE_DSN_<NAME> for every store whose YAML DSN is empty.
    pub(super) fn resolve_environment_dsns(&mut self) -> Result<(), ConfigError> {
        for (store_name, store) in &mut self.stores {
            if !store.dsn.is_empty() {
                continue;
            }
            let variable = dsn_environment_variable(store_name);
            match std::env::var(&variable) {
                Ok(dsn) => store.dsn = dsn,
                Err(std::env::VarError::NotPresent) => {}
                Err(std::env::VarError::NotUnicode(_)) => {
                    return Err(ConfigError::InvalidAttributeStore {
                        store_name: store_name.clone(),
                        field: "dsn",
                        message: format!("environment variable {variable} must be Unicode"),
                    });
                }
            }
        }
        Ok(())
    }

    /// Checks every store and the retry policy with the Go Dex Server's rules, plus the
    /// postgres-only rule of dex-server-zd.
    pub(super) fn validate(&self) -> Result<(), ConfigError> {
        let policy = self.effective_sync_retry_policy();
        if !policy.backoff_coefficient.is_finite() || policy.backoff_coefficient <= 0.0 {
            return Err(ConfigError::Invalid {
                field: "attributeStore.syncRetryPolicy.backoffCoefficient",
                message: "must be positive and finite".to_owned(),
            });
        }
        if policy.maximum_interval < policy.initial_interval {
            return Err(ConfigError::Invalid {
                field: "attributeStore.syncRetryPolicy.maximumInterval",
                message: "must not be less than initialInterval".to_owned(),
            });
        }
        for (store_name, store) in &self.stores {
            if store_name.is_empty() {
                return Err(ConfigError::Invalid {
                    field: "attributeStore.stores",
                    message: "Attribute Store name must not be empty".to_owned(),
                });
            }
            store.validate(store_name)?;
        }
        Ok(())
    }
}

impl AttributeStoreEntryConfig {
    fn validate(&self, store_name: &str) -> Result<(), ConfigError> {
        let invalid = |field: &'static str, message: String| ConfigError::InvalidAttributeStore {
            store_name: store_name.to_owned(),
            field,
            message,
        };
        match self.store_type {
            AttributeStoreType::Postgres => {}
            AttributeStoreType::Unset => {
                return Err(invalid(
                    "type",
                    "is required and must be postgres".to_owned(),
                ));
            }
            unsupported => {
                return Err(invalid(
                    "type",
                    format!(
                        "{} is not supported by dex-server-zd, which supports only postgres; keep this store on the Go Dex Server",
                        unsupported.yaml_name()
                    ),
                ));
            }
        }
        if self.dsn.is_empty() {
            return Err(invalid(
                "dsn",
                format!(
                    "is required; set it in YAML or in {}",
                    dsn_environment_variable(store_name)
                ),
            ));
        }
        if self.table_name.is_empty() {
            return Err(invalid("tableName", "is required".to_owned()));
        }
        validate_table_name(&self.table_name).map_err(|message| invalid("tableName", message))?;
        if !self.database_name.is_empty() || !self.collection_name.is_empty() {
            return Err(invalid(
                "databaseName",
                "SQL configuration does not accept databaseName or collectionName".to_owned(),
            ));
        }
        if !self.flow_id_column.is_empty() {
            return Err(invalid(
                "flowIdColumn",
                "only warehouse stores accept flowIdColumn".to_owned(),
            ));
        }
        for (map_name, map_table) in &self.attribute_maps {
            if map_name.is_empty() || map_name.contains('/') {
                return Err(invalid(
                    "attributeMaps",
                    format!("AttributeMap name {map_name:?} must be non-empty and contain no '/'"),
                ));
            }
            if map_table.table_name.is_empty() || map_table.instance_key_column.is_empty() {
                return Err(invalid(
                    "attributeMaps",
                    format!("AttributeMap {map_name:?} requires tableName and instanceKeyColumn"),
                ));
            }
            validate_table_name(&map_table.table_name).map_err(|message| {
                invalid(
                    "attributeMaps",
                    format!("AttributeMap {map_name:?} has invalid tableName: {message}"),
                )
            })?;
        }
        validate_table_name(&self.fencing_table_name)
            .map_err(|message| invalid("fencingTableName", message))?;
        Ok(())
    }
}

/// Returns the environment variable that supplies a store's DSN when its YAML DSN is empty.
pub fn dsn_environment_variable(store_name: &str) -> String {
    let suffix: String = store_name
        .chars()
        .map(|character| {
            if character.is_ascii_alphanumeric() {
                character.to_ascii_uppercase()
            } else {
                '_'
            }
        })
        .collect();
    format!("DEX_ZD_ATTRIBUTE_STORE_DSN_{suffix}")
}

/// PostgreSQL accepts table or schema.table, matching Go's two-part limit.
fn validate_table_name(table_name: &str) -> Result<(), String> {
    let parts: Vec<&str> = table_name.split('.').collect();
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

fn non_zero_duration_or(value: Duration, default: Duration) -> Duration {
    if value.is_zero() { default } else { value }
}

fn deserialize_null_as_default<'de, D, T>(deserializer: D) -> Result<T, D::Error>
where
    D: Deserializer<'de>,
    T: Deserialize<'de> + Default,
{
    Option::<T>::deserialize(deserializer).map(Option::unwrap_or_default)
}

fn serialize_redacted_dsn<S>(dsn: &str, serializer: S) -> Result<S::Ok, S::Error>
where
    S: Serializer,
{
    serializer.serialize_str(if dsn.is_empty() { "" } else { "[redacted]" })
}
