// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;
use dexzd_protocol::dex::{ChannelMessage, DeleteChannelMessageRequest, PublishToChannelRequest};
use dexzd_protocol::dexzd::v1::{
    ChannelMessageDeleteCommand, ChannelPublishCommand, MethodSnapshotRecord,
};

#[derive(Clone, Copy, Debug)]
enum RetryMethod {
    TimeoutHandler,
    WaitFor,
    Execute,
}

struct RetrySnapshotHarness {
    harness: Harness,
    step_execution_id: &'static str,
    method: StepMethod,
    first_snapshot: MethodSnapshotRecord,
    attribute_value_id: ValueId,
    channel_value_id: ValueId,
}

impl RetrySnapshotHarness {
    fn new(retry_method: RetryMethod) -> Self {
        Self::with_settings(retry_method, settings())
    }

    fn with_settings(retry_method: RetryMethod, run_settings: RunSettings) -> Self {
        let mut harness = Harness::new("retry-snapshot");
        let mut request = start_request("start-retry-snapshot");
        request.flow_id = harness.flow_id.clone();
        let retry_policy = Some(RetryPolicy {
            maximum_attempts: 3,
            ..RetryPolicy::default()
        });
        let attribute_map_instances = vec!["settings/".to_owned()];
        let channel_names = vec!["commands".to_owned()];
        let channel_map_instances = vec!["messages/".to_owned(), "exact/one".to_owned()];
        request.flow_timeout_seconds = 30;
        request.flow_start_options = Some(FlowStartOptions {
            timeout_handler_options: Some(FlowTimeoutHandlerOptions {
                retry_policy,
                load_attribute_map_instances: attribute_map_instances.clone(),
                load_channel_names: channel_names.clone(),
                load_channel_map_instances: channel_map_instances.clone(),
                ..FlowTimeoutHandlerOptions::default()
            }),
            ..FlowStartOptions::default()
        });
        request.step_options = Some(StepOptions {
            wait_for_retry_policy: retry_policy,
            execute_retry_policy: retry_policy,
            wait_for_load_attribute_map_instances: attribute_map_instances.clone(),
            execute_load_attribute_map_instances: attribute_map_instances,
            wait_for_load_channel_names: channel_names.clone(),
            execute_load_channel_names: channel_names,
            wait_for_load_channel_map_instances: channel_map_instances.clone(),
            execute_load_channel_map_instances: channel_map_instances,
            skip_wait_for: matches!(retry_method, RetryMethod::Execute),
            ..StepOptions::default()
        });
        harness.start_with_settings_and_timeout_policy(
            request,
            run_settings,
            FlowTimeoutPolicy::Handler,
        );
        harness.set_attribute("report", string_value(&"a".repeat(500)), "initial-report");
        harness.set_attribute("settings/one", string_value("first"), "initial-setting");
        harness.set_attribute("excluded/one", string_value("excluded"), "excluded-setting");
        publish(&mut harness, "commands", "first-command", &"c".repeat(500));
        publish(&mut harness, "messages/one", "first-map-message", "first");
        publish(&mut harness, "exact/one", "first-exact-message", "first");
        publish(&mut harness, "unloaded", "first-unloaded-message", "first");
        let (step_execution_id, method) = match retry_method {
            RetryMethod::TimeoutHandler => {
                harness.apply(Command::TimersFired(TimersFiredCommand {
                    timers: vec![TimerId {
                        run_attempt: 1,
                        kind: Some(timer_id::Kind::FlowTimeout(FlowTimeoutTimerId {})),
                    }],
                }));
                ("sys:timeout_handler-1", StepMethod::Execute)
            }
            RetryMethod::WaitFor => ("ChargeStep-1", StepMethod::WaitFor),
            RetryMethod::Execute => ("ChargeStep-1", StepMethod::Execute),
        };
        harness.start_attempt(step_execution_id, method, 1);
        let first_snapshot = harness
            .step_execution(step_execution_id)
            .invocation
            .expect("the method has an invocation")
            .snapshot
            .expect("a running method has its snapshot");
        assert_eq!(first_snapshot.attributes.len(), 2);
        assert_eq!(first_snapshot.loaded_channel_messages.len(), 3);
        assert_eq!(first_snapshot.channel_infos["commands"].size, 1);
        let attribute_value_id = table_value_id(
            first_snapshot
                .attributes
                .iter()
                .find(|attribute| attribute.key == "report")
                .expect("the report Attribute was selected")
                .value
                .as_ref(),
        );
        let channel_value_id = table_value_id(
            first_snapshot
                .loaded_channel_messages
                .iter()
                .find(|channel| channel.channel_name == "commands")
                .expect("the commands Channel was selected")
                .messages[0]
                .value
                .as_ref(),
        );
        Self {
            harness,
            step_execution_id,
            method,
            first_snapshot,
            attribute_value_id,
            channel_value_id,
        }
    }

