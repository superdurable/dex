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
use dexzd_protocol::dexzd::v1::AttributeSyncAckCommand;
use tokio::sync::watch;
use tokio::task::{JoinHandle, JoinSet};
use tokio::time::Instant;
use tokio_util::sync::CancellationToken;

use super::ack_proposer::{AckBatch, AckBatchResult, AckProposer, PendingAck};
use super::pending_entries::{
    PendingEntry, PendingRunEntries, PendingStoreWork, read_pending_store_work,
};
use super::unconfigured_stores::{UnconfiguredStoreDrops, read_unconfigured_store_drops};
use super::{
    AttributeSyncConfig, AttributeSyncDependencies, AttributeSyncFence, AttributeSyncItem,
    AttributeSyncRetryPolicy, AttributeSyncWaker, AttributeSyncWriteError, AttributeSyncWriter,
};
use crate::commit::NodeStopReason;
use crate::effects::{EffectRuntime, RunLocation};
use crate::runtime::FlowAddress;

/// Distinct runs that time out without a commit in between before the whole store backs off.
const STORE_TIMEOUT_RUN_COUNT: usize = 3;

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
    /// Backoffs after store-scoped failures; every run of the store waits.
    store_backoffs: BTreeMap<String, Backoff>,
    /// Distinct runs of each store whose writes timed out since the store's last commit.
    timed_out_runs: BTreeMap<String, BTreeSet<RunLocation>>,
    /// Failure counts and run-scoped backoffs; other runs of the store keep syncing.
    run_retries: BTreeMap<(String, RunLocation), RunRetryState>,
    /// When each deferred (partition, store) pair is read again; each pair is due on its own.
    deferred_work: BTreeMap<(PartitionId, String), Instant>,
    /// When partitions whose unconfigured-store read failed are read again.
    deferred_unconfigured_reads: BTreeMap<PartitionId, Instant>,
    /// The Flow whose store-scoped failure backed off the store; the pair's next pass starts after it.
    resume_after_flow: BTreeMap<(PartitionId, String), FlowAddress>,
    /// The partition each cycle visits first; it moves past the last store-scoped failure.
    cycle_start_partition: PartitionId,
    backlog: BTreeMap<(PartitionId, String), BacklogSnapshot>,
}

/// The running projector; stop it before the node lease is released.
pub struct AttributeSyncProjectorHandle {
    stop: CancellationToken,
    task: JoinHandle<()>,
}

#[derive(Clone, Copy)]
struct Backoff {
    retry_at: Instant,
    next_delay: Duration,
}

#[derive(Default)]
struct RunRetryState {
    /// Consecutive failed writes of either scope, for `maximumAttempts`.
    failed_attempts: u32,
    /// Set by a run-scoped write failure or a rejected ack.
    backoff: Option<Backoff>,
    /// Most statements per transaction after a timeout; `None` chunks by the batch size.
    statements_per_transaction: Option<usize>,
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
    /// The run waits until this instant; later runs of its Flow ID wait with it.
    RetryRunAt(Instant),
    /// Every run of the store waits until this instant.
    RetryStoreAt(Instant),
    Stop,
}

#[derive(Clone, Copy)]
enum RetryScope {
    Store,
    Run,
    /// The run backs off, and the store too once enough distinct runs timed out without a commit.
    TimedOutRun,
}

enum WriteResult {
    Committed,
    Failed(AttributeSyncWriteError),
    Stopped,
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
            timed_out_runs: BTreeMap::new(),
            run_retries: BTreeMap::new(),
            deferred_work: BTreeMap::new(),
            deferred_unconfigured_reads: BTreeMap::new(),
            resume_after_flow: BTreeMap::new(),
            cycle_start_partition: PartitionId(0),
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
            if self.project_cycle(work).await.is_break() {
                return;
            }
            self.publish_backlog_metrics();
            work = self.next_work();
            if !work.is_empty() {
                continue;
            }
            // `next_work` took every due pair, so this instant is in the future and the wait never spins.
            let retry_at = self
                .deferred_work
                .values()
                .chain(self.deferred_unconfigured_reads.values())
                .min()
                .copied();
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

