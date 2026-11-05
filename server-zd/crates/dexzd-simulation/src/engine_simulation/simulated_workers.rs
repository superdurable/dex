// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Seeded pull Workers behind the engine's dispatch ports, without the matching layer.
//!
//! A reservation stands for an idle reserved Worker slot; the drawn behavior plays once the task
//! frame is sent.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use dexzd_engine::{
    MethodClaim, MethodDispatchSession, MethodDispatcher, MethodRequest, MethodStreamRequest,
    MethodStreamReservation, MethodTask, ReservedMethodStream, RpcInvokeError, RpcWorkerInvoker,
    StepQueueStatus, WorkerRpcInvocation, WorkerRpcResult,
};
use dexzd_keyspace::PartitionId;
use dexzd_keyspace::dispatch_queue_keys::DispatchQueueName;
use dexzd_protocol::dex::{
    Context, InvokeExecuteMethodOutput, InvokeWaitForMethodOutput, PollWorkerTaskFrame,
    PollWorkerTaskRejectReason, StepMethodHeartbeat, invoke_execute_method_output,
    invoke_wait_for_method_output, poll_worker_task_frame,
};
use dexzd_worker_dispatch::{
    ClaimReply, DispatchOutcome, HeadsScanToken, QueuedTaskRef, ScannedQueueHead, StepTaskEvent,
    StepTaskSendFailure, WorkerDispatch,
};
use futures::future::BoxFuture;
use rand::{RngExt, SeedableRng};
use rand_chacha::ChaCha8Rng;
use tokio::sync::{mpsc, watch};

use crate::engine_simulation::event_trace::EventTrace;
use crate::engine_simulation::simulated_clients::ENGINE_FLOW_IDS;
use crate::engine_simulation::workload::{
    AWAIT_CHILD_RESULT_RPC_NAME, MethodCallView, execute_response, heartbeat_value, rpc_response,
    wait_for_response, worker_error,
};
use simulated_task_queue::SimulatedTaskQueue;

mod simulated_task_queue;

/// New Steps each run's Workers may create, so every run eventually closes.
const STEPS_PER_RUN: u32 = 8;
/// Percent of Claim replies and Claims that draw a queue fault outside calm and hold.
const QUEUE_FAULT_PERCENT: u32 = 20;
/// Separates queue fault draws from Worker draws, so other trajectories keep their draws.
const QUEUE_FAULT_SEED_SALT: u64 = 0x7a51_c0de;

/// One method attempt as the Worker sees it.
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord)]
pub(crate) struct AttemptIdentity {
    pub(crate) flow_id: String,
    pub(crate) run_id: String,
    pub(crate) run_attempt: i32,
    pub(crate) step_execution_id: String,
    pub(crate) is_execute: bool,
    pub(crate) attempt: i32,
}

impl AttemptIdentity {
    fn of(request: &MethodRequest) -> Self {
        let (context, is_execute) = match request {
            MethodRequest::WaitFor(request) => (request.context.as_ref(), false),
            MethodRequest::Execute(request) => (request.context.as_ref(), true),
        };
        let context = context.cloned().unwrap_or_else(Context::default);
        Self {
            flow_id: context.flow_id,
            run_id: context.run_id,
            run_attempt: context.run_attempt,
            step_execution_id: context.step_execution_id,
            is_execute,
            attempt: context.attempt,
        }
    }
}

/// One task frame a Worker received.
#[derive(Debug, Clone)]
pub(crate) struct TaskFrameRecord {
    pub(crate) task_id: String,
    pub(crate) is_sync: bool,
    /// False when the Worker declined or half-closed before running the task.
    pub(crate) has_possibly_run: bool,
}

