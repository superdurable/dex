// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! What processes did to object storage, so coverage checks can show the risky paths ran.

use std::collections::{BTreeMap, BTreeSet};

use bytes::Bytes;

use crate::engine_simulation::simulated_object_store::{FaultEffect, InjectedFault};

/// What processes did to object storage, so coverage checks can show the risky paths ran.
#[derive(Debug, Default)]
pub(crate) struct StorageObservations {
    /// Faults by kind and by what they did to the call, counted once the call's effect is known.
    pub(crate) injected_faults: BTreeMap<(InjectedFault, FaultEffect), u64>,
    /// Conditional writes the store rejected, by key class.
    pub(crate) precondition_failed_puts: BTreeMap<&'static str, u64>,
    /// Objects deleted, by key class.
    pub(crate) deleted_objects: BTreeMap<&'static str, u64>,
    /// Snapshot manifest reads during a start, by how the start found its local store.
    pub(crate) snapshot_reads_during_start: BTreeMap<String, u64>,
    pub(crate) old_epoch: OldEpochWindows,
    /// Writes that took effect after their process died, by key class.
    pub(crate) writes_landed_after_crash: BTreeMap<&'static str, u64>,
    /// EpochStart batches written below a batch that had already landed: recovery voids or
    /// fills what lies between.
    pub(crate) epoch_starts_below_landed_batches: u64,
    /// Bodies of hard-delete objects processes sent, by key; each key is written once.
    pub(crate) hard_delete_bodies: BTreeMap<String, Bytes>,
    pub(crate) log_batches: LandedLogBatches,
}

/// Epochs of the log batches that landed, by stream incarnation and seq.
#[derive(Debug, Default)]
pub(crate) struct LandedLogBatches {
    /// Epochs of batches that carry records.
    record_batches: BTreeMap<(u32, u64), u64>,
    /// Partitions that each batch with records carries records of.
    record_partitions: BTreeMap<(u32, u64), BTreeSet<u32>>,
    epoch_starts: BTreeMap<(u32, u64), u64>,
    /// Recorded end seqs of closed streams; later batches of such a stream are not in the log.
    closed_stream_ends: BTreeMap<u32, u64>,
}

impl LandedLogBatches {
    pub(super) fn record(&mut self, incarnation: u32, seq: u64, epoch: u64, is_epoch_start: bool) {
        let batches = if is_epoch_start {
            &mut self.epoch_starts
        } else {
            &mut self.record_batches
        };
        batches.insert((incarnation, seq), epoch);
    }

    /// Records the partitions that the landed batch at `seq` of stream `incarnation` carries.
    pub(super) fn record_partitions(
        &mut self,
        incarnation: u32,
        seq: u64,
        partitions: BTreeSet<u32>,
    ) {
        self.record_partitions
            .insert((incarnation, seq), partitions);
    }

    /// Records the end seq a manifest recorded for closed stream `incarnation`.
    pub(super) fn record_closed_stream_end(&mut self, incarnation: u32, end_seq: u64) {
        self.closed_stream_ends.insert(incarnation, end_seq);
    }

    /// Returns the positions of landed batches in the log that carry records of `partition`, up
    /// to and including `through`. Void batches and batches past a closed stream's end are not
    /// in the log.
    pub(crate) fn log_batches_of(&self, partition: u32, through: (u32, u64)) -> Vec<(u32, u64)> {
        self.record_partitions
            .range(..=through)
            .filter(|(_, partitions)| partitions.contains(&partition))
            .map(|(position, _)| *position)
            .filter(|&(incarnation, seq)| {
                let Some(&epoch) = self.record_batches.get(&(incarnation, seq)) else {
                    return false;
                };
                let is_past_end = self
                    .closed_stream_ends
                    .get(&incarnation)
                    .is_some_and(|end_seq| seq > *end_seq);
                !is_past_end && !self.is_voided(incarnation, seq, epoch)
            })
            .collect()
    }

