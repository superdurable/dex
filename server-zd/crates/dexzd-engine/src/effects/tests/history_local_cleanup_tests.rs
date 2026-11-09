// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Proposing HistoryLocalCleanup with the real apply state machine.

use std::time::Duration;

use dexzd_apply::FlowStateReader;
use dexzd_keyspace::row_keys::{history_local_cleanup_pending_index_key, prefix_range};
use dexzd_keyspace::{ColumnFamily, PartitionId, RunId, RunTable, run_table_prefix};
use dexzd_protocol::dex::{AttributeWrite, SetAttributesRequest, StartFlowRequest};
use dexzd_protocol::dexzd::v1::{
    AttributesSetCommand, FlowCommand, HistorySegmentState, RunSettings, ValueBirthRecord,
    flow_command,
};
use prost::Message;

use super::history_upload_tests::{
    enable_history_offload, segments, set_history_offload_mode, wait_until,
};
use super::{
    DurableState, EffectHarness, RecordedCommand, is_error_reply, ok_reply, settings, string_value,
};
use crate::effects::EffectsConfig;
use crate::flow_coordination::CommitUrgency;

const MINUTE: Duration = Duration::from_secs(60);

/// Every command seals its own segment, so each superseded value uploads at once.
fn cleanup_settings(grace_period: Duration) -> RunSettings {
    RunSettings {
        history_segment_target_bytes: 1,
        superseded_value_grace_micros: i64::try_from(grace_period.as_micros())
            .expect("the grace period fits"),
        ..settings()
    }
}

/// Starts a run without a start Step, so it has no Step work.
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

/// Rewrites the run's `profile` Attribute with a value stored in the value table.
fn write_profile(harness: &EffectHarness, flow_id: &str, run_id: RunId, round: usize) {
    let command = FlowCommand {
        flow_id: flow_id.to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        command: Some(flow_command::Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                flow_id: flow_id.to_owned(),
                attributes: vec![AttributeWrite {
                    key: "profile".to_owned(),
                    value: Some(string_value(&format!(
                        "profile {round} {}",
                        "x".repeat(200)
                    ))),
                    ..AttributeWrite::default()
                }],
                request_id: uuid::Uuid::now_v7().to_string(),
                ..SetAttributesRequest::default()
            }),
            hard_delete_objects: Vec::new(),
            request_fingerprint: vec![1],
        })),
        ..FlowCommand::default()
    };
    let reply = harness
        .flow_effect_operations
        .commit(
            &EffectHarness::flow(flow_id),
            command,
            CommitUrgency::Prompt,
        )
        .expect("the write commits");
    assert!(!is_error_reply(&reply), "the write failed: {reply:?}");
}

async fn wait_until_uploaded(harness: &EffectHarness, flow_id: &str, run_id: RunId) {
    wait_until(harness, "every sealed segment is uploaded", || {
        segments(harness, flow_id, run_id)
            .iter()
            .all(|segment| segment.state != HistorySegmentState::Sealed as i32)
    })
    .await;
}

fn cleanup_commands(harness: &EffectHarness) -> Vec<RecordedCommand> {
    harness.flow_effect_operations.commands_matching(|command| {
        matches!(command, flow_command::Command::HistoryLocalCleanup(_))
    })
}

fn is_cleanup_pending(harness: &EffectHarness, flow_id: &str, run_id: RunId) -> bool {
    let run = harness.run_location(flow_id, run_id);
    let partition: PartitionId = harness.partition_of(flow_id);
    harness
        .flow_effect_operations
        .snapshot()
        .get(
            ColumnFamily::PartitionIndex,
            &history_local_cleanup_pending_index_key(partition, run.flow_hash, run.run_id),
        )
        .expect("memory reads never fail")
        .is_some()
}

/// Returns the rows of one run table.
fn table_rows(
    harness: &EffectHarness,
    flow_id: &str,
    run_id: RunId,
    table: RunTable,
) -> Vec<Vec<u8>> {
    let run = harness.run_location(flow_id, run_id);
    let (start, end) = prefix_range(&run_table_prefix(run.flow_hash, run.run_id, table));
    harness
        .flow_effect_operations
        .snapshot()
        .scan(ColumnFamily::Flow, &start, &end, usize::MAX)
        .expect("memory reads never fail")
        .into_iter()
        .map(|(_, value)| value)
        .collect()
}

/// Superseded values the run still stores locally.
fn local_superseded_value_count(harness: &EffectHarness, flow_id: &str, run_id: RunId) -> usize {
    table_rows(harness, flow_id, run_id, RunTable::ValueGarbage).len()
}

fn offloaded_value_count(harness: &EffectHarness, flow_id: &str, run_id: RunId) -> usize {
    table_rows(harness, flow_id, run_id, RunTable::ValueBirth)
        .iter()
        .map(|bytes| ValueBirthRecord::decode(bytes.as_slice()).expect("birth decodes"))
        .filter(|birth| birth.is_offloaded)
        .count()
}

