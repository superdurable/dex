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
    Attribute, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, RetryPolicy, Rpc,
    RpcList, RpcResult, Step, StepDecision, StepList, StepMovement, StepOptions,
};
use serde::{Deserialize, Serialize};

use crate::data_storage::entity_flow::account_archive::FakeAccountArchive;

pub const UPDATE_CONTACT_DETAILS: Rpc<ContactDetails, ()> = Rpc::new("UpdateContactDetails");
pub const ADJUST_CREDIT_BALANCE: Rpc<CreditAdjustment, i64> = Rpc::new("AdjustCreditBalance");
pub const RETIRE_ACCOUNT: Rpc<(), ()> = Rpc::new("RetireAccount");

pub static DISPLAY_NAME: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("display_name"));
pub static EMAIL: LazyLock<Attribute<String>> = LazyLock::new(|| Attribute::new("email"));
pub static CREDIT_BALANCE: LazyLock<Attribute<i64>> =
    LazyLock::new(|| Attribute::new("credit_balance"));
pub static RETIRED: LazyLock<Attribute<bool>> = LazyLock::new(|| Attribute::new("retired"));

pub fn customer_account_flow_id(account_id: &str) -> String {
    format!("customer-account-{account_id}")
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ContactDetails {
    pub display_name: String,
    pub email: String,
}

impl ContactDetails {
    pub fn validate(&self) -> HandlerResult<()> {
        if self.display_name.trim().is_empty() {
            return Err(account_error("display name is required"));
        }
        if !self.email.contains('@') {
            return Err(account_error("email must contain @"));
        }
        Ok(())
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct CreditAdjustment {
    pub amount: i64,
}

#[derive(Default)]
pub struct CustomerAccountFlow {
    close_account: CloseAccountStep,
}

impl CustomerAccountFlow {
    pub fn new(archive: Arc<FakeAccountArchive>) -> Self {
        Self {
            close_account: CloseAccountStep { archive },
        }
    }

    fn update_contact_details(
        &self,
        context: &mut Context,
        contact: ContactDetails,
    ) -> HandlerResult<()> {
        require_open_account(context)?;
        contact.validate()?;
        DISPLAY_NAME.set(context, contact.display_name)?;
        EMAIL.set(context, contact.email)
    }

    fn adjust_credit_balance(
        &self,
        context: &mut Context,
        adjustment: CreditAdjustment,
    ) -> HandlerResult<RpcResult<i64>> {
        require_open_account(context)?;
        if adjustment.amount == 0 {
            return Err(account_error("amount must not be zero"));
        }
        let balance = CREDIT_BALANCE
            .get_required(context)?
            .checked_add(adjustment.amount)
            .ok_or_else(|| account_error("credit balance is out of range"))?;
        if balance < 0 {
            return Err(account_error("credit balance cannot go below zero"));
        }
        CREDIT_BALANCE.set(context, balance)?;
        Ok(RpcResult::new(balance))
    }

    fn retire_account(&self, context: &mut Context) -> HandlerResult<RpcResult<()>> {
        if RETIRED.get(context)?.unwrap_or(false) {
            return Ok(RpcResult::new(()));
        }
        if CREDIT_BALANCE.get_required(context)? != 0 {
            return Err(account_error(
                "spend or refund the credit balance before retiring",
            ));
        }
        RETIRED.set(context, true)?;
        Ok(RpcResult::new(()).then(StepMovement::to(&self.close_account, ())))
    }
}

impl Flow for CustomerAccountFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::empty().and(&self.close_account)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&DISPLAY_NAME)
            .attribute(&EMAIL)
            .attribute(&CREDIT_BALANCE)
            .attribute(&RETIRED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure(
                UPDATE_CONTACT_DETAILS.lock(CREDIT_BALANCE.lock()),
                Self::update_contact_details,
            )
            .function(
                ADJUST_CREDIT_BALANCE.lock(CREDIT_BALANCE.lock()),
                Self::adjust_credit_balance,
            )
            .function_without_input(
                RETIRE_ACCOUNT.lock(CREDIT_BALANCE.lock()),
                Self::retire_account,
            )
    }
}

// dex:group group-id:account-retirement group-label:"Account retirement"
// dex:explanation text:"Archive the retired account, then complete its Flow."
#[derive(Default)]
pub struct CloseAccountStep {
    archive: Arc<FakeAccountArchive>,
}

impl Step for CloseAccountStep {
    type Input = ();

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_retry(
            RetryPolicy::new()
                .initial_interval(Duration::from_secs(1))
                .maximum_interval(Duration::from_secs(1)),
        )
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        self.archive
            .archive_account(context.flow_id())
            .map_err(|message| account_error(&message))?;
        Ok(StepDecision::graceful_complete(()))
    }
}

fn require_open_account(context: &Context) -> HandlerResult<()> {
    if RETIRED.get(context)?.unwrap_or(false) {
        return Err(account_error("account is retired"));
    }
    Ok(())
}

fn account_error(message: &str) -> HandlerError {
    HandlerError::new("CustomerAccountFlow", message)
}
