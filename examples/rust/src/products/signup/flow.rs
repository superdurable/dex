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

use std::{sync::LazyLock, time::Duration};

use dex_sdk::{
    Attribute, Channel, Context, Flow, HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult,
    Step, StepDecision, StepList, Timer, Wait,
};

pub const ONBOARDING_VERIFY: Rpc<(), String> = Rpc::new("OnboardingVerify");
pub const ONBOARDING_TASK_1: Rpc<(), String> = Rpc::new("OnboardingTask1");
pub const ONBOARDING_TASK_2: Rpc<(), String> = Rpc::new("OnboardingTask2");

pub const WAITING_FOR_VERIFICATION: &str = "waiting_for_verification";
pub const WAITING_FOR_TASK_1: &str = "waiting_for_task_1";
pub const WAITING_FOR_TASK_2: &str = "waiting_for_task_2";
pub const COMPLETED: &str = "completed";

#[derive(Default)]
pub struct UserOnboardingFlow {
    submit: Submit,
    verify_email: VerifyEmail,
    accomplish_task_1: AccomplishTask1,
    accomplish_task_2: AccomplishTask2,
}

impl UserOnboardingFlow {
    fn verify(&self, context: &mut Context) -> HandlerResult<RpcResult<String>> {
        if ONBOARDING_STATUS.get(context)?.as_deref() != Some(WAITING_FOR_VERIFICATION) {
            return Ok(RpcResult::new("already verified".to_string()));
        }
        VERIFY_EMAIL.publish(context, ())?;
        Ok(RpcResult::new("verified".to_string()))
    }

    fn accomplish_task_1(&self, context: &mut Context) -> HandlerResult<RpcResult<String>> {
        if ONBOARDING_STATUS.get(context)?.as_deref() != Some(WAITING_FOR_TASK_1) {
            return Ok(RpcResult::new("task 1 is not waiting".to_string()));
        }
        TASK_1_COMPLETED.publish(context, ())?;
        Ok(RpcResult::new("task 1 accomplished".to_string()))
    }

    fn accomplish_task_2(&self, context: &mut Context) -> HandlerResult<RpcResult<String>> {
        if ONBOARDING_STATUS.get(context)?.as_deref() != Some(WAITING_FOR_TASK_2) {
            return Ok(RpcResult::new("task 2 is not waiting".to_string()));
        }
        TASK_2_COMPLETED.publish(context, ())?;
        Ok(RpcResult::new("task 2 accomplished".to_string()))
    }
}

impl Flow for UserOnboardingFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.submit)
            .and(&self.verify_email)
            .and(&self.accomplish_task_1)
            .and(&self.accomplish_task_2)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&ONBOARDING_STATUS)
            .channel(&VERIFY_EMAIL)
            .channel(&TASK_1_COMPLETED)
            .channel(&TASK_2_COMPLETED)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(ONBOARDING_VERIFY, Self::verify)
            .function_without_input(ONBOARDING_TASK_1, Self::accomplish_task_1)
            .function_without_input(ONBOARDING_TASK_2, Self::accomplish_task_2)
    }
}

#[derive(Default)]
struct Submit;

impl Step for Submit {
    type Input = String;

    fn execute(&self, context: &mut Context, email: Self::Input) -> HandlerResult<StepDecision> {
        ONBOARDING_STATUS.set(context, WAITING_FOR_VERIFICATION.to_string())?;
        context.record_event("verification-email", email.clone())?;
        Ok(StepDecision::go_to(&VerifyEmail, email))
    }
}

#[derive(Default)]
struct VerifyEmail;

impl Step for VerifyEmail {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            VERIFY_EMAIL.for_one(),
            Timer::by_duration(Duration::from_secs(24)),
        ]))
    }

    fn execute(&self, context: &mut Context, email: Self::Input) -> HandlerResult<StepDecision> {
        if !VERIFY_EMAIL.condition_results(context)?.is_empty() {
            ONBOARDING_STATUS.set(context, WAITING_FOR_TASK_1.to_string())?;
            context.record_event("onboarding-task-1", email.clone())?;
            return Ok(StepDecision::go_to(&AccomplishTask1, email));
        }
        context.record_event("verification-reminder", email.clone())?;
        Ok(StepDecision::go_to(&VerifyEmail, email))
    }
}

#[derive(Default)]
struct AccomplishTask1;

impl Step for AccomplishTask1 {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            TASK_1_COMPLETED.for_one(),
            Timer::by_duration(Duration::from_secs(24)),
        ]))
    }

    fn execute(&self, context: &mut Context, email: Self::Input) -> HandlerResult<StepDecision> {
        if !TASK_1_COMPLETED.condition_results(context)?.is_empty() {
            ONBOARDING_STATUS.set(context, WAITING_FOR_TASK_2.to_string())?;
            context.record_event("onboarding-task-2", email.clone())?;
            return Ok(StepDecision::go_to(&AccomplishTask2, email));
        }
        context.record_event("task-1-reminder", email.clone())?;
        Ok(StepDecision::go_to(&AccomplishTask1, email))
    }
}

#[derive(Default)]
struct AccomplishTask2;

impl Step for AccomplishTask2 {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            TASK_2_COMPLETED.for_one(),
            Timer::by_duration(Duration::from_secs(24)),
        ]))
    }

    fn execute(&self, context: &mut Context, email: Self::Input) -> HandlerResult<StepDecision> {
        if !TASK_2_COMPLETED.condition_results(context)?.is_empty() {
            ONBOARDING_STATUS.set(context, COMPLETED.to_string())?;
            context.record_event("onboarding-complete", email)?;
            return Ok(StepDecision::graceful_complete(
                "onboarding completed".to_string(),
            ));
        }
        context.record_event("task-2-reminder", email.clone())?;
        Ok(StepDecision::go_to(&AccomplishTask2, email))
    }
}

pub static ONBOARDING_STATUS: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("Status"));
static VERIFY_EMAIL: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("VerifyEmail"));
static TASK_1_COMPLETED: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("Task1Completed"));
static TASK_2_COMPLETED: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("Task2Completed"));
