// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Step hand-out: reserve an idle Worker slot first, let the engine record the start, then send
//! the task frame (plan D51).

use super::*;
use dexzd_metrics::WorkerStreamProtocolError;
use dexzd_protocol::dex::PollWorkerTask;

use super::backlog_matching::partition_removed;
use super::worker_streams::record_protocol_error;
use dexzd_keyspace::PartitionId;
use dexzd_keyspace::dispatch_queue_keys::DispatchQueueName;

/// Inputs for reserving one Worker stream for a Step method attempt.
#[derive(Debug, Clone)]
pub struct StepStreamRequest {
    /// Partition of the Flow; removing the partition ends the search.
    pub partition: PartitionId,
    /// Tenant containing the Flow and its Worker queues; use zero for the default tenant.
    pub tenant_id: u64,
    /// Flow instance ID used for same-Flow affinity.
    pub flow_id: String,
    /// Flow type queue used after an optional sticky Worker search.
    pub flow_type: String,
    /// Step type of the attempt; Workers that declined it are skipped.
    pub step_type: String,
    /// Worker process ID preferred while the sticky wait lasts.
    pub sticky_app_worker_id: Option<String>,
}

/// The result of reserving a Worker stream.
pub enum StepStreamReservation {
    /// An idle Worker slot is reserved for this attempt until it is sent or dropped.
    Reserved(ReservedStepStream),
    /// No Worker took the attempt within the sync match wait, or its queue already has
    /// backlog; the attempt goes to the durable task queue (plan D53).
    Enqueue,
    /// Matching is closing, its node lease is no longer valid, or the partition was removed;
    /// nothing was reserved and nothing should be written.
    ServerClosing,
    /// The request is invalid, such as an overlong Flow type.
    Invalid {
        /// Why the request is invalid.
        detail: String,
    },
}

/// One reserved Worker slot. Dropping it without sending returns the slot to its queues.
pub struct ReservedStepStream {
    dispatch: WorkerDispatch,
    worker: Option<Arc<WorkerStream>>,
}

/// Why a reserved stream did not receive its task frame.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum StepTaskSendFailure {
    /// The Worker certainly never received the task: its stream half-closed, broke, or
    /// is gone.
    NotDelivered,
    /// The node lease is no longer valid, so this node hands out no task. The slot returns
    /// to its queues.
    LeaseInvalid,
}

/// One Worker frame or the end of a sent Step task.
#[derive(Debug)]
pub enum StepTaskEvent {
    /// A non-final heartbeat or stream write.
    Output(PollWorkerTaskFrame),
    /// The Worker's final result or error, or the end of a canceled task.
    Completed(DispatchOutcome),
    /// The Worker declined the task before any output, so the handler never ran.
    Rejected {
        /// The Worker's reason.
        reason: PollWorkerTaskRejectReason,
        /// Whether the stream used reserved-slot dispatch, where a capacity rejection is a
        /// protocol error.
        is_reserved_slot: bool,
    },
    /// The Worker cleanly half-closed a reserved-slot stream before any output, so by
    /// protocol it never ran the task.
    NotDelivered,
    /// The stream failed, or the Worker broke the protocol, after the task frame went out;
    /// the Worker may have run the task.
    Disconnected {
        /// Human-readable diagnostic.
        detail: String,
    },
}

/// Receives the events of one sent Step task; dropping it cancels the task.
pub struct StepTaskHandle {
    events: mpsc::Receiver<StepTaskEvent>,
    cancel: watch::Sender<bool>,
    task: Option<JoinHandle<()>>,
}

impl StepTaskHandle {
    /// Returns the next Worker output or the task's single final event, then `None`.
    pub async fn next(&mut self) -> Option<StepTaskEvent> {
        self.events.recv().await
    }

    /// Sends cancel_task, then allows one second for the Worker's final frame.
    pub fn cancel(&self) {
        self.cancel.send_replace(true);
    }
}

impl Drop for StepTaskHandle {
    fn drop(&mut self) {
        self.cancel();
        // The follower task owns its one-second cancellation grace period.
        drop(self.task.take());
    }
}