/// How a Worker handles one dispatch.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub(crate) enum WorkerBehavior {
    Succeed,
    Fail,
    /// Never answers; the method or heartbeat timeout ends the attempt.
    Hang,
    /// Never answers and sends no heartbeat, while the simulator compares dispatches.
    Hold,
    Slow,
    /// Returns the result it already returned for the same attempt.
    RepeatResult,
    /// The stream breaks after the task frame, before any output.
    DisconnectAfterHandOut,
    DisconnectAfterOutput,
    /// The Worker declines the Step type before running it.
    Unsupported,
    /// A Worker without reserved-slot dispatch declines the task for capacity.
    LegacyNoCapacity,
    /// The Worker cleanly half-closes its slot before running the task.
    HalfCloseBeforeOutput,
    /// The reservation finds no Worker within the dispatch wait.
    NoWorker,
    RpcSucceed,
    RpcFail,
    RpcTimeout,
    /// A heartbeat frame that carries a checkpoint value.
    HeartbeatWithValue,
    /// A heartbeat frame without a value, which clears the checkpoint.
    HeartbeatWithoutValue,
    /// Matching receives the claimed ref again, like a late Queued, so it is claimed twice.
    DuplicateQueueRef,
    /// The claimed stream may not hand out tasks, so the owner answers "retry later".
    ClaimWithoutLease,
}

/// One timed event of a scripted dispatch; `None` waits until the dispatch is canceled.
struct ScriptedEvent {
    /// Whether the delay starts only once the pool stops holding.
    should_wait_for_release: bool,
    delay: Duration,
    event: Option<StepTaskEvent>,
}

struct WorkerPoolState {
    rng: ChaCha8Rng,
    queue_fault_rng: ChaCha8Rng,
    /// Whether new Steps carry failure policies, as in the Step failure profile.
    has_step_failure_policies: bool,
    /// Whether queue faults may be drawn at all.
    has_queue_faults: bool,
    is_calm: bool,
    is_holding: bool,
    dispatch_history: BTreeMap<String, Vec<AttemptIdentity>>,
    /// Every task frame of every process, by attempt.
    task_frames: BTreeMap<AttemptIdentity, Vec<TaskFrameRecord>>,
    returned_results: BTreeMap<AttemptIdentity, PollWorkerTaskFrame>,
    steps_left: BTreeMap<(String, String), u32>,
    /// WaitFor results that wait for a child Flow, as (Flow ID, run ID, Step execution ID).
    child_waits: BTreeSet<(String, String, String)>,
    behaviors: BTreeMap<WorkerBehavior, u64>,
}

/// The Worker pool every node process dispatches to.
pub(crate) struct SimulatedWorkers {
    state: Mutex<WorkerPoolState>,
    /// True while held Workers keep their tasks; they answer once it turns false.
    hold: watch::Sender<bool>,
    /// Bumped whenever a child wait ends, so RPCs held for it check again.
    child_waits_ended: watch::Sender<u64>,
    trace: Arc<EventTrace>,
    /// Issues periodic Heads tokens; the simulated queues hold the refs themselves.
    heads_matching: WorkerDispatch,
}

impl SimulatedWorkers {
    pub(crate) fn new(
        seed: u64,
        has_step_failure_policies: bool,
        has_queue_faults: bool,
        trace: Arc<EventTrace>,
    ) -> Self {
        Self {
            heads_matching: WorkerDispatch::new(
                dexzd_worker_dispatch::MatchingConfig::default(),
                Arc::new(dexzd_clock::SystemWallClock),
            ),
            state: Mutex::new(WorkerPoolState {
                rng: ChaCha8Rng::seed_from_u64(seed),
                queue_fault_rng: ChaCha8Rng::seed_from_u64(seed ^ QUEUE_FAULT_SEED_SALT),
                has_step_failure_policies,
                has_queue_faults,
                is_calm: false,
                is_holding: false,
                dispatch_history: BTreeMap::new(),
                task_frames: BTreeMap::new(),
                returned_results: BTreeMap::new(),
                steps_left: BTreeMap::new(),
                child_waits: BTreeSet::new(),
                behaviors: BTreeMap::new(),
            }),
            hold: watch::Sender::new(false),
            child_waits_ended: watch::Sender::new(0),
            trace,
        }
    }

    /// In calm mode every Worker answers promptly and successfully.
    pub(crate) fn set_calm(&self, is_calm: bool) {
        self.lock().is_calm = is_calm;
    }

    /// While holding, new tasks get Workers that answer only after the hold ends.
    pub(crate) fn set_holding(&self, is_holding: bool) {
        self.lock().is_holding = is_holding;
        self.hold.send_replace(is_holding);
    }

