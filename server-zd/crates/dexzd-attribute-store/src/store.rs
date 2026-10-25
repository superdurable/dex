// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};
use std::sync::{Arc, Mutex, PoisonError, RwLock};
use std::time::Duration;

use dexzd_protocol::dex::Value;
use dexzd_protocol::dex::value::Kind;
use tokio::task::JoinHandle;
use tokio::time::Instant;
use tokio_util::sync::CancellationToken;

use crate::connection::{ConnectionSettings, ConnectionSlot};
use crate::error::{AttributeStoreError, database_error, redact_secret};
use crate::map_table::{MapTableSchema, is_null};
use crate::row_key::RowKeyTemplate;
use crate::schema::{ResolvedTables, SchemaDescriber, TableSchema};
use crate::settings::{AttributeStoreSettings, AttributeSyncItem, FencingToken};
use crate::statements::{
    SqlStatement, build_fencing_statement, build_keyed_delete, build_keyed_upsert,
};
use crate::value_conversion::SqlArgument;

/// Bounds how often unremembered names refresh once `MAXIMUM_CONFIRMED_MISSING_COLUMNS` is reached.
const COLUMN_REFRESH_INTERVAL: Duration = Duration::from_secs(30);

/// Bounds the remembered missing columns; beyond it, unremembered names share `COLUMN_REFRESH_INTERVAL`.
const MAXIMUM_CONFIRMED_MISSING_COLUMNS: usize = 1000;

/// Bounds the remembered fields when instance values have dynamic top-level keys.
const MAXIMUM_MISSING_COLUMN_WARNINGS: usize = 1000;

/// Bounds one schema refresh so a hung metadata query cannot block writes waiting for it.
const SCHEMA_REFRESH_TIMEOUT: Duration = Duration::from_secs(30);

/// A validated Postgres Attribute Store that writes fenced batches.
pub struct AttributeStore {
    settings: AttributeStoreSettings,
    connection_settings: ConnectionSettings,
    row_key: Option<RowKeyTemplate>,
    tables: ResolvedTables,
    schema: RwLock<Arc<TableSchema>>,
    /// Serializes refreshes for unknown columns, so concurrent batches wait for one refresh.
    column_refresh: tokio::sync::Mutex<ColumnRefreshState>,
    missing_column_warnings: Mutex<BTreeSet<String>>,
    metadata_connection: tokio::sync::Mutex<Option<ConnectionSlot>>,
    write_connection: tokio::sync::Mutex<Option<ConnectionSlot>>,
}

#[derive(Debug, Default)]
pub(crate) struct ColumnRefreshState {
    pub(crate) last_refresh: Option<Instant>,
    /// The columns a refresh found missing; they wait for the periodic refresh.
    pub(crate) confirmed_missing_columns: BTreeSet<String>,
}

impl AttributeStore {
    /// Connects and validates every table like Go's startup; it never creates or alters a table.
    pub async fn connect_and_validate(
        settings: AttributeStoreSettings,
    ) -> Result<Self, AttributeStoreError> {
        settings.validate()?;
        let row_key = settings
            .row_key
            .as_deref()
            .filter(|row_key| !row_key.is_empty())
            .map(RowKeyTemplate::parse)
            .transpose()
            .map_err(|reason| AttributeStoreError::InvalidSettings {
                store: settings.name.clone(),
                reason,
            })?;
        let connection_settings = ConnectionSettings::parse(&settings.dsn).map_err(|reason| {
            AttributeStoreError::InvalidConnectionSettings {
                store: settings.name.clone(),
                reason: redact_secret(reason, &settings.dsn),
            }
        })?;
        let slot = connection_settings.connect(&settings).await?;
        slot.client
            .simple_query("SELECT 1")
            .await
            .map_err(|error| {
                database_error(&settings.name, &settings.dsn, "ping database", &error)
            })?;
        let describer = SchemaDescriber {
            client: &slot.client,
            settings: &settings,
        };
        let tables = describer.resolve_tables().await?;
        let snapshot = describer.describe(&tables, row_key.as_ref()).await?;
        describer
            .validate_fencing_table(&tables.fencing_table)
            .await?;
        Ok(Self {
            connection_settings,
            row_key,
            tables,
            schema: RwLock::new(Arc::new(snapshot)),
            column_refresh: tokio::sync::Mutex::new(ColumnRefreshState::default()),
            missing_column_warnings: Mutex::new(BTreeSet::new()),
            metadata_connection: tokio::sync::Mutex::new(Some(slot)),
            write_connection: tokio::sync::Mutex::new(None),
            settings,
        })
    }

