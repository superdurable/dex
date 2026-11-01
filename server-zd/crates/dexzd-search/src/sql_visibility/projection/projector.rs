// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;
use std::sync::Arc;

use dexzd_apply::StateReadError;
use dexzd_keyspace::{PartitionId, RunId, TenantId};
use dexzd_postgres_connection::ConnectionSettings;
use dexzd_protocol::dex::IndexType;
use dexzd_protocol::dexzd::v1::{LogPosition, SearchDocumentRecord};
use prost::Message;
use thiserror::Error;
use tokio_postgres::Transaction;
use tokio_postgres::types::{ToSql, Type};

use super::super::SearchRow;
use super::super::search_backend::{
    DATABASE_OPERATION_TIMEOUT, SqlVisibilitySearchBackend, database_error_message,
    is_transient_database_error,
};
use super::super::upsert::upsert_statements_with_slots;
use super::super::{AttributeSlot, VisibilityDialect};
use super::changes::SearchDocumentChange;
use super::progress::{lock_progress, read_progress_rows, update_progress, validate_position};
use super::skipped_document::{SkippedSearchDocument, record_skipped_documents};
use crate::SearchError;
use crate::builtin::document::IndexedDocument;
use dexzd_protocol::attribute_index::indexed_keyword_strings;
use dexzd_protocol::attribute_index::is_system_search_field;

/// What SQL visibility recorded for one partition.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct ProjectionProgress {
    /// Committed view whose SearchOutbox entries were all projected; `None` before the initial load finished.
    pub applied_position: Option<LogPosition>,
    /// Node epoch of the last writer; a lower epoch can no longer write the partition.
    pub applied_epoch: Option<u64>,
    /// Every SearchOutbox entry at or below this sequence is projected; zero when none is.
    pub outbox_through_seq: u64,
}

/// One partition's share of an outbox batch, written in one SQL transaction with the others.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PartitionProjectionBatch {
    /// Partition being projected.
    pub partition_id: PartitionId,
    /// Deletes the partition's rows before its changes; the first batch of an initial load sets it.
    pub is_initial_load: bool,
    /// Changes in outbox order; later changes of a run win.
    pub changes: Vec<SearchDocumentChange>,
    /// Raises the partition's outbox progress to this sequence; never lowers it.
    pub outbox_through_seq: u64,
    /// Set by the batch that finishes a committed view; it records that view's applied position.
    pub applied_position: Option<LogPosition>,
}

/// Receives a countable event for each committed search document omitted from SQL visibility.
pub trait ProjectionMetricRecorder: Send + Sync {
    /// Records a document omitted because its committed value cannot be indexed.
    fn record_skipped_search_document(
        &self,
        partition_id: PartitionId,
        tenant_id: TenantId,
        run_id: RunId,
    );
}

