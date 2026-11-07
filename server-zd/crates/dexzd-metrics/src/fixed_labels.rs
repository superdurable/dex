// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use prometheus_client::encoding::{EncodeLabelSet, EncodeMetric, MetricEncoder, NoLabelSet};
use prometheus_client::metrics::MetricType;
use prometheus_client::metrics::counter::Counter;
use prometheus_client::metrics::gauge::Gauge;

use crate::histogram::TAKEOVER_LATENCY_BOUNDS;
use crate::{
    AtomicHistogram, NodeRole, ObjectOperation, ObjectOperationLabel, Outcome, OutcomeLabel,
    RoleLabel,
};

/// Fixed lifecycle gauges; exactly one role has value one.
#[derive(Clone, Debug)]
pub struct RoleGauges {
    values: Arc<[Gauge; 5]>,
}

impl Default for RoleGauges {
    fn default() -> Self {
        Self {
            values: Arc::new(std::array::from_fn(|_| Gauge::default())),
        }
    }
}

impl RoleGauges {
    pub(crate) fn set(&self, current: NodeRole) {
        for (index, role) in NodeRole::ALL.into_iter().enumerate() {
            self.values[index].set(i64::from(role == current));
        }
    }
}

impl EncodeMetric for RoleGauges {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for (index, role) in NodeRole::ALL.into_iter().enumerate() {
            encoder
                .encode_family(&RoleLabel { role })?
                .encode_gauge(&self.values[index].get())?;
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Gauge
    }
}

/// Fixed success and failure counters.
#[derive(Clone, Debug)]
pub struct OutcomeCounters {
    values: Arc<[Counter; 2]>,
}

impl Default for OutcomeCounters {
    fn default() -> Self {
        Self {
            values: Arc::new(std::array::from_fn(|_| Counter::default())),
        }
    }
}

impl OutcomeCounters {
    pub(crate) fn record(&self, outcome: Outcome) {
        self.values[match outcome {
            Outcome::Ok => 0,
            Outcome::Error => 1,
        }]
        .inc();
    }
}

impl EncodeMetric for OutcomeCounters {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for (index, outcome) in [Outcome::Ok, Outcome::Error].into_iter().enumerate() {
            encoder
                .encode_family(&OutcomeLabel { outcome })?
                .encode_counter::<NoLabelSet, _, u64>(&self.values[index].get(), None)?;
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Counter
    }
}

/// Fixed failed-object-request counters.
#[derive(Clone, Debug)]
pub struct ObjectErrorCounters {
    values: Arc<[Counter; 4]>,
}

impl Default for ObjectErrorCounters {
    fn default() -> Self {
        Self {
            values: Arc::new(std::array::from_fn(|_| Counter::default())),
        }
    }
}

impl ObjectErrorCounters {
    pub(crate) fn record(&self, operation: ObjectOperation) {
        let index = match operation {
            ObjectOperation::Put => 0,
            ObjectOperation::Get => 1,
            ObjectOperation::List => 2,
            ObjectOperation::Delete => 3,
        };
        self.values[index].inc();
    }
}

impl EncodeMetric for ObjectErrorCounters {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for (index, operation) in ObjectOperation::ALL.into_iter().enumerate() {
            encoder
                .encode_family(&ObjectOperationLabel { operation })?
                .encode_counter::<NoLabelSet, _, u64>(&self.values[index].get(), None)?;
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Counter
    }
}

/// Whether retrying an Attribute Store write can succeed.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum AttributeSyncFailureKind {
    /// A connection or transient database failure; the write is retried with backoff.
    Retryable,
    /// The store rejected the write, such as a Flow ID that does not match rowKey; it is dropped.
    NonRetryable,
}

label_value!(AttributeSyncFailureKind { Retryable => "retryable", NonRetryable => "non_retryable" });

impl AttributeSyncFailureKind {
    const ALL: [Self; 2] = [Self::Retryable, Self::NonRetryable];
}

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct AttributeSyncFailureLabel {
    failure_kind: AttributeSyncFailureKind,
}

