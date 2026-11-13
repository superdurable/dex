// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Object storage deletions that precede a command: hard-delete objects replaced by a committed
//! write, and a run's history segment objects and whole hard-delete prefix before its retention
//! purge.

use std::collections::BTreeSet;

use dexzd_keyspace::row_keys::HARD_DELETE_PENDING_OBJECT;
use dexzd_keyspace::{ColumnFamily, RunId, RunTable, run_table_prefix};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, HardDeleteObjectRecord, HardDeleteObjectsDeletedCommand, RunPurgeCommand,
    command_reply, flow_command,
};

use crate::effects::hard_delete_objects::{HardDeleteObjectOwner, HardDeletePrefixListing};
use crate::effects::history_uploads::{delete_run_history_objects, sleep_until_stopped};
use crate::effects::state_reads::{RunLocation, decode_row, read_run_record, scan_all};
use crate::effects::{EffectContext, EffectError};
use crate::flow_coordination::{CommitUrgency, FlowAddress};

/// Deletes the run's hard-delete objects that wait for deletion, then proposes
/// `HardDeleteObjectsDeleted`; repeats until committed state has none left.
pub(crate) async fn delete_pending_hard_delete_objects(
    context: &EffectContext,
    flow: &FlowAddress,
    run_id: RunId,
) -> Result<(), EffectError> {
    let run = context.run_location(flow, run_id);
    let owner = HardDeleteObjectOwner {
        tenant_id: flow.tenant_id,
        flow_hash: run.flow_hash,
        run_id,
    };
    loop {
        let object_ids =
            committed_hard_delete_object_ids(context, flow, run, &[HARD_DELETE_PENDING_OBJECT])?;
        if object_ids.is_empty() {
            return Ok(());
        }
        delete_hard_delete_objects(context, owner, &object_ids).await?;
        let mut sorted_object_ids = object_ids;
        object_ids_sorted_and_unique(&mut sorted_object_ids);
        let command = FlowCommand {
            flow_id: flow.flow_id.clone(),
            run_id: run_id.as_bytes().to_vec(),
            tenant_id: flow.tenant_id.0,
            command: Some(flow_command::Command::HardDeleteObjectsDeleted(
                HardDeleteObjectsDeletedCommand {
                    object_ids: sorted_object_ids,
                },
            )),
            ..FlowCommand::default()
        };
        let reply = context
            .propose_until_committed(flow, command, CommitUrgency::Prompt)
            .await
            .ok_or(EffectError::Stopped)?;
        if let Some(command_reply::Reply::Error(error)) = reply.reply {
            tracing::warn!(%run_id, message = %error.message, "HardDeleteObjectsDeleted was rejected");
            return Ok(());
        }
    }
}

/// Deletes the run's history segment objects and its hard-delete object prefix, then proposes
/// `RunPurge`.
///
/// Apply requests it only after deciding a purge that never postpones, so deletion is safe.
pub(crate) async fn purge_run(
    context: &EffectContext,
    flow: &FlowAddress,
    run_id: RunId,
) -> Result<(), EffectError> {
    let run = context.run_location(flow, run_id);
    let is_purging = {
        let reader = context
            .flow_effect_operations
            .flow_state_reader(flow, false)?;
        read_run_record(reader.as_ref(), run)?.is_some_and(|record| record.is_purging)
    };
    if !is_purging {
        return Ok(());
    }
    delete_run_history_objects(context, flow, run).await?;
    let owner = HardDeleteObjectOwner {
        tenant_id: flow.tenant_id,
        flow_hash: run.flow_hash,
        run_id,
    };
    let prefix_listing = context.hard_delete_objects.prefix_listing();
    if !prefix_listing.listing_consistency.is_strong() {
        // An eventual listing can hide objects, so referenced ones go by key first.
        let object_ids = committed_hard_delete_object_ids(context, flow, run, &[])?;
        delete_hard_delete_objects(context, owner, &object_ids).await?;
    }
    delete_run_hard_delete_prefix(context, owner, prefix_listing).await?;
    let command = FlowCommand {
        flow_id: flow.flow_id.clone(),
        run_id: run_id.as_bytes().to_vec(),
        tenant_id: flow.tenant_id.0,
        command: Some(flow_command::Command::RunPurge(RunPurgeCommand {})),
        ..FlowCommand::default()
    };
    let reply = context
        .propose_until_committed(flow, command, CommitUrgency::Prompt)
        .await
        .ok_or(EffectError::Stopped)?;
    if let Some(command_reply::Reply::Error(error)) = reply.reply {
        tracing::warn!(%run_id, message = %error.message, "RunPurge was rejected");
    }
    Ok(())
}

