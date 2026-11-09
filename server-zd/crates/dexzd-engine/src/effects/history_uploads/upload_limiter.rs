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
    /// Default 128 MiB, `history.uploadMaxInFlightBytes`. Most bytes that uploads in flight
    /// reserve at once for encoding segments and holding the encoded objects. Each upload reserves
    /// the most memory its encoding can allocate before it encodes, then keeps only its object's
    /// size. A segment larger than this reserves all of it and uploads alone. Reservations count
    /// whole KiB. Values below 1 KiB count as 1 KiB; values above 4 TiB count as 4 TiB.
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
    /// The byte budget in permits of [`BYTES_PER_PERMIT`], rounded up.
    budget_permits: u32,
    slots: Arc<Semaphore>,
    /// Byte budget permits; one permit stands for [`BYTES_PER_PERMIT`] bytes.
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
    reserved_bytes: Option<ByteReservation>,
    /// Reductions made before this slot was granted.
    granted_after_reductions: u64,
}

/// Byte budget permits that the in-flight bytes gauge counts until they drop.
struct ByteReservation {
    permits: OwnedSemaphorePermit,
    in_flight_bytes_gauge: Gauge,
}

/// The bytes a reservation holds.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) struct GrantedBytes {
    pub(crate) bytes: u64,
    /// Whether it holds the whole budget, which lets a segment of any size proceed alone.
    pub(crate) is_whole_budget: bool,
}

/// Bytes per byte budget permit; a 32-bit permit count then covers budgets up to 4 TiB.
const BYTES_PER_PERMIT: u64 = 1024;

