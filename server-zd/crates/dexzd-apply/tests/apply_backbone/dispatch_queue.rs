// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The DispatchQueue index: `StepAttemptQueued`, row removal on every exit from the pending
//! phase, and `pending_since_micros` (plan section 3.2, TB20 to TB22).

use super::*;
use dexzd_apply::{dispatch_queue_entry, effective_pending_since_micros};
use dexzd_keyspace::dispatch_queue_keys::{
    DispatchQueueEntry, TIMEOUT_HANDLER_DISPATCH_PRIORITY, decode_dispatch_queue_key,
    dispatch_queue_key, dispatch_queue_partition_prefix,
};
use dexzd_protocol::dexzd::v1::{
    DispatchQueueRecord, StepAttemptQueuedCommand, StepAttemptReturnReason,
    StepAttemptReturnedCommand,
};

const TASK_A: [u8; 16] = [0xa; 16];

impl Harness {
    fn queue(&mut self, step_execution_id: &str, method: StepMethod) -> ApplyOutcome {
        let invocation = self
            .step_execution(step_execution_id)
            .invocation
            .expect("the Step has an invocation");
        self.queue_at(
            step_execution_id,
            method,
            invocation.attempt,
            effective_pending_since_micros(&invocation),
        )
    }

    fn queue_at(
        &mut self,
        step_execution_id: &str,
        method: StepMethod,
        attempt: u32,
        pending_since_micros: i64,
    ) -> ApplyOutcome {
        self.apply(Command::StepAttemptQueued(StepAttemptQueuedCommand {
            invocation: Some(key(step_execution_id, method)),
            attempt,
            pending_since_micros,
        }))
    }

    /// Returns every DispatchQueue row of the harness partition, in key order.
    fn dispatch_queue_rows(&self) -> Vec<(DispatchQueueEntry, DispatchQueueRecord)> {
        let (start, end) = prefix_range(&dispatch_queue_partition_prefix(self.context.partition));
        self.state
            .rows
            .range((ColumnFamily::PartitionIndex, start)..(ColumnFamily::PartitionIndex, end))
            .map(|((_, key), value)| {
                (
                    decode_dispatch_queue_key(key).expect("DispatchQueue keys decode"),
                    DispatchQueueRecord::decode(value.as_slice()).expect("rows decode"),
                )
            })
            .collect()
    }

    /// Asserts that every row names a pending invocation of the current run attempt, at most
    /// one row per invocation.
    pub(super) fn assert_dispatch_queue_invariant(&self) {
        if self.run_id.is_empty() {
            return;
        }
        let record = self.run_record();
        let mut seen = std::collections::BTreeSet::new();
        for (entry, row) in self.dispatch_queue_rows() {
            let step = self.step_execution(&entry.position.step_execution_id);
            let expected = dispatch_queue_entry(
                self.context.partition,
                &record,
                self.context.flow_hash,
                self.harness_run_id(),
                &step,
            );
            assert_eq!(expected.as_ref(), Some(&entry), "the row matches its Step");
            let invocation = step.invocation.expect("a queued Step has an invocation");
            assert_eq!(invocation.phase, StepInvocationPhase::Pending as i32);
            assert_eq!(row.attempt, invocation.attempt);
            assert_eq!(row.run_attempt, record.current_attempt);
            assert!(seen.insert((entry.position.step_execution_id, invocation.method)));
        }
    }
}

fn queue_changes(outcome: &ApplyOutcome) -> Vec<(usize, u32)> {
    outcome
        .effects
        .iter()
        .filter_map(|effect| match effect {
            ApplyEffect::DispatchQueueChanged {
                written_keys,
                removed_count,
            } => Some((written_keys.len(), *removed_count)),
            _ => None,
        })
        .collect()
}

fn single_deletes(outcome: &ApplyOutcome) -> usize {
    outcome
        .writes
        .iter()
        .filter(|write| matches!(write, StateWrite::SingleDelete { .. }))
        .count()
}

fn retrying_start(maximum_attempts: i32) -> Harness {
    let mut harness = Harness::new("order-1");
    let mut request = start_request("request-1");
    request.step_options = Some(StepOptions {
        skip_wait_for: true,
        execute_retry_policy: Some(RetryPolicy {
            maximum_attempts,
            ..RetryPolicy::default()
        }),
        ..StepOptions::default()
    });
    harness.start(request);
    harness
}

