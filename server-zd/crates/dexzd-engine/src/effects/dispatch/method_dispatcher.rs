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

use dexzd_keyspace::PartitionId;
use dexzd_keyspace::dispatch_queue_keys::DispatchQueueName;
use dexzd_worker_dispatch::{
    ClaimReply, DispatchTaskPayload, HeadsScanToken, QueuedTaskRef, ReservedStepStream,
    ScannedQueueHead, StepStreamRequest, StepStreamReservation, StepTaskEvent, StepTaskHandle,
    StepTaskSendFailure, WorkerDispatch,
};
use futures::future::BoxFuture;

use crate::effects::method_request::MethodRequest;

/// One method attempt that needs a Worker slot.
#[derive(Debug, Clone)]
pub struct MethodStreamRequest {
    /// Partition of the Flow.
    pub partition: PartitionId,
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
}

/// The result of reserving a Worker slot.
pub enum MethodStreamReservation {
    /// A Worker slot is reserved until its task is sent or the reservation is dropped.
    Reserved(Box<dyn ReservedMethodStream>),
    /// No Worker took the attempt in memory; it goes to the durable task queue.
    Enqueue,
    /// The matching owner is closing; nothing was reserved.
    ServerClosing,
    /// Matching refused the request.
    Invalid {
        /// Why matching refused it.
        detail: String,
    },
}

/// Matching asks the partition owner to start a queued row on a reserved Worker slot.
pub struct MethodClaim {
    /// Identifies the Claim in its reply.
    pub claim_id: u64,
    /// The claimed row, possibly stale.
    pub task: QueuedTaskRef,
    /// The reserved Worker slot; dropping it returns the slot.
    pub stream: Box<dyn ReservedMethodStream>,
    /// Removals of the row's partition when matching delivered the Claim.
    pub partition_removals: u64,
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
    /// Whether the invocation's pinned durability is SYNC: the task goes out only after its
    /// start committed, so no other frame of the attempt may run.
    pub is_sync: bool,
}

/// One reserved Worker slot. Dropping it without sending returns the slot.
pub trait ReservedMethodStream: Send {
    /// Returns the Worker process that owns the slot.
    fn app_worker_id(&self) -> &str;

    /// Returns whether this node may hand out a task now.
    fn may_hand_out_tasks(&self) -> bool;

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

/// Hands method attempts to Workers, in memory or through the durable task queue's refs.
pub trait MethodDispatcher: Send + Sync + 'static {
    /// Reserves a Worker slot for one method attempt.
    fn reserve_stream(
        &self,
        request: MethodStreamRequest,
    ) -> BoxFuture<'_, MethodStreamReservation>;

    /// Hands matching the ref of a committed DispatchQueue row.
    fn queued(&self, task: QueuedTaskRef);

    /// Hands matching the queue heads a partition rebuild read.
    fn report_rebuilt_heads(&self, heads: Vec<QueuedTaskRef>);

    /// Starts a periodic Heads scan of `partition`.
    fn take_heads_token(&self, partition: PartitionId) -> HeadsScanToken;

    /// Merges a periodic Heads scan.
    ///
    /// Returns the number of refs matching had lost.
    fn report_scanned_heads(&self, token: HeadsScanToken, results: Vec<ScannedQueueHead>) -> usize;

    /// Waits for matching's next Claim.
    ///
    /// Returns `None` once matching closed.
    fn next_claim(&self) -> BoxFuture<'_, Option<MethodClaim>>;

    /// Answers a Claim.
    fn reply_to_claim(
        &self,
        partition: PartitionId,
        queue: &DispatchQueueName,
        claim_id: u64,
        reply: ClaimReply,
    );

    /// Returns how many times matching removed `partition`, for
    /// [`Self::is_partition_unremoved_since`].
    fn partition_removals(&self, partition: PartitionId) -> u64;

    /// Returns whether matching has not removed `partition` since a Claim delivered with
    /// `partition_removals`.
    fn is_partition_unremoved_since(&self, partition: PartitionId, partition_removals: u64)
    -> bool;

    /// Returns whether matching still waits for the reply to this Claim.
    fn is_claim_tracked(
        &self,
        partition: PartitionId,
        queue: &DispatchQueueName,
        claim_id: u64,
    ) -> bool;

    /// Reports the pending time of the first ordinary-priority row behind a priority-0 head.
    fn report_ordinary_backlog(
        &self,
        partition: PartitionId,
        queue: &DispatchQueueName,
        pending_since_micros: Option<i64>,
    );

