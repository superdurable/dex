// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Durable task queue races: QueuedSent takeovers, owed next refs, and same-key requeues.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::atomic::Ordering;
use std::time::Duration;

use dexzd_keyspace::dispatch_queue_keys::DispatchQueueName;
use dexzd_keyspace::{PartitionId, RunId, TenantId};
use dexzd_protocol::dex::{
    FlowStatus, PollWorkerTaskRejectReason, StartFlowRequest, StepDurability, StepOptions,
    StopFlowRequest, StopType, WorkerTarget,
};
use dexzd_protocol::dexzd::v1::{FlowCommand, FlowStartCommand, FlowStopCommand, flow_command};
use dexzd_worker_dispatch::{ClaimResult, QueuedTaskRef};

use super::dispatch_tests::force_complete;
use super::{EffectHarness, is_attempt_failed, is_error_reply, settings, start_request};
use crate::effects::dispatch::{EntryHolder, HandoutEnd};
use crate::effects::method_request::MethodAttemptKey;
use crate::effects::{EffectsConfig, RouterMessage};
use crate::flow_coordination::{CommitUrgency, FlowAddress};

/// Longer than the first retry backoff of an owed next ref.
const OWED_REF_BACKOFF: Duration = Duration::from_millis(500);

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

fn failure_count(harness: &EffectHarness) -> usize {
    harness
        .flow_effect_operations
        .commands_matching(is_attempt_failed)
        .len()
}

fn attempt_key(task: &QueuedTaskRef) -> MethodAttemptKey {
    MethodAttemptKey {
        run_id: task.position.run_id,
        run_attempt: task.run_attempt,
        step_execution_id: task.position.step_execution_id.clone(),
        method: i32::from(task.position.method),
        attempt: task.position.attempt,
    }
}

/// Returns `count` Flow IDs of the default tenant in the partition of `order-1`, starting with it.
fn flow_ids_in_one_partition(harness: &EffectHarness, count: usize) -> Vec<String> {
    let partition = harness.partition_of("order-1");
    (1..10_000)
        .map(|index| format!("order-{index}"))
        .filter(|flow_id| harness.partition_of(flow_id) == partition)
        .take(count)
        .collect()
}

fn execute_request(flow_id: &str, durability: StepDurability) -> StartFlowRequest {
    StartFlowRequest {
        step_options: Some(StepOptions {
            skip_wait_for: true,
            execute_durability_override: durability as i32,
            ..StepOptions::default()
        }),
        ..start_request(flow_id)
    }
}

/// Queues one Execute attempt per Flow, in order, in one queue of one partition.
///
/// Returns the run IDs and refs in queue order.
async fn queue_rows(harness: &EffectHarness, flow_ids: &[String]) -> Vec<(RunId, QueuedTaskRef)> {
    harness.rebuild_all_partitions().await;
    harness
        .dispatcher
        .refuse_dispatch
        .store(true, Ordering::SeqCst);
    let mut run_ids = Vec::new();
    for flow_id in flow_ids {
        run_ids
            .push(harness.start_flow(execute_request(flow_id, StepDurability::Sync), settings()));
        harness.settle().await;
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    let refs = queued_refs(harness);
    assert_eq!(refs.len(), flow_ids.len(), "every attempt is queued");
    assert!(
        refs.windows(2)
            .all(|pair| pair[0].queue == pair[1].queue && pair[0].position < pair[1].position)
    );
    run_ids.into_iter().zip(refs).collect()
}

fn stop_flow(harness: &EffectHarness, flow_id: &str, run_id: RunId) {
    let stop = FlowCommand {
        flow_id: flow_id.to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        command: Some(flow_command::Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                flow_id: flow_id.to_owned(),
                stop_type: StopType::Terminate as i32,
                ..StopFlowRequest::default()
            }),
        })),
        ..FlowCommand::default()
    };
    harness
        .flow_effect_operations
        .commit(&EffectHarness::flow(flow_id), stop, CommitUrgency::Prompt)
        .expect("stop commits");
}

/// When the sender's HandoutFinished reaches the router, relative to the Claim.
#[derive(Clone, Copy, Debug)]
enum SenderFinish {
    AfterClaim,
    BeforeClaim,
}

/// A Claim takes over an entry whose sender already sent Queued, before its HandoutFinished.
#[tokio::test(start_paused = true)]
async fn a_claim_before_the_senders_finish_takes_over_the_queued_entry() {
    check_queued_sent_takeover(SenderFinish::AfterClaim).await;
}

