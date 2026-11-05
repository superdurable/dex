// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Bounded cache of the task types each app Worker declined (plan D54).

use std::collections::{BTreeMap, HashMap, HashSet};
use std::time::{Duration, Instant};

use prometheus_client::metrics::counter::Counter;
use prometheus_client::metrics::gauge::Gauge;

const FULL_CACHE_WARNING_INTERVAL: Duration = Duration::from_secs(60);

/// Whether a task runs a Step method or an RPC; their names never share a cache entry.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub(super) enum TaskKind {
    Step,
    Rpc,
}

/// Names one task type of one tenant and Flow type.
#[derive(Clone, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub(super) struct TaskTypeKey {
    pub(super) tenant_id: u64,
    pub(super) flow_type: String,
    pub(super) kind: TaskKind,
    /// Step type or RPC name.
    pub(super) name: String,
}

type CacheEntry = (TaskTypeKey, String);

/// LRU of (task type, app Worker ID) pairs, bounded by `capacity` pairs.
///
/// An evicted pair keeps excluding its Worker until the Worker next completes a Step (plan
/// section 3.5). A Worker that only declines keeps its evicted pairs until it disconnects; they
/// are bounded by the application's distinct Step types and RPC names.
pub(super) struct UnsupportedTypeCache {
    capacity: usize,
    next_use: u64,
    uses: HashMap<CacheEntry, u64>,
    use_order: BTreeMap<u64, CacheEntry>,
    workers_by_type: HashMap<TaskTypeKey, HashSet<String>>,
    types_by_worker: HashMap<(u64, String), HashSet<TaskTypeKey>>,
    /// Evicted pairs by tenant and Worker, until the Worker completes a Step.
    awaiting_reprobe: HashMap<u64, HashMap<String, HashSet<TaskTypeKey>>>,
    entries_gauge: Gauge,
    evictions: Counter,
    last_full_warning: Option<Instant>,
}

impl UnsupportedTypeCache {
    pub(super) fn new(capacity: usize, entries_gauge: Gauge, evictions: Counter) -> Self {
        assert!(
            capacity > 0,
            "the unsupported type cache holds at least one entry"
        );
        Self {
            capacity,
            next_use: 0,
            uses: HashMap::new(),
            use_order: BTreeMap::new(),
            workers_by_type: HashMap::new(),
            types_by_worker: HashMap::new(),
            awaiting_reprobe: HashMap::new(),
            entries_gauge,
            evictions,
            last_full_warning: None,
        }
    }

    /// Returns whether `app_worker_id` declined `key`.
    pub(super) fn contains(&self, key: &TaskTypeKey, app_worker_id: &str) -> bool {
        self.workers_by_type
            .get(key)
            .is_some_and(|workers| workers.contains(app_worker_id))
    }

    /// Returns whether `app_worker_id` may not run `key`: it declined `key`, or that decline
    /// was evicted and the Worker has completed no Step since.
    pub(super) fn excludes(&self, key: &TaskTypeKey, app_worker_id: &str) -> bool {
        self.contains(key, app_worker_id)
            || self
                .awaiting_reprobe
                .get(&key.tenant_id)
                .and_then(|workers| workers.get(app_worker_id))
                .is_some_and(|keys| keys.contains(key))
    }

    /// Records that `app_worker_id` declined `key`, evicting the least recently recorded pair
    /// when full. An evicted pair is not probed again until its Worker next completes a Step.
    pub(super) fn insert(&mut self, key: TaskTypeKey, app_worker_id: &str) {
        let entry = (key, app_worker_id.to_owned());
        self.forget_reprobe(&entry);
        if let Some(previous_use) = self.uses.remove(&entry) {
            self.use_order.remove(&previous_use);
        } else {
            self.workers_by_type
                .entry(entry.0.clone())
                .or_default()
                .insert(entry.1.clone());
            self.types_by_worker
                .entry((entry.0.tenant_id, entry.1.clone()))
                .or_default()
                .insert(entry.0.clone());
        }
        self.next_use += 1;
        self.uses.insert(entry.clone(), self.next_use);
        self.use_order.insert(self.next_use, entry);
        while self.uses.len() > self.capacity {
            let (_, evicted) = self
                .use_order
                .pop_first()
                .expect("an over-full cache has a least recent entry");
            self.uses.remove(&evicted);
            self.forget(&evicted);
            self.await_reprobe(evicted);
            self.evictions.inc();
            self.warn_when_full();
        }
        self.entries_gauge
            .set(i64::try_from(self.uses.len()).unwrap_or(i64::MAX));
    }