    /// Returns every store of woken partitions plus each deferred pair that is due.
    fn next_work(&self) -> BTreeMap<PartitionId, StoreSelection> {
        let mut work: BTreeMap<PartitionId, StoreSelection> = self
            .waker
            .take_woken_partitions()
            .into_iter()
            .map(|partition| (partition, StoreSelection::Every))
            .collect();
        let now = Instant::now();
        for ((partition, store_name), retry_at) in &self.deferred_work {
            if *retry_at > now {
                continue;
            }
            match work
                .entry(*partition)
                .or_insert_with(|| StoreSelection::Only(BTreeSet::new()))
            {
                StoreSelection::Every => {}
                StoreSelection::Only(stores) => {
                    stores.insert(store_name.clone());
                }
            }
        }
        for (partition, retry_at) in &self.deferred_unconfigured_reads {
            if *retry_at <= now {
                work.insert(*partition, StoreSelection::Every);
            }
        }
        work
    }

    /// Projects `work`, committing each pair's acks in the background while later pairs write.
    ///
    /// Returns `Break` when the projector stops.
    async fn project_cycle(
        &mut self,
        work: BTreeMap<PartitionId, StoreSelection>,
    ) -> ControlFlow<()> {
        // Dropping the set on a stop aborts proposals; the next owner rewrites unacknowledged rows.
        let mut ack_batches = JoinSet::new();
        let mut unconfigured_drops = self.read_unconfigured_drops(&work).await;
        let mut earlier_partitions = work;
        let later_partitions = earlier_partitions.split_off(&self.cycle_start_partition);
        for (partition, stores) in later_partitions.into_iter().chain(earlier_partitions) {
            if self.stop.is_cancelled() {
                return ControlFlow::Break(());
            }
            let drops = unconfigured_drops.remove(&partition).unwrap_or_default();
            self.project_partition(partition, stores, drops, &mut ack_batches)
                .await?;
        }
        self.finish_ack_batches(ack_batches).await
    }

    /// Returns `work`'s drops of unconfigured stores by partition; a failed read defers instead.
    async fn read_unconfigured_drops(
        &mut self,
        work: &BTreeMap<PartitionId, StoreSelection>,
    ) -> BTreeMap<PartitionId, Vec<UnconfiguredStoreDrops>> {
        let configured: BTreeSet<String> = self.config.store_names.iter().cloned().collect();
        let reads: Vec<(PartitionId, Option<BTreeSet<String>>)> = work
            .iter()
            .filter_map(|(partition, stores)| match stores {
                StoreSelection::Every => Some((*partition, None)),
                StoreSelection::Only(selected) if selected.is_subset(&configured) => None,
                StoreSelection::Only(selected) => Some((*partition, Some(selected.clone()))),
            })
            .collect();
        if reads.is_empty() {
            return BTreeMap::new();
        }
        let partitions: Vec<PartitionId> = reads.iter().map(|(partition, _)| *partition).collect();
        for partition in &partitions {
            self.deferred_unconfigured_reads.remove(partition);
        }
        let runtime = Arc::clone(&self.runtime);
        match read_unconfigured_store_drops(runtime, reads, configured, self.stop.clone()).await {
            Ok(drops) => drops,
            Err(detail) => {
                tracing::warn!(
                    detail,
                    "reading entries of unconfigured Attribute Stores failed; retrying"
                );
                let retry_at = Instant::now() + self.config.retry_policy.initial_interval;
                for partition in partitions {
                    self.deferred_unconfigured_reads.insert(partition, retry_at);
                }
                BTreeMap::new()
            }
        }
    }

