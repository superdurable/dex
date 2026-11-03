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

//! An RPC that cancels a running Step: a user stops a report while it generates.

use std::sync::LazyLock;
use std::thread;
use std::time::Duration;

use dex_sdk::{
    Attribute, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult, Step,
    StepDecision, StepList, StepMovement, StepOptions, Stream,
};

pub const STOP_REPORT_RPC: Rpc<(), bool> = Rpc::new("StopReport");
pub const REPORT_READY: &str = "ready";
pub const REPORT_STOPPED: &str = "stopped";
const SECTION_COUNT: i32 = 10;
const SECTION_WORK: Duration = Duration::from_secs(1);

static REPORT_STATUS: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("ReportStatus"));
pub static REPORT_PROGRESS: LazyLock<Stream<i32>> =
    LazyLock::new(|| Stream::new("ReportProgress", 1 << 20));

#[derive(Default)]
pub struct ReportGenerationFlow {
    generate_report: GenerateReport,
    deliver_report: DeliverReport,
    finish_report_stop: FinishReportStop,
}

impl ReportGenerationFlow {
    fn stop_report(&self, context: &mut Context, _input: ()) -> HandlerResult<RpcResult<bool>> {
        let status = REPORT_STATUS.get(context)?;
        if matches!(status.as_deref(), Some(REPORT_READY | REPORT_STOPPED)) {
            return Ok(RpcResult::new(false));
        }
        Ok(RpcResult::new(true)
            .then(StepMovement::to(&self.finish_report_stop, ()))
            .cancel_step(&self.generate_report))
    }
}

impl Flow for ReportGenerationFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.generate_report)
            .and(&self.deliver_report)
            .and(&self.finish_report_stop)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&REPORT_STATUS)
            .stream(&REPORT_PROGRESS)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(
            STOP_REPORT_RPC.timeout(Duration::from_secs(30)),
            Self::stop_report,
        )
    }
}

#[derive(Default)]
struct GenerateReport;

impl Step for GenerateReport {
    type Input = String;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_method_timeout(Duration::from_secs(5 * 60))
            .heartbeat_timeout(Duration::from_secs(30))
    }

    fn execute(
        &self,
        context: &mut Context,
        report_name: Self::Input,
    ) -> HandlerResult<StepDecision> {
        for section in 1..=SECTION_COUNT {
            // A Stop cancels this attempt, and Dex discards its result.
            if context.is_cancelled() {
                return Ok(StepDecision::dead_end());
            }
            thread::sleep(SECTION_WORK);
            REPORT_PROGRESS.write(context, section)?;
        }
        REPORT_STATUS.set(context, REPORT_READY.to_string())?;
        Ok(StepDecision::go_to(&DeliverReport, report_name))
    }
}

#[derive(Default)]
struct DeliverReport;

impl Step for DeliverReport {
    type Input = String;

    fn execute(
        &self,
        _context: &mut Context,
        report_name: Self::Input,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(format!(
            "{report_name} delivered"
        )))
    }
}

#[derive(Default)]
struct FinishReportStop;

impl Step for FinishReportStop {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        // The report can commit after stop_report read its snapshot.
        if REPORT_STATUS.get(context)?.as_deref() == Some(REPORT_READY) {
            return Ok(StepDecision::dead_end());
        }
        REPORT_STATUS.set(context, REPORT_STOPPED.to_string())?;
        Ok(StepDecision::graceful_complete(REPORT_STOPPED.to_string()))
    }
}
