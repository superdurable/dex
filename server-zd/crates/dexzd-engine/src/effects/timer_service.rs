// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Per-partition timer services over the committed Timer index. Each loads the near future,
//! fires due timers in time order merged per run into `TimersFired`, and never fires a timer
//! before the owner's wall clock reaches its fire time.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;
use std::sync::atomic::{AtomicI64, Ordering};
use std::time::Duration;

use dexzd_keyspace::{ColumnFamily, IndexKind, PartitionId, TenantId, partition_index_prefix};
use dexzd_protocol::dexzd::v1::{FlowCommand, TimerId, TimersFiredCommand, flow_command};
use tokio::sync::Notify;
use tokio::time::Instant;

use crate::effects::state_reads::{
    RunLocation, TimerIndexEntry, parse_timer_index_key, read_run_record, timer_index_start_key,
};
use crate::effects::{EffectContext, EffectError};
use crate::flow_coordination::{CommitUrgency, FlowAddress};

/// The router's handles to the running partition timer services.
pub(crate) struct TimerServices {
    context: Arc<EffectContext>,
    partitions: BTreeMap<PartitionId, Arc<PartitionTimerWake>>,
}

impl TimerServices {
    pub(crate) fn new(context: Arc<EffectContext>) -> Self {
        Self {
            context,
            partitions: BTreeMap::new(),
        }
    }

    /// Starts the timer service of `partition` once; a later call rescans its Timer index from
    /// the start.
    pub(crate) fn start_partition(&mut self, partition: PartitionId) {
        if let Some(wake) = self.partitions.get(&partition) {
            // Replayed commits arm timers without TimerArmed; a rebuild must see them.
            wake.note_armed(i64::MIN);
            return;
        }
        let wake = Arc::new(PartitionTimerWake::new());
        self.partitions.insert(partition, Arc::clone(&wake));
        let service = PartitionTimerService::new(Arc::clone(&self.context), partition, wake);
        self.context.tracker.spawn(service.run());
    }

    /// Wakes the partition's service so it rereads the index for a newly armed timer.
    pub(crate) fn notify_armed(&mut self, partition: PartitionId, fire_at_micros: i64) {
        match self.partitions.get(&partition) {
            Some(wake) => wake.note_armed(fire_at_micros),
            None => tracing::warn!(
                %partition,
                fire_at_micros,
                "timer armed in a partition without a timer service"
            ),
        }
    }
}

/// What the router and one partition timer service share to decide when the service reads the
/// Timer index again.
pub(super) struct PartitionTimerWake {
    notify: Notify,
    /// Earliest fire time armed since the service's last pass began; `i64::MAX` when none.
    earliest_armed_micros: AtomicI64,
}

impl PartitionTimerWake {
    pub(super) fn new() -> Self {
        Self {
            notify: Notify::new(),
            earliest_armed_micros: AtomicI64::new(i64::MAX),
        }
    }

    /// Records an armed timer and wakes the service. Call it after the timer's index row commits.
    pub(super) fn note_armed(&self, fire_at_micros: i64) {
        self.earliest_armed_micros
            .fetch_min(fire_at_micros, Ordering::SeqCst);
        self.notify.notify_one();
    }
}

pub(super) struct PartitionTimerService {
    context: Arc<EffectContext>,
    partition: PartitionId,
    wake: Arc<PartitionTimerWake>,
    /// Index keys of fired timers still in the index: when to try again, and the delay that
    /// doubles on each try.
    retry_after: BTreeMap<Vec<u8>, (Instant, Duration)>,
    /// The Timer index key where the next pass starts its due scan.
    scan_start_key: Vec<u8>,
    /// When a pass last read the Timer index from the partition's first timer.
    last_full_scan_at: Instant,
}

/// The Timer index keys one due scan covered; a key in it and not returned left the index.
struct ScannedKeyRange<'a> {
    start: &'a [u8],
    end: ScannedKeyRangeEnd<'a>,
}