    /// Forgets every type the Worker declined; its streams are all gone.
    pub(super) fn remove_worker(&mut self, tenant_id: u64, app_worker_id: &str) {
        self.forget_awaiting_reprobes(tenant_id, app_worker_id);
        let Some(keys) = self
            .types_by_worker
            .remove(&(tenant_id, app_worker_id.to_owned()))
        else {
            return;
        };
        for key in keys {
            let entry = (key, app_worker_id.to_owned());
            if let Some(previous_use) = self.uses.remove(&entry) {
                self.use_order.remove(&previous_use);
            }
            if let Some(workers) = self.workers_by_type.get_mut(&entry.0) {
                workers.remove(app_worker_id);
                if workers.is_empty() {
                    self.workers_by_type.remove(&entry.0);
                }
            }
        }
        self.entries_gauge
            .set(i64::try_from(self.uses.len()).unwrap_or(i64::MAX));
    }

    /// Lets the Worker's evicted pairs be probed again: it completed a Step.
    ///
    /// Returns whether the Worker had evicted pairs waiting.
    pub(super) fn release_reprobes(&mut self, tenant_id: u64, app_worker_id: &str) -> bool {
        self.forget_awaiting_reprobes(tenant_id, app_worker_id)
    }

    /// Removes the Worker's evicted pairs; returns whether it had any.
    fn forget_awaiting_reprobes(&mut self, tenant_id: u64, app_worker_id: &str) -> bool {
        let Some(workers) = self.awaiting_reprobe.get_mut(&tenant_id) else {
            return false;
        };
        let had_pairs = workers.remove(app_worker_id).is_some();
        if workers.is_empty() {
            self.awaiting_reprobe.remove(&tenant_id);
        }
        had_pairs
    }

    /// Keeps `evicted` excluding its Worker until that Worker completes a Step.
    fn await_reprobe(&mut self, (key, app_worker_id): CacheEntry) {
        self.awaiting_reprobe
            .entry(key.tenant_id)
            .or_default()
            .entry(app_worker_id)
            .or_default()
            .insert(key);
    }

    fn forget_reprobe(&mut self, (key, app_worker_id): &CacheEntry) {
        let Some(workers) = self.awaiting_reprobe.get_mut(&key.tenant_id) else {
            return;
        };
        if let Some(keys) = workers.get_mut(app_worker_id) {
            keys.remove(key);
            if keys.is_empty() {
                workers.remove(app_worker_id);
            }
        }
        if workers.is_empty() {
            self.awaiting_reprobe.remove(&key.tenant_id);
        }
    }

    fn forget(&mut self, (key, app_worker_id): &CacheEntry) {
        if let Some(workers) = self.workers_by_type.get_mut(key) {
            workers.remove(app_worker_id);
            if workers.is_empty() {
                self.workers_by_type.remove(key);
            }
        }
        let worker_key = (key.tenant_id, app_worker_id.clone());
        if let Some(keys) = self.types_by_worker.get_mut(&worker_key) {
            keys.remove(key);
            if keys.is_empty() {
                self.types_by_worker.remove(&worker_key);
            }
        }
    }

    fn warn_when_full(&mut self) {
        let now = Instant::now();
        if self
            .last_full_warning
            .is_some_and(|last| now.duration_since(last) < FULL_CACHE_WARNING_INTERVAL)
        {
            return;
        }
        self.last_full_warning = Some(now);
        tracing::warn!(
            capacity = self.capacity,
            "the unsupported type cache is full; raise matching.unsupportedTypeCacheEntries"
        );
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn key(name: &str) -> TaskTypeKey {
        TaskTypeKey {
            tenant_id: 7,
            flow_type: "Flow".to_owned(),
            kind: TaskKind::Step,
            name: name.to_owned(),
        }
    }

    #[test]
    fn full_cache_evicts_the_least_recent_pair_and_workers_forget_their_types() {
        let evictions = Counter::default();
        let mut cache = UnsupportedTypeCache::new(2, Gauge::default(), evictions.clone());
        cache.insert(key("A"), "old");
        cache.insert(key("B"), "old");
        cache.insert(key("A"), "old");
        cache.insert(key("C"), "new");
        assert!(cache.contains(&key("A"), "old"));
        assert!(!cache.contains(&key("B"), "old"), "B was least recent");
        assert!(cache.contains(&key("C"), "new"));
        assert_eq!(evictions.get(), 1);
        // The evicted pair excludes its Worker until that Worker completes a Step.
        assert!(cache.excludes(&key("B"), "old"));
        assert!(!cache.release_reprobes(7, "new"));
        assert!(
            cache.excludes(&key("B"), "old"),
            "another Worker's completion"
        );
        assert!(cache.release_reprobes(7, "old"));
        assert!(!cache.excludes(&key("B"), "old"));
        cache.insert(key("B"), "old");
        assert!(cache.excludes(&key("B"), "old"), "a new decline is cached");
        let rpc = TaskTypeKey {
            kind: TaskKind::Rpc,
            ..key("C")
        };
        assert!(
            !cache.contains(&rpc, "new"),
            "Step and RPC names are distinct"
        );
        cache.remove_worker(7, "old");
        assert!(!cache.contains(&key("A"), "old"));
        assert!(cache.contains(&key("C"), "new"));
    }
}
