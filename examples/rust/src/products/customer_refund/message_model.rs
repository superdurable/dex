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

//! A deterministic stand-in for a language model that drafts customer messages.

use super::billing_service::format_usd;
use super::refund_flow::RefundOutcome;

/// Answers like a model would, without an API key or a network call.
#[derive(Default)]
pub struct MockMessageModel;

impl MockMessageModel {
    pub fn write_message(
        &self,
        customer: &str,
        outcome: RefundOutcome,
        amount_cents: u64,
        note: &str,
    ) -> String {
        let message = match outcome {
            RefundOutcome::Refunded => format!(
                "Hi {customer}, we are sorry for the service interruption. \
                 We refunded {} USD to your card. {note}",
                format_usd(amount_cents)
            ),
            RefundOutcome::Denied => format!(
                "Hi {customer}, thank you for reaching out. We cannot refund this charge. {note}"
            ),
        };
        message.trim().to_string()
    }
}
