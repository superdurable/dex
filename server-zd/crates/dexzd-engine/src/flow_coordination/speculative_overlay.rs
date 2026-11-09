// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Uncommitted commands of one Flow, layered over committed state (plan 6.4).

use std::collections::{BTreeMap, VecDeque};
use std::ops::Bound;

use dexzd_apply::{
    ApplyEffect, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
};
use dexzd_keyspace::ColumnFamily;
use dexzd_protocol::dexzd::v1::FlowCommand;
use prost::Message;

/// One proposed, uncommitted command and its speculative outcome.
#[derive(Debug, Clone)]
pub(crate) struct OverlayEntry {
    pub(crate) command_id: Vec<u8>,
    pub(crate) outcome: ApplyOutcome,
    pub(crate) size_bytes: usize,
}

impl OverlayEntry {
    /// Sizes the entry by everything retained until commit: the queued command and its outcome.
    pub(crate) fn new(command: &FlowCommand, outcome: ApplyOutcome) -> Self {
        let size_bytes = command.encoded_len()
            + outcome.writes.iter().map(write_size_bytes).sum::<usize>()
            + outcome.effects.iter().map(effect_size_bytes).sum::<usize>()
            + outcome.reply.encoded_len();
        Self {
            command_id: command.command_id.clone(),
            outcome,
            size_bytes,
        }
    }
}

/// Speculative outcomes of uncommitted commands, oldest first.
///
/// Reads use a per-key index of point writes plus a range-delete list, ordered by [`WriteStamp`].
#[derive(Debug, Default, Clone)]
pub(crate) struct SpeculativeOverlay {
    entries: VecDeque<OverlayEntry>,
    total_bytes: usize,
    /// Sequence number of the head entry; the entry at position `i` has `head_entry_seq + i`.
    head_entry_seq: u64,
    /// Stamp of the newest Put, Delete, or SingleDelete of each key.
    point_writes: BTreeMap<ColumnFamily, BTreeMap<Vec<u8>, WriteStamp>>,
    /// Range deletes, oldest first. Scanned linearly: apply emits them only on rare commands.
    range_deletes: VecDeque<RangeDeleteWrite>,
}

/// Position of one overlay write: its entry's sequence number, then its index in the entry.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
struct WriteStamp {
    entry_seq: u64,
    write_index: usize,
}

/// One `StateWrite::DeleteRange` of an overlay entry.
#[derive(Debug, Clone)]
struct RangeDeleteWrite {
    stamp: WriteStamp,
    column_family: ColumnFamily,
    start: Vec<u8>,
    end: Vec<u8>,
}

/// Result of matching a committed command against the overlay.
#[derive(Debug)]
pub(crate) enum CommittedMatch {
    /// The oldest entry belongs to the committed command; it was removed.
    Head(OverlayEntry),
    /// The command was never proposed through this overlay (for example, replayed in recovery).
    Foreign,
    /// The command is in the overlay but not at its head: commit order differs from proposal
    /// order.
    OutOfOrder,
}

impl SpeculativeOverlay {
    pub(crate) fn len(&self) -> usize {
        self.entries.len()
    }

    pub(crate) fn is_empty(&self) -> bool {
        self.entries.is_empty()
    }

    #[cfg(test)]
    pub(crate) fn total_bytes(&self) -> usize {
        self.total_bytes
    }

    pub(crate) fn push(&mut self, entry: OverlayEntry) {
        let entry_seq = self.head_entry_seq
            + u64::try_from(self.entries.len()).expect("overlay length fits in u64");
        for (write_index, write) in entry.outcome.writes.iter().enumerate() {
            let stamp = WriteStamp {
                entry_seq,
                write_index,
            };
            match write {
                StateWrite::Put {
                    column_family, key, ..
                }
                | StateWrite::Delete { column_family, key }
                | StateWrite::SingleDelete { column_family, key } => {
                    self.point_writes
                        .entry(*column_family)
                        .or_default()
                        .insert(key.clone(), stamp);
                }
                StateWrite::DeleteRange {
                    column_family,
                    start,
                    end,
                } => self.range_deletes.push_back(RangeDeleteWrite {
                    stamp,
                    column_family: *column_family,
                    start: start.clone(),
                    end: end.clone(),
                }),
            }
        }
        self.total_bytes += entry.size_bytes;
        self.entries.push_back(entry);
    }

    /// Removes the head entry when it belongs to `command_id`.
    pub(crate) fn take_committed(&mut self, command_id: &[u8]) -> CommittedMatch {
        match self.entries.front() {
            Some(head) if head.command_id == command_id => {
                let entry = self.entries.pop_front().expect("front exists");
                self.remove_head_from_index(&entry);
                self.total_bytes -= entry.size_bytes;
                CommittedMatch::Head(entry)
            }
            _ if self
                .entries
                .iter()
                .any(|entry| entry.command_id == command_id) =>
            {
                CommittedMatch::OutOfOrder
            }
            _ => CommittedMatch::Foreign,
        }
    }