/// A Claim after the sender's HandoutFinished registers a fresh entry and starts the row.
#[tokio::test(start_paused = true)]
async fn a_claim_after_the_senders_finish_starts_the_row_once() {
    check_queued_sent_takeover(SenderFinish::BeforeClaim).await;
}

async fn check_queued_sent_takeover(order: SenderFinish) {
    let harness = EffectHarness::new();
    let flow_ids = flow_ids_in_one_partition(&harness, 2);
    let rows = queue_rows(&harness, &flow_ids).await;
    let (_, claimed) = rows[0].clone();
    let (_, behind) = rows[1].clone();
    let key = attempt_key(&claimed);
    let context = harness.effect_context();
    // The sender registered the entry and marked it sent; its HandoutFinished is still on the way.
    let sender_generation = {
        let mut entries = context.attempt_entries();
        let generation = entries.register(&key).expect("the first hand-out ended");
        assert!(entries.set_holder(&key, generation, EntryHolder::QueuedSent));
        generation
    };
    let finish_sender = || {
        context.report(RouterMessage::HandoutFinished {
            flow: EffectHarness::flow(&claimed.flow_id),
            key: key.clone(),
            generation: sender_generation,
            end: HandoutEnd::Released,
        });
    };
    if matches!(order, SenderFinish::BeforeClaim) {
        finish_sender();
        harness.settle().await;
        assert!(
            !context.attempt_entries().is_held(&key)
                && !context.attempt_entries().is_queued_sent(&key),
            "{order:?}: the sender's finish removes its entry"
        );
    } else {
        assert!(context.attempt_entries().is_queued_sent(&key));
    }

    harness.dispatcher.claim(1, claimed.clone());
    let dispatch = harness.dispatcher.next_dispatch().await;
    assert_eq!(dispatch.request.flow_id, claimed.flow_id);
    assert_eq!(dispatch.context().attempt, 1);
    harness.settle().await;
    assert!(
        context.attempt_entries().is_held(&key) && !context.attempt_entries().is_queued_sent(&key),
        "{order:?}: the Claim's hand-out holds the running attempt"
    );
    if matches!(order, SenderFinish::AfterClaim) {
        finish_sender();
        harness.settle().await;
        assert!(
            context.attempt_entries().is_held(&key),
            "{order:?}: a stale sender finish leaves the Claim's entry"
        );
    }
    assert_eq!(
        claim_results(&harness),
        vec![(1, ClaimResult::Started, Some(behind.clone()))],
        "{order:?}: no ref is lost"
    );

    dispatch.complete_execute(force_complete());
    harness.settle().await;
    assert!(!context.attempt_entries().is_held(&key));
    harness.dispatcher.claim(2, behind.clone());
    let behind_dispatch = harness.dispatcher.next_dispatch().await;
    assert_eq!(behind_dispatch.request.flow_id, behind.flow_id);
    harness.settle().await;
    assert_eq!(claim_results(&harness)[1].1, ClaimResult::Started);
    assert_eq!(
        harness.dispatcher.dispatch_count.load(Ordering::SeqCst),
        2,
        "{order:?}: each row is handed out once"
    );
    assert_eq!(failure_count(&harness), 0);
    harness.shutdown().await;
}

fn harness_without_periodic_heads() -> EffectHarness {
    EffectHarness::with_config(
        super::DurableState::new(),
        EffectsConfig {
            heads_resync_interval: Duration::ZERO,
            ..EffectsConfig::default()
        },
    )
}

/// A stale Claim whose next-ref seek fails still gets the next row sent after a backoff.
#[tokio::test(start_paused = true)]
async fn a_stale_claim_with_a_failed_seek_owes_the_next_row_and_sends_it() {
    let harness = harness_without_periodic_heads();
    let flow_ids = flow_ids_in_one_partition(&harness, 3);
    let rows = queue_rows(&harness, &flow_ids).await;
    let (stopped_run, stale) = rows[0].clone();
    stop_flow(&harness, &flow_ids[0], stopped_run);
    harness.settle().await;
    harness
        .flow_effect_operations
        .committed_reader_failures
        .store(1, Ordering::SeqCst);
    harness.dispatcher.claim(1, stale);
    harness.settle().await;
    assert_eq!(
        claim_results(&harness),
        vec![(1, ClaimResult::Stale, None)],
        "the failed seek leaves the reply without a next ref"
    );
    assert_eq!(queued_refs(&harness).len(), 3);

    tokio::time::sleep(OWED_REF_BACKOFF).await;
    harness.settle().await;
    let refs = queued_refs(&harness);
    assert_eq!(refs.len(), 4, "the owner sends the owed next ref");
    assert_eq!(refs[3], rows[1].1);
    harness.dispatcher.claim(2, refs[3].clone());
    let dispatch = harness.dispatcher.next_dispatch().await;
    assert_eq!(dispatch.request.flow_id, flow_ids[1]);
    harness.settle().await;
    assert_eq!(
        claim_results(&harness)[1],
        (2, ClaimResult::Started, Some(rows[2].1.clone()))
    );
    assert_eq!(
        harness
            .dispatcher
            .restored_head_count
            .load(Ordering::SeqCst),
        0
    );
    harness.shutdown().await;
}

