// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use dexzd_engine::{
    MethodClaim, MethodDispatchSession, MethodDispatcher, MethodRequest, MethodStreamRequest,
    MethodStreamReservation, MethodTask, ReservedMethodStream,
};
use dexzd_keyspace::PartitionId;
use dexzd_keyspace::dispatch_queue_keys::DispatchQueueName;
use dexzd_worker_dispatch::{
    ClaimReply, HeadsScanToken, QueuedTaskRef, ScannedQueueHead, StepTaskEvent, StepTaskSendFailure,
};
use futures::future::BoxFuture;

use super::ResultLossDecisions;

/// Wraps Worker matching so that the engine fails a dropped, valid Step result as a disconnect.
pub(super) struct ResultLossMethodDispatcher {
    inner: Arc<dyn MethodDispatcher>,
    decisions: ResultLossDecisions,
}

impl ResultLossMethodDispatcher {
    pub(super) fn new(inner: Arc<dyn MethodDispatcher>, decisions: ResultLossDecisions) -> Self {
        Self { inner, decisions }
    }

    fn wrap_stream(&self, inner: Box<dyn ReservedMethodStream>) -> Box<dyn ReservedMethodStream> {
        Box::new(ResultLossStream {
            inner,
            decisions: self.decisions,
        })
    }
}

impl MethodDispatcher for ResultLossMethodDispatcher {
    fn reserve_stream(
        &self,
        request: MethodStreamRequest,
    ) -> BoxFuture<'_, MethodStreamReservation> {
        Box::pin(async move {
            match self.inner.reserve_stream(request).await {
                MethodStreamReservation::Reserved(stream) => {
                    MethodStreamReservation::Reserved(self.wrap_stream(stream))
                }
                reservation => reservation,
            }
        })
    }

    fn queued(&self, task: QueuedTaskRef) {
        self.inner.queued(task);
    }

    fn report_rebuilt_heads(&self, heads: Vec<QueuedTaskRef>) {
        self.inner.report_rebuilt_heads(heads);
    }

    fn take_heads_token(&self, partition: PartitionId) -> HeadsScanToken {
        self.inner.take_heads_token(partition)
    }

    fn report_scanned_heads(&self, token: HeadsScanToken, results: Vec<ScannedQueueHead>) -> usize {
        self.inner.report_scanned_heads(token, results)
    }

    fn next_claim(&self) -> BoxFuture<'_, Option<MethodClaim>> {
        Box::pin(async move {
            let claim = self.inner.next_claim().await?;
            // Queued attempts reach Workers through Claims, so queueing never changes a decision.
            Some(MethodClaim {
                stream: self.wrap_stream(claim.stream),
                ..claim
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
        self.inner.reply_to_claim(partition, queue, claim_id, reply);
    }

    fn partition_removals(&self, partition: PartitionId) -> u64 {
        self.inner.partition_removals(partition)
    }

    fn is_partition_unremoved_since(
        &self,
        partition: PartitionId,
        partition_removals: u64,
    ) -> bool {
        self.inner
            .is_partition_unremoved_since(partition, partition_removals)
    }

    fn is_claim_tracked(
        &self,
        partition: PartitionId,
        queue: &DispatchQueueName,
        claim_id: u64,
    ) -> bool {
        self.inner.is_claim_tracked(partition, queue, claim_id)
    }

    fn report_ordinary_backlog(
        &self,
        partition: PartitionId,
        queue: &DispatchQueueName,
        pending_since_micros: Option<i64>,
    ) {
        self.inner
            .report_ordinary_backlog(partition, queue, pending_since_micros);
    }

    fn is_waiting_for_supporting_worker(
        &self,
        tenant_id: u64,
        task_queue: &str,
        flow_type: &str,
        step_type: &str,
    ) -> bool {
        self.inner
            .is_waiting_for_supporting_worker(tenant_id, task_queue, flow_type, step_type)
    }

    fn refresh_task_queue_gauges(&self) {
        self.inner.refresh_task_queue_gauges();
    }
}

/// A reserved Worker slot that decides, when its task goes out, whether to drop the result.
struct ResultLossStream {
    inner: Box<dyn ReservedMethodStream>,
    decisions: ResultLossDecisions,
}

impl ReservedMethodStream for ResultLossStream {
    fn app_worker_id(&self) -> &str {
        self.inner.app_worker_id()
    }

    fn may_hand_out_tasks(&self) -> bool {
        self.inner.may_hand_out_tasks()
    }

    fn send_task(
        self: Box<Self>,
        task: MethodTask,
    ) -> Result<Box<dyn MethodDispatchSession>, StepTaskSendFailure> {
        let dropped_result_detail = dropped_result_detail(&task.request, self.decisions);
        let session = self.inner.send_task(task)?;
        Ok(match dropped_result_detail {
            Some(detail) => Box::new(ResultLossSession {
                inner: session,
                detail,
            }),
            None => session,
        })
    }
}

/// Returns the disconnect detail when the result of `request` is dropped.
///
/// The identity leaves out the server-minted run ID, so every new run drops the same attempts.
fn dropped_result_detail(
    request: &MethodRequest,
    decisions: ResultLossDecisions,
) -> Option<String> {
    let (method_name, context) = match request {
        MethodRequest::WaitFor(request) => ("waitFor", request.context.as_ref()),
        MethodRequest::Execute(request) => ("execute", request.context.as_ref()),
    };
    let context = context.expect("engine-built Step method requests carry an invocation context");
    let is_dropped = decisions.is_dropped(&[
        b"step",
        context.flow_id.as_bytes(),
        context.step_execution_id.as_bytes(),
        method_name.as_bytes(),
        &context.run_attempt.to_le_bytes(),
        &context.attempt.to_le_bytes(),
    ]);
    is_dropped.then(|| decisions.dropped_result_detail("--inject-step-result-loss"))
}

/// A sent task whose valid result the engine fails as a Worker disconnect.
struct ResultLossSession {
    inner: Box<dyn MethodDispatchSession>,
    detail: String,
}

impl MethodDispatchSession for ResultLossSession {
    fn next_event(&mut self) -> BoxFuture<'_, Option<StepTaskEvent>> {
        self.inner.next_event()
    }

    fn cancel(&self) {
        self.inner.cancel();
    }

    fn injected_result_loss(&self) -> Option<&str> {
        Some(&self.detail)
    }
}
