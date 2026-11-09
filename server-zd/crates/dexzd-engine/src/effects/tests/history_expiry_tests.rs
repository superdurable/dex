// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Deleting the objects of expired history segments with the real apply state machine: the
//! deletion delay, the per-run switch, restarts, purges, and the orphan sweep.

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;

use bytes::Bytes;
use dexzd_apply::FlowStateReader;
use dexzd_history_segment::{history_run_object_key_prefix, history_segment_object_key};
use dexzd_keyspace::row_keys::history_object_delete_pending_index_key;
use dexzd_keyspace::{ColumnFamily, PartitionId, RunId, TenantId};
use dexzd_object_store::{
    FaultAction, FaultInjector, InMemoryObjectStore, ObjectStore, ObjectStoreOperation,
    WriteCondition,
};
use dexzd_protocol::dex::{
    AttributeWrite, FlowConfig, SetAttributesRequest, StartFlowRequest, UpdateFlowConfigRequest,
};
use dexzd_protocol::dexzd::v1::{
    AttributesSetCommand, FlowCommand, FlowConfigUpdateCommand, HistorySegmentRecord,
    HistorySegmentState, RunSettings, flow_command,
};

use super::history_upload_tests::{
    close_run, enable_history_offload, object_key, object_keys_with_prefix, run_purges, segments,
    short_retention_settings, stored_object, wait_until,
};
use super::{
    DurableState, EffectHarness, RecordedCommand, is_error_reply, ok_reply, settings, string_value,
};
use crate::effects::EffectsConfig;
use crate::flow_coordination::CommitUrgency;
use crate::recovery::PartitionEffectRebuilder;

const MINUTE: Duration = Duration::from_secs(60);
const HOUR: Duration = Duration::from_secs(60 * 60);

/// Every command seals its own segment, and a small window lets pruning pass old segments.
fn expiring_settings(is_deletion_enabled: bool) -> RunSettings {
    RunSettings {
        history_window_events: 4,
        history_prune_chunk_events: 1,
        checkpoint_command_threshold: 2,
        history_segment_target_bytes: 1,
        delete_expired_history_objects: is_deletion_enabled,
        ..settings()
    }
}

/// Starts a run without a start Step, so it has no Step work to rebuild.
fn start_run(harness: &EffectHarness, flow_id: &str, settings: RunSettings) -> RunId {
    harness.start_flow(
        StartFlowRequest {
            flow_id: flow_id.to_owned(),
            flow_type: "OrderFlow".to_owned(),
            request_id: format!("request-{flow_id}"),
            ..StartFlowRequest::default()
        },
        settings,
    )
}

fn commit_run_command(
    harness: &EffectHarness,
    flow_id: &str,
    run_id: RunId,
    body: flow_command::Command,
) {
    let reply = harness
        .flow_effect_operations
        .commit(
            &EffectHarness::flow(flow_id),
            FlowCommand {
                flow_id: flow_id.to_owned(),
                run_id: run_id.as_bytes().to_vec(),
                command: Some(body),
                ..FlowCommand::default()
            },
            CommitUrgency::Prompt,
        )
        .expect("the command commits");
    assert!(!is_error_reply(&reply), "the command failed: {reply:?}");
}

fn write_note(harness: &EffectHarness, flow_id: &str, run_id: RunId, round: usize) {
    commit_run_command(
        harness,
        flow_id,
        run_id,
        flow_command::Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                flow_id: flow_id.to_owned(),
                attributes: vec![AttributeWrite {
                    key: "note".to_owned(),
                    value: Some(string_value(&format!("note {round}"))),
                    ..AttributeWrite::default()
                }],
                request_id: uuid::Uuid::now_v7().to_string(),
                ..SetAttributesRequest::default()
            }),
            hard_delete_objects: Vec::new(),
            request_fingerprint: vec![1],
        }),
    );
}

fn set_delete_switch(harness: &EffectHarness, flow_id: &str, run_id: RunId, is_enabled: bool) {
    commit_run_command(
        harness,
        flow_id,
        run_id,
        flow_command::Command::FlowConfigUpdate(FlowConfigUpdateCommand {
            request: Some(UpdateFlowConfigRequest {
                flow_id: flow_id.to_owned(),
                flow_config: Some(FlowConfig {
                    delete_expired_history_objects: Some(is_enabled),
                    ..FlowConfig::default()
                }),
                ..UpdateFlowConfigRequest::default()
            }),
        }),
    );
}