#[test]
fn a_queued_attempt_writes_only_its_index_row_once() {
    let mut harness = retrying_start(3);
    let invocation = harness.step_execution("ChargeStep-1").invocation.unwrap();
    assert_eq!(
        invocation.pending_since_micros, invocation.first_attempt_at_micros,
        "a new Step is pending since its creating command"
    );
    let record_before = harness.run_record();
    let queued = harness.queue("ChargeStep-1", StepMethod::Execute);
    assert_eq!(queued.writes.len(), 1, "only the index row: {queued:?}");
    assert!(matches!(
        &queued.writes[0],
        StateWrite::Put {
            column_family: ColumnFamily::PartitionIndex,
            ..
        }
    ));
    assert_eq!(queue_changes(&queued), vec![(1, 0)]);
    assert_eq!(
        harness.run_record(),
        record_before,
        "no seq, history, or storage change"
    );
    let rows = harness.dispatch_queue_rows();
    assert_eq!(rows.len(), 1);
    assert_eq!(rows[0].0.queue.step_type, "ChargeStep");
    assert_eq!(rows[0].1.attempt, 1);
    assert_eq!(rows[0].1.run_attempt, 1);
    let again = harness.queue("ChargeStep-1", StepMethod::Execute);
    assert!(
        again.writes.is_empty(),
        "an existing row is not written again"
    );
    assert!(!is_error(&again.reply, 9));
    let wrong_time = harness.queue_at(
        "ChargeStep-1",
        StepMethod::Execute,
        1,
        invocation.pending_since_micros + 1,
    );
    assert!(wrong_time.writes.is_empty());
    assert!(is_error(&wrong_time.reply, 9));
    let wrong_attempt = harness.queue_at(
        "ChargeStep-1",
        StepMethod::Execute,
        2,
        invocation.pending_since_micros,
    );
    assert!(is_error(&wrong_attempt.reply, 9));
}

#[test]
fn a_start_removes_the_row_with_a_single_delete_and_a_late_queue_is_ignored() {
    let mut harness = retrying_start(3);
    harness.queue("ChargeStep-1", StepMethod::Execute);
    let started = harness.apply(Command::StepAttemptStarted(StepAttemptStartedCommand {
        invocation: Some(key("ChargeStep-1", StepMethod::Execute)),
        attempt: 1,
        task_id: TASK_A.to_vec(),
        pull_routing: None,
    }));
    assert_eq!(single_deletes(&started), 1);
    assert_eq!(queue_changes(&started), vec![(0, 1)]);
    assert!(harness.dispatch_queue_rows().is_empty());
    let late = harness.queue_at(
        "ChargeStep-1",
        StepMethod::Execute,
        1,
        harness
            .step_execution("ChargeStep-1")
            .invocation
            .unwrap()
            .pending_since_micros,
    );
    assert!(
        late.writes.is_empty(),
        "a late queue after the start is a no-op"
    );
    assert!(harness.dispatch_queue_rows().is_empty());
}

#[test]
fn an_unqueued_start_leaves_no_tombstone() {
    let mut harness = retrying_start(3);
    let started = harness.start_attempt("ChargeStep-1", StepMethod::Execute, 1);
    assert_eq!(single_deletes(&started), 0);
    assert!(queue_changes(&started).is_empty());
}

#[test]
fn a_returned_attempt_keeps_its_queue_position() {
    let mut harness = retrying_start(3);
    harness.queue("ChargeStep-1", StepMethod::Execute);
    let first_key = dispatch_queue_key(&harness.dispatch_queue_rows()[0].0);
    harness.apply(Command::StepAttemptStarted(StepAttemptStartedCommand {
        invocation: Some(key("ChargeStep-1", StepMethod::Execute)),
        attempt: 1,
        task_id: TASK_A.to_vec(),
        pull_routing: None,
    }));
    harness.apply(Command::StepAttemptReturned(StepAttemptReturnedCommand {
        invocation: Some(key("ChargeStep-1", StepMethod::Execute)),
        attempt: 1,
        expected_task_id: TASK_A.to_vec(),
        reason: StepAttemptReturnReason::Unsupported as i32,
    }));
    harness.queue("ChargeStep-1", StepMethod::Execute);
    let rows = harness.dispatch_queue_rows();
    assert_eq!(rows.len(), 1);
    assert_eq!(
        dispatch_queue_key(&rows[0].0),
        first_key,
        "the same key again"
    );
}

