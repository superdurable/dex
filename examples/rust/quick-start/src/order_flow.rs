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

use dex_sdk::{Context, Flow, HandlerResult, Step, StepDecision, StepList, Timer, Wait};

use crate::shop_api::ShopApi;

pub const SHIP_DELAY_TIMER: &str = "ship-delay";

#[derive(Clone)]
pub struct Charge {
    shop: ShopApi,
}

impl Step for Charge {
    type Input = String;

    fn execute(&self, _context: &mut Context, order_id: String) -> HandlerResult<StepDecision> {
        self.shop.charge(&order_id, &format!("charge-{order_id}"));
        let ship = Ship {
            shop: self.shop.clone(),
        };
        Ok(StepDecision::go_to(&ship, order_id))
    }
}

#[derive(Clone)]
pub struct Ship {
    shop: ShopApi,
}

impl Step for Ship {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _order_id: String) -> HandlerResult<Wait> {
        let delay = Timer::by_duration(Duration::from_secs(30)).with_id(SHIP_DELAY_TIMER);
        Ok(Wait::until(delay))
    }

    fn execute(&self, _context: &mut Context, order_id: String) -> HandlerResult<StepDecision> {
        self.shop.ship(&order_id, &format!("ship-{order_id}"));
        Ok(StepDecision::graceful_complete(format!(
            "shipped {order_id}"
        )))
    }
}

#[derive(Clone)]
pub struct OrderFlow {
    pub charge: Charge,
    pub ship: Ship,
}

impl OrderFlow {
    pub fn new(shop: ShopApi) -> Self {
        Self {
            charge: Charge { shop: shop.clone() },
            ship: Ship { shop },
        }
    }
}

impl Flow for OrderFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, String> {
        StepList::start(&self.charge).and(&self.ship)
    }
}