/// Fixed counters of failed Attribute Store write transactions.
#[derive(Clone, Debug)]
pub struct AttributeSyncWriteFailures {
    values: Arc<[Counter; 2]>,
}

impl Default for AttributeSyncWriteFailures {
    fn default() -> Self {
        Self {
            values: Arc::new(std::array::from_fn(|_| Counter::default())),
        }
    }
}

impl AttributeSyncWriteFailures {
    /// Records one failed write transaction.
    pub fn record(&self, failure_kind: AttributeSyncFailureKind) {
        self.values[match failure_kind {
            AttributeSyncFailureKind::Retryable => 0,
            AttributeSyncFailureKind::NonRetryable => 1,
        }]
        .inc();
    }

    /// Returns the failures recorded for `failure_kind`.
    pub fn count(&self, failure_kind: AttributeSyncFailureKind) -> u64 {
        self.values[match failure_kind {
            AttributeSyncFailureKind::Retryable => 0,
            AttributeSyncFailureKind::NonRetryable => 1,
        }]
        .get()
    }
}

impl EncodeMetric for AttributeSyncWriteFailures {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for (index, failure_kind) in AttributeSyncFailureKind::ALL.into_iter().enumerate() {
            encoder
                .encode_family(&AttributeSyncFailureLabel { failure_kind })?
                .encode_counter::<NoLabelSet, _, u64>(&self.values[index].get(), None)?;
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Counter
    }
}

/// How one upload of a sealed history segment ended; every outcome but `Failed` is counted once
/// the upload's acknowledgment committed.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum HistoryUploadOutcome {
    /// The upload created the object.
    Created,
    /// An object with the same content checksum already existed, as a repeated upload finds.
    AlreadyPresent,
    /// An object with another content checksum existed; the upload replaced it.
    Replaced,
    /// An upload attempt failed. A transient object storage failure counts here and is retried
    /// with backoff. A failure that is not retried with backoff also counts here and is retried
    /// only at the next rescan: a permanent object storage rejection, a segment that cannot be
    /// read or encoded from committed state, or a rejected acknowledgment.
    Failed,
}

label_value!(HistoryUploadOutcome { Created => "created", AlreadyPresent => "already_present", Replaced => "replaced", Failed => "failed" });

impl HistoryUploadOutcome {
    const ALL: [Self; 4] = [
        Self::Created,
        Self::AlreadyPresent,
        Self::Replaced,
        Self::Failed,
    ];

    const fn index(self) -> usize {
        match self {
            Self::Created => 0,
            Self::AlreadyPresent => 1,
            Self::Replaced => 2,
            Self::Failed => 3,
        }
    }
}

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct HistoryUploadOutcomeLabel {
    outcome: HistoryUploadOutcome,
}

/// Fixed counters of history segment uploads, by outcome.
#[derive(Clone, Debug)]
pub struct HistoryUploadOutcomes {
    values: Arc<[Counter; 4]>,
}

impl Default for HistoryUploadOutcomes {
    fn default() -> Self {
        Self {
            values: Arc::new(std::array::from_fn(|_| Counter::default())),
        }
    }
}

impl HistoryUploadOutcomes {
    /// Records one upload attempt.
    pub fn record(&self, outcome: HistoryUploadOutcome) {
        self.values[outcome.index()].inc();
    }

    /// Returns the upload attempts recorded with `outcome`.
    pub fn count(&self, outcome: HistoryUploadOutcome) -> u64 {
        self.values[outcome.index()].get()
    }
}

impl EncodeMetric for HistoryUploadOutcomes {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for outcome in HistoryUploadOutcome::ALL {
            encoder
                .encode_family(&HistoryUploadOutcomeLabel { outcome })?
                .encode_counter::<NoLabelSet, _, u64>(&self.values[outcome.index()].get(), None)?;
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Counter
    }
}

/// What a history read returned; see [`HistoryReads`].
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum HistoryReadKind {
    /// A GetHistoryEvents page.
    Page,
    /// A LoadBlobs value.
    Blob,
}

