// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeMap;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

use dexzd_apply::{FlowStateReader, StateReadError, StateRow};
use dexzd_keyspace::row_keys::{run_singleton_key, tenant_run_index_key};
use dexzd_keyspace::{ColumnFamily, FlowHash, IndexKind, PartitionId, RunId, RunTable, TenantId};
use dexzd_protocol::dex::FlowStatus;
use dexzd_protocol::dexzd::v1::{LogPosition, RunRecord, SearchDocumentRecord};
use dexzd_visibility_query::FieldRegistry;
use prost::Message;
use tokio_postgres::{Client, Config};

use super::super::integration::TestAttributeRegistry;
use super::super::{SqlVisibilitySearchBackend, VisibilityDialect};
use super::{
    PartitionProjectionBatch, ProjectionError, ProjectionMetricRecorder, ProjectionProgress,
    SearchDocumentChange, SqlVisibilityProjector, read_initial_load_page,
};
use crate::{SearchBackend, SearchError, SearchRequest};
use dexzd_postgres_connection::ConnectionSettings;

#[derive(Default)]
struct CommittedDocuments(BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>>);

impl CommittedDocuments {
    fn insert(
        &mut self,
        partition_id: PartitionId,
        tenant_id: TenantId,
        flow_hash: FlowHash,
        run_id: RunId,
        record: &SearchDocumentRecord,
    ) {
        self.0.insert(
            (
                ColumnFamily::PartitionIndex,
                tenant_run_index_key(
                    partition_id,
                    IndexKind::SearchDocument,
                    tenant_id,
                    flow_hash,
                    run_id,
                ),
            ),
            record.version.to_be_bytes().to_vec(),
        );
        self.0.insert(
            (
                ColumnFamily::Flow,
                run_singleton_key(flow_hash, run_id, RunTable::SearchDocument),
            ),
            record.encode_to_vec(),
        );
        self.0.insert(
            (
                ColumnFamily::Flow,
                run_singleton_key(flow_hash, run_id, RunTable::RunRecord),
            ),
            RunRecord {
                tenant_id: tenant_id.0,
                flow_id: record.flow_id.clone(),
                run_id: run_id.as_bytes().to_vec(),
                flow_type: record.flow_type.clone(),
                status: record.status,
                ..RunRecord::default()
            }
            .encode_to_vec(),
        );
    }
}

impl FlowStateReader for CommittedDocuments {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        Ok(self.0.get(&(column_family, key.to_vec())).cloned())
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        Ok(self
            .0
            .range((column_family, start.to_vec())..(column_family, end.to_vec()))
            .take(limit)
            .map(|((_, key), value)| (key.clone(), value.clone()))
            .collect())
    }
}

struct FailingDocuments {
    documents: CommittedDocuments,
    reads: AtomicUsize,
}

struct CountingProjectionMetrics {
    skipped_documents: AtomicUsize,
    partition_id: PartitionId,
    tenant_id: TenantId,
}

impl CountingProjectionMetrics {
    fn new(partition_id: PartitionId, tenant_id: TenantId) -> Self {
        Self {
            skipped_documents: AtomicUsize::new(0),
            partition_id,
            tenant_id,
        }
    }
}

impl ProjectionMetricRecorder for CountingProjectionMetrics {
    fn record_skipped_search_document(
        &self,
        partition_id: PartitionId,
        tenant_id: TenantId,
        _run_id: RunId,
    ) {
        assert_eq!(partition_id, self.partition_id);
        assert_eq!(tenant_id, self.tenant_id);
        self.skipped_documents.fetch_add(1, Ordering::Relaxed);
    }
}

impl FlowStateReader for FailingDocuments {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        if self.reads.fetch_add(1, Ordering::Relaxed) == 1 {
            return Err(StateReadError {
                column_family,
                detail: "injected committed-view read failure".into(),
            });
        }
        self.documents.get(column_family, key)
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        self.documents.scan(column_family, start, end, limit)
    }
}

fn document(tenant_id: TenantId, run_id: RunId, version: u64) -> SearchDocumentRecord {
    SearchDocumentRecord {
        version,
        tenant_id: tenant_id.0,
        flow_id: format!("flow-{run_id}"),
        run_id: run_id.as_bytes().to_vec(),
        flow_type: "ProjectorFlow".into(),
        status: FlowStatus::Running as i32,
        started_at_micros: 1_000_000,
        ..Default::default()
    }
}

