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
use dexzd_request_validation::quote_go_string;

use super::worker_streams::record_protocol_error;

/// Inputs for reserving one Worker stream for a Step method attempt.
#[derive(Debug, Clone)]
pub struct StepStreamRequest {
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
    /// When the attempt first searched for a Worker; the sticky and dispatch waits count from
    /// it, also across returns of the same attempt.
    pub search_started_at: Instant,
}

/// The result of reserving a Worker stream.
pub enum StepStreamReservation {
    /// An idle Worker slot is reserved for this attempt until it is sent or dropped.
    Reserved(ReservedStepStream),
    /// No Worker took the attempt within the dispatch wait.
    NoWorker {
        /// NO_WORKER_AVAILABLE, or UNKNOWN_NAME when every connected Worker declined the Step
        /// type.
        reason: DispatchTaskErrorReason,
        /// Human-readable diagnostic.
        detail: String,
    },
    /// Matching is closing or its node lease is no longer valid; nothing was reserved.
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

impl WorkerDispatch {
    /// Reserves an idle Worker slot that may run `request`'s Step type: the sticky Worker
    /// first while the sticky wait lasts, then any Worker of the Flow type until the dispatch
    /// wait ends. Workers in the unsupported type cache are skipped.
    pub async fn reserve_step_stream(&self, request: StepStreamRequest) -> StepStreamReservation {
        if let Err(status) = validate_step_request(&request, &self.inner.config) {
            return StepStreamReservation::Invalid {
                detail: status.message().to_owned(),
            };
        }
        let Ok(waiter_sequence) = self.next_waiter_sequence() else {
            return StepStreamReservation::ServerClosing;
        };
        let task_type = TaskTypeKey {
            tenant_id: request.tenant_id,
            flow_type: request.flow_type.clone(),
            kind: TaskKind::Step,
            name: request.step_type.clone(),
        };
        let task = TaskSearch {
            task_type: &task_type,
            flow_id: &request.flow_id,
        };
        let config = &self.inner.config;
        let dispatch_end = request.search_started_at + config.dispatch_wait;
        let sticky_end = request.search_started_at + config.sticky_wait.min(config.dispatch_wait);
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
        if let Some(queue) = sticky_queue
            && Instant::now() < sticky_end
        {
            match self
                .reserve_in_queue(&request, &task, queue, waiter_sequence, sticky_end, true)
                .await
            {
                QueueReservation::Reserved(worker) => {
                    return self.reserved(worker);
                }
                QueueReservation::ServerClosing => return StepStreamReservation::ServerClosing,
                QueueReservation::Ended => {}
            }
        }
        match self
            .reserve_in_queue(
                &request,
                &task,
                &request.flow_type,
                waiter_sequence,
                dispatch_end,
                false,
            )
            .await
        {
            QueueReservation::Reserved(worker) => self.reserved(worker),
            QueueReservation::ServerClosing => StepStreamReservation::ServerClosing,
            QueueReservation::Ended => {
                if self.has_only_unsupported_streams(
                    request.tenant_id,
                    &request.flow_type,
                    &task_type,
                ) {
                    StepStreamReservation::NoWorker {
                        reason: DispatchTaskErrorReason::UnknownName,
                        detail: format!(
                            "no pull Worker registered Step {} for Flow type {}",
                            quote_go_string(&request.step_type),
                            quote_go_string(&request.flow_type)
                        ),
                    }
                } else {
                    StepStreamReservation::NoWorker {
                        reason: DispatchTaskErrorReason::NoWorkerAvailable,
                        detail: "no Worker accepted the task".to_owned(),
                    }
                }
            }
        }
    }

    fn reserved(&self, worker: Arc<WorkerStream>) -> StepStreamReservation {
        StepStreamReservation::Reserved(ReservedStepStream {
            dispatch: self.clone(),
            worker: Some(worker),
        })
    }

    /// Waits in one queue until it reserves a stream, `queue_end`, or matching closes. A
    /// sticky search also ends once its queue has no streams.
    async fn reserve_in_queue(
        &self,
        request: &StepStreamRequest,
        task: &TaskSearch<'_>,
        queue_name: &str,
        waiter_sequence: u64,
        queue_end: Instant,
        is_sticky: bool,
    ) -> QueueReservation {
        let mut queue_lease =
            QueueLease::acquire(self, request.tenant_id, queue_name, waiter_sequence);
        let wakeup = queue_lease.wakeup.clone();
        let _queued_task = QueuedTaskMetric::new(self.inner.metrics.matching_queued_tasks.clone());
        let mut shutdown = self.inner.shutdown.subscribe();
        let search_timer = tokio::time::sleep_until(tokio::time::Instant::from_std(queue_end));
        tokio::pin!(search_timer);
        while Instant::now() < queue_end {
            if *shutdown.borrow() || !self.may_hand_out_tasks() {
                return QueueReservation::ServerClosing;
            }
            let picked = self.pick_worker(
                &mut queue_lease,
                task,
                is_sticky,
                &HashSet::new(),
                &HashMap::new(),
            );
            if let Some(worker) = picked.worker {
                return QueueReservation::Reserved(worker);
            }
            if is_sticky && !picked.has_streams {
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
                changed = shutdown.changed() => {
                    if changed.is_err() || *shutdown.borrow() {
                        return QueueReservation::ServerClosing;
                    }
                },
            }
        }
        QueueReservation::Ended
    }
}

enum QueueReservation {
    Reserved(Arc<WorkerStream>),
    Ended,
    ServerClosing,
}

impl ReservedStepStream {
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