/// A failure while projecting committed run documents.
#[derive(Debug, Error)]
#[non_exhaustive]
pub enum ProjectionError {
    /// A committed row or index violates its expected format.
    #[error("invalid committed search state: {0}")]
    InvalidState(String),
    /// A newer lease holder wrote the partition; this node must stop projecting.
    #[error(
        "SQL visibility partition {} was written at node epoch {stored_epoch}, newer than this node's epoch {epoch}",
        partition_id.0
    )]
    Fenced {
        /// Partition being projected.
        partition_id: PartitionId,
        /// Epoch stored in the partition's progress row.
        stored_epoch: u64,
        /// This writer's node epoch.
        epoch: u64,
    },
    /// Postgres could not complete a projection operation.
    #[error("{operation} SQL visibility projection: {message}")]
    Database {
        /// Failed operation.
        operation: &'static str,
        /// The Postgres error; the projector redacts the DSN and password before returning it.
        message: String,
        /// Whether the database failure is transient, classified before its raw Postgres error was dropped.
        is_transient: bool,
    },
    /// Postgres rejected one committed run's upsert.
    #[error("upsert run {run_id} in partition {} into SQL visibility projection: {message}", partition_id.0)]
    RowDatabase {
        /// Partition containing the run.
        partition_id: PartitionId,
        /// Run whose upsert failed.
        run_id: RunId,
        /// The Postgres error; the projector redacts the DSN and password before returning it.
        message: String,
        /// Whether the database failure is transient, classified before its raw Postgres error was dropped.
        is_transient: bool,
    },
    /// Postgres rejected a statement containing multiple committed runs.
    #[error("upsert {row_count} runs from {first_run_id} through {last_run_id} in partition {} into SQL visibility projection: {message}", partition_id.0)]
    BatchDatabase {
        /// Partition containing the runs.
        partition_id: PartitionId,
        /// Number of distinct rows in the failed statement.
        row_count: usize,
        /// First run in the statement's row order.
        first_run_id: RunId,
        /// Last run in the statement's row order.
        last_run_id: RunId,
        /// The Postgres error; the projector redacts the DSN and password before returning it.
        message: String,
        /// Whether the database failure is transient, classified before its raw Postgres error was dropped.
        is_transient: bool,
    },
    /// A pooled connection could not be reopened.
    #[error("reconnect SQL visibility projector: {0}")]
    Connection(#[from] SearchError),
    /// Reading a committed local-store row failed.
    #[error("read committed search state: {0}")]
    StateRead(#[from] StateReadError),
}

impl ProjectionError {
    /// Returns whether reopening a failed connection can recover this projection operation.
    #[must_use]
    pub fn is_retryable(&self) -> bool {
        match self {
            Self::Connection(SearchError::Unavailable(_)) => true,
            Self::Database { is_transient, .. }
            | Self::RowDatabase { is_transient, .. }
            | Self::BatchDatabase { is_transient, .. } => *is_transient,
            Self::StateRead(_) => true,
            _ => false,
        }
    }

    /// Redacts database messages before they leave the projector; [`Self::Connection`] also carries backfill failures.
    fn redact_credentials(mut self, configuration: &ConnectionSettings) -> Self {
        if let Self::Database { message, .. }
        | Self::RowDatabase { message, .. }
        | Self::BatchDatabase { message, .. }
        | Self::Connection(
            SearchError::InvalidArgument(message)
            | SearchError::ResourceExhausted(message)
            | SearchError::Unavailable(message)
            | SearchError::Internal(message),
        ) = &mut self
        {
            *message = configuration.redact_credentials(std::mem::take(message));
        }
        self
    }
}

/// Projects committed partition documents through the search backend's shared connection pool.
#[derive(Clone)]
pub struct SqlVisibilityProjector {
    backend: Arc<SqlVisibilitySearchBackend>,
    metric_recorder: Option<Arc<dyn ProjectionMetricRecorder>>,
}

impl SqlVisibilityProjector {
    /// Shares the backend's `pool_size` connections with SearchFlows requests.
    #[must_use]
    pub fn new(backend: Arc<SqlVisibilitySearchBackend>) -> Self {
        Self {
            backend,
            metric_recorder: None,
        }
    }

    /// Shares backend connections and reports committed poison documents to `metric_recorder`.
    #[must_use]
    pub fn new_with_metric_recorder(
        backend: Arc<SqlVisibilitySearchBackend>,
        metric_recorder: Arc<dyn ProjectionMetricRecorder>,
    ) -> Self {
        Self {
            backend,
            metric_recorder: Some(metric_recorder),
        }
    }

    /// Refreshes and backfills registered slots, including when no document writes arrive.
    pub async fn synchronize_attribute_slots(&self) -> Result<(), ProjectionError> {
        self.backend
            .backfill_registered_attributes(self.metric_recorder.as_deref())
            .await
            .map_err(ProjectionError::from)
            .map_err(|error| error.redact_credentials(self.backend.configuration()))
    }

    /// Returns the recorded progress of every partition that SQL visibility has a row for.
    pub async fn read_all_progress(
        &self,
    ) -> Result<BTreeMap<PartitionId, ProjectionProgress>, ProjectionError> {
        let mut has_retried = false;
        loop {
            let mut slot = self.backend.lock_next_slot().await;
            if slot.is_disconnected() {
                slot.reconnect(self.backend.configuration()).await?;
            }
            let result =
                tokio::time::timeout(DATABASE_OPERATION_TIMEOUT, read_progress_rows(&slot.client))
                    .await;
            let result = match result {
                Ok(result) => result,
                Err(_) => {
                    slot.invalidate();
                    Err(operation_timeout("read progress from"))
                }
            };
            if should_retry(&result, has_retried) {
                has_retried = true;
                slot.reconnect(self.backend.configuration()).await?;
                continue;
            }
            return result.map_err(|error| error.redact_credentials(self.backend.configuration()));
        }
    }

    /// Returns one partition's recorded progress, or `None` when SQL visibility has no row for it.
    pub async fn read_progress(
        &self,
        partition_id: PartitionId,
    ) -> Result<Option<ProjectionProgress>, ProjectionError> {
        Ok(self.read_all_progress().await?.remove(&partition_id))
    }

    /// Writes `batches` and raises their progress in one transaction fenced by `epoch`.
    ///
    /// Partitions must be distinct. Every touched progress row records `epoch`. A row the database
    /// rejects is retried with each row isolated, then recorded as a skipped document.
    ///
    /// Returns [`ProjectionError::Fenced`] when a partition's progress carries a newer epoch.
    pub async fn apply_outbox_batch(
        &self,
        epoch: u64,
        batches: &[PartitionProjectionBatch],
    ) -> Result<(), ProjectionError> {
        validate_batches(batches)?;
        let mut has_retried = false;
        let mut row_isolation = RowIsolation::Batched;
        loop {
            let mut slot = self.backend.lock_next_slot().await;
            if slot.is_disconnected() {
                slot.reconnect(self.backend.configuration()).await?;
            }
            let result = tokio::time::timeout(
                DATABASE_OPERATION_TIMEOUT,
                outbox_transaction(
                    &mut slot.client,
                    epoch,
                    batches,
                    row_isolation,
                    &self.backend,
                ),
            )
            .await;
            let result = match result {
                Ok(result) => result,
                Err(_) => {
                    slot.invalidate();
                    Err(operation_timeout("apply outbox batch to"))
                }
            };
            if row_isolation == RowIsolation::Batched && is_row_rejection(&result) {
                row_isolation = RowIsolation::PerRow;
                continue;
            }
            if should_retry(&result, has_retried) {
                has_retried = true;
                slot.reconnect(self.backend.configuration()).await?;
                continue;
            }
            drop(slot);
            return result
                .map(|skipped| record_skipped_documents(skipped, self.metric_recorder.as_deref()))
                .map_err(|error| error.redact_credentials(self.backend.configuration()));
        }
    }

    /// Projects `changes` of one partition and records `position` with node epoch 1.
    #[cfg(test)]
    pub(crate) async fn apply_changes_for_test(
        &self,
        partition_id: PartitionId,
        position: &LogPosition,
        changes: &[SearchDocumentChange],
    ) -> Result<(), ProjectionError> {
        self.apply_outbox_batch(
            1,
            &[PartitionProjectionBatch {
                partition_id,
                is_initial_load: false,
                changes: changes.to_vec(),
                outbox_through_seq: position.seq,
                applied_position: Some(position.clone()),
            }],
        )
        .await
    }
}

/// How a transaction writes upserts; isolating rows lets one rejected row skip alone.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum RowIsolation {
    Batched,
    PerRow,
}

