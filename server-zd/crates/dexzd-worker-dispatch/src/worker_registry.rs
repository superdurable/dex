// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! In-memory, tenant-scoped pull Worker streams and task matching.

use std::collections::{BTreeMap, HashMap, HashSet};
use std::pin::Pin;
use std::sync::{Arc, Mutex};
use std::task::{Context, Poll};
use std::time::{Duration, Instant, SystemTime};

use dexzd_clock::WallClock;
use dexzd_cluster_metadata::LeaseValidityDeadline;
use dexzd_metrics::{NodeMetrics, WorkerStreamEvent};
use dexzd_protocol::dex::{
    DispatchTaskErrorReason, PollWorkerTaskCancelTask, PollWorkerTaskFrame, PollWorkerTaskPing,
    PollWorkerTaskReconnect, PollWorkerTaskRegistered, PollWorkerTaskRejectReason,
    PollWorkerTaskRequest, PollWorkerTaskResponse, poll_worker_task_frame,
    poll_worker_task_request, poll_worker_task_response, worker_task_service_server,
};
use futures::Stream;
use tokio::sync::{Notify, mpsc, watch};
use tokio::task::JoinHandle;
use tokio_stream::wrappers::UnboundedReceiverStream;
use tonic::{Request, Response, Status, Streaming};
use uuid::Uuid;

use crate::MatchingConfig;
mod dispatch_handle;
mod dispatch_request;
mod queue_state;
mod step_reservation;
mod stream_registry;
mod task_frames;
mod unsupported_types;
mod worker_protocol;
mod worker_streams;
pub use dispatch_handle::DispatchHandle;
pub use dispatch_request::{DispatchRequest, DispatchTaskPayload};
use queue_state::{QueueWaiter, QueuedTaskMetric, SearchState};
pub use step_reservation::{
    ReservedStepStream, StepStreamRequest, StepStreamReservation, StepTaskEvent, StepTaskHandle,
    StepTaskSendFailure,
};
use stream_registry::Registry;
use task_frames::{
    build_task, disconnected_outcome, is_final, system_time_micros, validate_dispatch,
    validate_registration,
};
use unsupported_types::{TaskKind, TaskTypeKey, UnsupportedTypeCache};
pub use worker_protocol::WorkerTaskService;
use worker_streams::{StepBackoff, StreamSlot, TaskSendError};

/// One ordered frame from the Worker, an assignment change, or the terminal dispatch outcome.
#[derive(Debug)]
pub enum DispatchEvent {
    /// The task was sent to this Worker, which may still reject it before any output.
    Assigned {
        /// Assigned Worker process ID.
        app_worker_id: String,
    },
    /// The assigned Worker rejected the task or disconnected before any output; matching
    /// continues with another Worker.
    Unassigned,
    /// A non-final heartbeat or stream write from the assigned Worker.
    Output(PollWorkerTaskFrame),
    /// The unique final outcome of the dispatch.
    Completed(DispatchOutcome),
}

/// The terminal result of one dispatch.
#[derive(Debug)]
pub enum DispatchOutcome {
    /// A final WaitFor, Execute, or RPC result and the Worker that served it.
    WorkerResult {
        /// Final Worker frame.
        frame: PollWorkerTaskFrame,
        /// Serving Worker process ID.
        app_worker_id: String,
    },
    /// A Worker error response and the Worker that returned it.
    WorkerError {
        /// Final Worker error frame.
        frame: PollWorkerTaskFrame,
        /// Serving Worker process ID.
        app_worker_id: String,
    },
    /// A matching failure with the same reason values as DispatchTaskErrorReason.
    DispatchError {
        /// Matching failure reason.
        reason: DispatchTaskErrorReason,
        /// Human-readable diagnostic.
        detail: String,
    },
    /// The caller cancelled this dispatch.
    Cancelled,
    /// The method deadline elapsed before a final Worker frame.
    DeadlineExceeded,
    /// The matching owner shut down while waiting for a Worker, or its node lease is no longer
    /// valid.
    ServerClosing,
}

/// Shared in-memory matching owner. Clone it to give a tenant-scoped gRPC service access.
#[derive(Clone)]
pub struct WorkerDispatch {
    inner: Arc<DispatchInner>,
}

struct DispatchInner {
    metrics: NodeMetrics,
    config: MatchingConfig,
    clock: Arc<dyn WallClock>,
    /// When set, no Task goes out unless it is valid, so a resumed process hands out no work.
    lease_validity_deadline: Option<LeaseValidityDeadline>,
    registry: Mutex<Registry>,
    shutdown: watch::Sender<bool>,
}

