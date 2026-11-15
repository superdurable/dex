// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! SubFlow reset policies of a time travel, for each state of a restored wait's child.

use std::cell::{Cell, RefCell};

use super::*;
use dexzd_apply::values::NoOffloadedValues;
use dexzd_apply::{
    ChildFlowRead, ChildFlowRunQuery, ChildFlowRuns, StateChildFlowRuns, StateHistorySource,
    TimeTravelComputation, TimeTravelComputationRequest, TimeTravelComputationSources,
    compute_time_travel,
};
use dexzd_protocol::dex::{
    ChannelCondition, ChannelMessage, PublishToChannelRequest, StepMovement, SubFlowResetPolicy,
};
use dexzd_protocol::dexzd::v1::{ChannelPublishCommand, CommandErrorReply};

const SIGNAL_CHANNEL: &str = "signal";
const CHILD_INPUT: &str = "child input";
const SECOND_CHILD_ID: &str = "SubFlow:parent-StartStep-1-1";

const POLICIES: [SubFlowResetPolicy; 7] = [
    SubFlowResetPolicy::Unspecified,
    SubFlowResetPolicy::AlwaysRestart,
    SubFlowResetPolicy::AlwaysFail,
    SubFlowResetPolicy::AlwaysReject,
    SubFlowResetPolicy::AttachElseRestart,
    SubFlowResetPolicy::AttachElseFail,
    SubFlowResetPolicy::AttachElseReject,
];

/// How the restored wait's child stands when the parent travels back to the wait.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ChildState {
    /// The parent received the child's result before the target point.
    ReceivedBeforeTarget,
    /// The parent received the child's result after the target point.
    ReceivedAfterTarget,
    /// The child run still runs.
    Running,
    /// The child run ended, and its report has not reached the parent.
    EndedUnreceived,
    /// The child run no longer exists, as after a purge.
    Gone,
    /// The child start still waits in the parent's outbox.
    StartPending,
}

const CHILD_STATES: [ChildState; 6] = [
    ChildState::ReceivedBeforeTarget,
    ChildState::ReceivedAfterTarget,
    ChildState::Running,
    ChildState::EndedUnreceived,
    ChildState::Gone,
    ChildState::StartPending,
];

/// What a time travel does with the restored SubFlow condition.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ResetOutcome {
    /// The ResetFlow is rejected and writes nothing.
    Rejected,
    /// The condition completes with the child's own result.
    UsesChildResult,
    /// The condition keeps waiting for the running child.
    KeepsWaiting,
    /// The child restarts for run attempt 2.
    Restarted,
    /// The condition fails with SUB_FLOW_ABANDONED.
    Failed { terminates_child: bool },
}

fn expected_outcome(policy: SubFlowResetPolicy, state: ChildState) -> ResetOutcome {
    use ChildState::{
        EndedUnreceived, Gone, ReceivedAfterTarget, ReceivedBeforeTarget, Running, StartPending,
    };
    use SubFlowResetPolicy as Policy;
    match (policy, state) {
        (Policy::AlwaysReject, _)
        | (Policy::AlwaysRestart, Gone)
        | (Policy::AlwaysRestart | Policy::AlwaysFail, StartPending) => ResetOutcome::Rejected,
        (Policy::AlwaysRestart, _) => ResetOutcome::Restarted,
        (Policy::AlwaysFail, ReceivedBeforeTarget | ReceivedAfterTarget) => ResetOutcome::Failed {
            terminates_child: false,
        },
        (Policy::AlwaysFail, _) => ResetOutcome::Failed {
            terminates_child: true,
        },
        (_, ReceivedBeforeTarget | ReceivedAfterTarget | EndedUnreceived) => {
            ResetOutcome::UsesChildResult
        }
        // A start on its way attaches: the child it starts reports to the condition.
        (_, Running | StartPending) => ResetOutcome::KeepsWaiting,
        (Policy::AttachElseFail, Gone) => ResetOutcome::Failed {
            terminates_child: false,
        },
        (_, Gone) => ResetOutcome::Rejected,
    }
}

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}

fn sub_flow_condition(condition_id: &str) -> SubFlowCondition {
    SubFlowCondition {
        condition_id: condition_id.to_owned(),
        sub_flow_type: "ChildFlow".to_owned(),
        start_step_type: "ChildStep".to_owned(),
        step_input: Some(string_value(CHILD_INPUT)),
        step_options: Some(StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        }),
        options: Some(SubFlowOptions::default()),
        ..SubFlowCondition::default()
    }
}

fn error_of(outcome: &ApplyOutcome) -> Option<&CommandErrorReply> {
    match &outcome.reply.reply {
        Some(command_reply::Reply::Error(error)) => Some(error),
        _ => None,
    }
}

impl Harness {
    /// Registers a wait on the SubFlow conditions and a signal, so the Step keeps waiting after
    /// its children complete. Returns the child starts.
    fn register_children_and_signal_wait(&mut self, condition_ids: &[&str]) -> Vec<OutboxRecord> {
        self.register_step_children_and_signal_wait("StartStep-1", condition_ids)
    }

    /// Registers the wait of `step_execution_id` as [`Self::register_children_and_signal_wait`]
    /// does. Returns every child start in the parent's outbox.
    fn register_step_children_and_signal_wait(
        &mut self,
        step_execution_id: &str,
        condition_ids: &[&str],
    ) -> Vec<OutboxRecord> {
        let invocation = StepInvocationKey {
            step_execution_id: step_execution_id.to_owned(),
            method: StepMethod::WaitFor as i32,
        };
        assert_ok(&self.apply(
            "parent",
            &PARENT_RUN,
            Command::StepAttemptStarted(StepAttemptStartedCommand {
                invocation: Some(invocation.clone()),
                attempt: 1,
                task_id: vec![9; 16],
                pull_routing: None,
            }),
        ));
        assert_ok(
            &self.apply(
                "parent",
                &PARENT_RUN,
                Command::StepMethodCompleted(StepMethodCompletedCommand {
                    invocation: Some(invocation),
                    attempt: 1,
                    response: Some(step_method_completed_command::Response::WaitFor(
                        InvokeWaitForMethodResponse {
                            waiting_condition: Some(WaitingCondition {
                                waiting_condition_type: WaitingConditionType::AllCompleted as i32,
                                channel_conditions: vec![ChannelCondition {
                                    condition_id: "signal".to_owned(),
                                    channel_name: SIGNAL_CHANNEL.to_owned(),
                                    ..ChannelCondition::default()
                                }],
                                sub_flow_conditions: condition_ids
                                    .iter()
                                    .enumerate()
                                    .map(|(index, condition_id)| SubFlowCondition {
                                        sub_flow_index: i32::try_from(index).expect("index fits"),
                                        ..sub_flow_condition(condition_id)
                                    })
                                    .collect(),
                                ..WaitingCondition::default()
                            }),
                            ..InvokeWaitForMethodResponse::default()
                        },
                    )),
                    ..StepMethodCompletedCommand::default()
                }),
            ),
        );
        self.outbox("parent", PARENT_RUN)
    }

    fn last_parent_seq(&self) -> u64 {
        self.record("parent", PARENT_RUN).next_run_seq - 1
    }

    fn ack_parent_outbox(&mut self, message_id: &[u8]) {
        assert_ok(&self.apply(
            "parent",
            &PARENT_RUN,
            Command::OutboxAck(OutboxAckCommand {
                message_ids: vec![message_id.to_vec()],
            }),
        ));
    }

    fn terminate_child(&mut self, child_flow_id: &str, run_id: [u8; 16]) {
        assert_ok(&self.apply(
            child_flow_id,
            &run_id,
            Command::FlowStop(FlowStopCommand {
                request: Some(StopFlowRequest {
                    stop_type: StopType::Terminate as i32,
                    ..StopFlowRequest::default()
                }),
            }),
        ));
    }

