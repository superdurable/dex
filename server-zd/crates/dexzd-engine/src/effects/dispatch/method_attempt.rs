// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! One started method attempt: reads its snapshot, builds its request, sends it on its reserved
//! Worker slot, follows Worker frames and the owner-memory method and heartbeat timeouts, and
//! proposes its result, failure, or return.

use std::collections::BTreeMap;
use std::future::Future;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use dexzd_apply::FlowStateReader;
use dexzd_keyspace::row_keys::hard_delete_current_key;
use dexzd_keyspace::{ColumnFamily, TenantId};
use dexzd_protocol::dex::{
    self, PollWorkerTaskFrame, PullRouting, PullRoutingSource, StepStreamWrite, Value,
};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, HardDeleteObjectRecord, StepAttemptFailedCommand, StepAttemptFailureKind,
    StepAttemptHeartbeatCommand, StepAttemptReturnReason, StepAttemptReturnedCommand, flow_command,
    step_attempt_heartbeat_command,
};
use dexzd_stream_store::WriteStreamInput;
use dexzd_worker_dispatch::{StepTaskEvent, StepTaskSendFailure};
use tokio::time::{Instant, sleep_until};
use tokio_util::sync::CancellationToken;

use super::attempt_checkpoints::{CheckpointTracker, heartbeat_change, into_step_stream_write};
use super::attempt_deadlines::{AttemptEnd, AttemptTimeouts, MethodDeadline, task_event_end};
use crate::blocking_work::BlockingWorkFailure;
use crate::effects::dispatch::attempt_failure::AttemptFailure;
use crate::effects::dispatch::worker_result::{WorkerResultCheck, check_worker_result};
use crate::effects::dispatch::{MethodDispatchSession, MethodTask, ReservedMethodStream};
use crate::effects::hard_delete_objects::{HardDeleteObjectOwner, HardDeleteUploads};
use crate::effects::method_request::{
    MethodAttemptKey, MethodAttemptState, MethodRequest, RequestValueHydration,
    build_method_request, heartbeat_checkpoint_value, read_method_attempt_state,
};
use crate::effects::state_reads::{RunLocation, decode_row};
use crate::effects::{EffectContext, EffectError, FlowEffectOperationError, StartCommit};
use crate::flow_coordination::{CommitUrgency, FlowAddress};

/// A started method attempt's inputs.
pub(crate) struct MethodAttempt {
    pub(crate) context: Arc<EffectContext>,
    pub(crate) flow: FlowAddress,
    pub(crate) run: RunLocation,
    pub(crate) key: MethodAttemptKey,
    /// Task ID recorded by the attempt's `StepAttemptStarted`; frames, fences, and results use it.
    pub(crate) task_id: Vec<u8>,
    /// Durability pinned on the invocation; it decides commit urgencies and the commit wait.
    pub(crate) durability: dex::StepDurability,
    /// The method's configured timeout in seconds; 0 means the default.
    pub(crate) method_timeout_seconds: i32,
    pub(crate) scheduled_at: Option<std::time::Instant>,
    /// Canceled by `InvocationCanceled` or node shutdown.
    pub(crate) cancel: CancellationToken,
}

/// How a started attempt ended for the task that handed it out.
pub(crate) enum StartedAttemptEnd {
    /// The attempt ended, or this node can no longer act on it.
    Ended,
    /// The attempt is pending again with the same number; hand it out again.
    Returned,
}

/// Outcome of preparing the Worker request.
enum PreparedRequest {
    Ready(Box<MethodAttemptState>, Box<MethodRequest>),
    Canceled,
    /// The Worker never got the attempt, with the run's sticky Worker for the failure's routing.
    NotRun(AttemptFailure, String),
    /// The attempt no longer runs with this task ID.
    Gone,
}

/// Outcome of work raced against the method deadline and cancellation.
enum BeforeDeadline<T> {
    Done(T),
    Canceled,
    TimedOut,
}

