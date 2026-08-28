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

use std::sync::LazyLock;

use dex_sdk::{
    Attribute, AttributeIndex, Channel, Context, Flow, HandlerResult, PersistenceSchema,
    RetryPolicy, Rpc, RpcList, RpcResult, Step, StepDecision, StepList, StepOptions, Timer, Wait,
};
use serde::{Deserialize, Serialize};

use crate::shared::MyDependencyService;

pub const ORDER_APPROVE: Rpc<String, String> = Rpc::new("OrderApprove");
pub const ORDER_DESCRIBE: Rpc<(), String> = Rpc::new("OrderDescribe");

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct OrderRequest {
    pub order_id: String,
    pub email: String,
    pub customer_id: String,
    pub amount: i64,
    #[serde(default, rename = "testFailAtShipping")]
    pub test_fail_at_shipping: bool,
}

#[derive(Clone)]
pub struct OrderProcessingFlow {
    charge: Charge,
    ship: Ship,
    refund: Refund,
}

impl OrderProcessingFlow {
    pub fn new(service: MyDependencyService) -> Self {
        Self {
            charge: Charge {
                service: service.clone(),
            },
            ship: Ship {
                service: service.clone(),
            },
            refund: Refund { service },
        }
    }

    fn approve(&self, context: &mut Context, _note: String) -> HandlerResult<RpcResult<String>> {
        SELLER_OK.publish(context, "approved".to_string())?;
        Ok(RpcResult::new("ok".to_string()))
    }

    fn describe(&self, context: &mut Context) -> HandlerResult<RpcResult<String>> {
        Ok(RpcResult::new(ORDER_STATUS.get_required(context)?))
    }
}

impl Default for OrderProcessingFlow {
    fn default() -> Self {
        Self::new(MyDependencyService)
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
            .attribute(&ORDER_STATUS)
            .channel(&SELLER_OK)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function(ORDER_APPROVE, Self::approve)
            .function_without_input(ORDER_DESCRIBE, Self::describe)
    }
}

#[derive(Clone, Default)]
pub struct Charge {
    service: MyDependencyService,
}

impl Step for Charge {
    type Input = OrderRequest;

    fn step_type(&self) -> &'static str {
        "ChargeStep"
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_retry(
            RetryPolicy::new()
                // .total_duration(Duration::from_secs(60 * 60))
                .total_duration(Duration::from_secs(3)),
        )
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        self.service
            .charge_user(&input.email, &input.customer_id, input.amount);
        context.record_event("charge", input.order_id.clone())?;
        ORDER_STATUS.set(context, "charged".to_string())?;
        Ok(StepDecision::go_to(&Ship::default(), input))
    }
}

#[derive(Clone, Default)]
pub struct Ship {
    service: MyDependencyService,
}

impl Step for Ship {
    type Input = OrderRequest;

    fn step_type(&self) -> &'static str {
        "ShipStep"
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_retry(
                RetryPolicy::new()
                    // .total_duration(Duration::from_secs(60 * 60))
                    .total_duration(Duration::from_secs(3)),
            )
            .on_execute_failure_proceed_to(&Refund::default())
    }

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            SELLER_OK.for_one(),
            Timer::by_duration(Duration::from_secs(24 * 60 * 60)),
        ]))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        if context.has_any_timer_fired() {
            self.service.send_email(
                &input.email,
                "Reminder: approve shipment",
                "Please approve or provide a tracking number.",
            );
            context.record_event("shipment-reminder", input.order_id.clone())?;
            return Ok(StepDecision::go_to(&Ship::default(), input));
        }
        self.service
            .ship_item(&input.order_id, input.test_fail_at_shipping)?;
        context.record_event("ship", input.order_id.clone())?;
        ORDER_STATUS.set(context, "shipped".to_string())?;
        Ok(StepDecision::graceful_complete(format!(
            "shipped:{}",
            input.order_id
        )))
    }
}

#[derive(Clone, Default)]
pub struct Refund {
    service: MyDependencyService,
}

impl Step for Refund {
    type Input = OrderRequest;

    fn step_type(&self) -> &'static str {
        "RefundStep"
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_retry(
            RetryPolicy::new()
                // .total_duration(Duration::from_secs(60 * 60))
                .total_duration(Duration::from_secs(3)),
        )
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        self.service
            .update_external_system(&format!("refund {}", input.order_id));
        context.record_event("refund", input.order_id.clone())?;
        ORDER_STATUS.set(context, "refunded".to_string())?;
        Ok(StepDecision::graceful_complete(format!(
            "refunded:{}",
            input.order_id
        )))
    }
}

static ORDER_STATUS: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("order-status").indexed(AttributeIndex::keyword()));

static SELLER_OK: LazyLock<Channel<String>> = LazyLock::new(|| Channel::new("seller-ok"));
