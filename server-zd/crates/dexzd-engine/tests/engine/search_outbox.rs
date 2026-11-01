// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The SearchOutbox queue and projection modes recover exactly from a snapshot and the log.

use dexzd_apply::FlowStateReader;
use dexzd_engine::{CommitUrgency, EngineNode, LocalStoreConfig};
use dexzd_keyspace::row_keys::{partition_index_range, search_projection_mode_key};
use dexzd_keyspace::{ColumnFamily, IndexKind, PartitionHasher, PartitionId, RunId, TenantId};
use dexzd_protocol::dex::{
    AttributeWrite, FlowTimeoutPolicy, IndexConfig, IndexType, SetAttributesRequest,
    StartFlowRequest, Value, WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributesSetCommand, FlowCommand, FlowStartCommand, RunSettings, SearchOutboxAckCommand,
    SetSearchProjectionModeCommand, command_reply, flow_command::Command,
};

use crate::harness::{PARTITION_COUNT, TestCluster};
use crate::snapshots::start_deterministic_node;

const FLOW_COUNT: u8 = 6;

#[tokio::test(start_paused = true)]
async fn search_outbox_and_modes_recover_exactly_from_a_snapshot_and_the_log() {
    let cluster = TestCluster::new();
    let mut config = cluster.config(LocalStoreConfig::InMemory);
    config.commit.debug_replay_checks = true;
    let (first, _, hasher, _) = start_deterministic_node(&cluster, &config).await;
    set_modes(&first, &hasher, true).await;
    for number in 0..FLOW_COUNT {
        start_flow(&first, &hasher, number).await;
        set_counter(&first, &hasher, number, 1).await;
    }
    ack_first_entry_of_each_partition(&first, &hasher).await;
    first.take_snapshot().await.expect("snapshot succeeds");
    for number in 0..FLOW_COUNT {
        set_counter(&first, &hasher, number, 2).await;
    }
    ack_first_entry_of_each_partition(&first, &hasher).await;
    submit_partition_command(
        &first,
        &hasher,
        PartitionId(0),
        Command::SetSearchProjectionMode(SetSearchProjectionModeCommand { enabled: false }),
    )
    .await;
    submit_partition_command(
        &first,
        &hasher,
        PartitionId(0),
        Command::SetSearchProjectionMode(SetSearchProjectionModeCommand { enabled: true }),
    )
    .await;
    for number in 0..FLOW_COUNT {
        set_counter(&first, &hasher, number, 3).await;
    }
    let continuous = first
        .committer()
        .committed_view()
        .expect("continuous state view");
    assert!(
        outbox_entry_count(&continuous) > 0,
        "the queue holds entries"
    );
    drop(first);

    let (second, _, _, _) = start_deterministic_node(&cluster, &config).await;
    let recovered = second
        .committer()
        .committed_view()
        .expect("recovered state view");
    dexzd_storage::compare_store_views(
        &continuous,
        &recovered,
        &[
            dexzd_storage::CanonicalizedStoreField::NodeLineageId,
            dexzd_storage::CanonicalizedStoreField::NodeLastSnapshotNumber,
            dexzd_storage::CanonicalizedStoreField::PartitionApplied,
            dexzd_storage::CanonicalizedStoreField::PartitionAppliedEpoch,
        ],
    )
    .unwrap_or_else(|error| panic!("recovery differs from live apply: {error}"));
    assert_eq!(
        outbox_entry_count(&recovered),
        outbox_entry_count(&continuous)
    );
    second.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn disabling_clears_the_queue_and_disabled_partitions_append_nothing() {
    let cluster = TestCluster::new();
    let config = cluster.config(LocalStoreConfig::InMemory);
    let (node, _, hasher, _) = start_deterministic_node(&cluster, &config).await;
    for number in 0..FLOW_COUNT {
        start_flow(&node, &hasher, number).await;
    }
    let view = node.committer().committed_view().expect("state view");
    assert_eq!(outbox_entry_count(&view), 0, "no mode appends nothing");
    assert!(mode_records(&view).iter().all(Option::is_none));

    set_modes(&node, &hasher, true).await;
    for number in 0..FLOW_COUNT {
        set_counter(&node, &hasher, number, 1).await;
    }
    let view = node.committer().committed_view().expect("state view");
    assert_eq!(outbox_entry_count(&view), usize::from(FLOW_COUNT));

    set_modes(&node, &hasher, false).await;
    for number in 0..FLOW_COUNT {
        set_counter(&node, &hasher, number, 2).await;
    }
    let view = node.committer().committed_view().expect("state view");
    assert_eq!(outbox_entry_count(&view), 0, "disabling clears the queue");
    assert!(
        mode_records(&view)
            .iter()
            .all(|is_enabled| *is_enabled == Some(false))
    );
    node.shutdown().await.expect("lease released");
}

async fn set_modes(node: &EngineNode, hasher: &PartitionHasher, is_enabled: bool) {
    for partition in 0..PARTITION_COUNT {
        submit_partition_command(
            node,
            hasher,
            PartitionId(u16::try_from(partition).expect("few partitions")),
            Command::SetSearchProjectionMode(SetSearchProjectionModeCommand {
                enabled: is_enabled,
            }),
        )
        .await;
    }
}

async fn ack_first_entry_of_each_partition(node: &EngineNode, hasher: &PartitionHasher) {
    let view = node.committer().committed_view().expect("state view");
    for partition in 0..PARTITION_COUNT {
        let partition = PartitionId(u16::try_from(partition).expect("few partitions"));
        let (start, end) = partition_index_range(partition, IndexKind::SearchOutbox);
        let Some((key, _)) = view
            .scan(ColumnFamily::PartitionIndex, &start, &end, 1)
            .expect("scan the queue")
            .into_iter()
            .next()
        else {
            continue;
        };
        let entry = dexzd_keyspace::row_keys::decode_search_outbox_key(partition, &key)
            .expect("queue keys decode");
        submit_partition_command(
            node,
            hasher,
            partition,
            Command::SearchOutboxAck(SearchOutboxAckCommand {
                partition: u32::from(partition.0),
                through_seq: entry.seq,
            }),
        )
        .await;
    }
}

async fn submit_partition_command(
    node: &EngineNode,
    hasher: &PartitionHasher,
    partition: PartitionId,
    command: Command,
) {
    let flow_id = hasher.partition_command_flow_ids()[usize::from(partition.0)].clone();
    let context = dexzd_apply::ApplyContext {
        partition,
        flow_hash: hasher.flow_hash(TenantId::DEFAULT, &flow_id),
    };
    let (_, ticket) = node
        .committer()
        .submit(
            context,
            FlowCommand {
                flow_id,
                command: Some(command),
                ..FlowCommand::default()
            },
            CommitUrgency::Prompt,
        )
        .expect("partition command submitted");
    let committed = ticket.wait().await.expect("partition command committed");
    assert!(
        !matches!(
            committed.outcome.reply.reply,
            Some(command_reply::Reply::Error(_))
        ),
        "{:?}",
        committed.outcome.reply
    );
}

fn flow_id(number: u8) -> String {
    format!("outbox-recovery-flow-{number}")
}

async fn start_flow(node: &EngineNode, hasher: &PartitionHasher, number: u8) {
    let flow_id = flow_id(number);
    submit_flow_command(
        node,
        hasher,
        &flow_id,
        Vec::new(),
        Command::FlowStart(FlowStartCommand {
            new_run_id: vec![number; 16],
            request: Some(StartFlowRequest {
                flow_id: flow_id.clone(),
                flow_type: "OutboxRecoveryFlow".to_owned(),
                start_step_type: "StartStep".to_owned(),
                request_id: format!("{flow_id}-start"),
                ..StartFlowRequest::default()
            }),
            settings: Some(RunSettings {
                history_window_semantic_events: 10_000,
                history_prune_chunk_semantic_events: 1000,
                max_retained_superseded_bytes: 256 << 20,
                retention_after_close_micros: 7 * 24 * 3600 * 1_000_000,
                wire_inline_threshold_bytes: 100,
                max_value_bytes: 16 << 20,
                checkpoint_min_written_bytes: 1 << 20,
                checkpoint_max_commands: 10_000,
                request_dedup_capacity: 1000,
                max_run_state_bytes: 1 << 30,
                max_active_step_executions: 10_000,
            }),
            resolved_timeout_policy: FlowTimeoutPolicy::Fail as i32,
            worker_target: Some(WorkerTarget { is_pull_mode: true }),
            ..FlowStartCommand::default()
        }),
    )
    .await;
}

async fn set_counter(node: &EngineNode, hasher: &PartitionHasher, number: u8, counter: i64) {
    submit_flow_command(
        node,
        hasher,
        &flow_id(number),
        RunId([number; 16]).as_bytes().to_vec(),
        Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                flow_id: flow_id(number),
                attributes: vec![AttributeWrite {
                    key: "counter".to_owned(),
                    value: Some(Value {
                        kind: Some(value::Kind::IntValue(counter)),
                    }),
                    index_config: Some(IndexConfig {
                        enable: true,
                        r#type: IndexType::Int as i32,
                        ..IndexConfig::default()
                    }),
                    ..AttributeWrite::default()
                }],
                ..SetAttributesRequest::default()
            }),
            ..AttributesSetCommand::default()
        }),
    )
    .await;
}

