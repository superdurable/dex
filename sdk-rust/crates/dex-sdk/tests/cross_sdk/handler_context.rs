// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::{Duration, SystemTime};

use dex_sdk::{
    Context, Flow, HandlerError, HandlerResult, Registry, Step, StepDecision, StepList, Wait,
};

use crate::support::{DexDevTestEnvironment, flow_id};

struct HandlerContextWorkflow {
    first: HandlerContextFirstStep,
    second: HandlerContextSecondStep,
}

impl Flow for HandlerContextWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

struct HandlerContextFirstStep;

impl Step for HandlerContextFirstStep {
    type Input = i32;

    fn wait_for(&self, context: &mut Context, input: i32) -> HandlerResult<Wait> {
        validate_attempt_metadata(context)?;
        context.set_step_execution_local("input", input)?;
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        validate_attempt_metadata(context)?;
        Ok(StepDecision::go_to(&HandlerContextSecondStep, input + 1))
    }
}

fn validate_attempt_metadata(context: &Context) -> HandlerResult<()> {
    if context.attempt() < 1 || context.first_attempt_at() == SystemTime::UNIX_EPOCH {
        return Err(HandlerError::new("invalid first-step attempt metadata"));
    }
    Ok(())
}

struct HandlerContextSecondStep;

impl Step for HandlerContextSecondStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input + 1))
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn handler_attempt_metadata_is_available_in_both_methods() {
    let environment =
        DexDevTestEnvironment::start(Registry::new().register(HandlerContextWorkflow {
            first: HandlerContextFirstStep,
            second: HandlerContextSecondStep,
        }));
    let workflow = HandlerContextWorkflow {
        first: HandlerContextFirstStep,
        second: HandlerContextSecondStep,
    };
    let flow_id = flow_id("handler-context");
    environment
        .client
        .start_flow(&workflow, &flow_id, 1)
        .expect("start handler-context Flow");
    assert_eq!(
        3,
        environment
            .client
            .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
            .and_then(|result| result.single_output::<i32>())
            .expect("complete handler-context Flow")
    );
}
