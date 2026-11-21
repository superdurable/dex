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

//! The refund requests that `customer-refund start FLOW_ID SCENARIO` can start.

use std::fmt::{self, Display, Formatter};
use std::str::FromStr;

use super::refund_flow::RefundRequest;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum RefundScenario {
    /// INV-1249, 1,249.00 USD: waits for a manager, then refunds.
    #[default]
    Normal,
    /// INV-1249: after approval, the refund fails twice, then succeeds.
    FailThenSucceed,
    /// INV-0388, charged 47 days ago: the policy denies it.
    Deny,
    /// INV-0049, 49.00 USD: refunds without a manager.
    Small,
}

impl RefundScenario {
    pub fn refund_request(self) -> RefundRequest {
        let northstar_renewal = RefundRequest {
            customer: "Northstar Studio".to_string(),
            customer_email: "billing@northstar.example".to_string(),
            order_id: "INV-1249".to_string(),
            reason: "Our workspace was down for 31 hours after the renewal.".to_string(),
            payment_timeouts_to_simulate: 0,
        };
        match self {
            Self::Normal => northstar_renewal,
            Self::FailThenSucceed => RefundRequest {
                payment_timeouts_to_simulate: 2,
                ..northstar_renewal
            },
            Self::Deny => RefundRequest {
                order_id: "INV-0388".to_string(),
                ..northstar_renewal
            },
            Self::Small => RefundRequest {
                order_id: "INV-0049".to_string(),
                ..northstar_renewal
            },
        }
    }
}

impl Display for RefundScenario {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Normal => "normal",
            Self::FailThenSucceed => "fail-then-succeed",
            Self::Deny => "deny",
            Self::Small => "small",
        })
    }
}

impl FromStr for RefundScenario {
    type Err = String;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        match value {
            "normal" => Ok(Self::Normal),
            "fail-then-succeed" => Ok(Self::FailThenSucceed),
            "deny" => Ok(Self::Deny),
            "small" => Ok(Self::Small),
            other => Err(format!(
                "unknown scenario {other:?}; choose normal, fail-then-succeed, deny, or small"
            )),
        }
    }
}
