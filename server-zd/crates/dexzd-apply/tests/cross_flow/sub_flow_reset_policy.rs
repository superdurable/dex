// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! SubFlow reset policies of a time travel, for each state of a restored wait's child.

use super::*;
use dexzd_protocol::dex::{
    ChannelCondition, ChannelMessage, PublishToChannelRequest, SubFlowResetPolicy,
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
}

const CHILD_STATES: [ChildState; 5] = [
    ChildState::ReceivedBeforeTarget,
    ChildState::ReceivedAfterTarget,
    ChildState::Running,
    ChildState::EndedUnreceived,
    ChildState::Gone,
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
    use ChildState::{EndedUnreceived, Gone, ReceivedAfterTarget, ReceivedBeforeTarget, Running};
    use SubFlowResetPolicy as Policy;
    match (policy, state) {
        (Policy::AlwaysReject, _) | (Policy::AlwaysRestart, Gone) => ResetOutcome::Rejected,
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
        (_, Running) => ResetOutcome::KeepsWaiting,
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
        let invocation = StepInvocationKey {
            step_execution_id: "StartStep-1".to_owned(),
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
        // A gone child emulates a purge: its start was delivered, and its run no longer exists.
        if state != ChildState::Gone {
            assert_ok(&self.deliver_start_outbox(&start, FIRST_CHILD_RUN));
        }
        self.ack_parent_outbox(&start.message_id);
        match state {
            ChildState::Running | ChildState::Gone => wait_time,
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
fn an_unknown_policy_is_rejected() {
    let mut harness = Harness::new();
    let target = harness.prepare_child(ChildState::Running);
    let rows_before = harness.state.rows.clone();
    let travel = harness.travel_with_policy(target, 99);
    let error = error_of(&travel).expect("the travel is rejected");
    assert_eq!(error.grpc_code, 3, "INVALID_ARGUMENT: {}", error.message);
    assert_eq!(harness.state.rows, rows_before);
}
