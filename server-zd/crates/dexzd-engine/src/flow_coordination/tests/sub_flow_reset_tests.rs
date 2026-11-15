// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! ResetFlow and CloneForkFlow compute again when a round's two views make a child look gone.

use std::collections::VecDeque;
use std::sync::atomic::Ordering;
use std::sync::{Arc, Mutex, Weak};

use dexzd_apply::{ApplyContext, FlowStateReader, apply_flow_command};
use dexzd_keyspace::row_keys::{history_record_key, prefix_range, run_singleton_key};
use dexzd_keyspace::{ColumnFamily, RunId, RunTable, run_table_prefix};
use dexzd_protocol::dex::{
    ChannelCondition, CloneForkFlowRequest, CloneForkFlowResponse, FlowCloneForkPointType,
    InvokeWaitForMethodResponse, ResetFlowRequest, StepOptions, StopFlowRequest, StopType,
    SubFlowCondition, WaitingCondition, WaitingConditionType,
};
use dexzd_protocol::dexzd::v1::{
    CrossFlowEndpoint, FlowCommand, FlowStopCommand, HistoryRecord, InboxMessageCommand,
    OutboxAckCommand, OutboxRecord, RunRecord, StepAttemptStartedCommand, StepExecutionRecord,
    StepInvocationKey, StepMethod, StepMethodCompletedCommand, command_reply, flow_command,
    inbox_message_command, outbox_record, step_method_completed_command,
};
use prost::Message;

use crate::flow_coordination::FlowCoordinationConfig;
use crate::flow_coordination::grpc_code;
use crate::flow_coordination::tests::fixtures::{TENANT, flow, start_flow};
use crate::flow_coordination::tests::test_support::{
    FakeCommitter, FlowCoordinationHarness, MemoryStore,
};

const PARENT_FLOW: &str = "parent";
const CLONE_FLOW: &str = "clone";

/// Applies commands straight to the store, between the times the fake pipeline stamps.
struct DirectCommands {
    store: MemoryStore,
    next_micros: i64,
    next_command_id: u8,
}

impl DirectCommands {
    fn apply(
        &mut self,
        context: ApplyContext,
        flow_id: &str,
        run_id: &[u8],
        body: flow_command::Command,
    ) {
        self.next_micros += 1000;
        self.next_command_id += 1;
        let command = FlowCommand {
            flow_id: flow_id.to_owned(),
            run_id: run_id.to_vec(),
            epoch: 1,
            behavior_version: 1,
            proposed_at_micros: self.next_micros,
            command_id: [self.next_command_id; 16].to_vec(),
            tenant_id: TENANT.0,
            command: Some(body),
            ..FlowCommand::default()
        };
        let outcome = apply_flow_command(&context, &self.store.view(), &command)
            .expect("the command applies");
        assert!(
            !matches!(outcome.reply.reply, Some(command_reply::Reply::Error(_))),
            "{:?}",
            outcome.reply
        );
        self.store.apply_writes(&outcome.writes);
    }

    /// Delivers `child`'s start to it, and the parent acknowledges the start.
    fn deliver_child_start(
        &mut self,
        parent: ApplyContext,
        parent_run: RunId,
        child: &WaitedChild,
    ) {
        self.apply(
            child.context,
            &child.flow_id,
            &[],
            child_start_delivery(&child.start, child.run_id),
        );
        self.apply(
            parent,
            PARENT_FLOW,
            parent_run.as_bytes(),
            flow_command::Command::OutboxAck(OutboxAckCommand {
                message_ids: vec![child.start.message_id.clone()],
            }),
        );
    }
}

fn outbox_records(store: &MemoryStore, context: ApplyContext, run_id: RunId) -> Vec<OutboxRecord> {
    let prefix = run_table_prefix(context.flow_hash, run_id, RunTable::Outbox);
    let (start, end) = prefix_range(&prefix);
    store
        .view()
        .scan(ColumnFamily::Flow, &start, &end, usize::MAX)
        .expect("the outbox reads")
        .into_iter()
        .map(|(_, bytes)| OutboxRecord::decode(bytes.as_slice()).expect("outbox decodes"))
        .collect()
}

