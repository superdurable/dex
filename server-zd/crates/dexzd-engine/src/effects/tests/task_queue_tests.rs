// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Durable task queue tests: Claim replies, retry clocks of queued attempts, and closing owners.

use std::collections::BTreeSet;
use std::sync::Arc;
use std::sync::atomic::Ordering;
use std::time::Duration;

use dexzd_apply::ApplyEffect;
use dexzd_keyspace::dispatch_queue_keys::{
    DispatchQueueEntry, DispatchQueueName, DispatchQueuePosition, dispatch_queue_key,
};
use dexzd_keyspace::{ColumnFamily, FlowHash, PartitionId, RunId, TenantId};
use dexzd_protocol::dex::{
    FlowStatus, FlowTimeoutPolicy, PollWorkerTaskRejectReason, RetryPolicy, StartFlowRequest,
    StepDurability, StepOptions, StopFlowRequest, StopType,
};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, FlowStopCommand, StepAttemptFailureKind, StepAttemptReturnReason,
    StepInvocationPhase, StepMethod, flow_command,
};
use dexzd_worker_dispatch::{ClaimResult, QueuedTaskRef};
use prometheus_client::metrics::gauge::Gauge;

use super::dispatch_tests::force_complete;
use super::handout_tests::{returns, started_task_ids};
use super::{
    EffectHarness, MemoryState, is_attempt_failed, is_timers_fired, settings, start_request,
};
use crate::effects::dispatch::{AttemptEntries, ClaimHolder, HandoutProgress};
use crate::effects::method_request::MethodAttemptKey;
use crate::effects::task_queue::{TaskQueueMemory, first_ordinary_pending_since};
use crate::effects::{EffectsConfig, MethodRequest};
use crate::flow_coordination::{CommitUrgency, FlowCoordinationEffectSink};

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

/// Queues a locked row, then an unlocked row, in one queue and partition.
///
/// Returns the locked row's run and both refs in queue order.
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

/// TB7: a row claimed with busy locks answers "lock busy", then returns to matching on release.
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

/// An unavailable start answers "retry later"; the owner sends the row back after a backoff.
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

/// A closing owner's refusal writes nothing; the next rebuild dispatches the pending attempt.
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

/// TB4 and TB4c (plan D71): queue time before the first hand-out does not count against retries.
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

/// TB4 control: an attempt that runs past the total duration before failing is not retried.
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

/// A Claim takes an unheld attempt, gets "busy" from a hand-out, and "lock busy" while parked.
#[test]
fn claims_are_classified_by_the_holder_of_the_attempt_entry() {
    let key = MethodAttemptKey {
        run_id: RunId([3; 16]),
        run_attempt: 1,
        step_execution_id: "ChargeStep-1".to_owned(),
        method: StepMethod::Execute as i32,
        attempt: 1,
    };
    let mut entries = AttemptEntries::default();
    let ClaimHolder::Claimed {
        generation: claimed,
    } = entries.claim(&key)
    else {
        panic!("a Claim takes an attempt without an entry");
    };
    assert!(entries.is_held(&key));
    assert_eq!(entries.register(&key), None, "the Claim holds the entry");
    assert_eq!(entries.claim(&key), ClaimHolder::Busy);
    // The restart decision is checked through later Claims.
    drop(entries.finish_handout(
        &key,
        claimed,
        Some((EffectHarness::flow("order-1"), HandoutProgress::default())),
    ));
    assert_eq!(entries.claim(&key), ClaimHolder::Parked);
    assert!(entries.is_held(&key), "seeks skip a parked row");
    let unparked = entries.release_locks(key.run_id);
    assert_eq!(unparked.len(), 1);
    let (unparked_key, _, _, unparked_generation) = &unparked[0];
    assert_eq!(unparked_key, &key);
    assert_eq!(
        entries.claim(&key),
        ClaimHolder::Busy,
        "the woken hand-out holds the entry"
    );
    // A finished hand-out of an older generation leaves the entry alone.
    drop(entries.finish_handout(&key, claimed, None));
    assert!(entries.is_held(&key));
    drop(entries.finish_handout(&key, *unparked_generation, None));
    assert!(!entries.is_held(&key));
    assert!(matches!(
        entries.claim(&key),
        ClaimHolder::Claimed { generation } if generation > *unparked_generation
    ));
}

