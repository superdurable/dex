# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from __future__ import annotations

from dex import (
    Context,
    Flow,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    go_to,
    graceful_complete,
)

from quick_start.config import SHIP_DELAY
from quick_start.shop_api import ShopApi

SHIP_DELAY_TIMER = "ship-delay"


# dex:group group-id:payment group-label:"Payment"
# dex:explanation text:"Charge the order through the payment API."
class Charge(Step[str]):
    def __init__(self, shop: ShopApi) -> None:
        self.shop = shop

    def execute(self, context: Context, input: str) -> StepDecision:
        self.shop.charge(input, idempotency_key=f"charge-{input}")
        return go_to(Ship, input)


# dex:group group-id:shipping group-label:"Shipping"
# dex:explanation text:"Wait on a 60-second Timer, then ship the order."
class Ship(Step[str]):
    def __init__(self, shop: ShopApi) -> None:
        self.shop = shop

    def wait_for(self, context: Context, input: str) -> Wait:
        return Wait.until(
            Timer.by_duration(SHIP_DELAY, condition_id=SHIP_DELAY_TIMER)
        )

    def execute(self, context: Context, input: str) -> StepDecision:
        self.shop.ship(input, idempotency_key=f"ship-{input}")
        return graceful_complete(f"shipped {input}")


class OrderFlow(Flow[str]):
    def __init__(self, shop: ShopApi) -> None:
        self.charge = Charge(shop)
        self.ship = Ship(shop)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.charge).other_steps(self.ship)
