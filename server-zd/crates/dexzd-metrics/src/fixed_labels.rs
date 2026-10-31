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

use crate::{NodeRole, ObjectOperation, ObjectOperationLabel, Outcome, OutcomeLabel, RoleLabel};

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

/// The priority of an Upsert omitted during search projection overload.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum ProjectionDropReason {
    /// An update to an existing Running run.
    Intermediate,
    /// A terminal document, including updates after close.
    Close,
    /// A start document or its coalesced Running replacement.
    Start,
}

label_value!(ProjectionDropReason { Intermediate => "intermediate", Close => "close", Start => "start" });

impl ProjectionDropReason {
    const ALL: [Self; 3] = [Self::Intermediate, Self::Close, Self::Start];

    const fn index(self) -> usize {
        match self {
            Self::Intermediate => 0,
            Self::Close => 1,
            Self::Start => 2,
        }
    }
}

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct ProjectionDropLabel {
    reason: ProjectionDropReason,
}

/// Fixed search projection change counters, partitioned by priority.
#[derive(Clone, Debug)]
pub struct ProjectionDropCounters {
    values: Arc<[Counter; 3]>,
}

impl Default for ProjectionDropCounters {
    fn default() -> Self {
        Self {
            values: Arc::new(std::array::from_fn(|_| Counter::default())),
        }
    }
}

impl ProjectionDropCounters {
    /// Records one search Upsert for the given drop or repair priority.
    pub fn record(&self, reason: ProjectionDropReason) {
        self.values[reason.index()].inc();
    }

    /// Returns the recorded Upserts for the given priority.
    pub fn count(&self, reason: ProjectionDropReason) -> u64 {
        self.values[reason.index()].get()
    }
}

impl EncodeMetric for ProjectionDropCounters {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for reason in ProjectionDropReason::ALL {
            encoder
                .encode_family(&ProjectionDropLabel { reason })?
                .encode_counter::<NoLabelSet, _, u64>(&self.values[reason.index()].get(), None)?;
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Counter
    }
}

/// A condition that operators can resolve with a manual partition rebuild.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum ProjectionRebuildReason {
    /// SQL projection progress is behind committed state.
    StartupBehind,
    /// The bounded in-memory staging area filled before persistence.
    RepairOverflow,
    /// Protected Deletes exceeded queue limits.
    DeleteOverflow,
    /// SQL progress differs from the active owner's expected progress.
    Divergence,
    /// Committed search state cannot be decoded.
    Unreadable,
}
label_value!(ProjectionRebuildReason {
    StartupBehind => "startup_behind", RepairOverflow => "repair_overflow",
    DeleteOverflow => "delete_overflow", Divergence => "divergence", Unreadable => "unreadable"
});
#[derive(EncodeLabelSet)]
struct RebuildReasonLabel {
    reason: ProjectionRebuildReason,
}

/// Fixed rebuild-needed counters for every supported reason.
#[derive(Clone, Debug)]
pub struct ProjectionRebuildCounters {
    counters: Arc<[Counter; 5]>,
}
impl Default for ProjectionRebuildCounters {
    fn default() -> Self {
        Self {
            counters: Arc::new(std::array::from_fn(|_| Counter::default())),
        }
    }
}
impl ProjectionRebuildCounters {
    /// Counts one newly observed partition and reason pair.
    pub fn record(&self, reason: ProjectionRebuildReason) {
        self.counters[reason as usize].inc();
    }
}
impl EncodeMetric for ProjectionRebuildCounters {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for reason in [
            ProjectionRebuildReason::StartupBehind,
            ProjectionRebuildReason::RepairOverflow,
            ProjectionRebuildReason::DeleteOverflow,
            ProjectionRebuildReason::Divergence,
            ProjectionRebuildReason::Unreadable,
        ] {
            encoder
                .encode_family(&RebuildReasonLabel { reason })?
                .encode_counter::<NoLabelSet, _, u64>(
                    &self.counters[reason as usize].get(),
                    None,
                )?;
        }
        Ok(())
    }
    fn metric_type(&self) -> MetricType {
        MetricType::Counter
    }
}