async fn submit_flow_command(
    node: &EngineNode,
    hasher: &PartitionHasher,
    flow_id: &str,
    run_id: Vec<u8>,
    command: Command,
) {
    let flow_hash = hasher.flow_hash(TenantId::DEFAULT, flow_id);
    let context = dexzd_apply::ApplyContext {
        partition: hasher.partition_of(flow_hash),
        flow_hash,
    };
    let (_, ticket) = node
        .committer()
        .submit(
            context,
            FlowCommand {
                flow_id: flow_id.to_owned(),
                run_id,
                command: Some(command),
                ..FlowCommand::default()
            },
            CommitUrgency::Prompt,
        )
        .expect("Flow command submitted");
    let committed = ticket.wait().await.expect("Flow command committed");
    assert!(
        !matches!(
            committed.outcome.reply.reply,
            Some(command_reply::Reply::Error(_))
        ),
        "{flow_id}: {:?}",
        committed.outcome.reply
    );
}

fn outbox_entry_count(view: &dyn FlowStateReader) -> usize {
    (0..PARTITION_COUNT)
        .map(|partition| {
            let partition = PartitionId(u16::try_from(partition).expect("few partitions"));
            let (start, end) = partition_index_range(partition, IndexKind::SearchOutbox);
            view.scan(ColumnFamily::PartitionIndex, &start, &end, usize::MAX)
                .expect("scan the queue")
                .len()
        })
        .sum()
}

fn mode_records(view: &dyn FlowStateReader) -> Vec<Option<bool>> {
    (0..PARTITION_COUNT)
        .map(|partition| {
            let partition = PartitionId(u16::try_from(partition).expect("few partitions"));
            view.get(
                ColumnFamily::PartitionIndex,
                &search_projection_mode_key(partition),
            )
            .expect("read the mode")
            .map(|bytes| {
                <dexzd_protocol::dexzd::v1::SearchProjectionModeRecord as prost::Message>::decode(
                    bytes.as_slice(),
                )
                .expect("the mode decodes")
                .enabled
            })
        })
        .collect()
}
