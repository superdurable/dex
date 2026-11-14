// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Registry lock use per dispatch, matching consistency under concurrent dispatches, durable
//! backlog Claims, and disconnects, and searches that meet a partition removal.

use super::*;
use crate::task_backlog::{ClaimReply, ClaimResult, QueuedTaskRef};
use dexzd_clock::SystemWallClock;
use dexzd_keyspace::PartitionId;
use dexzd_keyspace::dispatch_queue_keys::{DispatchQueueName, DispatchQueuePosition};
use dexzd_keyspace::{FlowHash, RunId, TenantId};
use dexzd_protocol::dex::{
    InvokeWorkerRpcRequest, InvokeWorkerRpcResponse, poll_worker_task_request,
};

const TENANT_ID: u64 = 7;
/// Each stream serves its sticky queue and all of these, as a reserved-slot SDK registers.
const FLOW_TYPES: [&str; 6] = ["Audit", "Billing", "Flow", "Report", "Search", "Shipping"];

type WorkerResponses = mpsc::UnboundedReceiver<Result<PollWorkerTaskResponse, Status>>;

/// Registers one idle reserved-slot stream the way a Worker registration does.
fn connect_stream(
    dispatch: &WorkerDispatch,
    app_worker_id: &str,
) -> (Arc<WorkerStream>, WorkerResponses) {
    let (output, responses) = mpsc::unbounded_channel();
    let (close, _) = watch::channel(false);
    let mut queue_keys = vec![MatchingQueueKey::new(TENANT_ID, "", app_worker_id)];
    queue_keys.extend(
        FLOW_TYPES
            .iter()
            .map(|flow_type| MatchingQueueKey::new(TENANT_ID, "", flow_type)),
    );
    let worker = Arc::new(WorkerStream {
        id: Uuid::now_v7(),
        tenant_id: TENANT_ID,
        queue_keys,
        task_queue: String::new(),
        backlog_flow_types: FLOW_TYPES
            .iter()
            .map(|flow_type| (*flow_type).to_owned())
            .collect(),
        app_worker_id: app_worker_id.to_owned(),
        slot: StreamSlot::ReservedStepAndRpc,
        output,
        close,
        state: Mutex::new(WorkerState {
            is_busy: true,
            ..WorkerState::default()
        }),
    });
    {
        let mut registry = dispatch.lock_registry();
        registry
            .worker_stream_ids
            .entry((TENANT_ID, app_worker_id.to_owned()))
            .or_default()
            .insert(worker.id);
        for queue_key in &worker.queue_keys {
            let queue = registry
                .queues
                .entry(queue_key.clone())
                .or_insert_with(QueueState::new);
            queue.stream_ids.insert(worker.id);
            queue.users += 1;
        }
        registry.streams.insert(worker.id, worker.clone());
        registry.queue_matching.add_step_stream(&worker);
    }
    dispatch.make_idle(&worker);
    (worker, responses)
}

fn rpc_request(index: usize, sticky_app_worker_id: Option<String>) -> DispatchRequest {
    let flow_type = FLOW_TYPES[index % FLOW_TYPES.len()];
    DispatchRequest {
        tenant_id: TENANT_ID,
        flow_id: format!("flow-{index}"),
        flow_type: flow_type.to_owned(),
        task_queue: String::new(),
        sticky_app_worker_id,
        payload: DispatchTaskPayload::Rpc(InvokeWorkerRpcRequest {
            flow_type: flow_type.to_owned(),
            rpc_name: "getStatus".to_owned(),
            ..Default::default()
        }),
        deadline: SystemTime::now() + Duration::from_secs(30),
        sticky_wait: Some(Duration::from_secs(5)),
        dispatch_wait: Some(Duration::from_secs(20)),
        partition: Some(PartitionId(
            u16::try_from(index % 8).expect("small partition"),
        )),
    }
}

