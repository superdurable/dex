// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Restores FlowConfig fields 2 and 3, which frozen logs hold and current types drop (#1072).

use std::collections::BTreeMap;
use std::path::Path;

use dexzd_keyspace::row_keys::run_singleton_key;
use dexzd_keyspace::{ColumnFamily, RunTable, parse_run_key};
use dexzd_log::decode_batch;
use dexzd_protocol::dex::{ActiveStepSearchMode, FlowConfig, StepDurability};
use dexzd_protocol::dexzd::v1::{OutboxRecord, RunRecord};
use prost::Message;
use prost::encoding::{WireType, decode_key, decode_varint, encode_key, encode_varint};

use crate::old_log_replay::StoredRows;
use crate::older_backend_error::{replace_history_row, replace_row};
use crate::state_dump::{StateDump, hex};
use crate::testdata_tree::{OBJECTS_DIRECTORY, read_files};

/// FlowConfig fields 2 and 3, as the older binaries encoded them.
#[derive(Clone, Copy, PartialEq, Eq, Message)]
pub(crate) struct RemovedFlowConfigFields {
    #[prost(int32, optional, tag = "2")]
    pub(crate) continue_as_new_threshold: Option<i32>,
    #[prost(int32, optional, tag = "3")]
    pub(crate) continue_as_new_page_size_in_bytes: Option<i32>,
}

/// `LogRecord` with only the fields that lead to a logged FlowConfig.
#[derive(Clone, PartialEq, Message)]
struct LoggedRecord {
    #[prost(message, optional, tag = "3")]
    flow: Option<LoggedFlowCommand>,
}

/// `FlowCommand` with only the commands that carry a FlowConfig.
#[derive(Clone, PartialEq, Message)]
struct LoggedFlowCommand {
    #[prost(message, optional, tag = "20")]
    flow_start: Option<LoggedFlowStart>,
    #[prost(message, optional, tag = "22")]
    flow_config_update: Option<LoggedFlowConfigUpdate>,
    #[prost(message, optional, tag = "70")]
    inbox_message: Option<LoggedInboxMessage>,
}

/// `FlowStartCommand`: the new run, the request override, and the effective FlowConfig.
#[derive(Clone, PartialEq, Message)]
struct LoggedFlowStart {
    #[prost(bytes = "vec", tag = "1")]
    new_run_id: Vec<u8>,
    #[prost(message, optional, tag = "2")]
    request: Option<LoggedStartFlowRequest>,
    #[prost(message, optional, tag = "8")]
    effective_flow_config: Option<RemovedFlowConfigFields>,
}

/// `StartFlowRequest` with only its start options.
#[derive(Clone, PartialEq, Message)]
struct LoggedStartFlowRequest {
    #[prost(message, optional, tag = "10")]
    flow_start_options: Option<LoggedFlowStartOptions>,
}

/// `FlowStartOptions` with only its FlowConfig override.
#[derive(Clone, PartialEq, Message)]
struct LoggedFlowStartOptions {
    #[prost(message, optional, tag = "5")]
    flow_config_override: Option<RemovedFlowConfigFields>,
}

/// `FlowConfigUpdateCommand` with only the FlowConfig of its request.
#[derive(Clone, PartialEq, Message)]
struct LoggedFlowConfigUpdate {
    #[prost(message, optional, tag = "1")]
    request: Option<LoggedUpdateFlowConfigRequest>,
}

/// `UpdateFlowConfigRequest` with only its FlowConfig.
#[derive(Clone, PartialEq, Message)]
struct LoggedUpdateFlowConfigRequest {
    #[prost(message, optional, tag = "3")]
    flow_config: Option<RemovedFlowConfigFields>,
}

/// `InboxMessageCommand`: the outbox message ID, a child start, and the child's new run.
#[derive(Clone, PartialEq, Message)]
struct LoggedInboxMessage {
    #[prost(bytes = "vec", tag = "1")]
    message_id: Vec<u8>,
    #[prost(message, optional, tag = "3")]
    child_start: Option<LoggedChildStart>,
    #[prost(bytes = "vec", tag = "5")]
    new_run_id: Vec<u8>,
}