/// An owed next ref skips a row cancelled during its backoff and sends the row after it.
#[tokio::test(start_paused = true)]
async fn an_owed_next_ref_skips_a_row_cancelled_during_the_backoff() {
    let harness = harness_without_periodic_heads();
    let flow_ids = flow_ids_in_one_partition(&harness, 3);
    let rows = queue_rows(&harness, &flow_ids).await;
    harness
        .flow_effect_operations
        .committed_reader_failures
        .store(1, Ordering::SeqCst);
    harness.dispatcher.claim(1, rows[0].1.clone());
    let started = harness.dispatcher.next_dispatch().await;
    assert_eq!(started.request.flow_id, flow_ids[0]);
    harness.settle().await;
    assert_eq!(
        claim_results(&harness),
        vec![(1, ClaimResult::Started, None)]
    );
    // The row behind is cancelled before the owed seek runs.
    stop_flow(&harness, &flow_ids[1], rows[1].0);
    harness.settle().await;

    tokio::time::sleep(OWED_REF_BACKOFF).await;
    harness.settle().await;
    let refs = queued_refs(&harness);
    assert_eq!(refs.len(), 4, "the owner sends one owed next ref");
    assert_eq!(refs[3], rows[2].1, "the cancelled row is skipped");
    harness.dispatcher.claim(2, refs[3].clone());
    let dispatch = harness.dispatcher.next_dispatch().await;
    assert_eq!(dispatch.request.flow_id, flow_ids[2]);
    harness.settle().await;
    assert_eq!(claim_results(&harness)[1].1, ClaimResult::Started);
    assert_eq!(harness.dispatcher.dispatch_count.load(Ordering::SeqCst), 2);
    assert_eq!(
        harness
            .dispatcher
            .restored_head_count
            .load(Ordering::SeqCst),
        0
    );
    harness.shutdown().await;
}

/// An ASYNC attempt declined before its start commits requeues at its key and runs once more.
#[tokio::test(start_paused = true)]
async fn an_async_attempt_declined_before_its_start_commits_requeues_and_runs_once() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness
        .dispatcher
        .refuse_dispatch
        .store(true, Ordering::SeqCst);
    *harness
        .flow_effect_operations
        .start_commit_delay
        .lock()
        .expect("lock") = Duration::from_secs(1);
    let run_id = harness.start_flow(
        execute_request("order-1", StepDurability::Async),
        settings(),
    );
    harness.settle().await;
    let refs = queued_refs(&harness);
    assert_eq!(refs.len(), 1);
    let first_ref = refs[0].clone();

    harness.dispatcher.claim(1, first_ref.clone());
    let declined = harness.dispatcher.next_dispatch().await;
    declined.reject(PollWorkerTaskRejectReason::UnknownStep, true);
    harness.settle().await;
    tokio::time::sleep(Duration::from_secs(2)).await;
    harness.settle().await;
    let refs = queued_refs(&harness);
    assert_eq!(refs.len(), 2, "the declined attempt is queued again");
    assert_eq!(refs[1], first_ref, "it keeps its key");

    harness.dispatcher.claim(2, refs[1].clone());
    let runner = harness.dispatcher.next_dispatch().await;
    assert_eq!(runner.context().attempt, 1);
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
        2,
        "one declined hand-out and one run"
    );
    assert_eq!(queued_refs(&harness).len(), 2, "nothing is queued again");
    assert_eq!(
        claim_results(&harness)
            .iter()
            .map(|(claim_id, result, _)| (*claim_id, *result))
            .collect::<Vec<_>>(),
        vec![(1, ClaimResult::Started), (2, ClaimResult::Started)]
    );
    assert_eq!(failure_count(&harness), 0);
    harness.shutdown().await;
}

const TENANTS: [TenantId; 2] = [TenantId(0), TenantId(7)];
const STEP_TYPES: [&str; 2] = ["ChargeStep", "PackStep"];
const ROWS_PER_QUEUE: usize = 2;

