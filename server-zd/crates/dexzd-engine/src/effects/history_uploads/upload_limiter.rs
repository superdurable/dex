// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Adaptive limit on history segment uploads: how many run at once, and the encoded bytes they
//! reserve.

use std::sync::{Arc, Mutex, MutexGuard};

use dexzd_metrics::NodeMetrics;
use prometheus_client::metrics::gauge::Gauge;
use tokio::sync::{OwnedSemaphorePermit, Semaphore};
use tokio_util::sync::CancellationToken;

use crate::effects::EffectError;

/// Limits of history segment uploads on one node.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct HistoryUploadLimits {
    /// Default 256, `history.uploadConcurrency`. Most sealed history segments this node reads,
    /// encodes, and uploads at once. Values below 1 count as 1.
    pub max_concurrency: usize,
    /// Default 16, `history.uploadMinConcurrency`. Uploads allowed at once while no upload waits
    /// for a slot. Each upload that object storage accepts while others wait raises the limit by
    /// one, up to `max_concurrency`; each slot released while none waits lowers it by one, down
    /// to this value. A retryable object storage failure halves the limit. Values below 1 count
    /// as 1; values above `max_concurrency` count as `max_concurrency`.
    pub min_concurrency: usize,
    /// Default 128 MiB, `history.uploadMaxInFlightBytes`. Most encoded segment bytes that uploads
    /// in flight reserve at once. Each upload reserves its segment's bytes before it reads and
    /// encodes the segment. A segment larger than this reserves all of it and uploads alone.
    /// Values below 1 count as 1.
    pub max_in_flight_bytes: u64,
}

impl Default for HistoryUploadLimits {
    fn default() -> Self {
        Self {
            max_concurrency: 256,
            min_concurrency: 16,
            max_in_flight_bytes: 128 * 1024 * 1024,
        }
    }
}

/// Grants upload slots under an adaptive limit, and byte reservations under a fixed budget.
pub(crate) struct HistoryUploadLimiter {
    min_concurrency: usize,
    max_concurrency: usize,
    /// Most bytes one reservation takes: the byte budget, at most `u32::MAX`.
    max_reservation_bytes: u64,
    budget_bytes: usize,
    slots: Arc<Semaphore>,
    bytes: Arc<Semaphore>,
    state: Mutex<LimiterState>,
    concurrency_limit_gauge: Gauge,
    in_flight_bytes_gauge: Gauge,
}

struct LimiterState {
    /// Uploads allowed at once: the slot permits, available or held, minus `debt`.
    limit: usize,
    /// Held slot permits to forget on return, because the limit fell below them.
    debt: usize,
    /// Uploads waiting for a slot.
    waiting: usize,
    /// Reductions so far; only a slot granted after the last one reduces again.
    reductions: u64,
    /// Successful uploads still needed before the limit grows after a reduction.
    successes_before_growth: usize,
}

/// One upload's slot and byte reservation; dropping it returns both.
pub(crate) struct UploadSlot {
    limiter: Arc<HistoryUploadLimiter>,
    permit: Option<OwnedSemaphorePermit>,
    reserved_bytes: Option<OwnedSemaphorePermit>,
    /// Reductions made before this slot was granted.
    granted_after_reductions: u64,
}

impl HistoryUploadLimiter {
    pub(crate) fn new(limits: HistoryUploadLimits, metrics: &NodeMetrics) -> Arc<Self> {
        let concurrency_limit_gauge = metrics.history_upload_concurrency_limit.clone();
        let in_flight_bytes_gauge = metrics.history_upload_in_flight_bytes.clone();
        let max_concurrency = limits.max_concurrency.clamp(1, Semaphore::MAX_PERMITS);
        let min_concurrency = limits.min_concurrency.clamp(1, max_concurrency);
        let budget_bytes = usize::try_from(limits.max_in_flight_bytes)
            .unwrap_or(usize::MAX)
            .clamp(1, Semaphore::MAX_PERMITS);
        let max_reservation_bytes = (budget_bytes as u64).min(u64::from(u32::MAX));
        concurrency_limit_gauge.set(i64::try_from(min_concurrency).unwrap_or(i64::MAX));
        in_flight_bytes_gauge.set(0);
        Arc::new(Self {
            min_concurrency,
            max_concurrency,
            max_reservation_bytes,
            budget_bytes,
            slots: Arc::new(Semaphore::new(min_concurrency)),
            bytes: Arc::new(Semaphore::new(budget_bytes)),
            state: Mutex::new(LimiterState {
                limit: min_concurrency,
                debt: 0,
                waiting: 0,
                reductions: 0,
                successes_before_growth: 0,
            }),
            concurrency_limit_gauge,
            in_flight_bytes_gauge,
        })
    }

