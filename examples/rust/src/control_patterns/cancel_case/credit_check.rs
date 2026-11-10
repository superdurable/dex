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

use std::time::Duration;

use dex_sdk::{
    Context, Flow, HandlerResult, PersistenceSchema, Step, StepDecision, StepList, Timer, Wait,
};
use serde::{Deserialize, Serialize};

/// The input of one credit check child Flow.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CreditCheckRequest {
    pub applicant_id: String,
    /// Simulates how long the credit bureau takes to answer.
    pub report_delay_seconds: u64,
}

/// The child Flow that `LoanApplicationFlow` starts.
#[derive(Default)]
pub struct CreditCheckFlow {
    await_credit_report: AwaitCreditReport,
}

impl Flow for CreditCheckFlow {
    type StartInput = CreditCheckRequest;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.await_credit_report)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
    }
}

/// Waits for the simulated credit bureau to answer.
#[derive(Default)]
struct AwaitCreditReport;

impl Step for AwaitCreditReport {
    type Input = CreditCheckRequest;

    fn wait_for(&self, _context: &mut Context, request: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(
            request.report_delay_seconds,
        ))))
    }

    fn execute(&self, _context: &mut Context, request: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(format!(
            "credit report for {}",
            request.applicant_id
        )))
    }
}
