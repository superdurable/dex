// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! One process's queue refs under matching's rules: keep the smaller, one Claim per queue.

use std::collections::BTreeMap;
use std::sync::Mutex;

use dexzd_keyspace::PartitionId;
use dexzd_keyspace::dispatch_queue_keys::DispatchQueueName;
use dexzd_worker_dispatch::QueuedTaskRef;

/// A process's queue refs; a Claim removes its ref until the owner replies.
#[derive(Default)]
pub(crate) struct SimulatedTaskQueue {
    refs: Mutex<SimulatedRefs>,
    pub(crate) has_refs: tokio::sync::Notify,
    next_claim_id: Mutex<u64>,
}

#[derive(Default)]
struct SimulatedRefs {
    heads: BTreeMap<(PartitionId, DispatchQueueName), QueuedTaskRef>,
    /// The Claim in flight per queue, with the ref it took.
    claims: BTreeMap<(PartitionId, DispatchQueueName), (u64, QueuedTaskRef)>,
}

impl SimulatedTaskQueue {
    pub(crate) fn push(&self, task: QueuedTaskRef) {
        let mut refs = self.refs.lock().expect("queue lock");
        let slot = (task.partition, task.queue.clone());
        let is_smaller = refs
            .heads
            .get(&slot)
            .is_none_or(|head| task.position < head.position);
        if is_smaller {
            refs.heads.insert(slot, task);
        }
        drop(refs);
        self.has_refs.notify_one();
    }

    pub(crate) fn new_claim_id(&self) -> u64 {
        let mut next = self.next_claim_id.lock().expect("claim id lock");
        *next += 1;
        *next
    }

    /// Takes the first head without a Claim in flight.
    pub(crate) fn take_claimable(&self, claim_id: u64) -> Option<QueuedTaskRef> {
        let mut refs = self.refs.lock().expect("queue lock");
        let slot = refs
            .heads
            .keys()
            .find(|slot| !refs.claims.contains_key(*slot))
            .cloned()?;
        let task = refs.heads.remove(&slot)?;
        refs.claims.insert(slot, (claim_id, task.clone()));
        Some(task)
    }

    pub(crate) fn is_claim_tracked(
        &self,
        partition: PartitionId,
        queue: &DispatchQueueName,
        claim_id: u64,
    ) -> bool {
        self.refs
            .lock()
            .expect("queue lock")
            .claims
            .get(&(partition, queue.clone()))
            .is_some_and(|(tracked_id, _)| *tracked_id == claim_id)
    }

    /// Ends a tracked Claim and inserts the reply's next ref; replies to other Claims are dropped.
    ///
    /// Returns the ref the Claim took, or `None` for an untracked reply.
    pub(crate) fn finish_claim(
        &self,
        partition: PartitionId,
        queue: &DispatchQueueName,
        claim_id: u64,
        next: Option<QueuedTaskRef>,
    ) -> Option<QueuedTaskRef> {
        let mut refs = self.refs.lock().expect("queue lock");
        let slot = (partition, queue.clone());
        if refs
            .claims
            .get(&slot)
            .is_none_or(|(tracked_id, _)| *tracked_id != claim_id)
        {
            return None;
        }
        let (_, claimed) = refs.claims.remove(&slot)?;
        drop(refs);
        match next {
            Some(next) => self.push(next),
            None => self.has_refs.notify_one(),
        }
        Some(claimed)
    }
}
