// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The Attribute Sync outbox: marking, coalescing, acks, drops, purge, and time travel.

use std::collections::BTreeMap;

use dexzd_apply::{
    ApplyContext, ApplyEffect, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command,
};
use dexzd_keyspace::row_keys::{
    attribute_sync_outbox_key, attribute_sync_pending_index_key, prefix_range,
};
use dexzd_keyspace::{
    ColumnFamily, IndexKind, PartitionHasher, RunId, RunTable, TenantId, run_prefix,
    run_table_prefix,
};
use dexzd_protocol::dex::{
    AttributeStoreNames, AttributeSyncConfig, AttributeWrite, FlowConfig, FlowResetType,
    FlowStartOptions, FlowTimeoutPolicy, ResetFlowRequest, SetAttributesRequest, StartFlowRequest,
    StopFlowRequest, StopType, UpdateFlowConfigRequest, Value, WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributeSyncAckCommand, AttributeSyncOutboxRecord, AttributesSetCommand, CommandReply,
    FlowCommand, FlowConfigUpdateCommand, FlowStartCommand, FlowStopCommand, RetentionTimerId,
    RunPurgeCommand, RunSettings, TimeTravelCommand, TimerId, TimersFiredCommand, command_reply,
    flow_command::Command, timer_id,
};
use prost::Message;

const SECOND: i64 = 1_000_000;
const PRIMARY: &str = "primary";
const REPLICA: &str = "replica";

#[derive(Default, Clone)]
struct MemoryState {
    rows: BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>>,
}

impl FlowStateReader for MemoryState {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        Ok(self.rows.get(&(column_family, key.to_vec())).cloned())
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        Ok(self
            .rows
            .range((column_family, start.to_vec())..(column_family, end.to_vec()))
            .take(limit)
            .map(|((_, key), value)| (key.clone(), value.clone()))
            .collect())
    }
}

impl MemoryState {
    fn commit(&mut self, outcome: &ApplyOutcome) {
        for write in &outcome.writes {
            match write {
                StateWrite::Put {
                    column_family,
                    key,
                    value,
                } => {
                    self.rows
                        .insert((*column_family, key.clone()), value.clone());
                }
                StateWrite::Delete { column_family, key } => {
                    self.rows.remove(&(*column_family, key.clone()));
                }
                StateWrite::DeleteRange {
                    column_family,
                    start,
                    end,
                } => {
                    let doomed: Vec<_> = self
                        .rows
                        .range((*column_family, start.clone())..(*column_family, end.clone()))
                        .map(|(key, _)| key.clone())
                        .collect();
                    for key in doomed {
                        self.rows.remove(&key);
                    }
                }
            }
        }
    }
}

struct Harness {
    state: MemoryState,
    context: ApplyContext,
    now_micros: i64,
    flow_id: String,
    run_id: Vec<u8>,
}

impl Harness {
    fn new() -> Self {
        let flow_id = "account-1";
        let hasher = PartitionHasher::new(&[7; 16], 1024).expect("valid partition hasher");
        let flow_hash = hasher.flow_hash(TenantId::DEFAULT, flow_id);
        Self {
            state: MemoryState::default(),
            context: ApplyContext {
                partition: hasher.partition_of(flow_hash),
                flow_hash,
            },
            now_micros: 1_800_000_000 * SECOND,
            flow_id: flow_id.to_owned(),
            run_id: Vec::new(),
        }
    }

    fn apply(&mut self, body: Command) -> ApplyOutcome {
        self.now_micros += 1000;
        let command = FlowCommand {
            flow_id: self.flow_id.clone(),
            run_id: if matches!(body, Command::FlowStart(_)) {
                Vec::new()
            } else {
                self.run_id.clone()
            },
            expected_run_attempt: 0,
            epoch: 1,
            behavior_version: 1,
            proposed_at_micros: self.now_micros,
            command_id: (self.now_micros as u128).to_be_bytes().to_vec(),
            tenant_id: 0,
            command: Some(body),
        };
        let outcome =
            apply_flow_command(&self.context, &self.state, &command).expect("apply succeeds");
        let again =
            apply_flow_command(&self.context, &self.state, &command).expect("apply succeeds");
        assert_eq!(outcome.writes, again.writes, "apply must be deterministic");
        assert_eq!(
            outcome.effects, again.effects,
            "effects must be deterministic"
        );
        self.state.commit(&outcome);
        outcome
    }

