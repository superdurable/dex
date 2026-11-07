// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Effect runner tests on paused tokio time. A fake runtime applies commands with the real
//! apply on an in-memory state and feeds the committed effects back to the runner.

mod attribute_sync_tests;
mod deadline_tests;
mod dispatch_tests;
mod handout_tests;
mod hard_delete_tests;
mod history_migration_upload_tests;
mod history_upload_tests;
mod keyword_index_tests;
mod outbox_and_purge_tests;
mod rebuild_tests;
mod task_queue_race_tests;
mod task_queue_tests;
mod timer_tests;
mod worker_output_tests;

use std::collections::{BTreeMap, BTreeSet};
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::thread::ThreadId;
use std::time::Duration;

use dexzd_apply::{
    ApplyContext, ApplyEffect, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command, apply_staged_time_travel_for_test, is_unstaged_time_travel_for_test,
    stage_time_travel_for_test,
};
use dexzd_clock::WallClock;
use dexzd_keyspace::dispatch_queue_keys::DispatchQueueName;
use dexzd_keyspace::row_keys::run_singleton_key;
use dexzd_keyspace::{ColumnFamily, PartitionHasher, PartitionId, RunId, RunTable, TenantId};
use dexzd_object_store::{FaultAction, FaultInjector, InMemoryObjectStore, ObjectStoreOperation};
use dexzd_protocol::dex::{
    FlowTimeoutPolicy, InvokeExecuteMethodOutput, InvokeExecuteMethodResponse,
    InvokeWaitForMethodOutput, InvokeWaitForMethodResponse, PollWorkerTaskFrame, PullRouting,
    StartFlowRequest, StepMethodHeartbeat, StepStreamWrite, Value, WorkerErrorResponse,
    WorkerTarget, invoke_execute_method_output, invoke_wait_for_method_output,
    poll_worker_task_frame, value,
};
use dexzd_protocol::dexzd::v1::{
    CommandErrorReply, CommandReply, EmptyReply, FlowCommand, FlowStartCommand, RunRecord,
    RunSettings, StepAttemptStartedCommand, StepExecutionRecord, StepInvocationKey,
    StepInvocationPhase, command_reply, flow_command,
};
use dexzd_request_validation::ValidationConfig;
use dexzd_storage::InMemoryLocalStore;
use dexzd_stream_store::{StreamStore, StreamStoreConfig};
use dexzd_worker_dispatch::{
    ClaimReply, DispatchOutcome, HeadsScanToken, QueuedTaskRef, ScannedQueueHead, StepTaskEvent,
    StepTaskSendFailure, WorkerDispatch,
};
use futures::future::BoxFuture;
use prost::Message;
use tokio::sync::{mpsc, watch};

use crate::SystemEngineIdSource;
use crate::attribute_sync::AttributeSyncWaker;
use crate::effects::flow_effect_operations::{
    FlowEffectOperationError, FlowEffectOperations, InvocationLockCheck, InvocationStartOutcome,
};
use crate::effects::state_reads::{RunLocation, read_step_execution};
use crate::effects::{
    AttemptDrainProgress, EffectDependencies, EffectError, EffectRunner, EffectTasksHandle,
    EffectsConfig, HardDeleteObjects, HardDeleteUploadLedger, MethodClaim, MethodDispatchSession,
    MethodDispatcher, MethodRequest, MethodStreamRequest, MethodStreamReservation, MethodTask,
    NodeMetaUploadLedger, ReservedMethodStream,
};
use crate::flow_coordination::{CommitUrgency, FlowAddress, FlowCoordinationEffectSink};

pub(super) const SECOND_MICROS: i64 = 1_000_000;
pub(super) const START_MICROS: i64 = 1_800_000_000 * SECOND_MICROS;

/// Wall clock that follows paused tokio time.
pub(super) struct TokioWallClock {
    base_micros: i64,
    start: tokio::time::Instant,
}

impl TokioWallClock {
    pub(super) fn new() -> Self {
        Self {
            base_micros: START_MICROS,
            start: tokio::time::Instant::now(),
        }
    }
}

impl WallClock for TokioWallClock {
    fn now_unix_micros(&self) -> i64 {
        self.base_micros + i64::try_from(self.start.elapsed().as_micros()).expect("fits")
    }
}

/// Rows of committed state.
#[derive(Debug, Default, Clone)]
pub(super) struct MemoryState {
    pub(super) rows: BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>>,
}

impl FlowStateReader for MemoryState {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        Ok(self.rows.get(&(column_family, key.to_vec())).cloned())
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        Ok(self
            .rows
            .range((column_family, start.to_vec())..(column_family, end.to_vec()))
            .take(limit)
            .map(|((_, key), value)| (key.clone(), value.clone()))
            .collect())
    }
}

impl MemoryState {
    pub(super) fn commit(&mut self, writes: &[StateWrite]) {
        for write in writes {
            match write {
                StateWrite::Put {
                    column_family,
                    key,
                    value,
                } => {
                    self.rows
                        .insert((*column_family, key.clone()), value.clone());
                }
                StateWrite::Delete { column_family, key }
                | StateWrite::SingleDelete { column_family, key } => {
                    self.rows.remove(&(*column_family, key.clone()));
                }
                StateWrite::DeleteRange {
                    column_family,
                    start,
                    end,
                } => {
                    let doomed: Vec<_> = self
                        .rows
                        .range((*column_family, start.clone())..(*column_family, end.clone()))
                        .map(|(key, _)| key.clone())
                        .collect();
                    for key in doomed {
                        self.rows.remove(&key);
                    }
                }
            }
        }
    }

    pub(super) fn put_message(
        &mut self,
        column_family: ColumnFamily,
        key: Vec<u8>,
        row: &impl Message,
    ) {
        self.rows.insert((column_family, key), row.encode_to_vec());
    }
}

/// Replaces apply for one command: returns the reply after changing the state, or `None` to
/// let the real apply run.
pub(super) type CommandOverride =
    Box<dyn Fn(&FlowCommand, &mut MemoryState) -> Option<CommandReply> + Send + Sync>;

/// A recorded proposal.
#[derive(Debug, Clone)]
pub(super) struct RecordedCommand {
    pub(super) command: FlowCommand,
    pub(super) urgency: CommitUrgency,
    pub(super) reply: CommandReply,
}

/// Proposals applied speculatively behind a held attempt start.
struct HeldCommits {
    /// The committed state from before the held start.
    committed: MemoryState,
    /// Effects to publish as committed on release, in apply order.
    effects: Vec<(FlowAddress, Vec<ApplyEffect>)>,
}