impl MethodAttempt {
    /// Hands the started attempt to its reserved Worker slot and follows it to its end. A
    /// server-side error fails the attempt so that it retries; otherwise the invocation would
    /// stay running until a restart.
    pub(crate) async fn run(
        self,
        stream: Box<dyn ReservedMethodStream>,
        start_commit: StartCommit,
    ) -> StartedAttemptEnd {
        let error = match self.run_attempt(stream, start_commit).await {
            Ok(end) => return end,
            Err(error) => error,
        };
        if matches!(error, EffectError::Stopped) || self.cancel.is_cancelled() {
            return StartedAttemptEnd::Ended;
        }
        tracing::warn!(
            %error,
            run_id = %self.key.run_id,
            step_execution_id = %self.key.step_execution_id,
            attempt = self.key.attempt,
            "method attempt failed on the server"
        );
        let failure = AttemptFailure::server(
            StepAttemptFailureKind::DispatchError,
            format!("server error: {error}"),
        );
        self.propose_failure(failure, "", self.urgency()).await;
        StartedAttemptEnd::Ended
    }

    /// Returns the task ID that fences every command this attempt proposes.
    fn fence_task_id(&self) -> Vec<u8> {
        fence_task_id(&self.task_id)
    }

    fn urgency(&self) -> CommitUrgency {
        match self.durability {
            dex::StepDurability::Async => CommitUrgency::Batched,
            _ => CommitUrgency::Prompt,
        }
    }

    fn is_sync(&self) -> bool {
        self.durability != dex::StepDurability::Async
    }

    async fn run_attempt(
        &self,
        stream: Box<dyn ReservedMethodStream>,
        start_commit: StartCommit,
    ) -> Result<StartedAttemptEnd, EffectError> {
        // Building the request after the start has its own budget of one method timeout.
        let build_budget = MethodDeadline::start(self.method_timeout_seconds);
        let urgency = self.urgency();
        let prepared = if self.is_sync() {
            // A SYNC attempt reaches its Worker only after its start commits (plan D51).
            let (committed, prepared) =
                tokio::join!(start_commit, self.prepare_request(&build_budget));
            if let Err(error) = committed {
                tracing::debug!(%error, run_id = %self.key.run_id, "the start did not commit; the frame stays unsent");
                return Ok(StartedAttemptEnd::Ended);
            }
            prepared?
        } else {
            drop(start_commit);
            self.prepare_request(&build_budget).await?
        };
        let (state, request) = match prepared {
            PreparedRequest::Ready(state, request) => (state, request),
            PreparedRequest::Gone | PreparedRequest::Canceled => {
                return Ok(StartedAttemptEnd::Ended);
            }
            PreparedRequest::NotRun(failure, sticky_app_worker_id) => {
                // The slot goes back before the failure, which may take a while to commit.
                drop(stream);
                self.propose_failure(failure, &sticky_app_worker_id, urgency)
                    .await;
                return Ok(StartedAttemptEnd::Ended);
            }
        };
        // Cancellations ordered after the start reach the speculative state before the send.
        if self.cancel.is_cancelled() || (self.is_sync() && !self.is_still_running().await?) {
            return Ok(StartedAttemptEnd::Ended);
        }
        let method_deadline = MethodDeadline::start(self.method_timeout_seconds);
        let wall_deadline =
            unix_micros_to_system_time(self.context.clock.now_unix_micros().saturating_add(
                i64::try_from(method_deadline.method_timeout().as_micros()).unwrap_or(i64::MAX),
            ));
        let task = MethodTask {
            task_id: render_task_id(&self.task_id),
            request: *request,
            deadline: wall_deadline,
            scheduled_at: self.scheduled_at,
        };
        let session = match stream.send_task(task) {
            Ok(session) => session,
            Err(StepTaskSendFailure::LeaseInvalid) => return Ok(StartedAttemptEnd::Ended),
            Err(StepTaskSendFailure::NotDelivered) => {
                return Ok(self
                    .return_attempt(StepAttemptReturnReason::NotDelivered, urgency)
                    .await);
            }
        };
        let timeouts = AttemptTimeouts::new(&state, method_deadline);
        let end = self.follow(&state, session, &timeouts).await;
        let sticky_app_worker_id = state.run_record.sticky_app_worker_id.clone();
        match end {
            AttemptEnd::Canceled => Ok(StartedAttemptEnd::Ended),
            AttemptEnd::Returned(reason) => Ok(self.return_attempt(reason, urgency).await),
            AttemptEnd::Failure(mut failure) => {
                failure.omitted_value_slots =
                    state.omitted_value_slots(self.key.run_id, failure.worker_error.as_ref());
                self.propose_failure(failure, &sticky_app_worker_id, urgency)
                    .await;
                Ok(StartedAttemptEnd::Ended)
            }
            AttemptEnd::Result(frame, app_worker_id) => {
                self.complete(&state, *frame, app_worker_id, &timeouts.method, urgency)
                    .await?;
                Ok(StartedAttemptEnd::Ended)
            }
        }
    }

