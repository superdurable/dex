// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Hand-out commands: task ID fences, failure guards, returned attempts, attempt progress, and the
//! retry clock (plan sections 3.4 and 3.9).

use super::*;
use dexzd_protocol::dex::{StepAttemptProgress, StepDurability};
use dexzd_protocol::dexzd::v1::{
    LockRecord, StepAttemptReturnReason, StepAttemptReturnedCommand, StepInvocationRecord,
};

const TASK_A: [u8; 16] = [0xa; 16];
const TASK_B: [u8; 16] = [0xb; 16];

impl Harness {
    fn start_with_task(
        &mut self,
        method: StepMethod,
        attempt: u32,
        task_id: &[u8],
    ) -> ApplyOutcome {
        self.apply(Command::StepAttemptStarted(StepAttemptStartedCommand {
            invocation: Some(key("ChargeStep-1", method)),
            attempt,
            task_id: task_id.to_vec(),
            pull_routing: None,
        }))
    }

    fn return_attempt(
        &mut self,
        method: StepMethod,
        attempt: u32,
        task_id: &[u8],
        reason: StepAttemptReturnReason,
    ) -> ApplyOutcome {
        self.apply(Command::StepAttemptReturned(StepAttemptReturnedCommand {
            invocation: Some(key("ChargeStep-1", method)),
            attempt,
            expected_task_id: task_id.to_vec(),
            reason: reason as i32,
        }))
    }

    fn fail_attempt(
        &mut self,
        method: StepMethod,
        mut failure: StepAttemptFailedCommand,
    ) -> ApplyOutcome {
        failure.invocation = Some(key("ChargeStep-1", method));
        if failure.kind == 0 {
            failure.kind = StepAttemptFailureKind::DispatchError as i32;
            failure.server_detail = "WORKER_DISCONNECTED: gone".to_owned();
        }
        self.apply(Command::StepAttemptFailed(failure))
    }

    fn fire_retry(&mut self, method: StepMethod, next_attempt: u32) -> ApplyOutcome {
        self.apply(Command::TimersFired(TimersFiredCommand {
            timers: vec![TimerId {
                run_attempt: self.run_record().current_attempt,
                kind: Some(timer_id::Kind::RetryBackoff(RetryBackoffTimerId {
                    invocation: Some(key("ChargeStep-1", method)),
                    next_attempt,
                })),
            }],
        }))
    }

    fn invocation(&self) -> StepInvocationRecord {
        self.step_execution("ChargeStep-1")
            .invocation
            .expect("the Step has an invocation")
    }

    /// Rewrites the Step row as an older binary would have left it.
    fn rewrite_invocation(&mut self, change: impl FnOnce(&mut StepInvocationRecord)) {
        let row_key = run_named_key(
            self.context.flow_hash,
            self.harness_run_id(),
            RunTable::StepExecution,
            "ChargeStep-1",
        );
        let mut step = self.step_execution("ChargeStep-1");
        change(
            step.invocation
                .as_mut()
                .expect("the Step has an invocation"),
        );
        self.state
            .rows
            .insert((ColumnFamily::Flow, row_key), step.encode_to_vec());
        self.is_storage_recount_checked = false;
    }

    fn charge_lock(&self) -> Option<LockRecord> {
        self.decoded_row(&run_named_key(
            self.context.flow_hash,
            self.harness_run_id(),
            RunTable::LockTable,
            "balance",
        ))
    }
}

/// Starts a SYNC Flow whose first Step skips WaitFor and locks `balance` in Execute.
fn start_execute_only(retry_policy: RetryPolicy, durability: StepDurability) -> Harness {
    let mut harness = Harness::new("order-1");
    let mut request = start_request("request-1");
    request.step_options = Some(StepOptions {
        skip_wait_for: true,
        execute_lock_attribute_keys: vec!["balance".to_owned()],
        execute_retry_policy: Some(retry_policy),
        execute_durability_override: durability as i32,
        execute_failure_policy: ExecuteMethodFailurePolicy::ProceedToConfiguredStep as i32,
        execute_failure_proceed_step_type: "RecoverStep".to_owned(),
        ..StepOptions::default()
    });
    harness.start(request);
    harness
}

fn attempts(maximum_attempts: i32) -> RetryPolicy {
    RetryPolicy {
        maximum_attempts,
        ..RetryPolicy::default()
    }
}

/// Returns the attempt progress reported to the recovery Step after the method failed for good.
fn recovery_progress(harness: &Harness) -> i32 {
    harness
        .step_execution("RecoverStep-1")
        .recovery_error
        .expect("the recovery Step has a recovery error")
        .attempt_progress
}

