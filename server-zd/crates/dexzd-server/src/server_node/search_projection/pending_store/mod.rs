// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

mod object_sync;
mod rebuild_markers;

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;

use dexzd_keyspace::{ColumnFamily, FlowHash, PartitionId, RunId, TenantId};
use dexzd_object_store::ObjectStore;
use dexzd_storage::{LocalStore, ScanDirection, StorageError, WriteBatch};
use prost::Message;

use super::projection_repairs::{ProjectionRepair, RepairPriority};

#[derive(Debug, thiserror::Error)]
pub(crate) enum PendingStoreError {
    #[error("access local search pending runs: {0}")]
    Storage(#[from] StorageError),
    #[error("sync search pending object: {0}")]
    Object(#[from] dexzd_object_store::ObjectStoreError),
    #[error("decode search pending object: {0}")]
    Decode(#[from] prost::DecodeError),
    #[error("{operation} search pending partitions {partitions:?}: {source}")]
    PartitionOperation {
        operation: &'static str,
        partitions: Vec<PartitionId>,
        source: Box<PendingStoreError>,
    },
    #[error("invalid search pending state: {0}")]
    Invalid(String),
    #[error(
        "search pending partition {partition} object {key} belongs to newer owner epoch {epoch}"
    )]
    Fenced {
        partition: PartitionId,
        key: String,
        epoch: u64,
    },
    #[error("search pending partition {partition} object {key} changed concurrently")]
    ConcurrentWrite { partition: PartitionId, key: String },
}

#[derive(Clone, PartialEq, Message)]
struct PendingRunRecord {
    #[prost(uint64, tag = "1")]
    tenant_id: u64,
    #[prost(bytes = "vec", tag = "2")]
    run_id: Vec<u8>,
    #[prost(uint64, tag = "3")]
    flow_hash: u64,
    #[prost(uint32, tag = "4")]
    priority: u32,
    #[prost(uint64, tag = "5")]
    revision: u64,
}

pub(crate) struct PendingRunStore {
    local: Arc<dyn LocalStore>,
    objects: Arc<dyn ObjectStore>,
    namespace: Vec<u8>,
    cluster_id: [u8; 16],
    epoch: u64,
    dirty: BTreeSet<PartitionId>,
    blocked_partitions: BTreeSet<PartitionId>,
    known_partitions: BTreeSet<PartitionId>,
    last_attempted_partition: Option<PartitionId>,
    etags: BTreeMap<PartitionId, String>,
    revisions: BTreeMap<PartitionId, u64>,
    object_keys: BTreeMap<PartitionId, String>,
    pub(crate) unknown_objects: u64,
    count: usize,
}

impl PendingRunStore {
    pub(crate) fn new(
        local: Arc<dyn LocalStore>,
        objects: Arc<dyn ObjectStore>,
        cluster_id: [u8; 16],
        epoch: u64,
    ) -> Result<Self, PendingStoreError> {
        let mut namespace = b"search-pending/v1/".to_vec();
        namespace.extend_from_slice(&cluster_id);
        let mut store = Self {
            local,
            objects,
            namespace,
            cluster_id,
            epoch,
            dirty: BTreeSet::new(),
            blocked_partitions: BTreeSet::new(),
            known_partitions: BTreeSet::new(),
            last_attempted_partition: None,
            etags: BTreeMap::new(),
            revisions: BTreeMap::new(),
            object_keys: BTreeMap::new(),
            unknown_objects: 0,
            count: 0,
        };
        let prefix = store.prefix(b'e');
        let (mut start, end) = dexzd_keyspace::row_keys::prefix_range(&prefix);
        loop {
            let rows = store.local.scan(
                ColumnFamily::NodeMeta,
                Some(&start),
                Some(&end),
                ScanDirection::Forward,
                256,
            )?;
            let mut has_rows = false;
            for (key, _) in rows
                .into_iter()
                .take_while(|(key, _)| key.starts_with(&prefix))
            {
                start = key.clone();
                start.push(0);
                has_rows = true;
                let offset = store.namespace.len() + 1;
                let bytes = key.get(offset..offset + 2).ok_or_else(|| {
                    PendingStoreError::Invalid("truncated pending partition key".into())
                })?;
                store.dirty.insert(PartitionId(u16::from_be_bytes(
                    bytes.try_into().expect("slice contains two bytes"),
                )));
                store
                    .known_partitions
                    .insert(PartitionId(u16::from_be_bytes(
                        bytes.try_into().expect("slice contains two bytes"),
                    )));
                store.count += 1;
            }
            if !has_rows {
                break;
            }
        }
        Ok(store)
    }