/// Where a due scan's covered keys end.
enum ScannedKeyRangeEnd<'a> {
    /// The scan read the whole due range, which ends before this key. A wall clock moved back
    /// can put this key at or below the start.
    Before(&'a [u8]),
    /// The scan stopped at its load threshold after reading this key.
    Through(&'a [u8]),
    /// The scan read no page.
    Empty,
}

/// The Timer index rows one pass read.
struct TimerIndexScan {
    /// Rows firing at or before now, in index order, including rows in retry backoff.
    due_entries: Vec<(Vec<u8>, TimerIndexEntry)>,
    /// First fire time after now within the horizon; read only when every due row is in backoff.
    next_fire_micros: Option<i64>,
}

impl PartitionTimerService {
    pub(super) fn new(
        context: Arc<EffectContext>,
        partition: PartitionId,
        wake: Arc<PartitionTimerWake>,
    ) -> Self {
        Self {
            context,
            partition,
            wake,
            retry_after: BTreeMap::new(),
            scan_start_key: partition_index_prefix(partition, IndexKind::Timer),
            last_full_scan_at: Instant::now(),
        }
    }

    async fn run(mut self) {
        let mut backoff = self.context.retry_backoff();
        loop {
            if self.context.shutdown.is_cancelled() {
                return;
            }
            let wake_at = match self.fire_due_timers().await {
                Ok(wake_at) => {
                    backoff = self.context.retry_backoff();
                    wake_at
                }
                Err(EffectError::Stopped) => return,
                Err(error) => {
                    tracing::warn!(%error, partition = %self.partition, "timer service pass failed");
                    if !backoff.wait(&self.context.shutdown).await {
                        return;
                    }
                    continue;
                }
            };
            let Some(wake_at) = wake_at else {
                continue;
            };
            // Biased for reproducible simulation; every wake starts a pass that fires due
            // timers.
            tokio::select! {
                biased;
                () = self.context.shutdown.cancelled() => return,
                () = self.wake.notify.notified() => {}
                () = tokio::time::sleep_until(wake_at) => {}
            }
        }
    }

    /// Fires every due timer. Returns when to look again, or `None` to look again at once.
    pub(super) async fn fire_due_timers(&mut self) -> Result<Option<Instant>, EffectError> {
        self.rewind_scan_start_key();
        let now_micros = self.context.clock.now_unix_micros();
        let lookahead_micros =
            i64::try_from(self.context.config.timer_lookahead.as_micros()).unwrap_or(i64::MAX);
        let horizon_micros = now_micros.saturating_add(lookahead_micros);
        let now = Instant::now();
        let scan = self.load_timers(now_micros, horizon_micros, now)?;
        let entries = scan.due_entries;
        let is_waiting = |retry_after: &BTreeMap<Vec<u8>, (Instant, Duration)>, key: &Vec<u8>| {
            retry_after
                .get(key)
                .is_some_and(|(retry_at, _)| *retry_at > now)
        };
        let oldest_due = entries
            .iter()
            .filter(|(key, _)| !is_waiting(&self.retry_after, key))
            .map(|(_, entry)| entry.fire_at_micros)
            .min();
        self.context
            .metrics
            .timer_lag
            .set_due(self.partition.0 as usize, oldest_due);

        let mut runs: Vec<(RunLocation, Vec<TimerId>)> = Vec::new();
        let mut run_positions: BTreeMap<RunLocation, usize> = BTreeMap::new();
        for (key, entry) in &entries {
            if is_waiting(&self.retry_after, key) {
                continue;
            }
            let position = *run_positions.entry(entry.run).or_insert_with(|| {
                runs.push((entry.run, Vec::new()));
                runs.len() - 1
            });
            runs[position].1.push(entry.timer_id.clone());
        }
        if !runs.is_empty() {
            let fired_keys: Vec<Vec<u8>> = entries
                .iter()
                .filter(|(key, _)| !is_waiting(&self.retry_after, key))
                .map(|(key, _)| key.clone())
                .collect();
            let proposals = runs
                .into_iter()
                .map(|(run, timers)| self.propose_timers_fired(run, timers));
            for result in futures::future::join_all(proposals).await {
                result?;
            }
            self.context
                .metrics
                .timer_lag
                .set_due(self.partition.0 as usize, None);
            let fired_at = Instant::now();
            for key in fired_keys {
                let delay = self
                    .retry_after
                    .get(&key)
                    .map_or(self.context.config.fired_timer_retry_delay, |(_, delay)| {
                        (*delay * 2).min(self.context.config.timer_lookahead)
                    });
                self.retry_after.insert(key, (fired_at + delay, delay));
            }
            return Ok(None);
        }
        let fire_wait_micros = scan
            .next_fire_micros
            .unwrap_or(horizon_micros)
            .saturating_sub(now_micros)
            .max(0);
        let mut wake_at = now + Duration::from_micros(u64::try_from(fire_wait_micros).unwrap_or(0));
        if let Some(retry_at) = self
            .retry_after
            .values()
            .map(|(retry_at, _)| *retry_at)
            .filter(|retry_at| *retry_at > now)
            .min()
        {
            wake_at = wake_at.min(retry_at);
        }
        let full_scan_interval = self.context.config.timer_full_scan_interval;
        if !full_scan_interval.is_zero()
            && let Some(full_scan_at) = self.last_full_scan_at.checked_add(full_scan_interval)
        {
            wake_at = wake_at.min(full_scan_at);
        }
        Ok(Some(wake_at))
    }

    /// Lowers the read cursor to the earliest newly armed timer, or resets it for a full scan.
    fn rewind_scan_start_key(&mut self) {
        let earliest_armed_micros = self
            .wake
            .earliest_armed_micros
            .swap(i64::MAX, Ordering::SeqCst);
        let now = Instant::now();
        if now.duration_since(self.last_full_scan_at)
            >= self.context.config.timer_full_scan_interval
        {
            self.last_full_scan_at = now;
            self.scan_start_key = partition_index_prefix(self.partition, IndexKind::Timer);
            return;
        }
        if earliest_armed_micros == i64::MAX {
            return;
        }
        let armed_start_key = timer_index_start_key(self.partition, earliest_armed_micros);
        if armed_start_key < self.scan_start_key {
            self.scan_start_key = armed_start_key;
        }
    }

    /// Reads due timer rows from the read cursor, paged by the load limit, then advances the
    /// cursor.
    ///
    /// Returns the rows that fire at or before `now_micros`, in index order and including rows in
    /// retry backoff. Paging stops after the page that brings the rows outside backoff to the load
    /// limit, so a pass may return more than the limit. When none of them is outside backoff, also
    /// returns the fire time of the first timer after `now_micros` and at or before
    /// `horizon_micros`, if any.
    fn load_timers(
        &mut self,
        now_micros: i64,
        horizon_micros: i64,
        now: Instant,
    ) -> Result<TimerIndexScan, EffectError> {
        let limit = self.context.config.timer_load_limit;
        let reader = self
            .context
            .flow_effect_operations
            .committed_state_reader()?;
        let due_end = timer_index_start_key(self.partition, now_micros.saturating_add(1));
        let mut start = self.scan_start_key.clone();
        let mut entries = Vec::new();
        let mut ready_count = 0;
        // An in-memory reader panics on a reversed range.
        let mut is_due_range_read = start >= due_end;
        while !is_due_range_read && ready_count < limit {
            let page = reader.scan(ColumnFamily::PartitionIndex, &start, &due_end, limit)?;
            is_due_range_read = page.len() < limit;
            for (key, _) in page {
                let entry = parse_timer_index_key(self.partition, &key)?;
                let is_waiting = self
                    .retry_after
                    .get(&key)
                    .is_some_and(|(retry_at, _)| *retry_at > now);
                if !is_waiting {
                    ready_count += 1;
                }
                entries.push((key, entry));
            }
            let Some((last_key, _)) = entries.last() else {
                break;
            };
            start = last_key.iter().copied().chain([0]).collect();
        }
        let mut next_fire_micros = None;
        // Due timers outside backoff fire now, and the next pass starts at once.
        if is_due_range_read && ready_count == 0 {
            let next_start = self.scan_start_key.as_slice().max(due_end.as_slice());
            let horizon_end =
                timer_index_start_key(self.partition, horizon_micros.saturating_add(1));
            if next_start < horizon_end.as_slice() {
                let next_page =
                    reader.scan(ColumnFamily::PartitionIndex, next_start, &horizon_end, 1)?;
                if let Some((key, _)) = next_page.first() {
                    next_fire_micros =
                        Some(parse_timer_index_key(self.partition, key)?.fire_at_micros);
                }
            }
        }
        let scanned_keys: BTreeSet<&Vec<u8>> = entries.iter().map(|(key, _)| key).collect();
        let scanned_range = ScannedKeyRange {
            start: self.scan_start_key.as_slice(),
            end: match entries.last() {
                _ if is_due_range_read => ScannedKeyRangeEnd::Before(due_end.as_slice()),
                Some((last_key, _)) => ScannedKeyRangeEnd::Through(last_key.as_slice()),
                None => ScannedKeyRangeEnd::Empty,
            },
        };
        // Rows beyond the scan keep their backoff; scanned rows no longer in the index lose it.
        self.retry_after
            .retain(|key, _| scanned_keys.contains(key) || !scanned_range.contains(key));
        // An empty scan moves the cursor at most to this pass's due-range end, so later passes
        // reread future timers.
        match entries.first() {
            Some((first_key, _)) => self.scan_start_key.clone_from(first_key),
            None if is_due_range_read && due_end > self.scan_start_key => {
                self.scan_start_key = due_end;
            }
            None => {}
        }
        Ok(TimerIndexScan {
            due_entries: entries,
            next_fire_micros,
        })
    }

    async fn propose_timers_fired(
        &self,
        run: RunLocation,
        timers: Vec<TimerId>,
    ) -> Result<(), EffectError> {
        let run_record = {
            let reader = self
                .context
                .flow_effect_operations
                .committed_state_reader()?;
            read_run_record(reader.as_ref(), run)?
        };
        let Some(run_record) = run_record else {
            tracing::warn!(run_id = %run.run_id, "timer index refers to a missing run");
            return Ok(());
        };
        let flow = FlowAddress {
            tenant_id: TenantId(run_record.tenant_id),
            flow_id: run_record.flow_id,
        };
        let command = FlowCommand {
            flow_id: flow.flow_id.clone(),
            run_id: run.run_id.as_bytes().to_vec(),
            tenant_id: flow.tenant_id.0,
            command: Some(flow_command::Command::TimersFired(TimersFiredCommand {
                timers,
            })),
            ..FlowCommand::default()
        };
        self.context
            .propose_until_committed(&flow, command, CommitUrgency::Prompt)
            .await
            .ok_or(EffectError::Stopped)?;
        Ok(())
    }
}

impl ScannedKeyRange<'_> {
    fn contains(&self, key: &[u8]) -> bool {
        key >= self.start
            && match self.end {
                ScannedKeyRangeEnd::Before(end) => key < end,
                ScannedKeyRangeEnd::Through(last) => key <= last,
                ScannedKeyRangeEnd::Empty => false,
            }
    }
}
