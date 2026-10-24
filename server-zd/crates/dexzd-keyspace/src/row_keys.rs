// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Key suffixes of run tables and keys of partition indexes. Apply writes these keys and the
//! engine and read paths scan them, so both sides must use these functions.

use crate::{
    FlowHash, IndexKind, PartitionId, RunId, RunTable, TenantId, ValueId, partition_index_key,
    partition_index_prefix, run_key,
};

/// Encodes a variable-length name as a 2-byte big-endian length and its UTF-8 bytes, so one
/// name is never a prefix match for another.
pub fn length_prefixed_name(name: &str) -> Vec<u8> {
    let length = u16::try_from(name.len()).unwrap_or(u16::MAX);
    let mut encoded = length.to_be_bytes().to_vec();
    encoded.extend_from_slice(&name.as_bytes()[..usize::from(length)]);
    encoded
}

/// Hard-delete table sub-key kind for the current object of an Attribute.
pub const HARD_DELETE_CURRENT_OBJECT: u8 = 0x00;
/// Hard-delete table sub-key kind for an object waiting for deletion.
pub const HARD_DELETE_PENDING_OBJECT: u8 = 0x01;

/// Encodes a signed timestamp so that byte order equals numeric order.
pub fn ordered_timestamp_bytes(micros: i64) -> [u8; 8] {
    ((micros as u64) ^ (1_u64 << 63)).to_be_bytes()
}

/// Decodes [`ordered_timestamp_bytes`].
pub fn timestamp_from_ordered_bytes(bytes: [u8; 8]) -> i64 {
    (u64::from_be_bytes(bytes) ^ (1_u64 << 63)) as i64
}

/// Key of a single-row run table (RunRecord, PermissionSet, SearchDocument).
pub fn run_singleton_key(flow_hash: FlowHash, run_id: RunId, table: RunTable) -> Vec<u8> {
    run_key(flow_hash, run_id, table, &[])
}

/// Key of an AttemptRecord.
pub fn attempt_record_key(flow_hash: FlowHash, run_id: RunId, attempt: u32) -> Vec<u8> {
    run_key(
        flow_hash,
        run_id,
        RunTable::AttemptRecord,
        &attempt.to_be_bytes(),
    )
}

/// Key of a row whose suffix is one name: Attribute, ChannelMeta, StepExecution, StepCounter,
/// LockTable, SubFlowResult.
pub fn run_named_key(flow_hash: FlowHash, run_id: RunId, table: RunTable, name: &str) -> Vec<u8> {
    run_key(flow_hash, run_id, table, name.as_bytes())
}

/// Key of a pending Channel message: length-prefixed channel name, enqueue seq.
pub fn channel_message_key(
    flow_hash: FlowHash,
    run_id: RunId,
    channel_name: &str,
    enqueued_seq: u64,
) -> Vec<u8> {
    let mut suffix = channel_name_prefix(channel_name);
    suffix.extend_from_slice(&enqueued_seq.to_be_bytes());
    run_key(flow_hash, run_id, RunTable::ChannelMessage, &suffix)
}

/// Suffix prefix of every message (or waiter) of one channel.
pub fn channel_name_prefix(channel_name: &str) -> Vec<u8> {
    length_prefixed_name(channel_name)
}

/// Key of a channel waiter: length-prefixed channel name, Step execution ID.
pub fn channel_waiter_key(
    flow_hash: FlowHash,
    run_id: RunId,
    channel_name: &str,
    step_execution_id: &str,
) -> Vec<u8> {
    let mut suffix = channel_name_prefix(channel_name);
    suffix.extend_from_slice(step_execution_id.as_bytes());
    run_key(flow_hash, run_id, RunTable::Waiter, &suffix)
}

/// Key of a timer row; `encoded_timer_id` is the protobuf encoding of the TimerId.
pub fn timer_key(flow_hash: FlowHash, run_id: RunId, encoded_timer_id: &[u8]) -> Vec<u8> {
    run_key(flow_hash, run_id, RunTable::Timer, encoded_timer_id)
}

