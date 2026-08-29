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

use std::sync::LazyLock;
use std::time::Duration;

use dex_sdk::{
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, Step, StepDecision,
    StepList, Timer, Wait,
};

pub const RECORD_ACTIVITY: Rpc<(), ()> = Rpc::new("RecordActivity");
pub static ACTIVE_CHANNEL: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("Active"));

#[derive(Default)]
pub struct InactivenessTrackerFlow {
    tracker_step: TrackerStep,
    process_inactiveness_step: ProcessInactivenessStep,
}

impl InactivenessTrackerFlow {
    fn record_activity(&self, context: &mut Context) -> HandlerResult<()> {
        ACTIVE_CHANNEL.publish(context, ())
    }
}

impl Flow for InactivenessTrackerFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.tracker_step).and(&self.process_inactiveness_step)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&ACTIVE_CHANNEL)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure_without_input(RECORD_ACTIVITY, Self::record_activity)
    }
}

#[derive(Default)]
struct TrackerStep;

impl Step for TrackerStep {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            Timer::by_duration(Duration::from_secs(300)),
            ACTIVE_CHANNEL.for_one(),
        ]))
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        if context.has_any_timer_fired() {
            return Ok(StepDecision::go_to(&ProcessInactivenessStep, ()));
        }
        Ok(StepDecision::go_to(&TrackerStep, ()))
    }
}

#[derive(Default)]
struct ProcessInactivenessStep;

impl Step for ProcessInactivenessStep {
    type Input = ();

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        context.record_event("inactiveness", "timer-fired".to_string())?;
        Ok(StepDecision::graceful_complete("timer-fired".to_string()))
    }
}
