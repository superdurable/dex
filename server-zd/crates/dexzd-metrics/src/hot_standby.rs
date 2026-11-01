// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Hot standby state and takeover metrics, which the server records from the standby status.

use std::sync::Arc;

use prometheus_client::encoding::{EncodeLabelSet, EncodeMetric, MetricEncoder, NoLabelSet};
use prometheus_client::metrics::MetricType;
use prometheus_client::metrics::counter::Counter;
use prometheus_client::metrics::gauge::Gauge;

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
    /// Stopped after an error it does not retry.
    Failed,
}

label_value!(HotStandbyMetricState { Inactive => "inactive", Bootstrapping => "bootstrapping", Following => "following", Behind => "behind", Failed => "failed" });

impl HotStandbyMetricState {
    const ALL: [Self; 5] = [
        Self::Inactive,
        Self::Bootstrapping,
        Self::Following,
        Self::Behind,
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
    values: Arc<[Gauge; 5]>,
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
}

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct TakeoverStandbyModeLabel {
    standby_mode: TakeoverStandbyMode,
}

/// Fixed takeover counters by standby mode.
#[derive(Clone, Debug)]
pub struct TakeoverCounters {
    values: Arc<[Counter; 2]>,
}

impl Default for TakeoverCounters {
    fn default() -> Self {
        Self {
            values: Arc::new(std::array::from_fn(|_| Counter::default())),
        }
    }
}

impl TakeoverCounters {
    /// Records one completed takeover.
    pub fn record(&self, standby_mode: TakeoverStandbyMode) {
        self.values[match standby_mode {
            TakeoverStandbyMode::Hot => 0,
            TakeoverStandbyMode::Cold => 1,
        }]
        .inc();
    }
}

impl EncodeMetric for TakeoverCounters {
    fn encode(&self, mut encoder: MetricEncoder) -> Result<(), std::fmt::Error> {
        for (index, standby_mode) in TakeoverStandbyMode::ALL.into_iter().enumerate() {
            encoder
                .encode_family(&TakeoverStandbyModeLabel { standby_mode })?
                .encode_counter::<NoLabelSet, _, u64>(&self.values[index].get(), None)?;
        }
        Ok(())
    }

    fn metric_type(&self) -> MetricType {
        MetricType::Counter
    }
}