/// Fake Flow runtime: stamps, applies with the real apply, and publishes committed effects.
pub(super) struct InMemoryFlowEffectOperations {
    pub(super) state: Arc<Mutex<MemoryState>>,
    hasher: PartitionHasher,
    clock: Arc<TokioWallClock>,
    sink: EffectRunner,
    last_proposed_micros: Mutex<i64>,
    pub(super) commands: Mutex<Vec<RecordedCommand>>,
    /// Effects of every committed command, in commit order.
    pub(super) published_effects: Mutex<Vec<ApplyEffect>>,
    pub(super) busy_lock_keys: Mutex<BTreeSet<String>>,
    pub(super) unavailable_proposals: AtomicUsize,
    /// Makes `flow_state_reader` fail as unavailable; committed reads still succeed.
    pub(super) is_flow_state_unreadable: AtomicBool,
    pub(super) flow_state_reader_failures: Arc<AtomicUsize>,
    /// Sets `is_flow_state_unreadable` once an attempt start commits.
    pub(super) is_flow_state_unreadable_after_start: AtomicBool,
    /// Holds `flow_state_reader` calls from blocking threads while closed.
    pub(super) state_read_gate: Arc<StateReadGate>,
    /// Holds committed reads from blocking threads while closed.
    pub(super) committed_read_gate: Arc<StateReadGate>,
    /// Holds the entries gauge's row-count pages from blocking threads while closed.
    pub(super) row_count_gate: Arc<StateReadGate>,
    /// Counts `committed_state_reader` calls.
    pub(super) committed_reads: AtomicUsize,
    /// Fails the next `committed_state_reader` calls as unavailable; other reads succeed.
    pub(super) committed_reader_failures: AtomicUsize,
    /// Counts committed DispatchQueue seeks and pages other than row-count pages.
    pub(super) committed_queue_seeks: Arc<AtomicUsize>,
    /// Counts `flow_state_reader` calls that leave out speculative state.
    pub(super) committed_flow_reads: AtomicUsize,
    /// Delays each `propose_committed`, like a group commit window plus an object store write.
    pub(super) proposal_delay: Mutex<Duration>,
    /// Delays the commit of each attempt start after its speculative apply.
    pub(super) start_commit_delay: Mutex<Duration>,
    /// Holds the next attempt start, and every later proposal, uncommitted until released.
    pub(super) should_hold_next_start: AtomicBool,
    held_commits: Mutex<Option<HeldCommits>>,
    /// Turns true when held commits are released.
    held_commits_released: tokio::sync::watch::Sender<bool>,
    /// Starts that find their locks taken although the pre-check passed, as when a
    /// transactional RPC takes them in between.
    pub(super) starts_finding_busy_locks: AtomicUsize,
    overrides: Mutex<Vec<CommandOverride>>,
    /// Transactional RPCs a test holds in flight; pending close checks wait for zero.
    pub(super) transactional_rpcs: tokio::sync::watch::Sender<u32>,
}

impl InMemoryFlowEffectOperations {
    /// Proposes and commits one command, then publishes its effects.
    pub(super) fn commit(
        &self,
        flow: &FlowAddress,
        mut command: FlowCommand,
        urgency: CommitUrgency,
    ) -> Result<CommandReply, FlowEffectOperationError> {
        if self
            .unavailable_proposals
            .fetch_update(Ordering::SeqCst, Ordering::SeqCst, |budget| {
                budget.checked_sub(1)
            })
            .is_ok()
        {
            return Err(FlowEffectOperationError::Unavailable {
                detail: "injected outage".to_owned(),
            });
        }
        let effects;
        let reply;
        {
            let mut last = self.last_proposed_micros.lock().expect("lock");
            *last = (*last + 1).max(self.clock.now_unix_micros());
            command.epoch = 1;
            command.behavior_version = 1;
            command.proposed_at_micros = *last;
            command.command_id = uuid::Uuid::now_v7().as_bytes().to_vec();
            let mut state = self.state.lock().expect("lock");
            let overridden = self
                .overrides
                .lock()
                .expect("lock")
                .iter()
                .find_map(|handler| handler(&command, &mut state));
            if let Some(override_reply) = overridden {
                effects = Vec::new();
                reply = override_reply;
            } else {
                let flow_hash = self.hasher.flow_hash(flow.tenant_id, &flow.flow_id);
                let context = ApplyContext {
                    partition: self.hasher.partition_of(flow_hash),
                    flow_hash,
                };
                let outcome: ApplyOutcome = if is_unstaged_time_travel_for_test(&command) {
                    // An owner stages the result before it commits the TimeTravel.
                    let staged = stage_time_travel_for_test(&context, &*state, &command)
                        .expect("time travel computes");
                    apply_staged_time_travel_for_test(staged, |staged_command| {
                        let outcome = apply_flow_command(&context, &*state, &staged_command)
                            .expect("apply succeeds");
                        state.commit(&outcome.writes);
                        outcome
                    })
                } else {
                    let outcome =
                        apply_flow_command(&context, &*state, &command).expect("apply succeeds");
                    state.commit(&outcome.writes);
                    outcome
                };
                effects = outcome.effects;
                reply = outcome.reply;
            }
            self.commands.lock().expect("lock").push(RecordedCommand {
                command,
                urgency,
                reply: reply.clone(),
            });
            self.published_effects
                .lock()
                .expect("lock")
                .extend(effects.iter().cloned());
        }
        let is_held = match self.held_commits.lock().expect("lock").as_mut() {
            Some(held) => {
                held.effects.push((flow.clone(), effects.clone()));
                true
            }
            None => false,
        };
        self.sink.handle(flow, &effects, !is_held);
        Ok(reply)
    }

    /// Returns the committed state, which leaves out held proposals.
    pub(super) fn committed_snapshot(&self) -> MemoryState {
        match self.held_commits.lock().expect("lock").as_ref() {
            Some(held) => held.committed.clone(),
            None => self.snapshot(),
        }
    }

    /// Commits the held start and every proposal behind it, and publishes their effects.
    pub(super) fn release_held_commits(&self) {
        let held = self
            .held_commits
            .lock()
            .expect("lock")
            .take()
            .expect("a start is held");
        for (flow, effects) in &held.effects {
            self.sink.handle(flow, effects, true);
        }
        self.held_commits_released.send_replace(true);
    }

    /// Waits until no proposal is held.
    async fn wait_for_held_commits(&self) {
        let mut released = self.held_commits_released.subscribe();
        if self.held_commits.lock().expect("lock").is_none() {
            return;
        }
        // The sender lives as long as this runtime.
        if released.wait_for(|is_released| *is_released).await.is_err() {
            tracing::debug!("the test runtime dropped");
        }
    }

    pub(super) fn add_override(&self, handler: CommandOverride) {
        self.overrides.lock().expect("lock").push(handler);
    }

    /// Returns recorded commands whose body matches `predicate`.
    pub(super) fn commands_matching(
        &self,
        predicate: impl Fn(&flow_command::Command) -> bool,
    ) -> Vec<RecordedCommand> {
        self.commands
            .lock()
            .expect("lock")
            .iter()
            .filter(|recorded| recorded.command.command.as_ref().is_some_and(&predicate))
            .cloned()
            .collect()
    }

    pub(super) fn snapshot(&self) -> MemoryState {
        self.state.lock().expect("lock").clone()
    }
}