    /// Returns the latest report of the child run.
    fn latest_child_report(&self, child_flow_id: &str, run_id: [u8; 16]) -> OutboxRecord {
        self.outbox(child_flow_id, run_id)
            .into_iter()
            .rfind(|record| {
                matches!(
                    record.payload,
                    Some(outbox_record::Payload::ChildCompleted(_))
                )
            })
            .expect("the child run reports")
    }

    /// Delivers a child report to the parent.
    fn deliver_report(&mut self, child_flow_id: &str, report: &OutboxRecord) -> ApplyOutcome {
        let Some(outbox_record::Payload::ChildCompleted(completed)) = report.payload.clone() else {
            panic!("expected a child report");
        };
        self.apply(
            "parent",
            &PARENT_RUN,
            Command::InboxMessage(InboxMessageCommand {
                message_id: report.message_id.clone(),
                source: Some(CrossFlowEndpoint {
                    tenant_id: 0,
                    flow_id: child_flow_id.to_owned(),
                    run_id: completed.child_run_id.clone(),
                    run_attempt: completed.child_run_attempt,
                }),
                payload: Some(inbox_message_command::Payload::ChildCompleted(completed)),
                ..InboxMessageCommand::default()
            }),
        )
    }

    /// Delivers a child start or termination that the parent wrote, as the outbox does.
    fn deliver_to_child(&mut self, outbox: &OutboxRecord, new_run_id: [u8; 16]) -> ApplyOutcome {
        let (child_flow_id, parent, payload) = match outbox.payload.clone() {
            Some(outbox_record::Payload::ChildStart(start)) => (
                start.child_flow_id.clone(),
                start.parent.clone(),
                inbox_message_command::Payload::ChildStart(start),
            ),
            Some(outbox_record::Payload::ChildTerminate(terminate)) => (
                terminate.child_flow_id.clone(),
                terminate.parent.clone(),
                inbox_message_command::Payload::ChildTerminate(terminate),
            ),
            other => panic!("expected a message to a child: {other:?}"),
        };
        let is_start = matches!(payload, inbox_message_command::Payload::ChildStart(_));
        self.apply(
            &child_flow_id,
            &[],
            Command::InboxMessage(InboxMessageCommand {
                message_id: outbox.message_id.clone(),
                source: parent.and_then(|parent| parent.parent),
                payload: Some(payload),
                new_run_id: if is_start {
                    new_run_id.to_vec()
                } else {
                    Vec::new()
                },
                settings: is_start.then(settings),
            }),
        )
    }

    /// Starts the parent, registers the wait, and leaves its child in `state`. Returns the time
    /// of the target point.
    fn prepare_child(&mut self, state: ChildState) -> i64 {
        self.start_parent();
        let start = self
            .register_children_and_signal_wait(&["child"])
            .pop()
            .expect("the wait starts its child");
        let wait_time = self.parent_history_time(self.last_parent_seq());
        if state == ChildState::StartPending {
            return wait_time;
        }
        // A gone child emulates a purge: its start was delivered, and its run no longer exists.
        if state != ChildState::Gone {
            assert_ok(&self.deliver_start_outbox(&start, FIRST_CHILD_RUN));
        }
        self.ack_parent_outbox(&start.message_id);
        match state {
            ChildState::Running | ChildState::Gone | ChildState::StartPending => wait_time,
            ChildState::EndedUnreceived => {
                self.terminate_child(CHILD_ID, FIRST_CHILD_RUN);
                wait_time
            }
            ChildState::ReceivedAfterTarget | ChildState::ReceivedBeforeTarget => {
                self.terminate_child(CHILD_ID, FIRST_CHILD_RUN);
                let report = self.latest_child_report(CHILD_ID, FIRST_CHILD_RUN);
                assert_ok(&self.deliver_report(CHILD_ID, &report));
                assert_eq!(
                    self.step("parent", PARENT_RUN, "StartStep-1").phase,
                    StepExecutionPhase::Waiting as i32,
                    "the signal keeps the Step waiting"
                );
                if state == ChildState::ReceivedBeforeTarget {
                    self.parent_history_time(self.last_parent_seq())
                } else {
                    wait_time
                }
            }
        }
    }

    fn travel_with_policy(&mut self, target_micros: i64, policy: i32) -> ApplyOutcome {
        self.apply(
            "parent",
            &PARENT_RUN,
            Command::TimeTravel(TimeTravelCommand {
                request: Some(ResetFlowRequest {
                    flow_id: "parent".to_owned(),
                    reset_type: FlowResetType::HistoryEventTime as i32,
                    history_event_time: (i128::from(target_micros) * 1000).to_string(),
                    run_attempt: 1,
                    sub_flow_reset_policy: policy,
                    ..ResetFlowRequest::default()
                }),
                ..TimeTravelCommand::default()
            }),
        )
    }

    fn publish_signal(&mut self) {
        assert_ok(&self.apply(
            "parent",
            &PARENT_RUN,
            Command::ChannelPublish(ChannelPublishCommand {
                request: Some(PublishToChannelRequest {
                    messages: vec![ChannelMessage {
                        channel_name: SIGNAL_CHANNEL.to_owned(),
                        value: Some(string_value("go")),
                        message_id: "signal-1".to_owned(),
                    }],
                    ..PublishToChannelRequest::default()
                }),
            }),
        ));
    }

    /// The parent's outbox messages that the command at `seq` wrote.
    fn parent_outbox_written_at(&self, seq: u64) -> Vec<OutboxRecord> {
        self.outbox("parent", PARENT_RUN)
            .into_iter()
            .filter(|record| record.written_seq == seq)
            .collect()
    }

    fn parent_sub_flow_result_of(
        &self,
        child_flow_id: &str,
        attempt: u32,
    ) -> Option<SubFlowResultRecord> {
        let parent_hash = self.hasher.flow_hash(TenantId::DEFAULT, "parent");
        let key = run_named_key(
            parent_hash,
            RunId(PARENT_RUN),
            RunTable::SubFlowResult,
            &format!("{attempt:010}:{child_flow_id}"),
        );
        self.state
            .get(ColumnFamily::Flow, &key)
            .expect("memory reads")
            .map(|bytes| SubFlowResultRecord::decode(bytes.as_slice()).expect("result decodes"))
    }

    fn snapshot(&self) -> Harness {
        Harness {
            state: MemoryState {
                rows: self.state.rows.clone(),
            },
            hasher: self.hasher.clone(),
            now_micros: self.now_micros,
        }
    }
}