struct QueueState {
    stream_ids: HashSet<Uuid>,
    /// Idle subscribed streams in the order they became idle.
    idle: BTreeMap<u64, Uuid>,
    affinity: HashMap<String, (String, Instant)>,
    affinity_order: std::collections::VecDeque<(String, Instant)>,
    // Each idle stream wakes one waiter; waking every waiter made dispatch cost grow with them.
    // RPC waiters are kept apart so the common Step wakeup never scans them (plan D56).
    rpc_waiters: BTreeMap<u64, QueueWaiter>,
    step_waiters: BTreeMap<u64, QueueWaiter>,
    // The stream each woken waiter must hand on if it does not take that stream.
    woken_waiter_streams: HashMap<u64, Uuid>,
    users: usize,
}

struct QueueLease {
    dispatch: WorkerDispatch,
    key: (u64, String),
    waiter_sequence: u64,
    wakeup: Arc<Notify>,
    is_waiting: bool,
}

struct WorkerStream {
    id: Uuid,
    tenant_id: u64,
    /// Queues this stream serves: its registered queue, or its sticky queue and Flow types.
    queues: Vec<String>,
    app_worker_id: String,
    slot: StreamSlot,
    output: mpsc::UnboundedSender<Result<PollWorkerTaskResponse, Status>>,
    close: watch::Sender<bool>,
    state: Mutex<WorkerState>,
}

#[derive(Default)]
struct WorkerState {
    is_busy: bool,
    is_closing: bool,
    /// The Worker cleanly half-closed this reserved-slot stream; no task frame goes out.
    is_send_stopped: bool,
    current: Option<Assignment>,
    /// Steps skip a stream without reserved-slot dispatch that declined for capacity.
    step_backoff: Option<StepBackoff>,
}

struct Assignment {
    task_id: String,
    task_type: TaskTypeKey,
    frames: mpsc::Sender<AssignmentFrame>,
    /// The Worker sent output for the task, so it may be running it.
    has_output: bool,
}

enum AssignmentFrame {
    Output(Box<PollWorkerTaskFrame>),
    Reject(PollWorkerTaskRejectReason),
    /// The Worker cleanly half-closed the stream after every earlier frame.
    HalfClosed,
}

struct QueuePick {
    worker: Option<Arc<WorkerStream>>,
    has_streams: bool,
    /// When the earliest skipped stream's Step backoff ends.
    next_backoff_end: Option<Instant>,
}

/// What a dispatch searches for: its task type and whether it is a Step.
struct TaskSearch<'a> {
    task_type: &'a TaskTypeKey,
    flow_id: &'a str,
}

impl WorkerDispatch {
    /// Creates a matching owner using the supplied Go-compatible defaults and wall clock.
    pub fn new(config: MatchingConfig, clock: Arc<dyn WallClock>) -> Self {
        Self::new_with_metrics(config, clock, NodeMetrics::unregistered())
    }

    /// Creates a matching owner with the process-local metric handles.
    pub fn new_with_metrics(
        config: MatchingConfig,
        clock: Arc<dyn WallClock>,
        metrics: NodeMetrics,
    ) -> Self {
        Self::new_with_lease_validity_deadline(config, clock, metrics, None)
    }

    /// Creates a matching owner that hands out Tasks only while `lease_validity_deadline` is valid;
    /// `None` never refuses.
    pub fn new_with_lease_validity_deadline(
        config: MatchingConfig,
        clock: Arc<dyn WallClock>,
        metrics: NodeMetrics,
        lease_validity_deadline: Option<LeaseValidityDeadline>,
    ) -> Self {
        assert!(config.max_streams > 0 && config.max_concurrent_tasks_cap > 0);
        assert!(
            config.max_queue_length > 0
                && config.max_flow_types > 0
                && config.max_flow_type_length > 0
        );
        assert!(config.dispatch_wait > Duration::ZERO && config.sticky_wait > Duration::ZERO);
        assert!(config.affinity_duration > Duration::ZERO && config.ping_interval > Duration::ZERO);
        assert!(config.unsupported_type_cache_entries > 0);
        let (shutdown, _) = watch::channel(false);
        let unsupported_types = UnsupportedTypeCache::new(
            config.unsupported_type_cache_entries,
            metrics.matching_unsupported_type_entries.clone(),
            metrics.matching_unsupported_type_evictions.clone(),
        );
        Self {
            inner: Arc::new(DispatchInner {
                metrics,
                config,
                clock,
                lease_validity_deadline,
                registry: Mutex::new(Registry {
                    is_closing: false,
                    streams: HashMap::new(),
                    worker_counts: HashMap::new(),
                    queues: HashMap::new(),
                    next_waiter_sequence: 0,
                    idle_positions: HashMap::new(),
                    next_idle_position: 0,
                    promised_streams: HashMap::new(),
                    unsupported_types,
                }),
                shutdown,
            }),
        }
    }