/// Key of an outbox row.
pub fn outbox_key(flow_hash: FlowHash, run_id: RunId, message_id: &[u8]) -> Vec<u8> {
    run_key(flow_hash, run_id, RunTable::Outbox, message_id)
}

/// Key of an Attribute Sync outbox row: length-prefixed store name, then the Attribute key.
pub fn attribute_sync_outbox_key(
    flow_hash: FlowHash,
    run_id: RunId,
    store_name: &str,
    attribute_key: &str,
) -> Vec<u8> {
    let mut key = attribute_sync_outbox_store_prefix(flow_hash, run_id, store_name);
    key.extend_from_slice(attribute_key.as_bytes());
    key
}

/// Prefix of every Attribute Sync outbox row of one run and one store.
pub fn attribute_sync_outbox_store_prefix(
    flow_hash: FlowHash,
    run_id: RunId,
    store_name: &str,
) -> Vec<u8> {
    run_key(
        flow_hash,
        run_id,
        RunTable::AttributeSyncOutbox,
        &length_prefixed_name(store_name),
    )
}

/// Key of an inbox deduplication row.
pub fn inbox_dedup_key(
    flow_hash: FlowHash,
    run_id: RunId,
    source_run_id: &[u8],
    message_id: &[u8],
) -> Vec<u8> {
    let mut suffix = source_run_id.to_vec();
    suffix.extend_from_slice(message_id);
    run_key(flow_hash, run_id, RunTable::InboxDedup, &suffix)
}

/// Kinds of requests deduplicated by request ID. Byte values are permanent.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
#[repr(u8)]
pub enum RequestKind {
    /// StartFlow.
    StartFlow = 0x01,
    /// SetAttributes.
    SetAttributes = 0x02,
    /// DeleteChannelMessage.
    DeleteChannelMessage = 0x03,
    /// InvokeRPC.
    InvokeRpc = 0x04,
}

/// Key of a request deduplication row.
pub fn request_dedup_key(
    flow_hash: FlowHash,
    run_id: RunId,
    kind: RequestKind,
    request_id: &str,
) -> Vec<u8> {
    let mut suffix = vec![kind as u8];
    suffix.extend_from_slice(request_id.as_bytes());
    run_key(flow_hash, run_id, RunTable::RequestDedup, &suffix)
}

/// Key of a request deduplication order row (recorded seq).
pub fn request_dedup_order_key(flow_hash: FlowHash, run_id: RunId, recorded_seq: u64) -> Vec<u8> {
    run_key(
        flow_hash,
        run_id,
        RunTable::RequestDedupOrder,
        &recorded_seq.to_be_bytes(),
    )
}

/// Key of a value-table row, a value birth row, or the prefix of a value's holders.
pub fn value_row_key(
    flow_hash: FlowHash,
    run_id: RunId,
    table: RunTable,
    value_id: ValueId,
) -> Vec<u8> {
    run_key(flow_hash, run_id, table, &value_id.to_key_suffix())
}

/// Key of one holder of a value.
pub fn value_holder_key(
    flow_hash: FlowHash,
    run_id: RunId,
    value_id: ValueId,
    holder: &[u8],
) -> Vec<u8> {
    let mut suffix = value_id.to_key_suffix().to_vec();
    suffix.extend_from_slice(holder);
    run_key(flow_hash, run_id, RunTable::ValueHolder, &suffix)
}

/// Key of a garbage row: the seq at which the value lost its last holder, then the value ID.
pub fn value_garbage_key(
    flow_hash: FlowHash,
    run_id: RunId,
    lost_last_holder_seq: u64,
    value_id: ValueId,
) -> Vec<u8> {
    let mut suffix = lost_last_holder_seq.to_be_bytes().to_vec();
    suffix.extend_from_slice(&value_id.to_key_suffix());
    run_key(flow_hash, run_id, RunTable::ValueGarbage, &suffix)
}

