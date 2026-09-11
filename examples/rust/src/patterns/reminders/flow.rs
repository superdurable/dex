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

pub static OPT_OUT: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("OptOut"));
pub const OPT_OUT_REMINDERS: Rpc<(), ()> = Rpc::new("OptOutReminders");

#[derive(Default)]
pub struct ReminderFlow {
    reminder_step: ReminderStep,
}

impl ReminderFlow {
    fn opt_out(&self, context: &mut Context) -> HandlerResult<()> {
        OPT_OUT.publish(context, ())
    }
}

impl Flow for ReminderFlow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.reminder_step)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&OPT_OUT)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure_without_input(OPT_OUT_REMINDERS, Self::opt_out)
    }
}

#[derive(Default)]
struct ReminderStep;

impl Step for ReminderStep {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            Timer::by_duration(Duration::from_secs(5)),
            OPT_OUT.for_one(),
        ]))
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        if !context.has_any_timer_fired() {
            return Ok(StepDecision::graceful_complete("opted-out".to_string()));
        }
        context.record_event("reminder", "sent".to_string())?;
        Ok(StepDecision::go_to(&ReminderStep, ()))
    }
}
