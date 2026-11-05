// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Worker stream kinds, the Step backoff of streams without reserved-slot dispatch, clean
//! half-close, and the end of an assignment.

use super::*;
use dexzd_metrics::WorkerStreamProtocolError;
use dexzd_protocol::dex::{PollWorkerSlotKind, PollWorkerTaskRegister};

const FIRST_STEP_BACKOFF: Duration = Duration::from_millis(300);
const LONGEST_STEP_BACKOFF: Duration = Duration::from_secs(2);

/// How a stream takes tasks.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) enum StreamSlot {
    /// An SDK without reserved-slot dispatch: one queue, and capacity rejections are normal.
    Legacy,
    /// One reserved Worker slot for Steps and RPCs.
    ReservedStepAndRpc,
    /// One reserved Worker slot for RPCs only (plan D57).
    ReservedRpcOnly,
}

impl StreamSlot {
    /// Returns the slot of a validated registration.
    pub(super) fn of(registration: &PollWorkerTaskRegister) -> Self {
        if !registration.supports_reserved_slot_dispatch {
            return Self::Legacy;
        }
        match PollWorkerSlotKind::try_from(registration.slot_kind) {
            Ok(PollWorkerSlotKind::RpcOnly) => Self::ReservedRpcOnly,
            _ => Self::ReservedStepAndRpc,
        }
    }

    pub(super) fn is_reserved(self) -> bool {
        self != Self::Legacy
    }
}

/// Returns the queues a validated registration serves: a reserved slot serves its sticky queue
/// and every declared Flow type (plan D58), a legacy stream its one queue.
pub(super) fn subscribed_queues(registration: &PollWorkerTaskRegister) -> Vec<String> {
    if !registration.supports_reserved_slot_dispatch {
        return vec![registration.queue.clone()];
    }
    let mut queues = vec![registration.app_worker_id.clone()];
    for flow_type in &registration.flow_types {
        if !queues.contains(flow_type) {
            queues.push(flow_type.clone());
        }
    }
    queues
}

/// A Step capacity backoff of a legacy stream; dropping it stops its wake-up timer.
pub(super) struct StepBackoff {
    duration: Duration,
    ends_at: Instant,
    end_timer: JoinHandle<()>,
}

impl Drop for StepBackoff {
    fn drop(&mut self) {
        self.end_timer.abort();
    }
}

/// Why a task frame was not sent.
pub(super) enum TaskSendError {
    /// The stream half-closed or is gone, so the Worker never received the task.
    NotDelivered,
    /// The node lease is no longer valid; the stream stays with the caller.
    LeaseInvalid,
}

impl WorkerStream {
    /// Returns when the stream's Step backoff ends, or `None` when Steps may use it at `now`.
    pub(super) fn step_backoff_end(&self, now: Instant) -> Option<Instant> {
        let state = self.state.lock().expect("Worker state lock poisoned");
        state
            .step_backoff
            .as_ref()
            .map(|backoff| backoff.ends_at)
            .filter(|ends_at| *ends_at > now)
    }
}

/// Stops sending on a reserved-slot stream the Worker cleanly half-closed, then tells its
/// assignment, after every earlier frame, so it can tell whether the task had output.
pub(super) async fn half_close(dispatch: &WorkerDispatch, worker: &WorkerStream) {
    let frames = {
        let mut registry = dispatch.lock_registry();
        registry.take_idle(worker);
        let mut state = worker.state.lock().expect("Worker state lock poisoned");
        state.is_send_stopped = true;
        state
            .current
            .as_ref()
            .map(|assignment| assignment.frames.clone())
    };
    if let Some(frames) = frames
        && frames.send(AssignmentFrame::HalfClosed).await.is_err()
    {
        tracing::debug!("half-close reached an abandoned assignment");
    }
}

/// How an assignment ended on its stream.
pub(super) enum AssignmentEnd {
    /// The Worker sent the task's final output.
    Final,
    /// The Worker declined the task before any output.
    Rejected(PollWorkerTaskRejectReason),
}

/// Ends the assignment of `task_id` and makes the stream idle again.
///
/// An unknown type goes into the unsupported type cache first, so no dispatch of that type
/// takes the stream once it is idle. A legacy capacity rejection starts or doubles the
/// stream's Step backoff; a Step's final output ends it. Returns false when the stream must close because matching shuts down.
pub(super) fn finish_assignment(
    dispatch: &WorkerDispatch,
    worker: &WorkerStream,
    task_id: &str,
    end: AssignmentEnd,
) -> bool {
    let mut state = worker.state.lock().expect("Worker state lock poisoned");
    let Some(assignment) = state
        .current
        .as_ref()
        .filter(|assignment| assignment.task_id == task_id)
    else {
        return true;
    };
    let task_type = assignment.task_type.clone();
    state.current = None;
    state.is_busy = false;
    match end {
        // An RPC result says nothing about the Worker's Step capacity.
        AssignmentEnd::Final if task_type.kind == TaskKind::Step => state.step_backoff = None,
        AssignmentEnd::Final => {}
        AssignmentEnd::Rejected(PollWorkerTaskRejectReason::NoCapacity)
            if worker.slot == StreamSlot::Legacy =>
        {
            let duration = state
                .step_backoff
                .as_ref()
                .map_or(FIRST_STEP_BACKOFF, |backoff| {
                    (backoff.duration * 2).min(LONGEST_STEP_BACKOFF)
                });
            let ends_at = Instant::now() + duration;
            let timer_dispatch = dispatch.clone();
            let stream_id = worker.id;
            let end_timer = tokio::spawn(async move {
                tokio::time::sleep_until(tokio::time::Instant::from_std(ends_at)).await;
                timer_dispatch.end_step_backoff(stream_id);
            });
            state.step_backoff = Some(StepBackoff {
                duration,
                ends_at,
                end_timer,
            });
        }
        AssignmentEnd::Rejected(reason) => {
            let is_unknown_type = match task_type.kind {
                TaskKind::Step => reason == PollWorkerTaskRejectReason::UnknownStep,
                TaskKind::Rpc => reason == PollWorkerTaskRejectReason::UnknownRpc,
            };
            if is_unknown_type {
                drop(state);
                dispatch
                    .lock_registry()
                    .unsupported_types
                    .insert(task_type, &worker.app_worker_id);
                state = worker.state.lock().expect("Worker state lock poisoned");
            }
        }
    }
    if state.is_closing {
        send_reconnect(worker, "matching server is closing");
        return false;
    }
    drop(state);
    dispatch.make_idle(worker);
    true
}

/// Records a reserved-slot stream that broke the dispatch protocol.
pub(super) fn record_protocol_error(dispatch: &WorkerDispatch, error: WorkerStreamProtocolError) {
    dispatch
        .inner
        .metrics
        .worker_stream_protocol_errors
        .record(error);
}

pub(super) fn send_reconnect(worker: &WorkerStream, reason: &str) {
    if worker
        .output
        .send(Ok(PollWorkerTaskResponse {
            frame: Some(poll_worker_task_response::Frame::Reconnect(
                PollWorkerTaskReconnect {
                    reason: reason.to_owned(),
                },
            )),
        }))
        .is_err()
    {
        tracing::debug!("Worker response stream closed before reconnect");
    }
}
