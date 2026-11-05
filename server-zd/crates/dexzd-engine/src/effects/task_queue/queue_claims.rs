// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Claims (plan section 3.2): check a queued row, then start it or answer why not.

use std::sync::Arc;
use std::time::{Duration, Instant};

use dexzd_keyspace::PartitionId;
use dexzd_worker_dispatch::{ClaimResult, QueuedTaskRef, StepTaskSendFailure};

use super::{
    QueueReadView, is_ref_pending_in, read_off_task, ref_attempt_key, ref_flow, reply_to_claim,
};
use crate::effects::dispatch::{
    AttemptHandout, ClaimHolder, ClaimedRow, HandoutProgress, MethodClaim, MethodDispatchSession,
    MethodTask, ReservedMethodStream,
};
use crate::effects::{EffectContext, RouterMessage};

/// Takes matching's Claims until the node stops and hands each to the router.
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
        context.report(RouterMessage::ClaimDelivered { claim });
    }
}

/// Handles an admitted Claim in its own task, which reports its end to the router.
pub(crate) fn spawn_claim(context: &Arc<EffectContext>, claim: MethodClaim) {
    let task_context = Arc::clone(context);
    context.tracker.spawn(async move {
        handle_claim(Arc::clone(&task_context), claim).await;
        task_context.report(RouterMessage::ClaimFinished);
    });
}

/// Checks a claimed row and either starts it on the claimed stream or answers why not.
async fn handle_claim(context: Arc<EffectContext>, claim: MethodClaim) {
    let MethodClaim {
        claim_id,
        task,
        stream,
        partition_removals,
    } = claim;
    if !context
        .dispatcher
        .is_claim_tracked(task.partition, &task.queue, claim_id)
    {
        // Matching removed the partition; its next owner serves the row.
        drop(stream);
        return;
    }
    // Without a valid lease the node hands out nothing; it holds the row and retries.
    let check = if stream.may_hand_out_tasks() {
        let pending_task = task.clone();
        read_off_task(
            &context,
            &context.blocking_read_slots,
            QueueReadView::Speculative(ref_flow(&task)),
            move |_, reader| is_ref_pending_in(reader, &pending_task),
        )
        .await
        .map_err(|error| error.to_string())
    } else {
        Err("the node lease is not valid".to_owned())
    };
    // The partition may have been removed while the check waited.
    if !context
        .dispatcher
        .is_claim_tracked(task.partition, &task.queue, claim_id)
    {
        drop(stream);
        return;
    }
    let stream: Box<dyn ReservedMethodStream> = Box::new(PartitionFencedStream {
        stream,
        context: Arc::clone(&context),
        partition: task.partition,
        partition_removals,
    });
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

/// A claimed slot that sends nothing once matching removed the row's partition.
struct PartitionFencedStream {
    stream: Box<dyn ReservedMethodStream>,
    context: Arc<EffectContext>,
    partition: PartitionId,
    partition_removals: u64,
}

impl PartitionFencedStream {
    fn is_partition_kept(&self) -> bool {
        self.context
            .dispatcher
            .is_partition_unremoved_since(self.partition, self.partition_removals)
    }
}

impl ReservedMethodStream for PartitionFencedStream {
    fn app_worker_id(&self) -> &str {
        self.stream.app_worker_id()
    }

    fn may_hand_out_tasks(&self) -> bool {
        self.stream.may_hand_out_tasks() && self.is_partition_kept()
    }

    fn send_task(
        self: Box<Self>,
        task: MethodTask,
    ) -> Result<Box<dyn MethodDispatchSession>, StepTaskSendFailure> {
        if !self.is_partition_kept() {
            tracing::debug!(
                partition = self.partition.0,
                "matching removed the partition before the send; returning the started attempt"
            );
            // No frame went out, so returning spends no retry (plan D59); dropping self frees
            // the slot.
            return Err(StepTaskSendFailure::NotDelivered);
        }
        self.stream.send_task(task)
    }
}

/// Returns when the row became pending, on the monotonic clock.
fn pending_instant(context: &EffectContext, task: &QueuedTaskRef) -> Option<Instant> {
    let waited_micros = context
        .clock
        .now_unix_micros()
        .saturating_sub(task.position.pending_since_micros);
    let waited = Duration::from_micros(u64::try_from(waited_micros).unwrap_or(0));
    Instant::now().checked_sub(waited)
}