fn validate_batches(batches: &[PartitionProjectionBatch]) -> Result<(), ProjectionError> {
    let mut partitions = std::collections::BTreeSet::new();
    for batch in batches {
        if !partitions.insert(batch.partition_id) {
            return Err(ProjectionError::InvalidState(format!(
                "outbox batch repeats partition {}",
                batch.partition_id.0
            )));
        }
        if let Some(position) = &batch.applied_position {
            validate_position(position)?;
        }
    }
    Ok(())
}

async fn outbox_transaction(
    client: &mut tokio_postgres::Client,
    epoch: u64,
    batches: &[PartitionProjectionBatch],
    row_isolation: RowIsolation,
    backend: &SqlVisibilitySearchBackend,
) -> Result<Vec<SkippedSearchDocument>, ProjectionError> {
    let transaction = client
        .transaction()
        .await
        .map_err(|source| database_error("begin", source))?;
    let assignments = prepare_attribute_slots(&transaction, backend).await?;
    let mut partition_ids: Vec<PartitionId> =
        batches.iter().map(|batch| batch.partition_id).collect();
    partition_ids.sort_unstable();
    lock_progress(&transaction, &partition_ids, epoch).await?;
    let initial_load_partitions: Vec<i32> = batches
        .iter()
        .filter(|batch| batch.is_initial_load)
        .map(|batch| i32::from(batch.partition_id.0))
        .collect();
    if !initial_load_partitions.is_empty() {
        delete_partition_rows(&transaction, &initial_load_partitions).await?;
    }
    let changes: Vec<(PartitionId, &SearchDocumentChange)> = batches
        .iter()
        .flat_map(|batch| {
            batch
                .changes
                .iter()
                .map(move |change| (batch.partition_id, change))
        })
        .collect();
    let skipped_documents = apply_changes(
        &transaction,
        &changes,
        assignments
            .as_ref()
            .map(|snapshot| snapshot.assignments.as_slice()),
        row_isolation,
    )
    .await?;
    update_progress(&transaction, epoch, batches).await?;
    transaction
        .commit()
        .await
        .map_err(|source| database_error("commit", source))?;
    Ok(skipped_documents)
}

