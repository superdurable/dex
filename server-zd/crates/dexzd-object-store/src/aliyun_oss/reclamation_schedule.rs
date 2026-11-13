// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! When each key's next reclamation pass runs: coalesced, throttled, followed up, and retried.
//!
//! Local time only decides when a pass runs. Each pass decides deletions from store timestamps.

use std::collections::{BTreeMap, BTreeSet};
use std::time::Duration;

use tokio::time::Instant;

/// How often passes of one key may run and how failed passes are retried.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(super) struct ReclamationTimings {
    /// Shortest time between the starts of two passes of one key.
    pub(super) minimum_interval_between_passes: Duration,
    /// Wait before the first retry of a failed pass; later retries double it.
    pub(super) initial_retry_backoff: Duration,
    /// Longest wait between retries of a failed pass.
    pub(super) maximum_retry_backoff: Duration,
}

/// One reclamation pass that is due now.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(super) struct DueReclamationPass {
    pub(super) logical_key: String,
    /// The highest confirmed generation of the key that requested a pass.
    pub(super) confirmed_generation: u64,
    /// Whether the pass reads the store's clock, because it runs well after that write.
    pub(super) needs_store_time: bool,
    /// Failed attempts of this pass so far.
    pub(super) failed_attempts: u32,
}

/// The tracked state of one key.
#[derive(Debug)]
struct ScheduledKey {
    /// The generation of the next pass; `None` while the key is only remembered for throttling.
    confirmed_generation: Option<u64>,
    needs_store_time: bool,
    failed_attempts: u32,
    last_pass_started_at: Option<Instant>,
    /// When the next pass runs, or when the key is forgotten without a pending generation.
    deadline: Instant,
}

/// Orders the reclamation passes of every key.
///
/// Each tracked key has exactly one deadline in `deadlines`.
#[derive(Debug)]
pub(super) struct ReclamationSchedule {
    timings: ReclamationTimings,
    maximum_tracked_keys: usize,
    keys: BTreeMap<String, ScheduledKey>,
    deadlines: BTreeSet<(Instant, String)>,
}

impl ReclamationSchedule {
    /// Creates an empty schedule that tracks at most `maximum_tracked_keys` keys.
    pub(super) fn new(timings: ReclamationTimings, maximum_tracked_keys: usize) -> Self {
        assert!(
            maximum_tracked_keys > 0,
            "the schedule tracks at least one key"
        );
        Self {
            timings,
            maximum_tracked_keys,
            keys: BTreeMap::new(),
            deadlines: BTreeSet::new(),
        }
    }

    /// Schedules a pass for `confirmed_generation` of `logical_key`, merged with any pending one.
    /// It runs once the interval since the key's last pass has ended.
    pub(super) fn record_confirmed_write(
        &mut self,
        logical_key: &str,
        confirmed_generation: u64,
        now: Instant,
    ) {
        let interval = self.timings.minimum_interval_between_passes;
        let Some(state) = self.keys.get_mut(logical_key) else {
            self.make_room_for_one_key();
            self.deadlines.insert((now, logical_key.to_owned()));
            self.keys.insert(
                logical_key.to_owned(),
                ScheduledKey {
                    confirmed_generation: Some(confirmed_generation),
                    needs_store_time: false,
                    failed_attempts: 0,
                    last_pass_started_at: None,
                    deadline: now,
                },
            );
            return;
        };
        let throttled_until = state
            .last_pass_started_at
            .map_or(now, |started_at| (started_at + interval).max(now));
        let is_newer_generation = state
            .confirmed_generation
            .is_none_or(|pending| confirmed_generation > pending);
        let new_deadline = if state.confirmed_generation.is_some() {
            state.deadline.min(throttled_until)
        } else {
            throttled_until
        };
        if is_newer_generation {
            state.confirmed_generation = Some(confirmed_generation);
            state.needs_store_time = false;
            state.failed_attempts = 0;
        }
        let old_deadline = std::mem::replace(&mut state.deadline, new_deadline);
        self.deadlines
            .remove(&(old_deadline, logical_key.to_owned()));
        self.deadlines
            .insert((new_deadline, logical_key.to_owned()));
    }

    /// Returns the earliest deadline of any tracked key.
    pub(super) fn next_deadline(&self) -> Option<Instant> {
        self.deadlines.first().map(|(deadline, _)| *deadline)
    }