/// Checks what a time travel with `policy` did, then publishes the signal and checks what the
/// Step's Execute receives.
fn assert_travel_outcome(
    harness: &mut Harness,
    travel: &ApplyOutcome,
    rows_before: &BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>>,
    expected: ResetOutcome,
    case: &str,
) {
    if expected == ResetOutcome::Rejected {
        let error = error_of(travel).unwrap_or_else(|| panic!("{case}: the travel is rejected"));
        assert_eq!(error.grpc_code, 9, "{case}: FAILED_PRECONDITION");
        assert!(
            error.message.contains("StartStep-1") && error.message.contains(CHILD_ID),
            "{case}: the rejection names the Step and the child: {}",
            error.message
        );
        assert_eq!(
            &harness.state.rows, rows_before,
            "{case}: nothing is written"
        );
        return;
    }
    assert!(
        matches!(
            travel.reply.reply,
            Some(command_reply::Reply::TimeTravel(_))
        ),
        "{case}: {:?}",
        travel.reply
    );
    assert_eq!(harness.record("parent", PARENT_RUN).current_attempt, 2);
    let fork_seq = harness.last_parent_seq();
    let written = harness.parent_outbox_written_at(fork_seq);
    let wait = harness
        .step("parent", PARENT_RUN, "StartStep-1")
        .wait
        .unwrap_or_else(|| panic!("{case}: the restored Step waits"));
    match expected {
        ResetOutcome::UsesChildResult | ResetOutcome::KeepsWaiting => {
            assert!(written.is_empty(), "{case}: no child message: {written:?}");
            assert!(wait.sub_flow_registration_attempts.is_empty(), "{case}");
        }
        ResetOutcome::Restarted => {
            assert_eq!(written.len(), 1, "{case}: one restart");
            let Some(outbox_record::Payload::ChildStart(start)) = written[0].payload.clone() else {
                panic!("{case}: the restart writes a child start");
            };
            assert_eq!(
                start.request_id,
                format!("{}:2:StartStep-1:0", RunId(PARENT_RUN))
            );
            let parent = start.parent.expect("the start names its parent");
            assert_eq!(parent.parent.expect("parent endpoint").run_attempt, 2);
            assert_eq!(parent.condition_id, "child");
            let condition = start.condition.expect("the start has its condition");
            assert_eq!(condition.step_input, Some(string_value(CHILD_INPUT)));
            assert_eq!(
                condition.options.expect("options").reuse_policy,
                SubFlowReusePolicy::AlwaysRestart as i32
            );
            assert_eq!(wait.sub_flow_registration_attempts, vec![2], "{case}");
        }
        ResetOutcome::Failed { terminates_child } => {
            let terminations: Vec<_> = written
                .iter()
                .filter_map(|record| match &record.payload {
                    Some(outbox_record::Payload::ChildTerminate(terminate)) => Some(terminate),
                    _ => None,
                })
                .collect();
            assert_eq!(terminations.len(), usize::from(terminates_child), "{case}");
            for terminate in terminations {
                assert_eq!(terminate.child_flow_id, CHILD_ID);
                let parent = terminate.parent.clone().expect("termination parent");
                assert_eq!(parent.parent.expect("parent endpoint").run_attempt, 1);
            }
            assert_eq!(wait.sub_flow_registration_attempts, vec![2], "{case}");
            let abandoned = harness
                .parent_sub_flow_result_of(CHILD_ID, 2)
                .and_then(|record| record.result)
                .expect("the abandoned result is stored for attempt 2");
            assert_eq!(abandoned.flow_status, FlowStatus::Failed as i32);
            assert_eq!(abandoned.error_type, FlowErrorType::SubFlowAbandoned as i32);
        }
        ResetOutcome::Rejected => unreachable!("handled above"),
    }
    harness.publish_signal();
    let step = harness.step("parent", PARENT_RUN, "StartStep-1");
    match expected {
        ResetOutcome::UsesChildResult => {
            assert_eq!(step.phase, StepExecutionPhase::Execute as i32, "{case}");
            let result = &step.condition_results.expect("results").sub_flow_results[0];
            assert_eq!(result.flow_status, FlowStatus::Terminated as i32, "{case}");
        }
        ResetOutcome::Failed { .. } => {
            assert_eq!(step.phase, StepExecutionPhase::Execute as i32, "{case}");
            let result = &step.condition_results.expect("results").sub_flow_results[0];
            assert_eq!(result.flow_status, FlowStatus::Failed as i32, "{case}");
            assert_eq!(
                result.error_type,
                FlowErrorType::SubFlowAbandoned as i32,
                "{case}"
            );
        }
        ResetOutcome::KeepsWaiting | ResetOutcome::Restarted => {
            assert_eq!(step.phase, StepExecutionPhase::Waiting as i32, "{case}");
        }
        ResetOutcome::Rejected => unreachable!("handled above"),
    }
}

#[test]
fn every_policy_handles_every_child_state() {
    for policy in POLICIES {
        for state in CHILD_STATES {
            let case = format!("{policy:?} with a child that is {state:?}");
            let mut harness = Harness::new();
            let target = harness.prepare_child(state);
            let rows_before = harness.state.rows.clone();
            let travel = harness.travel_with_policy(target, policy as i32);
            let expected = expected_outcome(policy, state);
            assert_travel_outcome(&mut harness, &travel, &rows_before, expected, &case);
        }
    }
}

#[test]
fn unspecified_policy_is_attach_else_restart() {
    for state in CHILD_STATES {
        let mut unspecified = Harness::new();
        let target = unspecified.prepare_child(state);
        let mut explicit = unspecified.snapshot();
        let by_default = unspecified.travel_with_policy(target, 0);
        let by_name =
            explicit.travel_with_policy(target, SubFlowResetPolicy::AttachElseRestart as i32);
        // Only the recorded request differs, since it echoes the policy field.
        assert_eq!(by_default.effects, by_name.effects, "{state:?}");
        assert_eq!(by_default.reply, by_name.reply, "{state:?}");
        assert_eq!(
            unspecified.attempt_rows("parent", PARENT_RUN),
            explicit.attempt_rows("parent", PARENT_RUN),
            "{state:?}"
        );
        assert_eq!(
            unspecified.outbox("parent", PARENT_RUN),
            explicit.outbox("parent", PARENT_RUN),
            "{state:?}"
        );
        for attempt in [1, 2] {
            assert_eq!(
                unspecified.parent_sub_flow_result_of(CHILD_ID, attempt),
                explicit.parent_sub_flow_result_of(CHILD_ID, attempt),
                "{state:?}"
            );
        }
    }
}

#[test]
fn unspecified_policy_keeps_received_and_running_children_as_before() {
    for state in [
        ChildState::ReceivedBeforeTarget,
        ChildState::ReceivedAfterTarget,
        ChildState::Running,
    ] {
        let mut harness = Harness::new();
        let target = harness.prepare_child(state);
        let travel = harness.travel_with_policy(target, 0);
        let touches_policy_rows = travel.writes.iter().any(|write| {
            let key = match write {
                StateWrite::Put { key, .. }
                | StateWrite::Delete { key, .. }
                | StateWrite::SingleDelete { key, .. } => key,
                StateWrite::DeleteRange { .. } => return false,
            };
            parse_run_key(key).is_ok_and(|parsed| {
                matches!(parsed.table, RunTable::Outbox | RunTable::SubFlowResult)
            })
        });
        assert!(
            !touches_policy_rows,
            "{state:?}: the travel writes no child message and no child result"
        );
        assert!(
            !travel
                .effects
                .iter()
                .any(|effect| matches!(effect, ApplyEffect::OutboxMessagesWritten { .. })),
            "{state:?}"
        );
    }
}

#[test]
fn a_staged_ended_result_is_delivered_once_when_the_real_report_arrives() {
    let mut harness = Harness::new();
    let target = harness.prepare_child(ChildState::EndedUnreceived);
    assert!(harness.parent_sub_flow_result_of(CHILD_ID, 1).is_none());
    let travel = harness.travel_with_policy(target, 0);
    assert!(matches!(
        travel.reply.reply,
        Some(command_reply::Reply::TimeTravel(_))
    ));
    let delivered = harness
        .parent_sub_flow_result_of(CHILD_ID, 1)
        .expect("the travel delivers the child's final result");
    assert_eq!(delivered.child_run_id, FIRST_CHILD_RUN.to_vec());
    harness.publish_signal();
    let completed = harness.step("parent", PARENT_RUN, "StartStep-1");
    assert_eq!(completed.phase, StepExecutionPhase::Execute as i32);

    let report = harness.latest_child_report(CHILD_ID, FIRST_CHILD_RUN);
    assert_ok(&harness.deliver_report(CHILD_ID, &report));
    let after_report = harness.step("parent", PARENT_RUN, "StartStep-1");
    assert_eq!(after_report.phase, completed.phase);
    assert_eq!(after_report.condition_results, completed.condition_results);
    assert_eq!(after_report.invocation, completed.invocation);
}

