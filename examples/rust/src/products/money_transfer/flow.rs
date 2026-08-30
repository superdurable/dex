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

/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

use std::time::Duration;

use dex_sdk::{
    Context, Flow, HandlerResult, RetryPolicy, Step, StepDecision, StepList, StepOptions,
};
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct TransferRequest {
    pub from_account: String,
    pub to_account: String,
    pub amount_cents: i64,
    pub notes: String,
}

#[derive(Default)]
pub struct MoneyTransferFlow {
    check_balance: CheckBalance,
    create_debit_memo: CreateDebitMemo,
    debit: Debit,
    create_credit_memo: CreateCreditMemo,
    credit: Credit,
    compensate: Compensate,
}

impl Flow for MoneyTransferFlow {
    type StartInput = TransferRequest;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.check_balance)
            .and(&self.create_debit_memo)
            .and(&self.debit)
            .and(&self.create_credit_memo)
            .and(&self.credit)
            .and(&self.compensate)
    }
}

#[derive(Default)]
struct CheckBalance;

impl Step for CheckBalance {
    type Input = TransferRequest;

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        if input.amount_cents <= 0 {
            return Ok(StepDecision::force_fail("transfer amount must be positive"));
        }
        Ok(StepDecision::go_to(&CreateDebitMemo, input))
    }
}

#[derive(Default)]
struct CreateDebitMemo;

impl Step for CreateDebitMemo {
    type Input = TransferRequest;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event(
            "debit-memo",
            format!("{}:{}", input.from_account, input.amount_cents),
        )?;
        Ok(StepDecision::go_to(&Debit, input))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        compensated_step_options()
    }
}

#[derive(Default)]
struct Debit;

impl Step for Debit {
    type Input = TransferRequest;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event(
            "debit",
            format!("{}:{}", input.from_account, input.amount_cents),
        )?;
        Ok(StepDecision::go_to(&CreateCreditMemo, input))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        compensated_step_options()
    }
}

#[derive(Default)]
struct CreateCreditMemo;

impl Step for CreateCreditMemo {
    type Input = TransferRequest;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event(
            "credit-memo",
            format!("{}:{}", input.to_account, input.amount_cents),
        )?;
        Ok(StepDecision::go_to(&Credit, input))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        compensated_step_options()
    }
}

#[derive(Default)]
struct Credit;

impl Step for Credit {
    type Input = TransferRequest;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event(
            "credit",
            format!("{}:{}", input.to_account, input.amount_cents),
        )?;
        Ok(StepDecision::graceful_complete(input))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        compensated_step_options()
    }
}

#[derive(Default)]
struct Compensate;

impl Step for Compensate {
    type Input = TransferRequest;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event("undo-credit", input.amount_cents)?;
        context.record_event("undo-credit-memo", input.amount_cents)?;
        context.record_event("undo-debit", input.amount_cents)?;
        context.record_event("undo-debit-memo", input.amount_cents)?;
        Ok(StepDecision::force_fail(
            "transfer failed; effects compensated",
        ))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_retry(transfer_retry())
    }
}

fn compensated_step_options() -> StepOptions<TransferRequest> {
    StepOptions::new()
        .execute_retry(transfer_retry())
        .on_execute_failure_proceed_to(&Compensate)
}

fn transfer_retry() -> RetryPolicy {
    RetryPolicy::new()
        .initial_interval(Duration::from_secs(1))
        .maximum_attempts(3)
}
