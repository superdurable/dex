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

from dataclasses import dataclass
from datetime import timedelta

from dex import (
    Context,
    Flow,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Timer,
    Wait,
    dead_end,
    go_to,
    go_to_many,
    graceful_complete,
)


@dataclass(frozen=True)
class Quote:
    carrier: str
    price: int


class RouteStep(Step[str]):
    def __init__(self, flow: StepDecisionFlow) -> None:
        self.flow = flow

    def execute(self, context: Context, mode: str) -> StepDecision:
        del context
        if mode == "graceful":
            return graceful_complete("done")
        if mode == "dead-end":
            return go_to_many(
                StepMovement.of(BranchWorkerStep, "left"),
                StepMovement.of(BranchWorkerStep, "right"),
            )
        quote = Quote(carrier="winner", price=9)
        return go_to_many(
            StepMovement.of(CarrierAStep, Quote(carrier="A", price=10)),
            StepMovement.of(CarrierBStep, Quote(carrier="B", price=12)),
            StepMovement.of(WinnerStep, quote),
        )


class BranchWorkerStep(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        del context, input
        return dead_end()


class CarrierAStep(Step[Quote]):
    def wait_for(self, context: Context, quote: Quote) -> Wait:
        del context, quote
        return Wait.until(Timer.by_duration(timedelta(seconds=2)))

    def execute(self, context: Context, quote: Quote) -> StepDecision:
        del context, quote
        return dead_end()


class CarrierBStep(Step[Quote]):
    def wait_for(self, context: Context, quote: Quote) -> Wait:
        del context, quote
        return Wait.until(Timer.by_duration(timedelta(seconds=2)))

    def execute(self, context: Context, quote: Quote) -> StepDecision:
        del context, quote
        return dead_end()


class WinnerStep(Step[Quote]):
    def __init__(self, flow: StepDecisionFlow) -> None:
        self.flow = flow

    def execute(self, context: Context, quote: Quote) -> StepDecision:
        del context
        return go_to(RecordQuoteStep, quote).with_canceling_steps(
            CarrierAStep,
            self.flow.carrier_b,
        )


class RecordQuoteStep(Step[Quote]):
    def execute(self, context: Context, quote: Quote) -> StepDecision:
        del context
        return graceful_complete(quote)


class StepDecisionFlow(Flow[str]):
    def __init__(self) -> None:
        self.branch_worker = BranchWorkerStep()
        self.carrier_a = CarrierAStep()
        self.carrier_b = CarrierBStep()
        self.winner = WinnerStep(self)
        self.record_quote = RecordQuoteStep()
        self.route = RouteStep(self)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.route).other_steps(
            self.carrier_a,
            self.carrier_b,
            self.winner,
            self.record_quote,
            self.branch_worker,
        )
