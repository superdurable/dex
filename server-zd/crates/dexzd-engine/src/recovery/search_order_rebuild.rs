// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Startup rebuild of the SearchOrder index for partitions that an earlier binary wrote.

use dexzd_apply::read_model::search_order_index_keys_of_document;
use dexzd_keyspace::row_keys::{
    SEARCH_ORDER_INDEX_VERSION, decode_flow_run_after_prefix, partition_index_range,
    run_singleton_key,
};
use dexzd_keyspace::{ColumnFamily, IndexKind, PartitionId, RunTable};
use dexzd_protocol::dexzd::v1::{PartitionMeta, SearchDocumentRecord};
use dexzd_storage::{LocalReadView, LocalStore, ScanDirection, StorageError, WriteBatch};
use prost::Message;

use crate::commit::EngineLocalStore;
use crate::recovery::EngineStartError;

const REBUILD_SCAN_BATCH: usize = 1_024;
/// Bytes of `partition(2) ‖ kind(1) ‖ tenant_id(8)` before a SearchDocument index identity.
const SEARCH_DOCUMENT_IDENTITY_OFFSET: usize = 11;

/// Rebuilds the SearchOrder index of every partition whose metadata records an older index
/// version. It must run before log replay, so no command applies concurrently. The rebuild
/// replaces the partition's whole index, so rerunning it after a crash is safe.
///
/// Returns the number of rebuilt partitions, or a storage error when the local store cannot be
/// read or written or a SearchDocument index row has no document.
pub(crate) fn rebuild_search_order_indexes(
    store: &EngineLocalStore,
) -> Result<usize, EngineStartError> {
    let mut rebuilt_partition_count = 0;
    for meta in store.partition_metas()? {
        if meta.search_order_index_version >= SEARCH_ORDER_INDEX_VERSION {
            continue;
        }
        let partition = PartitionId(u16::try_from(meta.partition).map_err(|_| {
            EngineStartError::InvalidMetadata {
                detail: format!("local store names partition {}", meta.partition),
            }
        })?);
        let document_count = rebuild_partition_search_order(store, partition)?;
        let mut batch = WriteBatch::new();
        batch.put_partition_meta(&PartitionMeta {
            search_order_index_version: SEARCH_ORDER_INDEX_VERSION,
            ..meta
        })?;
        store.write(batch)?;
        tracing::debug!(%partition, document_count, "rebuilt search order index");
        rebuilt_partition_count += 1;
    }
    Ok(rebuilt_partition_count)
}

fn rebuild_partition_search_order(
    store: &EngineLocalStore,
    partition: PartitionId,
) -> Result<usize, EngineStartError> {
    let (search_order_start, search_order_end) =
        partition_index_range(partition, IndexKind::SearchOrder);
    let mut batch = WriteBatch::new();
    batch.delete_range(
        ColumnFamily::PartitionIndex,
        search_order_start,
        search_order_end,
    );
    store.write(batch)?;
    let (mut start, end) = partition_index_range(partition, IndexKind::SearchDocument);
    let mut document_count = 0;
    loop {
        let rows = store.scan(
            ColumnFamily::PartitionIndex,
            Some(&start),
            Some(&end),
            ScanDirection::Forward,
            REBUILD_SCAN_BATCH,
        )?;
        let Some((last_key, _)) = rows.last() else {
            return Ok(document_count);
        };
        let mut next_start = last_key.clone();
        next_start.push(0);
        let mut batch = WriteBatch::new();
        for (index_key, _) in &rows {
            let (flow_hash, run_id) =
                decode_flow_run_after_prefix(index_key, SEARCH_DOCUMENT_IDENTITY_OFFSET)
                    .ok_or_else(|| {
                        StorageError::Invalid(format!(
                            "malformed search document index key in partition {partition}"
                        ))
                    })?;
            let document_key = run_singleton_key(flow_hash, run_id, RunTable::SearchDocument);
            let encoded = store.get(ColumnFamily::Flow, &document_key)?.ok_or_else(|| {
                StorageError::Invalid(format!(
                    "search document index of run {run_id} in partition {partition} has no document"
                ))
            })?;
            let document =
                SearchDocumentRecord::decode(encoded.as_slice()).map_err(StorageError::from)?;
            for search_order_key in
                search_order_index_keys_of_document(partition, flow_hash, run_id, &document)
            {
                batch.put(ColumnFamily::PartitionIndex, search_order_key, Vec::new());
            }
            document_count += 1;
        }
        store.write(batch)?;
        start = next_start;
    }
}

