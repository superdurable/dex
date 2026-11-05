// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::*;
use crate::task_backlog::QueuedTaskRef;
use dexzd_clock::SystemWallClock;
use dexzd_keyspace::dispatch_queue_keys::DispatchQueuePosition;
use dexzd_keyspace::{FlowHash, RunId, TenantId};

fn queue_name(step_type: &str) -> DispatchQueueName {
    DispatchQueueName {
        tenant_id: TenantId(7),
        flow_type: "Flow".to_owned(),
        step_type: step_type.to_owned(),
    }
}

fn queued_ref(partition: u16, step_type: &str, pending_since_micros: i64) -> QueuedTaskRef {
    QueuedTaskRef {
        partition: PartitionId(partition),
        queue: queue_name(step_type),
        position: DispatchQueuePosition {
            priority: 1,
            pending_since_micros,
            flow_hash: FlowHash(u64::from(partition)),
            run_id: RunId([1; 16]),
            method: 2,
            attempt: 1,
            step_execution_id: format!("{step_type}-{pending_since_micros}"),
        },
        run_attempt: 1,
        flow_id: format!("flow-{partition}"),
    }
}

/// Registers a busy stream of the Flow type; `make_idle` frees it.
fn register_busy_stream(dispatch: &WorkerDispatch, app_worker_id: &str) -> Arc<WorkerStream> {
    let (output, _) = mpsc::unbounded_channel();
    let (close, _) = watch::channel(false);
    let worker = Arc::new(WorkerStream {
        id: Uuid::now_v7(),
        tenant_id: 7,
        queues: vec!["Flow".to_owned()],
        backlog_flow_types: vec!["Flow".to_owned()],
        app_worker_id: app_worker_id.to_owned(),
        slot: StreamSlot::Legacy,
        output,
        close,
        state: Mutex::new(WorkerState {
            is_busy: true,
            ..WorkerState::default()
        }),
    });
    let mut registry = dispatch.lock_registry();
    let queue = registry
        .queues
        .entry((7, "Flow".to_owned()))
        .or_insert_with(QueueState::new);
    queue.stream_ids.insert(worker.id);
    queue.users += 1;
    registry.streams.insert(worker.id, worker.clone());
    registry.queue_matching.add_step_stream(&worker);
    drop(registry);
    worker
}

fn pending_claim_count(dispatch: &WorkerDispatch) -> usize {
    dispatch.lock_registry().queue_matching.claims.len()
}

#[tokio::test]
async fn removing_a_partition_frees_the_slot_of_its_undelivered_claim() {
    let dispatch = WorkerDispatch::new(MatchingConfig::default(), Arc::new(SystemWallClock));
    let worker = register_busy_stream(&dispatch, "worker-a");
    dispatch.make_idle(&worker);
    dispatch.queued(queued_ref(7, "Charge", 10));
    assert_eq!(pending_claim_count(&dispatch), 1);

    dispatch.remove_partition(PartitionId(7));
    assert_eq!(pending_claim_count(&dispatch), 0);
    dispatch.queued(queued_ref(8, "Charge", 20));
    let claim = dispatch.next_claim().await.expect("a Claim");
    assert_eq!(claim.task.partition, PartitionId(8));
    assert!(dispatch.is_claim_tracked(PartitionId(8), &claim.task.queue, claim.claim_id));
}

#[tokio::test]
async fn a_delivered_claim_of_a_removed_partition_is_untracked_and_its_slot_returns() {
    let dispatch = WorkerDispatch::new(MatchingConfig::default(), Arc::new(SystemWallClock));
    let worker = register_busy_stream(&dispatch, "worker-a");
    dispatch.make_idle(&worker);
    dispatch.queued(queued_ref(7, "Charge", 10));
    let delivered = dispatch.next_claim().await.expect("a Claim");
    let queue = delivered.task.queue.clone();
    assert!(dispatch.is_claim_tracked(PartitionId(7), &queue, delivered.claim_id));

    dispatch.remove_partition(PartitionId(7));
    assert!(!dispatch.is_claim_tracked(PartitionId(7), &queue, delivered.claim_id));
    // The owner drops the stream without a reply; another partition gets the slot.
    drop(delivered);
    dispatch.queued(queued_ref(8, "Charge", 20));
    let claim = dispatch.next_claim().await.expect("a Claim");
    assert_eq!(claim.task.partition, PartitionId(8));
}

#[test]
fn backoff_callback_preserves_a_promised_rpc_slot() {
    let dispatch = WorkerDispatch::new(MatchingConfig::default(), Arc::new(SystemWallClock));
    let worker = register_busy_stream(&dispatch, "worker");
    let mut lease = QueueLease::acquire(&dispatch, 7, "Flow", 1);
    let task_type = TaskTypeKey {
        tenant_id: 7,
        flow_type: "Flow".to_owned(),
        kind: TaskKind::Rpc,
        name: "getStatus".to_owned(),
    };
    let search = TaskSearch {
        task_type: &task_type,
        flow_id: "rpc-flow",
        partition_search: None,
    };
    let picked = dispatch.pick_worker(&mut lease, &search, false, &HashSet::new(), &HashMap::new());
    assert!(picked.worker.is_none());
    dispatch.make_idle(&worker);
    assert!(
        dispatch
            .lock_registry()
            .promised_streams
            .contains_key(&worker.id)
    );

    dispatch.queued(queued_ref(1, "Charge", 1));
    dispatch.end_step_backoff(worker.id);
    assert!(
        dispatch
            .lock_registry()
            .promised_streams
            .contains_key(&worker.id),
        "the backoff callback took the slot promised to a woken RPC"
    );
    assert_eq!(pending_claim_count(&dispatch), 0);
}

#[test]
fn backlog_service_agrees_with_claim_eligibility_after_an_evicted_decline() {
    let config = MatchingConfig {
        unsupported_type_cache_entries: 1,
        ..MatchingConfig::default()
    };
    let dispatch = WorkerDispatch::new(config, Arc::new(SystemWallClock));
    let worker = register_busy_stream(&dispatch, "worker-a");
    dispatch.make_idle(&worker);
    let charge = step_type_key(&queue_name("Charge"));
    let refund = step_type_key(&queue_name("Refund"));
    {
        let mut registry = dispatch.lock_registry();
        let newest = registry.next_idle_position;
        registry
            .unsupported_types
            .insert(charge.clone(), "worker-a", newest);
        // Evicts the Charge decline; the idle stream keeps excluding Charge until it idles again.
        registry
            .unsupported_types
            .insert(refund.clone(), "worker-a", newest);
        assert!(registry.is_unsupported_on(&charge, &worker));
        assert_eq!(
            registry.backlog_service(&queue_name("Charge")),
            BacklogService::NoSupportingWorker
        );
    }
    dispatch.queued(queued_ref(1, "Charge", 1));
    assert_eq!(pending_claim_count(&dispatch), 0);
    assert!(!dispatch.has_qualified_backlog(&queue_name("Charge")));
}