/// `ChildFlowStartMessage` with only its effective FlowConfig.
#[derive(Clone, PartialEq, Message)]
struct LoggedChildStart {
    #[prost(message, optional, tag = "3")]
    flow_config: Option<RemovedFlowConfigFields>,
}

/// FlowConfig field paths of each row table whose older rows held the removed fields.
const RUN_RECORD_PATHS: &[&[u32]] = &[&[14]];
/// `ChildFlowStartMessage.flow_config` of `OutboxRecord.child_start`.
const OUTBOX_PATHS: &[&[u32]] = &[&[3, 3]];
/// FlowConfigs of the logged FlowStart, the logged child start, and the Flow start event.
const HISTORY_PATHS: &[&[u32]] = &[&[6, 20, 8], &[6, 70, 3, 3], &[7, 20, 3]];

/// Removed field values from the log, by run and by child start outbox message.
pub(crate) struct LoggedRemovedFields {
    by_run: BTreeMap<Vec<u8>, RemovedFlowConfigFields>,
    by_outbox_message: BTreeMap<Vec<u8>, RemovedFlowConfigFields>,
}

impl LoggedRemovedFields {
    /// Reads every logged FlowStart and child start; panics when an override or update sets
    /// the fields.
    pub(crate) fn read(directory: &Path) -> Self {
        let mut logged = Self {
            by_run: BTreeMap::new(),
            by_outbox_message: BTreeMap::new(),
        };
        for (key, body) in read_files(&directory.join(OBJECTS_DIRECTORY)) {
            if !key.starts_with("log/") {
                continue;
            }
            let batch = decode_batch(&body)
                .unwrap_or_else(|error| panic!("log batch {key} decodes: {error}"));
            for record in batch.records {
                let command = LoggedRecord::decode(record.bytes.as_ref())
                    .unwrap_or_else(|error| panic!("LogRecord in {key} decodes: {error}"))
                    .flow
                    .unwrap_or_default();
                logged.add(command);
            }
        }
        logged
    }

    /// Returns whether any logged FlowStart or child start holds the removed fields.
    pub(crate) fn holds_removed_fields(&self) -> bool {
        self.by_run
            .values()
            .any(|fields| *fields != RemovedFlowConfigFields::default())
    }

    fn add(&mut self, command: LoggedFlowCommand) {
        if let Some(start) = command.flow_start {
            let override_config = start
                .request
                .and_then(|request| request.flow_start_options)
                .and_then(|options| options.flow_config_override);
            assert_eq!(
                override_config.unwrap_or_default(),
                RemovedFlowConfigFields::default(),
                "no logged FlowStart overrides the removed fields"
            );
            insert_once(
                &mut self.by_run,
                start.new_run_id,
                start.effective_flow_config.unwrap_or_default(),
            );
        }
        if let Some(update) = command.flow_config_update {
            let flow_config = update.request.and_then(|request| request.flow_config);
            assert_eq!(
                flow_config.unwrap_or_default(),
                RemovedFlowConfigFields::default(),
                "no logged FlowConfig update sets the removed fields"
            );
        }
        if let Some(inbox) = command.inbox_message
            && let Some(child_start) = inbox.child_start
        {
            let fields = child_start.flow_config.unwrap_or_default();
            insert_once(&mut self.by_run, inbox.new_run_id, fields);
            insert_once(&mut self.by_outbox_message, inbox.message_id, fields);
        }
    }