/// TB4d (plan D54, D74): a declined retry queued past the total duration still completes.
#[tokio::test(start_paused = true)]
async fn a_declined_retry_waits_past_the_total_duration_and_completes() {
    for durability in [StepDurability::Sync, StepDurability::Async] {
        let harness = EffectHarness::new();
        harness.rebuild_all_partitions().await;
        let run_id = start_execute_flow(&harness, "order-1", retry_options(durability, 0));
        let first = harness.dispatcher.next_dispatch().await;
        first.fail_with_worker_error("first attempt fails");
        let second = harness.dispatcher.next_dispatch().await;
        assert_eq!(second.context().attempt, 2);
        harness
            .dispatcher
            .refuse_dispatch
            .store(true, Ordering::SeqCst);
        second.reject(PollWorkerTaskRejectReason::UnknownStep, true);
        harness.settle().await;
        let refs = queued_refs(&harness);
        assert_eq!(
            refs.len(),
            1,
            "{durability:?}: the declined retry is queued"
        );
        assert_eq!(refs[0].position.attempt, 2);
        let commands_while_queued = harness
            .flow_effect_operations
            .commands
            .lock()
            .expect("lock")
            .len();
        tokio::time::sleep(QUEUED_DURATION).await;
        harness.settle().await;
        assert_eq!(
            harness
                .flow_effect_operations
                .commands
                .lock()
                .expect("lock")
                .len(),
            commands_while_queued,
            "{durability:?}: no command, such as a clock pause or a failure, while queued"
        );
        harness.dispatcher.claim(1, refs[0].clone());
        let third = harness.dispatcher.next_dispatch().await;
        assert_eq!(
            third.context().attempt,
            2,
            "{durability:?}: a return uses no retry"
        );
        third.complete_execute(force_complete());
        harness.settle().await;
        assert_eq!(
            failure_kinds(&harness),
            vec![StepAttemptFailureKind::WorkerError as i32],
            "{durability:?}: no RetryDeadline failure"
        );
        assert_eq!(
            harness.run_record("order-1", run_id).status,
            FlowStatus::Completed as i32
        );
        harness.shutdown().await;
    }
}

/// TB24 (plan D69): a retry queues at its backoff expiry, behind rows queued earlier.
#[tokio::test(start_paused = true)]
async fn a_queued_retry_goes_behind_the_backlog_queued_before_its_backoff_ended() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let first_id = "order-1";
    let retried_id = flow_id_in_partition_of(&harness, first_id);
    let later_id = (2..10_000)
        .map(|index| format!("order-{index}"))
        .find(|candidate| {
            candidate != &retried_id
                && harness.partition_of(candidate) == harness.partition_of(first_id)
        })
        .expect("16 partitions leave a third Flow ID in the partition");
    let refuse = |is_refused: bool| {
        harness
            .dispatcher
            .refuse_dispatch
            .store(is_refused, Ordering::SeqCst);
    };
    refuse(true);
    start_execute_flow(&harness, first_id, StepOptions::default());
    harness.settle().await;
    refuse(false);
    start_execute_flow(
        &harness,
        &retried_id,
        retry_options(StepDurability::Sync, 0),
    );
    let failing = harness.dispatcher.next_dispatch().await;
    refuse(true);
    tokio::time::sleep(Duration::from_millis(500)).await;
    start_execute_flow(&harness, &later_id, StepOptions::default());
    harness.settle().await;
    failing.fail_with_worker_error("first attempt fails");
    harness.settle().await;
    tokio::time::sleep(Duration::from_millis(1500)).await;
    harness.settle().await;

    let refs = queued_refs(&harness);
    let flow_ids: Vec<_> = refs.iter().map(|task| task.flow_id.clone()).collect();
    assert_eq!(
        flow_ids,
        vec![first_id.to_owned(), later_id.clone(), retried_id.clone()]
    );
    assert_eq!(refs[2].position.attempt, 2);
    assert!(refs.windows(2).all(|pair| pair[0].queue == pair[1].queue));
    assert!(
        refs[0].position < refs[1].position && refs[1].position < refs[2].position,
        "the retry sorts behind the row queued before its backoff ended"
    );
    harness.dispatcher.claim(1, refs[0].clone());
    harness.dispatcher.next_dispatch().await;
    harness.dispatcher.claim(2, refs[1].clone());
    harness.dispatcher.next_dispatch().await;
    harness.settle().await;
    assert_eq!(
        claim_results(&harness),
        vec![
            (1, ClaimResult::Started, Some(refs[1].clone())),
            (2, ClaimResult::Started, Some(refs[2].clone())),
        ]
    );
    harness.shutdown().await;
}