impl FlowEffectOperations for InMemoryFlowEffectOperations {
    fn propose_committed<'a>(
        &'a self,
        flow: &'a FlowAddress,
        command: FlowCommand,
        urgency: CommitUrgency,
    ) -> BoxFuture<'a, Result<CommandReply, FlowEffectOperationError>> {
        Box::pin(async move {
            let delay = *self.proposal_delay.lock().expect("lock");
            if !delay.is_zero() {
                tokio::time::sleep(delay).await;
            }
            let reply = self.commit(flow, command, urgency)?;
            self.wait_for_held_commits().await;
            Ok(reply)
        })
    }

    fn propose_speculative<'a>(
        &'a self,
        flow: &'a FlowAddress,
        command: FlowCommand,
        urgency: CommitUrgency,
    ) -> BoxFuture<'a, Result<CommandReply, FlowEffectOperationError>> {
        Box::pin(async move { self.commit(flow, command, urgency) })
    }

    fn sync_barrier<'a>(
        &'a self,
        _flow: &'a FlowAddress,
    ) -> BoxFuture<'a, Result<(), FlowEffectOperationError>> {
        // Proposals commit before they return, unless a start is held.
        Box::pin(async move {
            self.wait_for_held_commits().await;
            Ok(())
        })
    }

    fn wait_for_transactional_rpcs<'a>(
        &'a self,
        _flow: &'a FlowAddress,
    ) -> BoxFuture<'a, Result<(), FlowEffectOperationError>> {
        let mut in_flight = self.transactional_rpcs.subscribe();
        Box::pin(async move {
            in_flight
                .wait_for(|count| *count == 0)
                .await
                .map(drop)
                .map_err(|_| FlowEffectOperationError::NodeStopping {
                    detail: "the test runtime dropped".to_owned(),
                })
        })
    }

    fn try_start_invocation<'a>(
        &'a self,
        flow: &'a FlowAddress,
        run_id: RunId,
        run_attempt: u32,
        invocation: &'a StepInvocationKey,
        attempt: u32,
        task_id: Vec<u8>,
        pull_routing: PullRouting,
    ) -> BoxFuture<'a, Result<InvocationStartOutcome, FlowEffectOperationError>> {
        Box::pin(async move {
            let run = RunLocation {
                flow_hash: self.hasher.flow_hash(flow.tenant_id, &flow.flow_id),
                run_id,
            };
            let step = read_step_execution(&self.snapshot(), run, &invocation.step_execution_id)
                .expect("state reads succeed");
            let Some(record) = step.and_then(|step| step.invocation) else {
                return Ok(InvocationStartOutcome::Stale);
            };
            if record.phase != StepInvocationPhase::Pending as i32 || record.attempt != attempt {
                return Ok(InvocationStartOutcome::Stale);
            }
            if self
                .starts_finding_busy_locks
                .fetch_update(Ordering::SeqCst, Ordering::SeqCst, |remaining| {
                    remaining.checked_sub(1)
                })
                .is_ok()
            {
                return Ok(InvocationStartOutcome::LocksBusy);
            }
            let busy = self.busy_lock_keys.lock().expect("lock");
            if record.lock_keys.iter().any(|key| busy.contains(key)) {
                return Ok(InvocationStartOutcome::LocksBusy);
            }
            drop(busy);
            let command = FlowCommand {
                flow_id: flow.flow_id.clone(),
                run_id: run_id.as_bytes().to_vec(),
                expected_run_attempt: run_attempt,
                tenant_id: flow.tenant_id.0,
                command: Some(flow_command::Command::StepAttemptStarted(
                    StepAttemptStartedCommand {
                        invocation: Some(invocation.clone()),
                        attempt,
                        task_id,
                        pull_routing: Some(pull_routing),
                    },
                )),
                ..FlowCommand::default()
            };
            let urgency = if record.effective_durability
                == dexzd_protocol::dex::StepDurability::Async as i32
            {
                CommitUrgency::Batched
            } else {
                CommitUrgency::Prompt
            };
            let is_held = self.should_hold_next_start.swap(false, Ordering::SeqCst);
            if is_held {
                self.held_commits_released.send_replace(false);
                *self.held_commits.lock().expect("lock") = Some(HeldCommits {
                    committed: self.snapshot(),
                    effects: Vec::new(),
                });
            }
            let mut released = self.held_commits_released.subscribe();
            let reply = self.commit(flow, command, urgency)?;
            if is_error_reply(&reply) {
                return Ok(InvocationStartOutcome::Stale);
            }
            if self
                .is_flow_state_unreadable_after_start
                .load(Ordering::SeqCst)
            {
                self.is_flow_state_unreadable.store(true, Ordering::SeqCst);
            }
            let commit_delay = *self.start_commit_delay.lock().expect("lock");
            Ok(InvocationStartOutcome::Started(Box::pin(async move {
                if is_held && released.wait_for(|is_released| *is_released).await.is_err() {
                    tracing::debug!("the test runtime dropped");
                }
                tokio::time::sleep(commit_delay).await;
                Ok(())
            })))
        })
    }

    fn check_invocation_locks<'a>(
        &'a self,
        flow: &'a FlowAddress,
        run_id: RunId,
        invocation: &'a StepInvocationKey,
        attempt: u32,
    ) -> BoxFuture<'a, Result<InvocationLockCheck, FlowEffectOperationError>> {
        Box::pin(async move {
            let run = RunLocation {
                flow_hash: self.hasher.flow_hash(flow.tenant_id, &flow.flow_id),
                run_id,
            };
            let step = read_step_execution(&self.snapshot(), run, &invocation.step_execution_id)
                .expect("state reads succeed");
            let Some(record) = step.and_then(|step| step.invocation) else {
                return Ok(InvocationLockCheck::Stale);
            };
            if record.phase != StepInvocationPhase::Pending as i32 || record.attempt != attempt {
                return Ok(InvocationLockCheck::Stale);
            }
            let busy = self.busy_lock_keys.lock().expect("lock");
            if record.lock_keys.iter().any(|key| busy.contains(key)) {
                return Ok(InvocationLockCheck::Busy);
            }
            Ok(InvocationLockCheck::Free)
        })
    }

    fn flow_state_reader(
        &self,
        _flow: &FlowAddress,
        should_include_speculative: bool,
    ) -> Result<Box<dyn FlowStateReader + Send>, FlowEffectOperationError> {
        if !should_include_speculative {
            self.committed_flow_reads.fetch_add(1, Ordering::SeqCst);
        }
        if self.is_flow_state_unreadable.load(Ordering::SeqCst)
            || self
                .flow_state_reader_failures
                .fetch_update(Ordering::SeqCst, Ordering::SeqCst, |remaining| {
                    remaining.checked_sub(1)
                })
                .is_ok()
        {
            return Err(FlowEffectOperationError::Unavailable {
                detail: "injected state reader outage".to_owned(),
            });
        }
        let state = if should_include_speculative {
            self.snapshot()
        } else {
            self.committed_snapshot()
        };
        Ok(Box::new(GatedStateReader {
            state,
            read: StateReadInProgress::begin(&self.state_read_gate),
        }))
    }

    fn committed_state_reader(
        &self,
    ) -> Result<Box<dyn FlowStateReader + Send>, FlowEffectOperationError> {
        self.committed_reads.fetch_add(1, Ordering::SeqCst);
        if self
            .committed_reader_failures
            .fetch_update(Ordering::SeqCst, Ordering::SeqCst, |remaining| {
                remaining.checked_sub(1)
            })
            .is_ok()
        {
            return Err(FlowEffectOperationError::Unavailable {
                detail: "injected committed reader outage".to_owned(),
            });
        }
        Ok(Box::new(CountingStateReader {
            state: self.committed_snapshot(),
            queue_seeks: Arc::clone(&self.committed_queue_seeks),
            committed_read_gate: Arc::clone(&self.committed_read_gate),
            row_count_gate: Arc::clone(&self.row_count_gate),
            _open_read: StateReadInProgress::begin(&self.state_read_gate),
        }))
    }
}

/// Holds store reads on blocking threads while closed, like a stalled local store read;
/// runtime-thread reads pass.
pub(super) struct StateReadGate {
    runtime_thread: ThreadId,
    state: Mutex<StateReadGateState>,
    changed: Condvar,
    /// When set, the next store read past the gate panics, as a read that hits a bug.
    pub(super) should_panic_next_read: AtomicBool,
}

#[derive(Default)]
struct StateReadGateState {
    is_closed: bool,
    held_reads: usize,
    /// Readers still alive; the effect runner may hand one to a blocking thread.
    open_readers: usize,
    /// Paused time at which a reader last dropped on a blocking thread.
    last_blocking_read_ended_at: Option<tokio::time::Instant>,
}

impl StateReadGate {
    fn new() -> Self {
        Self {
            runtime_thread: std::thread::current().id(),
            state: Mutex::new(StateReadGateState::default()),
            changed: Condvar::new(),
            should_panic_next_read: AtomicBool::new(false),
        }
    }

    fn panic_if_armed(&self) {
        if self.should_panic_next_read.swap(false, Ordering::SeqCst) {
            panic!("injected store read panic");
        }
    }

    /// Holds reads until the returned guard drops, so a failed assertion cannot block runtime
    /// shutdown.
    pub(super) fn close(self: &Arc<Self>) -> OpenStateReadGateOnDrop {
        self.state.lock().expect("lock").is_closed = true;
        OpenStateReadGateOnDrop(Arc::clone(self))
    }

    fn open(&self) {
        self.state.lock().expect("lock").is_closed = false;
        self.changed.notify_all();
    }

    pub(super) fn held_reads(&self) -> usize {
        self.state.lock().expect("lock").held_reads
    }

