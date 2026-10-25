// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, OnceLock};
use std::time::{Duration, Instant};

/// Stored offset that means no lease is held.
const DISARMED: u64 = 0;
/// How far before the first armed deadline offsets start, so later deadlines are never earlier.
const ORIGIN_LEAD: Duration = Duration::from_secs(3600);

/// The node lease's local monotonic deadline; any thread reads it without locks or the runtime.
#[derive(Clone, Debug)]
pub struct LeaseValidityDeadline {
    cell: Arc<DeadlineCell>,
}

#[derive(Debug)]
struct DeadlineCell {
    /// Derived from the first armed deadline, so a paused Tokio clock and real time never mix.
    origin: OnceLock<Instant>,
    /// Nanoseconds after `origin`, plus one; [`DISARMED`] when no lease is held.
    offset_nanos: AtomicU64,
}

impl LeaseValidityDeadline {
    /// Creates a deadline that holds no lease until [`Self::arm_until`] runs.
    pub fn disarmed() -> Self {
        Self {
            cell: Arc::new(DeadlineCell {
                origin: OnceLock::new(),
                offset_nanos: AtomicU64::new(DISARMED),
            }),
        }
    }

    /// Records a successful acquisition or renewal that keeps the lease valid until `valid_until`.
    pub fn arm_until(&self, valid_until: Instant) {
        let origin = *self
            .cell
            .origin
            .get_or_init(|| valid_until.checked_sub(ORIGIN_LEAD).unwrap_or(valid_until));
        let offset = valid_until.saturating_duration_since(origin);
        let offset_nanos = u64::try_from(offset.as_nanos())
            .unwrap_or(u64::MAX - 1)
            .min(u64::MAX - 1);
        self.cell
            .offset_nanos
            .store(offset_nanos + 1, Ordering::Release);
    }

    /// Records a released lease. A lost lease stays armed, since serving may last until its deadline.
    pub fn disarm(&self) {
        self.cell.offset_nanos.store(DISARMED, Ordering::Release);
    }

    /// Returns the armed deadline, or `None` when no lease is held.
    pub fn armed_deadline(&self) -> Option<Instant> {
        match self.cell.offset_nanos.load(Ordering::Acquire) {
            DISARMED => None,
            stored => {
                let origin = self
                    .cell
                    .origin
                    .get()
                    .expect("arming sets the origin before it stores an offset");
                Some(*origin + Duration::from_nanos(stored - 1))
            }
        }
    }

    /// Returns whether a lease is held and `now` is before its deadline.
    ///
    /// On a paused Tokio clock, pass `tokio::time::Instant::now().into_std()`.
    pub fn is_valid_at(&self, now: Instant) -> bool {
        self.armed_deadline().is_some_and(|deadline| now < deadline)
    }
}

impl Default for LeaseValidityDeadline {
    fn default() -> Self {
        Self::disarmed()
    }
}
