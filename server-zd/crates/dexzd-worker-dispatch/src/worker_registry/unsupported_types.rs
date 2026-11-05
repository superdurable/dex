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
/// An evicted pair keeps excluding the streams that were idle at its eviction, so it is probed
/// again only after a stream of its Worker next becomes idle (plan section 3.5).
pub(super) struct UnsupportedTypeCache {
    capacity: usize,
    next_use: u64,
    uses: HashMap<CacheEntry, u64>,
    use_order: BTreeMap<u64, CacheEntry>,
    workers_by_type: HashMap<TaskTypeKey, HashSet<String>>,
    types_by_worker: HashMap<(u64, String), HashSet<TaskTypeKey>>,
    /// Evicted pairs and the last idle position assigned at their eviction.
    reprobe_marks: HashMap<TaskTypeKey, HashMap<String, ReprobeMark>>,
    /// Evicted pairs by eviction order; at most `capacity` are kept.
    reprobe_order: BTreeMap<u64, CacheEntry>,
    next_eviction: u64,
    entries_gauge: Gauge,
    evictions: Counter,
    last_full_warning: Option<Instant>,
}

/// When an evicted pair was evicted, and the newest idle position at that moment.
#[derive(Clone, Copy)]
struct ReprobeMark {
    eviction: u64,
    idle_position: u64,
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
            reprobe_marks: HashMap::new(),
            reprobe_order: BTreeMap::new(),
            next_eviction: 0,
            entries_gauge,
            evictions,
            last_full_warning: None,
        }
    }

    /// Returns the app Workers that declined `key`.
    pub(super) fn declining_workers(&self, key: &TaskTypeKey) -> impl Iterator<Item = &String> {
        self.workers_by_type.get(key).into_iter().flatten()
    }

    /// Returns whether `app_worker_id` declined `key`.
    pub(super) fn contains(&self, key: &TaskTypeKey, app_worker_id: &str) -> bool {
        self.workers_by_type
            .get(key)
            .is_some_and(|workers| workers.contains(app_worker_id))
    }

    /// Returns whether a stream of `app_worker_id` may not run `key`: the Worker declined it,
    /// or the decline was evicted while this stream was idle at `idle_position` and the
    /// stream has not become idle again since. A busy stream has no idle position.
    pub(super) fn excludes(
        &self,
        key: &TaskTypeKey,
        app_worker_id: &str,
        idle_position: Option<u64>,
    ) -> bool {
        self.contains(key, app_worker_id)
            || idle_position.is_some_and(|position| {
                self.reprobe_marks
                    .get(key)
                    .and_then(|workers| workers.get(app_worker_id))
                    .is_some_and(|mark| position <= mark.idle_position)
            })
    }

    /// Records that `app_worker_id` declined `key`, evicting the least recently recorded pair
    /// when full. `newest_idle_position` is the last idle position the registry assigned; an
    /// evicted pair is not probed again on streams that were idle at that position or before.
    pub(super) fn insert(
        &mut self,
        key: TaskTypeKey,
        app_worker_id: &str,
        newest_idle_position: u64,
    ) {
        let entry = (key, app_worker_id.to_owned());
        self.forget_reprobe_mark(&entry);
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
            self.mark_for_reprobe(evicted, newest_idle_position);
            self.evictions.inc();
            self.warn_when_full();
        }
        self.entries_gauge
            .set(i64::try_from(self.uses.len()).unwrap_or(i64::MAX));
    }

    /// Forgets every type the Worker declined; its streams are all gone.
    pub(super) fn remove_worker(&mut self, tenant_id: u64, app_worker_id: &str) {
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

    /// Keeps `evicted` excluded on streams idle at `newest_idle_position` or before; the
    /// oldest mark goes once `capacity` marks exist.
    fn mark_for_reprobe(&mut self, evicted: CacheEntry, newest_idle_position: u64) {
        self.next_eviction += 1;
        let mark = ReprobeMark {
            eviction: self.next_eviction,
            idle_position: newest_idle_position,
        };
        self.reprobe_marks
            .entry(evicted.0.clone())
            .or_default()
            .insert(evicted.1.clone(), mark);
        self.reprobe_order.insert(mark.eviction, evicted);
        while self.reprobe_order.len() > self.capacity {
            let (_, oldest) = self
                .reprobe_order
                .pop_first()
                .expect("an over-full mark order has an oldest mark");
            self.remove_reprobe_mark(&oldest);
        }
    }

    fn forget_reprobe_mark(&mut self, entry: &CacheEntry) {
        if let Some(mark) = self.remove_reprobe_mark(entry) {
            self.reprobe_order.remove(&mark.eviction);
        }
    }

    fn remove_reprobe_mark(&mut self, (key, app_worker_id): &CacheEntry) -> Option<ReprobeMark> {
        let workers = self.reprobe_marks.get_mut(key)?;
        let mark = workers.remove(app_worker_id);
        if workers.is_empty() {
            self.reprobe_marks.remove(key);
        }
        mark
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
        cache.insert(key("A"), "old", 1);
        cache.insert(key("B"), "old", 1);
        cache.insert(key("A"), "old", 1);
        cache.insert(key("C"), "new", 3);
        assert!(cache.contains(&key("A"), "old"));
        assert!(!cache.contains(&key("B"), "old"), "B was least recent");
        assert!(cache.contains(&key("C"), "new"));
        assert_eq!(evictions.get(), 1);
        // The evicted pair stays excluded on streams idle since before its eviction.
        assert!(cache.excludes(&key("B"), "old", Some(3)));
        assert!(!cache.excludes(&key("B"), "old", Some(4)));
        assert!(!cache.excludes(&key("B"), "old", None));
        cache.insert(key("B"), "old", 4);
        assert!(
            cache.excludes(&key("B"), "old", Some(9)),
            "a new decline is cached"
        );
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