    /// Returns every attempt `process_label` dispatched, in dispatch order.
    pub(crate) fn dispatch_history(&self, process_label: &str) -> Vec<AttemptIdentity> {
        self.lock()
            .dispatch_history
            .get(process_label)
            .cloned()
            .unwrap_or_default()
    }

    /// Returns every task frame a Worker received, by attempt, across all processes.
    pub(crate) fn task_frames(&self) -> BTreeMap<AttemptIdentity, Vec<TaskFrameRecord>> {
        self.lock().task_frames.clone()
    }

    /// Draws whether the queue fault `behavior` happens now, and counts it when it does.
    fn draw_queue_fault(&self, behavior: WorkerBehavior) -> bool {
        let mut state = self.lock();
        if !state.has_queue_faults || state.is_calm || state.is_holding {
            return false;
        }
        let is_drawn = state.queue_fault_rng.random_range(0..100) < QUEUE_FAULT_PERCENT;
        if is_drawn {
            *state.behaviors.entry(behavior).or_default() += 1;
        }
        is_drawn
    }

    pub(crate) fn behavior_counts(&self) -> BTreeMap<WorkerBehavior, u64> {
        self.lock().behaviors.clone()
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, WorkerPoolState> {
        self.state.lock().expect("worker pool lock is not poisoned")
    }

    /// Returns the dispatch port of one node process.
    pub(crate) fn dispatcher_for(self: &Arc<Self>, process_label: &str) -> Arc<NodeWorkerPort> {
        Arc::new(NodeWorkerPort {
            queue: SimulatedTaskQueue::default(),
            workers: Arc::clone(self),
            process_label: process_label.to_owned(),
        })
    }

    /// Draws whether any Worker takes the reservation; without one, it ends after a delay.
    async fn reserve(self: &Arc<Self>, process_label: &str) -> MethodStreamReservation {
        let delay = {
            let mut state = self.lock();
            let has_no_worker =
                !state.is_calm && !state.is_holding && state.rng.random_range(0..100) < 3;
            if !has_no_worker {
                None
            } else {
                *state.behaviors.entry(WorkerBehavior::NoWorker).or_default() += 1;
                Some(Duration::from_millis(state.rng.random_range(10..1_000)))
            }
        };
        let Some(delay) = delay else {
            return MethodStreamReservation::Reserved(Box::new(SimulatedSlot {
                workers: Arc::clone(self),
                process_label: process_label.to_owned(),
                may_hand_out_tasks: true,
            }));
        };
        self.trace
            .record(&format!("{process_label} reservation found no Worker"));
        tokio::time::sleep(delay).await;
        MethodStreamReservation::Enqueue
    }

    fn start_dispatch(
        self: &Arc<Self>,
        process_label: &str,
        task: &MethodTask,
    ) -> Result<Box<dyn MethodDispatchSession>, StepTaskSendFailure> {
        let request = &task.request;
        let identity = AttemptIdentity::of(request);
        let mut state = self.lock();
        let behavior = state.draw_behavior(&identity);
        *state.behaviors.entry(behavior).or_default() += 1;
        self.trace.record(&format!(
            "{process_label} dispatch {} run {} step {} execute={} attempt {} {behavior:?}",
            identity.flow_id,
            identity.run_id,
            identity.step_execution_id,
            identity.is_execute,
            identity.attempt
        ));
        if behavior == WorkerBehavior::HalfCloseBeforeOutput && state.rng.random_bool(0.5) {
            // The slot half-closed before the frame went out.
            return Err(StepTaskSendFailure::NotDelivered);
        }
        state
            .dispatch_history
            .entry(process_label.to_owned())
            .or_default()
            .push(identity.clone());
        let has_possibly_run = !matches!(
            behavior,
            WorkerBehavior::Unsupported
                | WorkerBehavior::LegacyNoCapacity
                | WorkerBehavior::HalfCloseBeforeOutput
        );
        state
            .task_frames
            .entry(identity.clone())
            .or_default()
            .push(TaskFrameRecord {
                task_id: task.task_id.clone(),
                is_sync: task.is_sync,
                has_possibly_run,
            });
        // An Execute dispatch means the WaitFor condition, such as a child result, was met.
        if identity.is_execute
            && state.child_waits.remove(&(
                identity.flow_id.clone(),
                identity.run_id.clone(),
                identity.step_execution_id.clone(),
            ))
        {
            self.child_waits_ended
                .send_modify(|generation| *generation += 1);
        }
        let script = state.script(behavior, &identity, request);
        drop(state);
        let (event_sender, event_receiver) = mpsc::channel(script.len().max(1));
        let (cancel_sender, cancel_receiver) = watch::channel(false);
        let worker_task = tokio::spawn(play_script(
            script,
            event_sender,
            cancel_receiver,
            self.hold.subscribe(),
        ));
        // The session owns the task: dropping the session ends the script.
        Ok(Box::new(SimulatedDispatchSession {
            events: event_receiver,
            cancel: cancel_sender,
            worker_task,
        }))
    }

    async fn invoke_rpc(
        &self,
        process_label: &str,
        invocation: WorkerRpcInvocation,
    ) -> Result<WorkerRpcResult, RpcInvokeError> {
        let (behavior, delay, outcome) = {
            let mut state = self.lock();
            let rng = &mut state.rng;
            let behavior = match rng.random_range(0..20) {
                _ if state.is_calm => WorkerBehavior::RpcSucceed,
                0..=1 => WorkerBehavior::RpcFail,
                2 => WorkerBehavior::RpcTimeout,
                _ => WorkerBehavior::RpcSucceed,
            };
            let rng = &mut state.rng;
            let delay = Duration::from_millis(rng.random_range(1..500));
            let outcome = match behavior {
                WorkerBehavior::RpcFail => Err(RpcInvokeError::WorkerError {
                    response: worker_error(rng),
                }),
                WorkerBehavior::RpcTimeout => Err(RpcInvokeError::DeadlineExceeded),
                _ => Ok(WorkerRpcResult {
                    response: rpc_response(rng),
                    app_worker_id: format!("worker-{}", rng.random_range(0..3)),
                    pull_routing: None,
                }),
            };
            *state.behaviors.entry(behavior).or_default() += 1;
            (behavior, delay, outcome)
        };
        self.trace.record(&format!(
            "{process_label} rpc {} run {} {behavior:?}",
            invocation.flow.flow_id, invocation.run_id
        ));
        if behavior == WorkerBehavior::RpcTimeout {
            tokio::time::sleep_until(invocation.deadline).await;
            return outcome;
        }
        self.hold_rpc_during_child_wait(&invocation).await;
        tokio::time::sleep(delay).await;
        outcome
    }

    /// Holds a child-wait probe RPC until its run's child wait ends or its deadline passes.
    async fn hold_rpc_during_child_wait(&self, invocation: &WorkerRpcInvocation) {
        if invocation.request.rpc_name != AWAIT_CHILD_RESULT_RPC_NAME {
            return;
        }
        let flow_id = invocation.flow.flow_id.as_str();
        let run_id = invocation.run_id.to_string();
        let mut child_waits_ended = self.child_waits_ended.subscribe();
        while self.lock().is_waiting_for_child(flow_id, &run_id) {
            // Biased for reproducible simulation; the deadline ends the hold.
            tokio::select! {
                biased;
                () = tokio::time::sleep_until(invocation.deadline) => return,
                changed = child_waits_ended.changed() => {
                    if changed.is_err() {
                        return;
                    }
                }
            }
        }
    }

    /// Returns a run of `flow_id` whose WaitFor result waits for a child Flow, if any.
    pub(crate) fn run_waiting_for_child(&self, flow_id: &str) -> Option<String> {
        self.lock()
            .child_waits
            .iter()
            .find(|(wait_flow_id, _, _)| wait_flow_id == flow_id)
            .map(|(_, run_id, _)| run_id.clone())
    }

    /// Forgets the child waits of a run that no longer exists or is closed.
    pub(crate) fn end_child_waits_of_run(&self, flow_id: &str, run_id: &str) {
        self.lock()
            .child_waits
            .retain(|(wait_flow_id, wait_run_id, _)| {
                wait_flow_id != flow_id || wait_run_id != run_id
            });
        self.child_waits_ended
            .send_modify(|generation| *generation += 1);
    }
}

impl WorkerPoolState {
    fn is_waiting_for_child(&self, flow_id: &str, run_id: &str) -> bool {
        self.child_waits
            .iter()
            .any(|(wait_flow_id, wait_run_id, _)| wait_flow_id == flow_id && wait_run_id == run_id)
    }