fn upsert(
    tenant_id: TenantId,
    run_id: RunId,
    record: &SearchDocumentRecord,
) -> SearchDocumentChange {
    SearchDocumentChange::Upsert {
        tenant_id,
        run_id,
        encoded_document: record.encode_to_vec(),
    }
}

fn position(sequence: u64) -> LogPosition {
    LogPosition {
        node_id: "projector-node".into(),
        incarnation: 1,
        seq: sequence,
    }
}

fn assert_send<T: Send>(_: T) {}

const EPOCH: u64 = 5;

fn batch(
    partition_id: PartitionId,
    changes: Vec<SearchDocumentChange>,
    outbox_through_seq: u64,
    applied_position: Option<LogPosition>,
) -> PartitionProjectionBatch {
    PartitionProjectionBatch {
        partition_id,
        is_initial_load: false,
        changes,
        outbox_through_seq,
        applied_position,
    }
}

fn progress(
    applied_position: Option<LogPosition>,
    applied_epoch: u64,
    outbox_through_seq: u64,
) -> Option<ProjectionProgress> {
    Some(ProjectionProgress {
        applied_position,
        applied_epoch: Some(applied_epoch),
        outbox_through_seq,
    })
}

async fn stored_version(
    backend: &SqlVisibilitySearchBackend,
    tenant_id: TenantId,
    run_id: RunId,
) -> i64 {
    let slot = backend.lock_next_slot().await;
    slot.client
        .query_typed_one(
            "SELECT version FROM dexzd_visibility_runs WHERE tenant_id = $1::text AND run_id = $2::text",
            &[
                (
                    &tenant_id.0.to_string() as &(dyn tokio_postgres::types::ToSql + Sync),
                    tokio_postgres::types::Type::TEXT,
                ),
                (&run_id.to_string(), tokio_postgres::types::Type::TEXT),
            ],
        )
        .await
        .expect("read projected version")
        .get(0)
}

async fn listed_runs(backend: &SqlVisibilitySearchBackend, tenant_id: TenantId) -> Vec<String> {
    backend
        .search(SearchRequest {
            tenant_id,
            query: "",
            page_size: 20,
            page_token: None,
            field_registry: &FieldRegistry::new(),
        })
        .await
        .expect("search projected documents")
        .entries
        .into_iter()
        .map(|entry| entry.run_id)
        .collect()
}

async fn skipped_reason(
    backend: &SqlVisibilitySearchBackend,
    tenant_id: TenantId,
    run_id: RunId,
) -> Option<String> {
    let slot = backend.lock_next_slot().await;
    let tenant_column = tenant_id.0.to_string();
    let run_column = run_id.to_string();
    slot.client
        .query_typed_opt(
            "SELECT reason FROM dexzd_visibility_skipped_documents WHERE tenant_id = $1::text AND run_id = $2::text",
            &[
                (&tenant_column as &(dyn tokio_postgres::types::ToSql + Sync), tokio_postgres::types::Type::TEXT),
                (&run_column, tokio_postgres::types::Type::TEXT),
            ],
        )
        .await
        .expect("read skipped document")
        .map(|row| row.get(0))
}

