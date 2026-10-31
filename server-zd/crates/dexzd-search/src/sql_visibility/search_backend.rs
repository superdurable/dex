// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::VecDeque;
use std::ops::{Deref, DerefMut};
use std::sync::Mutex as StandardMutex;
use std::sync::{Arc, RwLock};
use std::time::Duration;

use async_trait::async_trait;
use dexzd_keyspace::{RunId, TenantId};
use dexzd_postgres_connection::{
    ConnectionError, ConnectionSettings, OpenConnection, error_with_sources,
};
use dexzd_visibility_query::parse_and_check;
use tokio::sync::{Mutex, MutexGuard, Semaphore, SemaphorePermit};
use tokio::task::JoinHandle;
use tokio_postgres::{Client, Config, Row};

use super::{
    AttributeIndexRegistryReader, AttributeSlot, PageCursor, SchemaError, SqlStatement,
    SqlTranslationError, VisibilityDialect, assign_attribute_slots, ensure_schema_for_dialect,
    tenant_column_value, translate_query_for_dialect,
};
use crate::page_token::PagePosition;
use crate::search_backend::{DEFAULT_PAGE_SIZE, MAX_PAGE_SIZE};
use crate::{SearchBackend, SearchError, SearchPage, SearchRequest};

use super::search_result::response_entry;

pub(super) const DATABASE_OPERATION_TIMEOUT: Duration = Duration::from_secs(15);
const SCHEMA_SETUP_TIMEOUT: Duration = Duration::from_secs(60);

/// Waits for an application-defined projection watermark before reading search results.
#[async_trait]
pub trait ProjectionVisibilityWait: Send + Sync {
    /// Waits until the required writes for this request are visible.
    async fn wait_until_visible(&self, request: SearchRequest<'_>) -> Result<(), SearchError>;
}

enum AttributeSlotOperation<'assignments> {
    Register {
        registry_version: u64,
        assignments: &'assignments [AttributeSlot],
    },
    BackfillBatch,
}

/// Searches tenant runs through a bounded pool of PgBouncer-compatible connections.
pub struct SqlVisibilitySearchBackend {
    configuration: ConnectionSettings,
    dialect: VisibilityDialect,
    registry_reader: Option<Arc<dyn AttributeIndexRegistryReader>>,
    attribute_slot_cache: RwLock<Option<Arc<super::slots::AttributeSlotSnapshot>>>,
    slots: Vec<Mutex<ConnectionSlot>>,
    free_slots: StandardMutex<VecDeque<usize>>,
    available_slots: Semaphore,
    visibility_wait: Option<Arc<dyn ProjectionVisibilityWait>>,
    visibility_wait_timeout: Duration,
}

impl SqlVisibilitySearchBackend {
    /// Opens `pool_size` connections, ensures the schema, and bounds projection visibility waits.
    pub async fn connect(
        configuration: &Config,
        pool_size: usize,
        visibility_wait: Option<Arc<dyn ProjectionVisibilityWait>>,
        visibility_wait_timeout: Duration,
    ) -> Result<Self, SearchError> {
        Self::connect_with_settings(
            &ConnectionSettings::from_config(configuration.clone()),
            pool_size,
            visibility_wait,
            visibility_wait_timeout,
        )
        .await
    }

    /// Opens connections using parsed PostgreSQL TLS settings.
    pub async fn connect_with_settings(
        configuration: &ConnectionSettings,
        pool_size: usize,
        visibility_wait: Option<Arc<dyn ProjectionVisibilityWait>>,
        visibility_wait_timeout: Duration,
    ) -> Result<Self, SearchError> {
        Self::connect_dialect_with_settings(
            configuration,
            pool_size,
            visibility_wait,
            visibility_wait_timeout,
            VisibilityDialect::ParadeDb,
            None,
        )
        .await
    }