    /// Whether an EpochStart below `seq` in stream `incarnation` started an epoch newer than
    /// `epoch`.
    pub(crate) fn is_voided(&self, incarnation: u32, seq: u64, epoch: u64) -> bool {
        self.epoch_starts
            .range((incarnation, 0)..(incarnation, seq))
            .any(|(_, start_epoch)| *start_epoch > epoch)
    }

    /// Whether stream `incarnation` holds a voided batch with records in `seqs`.
    pub(crate) fn has_voided_batch(
        &self,
        incarnation: u32,
        seqs: std::ops::RangeInclusive<u64>,
    ) -> bool {
        self.record_batches
            .range((incarnation, *seqs.start())..=(incarnation, *seqs.end()))
            .any(|((_, seq), epoch)| self.is_voided(incarnation, *seq, *epoch))
    }
}

/// Old-epoch processes before and after a successor's lease write took effect.
#[derive(Debug, Default)]
pub(crate) struct OldEpochWindows {
    /// Old-epoch processes whose successor has not written the lease yet.
    awaiting_successor: BTreeSet<String>,
    /// Old-epoch processes that kept running after their successor's lease write took effect.
    superseded: BTreeSet<String>,
    /// Successor lease writes that took effect while an old-epoch process still ran.
    pub(crate) overlaps: u64,
    /// Writes superseded processes started, by key class.
    pub(crate) write_attempts: BTreeMap<&'static str, u64>,
    /// Frames superseded processes applied.
    pub(crate) frames: u64,
}

impl OldEpochWindows {
    /// Records that `process_label` keeps running while a successor starts.
    pub(crate) fn await_successor(&mut self, process_label: &str) {
        self.awaiting_successor.insert(process_label.to_owned());
    }

    /// Records a lease write of `writer` that took effect; returns the processes it superseded.
    pub(super) fn record_lease_written(&mut self, writer: &str) -> Vec<String> {
        let superseded: Vec<String> = self
            .awaiting_successor
            .iter()
            .filter(|process_label| *process_label != writer)
            .cloned()
            .collect();
        for process_label in &superseded {
            self.awaiting_successor.remove(process_label);
            self.superseded.insert(process_label.clone());
            self.overlaps += 1;
        }
        superseded
    }

    pub(super) fn record_write_attempt(&mut self, process_label: &str, key_class: &'static str) {
        if self.superseded.contains(process_label) {
            *self.write_attempts.entry(key_class).or_default() += 1;
        }
    }

    /// Counts a frame that `process_label` applied, if its successor already holds the lease.
    pub(crate) fn record_frame(&mut self, process_label: &str) {
        if self.superseded.contains(process_label) {
            self.frames += 1;
        }
    }
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeSet;

    use super::LandedLogBatches;

    #[test]
    fn the_log_excludes_void_batches_and_batches_past_a_closed_end() {
        let mut batches = LandedLogBatches::default();
        for (seq, epoch) in [(1, 1), (2, 1), (4, 1), (5, 2)] {
            batches.record(1, seq, epoch, false);
            batches.record_partitions(1, seq, BTreeSet::from([7]));
        }
        // An EpochStart of epoch 2 at seq 3 voids the epoch-1 batch at seq 4.
        batches.record(1, 3, 2, true);
        batches.record(2, 1, 2, false);
        batches.record_partitions(2, 1, BTreeSet::from([8]));
        assert_eq!(
            batches.log_batches_of(7, (2, 9)),
            vec![(1, 1), (1, 2), (1, 5)]
        );
        batches.record_closed_stream_end(1, 3);
        assert_eq!(batches.log_batches_of(7, (2, 9)), vec![(1, 1), (1, 2)]);
        assert_eq!(batches.log_batches_of(7, (1, 1)), vec![(1, 1)]);
        assert_eq!(batches.log_batches_of(8, (2, 1)), vec![(2, 1)]);
    }
}