async fn verify_ordered_batch_changes(backend: &Arc<SqlVisibilitySearchBackend>) {
    let partition_id = PartitionId(6);
    let tenant_id = TenantId(19);
    let metrics = Arc::new(CountingProjectionMetrics::new(partition_id, tenant_id));
    let projector =
        SqlVisibilityProjector::new_with_metric_recorder(backend.clone(), metrics.clone());
    let run_id = RunId([9; 16]);
    let newest = document(tenant_id, run_id, 10);
    let older = document(tenant_id, run_id, 1);
    let deleted = SearchDocumentChange::Delete { tenant_id, run_id };
    let invalid = SearchDocumentChange::Upsert {
        tenant_id,
        run_id,
        encoded_document: vec![0xff],
    };
    for (sequence, changes, should_exist, should_be_skipped, skipped_count) in [
        (
            1,
            vec![
                upsert(tenant_id, run_id, &newest),
                deleted.clone(),
                upsert(tenant_id, run_id, &older),
            ],
            true,
            false,
            0,
        ),
        (
            2,
            vec![
                upsert(tenant_id, run_id, &newest),
                invalid.clone(),
                upsert(tenant_id, run_id, &older),
            ],
            true,
            false,
            1,
        ),
        (
            3,
            vec![upsert(tenant_id, run_id, &newest), invalid],
            false,
            true,
            2,
        ),
        (
            4,
            vec![upsert(tenant_id, run_id, &older), deleted],
            false,
            false,
            2,
        ),
    ] {
        projector
            .apply_outbox_batch(
                EPOCH,
                &[batch(
                    partition_id,
                    changes,
                    sequence,
                    Some(position(sequence)),
                )],
            )
            .await
            .expect("apply ordered changes within one batch");
        assert_eq!(
            !listed_runs(backend, tenant_id).await.is_empty(),
            should_exist
        );
        assert_eq!(
            skipped_reason(backend, tenant_id, run_id).await.is_some(),
            should_be_skipped
        );
        if should_exist {
            let slot = backend.lock_next_slot().await;
            let stored = slot.client.query_typed_one(
                "SELECT version FROM dexzd_visibility_runs WHERE tenant_id = $1::text AND run_id = $2::text",
                &[
                    (&tenant_id.0.to_string() as &(dyn tokio_postgres::types::ToSql + Sync), tokio_postgres::types::Type::TEXT),
                    (&run_id.to_string(), tokio_postgres::types::Type::TEXT),
                ],
            ).await.expect("read version after an ordering barrier");
            assert_eq!(stored.get::<_, i64>(0), 1);
        }
        assert_eq!(
            metrics.skipped_documents.load(Ordering::Relaxed),
            skipped_count
        );
        assert_eq!(
            projector.read_progress(partition_id).await.unwrap(),
            progress(Some(position(sequence)), EPOCH, sequence)
        );
    }
}

