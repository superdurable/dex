// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The single dispatcher of one pending attempt: lock pre-check, Worker slot reservation, the
//! start, and the hand-out, again after every return of the attempt (plan section 3.1).

use std::sync::Arc;
use std::time::Instant;

use dexzd_keyspace::RunId;
use dexzd_protocol::dex::{self, PullRouting};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, StepAttemptFailedCommand, StepAttemptFailureKind, StepInvocationPhase,
    flow_command,
};
use tokio_util::sync::CancellationToken;

use crate::effects::dispatch::method_attempt::{
    MethodAttempt, StartedAttemptEnd, fence_task_id, pull_routing,
};
use crate::effects::dispatch::{MethodStreamRequest, MethodStreamReservation};
use crate::effects::flow_effect_operations::{
    FlowEffectOperationError, InvocationLockCheck, InvocationStartOutcome,
};
use crate::effects::method_request::{
    MethodAttemptKey, configured_method_timeout_seconds, pinned_durability,
};
use crate::effects::state_reads::{RunLocation, read_run_record, read_step_execution};
use crate::effects::{EffectContext, EffectError, RouterMessage};
use crate::flow_coordination::{CommitUrgency, FlowAddress};

/// Hand-out state that survives returns and lock waits of one attempt.
#[derive(Debug, Clone, Default)]
pub(crate) struct HandoutProgress {
    /// When the attempt first searched for a Worker; the dispatch wait counts from it.
    pub(crate) search_started_at: Option<Instant>,
    /// When the attempt's pending effect committed, for the schedule-to-start latency.
    pub(crate) scheduled_at: Option<Instant>,
}

/// How a hand-out task ended.
pub(crate) enum HandoutEnd {
    /// Nothing more to do: the attempt ended, is no longer pending, or the node stops.
    Released,
    /// A lock key is busy; start again after locks are released.
    LocksBusy(HandoutProgress),
}

/// The task that owns the dispatch of one pending attempt.
pub(crate) struct AttemptHandout {
    pub(crate) context: Arc<EffectContext>,
    pub(crate) flow: FlowAddress,
    pub(crate) key: MethodAttemptKey,
    pub(crate) progress: HandoutProgress,
}

/// What a hand-out needs from the pending attempt's committed or speculative rows.
struct PendingAttempt {
    tenant_id: u64,
    flow_type: String,
    step_type: String,
    sticky_app_worker_id: String,
    durability: dex::StepDurability,
    method_timeout_seconds: i32,
}

impl PendingAttempt {
    fn urgency(&self) -> CommitUrgency {
        match self.durability {
            dex::StepDurability::Async => CommitUrgency::Batched,
            _ => CommitUrgency::Prompt,
        }
    }
}

