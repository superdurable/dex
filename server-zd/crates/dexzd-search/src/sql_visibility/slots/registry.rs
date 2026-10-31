// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use tokio_postgres::types::{ToSql, Type};
use tokio_postgres::{Client, GenericClient, Row, Transaction};

use super::{AttributeSlot, AttributeSlotColumn};
use crate::SearchError;
use crate::sql_visibility::search_backend::{database_error_message, is_transient_database_error};

const SLOT_LOCK_NAMESPACE: i64 = 5_589_034_107_693_961_331;

#[derive(Clone, Debug, Default)]
pub(crate) struct AttributeSlotSnapshot {
    pub(crate) registry_version: u64,
    pub(crate) assignments: Vec<AttributeSlot>,
    pub(crate) ready: Vec<AttributeSlot>,
}

pub(crate) async fn initialize_attribute_slots(
    client: &mut Client,
    registry_version: u64,
    assignments: &[AttributeSlot],
    should_reject_older_registry: bool,
) -> Result<AttributeSlotSnapshot, SearchError> {
    let transaction = client
        .transaction()
        .await
        .map_err(|source| registry_database_error("begin Attribute slot registration", source))?;
    transaction
        .batch_execute("SET LOCAL lock_timeout = '5s'; SET LOCAL statement_timeout = '15s'")
        .await
        .map_err(|source| registry_database_error("bound Attribute slot registration", source))?;
    lock_attribute_slots_exclusive(&transaction).await?;
    let stored_version = read_slot_registry_version(&transaction).await?;
    if stored_version > registry_version {
        let detail = format!(
            "stored registry version {stored_version} exceeds object-store registry version {registry_version}"
        );
        return Err(if should_reject_older_registry {
            registry_mismatch(detail)
        } else {
            SearchError::Unavailable(format!(
                "PostgreSQL visibility Attribute registry read is stale: {detail}; retry with a fresh registry read"
            ))
        });
    }
    let existing = load_attribute_slot_snapshot(&transaction, stored_version).await?;
    for stored in &existing.assignments {
        if !assignments.contains(stored) {
            return Err(registry_mismatch(format!(
                "tenant {} index {:?} slot {} is absent or changed in the Attribute registry",
                stored.tenant_id, stored.index_key, stored.slot_column
            )));
        }
    }
    for assignment in assignments {
        if existing.assignments.contains(assignment) {
            continue;
        }
        let tenant = assignment.tenant_id.to_string();
        let column = assignment.slot_column.as_str();
        transaction.execute_typed("INSERT INTO dexzd_visibility_attribute_slots (tenant_id, index_key, slot_column, state) VALUES ($1::text, $2::text, $3::text, 'backfilling')", &[
            (&tenant as &(dyn ToSql + Sync), Type::TEXT), (&assignment.index_key, Type::TEXT), (&column, Type::TEXT)
        ]).await.map_err(|source| slot_database_error("register", assignment, source))?;
        tracing::info!(tenant_id = assignment.tenant_id, index_key = %assignment.index_key, slot_column = %assignment.slot_column, "Attribute slot registered for backfill");
    }
    let version = i64::try_from(registry_version)
        .map_err(|_| registry_mismatch("Attribute registry version exceeds bigint".into()))?;
    transaction.execute_typed("UPDATE dexzd_visibility_schema_version SET attribute_registry_version = $1::bigint WHERE id = 1", &[(&version as &(dyn ToSql + Sync), Type::INT8)]).await.map_err(|source| registry_database_error("record Attribute slot registry version", source))?;
    let snapshot = load_attribute_slot_snapshot(&transaction, registry_version).await?;
    transaction
        .commit()
        .await
        .map_err(|source| registry_database_error("commit Attribute slot registration", source))?;
    Ok(snapshot)
}

pub(crate) async fn load_committed_attribute_slot_snapshot(
    client: &mut Client,
) -> Result<AttributeSlotSnapshot, SearchError> {
    let transaction = client
        .transaction()
        .await
        .map_err(|source| registry_database_error("begin Attribute slot snapshot read", source))?;
    lock_attribute_slots_shared(&transaction).await?;
    let version = read_slot_registry_version(&transaction).await?;
    let snapshot = load_attribute_slot_snapshot(&transaction, version).await?;
    transaction
        .commit()
        .await
        .map_err(|source| registry_database_error("commit Attribute slot snapshot read", source))?;
    Ok(snapshot)
}