    /// Returns whether the attempt still runs with this task ID in the speculative state.
    async fn is_still_running(&self) -> Result<bool, EffectError> {
        let run = self.run;
        let key = self.key.clone();
        let task_id = self.task_id.clone();
        let read = self.retry_transient(|| {
            let key = key.clone();
            let task_id = task_id.clone();
            self.read_off_task(true, move |reader| {
                Ok(read_method_attempt_state(
                    reader,
                    run,
                    &key,
                    &task_id,
                    RequestValueHydration::References,
                )?
                .is_some())
            })
        });
        read.await
    }

    /// Proposes that the Worker never ran the attempt.
    ///
    /// Returns `Returned` once the attempt is pending again in the speculative state, or
    /// `Ended` when apply ignored the return or the node is stopping.
    async fn return_attempt(
        &self,
        reason: StepAttemptReturnReason,
        urgency: CommitUrgency,
    ) -> StartedAttemptEnd {
        tracing::debug!(
            run_id = %self.key.run_id,
            step_execution_id = %self.key.step_execution_id,
            attempt = self.key.attempt,
            ?reason,
            "the Worker never ran the attempt; returning it"
        );
        let command = self.flow_command(flow_command::Command::StepAttemptReturned(
            StepAttemptReturnedCommand {
                invocation: Some(self.key.invocation()),
                attempt: self.key.attempt,
                expected_task_id: self.fence_task_id(),
                reason: reason as i32,
            },
        ));
        match self
            .context
            .propose_until_applied(&self.flow, command, urgency)
            .await
        {
            Some(reply) if !reply.is_error() => StartedAttemptEnd::Returned,
            _ => StartedAttemptEnd::Ended,
        }
    }

    /// Runs `operation` until the method deadline or cancellation, giving the deadline priority.
    ///
    /// Returns `TimedOut` once the deadline has expired, even when `operation` finished in the
    /// same poll or before the deadline timer fired; returns `Canceled` when the attempt was
    /// canceled first; otherwise returns the operation's output.
    async fn before_method_deadline<T>(
        &self,
        deadline: &MethodDeadline,
        operation: impl Future<Output = T>,
    ) -> BeforeDeadline<T> {
        tokio::select! {
            biased;
            () = sleep_until(deadline.expires_at) => BeforeDeadline::TimedOut,
            () = self.cancel.cancelled() => BeforeDeadline::Canceled,
            output = operation => {
                if deadline.has_expired() {
                    BeforeDeadline::TimedOut
                } else {
                    BeforeDeadline::Done(output)
                }
            }
        }
    }

    /// Reads the attempt's state with the store reads on a blocking thread, so that a stalled
    /// read cannot hold off the method deadline. Opening the snapshot view does no I/O.
    ///
    /// Returns the state, `None` when the attempt is no longer running with this dispatch ID,
    /// or the read's error.
    async fn read_attempt_state(&self) -> Result<Option<MethodAttemptState>, EffectError> {
        let run = self.run;
        let key = self.key.clone();
        let task_id = self.task_id.clone();
        let is_hydrating = self.context.config.hydrate_before_dispatch;
        let is_sync = self.is_sync();
        let flow_effect_operations = Arc::clone(&self.context.flow_effect_operations);
        let flow = self.flow.clone();
        self.read_off_task(true, move |reader| {
            if is_hydrating {
                return read_method_attempt_state(
                    reader,
                    run,
                    &key,
                    &task_id,
                    RequestValueHydration::AllValues,
                );
            }
            if is_sync {
                return read_method_attempt_state(
                    reader,
                    run,
                    &key,
                    &task_id,
                    RequestValueHydration::References,
                );
            }
            // Opened after `reader`, so fewer of its values still look uncommitted.
            let open_committed = || Ok(flow_effect_operations.flow_state_reader(&flow, false)?);
            let hydration = RequestValueHydration::UncommittedValues {
                open_committed: &open_committed,
            };
            read_method_attempt_state(reader, run, &key, &task_id, hydration)
        })
        .await
    }

