// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};
use std::future::Future;
use std::ops::ControlFlow;
use std::sync::Arc;
use std::time::Duration;

use dexzd_clock::WallClock;
use dexzd_cluster_metadata::LeaseStatus;
use dexzd_keyspace::{PartitionHasher, PartitionId};
use dexzd_metrics::AttributeSyncFailureKind;
use dexzd_protocol::dexzd::v1::{
    AttributeSyncAckCommand, FlowCommand, command_reply, flow_command,
};
use tokio::sync::watch;
use tokio::task::JoinHandle;
use tokio::time::Instant;
use tokio_util::sync::CancellationToken;

use super::pending_entries::{
    PendingEntry, PendingRunEntries, PendingStoreWork, read_pending_store_work,
};
use super::{
    AttributeSyncConfig, AttributeSyncDependencies, AttributeSyncFence, AttributeSyncItem,
    AttributeSyncRetryPolicy, AttributeSyncWaker, AttributeSyncWriteError, AttributeSyncWriter,
};
use crate::commit::NodeStopReason;
use crate::effects::{EffectRuntime, EffectRuntimeError, RunLocation};
use crate::runtime::{CommitUrgency, FlowAddress};

/// Writes pending Attribute Sync entries of every partition this node owns, one store at a time.
pub struct AttributeSyncProjector {
    config: AttributeSyncConfig,
    writer: Arc<dyn AttributeSyncWriter>,
    runtime: Arc<dyn EffectRuntime>,
    partition_hasher: PartitionHasher,
    clock: Arc<dyn WallClock>,
    metrics: Arc<dexzd_metrics::NodeMetrics>,
    fence: AttributeSyncFence,
    lease_status: watch::Receiver<LeaseStatus>,
    waker: AttributeSyncWaker,
    stop: CancellationToken,
    store_backoffs: BTreeMap<String, StoreBackoff>,
    /// Consecutive retryable failures of one run's entries, for `maximumAttempts`.
    failed_attempts: BTreeMap<(String, RunLocation), u32>,
    /// Stores with pending work per partition, left by a failure; they run once a backoff ends.
    deferred_work: BTreeMap<PartitionId, BTreeSet<String>>,
    backlog: BTreeMap<(PartitionId, String), BacklogSnapshot>,
}

/// The running projector; stop it before the node lease is released.
pub struct AttributeSyncProjectorHandle {
    stop: CancellationToken,
    task: JoinHandle<()>,
}

struct StoreBackoff {
    retry_at: Instant,
    next_delay: Duration,
}

#[derive(Clone, Copy)]
struct BacklogSnapshot {
    entry_count: usize,
    oldest_retry_window_started_at_micros: Option<i64>,
}

/// What processing one run decided.
struct RunOutcome {
    ack: Option<AttributeSyncAckCommand>,
    next: RunNext,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum RunNext {
    Continue,
    /// A retryable failure; later runs of this store wait for the store backoff.
    RetryStore,
    Stop,
}

enum WriteResult {
    Committed,
    Failed(AttributeSyncWriteError),
    Stopped,
}

struct PendingAck {
    flow: FlowAddress,
    run: RunLocation,
    ack: AttributeSyncAckCommand,
}

impl AttributeSyncProjector {
    /// Starts projecting in the background: a scan of every partition, then wake-ups.
    ///
    /// It stops at `node_stopped`, lease loss, a fenced write, or shutdown.
    pub fn start(
        config: &AttributeSyncConfig,
        dependencies: AttributeSyncDependencies,
        node_stopped: impl Future<Output = NodeStopReason> + Send + 'static,
    ) -> AttributeSyncProjectorHandle {
        let stop = CancellationToken::new();
        let projector = Self {
            config: config.clone(),
            writer: dependencies.writer,
            runtime: dependencies.runtime,
            partition_hasher: dependencies.partition_hasher,
            clock: dependencies.clock,
            metrics: dependencies.metrics,
            fence: dependencies.fence,
            lease_status: dependencies.lease_status,
            waker: dependencies.waker,
            stop: stop.clone(),
            store_backoffs: BTreeMap::new(),
            failed_attempts: BTreeMap::new(),
            deferred_work: BTreeMap::new(),
            backlog: BTreeMap::new(),
        };
        let task = tokio::spawn(async move {
            tokio::select! {
                () = projector.run() => {}
                reason = node_stopped => {
                    tracing::info!(%reason, "the Attribute Sync projector stopped with the node");
                }
            }
        });
        AttributeSyncProjectorHandle { stop, task }
    }