/// Reserves an idle Worker slot for `request`; see [`WorkerDispatch::reserve_step_stream`].
///
/// Reserves a slot; backlog that a connected Worker can run sends the attempt to the queue.
pub(super) async fn reserve_step_stream(
    dispatch: &WorkerDispatch,
    request: StepStreamRequest,
) -> StepStreamReservation {
    if let Err(status) = validate_step_request(&request, &dispatch.inner.config) {
        return StepStreamReservation::Invalid {
            detail: status.message().to_owned(),
        };
    }
    let Ok(waiter_sequence) = dispatch.next_waiter_sequence() else {
        return StepStreamReservation::ServerClosing;
    };
    let partition_search = dispatch.register_partition_search(request.partition);
    let task_type = TaskTypeKey {
        tenant_id: request.tenant_id,
        flow_type: request.flow_type.clone(),
        kind: TaskKind::Step,
        name: request.step_type.clone(),
    };
    let queue_name = DispatchQueueName {
        tenant_id: dexzd_keyspace::TenantId(request.tenant_id),
        flow_type: request.flow_type.clone(),
        step_type: request.step_type.clone(),
    };
    let task = TaskSearch {
        task_type: &task_type,
        flow_id: &request.flow_id,
        partition_search: Some(partition_search.search.as_ref()),
    };
    if dispatch.has_qualified_backlog(&queue_name) {
        return StepStreamReservation::Enqueue;
    }
    let config = &dispatch.inner.config;
    let sticky_queue = request
        .sticky_app_worker_id
        .as_ref()
        .filter(|id| *id != &request.flow_type)
        .filter(|id| {
            !dispatch
                .lock_registry()
                .unsupported_types
                .contains(&task_type, id)
        });
    if let Some(queue) = sticky_queue {
        let sticky_end = Instant::now() + config.sticky_wait;
        match reserve_in_queue(
            dispatch,
            &request,
            &task,
            queue,
            waiter_sequence,
            sticky_end,
            true,
        )
        .await
        {
            QueueReservation::Reserved(worker) => return reserved(dispatch, worker),
            QueueReservation::ServerClosing => return StepStreamReservation::ServerClosing,
            QueueReservation::Ended => {}
        }
        if dispatch.has_qualified_backlog(&queue_name) {
            return StepStreamReservation::Enqueue;
        }
    }
    let sync_end = Instant::now() + config.sync_match_wait;
    match reserve_in_queue(
        dispatch,
        &request,
        &task,
        &request.flow_type,
        waiter_sequence,
        sync_end,
        false,
    )
    .await
    {
        QueueReservation::Reserved(worker) => reserved(dispatch, worker),
        QueueReservation::ServerClosing => StepStreamReservation::ServerClosing,
        QueueReservation::Ended => StepStreamReservation::Enqueue,
    }
}

fn reserved(dispatch: &WorkerDispatch, worker: Arc<WorkerStream>) -> StepStreamReservation {
    StepStreamReservation::Reserved(ReservedStepStream {
        dispatch: dispatch.clone(),
        worker: Some(worker),
    })
}

