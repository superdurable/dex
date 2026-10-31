// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

#![cfg(test)]

use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use async_trait::async_trait;
use dexzd_keyspace::{PartitionId, RunId, TenantId};
use dexzd_postgres_connection::ConnectionSettings;
use dexzd_protocol::dex::{FlowStatus, IndexType, Value, value};
use dexzd_protocol::dexzd::v1::{
    AttributeIndexRegistration, AttributeIndexRegistry, LogPosition, SearchAttributeValue,
    SearchDocumentRecord,
};
use prost::Message;
use tokio::task::JoinHandle;
use tokio_postgres::{Client, Config, NoTls};

use crate::SearchError;
use crate::sql_visibility::slots::backfill::{
    apply_backfill_batch, backfill_next_batch, backfill_pending_count,
};
use crate::sql_visibility::slots::registry::{
    initialize_attribute_slots, lock_attribute_slots_exclusive,
};
use crate::sql_visibility::{
    AttributeIndexRegistryReader, SearchDocumentChange, SqlVisibilityProjector,
    SqlVisibilitySearchBackend, VisibilityDialect, assign_attribute_slots,
    ensure_schema_for_dialect,
};

struct FixedRegistry(AttributeIndexRegistry);

#[async_trait]
impl AttributeIndexRegistryReader for FixedRegistry {
    async fn read_attribute_index_registry(&self) -> Result<AttributeIndexRegistry, SearchError> {
        Ok(self.0.clone())
    }
}

struct PostgresFixture {
    client: Client,
    configuration: Config,
    driver: JoinHandle<()>,
    schema: String,
}

impl PostgresFixture {
    async fn create(database_url: &str) -> Self {
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let schema = format!("dexzd_lifecycle_{}_{nonce}", std::process::id());
        let mut configuration: Config = database_url.parse().unwrap();
        configuration
            .options(format!("-c search_path={schema},public"))
            .application_name(&schema);
        let (mut client, connection) = configuration.connect(NoTls).await.unwrap();
        let driver = tokio::spawn(async move {
            if let Err(error) = connection.await {
                tracing::debug!(%error, "test PostgreSQL connection ended");
            }
        });
        client
            .batch_execute(&format!("CREATE SCHEMA {schema}"))
            .await
            .unwrap();
        ensure_schema_for_dialect(&mut client, VisibilityDialect::Postgres)
            .await
            .unwrap();
        Self {
            client,
            configuration,
            driver,
            schema,
        }
    }

    async fn backend(&self, registry: AttributeIndexRegistry) -> Arc<SqlVisibilitySearchBackend> {
        Arc::new(
            SqlVisibilitySearchBackend::connect_dialect_with_settings(
                &ConnectionSettings::from_config(self.configuration.clone()),
                2,
                None,
                Duration::from_secs(2),
                VisibilityDialect::Postgres,
                Some(Arc::new(FixedRegistry(registry))),
            )
            .await
            .unwrap(),
        )
    }

    async fn seed_rows(&self, count: i32) {
        self.client.execute("INSERT INTO dexzd_visibility_runs (tenant_id, partition_id, run_id, workflow_id, workflow_type, flow_type, execution_status, start_time_ns, version, sa_keyword) SELECT '1', 0, lpad(position::text, 8, '0'), 'seed-' || position, 'Engine', 'SeedFlow', 'Running', position, 1, jsonb_build_object('keyword', 'before') FROM generate_series(1, $1::integer) AS position", &[&count]).await.unwrap();
    }

    async fn reconnect(&mut self) {
        self.driver.abort();
        let (client, connection) = self.configuration.connect(NoTls).await.unwrap();
        self.client = client;
        let replacement_driver = tokio::spawn(async move {
            if let Err(error) = connection.await {
                tracing::debug!(%error, "test PostgreSQL connection ended");
            }
        });
        self.driver = replacement_driver;
    }