#[test]
fn a_running_child_stays_attached_under_the_default_policy() {
    let mut harness = Harness::new();
    let target = harness.prepare_child(ChildState::Running);
    let travel = harness.travel_with_policy(target, 0);
    assert!(matches!(
        travel.reply.reply,
        Some(command_reply::Reply::TimeTravel(_))
    ));
    harness.publish_signal();
    harness.terminate_child(CHILD_ID, FIRST_CHILD_RUN);
    let report = harness.latest_child_report(CHILD_ID, FIRST_CHILD_RUN);
    assert_ok(&harness.deliver_report(CHILD_ID, &report));
    let step = harness.step("parent", PARENT_RUN, "StartStep-1");
    assert_eq!(step.phase, StepExecutionPhase::Execute as i32);
    assert_eq!(
        step.condition_results.expect("results").sub_flow_results[0].flow_status,
        FlowStatus::Terminated as i32
    );
}

#[test]
fn a_restart_terminates_the_running_child_and_ignores_its_late_result() {
    let mut harness = Harness::new();
    let target = harness.prepare_child(ChildState::Running);
    let travel = harness.travel_with_policy(target, SubFlowResetPolicy::AlwaysRestart as i32);
    assert!(matches!(
        travel.reply.reply,
        Some(command_reply::Reply::TimeTravel(_))
    ));
    harness.publish_signal();
    let restart = harness
        .parent_outbox_written_at(harness.last_parent_seq() - 1)
        .pop()
        .expect("the travel writes the restart");
    assert_ok(&harness.deliver_to_child(&restart, SECOND_CHILD_RUN));
    assert_eq!(
        harness.record(CHILD_ID, FIRST_CHILD_RUN).status,
        FlowStatus::Terminated as i32
    );
    assert_eq!(
        harness.record(CHILD_ID, SECOND_CHILD_RUN).status,
        FlowStatus::Running as i32
    );
    let old_report = harness.latest_child_report(CHILD_ID, FIRST_CHILD_RUN);
    assert_ok(&harness.deliver_report(CHILD_ID, &old_report));
    assert_eq!(
        harness.step("parent", PARENT_RUN, "StartStep-1").phase,
        StepExecutionPhase::Waiting as i32,
        "the old child's late result does not complete the restarted condition"
    );
    harness.terminate_child(CHILD_ID, SECOND_CHILD_RUN);
    let new_report = harness.latest_child_report(CHILD_ID, SECOND_CHILD_RUN);
    assert_ok(&harness.deliver_report(CHILD_ID, &new_report));
    let step = harness.step("parent", PARENT_RUN, "StartStep-1");
    assert_eq!(step.phase, StepExecutionPhase::Execute as i32);
    assert_eq!(
        harness
            .parent_sub_flow_result_of(CHILD_ID, 2)
            .expect("the restarted child's result")
            .child_run_id,
        SECOND_CHILD_RUN.to_vec()
    );
}

#[test]
fn a_failure_terminates_the_running_child_and_keeps_the_abandoned_result() {
    let mut harness = Harness::new();
    let target = harness.prepare_child(ChildState::Running);
    let travel = harness.travel_with_policy(target, SubFlowResetPolicy::AlwaysFail as i32);
    assert!(matches!(
        travel.reply.reply,
        Some(command_reply::Reply::TimeTravel(_))
    ));
    let termination = harness
        .parent_outbox_written_at(harness.last_parent_seq())
        .pop()
        .expect("the travel terminates the child");
    harness.publish_signal();
    let failed = harness.step("parent", PARENT_RUN, "StartStep-1");
    assert_eq!(failed.phase, StepExecutionPhase::Execute as i32);

    assert_ok(&harness.deliver_to_child(&termination, SECOND_CHILD_RUN));
    let child = harness.record(CHILD_ID, FIRST_CHILD_RUN);
    assert_eq!(child.status, FlowStatus::Terminated as i32);
    let repeated = harness.deliver_to_child(&termination, SECOND_CHILD_RUN);
    assert!(
        repeated.writes.is_empty(),
        "a repeated termination changes nothing"
    );

    let late_report = harness.latest_child_report(CHILD_ID, FIRST_CHILD_RUN);
    assert_ok(&harness.deliver_report(CHILD_ID, &late_report));
    let after_report = harness.step("parent", PARENT_RUN, "StartStep-1");
    assert_eq!(after_report.condition_results, failed.condition_results);
    assert_eq!(
        harness
            .parent_sub_flow_result_of(CHILD_ID, 2)
            .and_then(|record| record.result)
            .expect("the abandoned result stays")
            .error_type,
        FlowErrorType::SubFlowAbandoned as i32
    );
}

#[test]
fn restarts_of_several_conditions_use_their_own_message_ids() {
    let mut harness = Harness::new();
    harness.start_parent();
    let starts = harness.register_children_and_signal_wait(&["child", "second"]);
    let wait_time = harness.parent_history_time(harness.last_parent_seq());
    assert_eq!(starts.len(), 2);
    for (start, run_id) in starts.iter().zip([FIRST_CHILD_RUN, SECOND_CHILD_RUN]) {
        assert_ok(&harness.deliver_to_child(start, run_id));
        harness.ack_parent_outbox(&start.message_id);
    }
    let travel = harness.travel_with_policy(wait_time, SubFlowResetPolicy::AlwaysRestart as i32);
    assert!(
        matches!(
            travel.reply.reply,
            Some(command_reply::Reply::TimeTravel(_))
        ),
        "{:?}",
        travel.reply
    );
    let restarts = harness.parent_outbox_written_at(harness.last_parent_seq());
    let mut restarted_children: Vec<_> = restarts
        .iter()
        .map(|record| match &record.payload {
            Some(outbox_record::Payload::ChildStart(start)) => start.child_flow_id.clone(),
            other => panic!("expected restarts: {other:?}"),
        })
        .collect();
    restarted_children.sort();
    assert_eq!(restarted_children, vec![CHILD_ID, SECOND_CHILD_ID]);
    let original_ids: Vec<_> = starts.iter().map(|start| &start.message_id).collect();
    assert_ne!(restarts[0].message_id, restarts[1].message_id);
    for restart in &restarts {
        assert!(!original_ids.contains(&&restart.message_id));
        assert_eq!(restart.message_id[0], 1, "a restart is a child start");
    }
    let wait = harness
        .step("parent", PARENT_RUN, "StartStep-1")
        .wait
        .expect("the Step waits");
    assert_eq!(wait.sub_flow_registration_attempts, vec![2, 2]);
}

#[test]
fn a_rebuild_of_a_point_after_a_restart_or_failure_matches_the_live_state() {
    for policy in [
        SubFlowResetPolicy::AlwaysRestart,
        SubFlowResetPolicy::AlwaysFail,
    ] {
        let mut harness = Harness::new();
        let target = harness.prepare_child(ChildState::Running);
        let travel = harness.travel_with_policy(target, policy as i32);
        assert!(matches!(
            travel.reply.reply,
            Some(command_reply::Reply::TimeTravel(_))
        ));
        harness.publish_signal();
        let (signal_time, live_hash) = harness.parent_semantic_point();
        assert_eq!(
            harness.rebuilt_parent_point_hash(2, signal_time),
            live_hash,
            "{policy:?}"
        );
    }
}

