// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use crate::sql_visibility::projection::SkippedSearchDocument;
use dexzd_protocol::dex::IndexType;
#[cfg(test)]
use tokio_postgres::GenericClient;
use tokio_postgres::types::{ToSql, Type};
use tokio_postgres::{Client, Transaction};

use super::AttributeSlot;
use super::oversized_documents::skip_oversized_documents;
use super::registry::{assignment_from_row, registry_database_error, slot_database_error};
use super::values::slot_value_expression;
use crate::SearchError;

const BACKFILL_BATCH_SIZE: i64 = 256;

#[derive(Default)]
pub(crate) struct BackfillBatchResult {
    pub(crate) did_advance: bool,
    pub(crate) skipped_documents: Vec<SkippedSearchDocument>,
}

pub(crate) async fn backfill_next_batch(
    client: &mut Client,
) -> Result<BackfillBatchResult, SearchError> {
    let transaction = client
        .transaction()
        .await
        .map_err(|source| registry_database_error("begin Attribute slot backfill batch", source))?;
    transaction
        .batch_execute("SET LOCAL statement_timeout = '15s'; SET LOCAL lock_timeout = '5s'")
        .await
        .map_err(|source| registry_database_error("bound Attribute slot backfill batch", source))?;
    let result = apply_backfill_batch(&transaction).await?;
    transaction.commit().await.map_err(|source| {
        registry_database_error("commit Attribute slot backfill batch", source)
    })?;
    Ok(result)
}

pub(crate) async fn apply_backfill_batch(
    transaction: &Transaction<'_>,
) -> Result<BackfillBatchResult, SearchError> {
    let row = transaction.query_typed_opt("SELECT tenant_id, index_key, slot_column, backfill_partition_id, backfill_after_run_id FROM dexzd_visibility_attribute_slots WHERE state = 'backfilling' ORDER BY tenant_id, index_key FOR UPDATE SKIP LOCKED LIMIT 1", &[]).await.map_err(|source| registry_database_error("select pending Attribute slot backfill", source))?;
    let Some(row) = row else {
        return Ok(BackfillBatchResult::default());
    };
    let assignment = assignment_from_row(&row)?;
    let mut partition_id: Option<i32> = row.get(3);
    let after_run_id: String = row.get(4);
    let tenant = assignment.tenant_id.to_string();
    if partition_id.is_none() {
        partition_id = transaction
            .query_typed_one(
                "SELECT min(partition_id) FROM dexzd_visibility_runs WHERE tenant_id = $1::text",
                &[(&tenant as &(dyn ToSql + Sync), Type::TEXT)],
            )
            .await
            .map_err(|source| {
                slot_database_error("find first backfill partition for", &assignment, source)
            })?
            .get(0);
    }
    let Some(partition_id) = partition_id else {
        mark_ready(transaction, &assignment).await?;
        return Ok(BackfillBatchResult {
            did_advance: true,
            ..Default::default()
        });
    };
    let bucket = slot_bucket(assignment.index_type);
    let presence = "(sa_keyword ? $2::text OR sa_text ? $2::text OR sa_number ? $2::text OR sa_datetime ? $2::text OR sa_bool ? $2::text)";
    let rows = transaction.query_typed("SELECT run_id FROM dexzd_visibility_runs WHERE tenant_id = $1::text AND partition_id = $2::integer AND run_id > $3::text ORDER BY run_id LIMIT $4::bigint", &[
        (&tenant as &(dyn ToSql + Sync), Type::TEXT), (&partition_id, Type::INT4), (&after_run_id, Type::TEXT), (&BACKFILL_BATCH_SIZE, Type::INT8)
    ]).await.map_err(|source| slot_database_error("read backfill batch for", &assignment, source))?;
    if rows.is_empty() {
        let next_partition: Option<i32> = transaction.query_typed_one("SELECT min(partition_id) FROM dexzd_visibility_runs WHERE tenant_id = $1::text AND partition_id > $2::integer", &[(&tenant as &(dyn ToSql + Sync), Type::TEXT), (&partition_id, Type::INT4)]).await.map_err(|source| slot_database_error("find next backfill partition for", &assignment, source))?.get(0);
        if let Some(next_partition) = next_partition {
            record_checkpoint(transaction, &assignment, next_partition, "").await?;
        } else {
            mark_ready(transaction, &assignment).await?;
        }
        return Ok(BackfillBatchResult {
            did_advance: true,
            ..Default::default()
        });
    }
    let run_ids = rows
        .iter()
        .map(|row| row.get::<_, String>(0))
        .collect::<Vec<_>>();
    let skipped_documents = skip_oversized_documents(transaction, &assignment, &run_ids).await?;
    let expression = slot_value_expression(assignment.index_type, bucket, "$2::text");
    transaction.execute_typed(&format!("UPDATE dexzd_visibility_runs SET {} = {expression} WHERE tenant_id = $1::text AND run_id = ANY($3::text[]) AND {} IS NULL AND {presence}", assignment.slot_column, assignment.slot_column), &[
        (&tenant as &(dyn ToSql + Sync), Type::TEXT), (&assignment.index_key, Type::TEXT), (&run_ids, Type::TEXT_ARRAY)
    ]).await.map_err(|source| slot_database_error("update backfill batch for", &assignment, source))?;
    record_checkpoint(
        transaction,
        &assignment,
        partition_id,
        run_ids
            .last()
            .expect("a nonempty backfill batch has a last run"),
    )
    .await?;
    Ok(BackfillBatchResult {
        did_advance: true,
        skipped_documents,
    })
}

async fn record_checkpoint(
    transaction: &Transaction<'_>,
    assignment: &AttributeSlot,
    partition_id: i32,
    after_run_id: &str,
) -> Result<(), SearchError> {
    let tenant = assignment.tenant_id.to_string();
    transaction.execute_typed("UPDATE dexzd_visibility_attribute_slots SET backfill_partition_id = $3::integer, backfill_after_run_id = $4::text WHERE tenant_id = $1::text AND index_key = $2::text", &[
        (&tenant as &(dyn ToSql + Sync), Type::TEXT), (&assignment.index_key, Type::TEXT), (&partition_id, Type::INT4), (&after_run_id, Type::TEXT)
    ]).await.map_err(|source| slot_database_error("record backfill checkpoint for", assignment, source))?;
    Ok(())
}

async fn mark_ready(
    transaction: &Transaction<'_>,
    assignment: &AttributeSlot,
) -> Result<(), SearchError> {
    let tenant = assignment.tenant_id.to_string();
    transaction.execute_typed("UPDATE dexzd_visibility_attribute_slots SET state = 'ready' WHERE tenant_id = $1::text AND index_key = $2::text", &[(&tenant as &(dyn ToSql + Sync), Type::TEXT), (&assignment.index_key, Type::TEXT)]).await.map_err(|source| slot_database_error("mark ready", assignment, source))?;
    tracing::info!(tenant_id = assignment.tenant_id, index_key = %assignment.index_key, slot_column = %assignment.slot_column, "Attribute slot backfill completed");
    Ok(())
}

#[cfg(test)]
pub(crate) async fn backfill_pending_count(
    client: &impl GenericClient,
) -> Result<i64, SearchError> {
    let row = client
        .query_typed_one(
            "SELECT count(*) FROM dexzd_visibility_attribute_slots WHERE state = 'backfilling'",
            &[],
        )
        .await
        .map_err(|source| {
            registry_database_error("count pending Attribute slot backfills", source)
        })?;
    Ok(row.get(0))
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
