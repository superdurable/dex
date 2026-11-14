// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Named task queues: a Flow's Steps and RPCs reach only Workers of its task queue.

use std::time::Duration;

use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dex::{
    FlowConfig, FlowStartOptions, FlowStatus, GetFlowStateRequest, InvokeExecuteMethodResponse,
    InvokeRpcRequest, InvokeWaitForMethodResponse, InvokeWorkerRpcResponse, PollWorkerSlotKind,
    PollWorkerTask, PollWorkerTaskRegister, StartFlowRequest, StepDurability, StepOptions,
    SubFlowCondition, SubFlowOptions, TimerCondition, UpdateFlowConfigRequest, WaitForFlowRequest,
    WaitingCondition, WaitingConditionType, WorkerTarget, poll_worker_task, poll_worker_task_frame,
};
use tonic::transport::Channel;

use crate::harness::{
    DevServer, ScriptedWorker, execute_request, execute_result, force_complete, queue_registration,
    try_register_pull_stream, wait_for_result,
};
use crate::string_value;

const FLOW_TYPE: &str = "QueueFlow";
const CHILD_FLOW_TYPE: &str = "QueueChildFlow";
const STEP_TYPE: &str = "QueueStep";
const QUEUE_A: &str = "queue-a";
const QUEUE_B: &str = "queue-b";
/// How long a test waits to show that a Worker receives nothing.
const QUIET_WAIT: Duration = Duration::from_millis(700);

/// A Flow on a named task queue runs only on that queue's Workers, and a default Flow only on
/// default Workers, though every Worker serves the same Flow type.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_named_task_queue_flow_runs_only_on_its_own_workers() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let mut default_worker =
        ScriptedWorker::start_reserved_slot(server.worker_client(), FLOW_TYPE, "default-worker")
            .await;
    let mut queue_worker = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        FLOW_TYPE,
        "queue-a-worker",
        QUEUE_A,
    )
    .await;
    assert_eq!(default_worker.registered().task_queue, "");
    assert_eq!(queue_worker.registered().task_queue, QUEUE_A);

    start_flow(&mut flow_client, "named-flow", FLOW_TYPE, QUEUE_A, true).await;
    let task = queue_worker.next_task().await;
    assert_eq!(execute_request(&task).flow_type, FLOW_TYPE);
    let state = flow_state(&mut flow_client, "named-flow").await;
    assert_eq!(
        state.flow_config.and_then(|config| config.worker_target),
        Some(WorkerTarget {
            is_pull_mode: true,
            task_queue: QUEUE_A.to_owned(),
        })
    );
    let routing = state.active_step_executions[0]
        .execute_pull_routing
        .clone()
        .expect("a started Execute records its routing");
    assert_eq!(routing.task_queue, QUEUE_A);
    assert_eq!(routing.served_by_app_worker_id, "queue-a-worker");
    assert!(
        default_worker.next_task_within(QUIET_WAIT).await.is_none(),
        "a default Worker never runs a named-queue Flow"
    );
    queue_worker.reply(task.task_id, complete_output());
    assert_completed(&mut flow_client, "named-flow").await;

    start_flow(&mut flow_client, "default-flow", FLOW_TYPE, "", true).await;
    let task = default_worker.next_task().await;
    assert!(
        queue_worker.next_task_within(QUIET_WAIT).await.is_none(),
        "a named-queue Worker never runs a default Flow"
    );
    default_worker.reply(task.task_id, complete_output());
    assert_completed(&mut flow_client, "default-flow").await;

    default_worker.stop().await;
    queue_worker.stop().await;
    server.shutdown().await;
}