    fn start(
        &mut self,
        store_names: Option<&[&str]>,
        attributes: Vec<AttributeWrite>,
    ) -> ApplyOutcome {
        let flow_config = FlowConfig {
            attribute_store_names: store_names.map(|names| AttributeStoreNames {
                names: names.iter().map(|name| (*name).to_owned()).collect(),
            }),
            ..FlowConfig::default()
        };
        let outcome = self.apply(Command::FlowStart(FlowStartCommand {
            new_run_id: vec![9; 16],
            request: Some(StartFlowRequest {
                flow_id: self.flow_id.clone(),
                flow_type: "AccountFlow".to_owned(),
                request_id: "start-1".to_owned(),
                flow_start_options: Some(FlowStartOptions {
                    attributes,
                    ..FlowStartOptions::default()
                }),
                ..StartFlowRequest::default()
            }),
            settings: Some(settings()),
            resolved_timeout_policy: FlowTimeoutPolicy::Fail as i32,
            worker_target: Some(WorkerTarget {
                is_pull_mode: true,
                ..WorkerTarget::default()
            }),
            effective_flow_config: Some(flow_config),
            ..FlowStartCommand::default()
        }));
        let Some(command_reply::Reply::StartFlow(reply)) = &outcome.reply.reply else {
            panic!("the Flow starts: {:?}", outcome.reply);
        };
        self.run_id = reply.run_id.clone();
        outcome
    }

    fn set(&mut self, writes: Vec<AttributeWrite>, request_id: &str) -> ApplyOutcome {
        let outcome = self.apply(Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                attributes: writes,
                request_id: request_id.to_owned(),
                ..SetAttributesRequest::default()
            }),
            hard_delete_objects: Vec::new(),
            request_fingerprint: request_id.as_bytes().to_vec(),
        }));
        assert!(is_ok(&outcome.reply), "SetAttributes applies");
        outcome
    }

    fn ack(
        &mut self,
        store_name: &str,
        acked_seq: u64,
        keys: &[&str],
        is_dropped: bool,
    ) -> ApplyOutcome {
        self.apply(Command::AttributeSyncAck(AttributeSyncAckCommand {
            store_name: store_name.to_owned(),
            acked_seq,
            attribute_keys: keys.iter().map(|key| (*key).to_owned()).collect(),
            is_dropped,
        }))
    }

    fn run(&self) -> RunId {
        RunId::from_slice(&self.run_id).expect("the harness has a run")
    }

    /// Entries of the run as (store, key) -> (written seq, retry window start).
    fn entries(&self) -> BTreeMap<(String, String), (u64, i64)> {
        let prefix = run_table_prefix(
            self.context.flow_hash,
            self.run(),
            RunTable::AttributeSyncOutbox,
        );
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
            .map(|((_, key), value)| {
                let record =
                    AttributeSyncOutboxRecord::decode(value.as_slice()).expect("entry decodes");
                assert_eq!(
                    key,
                    &attribute_sync_outbox_key(
                        self.context.flow_hash,
                        self.run(),
                        &record.store_name,
                        &record.attribute_key
                    ),
                    "the entry is stored under its store and key"
                );
                (
                    (record.store_name, record.attribute_key),
                    (record.written_seq, record.retry_window_started_at_micros),
                )
            })
            .collect()
    }

    fn has_pending_index(&self, store_name: &str) -> bool {
        self.state.rows.contains_key(&(
            ColumnFamily::PartitionIndex,
            attribute_sync_pending_index_key(
                self.context.partition,
                store_name,
                self.context.flow_hash,
                self.run(),
            ),
        ))
    }

    fn pending_index_count(&self) -> usize {
        self.state
            .rows
            .keys()
            .filter(|(column_family, key)| {
                *column_family == ColumnFamily::PartitionIndex
                    && key.get(2) == Some(&(IndexKind::AttributeSyncPending as u8))
            })
            .count()
    }
}