    async fn run(mut self) {
        let mut work: BTreeMap<PartitionId, StoreSelection> =
            (0..self.partition_hasher.partition_count())
                .map(|partition| {
                    let partition = PartitionId(
                        u16::try_from(partition).expect("the manifest limits partitions to u16"),
                    );
                    (partition, StoreSelection::Every)
                })
                .collect();
        loop {
            for (partition, stores) in std::mem::take(&mut work) {
                if self.project_partition(partition, stores).await.is_break() {
                    return;
                }
            }
            self.publish_backlog_metrics();
            work = self.next_work();
            if !work.is_empty() {
                continue;
            }
            let retry_at = self.deferred_retry_at();
            let mut lease_status = self.lease_status.clone();
            // Biased for reproducible simulation; stop and lease loss fire once.
            tokio::select! {
                biased;
                () = self.stop.cancelled() => return,
                () = wait_for_lease_loss(&mut lease_status) => {
                    tracing::warn!("the Attribute Sync projector stopped after the node lease was lost");
                    return;
                }
                () = self.waker.woken() => {}
                () = sleep_until_if_set(retry_at) => {}
            }
            work = self.next_work();
        }
    }

    /// Returns every store of woken partitions plus deferred stores whose backoff ended.
    fn next_work(&mut self) -> BTreeMap<PartitionId, StoreSelection> {
        let mut work: BTreeMap<PartitionId, StoreSelection> = self
            .waker
            .take_woken_partitions()
            .into_iter()
            .map(|partition| (partition, StoreSelection::Every))
            .collect();
        if self
            .deferred_retry_at()
            .is_some_and(|retry_at| retry_at <= Instant::now())
        {
            for (partition, stores) in std::mem::take(&mut self.deferred_work) {
                work.entry(partition)
                    .or_insert(StoreSelection::Only(stores));
            }
        }
        work
    }

    fn defer(&mut self, partition: PartitionId, store_name: &str) {
        self.deferred_work
            .entry(partition)
            .or_default()
            .insert(store_name.to_owned());
    }

    /// Returns when deferred stores run again; elapsed backoffs are ignored so the wait never spins.
    fn deferred_retry_at(&self) -> Option<Instant> {
        if self.deferred_work.is_empty() {
            return None;
        }
        let now = Instant::now();
        Some(
            self.store_backoffs
                .values()
                .map(|backoff| backoff.retry_at)
                .filter(|retry_at| *retry_at > now)
                .min()
                .unwrap_or(now),
        )
    }

    async fn project_partition(
        &mut self,
        partition: PartitionId,
        stores: StoreSelection,
    ) -> ControlFlow<()> {
        for store_name in self.config.store_names.clone() {
            if let StoreSelection::Only(selected) = &stores
                && !selected.contains(&store_name)
            {
                continue;
            }
            let work = match self.read_pending_store_work(partition, &store_name).await {
                Ok(work) => work,
                Err(detail) => {
                    tracing::warn!(
                        partition = partition.0,
                        store_name,
                        detail,
                        "reading pending Attribute Sync entries failed; retrying"
                    );
                    self.fail_store(&store_name);
                    self.defer(partition, &store_name);
                    continue;
                }
            };
            self.backlog.insert(
                (partition, store_name.clone()),
                BacklogSnapshot {
                    entry_count: work.entry_count,
                    oldest_retry_window_started_at_micros: work
                        .oldest_retry_window_started_at_micros,
                },
            );
            self.publish_backlog_metrics();
            if work.entry_count == 0 {
                continue;
            }
            if self
                .store_backoffs
                .get(&store_name)
                .is_some_and(|backoff| Instant::now() < backoff.retry_at)
            {
                self.defer(partition, &store_name);
                continue;
            }
            match self.project_store_work(&store_name, work).await {
                StoreWorkOutcome::Stop => return ControlFlow::Break(()),
                StoreWorkOutcome::Done {
                    has_proposed_acks,
                    needs_retry,
                } => {
                    if has_proposed_acks {
                        // Rescanning confirms the acks and refreshes the backlog metrics.
                        self.waker.wake_partition(partition);
                    }
                    if needs_retry {
                        self.defer(partition, &store_name);
                    }
                }
            }
        }
        ControlFlow::Continue(())
    }