impl AttemptHandout {
    /// Dispatches the attempt until it ends or waits for locks.
    pub(crate) async fn run(mut self) -> HandoutEnd {
        let run = self.context.run_location(&self.flow, self.key.run_id);
        let mut backoff = self.context.retry_backoff();
        loop {
            let pending = match self.read_pending(run) {
                Ok(Some(pending)) => pending,
                Ok(None) => return HandoutEnd::Released,
                Err(error) => {
                    // A corrupt run record also waits: dropping the start would strand the call.
                    self.log_wait(&error);
                    if !backoff.wait(&self.context.shutdown).await {
                        return HandoutEnd::Released;
                    }
                    continue;
                }
            };
            match self.check_locks().await {
                Ok(InvocationLockCheck::Free) => {}
                Ok(InvocationLockCheck::Busy) => return HandoutEnd::LocksBusy(self.progress),
                Ok(InvocationLockCheck::Stale)
                | Err(FlowEffectOperationError::NodeStopping { .. }) => {
                    return HandoutEnd::Released;
                }
                Err(error @ FlowEffectOperationError::Unavailable { .. }) => {
                    self.log_wait(&EffectError::FlowEffectOperations(error));
                    if !backoff.wait(&self.context.shutdown).await {
                        return HandoutEnd::Released;
                    }
                    continue;
                }
            }
            let search_started_at = *self
                .progress
                .search_started_at
                .get_or_insert_with(Instant::now);
            let request = MethodStreamRequest {
                tenant_id: pending.tenant_id,
                flow_id: self.flow.flow_id.clone(),
                flow_type: pending.flow_type.clone(),
                step_type: pending.step_type.clone(),
                sticky_app_worker_id: (!pending.sticky_app_worker_id.is_empty())
                    .then(|| pending.sticky_app_worker_id.clone()),
                search_started_at,
            };
            // Biased for reproducible simulation; shutdown fires once.
            let reservation = tokio::select! {
                biased;
                () = self.context.shutdown.cancelled() => return HandoutEnd::Released,
                reservation = self.context.dispatcher.reserve_stream(request) => reservation,
            };
            let stream = match reservation {
                MethodStreamReservation::Reserved(stream) => stream,
                MethodStreamReservation::NoWorker { reason, detail } => {
                    let detail = format!("{}: {detail}", reason.as_str_name());
                    self.fail_before_start(&pending, detail).await;
                    return HandoutEnd::Released;
                }
                MethodStreamReservation::Invalid { detail } => {
                    self.fail_before_start(&pending, detail).await;
                    return HandoutEnd::Released;
                }
                // A planned stop writes nothing; the successor dispatches the attempt.
                MethodStreamReservation::ServerClosing => return HandoutEnd::Released,
            };
            let task_id = self
                .context
                .id_source
                .dispatch_task_id()
                .as_bytes()
                .to_vec();
            let routing = pull_routing(&pending.sticky_app_worker_id, stream.app_worker_id());
            let cancel = self.register_attempt(&task_id);
            let started = self.start(task_id.clone(), routing).await;
            let start_commit = match started {
                Ok(InvocationStartOutcome::Started(start_commit)) => start_commit,
                Ok(InvocationStartOutcome::LocksBusy) => {
                    self.finish_attempt(task_id);
                    return HandoutEnd::LocksBusy(self.progress);
                }
                Ok(InvocationStartOutcome::Stale)
                | Err(FlowEffectOperationError::NodeStopping { .. }) => {
                    self.finish_attempt(task_id);
                    return HandoutEnd::Released;
                }
                Err(error @ FlowEffectOperationError::Unavailable { .. }) => {
                    // The slot goes back at once; the attempt retries after a backoff.
                    drop(stream);
                    self.finish_attempt(task_id);
                    self.log_wait(&EffectError::FlowEffectOperations(error));
                    if !backoff.wait(&self.context.shutdown).await {
                        return HandoutEnd::Released;
                    }
                    continue;
                }
            };
            let attempt = MethodAttempt {
                context: Arc::clone(&self.context),
                flow: self.flow.clone(),
                run,
                key: self.key.clone(),
                task_id: task_id.clone(),
                durability: pending.durability,
                method_timeout_seconds: pending.method_timeout_seconds,
                scheduled_at: self.progress.scheduled_at.take(),
                cancel,
            };
            let end = attempt.run(stream, start_commit).await;
            self.finish_attempt(task_id);
            match end {
                StartedAttemptEnd::Ended => return HandoutEnd::Released,
                StartedAttemptEnd::Returned => backoff = self.context.retry_backoff(),
            }
        }
    }

    /// Reads the attempt from the speculative state.
    ///
    /// Returns `None` when it is no longer pending with this attempt in this run attempt.
    fn read_pending(&self, run: RunLocation) -> Result<Option<PendingAttempt>, EffectError> {
        let reader = self
            .context
            .flow_effect_operations
            .flow_state_reader(&self.flow, true)?;
        let Some(run_record) = read_run_record(reader.as_ref(), run)? else {
            return Ok(None);
        };
        if run_record.current_attempt != self.key.run_attempt {
            return Ok(None);
        }
        let Some(step_execution) =
            read_step_execution(reader.as_ref(), run, &self.key.step_execution_id)?
        else {
            return Ok(None);
        };
        let Some(invocation) = step_execution.invocation.as_ref() else {
            return Ok(None);
        };
        let is_pending = invocation.method == self.key.method
            && invocation.attempt == self.key.attempt
            && invocation.phase == StepInvocationPhase::Pending as i32;
        if !is_pending {
            return Ok(None);
        }
        Ok(Some(PendingAttempt {
            tenant_id: run_record.tenant_id,
            flow_type: run_record.flow_type,
            step_type: step_execution.step_type.clone(),
            sticky_app_worker_id: run_record.sticky_app_worker_id,
            durability: pinned_durability(invocation),
            method_timeout_seconds: configured_method_timeout_seconds(
                step_execution.options.as_ref(),
                invocation.method,
            ),
        }))
    }

    async fn check_locks(&self) -> Result<InvocationLockCheck, FlowEffectOperationError> {
        let invocation = self.key.invocation();
        self.context
            .flow_effect_operations
            .check_invocation_locks(&self.flow, self.key.run_id, &invocation, self.key.attempt)
            .await
    }