/// Waits for the next task frame on a Worker stream, skipping pings.
///
/// Returns the task ID, or `None` once the stream ends or sends a non-task frame. Panics on a
/// stream error status, which these tests never expect.
async fn next_task_id(responses: &mut WorkerResponses) -> Option<String> {
    loop {
        let response = match responses.recv().await? {
            Ok(response) => response,
            Err(status) => panic!("the Worker stream failed: {status:?}"),
        };
        match response.frame {
            Some(poll_worker_task_response::Frame::Task(task)) => return Some(task.task_id),
            Some(poll_worker_task_response::Frame::Ping(_)) => {}
            _ => return None,
        }
    }
}

/// Sends the Worker's final RPC output, which also returns the stream to its queues.
async fn complete_rpc(dispatch: &WorkerDispatch, worker: &WorkerStream, task_id: String) {
    let frame = PollWorkerTaskRequest {
        frame: Some(poll_worker_task_request::Frame::TaskFrame(
            PollWorkerTaskFrame {
                task_id,
                output: Some(poll_worker_task_frame::Output::Rpc(
                    InvokeWorkerRpcResponse::default(),
                )),
            },
        )),
    };
    worker_protocol::process_worker_frame(dispatch, worker, frame)
        .await
        .expect("a final RPC output is valid");
}

/// Returns the dispatch's single outcome, failing if any event follows it.
async fn single_outcome(mut handle: DispatchHandle) -> DispatchOutcome {
    let outcome = loop {
        match handle
            .next()
            .await
            .expect("a dispatch ends with an outcome")
        {
            DispatchEvent::Completed(outcome) => break outcome,
            DispatchEvent::Assigned { .. }
            | DispatchEvent::Unassigned
            | DispatchEvent::Output(_) => {}
        }
    };
    assert!(
        handle.next().await.is_none(),
        "a dispatch has exactly one outcome"
    );
    outcome
}

/// How long a test awaits one task frame, Worker output, or dispatch outcome before failing.
const TEST_WAIT_LIMIT: Duration = Duration::from_secs(5);

/// Awaits `future`, failing with `description` if it does not finish within [`TEST_WAIT_LIMIT`].
async fn within_test_wait_limit<T>(description: &str, future: impl Future<Output = T>) -> T {
    tokio::time::timeout(TEST_WAIT_LIMIT, future)
        .await
        .unwrap_or_else(|_| panic!("timed out waiting for {description}"))
}

fn registry_lock_acquisitions(dispatch: &WorkerDispatch) -> u64 {
    dispatch
        .inner
        .registry_lock_acquisitions_for_test
        .load(Ordering::Relaxed)
}

/// An RPC that finds an idle stream locks the registry twice, however many queues it serves.
#[tokio::test]
async fn an_rpc_matched_at_once_takes_the_registry_lock_twice() {
    let dispatch = WorkerDispatch::new(MatchingConfig::default(), Arc::new(SystemWallClock));
    let (worker, mut responses) = connect_stream(&dispatch, "worker-a");
    let dispatch_count = 12;
    let before = registry_lock_acquisitions(&dispatch);
    for index in 0..dispatch_count {
        let sticky_app_worker_id = (index % 2 == 0).then(|| "worker-a".to_owned());
        let handle = dispatch
            .dispatch(rpc_request(index, sticky_app_worker_id))
            .expect("dispatch starts");
        let task_id = within_test_wait_limit("the RPC task", next_task_id(&mut responses))
            .await
            .expect("a task");
        within_test_wait_limit(
            "the RPC output to be processed",
            complete_rpc(&dispatch, &worker, task_id),
        )
        .await;
        assert!(matches!(
            within_test_wait_limit("the RPC outcome", single_outcome(handle)).await,
            DispatchOutcome::WorkerResult { .. }
        ));
    }
    let acquisitions = registry_lock_acquisitions(&dispatch) - before;
    assert_eq!(acquisitions, 2 * dispatch_count as u64);
}