    /// Writes one Flow's batch in one fenced transaction with Go's `writeBatch` semantics.
    ///
    /// Returns Ok without SQL for an unmatched rowKey or an empty batch, and `Fenced` for an older token.
    pub async fn write_batch(
        &self,
        flow_id: &str,
        items: &[AttributeSyncItem],
        fence: &FencingToken,
    ) -> Result<(), AttributeStoreError> {
        let token =
            i64::try_from(fence.token).map_err(|_| AttributeStoreError::InvalidFencingToken {
                store: self.settings.name.clone(),
                token: fence.token,
            })?;
        let snapshot = self.current_schema();
        let Some(key_values) = snapshot.key_values(flow_id) else {
            tracing::error!(
                store = %self.settings.name,
                flow_id,
                "skip Attribute Store batch: Flow ID does not match rowKey"
            );
            return Ok(());
        };
        let latest = latest_values(items);
        let snapshot = self.snapshot_with_columns(snapshot, &latest).await;
        let statements = self.build_statements(&snapshot, &key_values, &latest);
        if statements.is_empty() {
            return Ok(());
        }
        let fencing = build_fencing_statement(
            &self.tables.fencing_table,
            &self.settings.name,
            &fence.cluster_id,
            &fence.scope,
            token,
        );
        self.execute_fenced(&fencing, &statements, fence).await
    }

    /// Replaces the schema snapshot; on failure the previous snapshot stays in use.
    pub async fn refresh_schema(&self) -> Result<(), AttributeStoreError> {
        let mut connection = self.metadata_connection.lock().await;
        let outcome = tokio::time::timeout(
            SCHEMA_REFRESH_TIMEOUT,
            self.describe_tables(&mut connection),
        )
        .await;
        let snapshot = match outcome {
            Ok(result) => result?,
            Err(_) => {
                *connection = None;
                return Err(AttributeStoreError::Timeout {
                    store: self.settings.name.clone(),
                    operation: format!(
                        "refresh the schema of table {}",
                        self.tables.table.qualified()
                    ),
                    timeout: SCHEMA_REFRESH_TIMEOUT,
                });
            }
        };
        *self.schema.write().unwrap_or_else(PoisonError::into_inner) = Arc::new(snapshot);
        Ok(())
    }