    async fn read_pending_store_work(
        &self,
        partition: PartitionId,
        store_name: &str,
    ) -> Result<PendingStoreWork, String> {
        let runtime = Arc::clone(&self.runtime);
        let store_name = store_name.to_owned();
        tokio::task::spawn_blocking(move || {
            let reader = runtime
                .committed_state_reader()
                .map_err(|error| error.to_string())?;
            read_pending_store_work(reader.as_ref(), partition, &store_name)
                .map_err(|error| error.to_string())
        })
        .await
        .map_err(|error| error.to_string())
        .and_then(|read| read)
    }

    /// Writes runs of each Flow ID in run order, then proposes the acks together.
    async fn project_store_work(
        &mut self,
        store_name: &str,
        work: PendingStoreWork,
    ) -> StoreWorkOutcome {
        let mut acks = Vec::new();
        let mut needs_retry = false;
        'flows: for flow_runs in work.flows {
            for run_entries in flow_runs {
                let flow = run_entries.flow.clone();
                let run = run_entries.run;
                let outcome = self.process_run(store_name, run_entries).await;
                if let Some(ack) = outcome.ack {
                    acks.push(PendingAck {
                        flow: flow.clone(),
                        run,
                        ack,
                    });
                }
                match outcome.next {
                    RunNext::Continue => {}
                    RunNext::RetryStore => {
                        needs_retry = true;
                        break 'flows;
                    }
                    RunNext::Stop => return StoreWorkOutcome::Stop,
                }
            }
        }
        let has_proposed_acks = !acks.is_empty();
        match self.propose_acks(store_name, acks).await {
            AckOutcome::Committed => {}
            AckOutcome::Rejected => {
                // A rejected ack leaves its entries pending; the backoff keeps them from looping hot.
                self.fail_store(store_name);
                needs_retry = true;
            }
            AckOutcome::Stopped => return StoreWorkOutcome::Stop,
        }
        StoreWorkOutcome::Done {
            has_proposed_acks,
            needs_retry,
        }
    }