/// Key of the current hard-delete object of an Attribute.
pub fn hard_delete_current_key(flow_hash: FlowHash, run_id: RunId, attribute_key: &str) -> Vec<u8> {
    let mut suffix = vec![HARD_DELETE_CURRENT_OBJECT];
    suffix.extend_from_slice(attribute_key.as_bytes());
    run_key(flow_hash, run_id, RunTable::HardDeleteObject, &suffix)
}

/// Key of a hard-delete object waiting for deletion.
pub fn hard_delete_pending_key(flow_hash: FlowHash, run_id: RunId, object_id: &str) -> Vec<u8> {
    let mut suffix = vec![HARD_DELETE_PENDING_OBJECT];
    suffix.extend_from_slice(object_id.as_bytes());
    run_key(flow_hash, run_id, RunTable::HardDeleteObject, &suffix)
}

/// Key of a history record.
pub fn history_record_key(flow_hash: FlowHash, run_id: RunId, seq: u64) -> Vec<u8> {
    run_key(
        flow_hash,
        run_id,
        RunTable::HistoryRecord,
        &seq.to_be_bytes(),
    )
}

/// Key of one History row's compact metadata, ordered by seq before table and attempt.
pub fn history_row_metadata_key(
    flow_hash: FlowHash,
    run_id: RunId,
    seq: u64,
    history_table: RunTable,
    attempt: u32,
) -> Vec<u8> {
    let mut suffix = seq.to_be_bytes().to_vec();
    suffix.push(history_table as u8);
    suffix.extend_from_slice(&attempt.to_be_bytes());
    run_key(flow_hash, run_id, RunTable::HistoryRowMetadata, &suffix)
}

/// Key of a checkpoint.
pub fn checkpoint_key(flow_hash: FlowHash, run_id: RunId, attempt: u32, seq: u64) -> Vec<u8> {
    let mut suffix = attempt.to_be_bytes().to_vec();
    suffix.extend_from_slice(&seq.to_be_bytes());
    run_key(flow_hash, run_id, RunTable::Checkpoint, &suffix)
}

fn flow_run_suffix(flow_hash: FlowHash, run_id: RunId) -> Vec<u8> {
    let mut suffix = flow_hash.to_be_bytes().to_vec();
    suffix.extend_from_slice(run_id.as_bytes());
    suffix
}

/// Timer index key: fire time, Flow hash, run ID, encoded TimerId.
pub fn timer_index_key(
    partition: PartitionId,
    fire_at_micros: i64,
    flow_hash: FlowHash,
    run_id: RunId,
    encoded_timer_id: &[u8],
) -> Vec<u8> {
    let mut rest = ordered_timestamp_bytes(fire_at_micros).to_vec();
    rest.extend_from_slice(&flow_run_suffix(flow_hash, run_id));
    rest.extend_from_slice(encoded_timer_id);
    partition_index_key(partition, IndexKind::Timer, &rest)
}

/// A decoded timer index key.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TimerIndexEntry {
    /// Fire time in microseconds since the Unix epoch.
    pub fire_at_micros: i64,
    /// Flow of the timer.
    pub flow_hash: FlowHash,
    /// Run of the timer.
    pub run_id: RunId,
    /// Protobuf encoding of the TimerId.
    pub encoded_timer_id: Vec<u8>,
}

/// Decodes a timer index key; returns `None` for keys of another kind or a malformed key.
pub fn decode_timer_index_key(key: &[u8]) -> Option<TimerIndexEntry> {
    if key.len() < 3 + 8 + 8 + 16 || key[2] != IndexKind::Timer as u8 {
        return None;
    }
    let fire_at_micros = timestamp_from_ordered_bytes(key[3..11].try_into().ok()?);
    let flow_hash = FlowHash(u64::from_be_bytes(key[11..19].try_into().ok()?));
    let run_id = RunId(key[19..35].try_into().ok()?);
    Some(TimerIndexEntry {
        fire_at_micros,
        flow_hash,
        run_id,
        encoded_timer_id: key[35..].to_vec(),
    })
}