fn settings() -> RunSettings {
    RunSettings {
        history_window_semantic_events: 10_000,
        history_prune_chunk_semantic_events: 1000,
        max_retained_superseded_bytes: 256 << 20,
        retention_after_close_micros: 60 * SECOND,
        wire_inline_threshold_bytes: 100,
        max_value_bytes: 16 << 20,
        checkpoint_min_written_bytes: 1 << 20,
        checkpoint_max_commands: 10_000,
        request_dedup_capacity: 1000,
        max_run_state_bytes: 1 << 30,
        max_active_step_executions: 10_000,
    }
}

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}

fn null_value() -> Value {
    Value {
        kind: Some(value::Kind::NullValue(0)),
    }
}

fn synced(key: &str, value: Value) -> AttributeWrite {
    AttributeWrite {
        key: key.to_owned(),
        value: Some(value),
        sync_config: Some(AttributeSyncConfig { enabled: true }),
        ..AttributeWrite::default()
    }
}

fn unsynced(key: &str, value: Value) -> AttributeWrite {
    AttributeWrite {
        key: key.to_owned(),
        value: Some(value),
        sync_config: Some(AttributeSyncConfig { enabled: false }),
        ..AttributeWrite::default()
    }
}

fn is_ok(reply: &CommandReply) -> bool {
    matches!(reply.reply, Some(command_reply::Reply::Ok(_)))
}

fn entry(store_name: &str, attribute_key: &str) -> (String, String) {
    (store_name.to_owned(), attribute_key.to_owned())
}

fn pending_stores(outcome: &ApplyOutcome) -> Vec<Vec<String>> {
    outcome
        .effects
        .iter()
        .filter_map(|effect| match effect {
            ApplyEffect::AttributeSyncPending { store_names, .. } => Some(store_names.clone()),
            _ => None,
        })
        .collect()
}

#[test]
fn synced_writes_mark_every_target_store_and_coalesce_per_key() {
    let mut harness = Harness::new();
    let started = harness.start(
        Some(&[REPLICA, PRIMARY]),
        vec![
            synced("status", string_value("opened")),
            unsynced("notes", string_value("local only")),
            AttributeWrite {
                key: "plain".to_owned(),
                value: Some(string_value("no sync config")),
                ..AttributeWrite::default()
            },
        ],
    );
    let start_time = harness.now_micros;
    assert_eq!(
        pending_stores(&started),
        vec![vec![PRIMARY.to_owned(), REPLICA.to_owned()]]
    );
    assert_eq!(
        harness.entries(),
        BTreeMap::from([
            (entry(PRIMARY, "status"), (1, start_time)),
            (entry(REPLICA, "status"), (1, start_time)),
        ])
    );
    assert!(harness.has_pending_index(PRIMARY) && harness.has_pending_index(REPLICA));

    harness.set(
        vec![
            synced("status", string_value("active")),
            synced("plans/basic", string_value("trial")),
            synced("status", string_value("verified")),
        ],
        "set-1",
    );
    let second_time = harness.now_micros;
    harness.set(vec![synced("plans/basic", null_value())], "set-2");
    let entries = harness.entries();
    assert_eq!(entries.len(), 4, "one entry per store and key: {entries:?}");
    assert_eq!(
        entries[&entry(PRIMARY, "status")],
        (2, start_time),
        "coalesced, first time kept"
    );
    assert_eq!(entries[&entry(REPLICA, "status")], (2, start_time));
    assert_eq!(
        entries[&entry(PRIMARY, "plans/basic")],
        (3, second_time),
        "null writes are marked"
    );
    assert_eq!(entries[&entry(REPLICA, "plans/basic")], (3, second_time));

    let unsynced_set = harness.set(vec![unsynced("status", string_value("quiet"))], "set-3");
    assert!(pending_stores(&unsynced_set).is_empty());
    assert_eq!(
        harness.entries(),
        entries,
        "unsynced writes leave the outbox alone"
    );
}