    /// Waits for an upload slot.
    ///
    /// Returns the slot, which reserves no bytes yet, or [`EffectError::Stopped`] once
    /// `shutdown` fires.
    pub(crate) async fn acquire_slot(
        self: &Arc<Self>,
        shutdown: &CancellationToken,
    ) -> Result<UploadSlot, EffectError> {
        let waiting = WaitingForSlot::new(self);
        // Biased for reproducible simulation; shutdown fires once.
        let permit = tokio::select! {
            biased;
            () = shutdown.cancelled() => return Err(EffectError::Stopped),
            permit = Arc::clone(&self.slots).acquire_owned() => {
                permit.map_err(|_| EffectError::Stopped)?
            }
        };
        drop(waiting);
        Ok(UploadSlot {
            limiter: Arc::clone(self),
            permit: Some(permit),
            reserved_bytes: None,
            granted_after_reductions: self.lock_state().reductions,
        })
    }

    /// Returns the most bytes one reservation takes; a segment needing more uploads alone.
    pub(crate) fn max_reservation_bytes(&self) -> u64 {
        self.max_reservation_bytes
    }

    /// Replaces the slot's reservation with one of `bytes`, releasing the old one before waiting.
    ///
    /// Returns the bytes now reserved, at most [`Self::max_reservation_bytes`], or
    /// [`EffectError::Stopped`] once `shutdown` fires.
    pub(crate) async fn reserve_bytes(
        &self,
        slot: &mut UploadSlot,
        bytes: u64,
        shutdown: &CancellationToken,
    ) -> Result<u64, EffectError> {
        slot.reserved_bytes = None;
        self.publish_in_flight_bytes();
        let wanted = u32::try_from(bytes.clamp(1, self.max_reservation_bytes))
            .expect("a reservation is at most u32::MAX bytes");
        // Biased for reproducible simulation; shutdown fires once.
        let reservation = tokio::select! {
            biased;
            () = shutdown.cancelled() => return Err(EffectError::Stopped),
            reservation = Arc::clone(&self.bytes).acquire_many_owned(wanted) => {
                reservation.map_err(|_| EffectError::Stopped)?
            }
        };
        slot.reserved_bytes = Some(reservation);
        self.publish_in_flight_bytes();
        Ok(u64::from(wanted))
    }

    /// Shrinks the slot's reservation to `bytes`, the size of its encoded segment.
    pub(crate) fn trim_reservation(&self, slot: &mut UploadSlot, bytes: u64) {
        if let Some(reservation) = slot.reserved_bytes.as_mut() {
            let kept = usize::try_from(bytes.max(1)).unwrap_or(usize::MAX);
            let excess = reservation.num_permits().saturating_sub(kept);
            if excess > 0 {
                drop(reservation.split(excess));
            }
        }
        self.publish_in_flight_bytes();
    }

    /// Records an upload that object storage accepted. Grows the limit by one while uploads wait.
    pub(crate) fn record_stored_upload(&self) {
        let mut state = self.lock_state();
        if state.successes_before_growth > 0 {
            state.successes_before_growth -= 1;
            return;
        }
        // More slots cannot help while reservations use the whole byte budget.
        if state.waiting == 0
            || state.limit >= self.max_concurrency
            || self.bytes.available_permits() == 0
        {
            return;
        }
        state.limit += 1;
        if state.debt > 0 {
            state.debt -= 1;
        } else {
            self.slots.add_permits(1);
        }
        self.publish_limit(&state);
    }

    /// Halves the limit after a retryable upload failure, such as S3 SlowDown.
    /// Failures of slots granted before the last reduction change nothing.
    pub(crate) fn record_retryable_failure(&self, granted_after_reductions: u64) {
        let mut state = self.lock_state();
        if granted_after_reductions != state.reductions {
            return;
        }
        state.reductions += 1;
        let reduced = (state.limit / 2).max(self.min_concurrency);
        state.successes_before_growth = reduced;
        let removed = state.limit - reduced;
        state.limit = reduced;
        let forgotten = self.slots.forget_permits(removed);
        state.debt += removed - forgotten;
        self.publish_limit(&state);
    }

    /// Returns a released slot permit, or forgets it to pay debt or to lower an idle limit.
    fn release_slot(&self, permit: OwnedSemaphorePermit) {
        let mut state = self.lock_state();
        if state.debt > 0 {
            state.debt -= 1;
            permit.forget();
        } else if state.waiting == 0 && state.limit > self.min_concurrency {
            state.limit -= 1;
            permit.forget();
            self.publish_limit(&state);
        } else {
            drop(permit);
        }
    }

    fn publish_limit(&self, state: &LimiterState) {
        self.concurrency_limit_gauge
            .set(i64::try_from(state.limit).unwrap_or(i64::MAX));
    }

    fn publish_in_flight_bytes(&self) {
        let in_flight = self
            .budget_bytes
            .saturating_sub(self.bytes.available_permits());
        self.in_flight_bytes_gauge
            .set(i64::try_from(in_flight).unwrap_or(i64::MAX));
    }