    async fn process_run(
        &mut self,
        store_name: &str,
        run_entries: PendingRunEntries,
    ) -> RunOutcome {
        let PendingRunEntries {
            run,
            flow,
            entries,
            items,
            unreadable_value_detail,
        } = run_entries;
        let now_micros = self.clock.now_unix_micros();
        let total_duration_micros =
            i64::try_from(self.config.retry_policy.total_duration.as_micros()).unwrap_or(i64::MAX);
        let expired: Vec<&PendingEntry> = entries
            .iter()
            .filter(|entry| {
                now_micros.saturating_sub(entry.retry_window_started_at_micros)
                    > total_duration_micros
            })
            .collect();
        if !expired.is_empty() {
            self.failed_attempts.remove(&(store_name.to_owned(), run));
            tracing::error!(
                store_name,
                flow_id = %flow.flow_id,
                run_id = %run.run_id,
                dropped_entry_count = expired.len(),
                "Attribute Store sync retries exceeded syncRetryPolicy.totalDuration; dropping the entries"
            );
            return RunOutcome {
                ack: Some(ack_command(store_name, &expired, true)),
                next: RunNext::Continue,
            };
        }
        let attempt_key = (store_name.to_owned(), run);
        let maximum_attempts = self.config.retry_policy.maximum_attempts;
        if maximum_attempts > 0
            && self
                .failed_attempts
                .get(&attempt_key)
                .is_some_and(|attempts| *attempts >= maximum_attempts)
        {
            self.failed_attempts.remove(&attempt_key);
            tracing::error!(
                store_name,
                flow_id = %flow.flow_id,
                run_id = %run.run_id,
                maximum_attempts,
                "Attribute Store sync reached syncRetryPolicy.maximumAttempts; dropping the entries"
            );
            return drop_every_entry(store_name, &entries);
        }
        if let Some(detail) = unreadable_value_detail {
            tracing::error!(
                store_name,
                flow_id = %flow.flow_id,
                run_id = %run.run_id,
                detail,
                "an Attribute value to sync cannot be loaded; dropping the entries"
            );
            return drop_every_entry(store_name, &entries);
        }
        let batch_size = self.config.batch_size.max(1);
        let mut written_keys = BTreeSet::new();
        for batch in items.chunks(batch_size) {
            match self.write_batch(store_name, &flow.flow_id, batch).await {
                WriteResult::Committed => {
                    written_keys.extend(batch.iter().map(|item| item.key.as_str()));
                }
                WriteResult::Stopped => {
                    return RunOutcome {
                        ack: None,
                        next: RunNext::Stop,
                    };
                }
                WriteResult::Failed(AttributeSyncWriteError::Fenced { detail }) => {
                    self.metrics.attribute_sync_fenced_writes.inc();
                    tracing::error!(
                        store_name,
                        detail,
                        "a newer owner fenced Attribute Store writes; stopping the Attribute Sync projector"
                    );
                    return RunOutcome {
                        ack: None,
                        next: RunNext::Stop,
                    };
                }
                WriteResult::Failed(AttributeSyncWriteError::Rejected { detail }) => {
                    self.metrics
                        .attribute_sync_write_failures
                        .record(AttributeSyncFailureKind::NonRetryable);
                    self.failed_attempts.remove(&attempt_key);
                    tracing::error!(
                        store_name,
                        flow_id = %flow.flow_id,
                        run_id = %run.run_id,
                        detail,
                        "the Attribute Store rejected a write; dropping the entries"
                    );
                    return drop_every_entry(store_name, &entries);
                }
                WriteResult::Failed(AttributeSyncWriteError::Retryable { detail }) => {
                    self.metrics
                        .attribute_sync_write_failures
                        .record(AttributeSyncFailureKind::Retryable);
                    *self.failed_attempts.entry(attempt_key).or_insert(0) += 1;
                    self.fail_store(store_name);
                    tracing::warn!(
                        store_name,
                        flow_id = %flow.flow_id,
                        run_id = %run.run_id,
                        detail,
                        "an Attribute Store write failed; retrying with backoff"
                    );
                    let written_entries: Vec<&PendingEntry> = entries
                        .iter()
                        .filter(|entry| written_keys.contains(entry.attribute_key.as_str()))
                        .collect();
                    return RunOutcome {
                        ack: (!written_entries.is_empty())
                            .then(|| ack_command(store_name, &written_entries, false)),
                        next: RunNext::RetryStore,
                    };
                }
            }
        }
        self.failed_attempts.remove(&attempt_key);
        self.store_backoffs.remove(store_name);
        let every_entry: Vec<&PendingEntry> = entries.iter().collect();
        let mut ack = ack_command(store_name, &every_entry, false);
        // Unwritten entries are superseded or hold hard-delete values, so the ack covers all.
        ack.attribute_keys.clear();
        RunOutcome {
            ack: Some(ack),
            next: RunNext::Continue,
        }
    }

    /// Writes one transaction once the lease has room for it, abandoning it if the lease ends.
    async fn write_batch(
        &self,
        store_name: &str,
        flow_id: &str,
        items: &[AttributeSyncItem],
    ) -> WriteResult {
        let Some(valid_until) = self.wait_for_lease_budget().await else {
            return WriteResult::Stopped;
        };
        let abandon_at = valid_until
            .checked_sub(self.config.lease_margin)
            .unwrap_or(valid_until);
        let mut lease_status = self.lease_status.clone();
        let write = self
            .writer
            .write_flow_attributes(store_name, flow_id, items, &self.fence);
        // Biased for reproducible simulation; dropping the write rolls its transaction back.
        tokio::select! {
            biased;
            () = self.stop.cancelled() => WriteResult::Stopped,
            () = wait_for_lease_loss(&mut lease_status) => {
                tracing::warn!(store_name, "the node lease ended during an Attribute Store write; abandoning it");
                WriteResult::Stopped
            }
            () = tokio::time::sleep_until(abandon_at) => {
                WriteResult::Failed(AttributeSyncWriteError::Retryable {
                    detail: "the write did not finish before the lease margin".to_owned(),
                })
            }
            result = write => match result {
                Ok(()) => WriteResult::Committed,
                Err(error) => WriteResult::Failed(error),
            },
        }
    }