/// TB4 (plan D71): a declined attempt 1 restarts its retry clock at its next hand-out.
#[tokio::test(start_paused = true)]
async fn a_declined_first_attempt_queued_past_the_total_duration_still_retries() {
    for durability in [StepDurability::Sync, StepDurability::Async] {
        let harness = EffectHarness::new();
        harness.rebuild_all_partitions().await;
        let run_id = start_execute_flow(&harness, "order-1", retry_options(durability, 0));
        let declined = harness.dispatcher.next_dispatch().await;
        assert_eq!(declined.context().attempt, 1);
        harness
            .dispatcher
            .refuse_dispatch
            .store(true, Ordering::SeqCst);
        declined.reject(PollWorkerTaskRejectReason::UnknownStep, true);
        harness.settle().await;
        let refs = queued_refs(&harness);
        assert_eq!(
            refs.len(),
            1,
            "{durability:?}: the declined attempt is queued"
        );
        assert_eq!(refs[0].position.attempt, 1);
        tokio::time::sleep(QUEUED_DURATION).await;
        harness.settle().await;
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
            "{durability:?}: the declined hand-out does not start the retry clock"
        );
        second.complete_execute(force_complete());
        harness.settle().await;
        assert_eq!(
            harness.run_record("order-1", run_id).status,
            FlowStatus::Completed as i32
        );
        harness.shutdown().await;
    }
}

fn has_queue_row(harness: &EffectHarness, task: &QueuedTaskRef) -> bool {
    let key = dispatch_queue_key(&DispatchQueueEntry {
        partition: task.partition,
        queue: task.queue.clone(),
        position: task.position.clone(),
    });
    harness
        .flow_effect_operations
        .snapshot()
        .rows
        .contains_key(&(ColumnFamily::PartitionIndex, key))
}

/// Queues the Execute attempt of `request` and returns its ref.
async fn queue_first_attempt(
    harness: &EffectHarness,
    request: StartFlowRequest,
    timeout_policy: FlowTimeoutPolicy,
) -> (RunId, QueuedTaskRef) {
    harness.rebuild_all_partitions().await;
    harness
        .dispatcher
        .refuse_dispatch
        .store(true, Ordering::SeqCst);
    let run_id = harness.start_flow_with_timeout_policy(request, settings(), timeout_policy);
    harness.settle().await;
    let refs = queued_refs(harness);
    assert_eq!(refs.len(), 1);
    assert!(has_queue_row(harness, &refs[0]));
    (run_id, refs[0].clone())
}

/// TB6: stopping a Flow deletes its queued row, and a later Claim of it is stale.
#[tokio::test(start_paused = true)]
async fn stopping_a_flow_deletes_its_queued_row() {
    let harness = EffectHarness::new();
    let (run_id, task) =
        queue_first_attempt(&harness, start_request("order-1"), FlowTimeoutPolicy::Fail).await;
    let stop = FlowCommand {
        flow_id: "order-1".to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        command: Some(flow_command::Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                flow_id: "order-1".to_owned(),
                stop_type: StopType::Terminate as i32,
                ..StopFlowRequest::default()
            }),
        })),
        ..FlowCommand::default()
    };
    harness
        .flow_effect_operations
        .commit(&EffectHarness::flow("order-1"), stop, CommitUrgency::Prompt)
        .expect("stop commits");
    harness.settle().await;
    assert!(!has_queue_row(&harness, &task));
    harness.dispatcher.claim(1, task);
    harness.settle().await;
    assert_eq!(claim_results(&harness)[0].1, ClaimResult::Stale);
    assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 0);
    harness.shutdown().await;
}