#[test]
fn apply_aborts_when_the_owner_found_a_result_that_apply_does_not_see() {
    let mut harness = Harness::new();
    let target = harness.prepare_child(ChildState::ReceivedAfterTarget);
    harness.now_micros += 1000;
    let command = FlowCommand {
        flow_id: "parent".to_owned(),
        run_id: PARENT_RUN.to_vec(),
        epoch: 1,
        behavior_version: 1,
        proposed_at_micros: harness.now_micros,
        command_id: [5; 16].to_vec(),
        command: Some(Command::TimeTravel(TimeTravelCommand {
            request: Some(ResetFlowRequest {
                flow_id: "parent".to_owned(),
                reset_type: FlowResetType::HistoryEventTime as i32,
                history_event_time: (i128::from(target) * 1000).to_string(),
                run_attempt: 1,
                sub_flow_reset_policy: SubFlowResetPolicy::AttachElseReject as i32,
                ..ResetFlowRequest::default()
            }),
            ..TimeTravelCommand::default()
        })),
        ..FlowCommand::default()
    };
    let context = harness.parent_context();
    let staged = stage_time_travel_reading_child_flows_for_test(
        &context,
        &harness.state,
        &harness.hasher,
        &command,
    )
    .expect("the owner computes")
    .expect("the owner finds the child result received");
    let mut diverged = harness.snapshot();
    let result_key = run_named_key(
        context.flow_hash,
        RunId(PARENT_RUN),
        RunTable::SubFlowResult,
        &format!("{:010}:{CHILD_ID}", 1),
    );
    diverged
        .state
        .rows
        .remove(&(ColumnFamily::Flow, result_key))
        .expect("the result row exists");
    let mut last = None;
    for staged_command in staged.commands {
        let outcome =
            apply_flow_command(&context, &diverged.state, &staged_command).expect("apply runs");
        diverged.state.commit(&outcome);
        last = Some(outcome);
    }
    let travel = last.expect("the owner proposes the TimeTravel");
    let error = error_of(&travel).expect("apply rejects the travel");
    assert_eq!(error.grpc_code, 10, "ABORTED: {}", error.message);
    assert!(error.message.contains(CHILD_ID), "{}", error.message);
}

#[test]
fn a_staged_result_without_child_flow_findings_keeps_every_restored_wait() {
    // Apply never reads child Flows, so a running child stands in for a gone one here.
    for state in [ChildState::Running, ChildState::EndedUnreceived] {
        let mut harness = Harness::new();
        let target = harness.prepare_child(state);
        harness.now_micros += 1000;
        let command = FlowCommand {
            flow_id: "parent".to_owned(),
            run_id: PARENT_RUN.to_vec(),
            epoch: 1,
            behavior_version: 1,
            proposed_at_micros: harness.now_micros,
            command_id: [6; 16].to_vec(),
            command: Some(Command::TimeTravel(TimeTravelCommand {
                request: Some(ResetFlowRequest {
                    flow_id: "parent".to_owned(),
                    reset_type: FlowResetType::HistoryEventTime as i32,
                    history_event_time: (i128::from(target) * 1000).to_string(),
                    run_attempt: 1,
                    ..ResetFlowRequest::default()
                }),
                ..TimeTravelCommand::default()
            })),
            ..FlowCommand::default()
        };
        let context = harness.parent_context();
        let staged = stage_time_travel_reading_child_flows_for_test(
            &context,
            &harness.state,
            &harness.hasher,
            &command,
        )
        .expect("the owner computes")
        .expect("the owner stages the result");
        // An older owner staged the same rows without findings or the marker.
        let mut commands = staged.commands;
        for staged_command in &mut commands {
            if let Some(Command::TimeTravelStage(stage)) = staged_command.command.as_mut() {
                let mut chunk = dexzd_protocol::dexzd::v1::TimeTravelStagedChunk::decode(
                    stage.chunk.as_slice(),
                )
                .expect("the chunk decodes");
                chunk.child_flows.clear();
                if let Some(target) = chunk.target.as_mut() {
                    target.has_child_flow_findings = false;
                }
                stage.chunk = chunk.encode_to_vec();
            }
        }
        let mut last = None;
        for staged_command in commands {
            last = Some(harness.apply_command(context, staged_command));
        }
        let travel = last.expect("the owner proposes the TimeTravel");
        assert!(
            matches!(
                travel.reply.reply,
                Some(command_reply::Reply::TimeTravel(_))
            ),
            "{state:?}: {:?}",
            travel.reply
        );
        let wait = harness
            .step("parent", PARENT_RUN, "StartStep-1")
            .wait
            .expect("the restored Step waits");
        assert!(wait.sub_flow_registration_attempts.is_empty());
        assert!(harness.parent_sub_flow_result_of(CHILD_ID, 1).is_none());
        assert!(
            harness
                .parent_outbox_written_at(harness.last_parent_seq())
                .is_empty()
        );
    }
}

#[test]
fn an_unknown_policy_is_rejected() {
    let mut harness = Harness::new();
    let target = harness.prepare_child(ChildState::Running);
    let rows_before = harness.state.rows.clone();
    let travel = harness.travel_with_policy(target, 99);
    let error = error_of(&travel).expect("the travel is rejected");
    assert_eq!(error.grpc_code, 3, "INVALID_ARGUMENT: {}", error.message);
    assert_eq!(harness.state.rows, rows_before);
}

fn assert_applied(outcome: &ApplyOutcome) {
    assert!(
        matches!(
            outcome.reply.reply,
            Some(command_reply::Reply::TimeTravel(_))
        ),
        "{:?}",
        outcome.reply
    );
}

/// Returns a parent command proposed at a new time of `harness`.
fn parent_command(harness: &mut Harness, command_id: u8, body: Command) -> FlowCommand {
    harness.now_micros += 1000;
    FlowCommand {
        flow_id: "parent".to_owned(),
        run_id: PARENT_RUN.to_vec(),
        epoch: 1,
        behavior_version: 1,
        proposed_at_micros: harness.now_micros,
        command_id: [command_id; 16].to_vec(),
        command: Some(body),
        ..FlowCommand::default()
    }
}

fn travel_body(target_micros: i64, run_attempt: i32, policy: SubFlowResetPolicy) -> Command {
    Command::TimeTravel(TimeTravelCommand {
        request: Some(ResetFlowRequest {
            flow_id: "parent".to_owned(),
            reset_type: FlowResetType::HistoryEventTime as i32,
            history_event_time: (i128::from(target_micros) * 1000).to_string(),
            run_attempt,
            sub_flow_reset_policy: policy as i32,
            ..ResetFlowRequest::default()
        }),
        ..TimeTravelCommand::default()
    })
}

#[test]
fn fail_and_restart_reject_a_child_start_that_has_not_reached_the_child() {
    for policy in [
        SubFlowResetPolicy::AlwaysFail,
        SubFlowResetPolicy::AlwaysRestart,
    ] {
        let mut harness = Harness::new();
        let target = harness.prepare_child(ChildState::StartPending);
        let start = harness
            .outbox("parent", PARENT_RUN)
            .pop()
            .expect("the start is pending");
        let rows_before = harness.state.rows.clone();
        let travel = harness.travel_with_policy(target, policy as i32);
        let error = error_of(&travel).expect("the travel is rejected");
        assert_eq!(error.grpc_code, 9, "{policy:?}: {}", error.message);
        assert!(
            error
                .message
                .contains("the child start has not reached the child yet")
                && error.message.contains("retry the time travel later"),
            "{policy:?}: {}",
            error.message
        );
        assert_eq!(harness.state.rows, rows_before, "{policy:?}");

        // Once the start reached the child, the same travel abandons a watched child.
        assert_ok(&harness.deliver_start_outbox(&start, FIRST_CHILD_RUN));
        harness.ack_parent_outbox(&start.message_id);
        assert_applied(&harness.travel_with_policy(target, policy as i32));
        let abandoning = harness
            .parent_outbox_written_at(harness.last_parent_seq())
            .pop()
            .expect("the travel writes a message to the child");
        assert_ok(&harness.deliver_to_child(&abandoning, SECOND_CHILD_RUN));
        assert_eq!(
            harness.record(CHILD_ID, FIRST_CHILD_RUN).status,
            FlowStatus::Terminated as i32,
            "{policy:?}"
        );
    }
}

