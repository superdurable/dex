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
    Context, Flow, HandlerResult, Step, StepDecision, StepDurability, StepList, StepOptions, Timer,
    Wait,
};

#[derive(Default)]
pub struct DurabilityFlow {
    route: RouteDurability,
    sync_work: SyncWork,
    async_work: AsyncWork,
    finish: FinishDurability,
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
struct RouteDurability;

impl Step for RouteDurability {
    type Input = String;

    fn execute(&self, _context: &mut Context, mode: Self::Input) -> HandlerResult<StepDecision> {
        if mode == "async" {
            Ok(StepDecision::go_to(&AsyncWork, mode))
        } else {
            Ok(StepDecision::go_to(&SyncWork, mode))
        }
    }
}

#[derive(Default)]
struct SyncWork;

impl Step for SyncWork {
    type Input = String;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_durability(StepDurability::Sync)
    }

    fn execute(&self, _context: &mut Context, mode: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &FinishDurability,
            format!("sync:{mode}"),
        ))
    }
}

#[derive(Default)]
struct AsyncWork;

impl Step for AsyncWork {
    type Input = String;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_durability(StepDurability::Async)
    }

    fn execute(&self, _context: &mut Context, mode: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &FinishDurability,
            format!("async:{mode}"),
        ))
    }
}

#[derive(Default)]
struct FinishDurability;

impl Step for FinishDurability {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _label: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(1))))
    }

    fn execute(&self, _context: &mut Context, label: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(label))
    }
}