    /// Returns the replay with the logged removed fields back in its run records, history rows,
    /// and child start outbox rows, and with the history row metadata, history bytes, and
    /// state row bytes that count them.
    ///
    /// Panics when a run or a child start outbox message has no logged command.
    pub(crate) fn restore(&self, replayed: &mut StateDump, stored: &mut StoredRows) {
        let flow_name = ColumnFamily::Flow.name().to_owned();
        let history_name = ColumnFamily::History.name().to_owned();
        let mut deltas: BTreeMap<Vec<u8>, OlderRunBytes> = BTreeMap::new();
        let rows = stored.clone();
        for ((column_family, key), value) in rows {
            let Ok(parsed) = parse_run_key(&key) else {
                continue;
            };
            let record_key =
                run_singleton_key(parsed.flow_hash, parsed.run_id, RunTable::RunRecord);
            let run_fields = || {
                *self
                    .by_run
                    .get(parsed.run_id.0.as_slice())
                    .unwrap_or_else(|| panic!("run {} has a logged start", hex(&parsed.run_id.0)))
            };
            match (column_family == flow_name, parsed.table) {
                (false, RunTable::HistoryRecord) if column_family == history_name => {
                    let older = with_fields(&value, HISTORY_PATHS, run_fields());
                    if older != value {
                        let seq = u64::from_be_bytes(
                            parsed
                                .suffix
                                .try_into()
                                .expect("history record keys end with an eight-byte seq"),
                        );
                        let run_deltas = deltas.entry(record_key).or_default();
                        run_deltas
                            .history_rows
                            .push((seq, older.len() as i128 - value.len() as i128));
                        run_deltas.history +=
                            replace_history_row(replayed, stored, key, &value, older);
                    }
                }
                (true, RunTable::Outbox) => {
                    let outbox =
                        OutboxRecord::decode(value.as_slice()).expect("outbox rows decode");
                    let fields = self.by_outbox_message.get(&outbox.message_id).copied();
                    let older = match fields {
                        Some(fields) => with_fields(&value, OUTBOX_PATHS, fields),
                        None => value.clone(),
                    };
                    if older != value {
                        deltas.entry(record_key).or_default().other_state +=
                            older.len() as i128 - value.len() as i128;
                        replace_row(replayed, stored, &flow_name, key, older);
                    } else if fields.is_none() {
                        assert!(
                            removed_fields_in(RunTable::Outbox, &value).is_empty(),
                            "the child start of outbox message {} is logged",
                            hex(&outbox.message_id)
                        );
                    }
                }
                _ => {}
            }
        }
        let run_record_keys: Vec<Vec<u8>> = stored
            .keys()
            .filter(|(column_family, key)| {
                *column_family == flow_name
                    && parse_run_key(key).is_ok_and(|parsed| parsed.table == RunTable::RunRecord)
            })
            .map(|(_, key)| key.clone())
            .collect();
        for record_key in run_record_keys {
            let parsed = parse_run_key(&record_key).expect("run record keys parse");
            let fields = *self
                .by_run
                .get(parsed.run_id.0.as_slice())
                .unwrap_or_else(|| panic!("run {} has a logged start", hex(&parsed.run_id.0)));
            let value = &stored[&(flow_name.clone(), record_key.clone())];
            let run_deltas = deltas.remove(&record_key).unwrap_or_default();
            let older = run_record_with_fields(value, fields, &run_deltas);
            if older != *value {
                replace_row(replayed, stored, &flow_name, record_key, older);
            }
        }
    }
}

fn insert_once(
    map: &mut BTreeMap<Vec<u8>, RemovedFlowConfigFields>,
    key: Vec<u8>,
    fields: RemovedFlowConfigFields,
) {
    if let Some(earlier) = map.insert(key.clone(), fields) {
        assert_eq!(
            earlier,
            fields,
            "logged commands give {} different removed fields",
            hex(&key)
        );
    }
}

/// How many more bytes a run's older rows count than its replayed ones.
#[derive(Default)]
struct OlderRunBytes {
    /// History rows and their metadata rows.
    history: i128,
    /// Flow rows other than the run record.
    other_state: i128,
    /// Each restored history row's seq and size change.
    history_rows: Vec<(u64, i128)>,
}

