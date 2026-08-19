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

/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

use std::time::Duration;

use dex_sdk::{
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, Step, StepDecision,
    StepList, StepMovement, Timer, Wait,
};

pub const REMINDER_ACCEPT: Rpc<(), ()> = Rpc::new("ReminderAccept");
pub const REMINDER_OPT_OUT: Rpc<(), ()> = Rpc::new("ReminderOptOut");

#[derive(Default)]
pub struct ReminderFlow {
    start: Start,
    remind: Remind,
    timeout: Timeout,
}

impl ReminderFlow {
    fn accept(&self, context: &mut Context) -> HandlerResult<()> {
        resolution().publish(context, "accepted".to_string())
    }

    fn opt_out(&self, context: &mut Context) -> HandlerResult<()> {
        resolution().publish(context, "opted-out".to_string())
    }
}

impl Flow for ReminderFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
            .and(&self.remind)
            .and(&self.timeout)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&resolution())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(REMINDER_ACCEPT, Self::accept)
            .procedure_without_input(REMINDER_OPT_OUT, Self::opt_out)
    }
}

#[derive(Default)]
struct Start;

impl Step for Start {
    type Input = String;

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many([
            StepMovement::to(&Remind, (input, 0)),
            StepMovement::to(&Timeout, ()),
        ]))
    }
}

#[derive(Default)]
struct Remind;

impl Step for Remind {
    type Input = (String, u32);

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            resolution().for_one(),
            Timer::by_duration(Duration::from_secs(3_600)),
        ]))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        let resolutions = resolution().condition_results(context)?;
        if let Some(resolved) = resolutions.into_iter().next() {
            return Ok(StepDecision::force_complete(resolved));
        }
        context.record_event("reminder", input.0.clone())?;
        Ok(StepDecision::go_to(&Remind, (input.0, input.1 + 1)))
    }
}

#[derive(Default)]
struct Timeout;

impl Step for Timeout {
    type Input = ();

    fn wait_for(&self, _context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(
            604_800,
        ))))
    }

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::force_complete("timed-out".to_string()))
    }
}

fn resolution() -> Channel<String> {
    Channel::new("reminder-resolution")
}
