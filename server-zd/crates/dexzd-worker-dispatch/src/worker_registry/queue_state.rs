// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Queue lifetime, waiting dispatches, and bounded same-Flow affinity.

use super::*;
use prometheus_client::metrics::gauge::Gauge;

/// A dispatch waiting for an idle stream, with the streams it must not be offered.
pub(super) struct QueueWaiter {
    wakeup: Arc<Notify>,
    excluded_worker_ids: HashSet<String>,
    excluded_streams: HashMap<Uuid, Instant>,
}

impl QueueWaiter {
    fn may_use(&self, worker: &WorkerStream, now: Instant) -> bool {
        !self.excluded_worker_ids.contains(&worker.app_worker_id)
            && self
                .excluded_streams
                .get(&worker.id)
                .is_none_or(|until| *until <= now)
    }
}

pub(super) struct QueuedTaskMetric {
    gauge: Gauge,
    is_waiting: bool,
}

impl QueuedTaskMetric {
    pub(super) fn new(gauge: Gauge) -> Self {
        gauge.inc();
        Self {
            gauge,
            is_waiting: true,
        }
    }

    pub(super) fn set_waiting(&mut self, should_wait: bool) {
        if should_wait && !self.is_waiting {
            self.gauge.inc();
        } else if !should_wait && self.is_waiting {
            self.gauge.dec();
        }
        self.is_waiting = should_wait;
    }
}

impl Drop for QueuedTaskMetric {
    fn drop(&mut self) {
        if self.is_waiting {
            self.gauge.dec();
        }
    }
}

impl QueuePick {
    pub(super) fn without_worker(has_streams: bool) -> Self {
        Self {
            worker: None,
            assignment_frames: None,
            has_streams,
        }
    }
}

impl Drop for QueueLease {
    fn drop(&mut self) {
        let waiter_sequence = self.is_waiting.then_some(self.waiter_sequence);
        self.dispatch.release_queue(&self.key, waiter_sequence);
    }
}

impl QueueState {
    pub(super) fn new() -> Self {
        Self {
            stream_ids: HashSet::new(),
            idle: VecDeque::new(),
            affinity: HashMap::new(),
            affinity_order: VecDeque::new(),
            waiters: BTreeMap::new(),
            users: 0,
        }
    }

    /// Registers `lease` in arrival order until a stream it may use becomes idle.
    pub(super) fn start_waiting(
        &mut self,
        lease: &mut QueueLease,
        excluded_worker_ids: &HashSet<String>,
        excluded_streams: &HashMap<Uuid, Instant>,
    ) {
        self.waiters.insert(
            lease.waiter_sequence,
            QueueWaiter {
                wakeup: lease.wakeup.clone(),
                excluded_worker_ids: excluded_worker_ids.clone(),
                excluded_streams: excluded_streams.clone(),
            },
        );
        lease.is_waiting = true;
    }

    /// Removes a registered waiter.
    ///
    /// Returns true when a wakeup removed it first. That waiter then owes the wakeup to another
    /// waiter unless it takes an idle stream.
    pub(super) fn remove_waiter(&mut self, waiter_sequence: u64) -> bool {
        self.waiters.remove(&waiter_sequence).is_none()
    }

    /// Wakes the longest-waiting dispatch that may use the newly idle `worker`.
    pub(super) fn wake_waiter_for(&mut self, worker: &WorkerStream, now: Instant) {
        let sequence = self
            .waiters
            .iter()
            .find(|(_, waiter)| waiter.may_use(worker, now))
            .map(|(sequence, _)| *sequence);
        self.wake_waiter(sequence);
    }

    /// Wakes the longest-waiting dispatch that may use any idle stream.
    pub(super) fn wake_waiter_for_any_idle(
        &mut self,
        streams: &HashMap<Uuid, Arc<WorkerStream>>,
        now: Instant,
    ) {
        let idle = &self.idle;
        let sequence = self
            .waiters
            .iter()
            .find(|(_, waiter)| {
                idle.iter().any(|id| {
                    waiter.may_use(streams.get(id).expect("idle stream is registered"), now)
                })
            })
            .map(|(sequence, _)| *sequence);
        self.wake_waiter(sequence);
    }