    /// Runs synchronous store reads on the blocking pool.
    ///
    /// Returns the read's result, or [`EffectError::Stopped`] when the runtime shut down before
    /// the read ran. A panic in `read` resumes on this task.
    async fn read_off_task<T: Send + 'static>(
        &self,
        should_include_speculative: bool,
        read: impl FnOnce(&dyn FlowStateReader) -> Result<T, EffectError> + Send + 'static,
    ) -> Result<T, EffectError> {
        // The effect tracker owns the read and its slot until the store call returns.
        let read_slot = Arc::clone(&self.context.blocking_read_slots)
            .acquire_owned()
            .await
            .expect("the blocking read slots are never closed");
        let reader = self
            .context
            .flow_effect_operations
            .flow_state_reader(&self.flow, should_include_speculative)?;
        let read_cancel = self.cancel.child_token();
        // When the attempt stops waiting, a read that has not started yet skips the store.
        let _cancel_unstarted_read = read_cancel.clone().drop_guard();
        let placement = self.context.config.blocking_work;
        // Dropping the handle at the deadline detaches the read; it ends with its store call.
        let handle = placement.run_tracked(&self.context.tracker, move || {
            let _read_slot = read_slot;
            if read_cancel.is_cancelled() {
                return Err(EffectError::Stopped);
            }
            read(reader.as_ref())
        });
        match handle.await {
            Ok(result) => result,
            Err(BlockingWorkFailure::Panicked(payload)) => std::panic::resume_unwind(payload),
            Err(error @ BlockingWorkFailure::Cancelled) => {
                tracing::debug!(%error, run_id = %self.key.run_id, "state read did not run");
                Err(EffectError::Stopped)
            }
        }
    }

    /// Reads the attempt's state and hard-delete values and builds the Worker request within
    /// `budget`, retrying transient read failures.
    ///
    /// When inlined uncommitted values make the request too large, waits for their commit and
    /// sends references instead.
    ///
    /// Returns the state and request, or why no Worker gets the attempt: it was canceled, it no
    /// longer runs with this task ID, or the budget expired or the request is too large.
    async fn prepare_request(
        &self,
        budget: &MethodDeadline,
    ) -> Result<PreparedRequest, EffectError> {
        let read = self.retry_transient(|| self.read_attempt_state());
        let state = match self.before_method_deadline(budget, read).await {
            BeforeDeadline::Canceled => return Ok(PreparedRequest::Canceled),
            BeforeDeadline::TimedOut => {
                return Ok(PreparedRequest::NotRun(
                    budget.build_timeout_failure(),
                    String::new(),
                ));
            }
            BeforeDeadline::Done(state) => state?,
        };
        let Some(state) = state else {
            tracing::debug!(run_id = %self.key.run_id, "method attempt is no longer running");
            return Ok(PreparedRequest::Gone);
        };
        let sticky_app_worker_id = state.run_record.sticky_app_worker_id.clone();
        let read = self.retry_transient(|| self.read_hard_delete_values(&state));
        let hard_delete_values = match self.before_method_deadline(budget, read).await {
            BeforeDeadline::Canceled => return Ok(PreparedRequest::Canceled),
            BeforeDeadline::TimedOut => {
                return Ok(PreparedRequest::NotRun(
                    budget.build_timeout_failure(),
                    sticky_app_worker_id,
                ));
            }
            BeforeDeadline::Done(values) => values?,
        };
        let mut request = build_method_request(&state, &hard_delete_values)?;
        if request.encoded_len() > self.context.config.max_worker_request_bytes
            && let Some(state_with_references) = state.with_value_references()
        {
            let commit = self.wait_for_proposed_commands();
            match self.before_method_deadline(budget, commit).await {
                BeforeDeadline::Canceled => return Ok(PreparedRequest::Canceled),
                BeforeDeadline::TimedOut => {
                    return Ok(PreparedRequest::NotRun(
                        budget.build_timeout_failure(),
                        sticky_app_worker_id,
                    ));
                }
                BeforeDeadline::Done(committed) => committed?,
            }
            request = build_method_request(&state_with_references, &hard_delete_values)?;
        }
        if request.encoded_len() > self.context.config.max_worker_request_bytes {
            return Ok(PreparedRequest::NotRun(
                AttemptFailure::not_run(
                    StepAttemptFailureKind::RequestTooLarge,
                    format!(
                        "Worker request of {} bytes exceeds the {} byte message limit",
                        request.encoded_len(),
                        self.context.config.max_worker_request_bytes
                    ),
                ),
                sticky_app_worker_id,
            ));
        }
        Ok(PreparedRequest::Ready(Box::new(state), Box::new(request)))
    }

    /// Waits until the Flow's proposed commands, including this attempt's value writes, commit.
    ///
    /// Returns [`EffectError::Stopped`] when the node stops first.
    async fn wait_for_proposed_commands(&self) -> Result<(), EffectError> {
        match self
            .context
            .flow_effect_operations
            .sync_barrier(&self.flow)
            .await
        {
            Ok(()) => Ok(()),
            Err(FlowEffectOperationError::NodeStopping { .. }) => Err(EffectError::Stopped),
            Err(error) => Err(error.into()),
        }
    }

    /// Runs `operation` until it succeeds or fails permanently, retrying transient failures.
    ///
    /// Returns [`EffectError::Stopped`] when the attempt is canceled while it waits.
    async fn retry_transient<T, Operation, OperationFuture>(
        &self,
        mut operation: Operation,
    ) -> Result<T, EffectError>
    where
        Operation: FnMut() -> OperationFuture,
        OperationFuture: Future<Output = Result<T, EffectError>>,
    {
        let mut backoff = self.context.retry_backoff();
        loop {
            match operation().await {
                Err(error) if error.is_retryable() => {
                    tracing::warn!(%error, run_id = %self.key.run_id, "retrying a transient attempt failure");
                    if !backoff.wait(&self.cancel).await {
                        return Err(EffectError::Stopped);
                    }
                }
                result => return result,
            }
        }
    }

    /// Reads hard-delete Attribute values; a deleted object is retried once with the current
    /// object of its Attribute, then omitted.
    async fn read_hard_delete_values(
        &self,
        state: &MethodAttemptState,
    ) -> Result<BTreeMap<String, Value>, EffectError> {
        let owner = self.object_owner(state);
        let mut values = BTreeMap::new();
        for (attribute_key, object) in state.hard_delete_attributes() {
            let objects = &self.context.hard_delete_objects;
            if let Some(value) = objects.read_value(owner, &object.object_id).await? {
                values.insert(attribute_key, value);
                continue;
            }
            let current_object_id = self.current_hard_delete_object_id(&attribute_key).await?;
            if let Some(object_id) = current_object_id.filter(|id| *id != object.object_id)
                && let Some(value) = objects.read_value(owner, &object_id).await?
            {
                values.insert(attribute_key, value);
            }
        }
        Ok(values)
    }

    async fn current_hard_delete_object_id(
        &self,
        attribute_key: &str,
    ) -> Result<Option<String>, EffectError> {
        let key = hard_delete_current_key(self.run.flow_hash, self.run.run_id, attribute_key);
        self.read_off_task(false, move |reader| {
            let Some(bytes) = reader.get(ColumnFamily::Flow, &key)? else {
                return Ok(None);
            };
            let record: HardDeleteObjectRecord = decode_row("HardDeleteObject", &key, &bytes)?;
            Ok(record.object.map(|object| object.object_id))
        })
        .await
    }

    fn object_owner(&self, state: &MethodAttemptState) -> HardDeleteObjectOwner {
        HardDeleteObjectOwner {
            tenant_id: TenantId(state.run_record.tenant_id),
            flow_hash: self.run.flow_hash,
            run_id: self.run.run_id,
        }
    }

    /// Follows the sent task's Worker frames, heartbeats, and timeouts to its end.
    async fn follow(
        &self,
        state: &MethodAttemptState,
        mut session: Box<dyn MethodDispatchSession>,
        timeouts: &AttemptTimeouts,
    ) -> AttemptEnd {
        let method_deadline = timeouts.method.expires_at;
        // The heartbeat timeout starts when the Worker receives the task.
        let mut heartbeat_deadline = Some(Instant::now() + timeouts.heartbeat_timeout);
        let mut checkpoints = CheckpointTracker {
            last_proposed: heartbeat_checkpoint_value(state),
            pending: None,
            flush_at: None,
        };
        let end = loop {
            let flush_at = checkpoints.flush_at;
            tokio::select! {
                biased;
                () = sleep_until(method_deadline) => {
                    session.cancel();
                    break AttemptEnd::Failure(timeouts.expired_failure(heartbeat_deadline)
                        .unwrap_or_else(|| timeouts.method.timeout_failure()));
                }
                () = sleep_until_optional(heartbeat_deadline) => {
                    session.cancel();
                    break AttemptEnd::Failure(timeouts.expired_failure(heartbeat_deadline)
                        .unwrap_or_else(|| timeouts.heartbeat_timeout_failure()));
                }
                () = self.cancel.cancelled() => {
                    session.cancel();
                    return AttemptEnd::Canceled;
                }
                () = sleep_until_optional(flush_at) => {
                    if let Some(failure) = timeouts.expired_failure(heartbeat_deadline) {
                        session.cancel();
                        break AttemptEnd::Failure(failure);
                    }
                    if let Some(checkpoint) = checkpoints.take_pending() {
                        self.propose_heartbeat(checkpoint).await;
                    }
                }
                event = session.next_event() => {
                    // A timer fires up to a driver tick late; an event after its deadline loses.
                    if let Some(failure) = timeouts.expired_failure(heartbeat_deadline) {
                        session.cancel();
                        break AttemptEnd::Failure(failure);
                    }
                    let Some(event) = event else {
                        break AttemptEnd::Failure(AttemptFailure::server(
                            StepAttemptFailureKind::DispatchError,
                            "dispatch ended without an outcome",
                        ));
                    };
                    if let StepTaskEvent::Output(frame) = event {
                        heartbeat_deadline = Some(Instant::now() + timeouts.heartbeat_timeout);
                        if let Some(change) = heartbeat_change(&frame) {
                            checkpoints.observe(change, self.context.config.heartbeat_batch_window);
                        } else if let Some(write) = into_step_stream_write(frame) {
                            self.write_step_stream(state, write).await;
                        }
                        continue;
                    }
                    break task_event_end(event, &timeouts.method)
                        .expect("only output frames are non-final");
                }
            }
        };
        // A checkpoint the Worker sent before the end reaches the next attempt (plan section 3.7).
        if let Some(checkpoint) = checkpoints.take_pending() {
            self.propose_heartbeat(checkpoint).await;
        }
        end
    }

    async fn propose_heartbeat(&self, checkpoint: Option<Value>) {
        let checkpoint = match checkpoint {
            Some(value) => step_attempt_heartbeat_command::Checkpoint::Value(value),
            None => step_attempt_heartbeat_command::Checkpoint::IsCleared(true),
        };
        let command = self.flow_command(flow_command::Command::StepAttemptHeartbeat(
            StepAttemptHeartbeatCommand {
                invocation: Some(self.key.invocation()),
                attempt: self.key.attempt,
                checkpoint: Some(checkpoint),
                task_id: self.fence_task_id(),
            },
        ));
        // A lost checkpoint only means the next attempt sees an older value.
        if let Err(error) = self
            .context
            .flow_effect_operations
            .propose_speculative(&self.flow, command, CommitUrgency::Batched)
            .await
        {
            tracing::warn!(%error, run_id = %self.key.run_id, "proposing a heartbeat failed");
        }
    }

    /// Appends a Step Stream write in frame order; a rejected write is logged and never fails
    /// the attempt.
    async fn write_step_stream(&self, state: &MethodAttemptState, write: StepStreamWrite) {
        let Some(value) = write.value else {
            tracing::warn!(
                run_id = %self.key.run_id,
                stream_name = %write.stream_name,
                "Worker Stream write without a Value was dropped"
            );
            return;
        };
        let input = WriteStreamInput {
            tenant_id: state.run_record.tenant_id,
            flow_id: state.run_record.flow_id.clone(),
            flow_type: state.run_record.flow_type.clone(),
            stream_name: write.stream_name,
            stream_capacity_bytes: Some(write.stream_capacity_bytes),
            value,
            source: String::new(),
        };
        // The store logs every dropped write itself.
        let _is_written = self
            .context
            .stream_store
            .write_worker_best_effort(input, &self.key.step_execution_id)
            .await;
    }

    async fn complete(
        &self,
        state: &MethodAttemptState,
        frame: PollWorkerTaskFrame,
        app_worker_id: String,
        method_deadline: &MethodDeadline,
        urgency: CommitUrgency,
    ) -> Result<(), EffectError> {
        let sticky_app_worker_id = state.run_record.sticky_app_worker_id.clone();
        let max_value_bytes = match state.run_record.settings.as_ref() {
            Some(settings) if settings.max_value_bytes > 0 => settings.max_value_bytes,
            _ => self.context.config.default_max_value_bytes,
        };
        let flow_config = state.run_record.flow_config.clone().unwrap_or_default();
        let check = check_worker_result(
            frame,
            self.key.method,
            &self.context.validation_config,
            &flow_config,
            max_value_bytes,
        );
        let mut result = match check {
            WorkerResultCheck::Valid(result) => *result,
            WorkerResultCheck::Invalid {
                kind,
                detail,
                is_non_retryable,
            } => {
                let failure = AttemptFailure {
                    kind,
                    worker_error: None,
                    server_detail: detail,
                    is_non_retryable,
                    served_by_app_worker_id: app_worker_id,
                    omitted_value_slots: None,
                    is_not_run: false,
                };
                self.propose_failure(failure, &sticky_app_worker_id, urgency)
                    .await;
                return Ok(());
            }
        };
        result.assign_channel_message_ids(self.context.id_source.as_ref());
        let mut backoff = self.context.retry_backoff();
        let uploads = loop {
            let objects = &self.context.hard_delete_objects;
            let upload = result
                .upload_attributes(objects, self.object_owner(state))
                .await;
            match upload {
                Ok(uploads) => break uploads,
                Err(error) if error.is_retryable() && method_deadline.has_expired() => {
                    tracing::warn!(%error, run_id = %self.key.run_id, "hard-delete uploads failed after the method deadline");
                    let failure = method_deadline.timeout_failure();
                    self.propose_failure(failure, &sticky_app_worker_id, urgency)
                        .await;
                    return Ok(());
                }
                Err(error) if error.is_retryable() => {
                    tracing::warn!(%error, run_id = %self.key.run_id, "retrying hard-delete uploads");
                    if !backoff.wait(&self.cancel).await {
                        return Err(EffectError::Stopped);
                    }
                }
                Err(error) => return Err(error),
            }
        };
        // Like the Go Server's backends, a result whose processing ends after the deadline loses.
        if method_deadline.has_expired() {
            let failure = method_deadline.timeout_failure();
            self.propose_failure(failure, &sticky_app_worker_id, urgency)
                .await;
            self.finish_result_uploads(uploads, false).await;
            return Ok(());
        }
        let command = self.flow_command(flow_command::Command::StepMethodCompleted(
            result.into_command(
                self.key.invocation(),
                self.key.attempt,
                app_worker_id.clone(),
                uploads.references.clone(),
                pull_routing(&sticky_app_worker_id, &app_worker_id),
                self.fence_task_id(),
            ),
        ));
        let reply = self
            .context
            .propose_until_committed(&self.flow, command, urgency)
            .await;
        // Without a reply the node is stopping; recovery finishes the recorded uploads.
        let Some(reply) = reply else {
            return Ok(());
        };
        self.finish_result_uploads(uploads, !reply.is_error()).await;
        Ok(())
    }

    /// Finishes the uploads of the attempt's only proposal. A failure is logged because the ledger
    /// keeps its records for recovery.
    async fn finish_result_uploads(&self, uploads: HardDeleteUploads, is_committed: bool) {
        if let Err(error) = self
            .context
            .hard_delete_objects
            .finish_uploads(uploads, is_committed)
            .await
        {
            tracing::warn!(
                %error,
                run_id = %self.key.run_id,
                attempt = self.key.attempt,
                "finishing hard-delete uploads failed; recovery deletes unreferenced objects"
            );
        }
    }

    async fn propose_failure(
        &self,
        failure: AttemptFailure,
        sticky_app_worker_id: &str,
        urgency: CommitUrgency,
    ) {
        self.log_failure(&failure);
        let command = self.flow_command(flow_command::Command::StepAttemptFailed(
            StepAttemptFailedCommand {
                invocation: Some(self.key.invocation()),
                attempt: self.key.attempt,
                kind: failure.kind as i32,
                worker_error: failure.worker_error,
                server_detail: failure.server_detail,
                is_non_retryable: failure.is_non_retryable,
                pull_routing: Some(pull_routing(
                    sticky_app_worker_id,
                    &failure.served_by_app_worker_id,
                )),
                expected_task_id: self.fence_task_id(),
                is_not_run: failure.is_not_run,
                ..StepAttemptFailedCommand::default()
            },
        ));
        // A stale failure is rejected by apply; nothing else to do with the reply.
        let _reply = self
            .context
            .propose_until_committed(&self.flow, command, urgency)
            .await;
    }

    /// Logs the failure without Worker error text, which may hold values or secrets.
    fn log_failure(&self, failure: &AttemptFailure) {
        let worker_error = failure.worker_error.as_ref();
        let worker_grpc_status = worker_error.map_or(0, |error| error.grpc_status);
        if failure.is_step_error() {
            tracing::info!(
                flow_id = %self.flow.flow_id,
                run_id = %self.key.run_id,
                step_execution_id = %self.key.step_execution_id,
                attempt = self.key.attempt,
                kind = ?failure.kind,
                detail = %failure.server_detail,
                worker_grpc_status,
                "method attempt failed"
            );
        } else {
            tracing::warn!(
                flow_id = %self.flow.flow_id,
                run_id = %self.key.run_id,
                step_execution_id = %self.key.step_execution_id,
                attempt = self.key.attempt,
                kind = ?failure.kind,
                detail = %failure.server_detail,
                worker_grpc_status,
                omitted_value_slots = ?failure.omitted_value_slots,
                "method attempt failed"
            );
        }
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
}

