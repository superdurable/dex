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

/// A persistent Worker stream event.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum WorkerStreamEvent {
    /// A task was delivered to a Worker.
    Delivered,
    /// A Worker stream closed.
    Closed,
}

label_value!(WorkerStreamEvent { Delivered => "delivered", Closed => "closed" });

impl WorkerStreamEvent {
    const ALL: [Self; 2] = [Self::Delivered, Self::Closed];

    const fn index(self) -> usize {
        match self {
            Self::Delivered => 0,
            Self::Closed => 1,
        }
    }
}

#[derive(Clone, Debug, EncodeLabelSet)]
struct WorkerStreamEventLabel {
    event: WorkerStreamEvent,
}

/// Delivered tasks and closed persistent Worker streams.
#[derive(Clone, Debug)]
pub struct WorkerStreamEvents {
    counts: Arc<[Counter; 2]>,
}

impl Default for WorkerStreamEvents {
    fn default() -> Self {
        Self {
            counts: Arc::new(std::array::from_fn(|_| Counter::default())),
        }
    }
}

impl WorkerStreamEvents {
    /// Records one task delivery or stream close.
    pub fn record(&self, event: WorkerStreamEvent) {
        self.counts[event.index()].inc();
    }
}

impl EncodeMetric for WorkerStreamEvents {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for event in WorkerStreamEvent::ALL {
            encoder
                .encode_family(&WorkerStreamEventLabel { event })?
                .encode_counter::<NoLabelSet, _, u64>(&self.counts[event.index()].get(), None)?;
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Counter
    }
}

/// Why a started Step attempt returned to pending without consuming a retry.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum StepAttemptReturnReason {
    /// The Worker declined the Step type.
    Unsupported,
    /// A Worker without reserved-slot dispatch declined for capacity.
    LegacyNoCapacity,
    /// The task never reached the Worker, or the Worker cleanly closed before output.
    NotDelivered,
}

label_value!(StepAttemptReturnReason { Unsupported => "unsupported", LegacyNoCapacity => "legacy_no_capacity", NotDelivered => "not_delivered" });

impl StepAttemptReturnReason {
    const ALL: [Self; 3] = [
        Self::Unsupported,
        Self::LegacyNoCapacity,
        Self::NotDelivered,
    ];

    const fn index(self) -> usize {
        match self {
            Self::Unsupported => 0,
            Self::LegacyNoCapacity => 1,
            Self::NotDelivered => 2,
        }
    }
}

#[derive(Clone, Debug, EncodeLabelSet)]
struct StepAttemptReturnLabel {
    reason: StepAttemptReturnReason,
}

/// Committed returns of started Step attempts by reason.
#[derive(Clone, Debug)]
pub struct StepAttemptsReturned {
    counts: Arc<[Counter; 3]>,
}

impl Default for StepAttemptsReturned {
    fn default() -> Self {
        Self {
            counts: Arc::new(std::array::from_fn(|_| Counter::default())),
        }
    }
}

impl StepAttemptsReturned {
    /// Records one committed return.
    pub fn record(&self, reason: StepAttemptReturnReason) {
        self.counts[reason.index()].inc();
    }
}

impl EncodeMetric for StepAttemptsReturned {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for reason in StepAttemptReturnReason::ALL {
            encoder
                .encode_family(&StepAttemptReturnLabel { reason })?
                .encode_counter::<NoLabelSet, _, u64>(&self.counts[reason.index()].get(), None)?;
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Counter
    }
}

/// A Worker frame that breaks the reserved-slot dispatch protocol.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum WorkerStreamProtocolError {
    /// A reserved-slot stream declined a task for capacity.
    NoCapacityOnReservedSlot,
    /// A reserved-slot stream half-closed after task output but before its final output.
    HalfCloseAfterOutput,
    /// A Worker declined a task after it had sent output for it.
    RejectAfterOutput,
}

label_value!(WorkerStreamProtocolError { NoCapacityOnReservedSlot => "no_capacity_on_reserved_slot", HalfCloseAfterOutput => "half_close_after_output", RejectAfterOutput => "reject_after_output" });

impl WorkerStreamProtocolError {
    const ALL: [Self; 3] = [
        Self::NoCapacityOnReservedSlot,
        Self::HalfCloseAfterOutput,
        Self::RejectAfterOutput,
    ];

    const fn index(self) -> usize {
        match self {
            Self::NoCapacityOnReservedSlot => 0,
            Self::HalfCloseAfterOutput => 1,
            Self::RejectAfterOutput => 2,
        }
    }
}

#[derive(Clone, Debug, EncodeLabelSet)]
struct WorkerStreamProtocolErrorLabel {
    error: WorkerStreamProtocolError,
}

/// Worker stream protocol errors by kind.
#[derive(Clone, Debug)]
pub struct WorkerStreamProtocolErrors {
    counts: Arc<[Counter; 3]>,
}

impl Default for WorkerStreamProtocolErrors {
    fn default() -> Self {
        Self {
            counts: Arc::new(std::array::from_fn(|_| Counter::default())),
        }
    }
}

