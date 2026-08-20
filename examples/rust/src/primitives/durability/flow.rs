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
    Context, Flow, HandlerResult, Step, StepDecision, StepList, StepDurability, StepOptions,
    Timer, Wait,
};

#[derive(Default)]
pub struct DurabilityFlow {
    route: RouteDurabilityStep,
    sync_work: SyncWorkStep,
    async_work: AsyncWorkStep,
    finish: FinishDurabilityStep,
}

impl Flow for DurabilityFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.route)
            .and(&self.sync_work)
            .and(&self.async_work)
            .and(&self.finish)
    }
}

#[derive(Default)]
struct RouteDurabilityStep;

impl Step for RouteDurabilityStep {
    type Input = String;

    fn execute(&self, _context: &mut Context, mode: Self::Input) -> HandlerResult<StepDecision> {
        if mode == "async" {
            Ok(StepDecision::go_to(&AsyncWorkStep, mode))
        } else {
            Ok(StepDecision::go_to(&SyncWorkStep, mode))
        }
    }
}

#[derive(Default)]
struct SyncWorkStep;

impl Step for SyncWorkStep {
    type Input = String;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_durability(StepDurability::Sync)
    }

    fn execute(&self, _context: &mut Context, mode: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&FinishDurabilityStep, format!("sync:{mode}")))
    }
}

#[derive(Default)]
struct AsyncWorkStep;

impl Step for AsyncWorkStep {
    type Input = String;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_durability(StepDurability::Async)
    }

    fn execute(&self, _context: &mut Context, mode: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &FinishDurabilityStep,
            format!("async:{mode}"),
        ))
    }
}

#[derive(Default)]
struct FinishDurabilityStep;

impl Step for FinishDurabilityStep {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _label: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(1))))
    }

    fn execute(&self, _context: &mut Context, label: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(label))
    }
}
