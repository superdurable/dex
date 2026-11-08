// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Proposes a run's HistoryLocalCleanup command when its cleanup comes due (plan section 5.5).

use std::time::Duration;

use dexzd_keyspace::RunId;
use dexzd_protocol::dexzd::v1::{
    FlowCommand, HistoryLocalCleanupCommand, command_reply, flow_command,
};

use super::run_history_objects::sleep_until_stopped;
use crate::effects::state_reads::{RunLocation, read_run_record};
use crate::effects::{EffectContext, EffectError};
use crate::flow_coordination::{CommitUrgency, FlowAddress};

/// Longest wait before the task rereads the run's due time, which later commands may move.
const DUE_TIME_RECHECK_INTERVAL: Duration = Duration::from_secs(60);
/// gRPC NOT_FOUND, which apply returns for a run that is gone or purging.
const GRPC_NOT_FOUND: i32 = 5;

/// Proposes the run's HistoryLocalCleanup command each time it comes due by the owner's clock.
///
/// Returns `Ok` once no cleanup is pending or the run is gone or purging. Returns an error when
/// a committed row cannot be decoded or apply rejects the cleanup for another reason.
pub(crate) async fn clean_up_local_history(
    context: &EffectContext,
    flow: &FlowAddress,
    run_id: RunId,
) -> Result<(), EffectError> {
    let run = context.run_location(flow, run_id);
    let mut backoff = context.retry_backoff();
    let mut proposed_due_micros = None;
    loop {
        let due_micros = match read_cleanup_due_micros(context, flow, run) {
            Ok(Some(due_micros)) => due_micros,
            Ok(None) => return Ok(()),
            Err(error) if error.is_retryable() => {
                tracing::warn!(%error, %run_id, "reading the run's local history cleanup failed");
                if !backoff.wait(&context.shutdown).await {
                    return Err(EffectError::Stopped);
                }
                continue;
            }
            Err(error) => return Err(error),
        };
        let now_micros = context.clock.now_unix_micros();
        if due_micros > now_micros {
            let wait_micros = u64::try_from(due_micros - now_micros).unwrap_or(0);
            let wait = Duration::from_micros(wait_micros).min(DUE_TIME_RECHECK_INTERVAL);
            if !sleep_until_stopped(context, wait).await {
                return Err(EffectError::Stopped);
            }
            continue;
        }
        // An unchanged due time after a committed cleanup means no progress, so back off.
        if proposed_due_micros == Some(due_micros) {
            tracing::warn!(
                %run_id,
                due_micros,
                "a committed local history cleanup left its due time unchanged"
            );
            if !backoff.wait(&context.shutdown).await {
                return Err(EffectError::Stopped);
            }
        } else {
            backoff = context.retry_backoff();
        }
        if !propose_local_cleanup(context, flow, run_id).await? {
            return Ok(());
        }
        proposed_due_micros = Some(due_micros);
    }
}

/// Returns the committed due time of the run's local history cleanup, or `None` when the run has
/// none pending or is gone or purging.
fn read_cleanup_due_micros(
    context: &EffectContext,
    flow: &FlowAddress,
    run: RunLocation,
) -> Result<Option<i64>, EffectError> {
    let reader = context
        .flow_effect_operations
        .flow_state_reader(flow, false)?;
    Ok(read_run_record(reader.as_ref(), run)?
        .filter(|record| !record.is_purging)
        .and_then(|record| record.history_local_cleanup_due_micros))
}

/// Proposes `HistoryLocalCleanup` for the run and waits until it commits.
///
/// Returns false when the run is gone or purging, which ends the run's cleanup.
async fn propose_local_cleanup(
    context: &EffectContext,
    flow: &FlowAddress,
    run_id: RunId,
) -> Result<bool, EffectError> {
    let command = FlowCommand {
        flow_id: flow.flow_id.clone(),
        run_id: run_id.as_bytes().to_vec(),
        tenant_id: flow.tenant_id.0,
        command: Some(flow_command::Command::HistoryLocalCleanup(
            HistoryLocalCleanupCommand {},
        )),
        ..FlowCommand::default()
    };
    let reply = context
        .propose_until_committed(flow, command, CommitUrgency::Batched)
        .await
        .ok_or(EffectError::Stopped)?;
    match reply.reply {
        Some(command_reply::Reply::Error(error)) if error.grpc_code == GRPC_NOT_FOUND => {
            tracing::debug!(%run_id, "stopping local history cleanup of a run that is gone");
            Ok(false)
        }
        Some(command_reply::Reply::Error(error)) => Err(EffectError::InvariantViolation {
            detail: format!(
                "HistoryLocalCleanup of run {run_id} was rejected: {}",
                error.message
            ),
        }),
        _ => Ok(true),
    }
}
