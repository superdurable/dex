// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The Worker transport behind method dispatch: pull Worker matching in production, a scripted
//! fake in tests.

use std::time::{Instant, SystemTime};

use dexzd_protocol::dex::DispatchTaskErrorReason;
use dexzd_worker_dispatch::{
    DispatchTaskPayload, ReservedStepStream, StepStreamRequest, StepStreamReservation,
    StepTaskEvent, StepTaskHandle, StepTaskSendFailure, WorkerDispatch,
};
use futures::future::BoxFuture;

use crate::effects::method_request::MethodRequest;

/// One method attempt that needs a Worker slot.
#[derive(Debug, Clone)]
pub struct MethodStreamRequest {
    /// Tenant of the Flow.
    pub tenant_id: u64,
    /// Flow ID, for same-Flow Worker affinity.
    pub flow_id: String,
    /// Flow type queue.
    pub flow_type: String,
    /// Step type of the attempt.
    pub step_type: String,
    /// Worker that last served the run, tried first.
    pub sticky_app_worker_id: Option<String>,
    /// When this search for a Worker started; the sticky and dispatch waits count from it.
    pub search_started_at: Instant,
}

/// The result of reserving a Worker slot.
pub enum MethodStreamReservation {
    /// A Worker slot is reserved until its task is sent or the reservation is dropped.
    Reserved(Box<dyn ReservedMethodStream>),
    /// No Worker took the attempt within the dispatch wait.
    NoWorker {
        /// NO_WORKER_AVAILABLE, or UNKNOWN_NAME when every connected Worker declined the Step
        /// type.
        reason: DispatchTaskErrorReason,
        /// Human-readable diagnostic.
        detail: String,
    },
    /// The matching owner is closing; nothing was reserved.
    ServerClosing,
    /// Matching refused the request.
    Invalid {
        /// Why matching refused it.
        detail: String,
    },
}

/// The task frame of one method attempt.
#[derive(Debug, Clone)]
pub struct MethodTask {
    /// The attempt's task ID as the Worker sees it.
    pub task_id: String,
    /// The Worker request.
    pub request: MethodRequest,
    /// Method deadline, including Worker processing time.
    pub deadline: SystemTime,
    /// When the attempt became pending, for the schedule-to-start latency.
    pub scheduled_at: Option<Instant>,
}

/// One reserved Worker slot. Dropping it without sending returns the slot.
pub trait ReservedMethodStream: Send {
    /// Returns the Worker process that owns the slot.
    fn app_worker_id(&self) -> &str;

    /// Sends the task frame.
    ///
    /// Returns the task's events, or why the Worker did not receive the task.
    fn send_task(
        self: Box<Self>,
        task: MethodTask,
    ) -> Result<Box<dyn MethodDispatchSession>, StepTaskSendFailure>;
}

/// Ordered Worker frames and the single final event of one sent task. Dropping it cancels the
/// Worker task.
pub trait MethodDispatchSession: Send {
    /// Returns the next Worker frame or the final event, then `None`.
    fn next_event(&mut self) -> BoxFuture<'_, Option<StepTaskEvent>>;
    /// Asks the Worker to stop the task.
    fn cancel(&self);
}

/// Hands method attempts to Workers.
pub trait MethodDispatcher: Send + Sync + 'static {
    /// Reserves a Worker slot for one method attempt.
    fn reserve_stream(
        &self,
        request: MethodStreamRequest,
    ) -> BoxFuture<'_, MethodStreamReservation>;
}

impl MethodDispatcher for WorkerDispatch {
    fn reserve_stream(
        &self,
        request: MethodStreamRequest,
    ) -> BoxFuture<'_, MethodStreamReservation> {
        Box::pin(async move {
            let reservation = self
                .reserve_step_stream(StepStreamRequest {
                    tenant_id: request.tenant_id,
                    flow_id: request.flow_id,
                    flow_type: request.flow_type,
                    step_type: request.step_type,
                    sticky_app_worker_id: request.sticky_app_worker_id,
                    search_started_at: request.search_started_at,
                })
                .await;
            match reservation {
                StepStreamReservation::Reserved(stream) => {
                    MethodStreamReservation::Reserved(Box::new(WorkerSlot { stream }))
                }
                StepStreamReservation::NoWorker { reason, detail } => {
                    MethodStreamReservation::NoWorker { reason, detail }
                }
                StepStreamReservation::ServerClosing => MethodStreamReservation::ServerClosing,
                StepStreamReservation::Invalid { detail } => {
                    MethodStreamReservation::Invalid { detail }
                }
            }
        })
    }
}

struct WorkerSlot {
    stream: ReservedStepStream,
}

impl ReservedMethodStream for WorkerSlot {
    fn app_worker_id(&self) -> &str {
        self.stream.app_worker_id()
    }

    fn send_task(
        self: Box<Self>,
        task: MethodTask,
    ) -> Result<Box<dyn MethodDispatchSession>, StepTaskSendFailure> {
        let payload = match task.request {
            MethodRequest::WaitFor(request) => DispatchTaskPayload::WaitFor(request),
            MethodRequest::Execute(request) => DispatchTaskPayload::Execute(request),
        };
        let handle =
            self.stream
                .send_task(task.task_id, payload, task.deadline, task.scheduled_at)?;
        Ok(Box::new(WorkerTaskSession { handle }))
    }
}

struct WorkerTaskSession {
    handle: StepTaskHandle,
}

impl MethodDispatchSession for WorkerTaskSession {
    fn next_event(&mut self) -> BoxFuture<'_, Option<StepTaskEvent>> {
        Box::pin(self.handle.next())
    }

    fn cancel(&self) {
        self.handle.cancel();
    }
}