    /// Spawns the jittered periodic schema refresh; the task ends when `cancel` fires.
    #[expect(
        clippy::let_and_return,
        reason = "R14 requires binding every spawned task handle before handing it to its owner"
    )]
    pub fn spawn_schema_refresher(self: &Arc<Self>, cancel: CancellationToken) -> JoinHandle<()> {
        let store = Arc::clone(self);
        let refresher = tokio::spawn(async move {
            loop {
                let wait =
                    jitter_interval(store.settings.schema_sync_interval, rand::random::<f64>());
                tokio::select! {
                    () = cancel.cancelled() => return,
                    () = tokio::time::sleep(wait) => {}
                }
                tokio::select! {
                    () = cancel.cancelled() => return,
                    result = store.refresh_schema() => {
                        if let Err(error) = result {
                            tracing::error!(store = %store.settings.name, %error, "refresh Attribute Store schema");
                        }
                    }
                }
            }
        });
        refresher
    }

    pub fn name(&self) -> &str {
        &self.settings.name
    }

    pub(crate) fn current_schema(&self) -> Arc<TableSchema> {
        Arc::clone(&self.schema.read().unwrap_or_else(PoisonError::into_inner))
    }

    async fn describe_tables(
        &self,
        connection: &mut Option<ConnectionSlot>,
    ) -> Result<TableSchema, AttributeStoreError> {
        let slot = self.connected(connection).await?;
        SchemaDescriber {
            client: &slot.client,
            settings: &self.settings,
        }
        .describe(&self.tables, self.row_key.as_ref())
        .await
    }

    /// Go's `snapshotWithColumns`: refreshes first when the batch writes an unconfirmed unknown column.
    async fn snapshot_with_columns(
        &self,
        snapshot: Arc<TableSchema>,
        latest: &BTreeMap<String, Value>,
    ) -> Arc<TableSchema> {
        if self.unknown_columns(&snapshot, latest).is_empty() {
            return snapshot;
        }
        let mut state = self.column_refresh.lock().await;
        let snapshot = self.current_schema();
        let now = Instant::now();
        let unknown = self.unknown_columns(&snapshot, latest);
        if !unknown
            .iter()
            .any(|column| state.needs_refresh(column, now))
        {
            return snapshot;
        }
        if let Err(error) = self.refresh_schema().await {
            tracing::error!(
                store = %self.settings.name,
                %error,
                "refresh Attribute Store schema for an unknown column"
            );
            return self.current_schema();
        }
        state.last_refresh = Some(now);
        let snapshot = self.current_schema();
        for column in self.unknown_columns(&snapshot, latest) {
            if state.confirmed_missing_columns.contains(&column)
                || state.confirmed_missing_columns.len() < MAXIMUM_CONFIRMED_MISSING_COLUMNS
            {
                state.confirmed_missing_columns.insert(column);
            }
        }
        snapshot
    }

    /// Returns the store columns, and the AttributeMap columns as `<map>.<column>`, that the batch writes
    /// and the snapshot lacks.
    fn unknown_columns(
        &self,
        snapshot: &TableSchema,
        latest: &BTreeMap<String, Value>,
    ) -> Vec<String> {
        let mut unknown = Vec::new();
        for (key, value) in latest {
            if let Some((map_name, _)) = key.split_once('/')
                && let Some(map_table) = snapshot.map_tables.get(map_name)
            {
                for column in map_table.unknown_columns(value, self.settings.column_naming) {
                    unknown.push(format!("{map_name}.{column}"));
                }
                continue;
            }
            let column = self.settings.column_naming.column_name(key);
            if !snapshot.columns.contains_key(&column) {
                unknown.push(column);
            }
        }
        unknown
    }

    /// Returns the store row's upsert, when any Attribute survives filtering, followed by one upsert or
    /// delete for each AttributeMap instance, ordered by map name and instance key.
    pub(crate) fn build_statements(
        &self,
        snapshot: &TableSchema,
        key_values: &[String],
        latest: &BTreeMap<String, Value>,
    ) -> Vec<SqlStatement> {
        let mut attribute_values = BTreeMap::new();
        let mut instance_values = BTreeMap::<&str, BTreeMap<String, &Value>>::new();
        for (key, value) in latest {
            let instance_key = key
                .split_once('/')
                .filter(|(map_name, _)| snapshot.map_tables.contains_key(*map_name));
            let Some((map_name, escaped_instance)) = instance_key else {
                attribute_values.insert(key.as_str(), value);
                continue;
            };
            match path_unescape(escaped_instance) {
                Some(instance) if !instance.is_empty() => {
                    instance_values
                        .entry(map_name)
                        .or_default()
                        .insert(instance, value);
                }
                _ => tracing::error!(
                    store = %self.settings.name,
                    attribute = key,
                    "skip Attribute Store map instance: invalid instance key"
                ),
            }
        }
        let mut statements = Vec::new();
        let filtered = self.filter_values(snapshot, &attribute_values);
        if !filtered.is_empty() {
            let column_names = filtered.keys().cloned().collect::<Vec<_>>();
            statements.push(build_keyed_upsert(
                &snapshot.reference,
                &snapshot.key_columns(),
                key_values,
                &column_names,
                &filtered,
            ));
        }
        for (map_name, instances) in instance_values {
            let map_table = &snapshot.map_tables[map_name];
            for (instance, value) in instances {
                let mut instance_key_values = key_values.to_vec();
                instance_key_values.push(instance);
                statements.push(self.build_instance_statement(
                    map_table,
                    map_name,
                    &instance_key_values,
                    value,
                ));
            }
        }
        statements
    }

    fn build_instance_statement(
        &self,
        map_table: &MapTableSchema,
        map_name: &str,
        key_values: &[String],
        value: &Value,
    ) -> SqlStatement {
        if is_null(value) {
            return build_keyed_delete(&map_table.reference, &map_table.key_columns, key_values);
        }
        let instance = key_values.last().map_or("", String::as_str);
        let key = format!("{map_name}/{}", path_escape(instance));
        let columns = map_table.filter_instance_value(
            &self.settings.name,
            &key,
            key_values,
            value,
            self.settings.column_naming,
        );
        for field in &columns.missing_column_fields {
            self.warn_missing_column_once(&format!("{map_name}.{field}"));
        }
        build_keyed_upsert(
            &map_table.reference,
            &map_table.key_columns,
            key_values,
            &map_table.replaced_columns,
            &columns.values,
        )
    }

    /// Go's `filterValues`: logs and skips every Attribute that cannot be written.
    fn filter_values(
        &self,
        snapshot: &TableSchema,
        latest: &BTreeMap<&str, &Value>,
    ) -> BTreeMap<String, SqlArgument> {
        let mut attribute_names_by_column = BTreeMap::<String, Vec<&str>>::new();
        for name in latest.keys() {
            attribute_names_by_column
                .entry(self.settings.column_naming.column_name(name))
                .or_default()
                .push(name);
        }
        let mut filtered = BTreeMap::new();
        for (name, attribute_names) in attribute_names_by_column {
            let store = self.settings.name.as_str();
            if attribute_names.len() > 1 {
                tracing::error!(
                    store,
                    attribute = attribute_names.join(", "),
                    "skip Attribute Store items: Attribute names share one column"
                );
                continue;
            }
            let attribute = attribute_names[0];
            let Some(column) = snapshot.columns.get(&name) else {
                tracing::error!(
                    store,
                    attribute,
                    "skip Attribute Store item: column does not exist"
                );
                continue;
            };
            if snapshot.is_key_column(&name) {
                tracing::error!(
                    store,
                    attribute,
                    "skip Attribute Store item: Flow ID column is immutable"
                );
                continue;
            }
            match column.convert(latest[attribute]) {
                Ok(converted) => {
                    filtered.insert(name, column.argument(converted));
                }
                Err(error) => {
                    tracing::error!(
                        store,
                        attribute,
                        error,
                        "skip incompatible Attribute Store item"
                    );
                }
            }
        }
        filtered
    }

    /// Logs a field without a column once; dynamically keyed values stop new warnings at the cap.
    fn warn_missing_column_once(&self, field: &str) {
        let mut warned = self
            .missing_column_warnings
            .lock()
            .unwrap_or_else(PoisonError::into_inner);
        if warned.len() >= MAXIMUM_MISSING_COLUMN_WARNINGS || !warned.insert(field.to_owned()) {
            return;
        }
        tracing::warn!(
            store = %self.settings.name,
            attribute = field,
            "skip Attribute Store map instance field without a column; later writes skip it silently"
        );
    }

    /// Waits for the write connection and runs the fenced transaction, all within `transaction_timeout`.
    async fn execute_fenced(
        &self,
        fencing: &SqlStatement,
        statements: &[SqlStatement],
        fence: &FencingToken,
    ) -> Result<(), AttributeStoreError> {
        let timeout = self.settings.transaction_timeout;
        let deadline = Instant::now() + timeout;
        let timed_out = || AttributeStoreError::Timeout {
            store: self.settings.name.clone(),
            operation: format!("write a batch to table {}", self.tables.table.qualified()),
            timeout,
        };
        let Ok(mut connection) =
            tokio::time::timeout_at(deadline, self.write_connection.lock()).await
        else {
            return Err(timed_out());
        };
        let outcome = tokio::time::timeout_at(
            deadline,
            self.run_fenced_transaction(&mut connection, fencing, statements, fence),
        )
        .await;
        outcome.unwrap_or_else(|_| {
            // The abandoned transaction commits within 2× the timeout or not at all;
            // fencing keeps a late commit safe.
            *connection = None;
            Err(timed_out())
        })
    }

    async fn run_fenced_transaction(
        &self,
        connection: &mut Option<ConnectionSlot>,
        fencing: &SqlStatement,
        statements: &[SqlStatement],
        fence: &FencingToken,
    ) -> Result<(), AttributeStoreError> {
        let slot = self.connected(connection).await?;
        let transaction = slot
            .client
            .transaction()
            .await
            .map_err(|error| self.database_error("begin transaction", &error))?;
        let milliseconds = timeout_milliseconds(self.settings.transaction_timeout);
        transaction
            .batch_execute(&format!(
                "SET LOCAL statement_timeout = {milliseconds}; \
                 SET LOCAL idle_in_transaction_session_timeout = {milliseconds}"
            ))
            .await
            .map_err(|error| self.database_error("set transaction timeouts", &error))?;
        let recorded_rows = transaction
            .execute_typed(&fencing.query, &fencing.typed_arguments())
            .await
            .map_err(|error| {
                self.database_error(
                    format!("record the fencing token in table {}", fencing.table),
                    &error,
                )
            })?;
        if recorded_rows == 0 {
            return Err(AttributeStoreError::Fenced {
                store: self.settings.name.clone(),
                cluster_id: fence.cluster_id.clone(),
                scope: fence.scope.clone(),
                token: fence.token,
            });
        }
        for statement in statements {
            transaction
                .execute_typed(&statement.query, &statement.typed_arguments())
                .await
                .map_err(|error| {
                    self.database_error(format!("write table {}", statement.table), &error)
                })?;
        }
        transaction
            .commit()
            .await
            .map_err(|error| self.database_error("commit transaction", &error))
    }

    /// Returns the open connection in `connection`, opening a new one when it is absent or closed.
    async fn connected<'a>(
        &self,
        connection: &'a mut Option<ConnectionSlot>,
    ) -> Result<&'a mut ConnectionSlot, AttributeStoreError> {
        if connection.as_ref().is_none_or(ConnectionSlot::is_closed) {
            *connection = None;
            *connection = Some(self.connection_settings.connect(&self.settings).await?);
        }
        Ok(connection
            .as_mut()
            .expect("the connection is either open or was just opened"))
    }

    fn database_error(
        &self,
        operation: impl Into<String>,
        error: &tokio_postgres::Error,
    ) -> AttributeStoreError {
        database_error(&self.settings.name, &self.settings.dsn, operation, error)
    }

    #[cfg(test)]
    pub(crate) fn new_for_test(
        settings: AttributeStoreSettings,
        tables: ResolvedTables,
        snapshot: TableSchema,
    ) -> Self {
        Self {
            connection_settings: ConnectionSettings::parse("host=localhost sslmode=disable")
                .expect("a static DSN parses"),
            row_key: snapshot.row_key.clone(),
            tables,
            schema: RwLock::new(Arc::new(snapshot)),
            column_refresh: tokio::sync::Mutex::new(ColumnRefreshState::default()),
            missing_column_warnings: Mutex::new(BTreeSet::new()),
            metadata_connection: tokio::sync::Mutex::new(None),
            write_connection: tokio::sync::Mutex::new(None),
            settings,
        }
    }

    #[cfg(test)]
    pub(crate) async fn column_refresh_state_for_test(
        &self,
    ) -> tokio::sync::MutexGuard<'_, ColumnRefreshState> {
        self.column_refresh.lock().await
    }

    #[cfg(test)]
    pub(crate) fn missing_column_warning_count_for_test(&self) -> usize {
        self.missing_column_warnings
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .len()
    }
}

