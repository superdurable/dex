// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use serde::{Deserialize, Serialize};

use super::{ConfigError, LeaseConfig};

/// What a process does while another process of its node ID holds the lease.
#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub enum StandbyMode {
    /// Waits for the lease, then opens its local store or restores the newest snapshot and
    /// replays the log. This is the behavior of every earlier release.
    #[default]
    Cold,
    /// Follows the node's log from object storage while it waits, so a takeover replays little.
    Hot,
}

/// How a process waits for the node lease and how the active process steps down.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct StandbyConfig {
    /// Defaults to `cold`, the behavior of earlier releases. `hot` follows the log while waiting.
    /// `hot` needs `lease.failIfHeld: false`; `dev` accepts only `cold`.
    pub mode: StandbyMode,
    /// Defaults to 250 ms; must be positive. In `hot` mode, how often a waiting process reads a
    /// lease that another process holds. The interval is at most a third of `lease.duration`.
    /// `cold` mode keeps reading at most once a second.
    #[serde(with = "humantime_serde")]
    pub held_lease_poll_interval: Duration,
    /// Defaults to 5 s; must be positive, and in `hot` mode exceed `laggingTakeoverDelay` plus
    /// `heldLeasePollInterval`. How long a process that stepped down waits before it competes for
    /// the lease again, so a standby takes over first. Without a standby, the process takes the
    /// lease back after this wait.
    #[serde(with = "humantime_serde")]
    pub step_down_hold_off: Duration,
    /// Defaults to 1500 ms. In `hot` mode, how long a standby that is not hot waits before it
    /// writes an available lease, so a hot standby takes it first. A jitter of up to 20% is
    /// added. It only reduces contention; the conditional lease write decides who takes over.
    #[serde(with = "humantime_serde")]
    pub lagging_takeover_delay: Duration,
    /// Defaults to 10 s; must be positive. In `hot` mode, the lag above which a standby reports
    /// `behind` instead of `following`, stops reporting `dex.HotStandby` SERVING, and waits
    /// `laggingTakeoverDelay` before it takes an available lease.
    #[serde(with = "humantime_serde")]
    pub behind_lag_threshold: Duration,
}

impl Default for StandbyConfig {
    fn default() -> Self {
        Self {
            mode: StandbyMode::Cold,
            held_lease_poll_interval: Duration::from_millis(250),
            step_down_hold_off: Duration::from_secs(5),
            lagging_takeover_delay: Duration::from_millis(1500),
            behind_lag_threshold: Duration::from_secs(10),
        }
    }
}

impl StandbyConfig {
    /// Checks the standby section against `lease` and reports the first failing field.
    pub(crate) fn validate(&self, lease: &LeaseConfig) -> Result<(), ConfigError> {
        let invalid = |field: &'static str, message: &str| ConfigError::Invalid {
            field,
            message: message.to_owned(),
        };
        if self.held_lease_poll_interval.is_zero() {
            return Err(invalid("standby.heldLeasePollInterval", "must be positive"));
        }
        if self.behind_lag_threshold.is_zero() {
            return Err(invalid("standby.behindLagThreshold", "must be positive"));
        }
        if self.step_down_hold_off.is_zero() {
            return Err(invalid("standby.stepDownHoldOff", "must be positive"));
        }
        // Only a hot standby defers by laggingTakeoverDelay and polls at heldLeasePollInterval.
        if self.mode == StandbyMode::Hot
            && self.step_down_hold_off
                <= self.lagging_takeover_delay + self.held_lease_poll_interval
        {
            return Err(invalid(
                "standby.stepDownHoldOff",
                "must exceed standby.laggingTakeoverDelay plus standby.heldLeasePollInterval in hot mode",
            ));
        }
        if self.mode == StandbyMode::Hot && lease.fail_if_held {
            return Err(invalid(
                "standby.mode",
                "hot needs lease.failIfHeld: false, because a hot standby waits for a held lease",
            ));
        }
        Ok(())
    }
}
