// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Durable task queue tests (plan section 3.2): Claim replies for busy locks and transient
//! failures, retry clocks of queued attempts, and closing matching owners.

use std::sync::atomic::Ordering;
use std::time::Duration;

use dexzd_apply::ApplyEffect;
use dexzd_keyspace::RunId;
use dexzd_protocol::dex::{FlowStatus, RetryPolicy, StartFlowRequest, StepDurability, StepOptions};
use dexzd_protocol::dexzd::v1::{
    StepAttemptFailureKind, StepInvocationPhase, StepMethod, flow_command,
};
use dexzd_worker_dispatch::{ClaimResult, QueuedTaskRef};

use super::dispatch_tests::force_complete;
use super::{EffectHarness, is_attempt_failed, is_timers_fired, settings, start_request};
use crate::effects::{EffectsConfig, MethodRequest};
use crate::flow_coordination::FlowCoordinationEffectSink;

const LOCK_KEY: &str = "balance";

fn is_attempt_queued(command: &flow_command::Command) -> bool {
    matches!(command, flow_command::Command::StepAttemptQueued(_))
}

fn command_count(
    harness: &EffectHarness,
    predicate: impl Fn(&flow_command::Command) -> bool,
) -> usize {
    harness
        .flow_effect_operations
        .commands_matching(predicate)
        .len()
}

fn queued_refs(harness: &EffectHarness) -> Vec<QueuedTaskRef> {
    harness.dispatcher.queued_refs.lock().expect("lock").clone()
}

fn claim_results(harness: &EffectHarness) -> Vec<(u64, ClaimResult, Option<QueuedTaskRef>)> {
    harness
        .dispatcher
        .claim_replies
        .lock()
        .expect("lock")
        .iter()
        .map(|(claim_id, reply)| (*claim_id, reply.result, reply.next.clone()))
        .collect()
}

/// Returns the kinds of every recorded attempt failure, in order.
fn failure_kinds(harness: &EffectHarness) -> Vec<i32> {
    harness
        .flow_effect_operations
        .commands_matching(is_attempt_failed)
        .into_iter()
        .filter_map(|recorded| match recorded.command.command {
            Some(flow_command::Command::StepAttemptFailed(failure)) => Some(failure.kind),
            _ => None,
        })
        .collect()
}

/// Returns a Flow ID other than `flow_id` whose Flow lands in the same partition.
fn flow_id_in_partition_of(harness: &EffectHarness, flow_id: &str) -> String {
    let partition = harness.partition_of(flow_id);
    (2..10_000)
        .map(|index| format!("order-{index}"))
        .find(|candidate| candidate != flow_id && harness.partition_of(candidate) == partition)
        .expect("16 partitions leave a Flow ID in every partition")
}

/// Starts `flow_id` with its Execute method first, under `options`.
fn start_execute_flow(harness: &EffectHarness, flow_id: &str, options: StepOptions) -> RunId {
    let request = StartFlowRequest {
        step_options: Some(StepOptions {
            skip_wait_for: true,
            ..options
        }),
        ..start_request(flow_id)
    };
    harness.start_flow(request, settings())
}

/// Queues one locked and one unlocked row of the same queue and partition.
///
/// Returns the run of the locked row and both refs in queue order.
async fn queue_locked_and_unlocked_rows(
    harness: &EffectHarness,
) -> (RunId, QueuedTaskRef, QueuedTaskRef) {
    harness.rebuild_all_partitions().await;
    harness
        .dispatcher
        .refuse_dispatch
        .store(true, Ordering::SeqCst);
    let locked_run = start_execute_flow(
        harness,
        "order-1",
        StepOptions {
            execute_lock_attribute_keys: vec![LOCK_KEY.to_owned()],
            ..StepOptions::default()
        },
    );
    harness.settle().await;
    tokio::time::sleep(Duration::from_millis(10)).await;
    let other_flow_id = flow_id_in_partition_of(harness, "order-1");
    start_execute_flow(harness, &other_flow_id, StepOptions::default());
    harness.settle().await;
    let refs = queued_refs(harness);
    assert_eq!(refs.len(), 2, "both attempts are queued: {refs:?}");
    assert_eq!(refs[0].flow_id, "order-1");
    assert_eq!(refs[1].flow_id, other_flow_id);
    assert_eq!(refs[0].queue, refs[1].queue, "one queue holds both rows");
    assert!(
        refs[0].position < refs[1].position,
        "the locked row is first"
    );
    (locked_run, refs[0].clone(), refs[1].clone())
}