/// Waits in one queue for a stream until `queue_end`, after at least one pick.
async fn reserve_in_queue(
    dispatch: &WorkerDispatch,
    request: &StepStreamRequest,
    task: &TaskSearch<'_>,
    queue_name: &str,
    waiter_sequence: u64,
    queue_end: Instant,
    is_sticky: bool,
) -> QueueReservation {
    let mut queue_lease =
        QueueLease::acquire(dispatch, request.tenant_id, queue_name, waiter_sequence);
    let wakeup = queue_lease.wakeup.clone();
    let _queued_task = QueuedTaskMetric::new(dispatch.inner.metrics.matching_queued_tasks.clone());
    let mut shutdown = dispatch.inner.shutdown.subscribe();
    let search_timer = tokio::time::sleep_until(tokio::time::Instant::from_std(queue_end));
    tokio::pin!(search_timer);
    loop {
        let is_removed = task
            .partition_search
            .is_some_and(|search| search.is_removed());
        if *shutdown.borrow() || !dispatch.may_hand_out_tasks() || is_removed {
            return QueueReservation::ServerClosing;
        }
        let picked = dispatch.pick_worker(
            &mut queue_lease,
            task,
            is_sticky,
            &HashSet::new(),
            &HashMap::new(),
        );
        if let Some(worker) = picked.worker {
            return QueueReservation::Reserved(worker);
        }
        if (is_sticky && !picked.has_streams) || Instant::now() >= queue_end {
            return QueueReservation::Ended;
        }
        let wake = picked.next_backoff_end.unwrap_or(queue_end).min(queue_end);
        let wake = tokio::time::Instant::from_std(wake);
        if search_timer.deadline() != wake {
            search_timer.as_mut().reset(wake);
        }
        tokio::select! {
            () = &mut search_timer => {},
            () = wakeup.notified() => {},
            () = partition_removed(task.partition_search) => return QueueReservation::ServerClosing,
            changed = shutdown.changed() => {
                if changed.is_err() || *shutdown.borrow() {
                    return QueueReservation::ServerClosing;
                }
            },
        }
    }
}

enum QueueReservation {
    Reserved(Arc<WorkerStream>),
    Ended,
    ServerClosing,
}

impl ReservedStepStream {
    pub(super) fn new(dispatch: WorkerDispatch, worker: Arc<WorkerStream>) -> Self {
        Self {
            dispatch,
            worker: Some(worker),
        }
    }

    /// Returns whether this node may hand out a task now; false once its lease is invalid.
    pub fn may_hand_out_tasks(&self) -> bool {
        self.dispatch.may_hand_out_tasks()
    }

    /// Returns the Worker process that owns the reserved slot.
    pub fn app_worker_id(&self) -> &str {
        &self.worker().app_worker_id
    }

    /// Sends the task frame of `task_id` on the reserved slot. The node lease and the stream's
    /// half-close are checked under the stream's lock, with no await before the send.
    ///
    /// Returns the task's events, or why the Worker did not receive it.
    pub fn send_task(
        mut self,
        task_id: String,
        payload: DispatchTaskPayload,
        deadline: SystemTime,
        scheduled_at: Option<Instant>,
    ) -> Result<StepTaskHandle, StepTaskSendFailure> {
        let worker = self.worker().clone();
        let (kind, step_type, flow_type) = match &payload {
            DispatchTaskPayload::WaitFor(request) => {
                (TaskKind::Step, &request.step_type, &request.flow_type)
            }
            DispatchTaskPayload::Execute(request) => {
                (TaskKind::Step, &request.step_type, &request.flow_type)
            }
            DispatchTaskPayload::Rpc(request) => {
                (TaskKind::Rpc, &request.rpc_name, &request.flow_type)
            }
        };
        let task_type = TaskTypeKey {
            tenant_id: worker.tenant_id,
            flow_type: flow_type.clone(),
            kind,
            name: step_type.clone(),
        };
        let task_frame: PollWorkerTask = build_task(&task_id, &payload, deadline);
        let frames = match self
            .dispatch
            .send_task_frame(&worker, &task_id, &task_type, task_frame)
        {
            Ok(frames) => frames,
            Err(TaskSendError::LeaseInvalid) => return Err(StepTaskSendFailure::LeaseInvalid),
            Err(TaskSendError::NotDelivered) => {
                // The stream half-closed or broke; it never becomes idle again.
                self.worker = None;
                self.dispatch.disconnect(worker.id);
                return Err(StepTaskSendFailure::NotDelivered);
            }
        };
        self.worker = None;
        self.dispatch.record_delivery(scheduled_at);
        let (events_sender, events) = mpsc::channel(32);
        let (cancel, cancel_receiver) = watch::channel(false);
        let dispatch = self.dispatch.clone();
        let task = tokio::spawn(async move {
            follow_step_task(
                &dispatch,
                &worker,
                &task_id,
                frames,
                &events_sender,
                cancel_receiver,
            )
            .await;
        });
        Ok(StepTaskHandle {
            events,
            cancel,
            task: Some(task),
        })
    }