    pub(super) fn wake_all_waiters(&mut self) {
        for waiter in std::mem::take(&mut self.waiters).into_values() {
            waiter.wakeup.notify_one();
        }
    }

    fn wake_waiter(&mut self, sequence: Option<u64>) {
        if let Some(waiter) = sequence.and_then(|sequence| self.waiters.remove(&sequence)) {
            waiter.wakeup.notify_one();
        }
    }

    pub(super) fn remember(&mut self, flow_id: &str, worker_id: &str, duration: Duration) {
        if flow_id.is_empty() {
            return;
        }
        let now = Instant::now();
        let expiry = now + duration;
        self.affinity
            .insert(flow_id.to_owned(), (worker_id.to_owned(), expiry));
        self.affinity_order.push_back((flow_id.to_owned(), expiry));
        while let Some((oldest_id, oldest_expiry)) = self.affinity_order.front() {
            let current = self
                .affinity
                .get(oldest_id)
                .map(|(_, current_expiry)| *current_expiry);
            if current == Some(*oldest_expiry) && *oldest_expiry > now {
                break;
            }
            if current == Some(*oldest_expiry) {
                self.affinity.remove(oldest_id);
            }
            self.affinity_order.pop_front();
        }
        while self.affinity.len() > 10_000 {
            let (oldest_id, oldest_expiry) = self
                .affinity_order
                .pop_front()
                .expect("affinity order has current entries");
            if self
                .affinity
                .get(&oldest_id)
                .is_some_and(|(_, current_expiry)| *current_expiry == oldest_expiry)
            {
                self.affinity.remove(&oldest_id);
            }
        }
        if self.affinity_order.len() > 20_000 {
            let affinity = &self.affinity;
            self.affinity_order.retain(|(flow_id, expiry)| {
                affinity
                    .get(flow_id)
                    .is_some_and(|(_, current_expiry)| current_expiry == expiry)
            });
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use dexzd_clock::SystemWallClock;
    use dexzd_protocol::dex::InvokeWaitForMethodRequest;
    use futures::FutureExt;

    #[tokio::test]
    async fn empty_queue_leases_and_absent_sticky_queues_are_removed() {
        let dispatch = WorkerDispatch::new(MatchingConfig::default(), Arc::new(SystemWallClock));
        let lease = dispatch.acquire_queue(0, "Flow");
        assert_eq!(
            dispatch
                .inner
                .registry
                .lock()
                .expect("registry lock")
                .queues
                .len(),
            1
        );
        drop(lease);
        assert!(
            dispatch
                .inner
                .registry
                .lock()
                .expect("registry lock")
                .queues
                .is_empty()
        );
        for index in 0..64 {
            let mut handle = dispatch
                .dispatch(DispatchRequest {
                    tenant_id: 0,
                    flow_id: format!("flow-{index}"),
                    flow_type: "Flow".to_owned(),
                    sticky_app_worker_id: Some(format!("absent-{index}")),
                    payload: DispatchTaskPayload::WaitFor(InvokeWaitForMethodRequest::default()),
                    deadline: SystemTime::now() + Duration::from_secs(1),
                    sticky_wait: Some(Duration::ZERO),
                    dispatch_wait: Some(Duration::from_millis(1)),
                })
                .expect("dispatch starts");
            assert!(matches!(
                handle.next().await,
                Some(DispatchEvent::Completed(DispatchOutcome::DispatchError {
                    reason: DispatchTaskErrorReason::NoWorkerAvailable,
                    ..
                }))
            ));
        }
        assert!(
            dispatch
                .inner
                .registry
                .lock()
                .expect("registry lock")
                .queues
                .is_empty()
        );
    }

    #[test]
    fn last_worker_disconnect_removes_its_queue() {
        let dispatch = WorkerDispatch::new(MatchingConfig::default(), Arc::new(SystemWallClock));
        let (output, _) = mpsc::unbounded_channel();
        let (close, _) = watch::channel(false);
        let worker = Arc::new(WorkerStream {
            id: Uuid::now_v7(),
            tenant_id: 7,
            queue: "Flow".to_owned(),
            app_worker_id: "worker".to_owned(),
            output,
            close,
            state: Mutex::new(WorkerState::default()),
        });
        {
            let mut registry = dispatch.inner.registry.lock().expect("registry lock");
            let queue = registry
                .queues
                .entry((7, "Flow".to_owned()))
                .or_insert_with(QueueState::new);
            queue.stream_ids.insert(worker.id);
            queue.idle.push_back(worker.id);
            queue.users += 1;
            registry.worker_counts.insert((7, "worker".to_owned()), 1);
            registry.streams.insert(worker.id, worker.clone());
        }
        dispatch.disconnect(worker.id);
        let registry = dispatch.inner.registry.lock().expect("registry lock");
        assert!(registry.queues.is_empty());
        assert!(registry.worker_counts.is_empty());
    }

    #[test]
    fn affinity_order_evicts_old_flows_and_compacts_repeated_updates() {
        let mut queue = QueueState::new();
        for index in 0..10_001 {
            queue.remember(&format!("flow-{index}"), "worker", Duration::from_secs(30));
        }
        assert_eq!(queue.affinity.len(), 10_000);
        assert!(!queue.affinity.contains_key("flow-0"));
        assert!(queue.affinity.contains_key("flow-10000"));
        for _ in 0..25_000 {
            queue.remember("hot-flow", "worker", Duration::from_secs(30));
        }
        assert!(queue.affinity_order.len() <= 20_000);
    }

    // These tests drive the wakeup races directly; the gRPC API cannot order them deterministically.
    fn register_busy_stream(dispatch: &WorkerDispatch, app_worker_id: &str) -> Arc<WorkerStream> {
        let (output, _) = mpsc::unbounded_channel();
        let (close, _) = watch::channel(false);
        let worker = Arc::new(WorkerStream {
            id: Uuid::now_v7(),
            tenant_id: 7,
            queue: "Flow".to_owned(),
            app_worker_id: app_worker_id.to_owned(),
            output,
            close,
            state: Mutex::new(WorkerState {
                is_busy: true,
                ..WorkerState::default()
            }),
        });
        let mut registry = dispatch.inner.registry.lock().expect("registry lock");
        let queue = registry
            .queues
            .entry((7, "Flow".to_owned()))
            .or_insert_with(QueueState::new);
        queue.stream_ids.insert(worker.id);
        queue.users += 1;
        registry.streams.insert(worker.id, worker.clone());
        worker
    }

    /// Returns the picked stream's Worker ID, or `None` after registering `lease` as a waiter.
    fn pick(
        dispatch: &WorkerDispatch,
        lease: &mut QueueLease,
        flow_id: &str,
        excluded_worker_ids: &[&str],
    ) -> Option<String> {
        let excluded_worker_ids = excluded_worker_ids
            .iter()
            .map(|worker_id| (*worker_id).to_owned())
            .collect();
        dispatch
            .pick_worker(
                lease,
                flow_id,
                "task",
                &excluded_worker_ids,
                &HashMap::new(),
            )
            .worker
            .map(|worker| worker.app_worker_id.clone())
    }

    /// Returns whether `lease` holds a wakeup, consuming it.
    fn take_wakeup(lease: &QueueLease) -> bool {
        lease.wakeup.notified().now_or_never().is_some()
    }

    #[test]
    fn a_stream_idle_before_the_wait_wakes_only_the_oldest_waiter() {
        let dispatch = WorkerDispatch::new(MatchingConfig::default(), Arc::new(SystemWallClock));
        let worker = register_busy_stream(&dispatch, "worker");
        let mut older = dispatch.acquire_queue(7, "Flow");
        let mut newer = dispatch.acquire_queue(7, "Flow");
        assert_eq!(pick(&dispatch, &mut older, "older", &[]), None);
        assert_eq!(pick(&dispatch, &mut newer, "newer", &[]), None);
        // The stream goes idle after the waiter registers but before it awaits its wakeup.
        dispatch.make_idle(&worker);
        assert!(take_wakeup(&older));
        assert!(!take_wakeup(&newer));
        assert_eq!(
            pick(&dispatch, &mut older, "older", &[]).as_deref(),
            Some("worker")
        );
    }

    #[test]
    fn a_waiter_that_ends_after_its_wakeup_passes_it_on() {
        let dispatch = WorkerDispatch::new(MatchingConfig::default(), Arc::new(SystemWallClock));
        let worker = register_busy_stream(&dispatch, "worker");
        let mut cancelled = dispatch.acquire_queue(7, "Flow");
        let mut waiting = dispatch.acquire_queue(7, "Flow");
        assert_eq!(pick(&dispatch, &mut cancelled, "cancelled", &[]), None);
        assert_eq!(pick(&dispatch, &mut waiting, "waiting", &[]), None);
        dispatch.make_idle(&worker);
        assert!(!take_wakeup(&waiting));
        // Cancellation or the end of the search drops the lease before it picks again.
        drop(cancelled);
        assert!(take_wakeup(&waiting));
        assert_eq!(
            pick(&dispatch, &mut waiting, "waiting", &[]).as_deref(),
            Some("worker")
        );
    }

    #[test]
    fn a_woken_waiter_that_excludes_every_idle_stream_passes_its_wakeup_on() {
        let dispatch = WorkerDispatch::new(MatchingConfig::default(), Arc::new(SystemWallClock));
        let first = register_busy_stream(&dispatch, "first");
        let second = register_busy_stream(&dispatch, "second");
        let mut affine = dispatch.acquire_queue(7, "Flow");
        let mut excluding = dispatch.acquire_queue(7, "Flow");
        let mut eligible = dispatch.acquire_queue(7, "Flow");
        dispatch
            .inner
            .registry
            .lock()
            .expect("registry lock")
            .queues
            .get_mut(&(7, "Flow".to_owned()))
            .expect("queue exists")
            .remember("affine", "second", Duration::from_secs(30));
        assert_eq!(pick(&dispatch, &mut affine, "affine", &[]), None);
        assert_eq!(
            pick(&dispatch, &mut excluding, "excluding", &["first"]),
            None
        );
        assert_eq!(pick(&dispatch, &mut eligible, "eligible", &[]), None);
        dispatch.make_idle(&first);
        dispatch.make_idle(&second);
        assert!(take_wakeup(&affine));
        assert!(take_wakeup(&excluding));
        assert!(!take_wakeup(&eligible));
        // Affinity takes the stream the second wakeup was for, leaving one this waiter excludes.
        assert_eq!(
            pick(&dispatch, &mut affine, "affine", &[]).as_deref(),
            Some("second")
        );
        assert_eq!(
            pick(&dispatch, &mut excluding, "excluding", &["first"]),
            None
        );
        assert!(take_wakeup(&eligible));
        assert_eq!(
            pick(&dispatch, &mut eligible, "eligible", &[]).as_deref(),
            Some("first")
        );
    }

    #[test]
    fn the_last_stream_disconnect_wakes_every_waiter() {
        let dispatch = WorkerDispatch::new(MatchingConfig::default(), Arc::new(SystemWallClock));
        let first = register_busy_stream(&dispatch, "first");
        let second = register_busy_stream(&dispatch, "second");
        let mut older = dispatch.acquire_queue(7, "Flow");
        let mut newer = dispatch.acquire_queue(7, "Flow");
        assert_eq!(pick(&dispatch, &mut older, "older", &[]), None);
        assert_eq!(pick(&dispatch, &mut newer, "newer", &[]), None);
        dispatch.disconnect(first.id);
        assert!(!take_wakeup(&older));
        assert!(!take_wakeup(&newer));
        // Sticky searches must learn that their queue has no streams left.
        dispatch.disconnect(second.id);
        assert!(take_wakeup(&older));
        assert!(take_wakeup(&newer));
        let picked = dispatch.pick_worker(
            &mut older,
            "older",
            "task",
            &HashSet::new(),
            &HashMap::new(),
        );
        assert!(picked.worker.is_none() && !picked.has_streams);
    }
}