/// TB4b: a Flow timeout with the FAIL policy closes a Flow whose Step never left the queue.
#[tokio::test(start_paused = true)]
async fn a_flow_timeout_closes_a_flow_whose_step_waits_in_the_queue() {
    let harness = EffectHarness::new();
    let request = StartFlowRequest {
        flow_timeout_seconds: 5,
        ..start_request("order-1")
    };
    let (run_id, task) = queue_first_attempt(&harness, request, FlowTimeoutPolicy::Fail).await;
    tokio::time::sleep(Duration::from_secs(6)).await;
    harness.settle().await;
    assert_eq!(
        harness.run_record("order-1", run_id).status,
        FlowStatus::Failed as i32
    );
    assert!(!has_queue_row(&harness, &task), "closing deletes the row");
    assert!(failure_kinds(&harness).is_empty(), "queueing never failed");
    harness.shutdown().await;
}

/// TB4b: with the HANDLER policy the Flow keeps running and its timeout handler queues too.
#[tokio::test(start_paused = true)]
async fn a_flow_timeout_handler_queues_ahead_of_the_waiting_step() {
    let harness = EffectHarness::new();
    let request = StartFlowRequest {
        flow_timeout_seconds: 5,
        ..start_request("order-1")
    };
    let (run_id, task) = queue_first_attempt(&harness, request, FlowTimeoutPolicy::Handler).await;
    tokio::time::sleep(Duration::from_secs(6)).await;
    harness.settle().await;
    assert_eq!(
        harness.run_record("order-1", run_id).status,
        FlowStatus::Running as i32
    );
    let refs = queued_refs(&harness);
    let handler = refs
        .iter()
        .find(|queued| {
            queued
                .position
                .step_execution_id
                .starts_with("sys:timeout_handler")
        })
        .unwrap_or_else(|| panic!("the timeout handler is queued: {refs:?}"));
    assert!(
        handler.position.priority < task.position.priority,
        "the handler sorts ahead (plan D16): {handler:?}"
    );
    assert!(failure_kinds(&harness).is_empty());
    harness.shutdown().await;
}

/// A held row whose committed read fails retries after a backoff and stops at shutdown.
#[tokio::test(start_paused = true)]
async fn a_failing_committed_read_backs_off_and_the_holder_stops_at_shutdown() {
    let harness = EffectHarness::new();
    let (_, first, second) = queue_locked_and_unlocked_rows(&harness).await;
    harness
        .flow_effect_operations
        .unavailable_proposals
        .store(1, Ordering::SeqCst);
    harness.dispatcher.claim(1, first);
    harness.settle().await;
    assert_eq!(
        claim_results(&harness),
        vec![(1, ClaimResult::RetryLater, Some(second))]
    );
    harness
        .flow_effect_operations
        .committed_reader_failures
        .store(usize::MAX, Ordering::SeqCst);
    let reads_before = harness
        .flow_effect_operations
        .committed_reads
        .load(Ordering::SeqCst);
    tokio::time::sleep(Duration::from_secs(10)).await;
    harness.settle().await;
    let reads = harness
        .flow_effect_operations
        .committed_reads
        .load(Ordering::SeqCst)
        - reads_before;
    assert!(reads >= 1, "the holder retried the committed read");
    assert!(
        reads <= 10,
        "{reads} committed reads in 10 s: retries back off"
    );
    assert_eq!(
        queued_refs(&harness).len(),
        2,
        "an unreadable row is not sent"
    );
    tokio::time::timeout(Duration::from_secs(60), harness.shutdown())
        .await
        .expect("the holder stops at shutdown");
}

/// TB18: rebuild Heads seeks once per partition and once per nonempty queue, not per row.
#[tokio::test(start_paused = true)]
async fn rebuild_heads_seek_once_per_queue_however_long_the_backlog() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness
        .dispatcher
        .refuse_dispatch
        .store(true, Ordering::SeqCst);
    for index in 0..48 {
        start_execute_flow(&harness, &format!("order-{index}"), StepOptions::default());
    }
    harness.settle().await;
    let refs = queued_refs(&harness);
    assert_eq!(refs.len(), 48, "every attempt is queued");
    let nonempty_queues: BTreeSet<_> = refs
        .iter()
        .map(|task| (task.partition, task.queue.clone()))
        .collect();
    let seeks = Arc::clone(&harness.flow_effect_operations.committed_queue_seeks);
    seeks.store(0, Ordering::SeqCst);
    harness
        .dispatcher
        .rebuilt_heads
        .lock()
        .expect("lock")
        .clear();

    harness.rebuild_all_partitions().await;
    let heads = harness
        .dispatcher
        .rebuilt_heads
        .lock()
        .expect("lock")
        .clone();
    assert_eq!(
        heads.len(),
        nonempty_queues.len(),
        "one head per nonempty queue"
    );
    assert_eq!(
        seeks.load(Ordering::SeqCst),
        16 + nonempty_queues.len(),
        "one seek per partition plus one per nonempty queue"
    );
    assert_eq!(
        command_count(&harness, is_attempt_queued),
        48,
        "rebuild proposes no StepAttemptQueued"
    );
    harness.shutdown().await;
}