pub(crate) async fn lock_attribute_slots_shared(
    transaction: &Transaction<'_>,
) -> Result<(), SearchError> {
    transaction.query_typed_one("SELECT pg_advisory_xact_lock_shared($1::bigint + current_schema()::regnamespace::oid::bigint)", &[(&SLOT_LOCK_NAMESPACE as &(dyn ToSql + Sync), Type::INT8)]).await.map_err(|source| registry_database_error("lock Attribute slot registry for projection", source))?;
    Ok(())
}

pub(crate) async fn lock_attribute_slots_exclusive(
    transaction: &Transaction<'_>,
) -> Result<(), SearchError> {
    transaction.query_typed_one("SELECT pg_advisory_xact_lock($1::bigint + current_schema()::regnamespace::oid::bigint)", &[(&SLOT_LOCK_NAMESPACE as &(dyn ToSql + Sync), Type::INT8)]).await.map_err(|source| registry_database_error("lock Attribute slot registration", source))?;
    Ok(())
}

pub(crate) async fn read_slot_registry_version(
    client: &impl GenericClient,
) -> Result<u64, SearchError> {
    let row = client
        .query_typed_one(
            "SELECT attribute_registry_version FROM dexzd_visibility_schema_version WHERE id = 1",
            &[],
        )
        .await
        .map_err(|source| {
            registry_database_error("read Attribute slot registry version", source)
        })?;
    u64::try_from(row.get::<_, i64>(0))
        .map_err(|_| registry_mismatch("negative Attribute slot registry version".into()))
}

pub(crate) async fn load_attribute_slot_snapshot(
    client: &impl GenericClient,
    registry_version: u64,
) -> Result<AttributeSlotSnapshot, SearchError> {
    let rows = client.query_typed("SELECT tenant_id, index_key, slot_column, state = 'ready' FROM dexzd_visibility_attribute_slots ORDER BY tenant_id, slot_column", &[]).await.map_err(|source| registry_database_error("read Attribute slot assignments", source))?;
    let mut snapshot = AttributeSlotSnapshot {
        registry_version,
        ..Default::default()
    };
    for row in rows {
        let assignment = assignment_from_row(&row)?;
        if row.get::<_, bool>(3) {
            snapshot.ready.push(assignment.clone());
        }
        snapshot.assignments.push(assignment);
    }
    Ok(snapshot)
}

pub(crate) fn assignment_from_row(row: &Row) -> Result<AttributeSlot, SearchError> {
    let tenant: String = row.get(0);
    let column: String = row.get(2);
    let slot_column = AttributeSlotColumn::from_database(&column)?;
    Ok(AttributeSlot {
        tenant_id: tenant
            .parse()
            .map_err(|_| registry_mismatch(format!("invalid slot tenant {tenant:?}")))?,
        index_key: row.get(1),
        index_type: slot_column.index_type(),
        slot_column,
    })
}

pub(crate) fn slot_database_error(
    operation: &str,
    assignment: &AttributeSlot,
    source: tokio_postgres::Error,
) -> SearchError {
    registry_database_error(
        &format!(
            "{operation} Attribute index {:?} slot {} in tenant {}",
            assignment.index_key, assignment.slot_column, assignment.tenant_id
        ),
        source,
    )
}

pub(crate) fn registry_database_error(
    operation: &str,
    source: tokio_postgres::Error,
) -> SearchError {
    let message = format!("{operation}: {}", database_error_message(&source));
    if is_transient_database_error(&source) {
        SearchError::Unavailable(message)
    } else {
        SearchError::Internal(message)
    }
}

fn registry_mismatch(detail: String) -> SearchError {
    SearchError::Internal(format!(
        "PostgreSQL visibility Attribute slot registry mismatch: {detail}; after restoring an older object-store prefix or replacing the registry, use a new database or schema"
    ))
}