    /// Opens pooled connections for a dialect and shares its Attribute registry reader.
    pub async fn connect_dialect_with_settings(
        configuration: &ConnectionSettings,
        pool_size: usize,
        visibility_wait: Option<Arc<dyn ProjectionVisibilityWait>>,
        visibility_wait_timeout: Duration,
        dialect: VisibilityDialect,
        registry_reader: Option<Arc<dyn AttributeIndexRegistryReader>>,
    ) -> Result<Self, SearchError> {
        if dialect == VisibilityDialect::Postgres && registry_reader.is_none() {
            return Err(SearchError::Internal(
                "PostgreSQL visibility requires an Attribute registry reader".into(),
            ));
        }
        if pool_size == 0 || visibility_wait_timeout.is_zero() {
            return Err(SearchError::Internal(
                "SQL visibility search pool size and visibility wait timeout must be positive"
                    .into(),
            ));
        }
        let mut slots = Vec::with_capacity(pool_size);
        for slot_index in 0..pool_size {
            let mut slot = ConnectionSlot::open(configuration).await?;
            if slot_index == 0 {
                slot.ensure_schema_with_timeout(configuration, dialect, SCHEMA_SETUP_TIMEOUT)
                    .await?;
            }
            slots.push(Mutex::new(slot));
        }
        let backend = Self {
            configuration: configuration.clone(),
            dialect,
            registry_reader,
            attribute_slot_cache: RwLock::new(None),
            slots,
            free_slots: StandardMutex::new((0..pool_size).collect()),
            available_slots: Semaphore::new(pool_size),
            visibility_wait,
            visibility_wait_timeout,
        };
        if dialect == VisibilityDialect::Postgres {
            backend.refresh_attribute_slot_registry(true).await?;
        }
        Ok(backend)
    }

    pub(crate) fn dialect(&self) -> VisibilityDialect {
        self.dialect
    }

    pub(crate) async fn attribute_slots_for_write(
        &self,
        transaction: &tokio_postgres::Transaction<'_>,
    ) -> Result<Arc<super::slots::AttributeSlotSnapshot>, SearchError> {
        super::slots::lock_attribute_slots_shared(transaction).await?;
        let version = super::slots::read_slot_registry_version(transaction).await?;
        if let Some(snapshot) = self.attribute_slot_snapshot()
            && snapshot.registry_version == version
        {
            return Ok(snapshot);
        }
        let snapshot = super::slots::load_attribute_slot_snapshot(transaction, version).await?;
        Ok(self.cache_attribute_slot_snapshot(snapshot))
    }

    pub(crate) async fn backfill_registered_attributes(&self) -> Result<(), SearchError> {
        if self.dialect != VisibilityDialect::Postgres {
            return Ok(());
        }
        self.refresh_attribute_slot_registry(false).await?;
        let snapshot = self
            .attribute_slot_snapshot()
            .expect("PostgreSQL startup initializes its Attribute slot snapshot");
        if snapshot.ready.len() == snapshot.assignments.len() {
            return Ok(());
        }
        let snapshot = self
            .run_attribute_slot_operation(AttributeSlotOperation::BackfillBatch)
            .await?;
        self.cache_attribute_slot_snapshot(snapshot);
        Ok(())
    }

    async fn refresh_attribute_slot_registry(
        &self,
        should_reconcile: bool,
    ) -> Result<(), SearchError> {
        let reader = self
            .registry_reader
            .as_ref()
            .expect("PostgreSQL requires a registry reader at construction");
        let registry = tokio::time::timeout(
            DATABASE_OPERATION_TIMEOUT,
            reader.refresh_attribute_index_registry(),
        )
        .await
        .map_err(|_| {
            SearchError::Unavailable("refresh Attribute index registry timed out".into())
        })??;
        if !should_reconcile
            && self
                .attribute_slot_snapshot()
                .is_some_and(|snapshot| snapshot.registry_version == registry.version)
        {
            return Ok(());
        }
        let assignments = assign_attribute_slots(&registry)?;
        let snapshot = self
            .run_attribute_slot_operation(AttributeSlotOperation::Register {
                registry_version: registry.version,
                assignments: &assignments,
            })
            .await?;
        self.cache_attribute_slot_snapshot(snapshot);
        Ok(())
    }

    async fn run_attribute_slot_operation(
        &self,
        operation: AttributeSlotOperation<'_>,
    ) -> Result<super::slots::AttributeSlotSnapshot, SearchError> {
        for attempt in 0..2 {
            let mut slot = self.lock_next_slot().await;
            if slot.is_disconnected() {
                slot.reconnect(&self.configuration).await?;
            }
            let result = tokio::time::timeout(DATABASE_OPERATION_TIMEOUT, async {
                match &operation {
                    AttributeSlotOperation::Register {
                        registry_version,
                        assignments,
                    } => {
                        super::slots::initialize_attribute_slots(
                            &mut slot.client,
                            *registry_version,
                            assignments,
                        )
                        .await
                    }
                    AttributeSlotOperation::BackfillBatch => {
                        super::slots::backfill_next_batch(&mut slot.client).await?;
                        super::slots::load_committed_attribute_slot_snapshot(&mut slot.client).await
                    }
                }
            })
            .await;
            let result = match result {
                Ok(result) => result,
                Err(_) => {
                    slot.invalidate();
                    Err(SearchError::Unavailable(format!(
                        "Attribute slot database operation timed out after {DATABASE_OPERATION_TIMEOUT:?}"
                    )))
                }
            };
            if attempt == 0 && matches!(&result, Err(SearchError::Unavailable(_))) {
                slot.reconnect(&self.configuration).await?;
                continue;
            }
            return result.map_err(|error| redact_search_error(&self.configuration, error));
        }
        unreachable!("an Attribute slot operation returns on its second attempt")
    }