#[test]
fn runs_without_attribute_store_names_mark_nothing() {
    let mut harness = Harness::new();
    let started = harness.start(None, vec![synced("status", string_value("opened"))]);
    assert!(pending_stores(&started).is_empty());
    harness.set(vec![synced("status", string_value("active"))], "set-1");
    assert!(harness.entries().is_empty());
    assert_eq!(harness.pending_index_count(), 0);

    harness.apply(Command::FlowConfigUpdate(FlowConfigUpdateCommand {
        request: Some(UpdateFlowConfigRequest {
            flow_id: "account-1".to_owned(),
            flow_config: Some(FlowConfig {
                attribute_store_names: Some(AttributeStoreNames {
                    names: vec![PRIMARY.to_owned()],
                }),
                ..FlowConfig::default()
            }),
            ..UpdateFlowConfigRequest::default()
        }),
    }));
    harness.set(vec![synced("status", string_value("verified"))], "set-2");
    assert_eq!(
        harness.entries().keys().cloned().collect::<Vec<_>>(),
        vec![entry(PRIMARY, "status")],
        "later writes use the updated store names"
    );
}

#[test]
fn ack_deletes_only_entries_up_to_the_acked_seq() {
    let mut harness = Harness::new();
    harness.start(
        Some(&[PRIMARY, REPLICA]),
        vec![
            synced("status", string_value("opened")),
            synced("plans/basic", string_value("trial")),
        ],
    );
    let start_time = harness.now_micros;
    harness.set(vec![synced("status", string_value("active"))], "set-1");

    let partial = harness.ack(PRIMARY, 1, &[], false);
    assert!(is_ok(&partial.reply));
    assert!(!partial.writes.is_empty(), "the ack applies");
    let ack_time = harness.now_micros;
    let entries = harness.entries();
    assert!(!entries.contains_key(&entry(PRIMARY, "plans/basic")));
    assert_eq!(
        entries[&entry(PRIMARY, "status")],
        (2, ack_time),
        "a change after the acked seq stays pending with a fresh retry window"
    );
    assert_eq!(
        entries[&entry(REPLICA, "status")],
        (2, start_time),
        "other stores are untouched"
    );
    assert_eq!(entries[&entry(REPLICA, "plans/basic")], (1, start_time));
    assert!(harness.has_pending_index(PRIMARY));

    let filtered = harness.ack(REPLICA, 5, &["plans/basic"], false);
    assert!(is_ok(&filtered.reply));
    assert_eq!(
        harness
            .entries()
            .keys()
            .filter(|(store_name, _)| store_name == REPLICA)
            .cloned()
            .collect::<Vec<_>>(),
        vec![entry(REPLICA, "status")],
        "a key filter limits the ack"
    );
    assert!(harness.has_pending_index(REPLICA));

    harness.ack(PRIMARY, 2, &[], false);
    assert!(
        !harness.has_pending_index(PRIMARY),
        "the last entry clears the index"
    );
    assert!(harness.has_pending_index(REPLICA));
    let repeated = harness.ack(PRIMARY, 2, &[], false);
    assert!(is_ok(&repeated.reply));
    assert!(repeated.writes.is_empty(), "a repeated ack changes nothing");
}

