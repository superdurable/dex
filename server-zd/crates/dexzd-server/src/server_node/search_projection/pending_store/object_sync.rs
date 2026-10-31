// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::{ColumnFamily, PartitionId};
use dexzd_object_store::{ConditionalDeleteOutcome, WriteCondition, WriteOutcome};
use dexzd_storage::ScanDirection;
use prost::Message;

use super::{PendingRunRecord, PendingRunStore, PendingStoreError};

#[derive(Message)]
struct PendingPartitionObject {
    #[prost(uint32, tag = "1")]
    format_version: u32,
    #[prost(bytes = "vec", tag = "2")]
    cluster_id: Vec<u8>,
    #[prost(uint64, tag = "3")]
    owner_epoch: u64,
    #[prost(uint32, tag = "4")]
    partition_id: u32,
    #[prost(message, repeated, tag = "5")]
    runs: Vec<PendingRunRecord>,
}

impl PendingRunStore {
    pub(crate) async fn load_objects(&mut self) -> Result<(), PendingStoreError> {
        let mut start_after = None;
        loop {
            let listing = self
                .objects
                .list("search-pending/partition-", start_after.as_deref(), 1000)
                .await?;
            for metadata in listing.objects {
                let Some(object) = self.objects.get(&metadata.key).await? else {
                    continue;
                };
                let partition = metadata
                    .key
                    .strip_prefix("search-pending/partition-")
                    .and_then(|number| number.parse::<u16>().ok())
                    .map(PartitionId)
                    .ok_or_else(|| {
                        PendingStoreError::Invalid("invalid pending object partition key".into())
                    })?;
                let decoded = (|| {
                    let pending = PendingPartitionObject::decode(object.bytes)?;
                    let encoded_partition = self.validate_object(&pending)?;
                    if partition != encoded_partition {
                        return Err(PendingStoreError::Invalid(
                            "pending object key and partition differ".into(),
                        ));
                    }
                    pending
                        .runs
                        .iter()
                        .map(|record| record.to_repair(partition))
                        .collect::<Result<Vec<_>, _>>()
                })();
                let repairs = match decoded {
                    Ok(repairs) => repairs,
                    Err(error @ PendingStoreError::Fenced(_)) => return Err(error),
                    Err(error) => {
                        tracing::debug!(partition = partition.0, %error, "unreadable search pending object requires manual rebuilding");
                        let mut markers = self.load_rebuild_markers()?;
                        markers
                            .entry(partition)
                            .or_default()
                            .insert(dexzd_metrics::ProjectionRebuildReason::Unreadable);
                        self.store_rebuild_markers(&markers)?;
                        self.blocked_partitions.insert(partition);
                        self.etags.insert(partition, object.etag);
                        continue;
                    }
                };
                self.record(&repairs)?;
                self.etags.insert(partition, object.etag);
                self.dirty.insert(partition);
            }
            start_after = listing.next_start_after;
            if start_after.is_none() {
                return Ok(());
            }
        }
    }

    pub(crate) async fn sync_objects(&mut self, limit: usize) -> Result<usize, PendingStoreError> {
        let partitions: Vec<_> = self
            .dirty
            .iter()
            .copied()
            .filter(|partition| !self.blocked_partitions.contains(partition))
            .take(limit)
            .collect();
        let mut synced = 0;
        for partition in partitions {
            let key = object_key(partition);
            let mut prefix = self.prefix(b'e');
            prefix.extend_from_slice(&partition.0.to_be_bytes());
            let runs = self
                .local
                .scan_prefix(
                    ColumnFamily::NodeMeta,
                    &prefix,
                    ScanDirection::Forward,
                    usize::MAX,
                )?
                .into_iter()
                .map(|(_, value)| PendingRunRecord::decode(value.as_slice()))
                .collect::<Result<Vec<_>, _>>()?;
            if runs.is_empty() {
                if let Some(etag) = self.etags.get(&partition) {
                    if self.objects.delete_if_etag_matches(&key, etag).await?
                        == ConditionalDeleteOutcome::PreconditionFailed
                    {
                        return Err(PendingStoreError::ConcurrentWrite);
                    }
                    self.etags.remove(&partition);
                }
            } else {
                let condition = match self.etags.get(&partition) {
                    Some(etag) => WriteCondition::ReplaceIfEtagMatches { etag: etag.clone() },
                    None => WriteCondition::CreateOnly,
                };
                let object = PendingPartitionObject {
                    format_version: 1,
                    cluster_id: self.cluster_id.to_vec(),
                    owner_epoch: self.epoch,
                    partition_id: u32::from(partition.0),
                    runs,
                };
                match self
                    .objects
                    .put(&key, object.encode_to_vec().into(), condition)
                    .await?
                {
                    WriteOutcome::Written { etag } => {
                        self.etags.insert(partition, etag);
                    }
                    WriteOutcome::PreconditionFailed => {
                        self.refresh_conflict(partition).await?;
                        return Err(PendingStoreError::ConcurrentWrite);
                    }
                }
            }
            self.dirty.remove(&partition);
            synced += 1;
        }
        Ok(synced)
    }

    async fn refresh_conflict(&mut self, partition: PartitionId) -> Result<(), PendingStoreError> {
        match self.objects.get(&object_key(partition)).await? {
            Some(object) => {
                let pending = PendingPartitionObject::decode(object.bytes)?;
                self.validate_object(&pending)?;
                self.etags.insert(partition, object.etag);
            }
            None => {
                self.etags.remove(&partition);
            }
        }
        Ok(())
    }

    fn validate_object(
        &self,
        object: &PendingPartitionObject,
    ) -> Result<PartitionId, PendingStoreError> {
        if object.format_version != 1 || object.cluster_id.as_slice() != self.cluster_id {
            return Err(PendingStoreError::Invalid(
                "unsupported pending object format or cluster".into(),
            ));
        }
        if object.owner_epoch > self.epoch {
            return Err(PendingStoreError::Fenced(object.owner_epoch));
        }
        let partition = u16::try_from(object.partition_id).map_err(|_| {
            PendingStoreError::Invalid("pending object partition exceeds u16".into())
        })?;
        Ok(PartitionId(partition))
    }
}

fn object_key(partition: PartitionId) -> String {
    format!("search-pending/partition-{}", partition.0)
}