    fn draw_behavior(&mut self, identity: &AttemptIdentity) -> WorkerBehavior {
        if self.is_holding {
            return WorkerBehavior::Hold;
        }
        if self.is_calm {
            return WorkerBehavior::Succeed;
        }
        let has_result = self.returned_results.contains_key(identity);
        match self.rng.random_range(0..100) {
            0..=54 => WorkerBehavior::Succeed,
            55..=64 => WorkerBehavior::Fail,
            65..=69 => WorkerBehavior::Hang,
            70..=77 => WorkerBehavior::Slow,
            78..=84 if has_result => WorkerBehavior::RepeatResult,
            78..=84 => WorkerBehavior::Succeed,
            85..=87 => WorkerBehavior::DisconnectAfterHandOut,
            88..=91 => WorkerBehavior::DisconnectAfterOutput,
            92..=94 => WorkerBehavior::Unsupported,
            95..=96 => WorkerBehavior::LegacyNoCapacity,
            _ => WorkerBehavior::HalfCloseBeforeOutput,
        }
    }

    fn script(
        &mut self,
        behavior: WorkerBehavior,
        identity: &AttemptIdentity,
        request: &MethodRequest,
    ) -> Vec<ScriptedEvent> {
        let worker_id = format!("worker-{}", self.rng.random_range(0..3));
        let mut script = Vec::new();
        match behavior {
            WorkerBehavior::Succeed | WorkerBehavior::Slow => {
                let is_slow = behavior == WorkerBehavior::Slow;
                self.push_heartbeats(&mut script, request, is_slow);
                let delay = if is_slow {
                    Duration::from_millis(self.rng.random_range(2_000..25_000))
                } else {
                    Duration::from_millis(self.rng.random_range(10..1_500))
                };
                script.push(self.result_event(delay, identity, request, worker_id));
            }
            WorkerBehavior::RepeatResult => {
                let frame = self
                    .returned_results
                    .get(identity)
                    .cloned()
                    .expect("RepeatResult is drawn only for attempts with a returned result");
                script.push(ScriptedEvent {
                    should_wait_for_release: false,
                    delay: Duration::from_millis(self.rng.random_range(1..300)),
                    event: Some(StepTaskEvent::Completed(DispatchOutcome::WorkerResult {
                        frame,
                        app_worker_id: worker_id,
                    })),
                });
            }
            WorkerBehavior::Fail => {
                script.push(ScriptedEvent {
                    should_wait_for_release: false,
                    delay: Duration::from_millis(self.rng.random_range(10..1_000)),
                    event: Some(StepTaskEvent::Completed(DispatchOutcome::WorkerError {
                        frame: PollWorkerTaskFrame {
                            task_id: String::new(),
                            output: Some(poll_worker_task_frame::Output::WorkerError(
                                worker_error(&mut self.rng),
                            )),
                        },
                        app_worker_id: worker_id,
                    })),
                });
            }
            WorkerBehavior::Hang => {
                self.push_heartbeats(&mut script, request, false);
                script.push(ScriptedEvent {
                    should_wait_for_release: false,
                    delay: Duration::ZERO,
                    event: None,
                });
            }
            WorkerBehavior::Hold => {
                let delay = Duration::from_millis(self.rng.random_range(10..500));
                let mut result = self.result_event(delay, identity, request, worker_id);
                result.should_wait_for_release = true;
                script.push(result);
            }
            WorkerBehavior::DisconnectAfterHandOut => {
                script.push(ScriptedEvent {
                    should_wait_for_release: false,
                    delay: Duration::from_millis(self.rng.random_range(10..2_000)),
                    event: Some(StepTaskEvent::Disconnected {
                        detail: "simulated Worker disconnected".to_owned(),
                    }),
                });
            }
            WorkerBehavior::Unsupported | WorkerBehavior::LegacyNoCapacity => {
                let reason = if behavior == WorkerBehavior::Unsupported {
                    PollWorkerTaskRejectReason::UnknownStep
                } else {
                    PollWorkerTaskRejectReason::NoCapacity
                };
                script.push(ScriptedEvent {
                    should_wait_for_release: false,
                    delay: Duration::from_millis(self.rng.random_range(1..300)),
                    event: Some(StepTaskEvent::Rejected {
                        reason,
                        is_reserved_slot: false,
                    }),
                });
            }
            WorkerBehavior::HalfCloseBeforeOutput => {
                script.push(ScriptedEvent {
                    should_wait_for_release: false,
                    delay: Duration::from_millis(self.rng.random_range(1..300)),
                    event: Some(StepTaskEvent::NotDelivered),
                });
            }
            WorkerBehavior::DisconnectAfterOutput => {
                self.push_heartbeats(&mut script, request, false);
                script.push(ScriptedEvent {
                    should_wait_for_release: false,
                    delay: Duration::from_millis(self.rng.random_range(10..1_000)),
                    event: Some(StepTaskEvent::Disconnected {
                        detail: "simulated Worker disconnected".to_owned(),
                    }),
                });
            }
            WorkerBehavior::NoWorker
            | WorkerBehavior::RpcSucceed
            | WorkerBehavior::RpcFail
            | WorkerBehavior::RpcTimeout
            | WorkerBehavior::HeartbeatWithValue
            | WorkerBehavior::HeartbeatWithoutValue
            | WorkerBehavior::DuplicateQueueRef
            | WorkerBehavior::ClaimWithoutLease => {
                unreachable!("{behavior:?} is never drawn for a sent Step task")
            }
        }
        script
    }