#[test]
fn attach_else_fail_attaches_a_child_start_that_has_not_reached_the_child() {
    let mut harness = Harness::new();
    let target = harness.prepare_child(ChildState::StartPending);
    let start = harness
        .outbox("parent", PARENT_RUN)
        .pop()
        .expect("the start is pending");
    let travel = harness.travel_with_policy(target, SubFlowResetPolicy::AttachElseFail as i32);
    assert_applied(&travel);
    assert!(harness.parent_sub_flow_result_of(CHILD_ID, 2).is_none());
    // The start arrives after the travel; its child reports to the restored condition.
    assert_ok(&harness.deliver_start_outbox(&start, FIRST_CHILD_RUN));
    harness.publish_signal();
    harness.terminate_child(CHILD_ID, FIRST_CHILD_RUN);
    let report = harness.latest_child_report(CHILD_ID, FIRST_CHILD_RUN);
    assert_ok(&harness.deliver_report(CHILD_ID, &report));
    let step = harness.step("parent", PARENT_RUN, "StartStep-1");
    assert_eq!(step.phase, StepExecutionPhase::Execute as i32);
    assert_eq!(
        step.condition_results.expect("results").sub_flow_results[0].flow_status,
        FlowStatus::Terminated as i32
    );
}

#[test]
fn apply_aborts_an_abandoning_policy_when_it_finds_the_child_start_pending() {
    let mut harness = Harness::new();
    let target = harness.prepare_child(ChildState::Running);
    let start = harness
        .outbox("parent", PARENT_RUN)
        .into_iter()
        .find(|record| record.written_seq > 0)
        .expect("the child start");
    let command = parent_command(
        &mut harness,
        7,
        travel_body(target, 1, SubFlowResetPolicy::AlwaysFail),
    );
    let context = harness.parent_context();
    let staged = stage_time_travel_reading_child_flows_for_test(
        &context,
        &harness.state,
        &harness.hasher,
        &command,
    )
    .expect("the owner computes")
    .expect("the owner finds the start delivered");
    let mut diverged = harness.snapshot();
    diverged.state.rows.insert(
        (
            ColumnFamily::PartitionIndex,
            harness.parent_pending_index(&start.message_id),
        ),
        Vec::new(),
    );
    let mut last = None;
    for staged_command in staged.commands {
        let outcome =
            apply_flow_command(&context, &diverged.state, &staged_command).expect("apply runs");
        diverged.state.commit(&outcome);
        last = Some(outcome);
    }
    let error = error_of(last.as_ref().expect("the owner proposes the TimeTravel"))
        .expect("apply rejects the travel")
        .clone();
    assert_eq!(error.grpc_code, 10, "ABORTED: {}", error.message);
    assert!(error.message.contains("has not reached the child yet"));
}

#[test]
fn child_reads_that_exceed_the_memory_budget_reject_the_time_travel() {
    let mut harness = Harness::new();
    harness.start_parent();
    let invocation = StepInvocationKey {
        step_execution_id: "StartStep-1".to_owned(),
        method: StepMethod::WaitFor as i32,
    };
    assert_ok(&harness.apply(
        "parent",
        &PARENT_RUN,
        Command::StepAttemptStarted(StepAttemptStartedCommand {
            invocation: Some(invocation.clone()),
            attempt: 1,
            task_id: vec![9; 16],
            pull_routing: None,
        }),
    ));
    let large_input = "x".repeat(600_000);
    assert_ok(&harness.apply(
        "parent",
        &PARENT_RUN,
        Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(invocation),
            attempt: 1,
            response: Some(step_method_completed_command::Response::WaitFor(
                InvokeWaitForMethodResponse {
                    waiting_condition: Some(WaitingCondition {
                        waiting_condition_type: WaitingConditionType::AllCompleted as i32,
                        sub_flow_conditions: vec![SubFlowCondition {
                            step_input: Some(string_value(&large_input)),
                            ..sub_flow_condition("child")
                        }],
                        ..WaitingCondition::default()
                    }),
                    ..InvokeWaitForMethodResponse::default()
                },
            )),
            ..StepMethodCompletedCommand::default()
        }),
    ));
    let target = harness.parent_history_time(harness.last_parent_seq());
    let start = harness
        .outbox("parent", PARENT_RUN)
        .pop()
        .expect("the child start");
    assert_ok(&harness.deliver_start_outbox(&start, FIRST_CHILD_RUN));
    harness.ack_parent_outbox(&start.message_id);

    let context = harness.parent_context();
    let child_hash = harness.hasher.flow_hash(TenantId::DEFAULT, CHILD_ID);
    let child_history_prefix = run_prefix(child_hash, RunId(FIRST_CHILD_RUN));
    // Returns the computation and whether it read any child history record.
    let compute = |policy: SubFlowResetPolicy, budget_bytes: u64| {
        let travel = travel_body(target, 1, policy);
        let Command::TimeTravel(time_travel) = &travel else {
            unreachable!("travel_body builds a TimeTravel");
        };
        let reset = time_travel
            .request
            .clone()
            .expect("the travel has a request");
        let history = StateHistorySource::new(&harness.state, context, RunId(PARENT_RUN));
        let child_state = ReadRecordingState::new(&harness.state);
        let child_flow_runs = StateChildFlowRuns::new(&child_state, &harness.hasher);
        let computation = compute_time_travel(
            &context,
            &TimeTravelComputationSources {
                state: &harness.state,
                history: &history,
                offloaded_values: &NoOffloadedValues,
                child_flow_runs: &child_flow_runs,
            },
            &TimeTravelComputationRequest {
                run_id: RunId(PARENT_RUN),
                reset: &reset,
                now_micros: harness.now_micros + 1000,
                replay_memory_budget_bytes: budget_bytes,
            },
        )
        .expect("the computation runs");
        let has_read_child_history =
            child_state.has_read(ColumnFamily::History, &child_history_prefix);
        (computation, has_read_child_history)
    };
    let budget_bytes = 300_000;
    assert!(
        matches!(
            compute(SubFlowResetPolicy::AlwaysFail, budget_bytes).0,
            TimeTravelComputation::Computed(_)
        ),
        "the target state fits the budget without child reads"
    );
    let (TimeTravelComputation::Rejected(reply), has_read_child_history) =
        compute(SubFlowResetPolicy::AlwaysRestart, budget_bytes)
    else {
        panic!("reading the child's large start exceeds the budget");
    };
    assert!(
        !has_read_child_history,
        "the budget refuses the child's genesis record before it is read"
    );
    let Some(command_reply::Reply::Error(error)) = reply.reply else {
        panic!("expected a rejection");
    };
    assert_eq!(error.grpc_code, 8, "RESOURCE_EXHAUSTED: {}", error.message);
    assert!(error.message.contains("child Flow"), "{}", error.message);
    let (computation, has_read_child_history) =
        compute(SubFlowResetPolicy::AlwaysRestart, 64 << 20);
    assert!(matches!(computation, TimeTravelComputation::Computed(_)));
    assert!(
        has_read_child_history,
        "a large budget reads the child's start"
    );
}

/// Records the column family and key of every read of `state`; a scan records its start.
struct ReadRecordingState<'state> {
    state: &'state MemoryState,
    reads: RefCell<Vec<(ColumnFamily, Vec<u8>)>>,
}

impl<'state> ReadRecordingState<'state> {
    fn new(state: &'state MemoryState) -> Self {
        Self {
            state,
            reads: RefCell::new(Vec::new()),
        }
    }

