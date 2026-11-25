// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//! Process records in batches that two control Attributes steer.

use std::sync::LazyLock;
use std::time::Duration;

use dex_sdk::{
    Attribute, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Rpc, RpcList, Step,
    StepDecision, StepList, Timer, Wait,
};
use serde::{Deserialize, Serialize};

pub const TOTAL_RECORDS: i64 = 1000;
pub const DEFAULT_BATCH_SIZE: i64 = 10;
pub const MAX_BATCH_SIZE: i64 = 1000;
pub const BATCH_INTERVAL: Duration = Duration::from_secs(1);

pub const SET_BATCH_SIZE: Rpc<i64, ()> = Rpc::new("set_batch_size");
pub const REQUEST_STOP: Rpc<(), ()> = Rpc::new("request_stop");

pub static BATCH_SIZE: LazyLock<Attribute<i64>> = LazyLock::new(|| Attribute::new("batchSize"));
pub static STOP_REQUESTED: LazyLock<Attribute<bool>> =
    LazyLock::new(|| Attribute::new("stopRequested"));

/// The range of records that the loop has not processed yet.
#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct BackfillProgress {
    pub next_record: i64,
    pub last_record: i64,
}

/// Processes records in batches that two control Attributes steer.
#[derive(Default)]
pub struct RecordBackfillFlow {
    start_record_backfill: StartRecordBackfill,
    process_record_batch: ProcessRecordBatch,
}

impl RecordBackfillFlow {
    /// Changes how many records each later batch processes.
    fn set_batch_size(&self, context: &mut Context, size: i64) -> HandlerResult<()> {
        if !(1..=MAX_BATCH_SIZE).contains(&size) {
            return Err(HandlerError::new(
                "RecordBackfillFlow",
                format!("batch size must be between 1 and {MAX_BATCH_SIZE}"),
            ));
        }
        BATCH_SIZE.set(context, size)
    }

    /// Asks the loop to stop before its next batch.
    fn request_stop(&self, context: &mut Context) -> HandlerResult<()> {
        STOP_REQUESTED.set(context, true)
    }
}

impl Flow for RecordBackfillFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_record_backfill).and(&self.process_record_batch)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&BATCH_SIZE)
            .attribute(&STOP_REQUESTED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(SET_BATCH_SIZE, Self::set_batch_size)
            .procedure_without_input(REQUEST_STOP, Self::request_stop)
    }
}

// dex:group group-id:start group-label:"Start"
// dex:explanation text:"Start the backfill at the first record."
#[derive(Default)]
pub struct StartRecordBackfill;

impl Step for StartRecordBackfill {
    type Input = ();

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &ProcessRecordBatch,
            BackfillProgress {
                next_record: 1,
                last_record: TOTAL_RECORDS,
            },
        ))
    }
}

// dex:group group-id:batches group-label:"Batches"
// dex:explanation text:"Read the controls, then process one batch of records or stop."
#[derive(Default)]
pub struct ProcessRecordBatch;

impl Step for ProcessRecordBatch {
    type Input = BackfillProgress;

    fn wait_for(&self, _context: &mut Context, _progress: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(BATCH_INTERVAL)))
    }

    fn execute(&self, context: &mut Context, progress: Self::Input) -> HandlerResult<StepDecision> {
        if STOP_REQUESTED.get(context)? == Some(true) {
            return Ok(StepDecision::graceful_complete(format!(
                "stopped before record {}",
                progress.next_record
            )));
        }
        let size = BATCH_SIZE.get(context)?.unwrap_or(DEFAULT_BATCH_SIZE);
        let last = (progress.next_record + size - 1).min(progress.last_record);
        println!("processing records {} to {last}", progress.next_record);
        if last == progress.last_record {
            return Ok(StepDecision::graceful_complete(format!(
                "processed {last} records"
            )));
        }
        Ok(StepDecision::go_to(
            &ProcessRecordBatch,
            BackfillProgress {
                next_record: last + 1,
                last_record: progress.last_record,
            },
        ))
    }
}
