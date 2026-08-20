// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use dex_sdk::{
    Context, Flow, HandlerError, HandlerResult, Rpc, RpcList, RpcResult, Step, StepDecision,
    StepList, StepMovement,
};

pub(crate) struct NoStartStateWorkflow {
    triggered: TriggeredStep,
}

impl NoStartStateWorkflow {
    pub(crate) const RPC_OUTPUT: i64 = 100;
    pub(crate) const INVOKE: Rpc<String, i64> = Rpc::new("invoke");

    pub(crate) fn new() -> Self {
        Self {
            triggered: TriggeredStep,
        }
    }

    fn invoke(&self, context: &mut Context, _input: String) -> HandlerResult<RpcResult<i64>> {
        if context.flow_id().is_empty() || context.run_id().is_empty() {
            return Err(HandlerError::new(
                "NoStartStateFailure",
                "invalid RPC context",
            ));
        }
        Ok(RpcResult::new(Self::RPC_OUTPUT).then(StepMovement::to(&self.triggered, ())))
    }
}

impl Flow for NoStartStateWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::empty().and(&self.triggered)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().function(Self::INVOKE, Self::invoke)
    }
}

struct TriggeredStep;

impl Step for TriggeredStep {
    type Input = ();

    fn execute(&self, _context: &mut Context, (): ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(1_i32))
    }
}