#[test]
fn drop_ack_deletes_entries_and_reports_them() {
    let mut harness = Harness::new();
    harness.start(
        Some(&[PRIMARY]),
        vec![
            synced("status", string_value("opened")),
            synced("plans/basic", string_value("trial")),
        ],
    );
    let dropped = harness.ack(PRIMARY, 1, &[], true);
    assert!(is_ok(&dropped.reply));
    let reported: Vec<_> = dropped
        .effects
        .iter()
        .filter_map(|effect| match effect {
            ApplyEffect::AttributeSyncDropped {
                run_id,
                store_name,
                attribute_keys,
            } => Some((*run_id, store_name.clone(), attribute_keys.clone())),
            _ => None,
        })
        .collect();
    assert_eq!(
        reported,
        vec![(
            harness.run(),
            PRIMARY.to_owned(),
            vec!["plans/basic".to_owned(), "status".to_owned()]
        )]
    );
    assert!(harness.entries().is_empty());
    assert!(!harness.has_pending_index(PRIMARY));

    harness.set(vec![synced("status", string_value("active"))], "set-1");
    assert_eq!(
        harness.entries().keys().cloned().collect::<Vec<_>>(),
        vec![entry(PRIMARY, "status")],
        "a later write syncs again after a drop"
    );

    harness.set(vec![synced("status", string_value("verified"))], "set-2");
    let partial_drop = harness.ack(PRIMARY, 3, &[], true);
    assert!(is_ok(&partial_drop.reply));
    let drop_time = harness.now_micros;
    assert_eq!(
        harness.entries(),
        BTreeMap::from([(entry(PRIMARY, "status"), (4, drop_time))]),
        "a drop ack restarts the retry window of a newer pending change"
    );
    assert!(
        !partial_drop
            .effects
            .iter()
            .any(|effect| matches!(effect, ApplyEffect::AttributeSyncDropped { .. })),
        "a drop ack that deletes nothing reports nothing"
    );
}

#[test]
fn purge_removes_entries_and_the_pending_index() {
    let mut harness = Harness::new();
    harness.start(
        Some(&[PRIMARY, REPLICA]),
        vec![synced("status", string_value("opened"))],
    );
    assert_eq!(harness.pending_index_count(), 2);
    harness.apply(Command::FlowStop(FlowStopCommand {
        request: Some(StopFlowRequest {
            stop_type: StopType::Terminate as i32,
            ..StopFlowRequest::default()
        }),
    }));
    let fired = harness.apply(Command::TimersFired(TimersFiredCommand {
        timers: vec![TimerId {
            run_attempt: 0,
            kind: Some(timer_id::Kind::Retention(RetentionTimerId {})),
        }],
    }));
    assert!(
        fired
            .effects
            .iter()
            .any(|effect| matches!(effect, ApplyEffect::RunPurgeRequested { .. })),
        "pending Attribute Sync entries do not postpone retention"
    );
    let purged = harness.apply(Command::RunPurge(RunPurgeCommand {}));
    assert!(is_ok(&purged.reply));
    let run_key_prefix = run_prefix(harness.context.flow_hash, harness.run());
    assert!(
        harness
            .state
            .rows
            .keys()
            .all(|(_, key)| !key.starts_with(&run_key_prefix))
    );
    assert_eq!(harness.pending_index_count(), 0);
}

#[test]
fn time_travel_remarks_rows_of_the_abandoned_and_restored_state() {
    let mut harness = Harness::new();
    harness.start(
        Some(&[PRIMARY]),
        vec![
            synced("status", string_value("opened")),
            unsynced("notes", string_value("local only")),
        ],
    );
    harness.set(
        vec![
            synced("status", string_value("active")),
            synced("plans/basic", string_value("trial")),
            unsynced("draft", string_value("local only")),
        ],
        "set-1",
    );
    harness.ack(PRIMARY, 2, &[], false);
    assert!(harness.entries().is_empty());
    assert!(!harness.has_pending_index(PRIMARY));

    let travel = harness.apply(Command::TimeTravel(TimeTravelCommand {
        request: Some(ResetFlowRequest {
            flow_id: "account-1".to_owned(),
            reset_type: FlowResetType::Beginning as i32,
            reason: "test".to_owned(),
            skip_writes_reapply: true,
            ..ResetFlowRequest::default()
        }),
    }));
    let Some(command_reply::Reply::TimeTravel(reply)) = &travel.reply.reply else {
        panic!("time travel applies: {:?}", travel.reply);
    };
    assert_eq!(reply.run_attempt, 2);
    let travel_time = harness.now_micros;
    assert_eq!(pending_stores(&travel), vec![vec![PRIMARY.to_owned()]]);
    assert_eq!(
        harness.entries(),
        BTreeMap::from([
            (entry(PRIMARY, "plans/basic"), (4, travel_time)),
            (entry(PRIMARY, "status"), (4, travel_time)),
        ]),
        "the restored status and the abandoned plan are rewritten"
    );
    assert!(harness.has_pending_index(PRIMARY));
}
