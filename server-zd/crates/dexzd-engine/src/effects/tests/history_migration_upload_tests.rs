// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Uploads of history that a run recorded before its partition offloaded history.

use std::collections::BTreeMap;
use std::time::Duration;

use dexzd_apply::FlowStateReader;
use dexzd_apply::read_model::read_history_segment_content;
use dexzd_history_segment::{
    HistorySegmentContent, decode_history_segment, encode_history_segment,
    history_run_object_key_prefix, history_segment_object_key,
};
use dexzd_keyspace::row_keys::prefix_range;
use dexzd_keyspace::{ColumnFamily, RunId, RunTable, TenantId, run_table_prefix};
use dexzd_object_store::ObjectStore;
use dexzd_protocol::dex::{AttributeWrite, SetAttributesRequest, StopFlowRequest, StopType};
use dexzd_protocol::dexzd::v1::{
    AttributesSetCommand, FlowCommand, FlowStopCommand, HistorySegmentRecord, HistorySegmentState,
    RunSettings, SetHistoryOffloadModeCommand, flow_command,
};
use prost::Message;

use super::{EffectHarness, is_error_reply, settings, start_request, string_value};
use crate::flow_coordination::CommitUrgency;

/// Rounds of 100 ms that a wait allows; paused time makes them instant.
const WAIT_ROUNDS: usize = 3_000;
const FLOW_ID: &str = "legacy-order";

#[tokio::test(start_paused = true)]
async fn a_run_started_while_offload_was_off_uploads_all_its_history_once_offload_is_on() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let run_id = harness.start_flow(
        start_request(FLOW_ID),
        RunSettings {
            history_segment_target_bytes: 1,
            ..settings()
        },
    );
    for round in 0..8 {
        write_notes(&harness, run_id, round);
    }
    assert_eq!(
        harness
            .run_record(FLOW_ID, run_id)
            .history_open_segment_first_seq,
        0
    );
    assert!(segments(&harness, run_id).is_empty());

    set_history_offload_mode(&harness, true);
    let mut commands = 0;
    loop {
        write_notes(&harness, run_id, 8 + commands);
        commands += 1;
        if harness
            .run_record(FLOW_ID, run_id)
            .history_migration_started_seq
            == 0
        {
            break;
        }
        assert!(commands < 30, "the migration catches up");
    }
    assert!(commands >= 2, "the recorded history took several commands");
    stop_run(&harness, run_id);
    let sealed = sealed_contents(&harness, run_id);

    wait_until(&harness, "every segment is uploaded", || {
        let record = harness.run_record(FLOW_ID, run_id);
        segments(&harness, run_id).iter().all(|segment| {
            segment.state == HistorySegmentState::Uploaded as i32
                && segment.last_seq <= record.history_uploaded_through_seq
        }) && record.history_uploaded_through_seq == record.next_run_seq - 1
    })
    .await;
    assert_uploaded_chain_from_the_first_seq(&harness, run_id, &sealed).await;
    harness.shutdown().await;
}

/// Contents of the run's SEALED segments by first seq, read before their upload lets offload
/// delete the local rows.
fn sealed_contents(harness: &EffectHarness, run_id: RunId) -> BTreeMap<u64, HistorySegmentContent> {
    let run = harness.run_location(FLOW_ID, run_id);
    segments(harness, run_id)
        .into_iter()
        .filter(|segment| segment.state == HistorySegmentState::Sealed as i32)
        .map(|segment| {
            let content = read_history_segment_content(
                &harness.flow_effect_operations.snapshot(),
                run.flow_hash,
                run_id,
                segment.first_seq,
            )
            .expect("the segment content reads")
            .expect("the segment exists");
            (segment.first_seq, content)
        })
        .collect()
}

