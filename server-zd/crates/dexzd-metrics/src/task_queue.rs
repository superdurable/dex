// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Durable Step task queue metrics (plan section 3.10). Labels are closed sets.

use std::sync::Arc;

use prometheus_client::encoding::{EncodeLabelSet, EncodeMetric, MetricEncoder};
use prometheus_client::metrics::MetricType;
use prometheus_client::metrics::gauge::Gauge;

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