    fn push_heartbeats(
        &mut self,
        script: &mut Vec<ScriptedEvent>,
        request: &MethodRequest,
        is_slow: bool,
    ) {
        if is_slow {
            return;
        }
        for _ in 0..self.rng.random_range(0..3) {
            let value = heartbeat_value(&mut self.rng);
            let kind = if value.is_some() {
                WorkerBehavior::HeartbeatWithValue
            } else {
                WorkerBehavior::HeartbeatWithoutValue
            };
            *self.behaviors.entry(kind).or_default() += 1;
            let output = match request {
                MethodRequest::WaitFor(_) => {
                    poll_worker_task_frame::Output::WaitFor(InvokeWaitForMethodOutput {
                        output: Some(invoke_wait_for_method_output::Output::Heartbeat(
                            StepMethodHeartbeat { value },
                        )),
                    })
                }
                MethodRequest::Execute(_) => {
                    poll_worker_task_frame::Output::Execute(InvokeExecuteMethodOutput {
                        output: Some(invoke_execute_method_output::Output::Heartbeat(
                            StepMethodHeartbeat { value },
                        )),
                    })
                }
            };
            script.push(ScriptedEvent {
                should_wait_for_release: false,
                delay: Duration::from_millis(self.rng.random_range(100..2_000)),
                event: Some(StepTaskEvent::Output(PollWorkerTaskFrame {
                    task_id: String::new(),
                    output: Some(output),
                })),
            });
        }
    }