    fn lock_state(&self) -> MutexGuard<'_, LimiterState> {
        self.state
            .lock()
            .expect("the upload limiter lock is never poisoned")
    }

    /// Takes every slot of the current limit, so uploads wait until the permit drops.
    #[cfg(test)]
    pub(crate) async fn hold_every_slot_for_test(&self) -> OwnedSemaphorePermit {
        let limit = u32::try_from(self.lock_state().limit).expect("test limits are small");
        Arc::clone(&self.slots)
            .acquire_many_owned(limit)
            .await
            .expect("the upload slots stay open")
    }
}

impl UploadSlot {
    /// Returns the reductions made before this slot was granted.
    pub(crate) fn granted_after_reductions(&self) -> u64 {
        self.granted_after_reductions
    }
}

impl Drop for UploadSlot {
    fn drop(&mut self) {
        if self.reserved_bytes.take().is_some() {
            self.limiter.publish_in_flight_bytes();
        }
        if let Some(permit) = self.permit.take() {
            self.limiter.release_slot(permit);
        }
    }
}

/// Counts an upload as waiting until it gets a slot or stops waiting.
struct WaitingForSlot<'a> {
    limiter: &'a HistoryUploadLimiter,
}

impl<'a> WaitingForSlot<'a> {
    fn new(limiter: &'a HistoryUploadLimiter) -> Self {
        limiter.lock_state().waiting += 1;
        Self { limiter }
    }
}

impl Drop for WaitingForSlot<'_> {
    fn drop(&mut self) {
        self.limiter.lock_state().waiting -= 1;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn limiter(min_concurrency: usize, max_concurrency: usize) -> Arc<HistoryUploadLimiter> {
        HistoryUploadLimiter::new(
            HistoryUploadLimits {
                max_concurrency,
                min_concurrency,
                max_in_flight_bytes: 1024,
            },
            &NodeMetrics::unregistered_with_partitions(1),
        )
    }

    /// Grows the limit to `target` with uploads that succeed while one more waits.
    async fn grow_to(limiter: &Arc<HistoryUploadLimiter>, target: usize) {
        let shutdown = CancellationToken::new();
        while limiter.lock_state().limit < target {
            let _waiting = WaitingForSlot::new(limiter);
            let slot = limiter.acquire_slot(&shutdown).await.expect("a free slot");
            limiter.record_stored_upload();
            drop(slot);
        }
    }

    #[tokio::test]
    async fn failures_of_one_window_halve_the_limit_once_and_held_slots_pay_the_debt() {
        let limiter = limiter(2, 64);
        grow_to(&limiter, 32).await;
        let shutdown = CancellationToken::new();
        let mut slots = Vec::new();
        for _ in 0..32 {
            slots.push(limiter.acquire_slot(&shutdown).await.expect("a free slot"));
        }
        for slot in &slots {
            limiter.record_retryable_failure(slot.granted_after_reductions());
        }
        assert_eq!(limiter.lock_state().limit, 16);
        assert_eq!(limiter.lock_state().debt, 16);
        drop(slots);
        let state = limiter.lock_state();
        assert_eq!(state.debt, 0);
        // Slots released with none waiting also lowered the limit to the minimum.
        assert_eq!(state.limit, 2);
        assert_eq!(limiter.slots.available_permits(), 2);
    }

    #[tokio::test]
    async fn growth_waits_for_successes_after_a_reduction() {
        let limiter = limiter(4, 64);
        grow_to(&limiter, 16).await;
        let reductions = limiter.lock_state().reductions;
        limiter.record_retryable_failure(reductions);
        assert_eq!(limiter.lock_state().limit, 8);
        let _waiting = WaitingForSlot::new(&limiter);
        for _ in 0..8 {
            limiter.record_stored_upload();
        }
        assert_eq!(limiter.lock_state().limit, 8);
        limiter.record_stored_upload();
        assert_eq!(limiter.lock_state().limit, 9);
    }

    #[tokio::test]
    async fn an_oversized_reservation_takes_the_whole_budget() {
        let limiter = limiter(1, 1);
        let shutdown = CancellationToken::new();
        let mut slot = limiter.acquire_slot(&shutdown).await.expect("a free slot");
        let reserved = limiter
            .reserve_bytes(&mut slot, 10_000, &shutdown)
            .await
            .expect("the budget is free");
        assert_eq!(reserved, 1024);
        assert_eq!(limiter.in_flight_bytes_gauge.get(), 1024);
        limiter.trim_reservation(&mut slot, 100);
        assert_eq!(limiter.in_flight_bytes_gauge.get(), 100);
        drop(slot);
        assert_eq!(limiter.in_flight_bytes_gauge.get(), 0);
    }
}