/// Returns the run record with `fields` in its FlowConfig, and its history bytes, open history
/// segment bytes, and state row bytes moved by `deltas` and by its own new size.
///
/// Panics when the run record does not encode back to the same bytes, apart from the removed
/// FlowConfig fields, so that unknown or noncanonical fields cannot hide behind the rewrite.
fn run_record_with_fields(
    value: &[u8],
    fields: RemovedFlowConfigFields,
    deltas: &OlderRunBytes,
) -> Vec<u8> {
    let mut record = RunRecord::decode(value).expect("run records decode");
    assert_eq!(
        with_kept_removed_fields(RunTable::RunRecord, value, &record.encode_to_vec()),
        value,
        "a replayed run record holds bytes that decoding drops or rewrites"
    );
    let mut usage = record.storage.expect("run records count storage");
    usage.history_bytes = u64::try_from(i128::from(usage.history_bytes) + deltas.history)
        .expect("older history bytes are not negative");
    // The open history segment counts the history rows that its commands wrote.
    if record.history_open_segment_first_seq != 0 {
        let open_segment_delta: i128 = deltas
            .history_rows
            .iter()
            .filter(|(seq, _)| *seq >= record.history_open_segment_first_seq)
            .map(|(_, delta)| delta)
            .sum();
        record.history_open_segment_bytes =
            u64::try_from(i128::from(record.history_open_segment_bytes) + open_segment_delta)
                .expect("older open segment bytes are not negative");
    }
    let other_state_bytes =
        i128::from(usage.state_row_bytes) - value.len() as i128 + deltas.other_state;
    let other_state_bytes =
        u64::try_from(other_state_bytes).expect("older state row bytes are not negative");
    // The record's own row counts in its state row bytes; repeat until its size settles.
    for _ in 0..4 {
        record.storage = Some(usage);
        let older = with_fields(&record.encode_to_vec(), RUN_RECORD_PATHS, fields);
        if usage.state_row_bytes == other_state_bytes + older.len() as u64 {
            return older;
        }
        usage.state_row_bytes = other_state_bytes + older.len() as u64;
    }
    panic!("the run record size does not settle")
}

/// Returns `reencoded` with the removed fields that `original` holds at the same FlowConfigs,
/// so that decoding and encoding a row of `table` with the current types keeps them.
///
/// Panics when the two rows hold different numbers of those FlowConfigs.
pub(crate) fn with_kept_removed_fields(
    table: RunTable,
    original: &[u8],
    reencoded: &[u8],
) -> Vec<u8> {
    let mut output = reencoded.to_vec();
    for path in paths_of(table) {
        let mut kept = removed_fields_at(original, path).into_iter();
        output = rewrite_flow_configs(&output, path, &mut |flow_config| {
            let fields = kept
                .next()
                .expect("the reencoded row holds no more FlowConfigs than the original");
            with_removed_fields(flow_config, fields)
        });
        assert!(
            kept.next().is_none(),
            "the reencoded row holds as many FlowConfigs as the original"
        );
    }
    output
}

/// Returns the removed fields of every FlowConfig that a row of `table` holds where older rows
/// held them.
pub(crate) fn removed_fields_in(table: RunTable, value: &[u8]) -> Vec<RemovedFlowConfigFields> {
    paths_of(table)
        .iter()
        .flat_map(|path| removed_fields_at(value, path))
        .collect()
}

/// Returns `value` with `fields` in each FlowConfig at `paths`.
pub(crate) fn with_fields(
    value: &[u8],
    paths: &[&[u32]],
    fields: RemovedFlowConfigFields,
) -> Vec<u8> {
    paths.iter().fold(value.to_vec(), |output, path| {
        rewrite_flow_configs(&output, path, &mut |flow_config| {
            with_removed_fields(flow_config, fields)
        })
    })
}

/// The run record's FlowConfig path.
pub(crate) fn run_record_paths() -> &'static [&'static [u32]] {
    RUN_RECORD_PATHS
}