/// Flow list index key: tenant, length-prefixed Flow type, status, Flow hash, run ID.
pub fn flow_list_index_key(
    partition: PartitionId,
    tenant_id: TenantId,
    flow_type: &str,
    status: u8,
    flow_hash: FlowHash,
    run_id: RunId,
) -> Vec<u8> {
    let mut rest = tenant_id.to_be_bytes().to_vec();
    rest.extend_from_slice(&length_prefixed_name(flow_type));
    rest.push(status);
    rest.extend_from_slice(&flow_run_suffix(flow_hash, run_id));
    partition_index_key(partition, IndexKind::FlowList, &rest)
}

/// Index key keyed by tenant, Flow hash, and run ID (SearchDocument, TenantRuns).
pub fn tenant_run_index_key(
    partition: PartitionId,
    kind: IndexKind,
    tenant_id: TenantId,
    flow_hash: FlowHash,
    run_id: RunId,
) -> Vec<u8> {
    let mut rest = tenant_id.to_be_bytes().to_vec();
    rest.extend_from_slice(&flow_run_suffix(flow_hash, run_id));
    partition_index_key(partition, kind, &rest)
}

/// Index key keyed by Flow hash, run ID, and an item ID (OutboxPending, HardDeletePending).
pub fn run_item_index_key(
    partition: PartitionId,
    kind: IndexKind,
    flow_hash: FlowHash,
    run_id: RunId,
    item_id: &[u8],
) -> Vec<u8> {
    let mut rest = flow_run_suffix(flow_hash, run_id);
    rest.extend_from_slice(item_id);
    partition_index_key(partition, kind, &rest)
}

/// Retention index key: deletion time, Flow hash, run ID.
pub fn retention_index_key(
    partition: PartitionId,
    delete_at_micros: i64,
    flow_hash: FlowHash,
    run_id: RunId,
) -> Vec<u8> {
    let mut rest = ordered_timestamp_bytes(delete_at_micros).to_vec();
    rest.extend_from_slice(&flow_run_suffix(flow_hash, run_id));
    partition_index_key(partition, IndexKind::Retention, &rest)
}

/// Active work index key: runs with pending or running method calls.
pub fn active_work_index_key(
    partition: PartitionId,
    flow_hash: FlowHash,
    run_id: RunId,
) -> Vec<u8> {
    partition_index_key(
        partition,
        IndexKind::ActiveWork,
        &flow_run_suffix(flow_hash, run_id),
    )
}

/// Attribute Sync pending index key: length-prefixed store name, Flow hash, run ID.
pub fn attribute_sync_pending_index_key(
    partition: PartitionId,
    store_name: &str,
    flow_hash: FlowHash,
    run_id: RunId,
) -> Vec<u8> {
    let mut key = attribute_sync_pending_store_prefix(partition, store_name);
    key.extend_from_slice(&flow_run_suffix(flow_hash, run_id));
    key
}

/// Prefix of the Attribute Sync pending index keys of one store in one partition.
pub fn attribute_sync_pending_store_prefix(partition: PartitionId, store_name: &str) -> Vec<u8> {
    partition_index_key(
        partition,
        IndexKind::AttributeSyncPending,
        &length_prefixed_name(store_name),
    )
}

/// A decoded [`attribute_sync_pending_index_key`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AttributeSyncPendingIndexEntry {
    /// Attribute Store the run has pending entries for.
    pub store_name: String,
    /// Hash of the run's Flow.
    pub flow_hash: FlowHash,
    /// The run.
    pub run_id: RunId,
}