#[test]
fn heartbeats_and_results_of_another_task_are_ignored_and_older_logs_skip_the_fence() {
    let mut harness = start_execute_only(attempts(1), StepDurability::Sync);
    harness.start_with_task(StepMethod::Execute, 1, &TASK_A);
    let stale_heartbeat =
        harness.apply(Command::StepAttemptHeartbeat(StepAttemptHeartbeatCommand {
            invocation: Some(key("ChargeStep-1", StepMethod::Execute)),
            attempt: 1,
            checkpoint: Some(step_attempt_heartbeat_command::Checkpoint::Value(
                string_value("stale"),
            )),
            task_id: TASK_B.to_vec(),
        }));
    assert!(stale_heartbeat.writes.is_empty());
    assert!(harness.invocation().heartbeat_checkpoint.is_none());
    let older_heartbeat =
        harness.apply(Command::StepAttemptHeartbeat(StepAttemptHeartbeatCommand {
            invocation: Some(key("ChargeStep-1", StepMethod::Execute)),
            attempt: 1,
            checkpoint: Some(step_attempt_heartbeat_command::Checkpoint::Value(
                string_value("from an older log"),
            )),
            task_id: Vec::new(),
        }));
    assert!(!older_heartbeat.writes.is_empty());
    let result = |task_id: &[u8]| {
        Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(key("ChargeStep-1", StepMethod::Execute)),
            attempt: 1,
            response: Some(step_method_completed_command::Response::Execute(
                InvokeExecuteMethodResponse::default(),
            )),
            app_worker_id: "worker-a".to_owned(),
            task_id: task_id.to_vec(),
            ..StepMethodCompletedCommand::default()
        })
    };
    let stale_result = harness.apply(result(&TASK_B));
    assert!(stale_result.writes.is_empty());
    assert_eq!(
        harness.invocation().phase,
        StepInvocationPhase::Running as i32
    );
    let completed = harness.apply(result(&TASK_A));
    assert!(!completed.writes.is_empty());
    assert!(
        harness.step_execution_if_present("ChargeStep-1").is_none(),
        "the result completes the Step, which deletes its row"
    );
}

#[test]
fn failure_guards_match_the_phase_and_task_of_the_attempt() {
    let mut harness = start_execute_only(attempts(3), StepDurability::Sync);
    let before_start = StepAttemptFailedCommand {
        attempt: 1,
        is_before_start: true,
        ..StepAttemptFailedCommand::default()
    };
    let older_log = StepAttemptFailedCommand {
        attempt: 1,
        ..StepAttemptFailedCommand::default()
    };
    // A pending attempt only accepts a failure before any start.
    assert!(
        harness
            .fail_attempt(StepMethod::Execute, older_log.clone())
            .writes
            .is_empty()
    );
    harness.start_with_task(StepMethod::Execute, 1, &TASK_A);
    assert!(
        harness
            .fail_attempt(StepMethod::Execute, before_start)
            .writes
            .is_empty(),
        "a started attempt ignores a failure from before its start"
    );
    let both_guards = StepAttemptFailedCommand {
        attempt: 1,
        is_before_start: true,
        expected_task_id: TASK_A.to_vec(),
        ..StepAttemptFailedCommand::default()
    };
    assert!(
        harness
            .fail_attempt(StepMethod::Execute, both_guards)
            .writes
            .is_empty(),
        "a failure is never both before the start and fenced on it"
    );
    let other_task = StepAttemptFailedCommand {
        attempt: 1,
        expected_task_id: TASK_B.to_vec(),
        ..StepAttemptFailedCommand::default()
    };
    assert!(
        harness
            .fail_attempt(StepMethod::Execute, other_task)
            .writes
            .is_empty()
    );
    assert!(
        !harness
            .fail_attempt(StepMethod::Execute, older_log)
            .writes
            .is_empty()
    );
    assert_eq!(
        harness.invocation().phase,
        StepInvocationPhase::Backoff as i32
    );
}

