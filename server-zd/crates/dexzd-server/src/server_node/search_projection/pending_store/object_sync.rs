// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use dexzd_keyspace::{ColumnFamily, PartitionId};
use dexzd_object_store::{ConditionalDeleteOutcome, ObjectStore, WriteCondition, WriteOutcome};
use dexzd_storage::ScanDirection;
use prost::Message;

use super::rebuild_markers::{reason_mask, reasons_from_mask};
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
    #[prost(uint32, tag = "6")]
    rebuild_reason_mask: u32,
}

pub(crate) struct PendingObjectSync {
    objects: Arc<dyn ObjectStore>,
    partition: PartitionId,
    key: String,
    revision: u64,
    epoch: u64,
    cluster_id: [u8; 16],
    etag: Option<String>,
    bytes: Option<Vec<u8>>,
}

pub(crate) struct PendingObjectSyncResult {
    partition: PartitionId,
    revision: u64,
    etag: Option<String>,
    result: Result<(), PendingStoreError>,
}

impl PendingObjectSync {
    pub(crate) async fn execute(self) -> PendingObjectSyncResult {
        let mut etag = self.etag.clone();
        let result = self.write_object(&mut etag).await;
        PendingObjectSyncResult {
            partition: self.partition,
            revision: self.revision,
            etag,
            result,
        }
    }

    async fn write_object(&self, etag: &mut Option<String>) -> Result<(), PendingStoreError> {
        let has_conflict = if let Some(bytes) = &self.bytes {
            let condition = match etag.as_ref() {
                Some(etag) => WriteCondition::ReplaceIfEtagMatches { etag: etag.clone() },
                None => WriteCondition::CreateOnly,
            };
            match self
                .objects
                .put(&self.key, bytes.clone().into(), condition)
                .await?
            {
                WriteOutcome::Written { etag: written } => {
                    *etag = Some(written);
                    return Ok(());
                }
                WriteOutcome::PreconditionFailed => true,
            }
        } else if let Some(previous) = etag.as_ref() {
            self.objects
                .delete_if_etag_matches(&self.key, previous)
                .await?
                == ConditionalDeleteOutcome::PreconditionFailed
        } else {
            false
        };
        if has_conflict {
            match self.objects.get(&self.key).await? {
                Some(object) => {
                    let pending = PendingPartitionObject::decode(object.bytes)?;
                    validate_object(
                        &pending,
                        self.partition,
                        &self.key,
                        self.cluster_id,
                        self.epoch,
                    )?;
                    *etag = Some(object.etag);
                    return Err(PendingStoreError::ConcurrentWrite {
                        partition: self.partition,
                        key: self.key.clone(),
                    });
                }
                None => {
                    *etag = None;
                    if self.bytes.is_some() {
                        return Err(PendingStoreError::ConcurrentWrite {
                            partition: self.partition,
                            key: self.key.clone(),
                        });
                    }
                }
            }
        } else {
            *etag = None;
        }
        Ok(())
    }
}

impl PendingRunStore {
    pub(crate) async fn load_objects(&mut self) -> Result<(), PendingStoreError> {
        self.store_rebuild_markers(&Default::default())?;
        let mut start_after = None;
        loop {
            let listing = self
                .objects
                .list("search-pending/", start_after.as_deref(), 1000)
                .await?;
            for metadata in listing.objects {
                let partition = metadata
                    .key
                    .strip_prefix("search-pending/v1/partition-")
                    .or_else(|| metadata.key.strip_prefix("search-pending/partition-"))
                    .and_then(|number| number.parse::<u16>().ok())
                    .map(PartitionId);
                let Some(partition) = partition else {
                    self.unknown_objects += 1;
                    continue;
                };
                let Some(object) = self.objects.get(&metadata.key).await? else {
                    continue;
                };
                let decoded = PendingPartitionObject::decode(object.bytes.clone());
                if decoded
                    .as_ref()
                    .is_ok_and(|pending| pending.format_version != 1)
                {
                    self.unknown_objects += 1;
                    continue;
                }
                let decoded = decoded
                    .map_err(PendingStoreError::from)
                    .and_then(|pending| {
                        validate_object(
                            &pending,
                            partition,
                            &metadata.key,
                            self.cluster_id,
                            self.epoch,
                        )?;
                        let repairs = pending
                            .runs
                            .iter()
                            .map(|record| record.to_repair(partition))
                            .collect::<Result<Vec<_>, _>>()?;
                        let mask = u8::try_from(pending.rebuild_reason_mask).map_err(|_| {
                            PendingStoreError::Invalid("invalid object reason mask".into())
                        })?;
                        Ok((repairs, reasons_from_mask(mask)))
                    });
                self.object_keys.insert(partition, metadata.key);
                self.etags.insert(partition, object.etag);
                let (repairs, reasons) = match decoded {
                    Ok(decoded) => decoded,
                    Err(error @ PendingStoreError::Fenced { .. }) => return Err(error),
                    Err(error) => {
                        tracing::debug!(partition = partition.0, %error, "unreadable search pending object requires manual rebuilding");
                        let mut markers = self.load_rebuild_markers()?;
                        markers
                            .entry(partition)
                            .or_default()
                            .insert(dexzd_metrics::ProjectionRebuildReason::Unreadable);
                        self.store_rebuild_markers(&markers)?;
                        self.blocked_partitions.insert(partition);
                        continue;
                    }
                };
                self.record(&repairs)?;
                let mut markers = self.load_rebuild_markers()?;
                if !reasons.is_empty() {
                    markers.insert(partition, reasons);
                }
                self.store_rebuild_markers(&markers)?;
                self.mark_dirty(partition);
                // Claim each loaded object before examining another partition.
                let snapshot = self.snapshot_partition(partition)?;
                let result = snapshot.execute().await;
                self.reconcile_sync(result)?;
            }
            start_after = listing.next_start_after;
            if start_after.is_none() {
                return Ok(());
            }
        }
    }