/// Decodes an Attribute Sync pending index key; returns `None` for any other or malformed key.
pub fn decode_attribute_sync_pending_index_key(
    key: &[u8],
) -> Option<AttributeSyncPendingIndexEntry> {
    if *key.get(2)? != IndexKind::AttributeSyncPending as u8 {
        return None;
    }
    let name_length = usize::from(u16::from_be_bytes(key.get(3..5)?.try_into().ok()?));
    let name_end = 5 + name_length;
    let store_name = String::from_utf8(key.get(5..name_end)?.to_vec()).ok()?;
    let (flow_hash, run_id) = decode_flow_run_after_prefix(key, name_end)?;
    if key.len() != name_end + 24 {
        return None;
    }
    Some(AttributeSyncPendingIndexEntry {
        store_name,
        flow_hash,
        run_id,
    })
}

/// SearchOrder scope byte of keys that cover every Flow type.
pub const SEARCH_ORDER_ALL_FLOW_TYPES: u8 = 0x00;
/// SearchOrder scope byte of keys narrowed to one Flow type.
pub const SEARCH_ORDER_ONE_FLOW_TYPE: u8 = 0x01;
/// Version of the SearchOrder rows that apply maintains, recorded in `PartitionMeta`.
pub const SEARCH_ORDER_INDEX_VERSION: u32 = 1;
/// Length of [`SearchOrderPosition::to_key_bytes`].
pub const SEARCH_ORDER_POSITION_BYTES: usize = 18;
/// Length of the run ID and Flow hash that end every SearchOrder key.
pub const SEARCH_ORDER_IDENTITY_BYTES: usize = 24;

/// A run's place in the SearchFlows default order: Running runs first, then CloseTime
/// descending with absent close times last, then StartTime descending, then run ID ascending.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct SearchOrderPosition {
    /// Whether the run's status is Running.
    pub is_running: bool,
    /// Close time in microseconds since the Unix epoch, when the run has one.
    pub close_time_micros: Option<i64>,
    /// Start time in microseconds since the Unix epoch.
    pub start_time_micros: i64,
}

impl SearchOrderPosition {
    /// Encodes the position so that ascending byte order is the default order.
    pub fn to_key_bytes(&self) -> [u8; SEARCH_ORDER_POSITION_BYTES] {
        let mut bytes = [0; SEARCH_ORDER_POSITION_BYTES];
        bytes[0] = u8::from(!self.is_running);
        bytes[1] = u8::from(self.close_time_micros.is_none());
        if let Some(close_time_micros) = self.close_time_micros {
            bytes[2..10].copy_from_slice(&descending_timestamp_bytes(close_time_micros));
        }
        bytes[10..18].copy_from_slice(&descending_timestamp_bytes(self.start_time_micros));
        bytes
    }
}

fn descending_timestamp_bytes(micros: i64) -> [u8; 8] {
    ordered_timestamp_bytes(micros).map(|byte| !byte)
}

/// SearchOrder prefix of one tenant: `scope(1)`, then the length-prefixed Flow type when
/// `flow_type` narrows the scope.
pub fn search_order_index_prefix(
    partition: PartitionId,
    tenant_id: TenantId,
    flow_type: Option<&str>,
) -> Vec<u8> {
    let mut rest = tenant_id.to_be_bytes().to_vec();
    match flow_type {
        None => rest.push(SEARCH_ORDER_ALL_FLOW_TYPES),
        Some(flow_type) => {
            rest.push(SEARCH_ORDER_ONE_FLOW_TYPE);
            rest.extend_from_slice(&length_prefixed_name(flow_type));
        }
    }
    partition_index_key(partition, IndexKind::SearchOrder, &rest)
}