#[test]
fn a_returned_attempt_is_pending_again_with_its_snapshot_lock_and_attempt_number() {
    let mut harness = start_execute_only(attempts(3), StepDurability::Sync);
    harness.start_with_task(StepMethod::Execute, 1, &TASK_A);
    let running = harness.invocation();
    assert!(running.retry_started_at_micros > 0);
    let history_before = harness.run_record().next_run_seq;
    let without_reason = harness.return_attempt(
        StepMethod::Execute,
        1,
        &TASK_A,
        StepAttemptReturnReason::Unspecified,
    );
    assert!(
        without_reason.writes.is_empty(),
        "a return names its reason"
    );
    let returned = harness.return_attempt(
        StepMethod::Execute,
        1,
        &TASK_A,
        StepAttemptReturnReason::Unsupported,
    );
    assert_eq!(
        pending_invocations(&returned),
        vec![("ChargeStep-1".to_owned(), StepMethod::Execute as i32, 1)]
    );
    assert!(method_failures(&returned).is_empty());
    let pending = harness.invocation();
    assert_eq!(pending.phase, StepInvocationPhase::Pending as i32);
    assert_eq!(pending.attempt, 1);
    assert!(pending.task_id.is_empty());
    assert!(pending.pull_routing.is_none());
    assert_eq!(pending.attempt_started_at_micros, 0);
    assert_eq!(pending.snapshot, running.snapshot);
    assert_eq!(pending.pending_since_seq, running.pending_since_seq);
    assert_eq!(
        pending.furthest_attempt_progress,
        StepAttemptProgress::NotStarted as i32
    );
    assert_eq!(pending.retry_started_at_micros, 0, "attempt 1 never ran");
    let lock = harness.charge_lock().expect("the lock row exists");
    assert_eq!(lock.holder, Some(key("ChargeStep-1", StepMethod::Execute)));
    assert_eq!(lock.holder_attempt, 1);
    assert!(harness.run_record().next_run_seq > history_before);
    // A second return of the same task finds no running attempt.
    let repeated = harness.return_attempt(
        StepMethod::Execute,
        1,
        &TASK_A,
        StepAttemptReturnReason::NotDelivered,
    );
    assert!(repeated.writes.is_empty());
    // The next hand-out reuses the snapshot and restarts the clock.
    harness.start_with_task(StepMethod::Execute, 1, &TASK_B);
    let restarted = harness.invocation();
    assert_eq!(restarted.snapshot, running.snapshot);
    assert_eq!(restarted.task_id, TASK_B.to_vec());
    assert_eq!(restarted.retry_started_at_micros, harness.now_micros);
}

#[test]
fn a_return_of_a_later_attempt_keeps_its_retry_clock() {
    let mut harness = start_execute_only(attempts(3), StepDurability::Sync);
    harness.start_with_task(StepMethod::Execute, 1, &TASK_A);
    let clock = harness.invocation().retry_started_at_micros;
    harness.fail_attempt(
        StepMethod::Execute,
        StepAttemptFailedCommand {
            attempt: 1,
            expected_task_id: TASK_A.to_vec(),
            ..StepAttemptFailedCommand::default()
        },
    );
    harness.fire_retry(StepMethod::Execute, 2);
    harness.start_with_task(StepMethod::Execute, 2, &TASK_B);
    harness.return_attempt(
        StepMethod::Execute,
        2,
        &TASK_B,
        StepAttemptReturnReason::LegacyNoCapacity,
    );
    let pending = harness.invocation();
    assert_eq!(pending.attempt, 2);
    assert_eq!(pending.retry_started_at_micros, clock);
    assert_eq!(
        pending.furthest_attempt_progress,
        StepAttemptProgress::NotCompleted as i32
    );
}

#[test]
fn a_failure_before_any_start_starts_the_retry_clock() {
    let mut harness = start_execute_only(attempts(3), StepDurability::Sync);
    harness.fail_attempt(
        StepMethod::Execute,
        StepAttemptFailedCommand {
            attempt: 1,
            is_before_start: true,
            server_detail: "NO_WORKER_AVAILABLE: no Worker accepted the task".to_owned(),
            ..StepAttemptFailedCommand::default()
        },
    );
    let failed_at = harness.now_micros;
    let backoff = harness.invocation();
    assert_eq!(backoff.phase, StepInvocationPhase::Backoff as i32);
    assert_eq!(backoff.retry_started_at_micros, failed_at);
    // The waiting attempt 1 queued on the lock and never held it; nothing stays queued.
    assert!(
        harness
            .charge_lock()
            .is_none_or(|lock| lock.holder.is_none() && lock.waiters.is_empty())
    );
    harness.fire_retry(StepMethod::Execute, 2);
    harness.start_with_task(StepMethod::Execute, 2, &TASK_A);
    assert_eq!(harness.invocation().retry_started_at_micros, failed_at);
}