    pub(crate) fn record(&mut self, repairs: &[ProjectionRepair]) -> Result<(), PendingStoreError> {
        self.do_record(repairs)
            .map_err(|source| PendingStoreError::PartitionOperation {
                operation: "record",
                partitions: repairs
                    .iter()
                    .map(|repair| repair.partition)
                    .collect::<BTreeSet<_>>()
                    .into_iter()
                    .collect(),
                source: Box::new(source),
            })
    }

    fn do_record(&mut self, repairs: &[ProjectionRepair]) -> Result<(), PendingStoreError> {
        let mut batch = WriteBatch::new();
        let mut updates: BTreeMap<Vec<u8>, PendingRunRecord> = BTreeMap::new();
        let mut added_count = 0;
        for repair in repairs {
            let key = self.entry_key(repair.partition, repair.tenant_id, repair.run_id);
            let existing = match updates.get(&key) {
                Some(record) => Some(record.clone()),
                None => self
                    .local
                    .get(ColumnFamily::NodeMeta, &key)?
                    .map(|bytes| PendingRunRecord::decode(bytes.as_slice()))
                    .transpose()?,
            };
            let mut record = PendingRunRecord::from_repair(repair);
            if let Some(previous) = existing {
                batch.delete(
                    ColumnFamily::NodeMeta,
                    self.order_key(repair.partition, &previous),
                );
                record.priority = record.priority.min(previous.priority);
                record.revision = previous
                    .revision
                    .checked_add(1)
                    .expect("pending revision cannot exhaust u64");
            } else {
                added_count += 1;
            }
            batch.put(
                ColumnFamily::NodeMeta,
                key.as_slice(),
                record.encode_to_vec(),
            );
            batch.put(
                ColumnFamily::NodeMeta,
                self.order_key(repair.partition, &record),
                key.as_slice(),
            );
            updates.insert(key, record);
        }
        self.local.write(batch)?;
        self.count += added_count;
        self.known_partitions
            .extend(repairs.iter().map(|repair| repair.partition));
        for partition in repairs
            .iter()
            .map(|repair| repair.partition)
            .collect::<BTreeSet<_>>()
        {
            self.mark_dirty(partition);
        }
        Ok(())
    }

    #[cfg(test)]
    pub(crate) fn take_batch(
        &self,
        limit: usize,
    ) -> Result<Vec<ProjectionRepair>, PendingStoreError> {
        self.take_batch_excluding(limit, &BTreeSet::new())
    }

    pub(crate) fn take_batch_excluding(
        &self,
        limit: usize,
        excluded: &BTreeSet<PartitionId>,
    ) -> Result<Vec<ProjectionRepair>, PendingStoreError> {
        if excluded.is_empty() {
            return self.read_order_prefix(&self.prefix(b'o'), limit);
        }
        let mut repairs = Vec::new();
        for priority in 0..=3_u8 {
            for partition in self.known_partitions.difference(excluded) {
                if repairs.len() == limit {
                    return Ok(repairs);
                }
                let mut prefix = self.prefix(b'o');
                prefix.push(priority);
                prefix.extend_from_slice(&partition.0.to_be_bytes());
                repairs.extend(self.read_order_prefix(&prefix, limit - repairs.len())?);
            }
        }
        Ok(repairs)
    }

    fn read_order_prefix(
        &self,
        prefix: &[u8],
        limit: usize,
    ) -> Result<Vec<ProjectionRepair>, PendingStoreError> {
        self.local
            .scan_prefix(
                ColumnFamily::NodeMeta,
                prefix,
                ScanDirection::Forward,
                limit,
            )?
            .into_iter()
            .map(|(order, key)| {
                let encoded = self
                    .local
                    .get(ColumnFamily::NodeMeta, &key)?
                    .ok_or_else(|| {
                        PendingStoreError::Invalid("orphan pending priority index".into())
                    })?;
                let record = PendingRunRecord::decode(encoded.as_slice())?;
                let offset = self.namespace.len() + 2;
                let bytes = order
                    .get(offset..offset + 2)
                    .ok_or_else(|| PendingStoreError::Invalid("truncated priority index".into()))?;
                record.to_repair(PartitionId(u16::from_be_bytes(
                    bytes.try_into().expect("slice contains two bytes"),
                )))
            })
            .collect()
    }

    pub(crate) fn finish(&mut self, repairs: &[ProjectionRepair]) -> Result<(), PendingStoreError> {
        self.do_finish(repairs)
            .map_err(|source| PendingStoreError::PartitionOperation {
                operation: "finish",
                partitions: repairs
                    .iter()
                    .map(|repair| repair.partition)
                    .collect::<BTreeSet<_>>()
                    .into_iter()
                    .collect(),
                source: Box::new(source),
            })
    }

