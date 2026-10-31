// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::VecDeque;
use std::sync::Arc;

use dexzd_storage::{LocalStore, ScanDirection, WriteBatch};

use dexzd_apply::FlowStateReader;
use dexzd_engine::CommittedStateView;
use dexzd_keyspace::row_keys::{
    decode_flow_run_after_prefix, partition_index_range, run_singleton_key,
};
use dexzd_keyspace::{ColumnFamily, IndexKind, PartitionId, RunId, RunTable, TenantId};
use dexzd_protocol::dex::FlowStatus;
use dexzd_protocol::dexzd::v1::{RunRecord, SearchDocumentRecord};
use prost::Message;

use super::projection_repairs::{ProjectionRepair, RepairPriority};
#[derive(Debug, thiserror::Error)]
#[error("read committed partition versions: {0}")]
pub(super) struct PartitionVersionReadError(String);

pub(super) struct LocalRunVersions {
    local: Arc<dyn LocalStore>,
    partition: PartitionId,
    prefix: Vec<u8>,
    start: Vec<u8>,
    buffered: VecDeque<LocalRunVersion>,
}

pub(super) struct LocalRunVersion {
    pub(super) repair: ProjectionRepair,
    pub(super) version: u64,
    pub(super) identity: (String, String),
}

#[derive(Message)]
struct SpoolVersion {
    #[prost(uint64, tag = "1")]
    tenant_id: u64,
    #[prost(bytes = "vec", tag = "2")]
    run_id: Vec<u8>,
    #[prost(uint64, tag = "3")]
    flow_hash: u64,
    #[prost(uint64, tag = "4")]
    version: u64,
    #[prost(int32, tag = "5")]
    status: i32,
}

impl LocalRunVersions {
    pub(super) fn next(&mut self) -> Result<Option<LocalRunVersion>, PartitionVersionReadError> {
        if self.buffered.is_empty() {
            let rows = self
                .local
                .scan(
                    ColumnFamily::NodeMeta,
                    Some(&self.start),
                    None,
                    ScanDirection::Forward,
                    256,
                )
                .map_err(|error| invalid(&error.to_string()))?;
            for (key, value) in rows
                .into_iter()
                .take_while(|(key, _)| key.starts_with(&self.prefix))
            {
                self.start = key;
                self.start.push(0);
                let record = SpoolVersion::decode(value.as_slice())
                    .map_err(|error| invalid(&error.to_string()))?;
                let run_id = RunId(
                    record
                        .run_id
                        .as_slice()
                        .try_into()
                        .map_err(|_| invalid("invalid spooled run ID"))?,
                );
                let tenant_id = TenantId(record.tenant_id);
                let priority = if record.version == 1 {
                    RepairPriority::Start
                } else if record.status == FlowStatus::Running as i32 {
                    RepairPriority::Intermediate
                } else {
                    RepairPriority::Close
                };
                self.buffered.push_back(LocalRunVersion {
                    repair: ProjectionRepair::new(
                        tenant_id,
                        run_id,
                        dexzd_keyspace::FlowHash(record.flow_hash),
                        self.partition,
                        priority,
                    ),
                    version: record.version,
                    identity: (tenant_id.0.to_string(), run_id.to_string()),
                });
            }
        }
        Ok(self.buffered.pop_front())
    }

    pub(super) fn clear(self) -> Result<(), PartitionVersionReadError> {
        clear_spool(&self.local, &self.prefix)
    }
}

fn clear_spool(
    local: &Arc<dyn LocalStore>,
    prefix: &[u8],
) -> Result<(), PartitionVersionReadError> {
    let mut end = prefix.to_vec();
    end.push(255);
    let mut batch = WriteBatch::new();
    batch.delete_range(ColumnFamily::NodeMeta, prefix, end);
    local
        .write(batch)
        .map_err(|error| invalid(&error.to_string()))
}

impl From<dexzd_apply::StateReadError> for PartitionVersionReadError {
    fn from(error: dexzd_apply::StateReadError) -> Self {
        Self(error.to_string())
    }
}

pub(crate) fn read_local_versions(
    view: CommittedStateView,
    partition: PartitionId,
    local: Arc<dyn LocalStore>,
    prefix: Vec<u8>,
) -> Result<LocalRunVersions, PartitionVersionReadError> {
    let (mut start, end) = partition_index_range(partition, IndexKind::SearchDocument);
    let prefix_length = start.len();
    clear_spool(&local, &prefix)?;
    loop {
        let rows = view.scan(ColumnFamily::PartitionIndex, &start, &end, 256)?;
        let Some((last_key, _)) = rows.last() else {
            break;
        };
        start = last_key.clone();
        start.push(0);
        let mut batch = WriteBatch::new();
        for (key, _) in rows {
            let tenant_bytes = key
                .get(prefix_length..prefix_length + 8)
                .ok_or_else(|| invalid("truncated search tenant key"))?;
            let tenant_id = TenantId(u64::from_be_bytes(
                tenant_bytes.try_into().expect("slice contains eight bytes"),
            ));
            let (flow_hash, run_id) = decode_flow_run_after_prefix(&key, prefix_length + 8)
                .ok_or_else(|| invalid("malformed search run key"))?;
            let record_key = run_singleton_key(flow_hash, run_id, RunTable::RunRecord);
            let record = view
                .get(ColumnFamily::Flow, &record_key)?
                .ok_or_else(|| invalid("search index has no run record"))?;
            let record = RunRecord::decode(record.as_slice())
                .map_err(|error| invalid(&error.to_string()))?;
            if record.is_purging {
                continue;
            }
            let document_key = run_singleton_key(flow_hash, run_id, RunTable::SearchDocument);
            let document = view
                .get(ColumnFamily::Flow, &document_key)?
                .ok_or_else(|| invalid("search index has no document"))?;
            let document = SearchDocumentRecord::decode(document.as_slice())
                .map_err(|error| invalid(&error.to_string()))?;
            let mut key = prefix.clone();
            key.extend_from_slice(tenant_id.0.to_string().as_bytes());
            key.push(0);
            key.extend_from_slice(run_id.to_string().as_bytes());
            let record = SpoolVersion {
                tenant_id: tenant_id.0,
                run_id: run_id.0.to_vec(),
                flow_hash: flow_hash.0,
                version: document.version,
                status: document.status,
            };
            batch.put(ColumnFamily::NodeMeta, key, record.encode_to_vec());
        }
        local
            .write(batch)
            .map_err(|error| invalid(&error.to_string()))?;
    }
    Ok(LocalRunVersions {
        local,
        partition,
        start: prefix.clone(),
        prefix,
        buffered: VecDeque::new(),
    })
}

fn invalid(detail: &str) -> PartitionVersionReadError {
    PartitionVersionReadError(detail.into())
}