    /// Returns a gRPC WorkerTaskService bound to one trusted tenant ID.
    pub fn service_for_tenant(&self, tenant_id: u64) -> WorkerTaskService {
        WorkerTaskService {
            dispatch: self.clone(),
            tenant_id,
        }
    }

    /// Starts one dispatch; its handle emits Worker frames in order and exactly one outcome.
    pub fn dispatch(&self, request: DispatchRequest) -> Result<DispatchHandle, Status> {
        self.dispatch_with_schedule(request, None)
    }

    /// Starts one dispatch and records latency from a committed Step schedule time.
    pub fn dispatch_with_schedule(
        &self,
        request: DispatchRequest,
        scheduled_at: Option<Instant>,
    ) -> Result<DispatchHandle, Status> {
        validate_dispatch(&request, &self.inner.config, self.inner.clock.as_ref())?;
        let waiter_sequence = self.next_waiter_sequence()?;
        let (events_tx, events) = mpsc::channel(32);
        let (cancel, cancel_rx) = watch::channel(false);
        let dispatch = self.clone();
        let task = tokio::spawn(async move {
            let outcome = dispatch
                .run_dispatch(
                    request,
                    waiter_sequence,
                    scheduled_at,
                    &events_tx,
                    cancel_rx,
                )
                .await;
            if events_tx
                .send(DispatchEvent::Completed(outcome))
                .await
                .is_err()
            {
                tracing::debug!("dispatch result receiver closed");
            }
        });
        Ok(DispatchHandle {
            events,
            cancel,
            task: Some(task),
        })
    }

    /// Returns a sequence that keeps one dispatch's waiting order across its queues.
    ///
    /// Returns UNAVAILABLE once matching is closing.
    fn next_waiter_sequence(&self) -> Result<u64, Status> {
        let mut registry = self.lock_registry();
        if registry.is_closing {
            return Err(Status::unavailable("matching server is closing"));
        }
        registry.next_waiter_sequence += 1;
        Ok(registry.next_waiter_sequence)
    }

    /// Reserves an idle Worker slot that may run `request`'s Step type: the sticky Worker
    /// first while the sticky wait lasts, then any Worker of the Flow type until the dispatch
    /// wait ends. Workers in the unsupported type cache are skipped.
    pub async fn reserve_step_stream(&self, request: StepStreamRequest) -> StepStreamReservation {
        step_reservation::reserve_step_stream(self, request).await
    }

    /// Returns whether a Task may go out now: true without a lease validity deadline.
    fn may_hand_out_tasks(&self) -> bool {
        self.inner
            .lease_validity_deadline
            .as_ref()
            .is_none_or(|deadline| deadline.is_valid_at(tokio::time::Instant::now().into_std()))
    }

    /// Drains streams: idle Workers reconnect now; busy Workers reconnect after their task.
    pub fn shutdown(&self) {
        let mut registry = self.lock_registry();
        registry.is_closing = true;
        self.inner.shutdown.send_replace(true);
    }

