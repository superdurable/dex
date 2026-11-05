// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! In-flight attempt entries (plan section 3.1), shared so Claims and Heads see each holder.

use std::collections::{BTreeMap, BTreeSet};

use dexzd_keyspace::RunId;

use crate::effects::dispatch::attempt_handout::HandoutProgress;
use crate::effects::method_request::MethodAttemptKey;
use crate::flow_coordination::FlowAddress;

/// Who holds an attempt's entry.
pub(crate) enum EntryHolder {
    /// A task dispatches, queues, starts, or runs the attempt.
    Dispatching,
    /// The holder sent Queued for the committed row; the entry ends once it is released.
    QueuedSent,
    /// The attempt waits for its locks; the router restarts it once they are released.
    Parked {
        flow: FlowAddress,
        progress: HandoutProgress,
    },
    /// A task sends the row back after the backoff of a Claim that answered "retry later".
    RetryingClaim,
}

/// How a queued row's holder answers a Claim of it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum ClaimHolder {
    /// The Claim registered the entry, or took it over from a holder that sent Queued.
    Claimed { generation: u64 },
    /// The attempt waits for its locks.
    Parked,
    /// Another holder sends the row back later.
    Busy,
}

/// What happens after a hand-out task ends.
pub(crate) enum HandoutRestart {
    /// The entry ended, or another holder owns it now.
    Done,
    /// Locks were released while the task checked them; run it again now.
    Again(FlowAddress, HandoutProgress, u64),
}

struct AttemptEntry {
    generation: u64,
    holder: EntryHolder,
    /// Lock releases of the run already seen when the holder last checked locks.
    lock_release_count: u64,
}

/// Every attempt entry of this node, behind one mutex that no holder keeps across an await.
#[derive(Default)]
pub(crate) struct AttemptEntries {
    entries: BTreeMap<MethodAttemptKey, AttemptEntry>,
    parked_by_run: BTreeMap<RunId, BTreeSet<MethodAttemptKey>>,
    lock_release_counts: BTreeMap<RunId, u64>,
    next_generation: u64,
}

impl AttemptEntries {
    /// Registers a dispatching entry for `key`.
    ///
    /// Returns its generation, or `None` when an entry exists (a duplicate pending effect).
    pub(crate) fn register(&mut self, key: &MethodAttemptKey) -> Option<u64> {
        if self.entries.contains_key(key) {
            return None;
        }
        let generation = self.new_generation();
        let lock_release_count = self.lock_release_count(key.run_id);
        self.entries.insert(
            key.clone(),
            AttemptEntry {
                generation,
                holder: EntryHolder::Dispatching,
                lock_release_count,
            },
        );
        Some(generation)
    }

    /// Classifies a Claim of `key` and, when the Claim may start the row, makes it the holder.
    pub(crate) fn claim(&mut self, key: &MethodAttemptKey) -> ClaimHolder {
        let generation = self.new_generation();
        let lock_release_count = self.lock_release_count(key.run_id);
        match self.entries.get_mut(key) {
            None => {
                self.entries.insert(
                    key.clone(),
                    AttemptEntry {
                        generation,
                        holder: EntryHolder::Dispatching,
                        lock_release_count,
                    },
                );
                ClaimHolder::Claimed { generation }
            }
            // The sender will not send again, so answering "busy" would lose the row.
            Some(entry) if matches!(entry.holder, EntryHolder::QueuedSent) => {
                entry.generation = generation;
                entry.holder = EntryHolder::Dispatching;
                entry.lock_release_count = lock_release_count;
                ClaimHolder::Claimed { generation }
            }
            Some(entry) if matches!(entry.holder, EntryHolder::Parked { .. }) => {
                ClaimHolder::Parked
            }
            Some(_) => ClaimHolder::Busy,
        }
    }