    /// Returns a reader that layers the overlay over `committed`.
    pub(crate) fn reader<'a>(
        &'a self,
        committed: &'a dyn FlowStateReader,
    ) -> OverlayStateReader<'a> {
        OverlayStateReader {
            overlay: self,
            committed,
        }
    }

    /// Drops the index records of the just-popped head `entry`; FIFO commits leave none older.
    fn remove_head_from_index(&mut self, entry: &OverlayEntry) {
        let entry_seq = self.head_entry_seq;
        for write in &entry.outcome.writes {
            if let StateWrite::Put {
                column_family, key, ..
            }
            | StateWrite::Delete { column_family, key }
            | StateWrite::SingleDelete { column_family, key } = write
                && let Some(keys) = self.point_writes.get_mut(column_family)
                && keys
                    .get(key.as_slice())
                    .is_some_and(|stamp| stamp.entry_seq == entry_seq)
            {
                keys.remove(key.as_slice());
                if keys.is_empty() {
                    self.point_writes.remove(column_family);
                }
            }
        }
        while self
            .range_deletes
            .front()
            .is_some_and(|range_delete| range_delete.stamp.entry_seq == entry_seq)
        {
            self.range_deletes.pop_front();
        }
        self.head_entry_seq += 1;
    }

    /// Returns the newest overlay decision for `key`.
    fn resolve(&self, column_family: ColumnFamily, key: &[u8]) -> OverlayResolution<'_> {
        let point_stamp = self
            .point_writes
            .get(&column_family)
            .and_then(|keys| keys.get(key))
            .copied();
        self.resolve_with_point_stamp(column_family, key, point_stamp)
    }

    /// Returns the decision for `key` given the stamp of its newest point write, if any.
    fn resolve_with_point_stamp(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
        point_stamp: Option<WriteStamp>,
    ) -> OverlayResolution<'_> {
        if self.has_newer_covering_range_delete(column_family, key, point_stamp) {
            return OverlayResolution::Deleted;
        }
        let Some(point_stamp) = point_stamp else {
            return OverlayResolution::Untouched;
        };
        record_examined_overlay_write();
        let entry_offset = usize::try_from(point_stamp.entry_seq - self.head_entry_seq)
            .expect("indexed entries are within the overlay");
        match &self.entries[entry_offset].outcome.writes[point_stamp.write_index] {
            StateWrite::Put { value, .. } => OverlayResolution::Present(value),
            StateWrite::Delete { .. } | StateWrite::SingleDelete { .. } => {
                OverlayResolution::Deleted
            }
            StateWrite::DeleteRange { .. } => {
                unreachable!("the point index holds only Put, Delete, and SingleDelete writes")
            }
        }
    }

    /// Returns whether a range delete newer than `point_stamp` covers `key`.
    fn has_newer_covering_range_delete(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
        point_stamp: Option<WriteStamp>,
    ) -> bool {
        self.range_deletes
            .iter()
            .rev()
            .take_while(|range_delete| point_stamp.is_none_or(|stamp| range_delete.stamp > stamp))
            .any(|range_delete| {
                record_examined_overlay_write();
                range_delete.column_family == column_family
                    && key >= range_delete.start.as_slice()
                    && key < range_delete.end.as_slice()
            })
    }

    /// Returns the rows in `[start, end)` that the overlay itself provides.
    fn present_rows<'a>(
        &'a self,
        column_family: ColumnFamily,
        start: &'a [u8],
        end: &'a [u8],
    ) -> impl Iterator<Item = StateRow> + 'a {
        self.point_writes
            .get(&column_family)
            .into_iter()
            .flat_map(move |keys| {
                keys.range::<[u8], _>((Bound::Included(start), Bound::Excluded(end)))
            })
            .filter_map(move |(key, stamp)| {
                match self.resolve_with_point_stamp(column_family, key, Some(*stamp)) {
                    OverlayResolution::Present(value) => Some((key.clone(), value.clone())),
                    OverlayResolution::Deleted | OverlayResolution::Untouched => None,
                }
            })
    }
}

#[cfg(test)]
thread_local! {
    static EXAMINED_OVERLAY_WRITE_COUNT: std::cell::Cell<usize> = const { std::cell::Cell::new(0) };
}

/// Counts one overlay write record read while resolving a key; a no-op outside tests.
fn record_examined_overlay_write() {
    #[cfg(test)]
    EXAMINED_OVERLAY_WRITE_COUNT.with(|count| count.set(count.get() + 1));
}