/// Removes rows left by an unfinished initial load; a partition without progress owns none.
async fn delete_partition_rows(
    transaction: &Transaction<'_>,
    partitions: &[i32],
) -> Result<(), ProjectionError> {
    transaction
        .execute_typed(
            "DELETE FROM dexzd_visibility_runs WHERE partition_id = ANY($1::integer[])",
            &[(&partitions as &(dyn ToSql + Sync), Type::INT4_ARRAY)],
        )
        .await
        .map_err(|source| database_error("remove unloaded rows from", source))?;
    transaction
        .execute_typed(
            "DELETE FROM dexzd_visibility_skipped_documents WHERE partition_id = ANY($1::integer[])",
            &[(&partitions as &(dyn ToSql + Sync), Type::INT4_ARRAY)],
        )
        .await
        .map_err(|source| database_error("remove unloaded skipped documents from", source))?;
    Ok(())
}

async fn prepare_attribute_slots(
    transaction: &Transaction<'_>,
    backend: &SqlVisibilitySearchBackend,
) -> Result<Option<Arc<super::super::slots::AttributeSlotSnapshot>>, ProjectionError> {
    if backend.dialect() != VisibilityDialect::Postgres {
        return Ok(None);
    }
    Ok(Some(backend.attribute_slots_for_write(transaction).await?))
}

async fn apply_changes(
    transaction: &Transaction<'_>,
    changes: &[(PartitionId, &SearchDocumentChange)],
    assignments: Option<&[AttributeSlot]>,
    row_isolation: RowIsolation,
) -> Result<Vec<SkippedSearchDocument>, ProjectionError> {
    let mut pending_rows = Vec::new();
    let mut skipped_documents = Vec::new();
    for (partition_id, change) in changes {
        match change {
            SearchDocumentChange::Upsert {
                tenant_id,
                run_id,
                encoded_document,
            } => match row_from_document_for_dialect(
                *partition_id,
                *tenant_id,
                *run_id,
                encoded_document,
                assignments,
            ) {
                Ok(row) => {
                    pending_rows.push(row);
                }
                Err(reason) => {
                    flush_upserts(
                        transaction,
                        &mut pending_rows,
                        assignments,
                        row_isolation,
                        &mut skipped_documents,
                    )
                    .await?;
                    skip_document(
                        transaction,
                        SkippedSearchDocument {
                            partition_id: *partition_id,
                            tenant_id: *tenant_id,
                            run_id: *run_id,
                            reason: reason.to_string(),
                        },
                        &mut skipped_documents,
                    )
                    .await?;
                }
            },
            SearchDocumentChange::Delete { tenant_id, run_id } => {
                flush_upserts(
                    transaction,
                    &mut pending_rows,
                    assignments,
                    row_isolation,
                    &mut skipped_documents,
                )
                .await?;
                delete_search_row(transaction, *partition_id, *tenant_id, *run_id).await?;
                delete_skipped_document(transaction, *tenant_id, *run_id).await?;
            }
        }
    }
    flush_upserts(
        transaction,
        &mut pending_rows,
        assignments,
        row_isolation,
        &mut skipped_documents,
    )
    .await?;
    Ok(skipped_documents)
}