    /// Changes the holder of `key` while `generation` still owns it.
    ///
    /// Returns whether it does.
    pub(crate) fn set_holder(
        &mut self,
        key: &MethodAttemptKey,
        generation: u64,
        holder: EntryHolder,
    ) -> bool {
        match self.entries.get_mut(key) {
            Some(entry) if entry.generation == generation => {
                entry.holder = holder;
                true
            }
            _ => false,
        }
    }

    /// Ends a hand-out task: removes, parks, or restarts its entry.
    pub(crate) fn finish_handout(
        &mut self,
        key: &MethodAttemptKey,
        generation: u64,
        locks_busy: Option<(FlowAddress, HandoutProgress)>,
    ) -> HandoutRestart {
        let current_count = self.lock_release_count(key.run_id);
        let Some(entry) = self
            .entries
            .get_mut(key)
            .filter(|entry| entry.generation == generation)
        else {
            return HandoutRestart::Done;
        };
        let Some((flow, progress)) = locks_busy else {
            self.entries.remove(key);
            return HandoutRestart::Done;
        };
        if entry.lock_release_count != current_count {
            entry.lock_release_count = current_count;
            entry.holder = EntryHolder::Dispatching;
            return HandoutRestart::Again(flow, progress, generation);
        }
        entry.holder = EntryHolder::Parked { flow, progress };
        self.parked_by_run
            .entry(key.run_id)
            .or_default()
            .insert(key.clone());
        HandoutRestart::Done
    }

    /// Records a lock release of `run_id` and wakes its parked attempts.
    ///
    /// Returns the attempts to run again, now dispatching.
    pub(crate) fn release_locks(
        &mut self,
        run_id: RunId,
    ) -> Vec<(MethodAttemptKey, FlowAddress, HandoutProgress, u64)> {
        *self.lock_release_counts.entry(run_id).or_insert(0) += 1;
        let keys = self.parked_by_run.remove(&run_id).unwrap_or_default();
        self.unpark(keys)
    }

    /// Wakes every parked attempt; in-memory RPC locks release without an effect.
    pub(crate) fn release_all_parked(
        &mut self,
    ) -> Vec<(MethodAttemptKey, FlowAddress, HandoutProgress, u64)> {
        let keys: BTreeSet<MethodAttemptKey> = std::mem::take(&mut self.parked_by_run)
            .into_values()
            .flatten()
            .collect();
        self.unpark(keys)
    }

    pub(crate) fn has_parked(&self) -> bool {
        !self.parked_by_run.is_empty()
    }

    /// Returns whether a holder that sends the row back later holds `key`.
    pub(crate) fn is_held(&self, key: &MethodAttemptKey) -> bool {
        self.entries
            .get(key)
            .is_some_and(|entry| !matches!(entry.holder, EntryHolder::QueuedSent))
    }

    /// Returns whether the holder of `key` sent its Queued, which may not have arrived yet.
    pub(crate) fn is_queued_sent(&self, key: &MethodAttemptKey) -> bool {
        self.entries
            .get(key)
            .is_some_and(|entry| matches!(entry.holder, EntryHolder::QueuedSent))
    }

    fn unpark(
        &mut self,
        keys: BTreeSet<MethodAttemptKey>,
    ) -> Vec<(MethodAttemptKey, FlowAddress, HandoutProgress, u64)> {
        let mut unparked = Vec::new();
        for key in keys {
            let generation = self.new_generation();
            let current_count = self.lock_release_count(key.run_id);
            let Some(entry) = self.entries.get_mut(&key) else {
                continue;
            };
            let holder = std::mem::replace(&mut entry.holder, EntryHolder::Dispatching);
            let EntryHolder::Parked { flow, progress } = holder else {
                entry.holder = holder;
                continue;
            };
            entry.generation = generation;
            entry.lock_release_count = current_count;
            unparked.push((key, flow, progress, generation));
        }
        unparked
    }

    fn lock_release_count(&self, run_id: RunId) -> u64 {
        self.lock_release_counts.get(&run_id).copied().unwrap_or(0)
    }

    fn new_generation(&mut self) -> u64 {
        self.next_generation += 1;
        self.next_generation
    }
}