    /// Builds the final result now, so the draws do not depend on timing, and remembers it.
    fn result_event(
        &mut self,
        delay: Duration,
        identity: &AttemptIdentity,
        request: &MethodRequest,
        worker_id: String,
    ) -> ScriptedEvent {
        let can_start_sub_flows = ENGINE_FLOW_IDS.contains(&identity.flow_id.as_str());
        let output = match request {
            MethodRequest::WaitFor(request) => {
                let call = MethodCallView {
                    step_type: &request.step_type,
                    can_start_sub_flows,
                    has_step_failure_policies: self.has_step_failure_policies,
                    is_execute: false,
                    attributes: &request.attributes,
                    loaded_channel_messages: &request.loaded_channel_messages,
                };
                let response = wait_for_response(&mut self.rng, &call);
                if response
                    .waiting_condition
                    .as_ref()
                    .is_some_and(|condition| !condition.sub_flow_conditions.is_empty())
                {
                    self.child_waits.insert((
                        identity.flow_id.clone(),
                        identity.run_id.clone(),
                        identity.step_execution_id.clone(),
                    ));
                }
                poll_worker_task_frame::Output::WaitFor(InvokeWaitForMethodOutput {
                    output: Some(invoke_wait_for_method_output::Output::Result(response)),
                })
            }
            MethodRequest::Execute(request) => {
                let call = MethodCallView {
                    step_type: &request.step_type,
                    can_start_sub_flows,
                    has_step_failure_policies: self.has_step_failure_policies,
                    is_execute: true,
                    attributes: &request.attributes,
                    loaded_channel_messages: &request.loaded_channel_messages,
                };
                let steps_left = self
                    .steps_left
                    .entry((identity.flow_id.clone(), identity.run_id.clone()))
                    .or_insert(STEPS_PER_RUN);
                poll_worker_task_frame::Output::Execute(InvokeExecuteMethodOutput {
                    output: Some(invoke_execute_method_output::Output::Result(
                        execute_response(&mut self.rng, &call, steps_left),
                    )),
                })
            }
        };
        let frame = PollWorkerTaskFrame {
            task_id: String::new(),
            output: Some(output),
        };
        self.returned_results
            .insert(identity.clone(), frame.clone());
        ScriptedEvent {
            should_wait_for_release: false,
            delay,
            event: Some(StepTaskEvent::Completed(DispatchOutcome::WorkerResult {
                frame,
                app_worker_id: worker_id,
            })),
        }
    }
}

/// Plays `script` until it ends, the session goes away, or the engine cancels the dispatch.
async fn play_script(
    script: Vec<ScriptedEvent>,
    events: mpsc::Sender<StepTaskEvent>,
    mut cancel: watch::Receiver<bool>,
    mut hold: watch::Receiver<bool>,
) {
    for scripted in script {
        let is_canceled_while_held = scripted.should_wait_for_release
            && tokio::select! {
                biased;
                changed = cancel.wait_for(|is_canceled| *is_canceled) => changed.is_ok(),
                released = hold.wait_for(|is_holding| !*is_holding) => released.is_err(),
            };
        let is_canceled = is_canceled_while_held
            || tokio::select! {
                biased;
                changed = cancel.wait_for(|is_canceled| *is_canceled) => changed.is_ok(),
                () = tokio::time::sleep(scripted.delay) => false,
            };
        let event = match scripted.event {
            _ if is_canceled => StepTaskEvent::Completed(DispatchOutcome::Cancelled),
            Some(event) => event,
            None => {
                if cancel.wait_for(|is_canceled| *is_canceled).await.is_err() {
                    return;
                }
                StepTaskEvent::Completed(DispatchOutcome::Cancelled)
            }
        };
        let is_final = !matches!(event, StepTaskEvent::Output(_));
        // A failed send means the engine dropped the session; the script ends either way.
        if events.send(event).await.is_err() || is_final {
            return;
        }
    }
}

struct SimulatedDispatchSession {
    events: mpsc::Receiver<StepTaskEvent>,
    cancel: watch::Sender<bool>,
    worker_task: tokio::task::JoinHandle<()>,
}

impl MethodDispatchSession for SimulatedDispatchSession {
    fn next_event(&mut self) -> BoxFuture<'_, Option<StepTaskEvent>> {
        Box::pin(self.events.recv())
    }