    fn mutate_state(&mut self) {
        self.harness
            .set_attribute("report", string_value(&"b".repeat(500)), "late-report");
        self.harness
            .set_attribute("settings/one", string_value("late"), "late-setting");
        self.harness
            .set_attribute("settings/two", string_value("new"), "new-setting");
        publish(&mut self.harness, "commands", "late-command", "late");
        publish(
            &mut self.harness,
            "messages/one",
            "late-map-message",
            "late",
        );
        publish(&mut self.harness, "messages/two", "new-map-instance", "new");
        publish(&mut self.harness, "exact/one", "late-exact-message", "late");
        publish(
            &mut self.harness,
            "unloaded",
            "late-unloaded-message",
            "late",
        );
        let outcome =
            self.harness
                .apply(Command::ChannelMessageDelete(ChannelMessageDeleteCommand {
                    request: Some(DeleteChannelMessageRequest {
                        channel_name: "commands".to_owned(),
                        message_id: "first-command".to_owned(),
                        request_id: "delete-first-command".to_owned(),
                        ..DeleteChannelMessageRequest::default()
                    }),
                    request_fingerprint: b"delete-first-command".to_vec(),
                }));
        assert!(!matches!(
            outcome.reply.reply,
            Some(command_reply::Reply::Error(_))
        ));
    }

    fn fail(&mut self, attempt: u32) {
        self.harness
            .apply(Command::StepAttemptFailed(StepAttemptFailedCommand {
                invocation: Some(key(self.step_execution_id, self.method)),
                attempt,
                kind: StepAttemptFailureKind::WorkerError as i32,
                worker_error: Some(WorkerErrorResponse {
                    detail: "retry snapshot".to_owned(),
                    ..WorkerErrorResponse::default()
                }),
                ..StepAttemptFailedCommand::default()
            }));
    }

    fn fire_retry(&mut self, next_attempt: u32) {
        self.harness.now_micros += 60 * SECOND;
        self.harness.apply(Command::TimersFired(TimersFiredCommand {
            timers: vec![TimerId {
                run_attempt: self.harness.run_record().current_attempt,
                kind: Some(timer_id::Kind::RetryBackoff(RetryBackoffTimerId {
                    invocation: Some(key(self.step_execution_id, self.method)),
                    next_attempt,
                })),
            }],
        }));
    }

    fn restart_from_persisted_rows(&mut self) {
        let mut restarted = Harness::new(&self.harness.flow_id);
        restarted.state.rows = std::mem::take(&mut self.harness.state.rows);
        restarted.run_id = self.harness.run_id.clone();
        restarted.now_micros = self.harness.now_micros;
        self.harness = restarted;
    }

    fn assert_snapshot(&self, phase: StepInvocationPhase) {
        let invocation = self
            .harness
            .step_execution(self.step_execution_id)
            .invocation
            .expect("the method has an invocation");
        assert_eq!(invocation.phase, phase as i32);
        assert_eq!(invocation.snapshot.as_ref(), Some(&self.first_snapshot));
        assert_eq!(self.harness.holder_count(self.attribute_value_id), 1);
        assert_eq!(self.harness.holder_count(self.channel_value_id), 1);
    }

    fn assert_snapshot_released(&self) {
        assert_eq!(self.harness.holder_count(self.attribute_value_id), 0);
        assert_eq!(self.harness.holder_count(self.channel_value_id), 0);
    }
}

#[test]
fn timeout_handler_and_step_retries_keep_their_snapshot_across_restart() {
    for retry_method in [
        RetryMethod::TimeoutHandler,
        RetryMethod::WaitFor,
        RetryMethod::Execute,
    ] {
        let mut fixture = RetrySnapshotHarness::new(retry_method);
        fixture.mutate_state();
        for attempt in 1..3 {
            fixture.fail(attempt);
            fixture.assert_snapshot(StepInvocationPhase::Backoff);
            fixture.restart_from_persisted_rows();
            fixture.fire_retry(attempt + 1);
            fixture.assert_snapshot(StepInvocationPhase::Pending);
            fixture.restart_from_persisted_rows();
            fixture
                .harness
                .start_attempt(fixture.step_execution_id, fixture.method, attempt + 1);
            fixture.assert_snapshot(StepInvocationPhase::Running);
        }
        match fixture.method {
            StepMethod::WaitFor => {
                fixture
                    .harness
                    .complete_wait_for(fixture.step_execution_id, 3);
                fixture.assert_snapshot_released();
                fixture
                    .harness
                    .start_attempt(fixture.step_execution_id, StepMethod::Execute, 1);
                let execute_snapshot = fixture
                    .harness
                    .step_execution(fixture.step_execution_id)
                    .invocation
                    .expect("Execute has an invocation")
                    .snapshot
                    .expect("Execute loaded its fresh snapshot");
                assert_ne!(execute_snapshot, fixture.first_snapshot);
                assert_eq!(execute_snapshot.attributes.len(), 3);
                assert_eq!(execute_snapshot.channel_infos["unloaded"].size, 2);
            }
            StepMethod::Execute => {
                fixture.harness.complete_execute(
                    fixture.step_execution_id,
                    3,
                    StepDecision::default(),
                );
                fixture.assert_snapshot_released();
            }
            StepMethod::Unspecified => panic!("fixture uses a concrete method"),
        }
    }
}