#[cfg(test)]
mod tests {
    use dexzd_apply::read_model::search_order_index_keys_of_document;
    use dexzd_keyspace::row_keys::{
        SEARCH_ORDER_INDEX_VERSION, partition_index_range, run_singleton_key, tenant_run_index_key,
    };
    use dexzd_keyspace::{
        ColumnFamily, FlowHash, IndexKind, PartitionId, RunId, RunTable, TenantId,
    };
    use dexzd_protocol::dex::FlowStatus;
    use dexzd_protocol::dexzd::v1::{LogPosition, PartitionMeta, SearchDocumentRecord};
    use dexzd_storage::{
        InMemoryLocalStore, LocalReadView, LocalStore, RestoredLocalStore, ScanDirection,
        WriteBatch,
    };
    use prost::Message;

    use super::rebuild_search_order_indexes;

    fn document(ordinal: u8, status: FlowStatus, closed_at_micros: i64) -> SearchDocumentRecord {
        SearchDocumentRecord {
            version: 3,
            tenant_id: TenantId::DEFAULT.0,
            flow_id: format!("flow-{ordinal}"),
            run_id: vec![ordinal; 16],
            flow_type: "OrderFlow".into(),
            status: status as i32,
            started_at_micros: i64::from(ordinal) * 10,
            closed_at_micros,
            ..SearchDocumentRecord::default()
        }
    }

    fn put_run(batch: &mut WriteBatch, partition: PartitionId, record: &SearchDocumentRecord) {
        let run_id = RunId::from_slice(&record.run_id).expect("fixture run ID");
        let flow_hash = FlowHash((u64::from(partition.0) << 60) | u64::from(record.run_id[0]));
        batch.put(
            ColumnFamily::Flow,
            run_singleton_key(flow_hash, run_id, RunTable::SearchDocument),
            record.encode_to_vec(),
        );
        batch.put(
            ColumnFamily::PartitionIndex,
            tenant_run_index_key(
                partition,
                IndexKind::SearchDocument,
                TenantId::DEFAULT,
                flow_hash,
                run_id,
            ),
            record.version.to_be_bytes().to_vec(),
        );
    }

    fn search_order_rows(store: &RestoredLocalStore, partition: PartitionId) -> Vec<Vec<u8>> {
        let (start, end) = partition_index_range(partition, IndexKind::SearchOrder);
        store
            .scan(
                ColumnFamily::PartitionIndex,
                Some(&start),
                Some(&end),
                ScanDirection::Forward,
                usize::MAX,
            )
            .expect("scan search order rows")
            .into_iter()
            .map(|(key, _)| key)
            .collect()
    }

    #[test]
    fn rebuilds_partitions_of_an_older_index_version_once() {
        let store = RestoredLocalStore::Memory(InMemoryLocalStore::new());
        let stale = PartitionId(0);
        let current = PartitionId(1);
        let running = document(1, FlowStatus::Running, 0);
        let closed = document(2, FlowStatus::Completed, 99);
        let stale_meta = PartitionMeta {
            partition: 0,
            applied: Some(LogPosition {
                node_id: "node-a".into(),
                incarnation: 1,
                seq: 42,
            }),
            last_command_timestamp_micros: 1_234,
            last_behavior_version: 1,
            applied_epoch: 3,
            search_order_index_version: 0,
        };
        let mut batch = WriteBatch::new();
        put_run(&mut batch, stale, &running);
        put_run(&mut batch, stale, &closed);
        put_run(&mut batch, current, &document(3, FlowStatus::Running, 0));
        let (stale_start, _) = partition_index_range(stale, IndexKind::SearchOrder);
        let mut leftover_key = stale_start;
        leftover_key.extend_from_slice(&[0xee; 8]);
        batch.put(ColumnFamily::PartitionIndex, leftover_key, Vec::new());
        batch
            .put_partition_meta(&stale_meta)
            .expect("stale partition metadata");
        batch
            .put_partition_meta(&PartitionMeta {
                partition: 1,
                search_order_index_version: SEARCH_ORDER_INDEX_VERSION,
                ..PartitionMeta::default()
            })
            .expect("current partition metadata");
        store.write(batch).expect("seed the store");

        assert_eq!(rebuild_search_order_indexes(&store).expect("rebuild"), 1);
        let mut expected: Vec<Vec<u8>> = [&running, &closed]
            .into_iter()
            .flat_map(|record| {
                let run_id = RunId::from_slice(&record.run_id).expect("fixture run ID");
                let flow_hash = FlowHash(u64::from(record.run_id[0]));
                search_order_index_keys_of_document(stale, flow_hash, run_id, record)
            })
            .collect();
        expected.sort();
        assert_eq!(search_order_rows(&store, stale), expected);
        assert!(search_order_rows(&store, current).is_empty());
        assert_eq!(
            store.partition_meta(stale).expect("read metadata"),
            Some(PartitionMeta {
                search_order_index_version: SEARCH_ORDER_INDEX_VERSION,
                ..stale_meta
            })
        );
        assert_eq!(rebuild_search_order_indexes(&store).expect("rerun"), 0);
        assert_eq!(search_order_rows(&store, stale), expected);
    }
}
