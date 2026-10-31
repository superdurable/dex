// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::{PartitionId, TenantId};
use dexzd_protocol::dex::IndexType;
use tokio_postgres::Transaction;
use tokio_postgres::types::{ToSql, Type};

use super::AttributeSlot;
use super::registry::slot_database_error;
use crate::SearchError;
use crate::sql_visibility::projection::SkippedSearchDocument;

pub(super) async fn skip_oversized_documents(
    transaction: &Transaction<'_>,
    assignment: &AttributeSlot,
    run_ids: &[String],
) -> Result<Vec<SkippedSearchDocument>, SearchError> {
    let maximum_bytes = match assignment.index_type {
        IndexType::Keyword => {
            "CASE WHEN jsonb_typeof(sa_keyword -> $2::text) = 'string' THEN octet_length(sa_keyword ->> $2::text) END"
        }
        IndexType::KeywordArray => {
            "(SELECT max(octet_length(element)) FROM jsonb_array_elements_text(CASE WHEN jsonb_typeof(sa_keyword -> $2::text) = 'array' THEN sa_keyword -> $2::text ELSE '[]'::jsonb END) AS element)"
        }
        _ => return Ok(Vec::new()),
    };
    let tenant = assignment.tenant_id.to_string();
    let reason_prefix = format!(
        "invalid committed search state: indexed Keyword {:?} has ",
        assignment.index_key
    );
    let rows = transaction.query_typed(&format!("WITH removed AS (DELETE FROM dexzd_visibility_runs WHERE tenant_id = $1::text AND run_id = ANY($3::text[]) AND {maximum_bytes} > 1024 RETURNING partition_id, run_id, {maximum_bytes} AS maximum_bytes) INSERT INTO dexzd_visibility_skipped_documents (tenant_id, partition_id, run_id, reason) SELECT $1::text, partition_id, run_id, $4::text || maximum_bytes::text || ' bytes; PostgreSQL maximum is 1024 bytes' FROM removed ON CONFLICT (tenant_id, run_id) DO UPDATE SET partition_id = EXCLUDED.partition_id, reason = EXCLUDED.reason RETURNING partition_id, run_id, reason"), &[
        (&tenant as &(dyn ToSql + Sync), Type::TEXT), (&assignment.index_key, Type::TEXT), (&run_ids, Type::TEXT_ARRAY), (&reason_prefix, Type::TEXT)
    ]).await.map_err(|source| slot_database_error("skip oversized backfill documents for", assignment, source))?;
    Ok(rows
        .iter()
        .map(|row| {
            let partition_id =
                u16::try_from(row.get::<_, i32>(0)).expect("stored search partitions fit u16");
            let run_id: String = row.get(1);
            SkippedSearchDocument {
                partition_id: PartitionId(partition_id),
                tenant_id: TenantId(assignment.tenant_id),
                run_id: run_id
                    .parse()
                    .expect("stored search runs have valid RunIds"),
                reason: row.get(2),
            }
        })
        .collect())
}
