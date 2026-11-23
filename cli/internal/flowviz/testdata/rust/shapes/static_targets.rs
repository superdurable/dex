// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use crate::Finish as FinishAlias;

const FLOW_NAME: &str = "Transfers";
static STEP_NAME: &str = "Finished";
const FINISH: FinishAlias = FinishAlias;
static FALLBACK: Fallback = Fallback;

struct TransferFlow {
    finish: Arc<Box<FinishAlias>>,
}

impl Flow for TransferFlow {
    type StartInput = u8;

    fn flow_type(&self) -> &'static str {
        FLOW_NAME
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&Start)
            .and(&**self.finish)
            .and(&FALLBACK)
    }
}

// dex:group group-id:transfer group-label:"Transfer"

/// Checks the transfer.
#[derive(
    Default,
)]
// dex:explanation text:"Checks the transfer."
struct Start;

impl Step for Start {
    type Input = u8;

    fn execute(&self, _ctx: &mut Context, input: u8) -> HandlerResult<StepDecision> {
        if input == 0 {
            return Ok(StepDecision::go_to(&*Box::new(FinishAlias::default()), ()));
        }
        match input {
            1 if enabled() => return Ok(StepDecision::go_to(&*Arc::new(FINISH), ())),
            _ => {}
        }
        Ok(StepDecision::go_to_many([build_movement(&*Rc::new(FinishAlias::new()))]))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        transfer_options(&FALLBACK)
    }
}

fn transfer_options(fallback: &Fallback) -> StepOptions<u8> {
    StepOptions::new().on_execute_failure_proceed_to(fallback)
}

fn build_movement(destination: &Finish) -> StepMovement {
    let finish = destination;
    StepMovement::to(finish, ())
}

// dex:group group-id:transfer group-label:"Transfer"
// dex:explanation text:"Finishes the transfer."
struct Finish;

impl Step for Finish {
    type Input = ();

    fn step_type(&self) -> &'static str {
        STEP_NAME
    }

    fn execute(&self, _ctx: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(()))
    }
}

// dex:group group-id:transfer group-label:"Transfer"
// dex:explanation text:"Handles a failed transfer."
struct Fallback;

impl Step for Fallback {
    type Input = u8;

    fn execute(&self, _ctx: &mut Context, _input: u8) -> HandlerResult<StepDecision> {
        Ok(StepDecision::force_fail("Transfer failed"))
    }
}
