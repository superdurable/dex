// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::HashMap;
use std::sync::atomic::{AtomicI32, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use dex_protocol::dex::poll_worker_task::Request as TaskRequest;
use dex_protocol::dex::poll_worker_task_frame::Output;
use dex_protocol::dex::poll_worker_task_request::Frame as RequestFrame;
use dex_protocol::dex::poll_worker_task_response::Frame as ResponseFrame;
use dex_protocol::dex::worker_task_service_client::WorkerTaskServiceClient;
use dex_protocol::dex::{
    PollWorkerSlotKind, PollWorkerTask, PollWorkerTaskPong, PollWorkerTaskRegister,
    PollWorkerTaskReject, PollWorkerTaskRequest, WorkerErrorResponse,
};
use tokio::sync::{Notify, mpsc, watch};
use tokio::task::{JoinHandle, JoinSet};
use tokio::time::{
    Instant, Interval, MissedTickBehavior, interval_at, sleep, sleep_until, timeout_at,
};
use tokio_stream::{StreamExt, wrappers::ReceiverStream};
use tonic::transport::Endpoint;
use tonic::{Code, Status};

use crate::Credential;
use crate::context::InvocationCancellation;
use crate::credential::{CredentialChannel, CredentialInterceptor};
use crate::pull_registration::PullRegistration;
use crate::worker_dispatcher::WorkerDispatcher;
use crate::{PullWorkerOptions, SdkError, SdkResult};

#[cfg(test)]
mod final_frame_lock_tests;
mod poll_transport;
mod reconnect;
mod task_deadline;
mod task_frame_sender;
mod worker_error;
mod worker_identity;

use task_deadline::task_deadline;
use task_frame_sender::TaskFrameSender;
use worker_error::worker_error_response;
use worker_identity::{generate_app_worker_id, validate_app_worker_id};

const DRAINING: u8 = 1;
const FORCED: u8 = 2;
const BUSY_PULL_STREAM_KEEPALIVE_INTERVAL: Duration = Duration::from_secs(20);
// A slot whose final output may still be in flight waits this long past the stop deadline.
const FINAL_OUTPUT_FLUSH_GRACE: Duration = Duration::from_secs(5);

#[derive(Clone, Copy)]
struct StopSignal {
    phase: u8,
    deadline: Option<Instant>,
}

pub(crate) fn outbound_task_channel(
    endpoint: Endpoint,
    credential: Option<Arc<dyn Credential>>,
) -> CredentialChannel {
    CredentialChannel::new(
        endpoint.connect_lazy(),
        CredentialInterceptor::new(credential),
    )
}

pub(crate) struct PullWorker {
    dispatcher: WorkerDispatcher,
    channel: CredentialChannel,
    credential: Option<Arc<dyn Credential>>,
    app_worker_id: String,
    flow_types: Vec<String>,
    pub(crate) task_queue: String,
    max_concurrent_tasks: usize,
    reserved_rpc_tasks: usize,
    stop_drain_timeout: Duration,
    task_start_gate: Arc<Mutex<bool>>,
    pub(crate) registration: Arc<PullRegistration>,
    should_warn_rpc_capacity: bool,
    stop_on_permanent_stream_rejection: bool,
}

struct PullState {
    streams: HashMap<u64, StreamSlot>,
    busy_tasks: usize,
    is_stopping: bool,
    logged_unsupported: bool,
}

struct StreamSlot {
    is_busy: bool,
    is_registered: bool,
    is_draining: bool,
}

struct PullShared {
    registration: Arc<PullRegistration>,
    state: Mutex<PullState>,
    notify: Notify,
    max_concurrent_tasks: usize,
    has_checked_registration_limit: Mutex<bool>,
    startup_error: Mutex<Option<SdkError>>,
    task_start_gate: Arc<Mutex<bool>>,
}

struct ActiveTask {
    task_id: String,
    cancellation: InvocationCancellation,
    transport_cancellation: InvocationCancellation,
    progress_cancellation: InvocationCancellation,
    cancel_code: Arc<AtomicI32>,
    handler: JoinHandle<()>,
    final_frame_sent: Arc<Mutex<bool>>,
    deadline: Option<Instant>,
}

impl ActiveTask {
    fn cancel(&self, cancellation_code: Code, should_cancel_transport: bool) {
        let _is_final_frame_sent = self.final_frame_sent.lock().expect("final frame lock");
        self.cancel_signals(cancellation_code, should_cancel_transport);
    }

    /// Returns true after cancelling the task and its transport, or false when its final output
    /// is already queued; the check and cancellation hold one lock so no final output slips between.
    fn cancel_unless_final_frame_sent(&self, cancellation_code: Code) -> bool {
        let is_final_frame_sent = self.final_frame_sent.lock().expect("final frame lock");
        if *is_final_frame_sent {
            return false;
        }
        self.cancel_signals(cancellation_code, true);
        true
    }

    fn cancel_signals(&self, cancellation_code: Code, should_cancel_transport: bool) {
        self.cancel_code
            .store(cancellation_code as i32, Ordering::Release);
        self.progress_cancellation.cancel();
        self.cancellation.cancel();
        if should_cancel_transport {
            self.transport_cancellation.cancel();
        }
    }
}

struct StreamRegistration {
    shared: Arc<PullShared>,
    stream_id: u64,
}

impl Drop for StreamRegistration {
    fn drop(&mut self) {
        let mut state = self.shared.state.lock().expect("pull state lock");
        if let Some(slot) = state.streams.remove(&self.stream_id)
            && slot.is_busy
        {
            state.busy_tasks -= 1;
        }
        self.shared.notify.notify_one();
    }
}

impl PullWorker {
    pub(crate) fn new(
        options: &PullWorkerOptions,
        dispatcher: &WorkerDispatcher,
        channel: CredentialChannel,
    ) -> SdkResult<Self> {
        let max_concurrent_tasks = match options.max_concurrent_tasks {
            Some(value) => value,
            None => std::thread::available_parallelism()
                .map(std::num::NonZeroUsize::get)
                .map_err(|error| SdkError::Service {
                    service: crate::ServiceError::local(
                        "worker",
                        format!("read CPU count: {error}"),
                    ),
                })?,
        };
        let reserved_rpc_tasks = options
            .reserved_rpc_tasks
            .unwrap_or(usize::from(max_concurrent_tasks >= 2));
        let stop_drain_timeout = options
            .stop_drain_timeout
            .unwrap_or(Duration::from_secs(30));
        if max_concurrent_tasks == 0 || stop_drain_timeout.is_zero() {
            return Err(invalid(
                "pull Worker task limit and drain timeout must be positive",
            ));
        }
        if reserved_rpc_tasks >= max_concurrent_tasks {
            return Err(invalid(
                "reserved_rpc_tasks must be less than max_concurrent_tasks",
            ));
        }
        let app_worker_id = match &options.app_worker_id {
            Some(value) => value.clone(),
            None => generate_app_worker_id(),
        };
        validate_app_worker_id(&app_worker_id)?;
        crate::client::task_queue_name::validate_pull_worker_task_queue(&options.task_queue)
            .map_err(invalid)?;
        let flow_types = dispatcher.flow_types();
        if flow_types
            .iter()
            .any(|flow_type| flow_type == &app_worker_id)
        {
            return Err(invalid(format!(
                "app Worker ID conflicts with Flow type {app_worker_id:?}"
            )));
        }
        let mut slot_kinds = vec!["STEP_AND_RPC".to_string()];
        if reserved_rpc_tasks > 0 {
            slot_kinds.push("RPC_ONLY".to_string());
        }
        Ok(Self {
            dispatcher: dispatcher.clone(),
            credential: channel.credential(),
            channel,
            app_worker_id,
            flow_types,
            task_queue: options.task_queue.clone(),
            registration: Arc::new(PullRegistration::new(
                &slot_kinds,
                options.task_queue.clone(),
            )),
            stop_on_permanent_stream_rejection: options.stop_on_permanent_stream_rejection,
            max_concurrent_tasks,
            reserved_rpc_tasks,
            should_warn_rpc_capacity: options.reserved_rpc_tasks.is_none()
                && max_concurrent_tasks == 1,
            stop_drain_timeout,
            task_start_gate: Arc::new(Mutex::new(false)),
        })
    }

    pub(crate) fn stop_accepting_tasks(&self) {
        *self.task_start_gate.lock().expect("task start gate lock") = true;
    }

    pub(crate) async fn run(&self, mut shutdown: watch::Receiver<bool>) -> SdkResult<()> {
        if self.should_warn_rpc_capacity {
            tracing::warn!(
                "RPCs may wait behind long-running Steps; use max_concurrent_tasks >= 2 or set reserved_rpc_tasks explicitly"
            );
        }
        let shared = Arc::new(PullShared {
            registration: Arc::clone(&self.registration),
            state: Mutex::new(PullState {
                streams: HashMap::new(),
                busy_tasks: 0,
                is_stopping: false,
                logged_unsupported: false,
            }),
            notify: Notify::new(),
            max_concurrent_tasks: self.max_concurrent_tasks,
            has_checked_registration_limit: Mutex::new(false),
            startup_error: Mutex::new(None),
            task_start_gate: Arc::clone(&self.task_start_gate),
        });
        let (stop, stop_receiver) = watch::channel(StopSignal {
            phase: 0,
            deadline: None,
        });
        let mut streams = JoinSet::new();
        self.open_slots(&shared, &stop_receiver, &mut streams);
        loop {
            if shared
                .startup_error
                .lock()
                .expect("startup error lock")
                .is_some()
                || *shutdown.borrow()
                || (self.stop_on_permanent_stream_rejection
                    && self.registration.permanent_error().is_some())
            {
                break;
            }
            tokio::select! {
                change = shutdown.changed() => {
                    if change.is_err() || *shutdown.borrow() { break; }
                }
                () = shared.notify.notified() => {}
                result = streams.join_next(), if !streams.is_empty() => {
                    if let Some(Err(error)) = result { tracing::error!(%error, "pull Worker stream failed"); }
                }
            }
        }
        self.stop_accepting_tasks();
        shared.state.lock().expect("pull state lock").is_stopping = true;
        self.registration.finish(None);
        let stop_deadline = Instant::now() + self.stop_drain_timeout;
        stop.send_replace(StopSignal {
            phase: DRAINING,
            deadline: Some(stop_deadline),
        });
        if timeout_at(stop_deadline, async {
            while let Some(result) = streams.join_next().await {
                if let Err(error) = result {
                    tracing::error!(%error, "pull Worker stream failed");
                }
            }
        })
        .await
        .is_err()
        {
            stop.send_replace(StopSignal {
                phase: FORCED,
                deadline: Some(stop_deadline),
            });
            while let Some(result) = streams.join_next().await {
                if let Err(error) = result {
                    tracing::error!(%error, "pull Worker stream failed");
                }
            }
        }
        if let Some(error) = shared
            .startup_error
            .lock()
            .expect("startup error lock")
            .take()
        {
            return Err(error);
        }
        if self.stop_on_permanent_stream_rejection
            && let Some(rejection) = self.registration.permanent_error()
        {
            return Err(SdkError::from_pull_registration(rejection));
        }
        Ok(())
    }

    fn open_slots(
        &self,
        shared: &Arc<PullShared>,
        stop: &watch::Receiver<StopSignal>,
        streams: &mut JoinSet<()>,
    ) {
        let mut state = shared.state.lock().expect("pull state lock");
        for stream_id in 0..self.max_concurrent_tasks as u64 {
            let slot_kind = if stream_id < self.reserved_rpc_tasks as u64 {
                PollWorkerSlotKind::RpcOnly
            } else {
                PollWorkerSlotKind::StepAndRpc
            };
            let queue = match slot_kind {
                PollWorkerSlotKind::RpcOnly => "RPC_ONLY",
                PollWorkerSlotKind::Unspecified | PollWorkerSlotKind::StepAndRpc => "STEP_AND_RPC",
            }
            .to_string();
            state.streams.insert(
                stream_id,
                StreamSlot {
                    is_busy: false,
                    is_registered: false,
                    is_draining: false,
                },
            );
            let worker = PullStream {
                channel: self.channel.clone(),
                credential: self.credential.clone(),
                dispatcher: self.dispatcher.clone(),
                app_worker_id: self.app_worker_id.clone(),
                flow_types: self.flow_types.clone(),
                task_queue: self.task_queue.clone(),
                queue,
                slot_kind,
                stream_id,
                shared: Arc::clone(shared),
                stop: stop.clone(),
                stop_drain_timeout: self.stop_drain_timeout,
                is_registered: false,
                has_recorded_result: false,
                has_task_queue_mismatch: false,
            };
            streams.spawn(async move { worker.run().await });
        }
    }
}

struct PullStream {
    channel: CredentialChannel,
    credential: Option<Arc<dyn Credential>>,
    dispatcher: WorkerDispatcher,
    app_worker_id: String,
    flow_types: Vec<String>,
    task_queue: String,
    queue: String,
    slot_kind: PollWorkerSlotKind,
    stream_id: u64,
    shared: Arc<PullShared>,
    stop: watch::Receiver<StopSignal>,
    stop_drain_timeout: Duration,
    is_registered: bool,
    has_recorded_result: bool,
    has_task_queue_mismatch: bool,
}

impl PullStream {
    async fn poll(&mut self) -> (bool, bool, Result<(), Status>) {
        self.is_registered = false;
        self.has_recorded_result = false;
        self.has_task_queue_mismatch = false;
        self.shared
            .state
            .lock()
            .expect("pull state lock")
            .streams
            .get_mut(&self.stream_id)
            .expect("registered pull stream")
            .is_draining = false;
        let result = self.poll_stream().await;
        if !result
            .2
            .as_ref()
            .is_err_and(|status| self.should_defer_stream_rejection(status))
        {
            self.record_stream_result(result.2.as_ref().err());
        }
        result
    }

    /// Returns whether `status` permanently rejects this stream; a task queue mismatch always does.
    fn is_permanent_stream_failure(&self, status: &Status) -> bool {
        self.has_task_queue_mismatch || is_permanent(status.code())
    }

    fn should_defer_stream_rejection(&self, status: &Status) -> bool {
        status.code() == Code::Unauthenticated
            && self
                .credential
                .as_ref()
                .is_some_and(|credential| credential.token_after_rejection().is_some())
    }

    fn record_stream_result(&mut self, failure: Option<&Status>) {
        if self.has_recorded_result {
            return;
        }
        self.has_recorded_result = true;
        let permanent_failure = failure.filter(|status| self.is_permanent_stream_failure(status));
        self.shared
            .registration
            .ended(&self.queue, self.is_registered, permanent_failure);
        self.is_registered = false;
        self.shared
            .state
            .lock()
            .expect("pull state lock")
            .streams
            .get_mut(&self.stream_id)
            .expect("registered pull stream")
            .is_registered = false;
        self.shared.notify.notify_one();
    }

    async fn poll_stream(&mut self) -> (bool, bool, Result<(), Status>) {
        let (outbound_sender, receiver) = mpsc::channel(16);
        let mut sender = Some(outbound_sender);
        let register = PollWorkerTaskRegister {
            queue: String::new(),
            supports_reserved_slot_dispatch: true,
            slot_kind: self.slot_kind as i32,
            app_worker_id: self.app_worker_id.clone(),
            flow_types: self.flow_types.clone(),
            sdk_name: "rust".to_string(),
            sdk_version: env!("CARGO_PKG_VERSION").to_string(),
            task_queue: self.task_queue.clone(),
        };
        if sender
            .as_ref()
            .expect("open Worker stream")
            .send(PollWorkerTaskRequest {
                frame: Some(RequestFrame::Register(register)),
            })
            .await
            .is_err()
        {
            return (
                false,
                false,
                Err(Status::internal("Worker register channel closed")),
            );
        }
        let stream_reset = InvocationCancellation::new();
        // Declared after the sender so it drops first: an error exit resets instead of sending EOF.
        let mut reset_guard = poll_transport::PollStreamResetGuard::new(stream_reset.clone());
        let mut client = WorkerTaskServiceClient::new(poll_transport::PollTransport::new(
            self.channel.clone(),
            stream_reset,
        ));
        let opening = client.poll_worker_task(ReceiverStream::new(receiver));
        tokio::pin!(opening);
        let opening = tokio::select! {
            result = &mut opening => result,
            change = self.stop.changed() => {
                if change.is_err() || self.stop.borrow().phase != 0 {
                    reset_guard.allow_clean_half_close();
                    sender.take();
                    let deadline = self.stop.borrow().deadline.unwrap_or_else(|| Instant::now() + self.stop_drain_timeout);
                    match timeout_at(deadline, &mut opening).await {
                        Ok(result) => result,
                        Err(_) => return (false, false, Err(Status::deadline_exceeded("Worker poll opening did not close after Stop"))),
                    }
                } else {
                    return (false, false, Err(Status::unavailable("Worker poll interrupted")));
                }
            }
        };
        let mut response_stream = match opening {
            Ok(response) => response.into_inner(),
            Err(status) => return (false, false, Err(status)),
        };
        let mut was_registered = false;
        let mut is_draining = false;
        let mut active: Option<ActiveTask> = None;
        let mut final_frame_sent = Arc::new(Mutex::new(false));
        let mut keepalive_interval: Option<Interval> = None;
        // Only the next task assignment proves that Dex received the previous final output.
        let mut has_unacknowledged_final_output = false;
        loop {
            if self.stop.borrow().phase != 0 && active.is_none() {
                reset_guard.allow_clean_half_close();
                let result = close_poll_send_and_wait(
                    &mut sender,
                    &mut response_stream,
                    &mut self.stop,
                    self.stop_drain_timeout,
                    has_unacknowledged_final_output,
                    &reset_guard,
                )
                .await;
                return (was_registered, false, result);
            }
            let deadline = active.as_ref().and_then(|task| task.deadline);
            tokio::select! {
                response = response_stream.message() => {
                    match response {
                        Ok(Some(response)) => {
                            if !was_registered {
                                match response.frame {
                                    Some(ResponseFrame::Registered(registered)) => {
                                        if registered.task_queue != self.task_queue {
                                            let status = self.task_queue_mismatch(&registered.task_queue);
                                            reset_guard.allow_clean_half_close();
                                            if let Err(drain_status) = close_poll_send_and_wait(&mut sender, &mut response_stream, &mut self.stop, self.stop_drain_timeout, false, &reset_guard).await {
                                                tracing::debug!(%drain_status, "drain Worker stream after task queue mismatch");
                                            }
                                            return (false, false, Err(status));
                                        }
                                        if let Err(status) = self.shared.check_registration_limit(registered.max_concurrent_tasks_per_app_worker) {
                                            // Half-close and drop later tasks so Dex returns them without consuming an attempt.
                                            reset_guard.allow_clean_half_close();
                                            if let Err(drain_status) = close_poll_send_and_wait(&mut sender, &mut response_stream, &mut self.stop, self.stop_drain_timeout, false, &reset_guard).await {
                                                tracing::debug!(%drain_status, "drain Worker stream after registration limit failure");
                                            }
                                            return (false, false, Err(status));
                                        }
                                        was_registered = true; self.is_registered = true; self.shared.state.lock().expect("pull state lock").streams.get_mut(&self.stream_id).expect("registered pull stream").is_registered = true; self.shared.registration.registered(&self.queue); self.shared.notify.notify_one(); }
                                    Some(ResponseFrame::Reconnect(_)) => {
                                        reset_guard.allow_clean_half_close();
                                        return (false, true, Ok(()));
                                    }
                                    _ => return (false, false, Err(Status::internal("Worker task stream did not register"))),
                                }
                                continue;
                            }
                            match response.frame {
                                Some(ResponseFrame::Task(task)) => {
                                    has_unacknowledged_final_output = false;
                                    if self.is_task_start_closed() {
                                        continue;
                                    }
                                    if active.is_some() {
                                        let has_sent_final_frame = *final_frame_sent.lock().expect("final frame lock");
                                        if !has_sent_final_frame {
                                            reset_guard.reset();
                                            self.cancel_and_wait(&mut active).await;
                                            return (was_registered, false, Err(Status::internal("Server assigned a second task to a busy reserved slot")));
                                        }
                                        // Dex may reuse the slot as soon as it receives the final output; finish local cleanup first.
                                        if let Some(previous) = active.take() {
                                            finish_handler_after_final_output(previous.handler).await;
                                        }
                                        self.shared.release(self.stream_id);
                                        keepalive_interval = None;
                                        if self.is_task_start_closed() {
                                            continue;
                                        }
                                    }
                                    // Validate untrusted metadata before admission; a malformed task never reaches a handler.
                                    let assigned_deadline = match validate_assigned_task(&task) {
                                        Ok(assigned_deadline) => assigned_deadline,
                                        Err(error) => {
                                            reset_guard.reset();
                                            return (was_registered, false, Err(Status::internal(format!("Server sent a malformed task: {error}"))));
                                        }
                                    };
                                    if let Some(reason) = self.dispatcher.unknown_task_reason(&task) {
                                        let reject = PollWorkerTaskReject { task_id: task.task_id, reason: reason as i32 };
                                        let sent = tokio::select! {
                                            result = send_control_frame(sender.as_ref().expect("open Worker stream"), RequestFrame::Reject(reject)) => result,
                                            () = wait_for_forced_stop(&mut self.stop) => Err(Status::cancelled("Worker stop timeout reset a blocked poll stream")),
                                        };
                                        if let Err(status) = sent {
                                            reset_guard.reset();
                                            if !self.should_defer_stream_rejection(&status) {
                                                self.record_stream_result(Some(&status));
                                            }
                                            return (was_registered, false, Err(status));
                                        }
                                    } else {
                                        if !self.shared.try_accept(self.stream_id) { continue; }
                                        let cancellation = InvocationCancellation::new();
                                        let transport_cancellation = InvocationCancellation::new();
                                        let progress_cancellation = InvocationCancellation::new();
                                        let cancel_code = Arc::new(AtomicI32::new(0));
                                        let task_id = task.task_id.clone();
                                        final_frame_sent = Arc::new(Mutex::new(false));
                                        let mut interval = interval_at(Instant::now() + BUSY_PULL_STREAM_KEEPALIVE_INTERVAL, BUSY_PULL_STREAM_KEEPALIVE_INTERVAL);
                                        interval.set_missed_tick_behavior(MissedTickBehavior::Delay);
                                        keepalive_interval = Some(interval);
                                        let frame_sender = TaskFrameSender::new(task_id.clone(), sender.as_ref().expect("open Worker stream").clone(), transport_cancellation.clone(), progress_cancellation.clone(), Arc::clone(&cancel_code), Arc::clone(&final_frame_sent));
                                        let handler = tokio::spawn(execute_task(self.dispatcher.clone(), task, cancellation.clone(), Arc::clone(&cancel_code), frame_sender));
                                        active = Some(ActiveTask { task_id, cancellation, transport_cancellation, progress_cancellation, cancel_code, handler, final_frame_sent: Arc::clone(&final_frame_sent), deadline: assigned_deadline });
                                        self.shared.notify.notify_one();
                                    }
                                }
                                Some(ResponseFrame::CancelTask(cancel)) => {
                                    if let Some(task) = &active && task.task_id == cancel.task_id {
                                        task.cancel(Code::Cancelled, false);
                                    }
                                }
                                Some(ResponseFrame::Ping(_)) => {
                                    // Never block the poll loop: a full outbound queue already carries upstream data.
                                    let sent = {
                                        let is_final_frame_sent = final_frame_sent.lock().expect("final frame lock");
                                        if active.is_some() && *is_final_frame_sent {
                                            Ok(())
                                        } else {
                                            match sender.as_ref().expect("open Worker stream").try_send(PollWorkerTaskRequest { frame: Some(RequestFrame::Pong(PollWorkerTaskPong {})) }) {
                                                Err(mpsc::error::TrySendError::Closed(_)) => Err(Status::unavailable("Worker task stream cannot send control frame: channel closed")),
                                                Ok(()) | Err(mpsc::error::TrySendError::Full(_)) => Ok(()),
                                            }
                                        }
                                    };
                                    if let Err(status) = sent {
                                        reset_guard.reset();
                                        if !self.should_defer_stream_rejection(&status) {
                                            self.record_stream_result(Some(&status));
                                        }
                                        self.cancel_and_wait(&mut active).await;
                                        return (was_registered, false, Err(status));
                                    }
                                }
                                Some(ResponseFrame::Reconnect(_)) => {
                                    self.shared.mark_draining(self.stream_id);
                                    is_draining = true;
                                    if active.is_none() {
                                        reset_guard.allow_clean_half_close();
                                        if has_unacknowledged_final_output {
                                            let result = close_poll_send_and_wait(&mut sender, &mut response_stream, &mut self.stop, self.stop_drain_timeout, true, &reset_guard).await;
                                            return (was_registered, self.stop.borrow().phase == 0, result);
                                        }
                                        return (was_registered, true, Ok(()));
                                    }
                                    self.shared.notify.notify_one();
                                }
                                _ => {}
                            }
                        }
                        Ok(None) => {
                            reset_guard.reset();
                            self.record_stream_result(None);
                            self.cancel_and_wait(&mut active).await;
                            return (was_registered, false, Ok(()));
                        }
                        Err(status) => {
                            reset_guard.reset();
                            if !self.should_defer_stream_rejection(&status) {
                                self.record_stream_result(Some(&status));
                            }
                            self.cancel_and_wait(&mut active).await;
                            return (was_registered, false, Err(status));
                        }
                    }
                }
                result = async { (&mut active.as_mut().expect("active task").handler).await }, if active.is_some() => {
                    if let Err(error) = result { tracing::error!(%error, "pull Worker handler failed"); }
                    keepalive_interval = None;
                    active = None;
                    self.shared.release(self.stream_id);
                    if !*final_frame_sent.lock().expect("final frame lock") {
                        reset_guard.reset();
                        return (was_registered, false, Err(Status::internal("Worker task ended without final output; reset stream")));
                    }
                    has_unacknowledged_final_output = true;
                    if is_draining || self.stop.borrow().phase != 0 {
                        let is_reconnecting = is_draining && self.stop.borrow().phase == 0;
                        reset_guard.allow_clean_half_close();
                        let result = close_poll_send_and_wait(&mut sender, &mut response_stream, &mut self.stop, self.stop_drain_timeout, true, &reset_guard).await;
                        return (was_registered, is_reconnecting, result);
                    }
                }
                () = async {
                    match &mut keepalive_interval {
                        Some(interval) => { interval.tick().await; }
                        None => std::future::pending().await,
                    }
                } => {
                    let is_final_frame_sent = final_frame_sent.lock().expect("final frame lock");
                    if !*is_final_frame_sent
                        && let Err(error) = sender.as_ref().expect("open Worker stream").try_send(PollWorkerTaskRequest {
                            frame: Some(RequestFrame::Pong(PollWorkerTaskPong {})),
                        }) {
                        tracing::debug!(%error, "send Worker keepalive Pong");
                    }
                }
                () = async { sleep_until(deadline.expect("task deadline")).await }, if deadline.is_some() => {
                    if let Some(task) = &mut active {
                        task.cancel(Code::DeadlineExceeded, false);
                        task.deadline = None;
                    }
                }
                change = self.stop.changed() => {
                    if change.is_err() || self.stop.borrow().phase != 0 {
                        is_draining = true;
                        if self.stop.borrow().phase >= FORCED && let Some(task) = &active {
                            if task.cancel_unless_final_frame_sent(Code::Cancelled) {
                                reset_guard.reset();
                                drop(response_stream);
                                self.cancel_and_wait(&mut active).await;
                                return (was_registered, false, Err(Status::cancelled("Worker stop timeout reset busy stream")));
                            }
                            // The final output is already queued; a reset could drop it and rerun the task.
                            if let Some(finished) = active.take() {
                                finish_handler_after_final_output(finished.handler).await;
                            }
                            self.shared.release(self.stream_id);
                            has_unacknowledged_final_output = true;
                        }
                        if active.is_none() {
                            reset_guard.allow_clean_half_close();
                            let result = close_poll_send_and_wait(&mut sender, &mut response_stream, &mut self.stop, self.stop_drain_timeout, has_unacknowledged_final_output, &reset_guard).await;
                            return (was_registered, false, result);
                        }
                    }
                }
            }
        }
    }

    // A Server without named task queues ignores the request and registers the default queue.
    fn task_queue_mismatch(&mut self, registered_task_queue: &str) -> Status {
        self.has_task_queue_mismatch = true;
        let message = format!(
            "Server registered task queue {registered_task_queue:?} instead of {:?}; \
             it may not support named task queues",
            self.task_queue
        );
        tracing::error!(queue = %self.queue, %message, "Worker task stream registered the wrong task queue");
        Status::failed_precondition(message)
    }

    fn is_task_start_closed(&self) -> bool {
        self.stop.borrow().phase != 0
            || *self
                .shared
                .task_start_gate
                .lock()
                .expect("task start gate lock")
    }

    async fn cancel_and_wait(&self, active: &mut Option<ActiveTask>) {
        if let Some(task) = active.take() {
            task.cancel(Code::Cancelled, true);
            if let Err(error) = task.handler.await {
                tracing::error!(%error, "pull Worker handler failed");
            }
            self.shared.release(self.stream_id);
        }
    }
}

impl PullShared {
    fn check_registration_limit(&self, server_limit: u32) -> Result<(), Status> {
        let mut has_checked = self
            .has_checked_registration_limit
            .lock()
            .expect("registration limit lock");
        if let Some(error) = self
            .startup_error
            .lock()
            .expect("startup error lock")
            .as_ref()
        {
            return Err(Status::failed_precondition(error.to_string()));
        }
        let is_over_limit = server_limit != 0 && self.max_concurrent_tasks > server_limit as usize;
        if *has_checked {
            if is_over_limit {
                tracing::warn!(
                    max_concurrent_tasks = self.max_concurrent_tasks,
                    server_limit,
                    "Server matching.maxConcurrentTasksCap is below max_concurrent_tasks after reconnect; extra slots may be rejected"
                );
            }
            return Ok(());
        }
        *has_checked = true;
        if is_over_limit {
            let message = format!(
                "max_concurrent_tasks {} exceeds Server matching.maxConcurrentTasksCap {}",
                self.max_concurrent_tasks, server_limit
            );
            *self.task_start_gate.lock().expect("task start gate lock") = true;
            let status = Status::failed_precondition(message);
            let error = SdkError::from_status(
                status.clone(),
                "start",
                None,
                crate::sdk_error::FlowTargetRequirement::None,
            );
            self.registration.finish(Some(error.clone()));
            *self.startup_error.lock().expect("startup error lock") = Some(error);
            self.notify.notify_one();
            return Err(status);
        }
        Ok(())
    }

    fn try_accept(&self, stream_id: u64) -> bool {
        let is_stopping = self.task_start_gate.lock().expect("task start gate lock");
        let mut state = self.state.lock().expect("pull state lock");
        if *is_stopping || state.is_stopping {
            return false;
        }
        let slot = state
            .streams
            .get_mut(&stream_id)
            .expect("registered pull stream");
        assert!(!slot.is_busy, "reserved slot has at most one running task");
        slot.is_busy = true;
        state.busy_tasks += 1;
        true
    }

    fn release(&self, stream_id: u64) {
        let mut state = self.state.lock().expect("pull state lock");
        let slot = state
            .streams
            .get_mut(&stream_id)
            .expect("registered pull stream");
        assert!(slot.is_busy, "pull stream must have an active task");
        slot.is_busy = false;
        state.busy_tasks -= 1;
        self.notify.notify_one();
    }

    fn mark_draining(&self, stream_id: u64) {
        self.state
            .lock()
            .expect("pull state lock")
            .streams
            .get_mut(&stream_id)
            .expect("registered pull stream")
            .is_draining = true;
    }
}

async fn execute_task(
    dispatcher: WorkerDispatcher,
    task: PollWorkerTask,
    cancellation: InvocationCancellation,
    cancel_code: Arc<AtomicI32>,
    frame_sender: TaskFrameSender,
) {
    #[cfg(test)]
    let held_task_id = task.task_id.clone();
    match task.request {
        Some(TaskRequest::WaitFor(request)) => {
            let mut output = dispatcher
                .invoke_wait_for_with_cancellation(request, cancellation.clone())
                .into_stream();
            while let Some(result) = output.next().await {
                let frame = match result {
                    Ok(value) => Output::WaitFor(value),
                    Err(error) => Output::WorkerError(with_cancel_status(
                        worker_error_response(error),
                        &cancel_code,
                    )),
                };
                if frame_sender.send(frame).await.is_err() {
                    cancellation.cancel();
                }
            }
        }
        Some(TaskRequest::Execute(request)) => {
            let mut output = dispatcher
                .invoke_execute_with_cancellation(request, cancellation.clone())
                .into_stream();
            while let Some(result) = output.next().await {
                let frame = match result {
                    Ok(value) => Output::Execute(value),
                    Err(error) => Output::WorkerError(with_cancel_status(
                        worker_error_response(error),
                        &cancel_code,
                    )),
                };
                if frame_sender.send(frame).await.is_err() {
                    cancellation.cancel();
                }
            }
        }
        Some(TaskRequest::Rpc(request)) => {
            let frame = match dispatcher
                .invoke_rpc_with_cancellation(request, cancellation)
                .await
            {
                Ok(value) => Output::Rpc(value),
                Err(error) => Output::WorkerError(with_cancel_status(
                    worker_error_response(error),
                    &cancel_code,
                )),
            };
            if let Err(error) = frame_sender.send(frame).await {
                tracing::warn!(%error, "send Worker RPC result");
            }
        }
        None => {
            let error = WorkerErrorResponse {
                detail: "Worker task request is missing".to_string(),
                grpc_status: Code::InvalidArgument as i32,
                ..Default::default()
            };
            if let Err(error) = frame_sender.send(Output::WorkerError(error)).await {
                tracing::warn!(%error, "send Worker task error");
            }
        }
    }
    #[cfg(test)]
    task_cleanup_barrier::wait(&held_task_id).await;
}

/// Lets tests hold a task's local cleanup open after its final output.
#[cfg(test)]
mod task_cleanup_barrier {
    use std::collections::HashMap;
    use std::sync::{Arc, LazyLock, Mutex};

    use tokio::sync::Notify;

    static BARRIERS: LazyLock<Mutex<HashMap<String, Arc<Notify>>>> =
        LazyLock::new(|| Mutex::new(HashMap::new()));

    /// Returns the barrier that releases `task_id`'s cleanup when notified.
    pub(super) fn hold(task_id: &str) -> Arc<Notify> {
        let barrier = Arc::new(Notify::new());
        BARRIERS
            .lock()
            .expect("task cleanup barrier lock")
            .insert(task_id.to_string(), Arc::clone(&barrier));
        barrier
    }

    pub(super) async fn wait(task_id: &str) {
        let barrier = BARRIERS
            .lock()
            .expect("task cleanup barrier lock")
            .remove(task_id);
        if let Some(barrier) = barrier {
            barrier.notified().await;
        }
    }
}

async fn send_control_frame(
    sender: &mpsc::Sender<PollWorkerTaskRequest>,
    frame: RequestFrame,
) -> Result<(), Status> {
    sender
        .send(PollWorkerTaskRequest { frame: Some(frame) })
        .await
        .map_err(|error| {
            Status::unavailable(format!(
                "Worker task stream cannot send control frame: {error}"
            ))
        })
}

async fn wait_for_forced_stop(stop: &mut watch::Receiver<StopSignal>) {
    while stop.borrow_and_update().phase < FORCED {
        if stop.changed().await.is_err() {
            return;
        }
    }
}

/// Returns the validated local deadline of an untrusted task assignment, or `None` when it has
/// none. Returns an error for an empty task ID, a missing request, or a malformed deadline.
fn validate_assigned_task(task: &PollWorkerTask) -> Result<Option<Instant>, String> {
    if task.task_id.is_empty() {
        return Err("task ID is empty".to_string());
    }
    if task.request.is_none() {
        return Err(format!("task {:?} has no request", task.task_id));
    }
    task.deadline.as_ref().map(task_deadline).transpose()
}

/// Waits a bounded time for a handler that already queued its final output, then aborts it.
async fn finish_handler_after_final_output(mut handler: JoinHandle<()>) {
    match timeout_at(Instant::now() + FINAL_OUTPUT_FLUSH_GRACE, &mut handler).await {
        Ok(Ok(())) => {}
        Ok(Err(error)) => tracing::error!(%error, "pull Worker handler failed"),
        Err(_) => {
            tracing::warn!(
                "pull Worker handler did not return after its final output; aborting it"
            );
            handler.abort();
        }
    }
}

/// Half-closes the request side and reads until Server EOF or the stop deadline, extended by
/// [`FINAL_OUTPUT_FLUSH_GRACE`] when a final output may still be in flight. Returns `Ok(())` on
/// Server EOF. Returns an error after resetting the stream when it fails or the deadline expires.
async fn close_poll_send_and_wait(
    sender: &mut Option<mpsc::Sender<PollWorkerTaskRequest>>,
    responses: &mut tonic::Streaming<dex_protocol::dex::PollWorkerTaskResponse>,
    stop: &mut watch::Receiver<StopSignal>,
    stop_drain_timeout: Duration,
    has_unacknowledged_final_output: bool,
    reset_guard: &poll_transport::PollStreamResetGuard,
) -> Result<(), Status> {
    sender.take();
    let with_flush_grace = |deadline: Instant| {
        if has_unacknowledged_final_output {
            deadline + FINAL_OUTPUT_FLUSH_GRACE
        } else {
            deadline
        }
    };
    let mut deadline = with_flush_grace(
        stop.borrow()
            .deadline
            .unwrap_or_else(|| Instant::now() + stop_drain_timeout),
    );
    let result = loop {
        tokio::select! {
            response = responses.message() => {
                match response {
                    Ok(Some(_)) => {}
                    Ok(None) => return Ok(()),
                    Err(status) => break Err(status),
                }
            }
            () = sleep_until(deadline) => {
                break Err(Status::deadline_exceeded("Worker task stream did not close after drain"));
            }
            change = stop.changed() => {
                if change.is_err() {
                    break Err(Status::cancelled("Worker stop signal closed"));
                }
                if let Some(stop_deadline) = stop.borrow_and_update().deadline {
                    deadline = with_flush_grace(stop_deadline);
                }
            }
        }
    };
    reset_guard.reset();
    result
}

fn with_cancel_status(
    mut error: WorkerErrorResponse,
    cancel_code: &AtomicI32,
) -> WorkerErrorResponse {
    let code = cancel_code.load(Ordering::Acquire);
    if code != 0 {
        error.grpc_status = code;
    }
    error
}

fn is_permanent(code: Code) -> bool {
    matches!(
        code,
        Code::Unauthenticated
            | Code::PermissionDenied
            | Code::InvalidArgument
            | Code::Unimplemented
            | Code::ResourceExhausted
    )
}

fn invalid(message: impl Into<String>) -> SdkError {
    SdkError::FlowDefinition {
        message: message.into(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    use crate::value_hydrator::ValueHydrator;
    use crate::{
        BlobCache, BlobCacheConfig, Context, FileCredential, Flow, HandlerError, HandlerResult,
        Registry, Rpc, RpcList, RpcResult, Worker, WorkerOptions,
    };
    use dex_protocol::dex::flow_service_client::FlowServiceClient;
    use dex_protocol::dex::worker_task_service_server::{
        WorkerTaskService, WorkerTaskServiceServer,
    };
    use dex_protocol::dex::{
        Context as ProtoContext, InvokeWaitForMethodRequest, InvokeWorkerRpcRequest,
        PollWorkerTaskCancelTask, PollWorkerTaskFrame, PollWorkerTaskPing, PollWorkerTaskReconnect,
        PollWorkerTaskRegistered, PollWorkerTaskRejectReason, PollWorkerTaskResponse,
    };
    use prost::Message;
    use std::collections::VecDeque;
    use std::sync::Condvar;
    use std::sync::atomic::{AtomicBool, AtomicU64, AtomicUsize, Ordering};
    use tokio::net::TcpListener;
    use tokio::time::timeout;
    use tokio_stream::wrappers::{ReceiverStream, TcpListenerStream};
    use tonic::transport::Server;
    use tonic::{Request, Response};

    mod options;
    mod output_lifecycle;
    mod reserved_slots;
    mod task_queue;

    #[derive(Clone)]
    struct RegistrationService {
        opened: mpsc::Sender<mpsc::Sender<Result<PollWorkerTaskResponse, Status>>>,
    }

    #[tonic::async_trait]
    impl WorkerTaskService for RegistrationService {
        type PollWorkerTaskStream = ReceiverStream<Result<PollWorkerTaskResponse, Status>>;

        async fn poll_worker_task(
            &self,
            request: Request<tonic::Streaming<PollWorkerTaskRequest>>,
        ) -> Result<Response<Self::PollWorkerTaskStream>, Status> {
            let mut incoming = request.into_inner();
            let frame = incoming
                .message()
                .await?
                .ok_or_else(|| Status::invalid_argument("missing Register"))?;
            assert!(matches!(frame.frame, Some(RequestFrame::Register(_))));
            let (sender, receiver) = mpsc::channel(8);
            let closing_sender = sender.clone();
            self.opened
                .send(sender)
                .await
                .map_err(|error| Status::internal(error.to_string()))?;
            // The test runtime owns cancellation of this deliberately detached mock stream reader.
            let _stream_reader = tokio::spawn(async move {
                loop {
                    match incoming.message().await {
                        Ok(Some(_)) => {}
                        Ok(None) => {
                            if let Err(error) = closing_sender
                                .send(Err(Status::cancelled("Server acknowledged half-close")))
                                .await
                            {
                                eprintln!("close registration stream: {error}");
                            }
                            break;
                        }
                        Err(error) => {
                            eprintln!("registration test stream closed: {error}");
                            break;
                        }
                    }
                }
            });
            Ok(Response::new(ReceiverStream::new(receiver)))
        }
    }

    async fn send_registered(sender: &mpsc::Sender<Result<PollWorkerTaskResponse, Status>>) {
        sender
            .send(Ok(PollWorkerTaskResponse {
                frame: Some(ResponseFrame::Registered(
                    PollWorkerTaskRegistered::default(),
                )),
            }))
            .await
            .expect("send Registered");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn registration_transport_handles_permanent_rejections_and_stop() {
        for code in [
            Code::InvalidArgument,
            Code::ResourceExhausted,
            Code::Unimplemented,
            Code::PermissionDenied,
            Code::Unauthenticated,
        ] {
            for has_registered in [false, true] {
                for should_stop in [false, true] {
                    let listener = TcpListener::bind("127.0.0.1:0")
                        .await
                        .expect("bind registration service");
                    let address = listener.local_addr().expect("service address");
                    let (opened, mut incoming) = mpsc::channel(8);
                    let (server_stop, mut server_shutdown) = watch::channel(false);
                    let server = tokio::spawn(
                        Server::builder()
                            .add_service(WorkerTaskServiceServer::new(RegistrationService {
                                opened,
                            }))
                            .serve_with_incoming_shutdown(
                                TcpListenerStream::new(listener),
                                async move {
                                    while !*server_shutdown.borrow()
                                        && server_shutdown.changed().await.is_ok()
                                    {
                                    }
                                },
                            ),
                    );
                    let channel = outbound_task_channel(
                        Endpoint::from_shared(format!("http://{address}")).expect("endpoint"),
                        None,
                    );
                    let (cache, _directory) = test_cache();
                    let dispatcher = WorkerDispatcher::new(
                        Registry::new(),
                        ValueHydrator::new(FlowServiceClient::new(channel.clone()), cache),
                        crate::server_protocol::NegotiatedServerProtocolVersion::default(),
                    );
                    let worker = PullWorker::new(
                        &PullWorkerOptions::new()
                            .max_concurrent_tasks(1)
                            .stop_on_permanent_stream_rejection(should_stop),
                        &dispatcher,
                        channel,
                    )
                    .expect("pull Worker");
                    let registration = Arc::clone(&worker.registration);
                    let (stop, shutdown) = watch::channel(false);
                    let running = tokio::spawn(async move { worker.run(shutdown).await });
                    let sender = timeout(Duration::from_secs(5), incoming.recv())
                        .await
                        .expect("stream opened")
                        .expect("response sender");
                    if has_registered {
                        send_registered(&sender).await;
                        timeout(Duration::from_secs(5), registration.wait())
                            .await
                            .expect("readiness deadline")
                            .expect("ready");
                        assert!(registration.snapshot().is_ready);
                    }
                    sender
                        .send(Err(Status::new(code, "permanent rejection")))
                        .await
                        .expect("send rejection");
                    if !has_registered {
                        let error = timeout(Duration::from_secs(5), registration.wait())
                            .await
                            .expect("rejection deadline")
                            .expect_err("rejected");
                        assert!(
                            error
                                .pull_registration_error()
                                .is_some_and(|rejection| rejection.status.code() == code)
                        );
                    }
                    timeout(Duration::from_secs(5), async {
                        while registration.snapshot().queues[0]
                            .last_permanent_error
                            .is_none()
                        {
                            tokio::task::yield_now().await;
                        }
                    })
                    .await
                    .expect("rejection recorded");
                    assert!(!registration.snapshot().is_ready);
                    if !should_stop {
                        assert!(!running.is_finished());
                    }
                    if !should_stop {
                        stop.send_replace(true);
                    }
                    let result = timeout(Duration::from_secs(5), running)
                        .await
                        .expect("Worker stopped")
                        .expect("join Worker");
                    if should_stop {
                        assert!(
                            result
                                .expect_err("fatal rejection")
                                .pull_registration_error()
                                .is_some_and(|rejection| rejection.status.code() == code)
                        );
                    } else {
                        result.expect("ordinary Stop");
                    }
                    stop.send_replace(true);
                    if has_registered {
                        registration.wait().await.expect("latched registration");
                    }
                    drop(sender);
                    server_stop.send_replace(true);
                    timeout(Duration::from_secs(5), server)
                        .await
                        .expect("server stopped")
                        .expect("join server")
                        .expect("server result");
                }
            }
        }
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn registered_spare_stream_prevents_fatal_shutdown_and_recovers_readiness() {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind service");
        let address = listener.local_addr().expect("service address");
        let (opened, mut incoming) = mpsc::channel(8);
        let (server_stop, mut server_shutdown) = watch::channel(false);
        let server = tokio::spawn(
            Server::builder()
                .add_service(WorkerTaskServiceServer::new(RegistrationService { opened }))
                .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async move {
                    while !*server_shutdown.borrow() && server_shutdown.changed().await.is_ok() {}
                }),
        );
        let channel = outbound_task_channel(
            Endpoint::from_shared(format!("http://{address}")).expect("endpoint"),
            None,
        );
        let (cache, _directory) = test_cache();
        let dispatcher = WorkerDispatcher::new(
            Registry::new(),
            ValueHydrator::new(FlowServiceClient::new(channel.clone()), cache),
            crate::server_protocol::NegotiatedServerProtocolVersion::default(),
        );
        let worker = PullWorker::new(
            &PullWorkerOptions::new()
                .max_concurrent_tasks(2)
                .reserved_rpc_tasks(0)
                .stop_on_permanent_stream_rejection(true),
            &dispatcher,
            channel,
        )
        .expect("Worker");
        let registration = Arc::clone(&worker.registration);
        let (stop, shutdown) = watch::channel(false);
        let running = tokio::spawn(async move { worker.run(shutdown).await });
        let first = timeout(Duration::from_secs(5), incoming.recv())
            .await
            .expect("first stream")
            .expect("first sender");
        let second = timeout(Duration::from_secs(5), incoming.recv())
            .await
            .expect("second stream")
            .expect("second sender");
        send_registered(&first).await;
        timeout(Duration::from_secs(5), registration.wait())
            .await
            .expect("readiness deadline")
            .expect("ready");
        second
            .send(Err(Status::permission_denied("rejected")))
            .await
            .expect("reject stream");
        timeout(Duration::from_secs(5), async {
            while registration.snapshot().queues[0]
                .last_permanent_error
                .is_none()
            {
                tokio::task::yield_now().await;
            }
        })
        .await
        .expect("rejection recorded");
        assert!(registration.snapshot().is_ready);
        assert!(registration.permanent_error().is_none());
        first
            .send(Err(Status::unavailable("transient")))
            .await
            .expect("disconnect registered stream");
        timeout(Duration::from_secs(5), async {
            while registration.snapshot().queues[0].has_registered_stream {
                tokio::task::yield_now().await;
            }
        })
        .await
        .expect("disconnect recorded");
        assert!(!registration.snapshot().is_ready);
        assert!(registration.permanent_error().is_none());
        let replacement = timeout(Duration::from_secs(5), incoming.recv())
            .await
            .expect("replacement stream")
            .expect("replacement sender");
        send_registered(&replacement).await;
        timeout(Duration::from_secs(5), async {
            while !registration.snapshot().is_ready {
                tokio::task::yield_now().await;
            }
        })
        .await
        .expect("readiness restored");
        assert!(!running.is_finished());
        assert!(
            registration.snapshot().queues[0]
                .last_permanent_error
                .is_none()
        );
        stop.send_replace(true);
        timeout(Duration::from_secs(5), running)
            .await
            .expect("Worker stopped")
            .expect("join Worker")
            .expect("ordinary shutdown");
        drop((first, second, replacement));
        server_stop.send_replace(true);
        timeout(Duration::from_secs(5), server)
            .await
            .expect("server stopped")
            .expect("join server")
            .expect("server result");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn permanent_rejection_updates_readiness_before_cancelled_handler_returns() {
        for rejection_code in [Code::PermissionDenied, Code::Unauthenticated] {
            assert_rejection_updates_readiness_before_cancelled_handler_returns(rejection_code)
                .await;
        }
    }

    async fn assert_rejection_updates_readiness_before_cancelled_handler_returns(
        rejection_code: Code,
    ) {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind service");
        let address = listener.local_addr().expect("service address");
        let (opened, mut incoming) = mpsc::channel(8);
        let (server_stop, mut server_shutdown) = watch::channel(false);
        let server = tokio::spawn(
            Server::builder()
                .add_service(WorkerTaskServiceServer::new(RegistrationService { opened }))
                .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async move {
                    while !*server_shutdown.borrow() && server_shutdown.changed().await.is_ok() {}
                }),
        );
        let credential_directory = tempfile::tempdir().expect("credential directory");
        let credential_path = credential_directory.path().join("access-token");
        std::fs::write(&credential_path, "file-access-token").expect("write credential");
        let credential: Arc<dyn Credential> =
            Arc::new(FileCredential::new(&credential_path).expect("file credential"));
        let channel = outbound_task_channel(
            Endpoint::from_shared(format!("http://{address}")).expect("endpoint"),
            Some(credential),
        );
        let (cache, _directory) = test_cache();
        let started = Arc::new(Notify::new());
        let release = Arc::new((Mutex::new(false), Condvar::new()));
        let registry = Registry::new()
            .register(DrainFlow {
                started: Arc::clone(&started),
                release: Arc::clone(&release),
                wait_for_cancel: false,
                result_size: None,
            })
            .expect("register Flow");
        let dispatcher = WorkerDispatcher::new(
            registry,
            ValueHydrator::new(FlowServiceClient::new(channel.clone()), cache),
            crate::server_protocol::NegotiatedServerProtocolVersion::default(),
        );
        let worker = PullWorker::new(
            &PullWorkerOptions::new()
                .max_concurrent_tasks(2)
                .stop_drain_timeout(Duration::from_millis(100))
                .stop_on_permanent_stream_rejection(true),
            &dispatcher,
            channel,
        )
        .expect("Worker");
        let registration = Arc::clone(&worker.registration);
        let (stop, shutdown) = watch::channel(false);
        let running = tokio::spawn(async move { worker.run(shutdown).await });
        let first = timeout(Duration::from_secs(5), incoming.recv())
            .await
            .expect("first stream")
            .expect("first sender");
        let second = timeout(Duration::from_secs(5), incoming.recv())
            .await
            .expect("second stream")
            .expect("second sender");
        send_registered(&first).await;
        send_registered(&second).await;
        timeout(Duration::from_secs(5), registration.wait())
            .await
            .expect("readiness deadline")
            .expect("ready");
        first
            .send(Ok(PollWorkerTaskResponse {
                frame: Some(ResponseFrame::Task(PollWorkerTask {
                    task_id: "rejected-busy-task".to_string(),
                    deadline: None,
                    request: Some(TaskRequest::Rpc(InvokeWorkerRpcRequest {
                        context: Some(ProtoContext {
                            flow_id: "flow-1".to_string(),
                            ..Default::default()
                        }),
                        flow_type: "DrainFlow".to_string(),
                        rpc_name: "get_drain_result".to_string(),
                        input: Some(crate::value_mapper::encode(&()).expect("encode input")),
                        ..Default::default()
                    })),
                })),
            }))
            .await
            .expect("send RPC task");
        timeout(Duration::from_secs(5), started.notified())
            .await
            .expect("handler started");
        first
            .send(Err(Status::new(rejection_code, "rejected while busy")))
            .await
            .expect("reject stream");
        let recorded = timeout(Duration::from_secs(5), async {
            while registration.snapshot().is_ready {
                tokio::task::yield_now().await;
            }
        })
        .await;
        let is_waiting_for_handler = !running.is_finished();
        let (lock, changed) = &*release;
        *lock.lock().expect("handler gate lock") = true;
        changed.notify_all();
        let result = timeout(Duration::from_secs(5), running)
            .await
            .expect("Worker stopped")
            .expect("join Worker");
        stop.send_replace(true);
        drop((first, second));
        server_stop.send_replace(true);
        timeout(Duration::from_secs(5), server)
            .await
            .expect("server stopped")
            .expect("join server")
            .expect("server result");
        recorded.expect("rejection recorded before handler release");
        assert!(is_waiting_for_handler);
        assert!(
            result
                .expect_err("fatal rejection")
                .pull_registration_error()
                .is_some()
        );
    }

    #[test]
    fn registration_wait_reports_start_failure() {
        let (cache, _directory) = test_cache();
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("wait runtime");
        let worker = Worker::try_new(
            Registry::new(),
            Arc::clone(&cache),
            WorkerOptions::new()
                .server_address("127.0.0.1:1")
                .attribute_index_sync_timeout(Duration::from_millis(100))
                .pull(PullWorkerOptions::new()),
        )
        .expect("Worker");
        let failure = worker.start().expect_err("startup failed");
        let waiting = runtime
            .block_on(worker.wait_pull_registered())
            .expect_err("wait failed");
        assert_eq!(failure.to_string(), waiting.to_string());
        worker.stop();
    }

    struct RegisteredFlow;

    impl Flow for RegisteredFlow {
        type StartInput = ();
    }

    fn test_cache() -> (Arc<BlobCache>, tempfile::TempDir) {
        let directory = tempfile::tempdir().expect("cache directory");
        let cache = Arc::new(
            BlobCache::open(
                BlobCacheConfig::new(directory.path(), 1024 * 1024, 0).expect("cache config"),
            )
            .expect("open cache"),
        );
        (cache, directory)
    }

    struct ReserveWarningSubscriber(Arc<AtomicU64>);

    impl tracing::Subscriber for ReserveWarningSubscriber {
        fn register_callsite(
            &self,
            metadata: &'static tracing::Metadata<'static>,
        ) -> tracing::subscriber::Interest {
            if self.enabled(metadata) {
                tracing::subscriber::Interest::always()
            } else {
                tracing::subscriber::Interest::never()
            }
        }
        fn max_level_hint(&self) -> Option<tracing::level_filters::LevelFilter> {
            Some(tracing::level_filters::LevelFilter::WARN)
        }
        fn enabled(&self, metadata: &tracing::Metadata<'_>) -> bool {
            *metadata.level() == tracing::Level::WARN
        }
        fn new_span(&self, _: &tracing::span::Attributes<'_>) -> tracing::span::Id {
            tracing::span::Id::from_u64(1)
        }
        fn record(&self, _: &tracing::span::Id, _: &tracing::span::Record<'_>) {}
        fn record_follows_from(&self, _: &tracing::span::Id, _: &tracing::span::Id) {}
        fn event(&self, event: &tracing::Event<'_>) {
            if *event.metadata().level() == tracing::Level::WARN {
                self.0.fetch_add(1, Ordering::Relaxed);
            }
        }
        fn enter(&self, _: &tracing::span::Id) {}
        fn exit(&self, _: &tracing::span::Id) {}
    }

    #[tokio::test]
    async fn defaults_reserve_one_and_warns_for_single_capacity() {
        let warnings = Arc::new(AtomicU64::new(0));
        let _subscriber =
            tracing::subscriber::set_default(ReserveWarningSubscriber(Arc::clone(&warnings)));
        for capacity in [1, 4] {
            let channel = outbound_task_channel(Endpoint::from_static("http://127.0.0.1:1"), None);
            let (cache, _directory) = test_cache();
            let dispatcher = WorkerDispatcher::new(
                Registry::new(),
                ValueHydrator::new(FlowServiceClient::new(channel.clone()), cache),
                crate::server_protocol::NegotiatedServerProtocolVersion::default(),
            );
            let worker = PullWorker::new(
                &PullWorkerOptions::new().max_concurrent_tasks(capacity),
                &dispatcher,
                channel,
            )
            .expect("Worker");
            assert_eq!(usize::from(capacity >= 2), worker.reserved_rpc_tasks);
            let (_stop, shutdown) = watch::channel(true);
            worker.run(shutdown).await.expect("stop Worker");
        }
        assert_eq!(1, warnings.load(Ordering::Relaxed));
    }

    #[test]
    fn later_lower_registration_limit_only_warns() {
        let warnings = Arc::new(AtomicU64::new(0));
        let _subscriber =
            tracing::subscriber::set_default(ReserveWarningSubscriber(Arc::clone(&warnings)));
        let shared = PullShared {
            registration: Arc::default(),
            state: Mutex::new(PullState {
                streams: HashMap::new(),
                busy_tasks: 0,
                is_stopping: false,
                logged_unsupported: false,
            }),
            notify: Notify::new(),
            max_concurrent_tasks: 2,
            has_checked_registration_limit: Mutex::new(false),
            startup_error: Mutex::new(None),
            task_start_gate: Arc::new(Mutex::new(false)),
        };
        shared
            .check_registration_limit(2)
            .expect("first registration within the limit");
        shared
            .check_registration_limit(1)
            .expect("a later lower limit does not stop the Worker");
        assert_eq!(1, warnings.load(Ordering::Relaxed));
        assert!(
            shared
                .startup_error
                .lock()
                .expect("startup error lock")
                .is_none()
        );
        assert!(!*shared.task_start_gate.lock().expect("task start gate lock"));
    }

    #[test]
    fn generates_distinct_valid_app_worker_ids() {
        let first = generate_app_worker_id();
        let second = generate_app_worker_id();
        assert_ne!(first, second);
        validate_app_worker_id(&first).expect("valid first ID");
        assert!(first.len() <= 63);
        let suffix = first.rsplit('-').next().expect("ID suffix");
        assert_eq!(8, suffix.len());
        assert!(
            suffix
                .bytes()
                .all(|byte| byte.is_ascii_lowercase() || (b'2'..=b'7').contains(&byte))
        );
        assert!(validate_app_worker_id("").is_err());
        assert!(validate_app_worker_id(&"a".repeat(64)).is_err());
        assert!(validate_app_worker_id("has space").is_err());
    }

    #[test]
    fn maps_handler_failure_fields_into_pull_frame() {
        let error = worker_error_response(HandlerError::retry_after(
            7,
            "PaymentDeclined",
            "card declined",
        ));
        assert_eq!(Code::Unknown as i32, error.grpc_status);
        assert_eq!("card declined", error.detail);
        assert_eq!("PaymentDeclined", error.error_type);
        assert_eq!(7, error.retry_after_seconds);
        assert!(error.stack_trace.contains("maps_handler_failure_fields"));
        let cancelled_reason = AtomicI32::new(Code::Cancelled as i32);
        let cancelled_error = with_cancel_status(error.clone(), &cancelled_reason);
        assert_eq!(Code::Cancelled as i32, cancelled_error.grpc_status);
        assert_eq!(error.detail, cancelled_error.detail);
        assert_eq!(error.error_type, cancelled_error.error_type);
        assert_eq!(error.stack_trace, cancelled_error.stack_trace);
        assert_eq!(
            error.retry_after_seconds,
            cancelled_error.retry_after_seconds
        );
        let deadline_reason = AtomicI32::new(Code::DeadlineExceeded as i32);
        assert_eq!(
            Code::DeadlineExceeded as i32,
            with_cancel_status(error, &deadline_reason).grpc_status
        );
    }

    #[test]
    fn expired_task_deadline_is_immediate() {
        for seconds in [0, -1, -62_135_596_800] {
            let deadline = task_deadline(&prost_types::Timestamp { seconds, nanos: 0 })
                .expect("valid expired deadline");
            assert!(deadline <= Instant::now());
        }
    }

    #[test]
    fn malformed_task_deadline_is_an_error() {
        for (seconds, nanos) in [
            (i64::MAX, -1),
            (0, -1),
            (0, 1_000_000_000),
            (i64::MAX, 0),
            (i64::MIN, 0),
            (253_402_300_800, 0),
        ] {
            assert!(task_deadline(&prost_types::Timestamp { seconds, nanos }).is_err());
        }
    }

    #[tokio::test]
    async fn cancellation_waits_for_the_handler_to_return() {
        let cancellation = InvocationCancellation::new();
        let waiting = cancellation.clone();
        let handler = tokio::spawn(async move { waiting.cancelled().await });
        cancellation.cancel();
        timeout(Duration::from_secs(1), handler)
            .await
            .expect("handler returned")
            .expect("join handler");
    }

    #[tokio::test]
    async fn cancelled_task_does_not_block_on_a_full_outbound_stream() {
        let (sender, _receiver) = mpsc::channel(1);
        sender
            .try_send(PollWorkerTaskRequest {
                frame: Some(RequestFrame::Pong(PollWorkerTaskPong {})),
            })
            .expect("fill outbound stream");
        let cancellation = InvocationCancellation::new();
        cancellation.cancel();
        let result = timeout(
            Duration::from_secs(1),
            TaskFrameSender::new(
                "task-1".to_string(),
                sender,
                cancellation,
                InvocationCancellation::new(),
                Arc::new(AtomicI32::new(0)),
                Arc::new(Mutex::new(false)),
            )
            .send(Output::WorkerError(WorkerErrorResponse::default())),
        )
        .await
        .expect("send stopped");
        assert_eq!(Code::Cancelled, result.expect_err("cancelled send").code());
    }

    #[derive(Clone)]
    struct ReconnectService {
        registrations: Arc<AtomicUsize>,
        pongs: Arc<AtomicUsize>,
        notify: Arc<Notify>,
        headers: Arc<Mutex<Vec<Option<String>>>>,
        credential_token: Arc<Mutex<String>>,
    }

    struct RotatingCredential(Arc<Mutex<String>>);

    impl Credential for RotatingCredential {
        fn token(&self) -> Result<String, crate::CredentialError> {
            Ok(self.0.lock().expect("credential token lock").clone())
        }
    }

    #[tonic::async_trait]
    impl WorkerTaskService for ReconnectService {
        type PollWorkerTaskStream = ReceiverStream<Result<PollWorkerTaskResponse, Status>>;

        async fn poll_worker_task(
            &self,
            request: Request<tonic::Streaming<PollWorkerTaskRequest>>,
        ) -> Result<Response<Self::PollWorkerTaskStream>, Status> {
            self.headers.lock().expect("headers lock").push(
                request
                    .metadata()
                    .get("authorization")
                    .map(|value| value.to_str().expect("ASCII bearer header").to_string()),
            );
            let mut incoming = request.into_inner();
            let first = incoming
                .message()
                .await?
                .ok_or_else(|| Status::invalid_argument("missing Register"))?;
            let Some(RequestFrame::Register(register)) = first.frame else {
                return Err(Status::invalid_argument("Register must be first"));
            };
            assert_eq!("rust", register.sdk_name);
            assert!(register.queue.is_empty());
            assert!(register.supports_reserved_slot_dispatch);
            if self.registrations.fetch_add(1, Ordering::SeqCst) == 0 {
                *self.credential_token.lock().expect("credential token lock") =
                    "rotated".to_string();
            }
            self.notify.notify_one();
            let (sender, receiver) = mpsc::channel(4);
            let pongs = Arc::clone(&self.pongs);
            // The test runtime owns cancellation of this deliberately detached mock stream reader.
            let _stream_reader = tokio::spawn(async move {
                let registered = PollWorkerTaskResponse {
                    frame: Some(ResponseFrame::Registered(PollWorkerTaskRegistered {
                        supports_unknown_name_rejection: false,
                        ..Default::default()
                    })),
                };
                if sender.send(Ok(registered)).await.is_err() {
                    return;
                }
                let ping = PollWorkerTaskResponse {
                    frame: Some(ResponseFrame::Ping(PollWorkerTaskPing {})),
                };
                if sender.send(Ok(ping)).await.is_err() {
                    return;
                }
                match incoming.message().await {
                    Ok(Some(PollWorkerTaskRequest {
                        frame: Some(RequestFrame::Pong(_)),
                    })) => {
                        pongs.fetch_add(1, Ordering::SeqCst);
                    }
                    Ok(other) => {
                        eprintln!("expected Pong frame, received {other:?}");
                        return;
                    }
                    Err(error) => {
                        eprintln!("read Pong frame: {error}");
                        return;
                    }
                }
                let reconnect = PollWorkerTaskResponse {
                    frame: Some(ResponseFrame::Reconnect(PollWorkerTaskReconnect {
                        reason: "test".to_string(),
                    })),
                };
                if let Err(error) = sender.send(Ok(reconnect)).await {
                    eprintln!("send reconnect frame: {error}");
                }
            });
            Ok(Response::new(ReceiverStream::new(receiver)))
        }
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn reconnects_after_ping_and_drains_idle_stream_on_stop() {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind fake task service");
        let address = listener.local_addr().expect("fake service address");
        let service = ReconnectService {
            registrations: Arc::new(AtomicUsize::new(0)),
            pongs: Arc::new(AtomicUsize::new(0)),
            notify: Arc::new(Notify::new()),
            headers: Arc::new(Mutex::new(Vec::new())),
            credential_token: Arc::new(Mutex::new("initial".to_string())),
        };
        let (server_stop, mut server_shutdown) = watch::channel(false);
        let server = tokio::spawn(
            Server::builder()
                .add_service(WorkerTaskServiceServer::new(service.clone()))
                .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async move {
                    while !*server_shutdown.borrow() && server_shutdown.changed().await.is_ok() {}
                }),
        );
        let channel = Endpoint::from_shared(format!("http://{address}"))
            .expect("endpoint")
            .connect_lazy();
        let channel = CredentialChannel::new(
            channel,
            CredentialInterceptor::new(Some(Arc::new(RotatingCredential(Arc::clone(
                &service.credential_token,
            ))))),
        );
        let (cache, _directory) = test_cache();
        let dispatcher = WorkerDispatcher::new(
            Registry::new(),
            ValueHydrator::new(FlowServiceClient::new(channel.clone()), cache),
            crate::server_protocol::NegotiatedServerProtocolVersion::default(),
        );
        let worker = PullWorker::new(
            &PullWorkerOptions::new().max_concurrent_tasks(1),
            &dispatcher,
            channel,
        )
        .expect("pull Worker");
        let (stop, shutdown) = watch::channel(false);
        let running = tokio::spawn(async move { worker.run(shutdown).await });
        timeout(Duration::from_secs(5), async {
            while service.registrations.load(Ordering::SeqCst) < 2 {
                service.notify.notified().await;
            }
        })
        .await
        .expect("second registration");
        assert!(service.pongs.load(Ordering::SeqCst) >= 1);
        assert_eq!(
            &service.headers.lock().expect("headers lock")[..2],
            &[
                Some("Bearer initial".to_string()),
                Some("Bearer rotated".to_string())
            ]
        );
        stop.send_replace(true);
        timeout(Duration::from_secs(5), running)
            .await
            .expect("pull Worker stopped")
            .expect("join Worker")
            .expect("Worker result");
        server_stop.send_replace(true);
        timeout(Duration::from_secs(5), server)
            .await
            .expect("fake server stopped")
            .expect("join server")
            .expect("server result");
    }

    #[derive(Clone)]
    struct UnknownTaskService {
        capabilities: Arc<Mutex<VecDeque<bool>>>,
        received: mpsc::Sender<PollWorkerTaskRequest>,
    }

    #[tonic::async_trait]
    impl WorkerTaskService for UnknownTaskService {
        type PollWorkerTaskStream = ReceiverStream<Result<PollWorkerTaskResponse, Status>>;

        async fn poll_worker_task(
            &self,
            request: Request<tonic::Streaming<PollWorkerTaskRequest>>,
        ) -> Result<Response<Self::PollWorkerTaskStream>, Status> {
            let mut incoming = request.into_inner();
            let register = incoming
                .message()
                .await?
                .ok_or_else(|| Status::invalid_argument("missing Register"))?;
            if !matches!(register.frame, Some(RequestFrame::Register(_))) {
                return Err(Status::invalid_argument("Register must be first"));
            }
            let supports_unknown_name_rejection = self
                .capabilities
                .lock()
                .expect("capabilities lock")
                .pop_front()
                .ok_or_else(|| Status::internal("unexpected registration"))?;
            let (sender, receiver) = mpsc::channel(4);
            sender
                .send(Ok(PollWorkerTaskResponse {
                    frame: Some(ResponseFrame::Registered(PollWorkerTaskRegistered {
                        supports_unknown_name_rejection,
                        ..Default::default()
                    })),
                }))
                .await
                .map_err(|_| Status::internal("registration response closed"))?;
            send_unknown_task(&sender, "unknown-step", true).await?;
            let received = self.received.clone();
            // The test runtime owns cancellation of this deliberately detached mock stream reader.
            let _stream_reader = tokio::spawn(async move {
                let mut completed_tasks = 0;
                while let Ok(Some(frame)) = incoming.message().await {
                    if matches!(
                        frame.frame,
                        Some(RequestFrame::TaskFrame(_)) | Some(RequestFrame::Reject(_))
                    ) {
                        if received.send(frame).await.is_err() {
                            return;
                        }
                        completed_tasks += 1;
                        if completed_tasks == 1 {
                            if send_unknown_task(&sender, "unknown-rpc", false)
                                .await
                                .is_err()
                            {
                                return;
                            }
                        } else if sender
                            .send(Ok(PollWorkerTaskResponse {
                                frame: Some(ResponseFrame::Ping(PollWorkerTaskPing {})),
                            }))
                            .await
                            .is_err()
                        {
                            return;
                        }
                    } else if matches!(frame.frame, Some(RequestFrame::Pong(_))) {
                        received
                            .send(frame)
                            .await
                            .expect("the test retains the Pong receiver until the frame arrives");
                        return;
                    }
                }
            });
            Ok(Response::new(ReceiverStream::new(receiver)))
        }
    }

    async fn send_unknown_task(
        sender: &mpsc::Sender<Result<PollWorkerTaskResponse, Status>>,
        task_id: &str,
        is_step: bool,
    ) -> Result<(), Status> {
        let value = crate::value_mapper::encode(&"unknown".to_string())
            .map_err(|error| Status::internal(error.to_string()))?;
        let context = Some(ProtoContext {
            flow_id: "flow-1".to_string(),
            ..Default::default()
        });
        let request = if is_step {
            TaskRequest::WaitFor(InvokeWaitForMethodRequest {
                context,
                flow_type: "TestFlow".to_string(),
                step_type: "MissingStep".to_string(),
                step_input: Some(value),
                ..Default::default()
            })
        } else {
            TaskRequest::Rpc(InvokeWorkerRpcRequest {
                context,
                flow_type: "TestFlow".to_string(),
                rpc_name: "MissingRPC".to_string(),
                input: Some(value),
                ..Default::default()
            })
        };
        sender
            .send(Ok(PollWorkerTaskResponse {
                frame: Some(ResponseFrame::Task(PollWorkerTask {
                    task_id: task_id.to_string(),
                    deadline: None,
                    request: Some(request),
                })),
            }))
            .await
            .map_err(|_| Status::internal("task response closed"))
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn reserved_slots_reject_unknown_names_regardless_of_legacy_capability() {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind fake task service");
        let address = listener.local_addr().expect("fake service address");
        let (received, mut frames) = mpsc::channel(9);
        let (server_stop, mut server_shutdown) = watch::channel(false);
        let server = tokio::spawn(
            Server::builder()
                .add_service(WorkerTaskServiceServer::new(UnknownTaskService {
                    capabilities: Arc::new(Mutex::new(VecDeque::from([false, true, false]))),
                    received,
                }))
                .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async move {
                    while !*server_shutdown.borrow() && server_shutdown.changed().await.is_ok() {}
                }),
        );
        let channel = CredentialChannel::new(
            Endpoint::from_shared(format!("http://{address}"))
                .expect("endpoint")
                .connect_lazy(),
            CredentialInterceptor::new(None),
        );
        let (cache, _directory) = test_cache();
        let dispatcher = WorkerDispatcher::new(
            Registry::new(),
            ValueHydrator::new(FlowServiceClient::new(channel.clone()), cache),
            crate::server_protocol::NegotiatedServerProtocolVersion::default(),
        );
        let shared = Arc::new(PullShared {
            registration: Arc::default(),
            state: Mutex::new(PullState {
                streams: HashMap::from([(
                    1,
                    StreamSlot {
                        is_busy: false,
                        is_registered: false,
                        is_draining: false,
                    },
                )]),
                busy_tasks: 0,
                is_stopping: false,
                logged_unsupported: false,
            }),
            notify: Notify::new(),
            max_concurrent_tasks: 1,
            has_checked_registration_limit: Mutex::new(false),
            startup_error: Mutex::new(None),
            task_start_gate: Arc::new(Mutex::new(false)),
        });
        let (_stop, stop_receiver) = watch::channel(StopSignal {
            phase: 0,
            deadline: None,
        });
        let mut stream = PullStream {
            channel,
            credential: None,
            dispatcher,
            app_worker_id: "worker".to_string(),
            flow_types: Vec::new(),
            task_queue: String::new(),
            queue: "STEP_AND_RPC".to_string(),
            slot_kind: PollWorkerSlotKind::StepAndRpc,
            stream_id: 1,
            shared,
            stop: stop_receiver,
            stop_drain_timeout: Duration::from_secs(30),
            is_registered: false,
            has_recorded_result: false,
            has_task_queue_mismatch: false,
        };
        for _ in 0..3 {
            let (was_registered, _, result) = timeout(Duration::from_secs(5), stream.poll())
                .await
                .expect("poll returned");
            assert!(was_registered);
            result.expect("poll result");
        }
        let mut received = Vec::new();
        for _ in 0..9 {
            received.push(
                timeout(Duration::from_secs(5), frames.recv())
                    .await
                    .expect("Worker frame timeout")
                    .expect("Worker frame channel"),
            );
        }
        for connection_frames in received.chunks(3) {
            assert_unknown_rejections(connection_frames);
        }
        server_stop.send_replace(true);
        timeout(Duration::from_secs(5), server)
            .await
            .expect("fake server stopped")
            .expect("join server")
            .expect("server result");
    }

    fn assert_unknown_rejections(frames: &[PollWorkerTaskRequest]) {
        let Some(RequestFrame::Reject(step)) = frames[0].frame.as_ref() else {
            panic!("expected unknown Step rejection");
        };
        assert_eq!(PollWorkerTaskRejectReason::UnknownStep as i32, step.reason);
        let Some(RequestFrame::Reject(rpc)) = frames[1].frame.as_ref() else {
            panic!("expected unknown RPC rejection");
        };
        assert_eq!(PollWorkerTaskRejectReason::UnknownRpc as i32, rpc.reason);
        assert!(matches!(frames[2].frame, Some(RequestFrame::Pong(_))));
    }

    const GET_DRAIN_RESULT: Rpc<(), String> = Rpc::new("get_drain_result");

    struct DrainFlow {
        started: Arc<Notify>,
        release: Arc<(Mutex<bool>, Condvar)>,
        wait_for_cancel: bool,
        result_size: Option<usize>,
    }

    impl DrainFlow {
        fn get_drain_result(&self, context: &mut Context) -> HandlerResult<RpcResult<String>> {
            self.started.notify_one();
            if self.wait_for_cancel {
                context.wait_for_cancellation();
                return Err(HandlerError::new("Cancelled", "handler was cancelled"));
            }
            let (lock, changed) = &*self.release;
            let mut is_released = lock.lock().expect("drain gate lock");
            while !*is_released {
                is_released = changed.wait(is_released).expect("drain gate lock");
            }
            Ok(RpcResult::new(match self.result_size {
                Some(result_size) => "x".repeat(result_size),
                None => "done".to_string(),
            }))
        }
    }

    impl Flow for DrainFlow {
        type StartInput = ();

        fn rpcs(&self) -> RpcList<Self> {
            RpcList::new().function_without_input(GET_DRAIN_RESULT, Self::get_drain_result)
        }
    }

    enum DrainEvent {
        FinalFrame(Box<PollWorkerTaskFrame>),
        KeepalivePong,
        HalfClosed,
        ReadError(Status),
    }

    #[derive(Clone)]
    struct DrainService {
        events: mpsc::Sender<DrainEvent>,
        assigned: Arc<AtomicBool>,
        cancel_kind: Option<Code>,
        read_delay: Duration,
        is_reconnecting: bool,
        ready: Option<mpsc::Sender<()>>,
        record_keepalive_pongs: bool,
    }

    #[tonic::async_trait]
    impl WorkerTaskService for DrainService {
        type PollWorkerTaskStream = ReceiverStream<Result<PollWorkerTaskResponse, Status>>;

        async fn poll_worker_task(
            &self,
            request: Request<tonic::Streaming<PollWorkerTaskRequest>>,
        ) -> Result<Response<Self::PollWorkerTaskStream>, Status> {
            let mut incoming = request.into_inner();
            let first = incoming
                .message()
                .await?
                .ok_or_else(|| Status::invalid_argument("missing Register"))?;
            let Some(RequestFrame::Register(register)) = first.frame else {
                return Err(Status::invalid_argument("Register must be first"));
            };
            let (sender, receiver) = mpsc::channel(4);
            let registered = PollWorkerTaskResponse {
                frame: Some(ResponseFrame::Registered(PollWorkerTaskRegistered {
                    supports_unknown_name_rejection: false,
                    ..Default::default()
                })),
            };
            sender
                .send(Ok(registered))
                .await
                .map_err(|_| Status::internal("registration response closed"))?;
            let is_task_stream = register.flow_types.contains(&"DrainFlow".to_string())
                && !self.assigned.swap(true, Ordering::AcqRel);
            if is_task_stream {
                let request = InvokeWorkerRpcRequest {
                    context: Some(ProtoContext {
                        flow_id: "flow-1".to_string(),
                        run_id: "run-1".to_string(),
                        ..Default::default()
                    }),
                    flow_type: "DrainFlow".to_string(),
                    rpc_name: "get_drain_result".to_string(),
                    input: Some(
                        crate::value_mapper::encode(&())
                            .map_err(|error| Status::internal(error.to_string()))?,
                    ),
                    ..Default::default()
                };
                let task = PollWorkerTaskResponse {
                    frame: Some(ResponseFrame::Task(PollWorkerTask {
                        task_id: "drain-task".to_string(),
                        deadline: (self.cancel_kind == Some(Code::DeadlineExceeded)).then_some(
                            prost_types::Timestamp {
                                seconds: 0,
                                nanos: 0,
                            },
                        ),
                        request: Some(TaskRequest::Rpc(request)),
                    })),
                };
                sender
                    .send(Ok(task))
                    .await
                    .map_err(|_| Status::internal("task response closed"))?;
                if self.cancel_kind == Some(Code::Cancelled) {
                    sender
                        .send(Ok(PollWorkerTaskResponse {
                            frame: Some(ResponseFrame::CancelTask(PollWorkerTaskCancelTask {
                                task_id: "drain-task".to_string(),
                            })),
                        }))
                        .await
                        .map_err(|_| Status::internal("cancel response closed"))?;
                }
                if self.is_reconnecting {
                    sender
                        .send(Ok(PollWorkerTaskResponse {
                            frame: Some(ResponseFrame::Reconnect(PollWorkerTaskReconnect {
                                reason: "test".to_string(),
                            })),
                        }))
                        .await
                        .map_err(|_| Status::internal("reconnect response closed"))?;
                }
                if !self.read_delay.is_zero() {
                    sender
                        .send(Ok(PollWorkerTaskResponse {
                            frame: Some(ResponseFrame::Ping(PollWorkerTaskPing {})),
                        }))
                        .await
                        .map_err(|_| Status::internal("ping response closed"))?;
                }
            }
            let events = self.events.clone();
            let read_delay = self.read_delay;
            let ready = self.ready.clone();
            let record_keepalive_pongs = self.record_keepalive_pongs;
            // The test runtime owns cancellation of this deliberately detached mock stream reader.
            let _stream_reader = tokio::spawn(async move {
                if is_task_stream && !read_delay.is_zero() {
                    match incoming.message().await {
                        Ok(Some(PollWorkerTaskRequest {
                            frame: Some(RequestFrame::Pong(_)),
                        })) => {}
                        Ok(other) => {
                            eprintln!("expected Pong before delayed read, received {other:?}");
                            return;
                        }
                        Err(error) => {
                            eprintln!("read Pong before delayed read: {error}");
                            return;
                        }
                    }
                    if let Some(ready) = ready
                        && let Err(error) = ready.send(()).await
                    {
                        eprintln!("record delayed read readiness: {error}");
                        return;
                    }
                    sleep(read_delay).await;
                }
                loop {
                    match incoming.message().await {
                        Ok(Some(PollWorkerTaskRequest {
                            frame: Some(RequestFrame::TaskFrame(frame)),
                        })) if is_task_stream => {
                            if let Err(error) =
                                events.send(DrainEvent::FinalFrame(Box::new(frame))).await
                            {
                                eprintln!("record final frame: {error}");
                                break;
                            }
                        }
                        Ok(Some(PollWorkerTaskRequest {
                            frame: Some(RequestFrame::Pong(_)),
                        })) if record_keepalive_pongs && is_task_stream => {
                            if let Err(error) = events.send(DrainEvent::KeepalivePong).await {
                                eprintln!("record keepalive Pong: {error}");
                                break;
                            }
                        }
                        Ok(Some(_)) => {}
                        Ok(None) => {
                            if is_task_stream
                                && let Err(error) = events.send(DrainEvent::HalfClosed).await
                            {
                                eprintln!("record half-close: {error}");
                            }
                            break;
                        }
                        Err(status) => {
                            if is_task_stream
                                && let Err(error) = events.send(DrainEvent::ReadError(status)).await
                            {
                                eprintln!("record stream error: {error}");
                            }
                            break;
                        }
                    }
                }
                drop(sender);
            });
            Ok(Response::new(ReceiverStream::new(receiver)))
        }
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn stop_drain_delivers_final_frame_before_half_close() {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind fake task service");
        let address = listener.local_addr().expect("fake service address");
        let (events, mut received) = mpsc::channel(4);
        let (server_stop, mut server_shutdown) = watch::channel(false);
        let server = tokio::spawn(
            Server::builder()
                .add_service(WorkerTaskServiceServer::new(DrainService {
                    events,
                    assigned: Arc::new(AtomicBool::new(false)),
                    cancel_kind: None,
                    read_delay: Duration::ZERO,
                    is_reconnecting: false,
                    ready: None,
                    record_keepalive_pongs: false,
                }))
                .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async move {
                    while !*server_shutdown.borrow() && server_shutdown.changed().await.is_ok() {}
                }),
        );
        let channel = Endpoint::from_shared(format!("http://{address}"))
            .expect("endpoint")
            .connect_lazy();
        let channel = CredentialChannel::new(channel, CredentialInterceptor::new(None));
        let (cache, _directory) = test_cache();
        let started = Arc::new(Notify::new());
        let release = Arc::new((Mutex::new(false), Condvar::new()));
        let registry = Registry::new()
            .register(DrainFlow {
                started: Arc::clone(&started),
                release: Arc::clone(&release),
                wait_for_cancel: false,
                result_size: None,
            })
            .expect("register Drain Flow");
        let dispatcher = WorkerDispatcher::new(
            registry,
            ValueHydrator::new(FlowServiceClient::new(channel.clone()), cache),
            crate::server_protocol::NegotiatedServerProtocolVersion::default(),
        );
        let worker = PullWorker::new(
            &PullWorkerOptions::new().app_worker_id("worker-drain"),
            &dispatcher,
            channel,
        )
        .expect("pull Worker");
        let (stop, shutdown) = watch::channel(false);
        let running = tokio::spawn(async move { worker.run(shutdown).await });
        timeout(Duration::from_secs(5), started.notified())
            .await
            .expect("RPC started");
        stop.send_replace(true);
        assert!(!running.is_finished());
        let (lock, changed) = &*release;
        *lock.lock().expect("drain gate lock") = true;
        changed.notify_all();
        timeout(Duration::from_secs(5), running)
            .await
            .expect("Worker stopped")
            .expect("join Worker")
            .expect("Worker result");
        let final_frame = timeout(Duration::from_secs(5), received.recv())
            .await
            .expect("final frame received")
            .expect("final frame event");
        match final_frame {
            DrainEvent::FinalFrame(frame) => {
                assert_eq!("drain-task", frame.task_id);
                assert!(matches!(frame.output, Some(Output::Rpc(_))));
            }
            DrainEvent::HalfClosed => panic!("stream closed before final frame"),
            DrainEvent::KeepalivePong => panic!("unexpected keepalive Pong"),
            DrainEvent::ReadError(error) => panic!("stream failed before final frame: {error}"),
        }
        let closed = timeout(Duration::from_secs(5), received.recv())
            .await
            .expect("half-close received")
            .expect("half-close event");
        assert!(matches!(closed, DrainEvent::HalfClosed));
        server_stop.send_replace(true);
        timeout(Duration::from_secs(5), server)
            .await
            .expect("fake server stopped")
            .expect("join server")
            .expect("server result");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn stop_drain_delivers_large_result_after_delayed_read() {
        delayed_read_delivers_large_result(false, false, Duration::from_millis(6_100), false).await;
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn busy_keepalive_pong_stops_before_rpc_result() {
        delayed_read_delivers_large_result(false, false, Duration::from_millis(10), true).await;
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn busy_reconnect_delivers_large_result_after_delayed_read() {
        delayed_read_delivers_large_result(true, false, Duration::from_millis(6_100), false).await;
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn stop_rebounds_busy_reconnect_half_close() {
        delayed_read_delivers_large_result(true, true, Duration::from_secs(8), false).await;
    }

    async fn delayed_read_delivers_large_result(
        is_reconnecting: bool,
        should_stop_during_reconnect: bool,
        read_delay: Duration,
        expect_keepalive_pong: bool,
    ) {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind fake task service");
        let address = listener.local_addr().expect("fake service address");
        let (events, mut received) = mpsc::channel(4);
        let (ready, mut ready_receiver) = mpsc::channel(1);
        let (server_stop, mut server_shutdown) = watch::channel(false);
        let server = tokio::spawn(
            Server::builder()
                .add_service(WorkerTaskServiceServer::new(DrainService {
                    events,
                    assigned: Arc::new(AtomicBool::new(false)),
                    cancel_kind: None,
                    read_delay,
                    is_reconnecting,
                    ready: Some(ready),
                    record_keepalive_pongs: expect_keepalive_pong,
                }))
                .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async move {
                    while !*server_shutdown.borrow() && server_shutdown.changed().await.is_ok() {}
                }),
        );
        let channel = Endpoint::from_shared(format!("http://{address}"))
            .expect("endpoint")
            .connect_lazy();
        let channel = CredentialChannel::new(channel, CredentialInterceptor::new(None));
        let (cache, _directory) = test_cache();
        let started = Arc::new(Notify::new());
        let release = Arc::new((Mutex::new(false), Condvar::new()));
        let registry = Registry::new()
            .register(DrainFlow {
                started: Arc::clone(&started),
                release: Arc::clone(&release),
                wait_for_cancel: false,
                result_size: Some(3_000_000),
            })
            .expect("register Drain Flow");
        let dispatcher = WorkerDispatcher::new(
            registry,
            ValueHydrator::new(FlowServiceClient::new(channel.clone()), cache),
            crate::server_protocol::NegotiatedServerProtocolVersion::default(),
        );
        let worker = PullWorker::new(
            &PullWorkerOptions::new()
                .app_worker_id("worker-drain")
                .stop_drain_timeout(Duration::from_secs(7)),
            &dispatcher,
            channel,
        )
        .expect("pull Worker");
        let (stop, shutdown) = watch::channel(false);
        let running = tokio::spawn(async move { worker.run(shutdown).await });
        timeout(Duration::from_secs(5), started.notified())
            .await
            .expect("RPC started");
        timeout(Duration::from_secs(5), ready_receiver.recv())
            .await
            .expect("delayed read ready")
            .expect("readiness event");
        if expect_keepalive_pong {
            let keepalive = timeout(Duration::from_secs(25), received.recv())
                .await
                .expect("busy keepalive received")
                .expect("busy keepalive event");
            assert!(matches!(keepalive, DrainEvent::KeepalivePong));
        }
        if !is_reconnecting {
            stop.send_replace(true);
        }
        let (lock, changed) = &*release;
        *lock.lock().expect("drain gate lock") = true;
        changed.notify_all();
        if should_stop_during_reconnect {
            sleep(Duration::from_secs(3)).await;
            stop.send_replace(true);
        }
        let final_frame = timeout(Duration::from_secs(10), received.recv())
            .await
            .expect("final frame received")
            .expect("final frame event");
        match final_frame {
            DrainEvent::FinalFrame(frame) => {
                assert_eq!("drain-task", frame.task_id);
                assert!(matches!(frame.output, Some(Output::Rpc(_))));
                assert!(frame.encoded_len() > 3_000_000);
            }
            DrainEvent::HalfClosed => panic!("stream closed before final frame"),
            DrainEvent::KeepalivePong => panic!("keepalive followed the final frame"),
            DrainEvent::ReadError(error) => panic!("stream failed before final frame: {error}"),
        }
        let closed = timeout(Duration::from_secs(5), received.recv())
            .await
            .expect("half-close received")
            .expect("half-close event");
        assert!(matches!(closed, DrainEvent::HalfClosed));
        stop.send_replace(true);
        timeout(Duration::from_secs(5), running)
            .await
            .expect("Worker stopped")
            .expect("join Worker")
            .expect("Worker result");
        server_stop.send_replace(true);
        timeout(Duration::from_secs(5), server)
            .await
            .expect("fake server stopped")
            .expect("join server")
            .expect("server result");
    }

    async fn cancellation_frame_has_status(cancel_kind: Code) {
        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind fake task service");
        let address = listener.local_addr().expect("fake service address");
        let (events, mut received) = mpsc::channel(4);
        let (server_stop, mut server_shutdown) = watch::channel(false);
        let server = tokio::spawn(
            Server::builder()
                .add_service(WorkerTaskServiceServer::new(DrainService {
                    events,
                    assigned: Arc::new(AtomicBool::new(false)),
                    cancel_kind: Some(cancel_kind),
                    read_delay: Duration::ZERO,
                    is_reconnecting: false,
                    ready: None,
                    record_keepalive_pongs: false,
                }))
                .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async move {
                    while !*server_shutdown.borrow() && server_shutdown.changed().await.is_ok() {}
                }),
        );
        let channel = Endpoint::from_shared(format!("http://{address}"))
            .expect("endpoint")
            .connect_lazy();
        let channel = CredentialChannel::new(channel, CredentialInterceptor::new(None));
        let (cache, _directory) = test_cache();
        let started = Arc::new(Notify::new());
        let release = Arc::new((Mutex::new(false), Condvar::new()));
        let registry = Registry::new()
            .register(DrainFlow {
                started,
                release,
                wait_for_cancel: true,
                result_size: None,
            })
            .expect("register cancellation Flow");
        let dispatcher = WorkerDispatcher::new(
            registry,
            ValueHydrator::new(FlowServiceClient::new(channel.clone()), cache),
            crate::server_protocol::NegotiatedServerProtocolVersion::default(),
        );
        let worker = PullWorker::new(
            &PullWorkerOptions::new().app_worker_id("worker-cancel"),
            &dispatcher,
            channel,
        )
        .expect("pull Worker");
        let (stop, shutdown) = watch::channel(false);
        let running = tokio::spawn(async move { worker.run(shutdown).await });
        let event = timeout(Duration::from_secs(5), received.recv())
            .await
            .expect("error frame received")
            .expect("error frame event");
        match event {
            DrainEvent::FinalFrame(frame) => {
                let Some(Output::WorkerError(error)) = frame.output else {
                    panic!("expected Worker error frame");
                };
                assert_eq!(cancel_kind as i32, error.grpc_status);
                assert_eq!("Cancelled", error.error_type);
            }
            DrainEvent::HalfClosed => panic!("stream closed before error frame"),
            DrainEvent::KeepalivePong => panic!("unexpected keepalive Pong"),
            DrainEvent::ReadError(error) => panic!("stream failed before error frame: {error}"),
        }
        stop.send_replace(true);
        timeout(Duration::from_secs(5), running)
            .await
            .expect("Worker stopped")
            .expect("join Worker")
            .expect("Worker result");
        server_stop.send_replace(true);
        timeout(Duration::from_secs(5), server)
            .await
            .expect("fake server stopped")
            .expect("join server")
            .expect("server result");
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn cancel_task_reports_cancelled_status() {
        cancellation_frame_has_status(Code::Cancelled).await;
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn deadline_reports_deadline_exceeded_status() {
        cancellation_frame_has_status(Code::DeadlineExceeded).await;
    }
}