fn has_sealed_segment(harness: &EffectHarness, flow_id: &str, run_id: RunId) -> bool {
    segments(harness, flow_id, run_id)
        .iter()
        .any(|segment| segment.state == HistorySegmentState::Sealed as i32)
}

fn expired_segments(
    harness: &EffectHarness,
    flow_id: &str,
    run_id: RunId,
) -> Vec<HistorySegmentRecord> {
    segments(harness, flow_id, run_id)
        .into_iter()
        .filter(|segment| segment.state == HistorySegmentState::Expired as i32)
        .collect()
}

fn object_keys(
    harness: &EffectHarness,
    flow_id: &str,
    run_id: RunId,
    segments: &[HistorySegmentRecord],
) -> Vec<String> {
    segments
        .iter()
        .map(|segment| object_key(harness, flow_id, run_id, segment))
        .collect()
}

/// Writes notes, letting every upload finish after each, until at least `count` segments of the
/// run are EXPIRED.
async fn write_until_expired(harness: &EffectHarness, flow_id: &str, run_id: RunId, count: usize) {
    for round in 0..200 {
        wait_until(harness, "every sealed segment is uploaded", || {
            !has_sealed_segment(harness, flow_id, run_id)
        })
        .await;
        if expired_segments(harness, flow_id, run_id).len() >= count {
            return;
        }
        write_note(harness, flow_id, run_id, round);
    }
    panic!("{count} segments of {flow_id} never expired");
}

fn is_delete_pending(harness: &EffectHarness, flow_id: &str, run_id: RunId) -> bool {
    let run = harness.run_location(flow_id, run_id);
    let partition: PartitionId = harness.partition_of(flow_id);
    harness
        .flow_effect_operations
        .snapshot()
        .get(
            ColumnFamily::PartitionIndex,
            &history_object_delete_pending_index_key(partition, run.flow_hash, run.run_id),
        )
        .expect("memory reads never fail")
        .is_some()
}

fn deletion_records(harness: &EffectHarness) -> Vec<RecordedCommand> {
    harness.flow_effect_operations.commands_matching(|command| {
        matches!(
            command,
            flow_command::Command::HistorySegmentObjectsDeleted(_)
        )
    })
}

async fn are_stored(harness: &EffectHarness, keys: &[String]) -> Vec<bool> {
    let mut stored = Vec::with_capacity(keys.len());
    for key in keys {
        stored.push(stored_object(harness, key).await.is_some());
    }
    stored
}