label_value!(HistoryReadKind { Page => "page", Blob => "blob" });

/// Where a history read found its data; see [`HistoryReads`].
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum HistoryReadSource {
    /// Local rows only.
    Local,
    /// The node's disk read cache.
    Cache,
    /// Object storage.
    ObjectStore,
}

label_value!(HistoryReadSource { Local => "local", Cache => "cache", ObjectStore => "object_store" });

impl HistoryReadSource {
    const ALL: [Self; 3] = [Self::Local, Self::Cache, Self::ObjectStore];
}

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct HistoryReadLabel {
    kind: HistoryReadKind,
    source: HistoryReadSource,
}

/// Fixed counters of history reads, by kind and source.
///
/// A local-only read counts once as `local`; each segment part read counts as its source.
#[derive(Clone, Debug)]
pub struct HistoryReads {
    values: Arc<[Counter; 6]>,
}

impl Default for HistoryReads {
    fn default() -> Self {
        Self {
            values: Arc::new(std::array::from_fn(|_| Counter::default())),
        }
    }
}

impl HistoryReads {
    /// Records one read of `kind` from `source`.
    pub fn record(&self, kind: HistoryReadKind, source: HistoryReadSource) {
        self.values[Self::index(kind, source)].inc();
    }

    /// Returns the reads recorded with `kind` and `source`.
    pub fn count(&self, kind: HistoryReadKind, source: HistoryReadSource) -> u64 {
        self.values[Self::index(kind, source)].get()
    }

    const fn index(kind: HistoryReadKind, source: HistoryReadSource) -> usize {
        let kind_index = match kind {
            HistoryReadKind::Page => 0,
            HistoryReadKind::Blob => 3,
        };
        let source_index = match source {
            HistoryReadSource::Local => 0,
            HistoryReadSource::Cache => 1,
            HistoryReadSource::ObjectStore => 2,
        };
        kind_index + source_index
    }
}

impl EncodeMetric for HistoryReads {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for kind in [HistoryReadKind::Page, HistoryReadKind::Blob] {
            for source in HistoryReadSource::ALL {
                encoder
                    .encode_family(&HistoryReadLabel { kind, source })?
                    .encode_counter::<NoLabelSet, _, u64>(
                        &self.values[Self::index(kind, source)].get(),
                        None,
                    )?;
            }
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Counter
    }
}

/// What this process does as a hot standby.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum HotStandbyMetricState {
    /// Not a hot standby: it serves, starts, stops, or runs as a cold standby.
    Inactive,
    /// Opening its local store or restoring the newest snapshot.
    Bootstrapping,
    /// Following the log within the lag threshold.
    Following,
    /// Following the log beyond the lag threshold.
    Behind,
    /// Waiting to bootstrap again after an error it retries.
    Restarting,
    /// Stopped after an error it does not retry.
    Failed,
}

label_value!(HotStandbyMetricState { Inactive => "inactive", Bootstrapping => "bootstrapping", Following => "following", Behind => "behind", Restarting => "restarting", Failed => "failed" });

impl HotStandbyMetricState {
    const ALL: [Self; 6] = [
        Self::Inactive,
        Self::Bootstrapping,
        Self::Following,
        Self::Behind,
        Self::Restarting,
        Self::Failed,
    ];
}

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct HotStandbyStateLabel {
    state: HotStandbyMetricState,
}

/// Fixed hot standby state gauges; exactly one state has value one.
#[derive(Clone, Debug)]
pub struct HotStandbyStateGauges {
    values: Arc<[Gauge; 6]>,
}

impl Default for HotStandbyStateGauges {
    fn default() -> Self {
        let gauges = Self {
            values: Arc::new(std::array::from_fn(|_| Gauge::default())),
        };
        gauges.set(HotStandbyMetricState::Inactive);
        gauges
    }
}

