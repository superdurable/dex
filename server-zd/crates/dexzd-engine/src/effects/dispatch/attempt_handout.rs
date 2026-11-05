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

use dexzd_apply::dispatch_queue_entry;
use dexzd_keyspace::dispatch_queue_keys::{DispatchQueueEntry, dispatch_queue_key};
use dexzd_keyspace::{ColumnFamily, RunId};
use dexzd_protocol::dex::{self, PullRouting};
use dexzd_protocol::dexzd::v1::{
    CommandReply, FlowCommand, StepAttemptFailedCommand, StepAttemptFailureKind,
    StepAttemptQueuedCommand, StepInvocationPhase, command_reply, flow_command,
};
use dexzd_worker_dispatch::{ClaimResult, QueuedTaskRef};
use tokio_util::sync::CancellationToken;

use crate::effects::dispatch::attempt_entries::EntryHolder;
use crate::effects::dispatch::method_attempt::{
    MethodAttempt, StartedAttemptEnd, fence_task_id, pull_routing,
};
use crate::effects::dispatch::{
    MethodStreamRequest, MethodStreamReservation, ReservedMethodStream,
};
use crate::effects::flow_effect_operations::{
    FlowEffectOperationError, InvocationLockCheck, InvocationStartOutcome,
};
use crate::effects::method_request::{
    MethodAttemptKey, configured_method_timeout_seconds, pinned_durability,
};
use crate::effects::state_reads::{RunLocation, read_run_record, read_step_execution};
use crate::effects::task_queue::{queued_task_ref, reply_to_claim};
use crate::effects::{EffectContext, EffectError, RetryBackoff, RouterMessage};
use crate::flow_coordination::{CommitUrgency, FlowAddress};

/// Hand-out state that survives returns and lock waits of one attempt.
#[derive(Debug, Clone, Default)]
pub(crate) struct HandoutProgress {
    /// When the attempt's pending effect committed, for the schedule-to-start latency.
    pub(crate) scheduled_at: Option<Instant>,
}

/// How a hand-out task ended.
pub(crate) enum HandoutEnd {
    /// Nothing more to do: the attempt ended, is no longer pending, is queued, or the node stops.
    Released,
    /// A lock key is busy; start again after locks are released.
    LocksBusy(HandoutProgress),
}

/// The task that owns the dispatch of one pending attempt.
pub(crate) struct AttemptHandout {
    pub(crate) context: Arc<EffectContext>,
    pub(crate) flow: FlowAddress,
    pub(crate) key: MethodAttemptKey,
    /// Generation of the attempt entry this task holds.
    pub(crate) generation: u64,
    pub(crate) progress: HandoutProgress,
}

/// A queued row that matching claimed for a reserved stream; the Claim awaits its reply.
pub(crate) struct ClaimedRow {
    pub(crate) claim_id: u64,
    pub(crate) task: QueuedTaskRef,
    pub(crate) stream: Box<dyn ReservedMethodStream>,
}

/// What a hand-out needs from the pending attempt's committed or speculative rows.
struct PendingAttempt {
    tenant_id: u64,
    flow_type: String,
    step_type: String,
    sticky_app_worker_id: String,
    durability: dex::StepDurability,
    method_timeout_seconds: i32,
    /// The attempt's DispatchQueue row, whether or not it exists.
    queue_entry: DispatchQueueEntry,
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
    /// Dispatches the attempt until it ends, is queued, or waits for locks.
    pub(crate) async fn run(self) -> HandoutEnd {
        self.run_from(None).await
    }

    /// Starts a claimed row on its reserved stream, answers the Claim, then dispatches the
    /// attempt like [`Self::run`] after any return.
    pub(crate) async fn run_claimed(self, claimed: ClaimedRow) -> HandoutEnd {
        self.run_from(Some(claimed)).await
    }

    /// Answers "retry later" for an unchecked claimed row and re-sends it after a backoff.
    pub(crate) async fn retry_claim_later(self, claim_id: u64, task: QueuedTaskRef) -> HandoutEnd {
        let mut backoff = self.context.retry_backoff();
        if !self.hold_for_retry(claim_id, &task, &mut backoff).await {
            return HandoutEnd::Released;
        }
        self.run().await
    }

