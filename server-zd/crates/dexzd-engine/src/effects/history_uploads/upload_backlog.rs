// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The sealed history segments each owned partition waits to upload, kept by segment identity so
//! a scan and concurrent commits never count a segment twice.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::{Arc, Mutex, MutexGuard};

use dexzd_apply::ApplyEffect;
use dexzd_keyspace::{PartitionHasher, PartitionId, RunId};
use dexzd_metrics::HistoryUploadBacklog;

use crate::effects::EffectDependencies;
use crate::flow_coordination::FlowAddress;

/// A sealed segment: its run and first seq.
pub(crate) type SealedSegmentKey = (RunId, u64);

/// Pending segments per partition, from committed seals, acknowledgments, and scans.
///
/// A scan ends as `(snapshot ∪ sealed) \ uploaded`, over changes reported since it began.
pub(crate) struct UploadBacklogTracker {
    gauges: HistoryUploadBacklog,
    hasher: PartitionHasher,
    state: Mutex<TrackerState>,
}

struct TrackerState {
    /// False once the effect tasks stopped; later reports change nothing.
    is_active: bool,
    partitions: BTreeMap<PartitionId, PartitionSegments>,
}

#[derive(Default)]
struct PartitionSegments {
    pending: BTreeSet<SealedSegmentKey>,
    /// Changes reported since the running scan began; `None` while no scan runs.
    running_scan: Option<ScanChanges>,
}

#[derive(Default)]
struct ScanChanges {
    sealed: BTreeSet<SealedSegmentKey>,
    uploaded: BTreeSet<SealedSegmentKey>,
}

/// What one scan of a partition found.
pub(crate) struct ScannedBacklog {
    pub(crate) sealed: BTreeSet<SealedSegmentKey>,
    /// Runs the scan could not read; their known segments stay pending.
    pub(crate) unreadable_runs: BTreeSet<RunId>,
    pub(crate) bytes: u64,
    pub(crate) oldest_sealed_at_micros: Option<i64>,
}

impl UploadBacklogTracker {
    pub(crate) fn new(dependencies: &EffectDependencies) -> Arc<Self> {
        Self::with_gauges(
            dependencies.metrics.history_upload_backlog.clone(),
            dependencies.partition_hasher.clone(),
        )
    }

    fn with_gauges(gauges: HistoryUploadBacklog, hasher: PartitionHasher) -> Arc<Self> {
        Arc::new(Self {
            gauges,
            hasher,
            state: Mutex::new(TrackerState {
                is_active: true,
                partitions: BTreeMap::new(),
            }),
        })
    }

    /// Records each seal among one proposed command's committed effects.
    /// Rebuilds and rescans report theirs to the router directly, not through here.
    pub(crate) fn record_committed_effects(&self, flow: &FlowAddress, effects: &[ApplyEffect]) {
        for effect in effects {
            if let ApplyEffect::HistorySegmentSealed { run_id, first_seq } = effect {
                self.update(flow, |segments| {
                    segments.pending.insert((*run_id, *first_seq));
                    if let Some(scan) = segments.running_scan.as_mut() {
                        scan.sealed.insert((*run_id, *first_seq));
                    }
                });
            }
        }
    }

    /// Records a segment whose upload acknowledgment committed.
    pub(crate) fn record_uploaded(&self, flow: &FlowAddress, run_id: RunId, first_seq: u64) {
        self.update(flow, |segments| {
            segments.pending.remove(&(run_id, first_seq));
            if let Some(scan) = segments.running_scan.as_mut() {
                scan.uploaded.insert((run_id, first_seq));
            }
        });
    }

    /// Marks the start of a scan of `partition`; call it before taking the scan's snapshot.
    pub(crate) fn begin_scan(&self, partition: PartitionId) {
        let mut state = self.lock_state();
        if state.is_active {
            let segments = state.partitions.entry(partition).or_default();
            segments.running_scan = Some(ScanChanges::default());
        }
    }

    /// Ends a scan that failed; the partition keeps its reported segments.
    pub(crate) fn cancel_scan(&self, partition: PartitionId) {
        if let Some(segments) = self.lock_state().partitions.get_mut(&partition) {
            segments.running_scan = None;
        }
    }