/// StartFlow rejects malformed and reserved task queue names, and a task queue without pull
/// mode; `dex.default` selects the default task queue.
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn start_flow_rejects_invalid_task_queues() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let invalid_targets = [
        ("space", pull_target("has space")),
        ("reserved", pull_target("dex.reserved")),
        ("long", pull_target(&"q".repeat(64))),
        (
            "not-pull",
            WorkerTarget {
                is_pull_mode: false,
                task_queue: QUEUE_A.to_owned(),
            },
        ),
    ];
    for (case, target) in invalid_targets {
        let error = flow_client
            .start_flow(start_request(
                &format!("invalid-{case}"),
                FLOW_TYPE,
                target,
                true,
            ))
            .await
            .expect_err("an invalid task queue is rejected");
        assert_eq!(
            error.code(),
            tonic::Code::InvalidArgument,
            "{case}: {error:?}"
        );
        assert!(error.message().contains("task_queue"), "{case}: {error:?}");
    }
    flow_client
        .start_flow(start_request(
            "explicit-default",
            FLOW_TYPE,
            pull_target("dex.default"),
            true,
        ))
        .await
        .expect("dex.default selects the default task queue");
    let state = flow_state(&mut flow_client, "explicit-default").await;
    assert_eq!(
        state.flow_config.and_then(|config| config.worker_target),
        Some(pull_target(""))
    );
    server.shutdown().await;
}

/// Registration rejects reserved and malformed names, and a second task queue for one app
/// Worker.
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn worker_registration_rejects_invalid_task_queues_and_a_second_queue_per_worker() {
    let server = DevServer::start().await;
    for task_queue in ["dex.default", "has space", &"q".repeat(64)] {
        let rejected = try_register_pull_stream(
            server.worker_client(),
            reserved_registration(&[FLOW_TYPE], "bad-worker", task_queue),
        )
        .await;
        let rejected = rejection(rejected, &format!("task queue {task_queue:?}"));
        assert_eq!(rejected.code(), tonic::Code::InvalidArgument);
    }
    let first = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        FLOW_TYPE,
        "one-queue-worker",
        QUEUE_A,
    )
    .await;
    let rejected = try_register_pull_stream(
        server.worker_client(),
        reserved_registration(&[FLOW_TYPE], "one-queue-worker", QUEUE_B),
    )
    .await;
    let rejected = rejection(rejected, "a second task queue of one app Worker");
    assert_eq!(rejected.code(), tonic::Code::InvalidArgument);
    assert!(
        rejected.message().contains("one task queue"),
        "{rejected:?}"
    );
    let second_slot = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        FLOW_TYPE,
        "one-queue-worker",
        QUEUE_A,
    )
    .await;
    first.stop().await;
    second_slot.stop().await;
    server.shutdown().await;
}

/// The task queue is fixed when the Flow starts; UpdateFlowConfig may repeat it or leave it empty.
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn update_flow_config_keeps_the_task_queue_fixed() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    start_flow(&mut flow_client, "fixed-named", FLOW_TYPE, QUEUE_A, true).await;
    start_flow(&mut flow_client, "fixed-default", FLOW_TYPE, "", true).await;
    for (flow_id, task_queue) in [
        ("fixed-named", QUEUE_B),
        ("fixed-named", "dex.default"),
        ("fixed-default", QUEUE_A),
    ] {
        let error = update_worker_target(&mut flow_client, flow_id, task_queue)
            .await
            .expect_err("a different task queue is rejected");
        assert_eq!(error.code(), tonic::Code::InvalidArgument, "{error:?}");
        assert!(
            error.message().contains("fixed when the Flow starts"),
            "{error:?}"
        );
    }
    for (flow_id, task_queue) in [
        ("fixed-named", ""),
        ("fixed-named", QUEUE_A),
        ("fixed-default", ""),
        ("fixed-default", "dex.default"),
    ] {
        update_worker_target(&mut flow_client, flow_id, task_queue)
            .await
            .expect("the same task queue is accepted");
    }
    flow_client
        .update_flow_config(UpdateFlowConfigRequest {
            flow_id: "fixed-named".to_owned(),
            flow_config: Some(FlowConfig {
                step_durability: Some(StepDurability::Async.into()),
                ..FlowConfig::default()
            }),
            ..UpdateFlowConfigRequest::default()
        })
        .await
        .expect("an update without a Worker target keeps the task queue");
    let named_config = flow_state(&mut flow_client, "fixed-named")
        .await
        .flow_config
        .expect("a running Flow has a config");
    assert_eq!(named_config.worker_target, Some(pull_target(QUEUE_A)));
    assert_eq!(
        named_config.step_durability,
        Some(StepDurability::Async.into())
    );
    let default_config = flow_state(&mut flow_client, "fixed-default")
        .await
        .flow_config
        .expect("a running Flow has a config");
    assert_eq!(default_config.worker_target, Some(pull_target("")));
    server.shutdown().await;
}