    pub(super) fn open_reader_count(&self) -> usize {
        self.state.lock().expect("lock").open_readers
    }

    /// Blocks the calling thread, in real time, until `count` reads are held.
    pub(super) fn wait_for_held_reads(&self, count: usize) {
        let state = self.state.lock().expect("lock");
        let (state, wait) = self
            .changed
            .wait_timeout_while(state, Duration::from_secs(10), |state| {
                state.held_reads != count
            })
            .expect("lock");
        let held_reads = state.held_reads;
        // Panicking under the lock would poison it for the guard that opens the gate.
        drop(state);
        assert!(
            !wait.timed_out(),
            "expected {count} held state reads, found {held_reads}"
        );
    }

    /// Returns true while a reader is open, or one dropped on a blocking thread at this instant.
    ///
    /// Paused time cannot advance during a blocking task, so a read that ended earlier has woken
    /// its attempt.
    fn may_have_unconsumed_reads(&self) -> bool {
        let state = self.state.lock().expect("lock");
        state.open_readers > 0
            || state.last_blocking_read_ended_at == Some(tokio::time::Instant::now())
    }

    fn hold_while_closed(&self) {
        if std::thread::current().id() == self.runtime_thread {
            return;
        }
        let mut state = self.state.lock().expect("lock");
        if !state.is_closed {
            return;
        }
        state.held_reads += 1;
        self.changed.notify_all();
        state = self
            .changed
            .wait_while(state, |state| state.is_closed)
            .expect("lock");
        state.held_reads -= 1;
        self.changed.notify_all();
    }
}

/// Tracks one open reader from its creation until it drops.
struct StateReadInProgress(Arc<StateReadGate>);

impl StateReadInProgress {
    fn begin(gate: &Arc<StateReadGate>) -> Self {
        gate.state.lock().expect("lock").open_readers += 1;
        Self(Arc::clone(gate))
    }
}

impl Drop for StateReadInProgress {
    fn drop(&mut self) {
        let mut state = self.0.state.lock().expect("lock");
        state.open_readers -= 1;
        if std::thread::current().id() != self.0.runtime_thread {
            state.last_blocking_read_ended_at = Some(tokio::time::Instant::now());
        }
    }
}

/// A snapshot reader whose store reads pass the [`StateReadGate`].
struct GatedStateReader {
    state: MemoryState,
    read: StateReadInProgress,
}

impl FlowStateReader for GatedStateReader {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        self.read.0.hold_while_closed();
        self.read.0.panic_if_armed();
        self.state.get(column_family, key)
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        self.read.0.hold_while_closed();
        self.read.0.panic_if_armed();
        self.state.scan(column_family, start, end, limit)
    }
}

/// Page size of the entries gauge's row count, which reads every row by design.
const ROW_COUNT_PAGE_ROWS: usize = 1024;

/// Counts DispatchQueue seeks and pages, except the row count's full pages.
struct CountingStateReader {
    state: MemoryState,
    queue_seeks: Arc<AtomicUsize>,
    committed_read_gate: Arc<StateReadGate>,
    row_count_gate: Arc<StateReadGate>,
    /// Counts the reader as open, so `settle` waits for its blocking read.
    _open_read: StateReadInProgress,
}

impl CountingStateReader {
    fn count_queue_seek(&self, column_family: ColumnFamily, start: &[u8]) {
        if is_dispatch_queue_key(column_family, start) {
            self.queue_seeks.fetch_add(1, Ordering::SeqCst);
        }
    }
}

fn is_dispatch_queue_key(column_family: ColumnFamily, key: &[u8]) -> bool {
    column_family == ColumnFamily::PartitionIndex
        && key.get(2) == Some(&(dexzd_keyspace::IndexKind::DispatchQueue as u8))
}

impl FlowStateReader for CountingStateReader {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        self.committed_read_gate.hold_while_closed();
        self.committed_read_gate.panic_if_armed();
        self.state.get(column_family, key)
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        self.committed_read_gate.hold_while_closed();
        self.committed_read_gate.panic_if_armed();
        if limit < ROW_COUNT_PAGE_ROWS {
            self.count_queue_seek(column_family, start);
        } else if is_dispatch_queue_key(column_family, start) {
            self.row_count_gate.hold_while_closed();
        }
        self.state.scan(column_family, start, end, limit)
    }

    fn seek_first_bounded(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        max_skipped_deletions: u64,
    ) -> Result<dexzd_apply::BoundedRowSeek, StateReadError> {
        self.committed_read_gate.hold_while_closed();
        self.committed_read_gate.panic_if_armed();
        self.count_queue_seek(column_family, start);
        self.state
            .seek_first_bounded(column_family, start, end, max_skipped_deletions)
    }
}

/// Opens the [`StateReadGate`] when dropped.
pub(super) struct OpenStateReadGateOnDrop(Arc<StateReadGate>);

impl OpenStateReadGateOnDrop {
    /// Releases the held reads and waits, in real time, until every one has passed the gate.
    pub(super) fn open_and_wait_for_reads(self) {
        let gate = Arc::clone(&self.0);
        drop(self);
        gate.wait_for_held_reads(0);
    }
}

impl Drop for OpenStateReadGateOnDrop {
    fn drop(&mut self) {
        self.0.open();
    }
}

pub(super) fn is_error_reply(reply: &CommandReply) -> bool {
    matches!(reply.reply, Some(command_reply::Reply::Error(_)))
}

pub(super) fn ok_reply() -> CommandReply {
    CommandReply {
        reply: Some(command_reply::Reply::Ok(EmptyReply {})),
    }
}

pub(super) fn error_reply(message: &str) -> CommandReply {
    CommandReply {
        reply: Some(command_reply::Reply::Error(CommandErrorReply {
            grpc_code: 9,
            message: message.to_owned(),
            ..CommandErrorReply::default()
        })),
    }
}

/// What the fake Worker received for one sent attempt.
#[derive(Debug, Clone)]
pub(super) struct FakeDispatchRequest {
    pub(super) flow_id: String,
    pub(super) flow_type: String,
    pub(super) step_type: String,
    pub(super) sticky_app_worker_id: Option<String>,
    pub(super) app_worker_id: String,
    pub(super) task_id: String,
    pub(super) request: MethodRequest,
    pub(super) deadline: std::time::SystemTime,
}

/// One task the fake dispatcher sent to a Worker.
pub(super) struct FakeDispatch {
    pub(super) request: FakeDispatchRequest,
    events: mpsc::UnboundedSender<StepTaskEvent>,
    pub(super) is_cancelled: Arc<AtomicBool>,
}

impl FakeDispatch {
    pub(super) fn method_request(&self) -> &MethodRequest {
        &self.request.request
    }

    pub(super) fn context(&self) -> dexzd_protocol::dex::Context {
        match self.method_request() {
            MethodRequest::WaitFor(request) => request.context.clone(),
            MethodRequest::Execute(request) => request.context.clone(),
        }
        .expect("requests carry a Context")
    }

    fn send_event(&self, event: StepTaskEvent) {
        // The attempt may already have stopped listening (timeouts, cancellation).
        if self.events.send(event).is_err() {
            tracing::debug!("attempt stopped listening");
        }
    }

    pub(super) fn send_frame(&self, frame: PollWorkerTaskFrame) {
        self.events
            .send(StepTaskEvent::Output(frame))
            .expect("the attempt listens");
    }

    pub(super) fn complete(&self, outcome: DispatchOutcome) {
        self.send_event(StepTaskEvent::Completed(outcome));
    }

    /// The Worker declines the task before any output.
    pub(super) fn reject(
        &self,
        reason: dexzd_protocol::dex::PollWorkerTaskRejectReason,
        is_reserved_slot: bool,
    ) {
        self.send_event(StepTaskEvent::Rejected {
            reason,
            is_reserved_slot,
        });
    }