#[test]
fn retry_snapshot_pins_are_released_on_final_failure_and_flow_stop() {
    for retry_method in [
        RetryMethod::TimeoutHandler,
        RetryMethod::WaitFor,
        RetryMethod::Execute,
    ] {
        for should_exhaust in [false, true] {
            let mut fixture = RetrySnapshotHarness::new(retry_method);
            fixture.mutate_state();
            fixture.fail(1);
            fixture.assert_snapshot(StepInvocationPhase::Backoff);
            if should_exhaust {
                fixture.fire_retry(2);
                fixture
                    .harness
                    .start_attempt(fixture.step_execution_id, fixture.method, 2);
                fixture.fail(2);
                fixture.fire_retry(3);
                fixture
                    .harness
                    .start_attempt(fixture.step_execution_id, fixture.method, 3);
                fixture.fail(3);
                assert_eq!(
                    fixture.harness.run_record().status,
                    FlowStatus::Failed as i32
                );
            } else {
                fixture.harness.apply(Command::FlowStop(FlowStopCommand {
                    request: Some(StopFlowRequest {
                        stop_type: StopType::Cancel as i32,
                        ..StopFlowRequest::default()
                    }),
                }));
                assert_eq!(
                    fixture.harness.run_record().status,
                    FlowStatus::Canceled as i32
                );
            }
            fixture.assert_snapshot_released();
        }
    }
}

#[test]
fn time_travel_restores_retry_snapshot_pins_and_releases_abandoned_snapshots() {
    for retry_method in [
        RetryMethod::TimeoutHandler,
        RetryMethod::WaitFor,
        RetryMethod::Execute,
    ] {
        for should_restore_pending in [false, true] {
            let mut fixture = RetrySnapshotHarness::new(retry_method);
            fixture.mutate_state();
            fixture.fail(1);
            if should_restore_pending {
                fixture.fire_retry(2);
            }
            fixture
                .harness
                .set_attribute("marker", string_value("restore here"), "restore-point");
            let target_time = fixture.harness.now_micros;
            if !should_restore_pending {
                fixture.fire_retry(2);
            }
            fixture
                .harness
                .start_attempt(fixture.step_execution_id, fixture.method, 2);
            match fixture.method {
                StepMethod::WaitFor => {
                    fixture
                        .harness
                        .complete_wait_for(fixture.step_execution_id, 2);
                }
                StepMethod::Execute => {
                    fixture.harness.complete_execute(
                        fixture.step_execution_id,
                        2,
                        StepDecision::default(),
                    );
                }
                StepMethod::Unspecified => panic!("fixture uses a concrete method"),
            }
            fixture.assert_snapshot_released();
            let outcome = fixture
                .harness
                .apply(Command::TimeTravel(TimeTravelCommand {
                    request: Some(ResetFlowRequest {
                        flow_id: fixture.harness.flow_id.clone(),
                        reset_type: FlowResetType::HistoryEventTime as i32,
                        history_event_time: (i128::from(target_time) * 1000).to_string(),
                        skip_writes_reapply: true,
                        ..ResetFlowRequest::default()
                    }),
                }));
            assert!(matches!(
                outcome.reply.reply,
                Some(command_reply::Reply::TimeTravel(_))
            ));
            fixture.assert_snapshot(if should_restore_pending {
                StepInvocationPhase::Pending
            } else {
                StepInvocationPhase::Backoff
            });
            fixture.restart_from_persisted_rows();
            let outcome = fixture
                .harness
                .apply(Command::TimeTravel(TimeTravelCommand {
                    request: Some(ResetFlowRequest {
                        flow_id: fixture.harness.flow_id.clone(),
                        reset_type: FlowResetType::Beginning as i32,
                        skip_writes_reapply: true,
                        ..ResetFlowRequest::default()
                    }),
                }));
            assert!(matches!(
                outcome.reply.reply,
                Some(command_reply::Reply::TimeTravel(_))
            ));
            fixture.assert_snapshot_released();
        }
    }
}

