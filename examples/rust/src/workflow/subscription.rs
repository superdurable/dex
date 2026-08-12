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
    Attribute, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult,
    Step, StepDecision, StepList, StepMovement, Timer, Wait,
};
use serde::{Deserialize, Serialize};

pub const SUBSCRIPTION_DESCRIBE: Rpc<(), SubscriptionState> = Rpc::new("SubscriptionDescribe");
pub const SUBSCRIPTION_UPDATE_CHARGE: Rpc<i64, ()> = Rpc::new("SubscriptionUpdateCharge");
pub const SUBSCRIPTION_CANCEL: Rpc<(), ()> = Rpc::new("SubscriptionCancel");

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct SubscriptionRequest {
    pub customer_id: String,
    pub charge_cents: i64,
    pub billing_periods: u32,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
pub struct SubscriptionState {
    pub charge_cents: i64,
    pub periods_charged: u32,
    pub cancelled: bool,
}

#[derive(Default)]
pub struct SubscriptionFlow {
    welcome: Welcome,
    billing: Billing,
    control: Control,
}

impl SubscriptionFlow {
    fn describe(&self, context: &mut Context) -> HandlerResult<RpcResult<SubscriptionState>> {
        Ok(RpcResult::new(state().get(context)?.unwrap_or_default()))
    }

    fn update_charge(&self, context: &mut Context, amount: i64) -> HandlerResult<()> {
        commands().publish(context, SubscriptionCommand::Charge(amount))
    }

    fn cancel(&self, context: &mut Context) -> HandlerResult<()> {
        commands().publish(context, SubscriptionCommand::Cancel)
    }
}

impl Flow for SubscriptionFlow {
    type StartInput = SubscriptionRequest;

    fn flow_type(&self) -> &'static str {
        "SubscriptionFlow"
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.welcome)
            .and(&self.billing)
            .and(&self.control)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&state())
            .channel(&commands())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(SUBSCRIPTION_DESCRIBE, Self::describe)
            .procedure(SUBSCRIPTION_UPDATE_CHARGE, Self::update_charge)
            .procedure_without_input(SUBSCRIPTION_CANCEL, Self::cancel)
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
enum SubscriptionCommand {
    Charge(i64),
    Cancel,
}

#[derive(Default)]
struct Welcome;

impl Step for Welcome {
    type Input = SubscriptionRequest;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        state().set(
            context,
            SubscriptionState {
                charge_cents: input.charge_cents,
                periods_charged: 0,
                cancelled: false,
            },
        )?;
        context.record_event("welcome-email", input.customer_id)?;
        Ok(StepDecision::go_to_many([
            StepMovement::to(&Billing, input.billing_periods),
            StepMovement::to(&Control, ()),
        ]))
    }
}

#[derive(Default)]
struct Billing;

impl Step for Billing {
    type Input = u32;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(86_400))))
    }

    fn execute(
        &self,
        context: &mut Context,
        remaining: Self::Input,
    ) -> HandlerResult<StepDecision> {
        let mut current = state().get_required(context)?;
        if current.cancelled || remaining == 0 {
            return Ok(StepDecision::graceful_complete(current));
        }
        current.periods_charged += 1;
        state().set(context, current)?;
        Ok(StepDecision::go_to(&Billing, remaining - 1))
    }
}

#[derive(Default)]
struct Control;

impl Step for Control {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(commands().for_one()))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        let mut current = state().get_required(context)?;
        for command in commands().condition_results(context)? {
            match command {
                SubscriptionCommand::Charge(amount) => current.charge_cents = amount,
                SubscriptionCommand::Cancel => current.cancelled = true,
            }
        }
        state().set(context, current.clone())?;
        if current.cancelled {
            Ok(StepDecision::force_complete(current))
        } else {
            Ok(StepDecision::go_to(&Control, ()))
        }
    }
}

fn state() -> Attribute<SubscriptionState> {
    Attribute::new("subscription-state")
}

fn commands() -> Channel<SubscriptionCommand> {
    Channel::new("subscription-commands")
}
