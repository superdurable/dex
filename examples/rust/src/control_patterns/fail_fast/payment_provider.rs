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

use std::collections::HashMap;
use std::error::Error;
use std::fmt::{Display, Formatter};
use std::sync::Mutex;

use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ExpenseClaim {
    pub amount_cents: i64,
    pub bank_account: String,
}

#[derive(Debug)]
pub enum PayoutError {
    /// The provider confirmed that the request can never succeed.
    Rejected { reason: String },
    /// The provider did not answer in time; the same request may succeed later.
    Timeout,
}

impl Display for PayoutError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Rejected { reason } => formatter.write_str(reason),
            Self::Timeout => formatter.write_str("payment provider timed out"),
        }
    }
}

impl Error for PayoutError {}

/// FakePaymentProvider stands in for a payment provider API.
/// It accepts only a bank account of eight ASCII digits and rejects any other at once.
/// It times out on the first submission of every valid claim, then pays it.
#[derive(Default)]
pub struct FakePaymentProvider {
    submissions: Mutex<HashMap<String, u32>>,
}

impl FakePaymentProvider {
    /// Pays a claim once per claim ID, so a retried Step does not pay twice.
    pub fn submit_payout(
        &self,
        claim_id: &str,
        claim: &ExpenseClaim,
    ) -> Result<String, PayoutError> {
        let submissions = {
            let mut submissions = self
                .submissions
                .lock()
                .expect("fake payment provider lock is poisoned");
            let count = submissions.entry(claim_id.to_string()).or_default();
            *count += 1;
            *count
        };
        let is_eight_digits = claim.bank_account.len() == 8
            && claim
                .bank_account
                .chars()
                .all(|character| character.is_ascii_digit());
        if !is_eight_digits {
            return Err(PayoutError::Rejected {
                reason: "bank account is invalid".to_string(),
            });
        }
        if submissions == 1 {
            return Err(PayoutError::Timeout);
        }
        Ok(format!("payout-{claim_id}"))
    }
}