/// The entries gauge counts every committed change once across a rebuild's snapshot.
#[test]
fn the_row_count_counts_each_change_once_across_a_snapshot() {
    let mut memory = TaskQueueMemory::default();
    let partition = PartitionId(3);
    // Applied before the count began, so the snapshot holds it.
    memory.add_entries(partition, Some((1, 5)), 1);
    let (total, first_count) = memory.begin_row_count(partition);
    assert_eq!(total, 0);
    // Batch 6 is in the snapshot taken at batch 7; batch 8 is not.
    memory.add_entries(partition, Some((1, 6)), 1);
    memory.add_entries(partition, Some((1, 8)), -1);
    assert_eq!(
        memory.finish_row_count(partition, first_count, 10, Some((1, 7))),
        Some(9)
    );
    assert_eq!(
        memory.add_entries(partition, Some((1, 7)), 1),
        9,
        "a late change the snapshot holds counts once"
    );
    assert_eq!(
        memory.add_entries(partition, Some((2, 1)), 2),
        11,
        "a later incarnation's batch counts"
    );
    let (_, other_count) = memory.begin_row_count(PartitionId(4));
    memory.add_entries(PartitionId(4), Some((2, 2)), 5);
    assert_eq!(
        memory.finish_row_count(PartitionId(4), other_count, 0, Some((2, 1))),
        Some(16)
    );
    let (total, stale_count) = memory.begin_row_count(partition);
    assert_eq!(total, 5, "an uncounted partition is left out");
    let (_, current_count) = memory.begin_row_count(partition);
    assert_eq!(
        memory.finish_row_count(partition, stale_count, 7, Some((2, 3))),
        None,
        "an older count that finishes late changes nothing"
    );
    assert_eq!(
        memory.finish_row_count(partition, current_count, 4, Some((2, 3))),
        Some(9),
        "a recount replaces the partition's share"
    );
}

/// A Claim that matching stopped tracking, as after a partition removal, starts nothing.
#[tokio::test(start_paused = true)]
async fn an_untracked_claim_starts_nothing_and_gets_no_reply() {
    let harness = EffectHarness::new();
    let (_, first, _) = queue_locked_and_unlocked_rows(&harness).await;
    harness
        .dispatcher
        .partition_removals
        .untracked_claim_ids
        .lock()
        .expect("lock")
        .insert(1);
    harness.dispatcher.claim(1, first);
    harness.settle().await;
    assert!(
        claim_results(&harness).is_empty(),
        "no reply to an untracked Claim"
    );
    assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 0);
    harness.shutdown().await;
}

fn queue_entry(priority: u8, pending_since_micros: i64, step_type: &str) -> DispatchQueueEntry {
    DispatchQueueEntry {
        partition: PartitionId(2),
        queue: DispatchQueueName {
            tenant_id: TenantId(7),
            flow_type: "OrderFlow".to_owned(),
            step_type: step_type.to_owned(),
        },
        position: DispatchQueuePosition {
            priority,
            pending_since_micros,
            flow_hash: FlowHash(9),
            run_id: RunId([3; 16]),
            method: 2,
            attempt: 1,
            step_execution_id: format!("{step_type}-{pending_since_micros}"),
        },
    }
}