    fn worker(&self) -> &Arc<WorkerStream> {
        self.worker
            .as_ref()
            .expect("a reservation keeps its stream until it is sent or dropped")
    }
}

impl Drop for ReservedStepStream {
    fn drop(&mut self) {
        if let Some(worker) = self.worker.take() {
            self.dispatch.make_idle(&worker);
        }
    }
}

/// Forwards one sent task's Worker frames until its final event or cancellation.
async fn follow_step_task(
    dispatch: &WorkerDispatch,
    worker: &WorkerStream,
    task_id: &str,
    mut frames: mpsc::Receiver<AssignmentFrame>,
    events: &mpsc::Sender<StepTaskEvent>,
    mut cancel: watch::Receiver<bool>,
) {
    let mut has_output = false;
    loop {
        let event = tokio::select! {
            frame = frames.recv() => frame,
            changed = cancel.changed() => {
                if changed.is_err() || *cancel.borrow() {
                    dispatch.cancel_assignment(worker, task_id, &mut frames).await;
                    send_final(events, StepTaskEvent::Completed(DispatchOutcome::Cancelled)).await;
                    return;
                }
                continue;
            },
        };
        let final_event = match event {
            Some(AssignmentFrame::Output(frame)) => {
                let frame = *frame;
                if !is_final(&frame) {
                    has_output = true;
                    tokio::select! {
                        sent = events.send(StepTaskEvent::Output(frame)) => {
                            if sent.is_err() {
                                dispatch.cancel_assignment(worker, task_id, &mut frames).await;
                                return;
                            }
                        },
                        changed = cancel.changed() => {
                            if changed.is_err() || *cancel.borrow() {
                                dispatch.cancel_assignment(worker, task_id, &mut frames).await;
                                send_final(events, StepTaskEvent::Completed(DispatchOutcome::Cancelled)).await;
                                return;
                            }
                        },
                    }
                    continue;
                }
                let app_worker_id = worker.app_worker_id.clone();
                let outcome = if matches!(
                    frame.output,
                    Some(poll_worker_task_frame::Output::WorkerError(_))
                ) {
                    DispatchOutcome::WorkerError {
                        frame,
                        app_worker_id,
                    }
                } else {
                    DispatchOutcome::WorkerResult {
                        frame,
                        app_worker_id,
                    }
                };
                StepTaskEvent::Completed(outcome)
            }
            Some(AssignmentFrame::Reject(_)) if has_output => StepTaskEvent::Disconnected {
                detail: "Worker rejected task after output".to_owned(),
            },
            Some(AssignmentFrame::Reject(reason)) => StepTaskEvent::Rejected {
                reason,
                is_reserved_slot: worker.slot.is_reserved(),
            },
            Some(AssignmentFrame::HalfClosed) if has_output => {
                record_protocol_error(dispatch, WorkerStreamProtocolError::HalfCloseAfterOutput);
                StepTaskEvent::Disconnected {
                    detail: "Worker half-closed its stream before the task's final output"
                        .to_owned(),
                }
            }
            Some(AssignmentFrame::HalfClosed) => StepTaskEvent::NotDelivered,
            None => StepTaskEvent::Disconnected {
                detail: "Worker stream disconnected".to_owned(),
            },
        };
        send_final(events, final_event).await;
        return;
    }
}

async fn send_final(events: &mpsc::Sender<StepTaskEvent>, event: StepTaskEvent) {
    if events.send(event).await.is_err() {
        tracing::debug!("Step task event receiver closed");
    }
}

fn validate_step_request(
    request: &StepStreamRequest,
    config: &MatchingConfig,
) -> Result<(), Status> {
    if request.flow_type.is_empty() || request.flow_type.len() > config.max_queue_length {
        return Err(Status::invalid_argument(
            "Flow type queue must have a valid length",
        ));
    }
    if request
        .sticky_app_worker_id
        .as_ref()
        .is_some_and(|id| id.is_empty() || id.len() > config.max_queue_length)
    {
        return Err(Status::invalid_argument(
            "sticky queue must have a valid length",
        ));
    }
    Ok(())
}