/// SearchOrder key: the scope prefix, the encoded position, the run ID, then the Flow hash.
/// The run ID precedes the Flow hash because the default order breaks ties by run ID.
pub fn search_order_index_key(
    partition: PartitionId,
    tenant_id: TenantId,
    flow_type: Option<&str>,
    position: &SearchOrderPosition,
    run_id: RunId,
    flow_hash: FlowHash,
) -> Vec<u8> {
    let mut key = search_order_index_prefix(partition, tenant_id, flow_type);
    key.extend_from_slice(&position.to_key_bytes());
    key.extend_from_slice(run_id.as_bytes());
    key.extend_from_slice(&flow_hash.to_be_bytes());
    key
}

/// Returns both SearchOrder keys of a run: over every Flow type, then of its own Flow type.
pub fn search_order_index_keys(
    partition: PartitionId,
    tenant_id: TenantId,
    flow_type: &str,
    position: &SearchOrderPosition,
    run_id: RunId,
    flow_hash: FlowHash,
) -> [Vec<u8>; 2] {
    [None, Some(flow_type)].map(|flow_type| {
        search_order_index_key(partition, tenant_id, flow_type, position, run_id, flow_hash)
    })
}

/// Decodes the run ID and Flow hash of a SearchOrder key whose scope prefix is
/// `prefix_length` bytes; returns `None` when the key has another length.
pub fn decode_search_order_identity(key: &[u8], prefix_length: usize) -> Option<(RunId, FlowHash)> {
    if key.len() != prefix_length + SEARCH_ORDER_POSITION_BYTES + SEARCH_ORDER_IDENTITY_BYTES {
        return None;
    }
    let identity = &key[prefix_length + SEARCH_ORDER_POSITION_BYTES..];
    let run_id = RunId(identity[..16].try_into().ok()?);
    let flow_hash = FlowHash(u64::from_be_bytes(identity[16..].try_into().ok()?));
    Some((run_id, flow_hash))
}

/// Decodes the Flow hash and run ID that follow the kind byte of a partition index key.
pub fn decode_flow_run_after_prefix(key: &[u8], prefix_length: usize) -> Option<(FlowHash, RunId)> {
    let rest = key.get(prefix_length..)?;
    let flow_hash = FlowHash(u64::from_be_bytes(rest.get(..8)?.try_into().ok()?));
    let run_id = RunId(rest.get(8..24)?.try_into().ok()?);
    Some((flow_hash, run_id))
}

/// Returns the `[start, end)` range of one index of one partition.
pub fn partition_index_range(partition: PartitionId, kind: IndexKind) -> (Vec<u8>, Vec<u8>) {
    let start = partition_index_prefix(partition, kind);
    let mut end = start.clone();
    let last = end.len() - 1;
    end[last] += 1;
    (start, end)
}

/// Returns the `[start, end)` range of keys that start with `prefix`.
pub fn prefix_range(prefix: &[u8]) -> (Vec<u8>, Vec<u8>) {
    let start = prefix.to_vec();
    let mut end = prefix.to_vec();
    while let Some(last) = end.last_mut() {
        if *last < u8::MAX {
            *last += 1;
            return (start, end);
        }
        end.pop();
    }
    (start, vec![u8::MAX; 64])
}

#[cfg(test)]
mod tests {
    use super::{
        SearchOrderPosition, attribute_sync_outbox_key, attribute_sync_outbox_store_prefix,
        attribute_sync_pending_index_key, attribute_sync_pending_store_prefix, channel_name_prefix,
        decode_attribute_sync_pending_index_key, decode_search_order_identity,
        decode_timer_index_key, ordered_timestamp_bytes, prefix_range, search_order_index_key,
        search_order_index_prefix, timer_index_key, timestamp_from_ordered_bytes,
    };
    use crate::{FlowHash, PartitionId, RunId, TenantId};

