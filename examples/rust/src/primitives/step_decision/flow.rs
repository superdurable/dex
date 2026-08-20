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
    Context, Flow, HandlerResult, Step, StepDecision, StepList, StepMovement, Timer, Wait,
};
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct Quote {
    pub carrier: String,
    pub price: i32,
}

#[derive(Default)]
pub struct StepDecisionFlow {
    route: RouteStep,
    carrier_a: CarrierAStep,
    carrier_b: CarrierBStep,
    winner: WinnerStep,
    record_quote: RecordQuoteStep,
    branch_worker: BranchWorkerStep,
}

impl Flow for StepDecisionFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.route)
            .and(&self.carrier_a)
            .and(&self.carrier_b)
            .and(&self.winner)
            .and(&self.record_quote)
            .and(&self.branch_worker)
    }
}

#[derive(Default)]
struct RouteStep;

impl Step for RouteStep {
    type Input = String;

    fn execute(&self, _context: &mut Context, mode: Self::Input) -> HandlerResult<StepDecision> {
        match mode.as_str() {
            "graceful" => Ok(StepDecision::graceful_complete(String::from("done"))),
            "dead-end" => Ok(StepDecision::go_to_many([
                StepMovement::to(&BranchWorkerStep, "left".to_string()),
                StepMovement::to(&BranchWorkerStep, "right".to_string()),
            ])),
            _ => {
                let quote = Quote {
                    carrier: "winner".to_string(),
                    price: 9,
                };
                Ok(StepDecision::go_to_many([
                    StepMovement::to(
                        &CarrierAStep,
                        Quote {
                            carrier: "A".to_string(),
                            price: 10,
                        },
                    ),
                    StepMovement::to(
                        &CarrierBStep,
                        Quote {
                            carrier: "B".to_string(),
                            price: 12,
                        },
                    ),
                    StepMovement::to(&WinnerStep, quote),
                ]))
            }
        }
    }
}

#[derive(Default)]
struct BranchWorkerStep;

impl Step for BranchWorkerStep {
    type Input = String;

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct CarrierAStep;

impl Step for CarrierAStep {
    type Input = Quote;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(2))))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct CarrierBStep;

impl Step for CarrierBStep {
    type Input = Quote;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(2))))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::dead_end())
    }
}

#[derive(Default)]
struct WinnerStep;

impl Step for WinnerStep {
    type Input = Quote;

    fn execute(&self, _context: &mut Context, quote: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&RecordQuoteStep, quote)
            .cancel_step(&CarrierAStep)
            .cancel_step(&CarrierBStep))
    }
}

#[derive(Default)]
struct RecordQuoteStep;

impl Step for RecordQuoteStep {
    type Input = Quote;

    fn execute(&self, _context: &mut Context, quote: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(quote))
    }
}
