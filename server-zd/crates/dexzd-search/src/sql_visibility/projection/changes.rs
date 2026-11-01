// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::row_keys::decode_flow_run_after_prefix;
use dexzd_keyspace::{FlowHash, IndexKind, PartitionId, RunId, TenantId, partition_index_prefix};

use super::projector::ProjectionError;

/// One committed search document change to project into SQL visibility.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum SearchDocumentChange {
    /// Store the run's encoded committed document.
    Upsert {
        /// Tenant owning the run.
        tenant_id: TenantId,
        /// Run being changed.
        run_id: RunId,
        /// Encoded SearchDocumentRecord.
        encoded_document: Vec<u8>,
    },
    /// Remove a purged or purging run's row.
    Delete {
        /// Tenant owning the run.
        tenant_id: TenantId,
        /// Run being purged.
        run_id: RunId,
    },
}

impl SearchDocumentChange {
    /// Returns the run the change applies to.
    #[must_use]
    pub fn run_id(&self) -> RunId {
        match self {
            Self::Upsert { run_id, .. } | Self::Delete { run_id, .. } => *run_id,
        }
    }
}

pub(super) fn decode_search_index_key(
    partition_id: PartitionId,
    key: &[u8],
) -> Result<Option<(TenantId, FlowHash, RunId)>, ProjectionError> {
    let prefix = partition_index_prefix(partition_id, IndexKind::SearchDocument);
    if !key.starts_with(&prefix) {
        return Ok(None);
    }
    if key.len() != prefix.len() + 32 {
        return Err(ProjectionError::InvalidState(
            "malformed committed SearchDocument index delete".into(),
        ));
    }
    let tenant_end = prefix.len() + 8;
    let tenant_bytes: [u8; 8] = key[prefix.len()..tenant_end]
        .try_into()
        .map_err(|_| ProjectionError::InvalidState("malformed search tenant ID".into()))?;
    let (flow_hash, run_id) = decode_flow_run_after_prefix(key, tenant_end)
        .ok_or_else(|| ProjectionError::InvalidState("malformed search run identity".into()))?;
    Ok(Some((
        TenantId(u64::from_be_bytes(tenant_bytes)),
        flow_hash,
        run_id,
    )))
}
