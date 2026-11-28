// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Mutex;
use std::sync::atomic::{AtomicU64, Ordering};

use dexzd_apply::diagnostics::{current_semantic_point_hash, time_travel_target_hash};
use dexzd_apply::{ApplyContext, ApplyOutcome};
use dexzd_keyspace::{ColumnFamily, RunId, RunTable, parse_run_key};
use dexzd_protocol::dexzd::v1::{FlowCommand, HistoryRecord, LogPosition, flow_command::Command};
use dexzd_storage::{LocalReadView, ScanDirection, StorageError};
use prost::Message;

use crate::commit::extension_record::FrameRecordHandling;
use crate::commit::partition_applier::FrameFailure;
use crate::commit::replay_checks::{compare_semantic_point_hash, semantic_metadata_points};
use crate::commit::staged_state_reader::StagedStateReader;

pub(crate) struct FlowReplayDiagnostics {
    semantic_point_hashes: Mutex<BTreeMap<(RunId, u32, u64), [u8; 16]>>,
    pre_replay_semantic_points: BTreeSet<(RunId, u32, u64)>,
    verified_time_travels: AtomicU64,
    unverifiable_time_travels: AtomicU64,
}

impl FlowReplayDiagnostics {
    /// Returns the store error of indexing semantic points when replay checks are on.
    pub(crate) fn new(
        store: &dyn LocalReadView,
        records: &FrameRecordHandling,
        is_checking_replay: bool,
    ) -> Result<Self, StorageError> {
        // Extension rows do not parse as run keys.
        let is_flow_command_node = matches!(records, FrameRecordHandling::FlowCommand { .. });
        let pre_replay_semantic_points = if is_checking_replay && is_flow_command_node {
            Self::pre_replay_semantic_points(store)?
        } else {
            BTreeSet::new()
        };
        tracing::debug!(
            unverifiable_semantic_points = pre_replay_semantic_points.len(),
            "semantic points present before log replay lack independent hashes"
        );
        Ok(Self {
            semantic_point_hashes: Mutex::new(BTreeMap::new()),
            pre_replay_semantic_points,
            verified_time_travels: AtomicU64::new(0),
            unverifiable_time_travels: AtomicU64::new(0),
        })
    }

    fn pre_replay_semantic_points(
        store: &dyn LocalReadView,
    ) -> Result<BTreeSet<(RunId, u32, u64)>, StorageError> {
        let mut points = BTreeSet::new();
        let mut runs = BTreeSet::new();
        let mut start = None;
        loop {
            let rows = store.scan(
                ColumnFamily::History,
                start.as_deref(),
                None,
                ScanDirection::Forward,
                256,
            )?;
            if rows.is_empty() {
                break;
            }
            for (key, value) in &rows {
                let parsed = parse_run_key(key)
                    .map_err(|error| StorageError::Invalid(format!("history key: {error}")))?;
                runs.insert((parsed.flow_hash, parsed.run_id));
                if parsed.table == RunTable::HistoryRecord {
                    let history = HistoryRecord::decode(value.as_slice())?;
                    if history.is_semantic {
                        points.insert((parsed.run_id, history.attempt, history.seq));
                    }
                }
            }
            let mut next = rows
                .last()
                .expect("a nonempty page has a last key")
                .0
                .clone();
            next.push(0);
            start = Some(next);
        }
        // Offload deletes uploaded records; their semantic metadata stays for the window.
        points.extend(semantic_metadata_points(store, &runs)?);
        Ok(points)
    }

    pub(super) fn check_time_travel_target(
        &self,
        context: &ApplyContext,
        reader: &StagedStateReader<'_>,
        command: &FlowCommand,
        position: &LogPosition,
    ) -> Result<(), FrameFailure> {
        let target = time_travel_target_hash(context, reader, command).map_err(|error| {
            FrameFailure::Halt(format!(
                "time-travel diagnostic for Flow {} at seq {} failed: {error}",
                command.flow_id, position.seq
            ))
        })?;
        let Some(target) = target else {
            return Ok(());
        };
        let hashes = self
            .semantic_point_hashes
            .lock()
            .expect("semantic point hash lock is not poisoned");
        let key = (target.run_id, target.run_attempt, target.run_seq);
        if let Some(expected) = hashes.get(&key) {
            compare_semantic_point_hash(expected, &target, command, position)
                .map_err(FrameFailure::Halt)?;
            self.verified_time_travels.fetch_add(1, Ordering::Relaxed);
        } else if self.pre_replay_semantic_points.contains(&key) {
            let unverifiable_count = self
                .unverifiable_time_travels
                .fetch_add(1, Ordering::Relaxed)
                + 1;
            tracing::debug!(
                flow_id = %command.flow_id,
                log_seq = position.seq,
                run_id = %target.run_id,
                run_attempt = target.run_attempt,
                run_seq = target.run_seq,
                unverifiable_count,
                "time-travel target predates replay and has no independent hash"
            );
        } else {
            return Err(FrameFailure::Halt(format!(
                "time-travel verification has no expected hash for Flow {} command {:?} at log seq {} semantic key ({}, {}, {})",
                command.flow_id,
                command.command_id,
                position.seq,
                target.run_id,
                target.run_attempt,
                target.run_seq
            )));
        }
        Ok(())
    }

    pub(super) fn record_semantic_point(
        &self,
        context: &ApplyContext,
        reader: &StagedStateReader<'_>,
        outcome: &ApplyOutcome,
        command: &FlowCommand,
    ) -> Result<(), FrameFailure> {
        if !outcome.writes.is_empty() && matches!(&command.command, Some(Command::RunPurge(_))) {
            let run_id = RunId::from_slice(&command.run_id).map_err(|error| {
                FrameFailure::Halt(format!(
                    "RunPurge command {:?} has invalid run ID: {error}",
                    command.command_id
                ))
            })?;
            self.semantic_point_hashes
                .lock()
                .expect("semantic point hash lock is not poisoned")
                .retain(|(stored_run_id, _, _), _| *stored_run_id != run_id);
            return Ok(());
        }
        let Some(run_id) = outcome.run_id else {
            return Ok(());
        };
        let point = current_semantic_point_hash(context, reader, run_id, &command.command_id)
            .map_err(|error| {
                FrameFailure::Halt(format!(
                    "semantic-point diagnostic for Flow {} command {:?} failed: {error}",
                    command.flow_id, command.command_id
                ))
            })?;
        if let Some(point) = point {
            let mut hashes = self
                .semantic_point_hashes
                .lock()
                .expect("semantic point hash lock is not poisoned");
            hashes.retain(|(run_id, _, run_seq), _| {
                *run_id != point.run_id
                    || *run_seq >= point.window_start_seq
                    || *run_seq == point.genesis_seq
            });
            hashes.insert(
                (point.run_id, point.run_attempt, point.run_seq),
                point.state_hash,
            );
        }
        Ok(())
    }

    pub(super) fn time_travel_verification_counts(&self) -> (u64, u64) {
        (
            self.verified_time_travels.load(Ordering::Relaxed),
            self.unverifiable_time_travels.load(Ordering::Relaxed),
        )
    }
}