/// A sticky Worker that declined the RPC is skipped at once instead of after the sticky wait.
#[tokio::test]
async fn a_declined_sticky_worker_is_skipped_without_waiting() {
    let dispatch = WorkerDispatch::new(MatchingConfig::default(), Arc::new(SystemWallClock));
    let (_declining, _declining_responses) = connect_stream(&dispatch, "worker-a");
    let (serving, mut serving_responses) = connect_stream(&dispatch, "worker-b");
    let mut sticky = rpc_request(2, Some("worker-a".to_owned()));
    dispatch
        .lock_registry()
        .unsupported_types
        .insert(sticky.task_type(), "worker-a");
    sticky.sticky_wait = Some(Duration::from_secs(30));
    sticky.dispatch_wait = Some(Duration::from_secs(30));
    let handle = dispatch.dispatch(sticky).expect("dispatch starts");
    let task_id =
        tokio::time::timeout(Duration::from_secs(5), next_task_id(&mut serving_responses))
            .await
            .expect("the sticky wait is skipped")
            .expect("a task");
    within_test_wait_limit(
        "the RPC output to be processed",
        complete_rpc(&dispatch, &serving, task_id),
    )
    .await;
    assert!(matches!(
        within_test_wait_limit("the RPC outcome", single_outcome(handle)).await,
        DispatchOutcome::WorkerResult { app_worker_id, .. } if app_worker_id == "worker-b"
    ));
}

fn queued_step(partition: u16, step_type: &str) -> QueuedTaskRef {
    QueuedTaskRef {
        partition: PartitionId(partition),
        queue: DispatchQueueName {
            tenant_id: TenantId(TENANT_ID),
            task_queue: String::new(),
            flow_type: "Flow".to_owned(),
            step_type: step_type.to_owned(),
        },
        position: DispatchQueuePosition {
            priority: 1,
            pending_since_micros: i64::from(partition),
            flow_hash: FlowHash(u64::from(partition)),
            run_id: RunId([1; 16]),
            method: 2,
            attempt: 1,
            step_execution_id: format!("{step_type}-{partition}"),
        },
        run_attempt: 1,
        flow_id: format!("flow-{partition}"),
    }
}

/// The task each stream holds, RPCs and Claims together; a stream may hold one at a time.
#[derive(Default)]
struct OutstandingTasks {
    by_stream: Mutex<HashMap<Uuid, String>>,
    violations: Mutex<Vec<String>>,
}

impl OutstandingTasks {
    fn start(&self, stream_id: Uuid, task: String) {
        let held = self
            .by_stream
            .lock()
            .expect("outstanding task lock")
            .insert(stream_id, task.clone());
        if let Some(held) = held {
            self.record_violation(format!(
                "stream {stream_id} got {task} while holding {held}"
            ));
        }
    }

    fn finish(&self, stream_id: Uuid, task: &str) {
        let held = self
            .by_stream
            .lock()
            .expect("outstanding task lock")
            .remove(&stream_id);
        if held.as_deref() != Some(task) {
            self.record_violation(format!(
                "stream {stream_id} finished {task} but held {held:?}"
            ));
        }
    }

    fn record_violation(&self, violation: String) {
        self.violations
            .lock()
            .expect("violation lock")
            .push(violation);
    }

    fn violations(&self) -> Vec<String> {
        self.violations.lock().expect("violation lock").clone()
    }
}

/// How long a simulated Worker holds each task; frames arriving meanwhile are still read.
const TASK_HOLD_TIME: Duration = Duration::from_micros(300);

/// Answers every RPC task on one stream after [`TASK_HOLD_TIME`], recording what it holds.
async fn serve_stream(
    dispatch: WorkerDispatch,
    worker: Arc<WorkerStream>,
    mut responses: WorkerResponses,
    outstanding: Arc<OutstandingTasks>,
) {
    let mut close = worker.close.subscribe();
    let mut completing_task_id: Option<String> = None;
    let completion = tokio::time::sleep(Duration::ZERO);
    tokio::pin!(completion);
    loop {
        tokio::select! {
            task_id = next_task_id(&mut responses) => {
                let Some(task_id) = task_id else { return };
                outstanding.start(worker.id, task_id.clone());
                completing_task_id = Some(task_id);
                completion.as_mut().reset(tokio::time::Instant::now() + TASK_HOLD_TIME);
            }
            () = &mut completion, if completing_task_id.is_some() => {
                let task_id = completing_task_id.take().expect("a task is completing");
                // The stream returns to idle inside complete_rpc, so release it first.
                outstanding.finish(worker.id, &task_id);
                complete_rpc(&dispatch, &worker, task_id).await;
            }
            _ = close.changed() => return,
        }
    }
}