    #[test]
    fn search_order_keys_sort_in_default_order() {
        let position = |is_running, close_time_micros, start_time_micros| SearchOrderPosition {
            is_running,
            close_time_micros,
            start_time_micros,
        };
        let ordered = [
            (position(true, None, 50), RunId([1; 16])),
            (position(true, None, 50), RunId([2; 16])),
            (position(true, None, -3), RunId([0; 16])),
            (position(false, Some(90), 10), RunId([0; 16])),
            (position(false, Some(90), 5), RunId([0; 16])),
            (position(false, Some(-1), 80), RunId([0; 16])),
            (position(false, None, 99), RunId([0; 16])),
        ];
        let keys: Vec<Vec<u8>> = ordered
            .iter()
            .map(|(position, run_id)| {
                search_order_index_key(
                    PartitionId(1),
                    TenantId(7),
                    Some("Checkout"),
                    position,
                    *run_id,
                    FlowHash(u64::MAX - u64::from(run_id.0[0])),
                )
            })
            .collect();
        assert!(keys.windows(2).all(|pair| pair[0] < pair[1]));
        let prefix_length =
            search_order_index_prefix(PartitionId(1), TenantId(7), Some("Checkout")).len();
        assert_eq!(
            decode_search_order_identity(&keys[1], prefix_length),
            Some((RunId([2; 16]), FlowHash(u64::MAX - 2)))
        );
        let all_types = search_order_index_prefix(PartitionId(1), TenantId(7), None);
        assert!(!keys[0].starts_with(&all_types));
    }

    #[test]
    fn ordered_timestamps_sort_numerically() {
        let values = [i64::MIN, -5, 0, 7, i64::MAX];
        for pair in values.windows(2) {
            assert!(ordered_timestamp_bytes(pair[0]) < ordered_timestamp_bytes(pair[1]));
        }
        for value in values {
            assert_eq!(
                timestamp_from_ordered_bytes(ordered_timestamp_bytes(value)),
                value
            );
        }
    }

    #[test]
    fn timer_index_key_round_trips() {
        let key = timer_index_key(
            PartitionId(3),
            1_700_000,
            FlowHash(9),
            RunId([4; 16]),
            b"tid",
        );
        let entry = decode_timer_index_key(&key).unwrap();
        assert_eq!(entry.fire_at_micros, 1_700_000);
        assert_eq!(entry.flow_hash, FlowHash(9));
        assert_eq!(entry.run_id, RunId([4; 16]));
        assert_eq!(entry.encoded_timer_id, b"tid");
    }

    #[test]
    fn channel_prefixes_never_match_longer_names() {
        let short = channel_name_prefix("a");
        let longer = channel_name_prefix("a\u{0}b");
        assert!(!longer.starts_with(&short));
    }

    #[test]
    fn attribute_sync_keys_group_by_store() {
        let run_id = RunId([6; 16]);
        let key = attribute_sync_outbox_key(FlowHash(2), run_id, "orders", "map/a");
        assert!(key.starts_with(&attribute_sync_outbox_store_prefix(
            FlowHash(2),
            run_id,
            "orders"
        )));
        assert!(!key.starts_with(&attribute_sync_outbox_store_prefix(
            FlowHash(2),
            run_id,
            "order"
        )));
        let index_key =
            attribute_sync_pending_index_key(PartitionId(7), "orders", FlowHash(2), run_id);
        assert!(index_key.starts_with(&attribute_sync_pending_store_prefix(
            PartitionId(7),
            "orders"
        )));
        let entry = decode_attribute_sync_pending_index_key(&index_key).unwrap();
        assert_eq!(entry.store_name, "orders");
        assert_eq!(entry.flow_hash, FlowHash(2));
        assert_eq!(entry.run_id, run_id);
        assert!(
            decode_attribute_sync_pending_index_key(&index_key[..index_key.len() - 1]).is_none()
        );
    }

    #[test]
    fn prefix_range_handles_trailing_max_bytes() {
        assert_eq!(prefix_range(&[1, 2]), (vec![1, 2], vec![1, 3]));
        assert_eq!(prefix_range(&[1, 0xff]), (vec![1, 0xff], vec![2]));
    }
}