    fn cancel(&self) {
        self.cancel.send_replace(true);
    }
}

impl Drop for SimulatedDispatchSession {
    fn drop(&mut self) {
        self.worker_task.abort();
    }
}

/// The Worker ports of one node process.
pub(crate) struct NodeWorkerPort {
    queue: SimulatedTaskQueue,
    workers: Arc<SimulatedWorkers>,
    process_label: String,
}

impl MethodDispatcher for NodeWorkerPort {
    fn reserve_stream(
        &self,
        _request: MethodStreamRequest,
    ) -> BoxFuture<'_, MethodStreamReservation> {
        Box::pin(self.workers.reserve(&self.process_label))
    }

    fn queued(&self, task: QueuedTaskRef) {
        self.workers.trace.record(&format!(
            "{} queued {}",
            self.process_label, task.position.step_execution_id
        ));
        self.queue.push(task);
    }

    fn report_rebuilt_heads(&self, heads: Vec<QueuedTaskRef>) {
        for head in heads {
            self.queue.push(head);
        }
    }

    fn take_heads_token(&self, partition: PartitionId) -> HeadsScanToken {
        self.workers.heads_matching.take_heads_token(partition)
    }

    fn report_scanned_heads(&self, token: HeadsScanToken, results: Vec<ScannedQueueHead>) -> usize {
        for head in results.iter().filter_map(|result| result.head.clone()) {
            self.queue.push(head);
        }
        self.workers
            .heads_matching
            .report_scanned_heads(token, Vec::new())
    }

    fn is_partition_unremoved_since(
        &self,
        _partition: PartitionId,
        _partition_removals: u64,
    ) -> bool {
        true
    }

    fn is_claim_tracked(
        &self,
        partition: PartitionId,
        queue: &DispatchQueueName,
        claim_id: u64,
    ) -> bool {
        self.queue.is_claim_tracked(partition, queue, claim_id)
    }

    fn report_ordinary_backlog(
        &self,
        _partition: PartitionId,
        _queue: &DispatchQueueName,
        _pending_since_micros: Option<i64>,
    ) {
    }

    fn next_claim(&self) -> BoxFuture<'_, Option<MethodClaim>> {
        Box::pin(async move {
            loop {
                let notified = self.queue.has_refs.notified();
                let claim_id = self.queue.new_claim_id();
                let Some(task) = self.queue.take_claimable(claim_id) else {
                    notified.await;
                    continue;
                };
                let delay = {
                    let mut state = self.workers.lock();
                    Duration::from_millis(state.rng.random_range(10..500))
                };
                tokio::time::sleep(delay).await;
                let is_without_lease = self
                    .workers
                    .draw_queue_fault(WorkerBehavior::ClaimWithoutLease);
                self.workers.trace.record(&format!(
                    "{} claim {claim_id} {} without_lease={is_without_lease}",
                    self.process_label, task.position.step_execution_id
                ));
                return Some(MethodClaim {
                    partition_removals: 0,
                    claim_id,
                    task,
                    stream: Box::new(SimulatedSlot {
                        workers: Arc::clone(&self.workers),
                        process_label: self.process_label.clone(),
                        may_hand_out_tasks: !is_without_lease,
                    }),
                });
            }
        })
    }

    fn reply_to_claim(
        &self,
        partition: PartitionId,
        queue: &DispatchQueueName,
        claim_id: u64,
        reply: ClaimReply,
    ) {
        let Some(claimed) = self
            .queue
            .finish_claim(partition, queue, claim_id, reply.next)
        else {
            return;
        };
        if self
            .workers
            .draw_queue_fault(WorkerBehavior::DuplicateQueueRef)
        {
            self.workers.trace.record(&format!(
                "{} duplicate ref {} after {:?}",
                self.process_label, claimed.position.step_execution_id, reply.result
            ));
            self.queue.push(claimed);
        }
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

/// A reserved slot of the simulated Worker pool.
struct SimulatedSlot {
    workers: Arc<SimulatedWorkers>,
    process_label: String,
    may_hand_out_tasks: bool,
}

impl ReservedMethodStream for SimulatedSlot {
    fn app_worker_id(&self) -> &str {
        "worker-0"
    }

    fn may_hand_out_tasks(&self) -> bool {
        self.may_hand_out_tasks
    }

    fn send_task(
        self: Box<Self>,
        task: MethodTask,
    ) -> Result<Box<dyn MethodDispatchSession>, StepTaskSendFailure> {
        self.workers.start_dispatch(&self.process_label, &task)
    }
}

impl StepQueueStatus for NodeWorkerPort {
    fn is_waiting_for_supporting_worker(
        &self,
        _tenant_id: u64,
        _flow_type: &str,
        _step_type: &str,
    ) -> bool {
        false
    }
}

impl RpcWorkerInvoker for NodeWorkerPort {
    fn invoke_worker_rpc(
        &self,
        invocation: WorkerRpcInvocation,
    ) -> BoxFuture<'_, Result<WorkerRpcResult, RpcInvokeError>> {
        Box::pin(self.workers.invoke_rpc(&self.process_label, invocation))
    }
}