    /// Returns the next pass due at `now`, forgetting keys whose throttle interval has ended.
    /// The returned key stays tracked until its throttle interval ends, so later writes wait.
    pub(super) fn take_due_pass(&mut self, now: Instant) -> Option<DueReclamationPass> {
        while let Some((deadline, logical_key)) = self.deadlines.first().cloned() {
            if deadline > now {
                return None;
            }
            self.deadlines.pop_first();
            let state = self
                .keys
                .get_mut(&logical_key)
                .expect("every deadline belongs to a tracked key");
            let Some(confirmed_generation) = state.confirmed_generation.take() else {
                self.keys.remove(&logical_key);
                continue;
            };
            state.last_pass_started_at = Some(now);
            state.deadline = now + self.timings.minimum_interval_between_passes;
            self.deadlines.insert((state.deadline, logical_key.clone()));
            return Some(DueReclamationPass {
                logical_key,
                confirmed_generation,
                needs_store_time: state.needs_store_time,
                failed_attempts: state.failed_attempts,
            });
        }
        None
    }

    /// Schedules `pass` again no earlier than `try_at`, when versions it kept may be old enough.
    pub(super) fn record_follow_up(&mut self, pass: DueReclamationPass, try_at: Instant) {
        self.schedule_again(pass, try_at, 0);
    }

    /// Schedules a failed `pass` again after an exponential backoff from `now`.
    pub(super) fn record_failed_pass(&mut self, pass: DueReclamationPass, now: Instant) {
        let failed_attempts = pass.failed_attempts.saturating_add(1);
        let backoff = self
            .timings
            .initial_retry_backoff
            .saturating_mul(2_u32.saturating_pow(failed_attempts - 1))
            .min(self.timings.maximum_retry_backoff);
        self.schedule_again(pass, now + backoff, failed_attempts);
    }

    /// Schedules a pass of the same generation that reads the store's clock.
    ///
    /// A newer write of the key keeps its own, earlier pass instead.
    fn schedule_again(&mut self, pass: DueReclamationPass, try_at: Instant, failed_attempts: u32) {
        let interval = self.timings.minimum_interval_between_passes;
        let Some(state) = self.keys.get_mut(&pass.logical_key) else {
            return;
        };
        if state.confirmed_generation.is_some() {
            return;
        }
        let throttled_until = state
            .last_pass_started_at
            .map_or(try_at, |started_at| started_at + interval);
        let new_deadline = try_at.max(throttled_until);
        state.confirmed_generation = Some(pass.confirmed_generation);
        state.needs_store_time = true;
        state.failed_attempts = failed_attempts;
        let old_deadline = std::mem::replace(&mut state.deadline, new_deadline);
        self.deadlines
            .remove(&(old_deadline, pass.logical_key.clone()));
        self.deadlines.insert((new_deadline, pass.logical_key));
    }