impl HistoryUploadLimiter {
    pub(crate) fn new(limits: HistoryUploadLimits, metrics: &NodeMetrics) -> Arc<Self> {
        let concurrency_limit_gauge = metrics.history_upload_concurrency_limit.clone();
        let in_flight_bytes_gauge = metrics.history_upload_in_flight_bytes.clone();
        let max_concurrency = limits.max_concurrency.clamp(1, Semaphore::MAX_PERMITS);
        let min_concurrency = limits.min_concurrency.clamp(1, max_concurrency);
        let budget_permits = u32::try_from(permits_for(limits.max_in_flight_bytes))
            .unwrap_or(u32::MAX)
            .max(1);
        concurrency_limit_gauge.set(i64::try_from(min_concurrency).unwrap_or(i64::MAX));
        in_flight_bytes_gauge.set(0);
        Arc::new(Self {
            min_concurrency,
            max_concurrency,
            budget_permits,
            slots: Arc::new(Semaphore::new(min_concurrency)),
            bytes: Arc::new(Semaphore::new(budget_permits as usize)),
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

    /// Replaces the slot's reservation with one of at least `bytes`, releasing the old one before
    /// waiting. A request above the budget takes the whole budget.
    ///
    /// Returns what the slot now holds, or [`EffectError::Stopped`] once `shutdown` fires.
    pub(crate) async fn reserve_bytes(
        &self,
        slot: &mut UploadSlot,
        bytes: u64,
        shutdown: &CancellationToken,
    ) -> Result<GrantedBytes, EffectError> {
        slot.reserved_bytes = None;
        let wanted = u32::try_from(permits_for(bytes).clamp(1, u64::from(self.budget_permits)))
            .expect("a reservation is at most the budget, which fits in u32");
        // Biased for reproducible simulation; shutdown fires once.
        let permits = tokio::select! {
            biased;
            () = shutdown.cancelled() => return Err(EffectError::Stopped),
            permits = Arc::clone(&self.bytes).acquire_many_owned(wanted) => {
                permits.map_err(|_| EffectError::Stopped)?
            }
        };
        let reservation = ByteReservation::new(permits, self.in_flight_bytes_gauge.clone());
        slot.reserved_bytes = Some(reservation);
        Ok(GrantedBytes {
            bytes: u64::from(wanted) * BYTES_PER_PERMIT,
            is_whole_budget: wanted == self.budget_permits,
        })
    }

    /// Shrinks the slot's reservation to cover `bytes`, the size of its encoded segment.
    pub(crate) fn trim_reservation(&self, slot: &mut UploadSlot, bytes: u64) {
        if let Some(reservation) = slot.reserved_bytes.as_mut() {
            reservation.shrink_to(permits_for(bytes).max(1));
        }
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
        self.reserved_bytes = None;
        if let Some(permit) = self.permit.take() {
            self.limiter.release_slot(permit);
        }
    }
}

impl ByteReservation {
    fn new(permits: OwnedSemaphorePermit, in_flight_bytes_gauge: Gauge) -> Self {
        in_flight_bytes_gauge.inc_by(gauge_bytes(permits.num_permits()));
        Self {
            permits,
            in_flight_bytes_gauge,
        }
    }

    /// Releases every permit beyond `permits`.
    fn shrink_to(&mut self, permits: u64) {
        let kept = usize::try_from(permits).unwrap_or(usize::MAX);
        let excess = self.permits.num_permits().saturating_sub(kept);
        if excess > 0 {
            drop(self.permits.split(excess));
            self.in_flight_bytes_gauge.dec_by(gauge_bytes(excess));
        }
    }
}

impl Drop for ByteReservation {
    fn drop(&mut self) {
        self.in_flight_bytes_gauge
            .dec_by(gauge_bytes(self.permits.num_permits()));
    }
}

/// Returns the permits that cover `bytes`, rounded up.
fn permits_for(bytes: u64) -> u64 {
    bytes.div_ceil(BYTES_PER_PERMIT)
}

fn gauge_bytes(permits: usize) -> i64 {
    i64::try_from(permits as u64 * BYTES_PER_PERMIT).unwrap_or(i64::MAX)
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

    const GIB: u64 = 1024 * 1024 * 1024;

    fn limiter(min_concurrency: usize, max_concurrency: usize) -> Arc<HistoryUploadLimiter> {
        limiter_with_budget(min_concurrency, max_concurrency, 8 * 1024)
    }

    fn limiter_with_budget(
        min_concurrency: usize,
        max_concurrency: usize,
        max_in_flight_bytes: u64,
    ) -> Arc<HistoryUploadLimiter> {
        HistoryUploadLimiter::new(
            HistoryUploadLimits {
                max_concurrency,
                min_concurrency,
                max_in_flight_bytes,
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
        assert_eq!(
            reserved,
            GrantedBytes {
                bytes: 8 * 1024,
                is_whole_budget: true
            }
        );
        assert_eq!(limiter.in_flight_bytes_gauge.get(), 8 * 1024);
        limiter.trim_reservation(&mut slot, 100);
        assert_eq!(limiter.in_flight_bytes_gauge.get(), 1024);
        drop(slot);
        assert_eq!(limiter.in_flight_bytes_gauge.get(), 0);
    }

    #[tokio::test]
    async fn reservations_above_four_gib_share_a_larger_budget_without_overlapping() {
        let limiter = limiter_with_budget(2, 2, 8 * GIB);
        let shutdown = CancellationToken::new();
        let mut first = limiter.acquire_slot(&shutdown).await.expect("a free slot");
        let mut second = limiter.acquire_slot(&shutdown).await.expect("a free slot");
        let granted = limiter
            .reserve_bytes(&mut first, 5 * GIB, &shutdown)
            .await
            .expect("the budget is free");
        assert_eq!(
            granted,
            GrantedBytes {
                bytes: 5 * GIB,
                is_whole_budget: false
            }
        );
        assert!(
            futures::FutureExt::now_or_never(limiter.reserve_bytes(
                &mut second,
                5 * GIB,
                &shutdown
            ))
            .is_none(),
            "two 5 GiB reservations never fit an 8 GiB budget"
        );
        drop(first);
        let granted = limiter
            .reserve_bytes(&mut second, 9 * GIB, &shutdown)
            .await
            .expect("the budget is free again");
        assert_eq!(
            granted,
            GrantedBytes {
                bytes: 8 * GIB,
                is_whole_budget: true
            }
        );
        assert_eq!(
            limiter.in_flight_bytes_gauge.get(),
            i64::try_from(8 * GIB).expect("fits")
        );

        let largest = limiter_with_budget(1, 1, 64 * GIB);
        let mut slot = largest.acquire_slot(&shutdown).await.expect("a free slot");
        let granted = largest
            .reserve_bytes(&mut slot, 100 * GIB, &shutdown)
            .await
            .expect("the budget is free");
        assert_eq!(granted.bytes, 64 * GIB);
        assert!(granted.is_whole_budget);
    }

    #[test]
    fn concurrent_releases_leave_the_in_flight_bytes_gauge_at_zero() {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .build()
            .expect("a test runtime starts");
        let limiter = limiter_with_budget(2, 2, 1024 * 1024);
        let shutdown = CancellationToken::new();
        for _ in 0..2_000 {
            let slots: Vec<UploadSlot> = runtime.block_on(async {
                let mut slots = Vec::new();
                for bytes in [3_000, 70_000] {
                    let mut slot = limiter.acquire_slot(&shutdown).await.expect("a free slot");
                    limiter
                        .reserve_bytes(&mut slot, bytes, &shutdown)
                        .await
                        .expect("the budget is free");
                    slots.push(slot);
                }
                slots
            });
            let barrier = std::sync::Barrier::new(slots.len());
            std::thread::scope(|scope| {
                for slot in slots {
                    let barrier = &barrier;
                    scope.spawn(move || {
                        barrier.wait();
                        drop(slot);
                    });
                }
            });
            assert_eq!(limiter.in_flight_bytes_gauge.get(), 0);
        }
    }
}