    async fn cleanup(&self) {
        self.client
            .batch_execute(&format!("DROP SCHEMA {} CASCADE", self.schema))
            .await
            .unwrap();
    }
}

impl Drop for PostgresFixture {
    fn drop(&mut self) {
        self.driver.abort();
    }
}

fn registry() -> AttributeIndexRegistry {
    AttributeIndexRegistry {
        version: 1,
        registrations: vec![AttributeIndexRegistration {
            tenant_id: 1,
            index_key: "keyword".into(),
            index_type: IndexType::Keyword as i32,
            ..Default::default()
        }],
    }
}

fn document_change(run_id: RunId) -> SearchDocumentChange {
    let record = SearchDocumentRecord {
        version: 1,
        tenant_id: 1,
        flow_id: "concurrent-projection".into(),
        run_id: run_id.as_bytes().to_vec(),
        flow_type: "ConcurrentProjectionFlow".into(),
        status: FlowStatus::Running as i32,
        started_at_micros: 1,
        attributes: vec![SearchAttributeValue {
            index_key: "keyword".into(),
            index_type: IndexType::Keyword as i32,
            value: Some(Value {
                kind: Some(value::Kind::StringValue("after".into())),
            }),
        }],
        ..Default::default()
    };
    SearchDocumentChange::Upsert {
        tenant_id: TenantId(1),
        run_id,
        encoded_document: record.encode_to_vec(),
    }
}

#[tokio::test]
async fn projection_advances_while_another_partition_backfills() {
    let Ok(database_url) = std::env::var("DEX_ZD_POSTGRES_URL") else {
        return;
    };
    let mut fixture = PostgresFixture::create(&database_url).await;
    fixture.seed_rows(800).await;
    let backend = fixture.backend(registry()).await;
    let projector = SqlVisibilityProjector::new(backend.clone());
    let transaction = fixture.client.transaction().await.unwrap();
    transaction.query_one("SELECT run_id FROM dexzd_visibility_runs WHERE tenant_id = '1' AND run_id = '00000001' FOR UPDATE", &[]).await.unwrap();
    let background_projector = projector.clone();
    let backfill =
        tokio::spawn(async move { background_projector.synchronize_attribute_slots().await });
    let probe = backend.lock_next_slot().await;
    tokio::time::timeout(Duration::from_secs(5), async {
        loop {
            let is_blocked: bool = probe.client.query_one("SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE application_name = $1 AND wait_event_type = 'Lock')", &[&fixture.schema]).await.unwrap().get(0);
            if is_blocked { break; }
            tokio::task::yield_now().await;
        }
    }).await.unwrap();
    drop(probe);
    let position = LogPosition {
        node_id: "concurrent-node".into(),
        incarnation: 1,
        seq: 1,
    };
    let run_id = RunId([74; 16]);
    tokio::time::timeout(
        Duration::from_secs(3),
        projector.apply_batch(PartitionId(1), None, &position, &[document_change(run_id)]),
    )
    .await
    .expect("another partition is not stalled by backfill")
    .unwrap();
    assert_eq!(
        projector.read_progress(PartitionId(1)).await.unwrap(),
        Some(position)
    );
    transaction.rollback().await.unwrap();
    backfill.await.unwrap().unwrap();
    while backfill_next_batch(&mut fixture.client).await.unwrap() {}
    let value: String = fixture
        .client
        .query_one(
            "SELECT keyword_01 FROM dexzd_visibility_runs WHERE run_id = $1",
            &[&run_id.to_string()],
        )
        .await
        .unwrap()
        .get(0);
    assert_eq!(value, "after");
    drop(projector);
    drop(backend);
    fixture.cleanup().await;
}