    /// Forgets the key with the latest deadline when the schedule is full.
    fn make_room_for_one_key(&mut self) {
        if self.keys.len() < self.maximum_tracked_keys {
            return;
        }
        let Some((_, evicted_key)) = self.deadlines.pop_last() else {
            return;
        };
        let evicted = self
            .keys
            .remove(&evicted_key)
            .expect("every deadline belongs to a tracked key");
        tracing::warn!(
            key = %evicted_key,
            generation = ?evicted.confirmed_generation,
            "the reclamation schedule is full; a later write of the key reclaims its versions"
        );
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const TIMINGS: ReclamationTimings = ReclamationTimings {
        minimum_interval_between_passes: Duration::from_secs(300),
        initial_retry_backoff: Duration::from_secs(60),
        maximum_retry_backoff: Duration::from_secs(3_600),
    };
    const INTERVAL: Duration = TIMINGS.minimum_interval_between_passes;

    fn first_pass(logical_key: &str, confirmed_generation: u64) -> DueReclamationPass {
        DueReclamationPass {
            logical_key: logical_key.to_owned(),
            confirmed_generation,
            needs_store_time: false,
            failed_attempts: 0,
        }
    }

    fn later_pass(
        logical_key: &str,
        confirmed_generation: u64,
        failed_attempts: u32,
    ) -> DueReclamationPass {
        DueReclamationPass {
            needs_store_time: true,
            failed_attempts,
            ..first_pass(logical_key, confirmed_generation)
        }
    }

    #[test]
    fn writes_within_the_interval_coalesce_into_one_pass_of_the_highest_generation() {
        let start = Instant::now();
        let mut schedule = ReclamationSchedule::new(TIMINGS, 16);
        schedule.record_confirmed_write("lease/a", 3, start);
        assert_eq!(
            schedule.take_due_pass(start),
            Some(first_pass("lease/a", 3))
        );
        let later = start + Duration::from_secs(10);
        schedule.record_confirmed_write("lease/a", 5, later);
        schedule.record_confirmed_write("lease/a", 4, later);
        assert_eq!(
            schedule.take_due_pass(later),
            None,
            "the interval has not ended"
        );
        assert_eq!(schedule.next_deadline(), Some(start + INTERVAL));
        assert_eq!(
            schedule.take_due_pass(start + INTERVAL),
            Some(first_pass("lease/a", 5))
        );
    }

    #[test]
    fn a_key_without_pending_writes_is_forgotten_after_the_interval() {
        let start = Instant::now();
        let mut schedule = ReclamationSchedule::new(TIMINGS, 16);
        schedule.record_confirmed_write("lease/a", 3, start);
        schedule.take_due_pass(start);
        assert_eq!(schedule.take_due_pass(start + INTERVAL), None);
        assert!(schedule.keys.is_empty());
        assert_eq!(schedule.next_deadline(), None);
        let much_later = start + 3 * INTERVAL;
        schedule.record_confirmed_write("lease/a", 4, much_later);
        assert_eq!(
            schedule.take_due_pass(much_later),
            Some(first_pass("lease/a", 4))
        );
    }

    #[test]
    fn a_follow_up_reads_the_store_clock_and_a_new_write_replaces_it() {
        let start = Instant::now();
        let mut schedule = ReclamationSchedule::new(TIMINGS, 16);
        schedule.record_confirmed_write("manifest", 3, start);
        let due = schedule
            .take_due_pass(start)
            .expect("the first pass is due");
        let try_at = start + Duration::from_secs(3_000);
        schedule.record_follow_up(due, try_at);
        assert_eq!(schedule.take_due_pass(start + INTERVAL), None);
        let follow_up = schedule
            .take_due_pass(try_at)
            .expect("the follow-up is due");
        assert_eq!(follow_up, later_pass("manifest", 3, 0));
        schedule.record_follow_up(follow_up, try_at + Duration::from_secs(3_000));
        let write_at = try_at + Duration::from_secs(1);
        schedule.record_confirmed_write("manifest", 4, write_at);
        assert_eq!(schedule.next_deadline(), Some(try_at + INTERVAL));
        assert_eq!(
            schedule.take_due_pass(try_at + INTERVAL),
            Some(first_pass("manifest", 4)),
            "the new write replaces the follow-up"
        );
    }

    #[test]
    fn failed_passes_retry_with_a_doubling_backoff_up_to_its_cap() {
        let start = Instant::now();
        let mut schedule = ReclamationSchedule::new(TIMINGS, 16);
        schedule.record_confirmed_write("lease/a", 3, start);
        let mut now = start;
        let mut pass = schedule.take_due_pass(now).expect("the first pass is due");
        let mut waits = Vec::new();
        for attempt in 1..=8 {
            schedule.record_failed_pass(pass, now);
            let deadline = schedule.next_deadline().expect("the retry is scheduled");
            waits.push((deadline - now).as_secs());
            now = deadline;
            pass = schedule.take_due_pass(now).expect("the retry is due");
            assert_eq!(pass, later_pass("lease/a", 3, attempt));
        }
        // The pass interval bounds the first retries from below, the cap the last ones.
        assert_eq!(waits, [300, 300, 300, 480, 960, 1_920, 3_600, 3_600]);
    }

    #[test]
    fn a_full_schedule_forgets_the_key_with_the_latest_deadline() {
        let start = Instant::now();
        let mut schedule = ReclamationSchedule::new(TIMINGS, 2);
        schedule.record_confirmed_write("lease/a", 3, start);
        let due = schedule
            .take_due_pass(start)
            .expect("the first pass is due");
        schedule.record_follow_up(due, start + Duration::from_secs(3_000));
        schedule.record_confirmed_write("lease/b", 3, start);
        schedule.record_confirmed_write("lease/c", 3, start);
        assert_eq!(schedule.keys.len(), 2);
        assert!(!schedule.keys.contains_key("lease/a"));
        assert_eq!(schedule.deadlines.len(), 2);
        assert_eq!(
            schedule.take_due_pass(start),
            Some(first_pass("lease/b", 3))
        );
        assert_eq!(
            schedule.take_due_pass(start),
            Some(first_pass("lease/c", 3))
        );
    }
}
