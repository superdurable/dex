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
    Channel, ConditionCombination, Context, Flow, HandlerError, HandlerResult, PersistenceSchema,
    Rpc, RpcList, Step, StepDecision, StepList, Timer, Wait,
};
use serde::{Deserialize, Serialize};

pub const WAIT_TYPES_SIGNAL_A: Rpc<(), ()> = Rpc::new("WaitTypesSignalA");
pub const WAIT_TYPES_SIGNAL_B: Rpc<(), ()> = Rpc::new("WaitTypesSignalB");

fn signal_a_channel() -> Channel<String> {
    Channel::new("SignalA")
}

fn signal_b_channel() -> Channel<String> {
    Channel::new("SignalB")
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct WaitTypesInput {
    pub mode: String,
    pub timeout_seconds: i32,
}

#[derive(Default)]
pub struct WaitTypesFlow {
    wait_types: WaitTypesStep,
}

impl WaitTypesFlow {
    fn signal_a(&self, context: &mut Context) -> HandlerResult<()> {
        signal_a_channel().publish(context, "signal-a".to_string())
    }

    fn signal_b(&self, context: &mut Context) -> HandlerResult<()> {
        signal_b_channel().publish(context, "signal-b".to_string())
    }
}

impl Flow for WaitTypesFlow {
    type StartInput = WaitTypesInput;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.wait_types)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .channel(&signal_a_channel())
            .channel(&signal_b_channel())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(WAIT_TYPES_SIGNAL_A, Self::signal_a)
            .procedure_without_input(WAIT_TYPES_SIGNAL_B, Self::signal_b)
    }
}

#[derive(Default)]
struct WaitTypesStep;

impl Step for WaitTypesStep {
    type Input = WaitTypesInput;

    fn wait_for(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<Wait> {
        let timeout = Duration::from_secs(input.timeout_seconds.max(0) as u64);
        match input.mode.as_str() {
            "any" => Ok(Wait::any_of([
                signal_a_channel().for_one().with_id("signal"),
                Timer::by_duration(timeout).with_id("timeout"),
            ])),
            "all" => Ok(Wait::all_of([
                signal_a_channel().for_one().with_id("signal-a"),
                signal_b_channel().for_one().with_id("signal-b"),
            ])),
            "combo" => Ok(Wait::any_combination_of([
                ConditionCombination::all_of([
                    signal_a_channel().for_one().with_id("signal-a"),
                    Timer::by_duration(timeout).with_id("timeout"),
                ]),
                ConditionCombination::all_of([signal_b_channel().for_one().with_id("signal-b")]),
            ])),
            _ => Err(HandlerError::new(
                "WaitTypes",
                format!("unknown wait mode {}", input.mode),
            )),
        }
    }

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input.mode))
    }
}
