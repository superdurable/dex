// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::time::Duration;

use dex_sdk::{
    Context, Flow, HandlerResult, Registry, Rpc, RpcList, RpcResult, Step, StepDecision, StepList,
    StepMovement,
};

use crate::support::{DexDevTestEnvironment, flow_id};

struct NoStartWorkflow {
    finish: NoStartFinishStep,
}

impl NoStartWorkflow {
    const START: Rpc<i32, i32> = Rpc::new("start");

    fn start(&self, _context: &mut Context, _input: i32) -> HandlerResult<RpcResult<i32>> {
        Ok(RpcResult::new(2).then(StepMovement::to(&self.finish, 2)))
    }
}

impl Flow for NoStartWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::empty().and(&self.finish)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(Self::START, Self::start)
    }
}

struct NoStartFinishStep;

impl Step for NoStartFinishStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input + 1))
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn no_start_step_contract_starts_from_typed_rpc_movement() {
    let environment = DexDevTestEnvironment::start(Registry::new().register(NoStartWorkflow {
        finish: NoStartFinishStep,
    }));
    let workflow = NoStartWorkflow {
        finish: NoStartFinishStep,
    };
    let flow_id = flow_id("go-no-start");
    environment
        .client
        .start_flow(&workflow, &flow_id, ())
        .expect("start Go no-start-Step Flow");
    assert_eq!(
        2,
        environment
            .client
            .invoke_rpc(&flow_id, NoStartWorkflow::START, 1)
            .expect("invoke Go no-start-Step RPC")
    );
    assert_eq!(
        3,
        environment
            .client
            .wait_for_flow_with_timeout::<i32>(&flow_id, Duration::from_secs(30))
            .expect("complete Go no-start-Step Flow")
    );
}