/// Requires contiguous segments from seq 1 and event 1, each stored as one object whose content
/// and checksum match `sealed`, the committed content when it sealed, and no other object of the
/// run.
async fn assert_uploaded_chain_from_the_first_seq(
    harness: &EffectHarness,
    run_id: RunId,
    sealed: &BTreeMap<u64, HistorySegmentContent>,
) {
    let run = harness.run_location(FLOW_ID, run_id);
    let mut next_seq = 1;
    let mut next_event_id = 1;
    let mut expected_keys = Vec::new();
    for segment in segments(harness, run_id) {
        assert_eq!(
            (segment.first_seq, segment.first_event_id),
            (next_seq, next_event_id),
            "{segment:?}"
        );
        next_seq = segment.last_seq + 1;
        next_event_id = segment.last_event_id + 1;
        let key = history_segment_object_key(
            TenantId::DEFAULT,
            run.flow_hash,
            run_id,
            segment.first_seq,
            segment.last_seq,
        );
        let object = harness
            .durable
            .objects
            .get(&key)
            .await
            .expect("the in-memory store reads")
            .unwrap_or_else(|| panic!("{key} exists"))
            .bytes;
        let content = sealed
            .get(&segment.first_seq)
            .unwrap_or_else(|| panic!("segment {segment:?} was read when it sealed"))
            .clone();
        assert_eq!(
            decode_history_segment(&object).expect("the object decodes"),
            content
        );
        let encoded = encode_history_segment(&content).expect("committed content encodes");
        assert_eq!(segment.content_checksum, encoded.content_checksum.to_vec());
        expected_keys.push(key);
    }
    let prefix = history_run_object_key_prefix(TenantId::DEFAULT, run.flow_hash, run_id);
    let stored_keys: Vec<String> = harness
        .durable
        .objects
        .list(&prefix, None, 1000)
        .await
        .expect("the in-memory store lists")
        .objects
        .into_iter()
        .map(|object| object.key)
        .collect();
    assert_eq!(stored_keys, expected_keys);
}

fn write_notes(harness: &EffectHarness, run_id: RunId, round: usize) {
    commit(
        harness,
        run_id,
        flow_command::Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                flow_id: FLOW_ID.to_owned(),
                attributes: vec![AttributeWrite {
                    key: "notes".to_owned(),
                    value: Some(string_value(&format!("v{round} {}", "x".repeat(200)))),
                    ..AttributeWrite::default()
                }],
                request_id: format!("write-{round}"),
                ..SetAttributesRequest::default()
            }),
            hard_delete_objects: Vec::new(),
            request_fingerprint: vec![1],
        }),
    );
}

fn stop_run(harness: &EffectHarness, run_id: RunId) {
    commit(
        harness,
        run_id,
        flow_command::Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                flow_id: FLOW_ID.to_owned(),
                stop_type: StopType::Terminate as i32,
                ..StopFlowRequest::default()
            }),
        }),
    );
}

fn commit(harness: &EffectHarness, run_id: RunId, body: flow_command::Command) {
    let reply = harness
        .flow_effect_operations
        .commit(
            &EffectHarness::flow(FLOW_ID),
            FlowCommand {
                flow_id: FLOW_ID.to_owned(),
                run_id: run_id.as_bytes().to_vec(),
                command: Some(body),
                ..FlowCommand::default()
            },
            CommitUrgency::Prompt,
        )
        .expect("the command commits");
    assert!(!is_error_reply(&reply), "the command failed: {reply:?}");
}

/// Commits the offload mode to every partition, as a node with `history.offloadEnabled` does.
fn set_history_offload_mode(harness: &EffectHarness, is_enabled: bool) {
    for (partition, flow_id) in harness
        .hasher
        .partition_command_flow_ids()
        .into_iter()
        .enumerate()
    {
        let reply = harness
            .flow_effect_operations
            .commit(
                &EffectHarness::flow(&flow_id),
                FlowCommand {
                    flow_id: flow_id.clone(),
                    command: Some(flow_command::Command::SetHistoryOffloadMode(
                        SetHistoryOffloadModeCommand {
                            enabled: is_enabled,
                            partition: u32::try_from(partition).expect("few partitions"),
                        },
                    )),
                    ..FlowCommand::default()
                },
                CommitUrgency::Prompt,
            )
            .expect("the mode commits");
        assert!(!is_error_reply(&reply), "the mode failed: {reply:?}");
    }
}

fn segments(harness: &EffectHarness, run_id: RunId) -> Vec<HistorySegmentRecord> {
    let run = harness.run_location(FLOW_ID, run_id);
    let prefix = run_table_prefix(run.flow_hash, run.run_id, RunTable::HistorySegment);
    let (start, end) = prefix_range(&prefix);
    harness
        .flow_effect_operations
        .snapshot()
        .scan(ColumnFamily::Flow, &start, &end, usize::MAX)
        .expect("memory reads never fail")
        .into_iter()
        .map(|(_, bytes)| HistorySegmentRecord::decode(bytes.as_slice()).expect("decodes"))
        .collect()
}

/// Waits until `condition` holds, letting tasks and paused time advance.
async fn wait_until(harness: &EffectHarness, what: &str, condition: impl Fn() -> bool) {
    for _ in 0..WAIT_ROUNDS {
        harness.settle().await;
        if condition() {
            return;
        }
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
    panic!("timed out waiting until {what}");
}
