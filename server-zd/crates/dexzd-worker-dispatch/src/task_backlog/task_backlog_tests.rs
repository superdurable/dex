// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The ref rules of plan section 3.2: keep the smaller ref, one Claim per (partition, queue),
//! insertion identity across a Claim, and the merge of periodic Heads.

use super::*;
use dexzd_keyspace::dispatch_queue_keys::DEFAULT_DISPATCH_PRIORITY;
use dexzd_keyspace::{FlowHash, RunId, TenantId};

fn queue(step_type: &str) -> DispatchQueueName {
    DispatchQueueName {
        tenant_id: TenantId(7),
        flow_type: "Order".to_owned(),
        step_type: step_type.to_owned(),
    }
}

fn task(partition: u16, step_type: &str, pending_since_micros: i64) -> QueuedTaskRef {
    QueuedTaskRef {
        partition: PartitionId(partition),
        queue: queue(step_type),
        position: DispatchQueuePosition {
            priority: DEFAULT_DISPATCH_PRIORITY,
            pending_since_micros,
            flow_hash: FlowHash(1),
            run_id: RunId([2; 16]),
            method: 2,
            attempt: 1,
            step_execution_id: format!("{step_type}-{pending_since_micros}"),
        },
        run_attempt: 1,
        flow_id: "flow".to_owned(),
    }
}

fn earliest(backlog: &TaskBacklog) -> Option<(DispatchQueueName, PartitionId)> {
    let flow_types = ["Order".to_owned()];
    backlog.earliest_claimable(7, flow_types.iter(), |_| true)
}

fn scan_head(head: Option<QueuedTaskRef>) -> ScannedQueueHead {
    ScannedQueueHead {
        queue: queue("Charge"),
        head,
        is_head_queued_in_flight: false,
        has_skipped_held_rows: false,
    }
}

#[test]
fn a_partition_keeps_its_smallest_ref_and_heads_order_by_pending_time() {
    let mut backlog = TaskBacklog::default();
    backlog.insert(task(1, "Charge", 30));
    backlog.insert(task(1, "Charge", 50));
    backlog.insert(task(2, "Charge", 20));
    backlog.insert(task(1, "Ship", 10));
    assert_eq!(earliest(&backlog), Some((queue("Ship"), PartitionId(1))));
    let (_, shipped) = backlog
        .start_claim(&queue("Ship"), PartitionId(1))
        .expect("claimable");
    assert_eq!(shipped.position.pending_since_micros, 10);
    assert_eq!(earliest(&backlog), Some((queue("Charge"), PartitionId(2))));
    let (_, charged) = backlog
        .start_claim(&queue("Charge"), PartitionId(1))
        .expect("claimable");
    assert_eq!(
        charged.position.pending_since_micros, 30,
        "the larger ref was dropped"
    );
    let flow_types = ["Order".to_owned()];
    assert_eq!(
        backlog.earliest_claimable(7, flow_types.iter(), |name| name.step_type != "Charge"),
        None,
        "an ineligible queue is never chosen"
    );
}

#[test]
fn a_claim_takes_its_insertion_and_keeps_inserts_that_arrive_meanwhile() {
    let mut backlog = TaskBacklog::default();
    let partition = PartitionId(1);
    backlog.insert(task(1, "Charge", 30));
    let (claim_id, claimed) = backlog
        .start_claim(&queue("Charge"), partition)
        .expect("claimable");
    assert!(backlog.start_claim(&queue("Charge"), partition).is_none());
    assert!(
        backlog.has_backlog(&queue("Charge")),
        "a Claim in flight counts as backlog"
    );
    // The same key comes back from a lock park before the reply arrives.
    backlog.insert(claimed.clone());
    assert_eq!(
        earliest(&backlog),
        None,
        "one Claim per (partition, queue) at a time"
    );
    assert!(backlog.finish_claim(
        &queue("Charge"),
        partition,
        claim_id,
        Some(task(1, "Charge", 40))
    ));
    let (_, again) = backlog
        .start_claim(&queue("Charge"), partition)
        .expect("the re-inserted ref survived the reply");
    assert_eq!(again, claimed);
}