#[cfg(test)]
pub(crate) async fn run_projector_scenarios_for_test(
    database_url: &str,
    admin_client: &Client,
    nonce: u128,
    dialect: VisibilityDialect,
) {
    for pool_size in [1, 2] {
        let schema_name = format!(
            "dexzd_projector_{}_{}_{}",
            std::process::id(),
            nonce,
            pool_size
        );
        admin_client
            .batch_execute(&format!("CREATE SCHEMA {schema_name}"))
            .await
            .expect("create empty projector schema");
        let mut configuration: Config = database_url.parse().expect("parse CI SQL visibility URL");
        configuration.options(format!("-c search_path={schema_name},public,pdb"));
        let backend = Arc::new(
            SqlVisibilitySearchBackend::connect_dialect_with_settings(
                &ConnectionSettings::from_config(configuration.clone()),
                pool_size,
                None,
                Duration::from_secs(2),
                dialect,
                Some(Arc::new(TestAttributeRegistry::default())),
            )
            .await
            .expect("create schema on empty database"),
        );
        let metrics = Arc::new(CountingProjectionMetrics::new(PartitionId(4), TenantId(17)));
        let projector = Arc::new(SqlVisibilityProjector::new_with_metric_recorder(
            backend.clone(),
            metrics.clone(),
        ));
        let schema_row = {
            let slot = backend.lock_next_slot().await;
            slot.client
                .query_typed_one(
                    "SELECT version FROM dexzd_visibility_schema_version WHERE id = 1",
                    &[],
                )
                .await
                .expect("read created schema version")
        };
        assert_eq!(
            schema_row.get::<_, i32>(0),
            if dialect == VisibilityDialect::ParadeDb {
                3
            } else {
                4
            }
        );
        let other_dialect = if dialect == VisibilityDialect::ParadeDb {
            VisibilityDialect::Postgres
        } else {
            VisibilityDialect::ParadeDb
        };
        let mismatched = SqlVisibilitySearchBackend::connect_dialect_with_settings(
            &ConnectionSettings::from_config(configuration.clone()),
            1,
            None,
            Duration::from_secs(2),
            other_dialect,
            Some(Arc::new(TestAttributeRegistry::default())),
        )
        .await;
        assert!(
            matches!(mismatched, Err(SearchError::Internal(message)) if message.contains("dialect") && message.contains("does not match"))
        );

        assert_eq!(projector.read_progress(PartitionId(4)).await.unwrap(), None);
        let mut locked_slot = backend.lock_next_slot().await;
        let table_lock = locked_slot
            .client
            .transaction()
            .await
            .expect("begin visibility table lock");
        table_lock
            .batch_execute("LOCK TABLE dexzd_visibility_runs IN ROW EXCLUSIVE MODE")
            .await
            .expect("hold visibility writer lock");
        let repeated_backend = tokio::time::timeout(
            Duration::from_secs(3),
            SqlVisibilitySearchBackend::connect_dialect_with_settings(
                &ConnectionSettings::from_config(configuration.clone()),
                1,
                None,
                Duration::from_secs(2),
                dialect,
                Some(Arc::new(TestAttributeRegistry::default())),
            ),
        )
        .await
        .expect("current schema setup does not wait on visibility table")
        .expect("ensure existing schema idempotently");
        table_lock
            .rollback()
            .await
            .expect("release visibility table lock");
        drop(locked_slot);
        drop(repeated_backend);

        let partition_id = PartitionId(4);
        let other_partition_id = PartitionId(5);
        let tenant_id = TenantId(17);
        let first_run = RunId([1; 16]);
        let second_run = RunId([2; 16]);
        let loaded_run = RunId([3; 16]);
        let other_run = RunId([4; 16]);
        let first_record = document(tenant_id, first_run, 1);
        projector
            .apply_outbox_batch(
                EPOCH,
                &[batch(
                    partition_id,
                    vec![upsert(tenant_id, first_run, &first_record)],
                    100,
                    Some(position(10)),
                )],
            )
            .await
            .expect("apply the first outbox batch");
        assert_eq!(
            projector.read_progress(partition_id).await.unwrap(),
            progress(Some(position(10)), EPOCH, 100)
        );
        assert_eq!(
            listed_runs(&backend, tenant_id).await,
            [first_run.to_string()]
        );

        let changed_record = document(tenant_id, first_run, 2);
        projector
            .apply_outbox_batch(
                EPOCH,
                &[batch(
                    partition_id,
                    vec![upsert(tenant_id, first_run, &changed_record)],
                    90,
                    None,
                )],
            )
            .await
            .expect("apply a newer document with a lower outbox seq");
        assert_eq!(stored_version(&backend, tenant_id, first_run).await, 2);
        assert_eq!(
            projector.read_progress(partition_id).await.unwrap(),
            progress(Some(position(10)), EPOCH, 100),
            "outbox progress never decreases and a batch without a position keeps it"
        );
        projector
            .apply_outbox_batch(
                EPOCH,
                &[batch(
                    partition_id,
                    vec![upsert(tenant_id, first_run, &first_record)],
                    101,
                    None,
                )],
            )
            .await
            .expect("an older redelivered document changes nothing");
        assert_eq!(stored_version(&backend, tenant_id, first_run).await, 2);

        // A newer lease holder's epoch fences every older writer of the partition.
        projector
            .apply_outbox_batch(EPOCH + 1, &[batch(partition_id, Vec::new(), 101, None)])
            .await
            .expect("a newer epoch takes over");
        let fenced = projector
            .apply_outbox_batch(
                EPOCH,
                &[
                    batch(partition_id, Vec::new(), 300, None),
                    batch(
                        other_partition_id,
                        vec![upsert(
                            tenant_id,
                            other_run,
                            &document(tenant_id, other_run, 1),
                        )],
                        300,
                        None,
                    ),
                ],
            )
            .await;
        assert!(
            matches!(fenced, Err(ProjectionError::Fenced { partition_id: fenced_partition, stored_epoch, epoch })
                if fenced_partition == partition_id && stored_epoch == EPOCH + 1 && epoch == EPOCH),
            "{fenced:?}"
        );
        assert!(
            !fenced
                .as_ref()
                .err()
                .is_some_and(ProjectionError::is_retryable)
        );
        assert_eq!(
            projector.read_progress(partition_id).await.unwrap(),
            progress(Some(position(10)), EPOCH + 1, 101)
        );
        assert_eq!(
            projector.read_progress(other_partition_id).await.unwrap(),
            None,
            "a fenced transaction writes no partition"
        );
        let epoch = EPOCH + 1;

        projector
            .apply_outbox_batch(
                epoch,
                &[batch(
                    partition_id,
                    vec![SearchDocumentChange::Upsert {
                        tenant_id,
                        run_id: first_run,
                        encoded_document: vec![0xff],
                    }],
                    102,
                    Some(position(11)),
                )],
            )
            .await
            .expect("skip a poison document and advance progress");
        assert!(listed_runs(&backend, tenant_id).await.is_empty());
        assert!(
            skipped_reason(&backend, tenant_id, first_run)
                .await
                .is_some_and(|reason| reason.contains("decode committed search document"))
        );
        assert_eq!(metrics.skipped_documents.load(Ordering::Relaxed), 1);
        let second_record = document(tenant_id, second_run, 1);
        let other_record = document(tenant_id, other_run, 1);
        projector
            .apply_outbox_batch(
                epoch,
                &[
                    batch(
                        partition_id,
                        vec![
                            upsert(tenant_id, first_run, &changed_record),
                            upsert(tenant_id, second_run, &second_record),
                        ],
                        103,
                        Some(position(13)),
                    ),
                    batch(
                        other_partition_id,
                        vec![upsert(tenant_id, other_run, &other_record)],
                        55,
                        Some(position(13)),
                    ),
                ],
            )
            .await
            .expect("write two partitions in one transaction");
        assert_eq!(skipped_reason(&backend, tenant_id, first_run).await, None);
        assert_eq!(listed_runs(&backend, tenant_id).await.len(), 3);
        assert_eq!(
            projector.read_progress(other_partition_id).await.unwrap(),
            progress(Some(position(13)), epoch, 55)
        );

        projector
            .apply_outbox_batch(
                epoch,
                &[batch(
                    partition_id,
                    vec![SearchDocumentChange::Delete {
                        tenant_id,
                        run_id: first_run,
                    }],
                    104,
                    Some(position(17)),
                )],
            )
            .await
            .expect("delete a purged run");
        let listed = listed_runs(&backend, tenant_id).await;
        assert!(!listed.contains(&first_run.to_string()));
        assert!(listed.contains(&second_run.to_string()));
        assert!(matches!(
            projector
                .apply_outbox_batch(
                    epoch,
                    &[
                        batch(partition_id, Vec::new(), 1, None),
                        batch(partition_id, Vec::new(), 1, None),
                    ],
                )
                .await,
            Err(ProjectionError::InvalidState(_))
        ));

        // An initial load replaces whatever an unfinished load left in its partition only.
        let mut committed_view = CommittedDocuments::default();
        committed_view.insert(
            partition_id,
            tenant_id,
            FlowHash(8),
            loaded_run,
            &document(tenant_id, loaded_run, 1),
        );
        let purging_run = RunId([7; 16]);
        let purging_hash = FlowHash(11);
        committed_view.insert(
            partition_id,
            tenant_id,
            purging_hash,
            purging_run,
            &document(tenant_id, purging_run, 1),
        );
        committed_view.0.insert(
            (
                ColumnFamily::Flow,
                run_singleton_key(purging_hash, purging_run, RunTable::RunRecord),
            ),
            RunRecord {
                tenant_id: tenant_id.0,
                run_id: purging_run.as_bytes().to_vec(),
                is_purging: true,
                ..RunRecord::default()
            }
            .encode_to_vec(),
        );
        let poison_run = RunId([6; 16]);
        committed_view.insert(
            partition_id,
            tenant_id,
            FlowHash(10),
            poison_run,
            &document(TenantId(18), poison_run, 1),
        );
        let failing_view = FailingDocuments {
            documents: CommittedDocuments(committed_view.0.clone()),
            reads: AtomicUsize::new(0),
        };
        assert!(matches!(
            read_initial_load_page(&failing_view, partition_id, None, 10),
            Err(ProjectionError::StateRead(_))
        ));
        let mut load_changes = Vec::new();
        let mut next_start = None;
        while let Some(page) =
            read_initial_load_page(&committed_view, partition_id, next_start.as_deref(), 2)
                .expect("read an initial load page")
        {
            load_changes.extend(page.changes);
            next_start = Some(page.next_start);
        }
        assert_eq!(load_changes.len(), 2, "the purging run is not loaded");
        assert_send(projector.apply_outbox_batch(epoch, &[]));
        projector
            .apply_outbox_batch(
                epoch,
                &[PartitionProjectionBatch {
                    partition_id,
                    is_initial_load: true,
                    changes: load_changes,
                    outbox_through_seq: 200,
                    applied_position: Some(position(19)),
                }],
            )
            .await
            .expect("load a partition");
        assert_eq!(
            projector.read_progress(partition_id).await.unwrap(),
            progress(Some(position(19)), epoch, 200)
        );
        let listed = listed_runs(&backend, tenant_id).await;
        assert_eq!(listed.len(), 2);
        assert!(listed.contains(&loaded_run.to_string()));
        assert!(listed.contains(&other_run.to_string()));
        assert!(!listed.contains(&second_run.to_string()));
        assert!(!listed.contains(&poison_run.to_string()));
        assert!(!listed.contains(&purging_run.to_string()));
        assert!(
            skipped_reason(&backend, tenant_id, poison_run)
                .await
                .is_some()
        );
        assert_eq!(metrics.skipped_documents.load(Ordering::Relaxed), 2);
        projector
            .apply_outbox_batch(
                epoch,
                &[batch(
                    partition_id,
                    vec![SearchDocumentChange::Delete {
                        tenant_id,
                        run_id: poison_run,
                    }],
                    201,
                    Some(position(20)),
                )],
            )
            .await
            .expect("clear a skipped document on purge");
        assert_eq!(skipped_reason(&backend, tenant_id, poison_run).await, None);

        // A row the database rejects is skipped alone after the batch retries row by row.
        admin_client
            .batch_execute(&format!(
                "ALTER TABLE {schema_name}.dexzd_visibility_runs ADD CONSTRAINT dexzd_test_rejects_poison CHECK (workflow_id <> 'rejected-flow')"
            ))
            .await
            .expect("add a constraint that rejects one row");
        let rejected_run = RunId([12; 16]);
        let accepted_runs = [RunId([13; 16]), RunId([14; 16])];
        let mut rejected_record = document(tenant_id, rejected_run, 1);
        rejected_record.flow_id = "rejected-flow".into();
        projector
            .apply_outbox_batch(
                epoch,
                &[batch(
                    partition_id,
                    vec![
                        upsert(
                            tenant_id,
                            accepted_runs[0],
                            &document(tenant_id, accepted_runs[0], 1),
                        ),
                        upsert(tenant_id, rejected_run, &rejected_record),
                        upsert(
                            tenant_id,
                            accepted_runs[1],
                            &document(tenant_id, accepted_runs[1], 1),
                        ),
                    ],
                    202,
                    Some(position(20)),
                )],
            )
            .await
            .expect("isolate the rejected row");
        let listed = listed_runs(&backend, tenant_id).await;
        assert!(
            accepted_runs
                .iter()
                .all(|run| listed.contains(&run.to_string()))
        );
        assert!(!listed.contains(&rejected_run.to_string()));
        assert!(
            skipped_reason(&backend, tenant_id, rejected_run)
                .await
                .is_some_and(|reason| reason.contains("23514")),
            "{:?}",
            skipped_reason(&backend, tenant_id, rejected_run).await
        );
        assert_eq!(
            projector.read_progress(partition_id).await.unwrap(),
            progress(Some(position(20)), epoch, 202)
        );
        assert_eq!(metrics.skipped_documents.load(Ordering::Relaxed), 3);
        for run_id in accepted_runs.into_iter().chain([rejected_run]) {
            projector
                .apply_outbox_batch(
                    epoch,
                    &[batch(
                        partition_id,
                        vec![SearchDocumentChange::Delete { tenant_id, run_id }],
                        203,
                        None,
                    )],
                )
                .await
                .expect("remove isolation test rows");
        }
        admin_client
            .batch_execute(&format!(
                "ALTER TABLE {schema_name}.dexzd_visibility_runs DROP CONSTRAINT dexzd_test_rejects_poison"
            ))
            .await
            .expect("drop the rejecting constraint");

        verify_ordered_batch_changes(&backend).await;

        let mut held_slots = Vec::new();
        for _ in 0..pool_size {
            held_slots.push(backend.lock_next_slot().await);
        }
        let deadline = tokio::time::timeout(
            Duration::from_millis(20),
            backend.search(SearchRequest {
                tenant_id,
                query: "",
                page_size: 1,
                page_token: None,
                field_registry: &FieldRegistry::new(),
            }),
        )
        .await;
        assert!(deadline.is_err());
        if pool_size == 2 {
            let released_slot = held_slots.pop().expect("two slots were acquired");
            let (runs, ()) = tokio::time::timeout(Duration::from_secs(2), async {
                tokio::join!(listed_runs(&backend, tenant_id), async {
                    tokio::task::yield_now().await;
                    drop(released_slot);
                })
            })
            .await
            .expect("queued search takes the first released slot");
            assert_eq!(runs.len(), 2);
            held_slots.push(backend.lock_next_slot().await);
        }
        let search_backend = backend.clone();
        let projection_writer = projector.clone();
        tokio::time::timeout(Duration::from_secs(10), async move {
            let searching = async {
                for _ in 0..20 {
                    let runs = listed_runs(&search_backend, tenant_id).await;
                    assert_eq!(runs.len(), 2);
                }
            };
            let projecting = async {
                for sequence in 21..=39 {
                    projection_writer
                        .apply_outbox_batch(
                            epoch,
                            &[batch(
                                partition_id,
                                Vec::new(),
                                sequence + 200,
                                Some(position(sequence)),
                            )],
                        )
                        .await
                        .expect("queue projection behind busy pool");
                }
            };
            let release = async {
                tokio::task::yield_now().await;
                drop(held_slots);
            };
            tokio::join!(searching, projecting, release);
        })
        .await
        .expect("search and projection complete with a small shared pool");
        assert_eq!(
            projector.read_progress(partition_id).await.unwrap(),
            progress(Some(position(39)), epoch, 239)
        );
        if pool_size == 2 {
            let first_batches = [batch(partition_id, Vec::new(), 243, Some(position(43)))];
            let second_batches = [
                batch(other_partition_id, Vec::new(), 60, None),
                batch(partition_id, Vec::new(), 244, Some(position(44))),
            ];
            let (first_result, second_result) =
                tokio::time::timeout(Duration::from_secs(10), async {
                    tokio::join!(
                        projector.apply_outbox_batch(epoch, &first_batches),
                        projector.apply_outbox_batch(epoch, &second_batches)
                    )
                })
                .await
                .expect("writers of the same partition serialize");
            first_result.expect("the first writer completes");
            second_result.expect("the second writer completes");
            assert_eq!(
                projector
                    .read_progress(partition_id)
                    .await
                    .unwrap()
                    .map(|progress| progress.outbox_through_seq),
                Some(244)
            );
        }

        let mut inspected_slots = Vec::new();
        for _ in 0..pool_size {
            let slot = backend.lock_next_slot().await;
            let prepared = slot
                .client
                .query_typed_one("SELECT count(*) FROM pg_prepared_statements", &[])
                .await
                .expect("inspect named prepared statements");
            assert_eq!(prepared.get::<_, i64>(0), 0);
            inspected_slots.push(slot);
        }
        drop(inspected_slots);
        drop(projector);
        drop(backend);

        if dialect == VisibilityDialect::ParadeDb {
            admin_client.batch_execute(&format!("UPDATE {schema_name}.dexzd_visibility_schema_version SET version = 3 WHERE id = 1; ALTER TABLE {schema_name}.dexzd_visibility_schema_version DROP COLUMN dialect")).await.unwrap();
            let previous_count: i64 = admin_client
                .query_one(
                    &format!("SELECT count(*) FROM {schema_name}.dexzd_visibility_runs"),
                    &[],
                )
                .await
                .unwrap()
                .get(0);
            let upgraded = SqlVisibilitySearchBackend::connect_dialect_with_settings(
                &ConnectionSettings::from_config(configuration.clone()),
                1,
                None,
                Duration::from_secs(2),
                dialect,
                Some(Arc::new(TestAttributeRegistry::default())),
            )
            .await
            .unwrap();
            let preserved_count: i64 = admin_client
                .query_one(
                    &format!("SELECT count(*) FROM {schema_name}.dexzd_visibility_runs"),
                    &[],
                )
                .await
                .unwrap()
                .get(0);
            let metadata = admin_client.query_one(&format!("SELECT version, dialect FROM {schema_name}.dexzd_visibility_schema_version WHERE id = 1"), &[]).await.unwrap();
            assert_eq!(metadata.get::<_, i32>(0), 3);
            assert_eq!(metadata.get::<_, String>(1), "paradedb");
            assert!(
                metadata.get::<_, i32>(0) <= 3,
                "the older binary's schema version check still accepts ParadeDB"
            );
            assert_eq!(previous_count, preserved_count);
            assert!(previous_count > 0);
            drop(upgraded);
        }
        admin_client
            .batch_execute(&format!(
                "DROP TABLE {schema_name}.dexzd_visibility_skipped_documents; \
                 ALTER TABLE {schema_name}.dexzd_visibility_progress DROP COLUMN applied_epoch, DROP COLUMN outbox_through_seq; \
                 UPDATE {schema_name}.dexzd_visibility_schema_version SET version = 2 WHERE id = 1"
            ))
            .await
            .expect("simulate schema version without skipped-document records");
        let version_two_backend = Arc::new(
            SqlVisibilitySearchBackend::connect_dialect_with_settings(
                &ConnectionSettings::from_config(configuration.clone()),
                1,
                None,
                Duration::from_secs(2),
                dialect,
                Some(Arc::new(TestAttributeRegistry::default())),
            )
            .await
            .expect("upgrade schema version two"),
        );
        let version_two_projector = SqlVisibilityProjector::new(version_two_backend.clone());
        let upgraded_progress = version_two_projector
            .read_progress(partition_id)
            .await
            .unwrap()
            .expect("the upgrade keeps progress rows");
        assert!(
            upgraded_progress.applied_position.is_some(),
            "{upgraded_progress:?}"
        );
        assert_eq!(upgraded_progress.applied_epoch, None);
        assert_eq!(upgraded_progress.outbox_through_seq, 0);
        assert_eq!(listed_runs(&version_two_backend, tenant_id).await.len(), 2);
        drop(version_two_projector);
        drop(version_two_backend);

        admin_client
            .batch_execute(&format!(
                "ALTER TABLE {schema_name}.dexzd_visibility_runs DROP COLUMN partition_id CASCADE; \
                 ALTER TABLE {schema_name}.dexzd_visibility_progress DROP COLUMN applied_node_id; \
                 ALTER TABLE {schema_name}.dexzd_visibility_progress DROP COLUMN applied_incarnation; \
                 ALTER TABLE {schema_name}.dexzd_visibility_progress DROP COLUMN applied_seq; \
                 UPDATE {schema_name}.dexzd_visibility_schema_version SET version = 1 WHERE id = 1"
            ))
            .await
            .expect("simulate an older visibility schema");
        let upgraded_backend = Arc::new(
            SqlVisibilitySearchBackend::connect_dialect_with_settings(
                &ConnectionSettings::from_config(configuration.clone()),
                1,
                None,
                Duration::from_secs(2),
                dialect,
                Some(Arc::new(TestAttributeRegistry::default())),
            )
            .await
            .expect("upgrade an existing visibility schema"),
        );
        let upgraded_projector = SqlVisibilityProjector::new(upgraded_backend.clone());
        assert_eq!(
            upgraded_projector
                .read_progress(partition_id)
                .await
                .unwrap()
                .and_then(|progress| progress.applied_position),
            None
        );
        assert!(listed_runs(&upgraded_backend, tenant_id).await.is_empty());
        drop(upgraded_projector);
        drop(upgraded_backend);

        admin_client
            .batch_execute(&format!(
                "UPDATE {schema_name}.dexzd_visibility_schema_version SET version = 99 WHERE id = 1"
            ))
            .await
            .expect("set future schema version");
        let newer_schema = SqlVisibilitySearchBackend::connect_dialect_with_settings(
            &ConnectionSettings::from_config(configuration.clone()),
            1,
            None,
            Duration::from_secs(2),
            dialect,
            Some(Arc::new(TestAttributeRegistry::default())),
        )
        .await;
        assert!(
            matches!(newer_schema, Err(SearchError::Internal(message)) if message.contains("version 99"))
        );
        admin_client
            .batch_execute(&format!("DROP SCHEMA {schema_name} CASCADE"))
            .await
            .expect("drop projector schema");
    }
}