    /// Replaces the partition's pending segments with the scan's, plus changes reported since
    /// [`Self::begin_scan`], then publishes its gauges.
    pub(crate) fn finish_scan(&self, partition: PartitionId, scanned: ScannedBacklog) {
        let mut state = self.lock_state();
        if !state.is_active {
            return;
        }
        let segments = state.partitions.entry(partition).or_default();
        let changes = segments.running_scan.take().unwrap_or_default();
        let mut pending = scanned.sealed;
        pending.extend(
            segments
                .pending
                .iter()
                .filter(|(run_id, _)| scanned.unreadable_runs.contains(run_id))
                .copied(),
        );
        pending.extend(changes.sealed);
        pending.retain(|segment| !changes.uploaded.contains(segment));
        segments.pending = pending;
        // Acknowledgments after the snapshot can empty the set; then its bytes and age are stale.
        let (bytes, oldest_sealed_at_micros) = if segments.pending.is_empty() {
            (0, None)
        } else {
            (scanned.bytes, scanned.oldest_sealed_at_micros)
        };
        self.gauges.set_partition(
            usize::from(partition.0),
            segments.pending.len() as u64,
            bytes,
            oldest_sealed_at_micros,
        );
    }

    /// Stops accounting for good and clears the gauges, under the lock every report takes.
    pub(crate) fn deactivate(&self) {
        let mut state = self.lock_state();
        state.is_active = false;
        state.partitions.clear();
        self.gauges.clear();
    }

    fn update(&self, flow: &FlowAddress, change: impl FnOnce(&mut PartitionSegments)) {
        let partition = self.hasher.partition_of_flow(flow.tenant_id, &flow.flow_id);
        let mut state = self.lock_state();
        if !state.is_active {
            return;
        }
        let segments = state.partitions.entry(partition).or_default();
        change(segments);
        self.gauges
            .set_partition_segments(usize::from(partition.0), segments.pending.len() as u64);
    }

    fn lock_state(&self) -> MutexGuard<'_, TrackerState> {
        self.state
            .lock()
            .expect("the upload backlog lock is never poisoned")
    }
}

#[cfg(test)]
mod tests {
    use dexzd_keyspace::TenantId;

    use super::*;

    const RUN: RunId = RunId([4; 16]);

    fn tracker() -> (
        Arc<UploadBacklogTracker>,
        HistoryUploadBacklog,
        FlowAddress,
        PartitionId,
    ) {
        let hasher = PartitionHasher::new(&[7; 16], 4).expect("valid partition hasher");
        let gauges = dexzd_metrics::NodeMetrics::unregistered_with_partitions(4)
            .history_upload_backlog
            .clone();
        let flow = FlowAddress::new(TenantId::DEFAULT, "order-1");
        let partition = hasher.partition_of_flow(flow.tenant_id, &flow.flow_id);
        let tracker = UploadBacklogTracker::with_gauges(gauges.clone(), hasher);
        (tracker, gauges, flow, partition)
    }

    fn sealed(first_seq: u64) -> ApplyEffect {
        ApplyEffect::HistorySegmentSealed {
            run_id: RUN,
            first_seq,
        }
    }

    fn scanned(segments: &[u64]) -> ScannedBacklog {
        ScannedBacklog {
            sealed: segments.iter().map(|first_seq| (RUN, *first_seq)).collect(),
            unreadable_runs: BTreeSet::new(),
            bytes: 10,
            oldest_sealed_at_micros: Some(1),
        }
    }

    #[test]
    fn a_seal_in_the_snapshot_and_reported_after_the_mark_counts_once() {
        let (tracker, gauges, flow, partition) = tracker();
        tracker.begin_scan(partition);
        // The seal commits before the snapshot, and its report arrives after the mark.
        tracker.record_committed_effects(&flow, &[sealed(1)]);
        tracker.finish_scan(partition, scanned(&[1]));
        assert_eq!(gauges.segments(), 1);
        // A seal in the snapshot whose report arrives after the scan also counts once.
        tracker.begin_scan(partition);
        tracker.finish_scan(partition, scanned(&[1, 5]));
        tracker.record_committed_effects(&flow, &[sealed(5)]);
        assert_eq!(gauges.segments(), 2);
    }