async fn sleep_until_optional(deadline: Option<Instant>) {
    match deadline {
        Some(deadline) => sleep_until(deadline).await,
        None => std::future::pending().await,
    }
}

/// Returns the routing of a dispatch to the run's sticky Worker or its Flow type queue; an
/// empty `served_by_app_worker_id` means no Worker served it yet.
pub(crate) fn pull_routing(
    sticky_app_worker_id: &str,
    served_by_app_worker_id: &str,
) -> PullRouting {
    let source = if served_by_app_worker_id.is_empty() {
        PullRoutingSource::Unspecified
    } else if served_by_app_worker_id == sticky_app_worker_id {
        PullRoutingSource::StickyQueue
    } else {
        PullRoutingSource::FlowTypeQueue
    };
    PullRouting {
        sticky_app_worker_id: sticky_app_worker_id.to_owned(),
        served_by_app_worker_id: served_by_app_worker_id.to_owned(),
        source: source as i32,
    }
}

/// Returns `task_id` for a command's fence field.
///
/// Panics on an empty ID: apply would read it as an older log and skip the fence.
pub(crate) fn fence_task_id(task_id: &[u8]) -> Vec<u8> {
    assert!(
        !task_id.is_empty(),
        "every started attempt has the task ID that its StepAttemptStarted recorded"
    );
    task_id.to_vec()
}

/// Renders a task ID recorded by `StepAttemptStarted` as the Worker sees it.
pub(crate) fn render_task_id(task_id: &[u8]) -> String {
    uuid::Uuid::from_slice(task_id).map_or_else(
        |_| task_id.iter().map(|byte| format!("{byte:02x}")).collect(),
        |uuid| uuid.hyphenated().to_string(),
    )
}

fn unix_micros_to_system_time(micros: i64) -> SystemTime {
    UNIX_EPOCH + Duration::from_micros(u64::try_from(micros).unwrap_or(0))
}

/// Whether a reply rejected the command.
trait ReplyOutcome {
    fn is_error(&self) -> bool;
}

impl ReplyOutcome for dexzd_protocol::dexzd::v1::CommandReply {
    fn is_error(&self) -> bool {
        matches!(
            self.reply,
            Some(dexzd_protocol::dexzd::v1::command_reply::Reply::Error(_))
        )
    }
}