/// TB7: a claimed row whose locks are busy is answered "lock busy" with the next row after it,
/// stays with its holder until the locks are released, then goes back to matching and starts.
#[tokio::test(start_paused = true)]
async fn a_claimed_row_with_busy_locks_is_sent_back_after_the_release_and_starts() {
    let config = EffectsConfig {
        locked_invocation_retry_interval: Duration::from_secs(3600),
        ..EffectsConfig::default()
    };
    let harness = EffectHarness::with_config(super::DurableState::new(), config);
    let (locked_run, locked, unlocked) = queue_locked_and_unlocked_rows(&harness).await;
    harness
        .flow_effect_operations
        .busy_lock_keys
        .lock()
        .expect("lock")
        .insert(LOCK_KEY.to_owned());

    harness.dispatcher.claim(1, locked.clone());
    harness.settle().await;
    // A second Claim of the parked row finds it waiting for its locks.
    harness.dispatcher.claim(2, locked.clone());
    harness.settle().await;
    tokio::time::sleep(Duration::from_secs(30)).await;
    harness.settle().await;
    assert_eq!(
        claim_results(&harness),
        vec![
            (1, ClaimResult::LockBusy, Some(unlocked.clone())),
            (2, ClaimResult::LockBusy, Some(unlocked.clone())),
        ],
        "the next ref skips the parked row (plan D72)"
    );
    assert_eq!(
        queued_refs(&harness).len(),
        2,
        "the parked row is not sent back while its locks are busy"
    );
    assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 0);

    // Rows after the parked one still start.
    harness.dispatcher.claim(3, unlocked.clone());
    let unlocked_dispatch = harness.dispatcher.next_dispatch().await;
    assert_eq!(unlocked_dispatch.request.flow_id, unlocked.flow_id);

    harness
        .flow_effect_operations
        .busy_lock_keys
        .lock()
        .expect("lock")
        .clear();
    harness.runner.handle(
        &EffectHarness::flow("order-1"),
        &[ApplyEffect::LocksReleased {
            run_id: locked_run,
            lock_keys: vec![LOCK_KEY.to_owned()],
        }],
        true,
    );
    harness.settle().await;
    let refs = queued_refs(&harness);
    assert_eq!(refs.len(), 3, "the release sends the parked row back");
    assert_eq!(refs[2], locked);
    assert_eq!(
        command_count(&harness, is_attempt_queued),
        2,
        "the row is sent back without being queued again"
    );
    harness.dispatcher.claim(4, locked);
    let dispatch = harness.dispatcher.next_dispatch().await;
    assert_eq!(dispatch.request.flow_id, "order-1");
    assert_eq!(dispatch.context().attempt, 1);
    harness.settle().await;
    let results = claim_results(&harness);
    assert_eq!(results[2].1, ClaimResult::Started);
    assert_eq!(results[3].1, ClaimResult::Started);
    assert!(failure_kinds(&harness).is_empty());
    harness.shutdown().await;
}

/// A start that the runtime cannot propose answers "retry later" with the next row, then the
/// owner sends the row back after its backoff and a later Claim starts it.
#[tokio::test(start_paused = true)]
async fn an_unavailable_start_answers_retry_later_and_sends_the_row_back_after_a_backoff() {
    check_retry_later(RetryLaterCause::StartUnavailable).await;
}

/// A Claim whose row cannot be checked answers "retry later" the same way.
#[tokio::test(start_paused = true)]
async fn an_unreadable_claimed_row_answers_retry_later_and_sends_the_row_back_after_a_backoff() {
    check_retry_later(RetryLaterCause::RowUnreadable).await;
}

#[derive(Clone, Copy, Debug)]
enum RetryLaterCause {
    StartUnavailable,
    RowUnreadable,
}

async fn check_retry_later(cause: RetryLaterCause) {
    let harness = EffectHarness::new();
    let (_, first, second) = queue_locked_and_unlocked_rows(&harness).await;
    match cause {
        RetryLaterCause::StartUnavailable => harness
            .flow_effect_operations
            .unavailable_proposals
            .store(1, Ordering::SeqCst),
        RetryLaterCause::RowUnreadable => harness
            .flow_effect_operations
            .flow_state_reader_failures
            .store(1, Ordering::SeqCst),
    }
    harness.dispatcher.claim(1, first.clone());
    harness.settle().await;
    assert_eq!(
        claim_results(&harness),
        vec![(1, ClaimResult::RetryLater, Some(second.clone()))],
        "{cause:?}: the next ref skips the held row"
    );
    assert_eq!(
        queued_refs(&harness).len(),
        2,
        "{cause:?}: the row is held through the backoff"
    );
    // A Claim during the backoff finds the row held.
    harness.dispatcher.claim(2, first.clone());
    harness.settle().await;
    assert_eq!(claim_results(&harness)[1].1, ClaimResult::Busy, "{cause:?}");

    tokio::time::sleep(Duration::from_secs(1)).await;
    harness.settle().await;
    let refs = queued_refs(&harness);
    assert_eq!(refs.len(), 3, "{cause:?}: the owner sends the row back");
    assert_eq!(refs[2], first);
    assert_eq!(command_count(&harness, is_attempt_queued), 2, "{cause:?}");
    harness.dispatcher.claim(3, first);
    let dispatch = harness.dispatcher.next_dispatch().await;
    assert_eq!(dispatch.request.flow_id, "order-1");
    assert_eq!(dispatch.context().attempt, 1);
    harness.settle().await;
    assert_eq!(claim_results(&harness)[2].1, ClaimResult::Started);
    assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 1);
    assert!(failure_kinds(&harness).is_empty());
    harness.shutdown().await;
}

