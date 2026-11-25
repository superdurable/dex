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
use dexzd_protocol::dex::{
    PollWorkerTaskFrame, invoke_execute_method_output, invoke_wait_for_method_output,
    poll_worker_task_frame,
};
use dexzd_protocol::dexzd::v1::StepMethod;
use dexzd_worker_dispatch::{
    ClaimReply, DispatchOutcome, HeadsScanToken, QueuedTaskRef, ScannedQueueHead, StepTaskEvent,
    StepTaskSendFailure,
};
use futures::future::BoxFuture;

use super::ResultLossDecisions;

/// Wraps Worker matching so that a dropped Step method result arrives as a Worker disconnect.
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
        let dropped_attempt = DroppedAttempt::decide(&task.request, self.decisions);
        let session = self.inner.send_task(task)?;
        Ok(match dropped_attempt {
            Some(dropped_attempt) => Box::new(ResultLossSession {
                inner: session,
                dropped_attempt,
            }),
            None => session,
        })
    }
}

/// The attempt whose result is dropped, kept for the drop's log line and disconnect detail.
struct DroppedAttempt {
    flow_id: String,
    step_execution_id: String,
    method: StepMethod,
    attempt: i32,
    detail: String,
}

impl DroppedAttempt {
    /// Returns the attempt of `request` when its result is dropped.
    ///
    /// The identity leaves out the server-minted run ID, so every new run drops the same attempts.
    fn decide(request: &MethodRequest, decisions: ResultLossDecisions) -> Option<Self> {
        let (method, method_name, context) = match request {
            MethodRequest::WaitFor(request) => {
                (StepMethod::WaitFor, "waitFor", request.context.as_ref())
            }
            MethodRequest::Execute(request) => {
                (StepMethod::Execute, "execute", request.context.as_ref())
            }
        };
        let context =
            context.expect("engine-built Step method requests carry an invocation context");
        let is_dropped = decisions.is_dropped(&[
            b"step",
            context.flow_id.as_bytes(),
            context.step_execution_id.as_bytes(),
            method_name.as_bytes(),
            &context.run_attempt.to_le_bytes(),
            &context.attempt.to_le_bytes(),
        ]);
        is_dropped.then(|| Self {
            flow_id: context.flow_id.clone(),
            step_execution_id: context.step_execution_id.clone(),
            method,
            attempt: context.attempt,
            detail: decisions.dropped_result_detail("--inject-step-result-loss"),
        })
    }

    /// Returns true when `frame` carries this attempt's method result; other frames keep their
    /// engine validation.
    fn is_method_result(&self, frame: &PollWorkerTaskFrame) -> bool {
        match (self.method, frame.output.as_ref()) {
            (StepMethod::WaitFor, Some(poll_worker_task_frame::Output::WaitFor(output))) => {
                matches!(
                    output.output,
                    Some(invoke_wait_for_method_output::Output::Result(_))
                )
            }
            (StepMethod::Execute, Some(poll_worker_task_frame::Output::Execute(output))) => {
                matches!(
                    output.output,
                    Some(invoke_execute_method_output::Output::Result(_))
                )
            }
            _ => false,
        }
    }
}

/// A sent task whose final Worker result becomes a Worker disconnect.
struct ResultLossSession {
    inner: Box<dyn MethodDispatchSession>,
    dropped_attempt: DroppedAttempt,
}

impl MethodDispatchSession for ResultLossSession {
    fn next_event(&mut self) -> BoxFuture<'_, Option<StepTaskEvent>> {
        Box::pin(async move {
            let event = self.inner.next_event().await?;
            let dropped = &self.dropped_attempt;
            let StepTaskEvent::Completed(DispatchOutcome::WorkerResult { frame, .. }) = &event
            else {
                return Some(event);
            };
            if !dropped.is_method_result(frame) {
                return Some(event);
            }
            tracing::info!(
                flow_id = %dropped.flow_id,
                step_execution_id = %dropped.step_execution_id,
                method = dropped.method.as_str_name(),
                attempt = dropped.attempt,
                "development fault injection dropped a Step method result"
            );
            Some(StepTaskEvent::Disconnected {
                detail: dropped.detail.clone(),
            })
        })
    }

    fn cancel(&self) {
        self.inner.cancel();
    }
}

#[cfg(test)]
mod tests {
    use dexzd_protocol::dex::{
        InvokeExecuteMethodOutput, InvokeExecuteMethodResponse, InvokeWaitForMethodOutput,
        InvokeWaitForMethodResponse, InvokeWorkerRpcResponse, PollWorkerTaskFrame,
        StepMethodHeartbeat, invoke_execute_method_output, invoke_wait_for_method_output,
        poll_worker_task_frame,
    };
    use dexzd_protocol::dexzd::v1::StepMethod;

    use super::DroppedAttempt;

    fn dropped_attempt(method: StepMethod) -> DroppedAttempt {
        DroppedAttempt {
            flow_id: "flow".to_owned(),
            step_execution_id: "S1-1".to_owned(),
            method,
            attempt: 1,
            detail: String::new(),
        }
    }

    fn frame(output: Option<poll_worker_task_frame::Output>) -> PollWorkerTaskFrame {
        PollWorkerTaskFrame {
            output,
            ..PollWorkerTaskFrame::default()
        }
    }

    #[test]
    fn only_a_result_of_the_dispatched_method_is_dropped() {
        let wait_for_result = frame(Some(poll_worker_task_frame::Output::WaitFor(
            InvokeWaitForMethodOutput {
                output: Some(invoke_wait_for_method_output::Output::Result(
                    InvokeWaitForMethodResponse::default(),
                )),
            },
        )));
        let execute_result = frame(Some(poll_worker_task_frame::Output::Execute(
            InvokeExecuteMethodOutput {
                output: Some(invoke_execute_method_output::Output::Result(
                    InvokeExecuteMethodResponse::default(),
                )),
            },
        )));
        let execute_heartbeat = frame(Some(poll_worker_task_frame::Output::Execute(
            InvokeExecuteMethodOutput {
                output: Some(invoke_execute_method_output::Output::Heartbeat(
                    StepMethodHeartbeat::default(),
                )),
            },
        )));
        let execute_without_output = frame(Some(poll_worker_task_frame::Output::Execute(
            InvokeExecuteMethodOutput { output: None },
        )));
        let rpc_result = frame(Some(poll_worker_task_frame::Output::Rpc(
            InvokeWorkerRpcResponse::default(),
        )));
        let wait_for = dropped_attempt(StepMethod::WaitFor);
        let execute = dropped_attempt(StepMethod::Execute);

        assert!(wait_for.is_method_result(&wait_for_result));
        assert!(execute.is_method_result(&execute_result));
        for (attempt, invalid_frame) in [
            (&wait_for, &execute_result),
            (&execute, &wait_for_result),
            (&execute, &execute_heartbeat),
            (&execute, &execute_without_output),
            (&execute, &rpc_result),
            (&execute, &frame(None)),
        ] {
            assert!(
                !attempt.is_method_result(invalid_frame),
                "{invalid_frame:?} must reach engine validation unchanged"
            );
        }
    }
}
