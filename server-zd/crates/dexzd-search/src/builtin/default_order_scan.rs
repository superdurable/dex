// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Default-order SearchFlows over the SearchOrder index: a lazy k-way merge of every
//! partition's keys that stops once the page is full.

use std::cmp::{Ordering, Reverse};
use std::collections::{BinaryHeap, VecDeque};

use dexzd_apply::FlowStateReader;
use dexzd_keyspace::row_keys::{
    SearchOrderPosition, decode_search_order_identity, prefix_range, search_order_index_prefix,
};
use dexzd_keyspace::{ColumnFamily, PartitionId, TenantId};
use dexzd_visibility_query::{Query, compare_documents, evaluate};

use super::document::IndexedDocument;
use super::load_indexed_document;
use crate::SearchError;
use crate::page_token::PagePosition;

/// Largest number of index keys read from one partition in one scan.
const MAX_KEY_BATCH: usize = 1_024;
/// Smallest first read per partition, so a one-entry page still reads ahead once.
const MIN_FIRST_KEY_BATCH: usize = 2;
/// Bytes of `partition(2) ‖ kind(1)`, which differ between partitions of the same scope.
const PARTITION_KIND_BYTES: usize = 3;

/// One default-order search over a consistent committed view.
pub(super) struct DefaultOrderScan<'a> {
    pub(super) committed_view: &'a (dyn FlowStateReader + Send + Sync),
    pub(super) tenant_id: TenantId,
    pub(super) flow_type: Option<&'a str>,
    pub(super) query: &'a Query,
    pub(super) cursor: Option<&'a PagePosition>,
    pub(super) page_size: usize,
    pub(super) scan_budget: usize,
}

impl DefaultOrderScan<'_> {
    /// Returns up to `page_size + 1` matching documents in default order.
    ///
    /// Returns ResourceExhausted when more than the scan budget of index entries must be
    /// examined, Unavailable when the view cannot be read, and Internal for a corrupt index.
    pub(super) fn collect_page(
        &self,
        partition_count: u32,
    ) -> Result<Vec<IndexedDocument>, SearchError> {
        let mut partitions = Vec::with_capacity(partition_count as usize);
        let mut heads = BinaryHeap::new();
        // A uniform spread needs only its share per partition; busier partitions grow their batches.
        let first_batch_size = self
            .page_size
            .saturating_add(1)
            .div_ceil(partition_count as usize)
            .clamp(MIN_FIRST_KEY_BATCH, MAX_KEY_BATCH);
        for partition_number in 0..partition_count {
            let partition = PartitionId(
                u16::try_from(partition_number)
                    .expect("with_scan_budget limits the partition count to 65536"),
            );
            let mut partition_keys = self.open_partition(partition, first_batch_size);
            if let Some(key) = partition_keys.next_key(self.committed_view)? {
                heads.push(Reverse((key, partitions.len())));
            }
            partitions.push(partition_keys);
        }
        let mut page = Vec::with_capacity(self.page_size.saturating_add(1));
        let mut examined = 0_usize;
        while let Some(Reverse((key, partition_slot))) = heads.pop() {
            examined += 1;
            if examined > self.scan_budget {
                return Err(SearchError::ResourceExhausted(format!(
                    "builtin SearchFlows scan exceeds {} documents; configure ParadeDB",
                    self.scan_budget
                )));
            }
            let partition_keys = &mut partitions[partition_slot];
            if let Some(document) = self.matching_document(&key, partition_keys.prefix_length)? {
                page.push(document);
                if page.len() > self.page_size {
                    break;
                }
            }
            if let Some(next_key) = partition_keys.next_key(self.committed_view)? {
                heads.push(Reverse((next_key, partition_slot)));
            }
        }
        Ok(page)
    }

    fn open_partition(&self, partition: PartitionId, first_batch_size: usize) -> PartitionKeys {
        let prefix = search_order_index_prefix(partition, self.tenant_id, self.flow_type);
        let (mut start, end) = prefix_range(&prefix);
        if let Some(cursor) = self.cursor {
            let mut cursor_key = prefix.clone();
            cursor_key.extend_from_slice(&cursor_position(cursor).to_key_bytes());
            cursor_key.extend_from_slice(cursor.run_id.as_bytes());
            let (_, after_cursor) = prefix_range(&cursor_key);
            start = start.max(after_cursor);
        }
        PartitionKeys {
            prefix_length: prefix.len(),
            next_start: start,
            end,
            buffered: VecDeque::new(),
            batch_size: first_batch_size,
            is_exhausted: false,
        }
    }

    fn matching_document(
        &self,
        merge_key: &[u8],
        prefix_length: usize,
    ) -> Result<Option<IndexedDocument>, SearchError> {
        let (run_id, flow_hash) =
            decode_search_order_identity(merge_key, prefix_length - PARTITION_KIND_BYTES)
                .ok_or_else(|| SearchError::Internal("malformed search order index key".into()))?;
        let Some(document) =
            load_indexed_document(self.committed_view, flow_hash, run_id, self.tenant_id)?
        else {
            return Ok(None);
        };
        let is_after_cursor = self.cursor.is_none_or(|cursor| {
            compare_documents(self.query, &document, cursor) == Ordering::Greater
        });
        Ok((is_after_cursor && evaluate(self.query, &document)).then_some(document))
    }
}

/// Keys of one partition read lazily in key order.
struct PartitionKeys {
    prefix_length: usize,
    next_start: Vec<u8>,
    end: Vec<u8>,
    buffered: VecDeque<Vec<u8>>,
    batch_size: usize,
    is_exhausted: bool,
}

impl PartitionKeys {
    /// Returns the next key without its partition and kind bytes, which every partition
    /// shares within one scope, so merging on the rest is the default order.
    fn next_key(
        &mut self,
        committed_view: &(dyn FlowStateReader + Send + Sync),
    ) -> Result<Option<Vec<u8>>, SearchError> {
        if self.buffered.is_empty() && !self.is_exhausted {
            self.read_batch(committed_view)?;
        }
        Ok(self
            .buffered
            .pop_front()
            .map(|key| key[PARTITION_KIND_BYTES..].to_vec()))
    }

    fn read_batch(
        &mut self,
        committed_view: &(dyn FlowStateReader + Send + Sync),
    ) -> Result<(), SearchError> {
        let rows = committed_view
            .scan(
                ColumnFamily::PartitionIndex,
                &self.next_start,
                &self.end,
                self.batch_size,
            )
            .map_err(|error| SearchError::Unavailable(error.to_string()))?;
        let Some((last_key, _)) = rows.last() else {
            self.is_exhausted = true;
            return Ok(());
        };
        if *last_key < self.next_start {
            return Err(SearchError::Internal(
                "search order index scan regressed".into(),
            ));
        }
        self.next_start = last_key.clone();
        self.next_start.push(0);
        self.buffered.extend(rows.into_iter().map(|(key, _)| key));
        self.batch_size = self.batch_size.saturating_mul(2).min(MAX_KEY_BATCH);
        Ok(())
    }
}

fn cursor_position(cursor: &PagePosition) -> SearchOrderPosition {
    SearchOrderPosition {
        is_running: cursor.is_running,
        close_time_micros: cursor.close_time_ns.map(|nanos| nanos.div_euclid(1_000)),
        start_time_micros: cursor.start_time_ns.div_euclid(1_000),
    }
}