impl ColumnRefreshState {
    /// Go's `needsColumnRefresh`: confirmed missing columns wait for the periodic refresh.
    fn needs_refresh(&self, column: &str, now: Instant) -> bool {
        if self.confirmed_missing_columns.contains(column) {
            return false;
        }
        if self.confirmed_missing_columns.len() >= MAXIMUM_CONFIRMED_MISSING_COLUMNS {
            return self.last_refresh.is_none_or(|last_refresh| {
                now.duration_since(last_refresh) >= COLUMN_REFRESH_INTERVAL
            });
        }
        true
    }
}

/// Returns the last value of each key; a missing value or a value without a kind is a null write.
pub(crate) fn latest_values(items: &[AttributeSyncItem]) -> BTreeMap<String, Value> {
    let mut latest = BTreeMap::new();
    for item in items {
        let value = item
            .value
            .clone()
            .filter(|value| value.kind.is_some())
            .unwrap_or(Value {
                kind: Some(Kind::NullValue(0)),
            });
        latest.insert(item.key.clone(), value);
    }
    latest
}

/// Go's `jitterInterval`: scales the interval by 0.9 to 1.1 for a sample in [0, 1).
pub(crate) fn jitter_interval(interval: Duration, sample: f64) -> Duration {
    Duration::try_from_secs_f64(interval.as_secs_f64() * (0.9 + sample * 0.2)).unwrap_or(interval)
}

