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

use dex_sdk::{
    Channel, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, RetryPolicy, Rpc,
    RpcList, Step, StepDecision, StepList, StepOptions, Wait,
};

pub const INTERVENTION_APPROVE: Rpc<(), ()> = Rpc::new("InterventionApprove");

#[derive(Default)]
pub struct ManualInterventionFlow {
    risky_operation: RiskyOperation,
    await_approval: AwaitApproval,
}

impl ManualInterventionFlow {
    fn approve(&self, context: &mut Context) -> HandlerResult<()> {
        approval().publish(context, ())
    }
}

impl Flow for ManualInterventionFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.risky_operation).and(&self.await_approval)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&approval())
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure_without_input(INTERVENTION_APPROVE, Self::approve)
    }
}

#[derive(Default)]
struct RiskyOperation;

impl Step for RiskyOperation {
    type Input = String;

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        Err(HandlerError::new(format!(
            "manual review required for {input}"
        )))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_retry(RetryPolicy::new().maximum_attempts(2))
            .on_execute_failure_proceed_to(&AwaitApproval)
    }
}

#[derive(Default)]
struct AwaitApproval;

impl Step for AwaitApproval {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(approval().for_one()))
    }

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input))
    }
}

fn approval() -> Channel<()> {
    Channel::new("manual-intervention-approval")
}