fn run_record(store: &MemoryStore, context: ApplyContext, run_id: RunId) -> RunRecord {
    let bytes = store
        .view()
        .get(
            ColumnFamily::Flow,
            &run_singleton_key(context.flow_hash, run_id, RunTable::RunRecord),
        )
        .expect("the run record reads")
        .expect("the run exists");
    RunRecord::decode(bytes.as_slice()).expect("the run record decodes")
}

/// One child that the parent's wait starts.
#[derive(Clone)]
struct WaitedChild {
    flow_id: String,
    context: ApplyContext,
    /// The run that delivering the start creates.
    run_id: RunId,
    /// The child start in the parent's outbox.
    start: OutboxRecord,
}

/// A parent whose InitStep-1 waits on children and a signal, before the child starts are
/// delivered.
struct WaitingParent {
    harness: FlowCoordinationHarness,
    parent_run: RunId,
    parent: ApplyContext,
    /// The waited children, by SubFlow index.
    children: Vec<WaitedChild>,
    /// Time of the wait's history record, the time travel target.
    wait_time: i64,
    direct: DirectCommands,
}

impl WaitingParent {
    async fn new(child_count: u8) -> Self {
        let harness = FlowCoordinationHarness::new();
        harness
            .applier
            .uses_real_apply
            .store(true, Ordering::SeqCst);
        let parent_run = start_flow(&harness, PARENT_FLOW).await.unwrap();
        let parent = harness.registry.flow_context(&flow(PARENT_FLOW));
        let mut direct = DirectCommands {
            store: harness.store.clone(),
            next_micros: 1_100_000,
            next_command_id: 0xA0,
        };
        let wait_time = register_wait(&mut direct, &harness.store, parent, parent_run, child_count);
        let starts = outbox_records(&harness.store, parent, parent_run);
        let children = (0..child_count)
            .map(|index| {
                let flow_id = format!("SubFlow:parent-InitStep-1-{index}");
                let start = starts
                    .iter()
                    .find(|start| {
                        matches!(
                            &start.payload,
                            Some(outbox_record::Payload::ChildStart(child_start))
                                if child_start.child_flow_id == flow_id
                        )
                    })
                    .expect("the wait starts each child")
                    .clone();
                WaitedChild {
                    context: harness.registry.flow_context(&flow(&flow_id)),
                    flow_id,
                    run_id: RunId([3 + index; 16]),
                    start,
                }
            })
            .collect();
        Self {
            harness,
            parent_run,
            parent,
            children,
            wait_time,
            direct,
        }
    }

    async fn reset(&self) -> Result<u32, crate::flow_coordination::FlowCoordinationError> {
        self.harness
            .drive(self.harness.registry.reset_flow(
                TENANT,
                ResetFlowRequest {
                    flow_id: PARENT_FLOW.to_owned(),
                    reset_type: dexzd_protocol::dex::FlowResetType::HistoryEventTime as i32,
                    history_event_time: (i128::from(self.wait_time) * 1000).to_string(),
                    run_attempt: 1,
                    ..ResetFlowRequest::default()
                },
            ))
            .await
            .map(|response| u32::try_from(response.run_attempt).expect("a positive attempt"))
    }

    /// Clone-forks the parent at the wait into [`CLONE_FLOW`] with the default SubFlow policy.
    async fn clone_fork(
        &self,
    ) -> Result<CloneForkFlowResponse, crate::flow_coordination::FlowCoordinationError> {
        self.harness
            .drive(self.harness.registry.clone_fork_flow(
                TENANT,
                CloneForkFlowRequest {
                    source_flow_id: PARENT_FLOW.to_owned(),
                    flow_id: CLONE_FLOW.to_owned(),
                    request_id: "clone-parent".to_owned(),
                    point_type: FlowCloneForkPointType::HistoryEventTime as i32,
                    history_event_time: (i128::from(self.wait_time) * 1000).to_string(),
                    run_attempt: 1,
                    ..CloneForkFlowRequest::default()
                },
            ))
            .await
    }

