// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! ResetFlow reads child runs no later than the parent state it computes on.

use std::sync::atomic::Ordering;

use dexzd_apply::{ApplyContext, FlowStateReader, apply_flow_command};
use dexzd_keyspace::row_keys::{history_record_key, prefix_range, run_singleton_key};
use dexzd_keyspace::{ColumnFamily, RunId, RunTable, run_table_prefix};
use dexzd_protocol::dex::{
    ChannelCondition, InvokeWaitForMethodResponse, ResetFlowRequest, StepOptions, StopFlowRequest,
    StopType, SubFlowCondition, WaitingCondition, WaitingConditionType,
};
use dexzd_protocol::dexzd::v1::{
    CrossFlowEndpoint, FlowCommand, FlowStopCommand, HistoryRecord, InboxMessageCommand,
    OutboxAckCommand, OutboxRecord, RunRecord, StepAttemptStartedCommand, StepExecutionRecord,
    StepInvocationKey, StepMethod, StepMethodCompletedCommand, command_reply, flow_command,
    inbox_message_command, outbox_record, step_method_completed_command,
};
use prost::Message;

use crate::flow_coordination::FlowCoordinationConfig;
use crate::flow_coordination::tests::fixtures::{TENANT, flow, start_flow};
use crate::flow_coordination::tests::test_support::{FlowCoordinationHarness, MemoryStore};

const PARENT_FLOW: &str = "parent";
const CHILD_FLOW: &str = "SubFlow:parent-InitStep-1-0";
const CHILD_RUN: [u8; 16] = [3; 16];

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

/// A child purged after its report reached the parent stays attached, because ResetFlow reads
/// the child no later than the parent.
#[tokio::test(start_paused = true)]
async fn a_child_purged_after_its_report_reached_the_parent_stays_attached() {
    let harness = FlowCoordinationHarness::new();
    harness
        .applier
        .uses_real_apply
        .store(true, Ordering::SeqCst);
    let parent_run = start_flow(&harness, PARENT_FLOW).await.unwrap();
    let parent = harness.registry.flow_context(&flow(PARENT_FLOW));
    let child = harness.registry.flow_context(&flow(CHILD_FLOW));
    let mut direct = DirectCommands {
        store: harness.store.clone(),
        next_micros: 1_100_000,
        next_command_id: 0xA0,
    };
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
                        sub_flow_conditions: vec![SubFlowCondition {
                            condition_id: "child".to_owned(),
                            sub_flow_type: "ChildFlow".to_owned(),
                            start_step_type: "ChildStep".to_owned(),
                            step_options: Some(StepOptions {
                                skip_wait_for: true,
                                ..StepOptions::default()
                            }),
                            ..SubFlowCondition::default()
                        }],
                        ..WaitingCondition::default()
                    }),
                    ..InvokeWaitForMethodResponse::default()
                },
            )),
            ..StepMethodCompletedCommand::default()
        }),
    );
    let wait_seq = run_record(&harness.store, parent, parent_run).next_run_seq - 1;
    let wait_time = HistoryRecord::decode(
        harness
            .store
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
    .timestamp_micros;
    let start = outbox_records(&harness.store, parent, parent_run)
        .pop()
        .expect("the wait starts its child");
    let Some(outbox_record::Payload::ChildStart(child_start)) = start.payload.clone() else {
        panic!("expected a child start");
    };
    direct.apply(
        child,
        CHILD_FLOW,
        &[],
        flow_command::Command::InboxMessage(InboxMessageCommand {
            message_id: start.message_id.clone(),
            source: child_start
                .parent
                .as_ref()
                .and_then(|reference| reference.parent.clone()),
            payload: Some(inbox_message_command::Payload::ChildStart(child_start)),
            new_run_id: CHILD_RUN.to_vec(),
            settings: Some(FlowCoordinationConfig::default().run_settings),
        }),
    );
    direct.apply(
        parent,
        PARENT_FLOW,
        parent_run.as_bytes(),
        flow_command::Command::OutboxAck(OutboxAckCommand {
            message_ids: vec![start.message_id],
        }),
    );
    direct.apply(
        child,
        CHILD_FLOW,
        &CHILD_RUN,
        flow_command::Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                stop_type: StopType::Terminate as i32,
                ..StopFlowRequest::default()
            }),
        }),
    );
    let report = outbox_records(&harness.store, child, RunId(CHILD_RUN))
        .pop()
        .expect("the child reports its result");
    let Some(outbox_record::Payload::ChildCompleted(completed)) = report.payload.clone() else {
        panic!("expected a child report");
    };

    // Right after the ResetFlow's first view, the report reaches the parent, the child's outbox
    // is acknowledged, and the child's purge begins. A parent view older than the child view
    // would then find the child gone without a result and reject the travel.
    let mut hook_commands = DirectCommands {
        store: harness.store.clone(),
        next_micros: 1_800_000,
        next_command_id: 0xD0,
    };
    let parent_run_bytes = parent_run.as_bytes().to_vec();
    *harness.committer.view_hook.lock().unwrap() = Some(Box::new(move || {
        hook_commands.apply(
            parent,
            PARENT_FLOW,
            &parent_run_bytes,
            flow_command::Command::InboxMessage(InboxMessageCommand {
                message_id: report.message_id.clone(),
                source: Some(CrossFlowEndpoint {
                    tenant_id: TENANT.0,
                    flow_id: CHILD_FLOW.to_owned(),
                    run_id: completed.child_run_id.clone(),
                    run_attempt: completed.child_run_attempt,
                }),
                payload: Some(inbox_message_command::Payload::ChildCompleted(completed)),
                ..InboxMessageCommand::default()
            }),
        );
        hook_commands.apply(
            child,
            CHILD_FLOW,
            &CHILD_RUN,
            flow_command::Command::OutboxAck(OutboxAckCommand {
                message_ids: vec![report.message_id],
            }),
        );
        let mut purging = run_record(&hook_commands.store, child, RunId(CHILD_RUN));
        purging.is_purging = true;
        hook_commands.store.put(
            ColumnFamily::Flow,
            run_singleton_key(child.flow_hash, RunId(CHILD_RUN), RunTable::RunRecord),
            purging.encode_to_vec(),
        );
    }));

    let response = harness
        .drive(harness.registry.reset_flow(
            TENANT,
            ResetFlowRequest {
                flow_id: PARENT_FLOW.to_owned(),
                reset_type: dexzd_protocol::dex::FlowResetType::HistoryEventTime as i32,
                history_event_time: (i128::from(wait_time) * 1000).to_string(),
                run_attempt: 1,
                ..ResetFlowRequest::default()
            },
        ))
        .await
        .expect("the purged child's result attaches its condition");
    assert_eq!(response.run_attempt, 2);
    let step_bytes = harness
        .store
        .view()
        .get(
            ColumnFamily::Flow,
            &dexzd_keyspace::row_keys::run_named_key(
                parent.flow_hash,
                parent_run,
                RunTable::StepExecution,
                "InitStep-1",
            ),
        )
        .unwrap()
        .expect("the restored Step exists");
    let wait = StepExecutionRecord::decode(step_bytes.as_slice())
        .unwrap()
        .wait
        .expect("the signal keeps the restored Step waiting");
    assert!(
        wait.sub_flow_registration_attempts.is_empty(),
        "the condition stays on the child's received result"
    );
}