/// The Flow's last Worker now serves another task queue under the same app Worker ID, so the
/// sticky search ends at once and another Worker of the Flow's task queue runs the Step.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_sticky_worker_on_another_task_queue_is_skipped_at_once() {
    let server = DevServer::start().await;
    let mut flow_client = server.flow_client();
    let mut sticky = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        FLOW_TYPE,
        "sticky-worker",
        QUEUE_A,
    )
    .await;
    start_flow(&mut flow_client, "sticky-flow", FLOW_TYPE, QUEUE_A, false).await;
    let wait_for = sticky.next_task().await;
    let step_execution_id = context_of(&wait_for).step_execution_id.clone();
    sticky.reply(
        wait_for.task_id,
        wait_for_result(InvokeWaitForMethodResponse {
            waiting_condition: Some(WaitingCondition {
                waiting_condition_type: WaitingConditionType::AllCompleted.into(),
                timer_conditions: vec![TimerCondition {
                    condition_id: "hour".to_owned(),
                    duration_seconds: 3600,
                    ..TimerCondition::default()
                }],
                ..WaitingCondition::default()
            }),
            ..InvokeWaitForMethodResponse::default()
        }),
    );
    wait_until_waiting(&mut flow_client, "sticky-flow").await;
    sticky.stop().await;
    let mut moved_sticky = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        FLOW_TYPE,
        "sticky-worker",
        QUEUE_B,
    )
    .await;
    let mut fallback = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        FLOW_TYPE,
        "fallback-worker",
        QUEUE_A,
    )
    .await;
    flow_client
        .skip_timer(dexzd_protocol::dex::SkipTimerRequest {
            flow_id: "sticky-flow".to_owned(),
            step_execution_id,
            timer_condition_id: "hour".to_owned(),
            ..dexzd_protocol::dex::SkipTimerRequest::default()
        })
        .await
        .expect("SkipTimer succeeds");
    // Well inside the 5-second sticky wait, so the sticky search did not wait it out.
    let execute = fallback
        .next_task_within(Duration::from_secs(3))
        .await
        .expect("a Worker of the Flow's task queue runs the Execute at once");
    assert_eq!(execute_request(&execute).flow_type, FLOW_TYPE);
    assert!(moved_sticky.next_task_within(QUIET_WAIT).await.is_none());
    fallback.reply(execute.task_id, complete_output());
    assert_completed(&mut flow_client, "sticky-flow").await;
    moved_sticky.stop().await;
    fallback.stop().await;
    server.shutdown().await;
}

/// A one-second RPC dispatch wait; Steps wait in memory, so only RPCs reach Workers here.
const RPC_MATCHING_SETTINGS: &str =
    "matching:\n  dispatchWait: 1s\n  stickyWait: 500ms\n  syncMatchWait: 1h\n";