    /// The Worker cleanly half-closes its reserved slot before any output.
    pub(super) fn half_close_before_output(&self) {
        self.send_event(StepTaskEvent::NotDelivered);
    }

    /// The Worker's stream breaks after the task frame went out.
    pub(super) fn disconnect(&self) {
        self.send_event(StepTaskEvent::Disconnected {
            detail: "Worker stream disconnected".to_owned(),
        });
    }

    pub(super) fn complete_wait_for(&self, response: InvokeWaitForMethodResponse) {
        self.complete(DispatchOutcome::WorkerResult {
            frame: PollWorkerTaskFrame {
                task_id: "task".to_owned(),
                output: Some(poll_worker_task_frame::Output::WaitFor(
                    InvokeWaitForMethodOutput {
                        output: Some(invoke_wait_for_method_output::Output::Result(response)),
                    },
                )),
            },
            app_worker_id: "worker-a".to_owned(),
        });
    }

    pub(super) fn complete_execute(&self, response: InvokeExecuteMethodResponse) {
        self.complete(DispatchOutcome::WorkerResult {
            frame: PollWorkerTaskFrame {
                task_id: "task".to_owned(),
                output: Some(poll_worker_task_frame::Output::Execute(
                    InvokeExecuteMethodOutput {
                        output: Some(invoke_execute_method_output::Output::Result(response)),
                    },
                )),
            },
            app_worker_id: "worker-a".to_owned(),
        });
    }

    pub(super) fn fail_with_worker_error(&self, detail: &str) {
        self.complete(DispatchOutcome::WorkerError {
            frame: PollWorkerTaskFrame {
                task_id: "task".to_owned(),
                output: Some(poll_worker_task_frame::Output::WorkerError(
                    WorkerErrorResponse {
                        detail: detail.to_owned(),
                        error_type: "TestError".to_owned(),
                        ..WorkerErrorResponse::default()
                    },
                )),
            },
            app_worker_id: "worker-a".to_owned(),
        });
    }

    pub(super) fn send_wait_for_heartbeat(&self, value: Option<Value>) {
        self.send_frame(PollWorkerTaskFrame {
            task_id: "task".to_owned(),
            output: Some(poll_worker_task_frame::Output::WaitFor(
                InvokeWaitForMethodOutput {
                    output: Some(invoke_wait_for_method_output::Output::Heartbeat(
                        StepMethodHeartbeat { value },
                    )),
                },
            )),
        });
    }

    /// Sends a Step Stream write frame of the dispatched method.
    pub(super) fn send_stream_write(&self, write: StepStreamWrite) {
        let output = match self.method_request() {
            MethodRequest::WaitFor(_) => {
                poll_worker_task_frame::Output::WaitFor(InvokeWaitForMethodOutput {
                    output: Some(invoke_wait_for_method_output::Output::StreamWrite(write)),
                })
            }
            MethodRequest::Execute(_) => {
                poll_worker_task_frame::Output::Execute(InvokeExecuteMethodOutput {
                    output: Some(invoke_execute_method_output::Output::StreamWrite(write)),
                })
            }
        };
        self.send_frame(PollWorkerTaskFrame {
            task_id: "task".to_owned(),
            output: Some(output),
        });
    }
}

/// Method dispatcher whose Worker `worker-a` takes every attempt at once and hands each sent
/// task to the test.
pub(super) struct FakeDispatcher {
    dispatches: Arc<Mutex<mpsc::UnboundedSender<FakeDispatch>>>,
    receiver: tokio::sync::Mutex<mpsc::UnboundedReceiver<FakeDispatch>>,
    /// Sent tasks.
    pub(super) dispatch_count: Arc<AtomicUsize>,
    /// Reserved Worker slots, including those never sent.
    pub(super) reservation_count: AtomicUsize,
    /// When set, every reservation finds no Worker and the attempt goes to the queue.
    pub(super) refuse_dispatch: AtomicBool,
    /// When set, every reservation reports that matching is closing.
    pub(super) is_closing: AtomicBool,
    /// Sends that fail as never delivered, before later sends succeed.
    pub(super) undelivered_sends: Arc<AtomicUsize>,
    /// Requests of every reservation, in order.
    pub(super) reservations: Mutex<Vec<MethodStreamRequest>>,
    /// Refs of every Queued, in order.
    pub(super) queued_refs: Mutex<Vec<QueuedTaskRef>>,
    /// Heads of every rebuild, in order.
    pub(super) rebuilt_heads: Mutex<Vec<QueuedTaskRef>>,
    /// Replies to the Claims the test injected, in order.
    pub(super) claim_replies: Mutex<Vec<(u64, ClaimReply)>>,
    claims: Mutex<mpsc::UnboundedSender<MethodClaim>>,
    claim_receiver: tokio::sync::Mutex<mpsc::UnboundedReceiver<MethodClaim>>,
    /// Issues periodic Heads tokens and merges scans like production matching.
    pub(super) heads_matching: WorkerDispatch,
    /// Refs that periodic Heads scans restored, summed over scans.
    pub(super) restored_head_count: AtomicUsize,
    /// Partition removals, shared with the slots so a slot can remove its partition.
    pub(super) partition_removals: Arc<FakePartitionRemovals>,
    /// Panics the slots inject once, shared with every slot.
    pub(super) slot_panics: Arc<FakeSlotPanics>,
    /// Ordinary-priority pending times reported behind priority-0 heads, in order.
    pub(super) ordinary_backlog: Mutex<Vec<Option<i64>>>,
}

/// One-shot panics of the next slot call, as a hand-out that hits a bug.
#[derive(Default)]
pub(super) struct FakeSlotPanics {
    /// When set, the next slot asked for its Worker panics, before the attempt starts.
    pub(super) should_panic_at_routing: AtomicBool,
    /// When set, the next send panics, after the attempt started.
    pub(super) should_panic_at_send: AtomicBool,
}

/// Partition removals as matching counts them, and the Claims they ended.
#[derive(Default)]
pub(super) struct FakePartitionRemovals {
    /// Removals of each partition.
    removals: Mutex<BTreeMap<PartitionId, u64>>,
    /// Claims that matching no longer tracks, as after a partition removal.
    pub(super) untracked_claim_ids: Mutex<BTreeSet<u64>>,
    /// When set, the next claimed slot asked for its Worker first removes its partition.
    pub(super) should_remove_before_start: AtomicBool,
}

impl FakePartitionRemovals {
    /// Removes `partition` as matching would, ending its tracked `claim_ids`.
    fn remove(&self, partition: PartitionId, claim_ids: &[u64]) {
        *self
            .removals
            .lock()
            .expect("lock")
            .entry(partition)
            .or_default() += 1;
        self.untracked_claim_ids
            .lock()
            .expect("lock")
            .extend(claim_ids.iter().copied());
    }

    fn removals_of(&self, partition: PartitionId) -> u64 {
        self.removals
            .lock()
            .expect("lock")
            .get(&partition)
            .copied()
            .unwrap_or_default()
    }
}

impl FakeDispatcher {
    fn new() -> Self {
        let (sender, receiver) = mpsc::unbounded_channel();
        let (claim_sender, claim_receiver) = mpsc::unbounded_channel();
        Self {
            dispatches: Arc::new(Mutex::new(sender)),
            receiver: tokio::sync::Mutex::new(receiver),
            dispatch_count: Arc::new(AtomicUsize::new(0)),
            reservation_count: AtomicUsize::new(0),
            refuse_dispatch: AtomicBool::new(false),
            is_closing: AtomicBool::new(false),
            undelivered_sends: Arc::new(AtomicUsize::new(0)),
            reservations: Mutex::new(Vec::new()),
            queued_refs: Mutex::new(Vec::new()),
            rebuilt_heads: Mutex::new(Vec::new()),
            claim_replies: Mutex::new(Vec::new()),
            claims: Mutex::new(claim_sender),
            claim_receiver: tokio::sync::Mutex::new(claim_receiver),
            heads_matching: WorkerDispatch::new(
                dexzd_worker_dispatch::MatchingConfig::default(),
                Arc::new(dexzd_clock::SystemWallClock),
            ),
            restored_head_count: AtomicUsize::new(0),
            partition_removals: Arc::new(FakePartitionRemovals::default()),
            slot_panics: Arc::new(FakeSlotPanics::default()),
            ordinary_backlog: Mutex::new(Vec::new()),
        }
    }