    async fn project_partition(
        &mut self,
        partition: PartitionId,
        stores: StoreSelection,
        unconfigured_drops: Vec<UnconfiguredStoreDrops>,
        ack_batches: &mut JoinSet<AckBatchResult>,
    ) -> ControlFlow<()> {
        for store_name in self.config.store_names.clone() {
            if let StoreSelection::Only(selected) = &stores
                && !selected.contains(&store_name)
            {
                continue;
            }
            // This pass defers again whatever still has to wait.
            self.deferred_work.remove(&(partition, store_name.clone()));
            let work = match self.read_pending_store_work(partition, &store_name).await {
                Ok(work) => work,
                Err(detail) => {
                    tracing::warn!(
                        partition = partition.0,
                        store_name,
                        detail,
                        "reading pending Attribute Sync entries failed; retrying"
                    );
                    let retry_at = self.fail_store(&store_name);
                    self.defer(partition, &store_name, retry_at);
                    self.cycle_start_partition = self.partition_after(partition);
                    continue;
                }
            };
            self.forget_settled_runs(partition, &store_name, &work);
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
            if let Some(retry_at) = self.store_retry_at(&store_name) {
                self.defer(partition, &store_name, retry_at);
                continue;
            }
            let acks = self
                .project_store_work(partition, &store_name, work)
                .await?;
            if !acks.is_empty() {
                let proposer = AckProposer {
                    runtime: Arc::clone(&self.runtime),
                    stop: self.stop.clone(),
                    retry_policy: self.config.retry_policy.clone(),
                };
                ack_batches.spawn(proposer.propose_batch(AckBatch {
                    partition,
                    store_name,
                    acks,
                }));
            }
        }
        for drop in unconfigured_drops {
            self.deferred_work
                .remove(&(partition, drop.store_name.clone()));
            let proposer = AckProposer {
                runtime: Arc::clone(&self.runtime),
                stop: self.stop.clone(),
                retry_policy: self.config.retry_policy.clone(),
            };
            ack_batches.spawn(proposer.propose_batch(AckBatch {
                partition,
                store_name: drop.store_name,
                acks: drop.acks,
            }));
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

    /// Writes runs of each Flow ID in run order.
    ///
    /// Returns the acks of the runs that finished, or `Break` when the projector stops.
    async fn project_store_work(
        &mut self,
        partition: PartitionId,
        store_name: &str,
        work: PendingStoreWork,
    ) -> ControlFlow<(), Vec<PendingAck>> {
        let mut acks = Vec::new();
        let mut flows = work.flows;
        let pair = (partition, store_name.to_owned());
        if let Some(failed_flow) = self.resume_after_flow.remove(&pair) {
            // Another run probes the store first, so one failing run cannot hold back the rest.
            let resume_at = flows.partition_point(|flow_runs| {
                flow_runs
                    .first()
                    .is_some_and(|run_entries| run_entries.flow <= failed_flow)
            });
            flows.rotate_left(resume_at);
        }
        'flows: for flow_runs in flows {
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
                    RunNext::RetryRunAt(retry_at) => {
                        // Newer runs of a reused Flow ID must not land before this run.
                        self.defer(partition, store_name, retry_at);
                        continue 'flows;
                    }
                    RunNext::RetryStoreAt(retry_at) => {
                        self.defer(partition, store_name, retry_at);
                        self.resume_after_flow.insert(pair, flow);
                        self.cycle_start_partition = self.partition_after(partition);
                        break 'flows;
                    }
                    RunNext::Stop => return ControlFlow::Break(()),
                }
            }
        }
        ControlFlow::Continue(acks)
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
            mut items,
            unreadable_value_detail,
        } = run_entries;
        let retry_key = (store_name.to_owned(), run);
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
            self.run_retries.remove(&retry_key);
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
        let maximum_attempts = self.config.retry_policy.maximum_attempts;
        if maximum_attempts > 0
            && self
                .run_retries
                .get(&retry_key)
                .is_some_and(|state| state.failed_attempts >= maximum_attempts)
        {
            self.run_retries.remove(&retry_key);
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
            self.run_retries.remove(&retry_key);
            tracing::error!(
                store_name,
                flow_id = %flow.flow_id,
                run_id = %run.run_id,
                detail,
                "an Attribute value to sync cannot be loaded; dropping the entries"
            );
            return drop_every_entry(store_name, &entries);
        }
        if let Some(retry_at) = self.run_retry_at(&retry_key) {
            return RunOutcome {
                ack: None,
                next: RunNext::RetryRunAt(retry_at),
            };
        }
        let statements_per_transaction = self
            .run_retries
            .get(&retry_key)
            .and_then(|state| state.statements_per_transaction);
        let batches = transaction_batches(
            &mut items,
            statements_per_transaction,
            self.config.batch_size,
        );
        let mut written_keys = BTreeSet::new();
        for batch in batches {
            let (scope, detail) = match self.write_batch(store_name, &flow.flow_id, batch).await {
                WriteResult::Committed => {
                    self.timed_out_runs.remove(store_name);
                    written_keys.extend(batch.iter().map(|item| item.key.as_str()));
                    continue;
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
                    self.run_retries.remove(&retry_key);
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
                    (RetryScope::Store, detail)
                }
                WriteResult::Failed(AttributeSyncWriteError::RetryableForRun { detail }) => {
                    (RetryScope::Run, detail)
                }
                WriteResult::Failed(AttributeSyncWriteError::TimedOut { detail }) => {
                    // Per-statement latency usually causes it, so a smaller transaction can fit.
                    self.run_retries
                        .entry(retry_key.clone())
                        .or_default()
                        .statements_per_transaction = Some((statement_count(batch) / 2).max(1));
                    (RetryScope::TimedOutRun, detail)
                }
            };
            self.metrics
                .attribute_sync_write_failures
                .record(AttributeSyncFailureKind::Retryable);
            self.run_retries
                .entry(retry_key.clone())
                .or_default()
                .failed_attempts += 1;
            let next = match scope {
                RetryScope::Store => RunNext::RetryStoreAt(self.fail_store(store_name)),
                RetryScope::Run => RunNext::RetryRunAt(self.fail_run(&retry_key)),
                RetryScope::TimedOutRun => self.fail_timed_out_run(&retry_key),
            };
            tracing::warn!(
                store_name,
                flow_id = %flow.flow_id,
                run_id = %run.run_id,
                failure_scope = scope.as_str(),
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
                next,
            };
        }
        self.run_retries.remove(&retry_key);
        if !written_keys.is_empty() {
            self.store_backoffs.remove(store_name);
        }
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
                WriteResult::Failed(AttributeSyncWriteError::TimedOut {
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

    /// Waits for the cycle's ack batches, then wakes their partitions to confirm the acks.
    ///
    /// Returns `Break` when the projector or node stopped during a proposal.
    async fn finish_ack_batches(
        &mut self,
        mut ack_batches: JoinSet<AckBatchResult>,
    ) -> ControlFlow<()> {
        while let Some(joined) = ack_batches.join_next().await {
            let result = match joined {
                Ok(result) => result,
                Err(error) => {
                    // The batch's partition is unknown, so rescan everything; rewrites are idempotent.
                    tracing::error!(%error, "an Attribute Sync ack task failed; rescanning every partition");
                    for partition in 0..self.partition_hasher.partition_count() {
                        self.waker.wake_partition(PartitionId(
                            u16::try_from(partition)
                                .expect("the manifest limits partitions to u16"),
                        ));
                    }
                    continue;
                }
            };
            if result.has_stopped {
                return ControlFlow::Break(());
            }
            for run in result.rejected_runs {
                // A rejected ack leaves its entries pending; the backoff keeps them from looping hot.
                let retry_at = self.fail_run(&(result.store_name.clone(), run));
                self.defer(result.partition, &result.store_name, retry_at);
            }
            self.waker.wake_partition(result.partition);
        }
        ControlFlow::Continue(())
    }

    fn defer(&mut self, partition: PartitionId, store_name: &str, retry_at: Instant) {
        self.deferred_work
            .entry((partition, store_name.to_owned()))
            .and_modify(|due| *due = (*due).min(retry_at))
            .or_insert(retry_at);
    }

    /// Returns when the store's backoff ends, or `None` when the store may write now.
    fn store_retry_at(&self, store_name: &str) -> Option<Instant> {
        self.store_backoffs
            .get(store_name)
            .map(|backoff| backoff.retry_at)
            .filter(|retry_at| Instant::now() < *retry_at)
    }

    /// Returns when the run's backoff ends, or `None` when the run may write now.
    fn run_retry_at(&self, retry_key: &(String, RunLocation)) -> Option<Instant> {
        self.run_retries
            .get(retry_key)
            .and_then(|state| state.backoff)
            .map(|backoff| backoff.retry_at)
            .filter(|retry_at| Instant::now() < *retry_at)
    }

    /// Backs off every run of the store and returns when the store retries.
    fn fail_store(&mut self, store_name: &str) -> Instant {
        let backoff = Backoff::after_failure(
            self.store_backoffs.get(store_name).copied(),
            &self.config.retry_policy,
        );
        self.store_backoffs.insert(store_name.to_owned(), backoff);
        backoff.retry_at
    }

    /// Backs off one run and returns when it retries.
    fn fail_run(&mut self, retry_key: &(String, RunLocation)) -> Instant {
        let state = self.run_retries.entry(retry_key.clone()).or_default();
        let backoff = Backoff::after_failure(state.backoff, &self.config.retry_policy);
        state.backoff = Some(backoff);
        backoff.retry_at
    }

    /// Backs off a timed-out run, and its store too after `STORE_TIMEOUT_RUN_COUNT` distinct runs
    /// time out without a commit.
    ///
    /// Returns `RetryRunAt` with the run's retry, or `RetryStoreAt` with the store's retry once the
    /// store backs off.
    fn fail_timed_out_run(&mut self, retry_key: &(String, RunLocation)) -> RunNext {
        let run_retry_at = self.fail_run(retry_key);
        let (store_name, run) = retry_key;
        let timed_out_runs = self.timed_out_runs.entry(store_name.clone()).or_default();
        timed_out_runs.insert(*run);
        if timed_out_runs.len() < STORE_TIMEOUT_RUN_COUNT {
            return RunNext::RetryRunAt(run_retry_at);
        }
        tracing::warn!(
            store_name,
            timed_out_run_count = timed_out_runs.len(),
            "Attribute Store writes of several runs timed out without a commit; backing off the store"
        );
        RunNext::RetryStoreAt(self.fail_store(store_name))
    }

    fn partition_after(&self, partition: PartitionId) -> PartitionId {
        let next = (u32::from(partition.0) + 1) % self.partition_hasher.partition_count();
        PartitionId(u16::try_from(next).expect("the manifest limits partitions to u16"))
    }

    /// Drops retry state of runs in `partition` that no longer have pending entries of `store_name`.
    fn forget_settled_runs(
        &mut self,
        partition: PartitionId,
        store_name: &str,
        work: &PendingStoreWork,
    ) {
        let pending_runs: BTreeSet<RunLocation> = work
            .flows
            .iter()
            .flatten()
            .map(|run_entries| run_entries.run)
            .collect();
        let partition_hasher = &self.partition_hasher;
        self.run_retries.retain(|(retry_store_name, run), _| {
            retry_store_name != store_name
                || pending_runs.contains(run)
                || partition_hasher.partition_of(run.flow_hash) != partition
        });
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

impl Backoff {
    /// Returns the backoff after one more consecutive failure, starting at `initialInterval`.
    fn after_failure(previous: Option<Backoff>, policy: &AttributeSyncRetryPolicy) -> Self {
        let delay = previous.map_or(policy.initial_interval, |backoff| backoff.next_delay);
        Self {
            retry_at: Instant::now() + delay,
            next_delay: next_retry_delay(policy, delay),
        }
    }
}

impl RetryScope {
    fn as_str(self) -> &'static str {
        match self {
            Self::Store => "store",
            Self::Run => "run",
            Self::TimedOutRun => "timed_out_run",
        }
    }
}

/// Which stores of a partition a cycle reads.
enum StoreSelection {
    /// Every configured store, after a wake-up or at start.
    Every,
    /// Only stores whose deferred work is due.
    Only(BTreeSet<String>),
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
/// Returns the transactions of a run, chunked by `batch_size` until a timeout sets a statement limit.
///
/// Under a statement limit, the first transaction holds every plain Attribute, followed by map
/// instances; later transactions hold only map instances.
fn transaction_batches<'items>(
    items: &'items mut [AttributeSyncItem],
    statements_per_transaction: Option<usize>,
    batch_size: usize,
) -> Vec<&'items [AttributeSyncItem]> {
    let Some(statement_limit) = statements_per_transaction else {
        return items.chunks(batch_size.max(1)).collect();
    };
    let statement_limit = statement_limit.max(1);
    // One upsert writes every plain Attribute; splitting them only exposes a half-updated main row.
    items.sort_by_key(is_attribute_map_instance);
    let items: &'items [AttributeSyncItem] = items;
    let plain_count = items.partition_point(|item| !is_attribute_map_instance(item));
    let first_len = if plain_count == 0 {
        0
    } else {
        (plain_count + statement_limit - 1).min(items.len())
    };
    let (first, instances) = items.split_at(first_len);
    std::iter::once(first)
        .filter(|batch| !batch.is_empty())
        .chain(instances.chunks(statement_limit))
        .collect()
}

/// Returns the statement count of a transaction: one main-row upsert plus one per map instance.
fn statement_count(batch: &[AttributeSyncItem]) -> usize {
    let instance_count = batch
        .iter()
        .filter(|item| is_attribute_map_instance(item))
        .count();
    instance_count + usize::from(instance_count < batch.len())
}

/// Attribute names cannot contain `/`, so only an AttributeMap instance key does.
fn is_attribute_map_instance(item: &AttributeSyncItem) -> bool {
    item.key.contains('/')
}

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

pub(super) fn next_retry_delay(policy: &AttributeSyncRetryPolicy, delay: Duration) -> Duration {
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