/// RPCs of a named-queue Flow go only to that queue's Workers; without one they fail with
/// UNAVAILABLE after the dispatch wait.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn rpcs_of_a_named_task_queue_flow_go_only_to_its_workers() {
    let server = DevServer::start_with_config_overrides(Some(RPC_MATCHING_SETTINGS)).await;
    let mut flow_client = server.flow_client();
    let mut default_worker =
        ScriptedWorker::start_reserved_slot(server.worker_client(), FLOW_TYPE, "default-worker")
            .await;
    let mut queue_worker = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        FLOW_TYPE,
        "queue-a-worker",
        QUEUE_A,
    )
    .await;
    start_flow(&mut flow_client, "rpc-flow", FLOW_TYPE, QUEUE_A, false).await;
    let wait_for = queue_worker.next_task().await;
    queue_worker.reply(
        wait_for.task_id,
        wait_for_result(InvokeWaitForMethodResponse {
            waiting_condition: Some(WaitingCondition {
                waiting_condition_type: WaitingConditionType::AllCompleted.into(),
                timer_conditions: vec![TimerCondition {
                    condition_id: "hour".to_owned(),
                    duration_seconds: 3600,
                    ..TimerCondition::default()
                }],
                ..WaitingCondition::default()
            }),
            ..InvokeWaitForMethodResponse::default()
        }),
    );
    wait_until_waiting(&mut flow_client, "rpc-flow").await;
    let mut rpc_client = flow_client.clone();
    let rpc = tokio::spawn(async move { rpc_client.invoke_rpc(rpc_request("rpc-flow")).await });
    let rpc_task = queue_worker.next_task().await;
    assert!(matches!(
        rpc_task.request,
        Some(poll_worker_task::Request::Rpc(_))
    ));
    assert!(default_worker.next_task_within(QUIET_WAIT).await.is_none());
    queue_worker.reply(
        rpc_task.task_id,
        poll_worker_task_frame::Output::Rpc(InvokeWorkerRpcResponse {
            output: Some(string_value("from queue-a")),
            ..InvokeWorkerRpcResponse::default()
        }),
    );
    let response = rpc
        .await
        .expect("the RPC task finishes")
        .expect("the RPC succeeds")
        .into_inner();
    assert_eq!(response.output, Some(string_value("from queue-a")));

    start_flow(&mut flow_client, "orphan-flow", FLOW_TYPE, QUEUE_B, true).await;
    let error = flow_client
        .invoke_rpc(rpc_request("orphan-flow"))
        .await
        .expect_err("no Worker serves queue-b");
    assert_eq!(error.code(), tonic::Code::Unavailable, "{error:?}");
    assert!(
        default_worker.next_task_within(QUIET_WAIT).await.is_none()
            && queue_worker.next_task_within(QUIET_WAIT).await.is_none(),
        "no other task queue's Worker runs the orphan Flow"
    );
    default_worker.stop().await;
    queue_worker.stop().await;
    server.shutdown().await;
}