/// The FlowConfig paths of a history row.
pub(crate) fn history_paths() -> &'static [&'static [u32]] {
    HISTORY_PATHS
}

fn paths_of(table: RunTable) -> &'static [&'static [u32]] {
    match table {
        RunTable::RunRecord => RUN_RECORD_PATHS,
        RunTable::Outbox => OUTBOX_PATHS,
        RunTable::HistoryRecord => HISTORY_PATHS,
        _ => &[],
    }
}

fn removed_fields_at(value: &[u8], path: &[u32]) -> Vec<RemovedFlowConfigFields> {
    let mut found = Vec::new();
    rewrite_flow_configs(value, path, &mut |flow_config| {
        found.push(RemovedFlowConfigFields::decode(flow_config).expect("FlowConfigs decode"));
        flow_config.to_vec()
    });
    found
}

/// Returns the FlowConfig with fields 2 and 3 replaced by `fields`, in field number order as
/// prost encodes them.
fn with_removed_fields(flow_config: &[u8], fields: RemovedFlowConfigFields) -> Vec<u8> {
    let encoded_fields = fields.encode_to_vec();
    let mut output = Vec::with_capacity(flow_config.len() + encoded_fields.len());
    let mut is_inserted = false;
    for field in wire_fields(flow_config) {
        if field.number == 2 || field.number == 3 {
            continue;
        }
        if !is_inserted && field.number > 3 {
            output.extend_from_slice(&encoded_fields);
            is_inserted = true;
        }
        output.extend_from_slice(field.raw);
    }
    if !is_inserted {
        output.extend_from_slice(&encoded_fields);
    }
    output
}

/// Returns `message` with each length-delimited field at `path` rewritten, and the length
/// prefixes of the fields that hold it encoded anew.
fn rewrite_flow_configs(
    message: &[u8],
    path: &[u32],
    rewrite: &mut dyn FnMut(&[u8]) -> Vec<u8>,
) -> Vec<u8> {
    let (&number, rest) = path.split_first().expect("FlowConfig paths are not empty");
    let mut output = Vec::with_capacity(message.len());
    for field in wire_fields(message) {
        let Some(payload) = field.payload.filter(|_| field.number == number) else {
            output.extend_from_slice(field.raw);
            continue;
        };
        let rewritten = if rest.is_empty() {
            rewrite(payload)
        } else {
            rewrite_flow_configs(payload, rest, rewrite)
        };
        encode_key(number, WireType::LengthDelimited, &mut output);
        encode_varint(rewritten.len() as u64, &mut output);
        output.extend_from_slice(&rewritten);
    }
    output
}

/// One encoded field: its number, its bytes with the key, and a length-delimited payload.
struct WireField<'bytes> {
    number: u32,
    raw: &'bytes [u8],
    payload: Option<&'bytes [u8]>,
}

/// Splits an encoded message into its fields.
///
/// Panics on malformed bytes or a group, which no Dex message uses.
fn wire_fields(message: &[u8]) -> Vec<WireField<'_>> {
    let mut fields = Vec::new();
    let mut rest = message;
    while !rest.is_empty() {
        let start = rest;
        let (number, wire_type) = decode_key(&mut rest).expect("field keys decode");
        let mut payload = None;
        match wire_type {
            WireType::Varint => {
                decode_varint(&mut rest).expect("varints decode");
            }
            WireType::SixtyFourBit => rest = &rest[8..],
            WireType::ThirtyTwoBit => rest = &rest[4..],
            WireType::LengthDelimited => {
                let length = decode_varint(&mut rest).expect("lengths decode");
                let length = usize::try_from(length).expect("lengths fit in memory");
                payload = Some(&rest[..length]);
                rest = &rest[length..];
            }
            WireType::StartGroup | WireType::EndGroup => panic!("Dex messages hold no groups"),
        }
        fields.push(WireField {
            number,
            raw: &start[..start.len() - rest.len()],
            payload,
        });
    }
    fields
}

