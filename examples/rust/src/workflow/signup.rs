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
    StepList, Timer, Wait,
};

pub const SIGNUP_VERIFY: Rpc<(), ()> = Rpc::new("SignupVerify");

#[derive(Default)]
pub struct UserSignupFlow {
    send_verification: SendVerification,
    await_verification: AwaitVerification,
}

impl UserSignupFlow {
    fn verify(&self, context: &mut Context) -> HandlerResult<()> {
        verified().publish(context, ())
    }
}

impl Flow for UserSignupFlow {
    type StartInput = String;

    fn flow_type(&self) -> &'static str {
        "UserSignupFlow"
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.send_verification).and(&self.await_verification)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&verified())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure_without_input(SIGNUP_VERIFY, Self::verify)
    }
}

#[derive(Default)]
struct SendVerification;

impl Step for SendVerification {
    type Input = String;

    fn execute(&self, context: &mut Context, email: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event("verification-email", email.clone())?;
        Ok(StepDecision::go_to(&AwaitVerification, (email, 0)))
    }
}

#[derive(Default)]
struct AwaitVerification;

impl Step for AwaitVerification {
    type Input = (String, u32);

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            verified().for_one(),
            Timer::by_duration(Duration::from_secs(86_400)),
        ]))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        if !verified().condition_results(context)?.is_empty() {
            return Ok(StepDecision::graceful_complete(input.0));
        }
        context.record_event("verification-reminder", input.0.clone())?;
        Ok(StepDecision::go_to(
            &AwaitVerification,
            (input.0, input.1 + 1),
        ))
    }
}

fn verified() -> Channel<()> {
    Channel::new("signup-verified")
}