#[tokio::test(start_paused = true)]
async fn an_idle_run_offloads_its_superseded_value_after_the_grace_period_without_commands() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_id = start_run(&harness, "order-1", cleanup_settings(2 * MINUTE));
    write_profile(&harness, "order-1", run_id, 1);
    write_profile(&harness, "order-1", run_id, 2);
    wait_until_uploaded(&harness, "order-1", run_id).await;
    assert!(is_cleanup_pending(&harness, "order-1", run_id));
    assert_eq!(local_superseded_value_count(&harness, "order-1", run_id), 1);

    tokio::time::sleep(MINUTE).await;
    harness.settle().await;
    assert_eq!(
        local_superseded_value_count(&harness, "order-1", run_id),
        1,
        "the value stays local during its grace period"
    );
    assert!(cleanup_commands(&harness).is_empty());

    tokio::time::sleep(2 * MINUTE).await;
    wait_until(
        &harness,
        "the idle run's superseded value is offloaded",
        || {
            local_superseded_value_count(&harness, "order-1", run_id) == 0
                && !is_cleanup_pending(&harness, "order-1", run_id)
        },
    )
    .await;
    assert_eq!(offloaded_value_count(&harness, "order-1", run_id), 1);
    let commands = cleanup_commands(&harness);
    assert_eq!(commands.len(), 1, "one cleanup offloads the value");
    assert_eq!(commands[0].reply, ok_reply());
    assert_eq!(commands[0].urgency, CommitUrgency::Batched);
    assert_eq!(commands[0].command.expected_run_attempt, 0);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_busy_run_commits_no_cleanup_until_it_goes_quiet() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_id = start_run(&harness, "order-1", cleanup_settings(MINUTE));
    for round in 0..15 {
        write_profile(&harness, "order-1", run_id, round);
        wait_until_uploaded(&harness, "order-1", run_id).await;
        tokio::time::sleep(Duration::from_secs(20)).await;
    }
    assert!(
        cleanup_commands(&harness).is_empty(),
        "every command of a busy run moves its cleanup a quiet period later"
    );
    assert!(
        offloaded_value_count(&harness, "order-1", run_id) > 0,
        "the run's own commands offloaded values whose grace period passed"
    );

    tokio::time::sleep(3 * MINUTE).await;
    wait_until(
        &harness,
        "the quiet run's last values are offloaded",
        || {
            local_superseded_value_count(&harness, "order-1", run_id) == 0
                && !is_cleanup_pending(&harness, "order-1", run_id)
        },
    )
    .await;
    assert!(!cleanup_commands(&harness).is_empty());
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn an_idle_run_resumes_its_cleanup_after_offload_turns_on_again() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_id = start_run(&harness, "order-1", cleanup_settings(2 * MINUTE));
    write_profile(&harness, "order-1", run_id, 1);
    write_profile(&harness, "order-1", run_id, 2);
    wait_until_uploaded(&harness, "order-1", run_id).await;
    set_history_offload_mode(&harness, false);

    tokio::time::sleep(5 * MINUTE).await;
    harness.settle().await;
    assert_eq!(cleanup_commands(&harness).len(), 1, "the due cleanup ran");
    assert_eq!(local_superseded_value_count(&harness, "order-1", run_id), 1);
    assert!(
        is_cleanup_pending(&harness, "order-1", run_id),
        "the cleanup stays pending while offload is off"
    );

    set_history_offload_mode(&harness, true);
    tokio::time::sleep(60 * MINUTE).await;
    wait_until(&harness, "the cleanup resumes once offload is on", || {
        local_superseded_value_count(&harness, "order-1", run_id) == 0
            && !is_cleanup_pending(&harness, "order-1", run_id)
    })
    .await;
    assert_eq!(offloaded_value_count(&harness, "order-1", run_id), 1);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_restarted_owner_resumes_the_pending_cleanup_from_committed_state() {
    let durable = DurableState::new();
    let first = EffectHarness::with_config(durable.clone(), EffectsConfig::default());
    first.rebuild_all_partitions().await;
    enable_history_offload(&first);
    let run_id = start_run(&first, "order-1", cleanup_settings(2 * MINUTE));
    write_profile(&first, "order-1", run_id, 1);
    write_profile(&first, "order-1", run_id, 2);
    wait_until_uploaded(&first, "order-1", run_id).await;
    assert!(is_cleanup_pending(&first, "order-1", run_id));
    assert!(cleanup_commands(&first).is_empty());
    first.shutdown().await;

    // The new owner learns of the pending cleanup only from its rebuild.
    let second = EffectHarness::with_config(durable, EffectsConfig::default());
    second.rebuild_all_partitions().await;
    tokio::time::sleep(3 * MINUTE).await;
    wait_until(
        &second,
        "the new owner offloads the superseded value",
        || {
            local_superseded_value_count(&second, "order-1", run_id) == 0
                && !is_cleanup_pending(&second, "order-1", run_id)
        },
    )
    .await;
    assert_eq!(offloaded_value_count(&second, "order-1", run_id), 1);
    assert_eq!(cleanup_commands(&second).len(), 1);
    second.shutdown().await;
}
