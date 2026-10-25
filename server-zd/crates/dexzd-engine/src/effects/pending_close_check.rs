// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Records a pending close that waited for transactional RPCs once they end, as the Go Server
//! closes only after its in-flight updates finish.

use dexzd_keyspace::row_keys::prefix_range;
use dexzd_keyspace::{ColumnFamily, RunId, RunTable, run_table_prefix};
use dexzd_protocol::dexzd::v1::{FlowCommand, PendingCloseCheckCommand, flow_command};

use crate::effects::state_reads::read_run_record;
use crate::effects::{EffectContext, EffectError, EffectRuntimeError};
use crate::runtime::{CommitUrgency, FlowAddress};

/// Waits until the run's transactional RPCs end, then proposes a check that records the close.
///
/// Returns once the run no longer has a pending close without outbox entries, or
/// [`EffectError::Stopped`] when the node stops.
pub(crate) async fn finalize_pending_close_after_rpcs(
    context: &EffectContext,
    flow: &FlowAddress,
    run_id: RunId,
) -> Result<(), EffectError> {
    let mut backoff = context.retry_backoff();
    loop {
        // Biased for reproducible simulation; shutdown fires once, so the wait cannot starve.
        let drained = tokio::select! {
            biased;
            () = context.shutdown.cancelled() => return Err(EffectError::Stopped),
            drained = context.runtime.wait_for_transactional_rpcs(flow) => drained,
        };
        match drained {
            Ok(()) => {}
            Err(EffectRuntimeError::NodeStopping { .. }) => return Err(EffectError::Stopped),
            Err(error @ EffectRuntimeError::Unavailable { .. }) => {
                tracing::warn!(%error, %run_id, "waiting for transactional RPCs failed; retrying");
                if !backoff.wait(&context.shutdown).await {
                    return Err(EffectError::Stopped);
                }
                continue;
            }
        }
        match has_unblocked_pending_close(context, flow, run_id) {
            Ok(false) => return Ok(()),
            Ok(true) => {}
            Err(error) if error.is_retryable() => {
                tracing::warn!(%error, %run_id, "reading a pending close failed; retrying");
                if !backoff.wait(&context.shutdown).await {
                    return Err(EffectError::Stopped);
                }
                continue;
            }
            Err(error) => return Err(error),
        }
        let command = FlowCommand {
            flow_id: flow.flow_id.clone(),
            run_id: run_id.as_bytes().to_vec(),
            tenant_id: flow.tenant_id.0,
            command: Some(flow_command::Command::PendingCloseCheck(
                PendingCloseCheckCommand {},
            )),
            ..FlowCommand::default()
        };
        context
            .propose_until_committed(flow, command, CommitUrgency::Prompt)
            .await
            .ok_or(EffectError::Stopped)?;
    }
}

/// Whether the run's committed state still has a pending close with no outbox entry.
fn has_unblocked_pending_close(
    context: &EffectContext,
    flow: &FlowAddress,
    run_id: RunId,
) -> Result<bool, EffectError> {
    let run = context.run_location(flow, run_id);
    let reader = context.runtime.flow_state_reader(flow, false)?;
    let Some(record) = read_run_record(reader.as_ref(), run)? else {
        return Ok(false);
    };
    if record.pending_close.is_none() {
        return Ok(false);
    }
    let prefix = run_table_prefix(run.flow_hash, run.run_id, RunTable::AttributeSyncOutbox);
    let (start, end) = prefix_range(&prefix);
    Ok(reader.scan(ColumnFamily::Flow, &start, &end, 1)?.is_empty())
}