    /// Claims `task` on a slot of the fake Worker, as matching would for a free stream.
    pub(super) fn claim(&self, claim_id: u64, task: QueuedTaskRef) {
        let request = MethodStreamRequest {
            partition: task.partition,
            tenant_id: task.queue.tenant_id.0,
            flow_id: task.flow_id.clone(),
            flow_type: task.queue.flow_type.clone(),
            step_type: task.queue.step_type.clone(),
            sticky_app_worker_id: None,
        };
        let partition_removals = self.removals_of(task.partition);
        let claim = MethodClaim {
            claim_id,
            partition_removals,
            task,
            stream: Box::new(FakeSlot {
                request,
                dispatches: Arc::clone(&self.dispatches),
                dispatch_count: Arc::clone(&self.dispatch_count),
                undelivered_sends: Arc::clone(&self.undelivered_sends),
                claim_id: Some(claim_id),
                partition_removals: Arc::clone(&self.partition_removals),
                panics: Arc::clone(&self.slot_panics),
            }),
        };
        self.claims
            .lock()
            .expect("lock")
            .send(claim)
            .expect("the claim pump holds the receiver");
    }

    /// Removes `partition` as matching would, ending its tracked Claims.
    pub(super) fn remove_partition(&self, partition: PartitionId, claim_ids: &[u64]) {
        self.partition_removals.remove(partition, claim_ids);
    }

    fn removals_of(&self, partition: PartitionId) -> u64 {
        self.partition_removals.removals_of(partition)
    }

    /// Waits for the next dispatch.
    pub(super) async fn next_dispatch(&self) -> FakeDispatch {
        tokio::time::timeout(Duration::from_secs(3600), self.receiver.lock().await.recv())
            .await
            .expect("a dispatch arrives")
            .expect("the dispatcher is alive")
    }

    /// Returns a dispatch if one is waiting.
    pub(super) async fn try_next_dispatch(&self) -> Option<FakeDispatch> {
        self.receiver.lock().await.try_recv().ok()
    }
}

impl MethodDispatcher for FakeDispatcher {
    fn reserve_stream(
        &self,
        request: MethodStreamRequest,
    ) -> BoxFuture<'_, MethodStreamReservation> {
        Box::pin(async move {
            self.reservation_count.fetch_add(1, Ordering::SeqCst);
            self.reservations
                .lock()
                .expect("lock")
                .push(request.clone());
            if self.is_closing.load(Ordering::SeqCst) {
                return MethodStreamReservation::ServerClosing;
            }
            if self.refuse_dispatch.load(Ordering::SeqCst) {
                return MethodStreamReservation::Enqueue;
            }
            MethodStreamReservation::Reserved(Box::new(FakeSlot {
                request,
                dispatches: Arc::clone(&self.dispatches),
                dispatch_count: Arc::clone(&self.dispatch_count),
                undelivered_sends: Arc::clone(&self.undelivered_sends),
                claim_id: None,
                partition_removals: Arc::clone(&self.partition_removals),
                panics: Arc::clone(&self.slot_panics),
            }))
        })
    }

    fn queued(&self, task: QueuedTaskRef) {
        self.queued_refs.lock().expect("lock").push(task);
    }

    fn report_rebuilt_heads(&self, heads: Vec<QueuedTaskRef>) {
        self.rebuilt_heads.lock().expect("lock").extend(heads);
    }

    fn take_heads_token(&self, partition: PartitionId) -> HeadsScanToken {
        self.heads_matching.take_heads_token(partition)
    }

    fn report_scanned_heads(&self, token: HeadsScanToken, results: Vec<ScannedQueueHead>) -> usize {
        let restored = self.heads_matching.report_scanned_heads(token, results);
        self.restored_head_count
            .fetch_add(restored, Ordering::SeqCst);
        restored
    }

    fn next_claim(&self) -> BoxFuture<'_, Option<MethodClaim>> {
        Box::pin(async move { self.claim_receiver.lock().await.recv().await })
    }

    fn reply_to_claim(
        &self,
        _partition: PartitionId,
        _queue: &DispatchQueueName,
        claim_id: u64,
        reply: ClaimReply,
    ) {
        self.claim_replies
            .lock()
            .expect("lock")
            .push((claim_id, reply));
    }

    fn partition_removals(&self, partition: PartitionId) -> u64 {
        self.removals_of(partition)
    }

    fn is_partition_unremoved_since(
        &self,
        partition: PartitionId,
        partition_removals: u64,
    ) -> bool {
        self.removals_of(partition) == partition_removals
    }

    fn is_claim_tracked(
        &self,
        _partition: PartitionId,
        _queue: &DispatchQueueName,
        claim_id: u64,
    ) -> bool {
        !self
            .partition_removals
            .untracked_claim_ids
            .lock()
            .expect("lock")
            .contains(&claim_id)
    }

    fn report_ordinary_backlog(
        &self,
        _partition: PartitionId,
        _queue: &DispatchQueueName,
        pending_since_micros: Option<i64>,
    ) {
        self.ordinary_backlog
            .lock()
            .expect("lock")
            .push(pending_since_micros);
    }

    fn is_waiting_for_supporting_worker(
        &self,
        _tenant_id: u64,
        _flow_type: &str,
        _step_type: &str,
    ) -> bool {
        false
    }

    fn refresh_task_queue_gauges(&self) {}
}

/// A reserved slot of the fake Worker `worker-a`.
struct FakeSlot {
    request: MethodStreamRequest,
    dispatches: Arc<Mutex<mpsc::UnboundedSender<FakeDispatch>>>,
    dispatch_count: Arc<AtomicUsize>,
    undelivered_sends: Arc<AtomicUsize>,
    /// The Claim that delivered this slot, if any.
    claim_id: Option<u64>,
    partition_removals: Arc<FakePartitionRemovals>,
    panics: Arc<FakeSlotPanics>,
}

impl ReservedMethodStream for FakeSlot {
    fn app_worker_id(&self) -> &str {
        // The hand-out asks for the Worker after the Claim's checks and before its start.
        if let Some(claim_id) = self.claim_id
            && self
                .partition_removals
                .should_remove_before_start
                .swap(false, Ordering::SeqCst)
        {
            self.partition_removals
                .remove(self.request.partition, &[claim_id]);
        }
        if self
            .panics
            .should_panic_at_routing
            .swap(false, Ordering::SeqCst)
        {
            panic!("injected panic at routing");
        }
        "worker-a"
    }

    fn may_hand_out_tasks(&self) -> bool {
        true
    }

    fn send_task(
        self: Box<Self>,
        task: MethodTask,
    ) -> Result<Box<dyn MethodDispatchSession>, StepTaskSendFailure> {
        if self
            .panics
            .should_panic_at_send
            .swap(false, Ordering::SeqCst)
        {
            panic!("injected panic at send");
        }
        if self
            .undelivered_sends
            .fetch_update(Ordering::SeqCst, Ordering::SeqCst, |count| {
                count.checked_sub(1)
            })
            .is_ok()
        {
            return Err(StepTaskSendFailure::NotDelivered);
        }
        self.dispatch_count.fetch_add(1, Ordering::SeqCst);
        let (events, receiver) = mpsc::unbounded_channel();
        let is_cancelled = Arc::new(AtomicBool::new(false));
        let dispatch = FakeDispatch {
            request: FakeDispatchRequest {
                flow_id: self.request.flow_id.clone(),
                flow_type: self.request.flow_type.clone(),
                step_type: self.request.step_type.clone(),
                sticky_app_worker_id: self.request.sticky_app_worker_id.clone(),
                app_worker_id: "worker-a".to_owned(),
                task_id: task.task_id,
                request: task.request,
                deadline: task.deadline,
            },
            events,
            is_cancelled: Arc::clone(&is_cancelled),
        };
        self.dispatches
            .lock()
            .expect("lock")
            .send(dispatch)
            .expect("the test holds the receiver");
        Ok(Box::new(FakeSession {
            receiver,
            is_cancelled,
        }))
    }
}