#[test]
fn the_removed_fields_go_between_fields_1_and_4() {
    let fields = RemovedFlowConfigFields {
        continue_as_new_threshold: Some(100),
        continue_as_new_page_size_in_bytes: Some(7),
    };
    let head = FlowConfig {
        active_step_search_mode: Some(ActiveStepSearchMode::Disabled as i32),
        ..FlowConfig::default()
    };
    let tail = FlowConfig {
        step_durability: Some(StepDurability::Sync as i32),
        history_window_events: Some(8),
        ..FlowConfig::default()
    };
    let current = FlowConfig {
        step_durability: tail.step_durability,
        history_window_events: tail.history_window_events,
        ..head.clone()
    };
    let older = [
        head.encode_to_vec(),
        fields.encode_to_vec(),
        tail.encode_to_vec(),
    ]
    .concat();
    let run = RunRecord {
        flow_id: "older-run".to_owned(),
        flow_config: Some(current),
        ..RunRecord::default()
    }
    .encode_to_vec();
    let restored = with_fields(&run, RUN_RECORD_PATHS, fields);
    assert_eq!(
        removed_fields_in(RunTable::RunRecord, &restored),
        vec![fields]
    );
    assert_eq!(
        with_fields(&restored, RUN_RECORD_PATHS, fields),
        restored,
        "restoring twice changes nothing"
    );
    let older_run = RunRecord::decode(restored.as_slice()).expect("decodes");
    assert_eq!(older_run.encode_to_vec(), run);
    assert_eq!(
        with_kept_removed_fields(RunTable::RunRecord, &restored, &run),
        restored
    );
    let flow_config = removed_flow_config_bytes(&restored);
    assert_eq!(flow_config, older);
}

#[test]
#[should_panic(expected = "decoding drops or rewrites")]
fn the_run_record_restore_rejects_an_unknown_field() {
    let fields = RemovedFlowConfigFields {
        continue_as_new_threshold: Some(100),
        continue_as_new_page_size_in_bytes: None,
    };
    let mut value = RunRecord {
        flow_id: "unknown-field-run".to_owned(),
        flow_config: Some(FlowConfig::default()),
        storage: Some(dexzd_protocol::dexzd::v1::RunStorageUsage::default()),
        ..RunRecord::default()
    }
    .encode_to_vec();
    // Field 78 as a varint: RunRecord has no such field, so decoding drops it.
    value.extend_from_slice(&[0xf0, 0x04, 0x01]);
    run_record_with_fields(&value, fields, &OlderRunBytes::default());
}

#[test]
fn the_run_record_restore_keeps_a_canonical_record() {
    let fields = RemovedFlowConfigFields {
        continue_as_new_threshold: Some(100),
        continue_as_new_page_size_in_bytes: None,
    };
    let value = RunRecord {
        flow_id: "canonical-run".to_owned(),
        flow_config: Some(FlowConfig::default()),
        storage: Some(dexzd_protocol::dexzd::v1::RunStorageUsage {
            state_row_bytes: 1_000,
            ..dexzd_protocol::dexzd::v1::RunStorageUsage::default()
        }),
        ..RunRecord::default()
    }
    .encode_to_vec();
    let older = run_record_with_fields(&value, fields, &OlderRunBytes::default());
    assert_eq!(removed_fields_in(RunTable::RunRecord, &older), vec![fields]);
    let usage = RunRecord::decode(older.as_slice())
        .expect("decodes")
        .storage
        .expect("storage");
    assert_eq!(
        usage.state_row_bytes,
        1_000 - value.len() as u64 + older.len() as u64,
        "the state row bytes count the record's own older size"
    );
}

fn removed_flow_config_bytes(run: &[u8]) -> Vec<u8> {
    let mut found = Vec::new();
    rewrite_flow_configs(run, &[14], &mut |flow_config| {
        found = flow_config.to_vec();
        flow_config.to_vec()
    });
    found
}
