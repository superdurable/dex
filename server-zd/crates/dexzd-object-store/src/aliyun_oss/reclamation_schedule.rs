// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! When each key's next reclamation pass runs: coalesced per key, throttled, and followed up.

use std::collections::{BTreeMap, BTreeSet};
use std::time::Duration;

use tokio::time::Instant;

/// A lower bound on the store's clock: a store time and the local instant it was known.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(super) struct ObservedStoreTime {
    /// Store time, in Unix microseconds, that had passed by `observed_at`.
    pub(super) written_at_unix_micros: i64,
    /// Local instant by which the store clock had reached `written_at_unix_micros`.
    pub(super) observed_at: Instant,
}

impl ObservedStoreTime {
    /// Returns a lower bound on the store clock at `now`, advanced by local elapsed time.
    pub(super) fn lower_bound_at(&self, now: Instant) -> i64 {
        let elapsed_micros =
            i64::try_from(now.saturating_duration_since(self.observed_at).as_micros())
                .unwrap_or(i64::MAX);
        self.written_at_unix_micros.saturating_add(elapsed_micros)
    }
}

/// One reclamation pass that is due now.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(super) struct DueReclamationPass {
    pub(super) logical_key: String,
    /// The highest confirmed generation of the key that requested a pass.
    pub(super) confirmed_generation: u64,
    /// The store time an earlier pass of this generation observed, for a follow-up pass.
    pub(super) reference: Option<ObservedStoreTime>,
}

/// The tracked state of one key.
#[derive(Debug)]
struct ScheduledKey {
    /// The generation of the next pass; `None` while the key is only remembered for throttling.
    confirmed_generation: Option<u64>,
    reference: Option<ObservedStoreTime>,
    last_pass_started_at: Option<Instant>,
    /// When the next pass runs, or when the key is forgotten without a pending generation.
    deadline: Instant,
}

/// Orders the reclamation passes of every key.
///
/// Each tracked key has exactly one deadline in `deadlines`.
#[derive(Debug)]
pub(super) struct ReclamationSchedule {
    minimum_interval_between_passes: Duration,
    maximum_tracked_keys: usize,
    keys: BTreeMap<String, ScheduledKey>,
    deadlines: BTreeSet<(Instant, String)>,
}

impl ReclamationSchedule {
    /// Creates an empty schedule that runs at most one pass of a key per
    /// `minimum_interval_between_passes` and tracks at most `maximum_tracked_keys` keys.
    pub(super) fn new(
        minimum_interval_between_passes: Duration,
        maximum_tracked_keys: usize,
    ) -> Self {
        assert!(
            maximum_tracked_keys > 0,
            "the schedule tracks at least one key"
        );
        Self {
            minimum_interval_between_passes,
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
        let interval = self.minimum_interval_between_passes;
        let Some(state) = self.keys.get_mut(logical_key) else {
            self.make_room_for_one_key();
            self.deadlines.insert((now, logical_key.to_owned()));
            self.keys.insert(
                logical_key.to_owned(),
                ScheduledKey {
                    confirmed_generation: Some(confirmed_generation),
                    reference: None,
                    last_pass_started_at: None,
                    deadline: now,
                },
            );
            return;
        };
        let throttled_until = state
            .last_pass_started_at
            .map_or(now, |started_at| (started_at + interval).max(now));
        let new_deadline = match state.confirmed_generation {
            Some(pending) => {
                if confirmed_generation > pending {
                    state.confirmed_generation = Some(confirmed_generation);
                    state.reference = None;
                }
                state.deadline.min(throttled_until)
            }
            None => {
                state.confirmed_generation = Some(confirmed_generation);
                state.reference = None;
                throttled_until
            }
        };
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
            let reference = state.reference.take();
            state.last_pass_started_at = Some(now);
            state.deadline = now + self.minimum_interval_between_passes;
            self.deadlines.insert((state.deadline, logical_key.clone()));
            return Some(DueReclamationPass {
                logical_key,
                confirmed_generation,
                reference,
            });
        }
        None
    }