    pub(crate) fn snapshot_sync(
        &mut self,
        limit: usize,
    ) -> Result<Vec<PendingObjectSync>, PendingStoreError> {
        let mut partitions: Vec<_> = self
            .dirty
            .iter()
            .copied()
            .filter(|partition| !self.blocked_partitions.contains(partition))
            .collect();
        if let Some(previous) = self.last_attempted_partition {
            let start = partitions.partition_point(|partition| *partition <= previous);
            partitions.rotate_left(start);
        }
        partitions.truncate(limit);
        let mut snapshots = Vec::new();
        for partition in partitions {
            self.last_attempted_partition = Some(partition);
            snapshots.push(self.snapshot_partition(partition)?);
        }
        Ok(snapshots)
    }

    fn snapshot_partition(
        &self,
        partition: PartitionId,
    ) -> Result<PendingObjectSync, PendingStoreError> {
        let reasons = self
            .load_rebuild_markers()?
            .remove(&partition)
            .unwrap_or_default();
        let mut bytes = PendingPartitionObject {
            format_version: 1,
            cluster_id: self.cluster_id.to_vec(),
            owner_epoch: self.epoch,
            partition_id: u32::from(partition.0),
            runs: Vec::new(),
            rebuild_reason_mask: u32::from(reason_mask(&reasons)),
        }
        .encode_to_vec();
        let mut prefix = self.prefix(b'e');
        prefix.extend_from_slice(&partition.0.to_be_bytes());
        let mut start = prefix.clone();
        let mut has_runs = false;
        loop {
            let rows = self.local.scan(
                ColumnFamily::NodeMeta,
                Some(&start),
                None,
                ScanDirection::Forward,
                256,
            )?;
            let mut has_rows = false;
            for (key, value) in rows
                .into_iter()
                .take_while(|(key, _)| key.starts_with(&prefix))
            {
                start = key;
                start.push(0);
                has_rows = true;
                has_runs = true;
                let record = PendingRunRecord::decode(value.as_slice())?;
                bytes.push(42);
                record
                    .encode_length_delimited(&mut bytes)
                    .expect("Vec encoding cannot fail");
            }
            if !has_rows {
                break;
            }
        }
        Ok(PendingObjectSync {
            objects: self.objects.clone(),
            partition,
            key: self
                .object_keys
                .get(&partition)
                .cloned()
                .unwrap_or_else(|| object_key(partition)),
            revision: self.revisions.get(&partition).copied().unwrap_or_default(),
            epoch: self.epoch,
            cluster_id: self.cluster_id,
            etag: self.etags.get(&partition).cloned(),
            bytes: (has_runs || !reasons.is_empty()).then_some(bytes),
        })
    }

    pub(crate) fn reconcile_sync(
        &mut self,
        completed: PendingObjectSyncResult,
    ) -> Result<(), PendingStoreError> {
        match completed.etag {
            Some(etag) => {
                self.etags.insert(completed.partition, etag);
            }
            None => {
                self.etags.remove(&completed.partition);
            }
        }
        completed.result?;
        if self
            .revisions
            .get(&completed.partition)
            .copied()
            .unwrap_or_default()
            == completed.revision
        {
            self.dirty.remove(&completed.partition);
        }
        Ok(())
    }