struct FakeSession {
    receiver: mpsc::UnboundedReceiver<StepTaskEvent>,
    is_cancelled: Arc<AtomicBool>,
}

impl MethodDispatchSession for FakeSession {
    fn next_event(&mut self) -> BoxFuture<'_, Option<StepTaskEvent>> {
        Box::pin(self.receiver.recv())
    }

    fn cancel(&self) {
        self.is_cancelled.store(true, Ordering::SeqCst);
    }
}

/// Everything a node keeps across a restart: committed state, objects, and the local store.
#[derive(Clone)]
pub(super) struct DurableState {
    pub(super) state: Arc<Mutex<MemoryState>>,
    pub(super) objects: Arc<InMemoryObjectStore>,
    pub(super) local_store: Arc<InMemoryLocalStore>,
    pub(super) clock: Arc<TokioWallClock>,
}

impl DurableState {
    pub(super) fn new() -> Self {
        Self {
            state: Arc::new(Mutex::new(MemoryState::default())),
            objects: Arc::new(InMemoryObjectStore::new()),
            local_store: Arc::new(InMemoryLocalStore::new()),
            clock: Arc::new(TokioWallClock::new()),
        }
    }
}

/// One running node: fake runtime, effect runner, and fake Worker dispatch.
pub(super) struct EffectHarness {
    pub(super) durable: DurableState,
    pub(super) flow_effect_operations: Arc<InMemoryFlowEffectOperations>,
    pub(super) dispatcher: Arc<FakeDispatcher>,
    pub(super) runner: EffectRunner,
    pub(super) hard_delete_objects: HardDeleteObjects,
    pub(super) hasher: PartitionHasher,
    pub(super) clock: Arc<TokioWallClock>,
    pub(super) stream_store: Arc<StreamStore>,
    pub(super) attribute_sync_waker: AttributeSyncWaker,
    pub(super) metrics: Arc<dexzd_metrics::NodeMetrics>,
    tasks: EffectTasksHandle,
}

impl EffectHarness {
    /// Returns the effect state the running tasks share, such as attempt entries.
    pub(super) fn effect_context(&self) -> &Arc<crate::effects::EffectContext> {
        self.tasks.context()
    }
}

impl EffectHarness {
    pub(super) fn new() -> Self {
        Self::with_config(DurableState::new(), EffectsConfig::default())
    }

    pub(super) fn with_config(durable: DurableState, config: EffectsConfig) -> Self {
        let ledger = Arc::new(NodeMetaUploadLedger::new(
            Arc::clone(&durable.local_store) as _,
            Arc::new(Mutex::new(())),
        ));
        Self::with_ledger(durable, config, ledger)
    }

    pub(super) fn with_ledger(
        durable: DurableState,
        config: EffectsConfig,
        ledger: Arc<dyn HardDeleteUploadLedger>,
    ) -> Self {
        Self::with_validation(
            durable,
            config,
            ledger,
            ValidationConfig {
                has_blob_store: true,
                ..ValidationConfig::default()
            },
        )
    }

    pub(super) fn with_validation(
        durable: DurableState,
        config: EffectsConfig,
        ledger: Arc<dyn HardDeleteUploadLedger>,
        validation_config: ValidationConfig,
    ) -> Self {
        let hasher = PartitionHasher::new(&[7; 16], 16).expect("valid partition hasher");
        let clock = Arc::clone(&durable.clock);
        let hard_delete_objects = HardDeleteObjects::new(
            Arc::clone(&durable.objects) as _,
            ledger,
            Arc::clone(&clock) as _,
            hasher.clone(),
            Arc::new(SystemEngineIdSource),
        );
        let dispatcher = Arc::new(FakeDispatcher::new());
        let metrics = Arc::new(dexzd_metrics::NodeMetrics::unregistered_with_partitions(
            usize::try_from(hasher.partition_count()).expect("a partition count fits in usize"),
        ));
        let attribute_sync_waker = AttributeSyncWaker::new();
        let stream_store = Arc::new(
            StreamStore::new(&StreamStoreConfig {
                enabled: true,
                ..StreamStoreConfig::default()
            })
            .expect("the Stream Store configuration is valid"),
        );
        let (runner, tasks) = EffectRunner::new(
            &config,
            EffectDependencies {
                metrics: Arc::clone(&metrics),
                dispatcher: Arc::clone(&dispatcher) as _,
                hard_delete_objects: hard_delete_objects.clone(),
                history_object_store: Arc::clone(&durable.objects) as _,
                partition_hasher: hasher.clone(),
                clock: Arc::clone(&clock) as _,
                validation_config,
                stream_store: Arc::clone(&stream_store),
                id_source: Arc::new(SystemEngineIdSource),
                attribute_sync_waker: attribute_sync_waker.clone(),
            },
        );
        let flow_effect_operations = Arc::new(InMemoryFlowEffectOperations {
            state: Arc::clone(&durable.state),
            hasher: hasher.clone(),
            clock: Arc::clone(&clock),
            sink: runner.clone(),
            last_proposed_micros: Mutex::new(0),
            commands: Mutex::new(Vec::new()),
            published_effects: Mutex::new(Vec::new()),
            busy_lock_keys: Mutex::new(BTreeSet::new()),
            start_commit_delay: Mutex::new(Duration::ZERO),
            should_hold_next_start: AtomicBool::new(false),
            held_commits: Mutex::new(None),
            held_commits_released: tokio::sync::watch::Sender::new(false),
            starts_finding_busy_locks: AtomicUsize::new(0),
            unavailable_proposals: AtomicUsize::new(0),
            is_flow_state_unreadable: AtomicBool::new(false),
            flow_state_reader_failures: Arc::new(AtomicUsize::new(0)),
            is_flow_state_unreadable_after_start: AtomicBool::new(false),
            state_read_gate: Arc::new(StateReadGate::new()),
            committed_read_gate: Arc::new(StateReadGate::new()),
            row_count_gate: Arc::new(StateReadGate::new()),
            committed_reads: AtomicUsize::new(0),
            committed_reader_failures: AtomicUsize::new(0),
            committed_queue_seeks: Arc::new(AtomicUsize::new(0)),
            committed_flow_reads: AtomicUsize::new(0),
            proposal_delay: Mutex::new(Duration::ZERO),
            overrides: Mutex::new(Vec::new()),
            transactional_rpcs: tokio::sync::watch::Sender::new(0),
        });
        let tasks = tasks.start(Arc::clone(&flow_effect_operations) as _);
        Self {
            durable,
            flow_effect_operations,
            dispatcher,
            runner,
            hard_delete_objects,
            hasher,
            clock,
            stream_store,
            attribute_sync_waker,
            metrics,
            tasks,
        }
    }

    pub(super) fn flow(flow_id: &str) -> FlowAddress {
        FlowAddress::new(TenantId::DEFAULT, flow_id)
    }

    pub(super) fn partition_of(&self, flow_id: &str) -> PartitionId {
        self.hasher.partition_of_flow(TenantId::DEFAULT, flow_id)
    }

    pub(super) fn run_location(&self, flow_id: &str, run_id: RunId) -> RunLocation {
        RunLocation {
            flow_hash: self.hasher.flow_hash(TenantId::DEFAULT, flow_id),
            run_id,
        }
    }