    /// Delivers the start of child `index`, and the parent acknowledges it.
    fn deliver_child_start(&mut self, index: usize) {
        let child = self.children[index].clone();
        self.direct
            .deliver_child_start(self.parent, self.parent_run, &child);
    }

    fn restored_wait_attempts(&self) -> Vec<u32> {
        let step_bytes = self
            .harness
            .store
            .view()
            .get(
                ColumnFamily::Flow,
                &dexzd_keyspace::row_keys::run_named_key(
                    self.parent.flow_hash,
                    self.parent_run,
                    RunTable::StepExecution,
                    "InitStep-1",
                ),
            )
            .unwrap()
            .expect("the restored Step exists");
        StepExecutionRecord::decode(step_bytes.as_slice())
            .unwrap()
            .wait
            .expect("the signal keeps the restored Step waiting")
            .sub_flow_registration_attempts
    }
}

/// The inbox command that delivers the parent's child start, creating run `run_id`.
fn child_start_delivery(start: &OutboxRecord, run_id: RunId) -> flow_command::Command {
    let Some(outbox_record::Payload::ChildStart(child_start)) = start.payload.clone() else {
        panic!("expected a child start");
    };
    flow_command::Command::InboxMessage(InboxMessageCommand {
        message_id: start.message_id.clone(),
        source: child_start
            .parent
            .as_ref()
            .and_then(|reference| reference.parent.clone()),
        payload: Some(inbox_message_command::Payload::ChildStart(child_start)),
        new_run_id: run_id.as_bytes().to_vec(),
        settings: Some(FlowCoordinationConfig::default().run_settings),
    })
}

/// Marks the child run purging, as its purge begins.
fn begin_child_purge(store: &MemoryStore, child: &WaitedChild) {
    set_child_purging(store, child, true);
}

/// Sets whether the child run is purging; tests clear it to make the child reappear.
fn set_child_purging(store: &MemoryStore, child: &WaitedChild, is_purging: bool) {
    let mut purging = run_record(store, child.context, child.run_id);
    purging.is_purging = is_purging;
    store.put(
        ColumnFamily::Flow,
        run_singleton_key(child.context.flow_hash, child.run_id, RunTable::RunRecord),
        purging.encode_to_vec(),
    );
}

type ViewAction = Box<dyn FnOnce() + Send>;

/// Runs `actions[n]` right after the `n + 1`th next committed view captured its snapshot.
fn run_after_views(committer: &Arc<FakeCommitter>, actions: Vec<ViewAction>) {
    arm_next_view_action(Arc::downgrade(committer), actions.into());
}

fn arm_next_view_action(committer: Weak<FakeCommitter>, mut actions: VecDeque<ViewAction>) {
    let (Some(action), Some(armed)) = (actions.pop_front(), committer.upgrade()) else {
        return;
    };
    *armed.view_hook.lock().unwrap() = Some(Box::new(move || {
        action();
        arm_next_view_action(committer, actions);
    }));
}

fn no_view_action() -> ViewAction {
    Box::new(|| {})
}

/// Codex's interleaving: the child start reaches the child and the parent acknowledges it right
/// after the first round's child view. That round finds the child gone; a new round attaches.
#[tokio::test(start_paused = true)]
async fn a_child_started_between_the_views_of_a_round_stays_attached() {
    let scenario = WaitingParent::new(1).await;
    let mut hook_commands = DirectCommands {
        store: scenario.harness.store.clone(),
        next_micros: 1_800_000,
        next_command_id: 0xD0,
    };
    let (parent, parent_run) = (scenario.parent, scenario.parent_run);
    let child = scenario.children[0].clone();
    *scenario.harness.committer.view_hook.lock().unwrap() = Some(Box::new(move || {
        hook_commands.deliver_child_start(parent, parent_run, &child);
    }));
    let run_attempt = scenario
        .reset()
        .await
        .expect("the default policy attaches the started child");
    assert_eq!(run_attempt, 2);
    assert!(scenario.restored_wait_attempts().is_empty());
}