    /// Returns the lease deadline once more than the transaction timeout plus margin remains.
    ///
    /// Returns `None` when the projector stops or the lease ends first.
    async fn wait_for_lease_budget(&self) -> Option<Instant> {
        let required = self
            .config
            .transaction_timeout
            .saturating_add(self.config.lease_margin);
        let mut lease_status = self.lease_status.clone();
        loop {
            let valid_until = match &*lease_status.borrow_and_update() {
                LeaseStatus::Lost(_) => return None,
                LeaseStatus::ValidUntil(valid_until) => *valid_until,
            };
            if valid_until.saturating_duration_since(Instant::now()) > required {
                return Some(valid_until);
            }
            tokio::select! {
                biased;
                () = self.stop.cancelled() => return None,
                changed = lease_status.changed() => {
                    if changed.is_err() {
                        return None;
                    }
                }
                () = tokio::time::sleep_until(valid_until) => return None,
            }
        }
    }

    /// Proposes every ack concurrently, so one group commit batch carries them.
    async fn propose_acks(&self, store_name: &str, acks: Vec<PendingAck>) -> AckOutcome {
        if acks.is_empty() {
            return AckOutcome::Committed;
        }
        let proposals = acks.into_iter().map(|pending| async move {
            let command = FlowCommand {
                flow_id: pending.flow.flow_id.clone(),
                run_id: pending.run.run_id.as_bytes().to_vec(),
                tenant_id: pending.flow.tenant_id.0,
                command: Some(flow_command::Command::AttributeSyncAck(pending.ack)),
                ..FlowCommand::default()
            };
            (
                pending.flow.clone(),
                self.propose_until_committed(&pending.flow, command).await,
            )
        });
        let mut outcome = AckOutcome::Committed;
        for (flow, reply) in futures::future::join_all(proposals).await {
            match reply {
                None => return AckOutcome::Stopped,
                Some(command_reply::Reply::Error(error)) => {
                    tracing::warn!(
                        store_name,
                        flow_id = %flow.flow_id,
                        message = %error.message,
                        "an Attribute Sync ack was rejected"
                    );
                    outcome = AckOutcome::Rejected;
                }
                Some(_) => {}
            }
        }
        outcome
    }

    /// Retries while the runtime is unavailable; returns `None` when the projector or node stops.
    async fn propose_until_committed(
        &self,
        flow: &FlowAddress,
        command: FlowCommand,
    ) -> Option<command_reply::Reply> {
        let mut delay = self.config.retry_policy.initial_interval;
        loop {
            let proposal =
                self.runtime
                    .propose_committed(flow, command.clone(), CommitUrgency::Batched);
            let outcome = tokio::select! {
                biased;
                () = self.stop.cancelled() => return None,
                outcome = proposal => outcome,
            };
            match outcome {
                Ok(reply) => {
                    return Some(reply.reply.unwrap_or(command_reply::Reply::Ok(
                        dexzd_protocol::dexzd::v1::EmptyReply::default(),
                    )));
                }
                Err(EffectRuntimeError::NodeStopping { .. }) => return None,
                Err(error @ EffectRuntimeError::Unavailable { .. }) => {
                    tracing::debug!(%error, flow_id = %flow.flow_id, "retrying an Attribute Sync ack");
                    tokio::select! {
                        biased;
                        () = self.stop.cancelled() => return None,
                        () = tokio::time::sleep(delay) => {}
                    }
                    delay = next_retry_delay(&self.config.retry_policy, delay);
                }
            }
        }
    }

    fn fail_store(&mut self, store_name: &str) {
        let initial_interval = self.config.retry_policy.initial_interval;
        let backoff = self
            .store_backoffs
            .entry(store_name.to_owned())
            .or_insert(StoreBackoff {
                retry_at: Instant::now(),
                next_delay: initial_interval,
            });
        let delay = backoff.next_delay;
        backoff.retry_at = Instant::now() + delay;
        backoff.next_delay = next_retry_delay(&self.config.retry_policy, delay);
    }