/// Returns the object IDs of the run's committed hard-delete rows whose key suffix starts with
/// `row_suffix`.
fn committed_hard_delete_object_ids(
    context: &EffectContext,
    flow: &FlowAddress,
    run: RunLocation,
    row_suffix: &[u8],
) -> Result<Vec<String>, EffectError> {
    let reader = context
        .flow_effect_operations
        .flow_state_reader(flow, false)?;
    let mut prefix = run_table_prefix(run.flow_hash, run.run_id, RunTable::HardDeleteObject);
    prefix.extend_from_slice(row_suffix);
    let mut object_ids = Vec::new();
    for (key, value) in scan_all(reader.as_ref(), ColumnFamily::Flow, &prefix)? {
        let record: HardDeleteObjectRecord = decode_row("HardDeleteObject", &key, &value)?;
        if let Some(object) = record.object {
            object_ids.push(object.object_id);
        }
    }
    Ok(object_ids)
}

/// Deletes the objects of one run by ID, retrying failures with backoff.
async fn delete_hard_delete_objects(
    context: &EffectContext,
    owner: HardDeleteObjectOwner,
    object_ids: &[String],
) -> Result<(), EffectError> {
    if object_ids.is_empty() {
        return Ok(());
    }
    let mut backoff = context.retry_backoff();
    while let Err(error) = context
        .hard_delete_objects
        .delete_objects(owner, object_ids)
        .await
    {
        tracing::warn!(%error, run_id = %owner.run_id, "deleting hard-delete objects failed; retrying");
        if !backoff.wait(&context.shutdown).await {
            return Err(EffectError::Stopped);
        }
    }
    Ok(())
}

/// Deletes every object under the run's hard-delete prefix until listings show none.
///
/// With a strong listing, one empty listing ends it. With an eventual listing, two empty
/// listings `empty_listing_confirmation_delay` apart end it, and a listing that shows a key
/// already deleted waits with backoff, since it repeats an earlier listing.
async fn delete_run_hard_delete_prefix(
    context: &EffectContext,
    owner: HardDeleteObjectOwner,
    prefix_listing: HardDeletePrefixListing,
) -> Result<(), EffectError> {
    let is_strong = prefix_listing.listing_consistency.is_strong();
    let mut backoff = context.retry_backoff();
    let mut replay_backoff = context.retry_backoff();
    let mut deleted_keys = BTreeSet::new();
    let mut has_empty_listing = false;
    loop {
        let keys = match context
            .hard_delete_objects
            .delete_listed_run_objects(owner)
            .await
        {
            Ok(keys) => keys,
            Err(error) => {
                tracing::warn!(%error, run_id = %owner.run_id, "deleting the run's hard-delete prefix failed; retrying");
                if !backoff.wait(&context.shutdown).await {
                    return Err(EffectError::Stopped);
                }
                continue;
            }
        };
        if keys.is_empty() {
            if is_strong || has_empty_listing {
                return Ok(());
            }
            has_empty_listing = true;
            if !sleep_until_stopped(context, prefix_listing.empty_listing_confirmation_delay).await
            {
                return Err(EffectError::Stopped);
            }
            continue;
        }
        has_empty_listing = false;
        let is_replay = keys.iter().any(|key| deleted_keys.contains(key));
        deleted_keys.extend(keys);
        if is_replay && !replay_backoff.wait(&context.shutdown).await {
            return Err(EffectError::Stopped);
        }
    }
}

fn object_ids_sorted_and_unique(object_ids: &mut Vec<String>) {
    object_ids.sort();
    object_ids.dedup();
}
