// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The sweep that expires abandoned CloneForkFlow stagings, with the real apply state machine.

use std::time::Duration;

use dexzd_apply::FlowStateReader;
use dexzd_keyspace::row_keys::flow_clone_fork_staging_key;
use dexzd_keyspace::{ColumnFamily, TenantId};
use dexzd_protocol::dex::{CloneForkFlowRequest, FlowCloneForkSource, FlowStatus};
use dexzd_protocol::dexzd::v1::{
    CheckpointRunStatus, FlowCloneForkStageCommand, FlowCloneForkStagedChunk,
    FlowCloneForkStagedTarget, FlowCommand, flow_command,
};
use prost::Message;

use super::history_upload_tests::wait_until;
use super::{EffectHarness, is_error_reply};
use crate::flow_coordination::{CommitUrgency, FlowAddress};

const MINUTE: Duration = Duration::from_secs(60);

/// A chunk 0 that announces more chunks, as an owner that stopped mid-staging leaves it.
fn abandoned_chunk(flow_id: &str, staging_id: Vec<u8>) -> flow_command::Command {
    flow_command::Command::FlowCloneForkStage(FlowCloneForkStageCommand {
        staging_id,
        chunk_index: 0,
        is_last_chunk: false,
        chunk: FlowCloneForkStagedChunk {
            target: Some(FlowCloneForkStagedTarget {
                request: Some(CloneForkFlowRequest {
                    source_flow_id: "source".to_owned(),
                    flow_id: flow_id.to_owned(),
                    request_id: "lost".to_owned(),
                    ..CloneForkFlowRequest::default()
                }),
                source: Some(FlowCloneForkSource::default()),
                source_run_seq: 1,
                first_run_seq: 2,
                flow_type: "SessionFlow".to_owned(),
                run_status: Some(CheckpointRunStatus {
                    status: FlowStatus::Running as i32,
                    ..CheckpointRunStatus::default()
                }),
            }),
            ..FlowCloneForkStagedChunk::default()
        }
        .encode_to_vec(),
    })
}

/// An idle staging stays for 10 minutes; then the sweep proposes its expiry.
#[tokio::test(start_paused = true)]
async fn the_sweep_expires_a_staging_idle_for_ten_minutes() {
    let harness = EffectHarness::new();
    harness.rebuild_all_partitions().await;
    let flow_id = "abandoned-clone";
    let flow = FlowAddress::new(TenantId::DEFAULT, flow_id);
    let staging_id = vec![4; 16];
    let reply = harness
        .flow_effect_operations
        .commit(
            &flow,
            FlowCommand {
                flow_id: flow_id.to_owned(),
                command: Some(abandoned_chunk(flow_id, staging_id.clone())),
                ..FlowCommand::default()
            },
            CommitUrgency::Prompt,
        )
        .expect("the chunk commits");
    assert!(!is_error_reply(&reply), "{reply:?}");
    let key = flow_clone_fork_staging_key(
        harness.hasher.flow_hash(TenantId::DEFAULT, flow_id),
        &staging_id,
    );
    let has_staging = || {
        harness
            .flow_effect_operations
            .snapshot()
            .get(ColumnFamily::Flow, &key)
            .expect("memory reads")
            .is_some()
    };
    tokio::time::sleep(9 * MINUTE).await;
    harness.settle().await;
    assert!(
        has_staging(),
        "a staging idle for less than 10 minutes stays"
    );
    tokio::time::sleep(2 * MINUTE).await;
    wait_until(&harness, "the sweep expires the staging", || !has_staging()).await;
    assert!(
        !harness
            .flow_effect_operations
            .commands_matching(|command| matches!(
                command,
                flow_command::Command::FlowCloneForkStagingExpire(_)
            ))
            .is_empty()
    );
}
