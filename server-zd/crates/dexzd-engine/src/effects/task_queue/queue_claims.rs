// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Claims (plan section 3.2): matching asks the partition owner to start a queued row on a
//! reserved stream. The owner checks the row in its speculative state, then answers Started,
//! stale, busy, lock busy, or retry later, always with the queue's next ref.

use std::sync::Arc;
use std::time::{Duration, Instant};

use dexzd_worker_dispatch::{ClaimResult, QueuedTaskRef};

use super::{is_ref_pending, ref_attempt_key, ref_flow, reply_to_claim};
use crate::effects::dispatch::{
    AttemptHandout, ClaimHolder, ClaimedRow, HandoutProgress, MethodClaim,
};
use crate::effects::{EffectContext, RouterMessage};

/// Takes matching's Claims until the node stops and handles each in its own task.
pub(crate) async fn run_claim_pump(context: Arc<EffectContext>) {
    loop {
        // Biased for reproducible simulation; shutdown fires once.
        let claim = tokio::select! {
            biased;
            () = context.shutdown.cancelled() => return,
            claim = context.dispatcher.next_claim() => claim,
        };
        let Some(claim) = claim else {
            return;
        };
        let task_context = Arc::clone(&context);
        context
            .tracker
            .spawn(async move { handle_claim(task_context, claim).await });
    }
}

/// Checks a claimed row and either starts it on the claimed stream or answers why not.
async fn handle_claim(context: Arc<EffectContext>, claim: MethodClaim) {
    let MethodClaim {
        claim_id,
        task,
        stream,
    } = claim;
    // Without a valid lease the node hands out nothing; it holds the row and retries.
    let check = if stream.may_hand_out_tasks() {
        is_ref_pending(&context, &task).map_err(|error| error.to_string())
    } else {
        Err("the node lease is not valid".to_owned())
    };
    if matches!(check, Ok(false)) {
        drop(stream);
        reply_to_claim(&context, &task, claim_id, ClaimResult::Stale);
        return;
    }
    let key = ref_attempt_key(&task);
    let holder = context.attempt_entries().claim(&key);
    let generation = match holder {
        ClaimHolder::Claimed { generation } => generation,
        ClaimHolder::Parked => {
            drop(stream);
            reply_to_claim(&context, &task, claim_id, ClaimResult::LockBusy);
            return;
        }
        ClaimHolder::Busy => {
            drop(stream);
            reply_to_claim(&context, &task, claim_id, ClaimResult::Busy);
            return;
        }
    };
    let flow = ref_flow(&task);
    let handout = AttemptHandout {
        context: Arc::clone(&context),
        flow: flow.clone(),
        key: key.clone(),
        generation,
        progress: HandoutProgress {
            scheduled_at: pending_instant(&context, &task),
        },
    };
    let end = match check {
        Ok(_) => {
            handout
                .run_claimed(ClaimedRow {
                    claim_id,
                    task,
                    stream,
                })
                .await
        }
        Err(detail) => {
            tracing::debug!(detail, "a claimed row could not be checked; retrying later");
            drop(stream);
            handout.retry_claim_later(claim_id, task).await
        }
    };
    context.report(RouterMessage::HandoutFinished {
        flow,
        key,
        generation,
        end,
    });
}

/// Returns when the row became pending on this process's monotonic clock, so the
/// schedule-to-start latency includes its time in the queue (plan section 3.10).
fn pending_instant(context: &EffectContext, task: &QueuedTaskRef) -> Option<Instant> {
    let waited_micros = context
        .clock
        .now_unix_micros()
        .saturating_sub(task.position.pending_since_micros);
    let waited = Duration::from_micros(u64::try_from(waited_micros).unwrap_or(0));
    Instant::now().checked_sub(waited)
}
