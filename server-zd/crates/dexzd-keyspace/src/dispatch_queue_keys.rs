// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Keys of the DispatchQueue partition index: one row per pending Step attempt that waits for a
//! Worker. Apply writes and deletes them; the engine seeks them for matching.

use crate::row_keys::{
    length_prefixed_name, ordered_timestamp_bytes, timestamp_from_ordered_bytes,
};
use crate::{FlowHash, IndexKind, PartitionId, RunId, TenantId, partition_index_prefix};

/// Priority byte of an ordinary pending Step attempt.
pub const DEFAULT_DISPATCH_PRIORITY: u8 = 1;
/// Priority byte of the Flow timeout handler, which goes ahead of the backlog (plan D16).
pub const TIMEOUT_HANDLER_DISPATCH_PRIORITY: u8 = 0;

/// One durable task queue: a Step type of one tenant's Flow type (plan D55).
#[derive(Debug, Clone, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct DispatchQueueName {
    /// Tenant of the Flow.
    pub tenant_id: TenantId,
    /// Flow type of the run.
    pub flow_type: String,
    /// Step type of the attempt.
    pub step_type: String,
}

/// Where a row sorts inside its queue. Field order is key order, so the derived order equals
/// the byte order of the encoded key.
#[derive(Debug, Clone, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct DispatchQueuePosition {
    /// [`TIMEOUT_HANDLER_DISPATCH_PRIORITY`] or [`DEFAULT_DISPATCH_PRIORITY`].
    pub priority: u8,
    /// When the attempt became pending, in microseconds since the Unix epoch.
    pub pending_since_micros: i64,
    /// Hash of the run's Flow.
    pub flow_hash: FlowHash,
    /// The run.
    pub run_id: RunId,
    /// StepMethod number of the attempt.
    pub method: u8,
    /// Attempt number.
    pub attempt: u32,
    /// Step execution of the attempt; the unprefixed tail of the key.
    pub step_execution_id: String,
}

/// A decoded DispatchQueue key.
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct DispatchQueueEntry {
    /// Partition that owns the row.
    pub partition: PartitionId,
    /// The row's queue.
    pub queue: DispatchQueueName,
    /// The row's place in its queue.
    pub position: DispatchQueuePosition,
}

/// Returns `partition ‖ DispatchQueue`, the prefix of every queue of one partition.
pub fn dispatch_queue_partition_prefix(partition: PartitionId) -> Vec<u8> {
    partition_index_prefix(partition, IndexKind::DispatchQueue)
}

/// Returns the prefix of every row of one queue of one partition.
pub fn dispatch_queue_prefix(partition: PartitionId, queue: &DispatchQueueName) -> Vec<u8> {
    let mut key = dispatch_queue_partition_prefix(partition);
    key.extend_from_slice(&queue.tenant_id.to_be_bytes());
    key.extend_from_slice(&length_prefixed_name(&queue.flow_type));
    key.extend_from_slice(&length_prefixed_name(&queue.step_type));
    key
}

/// Returns the prefix of one priority of one queue of one partition.
pub fn dispatch_queue_priority_prefix(
    partition: PartitionId,
    queue: &DispatchQueueName,
    priority: u8,
) -> Vec<u8> {
    let mut key = dispatch_queue_prefix(partition, queue);
    key.push(priority);
    key
}

/// Returns the key of one row.
pub fn dispatch_queue_key(entry: &DispatchQueueEntry) -> Vec<u8> {
    let position = &entry.position;
    let mut key = dispatch_queue_priority_prefix(entry.partition, &entry.queue, position.priority);
    key.extend_from_slice(&ordered_timestamp_bytes(position.pending_since_micros));
    key.extend_from_slice(&position.flow_hash.to_be_bytes());
    key.extend_from_slice(position.run_id.as_bytes());
    key.push(position.method);
    key.extend_from_slice(&position.attempt.to_be_bytes());
    key.extend_from_slice(position.step_execution_id.as_bytes());
    key
}