    fn publish_backlog_metrics(&mut self) {
        self.backlog.retain(|_, snapshot| snapshot.entry_count > 0);
        let entry_count: usize = self
            .backlog
            .values()
            .map(|snapshot| snapshot.entry_count)
            .sum();
        self.metrics
            .attribute_sync_backlog_entries
            .set(i64::try_from(entry_count).unwrap_or(i64::MAX));
        let oldest_age_seconds = self
            .backlog
            .values()
            .filter_map(|snapshot| snapshot.oldest_retry_window_started_at_micros)
            .min()
            .map_or(0.0, |oldest_micros| {
                let age_micros = self.clock.now_unix_micros().saturating_sub(oldest_micros);
                Duration::from_micros(u64::try_from(age_micros).unwrap_or(0)).as_secs_f64()
            });
        self.metrics
            .attribute_sync_oldest_pending_age
            .set(oldest_age_seconds);
    }
}

/// Which stores of a partition a cycle reads.
enum StoreSelection {
    /// Every configured store, after a wake-up or at start.
    Every,
    /// Only stores whose deferred work is due.
    Only(BTreeSet<String>),
}

enum StoreWorkOutcome {
    Done {
        has_proposed_acks: bool,
        needs_retry: bool,
    },
    Stop,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum AckOutcome {
    Committed,
    Rejected,
    Stopped,
}

impl AttributeSyncProjectorHandle {
    #[cfg(test)]
    pub(crate) fn has_stopped(&self) -> bool {
        self.task.is_finished()
    }

    /// Stops the projector, aborting it and its in-flight write when it does not stop within `budget`.
    ///
    /// Returns false when the projector had to be aborted.
    pub async fn shutdown(mut self, budget: Duration) -> bool {
        self.stop.cancel();
        match tokio::time::timeout(budget, &mut self.task).await {
            Ok(Ok(())) => true,
            Ok(Err(error)) => {
                tracing::warn!(%error, "the Attribute Sync projector task failed");
                true
            }
            Err(_) => {
                self.task.abort();
                false
            }
        }
    }
}

/// Acknowledges `entries` up to their largest written seq.
fn ack_command(
    store_name: &str,
    entries: &[&PendingEntry],
    is_dropped: bool,
) -> AttributeSyncAckCommand {
    AttributeSyncAckCommand {
        store_name: store_name.to_owned(),
        acked_seq: entries
            .iter()
            .map(|entry| entry.written_seq)
            .max()
            .unwrap_or(0),
        attribute_keys: entries
            .iter()
            .map(|entry| entry.attribute_key.clone())
            .collect(),
        is_dropped,
    }
}

fn drop_every_entry(store_name: &str, entries: &[PendingEntry]) -> RunOutcome {
    let every_entry: Vec<&PendingEntry> = entries.iter().collect();
    let mut ack = ack_command(store_name, &every_entry, true);
    ack.attribute_keys.clear();
    RunOutcome {
        ack: Some(ack),
        next: RunNext::Continue,
    }
}

/// Returns when the lease is lost, its renewal task ends, or it expires without renewal.
async fn wait_for_lease_loss(lease_status: &mut watch::Receiver<LeaseStatus>) {
    loop {
        let valid_until = match &*lease_status.borrow_and_update() {
            LeaseStatus::Lost(_) => return,
            LeaseStatus::ValidUntil(valid_until) => *valid_until,
        };
        tokio::select! {
            biased;
            changed = lease_status.changed() => {
                if changed.is_err() {
                    return;
                }
            }
            () = tokio::time::sleep_until(valid_until) => return,
        }
    }
}

fn next_retry_delay(policy: &AttributeSyncRetryPolicy, delay: Duration) -> Duration {
    Duration::try_from_secs_f64(delay.as_secs_f64() * policy.backoff_coefficient)
        .unwrap_or(policy.maximum_interval)
        .min(policy.maximum_interval)
}

async fn sleep_until_if_set(deadline: Option<Instant>) {
    match deadline {
        Some(deadline) => tokio::time::sleep_until(deadline).await,
        None => std::future::pending().await,
    }
}