#[test]
fn a_failed_queued_attempt_leaves_the_index_and_its_retry_queues_at_the_tail() {
    let mut harness = retrying_start(3);
    harness.queue("ChargeStep-1", StepMethod::Execute);
    let first = harness.dispatch_queue_rows()[0].0.clone();
    let failed = harness.apply(Command::StepAttemptFailed(StepAttemptFailedCommand {
        invocation: Some(key("ChargeStep-1", StepMethod::Execute)),
        attempt: 1,
        kind: StepAttemptFailureKind::DispatchError as i32,
        server_detail: "NO_WORKER_AVAILABLE: none".to_owned(),
        is_before_start: true,
        ..StepAttemptFailedCommand::default()
    }));
    assert_eq!(single_deletes(&failed), 1);
    assert!(harness.dispatch_queue_rows().is_empty());
    let backoff = harness.step_execution("ChargeStep-1").invocation.unwrap();
    assert_eq!(backoff.phase, StepInvocationPhase::Backoff as i32);
    harness.now_micros = backoff.backoff_fires_at_micros + 5 * SECOND;
    harness.apply(Command::TimersFired(TimersFiredCommand {
        timers: vec![TimerId {
            run_attempt: 1,
            kind: Some(timer_id::Kind::RetryBackoff(RetryBackoffTimerId {
                invocation: Some(key("ChargeStep-1", StepMethod::Execute)),
                next_attempt: 2,
            })),
        }],
    }));
    let retry = harness.step_execution("ChargeStep-1").invocation.unwrap();
    assert_eq!(retry.attempt, 2);
    assert_eq!(
        retry.pending_since_micros, harness.now_micros,
        "a retry is pending since its backoff fired (plan D69)"
    );
    harness.queue("ChargeStep-1", StepMethod::Execute);
    let second = harness.dispatch_queue_rows()[0].0.clone();
    assert!(first.position < second.position);
    assert!(dispatch_queue_key(&first) < dispatch_queue_key(&second));
}

#[test]
fn stopping_the_flow_removes_queued_rows() {
    let mut harness = retrying_start(3);
    harness.queue("ChargeStep-1", StepMethod::Execute);
    let stopped = harness.apply(Command::FlowStop(FlowStopCommand {
        request: Some(StopFlowRequest {
            stop_type: StopType::Cancel as i32,
            ..StopFlowRequest::default()
        }),
    }));
    assert_eq!(single_deletes(&stopped), 1);
    assert!(harness.dispatch_queue_rows().is_empty());
}

#[test]
fn a_wait_for_failure_that_proceeds_removes_the_wait_for_row() {
    let mut harness = Harness::new("order-1");
    let mut request = start_request("request-1");
    request.step_options = Some(StepOptions {
        wait_for_failure_policy: WaitForMethodFailurePolicy::ProceedOnFailure as i32,
        wait_for_retry_policy: Some(RetryPolicy {
            maximum_attempts: 1,
            ..RetryPolicy::default()
        }),
        ..StepOptions::default()
    });
    harness.start(request);
    harness.queue("ChargeStep-1", StepMethod::WaitFor);
    harness.apply(Command::StepAttemptFailed(StepAttemptFailedCommand {
        invocation: Some(key("ChargeStep-1", StepMethod::WaitFor)),
        attempt: 1,
        kind: StepAttemptFailureKind::DispatchError as i32,
        server_detail: "NO_WORKER_AVAILABLE: none".to_owned(),
        is_before_start: true,
        ..StepAttemptFailedCommand::default()
    }));
    let execute = harness.step_execution("ChargeStep-1").invocation.unwrap();
    assert_eq!(execute.method, StepMethod::Execute as i32);
    assert!(harness.dispatch_queue_rows().is_empty());
    harness.queue("ChargeStep-1", StepMethod::Execute);
    assert_eq!(harness.dispatch_queue_rows().len(), 1);
}

#[test]
fn the_timeout_handler_sorts_ahead_of_the_backlog() {
    let mut harness = Harness::new("order-1");
    let mut request = start_request("request-1");
    request.flow_timeout_seconds = 30;
    request.step_options = Some(StepOptions {
        skip_wait_for: true,
        ..StepOptions::default()
    });
    harness.start_with_timeout_policy(request, FlowTimeoutPolicy::Handler);
    harness.queue("ChargeStep-1", StepMethod::Execute);
    harness.apply(Command::TimersFired(TimersFiredCommand {
        timers: vec![TimerId {
            run_attempt: 1,
            kind: Some(timer_id::Kind::FlowTimeout(FlowTimeoutTimerId {})),
        }],
    }));
    let handler_id = "sys:timeout_handler-1";
    harness.queue(handler_id, StepMethod::Execute);
    let rows = harness.dispatch_queue_rows();
    assert_eq!(
        rows.len(),
        2,
        "the HANDLER timeout keeps the backlog queued"
    );
    let handler = rows
        .iter()
        .find(|(entry, _)| entry.position.step_execution_id == handler_id)
        .expect("the handler is queued");
    assert_eq!(
        handler.0.position.priority,
        TIMEOUT_HANDLER_DISPATCH_PRIORITY
    );
}