    /// Returns whether every connected Worker of the Flow type declined the Step type.
    fn is_waiting_for_supporting_worker(
        &self,
        tenant_id: u64,
        flow_type: &str,
        step_type: &str,
    ) -> bool;

    /// Refreshes the task queue gauges that matching owns.
    fn refresh_task_queue_gauges(&self);
}

impl crate::flow_coordination::StepQueueStatus for WorkerDispatch {
    fn is_waiting_for_supporting_worker(
        &self,
        tenant_id: u64,
        flow_type: &str,
        step_type: &str,
    ) -> bool {
        WorkerDispatch::is_waiting_for_supporting_worker(self, tenant_id, flow_type, step_type)
    }
}

impl MethodDispatcher for WorkerDispatch {
    fn reserve_stream(
        &self,
        request: MethodStreamRequest,
    ) -> BoxFuture<'_, MethodStreamReservation> {
        Box::pin(async move {
            let reservation = self
                .reserve_step_stream(StepStreamRequest {
                    partition: request.partition,
                    tenant_id: request.tenant_id,
                    flow_id: request.flow_id,
                    flow_type: request.flow_type,
                    step_type: request.step_type,
                    sticky_app_worker_id: request.sticky_app_worker_id,
                })
                .await;
            match reservation {
                StepStreamReservation::Reserved(stream) => {
                    MethodStreamReservation::Reserved(Box::new(WorkerSlot { stream }))
                }
                StepStreamReservation::Enqueue => MethodStreamReservation::Enqueue,
                StepStreamReservation::ServerClosing => MethodStreamReservation::ServerClosing,
                StepStreamReservation::Invalid { detail } => {
                    MethodStreamReservation::Invalid { detail }
                }
            }
        })
    }

    fn queued(&self, task: QueuedTaskRef) {
        WorkerDispatch::queued(self, task);
    }

    fn report_rebuilt_heads(&self, heads: Vec<QueuedTaskRef>) {
        WorkerDispatch::report_rebuilt_heads(self, heads);
    }

    fn take_heads_token(&self, partition: PartitionId) -> HeadsScanToken {
        WorkerDispatch::take_heads_token(self, partition)
    }

    fn report_scanned_heads(&self, token: HeadsScanToken, results: Vec<ScannedQueueHead>) -> usize {
        WorkerDispatch::report_scanned_heads(self, token, results)
    }

    fn next_claim(&self) -> BoxFuture<'_, Option<MethodClaim>> {
        Box::pin(async move {
            let claim = WorkerDispatch::next_claim(self).await?;
            Some(MethodClaim {
                claim_id: claim.claim_id,
                task: claim.task,
                stream: Box::new(WorkerSlot {
                    stream: claim.stream,
                }),
                partition_removals: claim.partition_removals,
            })
        })
    }

    fn reply_to_claim(
        &self,
        partition: PartitionId,
        queue: &DispatchQueueName,
        claim_id: u64,
        reply: ClaimReply,
    ) {
        WorkerDispatch::reply_to_claim(self, partition, queue, claim_id, reply);
    }

    fn partition_removals(&self, partition: PartitionId) -> u64 {
        WorkerDispatch::partition_removals(self, partition)
    }

    fn is_partition_unremoved_since(
        &self,
        partition: PartitionId,
        partition_removals: u64,
    ) -> bool {
        WorkerDispatch::is_partition_unremoved_since(self, partition, partition_removals)
    }

    fn is_claim_tracked(
        &self,
        partition: PartitionId,
        queue: &DispatchQueueName,
        claim_id: u64,
    ) -> bool {
        WorkerDispatch::is_claim_tracked(self, partition, queue, claim_id)
    }

    fn report_ordinary_backlog(
        &self,
        partition: PartitionId,
        queue: &DispatchQueueName,
        pending_since_micros: Option<i64>,
    ) {
        WorkerDispatch::report_ordinary_backlog(self, partition, queue, pending_since_micros);
    }

    fn is_waiting_for_supporting_worker(
        &self,
        tenant_id: u64,
        flow_type: &str,
        step_type: &str,
    ) -> bool {
        WorkerDispatch::is_waiting_for_supporting_worker(self, tenant_id, flow_type, step_type)
    }

    fn refresh_task_queue_gauges(&self) {
        WorkerDispatch::refresh_task_queue_gauges(self);
    }
}

struct WorkerSlot {
    stream: ReservedStepStream,
}

impl ReservedMethodStream for WorkerSlot {
    fn app_worker_id(&self) -> &str {
        self.stream.app_worker_id()
    }

    fn may_hand_out_tasks(&self) -> bool {
        self.stream.may_hand_out_tasks()
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