    fn cache_attribute_slot_snapshot(
        &self,
        mut snapshot: super::slots::AttributeSlotSnapshot,
    ) -> Arc<super::slots::AttributeSlotSnapshot> {
        let mut cached = self
            .attribute_slot_cache
            .write()
            .expect("the Attribute slot snapshot lock cannot be poisoned");
        if let Some(existing) = cached.as_ref() {
            if existing.registry_version > snapshot.registry_version {
                return existing.clone();
            }
            if existing.registry_version == snapshot.registry_version {
                for assignment in &existing.ready {
                    if !snapshot.ready.contains(assignment) {
                        snapshot.ready.push(assignment.clone());
                    }
                }
            }
        }
        let snapshot = Arc::new(snapshot);
        *cached = Some(snapshot.clone());
        snapshot
    }

    fn attribute_slot_snapshot(&self) -> Option<Arc<super::slots::AttributeSlotSnapshot>> {
        self.attribute_slot_cache
            .read()
            .expect("the Attribute slot snapshot lock cannot be poisoned")
            .clone()
    }

    fn ready_attribute_slots(&self) -> Vec<AttributeSlot> {
        self.attribute_slot_snapshot()
            .map_or_else(Vec::new, |snapshot| snapshot.ready.clone())
    }

    async fn query(&self, statement: &SqlStatement) -> Result<Vec<Row>, SearchError> {
        let mut slot = self.lock_next_slot().await;
        if slot.is_disconnected() {
            slot.reconnect(&self.configuration).await?;
        }
        // Keep query_typed unnamed: custom plans let PostgreSQL use indexes for each request's predicates.
        for attempt in 0..2 {
            match tokio::time::timeout(
                DATABASE_OPERATION_TIMEOUT,
                slot.client
                    .query_typed(&statement.sql, &statement.typed_parameter_refs()),
            )
            .await
            {
                Ok(Ok(rows)) => return Ok(rows),
                Ok(Err(error)) if is_transient_database_error(&error) && attempt == 0 => {
                    slot.reconnect(&self.configuration).await?;
                }
                Ok(Err(error)) => {
                    return Err(classify_database_error(
                        &self.configuration,
                        "query SQL visibility runs",
                        error,
                    ));
                }
                Err(_) => {
                    slot.invalidate();
                    if attempt == 0 {
                        slot.reconnect(&self.configuration).await?;
                    } else {
                        return Err(SearchError::Unavailable(format!(
                            "query SQL visibility runs timed out after {DATABASE_OPERATION_TIMEOUT:?}"
                        )));
                    }
                }
            }
        }
        unreachable!("the query returns on its second attempt")
    }

    pub(super) async fn lock_next_slot(&self) -> PooledConnectionSlot<'_> {
        let permit = self
            .available_slots
            .acquire()
            .await
            .expect("the owned connection pool never closes its semaphore");
        let slot_index = self
            .free_slots
            .lock()
            .expect("the connection pool slot queue cannot be poisoned")
            .pop_front()
            .expect("a semaphore permit guarantees one free connection slot");
        let slot = self.slots[slot_index]
            .try_lock()
            .expect("a free slot is unlocked before its permit is released");
        PooledConnectionSlot {
            slot,
            slot_index,
            backend: self,
            _permit: permit,
        }
    }

    pub(super) fn configuration(&self) -> &ConnectionSettings {
        &self.configuration
    }
}

pub(super) struct PooledConnectionSlot<'a> {
    slot: MutexGuard<'a, ConnectionSlot>,
    slot_index: usize,
    backend: &'a SqlVisibilitySearchBackend,
    _permit: SemaphorePermit<'a>,
}

impl Deref for PooledConnectionSlot<'_> {
    type Target = ConnectionSlot;

    fn deref(&self) -> &Self::Target {
        &self.slot
    }
}

