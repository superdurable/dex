// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_protocol::dex::IndexType;
use tokio_postgres::types::{ToSql, Type};
use tokio_postgres::{GenericClient, Transaction};

use super::AttributeSlot;
use super::values::slot_value_expression;
use crate::SearchError;
use crate::sql_visibility::search_backend::{database_error_message, is_transient_database_error};

const SLOT_ADVISORY_LOCK: i64 = 5_589_034_107_693_961_331;
const BACKFILL_BATCH_SIZE: i64 = 256;

pub(crate) async fn lock_attribute_slots(transaction: &Transaction<'_>) -> Result<(), SearchError> {
    transaction
        .query_typed_one(
            "SELECT pg_advisory_xact_lock($1::bigint)",
            &[(&SLOT_ADVISORY_LOCK as &(dyn ToSql + Sync), Type::INT8)],
        )
        .await
        .map_err(database_error)?;
    Ok(())
}

pub(crate) async fn read_ready_slots(
    client: &impl GenericClient,
    assignments: Vec<AttributeSlot>,
) -> Result<Vec<AttributeSlot>, SearchError> {
    let rows = client.query_typed("SELECT tenant_id, index_key, slot_column FROM dexzd_visibility_attribute_slots WHERE state = 'ready'", &[]).await.map_err(database_error)?;
    Ok(assignments
        .into_iter()
        .filter(|assignment| {
            rows.iter().any(|row| {
                row.get::<_, String>(0) == assignment.tenant_id.to_string()
                    && row.get::<_, String>(1) == assignment.index_key
                    && row.get::<_, String>(2) == assignment.slot_column
            })
        })
        .collect())
}

pub(crate) async fn backfill_attribute_slots(
    transaction: &Transaction<'_>,
    assignments: &[AttributeSlot],
) -> Result<(), SearchError> {
    for assignment in assignments {
        let tenant = assignment.tenant_id.to_string();
        let parameters: &[(&(dyn ToSql + Sync), Type)] = &[
            (&tenant, Type::TEXT),
            (&assignment.index_key, Type::TEXT),
            (&assignment.slot_column, Type::TEXT),
        ];
        transaction.execute_typed("INSERT INTO dexzd_visibility_attribute_slots (tenant_id, index_key, slot_column, state) VALUES ($1, $2, $3, 'backfilling') ON CONFLICT (tenant_id, index_key) DO NOTHING", parameters).await.map_err(database_error)?;
        let state = transaction.query_typed_one("SELECT state, slot_column FROM dexzd_visibility_attribute_slots WHERE tenant_id = $1 AND index_key = $2", &parameters[..2]).await.map_err(database_error)?;
        if state.get::<_, String>(1) != assignment.slot_column {
            return Err(SearchError::Internal(format!(
                "Attribute slot assignment changed for {:?}",
                assignment.index_key
            )));
        }
        if state.get::<_, String>(0) == "ready" {
            continue;
        }
        let partitions = transaction
            .query_typed(
                "SELECT DISTINCT partition_id FROM dexzd_visibility_runs WHERE tenant_id = $1",
                &parameters[..1],
            )
            .await
            .map_err(database_error)?;
        for partition in partitions {
            backfill_partition(transaction, assignment, partition.get(0)).await?;
        }
        transaction.execute_typed("UPDATE dexzd_visibility_attribute_slots SET state = 'ready' WHERE tenant_id = $1 AND index_key = $2", &parameters[..2]).await.map_err(database_error)?;
        tracing::info!(tenant_id = assignment.tenant_id, index_key = %assignment.index_key, slot_column = %assignment.slot_column, "Attribute slot backfill completed");
    }
    Ok(())
}

async fn backfill_partition(
    transaction: &Transaction<'_>,
    assignment: &AttributeSlot,
    partition_id: i32,
) -> Result<(), SearchError> {
    let tenant = assignment.tenant_id.to_string();
    let mut after_run = String::new();
    let bucket = slot_bucket(assignment.index_type);
    let expression = slot_value_expression(assignment.index_type, bucket, "$2::text");
    loop {
        let rows = transaction.query_typed(&format!("SELECT run_id, sa_text ->> $2::text AS text_value FROM dexzd_visibility_runs WHERE tenant_id = $1::text AND partition_id = $3::integer AND run_id COLLATE \"C\" > $4::text AND {} IS NULL AND {bucket} ? $2::text ORDER BY run_id COLLATE \"C\" LIMIT $5::bigint", assignment.slot_column), &[
            (&tenant as &(dyn ToSql + Sync), Type::TEXT), (&assignment.index_key, Type::TEXT), (&partition_id, Type::INT4), (&after_run, Type::TEXT), (&BACKFILL_BATCH_SIZE, Type::INT8)
        ]).await.map_err(database_error)?;
        if rows.is_empty() {
            break;
        }
        after_run = rows.last().expect("nonempty backfill batch").get(0);
        let run_ids = rows
            .iter()
            .map(|row| row.get::<_, String>(0))
            .collect::<Vec<_>>();
        transaction.execute_typed(&format!("UPDATE dexzd_visibility_runs SET {} = {expression} WHERE tenant_id = $1::text AND run_id = ANY($3::text[]) AND {} IS NULL AND {bucket} ? $2::text", assignment.slot_column, assignment.slot_column), &[
            (&tenant as &(dyn ToSql + Sync), Type::TEXT), (&assignment.index_key, Type::TEXT), (&run_ids, Type::TEXT_ARRAY)
        ]).await.map_err(database_error)?;
    }
    Ok(())
}

fn slot_bucket(index_type: IndexType) -> &'static str {
    match index_type {
        IndexType::Keyword | IndexType::KeywordArray => "sa_keyword",
        IndexType::Int | IndexType::Double => "sa_number",
        IndexType::Datetime => "sa_datetime",
        IndexType::Bool => "sa_bool",
        IndexType::Text => "sa_text",
        IndexType::Unspecified => unreachable!("slot assignments reject unspecified types"),
    }
}

fn database_error(source: tokio_postgres::Error) -> SearchError {
    let message = format!(
        "backfill Attribute slots: {}",
        database_error_message(&source)
    );
    if is_transient_database_error(&source) {
        SearchError::Unavailable(message)
    } else {
        SearchError::Internal(message)
    }
}