    /// Schedules `pass` again at `reclaimable_at`, unless a newer write already scheduled one.
    pub(super) fn record_follow_up(
        &mut self,
        pass: DueReclamationPass,
        reference: ObservedStoreTime,
        reclaimable_at: Instant,
    ) {
        let interval = self.minimum_interval_between_passes;
        let Some(state) = self.keys.get_mut(&pass.logical_key) else {
            return;
        };
        if state.confirmed_generation.is_some() {
            return;
        }
        let throttled_until = state
            .last_pass_started_at
            .map_or(reclaimable_at, |started_at| started_at + interval);
        let new_deadline = reclaimable_at.max(throttled_until);
        state.confirmed_generation = Some(pass.confirmed_generation);
        state.reference = Some(reference);
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

    const INTERVAL: Duration = Duration::from_secs(300);

    fn pass(logical_key: &str, confirmed_generation: u64) -> DueReclamationPass {
        DueReclamationPass {
            logical_key: logical_key.to_owned(),
            confirmed_generation,
            reference: None,
        }
    }

    #[test]
    fn writes_within_the_interval_coalesce_into_one_pass_of_the_highest_generation() {
        let start = Instant::now();
        let mut schedule = ReclamationSchedule::new(INTERVAL, 16);
        schedule.record_confirmed_write("lease/a", 3, start);
        assert_eq!(schedule.take_due_pass(start), Some(pass("lease/a", 3)));
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
            Some(pass("lease/a", 5))
        );
    }

    #[test]
    fn a_key_without_pending_writes_is_forgotten_after_the_interval() {
        let start = Instant::now();
        let mut schedule = ReclamationSchedule::new(INTERVAL, 16);
        schedule.record_confirmed_write("lease/a", 3, start);
        schedule.take_due_pass(start);
        assert_eq!(schedule.take_due_pass(start + INTERVAL), None);
        assert!(schedule.keys.is_empty());
        assert_eq!(schedule.next_deadline(), None);
        let much_later = start + 3 * INTERVAL;
        schedule.record_confirmed_write("lease/a", 4, much_later);
        assert_eq!(schedule.take_due_pass(much_later), Some(pass("lease/a", 4)));
    }

    #[test]
    fn a_follow_up_waits_for_its_versions_and_a_new_write_moves_it_earlier() {
        let start = Instant::now();
        let mut schedule = ReclamationSchedule::new(INTERVAL, 16);
        schedule.record_confirmed_write("manifest", 3, start);
        let due = schedule
            .take_due_pass(start)
            .expect("the first pass is due");
        let reference = ObservedStoreTime {
            written_at_unix_micros: 1_000,
            observed_at: start,
        };
        let reclaimable_at = start + Duration::from_secs(3_000);
        schedule.record_follow_up(due, reference, reclaimable_at);
        assert_eq!(schedule.take_due_pass(start + INTERVAL), None);
        assert_eq!(
            schedule.take_due_pass(reclaimable_at),
            Some(DueReclamationPass {
                logical_key: "manifest".to_owned(),
                confirmed_generation: 3,
                reference: Some(reference),
            })
        );
        let follow_up = schedule.take_due_pass(reclaimable_at);
        assert_eq!(follow_up, None);
        schedule.record_follow_up(
            pass("manifest", 3),
            reference,
            reclaimable_at + Duration::from_secs(3_000),
        );
        let write_at = reclaimable_at + Duration::from_secs(1);
        schedule.record_confirmed_write("manifest", 4, write_at);
        assert_eq!(schedule.next_deadline(), Some(reclaimable_at + INTERVAL));
        assert_eq!(
            schedule.take_due_pass(reclaimable_at + INTERVAL),
            Some(pass("manifest", 4)),
            "the new write replaces the follow-up and its reference"
        );
    }

    #[test]
    fn a_full_schedule_forgets_the_key_with_the_latest_deadline() {
        let start = Instant::now();
        let mut schedule = ReclamationSchedule::new(INTERVAL, 2);
        schedule.record_confirmed_write("lease/a", 3, start);
        let due = schedule
            .take_due_pass(start)
            .expect("the first pass is due");
        let reference = ObservedStoreTime {
            written_at_unix_micros: 1_000,
            observed_at: start,
        };
        schedule.record_follow_up(due, reference, start + Duration::from_secs(3_000));
        schedule.record_confirmed_write("lease/b", 3, start);
        schedule.record_confirmed_write("lease/c", 3, start);
        assert_eq!(schedule.keys.len(), 2);
        assert!(!schedule.keys.contains_key("lease/a"));
        assert_eq!(schedule.deadlines.len(), 2);
        assert_eq!(schedule.take_due_pass(start), Some(pass("lease/b", 3)));
        assert_eq!(schedule.take_due_pass(start), Some(pass("lease/c", 3)));
    }

    #[test]
    fn a_store_time_lower_bound_advances_with_local_time() {
        let start = Instant::now();
        let observed = ObservedStoreTime {
            written_at_unix_micros: 5_000_000,
            observed_at: start,
        };
        assert_eq!(observed.lower_bound_at(start), 5_000_000);
        assert_eq!(
            observed.lower_bound_at(start + Duration::from_secs(2)),
            7_000_000
        );
        assert_eq!(
            observed.lower_bound_at(start - Duration::from_secs(1)),
            5_000_000
        );
    }
}