/// Three children start between the views of three rounds in a row, one per round. Each looks
/// gone in one round only, so the fourth round attaches them all.
#[tokio::test(start_paused = true)]
async fn children_started_between_the_views_of_successive_rounds_stay_attached() {
    let scenario = WaitingParent::new(3).await;
    let hook_commands = Arc::new(Mutex::new(DirectCommands {
        store: scenario.harness.store.clone(),
        next_micros: 1_800_000,
        next_command_id: 0xD0,
    }));
    let (parent, parent_run) = (scenario.parent, scenario.parent_run);
    // Round k reads children on view 2k - 1 and the parent on view 2k.
    let mut actions = Vec::new();
    for child in scenario.children.clone() {
        let hook_commands = Arc::clone(&hook_commands);
        actions.push(Box::new(move || {
            hook_commands
                .lock()
                .unwrap()
                .deliver_child_start(parent, parent_run, &child);
        }) as ViewAction);
        actions.push(no_view_action());
    }
    run_after_views(&scenario.harness.committer, actions);
    let run_attempt = scenario
        .reset()
        .await
        .expect("children gone in different rounds are no reason to reject");
    assert_eq!(run_attempt, 2);
    assert!(scenario.restored_wait_attempts().is_empty());
}

/// A child that stays gone in every round is rejected.
#[tokio::test(start_paused = true)]
async fn a_child_gone_in_every_round_is_rejected() {
    let mut scenario = WaitingParent::new(1).await;
    scenario.deliver_child_start(0);
    begin_child_purge(&scenario.harness.store, &scenario.children[0]);
    let rejected = scenario.reset().await.expect_err("a gone child rejects");
    assert_eq!(rejected.grpc_code(), grpc_code::FAILED_PRECONDITION);
    assert!(
        rejected.to_string().contains("no longer exists"),
        "{rejected}"
    );
}

/// The first child is gone in every round; the second looks gone in the third round only. The
/// rejection names only the first.
#[tokio::test(start_paused = true)]
async fn a_rejection_names_only_the_children_gone_in_every_round() {
    let mut scenario = WaitingParent::new(2).await;
    scenario.deliver_child_start(0);
    begin_child_purge(&scenario.harness.store, &scenario.children[0]);
    let mut hook_commands = DirectCommands {
        store: scenario.harness.store.clone(),
        next_micros: 1_800_000,
        next_command_id: 0xD0,
    };
    let (parent, parent_run) = (scenario.parent, scenario.parent_run);
    let second = scenario.children[1].clone();
    let mut actions: Vec<ViewAction> = (0..4).map(|_| no_view_action()).collect();
    actions.push(Box::new(move || {
        hook_commands.deliver_child_start(parent, parent_run, &second);
    }));
    run_after_views(&scenario.harness.committer, actions);
    let rejected = scenario.reset().await.expect_err("the gone child rejects");
    assert_eq!(rejected.grpc_code(), grpc_code::FAILED_PRECONDITION);
    let message = rejected.to_string();
    assert!(message.contains(&scenario.children[0].flow_id), "{message}");
    assert!(
        !message.contains(&scenario.children[1].flow_id),
        "{message}"
    );
}

/// Two children take turns looking gone, so no gone child is stable; the ResetFlow aborts.
#[tokio::test(start_paused = true)]
async fn children_gone_in_alternate_rounds_abort_the_reset() {
    let mut scenario = WaitingParent::new(2).await;
    scenario.deliver_child_start(0);
    scenario.deliver_child_start(1);
    begin_child_purge(&scenario.harness.store, &scenario.children[0]);
    let mut actions = Vec::new();
    for round in 0..8 {
        let store = scenario.harness.store.clone();
        let children = scenario.children.clone();
        actions.push(no_view_action());
        // After the parent view of round `round`, the other child is gone for the next round.
        actions.push(Box::new(move || {
            let is_first_gone_next = round % 2 == 1;
            set_child_purging(&store, &children[0], is_first_gone_next);
            set_child_purging(&store, &children[1], !is_first_gone_next);
        }) as ViewAction);
    }
    run_after_views(&scenario.harness.committer, actions);
    let aborted = scenario
        .reset()
        .await
        .expect_err("unstable gone children abort");
    assert_eq!(aborted.grpc_code(), grpc_code::ABORTED, "{aborted}");
    assert_eq!(
        run_record(
            &scenario.harness.store,
            scenario.parent,
            scenario.parent_run
        )
        .current_attempt,
        1,
        "no time travel committed"
    );
}

