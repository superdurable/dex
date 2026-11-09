// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::atomic::{AtomicI64, AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

use prometheus_client::encoding::{EncodeMetric, MetricEncoder};
use prometheus_client::metrics::MetricType;

/// Oldest due timer age across partitions, without per-partition series.
#[derive(Clone, Debug)]
pub struct TimerLag {
    oldest_due_by_partition: Arc<Vec<AtomicI64>>,
}

impl TimerLag {
    pub(crate) fn new(partition_count: usize) -> Self {
        Self {
            oldest_due_by_partition: Arc::new(
                (0..partition_count).map(|_| AtomicI64::new(0)).collect(),
            ),
        }
    }

    /// Sets the oldest due, unfired timer in one partition, or clears it.
    pub fn set_due(&self, partition: usize, fire_at_unix_micros: Option<i64>) {
        if let Some(oldest) = self.oldest_due_by_partition.get(partition) {
            oldest.store(fire_at_unix_micros.unwrap_or(0), Ordering::Relaxed);
        }
    }
}

impl EncodeMetric for TimerLag {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        let oldest = self
            .oldest_due_by_partition
            .iter()
            .map(|value| value.load(Ordering::Relaxed))
            .filter(|value| *value > 0)
            .min();
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_secs_f64();
        let lag = oldest.map_or(0.0, |fire_at| (now - fire_at as f64 / 1_000_000.0).max(0.0));
        encoder.encode_gauge(&lag)
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Gauge
    }
}

/// SearchOutbox backlog of every partition; scrapes see totals, not per-partition series.
#[derive(Clone, Debug)]
pub struct SearchOutboxBacklog {
    partitions: Arc<Vec<PartitionBacklog>>,
}

#[derive(Debug, Default)]
struct PartitionBacklog {
    /// Unprojected entries the projection's last scan found.
    entries: AtomicU64,
    /// Seq of the oldest unprojected entry, the appending command's proposed_at_micros; 0 when none.
    oldest_unprojected_seq_micros: AtomicI64,
}

impl SearchOutboxBacklog {
    pub(crate) fn new(partition_count: usize) -> Self {
        Self {
            partitions: Arc::new(
                (0..partition_count)
                    .map(|_| PartitionBacklog::default())
                    .collect(),
            ),
        }
    }

    /// Records how many unprojected entries the projection's last scan of `partition` found.
    pub fn set_entries(&self, partition: usize, entries: u64) {
        if let Some(backlog) = self.partitions.get(partition) {
            backlog.entries.store(entries, Ordering::Relaxed);
        }
    }

    /// Records the seq of the partition's oldest unprojected entry, or `None` when SQL visibility covers every entry.
    pub fn set_oldest_unprojected(&self, partition: usize, seq_micros: Option<i64>) {
        if let Some(backlog) = self.partitions.get(partition) {
            backlog
                .oldest_unprojected_seq_micros
                .store(seq_micros.unwrap_or(0), Ordering::Relaxed);
        }
    }

    /// Clears every partition, for a node that stopped projecting.
    pub fn clear(&self) {
        for partition in 0..self.partitions.len() {
            self.set_entries(partition, 0);
            self.set_oldest_unprojected(partition, None);
        }
    }

    fn entries(&self) -> impl Iterator<Item = u64> + '_ {
        self.partitions
            .iter()
            .map(|backlog| backlog.entries.load(Ordering::Relaxed))
    }
}

/// Total unprojected SearchOutbox entries, as of each partition's last scan.
#[derive(Clone, Debug)]
pub struct SearchOutboxEntries(pub(crate) SearchOutboxBacklog);

/// Largest unprojected SearchOutbox backlog of one partition, as of its last scan.
#[derive(Clone, Debug)]
pub struct SearchOutboxMaxPartitionEntries(pub(crate) SearchOutboxBacklog);

/// Age of the oldest unprojected SearchOutbox entry, computed at scrape time.
#[derive(Clone, Debug)]
pub struct SearchOutboxOldestEntryAge(pub(crate) SearchOutboxBacklog);

impl EncodeMetric for SearchOutboxEntries {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        let total = self.0.entries().fold(0_u64, u64::saturating_add);
        encoder.encode_gauge(&i64::try_from(total).unwrap_or(i64::MAX))
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Gauge
    }
}

impl EncodeMetric for SearchOutboxMaxPartitionEntries {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        let largest = self.0.entries().max().unwrap_or(0);
        encoder.encode_gauge(&i64::try_from(largest).unwrap_or(i64::MAX))
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Gauge
    }
}

