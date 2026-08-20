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
    Attribute, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, Rpc, RpcList,
    RpcResult,
};

use crate::rpc_workflow::RpcWorkflow;

pub(crate) struct RpcNoStateWorkflow {
    counter: Attribute<i32>,
}

impl RpcNoStateWorkflow {
    pub(crate) const RPC_OUTPUT: i64 = 100;
    pub(crate) const INCREASE_COUNTER: Rpc<(), i32> = Rpc::new("increase_counter");
    pub(crate) const GET_COUNTER: Rpc<(), Option<i32>> = Rpc::new("get_counter");
    pub(crate) const FAIL: Rpc<String, i64> = Rpc::new("fail");
    pub(crate) const INVOKE: Rpc<String, i64> = Rpc::new("invoke");

    pub(crate) fn new() -> Self {
        Self {
            counter: Attribute::new("counter"),
        }
    }

    fn increase_counter(&self, context: &mut Context) -> HandlerResult<RpcResult<i32>> {
        let next = self.counter.get(context)?.unwrap_or_default() + 1;
        self.counter.set(context, next)?;
        Ok(RpcResult::new(next))
    }

    fn get_counter(&self, context: &mut Context) -> HandlerResult<RpcResult<Option<i32>>> {
        Ok(RpcResult::new(self.counter.get(context)?))
    }

    fn fail(&self, _context: &mut Context, input: String) -> HandlerResult<RpcResult<i64>> {
        Err(HandlerError::new("RpcNoStateFailure", input))
    }

    fn invoke(&self, context: &mut Context, _input: String) -> HandlerResult<RpcResult<i64>> {
        if context.flow_id().is_empty() || context.run_id().is_empty() {
            return Err(HandlerError::new(
                "RpcNoStateFailure",
                "invalid RPC context",
            ));
        }
        Ok(RpcResult::new(RpcWorkflow::RPC_OUTPUT))
    }
}

impl Flow for RpcNoStateWorkflow {
    type StartInput = ();

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&self.counter)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .function_without_input(
                Self::INCREASE_COUNTER.lock(self.counter.lock()),
                Self::increase_counter,
            )
            .function_without_input(Self::GET_COUNTER, Self::get_counter)
            .function(Self::FAIL, Self::fail)
            .function(Self::INVOKE, Self::invoke)
    }
}