async fn flush_upserts(
    transaction: &Transaction<'_>,
    pending_rows: &mut Vec<SearchRow>,
    assignments: Option<&[AttributeSlot]>,
    row_isolation: RowIsolation,
    skipped_documents: &mut Vec<SkippedSearchDocument>,
) -> Result<(), ProjectionError> {
    match row_isolation {
        RowIsolation::Batched => {
            let statements = upsert_statements_with_slots(pending_rows, assignments)
                .map_err(|error| ProjectionError::InvalidState(error.to_string()))?;
            for chunk in statements {
                transaction
                    .execute_typed(
                        &chunk.statement.sql,
                        &chunk.statement.typed_parameter_refs(),
                    )
                    .await
                    .map_err(|source| upsert_database_error(&chunk.rows, source))?;
                delete_skipped_documents(transaction, &chunk.rows).await?;
            }
        }
        RowIsolation::PerRow => {
            for row in pending_rows.iter() {
                upsert_isolated_row(transaction, row, assignments, skipped_documents).await?;
            }
        }
    }
    pending_rows.clear();
    Ok(())
}

/// Upserts one row inside a savepoint; a non-transient rejection skips only this row.
async fn upsert_isolated_row(
    transaction: &Transaction<'_>,
    row: &SearchRow,
    assignments: Option<&[AttributeSlot]>,
    skipped_documents: &mut Vec<SkippedSearchDocument>,
) -> Result<(), ProjectionError> {
    let statements = upsert_statements_with_slots(std::slice::from_ref(row), assignments)
        .map_err(|error| ProjectionError::InvalidState(error.to_string()))?;
    transaction
        .batch_execute("SAVEPOINT dexzd_projection_row")
        .await
        .map_err(|source| database_error("isolate a row in", source))?;
    let mut rejection = None;
    for chunk in &statements {
        if let Err(source) = transaction
            .execute_typed(
                &chunk.statement.sql,
                &chunk.statement.typed_parameter_refs(),
            )
            .await
        {
            rejection = Some(source);
            break;
        }
    }
    let Some(source) = rejection else {
        transaction
            .batch_execute("RELEASE SAVEPOINT dexzd_projection_row")
            .await
            .map_err(|source| database_error("release an isolated row in", source))?;
        return delete_skipped_documents(transaction, &[row]).await;
    };
    let (partition_id, tenant_id, run_id) = row_identity(row);
    if is_transient_database_error(&source) {
        return Err(row_database_error(partition_id, run_id, source));
    }
    transaction
        .batch_execute("ROLLBACK TO SAVEPOINT dexzd_projection_row")
        .await
        .map_err(|source| database_error("roll back an isolated row in", source))?;
    skip_document(
        transaction,
        SkippedSearchDocument {
            partition_id,
            tenant_id,
            run_id,
            reason: database_error_message(&source),
        },
        skipped_documents,
    )
    .await
}

/// Removes the run's row and records why its committed document is not searchable.
async fn skip_document(
    transaction: &Transaction<'_>,
    skipped: SkippedSearchDocument,
    skipped_documents: &mut Vec<SkippedSearchDocument>,
) -> Result<(), ProjectionError> {
    delete_search_row(
        transaction,
        skipped.partition_id,
        skipped.tenant_id,
        skipped.run_id,
    )
    .await?;
    record_skipped_document(transaction, &skipped).await?;
    skipped_documents.push(skipped);
    Ok(())
}

/// Projected rows keep the identifiers they were built from.
fn row_identity(row: &SearchRow) -> (PartitionId, TenantId, RunId) {
    (
        PartitionId(
            u16::try_from(row.partition_id).expect("projected rows keep a valid partition ID"),
        ),
        TenantId(
            row.tenant_id
                .parse()
                .expect("projected rows keep canonical tenant IDs"),
        ),
        row.run_id
            .parse()
            .expect("projected rows preserve canonical run identifiers"),
    )
}

