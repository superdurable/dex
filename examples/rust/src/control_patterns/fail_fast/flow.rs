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

use std::sync::{Arc, LazyLock};
use std::time::Duration;

use dex_sdk::{
    Attribute, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, RetryPolicy, Step,
    StepDecision, StepList, StepOptions,
};
use serde::{Deserialize, Serialize};

use crate::control_patterns::fail_fast::payment_provider::{
    ExpenseClaim, FakePaymentProvider, PayoutError,
};

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ClaimRejection {
    pub reason: String,
    pub submit_attempt: u32,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PayoutOutcome {
    pub status: String,
    pub detail: String,
    pub submit_attempt: u32,
}

pub static PAYOUT_OUTCOME: LazyLock<Attribute<PayoutOutcome>> =
    LazyLock::new(|| Attribute::new("PayoutOutcome"));

// dex:group group-id:payout group-label:"Payout"
// dex:explanation text:"Submit the payout; retry a timeout and route a rejection at once."
pub struct SubmitPayout {
    provider: Arc<FakePaymentProvider>,
}

impl Step for SubmitPayout {
    type Input = ExpenseClaim;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_retry(
            RetryPolicy::new()
                .initial_interval(Duration::from_secs(1))
                .backoff_coefficient(2.0)
                .maximum_interval(Duration::from_secs(5 * 60))
                .total_duration(Duration::from_secs(60 * 60)),
        )
    }

    fn execute(&self, context: &mut Context, claim: Self::Input) -> HandlerResult<StepDecision> {
        let payout_id = match self.provider.submit_payout(context.flow_id(), &claim) {
            Ok(payout_id) => payout_id,
            Err(PayoutError::Rejected { reason }) => {
                let rejection = ClaimRejection {
                    reason,
                    submit_attempt: context.attempt(),
                };
                return Ok(StepDecision::go_to(&RecordRejectedClaim, rejection));
            }
            Err(error @ PayoutError::Timeout) => return Err(HandlerError::from_error(error)),
        };
        let outcome = PayoutOutcome {
            status: "PAID".to_string(),
            detail: payout_id.clone(),
            submit_attempt: context.attempt(),
        };
        PAYOUT_OUTCOME.set(context, outcome)?;
        Ok(StepDecision::graceful_complete(payout_id))
    }
}

// dex:group group-id:rejection group-label:"Rejection"
// dex:explanation text:"Record the rejected claim and its reason."
pub struct RecordRejectedClaim;

impl Step for RecordRejectedClaim {
    type Input = ClaimRejection;

    fn execute(
        &self,
        context: &mut Context,
        rejection: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let outcome = PayoutOutcome {
            status: "REJECTED".to_string(),
            detail: rejection.reason.clone(),
            submit_attempt: rejection.submit_attempt,
        };
        PAYOUT_OUTCOME.set(context, outcome)?;
        Ok(StepDecision::graceful_complete(format!(
            "rejected: {}",
            rejection.reason
        )))
    }
}

pub struct ExpenseReimbursementFlow {
    submit_payout: SubmitPayout,
    record_rejected_claim: RecordRejectedClaim,
}

impl ExpenseReimbursementFlow {
    pub fn new(provider: Arc<FakePaymentProvider>) -> Self {
        Self {
            submit_payout: SubmitPayout { provider },
            record_rejected_claim: RecordRejectedClaim,
        }
    }
}

impl Default for ExpenseReimbursementFlow {
    fn default() -> Self {
        Self::new(Arc::new(FakePaymentProvider::default()))
    }
}

impl Flow for ExpenseReimbursementFlow {
    type StartInput = ExpenseClaim;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.submit_payout).and(&self.record_rejected_claim)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&PAYOUT_OUTCOME)
    }
}
