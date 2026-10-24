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
    epoch_starts: BTreeMap<(u32, u64), u64>,
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

    /// Returns how many log batches landed, EpochStarts included.
    pub(crate) fn landed_batch_count(&self) -> usize {
        self.record_batches.len() + self.epoch_starts.len()
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
