// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;
use std::sync::atomic::{AtomicI64, AtomicU64, Ordering};
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
}