#[test]
fn older_records_without_a_retry_clock_keep_the_first_attempt_deadline() {
    let short = RetryPolicy {
        total_duration_seconds: 5,
        ..RetryPolicy::default()
    };
    // An older running attempt 1 counts from its first attempt and is not restamped.
    let mut harness = start_execute_only(short, StepDurability::Sync);
    harness.start_with_task(StepMethod::Execute, 1, &TASK_A);
    harness.rewrite_invocation(|invocation| invocation.retry_started_at_micros = 0);
    harness.now_micros += 10 * SECOND;
    harness.fail_attempt(
        StepMethod::Execute,
        StepAttemptFailedCommand {
            attempt: 1,
            ..StepAttemptFailedCommand::default()
        },
    );
    assert!(
        harness.step_execution("ChargeStep-1").invocation.is_none(),
        "the deadline counted from the first attempt has passed"
    );
    // An older pending attempt 2 keeps falling back when it starts.
    let mut harness = start_execute_only(attempts(3), StepDurability::Sync);
    harness.start_with_task(StepMethod::Execute, 1, &TASK_A);
    harness.fail_attempt(
        StepMethod::Execute,
        StepAttemptFailedCommand {
            attempt: 1,
            expected_task_id: TASK_A.to_vec(),
            ..StepAttemptFailedCommand::default()
        },
    );
    harness.fire_retry(StepMethod::Execute, 2);
    harness.rewrite_invocation(|invocation| invocation.retry_started_at_micros = 0);
    harness.start_with_task(StepMethod::Execute, 2, &TASK_B);
    assert_eq!(harness.invocation().retry_started_at_micros, 0);
}

#[test]
fn progress_reports_the_furthest_attempt_of_the_method_call() {
    let not_run = |attempt, task_id: &[u8]| StepAttemptFailedCommand {
        attempt,
        expected_task_id: task_id.to_vec(),
        is_not_run: true,
        kind: StepAttemptFailureKind::RequestTooLarge as i32,
        ..StepAttemptFailedCommand::default()
    };
    let may_have_run = |attempt, task_id: &[u8]| StepAttemptFailedCommand {
        attempt,
        expected_task_id: task_id.to_vec(),
        ..StepAttemptFailedCommand::default()
    };
    // A handed-out attempt may have run, even when a later attempt never did.
    let mut harness = start_execute_only(attempts(2), StepDurability::Sync);
    harness.start_with_task(StepMethod::Execute, 1, &TASK_A);
    harness.fail_attempt(StepMethod::Execute, may_have_run(1, &TASK_A));
    harness.fire_retry(StepMethod::Execute, 2);
    harness.start_with_task(StepMethod::Execute, 2, &TASK_B);
    harness.fail_attempt(StepMethod::Execute, not_run(2, &TASK_B));
    assert_eq!(
        recovery_progress(&harness),
        StepAttemptProgress::NotCompleted as i32
    );
    // Attempts that never ran, including a returned one, report NOT_STARTED.
    let mut harness = start_execute_only(attempts(2), StepDurability::Sync);
    harness.fail_attempt(
        StepMethod::Execute,
        StepAttemptFailedCommand {
            attempt: 1,
            is_before_start: true,
            ..StepAttemptFailedCommand::default()
        },
    );
    harness.fire_retry(StepMethod::Execute, 2);
    harness.start_with_task(StepMethod::Execute, 2, &TASK_A);
    harness.return_attempt(
        StepMethod::Execute,
        2,
        &TASK_A,
        StepAttemptReturnReason::NotDelivered,
    );
    harness.start_with_task(StepMethod::Execute, 2, &TASK_B);
    harness.fail_attempt(StepMethod::Execute, not_run(2, &TASK_B));
    assert_eq!(
        recovery_progress(&harness),
        StepAttemptProgress::NotStarted as i32
    );
    // ASYNC never reports NOT_STARTED: a rolled-back execution may have run.
    let mut harness = start_execute_only(attempts(1), StepDurability::Async);
    harness.fail_attempt(
        StepMethod::Execute,
        StepAttemptFailedCommand {
            attempt: 1,
            is_before_start: true,
            ..StepAttemptFailedCommand::default()
        },
    );
    assert_eq!(
        recovery_progress(&harness),
        StepAttemptProgress::NotCompleted as i32
    );
}

#[test]
fn progress_of_older_records_stays_unspecified_unless_an_attempt_may_have_run() {
    for (is_not_run, expected) in [
        (true, StepAttemptProgress::Unspecified),
        (false, StepAttemptProgress::NotCompleted),
    ] {
        let mut harness = start_execute_only(attempts(1), StepDurability::Sync);
        harness.start_with_task(StepMethod::Execute, 1, &TASK_A);
        harness.rewrite_invocation(|invocation| invocation.furthest_attempt_progress = 0);
        harness.fail_attempt(
            StepMethod::Execute,
            StepAttemptFailedCommand {
                attempt: 1,
                expected_task_id: TASK_A.to_vec(),
                is_not_run,
                ..StepAttemptFailedCommand::default()
            },
        );
        assert_eq!(recovery_progress(&harness), expected as i32);
    }
}