#[tokio::test(start_paused = true)]
async fn expired_objects_are_deleted_an_hour_after_their_expiry() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_id = start_run(&harness, "order-1", expiring_settings(true));
    write_until_expired(&harness, "order-1", run_id, 2).await;
    let expired = expired_segments(&harness, "order-1", run_id);
    let keys = object_keys(&harness, "order-1", run_id, &expired);
    assert!(
        are_stored(&harness, &keys)
            .await
            .iter()
            .all(|is_stored| *is_stored)
    );
    assert!(is_delete_pending(&harness, "order-1", run_id));

    tokio::time::sleep(HOUR - MINUTE).await;
    harness.settle().await;
    assert!(
        are_stored(&harness, &keys)
            .await
            .iter()
            .all(|is_stored| *is_stored),
        "nothing is deleted before the hour after the expiry"
    );
    assert!(deletion_records(&harness).is_empty());

    tokio::time::sleep(2 * MINUTE).await;
    wait_until(&harness, "the expired objects are deleted", || {
        expired_segments(&harness, "order-1", run_id).is_empty()
            && !is_delete_pending(&harness, "order-1", run_id)
    })
    .await;
    assert!(
        are_stored(&harness, &keys)
            .await
            .iter()
            .all(|is_stored| !*is_stored)
    );
    let records = deletion_records(&harness);
    assert!(!records.is_empty());
    for record in &records {
        assert_eq!(record.reply, ok_reply());
        assert_eq!(record.urgency, CommitUrgency::Batched);
        assert_eq!(record.command.expected_run_attempt, 0);
    }
    match &records[0].command.command {
        Some(flow_command::Command::HistorySegmentObjectsDeleted(deleted)) => {
            assert_eq!(deleted.first_seqs.first(), Some(&expired[0].first_seq));
        }
        other => panic!("not a deletion record: {other:?}"),
    }
    let record = harness.run_record("order-1", run_id);
    assert_eq!(
        record.history_objects_deleted_through_seq,
        record.history_expired_through_seq
    );
    assert_eq!(
        harness.metrics.history_expired_objects_deleted.get(),
        keys.len() as u64
    );
    let remaining = segments(&harness, "order-1", run_id);
    assert!(!remaining.is_empty());
    let remaining_keys = object_keys(&harness, "order-1", run_id, &remaining);
    assert!(
        are_stored(&harness, &remaining_keys)
            .await
            .iter()
            .all(|is_stored| *is_stored),
        "objects inside the window stay"
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn deletion_follows_the_run_switch_and_turning_it_on_deletes_the_backlog() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_id = start_run(&harness, "order-1", expiring_settings(false));
    write_until_expired(&harness, "order-1", run_id, 2).await;
    let backlog = object_keys(
        &harness,
        "order-1",
        run_id,
        &expired_segments(&harness, "order-1", run_id),
    );
    tokio::time::sleep(2 * HOUR).await;
    harness.settle().await;
    assert!(
        are_stored(&harness, &backlog)
            .await
            .iter()
            .all(|is_stored| *is_stored),
        "with the switch off, expired objects stay"
    );
    assert!(deletion_records(&harness).is_empty());
    assert!(!is_delete_pending(&harness, "order-1", run_id));

    // Off to on deletes what already expired, here at once since the hour has passed.
    set_delete_switch(&harness, "order-1", run_id, true);
    wait_until(&harness, "the backlog is deleted", || {
        expired_segments(&harness, "order-1", run_id).is_empty()
    })
    .await;
    assert!(
        are_stored(&harness, &backlog)
            .await
            .iter()
            .all(|is_stored| !*is_stored)
    );

    // On to off keeps the objects that wait for their hour.
    write_until_expired(&harness, "order-1", run_id, 1).await;
    let kept = object_keys(
        &harness,
        "order-1",
        run_id,
        &expired_segments(&harness, "order-1", run_id),
    );
    let records = deletion_records(&harness).len();
    set_delete_switch(&harness, "order-1", run_id, false);
    assert!(!is_delete_pending(&harness, "order-1", run_id));
    tokio::time::sleep(2 * HOUR).await;
    harness.settle().await;
    assert!(
        are_stored(&harness, &kept)
            .await
            .iter()
            .all(|is_stored| *is_stored)
    );
    assert_eq!(deletion_records(&harness).len(), records);
    assert!(!expired_segments(&harness, "order-1", run_id).is_empty());
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_restart_resumes_deletions_only_for_runs_whose_switch_is_on() {
    let durable = DurableState::new();
    let first = EffectHarness::with_config(durable.clone(), EffectsConfig::default());
    first.rebuild_all_partitions().await;
    enable_history_offload(&first);
    let kept_run = start_run(&first, "order-1", expiring_settings(false));
    let deleted_run = start_run(&first, "order-2", expiring_settings(true));
    write_until_expired(&first, "order-1", kept_run, 2).await;
    write_until_expired(&first, "order-2", deleted_run, 2).await;
    let kept = object_keys(
        &first,
        "order-1",
        kept_run,
        &expired_segments(&first, "order-1", kept_run),
    );
    let deleted = object_keys(
        &first,
        "order-2",
        deleted_run,
        &expired_segments(&first, "order-2", deleted_run),
    );
    assert!(!is_delete_pending(&first, "order-1", kept_run));
    assert!(is_delete_pending(&first, "order-2", deleted_run));
    // The owner stops before the deletion delay passes.
    first.shutdown().await;

    let second = EffectHarness::with_config(durable, EffectsConfig::default());
    let mut rebuilt_effects = 0;
    for partition in 0..second.hasher.partition_count() {
        let partition = PartitionId(u16::try_from(partition).expect("few partitions"));
        rebuilt_effects += PartitionEffectRebuilder::rebuild_partition(&second.runner, partition)
            .await
            .expect("the partition rebuilds");
    }
    assert_eq!(
        rebuilt_effects, 1,
        "only the run whose switch is on starts deletion work"
    );
    tokio::time::sleep(2 * HOUR).await;
    wait_until(&second, "the switched-on run's objects are deleted", || {
        expired_segments(&second, "order-2", deleted_run).is_empty()
    })
    .await;
    assert!(
        are_stored(&second, &deleted)
            .await
            .iter()
            .all(|is_stored| !*is_stored)
    );
    assert!(
        are_stored(&second, &kept)
            .await
            .iter()
            .all(|is_stored| *is_stored)
    );
    for record in deletion_records(&second) {
        assert_eq!(record.command.run_id, deleted_run.as_bytes().to_vec());
    }
    second.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_purge_deletes_the_objects_of_expired_segments() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_id = start_run(
        &harness,
        "order-1",
        RunSettings {
            retention_after_close_micros: short_retention_settings().retention_after_close_micros,
            ..expiring_settings(false)
        },
    );
    write_until_expired(&harness, "order-1", run_id, 2).await;
    let expired = object_keys(
        &harness,
        "order-1",
        run_id,
        &expired_segments(&harness, "order-1", run_id),
    );
    close_run(&harness, "order-1", run_id);
    wait_until(&harness, "the closing segment is uploaded", || {
        !has_sealed_segment(&harness, "order-1", run_id)
    })
    .await;
    assert!(
        are_stored(&harness, &expired)
            .await
            .iter()
            .all(|is_stored| *is_stored)
    );

    tokio::time::sleep(HOUR).await;
    wait_until(&harness, "the purge commits", || {
        !run_purges(&harness).is_empty()
    })
    .await;
    let run = harness.run_location("order-1", run_id);
    let prefix = history_run_object_key_prefix(TenantId::DEFAULT, run.flow_hash, run_id);
    assert!(
        object_keys_with_prefix(&harness, &prefix).await.is_empty(),
        "the purge deleted the run's expired objects with the rest"
    );
    harness.shutdown().await;
}

/// Fails every object deletion while `is_failing` is set; a batch delete names no key.
#[derive(Default)]
struct DeleteFaults {
    is_failing: AtomicBool,
}

impl FaultInjector for DeleteFaults {
    fn action(&self, operation: ObjectStoreOperation, _key: &str) -> FaultAction {
        if operation == ObjectStoreOperation::Delete && self.is_failing.load(Ordering::SeqCst) {
            FaultAction::FailBeforeEffect
        } else {
            FaultAction::Proceed
        }
    }
}

#[tokio::test(start_paused = true)]
async fn a_switch_turned_off_while_deletion_retries_keeps_the_objects_once_storage_recovers() {
    let faults = Arc::new(DeleteFaults::default());
    let durable = DurableState {
        objects: Arc::new(InMemoryObjectStore::with_injector(faults.clone())),
        ..DurableState::new()
    };
    let harness = EffectHarness::with_config(durable, EffectsConfig::default());
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_id = start_run(&harness, "order-1", expiring_settings(true));
    write_until_expired(&harness, "order-1", run_id, 2).await;
    let kept = object_keys(
        &harness,
        "order-1",
        run_id,
        &expired_segments(&harness, "order-1", run_id),
    );

    // Deletion starts after the delay and keeps failing.
    faults.is_failing.store(true, Ordering::SeqCst);
    tokio::time::sleep(HOUR + 2 * MINUTE).await;
    harness.settle().await;
    let failed_deletes = harness
        .durable
        .objects
        .call_count(ObjectStoreOperation::Delete, "");
    assert!(failed_deletes > 0, "the owner tried to delete the objects");
    assert!(
        are_stored(&harness, &kept)
            .await
            .iter()
            .all(|is_stored| *is_stored)
    );

    // The switch turns off while the deletion retries, then storage recovers.
    set_delete_switch(&harness, "order-1", run_id, false);
    faults.is_failing.store(false, Ordering::SeqCst);
    tokio::time::sleep(2 * MINUTE).await;
    harness.settle().await;
    assert!(
        are_stored(&harness, &kept)
            .await
            .iter()
            .all(|is_stored| *is_stored),
        "a retry rechecks committed state, so the objects the switch keeps stay"
    );
    assert!(deletion_records(&harness).is_empty());
    assert_eq!(
        harness.metrics.history_expired_objects_deleted.get(),
        0,
        "no deletion succeeded"
    );
    assert!(!expired_segments(&harness, "order-1", run_id).is_empty());

    // Turning the switch on again deletes them.
    set_delete_switch(&harness, "order-1", run_id, true);
    wait_until(&harness, "the kept objects are deleted", || {
        expired_segments(&harness, "order-1", run_id).is_empty()
    })
    .await;
    assert!(
        are_stored(&harness, &kept)
            .await
            .iter()
            .all(|is_stored| !*is_stored)
    );
    harness.shutdown().await;
}

/// Lets every object storage call proceed.
struct NoFaults;

impl FaultInjector for NoFaults {
    fn action(&self, _operation: ObjectStoreOperation, _key: &str) -> FaultAction {
        FaultAction::Proceed
    }
}

#[tokio::test(start_paused = true)]
async fn the_orphan_sweep_deletes_hour_old_objects_of_runs_that_no_longer_exist() {
    let base = DurableState::new();
    let durable = DurableState {
        // Writes carry the owner's wall clock, so object ages follow paused time.
        objects: Arc::new(InMemoryObjectStore::with_injector_and_clock(
            Arc::new(NoFaults),
            base.clock.clone(),
        )),
        ..base
    };
    let harness = EffectHarness::with_config(
        durable,
        EffectsConfig {
            history_orphan_sweep_interval: 30 * MINUTE,
            ..EffectsConfig::default()
        },
    );
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let put = |key: String| {
        let objects = Arc::clone(&harness.durable.objects);
        async move {
            objects
                .put(
                    &key,
                    Bytes::from_static(b"orphan"),
                    WriteCondition::CreateOnly,
                )
                .await
                .expect("the object writes");
            key
        }
    };
    let gone_flow_hash = harness.hasher.flow_hash(TenantId::DEFAULT, "order-gone");
    let old_orphan = put(history_segment_object_key(
        TenantId::DEFAULT,
        gone_flow_hash,
        RunId([3; 16]),
        1,
        4,
    ))
    .await;
    let unknown_key = put("history/not-a-segment-key".to_owned()).await;
    let run_id = start_run(
        &harness,
        "order-1",
        RunSettings {
            history_segment_target_bytes: 1,
            ..settings()
        },
    );
    write_note(&harness, "order-1", run_id, 0);
    wait_until(&harness, "the run's segments are uploaded", || {
        !segments(&harness, "order-1", run_id).is_empty()
            && !has_sealed_segment(&harness, "order-1", run_id)
    })
    .await;
    let live_keys = object_keys(
        &harness,
        "order-1",
        run_id,
        &segments(&harness, "order-1", run_id),
    );

    tokio::time::sleep(40 * MINUTE).await;
    let young_orphan = put(history_segment_object_key(
        TenantId::DEFAULT,
        gone_flow_hash,
        RunId([4; 16]),
        1,
        1,
    ))
    .await;
    // Sweeps run every 30 minutes; the one at 90 minutes finds the first orphan an hour old.
    tokio::time::sleep(55 * MINUTE).await;
    wait_until(&harness, "the first orphan is deleted", || {
        harness.metrics.history_orphan_objects_deleted.get() == 1
    })
    .await;
    assert_eq!(
        are_stored(
            &harness,
            &[old_orphan, young_orphan.clone(), unknown_key.clone()]
        )
        .await,
        [false, true, true],
        "only the hour-old orphan goes; unknown keys stay"
    );
    assert!(
        are_stored(&harness, &live_keys)
            .await
            .iter()
            .all(|is_stored| *is_stored)
    );

    tokio::time::sleep(30 * MINUTE).await;
    wait_until(&harness, "the younger orphan is deleted", || {
        harness.metrics.history_orphan_objects_deleted.get() == 2
    })
    .await;
    assert_eq!(
        are_stored(&harness, &[young_orphan, unknown_key]).await,
        [false, true]
    );
    assert!(
        are_stored(&harness, &live_keys)
            .await
            .iter()
            .all(|is_stored| *is_stored),
        "objects of existing runs stay"
    );
    harness.shutdown().await;
}
