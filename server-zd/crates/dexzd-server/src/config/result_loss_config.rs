// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/// Parts per million in a whole: a share of this size drops every result.
const PARTS_PER_MILLION: u32 = 1_000_000;

/// A share of Worker results, kept in parts per million so that the configuration stays `Eq`.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct ResultLossShare {
    parts_per_million: u32,
}

impl ResultLossShare {
    /// Parses a fraction from 0 to 1, such as `0.3`, rounded to parts per million.
    ///
    /// Returns the share, or a message naming the accepted range for any other text.
    pub fn parse_fraction(text: &str) -> Result<Self, String> {
        let fraction: f64 = text
            .trim()
            .parse()
            .map_err(|_| format!("{text:?} is not a number from 0 to 1"))?;
        if !(0.0..=1.0).contains(&fraction) {
            return Err(format!("{text:?} is not a number from 0 to 1"));
        }
        let parts_per_million = (fraction * f64::from(PARTS_PER_MILLION)).round();
        Ok(Self {
            // The range check above keeps the product within 0..=1_000_000.
            parts_per_million: parts_per_million as u32,
        })
    }

    /// Returns the share as a fraction from 0 to 1.
    pub fn fraction(self) -> f64 {
        f64::from(self.parts_per_million) / f64::from(PARTS_PER_MILLION)
    }

    /// Returns true when the share drops no result.
    pub fn is_none(self) -> bool {
        self.parts_per_million == 0
    }

    /// Returns true when a uniformly distributed `draw` falls in the dropped share.
    pub(crate) fn is_draw_dropped(self, draw: u64) -> bool {
        draw % u64::from(PARTS_PER_MILLION) < u64::from(self.parts_per_million)
    }
}

/// Development-only Worker result loss; only `dex-server-zd dev` flags set it, never YAML.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct WorkerResultLossConfig {
    /// Share of WaitFor and Execute results to drop, as if the Worker crashed after the method
    /// ran and before its result arrived. Defaults to none.
    pub step_results: ResultLossShare,
    /// Share of RPC results to drop the same way; the RPC caller gets `UNAVAILABLE`. Defaults to
    /// none.
    pub rpc_results: ResultLossShare,
    /// Seed for Worker result loss decisions. Defaults to 0.
    pub seed: u64,
}

impl WorkerResultLossConfig {
    /// Returns true when any Worker result may be dropped.
    pub fn is_enabled(&self) -> bool {
        !self.step_results.is_none() || !self.rpc_results.is_none()
    }
}