/// The ordinary backlog's age comes from the first priority-1 row, not the priority-0 head.
#[test]
fn the_ordinary_pending_time_skips_priority_zero_rows_and_other_queues() {
    let mut state = MemoryState::default();
    for entry in [
        queue_entry(0, 50, "ChargeStep"),
        queue_entry(1, -20, "ChargeStep"),
        queue_entry(1, 30, "ChargeStep"),
        queue_entry(1, -90, "RefundStep"),
        queue_entry(0, 10, "ShipStep"),
    ] {
        state.rows.insert(
            (ColumnFamily::PartitionIndex, dispatch_queue_key(&entry)),
            Vec::new(),
        );
    }
    let ordinary = |step_type: &str| {
        let entry = queue_entry(1, 0, step_type);
        first_ordinary_pending_since(&state, u64::MAX, entry.partition, &entry.queue)
            .expect("the read succeeds")
    };
    assert_eq!(ordinary("ChargeStep"), Some(-20));
    assert_eq!(ordinary("ShipStep"), None, "only priority-0 rows");
    assert_eq!(ordinary("RefundStep"), Some(-90));
}

/// The entries gauge is set with each count update, so a stale total never overwrites a newer one.
#[test]
fn the_entries_gauge_publishes_every_total_with_its_count_update() {
    let partition = PartitionId(3);
    for is_start_before_count in [false, true] {
        let gauge = Gauge::default();
        let mut memory = TaskQueueMemory::new(gauge.clone());
        let (_, count) = memory.begin_row_count(partition);
        // The snapshot at batch 10 holds one row; its start commits at batch 11.
        if is_start_before_count {
            memory.add_entries(partition, Some((1, 11)), -1);
            assert_eq!(gauge.get(), 0, "an uncounted partition is left out");
        }
        memory.finish_row_count(partition, count, 1, Some((1, 10)));
        if !is_start_before_count {
            assert_eq!(gauge.get(), 1);
            memory.add_entries(partition, Some((1, 11)), -1);
        }
        assert_eq!(gauge.get(), 0, "start first: {is_start_before_count}");
    }
}

/// D78: a row count that cannot finish delays no rebuild, Heads, or hand-out.
#[tokio::test(start_paused = true)]
async fn a_blocked_row_count_delays_no_other_partitions_heads() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness.settle().await;
    harness
        .dispatcher
        .refuse_dispatch
        .store(true, Ordering::SeqCst);
    for index in 0..48 {
        start_execute_flow(&harness, &format!("order-{index}"), StepOptions::default());
    }
    harness.settle().await;
    let entries = &harness.effect_context().metrics.step_task_queue_entries;
    assert_eq!(entries.get(), 48);
    let nonempty: BTreeSet<_> = queued_refs(&harness)
        .iter()
        .map(|task| task.partition)
        .collect();
    harness
        .dispatcher
        .rebuilt_heads
        .lock()
        .expect("lock")
        .clear();

    let row_counts = harness.flow_effect_operations.row_count_gate.close();
    harness.rebuild_all_partitions().await;
    harness
        .flow_effect_operations
        .row_count_gate
        .wait_for_held_reads(1);
    let rebuilt: BTreeSet<_> = harness
        .dispatcher
        .rebuilt_heads
        .lock()
        .expect("lock")
        .iter()
        .map(|task| task.partition)
        .collect();
    assert_eq!(
        rebuilt, nonempty,
        "every partition's Heads arrived while a count is held"
    );
    assert_eq!(
        entries.get(),
        0,
        "uncounted partitions are left out of the gauge"
    );

    row_counts.open_and_wait_for_reads();
    harness.settle().await;
    assert_eq!(entries.get(), 48, "every count finished after the release");
    harness.shutdown().await;
}

/// Queues one SYNC Execute attempt with no lock and returns its ref.
async fn queue_one_row(harness: &EffectHarness) -> QueuedTaskRef {
    harness.rebuild_all_partitions().await;
    harness.settle().await;
    harness
        .dispatcher
        .refuse_dispatch
        .store(true, Ordering::SeqCst);
    start_execute_flow(harness, "order-1", StepOptions::default());
    harness.settle().await;
    let refs = queued_refs(harness);
    assert_eq!(refs.len(), 1, "the attempt is queued");
    refs[0].clone()
}