impl WorkerStreamProtocolErrors {
    /// Records one protocol error.
    pub fn record(&self, error: WorkerStreamProtocolError) {
        self.counts[error.index()].inc();
    }
}

impl EncodeMetric for WorkerStreamProtocolErrors {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for error in WorkerStreamProtocolError::ALL {
            encoder
                .encode_family(&WorkerStreamProtocolErrorLabel { error })?
                .encode_counter::<NoLabelSet, _, u64>(&self.counts[error.index()].get(), None)?;
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Counter
    }
}

/// Why no connected stream serves a queue with backlog.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum UnservedTaskQueueReason {
    /// No connected Worker serves the queue's Flow type.
    NoWorker,
    /// Every connected Worker of the Flow type declined the queue's Step type.
    NoSupportingWorker,
}

label_value!(UnservedTaskQueueReason { NoWorker => "no_worker", NoSupportingWorker => "no_supporting_worker" });

impl UnservedTaskQueueReason {
    const ALL: [Self; 2] = [Self::NoWorker, Self::NoSupportingWorker];

    const fn index(self) -> usize {
        match self {
            Self::NoWorker => 0,
            Self::NoSupportingWorker => 1,
        }
    }
}

#[derive(Clone, Debug, EncodeLabelSet)]
struct UnservedTaskQueueLabel {
    reason: UnservedTaskQueueReason,
}

/// Queues with backlog that no connected stream can serve, by reason.
#[derive(Clone, Debug)]
pub struct UnservedTaskQueues {
    counts: Arc<[Gauge; 2]>,
}

impl Default for UnservedTaskQueues {
    fn default() -> Self {
        Self {
            counts: Arc::new(std::array::from_fn(|_| Gauge::default())),
        }
    }
}

impl UnservedTaskQueues {
    /// Sets the number of unserved queues for `reason`.
    pub fn set(&self, reason: UnservedTaskQueueReason, count: i64) {
        self.counts[reason.index()].set(count);
    }
}

impl EncodeMetric for UnservedTaskQueues {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for reason in UnservedTaskQueueReason::ALL {
            encoder
                .encode_family(&UnservedTaskQueueLabel { reason })?
                .encode_gauge(&self.counts[reason.index()].get())?;
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Gauge
    }
}

/// Why the server dropped a Step Stream write that a Worker sent.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum StepStreamWriteDropReason {
    /// The Stream Store backend is disabled.
    Disabled,
    /// The write had no Value, Step execution ID, or valid capacity.
    InvalidArgument,
    /// The serialized Value exceeded streamStore.maxMessageBytes.
    MessageTooLarge,
    /// The Stream's capacity was full until background trimming caught up.
    StreamCapacityExceeded,
    /// streamStore.maxTotalBytes was full until background trimming caught up.
    TotalCapacityExceeded,
    /// The Stream Store was closing.
    Closed,
}

label_value!(StepStreamWriteDropReason {
    Disabled => "disabled",
    InvalidArgument => "invalid_argument",
    MessageTooLarge => "message_too_large",
    StreamCapacityExceeded => "stream_capacity_exceeded",
    TotalCapacityExceeded => "total_capacity_exceeded",
    Closed => "closed",
});

impl StepStreamWriteDropReason {
    const ALL: [Self; 6] = [
        Self::Disabled,
        Self::InvalidArgument,
        Self::MessageTooLarge,
        Self::StreamCapacityExceeded,
        Self::TotalCapacityExceeded,
        Self::Closed,
    ];

    const fn index(self) -> usize {
        match self {
            Self::Disabled => 0,
            Self::InvalidArgument => 1,
            Self::MessageTooLarge => 2,
            Self::StreamCapacityExceeded => 3,
            Self::TotalCapacityExceeded => 4,
            Self::Closed => 5,
        }
    }
}

#[derive(Clone, Debug, EncodeLabelSet)]
struct StepStreamWriteDropLabel {
    reason: StepStreamWriteDropReason,
}

/// Step Stream writes dropped by reason; a dropped write never fails the Step.
#[derive(Clone, Debug)]
pub struct StepStreamWritesDropped {
    counts: Arc<[Counter; 6]>,
}

impl Default for StepStreamWritesDropped {
    fn default() -> Self {
        Self {
            counts: Arc::new(std::array::from_fn(|_| Counter::default())),
        }
    }
}

impl StepStreamWritesDropped {
    /// Records one dropped write.
    pub fn record(&self, reason: StepStreamWriteDropReason) {
        self.counts[reason.index()].inc();
    }

    /// Returns the drops recorded for `reason`.
    pub fn count(&self, reason: StepStreamWriteDropReason) -> u64 {
        self.counts[reason.index()].get()
    }
}

impl EncodeMetric for StepStreamWritesDropped {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for reason in StepStreamWriteDropReason::ALL {
            encoder
                .encode_family(&StepStreamWriteDropLabel { reason })?
                .encode_counter::<NoLabelSet, _, u64>(&self.counts[reason.index()].get(), None)?;
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Counter
    }
}