    #[test]
    fn an_acknowledgment_before_or_after_the_snapshot_removes_its_segment_once() {
        let (tracker, gauges, flow, partition) = tracker();
        tracker.record_committed_effects(&flow, &[sealed(1), sealed(5)]);
        tracker.begin_scan(partition);
        // The acknowledgment of 1 commits before the snapshot and is reported after the mark.
        tracker.record_uploaded(&flow, RUN, 1);
        tracker.finish_scan(partition, scanned(&[5]));
        assert_eq!(gauges.segments(), 1);
        // The acknowledgment of 5 commits after the snapshot.
        tracker.begin_scan(partition);
        tracker.record_uploaded(&flow, RUN, 5);
        tracker.finish_scan(partition, scanned(&[5]));
        assert_eq!(gauges.segments(), 0);
        assert_eq!(gauges.bytes(), 0);
        assert_eq!(gauges.oldest_sealed_at_micros(), None);
        // A late report removes nothing twice.
        tracker.record_uploaded(&flow, RUN, 5);
        assert_eq!(gauges.segments(), 0);
        assert_eq!(gauges.bytes(), 0);
    }

    #[test]
    fn a_scan_whose_last_segment_is_acknowledged_after_the_snapshot_publishes_no_bytes_or_age() {
        let (tracker, gauges, flow, partition) = tracker();
        tracker.record_committed_effects(&flow, &[sealed(1)]);
        tracker.begin_scan(partition);
        // The snapshot still holds segment 1; its only acknowledgment is reported before publication.
        tracker.record_uploaded(&flow, RUN, 1);
        assert_eq!(gauges.segments(), 0);
        tracker.finish_scan(partition, scanned(&[1]));
        assert_eq!(gauges.segments(), 0);
        assert_eq!(gauges.bytes(), 0);
        assert_eq!(gauges.oldest_sealed_at_micros(), None);
        // A non-empty result still publishes the scan's bytes and age.
        tracker.record_committed_effects(&flow, &[sealed(5)]);
        tracker.begin_scan(partition);
        tracker.finish_scan(partition, scanned(&[5]));
        assert_eq!(gauges.segments(), 1);
        assert_eq!(gauges.bytes(), 10);
        assert_eq!(gauges.oldest_sealed_at_micros(), Some(1));
    }

    #[test]
    fn a_scan_drops_purged_segments_and_keeps_unreadable_runs() {
        let (tracker, gauges, flow, partition) = tracker();
        tracker.record_committed_effects(&flow, &[sealed(1), sealed(5)]);
        tracker.begin_scan(partition);
        tracker.finish_scan(partition, scanned(&[]));
        assert_eq!(gauges.segments(), 0, "purged segments leave with the scan");
        tracker.record_committed_effects(&flow, &[sealed(9)]);
        tracker.begin_scan(partition);
        let mut unreadable = scanned(&[]);
        unreadable.unreadable_runs.insert(RUN);
        tracker.finish_scan(partition, unreadable);
        assert_eq!(gauges.segments(), 1);
        tracker.begin_scan(partition);
        tracker.cancel_scan(partition);
        tracker.record_committed_effects(&flow, &[sealed(12)]);
        assert_eq!(gauges.segments(), 2);
    }

    #[test]
    fn reports_after_deactivation_never_repopulate_the_gauges() {
        let (tracker, gauges, flow, partition) = tracker();
        tracker.record_committed_effects(&flow, &[sealed(1)]);
        tracker.begin_scan(partition);
        tracker.deactivate();
        assert_eq!(gauges.segments(), 0);
        tracker.record_committed_effects(&flow, &[sealed(5)]);
        tracker.finish_scan(partition, scanned(&[1, 5]));
        tracker.begin_scan(partition);
        tracker.record_uploaded(&flow, RUN, 1);
        assert_eq!(gauges.segments(), 0);
        assert_eq!(gauges.bytes(), 0);
    }
}
