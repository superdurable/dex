// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Deletes a purging run's history segment objects before its RunPurge (plan section 5.12).

use std::collections::BTreeSet;
use std::time::Duration;

use dexzd_history_segment::{history_run_object_key_prefix, history_segment_object_key};
use dexzd_keyspace::{ColumnFamily, RunTable, run_table_prefix};
use dexzd_protocol::dexzd::v1::HistorySegmentRecord;

use super::until_stopped;
use crate::effects::state_reads::{RunLocation, decode_row, read_run_record, scan_all};
use crate::effects::{EffectContext, EffectError, RetryBackoff};
use crate::flow_coordination::FlowAddress;

/// Lets history writes still in flight end first, from this node's abandoned attempts or a
/// previous owner; object requests time out after 30 seconds by default.
const HISTORY_WRITE_SETTLE: Duration = Duration::from_secs(60);
/// How long a purge waits after object storage rejects a call permanently.
const PERMANENT_FAILURE_RECHECK: Duration = Duration::from_secs(60);
/// Most keys one listing page or batch delete takes.
const DELETE_BATCH_KEYS: usize = 1000;

/// Deletes every history segment object of a purging run that kept segment state, then sweeps
/// the run's `history/` prefix for objects outside its catalog, such as those of a timeline that
/// an older restored snapshot replaced.
///
/// Retention postpones while a segment is SEALED, so this node writes no object of the run any
/// more. Deletion starts only after [`HISTORY_WRITE_SETTLE`], so the client side of every write
/// that a previous owner or an abandoned attempt started has ended. Failed calls retry with
/// backoff, and permanent rejections every minute.
pub(crate) async fn delete_run_history_objects(
    context: &EffectContext,
    flow: &FlowAddress,
    run: RunLocation,
) -> Result<(), EffectError> {
    let Some(catalog_keys) = history_catalog_keys(context, flow, run)? else {
        return Ok(());
    };
    // A write the store already accepted can still land later; the orphan sweep removes it.
    if !sleep_until_stopped(context, HISTORY_WRITE_SETTLE).await {
        return Err(EffectError::Stopped);
    }
    for batch in catalog_keys.chunks(DELETE_BATCH_KEYS) {
        delete_keys(context, run, batch).await?;
    }
    sweep_run_prefix(context, flow, run).await
}

/// Returns the object key of every history segment row of the run, in any state, or `None`
/// when the run never kept segment state, so it never uploaded history.
fn history_catalog_keys(
    context: &EffectContext,
    flow: &FlowAddress,
    run: RunLocation,
) -> Result<Option<Vec<String>>, EffectError> {
    let reader = context
        .flow_effect_operations
        .flow_state_reader(flow, false)?;
    let has_segment_state = read_run_record(reader.as_ref(), run)?
        .is_some_and(|record| record.history_open_segment_first_seq != 0);
    let prefix = run_table_prefix(run.flow_hash, run.run_id, RunTable::HistorySegment);
    let keys = scan_all(reader.as_ref(), ColumnFamily::Flow, &prefix)?
        .into_iter()
        .map(|(key, value)| {
            let segment: HistorySegmentRecord = decode_row("HistorySegment", &key, &value)?;
            Ok(history_segment_object_key(
                flow.tenant_id,
                run.flow_hash,
                run.run_id,
                segment.first_seq,
                segment.last_seq,
            ))
        })
        .collect::<Result<Vec<_>, EffectError>>()?;
    Ok((has_segment_state || !keys.is_empty()).then_some(keys))
}

/// Lists the run's history prefix and deletes what it finds until a listing is empty.
///
/// A listing that shows a key this sweep already deleted repeats an earlier listing, so the
/// sweep waits with backoff before it lists again.
async fn sweep_run_prefix(
    context: &EffectContext,
    flow: &FlowAddress,
    run: RunLocation,
) -> Result<(), EffectError> {
    let prefix = history_run_object_key_prefix(flow.tenant_id, run.flow_hash, run.run_id);
    let max_keys = u32::try_from(DELETE_BATCH_KEYS).expect("the batch size fits in u32");
    let mut backoff = context.retry_backoff();
    let mut replay_backoff = context.retry_backoff();
    let mut deleted_keys = BTreeSet::new();
    loop {
        let listed = until_stopped(
            context,
            context.history_object_store.list(&prefix, None, max_keys),
        )
        .await;
        let listing = match listed {
            Ok(listing) => listing,
            Err(EffectError::Stopped) => return Err(EffectError::Stopped),
            Err(error) => {
                wait_after_failure(context, &mut backoff, run, &error).await?;
                continue;
            }
        };
        if listing.objects.is_empty() {
            return Ok(());
        }
        let keys: Vec<String> = listing
            .objects
            .into_iter()
            .map(|object| object.key)
            .collect();
        if keys.iter().any(|key| deleted_keys.contains(key))
            && !replay_backoff.wait(&context.shutdown).await
        {
            return Err(EffectError::Stopped);
        }
        delete_keys(context, run, &keys).await?;
        deleted_keys.extend(keys);
    }
}

async fn delete_keys(
    context: &EffectContext,
    run: RunLocation,
    keys: &[String],
) -> Result<(), EffectError> {
    let mut backoff = context.retry_backoff();
    loop {
        match until_stopped(context, context.history_object_store.delete_many(keys)).await {
            Ok(()) => return Ok(()),
            Err(EffectError::Stopped) => return Err(EffectError::Stopped),
            Err(error) => wait_after_failure(context, &mut backoff, run, &error).await?,
        }
    }
}

/// Waits before retrying a failed call: with backoff when it may pass, otherwise for the slow
/// recheck of a permanent rejection.
///
/// Returns [`EffectError::Stopped`] when the node stops first.
pub(super) async fn wait_after_failure(
    context: &EffectContext,
    backoff: &mut RetryBackoff,
    run: RunLocation,
    error: &EffectError,
) -> Result<(), EffectError> {
    let has_waited = if error.is_retryable() {
        tracing::warn!(
            %error,
            run_id = %run.run_id,
            "deleting the run's history segment objects failed; retrying"
        );
        backoff.wait(&context.shutdown).await
    } else {
        tracing::error!(
            %error,
            run_id = %run.run_id,
            "object storage rejected deleting the run's history segment objects; retrying in a minute"
        );
        sleep_until_stopped(context, PERMANENT_FAILURE_RECHECK).await
    };
    if has_waited {
        Ok(())
    } else {
        Err(EffectError::Stopped)
    }
}

/// Sleeps for `delay`; returns false when the node stopped first.
pub(crate) async fn sleep_until_stopped(context: &EffectContext, delay: Duration) -> bool {
    // Biased for reproducible simulation; shutdown fires once.
    tokio::select! {
        biased;
        () = context.shutdown.cancelled() => false,
        () = tokio::time::sleep(delay) => true,
    }
}