    fn lock_registry(&self) -> std::sync::MutexGuard<'_, Registry> {
        self.inner
            .registry
            .lock()
            .expect("matching registry lock poisoned")
    }

    async fn run_dispatch(
        &self,
        request: DispatchRequest,
        waiter_sequence: u64,
        mut scheduled_at: Option<Instant>,
        events: &mpsc::Sender<DispatchEvent>,
        mut cancel: watch::Receiver<bool>,
    ) -> DispatchOutcome {
        let task_id = Uuid::now_v7().to_string();
        let now = self.inner.clock.now_unix_micros();
        let deadline_micros = system_time_micros(request.deadline);
        let deadline_wait =
            Duration::from_micros(deadline_micros.saturating_sub(now).max(0) as u64);
        let total_wait = request
            .dispatch_wait
            .unwrap_or(self.inner.config.dispatch_wait);
        let dispatch_end = Instant::now() + total_wait.min(deadline_wait);
        let sticky_wait = request.sticky_wait.unwrap_or(self.inner.config.sticky_wait);
        let task_type = request.task_type();
        let sticky_queue = request
            .sticky_app_worker_id
            .as_ref()
            .filter(|id| *id != &request.flow_type)
            .filter(|id| {
                !self
                    .lock_registry()
                    .unsupported_types
                    .contains(&task_type, id)
            });
        let sticky_end = Instant::now() + sticky_wait.min(total_wait).min(deadline_wait);
        let mut search = SearchState {
            excluded_worker_ids: HashSet::new(),
            excluded_streams: HashMap::new(),
            capacity_backoffs: HashMap::new(),
            unavailable: false,
            unknown: false,
        };
        let task = TaskSearch {
            task_type: &task_type,
            flow_id: &request.flow_id,
        };
        if let Some(queue) = sticky_queue
            && let Some(outcome) = self
                .search_queue(
                    &request,
                    &task,
                    queue,
                    &task_id,
                    waiter_sequence,
                    (sticky_end, dispatch_end),
                    true,
                    events,
                    &mut cancel,
                    &mut search,
                    &mut scheduled_at,
                )
                .await
        {
            return outcome;
        }
        if let Some(outcome) = self
            .search_queue(
                &request,
                &task,
                &request.flow_type,
                &task_id,
                waiter_sequence,
                (dispatch_end, dispatch_end),
                false,
                events,
                &mut cancel,
                &mut search,
                &mut scheduled_at,
            )
            .await
        {
            return outcome;
        }
        let is_unsupported_everywhere = search.unknown
            || self.has_only_unsupported_streams(request.tenant_id, &request.flow_type, &task_type);
        let reason = if is_unsupported_everywhere && !search.unavailable {
            DispatchTaskErrorReason::UnknownName
        } else {
            DispatchTaskErrorReason::NoWorkerAvailable
        };
        DispatchOutcome::DispatchError {
            reason,
            detail: if reason == DispatchTaskErrorReason::UnknownName {
                request.unknown_name_detail()
            } else {
                "no Worker accepted the task".to_owned()
            },
        }
    }

    #[expect(
        clippy::too_many_arguments,
        reason = "Queue search carries one dispatch's retry state across sticky and fallback queues."
    )]
    async fn search_queue(
        &self,
        request: &DispatchRequest,
        task: &TaskSearch<'_>,
        queue_name: &str,
        task_id: &str,
        waiter_sequence: u64,
        (queue_end, dispatch_end): (Instant, Instant),
        is_sticky: bool,
        events: &mpsc::Sender<DispatchEvent>,
        cancel: &mut watch::Receiver<bool>,
        search: &mut SearchState,
        scheduled_at: &mut Option<Instant>,
    ) -> Option<DispatchOutcome> {
        let mut queue_lease =
            QueueLease::acquire(self, request.tenant_id, queue_name, waiter_sequence);
        let wakeup = queue_lease.wakeup.clone();
        let mut queued_task =
            QueuedTaskMetric::new(self.inner.metrics.matching_queued_tasks.clone());
        let mut shutdown = self.inner.shutdown.subscribe();
        let search_timer = tokio::time::sleep_until(tokio::time::Instant::from_std(queue_end));
        tokio::pin!(search_timer);
        while Instant::now() < queue_end {
            if *shutdown.borrow() || !self.may_hand_out_tasks() {
                return Some(DispatchOutcome::ServerClosing);
            }
            if *cancel.borrow() {
                return Some(DispatchOutcome::Cancelled);
            }
            let now = Instant::now();
            search.excluded_streams.retain(|_, until| *until > now);
            let picked = self.pick_worker(
                &mut queue_lease,
                task,
                is_sticky,
                &search.excluded_worker_ids,
                &search.excluded_streams,
            );
            if let Some(worker) = picked.worker {
                let task_frame = build_task(task_id, &request.payload, request.deadline);
                let mut frames =
                    match self.send_task_frame(&worker, task_id, task.task_type, task_frame) {
                        Ok(frames) => frames,
                        Err(TaskSendError::LeaseInvalid) => {
                            self.make_idle(&worker);
                            return Some(DispatchOutcome::ServerClosing);
                        }
                        Err(TaskSendError::NotDelivered) => {
                            search.unavailable = true;
                            if Instant::now() >= dispatch_end {
                                return Some(disconnected_outcome("Worker stream disconnected"));
                            }
                            continue;
                        }
                    };
                queued_task.set_waiting(false);
                self.record_delivery(scheduled_at.take());
                let assigned = DispatchEvent::Assigned {
                    app_worker_id: worker.app_worker_id.clone(),
                };
                if let Some(outcome) = self
                    .forward_event(assigned, request.deadline, events, cancel)
                    .await
                {
                    self.cancel_assignment(&worker, task_id, &mut frames).await;
                    return Some(outcome);
                }
                let attempt = self
                    .await_assignment(
                        &worker,
                        task_id,
                        request.deadline,
                        dispatch_end,
                        &request.payload,
                        events,
                        cancel,
                        &mut frames,
                    )
                    .await;
                if !matches!(attempt, AttemptOutcome::Final(_))
                    && let Some(outcome) = self
                        .forward_event(DispatchEvent::Unassigned, request.deadline, events, cancel)
                        .await
                {
                    return Some(outcome);
                }
                match attempt {
                    AttemptOutcome::Final(outcome) => return Some(*outcome),
                    AttemptOutcome::Capacity => {
                        queued_task.set_waiting(true);
                        search.unavailable = true;
                        let backoff = search
                            .capacity_backoffs
                            .entry(worker.id)
                            .and_modify(|duration| {
                                *duration = (*duration * 2).min(Duration::from_secs(2));
                            })
                            .or_insert(Duration::from_millis(300));
                        search
                            .excluded_streams
                            .insert(worker.id, Instant::now() + *backoff);
                    }
                    AttemptOutcome::UnknownName => {
                        queued_task.set_waiting(true);
                        search.unknown = true;
                        search
                            .excluded_worker_ids
                            .insert(worker.app_worker_id.clone());
                        if is_sticky {
                            return None;
                        }
                    }
                    AttemptOutcome::Disconnected => {
                        queued_task.set_waiting(true);
                        search.unavailable = true;
                        search.excluded_streams.insert(worker.id, dispatch_end);
                    }
                }
                continue;
            }
            if is_sticky && !picked.has_streams {
                return None;
            }
            let next_backoff = search
                .excluded_streams
                .values()
                .copied()
                .chain(picked.next_backoff_end)
                .min()
                .unwrap_or(queue_end);
            let wake = tokio::time::Instant::from_std(next_backoff.min(queue_end));
            if search_timer.deadline() != wake {
                search_timer.as_mut().reset(wake);
            }
            tokio::select! {
                () = &mut search_timer => {},
                () = wakeup.notified() => {},
                changed = cancel.changed() => { if changed.is_err() || *cancel.borrow() { return Some(DispatchOutcome::Cancelled); } },
                changed = shutdown.changed() => { if changed.is_err() || *shutdown.borrow() { return Some(DispatchOutcome::ServerClosing); } },
            }
        }
        None
    }

    #[expect(
        clippy::too_many_arguments,
        reason = "Assignment processing needs the task's deadline, output, and cancellation controls."
    )]
    async fn await_assignment(
        &self,
        worker: &WorkerStream,
        task_id: &str,
        deadline: SystemTime,
        dispatch_end: Instant,
        payload: &DispatchTaskPayload,
        events: &mpsc::Sender<DispatchEvent>,
        cancel: &mut watch::Receiver<bool>,
        frames: &mut mpsc::Receiver<AssignmentFrame>,
    ) -> AttemptOutcome {
        let mut has_output = false;
        loop {
            let deadline_wait = Duration::from_micros(
                system_time_micros(deadline)
                    .saturating_sub(self.inner.clock.now_unix_micros())
                    .max(0) as u64,
            );
            tokio::select! {
                frame = frames.recv() => match frame {
                    Some(AssignmentFrame::Output(frame)) => {
                        let frame = *frame;
                        if is_final(&frame) {
                            return AttemptOutcome::Final(Box::new(if matches!(frame.output, Some(poll_worker_task_frame::Output::WorkerError(_))) {
                                DispatchOutcome::WorkerError { frame, app_worker_id: worker.app_worker_id.clone() }
                            } else {
                                DispatchOutcome::WorkerResult { frame, app_worker_id: worker.app_worker_id.clone() }
                            }));
                        }
                        has_output = true;
                        tokio::select! {
                            sent = events.send(DispatchEvent::Output(frame)) => {
                                if sent.is_err() {
                                    self.cancel_assignment(worker, task_id, frames).await;
                                    return AttemptOutcome::Final(Box::new(DispatchOutcome::Cancelled));
                                }
                            },
                            changed = cancel.changed() => {
                                if changed.is_err() || *cancel.borrow() {
                                    self.cancel_assignment(worker, task_id, frames).await;
                                    return AttemptOutcome::Final(Box::new(DispatchOutcome::Cancelled));
                                }
                            },
                            _ = tokio::time::sleep(deadline_wait) => {
                                self.cancel_assignment(worker, task_id, frames).await;
                                return AttemptOutcome::Final(Box::new(DispatchOutcome::DeadlineExceeded));
                            },
                        }
                    }
                    Some(AssignmentFrame::Reject(reason)) => {
                        if has_output {
                            return AttemptOutcome::Final(Box::new(disconnected_outcome("Worker rejected task after output")));
                        }
                        return match reason {
                            PollWorkerTaskRejectReason::NoCapacity => AttemptOutcome::Capacity,
                            PollWorkerTaskRejectReason::UnknownStep if !matches!(payload, DispatchTaskPayload::Rpc(_)) => AttemptOutcome::UnknownName,
                            PollWorkerTaskRejectReason::UnknownRpc if matches!(payload, DispatchTaskPayload::Rpc(_)) => AttemptOutcome::UnknownName,
                            _ => AttemptOutcome::Disconnected,
                        };
                    }
                    Some(AssignmentFrame::HalfClosed) | None => {
                        if !has_output && Instant::now() < dispatch_end {
                            return AttemptOutcome::Disconnected;
                        }
                        return AttemptOutcome::Final(Box::new(disconnected_outcome("Worker stream disconnected")));
                    }
                },
                changed = cancel.changed() => {
                    if changed.is_err() || *cancel.borrow() {
                        self.cancel_assignment(worker, task_id, frames).await;
                        return AttemptOutcome::Final(Box::new(DispatchOutcome::Cancelled));
                    }
                },
                _ = tokio::time::sleep(deadline_wait) => {
                    self.cancel_assignment(worker, task_id, frames).await;
                    return AttemptOutcome::Final(Box::new(DispatchOutcome::DeadlineExceeded));
                },
            }
        }
    }

    /// Sends `event` to the dispatch handle, waiting for room in its channel.
    ///
    /// Returns `None` once sent, or the dispatch's final outcome when it is cancelled or its
    /// deadline passes first.
    async fn forward_event(
        &self,
        event: DispatchEvent,
        deadline: SystemTime,
        events: &mpsc::Sender<DispatchEvent>,
        cancel: &mut watch::Receiver<bool>,
    ) -> Option<DispatchOutcome> {
        let deadline_wait = Duration::from_micros(
            system_time_micros(deadline)
                .saturating_sub(self.inner.clock.now_unix_micros())
                .max(0) as u64,
        );
        let send = events.send(event);
        tokio::pin!(send);
        let deadline_sleep = tokio::time::sleep(deadline_wait);
        tokio::pin!(deadline_sleep);
        loop {
            tokio::select! {
                sent = &mut send => {
                    return sent.is_err().then_some(DispatchOutcome::Cancelled);
                },
                changed = cancel.changed() => {
                    if changed.is_err() || *cancel.borrow() {
                        return Some(DispatchOutcome::Cancelled);
                    }
                },
                () = &mut deadline_sleep => return Some(DispatchOutcome::DeadlineExceeded),
            }
        }
    }

    async fn cancel_assignment(
        &self,
        worker: &WorkerStream,
        task_id: &str,
        frames: &mut mpsc::Receiver<AssignmentFrame>,
    ) {
        if worker
            .output
            .send(Ok(PollWorkerTaskResponse {
                frame: Some(poll_worker_task_response::Frame::CancelTask(
                    PollWorkerTaskCancelTask {
                        task_id: task_id.to_owned(),
                    },
                )),
            }))
            .is_err()
        {
            self.disconnect(worker.id);
            return;
        }
        let grace_end = Instant::now() + Duration::from_secs(1);
        loop {
            match tokio::time::timeout_at(tokio::time::Instant::from_std(grace_end), frames.recv())
                .await
            {
                Ok(Some(AssignmentFrame::Output(frame))) if is_final(&frame) => return,
                Ok(Some(AssignmentFrame::Reject(_))) => return,
                Ok(Some(AssignmentFrame::Output(_))) => {}
                Ok(Some(AssignmentFrame::HalfClosed) | None) | Err(_) => {
                    self.disconnect(worker.id);
                    return;
                }
            }
        }
    }

    /// Takes an idle stream of the lease's queue that may run `task`, preferring the Flow's
    /// last Worker; otherwise registers the lease as a waiter.
    fn pick_worker(
        &self,
        lease: &mut QueueLease,
        task: &TaskSearch<'_>,
        is_sticky: bool,
        excluded_worker_ids: &HashSet<String>,
        excluded_streams: &HashMap<Uuid, Instant>,
    ) -> QueuePick {
        let mut guard = self.lock_registry();
        let registry = &mut *guard;
        let woken_for = if std::mem::take(&mut lease.is_waiting) {
            registry.end_wait(&lease.key, lease.waiter_sequence)
        } else {
            None
        };
        let queue = registry.queues.get(&lease.key).expect("queue lease exists");
        let has_streams = !queue.stream_ids.is_empty();
        if registry.is_closing {
            return QueuePick::without_worker(has_streams, None);
        }
        let now = Instant::now();
        let preferred = queue
            .affinity
            .get(task.flow_id)
            .filter(|(_, expiry)| *expiry > now)
            .map(|(worker_id, _)| worker_id.clone());
        let is_step = task.task_type.kind == TaskKind::Step;
        let mut selected = None;
        let mut next_backoff_end: Option<Instant> = None;
        for id in queue.idle.values() {
            let worker = registry.streams.get(id).expect("idle stream is registered");
            // A stream offered to a woken RPC waiter is that waiter's (plan D56).
            if registry.promised_streams.contains_key(id)
                || excluded_worker_ids.contains(&worker.app_worker_id)
                || excluded_streams.contains_key(id)
                || (is_step && worker.slot == StreamSlot::ReservedRpcOnly)
                || registry.is_unsupported_on(task.task_type, worker)
            {
                continue;
            }
            if is_step
                && worker.slot == StreamSlot::Legacy
                && let Some(backoff_end) = worker.step_backoff_end(now)
            {
                next_backoff_end =
                    Some(next_backoff_end.map_or(backoff_end, |end| end.min(backoff_end)));
                continue;
            }
            let is_preferred = preferred.as_ref() == Some(&worker.app_worker_id);
            if selected.is_none() || is_preferred {
                selected = Some(worker.clone());
            }
            if preferred.is_none() || is_preferred {
                break;
            }
        }
        let queue = registry
            .queues
            .get_mut(&lease.key)
            .expect("queue lease exists");
        let Some(worker) = selected else {
            if has_streams || !is_sticky {
                // Registering under the registry lock means no idle stream is missed before the wait.
                queue.start_waiting(
                    lease,
                    task.task_type.clone(),
                    excluded_worker_ids.clone(),
                    excluded_streams,
                );
            }
            registry.pass_on_wakeup(woken_for, None, now);
            return QueuePick::without_worker(has_streams, next_backoff_end);
        };
        queue.remember(
            task.flow_id,
            &worker.app_worker_id,
            self.inner.config.affinity_duration,
        );
        registry.take_idle(&worker);
        let mut state = worker.state.lock().expect("Worker state lock poisoned");
        assert!(
            !state.is_busy && !state.is_closing,
            "idle stream must be available"
        );
        state.is_busy = true;
        drop(state);
        // A dispatch woken for one stream that takes another must not strand the first.
        registry.pass_on_wakeup(woken_for, Some(worker.id), now);
        QueuePick {
            worker: Some(worker),
            has_streams,
            next_backoff_end: None,
        }
    }

    /// Sends `task_frame` on a stream this caller took, checking the node lease and the stream's
    /// half-close under the stream's state lock, with no await in between.
    ///
    /// Returns the receiver of the Worker's frames for the task, [`TaskSendError::LeaseInvalid`]
    /// when the node may not hand out tasks (the stream stays taken), or
    /// [`TaskSendError::NotDelivered`] when the stream half-closed or is gone.
    fn send_task_frame(
        &self,
        worker: &WorkerStream,
        task_id: &str,
        task_type: &TaskTypeKey,
        task_frame: dexzd_protocol::dex::PollWorkerTask,
    ) -> Result<mpsc::Receiver<AssignmentFrame>, TaskSendError> {
        let mut state = worker.state.lock().expect("Worker state lock poisoned");
        if state.is_send_stopped || state.is_closing {
            return Err(TaskSendError::NotDelivered);
        }
        if !self.may_hand_out_tasks() {
            return Err(TaskSendError::LeaseInvalid);
        }
        let (sender, receiver) = mpsc::channel(32);
        state.current = Some(Assignment {
            task_id: task_id.to_owned(),
            task_type: task_type.clone(),
            frames: sender,
            has_output: false,
        });
        let is_sent = worker
            .output
            .send(Ok(PollWorkerTaskResponse {
                frame: Some(poll_worker_task_response::Frame::Task(task_frame)),
            }))
            .is_ok();
        if !is_sent {
            state.current = None;
            drop(state);
            self.disconnect(worker.id);
            return Err(TaskSendError::NotDelivered);
        }
        Ok(receiver)
    }

    fn record_delivery(&self, scheduled_at: Option<Instant>) {
        self.inner
            .metrics
            .worker_stream_events
            .record(WorkerStreamEvent::Delivered);
        if let Some(when_scheduled) = scheduled_at {
            self.inner
                .metrics
                .step_schedule_to_start_latency
                .observe(when_scheduled.elapsed().as_secs_f64());
        }
    }

    /// Returns whether `queue_name` has streams and the unsupported type cache excludes every
    /// one of them for `task_type`.
    fn has_only_unsupported_streams(
        &self,
        tenant_id: u64,
        queue_name: &str,
        task_type: &TaskTypeKey,
    ) -> bool {
        self.lock_registry()
            .has_only_unsupported_streams(tenant_id, queue_name, task_type)
    }

    /// Returns a stream that finished its task, or was taken without one, to its queues.
    fn make_idle(&self, worker: &WorkerStream) {
        let mut registry = self.lock_registry();
        if !registry.streams.contains_key(&worker.id) {
            return;
        }
        let mut state = worker.state.lock().expect("Worker state lock poisoned");
        if state.is_send_stopped {
            return;
        }
        state.is_busy = false;
        state.current = None;
        if state.is_closing {
            // Matching shuts down; a slot freed without a task would otherwise stay open.
            drop(state);
            drop(registry);
            worker_streams::send_reconnect(worker, "matching server is closing");
            self.disconnect(worker.id);
            return;
        }
        drop(state);
        registry.add_idle(worker);
        registry.wake_waiter_for(worker, Instant::now());
    }

    /// Wakes a Step waiter for an idle stream whose Step backoff just ended.
    fn end_step_backoff(&self, stream_id: Uuid) {
        let mut registry = self.lock_registry();
        if !registry.idle_positions.contains_key(&stream_id) {
            return;
        }
        let Some(worker) = registry.streams.get(&stream_id).cloned() else {
            return;
        };
        registry.wake_waiter_for(&worker, Instant::now());
    }

    fn disconnect(&self, id: Uuid) {
        let mut registry = self.lock_registry();
        let Some(worker) = registry.streams.remove(&id) else {
            return;
        };
        self.inner
            .metrics
            .worker_stream_events
            .record(WorkerStreamEvent::Closed);
        self.inner.metrics.matching_worker_streams.dec();
        let worker_key = (worker.tenant_id, worker.app_worker_id.clone());
        if let Some(count) = registry.worker_counts.get_mut(&worker_key) {
            *count -= 1;
            if *count == 0 {
                registry.worker_counts.remove(&worker_key);
                registry
                    .unsupported_types
                    .remove_worker(worker.tenant_id, &worker.app_worker_id);
            }
        }
        registry.take_idle(&worker);
        for queue_name in &worker.queues {
            let queue_key = (worker.tenant_id, queue_name.clone());
            if let Some(queue) = registry.queues.get_mut(&queue_key) {
                queue.stream_ids.remove(&id);
                queue.users -= 1;
                if queue.stream_ids.is_empty() {
                    // Sticky searches end as soon as their queue has no streams.
                    queue.wake_all_waiters();
                }
                if queue.users == 0 {
                    registry.queues.remove(&queue_key);
                }
            }
        }
        let mut state = worker.state.lock().expect("Worker state lock poisoned");
        state.is_closing = true;
        state.current = None;
        state.step_backoff = None;
        worker.close.send_replace(true);
    }
}

enum AttemptOutcome {
    Final(Box<DispatchOutcome>),
    Capacity,
    UnknownName,
    Disconnected,
}