    fn has_read(&self, column_family: ColumnFamily, prefix: &[u8]) -> bool {
        self.reads
            .borrow()
            .iter()
            .any(|(read_family, key)| *read_family == column_family && key.starts_with(prefix))
    }
}

impl FlowStateReader for ReadRecordingState<'_> {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        self.reads.borrow_mut().push((column_family, key.to_vec()));
        self.state.get(column_family, key)
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        self.reads
            .borrow_mut()
            .push((column_family, start.to_vec()));
        self.state.scan(column_family, start, end, limit)
    }
}

/// A child that ended with an output larger than the inline threshold, as a table value.
fn harness_with_a_child_that_ended_with_a_large_output() -> Harness {
    let mut harness = Harness::new();
    harness.prepare_child(ChildState::Running);
    let execute = StepInvocationKey {
        step_execution_id: "ChildStep-1".to_owned(),
        method: StepMethod::Execute as i32,
    };
    assert_ok(&harness.apply(
        CHILD_ID,
        &FIRST_CHILD_RUN,
        Command::StepAttemptStarted(StepAttemptStartedCommand {
            invocation: Some(execute.clone()),
            attempt: 1,
            task_id: vec![8; 16],
            pull_routing: None,
        }),
    ));
    assert_ok(&harness.apply(
        CHILD_ID,
        &FIRST_CHILD_RUN,
        Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(execute),
            attempt: 1,
            response: Some(step_method_completed_command::Response::Execute(
                InvokeExecuteMethodResponse {
                    step_decision: Some(StepDecision {
                        close_decision: Some(CloseDecision {
                            close_decision_type: CloseDecisionType::ForceComplete as i32,
                            close_input: Some(string_value(&"o".repeat(2000))),
                            ..CloseDecision::default()
                        }),
                        ..StepDecision::default()
                    }),
                    ..InvokeExecuteMethodResponse::default()
                },
            )),
            ..StepMethodCompletedCommand::default()
        }),
    ));
    assert_ne!(
        harness.record(CHILD_ID, FIRST_CHILD_RUN).status,
        FlowStatus::Running as i32
    );
    harness
}

#[test]
fn child_reads_reserve_the_run_record_and_outputs_before_loading_them() {
    let harness = harness_with_a_child_that_ended_with_a_large_output();
    let parent = parent_reference();
    let start_request_id = format!("{}:1:StartStep-1:0", RunId(PARENT_RUN));
    let query = ChildFlowRunQuery {
        tenant_id: TenantId::DEFAULT,
        child_flow_id: CHILD_ID,
        start_request_id: &start_request_id,
        parent: &parent,
        should_read_start: false,
    };
    let child_hash = harness.hasher.flow_hash(TenantId::DEFAULT, CHILD_ID);
    let read = |state: &ReadRecordingState<'_>, refuse_after: usize| {
        let reservations = Cell::new(0);
        StateChildFlowRuns::new(state, &harness.hasher)
            .child_flow_run(&query, &|_bytes| {
                reservations.set(reservations.get() + 1);
                reservations.get() <= refuse_after
            })
            .expect("the read runs")
    };

    // A refused run record reservation stops before decoding: a corrupt record does not fail.
    let mut corrupt = MemoryState {
        rows: harness.state.rows.clone(),
    };
    corrupt.rows.insert(
        (
            ColumnFamily::Flow,
            run_singleton_key(child_hash, RunId(FIRST_CHILD_RUN), RunTable::RunRecord),
        ),
        vec![0xFF; 16],
    );
    let corrupt_reads = ReadRecordingState::new(&corrupt);
    assert_eq!(read(&corrupt_reads, 0), ChildFlowRead::MemoryExceeded);

    // A refused output reservation stops before any value row loads.
    let refused_reads = ReadRecordingState::new(&harness.state);
    assert_eq!(read(&refused_reads, 1), ChildFlowRead::MemoryExceeded);
    assert!(!refused_reads.has_read(ColumnFamily::Value, &[]));

    let granted_reads = ReadRecordingState::new(&harness.state);
    let ChildFlowRead::Found(run) = read(&granted_reads, usize::MAX) else {
        panic!("a granted read finds the child");
    };
    assert!(run.ended_result.is_some());
    assert!(
        granted_reads.has_read(ColumnFamily::Value, &[]),
        "a granted read loads the output value"
    );
}

#[test]
fn one_wait_uses_an_ended_child_and_fails_a_gone_one() {
    let mut harness = Harness::new();
    harness.start_parent();
    let starts = harness.register_children_and_signal_wait(&["child", "second"]);
    let target = harness.parent_history_time(harness.last_parent_seq());
    let first_start = starts
        .iter()
        .find(|start| matches!(&start.payload, Some(outbox_record::Payload::ChildStart(start)) if start.child_flow_id == CHILD_ID))
        .expect("the first child start");
    assert_ok(&harness.deliver_to_child(first_start, FIRST_CHILD_RUN));
    for start in &starts {
        harness.ack_parent_outbox(&start.message_id);
    }
    harness.terminate_child(CHILD_ID, FIRST_CHILD_RUN);

    let travel = harness.travel_with_policy(target, SubFlowResetPolicy::AttachElseFail as i32);
    assert_applied(&travel);
    let wait = harness
        .step("parent", PARENT_RUN, "StartStep-1")
        .wait
        .expect("the signal keeps the Step waiting");
    assert_eq!(wait.sub_flow_registration_attempts, vec![1, 2]);
    assert!(
        harness
            .parent_outbox_written_at(harness.last_parent_seq())
            .is_empty(),
        "a gone child needs no termination"
    );
    harness.publish_signal();
    let step = harness.step("parent", PARENT_RUN, "StartStep-1");
    assert_eq!(step.phase, StepExecutionPhase::Execute as i32);
    let results = step.condition_results.expect("results").sub_flow_results;
    assert_eq!(results[0].flow_status, FlowStatus::Terminated as i32);
    assert_eq!(results[1].flow_status, FlowStatus::Failed as i32);
    assert_eq!(
        results[1].error_type,
        FlowErrorType::SubFlowAbandoned as i32
    );
}

impl Harness {
    /// Makes StartStep-1 move on to LeftStep and RightStep, which each wait on one child and the
    /// signal. Returns the time of the point where both wait.
    fn wait_in_two_steps(&mut self) -> i64 {
        self.start_parent();
        let wait_for = StepInvocationKey {
            step_execution_id: "StartStep-1".to_owned(),
            method: StepMethod::WaitFor as i32,
        };
        let execute = StepInvocationKey {
            step_execution_id: "StartStep-1".to_owned(),
            method: StepMethod::Execute as i32,
        };
        for (invocation, response) in [
            (
                wait_for,
                step_method_completed_command::Response::WaitFor(
                    InvokeWaitForMethodResponse::default(),
                ),
            ),
            (
                execute,
                step_method_completed_command::Response::Execute(InvokeExecuteMethodResponse {
                    step_decision: Some(StepDecision {
                        next_steps: ["LeftStep", "RightStep"]
                            .into_iter()
                            .map(|step_type| StepMovement {
                                step_type: step_type.to_owned(),
                                ..StepMovement::default()
                            })
                            .collect(),
                        ..StepDecision::default()
                    }),
                    ..InvokeExecuteMethodResponse::default()
                }),
            ),
        ] {
            assert_ok(&self.apply(
                "parent",
                &PARENT_RUN,
                Command::StepAttemptStarted(StepAttemptStartedCommand {
                    invocation: Some(invocation.clone()),
                    attempt: 1,
                    task_id: vec![9; 16],
                    pull_routing: None,
                }),
            ));
            assert_ok(&self.apply(
                "parent",
                &PARENT_RUN,
                Command::StepMethodCompleted(StepMethodCompletedCommand {
                    invocation: Some(invocation),
                    attempt: 1,
                    response: Some(response),
                    ..StepMethodCompletedCommand::default()
                }),
            ));
        }
        let mut wait_time = 0;
        for (step_execution_id, run_id) in [
            ("LeftStep-1", FIRST_CHILD_RUN),
            ("RightStep-1", SECOND_CHILD_RUN),
        ] {
            let start = self
                .register_step_children_and_signal_wait(step_execution_id, &["child"])
                .into_iter()
                .find(|record| matches!(&record.payload, Some(outbox_record::Payload::ChildStart(start)) if start.parent.as_ref().is_some_and(|parent| parent.step_execution_id == step_execution_id)))
                .expect("the Step's child start");
            wait_time = self.parent_history_time(self.last_parent_seq());
            assert_ok(&self.deliver_to_child(&start, run_id));
            self.ack_parent_outbox(&start.message_id);
        }
        wait_time
    }
}