/// Decodes a DispatchQueue key.
///
/// Returns `None` for a key of another index or a malformed key.
pub fn decode_dispatch_queue_key(key: &[u8]) -> Option<DispatchQueueEntry> {
    let mut reader = KeyReader { rest: key };
    let partition = PartitionId(u16::from_be_bytes(reader.take_array()?));
    if reader.take_array::<1>()?[0] != IndexKind::DispatchQueue as u8 {
        return None;
    }
    let tenant_id = TenantId(u64::from_be_bytes(reader.take_array()?));
    let flow_type = reader.take_name()?;
    let step_type = reader.take_name()?;
    let priority = reader.take_array::<1>()?[0];
    let pending_since_micros = timestamp_from_ordered_bytes(reader.take_array()?);
    let flow_hash = FlowHash(u64::from_be_bytes(reader.take_array()?));
    let run_id = RunId(reader.take_array()?);
    let method = reader.take_array::<1>()?[0];
    let attempt = u32::from_be_bytes(reader.take_array()?);
    let step_execution_id = String::from_utf8(reader.rest.to_vec()).ok()?;
    Some(DispatchQueueEntry {
        partition,
        queue: DispatchQueueName {
            tenant_id,
            flow_type,
            step_type,
        },
        position: DispatchQueuePosition {
            priority,
            pending_since_micros,
            flow_hash,
            run_id,
            method,
            attempt,
            step_execution_id,
        },
    })
}

struct KeyReader<'key> {
    rest: &'key [u8],
}

impl KeyReader<'_> {
    fn take_array<const LENGTH: usize>(&mut self) -> Option<[u8; LENGTH]> {
        let (head, tail) = self.rest.split_at_checked(LENGTH)?;
        self.rest = tail;
        head.try_into().ok()
    }

    fn take_name(&mut self) -> Option<String> {
        let length = usize::from(u16::from_be_bytes(self.take_array()?));
        let (name, tail) = self.rest.split_at_checked(length)?;
        self.rest = tail;
        String::from_utf8(name.to_vec()).ok()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn entry(tenant: u64, flow_type: &str, pending_since_micros: i64) -> DispatchQueueEntry {
        DispatchQueueEntry {
            partition: PartitionId(3),
            queue: DispatchQueueName {
                tenant_id: TenantId(tenant),
                flow_type: flow_type.to_owned(),
                step_type: "Charge".to_owned(),
            },
            position: DispatchQueuePosition {
                priority: DEFAULT_DISPATCH_PRIORITY,
                pending_since_micros,
                flow_hash: FlowHash(9),
                run_id: RunId([4; 16]),
                method: 2,
                attempt: 1,
                step_execution_id: "Charge-1".to_owned(),
            },
        }
    }

    #[test]
    fn keys_round_trip_and_sort_by_position() {
        let early = entry(7, "Order", -5);
        let late = entry(7, "Order", 3);
        let early_key = dispatch_queue_key(&early);
        assert_eq!(decode_dispatch_queue_key(&early_key), Some(early.clone()));
        assert!(early_key < dispatch_queue_key(&late));
        assert!(early.position < late.position);
        let mut retry = early.clone();
        retry.position.attempt = 2;
        assert!(early_key < dispatch_queue_key(&retry));
        assert!(early.position < retry.position);
        let mut handler = late.clone();
        handler.position.priority = TIMEOUT_HANDLER_DISPATCH_PRIORITY;
        assert!(dispatch_queue_key(&handler) < early_key);
    }

    #[test]
    fn queue_prefixes_never_match_another_queue() {
        let order = entry(7, "Order", 1);
        let review = entry(7, "OrderReview", 1);
        let other_tenant = entry(9, "Order", 1);
        let prefix = dispatch_queue_prefix(order.partition, &order.queue);
        assert!(dispatch_queue_key(&order).starts_with(&prefix));
        assert!(!dispatch_queue_key(&review).starts_with(&prefix));
        assert!(!dispatch_queue_key(&other_tenant).starts_with(&prefix));
        assert_eq!(decode_dispatch_queue_key(&prefix), None);
    }
}