impl DerefMut for PooledConnectionSlot<'_> {
    fn deref_mut(&mut self) -> &mut Self::Target {
        &mut self.slot
    }
}

impl Drop for PooledConnectionSlot<'_> {
    fn drop(&mut self) {
        self.backend
            .free_slots
            .lock()
            .expect("the connection pool slot queue cannot be poisoned")
            .push_back(self.slot_index);
    }
}

pub(super) struct ConnectionSlot {
    pub(super) client: Client,
    connection_task: JoinHandle<()>,
    is_invalidated: bool,
}

impl ConnectionSlot {
    pub(super) async fn open(configuration: &ConnectionSettings) -> Result<Self, SearchError> {
        let OpenConnection { client, driver } =
            configuration.connect().await.map_err(|error| match error {
                ConnectionError::Timeout(timeout) => SearchError::Unavailable(format!(
                    "connect to SQL visibility database timed out after {timeout:?}"
                )),
                ConnectionError::Database(source) => classify_database_error(
                    configuration,
                    "connect to SQL visibility database",
                    source,
                ),
            })?;
        let connection_settings = configuration.clone();
        let connection_task = tokio::spawn(async move {
            if let Err(error) = driver.await {
                let error = connection_settings.redact_credentials(error_with_sources(&error));
                tracing::error!(%error, "SQL visibility search connection closed");
            }
        });
        Ok(Self {
            client,
            connection_task,
            is_invalidated: false,
        })
    }

    async fn ensure_schema_with_timeout(
        &mut self,
        configuration: &ConnectionSettings,
        dialect: VisibilityDialect,
        timeout: Duration,
    ) -> Result<(), SearchError> {
        match tokio::time::timeout(
            timeout,
            ensure_schema_for_dialect(&mut self.client, dialect),
        )
        .await
        {
            Ok(result) => result.map_err(|error| schema_search_error(configuration, error)),
            Err(_) => {
                self.invalidate();
                Err(SearchError::Unavailable(format!(
                    "ensure SQL visibility schema timed out after {timeout:?}"
                )))
            }
        }
    }

    pub(super) async fn reconnect(
        &mut self,
        configuration: &ConnectionSettings,
    ) -> Result<(), SearchError> {
        let replacement = Self::open(configuration).await?;
        *self = replacement;
        Ok(())
    }

    pub(super) fn is_disconnected(&self) -> bool {
        self.is_invalidated || self.client.is_closed() || self.connection_task.is_finished()
    }

    pub(super) fn invalidate(&mut self) {
        self.is_invalidated = true;
        self.connection_task.abort();
    }
}

impl Drop for ConnectionSlot {
    fn drop(&mut self) {
        self.connection_task.abort();
    }
}

#[async_trait]
impl SearchBackend for SqlVisibilitySearchBackend {
    async fn search(&self, request: SearchRequest<'_>) -> Result<SearchPage, SearchError> {
        let page_size = if request.page_size == 0 {
            DEFAULT_PAGE_SIZE
        } else {
            request.page_size.min(MAX_PAGE_SIZE)
        };
        let query = parse_and_check(request.query, request.field_registry)
            .map_err(|error| SearchError::InvalidArgument(error.to_string()))?;
        let position = request
            .page_token
            .map(|token| PagePosition::decode(token, request.tenant_id, request.query))
            .transpose()?;
        let cursor = position.as_ref().map(|position| PageCursor {
            is_running: position.is_running,
            close_time_ns: position.close_time_ns,
            start_time_ns: position.start_time_ns,
            run_id: position.run_id.to_string(),
        });
        let limit = (page_size + 1) as i64;
        let assignments = if self.dialect == VisibilityDialect::Postgres
            && super::query::contains_application_field(query.expression())
        {
            self.ready_attribute_slots()
        } else {
            Vec::new()
        };
        let statement = translate_query_for_dialect(
            &query,
            &tenant_column_value(request.tenant_id),
            limit,
            cursor.as_ref(),
            self.dialect,
            &assignments,
        )
        .map_err(translation_error)?;
        if let Some(visibility_wait) = &self.visibility_wait {
            tokio::time::timeout(
                self.visibility_wait_timeout,
                visibility_wait.wait_until_visible(request),
            )
            .await
            .map_err(|_| {
                SearchError::Unavailable(format!(
                    "wait for SQL visibility projection visibility timed out after {:?}",
                    self.visibility_wait_timeout
                ))
            })??;
        }
        let mut rows = self.query(&statement).await?;
        let has_next_page = rows.len() > page_size;
        if has_next_page {
            rows.truncate(page_size);
        }
        let next_page_token = if has_next_page {
            rows.last()
                .map(|row| row_position(row, request.tenant_id, request.query))
                .transpose()?
        } else {
            None
        };
        let entries = rows.iter().map(response_entry).collect::<Result<_, _>>()?;
        Ok(SearchPage {
            entries,
            next_page_token,
        })
    }
}