/// Returns the overlay write records this thread has examined, then resets the count.
#[cfg(test)]
pub(crate) fn take_examined_overlay_write_count_for_test() -> usize {
    EXAMINED_OVERLAY_WRITE_COUNT.with(std::cell::Cell::take)
}

enum OverlayResolution<'a> {
    Untouched,
    Present(&'a Vec<u8>),
    Deleted,
}

/// A [`FlowStateReader`] that sees committed state plus every uncommitted overlay entry.
pub(crate) struct OverlayStateReader<'a> {
    overlay: &'a SpeculativeOverlay,
    committed: &'a dyn FlowStateReader,
}

impl FlowStateReader for OverlayStateReader<'_> {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        match self.overlay.resolve(column_family, key) {
            OverlayResolution::Present(value) => Ok(Some(value.clone())),
            OverlayResolution::Deleted => Ok(None),
            OverlayResolution::Untouched => self.committed.get(column_family, key),
        }
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        let mut rows = Vec::new();
        if limit == 0 || start >= end {
            return Ok(rows);
        }
        let mut overlay_rows = self
            .overlay
            .present_rows(column_family, start, end)
            .peekable();
        let mut page_start = start.to_vec();
        let mut is_committed_exhausted = false;
        while rows.len() < limit && !is_committed_exhausted {
            let page = self
                .committed
                .scan(column_family, &page_start, end, limit)?;
            is_committed_exhausted = page.len() < limit;
            for (key, value) in page {
                page_start.clone_from(&key);
                page_start.push(0);
                while rows.len() < limit
                    && overlay_rows
                        .peek()
                        .is_some_and(|(overlay_key, _)| *overlay_key < key)
                {
                    rows.push(overlay_rows.next().expect("peeked"));
                }
                if rows.len() >= limit {
                    break;
                }
                // Overlay rows with this key are emitted from `overlay_rows` instead.
                if matches!(
                    self.overlay.resolve(column_family, &key),
                    OverlayResolution::Untouched
                ) {
                    rows.push((key, value));
                }
            }
        }
        while rows.len() < limit {
            let Some(row) = overlay_rows.next() else {
                break;
            };
            rows.push(row);
        }
        Ok(rows)
    }
}

/// A copy of a Flow's overlay over its own committed view, usable without the Flow's lock.
pub(crate) struct OwnedOverlayView {
    pub(crate) committed: Box<dyn FlowStateReader + Send>,
    pub(crate) overlay: SpeculativeOverlay,
}

impl FlowStateReader for OwnedOverlayView {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        self.overlay
            .reader(self.committed.as_ref())
            .get(column_family, key)
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        self.overlay
            .reader(self.committed.as_ref())
            .scan(column_family, start, end, limit)
    }
}

fn write_size_bytes(write: &StateWrite) -> usize {
    match write {
        StateWrite::Put { key, value, .. } => key.len() + value.len(),
        StateWrite::Delete { key, .. } | StateWrite::SingleDelete { key, .. } => key.len(),
        StateWrite::DeleteRange { start, end, .. } => start.len() + end.len(),
    }
}

fn effect_size_bytes(effect: &ApplyEffect) -> usize {
    let heap_bytes = match effect {
        ApplyEffect::InvocationPending { invocation, .. } => invocation.step_execution_id.len(),
        ApplyEffect::InvocationStarted {
            invocation,
            task_id,
            ..
        } => invocation.step_execution_id.len() + task_id.len(),
        ApplyEffect::InvocationCanceled { task_id, .. } => task_id.len(),
        ApplyEffect::LocksReleased { lock_keys, .. } => lock_keys.iter().map(String::len).sum(),
        ApplyEffect::OutboxMessagesWritten { message_ids, .. } => {
            message_ids.iter().map(Vec::len).sum()
        }
        ApplyEffect::HardDeleteObjectsPending { object_ids, .. } => {
            object_ids.iter().map(String::len).sum()
        }
        ApplyEffect::AttributeSyncPending { store_names, .. } => {
            store_names.iter().map(String::len).sum()
        }
        ApplyEffect::AttributeSyncDropped {
            store_name,
            attribute_keys,
            ..
        } => store_name.len() + attribute_keys.iter().map(String::len).sum::<usize>(),
        ApplyEffect::DispatchQueueChanged { written_keys, .. } => {
            written_keys.iter().map(Vec::len).sum()
        }
        ApplyEffect::TimerArmed { .. }
        | ApplyEffect::RunPurgeRequested { .. }
        | ApplyEffect::HistorySegmentSealed { .. }
        | ApplyEffect::HistorySegmentsExpired { .. }
        | ApplyEffect::HistoryLocalCleanupScheduled { .. }
        | ApplyEffect::PendingCloseHeldByRpcs { .. }
        | ApplyEffect::WakeWaiters { .. } => 0,
    };
    std::mem::size_of::<ApplyEffect>() + heap_bytes
}