impl EncodeMetric for SearchOutboxOldestEntryAge {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        let oldest = self
            .0
            .partitions
            .iter()
            .map(|backlog| {
                backlog
                    .oldest_unprojected_seq_micros
                    .load(Ordering::Relaxed)
            })
            .filter(|seq| *seq > 0)
            .min();
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_secs_f64();
        let age = oldest.map_or(0.0, |head| (now - head as f64 / 1_000_000.0).max(0.0));
        encoder.encode_gauge(&age)
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Gauge
    }
}

/// Sealed history segments waiting for upload in each partition.
///
/// The segment count follows seals and upload acknowledgments as they commit. Bytes and the
/// oldest seal time are as of the uploader's last scan, and clear once the count drains.
/// Each scan of a partition replaces its values, which also corrects the count's drift.
#[derive(Clone, Debug)]
pub struct HistoryUploadBacklog {
    partitions: Arc<Vec<Mutex<PartitionHistoryUploadBacklog>>>,
}

#[derive(Debug, Default)]
struct PartitionHistoryUploadBacklog {
    segments: u64,
    /// Uncompressed bytes of the waiting segments' history rows and superseded values.
    bytes: u64,
    /// Seal time of the oldest waiting segment, the sealing command's proposed_at_micros; 0 when none.
    oldest_sealed_at_micros: i64,
    /// Seals minus acknowledgments counted since the partition's running scan began.
    segment_change_since_scan: i64,
}

impl HistoryUploadBacklog {
    pub(crate) fn new(partition_count: usize) -> Self {
        Self {
            partitions: Arc::new(
                (0..partition_count)
                    .map(|_| Mutex::new(PartitionHistoryUploadBacklog::default()))
                    .collect(),
            ),
        }
    }

    /// Counts one segment whose seal committed. Ignores a partition outside the configured
    /// count.
    pub fn add_sealed_segment(&self, partition: usize) {
        self.update_partition(partition, |backlog| {
            backlog.segments = backlog.segments.saturating_add(1);
            backlog.segment_change_since_scan = backlog.segment_change_since_scan.saturating_add(1);
        });
    }

    /// Removes one segment whose upload acknowledgment committed; the last one also clears the
    /// partition's bytes and oldest seal time. Ignores a partition outside the configured count.
    pub fn remove_uploaded_segment(&self, partition: usize) {
        self.update_partition(partition, |backlog| {
            backlog.segments = backlog.segments.saturating_sub(1);
            backlog.segment_change_since_scan = backlog.segment_change_since_scan.saturating_sub(1);
            if backlog.segments == 0 {
                backlog.bytes = 0;
                backlog.oldest_sealed_at_micros = 0;
            }
        });
    }

    /// Marks the start of a scan of `partition`; call it before the scan reads committed state.
    /// [`Self::set_partition`] then adds the seals and acknowledgments counted after the mark.
    /// Ignores a partition outside the configured count.
    pub fn begin_partition_scan(&self, partition: usize) {
        self.update_partition(partition, |backlog| backlog.segment_change_since_scan = 0);
    }

    /// Records what the uploader's last scan of `partition` found waiting: the segment count,
    /// their uncompressed bytes, and the seal time of the oldest one, or `None` when none waits.
    /// The published count also includes the seals and acknowledgments counted since
    /// [`Self::begin_partition_scan`].
    /// Ignores a partition outside the configured count.
    pub fn set_partition(
        &self,
        partition: usize,
        segments: u64,
        bytes: u64,
        oldest_sealed_at_micros: Option<i64>,
    ) {
        self.update_partition(partition, |backlog| {
            let change = backlog.segment_change_since_scan;
            backlog.segments = if change >= 0 {
                segments.saturating_add(change.unsigned_abs())
            } else {
                segments.saturating_sub(change.unsigned_abs())
            };
            backlog.segment_change_since_scan = 0;
            backlog.bytes = bytes;
            backlog.oldest_sealed_at_micros = oldest_sealed_at_micros.unwrap_or(0);
        });
    }

    /// Clears every partition, for a node that stopped uploading.
    pub fn clear(&self) {
        for partition in 0..self.partitions.len() {
            self.update_partition(partition, |backlog| {
                *backlog = PartitionHistoryUploadBacklog::default();
            });
        }
    }

    /// Returns the segments waiting in every partition.
    pub fn segments(&self) -> u64 {
        self.read_partitions(|backlog| backlog.segments)
            .fold(0, u64::saturating_add)
    }

    /// Returns the uncompressed bytes waiting in every partition.
    pub fn bytes(&self) -> u64 {
        self.read_partitions(|backlog| backlog.bytes)
            .fold(0, u64::saturating_add)
    }