#[test]
fn flow_stop_releases_retry_snapshots_while_pending_or_running() {
    for should_start_retry in [false, true] {
        let mut fixture = RetrySnapshotHarness::new(RetryMethod::TimeoutHandler);
        fixture.mutate_state();
        fixture.fail(1);
        fixture.fire_retry(2);
        if should_start_retry {
            fixture
                .harness
                .start_attempt(fixture.step_execution_id, fixture.method, 2);
        }
        fixture.harness.apply(Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                stop_type: StopType::Cancel as i32,
                ..StopFlowRequest::default()
            }),
        }));
        fixture.assert_snapshot_released();
    }
}

#[test]
fn sibling_decisions_release_timeout_handler_retry_snapshots() {
    for should_close_flow in [false, true] {
        let mut fixture = RetrySnapshotHarness::new(RetryMethod::TimeoutHandler);
        fixture.mutate_state();
        fixture.fail(1);
        fixture
            .harness
            .start_attempt("ChargeStep-1", StepMethod::WaitFor, 1);
        fixture.harness.complete_wait_for("ChargeStep-1", 1);
        fixture
            .harness
            .start_attempt("ChargeStep-1", StepMethod::Execute, 1);
        let decision = if should_close_flow {
            StepDecision {
                close_decision: Some(CloseDecision {
                    close_decision_type: CloseDecisionType::ForceComplete as i32,
                    ..CloseDecision::default()
                }),
                ..StepDecision::default()
            }
        } else {
            StepDecision {
                cancel_step_types: vec!["sys:timeout_handler".to_owned()],
                ..StepDecision::default()
            }
        };
        fixture
            .harness
            .complete_execute("ChargeStep-1", 1, decision);
        fixture.assert_snapshot_released();
        assert_eq!(
            fixture
                .harness
                .step_execution(fixture.step_execution_id)
                .phase,
            StepExecutionPhase::Canceled as i32
        );
    }
}

#[test]
fn retry_snapshots_keep_values_readable_after_the_history_window_advances() {
    let mut fixture = RetrySnapshotHarness::with_settings(
        RetryMethod::TimeoutHandler,
        RunSettings {
            max_retained_superseded_bytes: 0,
            ..settings()
        },
    );
    fixture.mutate_state();
    fixture.fail(1);
    let run_id = fixture.harness.harness_run_id();
    let attribute_reference = ValueReference {
        run_id,
        value_id: fixture.attribute_value_id,
    }
    .to_string();
    let channel_reference = ValueReference {
        run_id,
        value_id: fixture.channel_value_id,
    }
    .to_string();
    for index in 0..8 {
        fixture.harness.set_attribute(
            "pressure",
            string_value(&format!("{index}{}", "p".repeat(500))),
            &format!("pressure-{index}"),
        );
    }
    assert!(fixture.harness.run_record().window_start_seq > fixture.channel_value_id.run_seq);
    fixture.assert_snapshot(StepInvocationPhase::Backoff);
    fixture.restart_from_persisted_rows();
    for (reference, expected) in [
        (&attribute_reference, "a".repeat(500)),
        (&channel_reference, "c".repeat(500)),
    ] {
        assert_eq!(
            load_for_wire(
                &fixture.harness.state,
                fixture.harness.context.flow_hash,
                run_id,
                reference
            )
            .expect("snapshot value lookup succeeds"),
            Some(string_value(&expected))
        );
    }
    fixture.fire_retry(2);
    fixture
        .harness
        .start_attempt(fixture.step_execution_id, fixture.method, 2);
    fixture
        .harness
        .complete_execute(fixture.step_execution_id, 2, StepDecision::default());
    fixture.assert_snapshot_released();
    for index in 8..16 {
        fixture.harness.set_attribute(
            "pressure",
            string_value(&format!("{index}{}", "p".repeat(500))),
            &format!("pressure-{index}"),
        );
    }
    for reference in [&attribute_reference, &channel_reference] {
        assert_eq!(
            load_for_wire(
                &fixture.harness.state,
                fixture.harness.context.flow_hash,
                run_id,
                reference
            )
            .expect("collected value lookup succeeds"),
            None
        );
    }
}

fn publish(harness: &mut Harness, channel_name: &str, message_id: &str, text: &str) {
    let outcome = harness.apply(Command::ChannelPublish(ChannelPublishCommand {
        request: Some(PublishToChannelRequest {
            messages: vec![ChannelMessage {
                channel_name: channel_name.to_owned(),
                message_id: message_id.to_owned(),
                value: Some(string_value(text)),
            }],
            ..PublishToChannelRequest::default()
        }),
    }));
    assert!(!matches!(
        outcome.reply.reply,
        Some(command_reply::Reply::Error(_))
    ));
}
