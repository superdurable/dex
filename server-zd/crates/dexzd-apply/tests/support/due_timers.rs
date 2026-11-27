// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Stamps a test TimersFired command no earlier than its timers' fire times, as the timer
//! service does.

use std::collections::BTreeMap;

use dexzd_keyspace::row_keys::timer_key;
use dexzd_keyspace::{ColumnFamily, FlowHash, RunId};
use dexzd_protocol::dexzd::v1::TimerRecord;
use dexzd_protocol::dexzd::v1::flow_command::Command;
use prost::Message;

/// Returns the time to stamp on a command with `body` for the run `run_id`.
///
/// Returns `now_micros` unless `body` is a TimersFired command. For TimersFired, returns the
/// later of `now_micros` and the latest fire time stored for its timers. Timers without a
/// stored row do not move the time.
pub(crate) fn command_time_when_timers_are_due(
    rows: &BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>>,
    flow_hash: FlowHash,
    run_id: &[u8],
    body: &Command,
    now_micros: i64,
) -> i64 {
    let Command::TimersFired(fired) = body else {
        return now_micros;
    };
    let Ok(run_id) = RunId::from_slice(run_id) else {
        return now_micros;
    };
    fired
        .timers
        .iter()
        .filter_map(|timer| {
            rows.get(&(
                ColumnFamily::Flow,
                timer_key(flow_hash, run_id, &timer.encode_to_vec()),
            ))
        })
        .map(|bytes| {
            TimerRecord::decode(bytes.as_slice())
                .expect("stored timer row decodes")
                .fire_at_micros
        })
        .fold(now_micros, i64::max)
}