    /// Returns the seal time of the oldest segment waiting in any partition, or `None` when none
    /// waits.
    pub fn oldest_sealed_at_micros(&self) -> Option<i64> {
        self.read_partitions(|backlog| backlog.oldest_sealed_at_micros)
            .filter(|sealed_at| *sealed_at > 0)
            .min()
    }

    fn update_partition(
        &self,
        partition: usize,
        update: impl FnOnce(&mut PartitionHistoryUploadBacklog),
    ) {
        if let Some(backlog) = self.partitions.get(partition) {
            update(
                &mut backlog
                    .lock()
                    .expect("the history upload backlog lock is never poisoned"),
            );
        }
    }

    fn read_partitions<T>(
        &self,
        read: impl Fn(&PartitionHistoryUploadBacklog) -> T,
    ) -> impl Iterator<Item = T> {
        self.partitions.iter().map(move |backlog| {
            read(
                &backlog
                    .lock()
                    .expect("the history upload backlog lock is never poisoned"),
            )
        })
    }
}

#[derive(Clone, Debug)]
pub struct HistoryUploadBacklogSegments(pub(crate) HistoryUploadBacklog);

#[derive(Clone, Debug)]
pub struct HistoryUploadBacklogBytes(pub(crate) HistoryUploadBacklog);

#[derive(Clone, Debug)]
pub struct HistoryUploadOldestSealedAge(pub(crate) HistoryUploadBacklog);

impl EncodeMetric for HistoryUploadBacklogSegments {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        encoder.encode_gauge(&i64::try_from(self.0.segments()).unwrap_or(i64::MAX))
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Gauge
    }
}

impl EncodeMetric for HistoryUploadBacklogBytes {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        encoder.encode_gauge(&i64::try_from(self.0.bytes()).unwrap_or(i64::MAX))
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Gauge
    }
}

impl EncodeMetric for HistoryUploadOldestSealedAge {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_secs_f64();
        let age = self.0.oldest_sealed_at_micros().map_or(0.0, |sealed_at| {
            (now - sealed_at as f64 / 1_000_000.0).max(0.0)
        });
        encoder.encode_gauge(&age)
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Gauge
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn backlog_totals_every_partition_and_ignores_out_of_range_ones() {
        let backlog = SearchOutboxBacklog::new(3);
        backlog.set_entries(0, 5);
        backlog.set_entries(2, 7);
        backlog.set_entries(9, 100);
        backlog.set_oldest_unprojected(9, Some(1));
        assert_eq!(backlog.entries().sum::<u64>(), 12);
        assert_eq!(backlog.entries().max(), Some(7));
        backlog.clear();
        assert_eq!(backlog.entries().sum::<u64>(), 0);
    }

    #[test]
    fn history_upload_backlog_totals_partitions_and_keeps_the_oldest_seal() {
        let backlog = HistoryUploadBacklog::new(3);
        backlog.set_partition(0, 2, 300, Some(50));
        backlog.set_partition(2, 1, 100, Some(20));
        backlog.set_partition(9, 100, 100, Some(1));
        assert_eq!(backlog.segments(), 3);
        assert_eq!(backlog.bytes(), 400);
        assert_eq!(backlog.oldest_sealed_at_micros(), Some(20));
        backlog.clear();
        assert_eq!(backlog.segments(), 0);
        assert_eq!(backlog.oldest_sealed_at_micros(), None);
    }

    #[test]
    fn history_upload_backlog_keeps_changes_counted_while_a_scan_runs() {
        let backlog = HistoryUploadBacklog::new(2);
        backlog.add_sealed_segment(0);
        backlog.add_sealed_segment(0);
        assert_eq!(backlog.segments(), 2);
        backlog.begin_partition_scan(0);
        // One seal and two acknowledgments commit after the scan read committed state.
        backlog.add_sealed_segment(0);
        backlog.remove_uploaded_segment(0);
        backlog.remove_uploaded_segment(0);
        backlog.set_partition(0, 2, 100, Some(5));
        assert_eq!(backlog.segments(), 1);
        assert_eq!(backlog.bytes(), 100);
        backlog.remove_uploaded_segment(0);
        backlog.remove_uploaded_segment(0);
        assert_eq!(backlog.segments(), 0);
        assert_eq!(backlog.bytes(), 0);
        assert_eq!(backlog.oldest_sealed_at_micros(), None);
        backlog.begin_partition_scan(1);
        backlog.remove_uploaded_segment(1);
        backlog.set_partition(1, 0, 0, None);
        assert_eq!(backlog.segments(), 0);
        backlog.add_sealed_segment(9);
        assert_eq!(backlog.segments(), 0);
    }
}