/// Registers InitStep-1's wait on `child_count` children and a signal. Returns the time of the
/// wait.
fn register_wait(
    direct: &mut DirectCommands,
    store: &MemoryStore,
    parent: ApplyContext,
    parent_run: RunId,
    child_count: u8,
) -> i64 {
    let wait_for = StepInvocationKey {
        step_execution_id: "InitStep-1".to_owned(),
        method: StepMethod::WaitFor as i32,
    };
    direct.apply(
        parent,
        PARENT_FLOW,
        parent_run.as_bytes(),
        flow_command::Command::StepAttemptStarted(StepAttemptStartedCommand {
            invocation: Some(wait_for.clone()),
            attempt: 1,
            task_id: vec![9; 16],
            pull_routing: None,
        }),
    );
    let sub_flow_conditions = (0..child_count)
        .map(|index| SubFlowCondition {
            condition_id: format!("child-{index}"),
            sub_flow_index: i32::from(index),
            sub_flow_type: "ChildFlow".to_owned(),
            start_step_type: "ChildStep".to_owned(),
            step_options: Some(StepOptions {
                skip_wait_for: true,
                ..StepOptions::default()
            }),
            ..SubFlowCondition::default()
        })
        .collect();
    direct.apply(
        parent,
        PARENT_FLOW,
        parent_run.as_bytes(),
        flow_command::Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(wait_for),
            attempt: 1,
            response: Some(step_method_completed_command::Response::WaitFor(
                InvokeWaitForMethodResponse {
                    waiting_condition: Some(WaitingCondition {
                        waiting_condition_type: WaitingConditionType::AllCompleted as i32,
                        // The signal keeps the Step waiting after the child's result.
                        channel_conditions: vec![ChannelCondition {
                            condition_id: "signal".to_owned(),
                            channel_name: "signal".to_owned(),
                            ..ChannelCondition::default()
                        }],
                        sub_flow_conditions,
                        ..WaitingCondition::default()
                    }),
                    ..InvokeWaitForMethodResponse::default()
                },
            )),
            ..StepMethodCompletedCommand::default()
        }),
    );
    let wait_seq = run_record(store, parent, parent_run).next_run_seq - 1;
    HistoryRecord::decode(
        store
            .view()
            .get(
                ColumnFamily::History,
                &history_record_key(parent.flow_hash, parent_run, wait_seq),
            )
            .unwrap()
            .expect("the wait is recorded")
            .as_slice(),
    )
    .unwrap()
    .timestamp_micros
}

/// A child purged after its report reached the parent between a round's views stays attached.
#[tokio::test(start_paused = true)]
async fn a_child_purged_after_its_report_reached_the_parent_stays_attached() {
    let mut scenario = WaitingParent::new(1).await;
    let (parent, parent_run) = (scenario.parent, scenario.parent_run);
    let child = scenario.children[0].clone();
    scenario.deliver_child_start(0);
    scenario.direct.apply(
        child.context,
        &child.flow_id,
        child.run_id.as_bytes(),
        flow_command::Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                stop_type: StopType::Terminate as i32,
                ..StopFlowRequest::default()
            }),
        }),
    );
    let report = outbox_records(&scenario.harness.store, child.context, child.run_id)
        .pop()
        .expect("the child reports its result");
    let Some(outbox_record::Payload::ChildCompleted(completed)) = report.payload.clone() else {
        panic!("expected a child report");
    };
    let mut hook_commands = DirectCommands {
        store: scenario.harness.store.clone(),
        next_micros: 1_800_000,
        next_command_id: 0xD0,
    };
    *scenario.harness.committer.view_hook.lock().unwrap() = Some(Box::new(move || {
        hook_commands.apply(
            parent,
            PARENT_FLOW,
            parent_run.as_bytes(),
            flow_command::Command::InboxMessage(InboxMessageCommand {
                message_id: report.message_id.clone(),
                source: Some(CrossFlowEndpoint {
                    tenant_id: TENANT.0,
                    flow_id: child.flow_id.clone(),
                    run_id: completed.child_run_id.clone(),
                    run_attempt: completed.child_run_attempt,
                }),
                payload: Some(inbox_message_command::Payload::ChildCompleted(completed)),
                ..InboxMessageCommand::default()
            }),
        );
        hook_commands.apply(
            child.context,
            &child.flow_id,
            child.run_id.as_bytes(),
            flow_command::Command::OutboxAck(OutboxAckCommand {
                message_ids: vec![report.message_id],
            }),
        );
        begin_child_purge(&hook_commands.store, &child);
    }));
    let run_attempt = scenario
        .reset()
        .await
        .expect("the purged child's result attaches its condition");
    assert_eq!(run_attempt, 2);
    assert!(
        scenario.restored_wait_attempts().is_empty(),
        "the condition stays on the child's received result"
    );
}