async fn delete_skipped_documents(
    transaction: &Transaction<'_>,
    rows: &[&SearchRow],
) -> Result<(), ProjectionError> {
    let tenant_columns = rows
        .iter()
        .map(|row| row.tenant_id.as_str())
        .collect::<Vec<_>>();
    let run_columns = rows
        .iter()
        .map(|row| row.run_id.as_str())
        .collect::<Vec<_>>();
    transaction.execute_typed(
        "DELETE FROM dexzd_visibility_skipped_documents WHERE (tenant_id, run_id) IN (SELECT * FROM unnest($1::text[], $2::text[]))",
        &[
            (&tenant_columns as &(dyn ToSql + Sync), Type::TEXT_ARRAY),
            (&run_columns, Type::TEXT_ARRAY),
        ],
    ).await.map_err(|source| database_error("clear skipped documents from", source))?;
    Ok(())
}

async fn record_skipped_document(
    transaction: &Transaction<'_>,
    skipped: &SkippedSearchDocument,
) -> Result<(), ProjectionError> {
    let tenant_column = super::super::tenant_column_value(skipped.tenant_id);
    let partition_number = i32::from(skipped.partition_id.0);
    let run_column = skipped.run_id.to_string();
    transaction
        .execute_typed(
            "INSERT INTO dexzd_visibility_skipped_documents (tenant_id, partition_id, run_id, reason) VALUES ($1::text, $2::integer, $3::text, $4::text) ON CONFLICT (tenant_id, run_id) DO UPDATE SET partition_id = EXCLUDED.partition_id, reason = EXCLUDED.reason",
            &[
                (&tenant_column as &(dyn ToSql + Sync), Type::TEXT),
                (&partition_number, Type::INT4),
                (&run_column, Type::TEXT),
                (&skipped.reason, Type::TEXT),
            ],
        )
        .await
        .map_err(|source| database_error("record skipped document in", source))?;
    Ok(())
}

async fn delete_skipped_document(
    transaction: &Transaction<'_>,
    tenant_id: TenantId,
    run_id: RunId,
) -> Result<(), ProjectionError> {
    let tenant_column = super::super::tenant_column_value(tenant_id);
    let run_column = run_id.to_string();
    transaction
        .execute_typed(
            "DELETE FROM dexzd_visibility_skipped_documents WHERE tenant_id = $1::text AND run_id = $2::text",
            &[
                (&tenant_column as &(dyn ToSql + Sync), Type::TEXT),
                (&run_column, Type::TEXT),
            ],
        )
        .await
        .map_err(|source| database_error("clear skipped document from", source))?;
    Ok(())
}

async fn delete_search_row(
    transaction: &Transaction<'_>,
    partition_id: PartitionId,
    tenant_id: TenantId,
    run_id: RunId,
) -> Result<(), ProjectionError> {
    let tenant_column = super::super::tenant_column_value(tenant_id);
    let partition_number = i32::from(partition_id.0);
    let run_column = run_id.to_string();
    transaction
        .execute_typed(
            "DELETE FROM dexzd_visibility_runs WHERE tenant_id = $1::text AND partition_id = $2::integer AND run_id = $3::text",
            &[
                (&tenant_column as &(dyn ToSql + Sync), Type::TEXT),
                (&partition_number, Type::INT4),
                (&run_column, Type::TEXT),
            ],
        )
        .await
        .map_err(|source| database_error("delete run from", source))?;
    Ok(())
}

#[cfg(test)]
fn row_from_document(
    partition_id: PartitionId,
    tenant_id: TenantId,
    run_id: RunId,
    encoded_document: &[u8],
) -> Result<SearchRow, ProjectionError> {
    row_from_document_for_dialect(partition_id, tenant_id, run_id, encoded_document, None)
}

