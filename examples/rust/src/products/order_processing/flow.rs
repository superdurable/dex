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
    Attribute, AttributeIndex, Channel, Context, Flow, HandlerError, HandlerResult,
    PersistenceSchema, RetryPolicy, Rpc, RpcList, RpcResult, Step, StepDecision, StepList,
    StepOptions, Timer, Wait,
};
use serde::{Deserialize, Serialize};

pub const ORDER_APPROVE: Rpc<String, String> = Rpc::new("OrderApprove");
pub const ORDER_DESCRIBE: Rpc<(), String> = Rpc::new("OrderDescribe");
pub const SELLER_REMINDER_TIMER: &str = "seller-reminder";

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct OrderRequest {
    pub order_id: String,
    pub email: String,
    pub customer_id: String,
    pub amount: i64,
    #[serde(default, rename = "testFailAtShipping")]
    pub test_fail_at_shipping: bool,
}

#[derive(Default)]
pub struct OrderProcessingFlow {
    charge: Charge,
    ship: Ship,
    refund: Refund,
}

impl OrderProcessingFlow {
    fn approve(&self, context: &mut Context, _note: String) -> HandlerResult<RpcResult<String>> {
        seller_ok().publish(context, "approved".to_string())?;
        Ok(RpcResult::new("ok".to_string()))
    }

    fn describe(&self, context: &mut Context) -> HandlerResult<RpcResult<String>> {
        Ok(RpcResult::new(order_status().get_required(context)?))
    }
}

impl Flow for OrderProcessingFlow {
    type StartInput = OrderRequest;

    fn flow_type(&self) -> &'static str {
        "OrderProcessingFlow"
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.charge)
            .and(&self.ship)
            .and(&self.refund)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&order_status())
            .channel(&seller_ok())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function(ORDER_APPROVE, Self::approve)
            .function_without_input(ORDER_DESCRIBE, Self::describe)
    }
}

#[derive(Default)]
pub struct Charge;

impl Step for Charge {
    type Input = OrderRequest;

    fn step_type(&self) -> &'static str {
        "ChargeStep"
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_retry(RetryPolicy::new().maximum_attempts(3))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event("charge", input.order_id.clone())?;
        order_status().set(context, "charged".to_string())?;
        Ok(StepDecision::go_to(&Ship, input))
    }
}

#[derive(Default)]
pub struct Ship;

impl Step for Ship {
    type Input = OrderRequest;

    fn step_type(&self) -> &'static str {
        "ShipStep"
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_retry(
                RetryPolicy::new()
                    .initial_interval(Duration::from_secs(1))
                    .maximum_attempts(2),
            )
            .on_execute_failure_proceed_to(&Refund)
    }

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            seller_ok().for_one(),
            Timer::by_duration(Duration::from_secs(24 * 60 * 60)).with_id(SELLER_REMINDER_TIMER),
        ]))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        if context.has_any_timer_fired() {
            context.record_event("shipment-reminder", input.order_id.clone())?;
            return Ok(StepDecision::go_to(&Ship, input));
        }
        ship_item(&input.order_id, input.test_fail_at_shipping)?;
        context.record_event("ship", input.order_id.clone())?;
        order_status().set(context, "shipped".to_string())?;
        Ok(StepDecision::graceful_complete(format!(
            "shipped:{}",
            input.order_id
        )))
    }
}

#[derive(Default)]
struct Refund;

impl Step for Refund {
    type Input = OrderRequest;

    fn step_type(&self) -> &'static str {
        "RefundStep"
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_retry(RetryPolicy::new().maximum_attempts(3))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event("refund", input.order_id.clone())?;
        order_status().set(context, "refunded".to_string())?;
        Ok(StepDecision::graceful_complete(format!(
            "refunded:{}",
            input.order_id
        )))
    }
}

fn order_status() -> Attribute<String> {
    Attribute::new("order-status").indexed(AttributeIndex::keyword())
}

fn seller_ok() -> Channel<String> {
    Channel::new("seller-ok")
}

fn ship_item(order_id: &str, test_fail_at_shipping: bool) -> HandlerResult<()> {
    if test_fail_at_shipping {
        return Err(HandlerError::new(format!(
            "ship failed for order {order_id}"
        )));
    }
    Ok(())
}