/// Takes `head_count` Claims, holding each stream for [`TASK_HOLD_TIME`].
///
/// Returns the claimed refs in Claim order.
async fn take_claims(
    dispatch: WorkerDispatch,
    head_count: usize,
    outstanding: Arc<OutstandingTasks>,
) -> Vec<QueuedTaskRef> {
    let mut claimed = Vec::with_capacity(head_count);
    for _ in 0..head_count {
        let claim = dispatch.next_claim().await.expect("a Claim");
        let stream_id = claim.stream.worker().id;
        let claim_task = format!("claim-{}", claim.claim_id);
        outstanding.start(stream_id, claim_task.clone());
        dispatch.reply_to_claim(
            claim.task.partition,
            &claim.task.queue,
            claim.claim_id,
            ClaimReply {
                result: ClaimResult::Stale,
                next: None,
            },
        );
        tokio::time::sleep(TASK_HOLD_TIME).await;
        outstanding.finish(stream_id, &claim_task);
        claimed.push(claim.task);
        drop(claim.stream);
    }
    claimed
}

/// Concurrent RPCs, backlog Claims, and disconnects end with one outcome each and a consistent
/// registry.
#[tokio::test(flavor = "multi_thread", worker_threads = 8)]
async fn concurrent_dispatches_claims_and_disconnects_keep_the_registry_consistent() {
    let dispatch = WorkerDispatch::new(MatchingConfig::default(), Arc::new(SystemWallClock));
    let outstanding = Arc::new(OutstandingTasks::default());
    let app_worker_ids: Vec<String> = (0..6).map(|index| format!("worker-{index}")).collect();
    let mut streams = Vec::new();
    let mut servers = Vec::new();
    for app_worker_id in &app_worker_ids {
        for _ in 0..4 {
            let (worker, responses) = connect_stream(&dispatch, app_worker_id);
            let server = tokio::spawn(serve_stream(
                dispatch.clone(),
                worker.clone(),
                responses,
                outstanding.clone(),
            ));
            servers.push(server);
            streams.push(worker);
        }
    }
    let step_types = ["Bill", "Charge", "Mail", "Pack", "Ship"];
    // The loop below queues each (partition, Step type) head once.
    let head_count = 8 * step_types.len();
    let claims = tokio::spawn(take_claims(
        dispatch.clone(),
        head_count,
        outstanding.clone(),
    ));
    let mut queued_heads = Vec::with_capacity(head_count);
    let mut dispatches = Vec::new();
    for index in 0..600 {
        let sticky_app_worker_id = match index % 4 {
            0 => Some(app_worker_ids[index % app_worker_ids.len()].clone()),
            1 => Some("absent-worker".to_owned()),
            _ => None,
        };
        let request = rpc_request(index, sticky_app_worker_id);
        let handle = dispatch.dispatch(request).expect("dispatch starts");
        let outcome = tokio::spawn(single_outcome(handle));
        dispatches.push(outcome);
        if index % 15 == 0 {
            let partition = u16::try_from(index / 15 % 8).expect("small partition");
            let head = queued_step(partition, step_types[index / 15 % step_types.len()]);
            queued_heads.push(head.clone());
            dispatch.queued(head);
        }
        if index == 200 || index == 400 {
            dispatch.disconnect(streams[index / 100].id);
        }
    }
    let mut outcomes = Vec::with_capacity(dispatches.len());
    for handle in dispatches {
        let outcome = tokio::time::timeout(Duration::from_secs(30), handle)
            .await
            .expect("the dispatch ends")
            .expect("the dispatch task completes");
        outcomes.push(outcome);
    }
    assert_eq!(outstanding.violations(), Vec::<String>::new());
    for outcome in outcomes {
        assert!(
            matches!(outcome, DispatchOutcome::WorkerResult { .. }),
            "unexpected outcome {outcome:?}"
        );
    }
    let claimed = tokio::time::timeout(Duration::from_secs(10), claims)
        .await
        .expect("every queued head is claimed")
        .expect("the Claim task completes");
    assert_eq!(queued_heads.len(), head_count);
    for head in &queued_heads {
        let claim_count = claimed.iter().filter(|task| *task == head).count();
        assert_eq!(claim_count, 1, "{head:?} is claimed exactly once");
    }
    assert_eq!(claimed.len(), head_count, "no Claim is a duplicate");
    // A Worker's final output reaches its dispatch just before its stream returns to idle.
    let stream_count = streams.len() - 2;
    let quiet_deadline = Instant::now() + Duration::from_secs(5);
    while dispatch.lock_registry().idle_positions.len() < stream_count
        && Instant::now() < quiet_deadline
    {
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert_registry_is_consistent(&dispatch, stream_count);
    assert_eq!(outstanding.violations(), Vec::<String>::new());
    for server in servers {
        if server.is_finished() {
            server.await.expect("a stream server never panics");
        } else {
            server.abort();
        }
    }
}

/// Polls `condition` until it holds, failing after five seconds.
async fn wait_until(description: &str, mut condition: impl FnMut() -> bool) {
    let deadline = Instant::now() + Duration::from_secs(5);
    while !condition() {
        assert!(
            Instant::now() < deadline,
            "timed out waiting until {description}"
        );
        tokio::time::sleep(Duration::from_millis(1)).await;
    }
}

/// Returns whether request `request_index`'s RPC waits in its Flow type queue.
fn is_rpc_waiting(dispatch: &WorkerDispatch, request_index: usize) -> bool {
    let queue_key =
        MatchingQueueKey::new(TENANT_ID, "", FLOW_TYPES[request_index % FLOW_TYPES.len()]);
    dispatch
        .lock_registry()
        .queues
        .get(&queue_key)
        .is_some_and(|queue| !queue.rpc_waiters.is_empty())
}

fn partition_search_count(dispatch: &WorkerDispatch) -> usize {
    dispatch.lock_partition_searches().search_count_for_test()
}

/// A search that registers between the two sections of a partition removal still ends as if
/// matching were closing, and leaves no search or queue user behind.
#[tokio::test]
async fn a_search_registered_during_partition_removal_ends_as_server_closing() {
    let dispatch = WorkerDispatch::new(MatchingConfig::default(), Arc::new(SystemWallClock));
    let request_index = 3;
    let partition = rpc_request(request_index, None)
        .partition
        .expect("the request has a partition");
    let undelivered = dispatch.remove_partition_matching_state(partition);
    assert!(undelivered.is_empty());
    // No stream is connected, so the RPC registers its search and waits.
    let handle = dispatch
        .dispatch(rpc_request(request_index, None))
        .expect("dispatch starts");
    wait_until("the RPC waits with its partition search", || {
        partition_search_count(&dispatch) == 1 && is_rpc_waiting(&dispatch, request_index)
    })
    .await;
    dispatch.end_partition_searches(partition);
    let outcome = tokio::time::timeout(Duration::from_secs(5), single_outcome(handle))
        .await
        .expect("the removal ends the search");
    assert!(
        matches!(outcome, DispatchOutcome::ServerClosing),
        "unexpected outcome {outcome:?}"
    );
    assert_eq!(partition_search_count(&dispatch), 0);
    assert!(
        dispatch.lock_registry().queues.is_empty(),
        "the ended search left a queue user"
    );
}

/// A search woken for a stream and cancelled between the two sections of a partition removal
/// returns the stream and leaves matching consistent.
#[tokio::test]
async fn a_search_cancelled_after_its_wakeup_during_partition_removal_keeps_matching_consistent() {
    let dispatch = WorkerDispatch::new(MatchingConfig::default(), Arc::new(SystemWallClock));
    let request_index = 3;
    let partition = rpc_request(request_index, None)
        .partition
        .expect("the request has a partition");
    let handle = dispatch
        .dispatch(rpc_request(request_index, None))
        .expect("dispatch starts");
    wait_until("the RPC waits with its partition search", || {
        partition_search_count(&dispatch) == 1 && is_rpc_waiting(&dispatch, request_index)
    })
    .await;
    // Nothing below yields, so the woken dispatch cannot run before both sections finish.
    let undelivered = dispatch.remove_partition_matching_state(partition);
    assert!(undelivered.is_empty());
    let (worker, mut responses) = connect_stream(&dispatch, "worker-a");
    assert!(
        dispatch
            .lock_registry()
            .promised_streams
            .contains_key(&worker.id),
        "the new stream wakes the waiting RPC and is promised to it"
    );
    handle.cancel();
    dispatch.end_partition_searches(partition);
    let outcome = tokio::time::timeout(Duration::from_secs(5), single_outcome(handle))
        .await
        .expect("the cancelled search ends");
    assert!(
        matches!(
            outcome,
            DispatchOutcome::Cancelled | DispatchOutcome::ServerClosing
        ),
        "unexpected outcome {outcome:?}"
    );
    assert!(
        responses.try_recv().is_err(),
        "the cancelled search sent no task"
    );
    assert_registry_is_consistent(&dispatch, 1);

    let next = dispatch
        .dispatch(rpc_request(5, None))
        .expect("dispatch starts");
    let task_id = within_test_wait_limit(
        "the follow-up RPC task on the returned stream",
        next_task_id(&mut responses),
    )
    .await
    .expect("a task");
    within_test_wait_limit(
        "the follow-up RPC output to be processed",
        complete_rpc(&dispatch, &worker, task_id),
    )
    .await;
    assert!(matches!(
        within_test_wait_limit("the follow-up RPC outcome", single_outcome(next)).await,
        DispatchOutcome::WorkerResult { .. }
    ));
}

/// With no dispatch in flight, every stream is idle in all its queues, and nothing waits or is
/// queued.
fn assert_registry_is_consistent(dispatch: &WorkerDispatch, stream_count: usize) {
    assert_eq!(partition_search_count(dispatch), 0, "a search was left");
    let registry = dispatch.lock_registry();
    assert_eq!(registry.streams.len(), stream_count);
    assert!(registry.promised_streams.is_empty());
    let queue_matching = &registry.queue_matching;
    assert_eq!(queue_matching.pending_claim_count_for_test(), 0);
    assert_eq!(queue_matching.backlog.backlogged_queues().count(), 0);
    assert!(!queue_matching.backlog.has_claimable_heads());
    for (queue_key, queue) in &registry.queues {
        assert!(queue.rpc_waiters.is_empty() && queue.step_waiters.is_empty());
        assert!(queue.woken_waiter_streams.is_empty());
        assert!(!queue.stream_ids.is_empty(), "{queue_key:?} has no stream");
        assert_eq!(
            queue.users,
            queue.stream_ids.len(),
            "{queue_key:?} counts a dispatch"
        );
    }
    assert_eq!(
        registry.idle_positions.len(),
        stream_count,
        "every stream is idle"
    );
    let mut idle_entries = 0;
    for (stream_id, position) in &registry.idle_positions {
        let worker = registry
            .streams
            .get(stream_id)
            .expect("an idle stream is registered");
        for queue_key in &worker.queue_keys {
            assert_eq!(
                registry.queues[queue_key].idle.get(position),
                Some(stream_id)
            );
            idle_entries += 1;
        }
    }
    let total_idle: usize = registry.queues.values().map(|queue| queue.idle.len()).sum();
    assert_eq!(
        total_idle, idle_entries,
        "no queue keeps a stale idle entry"
    );
}