#[test]
fn time_travel_removes_queued_rows_and_restored_attempts_are_pending_since_the_travel() {
    let mut harness = retrying_start(3);
    let start_time = harness.now_micros;
    harness.queue("ChargeStep-1", StepMethod::Execute);
    assert_eq!(harness.dispatch_queue_rows().len(), 1);
    let travel = harness.apply(Command::TimeTravel(TimeTravelCommand {
        should_restore_running_as_pending: true,
        request: Some(ResetFlowRequest {
            reset_type: FlowResetType::Beginning as i32,
            ..ResetFlowRequest::default()
        }),
    }));
    assert!(
        matches!(
            travel.reply.reply,
            Some(command_reply::Reply::TimeTravel(_))
        ),
        "{:?}",
        travel.reply
    );
    assert_eq!(single_deletes(&travel), 1);
    assert!(harness.dispatch_queue_rows().is_empty());
    let restored = harness.step_execution("ChargeStep-1").invocation.unwrap();
    assert_eq!(restored.phase, StepInvocationPhase::Pending as i32);
    assert_eq!(restored.pending_since_micros, harness.now_micros);
    assert!(restored.pending_since_micros > start_time);
    harness.queue("ChargeStep-1", StepMethod::Execute);
    assert_eq!(harness.dispatch_queue_rows()[0].1.run_attempt, 2);
}

#[test]
fn records_without_a_pending_time_use_their_first_attempt_time() {
    let mut harness = retrying_start(3);
    let row_key = run_named_key(
        harness.context.flow_hash,
        harness.harness_run_id(),
        RunTable::StepExecution,
        "ChargeStep-1",
    );
    let mut step = harness.step_execution("ChargeStep-1");
    let invocation = step.invocation.as_mut().unwrap();
    invocation.pending_since_micros = 0;
    let first_attempt_at = invocation.first_attempt_at_micros;
    harness
        .state
        .rows
        .insert((ColumnFamily::Flow, row_key), step.encode_to_vec());
    harness.is_storage_recount_checked = false;
    harness.queue_at("ChargeStep-1", StepMethod::Execute, 1, first_attempt_at);
    let rows = harness.dispatch_queue_rows();
    assert_eq!(rows[0].0.position.pending_since_micros, first_attempt_at);
    harness.start_attempt("ChargeStep-1", StepMethod::Execute, 1);
    assert!(harness.dispatch_queue_rows().is_empty());
}

#[test]
fn get_flow_state_reports_the_invocation_phase_and_why_it_waits() {
    use dexzd_protocol::dex::{StepMethodInvocationPhase, StepMethodPendingReason};
    let mut harness = Harness::new("order-1");
    start_three_locking_steps(&mut harness);
    harness.start_attempt("LockStep-1", StepMethod::Execute, 1);
    let state = dexzd_apply::read_model::get_flow_state(
        &harness.state,
        harness.context.flow_hash,
        harness.harness_run_id(),
    )
    .expect("the read model reads the run");
    let invocation = |step_execution_id: &str| {
        state
            .active_step_executions
            .iter()
            .find(|step| step.step_execution_id == step_execution_id)
            .and_then(|step| step.execute_invocation)
            .expect("the Step has an Execute invocation")
    };
    let running = invocation("LockStep-1");
    assert_eq!(running.phase, StepMethodInvocationPhase::Running as i32);
    assert_eq!(
        running.pending_reason,
        StepMethodPendingReason::Unspecified as i32
    );
    assert_eq!(running.attempt, 1);
    assert!(running.attempt_started_time.is_some());
    assert!(running.pending_since_time.is_some());
    for waiting in ["LockStep-2", "LockStep-3"] {
        let waiting = invocation(waiting);
        assert_eq!(waiting.phase, StepMethodInvocationPhase::Pending as i32);
        assert_eq!(
            waiting.pending_reason,
            StepMethodPendingReason::WaitingForLocks as i32
        );
        assert!(waiting.attempt_started_time.is_none());
    }
    let step = state
        .active_step_executions
        .iter()
        .find(|step| step.step_execution_id == "LockStep-1")
        .expect("LockStep-1 is active");
    assert!(step.wait_for_invocation.is_none());
}

#[test]
fn get_flow_state_reports_a_queued_attempt_as_waiting_for_a_worker() {
    use dexzd_protocol::dex::{StepMethodInvocationPhase, StepMethodPendingReason};
    let mut harness = retrying_start(3);
    harness.queue("ChargeStep-1", StepMethod::Execute);
    let state = dexzd_apply::read_model::get_flow_state(
        &harness.state,
        harness.context.flow_hash,
        harness.harness_run_id(),
    )
    .expect("the read model reads the run");
    let queued = state.active_step_executions[0]
        .execute_invocation
        .expect("Execute is pending");
    assert_eq!(queued.phase, StepMethodInvocationPhase::Pending as i32);
    assert_eq!(
        queued.pending_reason,
        StepMethodPendingReason::WaitingForWorker as i32
    );
    let pending_since = harness
        .step_execution("ChargeStep-1")
        .invocation
        .expect("invocation")
        .pending_since_micros;
    let time = queued.pending_since_time.expect("pending time");
    assert_eq!(
        time.seconds * 1_000_000 + i64::from(time.nanos / 1000),
        pending_since
    );
}