#[tokio::test]
async fn committed_backfill_batches_survive_a_connection_crash() {
    let Ok(database_url) = std::env::var("DEX_ZD_POSTGRES_URL") else {
        return;
    };
    let mut fixture = PostgresFixture::create(&database_url).await;
    fixture.seed_rows(800).await;
    let registry = registry();
    let assignments = assign_attribute_slots(&registry).unwrap();
    initialize_attribute_slots(&mut fixture.client, registry.version, &assignments)
        .await
        .unwrap();
    assert!(backfill_next_batch(&mut fixture.client).await.unwrap());
    let checkpoint: String = fixture
        .client
        .query_one(
            "SELECT backfill_after_run_id FROM dexzd_visibility_attribute_slots",
            &[],
        )
        .await
        .unwrap()
        .get(0);
    assert!(!checkpoint.is_empty());
    let transaction = fixture.client.transaction().await.unwrap();
    assert!(apply_backfill_batch(&transaction).await.unwrap());
    let process_id: i32 = transaction
        .query_one("SELECT pg_backend_pid()", &[])
        .await
        .unwrap()
        .get(0);
    let (terminator, connection) = fixture.configuration.connect(NoTls).await.unwrap();
    let terminator_driver = tokio::spawn(connection);
    assert!(
        terminator
            .query_one("SELECT pg_terminate_backend($1::integer)", &[&process_id])
            .await
            .unwrap()
            .get::<_, bool>(0)
    );
    transaction
        .commit()
        .await
        .expect_err("a crashed batch does not commit its checkpoint");
    fixture.reconnect().await;
    let restored: String = fixture
        .client
        .query_one(
            "SELECT backfill_after_run_id FROM dexzd_visibility_attribute_slots",
            &[],
        )
        .await
        .unwrap()
        .get(0);
    assert_eq!(restored, checkpoint);
    while backfill_next_batch(&mut fixture.client).await.unwrap() {}
    assert_eq!(backfill_pending_count(&fixture.client).await.unwrap(), 0);
    let count: i64 = fixture
        .client
        .query_one(
            "SELECT count(*) FROM dexzd_visibility_runs WHERE keyword_01 = 'before'",
            &[],
        )
        .await
        .unwrap()
        .get(0);
    assert_eq!(count, 800);
    drop(terminator);
    terminator_driver.await.unwrap().unwrap();
    fixture.cleanup().await;
}

#[tokio::test]
async fn distinct_schemas_do_not_share_registration_locks_and_reused_registries_fail_startup() {
    let Ok(database_url) = std::env::var("DEX_ZD_POSTGRES_URL") else {
        return;
    };
    let mut first = PostgresFixture::create(&database_url).await;
    let second = PostgresFixture::create(&database_url).await;
    let first_backend = first.backend(registry()).await;
    let second_backend = second.backend(registry()).await;
    let transaction = first.client.transaction().await.unwrap();
    lock_attribute_slots_exclusive(&transaction).await.unwrap();
    let projector = SqlVisibilityProjector::new(second_backend.clone());
    let position = LogPosition {
        node_id: "other-schema".into(),
        incarnation: 1,
        seq: 1,
    };
    tokio::time::timeout(
        Duration::from_secs(3),
        projector.apply_batch(
            PartitionId(2),
            None,
            &position,
            &[document_change(RunId([75; 16]))],
        ),
    )
    .await
    .expect("a different schema does not wait on this schema's lock")
    .unwrap();
    transaction.rollback().await.unwrap();
    let mismatch = SqlVisibilitySearchBackend::connect_dialect_with_settings(
        &ConnectionSettings::from_config(first.configuration.clone()),
        1,
        None,
        Duration::from_secs(2),
        VisibilityDialect::Postgres,
        Some(Arc::new(FixedRegistry(AttributeIndexRegistry::default()))),
    )
    .await;
    assert!(
        matches!(mismatch, Err(SearchError::Internal(message)) if message.contains("registry mismatch") && message.contains("new database or schema"))
    );
    drop(projector);
    drop(first_backend);
    drop(second_backend);
    first.cleanup().await;
    second.cleanup().await;
}