#[test]
fn time_travel_restores_a_running_attempt_as_pending_when_the_command_asks() {
    let mut harness = start_execute_only(attempts(3), StepDurability::Sync);
    harness.start_with_task(StepMethod::Execute, 1, &TASK_A);
    let running = harness.invocation();
    // Starts are bookkeeping, so the travel targets a later semantic event.
    harness.try_set_attribute("status", string_value("charging"), "set-1");
    let target_time = harness.now_micros;
    harness.complete_execute("ChargeStep-1", 1, StepDecision::default());
    harness.now_micros += 60 * SECOND;
    let outcome = harness.apply(Command::TimeTravel(TimeTravelCommand {
        staging_id: Vec::new(),
        computed_through_seq: 0,
        target_run_attempt: 0,
        target_run_seq: 0,
        should_restore_running_as_pending: true,
        request: Some(ResetFlowRequest {
            flow_id: harness.flow_id.clone(),
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
    assert!(
        !outcome
            .effects
            .iter()
            .any(|effect| matches!(effect, ApplyEffect::InvocationStarted { .. }))
    );
    assert_eq!(
        pending_invocations(&outcome),
        vec![("ChargeStep-1".to_owned(), StepMethod::Execute as i32, 1)]
    );
    let restored = harness.invocation();
    assert_eq!(restored.phase, StepInvocationPhase::Pending as i32);
    assert_eq!(restored.attempt, 1);
    assert!(restored.task_id.is_empty());
    assert_eq!(restored.snapshot, running.snapshot);
    assert_eq!(restored.retry_started_at_micros, 0);
    assert_eq!(
        restored.furthest_attempt_progress,
        StepAttemptProgress::NotCompleted as i32
    );
    let lock = harness.charge_lock().expect("the lock row exists");
    assert_eq!(lock.holder, Some(key("ChargeStep-1", StepMethod::Execute)));
    // It may have run before the travel, so a return keeps the clock of its next hand-out.
    harness.start_with_task(StepMethod::Execute, 1, &TASK_B);
    let restarted_at = harness.now_micros;
    harness.return_attempt(
        StepMethod::Execute,
        1,
        &TASK_B,
        StepAttemptReturnReason::Unsupported,
    );
    assert_eq!(harness.invocation().retry_started_at_micros, restarted_at);
}

#[test]
fn a_failure_that_replaces_a_rejected_result_is_fenced_on_its_task() {
    let mut harness = start_execute_only(attempts(1), StepDurability::Sync);
    harness.start_with_task(StepMethod::Execute, 1, &TASK_A);
    let unknown_reference = Value {
        kind: Some(value::Kind::InternalBlobIdForStringValue(
            "zd1|not-a-reference".to_owned(),
        )),
    };
    harness.apply(Command::StepMethodCompleted(StepMethodCompletedCommand {
        invocation: Some(key("ChargeStep-1", StepMethod::Execute)),
        attempt: 1,
        task_id: TASK_A.to_vec(),
        response: Some(step_method_completed_command::Response::Execute(
            InvokeExecuteMethodResponse {
                upsert_attributes: vec![AttributeWrite {
                    key: "report".to_owned(),
                    value: Some(unknown_reference),
                    ..AttributeWrite::default()
                }],
                ..InvokeExecuteMethodResponse::default()
            },
        )),
        app_worker_id: "worker-a".to_owned(),
        ..StepMethodCompletedCommand::default()
    }));
    let history_prefix = run_table_prefix(
        harness.context.flow_hash,
        harness.harness_run_id(),
        RunTable::HistoryRecord,
    );
    let recorded_failures: Vec<_> = harness
        .state
        .rows
        .iter()
        .filter(|((column_family, key), _)| {
            *column_family == ColumnFamily::History && key.starts_with(&history_prefix)
        })
        .filter_map(|(_, value)| {
            match HistoryRecord::decode(value.as_slice())
                .expect("history decodes")
                .command?
                .command?
            {
                Command::StepAttemptFailed(failed) => Some(failed),
                _ => None,
            }
        })
        .collect();
    assert_eq!(recorded_failures.len(), 1);
    assert_eq!(
        recorded_failures[0].kind,
        StepAttemptFailureKind::InvalidResponse as i32
    );
    assert_eq!(recorded_failures[0].expected_task_id, TASK_A.to_vec());
    assert!(
        harness
            .step_execution("RecoverStep-1")
            .recovery_error
            .is_some(),
        "the fenced failure applied and started the recovery Step"
    );
}