/// A pending attempt that a closing matching owner refused stays pending without any command,
/// and the next owner's rebuild dispatches it.
#[tokio::test(start_paused = true)]
async fn an_attempt_refused_by_a_closing_owner_stays_pending_and_the_rebuild_dispatches_it() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness.dispatcher.is_closing.store(true, Ordering::SeqCst);
    let run_id = harness.start_flow(start_request("order-1"), settings());
    harness.settle().await;
    tokio::time::sleep(Duration::from_secs(60)).await;
    harness.settle().await;
    let commands_after_start = harness
        .flow_effect_operations
        .commands
        .lock()
        .expect("lock")
        .len();
    assert_eq!(commands_after_start, 1, "only the start committed");
    let invocation = harness
        .step_execution("order-1", run_id, "ChargeStep-1")
        .invocation
        .expect("invocation");
    assert_eq!(invocation.phase, StepInvocationPhase::Pending as i32);
    assert_eq!(invocation.attempt, 1);

    harness.dispatcher.is_closing.store(false, Ordering::SeqCst);
    harness.rebuild_all_partitions().await;
    let dispatch = harness.dispatcher.next_dispatch().await;
    assert_eq!(dispatch.context().step_execution_id, "ChargeStep-1");
    assert_eq!(dispatch.context().attempt, 1);
    harness.settle().await;
    assert_eq!(command_count(&harness, is_attempt_queued), 0);
    assert!(failure_kinds(&harness).is_empty());
    harness.shutdown().await;
}

const RETRY_TOTAL_DURATION_SECONDS: i32 = 10;
/// Longer than the retry policy's total duration and the method timeout.
const QUEUED_DURATION: Duration = Duration::from_secs(30);

fn retry_options(durability: StepDurability, execute_timeout_seconds: i32) -> StepOptions {
    StepOptions {
        execute_durability_override: durability as i32,
        execute_timeout_seconds,
        execute_retry_policy: Some(RetryPolicy {
            initial_interval_seconds: 1,
            backoff_coefficient: 1.0,
            maximum_attempts: 5,
            total_duration_seconds: RETRY_TOTAL_DURATION_SECONDS,
            ..RetryPolicy::default()
        }),
        ..StepOptions::default()
    }
}

fn is_execute(request: &MethodRequest) -> bool {
    matches!(request, MethodRequest::Execute(_))
}

/// TB4: a retry attempt queued past the retry policy's total duration still runs and completes.
#[tokio::test(start_paused = true)]
async fn a_retry_queued_past_the_total_duration_runs_and_completes() {
    for durability in [StepDurability::Sync, StepDurability::Async] {
        check_queued_retry_past_total_duration(durability, RetryOutcome::Completes).await;
    }
}