/// A Claim whose partition is removed while it waits for a read permit starts nothing.
#[tokio::test(start_paused = true)]
async fn a_claim_removed_while_waiting_for_a_read_permit_starts_nothing() {
    let harness = EffectHarness::with_config(
        super::DurableState::new(),
        EffectsConfig {
            max_blocking_state_reads: 1,
            heads_resync_interval: Duration::ZERO,
            ..EffectsConfig::default()
        },
    );
    let task = queue_one_row(&harness).await;
    let permit = Arc::clone(&harness.effect_context().blocking_read_slots)
        .acquire_owned()
        .await
        .expect("permit");
    harness.dispatcher.claim(1, task.clone());
    harness.yield_to_tasks().await;
    harness.dispatcher.remove_partition(task.partition, &[1]);
    drop(permit);
    harness.settle().await;
    assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 0);
    assert!(
        claim_results(&harness).is_empty(),
        "a removed Claim gets no reply"
    );
    assert!(
        command_count(&harness, is_attempt_started) == 0,
        "no start commits"
    );
    harness.shutdown().await;
}

/// A Claim whose partition is removed while its pending check reads the store starts nothing.
#[tokio::test(start_paused = true)]
async fn a_claim_removed_while_its_check_reads_the_store_starts_nothing() {
    let harness = EffectHarness::new();
    let task = queue_one_row(&harness).await;
    let reads = harness.flow_effect_operations.state_read_gate.close();
    harness.dispatcher.claim(1, task.clone());
    harness.yield_to_tasks().await;
    harness
        .flow_effect_operations
        .state_read_gate
        .wait_for_held_reads(1);
    harness.dispatcher.remove_partition(task.partition, &[1]);
    reads.open_and_wait_for_reads();
    harness.settle().await;
    assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 0);
    assert!(
        claim_results(&harness).is_empty(),
        "a removed Claim gets no reply"
    );
    assert!(
        command_count(&harness, is_attempt_started) == 0,
        "no start commits"
    );
    harness.shutdown().await;
}

/// A Claim whose partition is removed right before its start starts nothing and keeps its row.
#[tokio::test(start_paused = true)]
async fn a_claim_removed_right_before_its_start_starts_nothing() {
    let harness = EffectHarness::new();
    let task = queue_one_row(&harness).await;
    let removals = Arc::clone(&harness.dispatcher.partition_removals);
    removals
        .should_remove_before_start
        .store(true, Ordering::SeqCst);
    harness.dispatcher.claim(1, task.clone());
    harness.settle().await;
    assert!(
        !removals.should_remove_before_start.load(Ordering::SeqCst),
        "the partition was removed during the hand-out"
    );
    assert_eq!(command_count(&harness, is_attempt_started), 0, "no start");
    assert!(
        claim_results(&harness).is_empty(),
        "a removed Claim gets no reply"
    );
    assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 0);
    assert!(has_queue_row(&harness, &task), "the row stays queued");

    // The released entry lets the partition's next Claim start the row.
    harness.dispatcher.claim(2, task);
    let runner = harness.dispatcher.next_dispatch().await;
    assert_eq!(runner.context().attempt, 1);
    assert_eq!(
        claim_results(&harness)
            .iter()
            .map(|(claim_id, result, _)| (*claim_id, *result))
            .collect::<Vec<_>>(),
        vec![(2, ClaimResult::Started)]
    );
    harness.shutdown().await;
}

/// A started Claim whose partition is removed before the send hands out nothing and returns
/// the attempt.
#[tokio::test(start_paused = true)]
async fn a_claim_removed_after_its_start_sends_no_task() {
    let harness = EffectHarness::new();
    let task = queue_one_row(&harness).await;
    *harness
        .flow_effect_operations
        .start_commit_delay
        .lock()
        .expect("lock") = Duration::from_secs(10);
    harness.dispatcher.claim(1, task.clone());
    harness.settle().await;
    assert_eq!(
        command_count(&harness, is_attempt_started),
        1,
        "the start was proposed"
    );
    harness.dispatcher.remove_partition(task.partition, &[]);
    tokio::time::sleep(Duration::from_secs(11)).await;
    harness.settle().await;
    assert_eq!(
        harness.dispatcher.dispatch_count.load(Ordering::SeqCst),
        0,
        "the removal fence holds through the start and the send"
    );
    assert_fenced_send_returned(&harness, &task);
    harness.shutdown().await;
}