    fn do_finish(&mut self, repairs: &[ProjectionRepair]) -> Result<(), PendingStoreError> {
        let mut batch = WriteBatch::new();
        let mut removed_count = 0;
        let mut changed_partitions = BTreeSet::new();
        for repair in repairs {
            let key = self.entry_key(repair.partition, repair.tenant_id, repair.run_id);
            if let Some(encoded) = self.local.get(ColumnFamily::NodeMeta, &key)? {
                let record = PendingRunRecord::decode(encoded.as_slice())?;
                if record.revision != repair.generation {
                    continue;
                }
                batch.delete(
                    ColumnFamily::NodeMeta,
                    self.order_key(repair.partition, &record),
                );
                batch.delete(ColumnFamily::NodeMeta, key);
                removed_count += 1;
                changed_partitions.insert(repair.partition);
            }
        }
        self.local.write(batch)?;
        self.count -= removed_count;
        for partition in changed_partitions {
            self.mark_dirty(partition);
        }
        Ok(())
    }

    pub(crate) fn has_pending_runs(
        &self,
        partition: PartitionId,
    ) -> Result<bool, PendingStoreError> {
        let mut prefix = self.prefix(b'e');
        prefix.extend_from_slice(&partition.0.to_be_bytes());
        self.local
            .scan_prefix(ColumnFamily::NodeMeta, &prefix, ScanDirection::Forward, 1)
            .map(|rows| !rows.is_empty())
            .map_err(|source| PendingStoreError::PartitionOperation {
                operation: "check pending runs for",
                partitions: vec![partition],
                source: Box::new(source.into()),
            })
    }

    pub(crate) fn count(&self) -> usize {
        self.count
    }

    pub(crate) fn has_unsynced(&self, partition: PartitionId) -> bool {
        self.dirty.contains(&partition) || self.blocked_partitions.contains(&partition)
    }

    pub(crate) fn finish_manual_rebuild(&mut self, partition: PartitionId) {
        self.blocked_partitions.remove(&partition);
        self.mark_dirty(partition);
    }

    pub(super) fn version_spool(&self, partition: PartitionId) -> (Arc<dyn LocalStore>, Vec<u8>) {
        let mut prefix = self.prefix(b'v');
        prefix.extend_from_slice(&partition.0.to_be_bytes());
        (self.local.clone(), prefix)
    }

    fn mark_dirty(&mut self, partition: PartitionId) {
        self.dirty.insert(partition);
        let revision = self.revisions.entry(partition).or_default();
        *revision = revision
            .checked_add(1)
            .expect("partition revision cannot exhaust u64");
    }

    fn prefix(&self, kind: u8) -> Vec<u8> {
        let mut key = self.namespace.clone();
        key.push(kind);
        key
    }

    fn entry_key(&self, partition: PartitionId, tenant: TenantId, run: RunId) -> Vec<u8> {
        let mut key = self.prefix(b'e');
        key.extend_from_slice(&partition.0.to_be_bytes());
        key.extend_from_slice(&tenant.0.to_be_bytes());
        key.extend_from_slice(&run.0);
        key
    }

    fn order_key(&self, partition: PartitionId, record: &PendingRunRecord) -> Vec<u8> {
        let mut key = self.prefix(b'o');
        key.push(u8::try_from(record.priority).expect("validated priority fits u8"));
        key.extend_from_slice(&partition.0.to_be_bytes());
        key.extend_from_slice(&record.tenant_id.to_be_bytes());
        key.extend_from_slice(&record.run_id);
        key
    }
}

impl PendingRunRecord {
    fn from_repair(repair: &ProjectionRepair) -> Self {
        Self {
            tenant_id: repair.tenant_id.0,
            run_id: repair.run_id.0.to_vec(),
            flow_hash: repair.flow_hash.0,
            revision: 1,
            priority: match repair.priority {
                RepairPriority::Start => 0,
                RepairPriority::Close => 1,
                RepairPriority::Intermediate => 2,
                RepairPriority::Delete => 3,
            },
        }
    }

    fn to_repair(&self, partition: PartitionId) -> Result<ProjectionRepair, PendingStoreError> {
        let run = RunId(self.run_id.as_slice().try_into().map_err(|_| {
            PendingStoreError::Invalid("pending run ID must contain 16 bytes".into())
        })?);
        let priority = match self.priority {
            0 => RepairPriority::Start,
            1 => RepairPriority::Close,
            2 => RepairPriority::Intermediate,
            3 => RepairPriority::Delete,
            priority => {
                return Err(PendingStoreError::Invalid(format!(
                    "unknown pending priority {priority}"
                )));
            }
        };
        let mut repair = ProjectionRepair::new(
            TenantId(self.tenant_id),
            run,
            FlowHash(self.flow_hash),
            partition,
            priority,
        );
        repair.generation = self.revision;
        Ok(repair)
    }
}

#[cfg(test)]
mod tests;