/// Rounds up to whole milliseconds, because zero disables a Postgres timeout, and caps at its int range.
pub(crate) fn timeout_milliseconds(timeout: Duration) -> u128 {
    timeout
        .as_micros()
        .div_ceil(1_000)
        .clamp(1, i32::MAX.unsigned_abs().into())
}

/// Go's `url.PathUnescape`; None for a malformed escape or a key that is not UTF-8.
pub(crate) fn path_unescape(escaped: &str) -> Option<String> {
    let bytes = escaped.as_bytes();
    let mut decoded = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        if bytes[index] != b'%' {
            decoded.push(bytes[index]);
            index += 1;
            continue;
        }
        let high = char::from(*bytes.get(index + 1)?).to_digit(16)?;
        let low = char::from(*bytes.get(index + 2)?).to_digit(16)?;
        decoded.push(u8::try_from(high * 16 + low).ok()?);
        index += 3;
    }
    String::from_utf8(decoded).ok()
}

/// Go's `url.PathEscape`, used only to name instances in logs.
pub(crate) fn path_escape(instance: &str) -> String {
    let mut escaped = String::with_capacity(instance.len());
    for byte in instance.bytes() {
        let is_unescaped = byte.is_ascii_alphanumeric()
            || matches!(
                byte,
                b'-' | b'_' | b'.' | b'~' | b'$' | b'&' | b'+' | b':' | b'=' | b'@'
            );
        if is_unescaped {
            escaped.push(char::from(byte));
        } else {
            escaped.push_str(&format!("%{byte:02X}"));
        }
    }
    escaped
}