/// A started Claim fenced by a removal and re-add of its partition on the same node is
/// returned, then handed out exactly once.
#[tokio::test(start_paused = true)]
async fn a_readded_partition_hands_out_a_fenced_started_claim_once() {
    let harness = EffectHarness::new();
    let task = queue_one_row(&harness).await;
    let run_id = task.position.run_id;
    *harness
        .flow_effect_operations
        .start_commit_delay
        .lock()
        .expect("lock") = Duration::from_secs(10);
    harness.dispatcher.claim(1, task.clone());
    harness.settle().await;
    assert_eq!(command_count(&harness, is_attempt_started), 1);
    harness.dispatcher.remove_partition(task.partition, &[]);
    harness.rebuild_all_partitions().await;
    harness.settle().await;
    assert!(
        failure_kinds(&harness).is_empty(),
        "the rebuild leaves the attempt this node still hands out"
    );

    tokio::time::sleep(Duration::from_secs(11)).await;
    harness.settle().await;
    assert_fenced_send_returned(&harness, &task);
    let refs = queued_refs(&harness);
    assert_eq!(refs.len(), 2, "the returned attempt is queued again");
    assert_eq!(refs[1], task, "it keeps its key");

    harness.dispatcher.claim(2, refs[1].clone());
    let runner = harness.dispatcher.next_dispatch().await;
    assert_eq!(runner.context().attempt, 1, "no retry was spent");
    runner.complete_execute(force_complete());
    harness.settle().await;
    tokio::time::sleep(Duration::from_secs(2)).await;
    harness.settle().await;
    assert_eq!(
        harness.run_record("order-1", run_id).status,
        FlowStatus::Completed as i32
    );
    assert_eq!(
        harness.dispatcher.dispatch_count.load(Ordering::SeqCst),
        1,
        "handed out exactly once"
    );
    assert_eq!(command_count(&harness, is_attempt_started), 2);
    assert!(failure_kinds(&harness).is_empty());
    harness.shutdown().await;
}

/// Asserts that the fenced send returned the started attempt unchanged as not delivered.
fn assert_fenced_send_returned(harness: &EffectHarness, task: &QueuedTaskRef) {
    let returned = returns(harness);
    assert_eq!(returned.len(), 1, "the started attempt is returned");
    assert_eq!(
        returned[0].reason,
        StepAttemptReturnReason::NotDelivered as i32
    );
    assert_eq!(
        returned[0].expected_task_id,
        started_task_ids(harness)[0],
        "the return is fenced by the start's task ID"
    );
    assert!(failure_kinds(harness).is_empty(), "no retry was spent");
    let invocation = harness
        .step_execution(
            &task.flow_id,
            task.position.run_id,
            &task.position.step_execution_id,
        )
        .invocation
        .expect("the Step has an invocation");
    assert_eq!(invocation.attempt, returned[0].attempt);
    assert_eq!(invocation.attempt, 1, "the same attempt is pending again");
    assert_eq!(invocation.phase, StepInvocationPhase::Pending as i32);
}

fn is_attempt_started(command: &flow_command::Command) -> bool {
    matches!(command, flow_command::Command::StepAttemptStarted(_))
}

/// Queued counts buffer nothing, and a running count folds changes once its snapshot is read.
#[test]
fn row_count_changes_stay_bounded_while_counts_wait() {
    let gauge = Gauge::default();
    let mut memory = TaskQueueMemory::new(gauge.clone());
    let partition = PartitionId(5);
    memory.forget_row_count(partition);
    for seq in 1..=1000 {
        memory.add_entries(partition, Some((1, seq)), 1);
    }
    assert_eq!(
        memory.buffered_row_changes(partition),
        0,
        "the later snapshot holds them"
    );
    let (_, count) = memory.begin_row_count(partition);
    memory.add_entries(partition, Some((1, 1001)), 1);
    memory.add_entries(partition, Some((1, 1002)), 1);
    memory.note_row_count_watermark(partition, count, Some((1, 1001)));
    for seq in 1003..=2000 {
        memory.add_entries(partition, Some((1, seq)), 1);
    }
    assert_eq!(
        memory.buffered_row_changes(partition),
        0,
        "folded into one sum"
    );
    assert_eq!(gauge.get(), 0, "an uncounted partition is left out");
    memory.finish_row_count(partition, count, 1001, Some((1, 1001)));
    assert_eq!(gauge.get(), 2000, "the snapshot plus every later change");
}