/// A SubFlow inherits its parent's task queue unless its override names one; `dex.default`
/// selects the default task queue.
#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn sub_flows_inherit_or_override_the_parent_task_queue() {
    // Children wait in memory, so the in-memory match decides where each one runs.
    let server =
        DevServer::start_with_config_overrides(Some("matching:\n  syncMatchWait: 30s\n")).await;
    let mut flow_client = server.flow_client();
    let mut parent_worker = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        FLOW_TYPE,
        "parent-worker",
        QUEUE_A,
    )
    .await;
    let mut queue_a_children = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        CHILD_FLOW_TYPE,
        "queue-a-children",
        QUEUE_A,
    )
    .await;
    let mut queue_a_children_second = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        CHILD_FLOW_TYPE,
        "queue-a-children",
        QUEUE_A,
    )
    .await;
    let mut default_children = ScriptedWorker::start_reserved_slot(
        server.worker_client(),
        CHILD_FLOW_TYPE,
        "default-children",
    )
    .await;
    let mut queue_b_children = ScriptedWorker::start_reserved_slot_on_task_queue(
        server.worker_client(),
        CHILD_FLOW_TYPE,
        "queue-b-children",
        QUEUE_B,
    )
    .await;
    start_flow(&mut flow_client, "parent-flow", FLOW_TYPE, QUEUE_A, false).await;
    let wait_for = parent_worker.next_task().await;
    let overrides = [None, Some(""), Some("dex.default"), Some(QUEUE_B)];
    let conditions = overrides
        .iter()
        .enumerate()
        .map(|(index, task_queue)| child_condition(index, *task_queue))
        .collect();
    parent_worker.reply(
        wait_for.task_id,
        wait_for_result(InvokeWaitForMethodResponse {
            waiting_condition: Some(WaitingCondition {
                waiting_condition_type: WaitingConditionType::AllCompleted.into(),
                sub_flow_conditions: conditions,
                ..WaitingCondition::default()
            }),
            ..InvokeWaitForMethodResponse::default()
        }),
    );
    let mut queue_a_child_indexes = vec![
        child_index(&queue_a_children.next_task().await),
        child_index(&queue_a_children_second.next_task().await),
    ];
    queue_a_child_indexes.sort_unstable();
    assert_eq!(queue_a_child_indexes, [0, 1]);
    assert_eq!(child_index(&default_children.next_task().await), 2);
    assert_eq!(child_index(&queue_b_children.next_task().await), 3);
    assert!(parent_worker.next_task_within(QUIET_WAIT).await.is_none());
    parent_worker.stop().await;
    queue_a_children.stop().await;
    queue_a_children_second.stop().await;
    default_children.stop().await;
    queue_b_children.stop().await;
    server.shutdown().await;
}

/// Returns the status that rejected a registration of `what`.
fn rejection<Registered>(result: Result<Registered, tonic::Status>, what: &str) -> tonic::Status {
    match result {
        Ok(_) => panic!("{what} is rejected"),
        Err(status) => status,
    }
}

fn pull_target(task_queue: &str) -> WorkerTarget {
    WorkerTarget {
        is_pull_mode: true,
        task_queue: task_queue.to_owned(),
    }
}

fn start_request(
    flow_id: &str,
    flow_type: &str,
    worker_target: WorkerTarget,
    should_skip_wait_for: bool,
) -> StartFlowRequest {
    StartFlowRequest {
        flow_id: flow_id.to_owned(),
        flow_type: flow_type.to_owned(),
        start_step_type: STEP_TYPE.to_owned(),
        step_input: Some(string_value("input")),
        step_options: Some(StepOptions {
            skip_wait_for: should_skip_wait_for,
            ..StepOptions::default()
        }),
        flow_start_options: Some(FlowStartOptions {
            flow_config_override: Some(FlowConfig {
                worker_target: Some(worker_target),
                ..FlowConfig::default()
            }),
            ..FlowStartOptions::default()
        }),
        request_id: format!("{flow_id}-start"),
        ..StartFlowRequest::default()
    }
}

/// Starts a pull Flow on `task_queue`; empty selects the default task queue.
pub(crate) async fn start_flow(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
    flow_type: &str,
    task_queue: &str,
    should_skip_wait_for: bool,
) {
    flow_client
        .start_flow(start_request(
            flow_id,
            flow_type,
            pull_target(task_queue),
            should_skip_wait_for,
        ))
        .await
        .expect("StartFlow succeeds");
}

fn reserved_registration(
    flow_types: &[&str],
    app_worker_id: &str,
    task_queue: &str,
) -> PollWorkerTaskRegister {
    PollWorkerTaskRegister {
        queue: String::new(),
        flow_types: flow_types
            .iter()
            .map(|flow_type| (*flow_type).to_owned())
            .collect(),
        supports_reserved_slot_dispatch: true,
        slot_kind: PollWorkerSlotKind::StepAndRpc.into(),
        task_queue: task_queue.to_owned(),
        ..queue_registration(flow_types[0], app_worker_id)
    }
}