    async fn run_from(mut self, mut claimed: Option<ClaimedRow>) -> HandoutEnd {
        let run = self.context.run_location(&self.flow, self.key.run_id);
        let mut backoff = self.context.retry_backoff();
        loop {
            let pending = match self.read_pending(run) {
                Ok(Some(pending)) => pending,
                Ok(None) => {
                    if let Some(row) = claimed.take() {
                        drop(row.stream);
                        reply_to_claim(&self.context, &row.task, row.claim_id, ClaimResult::Stale);
                    }
                    return HandoutEnd::Released;
                }
                Err(error) => {
                    // A corrupt run record also waits: dropping the start would strand the call.
                    self.log_wait(&error);
                    let is_waiting = match claimed.take() {
                        Some(row) => {
                            drop(row.stream);
                            self.hold_for_retry(row.claim_id, &row.task, &mut backoff)
                                .await
                        }
                        None => backoff.wait(&self.context.shutdown).await,
                    };
                    if !is_waiting {
                        return HandoutEnd::Released;
                    }
                    continue;
                }
            };
            let (stream, claim) = match claimed.take() {
                Some(row) => (row.stream, Some((row.claim_id, row.task))),
                None => match self.reserve(&pending, &mut backoff).await {
                    Reservation::Stream(stream) => (stream, None),
                    Reservation::Again => continue,
                    Reservation::End(end) => return end,
                },
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
                Ok(InvocationStartOutcome::Started(start_commit)) => {
                    self.answer_claim(claim.as_ref(), ClaimResult::Started);
                    start_commit
                }
                Ok(InvocationStartOutcome::LocksBusy) => {
                    drop(stream);
                    self.finish_attempt(task_id);
                    // The row stays held until the lock release sends it back (plan D72).
                    self.answer_claim(claim.as_ref(), ClaimResult::LockBusy);
                    return HandoutEnd::LocksBusy(self.progress);
                }
                Ok(InvocationStartOutcome::Stale)
                | Err(FlowEffectOperationError::NodeStopping { .. }) => {
                    drop(stream);
                    self.finish_attempt(task_id);
                    self.answer_claim(claim.as_ref(), ClaimResult::Stale);
                    return HandoutEnd::Released;
                }
                Err(error @ FlowEffectOperationError::Unavailable { .. }) => {
                    // The slot goes back at once; the attempt retries after a backoff.
                    drop(stream);
                    self.finish_attempt(task_id);
                    self.log_wait(&EffectError::FlowEffectOperations(error));
                    let is_waiting = match claim {
                        Some((claim_id, task)) => {
                            self.hold_for_retry(claim_id, &task, &mut backoff).await
                        }
                        None => backoff.wait(&self.context.shutdown).await,
                    };
                    if !is_waiting {
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

    /// Checks locks, then sends a queued row back or reserves a slot (plan section 3.1).
    async fn reserve(
        &mut self,
        pending: &PendingAttempt,
        backoff: &mut RetryBackoff,
    ) -> Reservation {
        match self.check_locks().await {
            Ok(InvocationLockCheck::Free) => {}
            Ok(InvocationLockCheck::Busy) => {
                return Reservation::End(HandoutEnd::LocksBusy(self.progress.clone()));
            }
            Ok(InvocationLockCheck::Stale) | Err(FlowEffectOperationError::NodeStopping { .. }) => {
                return Reservation::End(HandoutEnd::Released);
            }
            Err(error @ FlowEffectOperationError::Unavailable { .. }) => {
                self.log_wait(&EffectError::FlowEffectOperations(error));
                return self.wait_again(backoff).await;
            }
        }
        // The speculative view counts an uncommitted start as a deletion (plan section 3.2).
        match self.is_in_dispatch_queue(pending, true) {
            Ok(true) => return self.send_queued_row(pending, backoff).await,
            Ok(false) => {}
            Err(error) => {
                self.log_wait(&error);
                return self.wait_again(backoff).await;
            }
        }
        let request = MethodStreamRequest {
            partition: pending.queue_entry.partition,
            tenant_id: pending.tenant_id,
            flow_id: self.flow.flow_id.clone(),
            flow_type: pending.flow_type.clone(),
            step_type: pending.step_type.clone(),
            sticky_app_worker_id: (!pending.sticky_app_worker_id.is_empty())
                .then(|| pending.sticky_app_worker_id.clone()),
        };
        // Biased for reproducible simulation; shutdown fires once.
        let reservation = tokio::select! {
            biased;
            () = self.context.shutdown.cancelled() => return Reservation::End(HandoutEnd::Released),
            reservation = self.context.dispatcher.reserve_stream(request) => reservation,
        };
        match reservation {
            MethodStreamReservation::Reserved(stream) => Reservation::Stream(stream),
            MethodStreamReservation::Enqueue => self.enqueue(pending).await,
            MethodStreamReservation::Invalid { detail } => {
                self.fail_before_start(pending, detail).await;
                Reservation::End(HandoutEnd::Released)
            }
            // A planned stop writes nothing; the successor dispatches the attempt.
            MethodStreamReservation::ServerClosing => Reservation::End(HandoutEnd::Released),
        }
    }

    async fn wait_again(&self, backoff: &mut RetryBackoff) -> Reservation {
        if backoff.wait(&self.context.shutdown).await {
            Reservation::Again
        } else {
            Reservation::End(HandoutEnd::Released)
        }
    }

    /// Writes the attempt's DispatchQueue row and sends its ref once committed.
    async fn enqueue(&self, pending: &PendingAttempt) -> Reservation {
        let command = self.flow_command(flow_command::Command::StepAttemptQueued(
            StepAttemptQueuedCommand {
                invocation: Some(self.key.invocation()),
                attempt: self.key.attempt,
                pending_since_micros: pending.queue_entry.position.pending_since_micros,
            },
        ));
        let reply = self
            .context
            .propose_until_committed(&self.flow, command, CommitUrgency::Batched)
            .await;
        match reply {
            Some(reply) if !is_error_reply(&reply) => self.send_queued(pending),
            // The attempt is no longer pending, or the node stops.
            _ => Reservation::End(HandoutEnd::Released),
        }
    }

    /// Sends the ref of a row already queued, once the row is committed.
    async fn send_queued_row(
        &self,
        pending: &PendingAttempt,
        backoff: &mut RetryBackoff,
    ) -> Reservation {
        match self.is_in_dispatch_queue(pending, false) {
            Ok(true) => return self.send_queued(pending),
            Ok(false) => {}
            Err(error) => {
                self.log_wait(&error);
                return self.wait_again(backoff).await;
            }
        }
        match self
            .context
            .flow_effect_operations
            .sync_barrier(&self.flow)
            .await
        {
            Ok(()) => Reservation::Again,
            Err(FlowEffectOperationError::NodeStopping { .. }) => {
                Reservation::End(HandoutEnd::Released)
            }
            Err(error) => {
                self.log_wait(&EffectError::FlowEffectOperations(error));
                self.wait_again(backoff).await
            }
        }
    }

    /// Marks the entry sent, then hands matching the committed row's ref.
    fn send_queued(&self, pending: &PendingAttempt) -> Reservation {
        let entry = pending.queue_entry.clone();
        self.context.task_queue_memory().note_written(&entry);
        // Marked before sending, so a Claim woken by the Queued takes the entry over.
        self.context.attempt_entries().set_holder(
            &self.key,
            self.generation,
            EntryHolder::QueuedSent,
        );
        let task = queued_task_ref(entry, self.key.run_attempt, self.flow.flow_id.clone());
        self.context.dispatcher.queued(task);
        Reservation::End(HandoutEnd::Released)
    }

    /// Returns whether the attempt's DispatchQueue row exists in the speculative or committed
    /// state.
    fn is_in_dispatch_queue(
        &self,
        pending: &PendingAttempt,
        should_include_speculative: bool,
    ) -> Result<bool, EffectError> {
        let reader = if should_include_speculative {
            self.context
                .flow_effect_operations
                .flow_state_reader(&self.flow, true)?
        } else {
            self.context
                .flow_effect_operations
                .committed_state_reader()?
        };
        let key = dispatch_queue_key(&pending.queue_entry);
        Ok(reader.get(ColumnFamily::PartitionIndex, &key)?.is_some())
    }

    /// Answers a Claim, if this task holds one, with the queue's next ref.
    fn answer_claim(&self, claim: Option<&(u64, QueuedTaskRef)>, result: ClaimResult) {
        if let Some((claim_id, task)) = claim {
            reply_to_claim(&self.context, task, *claim_id, result);
        }
    }

    /// Answers a Claim with "retry later" and holds the row through one backoff.
    ///
    /// Returns false when the node stopped first.
    async fn hold_for_retry(
        &self,
        claim_id: u64,
        task: &QueuedTaskRef,
        backoff: &mut RetryBackoff,
    ) -> bool {
        self.context.attempt_entries().set_holder(
            &self.key,
            self.generation,
            EntryHolder::RetryingClaim,
        );
        reply_to_claim(&self.context, task, claim_id, ClaimResult::RetryLater);
        let is_waiting = backoff.wait(&self.context.shutdown).await;
        self.context.attempt_entries().set_holder(
            &self.key,
            self.generation,
            EntryHolder::Dispatching,
        );
        is_waiting
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
        let partition = self
            .context
            .hasher
            .partition_of_flow(self.flow.tenant_id, &self.flow.flow_id);
        let queue_entry = dispatch_queue_entry(
            partition,
            &run_record,
            run.flow_hash,
            run.run_id,
            &step_execution,
        )
        .expect("a pending Step has an invocation");
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
            queue_entry,
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

    /// Fails the pending attempt that matching refused as invalid, such as an overlong Flow
    /// type.
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

/// The outcome of looking for a Worker slot without a claimed stream.
enum Reservation {
    Stream(Box<dyn ReservedMethodStream>),
    /// Check the attempt again from the start.
    Again,
    End(HandoutEnd),
}

fn is_error_reply(reply: &CommandReply) -> bool {
    matches!(reply.reply, Some(command_reply::Reply::Error(_)))
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
    if attempt.task_id.is_empty() {
        // Every binary records a task ID on its starts, so this record is corrupt.
        tracing::error!(
            run_id = %run_id,
            step_execution_id = %attempt.key.step_execution_id,
            method = ?attempt.key.method,
            attempt = attempt.key.attempt,
            "a running attempt has no task ID, so it is not failed as interrupted"
        );
        return;
    }
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