    /// Starts every partition's timer service and rebuilds its work.
    pub(super) async fn rebuild_all_partitions(&self) {
        self.try_rebuild_all_partitions()
            .await
            .expect("rebuild succeeds");
    }

    /// Rebuilds every partition and returns the first failure.
    pub(super) async fn try_rebuild_all_partitions(&self) -> Result<(), EffectError> {
        for partition in 0..self.hasher.partition_count() {
            let partition = PartitionId(u16::try_from(partition).expect("fits"));
            self.runner.rebuild_partition(partition).await?;
        }
        Ok(())
    }

    /// Starts a Flow and returns its run ID.
    pub(super) fn start_flow(&self, request: StartFlowRequest, settings: RunSettings) -> RunId {
        self.start_flow_with_timeout_policy(request, settings, FlowTimeoutPolicy::Fail)
    }

    pub(super) fn start_flow_with_timeout_policy(
        &self,
        request: StartFlowRequest,
        settings: RunSettings,
        timeout_policy: FlowTimeoutPolicy,
    ) -> RunId {
        let flow = Self::flow(&request.flow_id);
        let new_run_id = uuid::Uuid::now_v7().as_bytes().to_vec();
        let command = FlowCommand {
            flow_id: request.flow_id.clone(),
            tenant_id: 0,
            command: Some(flow_command::Command::FlowStart(FlowStartCommand {
                new_run_id: new_run_id.clone(),
                request: Some(request),
                settings: Some(settings),
                resolved_timeout_policy: timeout_policy as i32,
                worker_target: Some(WorkerTarget { is_pull_mode: true }),
                ..FlowStartCommand::default()
            })),
            ..FlowCommand::default()
        };
        let reply = self
            .flow_effect_operations
            .commit(&flow, command, CommitUrgency::Prompt)
            .expect("start commits");
        assert!(!is_error_reply(&reply), "start failed: {reply:?}");
        RunId::from_slice(&new_run_id).expect("16 bytes")
    }

    pub(super) fn run_record(&self, flow_id: &str, run_id: RunId) -> RunRecord {
        let run = self.run_location(flow_id, run_id);
        let key = run_singleton_key(run.flow_hash, run.run_id, RunTable::RunRecord);
        let bytes = self
            .flow_effect_operations
            .snapshot()
            .get(ColumnFamily::Flow, &key)
            .expect("memory reads never fail")
            .expect("the run record exists");
        RunRecord::decode(bytes.as_slice()).expect("the run record decodes")
    }

    pub(super) fn step_execution(
        &self,
        flow_id: &str,
        run_id: RunId,
        step_execution_id: &str,
    ) -> StepExecutionRecord {
        read_step_execution(
            &self.flow_effect_operations.snapshot(),
            self.run_location(flow_id, run_id),
            step_execution_id,
        )
        .expect("state reads succeed")
        .expect("the Step execution exists")
    }

    /// Lets every runnable task proceed without moving time forward much.
    pub(super) async fn settle(&self) {
        self.yield_to_tasks().await;
        while self
            .flow_effect_operations
            .state_read_gate
            .may_have_unconsumed_reads()
        {
            // Paused time cannot auto-advance during a blocking state read, so this waits for reads.
            tokio::time::sleep(Duration::from_millis(1)).await;
            self.yield_to_tasks().await;
        }
    }

    /// Lets runnable tasks proceed without waiting for state reads on blocking threads.
    pub(super) async fn yield_to_tasks(&self) {
        for _ in 0..50 {
            tokio::task::yield_now().await;
        }
    }

    /// Begins a Step attempt drain, as a planned stop does after matching closes.
    pub(super) fn begin_attempt_drain(&self) -> watch::Receiver<AttemptDrainProgress> {
        self.tasks
            .begin_attempt_drain()
            .expect("the effect router runs")
    }

    pub(super) async fn shutdown(self) {
        self.tasks.shutdown().await;
        self.stream_store
            .close()
            .await
            .expect("the Stream Store closes");
    }
}

pub(super) fn settings() -> RunSettings {
    RunSettings {
        history_window_events: 10_000,
        history_prune_chunk_events: 1000,
        max_retained_superseded_bytes: 256 << 20,
        retention_after_close_micros: 7 * 24 * 3600 * SECOND_MICROS,
        wire_inline_threshold_bytes: 100,
        max_value_bytes: 16 << 20,
        checkpoint_row_write_bytes_threshold: 1 << 20,
        checkpoint_command_threshold: 10_000,
        request_dedup_capacity: 1000,
        max_run_state_bytes: 1 << 30,
        max_active_step_executions: 10_000,
        max_child_flow_result_bytes: 16 << 20,
        history_segment_target_bytes: 4 << 20,
    }
}

pub(super) fn start_request(flow_id: &str) -> StartFlowRequest {
    StartFlowRequest {
        flow_id: flow_id.to_owned(),
        flow_type: "OrderFlow".to_owned(),
        start_step_type: "ChargeStep".to_owned(),
        step_input: Some(string_value("small input")),
        request_id: format!("request-{flow_id}"),
        ..StartFlowRequest::default()
    }
}

pub(super) fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}

pub(super) fn is_attempt_failed(command: &flow_command::Command) -> bool {
    matches!(command, flow_command::Command::StepAttemptFailed(_))
}

pub(super) fn is_method_completed(command: &flow_command::Command) -> bool {
    matches!(command, flow_command::Command::StepMethodCompleted(_))
}

pub(super) fn is_timers_fired(command: &flow_command::Command) -> bool {
    matches!(command, flow_command::Command::TimersFired(_))
}

pub(super) fn ok_override(
    predicate: impl Fn(&flow_command::Command) -> bool + Send + Sync + 'static,
) -> CommandOverride {
    Box::new(move |command, _| {
        command
            .command
            .as_ref()
            .is_some_and(&predicate)
            .then(ok_reply)
    })
}

/// Applies each `actions` entry's action to its operation's calls on keys under `key_prefix`
/// while enabled.
pub(super) struct SwitchableFault {
    pub(super) is_enabled: AtomicBool,
    pub(super) actions: Vec<(ObjectStoreOperation, FaultAction)>,
    pub(super) key_prefix: &'static str,
}

impl SwitchableFault {
    /// Returns a durable state whose object store consults a disabled fault.
    pub(super) fn install(
        operations: Vec<ObjectStoreOperation>,
        action: FaultAction,
    ) -> (DurableState, Arc<Self>) {
        Self::install_actions(
            "hard-delete/",
            operations
                .into_iter()
                .map(|operation| (operation, action))
                .collect(),
        )
    }

    /// Like [`Self::install`], with an action per operation. Batch deletes pass an empty key,
    /// so only an empty `key_prefix` faults them.
    pub(super) fn install_actions(
        key_prefix: &'static str,
        actions: Vec<(ObjectStoreOperation, FaultAction)>,
    ) -> (DurableState, Arc<Self>) {
        let fault = Arc::new(Self {
            is_enabled: AtomicBool::new(false),
            actions,
            key_prefix,
        });
        let mut durable = DurableState::new();
        durable.objects = Arc::new(InMemoryObjectStore::with_injector(
            Arc::clone(&fault) as Arc<dyn FaultInjector>
        ));
        (durable, fault)
    }

    pub(super) fn set_enabled(&self, is_enabled: bool) {
        self.is_enabled.store(is_enabled, Ordering::SeqCst);
    }
}

impl FaultInjector for SwitchableFault {
    fn action(&self, operation: ObjectStoreOperation, key: &str) -> FaultAction {
        if !self.is_enabled.load(Ordering::SeqCst) || !key.starts_with(self.key_prefix) {
            return FaultAction::Proceed;
        }
        self.actions
            .iter()
            .find(|(faulted, _)| *faulted == operation)
            .map_or(FaultAction::Proceed, |(_, action)| *action)
    }
}