    async fn start(
        &self,
        task_id: Vec<u8>,
        routing: PullRouting,
    ) -> Result<InvocationStartOutcome, FlowEffectOperationError> {
        let invocation = self.key.invocation();
        self.context
            .flow_effect_operations
            .try_start_invocation(
                &self.flow,
                self.key.run_id,
                self.key.run_attempt,
                &invocation,
                self.key.attempt,
                task_id,
                routing,
            )
            .await
    }

    /// Registers the attempt's cancellation before its start is proposed, so a cancellation
    /// ordered after the start always finds it.
    fn register_attempt(&self, task_id: &[u8]) -> CancellationToken {
        let cancel = self.context.shutdown.child_token();
        self.context.report(RouterMessage::AttemptStarting {
            task_id: task_id.to_vec(),
            key: self.key.clone(),
            cancel: cancel.clone(),
        });
        cancel
    }

    fn finish_attempt(&self, task_id: Vec<u8>) {
        self.context.report(RouterMessage::AttemptFinished {
            task_id,
            key: self.key.clone(),
        });
    }

    /// Fails the pending attempt that no Worker took within the dispatch wait. This exists
    /// only until the durable task queue keeps such attempts waiting.
    async fn fail_before_start(&self, pending: &PendingAttempt, detail: String) {
        let command = self.flow_command(flow_command::Command::StepAttemptFailed(
            StepAttemptFailedCommand {
                invocation: Some(self.key.invocation()),
                attempt: self.key.attempt,
                kind: StepAttemptFailureKind::DispatchError as i32,
                server_detail: detail,
                pull_routing: Some(pull_routing(&pending.sticky_app_worker_id, "")),
                is_before_start: true,
                ..StepAttemptFailedCommand::default()
            },
        ));
        // A stale failure is rejected by apply; nothing else to do with the reply.
        let _reply = self
            .context
            .propose_until_committed(&self.flow, command, pending.urgency())
            .await;
    }

    fn flow_command(&self, command: flow_command::Command) -> FlowCommand {
        FlowCommand {
            flow_id: self.flow.flow_id.clone(),
            run_id: self.key.run_id.as_bytes().to_vec(),
            expected_run_attempt: self.key.run_attempt,
            tenant_id: self.flow.tenant_id.0,
            command: Some(command),
            ..FlowCommand::default()
        }
    }

    fn log_wait(&self, error: &EffectError) {
        if error.is_retryable() {
            tracing::debug!(%error, run_id = %self.key.run_id, "invocation start is waiting for the runtime");
        } else {
            tracing::warn!(%error, run_id = %self.key.run_id, "invocation start cannot read the run");
        }
    }
}

/// A started attempt that a restart or failover interrupted after its hand-out.
#[derive(Debug, Clone)]
pub(crate) struct InterruptedAttempt {
    pub(crate) key: MethodAttemptKey,
    pub(crate) task_id: Vec<u8>,
    pub(crate) durability: dex::StepDurability,
    pub(crate) pull_routing: Option<PullRouting>,
}

/// Fails an attempt the Worker may have run before the restart; it consumes a retry (plan D68).
pub(crate) async fn fail_interrupted_attempt(
    context: &EffectContext,
    flow: &FlowAddress,
    run_id: RunId,
    attempt: InterruptedAttempt,
) {
    let urgency = match attempt.durability {
        dex::StepDurability::Async => CommitUrgency::Batched,
        _ => CommitUrgency::Prompt,
    };
    let command = FlowCommand {
        flow_id: flow.flow_id.clone(),
        run_id: run_id.as_bytes().to_vec(),
        expected_run_attempt: attempt.key.run_attempt,
        tenant_id: flow.tenant_id.0,
        command: Some(flow_command::Command::StepAttemptFailed(
            StepAttemptFailedCommand {
                invocation: Some(attempt.key.invocation()),
                attempt: attempt.key.attempt,
                kind: StepAttemptFailureKind::StartedAttemptInterrupted as i32,
                server_detail: "a server restart or failover interrupted the attempt after a Worker received it"
                    .to_owned(),
                pull_routing: attempt.pull_routing,
                expected_task_id: fence_task_id(&attempt.task_id),
                ..StepAttemptFailedCommand::default()
            },
        )),
        ..FlowCommand::default()
    };
    // Apply ignores the failure when the attempt no longer runs with this task ID.
    let _reply = context
        .propose_until_committed(flow, command, urgency)
        .await;
}