/// TB4 variant: when that late retry fails, no further attempt is scheduled.
#[tokio::test(start_paused = true)]
async fn a_failed_retry_queued_past_the_total_duration_ends_the_retries() {
    for durability in [StepDurability::Sync, StepDurability::Async] {
        check_queued_retry_past_total_duration(durability, RetryOutcome::Fails).await;
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum RetryOutcome {
    Completes,
    Fails,
}

async fn check_queued_retry_past_total_duration(durability: StepDurability, outcome: RetryOutcome) {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let run_id = start_execute_flow(&harness, "order-1", retry_options(durability, 0));
    let first = harness.dispatcher.next_dispatch().await;
    assert!(is_execute(first.method_request()));
    assert_eq!(first.context().attempt, 1);
    harness
        .dispatcher
        .refuse_dispatch
        .store(true, Ordering::SeqCst);
    first.fail_with_worker_error("first attempt fails");
    harness.settle().await;
    tokio::time::sleep(Duration::from_millis(1500)).await;
    harness.settle().await;
    let refs = queued_refs(&harness);
    assert_eq!(refs.len(), 1, "{durability:?}: attempt 2 is queued");
    assert_eq!(refs[0].position.attempt, 2);
    let timers_before = command_count(&harness, is_timers_fired);
    let failures_before = failure_kinds(&harness);
    assert_eq!(
        failures_before,
        vec![StepAttemptFailureKind::WorkerError as i32]
    );

    tokio::time::sleep(QUEUED_DURATION).await;
    harness.settle().await;
    assert_eq!(
        command_count(&harness, is_timers_fired),
        timers_before,
        "{durability:?}: no timer fires while the retry is queued"
    );
    assert_eq!(
        failure_kinds(&harness),
        failures_before,
        "{durability:?}: queueing past the deadline is not a failure"
    );
    harness.dispatcher.claim(1, refs[0].clone());
    let second = harness.dispatcher.next_dispatch().await;
    assert_eq!(second.context().attempt, 2);
    match outcome {
        RetryOutcome::Completes => {
            second.complete_execute(force_complete());
            harness.settle().await;
            assert_eq!(
                failure_kinds(&harness),
                failures_before,
                "{durability:?}: no RetryDeadline failure"
            );
            assert_eq!(
                harness.run_record("order-1", run_id).status,
                FlowStatus::Completed as i32
            );
        }
        RetryOutcome::Fails => {
            second.fail_with_worker_error("second attempt fails");
            harness.settle().await;
            tokio::time::sleep(Duration::from_secs(5)).await;
            harness.settle().await;
            assert_eq!(
                failure_kinds(&harness),
                vec![StepAttemptFailureKind::WorkerError as i32; 2],
                "{durability:?}: the late attempt fails with its own kind"
            );
            assert_eq!(
                queued_refs(&harness).len(),
                1,
                "{durability:?}: no attempt 3"
            );
            assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 2);
            assert_eq!(
                harness.run_record("order-1", run_id).status,
                FlowStatus::Failed as i32
            );
        }
    }
    harness.shutdown().await;
}

/// TB4 and TB4c (plan D71): the retry clock starts when attempt 1 is first handed out, so an
/// attempt 1 queued past the total duration and the method timeout runs, fails fast, and retries.
#[tokio::test(start_paused = true)]
async fn the_retry_clock_starts_at_the_first_hand_out_not_while_queued() {
    for durability in [StepDurability::Sync, StepDurability::Async] {
        let harness = EffectHarness::new();
        harness.rebuild_all_partitions().await;
        harness
            .dispatcher
            .refuse_dispatch
            .store(true, Ordering::SeqCst);
        let run_id = start_execute_flow(&harness, "order-1", retry_options(durability, 5));
        harness.settle().await;
        tokio::time::sleep(QUEUED_DURATION).await;
        harness.settle().await;
        assert!(
            failure_kinds(&harness).is_empty(),
            "{durability:?}: no timeout or retry deadline while queued"
        );
        let refs = queued_refs(&harness);
        assert_eq!(refs.len(), 1);
        harness
            .dispatcher
            .refuse_dispatch
            .store(false, Ordering::SeqCst);
        harness.dispatcher.claim(1, refs[0].clone());
        let first = harness.dispatcher.next_dispatch().await;
        assert_eq!(first.context().attempt, 1);
        first.fail_with_worker_error("fails fast");
        let second = harness.dispatcher.next_dispatch().await;
        assert_eq!(
            second.context().attempt,
            2,
            "{durability:?}: the queued time does not count against the total duration"
        );
        second.complete_execute(force_complete());
        harness.settle().await;
        assert_eq!(
            failure_kinds(&harness),
            vec![StepAttemptFailureKind::WorkerError as i32]
        );
        assert_eq!(
            harness.run_record("order-1", run_id).status,
            FlowStatus::Completed as i32
        );
        harness.shutdown().await;
    }
}

/// TB4 control: the retry clock does run from the hand-out, so an attempt 1 that runs past the
/// total duration before it fails is not retried.
#[tokio::test(start_paused = true)]
async fn an_attempt_that_runs_past_the_total_duration_is_not_retried() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let run_id = start_execute_flow(&harness, "order-1", retry_options(StepDurability::Sync, 60));
    let first = harness.dispatcher.next_dispatch().await;
    tokio::time::sleep(Duration::from_secs(12)).await;
    first.fail_with_worker_error("fails late");
    harness.settle().await;
    tokio::time::sleep(Duration::from_secs(5)).await;
    harness.settle().await;
    assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 1);
    assert_eq!(
        harness.run_record("order-1", run_id).status,
        FlowStatus::Failed as i32
    );
    let invocation = harness
        .step_execution("order-1", run_id, "ChargeStep-1")
        .invocation;
    assert!(
        invocation.is_none_or(|invocation| invocation.method != StepMethod::Execute as i32
            || invocation.phase != StepInvocationPhase::Backoff as i32),
        "no retry is scheduled"
    );
    harness.shutdown().await;
}