async fn update_worker_target(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
    task_queue: &str,
) -> Result<tonic::Response<()>, tonic::Status> {
    flow_client
        .update_flow_config(UpdateFlowConfigRequest {
            flow_id: flow_id.to_owned(),
            flow_config: Some(FlowConfig {
                worker_target: Some(pull_target(task_queue)),
                ..FlowConfig::default()
            }),
            ..UpdateFlowConfigRequest::default()
        })
        .await
}

async fn flow_state(
    flow_client: &mut FlowServiceClient<Channel>,
    flow_id: &str,
) -> dexzd_protocol::dex::GetFlowStateResponse {
    flow_client
        .get_flow_state(GetFlowStateRequest {
            flow_id: flow_id.to_owned(),
            ..GetFlowStateRequest::default()
        })
        .await
        .expect("GetFlowState succeeds")
        .into_inner()
}

/// Waits until the Flow's Step waits on its conditions, so its WaitFor result committed.
async fn wait_until_waiting(flow_client: &mut FlowServiceClient<Channel>, flow_id: &str) {
    tokio::time::timeout(Duration::from_secs(30), async {
        loop {
            let state = flow_state(flow_client, flow_id).await;
            let is_waiting = state
                .active_step_executions
                .iter()
                .any(|step| step.phase == dexzd_protocol::dex::ActiveStepPhase::Waiting as i32);
            if is_waiting {
                return;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .expect("the Step waits on its conditions within 30 seconds");
}

pub(crate) async fn assert_completed(flow_client: &mut FlowServiceClient<Channel>, flow_id: &str) {
    let result = flow_client
        .wait_for_flow(WaitForFlowRequest {
            flow_id: flow_id.to_owned(),
            wait_time_seconds: 30,
            ..WaitForFlowRequest::default()
        })
        .await
        .expect("WaitForFlow succeeds")
        .into_inner();
    assert_eq!(
        result.flow_status,
        FlowStatus::Completed as i32,
        "{flow_id}"
    );
}

pub(crate) fn complete_output() -> poll_worker_task_frame::Output {
    execute_result(InvokeExecuteMethodResponse {
        step_decision: Some(force_complete(string_value("done"))),
        ..InvokeExecuteMethodResponse::default()
    })
}

fn rpc_request(flow_id: &str) -> InvokeRpcRequest {
    InvokeRpcRequest {
        flow_id: flow_id.to_owned(),
        rpc_name: "getQueueStatus".to_owned(),
        timeout_seconds: 30,
        request_id: format!("{flow_id}-rpc"),
        ..InvokeRpcRequest::default()
    }
}

fn context_of(task: &PollWorkerTask) -> &dexzd_protocol::dex::Context {
    let context = match &task.request {
        Some(poll_worker_task::Request::WaitFor(request)) => request.context.as_ref(),
        Some(poll_worker_task::Request::Execute(request)) => request.context.as_ref(),
        other => panic!("expected a Step task, got {other:?}"),
    };
    context.expect("a Step task has a context")
}

/// A SubFlow condition at `index`; `task_queue` is its override's task queue, or `None` for no
/// override.
fn child_condition(index: usize, task_queue: Option<&str>) -> SubFlowCondition {
    SubFlowCondition {
        condition_id: format!("child-{index}"),
        sub_flow_type: CHILD_FLOW_TYPE.to_owned(),
        start_step_type: STEP_TYPE.to_owned(),
        step_options: Some(StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        }),
        options: task_queue.map(|task_queue| SubFlowOptions {
            flow_config_override: Some(FlowConfig {
                worker_target: Some(pull_target(task_queue)),
                ..FlowConfig::default()
            }),
            ..SubFlowOptions::default()
        }),
        sub_flow_index: i32::try_from(index).expect("a small index"),
        ..SubFlowCondition::default()
    }
}

/// Returns the SubFlow index that a child's Flow ID ends with.
fn child_index(task: &PollWorkerTask) -> usize {
    context_of(task)
        .flow_id
        .rsplit('-')
        .next()
        .and_then(|index| index.parse().ok())
        .expect("a SubFlow ID ends with its index")
}