#[test]
fn one_time_travel_resets_the_waits_of_several_steps() {
    let mut harness = Harness::new();
    let target = harness.wait_in_two_steps();
    let rows_before = harness.state.rows.clone();
    let rejected = harness.travel_with_policy(target, SubFlowResetPolicy::AlwaysReject as i32);
    let error = error_of(&rejected).expect("ALWAYS_REJECT rejects");
    for named in [
        "LeftStep-1",
        "RightStep-1",
        "SubFlow:parent-LeftStep-1-0",
        "SubFlow:parent-RightStep-1-0",
    ] {
        assert!(error.message.contains(named), "{}", error.message);
    }
    assert_eq!(harness.state.rows, rows_before);

    assert_applied(&harness.travel_with_policy(target, SubFlowResetPolicy::AlwaysFail as i32));
    let terminations = harness.parent_outbox_written_at(harness.last_parent_seq());
    assert_eq!(terminations.len(), 2);
    assert_ne!(terminations[0].message_id, terminations[1].message_id);
    for (step_execution_id, child_run) in [
        ("LeftStep-1", FIRST_CHILD_RUN),
        ("RightStep-1", SECOND_CHILD_RUN),
    ] {
        let wait = harness
            .step("parent", PARENT_RUN, step_execution_id)
            .wait
            .expect("the Step waits for the signal");
        assert_eq!(
            wait.sub_flow_registration_attempts,
            vec![2],
            "{step_execution_id}"
        );
        let child_flow_id = format!("SubFlow:parent-{step_execution_id}-0");
        let termination = terminations
            .iter()
            .find(|record| matches!(&record.payload, Some(outbox_record::Payload::ChildTerminate(terminate)) if terminate.child_flow_id == child_flow_id))
            .expect("the child's termination");
        assert_ok(&harness.deliver_to_child(termination, child_run));
        assert_eq!(
            harness.record(&child_flow_id, child_run).status,
            FlowStatus::Terminated as i32
        );
    }
}

#[test]
fn a_second_time_travel_attaches_the_restarted_child_and_ignores_the_first() {
    let mut harness = Harness::new();
    let target = harness.prepare_child(ChildState::Running);
    assert_applied(&harness.travel_with_policy(target, SubFlowResetPolicy::AlwaysRestart as i32));
    let restart_seq = harness.last_parent_seq();
    let restart_time = harness.parent_history_time(restart_seq);
    let restart = harness
        .parent_outbox_written_at(restart_seq)
        .pop()
        .expect("the restart");
    assert_ok(&harness.deliver_to_child(&restart, SECOND_CHILD_RUN));
    harness.ack_parent_outbox(&restart.message_id);
    assert_eq!(
        harness.record(CHILD_ID, FIRST_CHILD_RUN).status,
        FlowStatus::Terminated as i32
    );

    // Travels back to the restart point of attempt 2; the restarted child still runs.
    let command = parent_command(
        &mut harness,
        8,
        travel_body(restart_time, 2, SubFlowResetPolicy::Unspecified),
    );
    let again = harness.apply_command(harness.parent_context(), command);
    assert_applied(&again);
    assert_eq!(harness.record("parent", PARENT_RUN).current_attempt, 3);
    assert!(
        harness
            .parent_outbox_written_at(harness.last_parent_seq())
            .is_empty(),
        "the restarted child stays attached"
    );
    let wait = harness
        .step("parent", PARENT_RUN, "StartStep-1")
        .wait
        .expect("the Step waits");
    assert_eq!(wait.sub_flow_registration_attempts, vec![2]);

    harness.publish_signal();
    let first_report = harness.latest_child_report(CHILD_ID, FIRST_CHILD_RUN);
    assert_ok(&harness.deliver_report(CHILD_ID, &first_report));
    assert_eq!(
        harness.step("parent", PARENT_RUN, "StartStep-1").phase,
        StepExecutionPhase::Waiting as i32,
        "the first child's late report does not complete the condition"
    );
    harness.terminate_child(CHILD_ID, SECOND_CHILD_RUN);
    let second_report = harness.latest_child_report(CHILD_ID, SECOND_CHILD_RUN);
    assert_ok(&harness.deliver_report(CHILD_ID, &second_report));
    assert_eq!(
        harness.step("parent", PARENT_RUN, "StartStep-1").phase,
        StepExecutionPhase::Execute as i32
    );
    assert_eq!(
        harness
            .parent_sub_flow_result_of(CHILD_ID, 2)
            .expect("the restarted child's result")
            .child_run_id,
        SECOND_CHILD_RUN.to_vec()
    );
}

#[test]
fn a_child_whose_start_wrote_hard_delete_attributes_cannot_restart() {
    let mut harness = Harness::new();
    harness.start_parent();
    let reference = HardDeleteObjectReference {
        object_id: "staged-secret".to_owned(),
        write_index: 0,
        size_bytes: 19,
        is_string_value: true,
    };
    assert_ok(&harness.submit_sub_flow_wait_with_objects(
        SubFlowCondition {
            sub_flow_type: "ChildFlow".to_owned(),
            start_step_type: "ChildStep".to_owned(),
            options: Some(SubFlowOptions {
                attributes: vec![AttributeWrite {
                    key: "secret".to_owned(),
                    value: Some(Value { kind: None }),
                    is_hard_delete_on_null: true,
                    ..AttributeWrite::default()
                }],
                ..SubFlowOptions::default()
            }),
            ..SubFlowCondition::default()
        },
        vec![reference],
    ));
    let target = harness.parent_history_time(harness.last_parent_seq());
    let message = harness.outbox("parent", PARENT_RUN).remove(0);
    let Some(outbox_record::Payload::ChildStart(mut start)) = message.payload.clone() else {
        panic!("child start");
    };
    start.hard_delete_objects[0].object_id = "child-secret".to_owned();
    let source = start
        .parent
        .as_ref()
        .and_then(|parent| parent.parent.clone());
    assert_ok(&harness.apply(
        CHILD_ID,
        &[],
        Command::InboxMessage(InboxMessageCommand {
            message_id: message.message_id.clone(),
            source,
            payload: Some(inbox_message_command::Payload::ChildStart(start)),
            new_run_id: FIRST_CHILD_RUN.to_vec(),
            settings: Some(settings()),
        }),
    ));
    harness.ack_parent_outbox(&message.message_id);
    let rows_before = harness.state.rows.clone();
    let travel = harness.travel_with_policy(target, SubFlowResetPolicy::AlwaysRestart as i32);
    let error = error_of(&travel).expect("the restart is rejected");
    assert_eq!(error.grpc_code, 9, "{}", error.message);
    assert!(
        error
            .message
            .contains("its start wrote hard-delete Attributes, which a restart cannot repeat"),
        "{}",
        error.message
    );
    assert_eq!(harness.state.rows, rows_before);
}