    #[cfg(test)]
    pub(crate) async fn sync_objects(&mut self, limit: usize) -> Result<usize, PendingStoreError> {
        let snapshots = self.snapshot_sync(limit)?;
        let count = snapshots.len();
        for snapshot in snapshots {
            self.reconcile_sync(snapshot.execute().await)?;
        }
        Ok(count)
    }
}

fn validate_object(
    object: &PendingPartitionObject,
    partition: PartitionId,
    key: &str,
    cluster_id: [u8; 16],
    epoch: u64,
) -> Result<(), PendingStoreError> {
    if object.format_version != 1
        || object.cluster_id.as_slice() != cluster_id
        || object.partition_id != u32::from(partition.0)
    {
        return Err(PendingStoreError::Invalid(format!(
            "unsupported pending object {key} format, cluster, or partition"
        )));
    }
    if object.owner_epoch > epoch {
        return Err(PendingStoreError::Fenced {
            partition,
            key: key.into(),
            epoch: object.owner_epoch,
        });
    }
    Ok(())
}

fn object_key(partition: PartitionId) -> String {
    format!("search-pending/v1/partition-{}", partition.0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use tokio::io::{AsyncReadExt, AsyncWriteExt};

    #[tokio::test]
    async fn a_concurrent_delete_of_a_missing_object_releases_the_checkpoint_guard() {
        for has_conflict in [false, true] {
            let directory = tempfile::TempDir::new().unwrap();
            let objects = Arc::new(dexzd_object_store::InMemoryObjectStore::new());
            let local = Arc::new(
                dexzd_storage::RocksDbLocalStore::open(directory.path(), Default::default())
                    .unwrap(),
            );
            let mut pending = PendingRunStore::new(local, objects, [1; 16], 1).unwrap();
            let repair = super::super::super::projection_repairs::ProjectionRepair::new(
                dexzd_keyspace::TenantId(2),
                dexzd_keyspace::RunId([1; 16]),
                dexzd_keyspace::FlowHash(3),
                PartitionId(4),
                super::super::super::projection_repairs::RepairPriority::Start,
            );
            pending.record(&[repair]).unwrap();
            pending.sync_objects(1).await.unwrap();
            pending.finish(&pending.take_batch(1).unwrap()).unwrap();
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let address = listener.local_addr().unwrap();
            let responder = tokio::spawn(async move {
                let responses = if has_conflict {
                    vec![
                        ("DELETE", 409, "ConditionalRequestConflict"),
                        ("GET", 404, "NoSuchKey"),
                    ]
                } else {
                    vec![("DELETE", 404, "NoSuchKey")]
                };
                for (method, status, code) in responses {
                    let (mut stream, _) = listener.accept().await.unwrap();
                    let mut request = Vec::new();
                    loop {
                        let mut buffer = [0_u8; 1024];
                        let count = stream.read(&mut buffer).await.unwrap();
                        assert!(count > 0);
                        request.extend_from_slice(&buffer[..count]);
                        if request.windows(4).any(|window| window == b"\r\n\r\n") {
                            break;
                        }
                    }
                    assert!(request.starts_with(method.as_bytes()));
                    let body = format!("<Error><Code>{code}</Code></Error>");
                    stream.write_all(format!("HTTP/1.1 {status} Error\r\nContent-Length: {}\r\nConnection: close\r\nContent-Type: application/xml\r\n\r\n{body}", body.len()).as_bytes()).await.unwrap();
                }
            });
            let mut config = dexzd_object_store::S3ObjectStoreConfig::new("us-east-1", "pending");
            config.endpoint = Some(format!("http://{address}"));
            config.force_path_style = true;
            config.credentials = dexzd_object_store::S3Credentials::Static {
                access_key_id: "test".into(),
                secret_access_key: "test".into(),
                session_token: None,
            };
            let mut snapshot = pending.snapshot_sync(1).unwrap().pop().unwrap();
            snapshot.objects = Arc::new(
                dexzd_object_store::S3ObjectStore::new(config)
                    .await
                    .unwrap(),
            );
            let completed =
                tokio::time::timeout(std::time::Duration::from_secs(10), snapshot.execute())
                    .await
                    .unwrap();
            pending.reconcile_sync(completed).unwrap();
            responder.await.unwrap();
            assert!(!pending.has_unsynced(PartitionId(4)));
            assert!(!pending.etags.contains_key(&PartitionId(4)));
        }
    }
}