/// Starts a pull Flow of `tenant` whose Execute runs first.
fn start_tenant_flow(harness: &EffectHarness, tenant: TenantId, flow_id: &str, step_type: &str) {
    let request = StartFlowRequest {
        start_step_type: step_type.to_owned(),
        ..execute_request(flow_id, StepDurability::Sync)
    };
    let command = FlowCommand {
        flow_id: flow_id.to_owned(),
        tenant_id: tenant.0,
        command: Some(flow_command::Command::FlowStart(FlowStartCommand {
            new_run_id: uuid::Uuid::now_v7().as_bytes().to_vec(),
            request: Some(request),
            settings: Some(settings()),
            resolved_timeout_policy: dexzd_protocol::dex::FlowTimeoutPolicy::Fail as i32,
            worker_target: Some(WorkerTarget { is_pull_mode: true }),
            ..FlowStartCommand::default()
        })),
        ..FlowCommand::default()
    };
    let reply = harness
        .flow_effect_operations
        .commit(
            &FlowAddress::new(tenant, flow_id),
            command,
            CommitUrgency::Prompt,
        )
        .expect("start commits");
    assert!(!is_error_reply(&reply), "start failed: {reply:?}");
}

/// TB18: a rebuild reports one head per nonempty queue, seeks each once, and keeps tenants apart.
#[tokio::test(start_paused = true)]
async fn a_rebuild_reports_one_head_per_queue_of_every_tenant() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    harness
        .dispatcher
        .refuse_dispatch
        .store(true, Ordering::SeqCst);
    let partition_count =
        u16::try_from(harness.hasher.partition_count()).expect("partition counts fit in u16");
    let mut tenant_of_flow = BTreeMap::new();
    let mut next_index = 0_u32;
    for _ in 0..ROWS_PER_QUEUE {
        for tenant in TENANTS {
            for step_type in STEP_TYPES {
                for partition in 0..partition_count {
                    let flow_id = loop {
                        next_index += 1;
                        let candidate = format!("tenant-{}-flow-{next_index}", tenant.0);
                        if harness.hasher.partition_of_flow(tenant, &candidate)
                            == PartitionId(partition)
                        {
                            break candidate;
                        }
                    };
                    start_tenant_flow(&harness, tenant, &flow_id, step_type);
                    tenant_of_flow.insert(flow_id, tenant);
                }
                harness.settle().await;
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        }
    }
    harness.settle().await;
    let queued = queued_refs(&harness);
    assert_eq!(
        queued.len(),
        tenant_of_flow.len(),
        "every attempt is queued"
    );
    let mut first_rows: BTreeMap<(PartitionId, DispatchQueueName), QueuedTaskRef> = BTreeMap::new();
    for task in &queued {
        let slot = first_rows
            .entry((task.partition, task.queue.clone()))
            .or_insert_with(|| task.clone());
        if task.position < slot.position {
            *slot = task.clone();
        }
    }
    let nonempty_queues = first_rows.len();
    assert_eq!(
        nonempty_queues,
        usize::from(partition_count) * TENANTS.len() * STEP_TYPES.len()
    );
    let queued_commands = harness
        .flow_effect_operations
        .commands_matching(|command| matches!(command, flow_command::Command::StepAttemptQueued(_)))
        .len();

    harness
        .flow_effect_operations
        .committed_queue_seeks
        .store(0, Ordering::SeqCst);
    harness.rebuild_all_partitions().await;
    harness.settle().await;
    let heads = harness
        .dispatcher
        .rebuilt_heads
        .lock()
        .expect("lock")
        .clone();
    let reported: BTreeSet<_> = heads
        .iter()
        .map(|head| (head.partition, head.queue.clone()))
        .collect();
    assert_eq!(heads.len(), nonempty_queues, "one head per nonempty queue");
    assert_eq!(reported.len(), nonempty_queues);
    for head in &heads {
        assert_eq!(
            Some(head),
            first_rows.get(&(head.partition, head.queue.clone())),
            "each head is its queue's first row"
        );
        assert_eq!(
            tenant_of_flow.get(&head.flow_id),
            Some(&head.queue.tenant_id),
            "no ref crosses tenants: {head:?}"
        );
    }
    assert_eq!(
        harness
            .flow_effect_operations
            .committed_queue_seeks
            .load(Ordering::SeqCst),
        usize::from(partition_count) + nonempty_queues,
        "one skip-scan seek per partition plus one per nonempty queue"
    );
    assert_eq!(
        harness
            .flow_effect_operations
            .commands_matching(|command| {
                matches!(command, flow_command::Command::StepAttemptQueued(_))
            })
            .len(),
        queued_commands,
        "a rebuild queues nothing again"
    );
    harness.shutdown().await;
}