#[test]
fn a_reply_without_a_next_ref_empties_the_partition_and_stale_replies_are_dropped() {
    let mut backlog = TaskBacklog::default();
    let partition = PartitionId(1);
    backlog.insert(task(1, "Charge", 30));
    let (claim_id, _) = backlog
        .start_claim(&queue("Charge"), partition)
        .expect("claimable");
    assert!(!backlog.finish_claim(&queue("Charge"), partition, claim_id + 1, None));
    assert!(backlog.finish_claim(&queue("Charge"), partition, claim_id, None));
    assert!(!backlog.has_backlog(&queue("Charge")));
    assert!(!backlog.finish_claim(&queue("Charge"), partition, claim_id, None));
}

#[test]
fn removing_a_partition_drops_its_refs_and_claims_but_not_others() {
    let mut backlog = TaskBacklog::default();
    backlog.insert(task(1, "Charge", 30));
    backlog.insert(task(2, "Charge", 40));
    let (claim_id, _) = backlog
        .start_claim(&queue("Charge"), PartitionId(1))
        .expect("claimable");
    backlog.insert(task(1, "Charge", 35));
    backlog.remove_partition(PartitionId(1));
    assert!(!backlog.finish_claim(
        &queue("Charge"),
        PartitionId(1),
        claim_id,
        Some(task(1, "Charge", 50))
    ));
    assert_eq!(earliest(&backlog), Some((queue("Charge"), PartitionId(2))));
    backlog.insert(task(1, "Charge", 60));
    assert!(
        backlog
            .start_claim(&queue("Charge"), PartitionId(1))
            .is_some()
    );
}

#[test]
fn a_scan_without_crossing_restores_a_lost_head_and_replaces_stale_refs() {
    let mut backlog = TaskBacklog::default();
    let token = backlog.begin_scan(PartitionId(1));
    let restored = backlog.finish_scan(token, vec![scan_head(Some(task(1, "Charge", 30)))]);
    assert_eq!(restored.len(), 1, "matching had no ref: a lost head");
    backlog.insert(task(1, "Charge", 10));
    let token = backlog.begin_scan(PartitionId(1));
    let restored = backlog.finish_scan(token, vec![scan_head(Some(task(1, "Charge", 30)))]);
    assert!(restored.is_empty(), "a smaller ref is only stale");
    let (_, head) = backlog
        .start_claim(&queue("Charge"), PartitionId(1))
        .expect("claimable");
    assert_eq!(
        head.position.pending_since_micros, 30,
        "the stale ref was replaced"
    );
}

#[test]
fn a_scan_that_skipped_held_rows_never_raises_or_removes_a_ref() {
    let mut backlog = TaskBacklog::default();
    backlog.insert(task(1, "Charge", 10));
    let token = backlog.begin_scan(PartitionId(1));
    let mut result = scan_head(None);
    result.has_skipped_held_rows = true;
    assert!(backlog.finish_scan(token, vec![result]).is_empty());
    assert!(backlog.has_backlog(&queue("Charge")));
    let token = backlog.begin_scan(PartitionId(1));
    assert!(backlog.finish_scan(token, vec![scan_head(None)]).is_empty());
    assert!(
        !backlog.has_backlog(&queue("Charge")),
        "an empty queue drops its ref"
    );
}

#[test]
fn a_crossed_scan_reports_only_heads_smaller_than_any_key_seen() {
    let mut backlog = TaskBacklog::default();
    backlog.insert(task(1, "Charge", 50));
    let token = backlog.begin_scan(PartitionId(1));
    // A Queued with a smaller key arrives after the snapshot: crossing, not a bug.
    backlog.insert(task(1, "Charge", 20));
    let restored = backlog.finish_scan(token, vec![scan_head(Some(task(1, "Charge", 40)))]);
    assert!(restored.is_empty());
    let token = backlog.begin_scan(PartitionId(1));
    backlog.insert(task(1, "Charge", 25));
    let restored = backlog.finish_scan(token, vec![scan_head(Some(task(1, "Charge", 5)))]);
    assert_eq!(
        restored.len(),
        1,
        "smaller than every key seen: the Queued was lost"
    );
    let (_, head) = backlog
        .start_claim(&queue("Charge"), PartitionId(1))
        .expect("claimable");
    assert_eq!(head.position.pending_since_micros, 5);
}

#[test]
fn a_head_whose_queued_is_in_flight_is_restored_silently() {
    let mut backlog = TaskBacklog::default();
    let token = backlog.begin_scan(PartitionId(1));
    let mut result = scan_head(Some(task(1, "Charge", 30)));
    result.is_head_queued_in_flight = true;
    assert!(backlog.finish_scan(token, vec![result]).is_empty());
    assert!(
        backlog
            .start_claim(&queue("Charge"), PartitionId(1))
            .is_some()
    );
}
