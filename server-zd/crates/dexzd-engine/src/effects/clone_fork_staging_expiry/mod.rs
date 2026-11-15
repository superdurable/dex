// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Expiry of abandoned CloneForkFlow stagings. A staging belongs to no run, so no run command
//! cleans it up: each owned partition is scanned once a minute, and a staging that no chunk
//! extended for 10 minutes gets a FlowCloneForkStagingExpire command. Apply decides again from
//! the command's own time, so a staging extended meanwhile stays.

use std::sync::Arc;
use std::time::Duration;

use dexzd_keyspace::row_keys::{
    decode_flow_clone_fork_staging_index_key, flow_clone_fork_staging_index_key,
};
use dexzd_keyspace::{ColumnFamily, FlowHash, IndexKind, PartitionId, TenantId};
use dexzd_protocol::dexzd::v1::{
    FlowCloneForkStagingExpireCommand, FlowCloneForkStagingIndexRecord, FlowCommand, flow_command,
};

use crate::effects::state_reads::decode_row;
use crate::effects::{EffectContext, EffectError};
use crate::flow_coordination::{CommitUrgency, FlowAddress};

/// How often every owned partition is scanned for idle stagings.
const STAGING_SCAN_INTERVAL: Duration = Duration::from_secs(60);

/// Command time after which a staging that no chunk extended is abandoned; apply uses the same.
const IDLE_STAGING_MICROS: i64 = 10 * 60 * 1_000_000;

/// Stagings read per partition scan; a partition holds at most 64 at once.
const MAX_STAGINGS_PER_SCAN: usize = 64;

/// Expires the idle stagings of every owned partition once a minute until shutdown.
pub(crate) async fn run_clone_fork_staging_expiry(context: Arc<EffectContext>) {
    let mut ticker = tokio::time::interval(STAGING_SCAN_INTERVAL);
    ticker.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Delay);
    loop {
        // Biased for reproducible simulation; shutdown fires once.
        tokio::select! {
            biased;
            () = context.shutdown.cancelled() => break,
            _ = ticker.tick() => {}
        }
        let partitions = context.task_queue_memory().owned_partition_list();
        for partition in partitions {
            match expire_idle_stagings(&context, partition).await {
                Ok(()) => {}
                Err(EffectError::Stopped) => return,
                Err(error) => {
                    tracing::warn!(%error, %partition, "expiring idle clone fork stagings failed");
                }
            }
        }
    }
}

/// Proposes the expiry of each staging of `partition` that no chunk extended for 10 minutes.
async fn expire_idle_stagings(
    context: &EffectContext,
    partition: PartitionId,
) -> Result<(), EffectError> {
    let idle_before_micros = context
        .clock
        .now_unix_micros()
        .saturating_sub(IDLE_STAGING_MICROS);
    let idle_stagings = {
        let reader = context.flow_effect_operations.committed_state_reader()?;
        let start =
            dexzd_keyspace::partition_index_prefix(partition, IndexKind::FlowCloneForkStaging);
        let end = flow_clone_fork_staging_index_key(
            partition,
            idle_before_micros.saturating_add(1),
            FlowHash(0),
            &[],
        );
        reader.scan(
            ColumnFamily::PartitionIndex,
            &start,
            &end,
            MAX_STAGINGS_PER_SCAN,
        )?
    };
    for (key, value) in idle_stagings {
        let Some((_, _, staging_id)) = decode_flow_clone_fork_staging_index_key(&key) else {
            return Err(EffectError::CorruptState {
                table: "clone fork staging index",
                key: key.iter().map(|byte| format!("{byte:02x}")).collect(),
                detail: "the key has another layout".to_owned(),
            });
        };
        let index: FlowCloneForkStagingIndexRecord =
            decode_row("clone fork staging index", &key, &value)?;
        let flow = FlowAddress::new(TenantId(index.tenant_id), index.flow_id.clone());
        let command = FlowCommand {
            flow_id: index.flow_id,
            tenant_id: index.tenant_id,
            command: Some(flow_command::Command::FlowCloneForkStagingExpire(
                FlowCloneForkStagingExpireCommand {
                    staging_id: staging_id.to_vec(),
                },
            )),
            ..FlowCommand::default()
        };
        if context
            .propose_until_committed(&flow, command, CommitUrgency::Batched)
            .await
            .is_none()
        {
            return Err(EffectError::Stopped);
        }
    }
    Ok(())
}