fn row_from_document_for_dialect(
    partition_id: PartitionId,
    tenant_id: TenantId,
    run_id: RunId,
    encoded_document: &[u8],
    assignments: Option<&[AttributeSlot]>,
) -> Result<SearchRow, ProjectionError> {
    let record = SearchDocumentRecord::decode(encoded_document).map_err(|error| {
        ProjectionError::InvalidState(format!("decode committed search document: {error}"))
    })?;
    if let Some(assignments) = assignments {
        for attribute in &record.attributes {
            if is_system_search_field(&attribute.index_key) {
                continue;
            }
            let registered_type = assignments
                .iter()
                .find(|assignment| {
                    assignment.tenant_id == tenant_id.0
                        && assignment.index_key == attribute.index_key
                })
                .map(|assignment| assignment.index_type as i32);
            if registered_type != Some(attribute.index_type) {
                continue;
            }
            if let (Some(value), Ok(index_type)) =
                (&attribute.value, IndexType::try_from(attribute.index_type))
                && let Some(strings) = indexed_keyword_strings(value, index_type)
                && let Some(value) = strings.iter().find(|value| value.len() > 1024)
            {
                return Err(ProjectionError::InvalidState(format!(
                    "indexed Keyword {:?} has {} bytes; PostgreSQL maximum is 1024 bytes",
                    attribute.index_key,
                    value.len()
                )));
            }
        }
    }
    let version = i64::try_from(record.version).map_err(|_| {
        ProjectionError::InvalidState("search document version exceeds bigint".into())
    })?;
    // System attributes, such as DexWorkQueuePermissions, are projected into dedicated columns.
    let application_fields = record
        .attributes
        .iter()
        .filter(|attribute| !is_system_search_field(&attribute.index_key))
        .map(|attribute| {
            IndexType::try_from(attribute.index_type)
                .map(|index_type| (attribute.index_key.clone(), index_type))
                .map_err(|_| {
                    ProjectionError::InvalidState(format!(
                        "search attribute {} has unknown index type",
                        attribute.index_key
                    ))
                })
        })
        .collect::<Result<Vec<_>, _>>()?;
    let document = IndexedDocument::from_record(record.clone(), tenant_id, run_id)
        .map_err(|error| ProjectionError::InvalidState(error.to_string()))?;
    let mut row = SearchRow::from_document(tenant_id, version, &document, &application_fields)
        .and_then(|row| row.with_wire_attributes(&record.attributes))
        .map_err(|error| ProjectionError::InvalidState(error.to_string()))?;
    row.partition_id = i32::from(partition_id.0);
    Ok(row)
}

/// Keeps only the message and classification, so no raw Postgres text survives redaction.
pub(super) fn database_error(
    operation: &'static str,
    source: tokio_postgres::Error,
) -> ProjectionError {
    ProjectionError::Database {
        operation,
        message: database_error_message(&source),
        is_transient: is_transient_database_error(&source),
    }
}

fn upsert_database_error(rows: &[&SearchRow], source: tokio_postgres::Error) -> ProjectionError {
    let (partition_id, _, first_run_id) = row_identity(
        rows.first()
            .expect("upsert chunks contain at least one row"),
    );
    if rows.len() == 1 {
        return row_database_error(partition_id, first_run_id, source);
    }
    let (_, _, last_run_id) =
        row_identity(rows.last().expect("upsert chunks contain at least one row"));
    ProjectionError::BatchDatabase {
        partition_id,
        row_count: rows.len(),
        first_run_id,
        last_run_id,
        message: database_error_message(&source),
        is_transient: is_transient_database_error(&source),
    }
}

fn row_database_error(
    partition_id: PartitionId,
    run_id: RunId,
    source: tokio_postgres::Error,
) -> ProjectionError {
    ProjectionError::RowDatabase {
        partition_id,
        run_id,
        message: database_error_message(&source),
        is_transient: is_transient_database_error(&source),
    }
}

fn operation_timeout(operation: &'static str) -> ProjectionError {
    ProjectionError::Connection(SearchError::Unavailable(format!(
        "{operation} SQL visibility projection timed out after {DATABASE_OPERATION_TIMEOUT:?}"
    )))
}

fn should_retry(result: &Result<impl Sized, ProjectionError>, has_retried: bool) -> bool {
    !has_retried
        && matches!(
            result,
            Err(ProjectionError::Connection(SearchError::Unavailable(_))
                | ProjectionError::Database {
                    is_transient: true,
                    ..
                }
                | ProjectionError::RowDatabase {
                    is_transient: true,
                    ..
                }
                | ProjectionError::BatchDatabase {
                    is_transient: true,
                    ..
                })
        )
}

/// Whether the database rejected a row's content rather than failing transiently.
fn is_row_rejection(result: &Result<impl Sized, ProjectionError>) -> bool {
    matches!(
        result,
        Err(ProjectionError::RowDatabase {
            is_transient: false,
            ..
        } | ProjectionError::BatchDatabase {
            is_transient: false,
            ..
        })
    )
}

#[cfg(test)]
#[path = "projector_tests.rs"]
mod tests;