fn row_position(
    row: &tokio_postgres::Row,
    tenant_id: TenantId,
    query: &str,
) -> Result<String, SearchError> {
    let run_id: String = row
        .try_get("run_id")
        .map_err(|error| SearchError::Internal(format!("read SQL visibility run ID: {error}")))?;
    let run_id = run_id
        .parse::<RunId>()
        .map_err(|error| SearchError::Internal(format!("parse SQL visibility run ID: {error}")))?;
    let execution_status: String = row.try_get("execution_status").map_err(|error| {
        SearchError::Internal(format!("read SQL visibility execution status: {error}"))
    })?;
    let close_time_ns: Option<i64> = row.try_get("close_time_ns").map_err(|error| {
        SearchError::Internal(format!("read SQL visibility close time: {error}"))
    })?;
    let start_time_ns: i64 = row.try_get("start_time_ns").map_err(|error| {
        SearchError::Internal(format!("read SQL visibility start time: {error}"))
    })?;
    Ok(PagePosition {
        is_running: execution_status == "Running",
        close_time_ns,
        start_time_ns,
        run_id,
    }
    .encode(tenant_id, query))
}

fn redact_search_error(configuration: &ConnectionSettings, error: SearchError) -> SearchError {
    match error {
        SearchError::InvalidArgument(message) => {
            SearchError::InvalidArgument(configuration.redact_credentials(message))
        }
        SearchError::ResourceExhausted(message) => {
            SearchError::ResourceExhausted(configuration.redact_credentials(message))
        }
        SearchError::Unavailable(message) => {
            SearchError::Unavailable(configuration.redact_credentials(message))
        }
        SearchError::Internal(message) => {
            SearchError::Internal(configuration.redact_credentials(message))
        }
    }
}

fn translation_error(error: SqlTranslationError) -> SearchError {
    SearchError::InvalidArgument(error.to_string())
}

pub(super) fn is_transient_database_error(error: &tokio_postgres::Error) -> bool {
    error
        .as_db_error()
        .is_none_or(|database_error| is_transient_sqlstate(database_error.code().code()))
}

fn is_transient_sqlstate(sqlstate: &str) -> bool {
    sqlstate.starts_with("08")
        || sqlstate.starts_with("53")
        || matches!(
            sqlstate,
            "55P03" | "57014" | "57P01" | "57P02" | "57P03" | "57P04" | "57P05" | "40001" | "40P01"
        )
}

/// Redacts the DSN and password from the message, because server-supplied text can repeat them.
pub(super) fn classify_database_error(
    configuration: &ConnectionSettings,
    operation: &str,
    error: tokio_postgres::Error,
) -> SearchError {
    let message = configuration
        .redact_credentials(format!("{operation}: {}", database_error_message(&error)));
    if is_transient_database_error(&error) {
        SearchError::Unavailable(message)
    } else {
        SearchError::Internal(message)
    }
}

/// Formats the error without redaction; callers apply [`ConnectionSettings::redact_credentials`].
pub(super) fn database_error_message(error: &tokio_postgres::Error) -> String {
    let Some(database_error) = error.as_db_error() else {
        return format!("SQLSTATE none: {}", error_with_sources(error));
    };
    let mut message = format!(
        "SQLSTATE {}: {}",
        database_error.code().code(),
        database_error.message()
    );
    if let Some(detail) = database_error.detail() {
        message.push_str(&format!("; detail: {detail}"));
    }
    if let Some(constraint) = database_error.constraint() {
        message.push_str(&format!("; constraint: {constraint}"));
    }
    message
}

fn schema_search_error(configuration: &ConnectionSettings, error: SchemaError) -> SearchError {
    match error {
        SchemaError::Database { operation, source } => {
            classify_database_error(configuration, operation, source)
        }
        SchemaError::NewerVersion { .. }
        | SchemaError::DialectMismatch { .. }
        | SchemaError::UnsupportedPostgresVersion { .. } => {
            SearchError::Internal(error.to_string())
        }
    }
}

#[cfg(test)]
#[path = "connection_tests.rs"]
mod tests;