/// The child start reaches the child right after the first round's child view, so that round
/// finds the child gone. The clone fork computes again and restarts the running child.
#[tokio::test(start_paused = true)]
async fn a_clone_fork_restarts_a_child_started_between_the_views_of_a_round() {
    let scenario = WaitingParent::new(1).await;
    let hook_commands = Arc::new(Mutex::new(DirectCommands {
        store: scenario.harness.store.clone(),
        next_micros: 1_800_000,
        next_command_id: 0xD0,
    }));
    let (parent, parent_run) = (scenario.parent, scenario.parent_run);
    let child = scenario.children[0].clone();
    // View 1 checks for a retry; round 1 reads children on view 2 and the source on view 3.
    run_after_views(
        &scenario.harness.committer,
        vec![
            no_view_action(),
            Box::new(move || {
                hook_commands
                    .lock()
                    .unwrap()
                    .deliver_child_start(parent, parent_run, &child);
            }),
        ],
    );
    let response = scenario
        .clone_fork()
        .await
        .expect("the running child restarts under the default policy");
    let clone_context = scenario.harness.registry.flow_context(&flow(CLONE_FLOW));
    let clone_run: RunId = response.run_id.parse().expect("a run ID");
    let restarted: Vec<String> = outbox_records(&scenario.harness.store, clone_context, clone_run)
        .into_iter()
        .filter_map(|record| match record.payload {
            Some(outbox_record::Payload::ChildStart(start)) => Some(start.child_flow_id),
            _ => None,
        })
        .collect();
    assert_eq!(restarted, vec!["SubFlow:clone-InitStep-1-0".to_owned()]);
}

/// Two children take turns looking gone, so no gone child is stable; the clone fork aborts and
/// creates nothing.
#[tokio::test(start_paused = true)]
async fn children_gone_in_alternate_rounds_abort_the_clone_fork() {
    let mut scenario = WaitingParent::new(2).await;
    scenario.deliver_child_start(0);
    scenario.deliver_child_start(1);
    begin_child_purge(&scenario.harness.store, &scenario.children[0]);
    // View 1 checks for a retry.
    let mut actions = vec![no_view_action()];
    for round in 0..8 {
        let store = scenario.harness.store.clone();
        let children = scenario.children.clone();
        actions.push(no_view_action());
        actions.push(Box::new(move || {
            let is_first_gone_next = round % 2 == 1;
            set_child_purging(&store, &children[0], is_first_gone_next);
            set_child_purging(&store, &children[1], !is_first_gone_next);
        }) as ViewAction);
    }
    run_after_views(&scenario.harness.committer, actions);
    let aborted = scenario
        .clone_fork()
        .await
        .expect_err("unstable gone children abort");
    assert_eq!(aborted.grpc_code(), grpc_code::ABORTED, "{aborted}");
    let clone_context = scenario.harness.registry.flow_context(&flow(CLONE_FLOW));
    let directory_key =
        dexzd_keyspace::flow_directory_key(clone_context.flow_hash, TENANT, CLONE_FLOW);
    assert!(
        scenario
            .harness
            .store
            .view()
            .get(ColumnFamily::Flow, &directory_key)
            .unwrap()
            .is_none(),
        "no clone fork committed"
    );
}