impl HotStandbyStateGauges {
    /// Makes `current` the one state with value one.
    pub fn set(&self, current: HotStandbyMetricState) {
        for (index, state) in HotStandbyMetricState::ALL.into_iter().enumerate() {
            self.values[index].set(i64::from(state == current));
        }
    }
}

impl EncodeMetric for HotStandbyStateGauges {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for (index, state) in HotStandbyMetricState::ALL.into_iter().enumerate() {
            encoder
                .encode_family(&HotStandbyStateLabel { state })?
                .encode_gauge(&self.values[index].get())?;
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Gauge
    }
}

/// Where a takeover found the local state it serves from.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum TakeoverStandbyMode {
    /// The process followed the log as a hot standby before it took the lease.
    Hot,
    /// The process waited for the lease, then opened or restored its store.
    Cold,
}

label_value!(TakeoverStandbyMode { Hot => "hot", Cold => "cold" });

impl TakeoverStandbyMode {
    const ALL: [Self; 2] = [Self::Hot, Self::Cold];

    const fn index(self) -> usize {
        match self {
            Self::Hot => 0,
            Self::Cold => 1,
        }
    }
}

/// Which local store a takeover serves from.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum TakeoverStoreSource {
    /// The store that the hot standby kept current, so the takeover replayed only the log tail.
    HotStandby,
    /// The process opened its own store or restored a snapshot, then replayed the log after it.
    Restored,
}

label_value!(TakeoverStoreSource { HotStandby => "hot_standby", Restored => "restored" });

impl TakeoverStoreSource {
    const ALL: [Self; 2] = [Self::HotStandby, Self::Restored];

    const fn index(self) -> usize {
        match self {
            Self::HotStandby => 0,
            Self::Restored => 1,
        }
    }
}

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct TakeoverLabels {
    standby_mode: TakeoverStandbyMode,
    store_source: TakeoverStoreSource,
}

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct TakeoverStoreSourceLabel {
    store_source: TakeoverStoreSource,
}

/// Fixed takeover counters by standby mode and store source.
#[derive(Clone, Debug)]
pub struct TakeoverCounters {
    values: Arc<[[Counter; 2]; 2]>,
}

impl Default for TakeoverCounters {
    fn default() -> Self {
        Self {
            values: Arc::new(std::array::from_fn(|_| {
                std::array::from_fn(|_| Counter::default())
            })),
        }
    }
}

impl TakeoverCounters {
    /// Records one completed takeover.
    pub fn record(&self, standby_mode: TakeoverStandbyMode, store_source: TakeoverStoreSource) {
        self.values[standby_mode.index()][store_source.index()].inc();
    }
}

impl EncodeMetric for TakeoverCounters {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for standby_mode in TakeoverStandbyMode::ALL {
            for store_source in TakeoverStoreSource::ALL {
                encoder
                    .encode_family(&TakeoverLabels {
                        standby_mode,
                        store_source,
                    })?
                    .encode_counter::<NoLabelSet, _, u64>(
                        &self.values[standby_mode.index()][store_source.index()].get(),
                        None,
                    )?;
            }
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Counter
    }
}

/// Fixed takeover duration histograms by store source.
#[derive(Clone, Debug)]
pub struct TakeoverDurations {
    histograms: Arc<[AtomicHistogram<7>; 2]>,
}

impl Default for TakeoverDurations {
    fn default() -> Self {
        Self {
            histograms: Arc::new(std::array::from_fn(|_| {
                AtomicHistogram::new(&TAKEOVER_LATENCY_BOUNDS)
            })),
        }
    }
}

impl TakeoverDurations {
    /// Records how long one takeover took, in seconds.
    pub fn observe(&self, store_source: TakeoverStoreSource, seconds: f64) {
        self.histograms[store_source.index()].observe(seconds);
    }
}

impl EncodeMetric for TakeoverDurations {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for store_source in TakeoverStoreSource::ALL {
            self.histograms[store_source.index()]
                .encode(encoder.encode_family(&TakeoverStoreSourceLabel { store_source })?)?;
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Histogram
    }
}
