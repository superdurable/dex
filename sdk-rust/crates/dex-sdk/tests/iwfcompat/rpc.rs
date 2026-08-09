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
    Attribute, AttributeIndex, AttributeMap, Channel, Client, Context, Flow, HandlerError,
    HandlerResult, PersistenceSchema, Rpc, RpcList, RpcResult, SdkResult, Step, StepDecision,
    StepList, Wait,
};

struct RpcWorkflow {
    internal: Channel<()>,
    data: Attribute<String>,
    keyword: Attribute<String>,
    integer: Attribute<i32>,
    map: AttributeMap<String>,
    first: RpcFirstStep,
    output: RpcOutputStep,
}

impl RpcWorkflow {
    const RPC_OUTPUT: i64 = 100;
    const HARDCODED_VALUE: &str = "random-string";
    const NO_PERSISTENCE: Rpc<(), ()> = Rpc::new("no_persistence");
    const FUNCTION_ONE: Rpc<String, i64> = Rpc::new("function_one");
    const FUNCTION_ZERO: Rpc<(), i64> = Rpc::new("function_zero");
    const PROCEDURE_ONE: Rpc<String, ()> = Rpc::new("procedure_one");
    const PROCEDURE_ZERO: Rpc<(), ()> = Rpc::new("procedure_zero");
    const READ_ONLY: Rpc<String, i64> = Rpc::new("read_only");
    const SET_DATA: Rpc<Option<String>, ()> = Rpc::new("set_data");
    const GET_DATA: Rpc<(), Option<String>> = Rpc::new("get_data");
    const SET_KEYWORD: Rpc<Option<String>, ()> = Rpc::new("set_keyword");
    const GET_KEYWORD: Rpc<(), Option<String>> = Rpc::new("get_keyword");
    const LOCK_MAP: Rpc<String, String> = Rpc::new("lock_map");

    fn new() -> Self {
        let internal = Channel::new("rpc-internal");
        Self {
            first: RpcFirstStep {
                internal: internal.clone(),
            },
            output: RpcOutputStep,
            internal,
            data: Attribute::new("rpc-data"),
            keyword: Attribute::new("CustomKeywordField").indexed(AttributeIndex::keyword()),
            integer: Attribute::new("CustomIntField").indexed(AttributeIndex::int()),
            map: AttributeMap::new("rpc-map"),
        }
    }

    fn no_persistence(&self, context: &mut Context) -> HandlerResult<()> {
        Self::require_context(context)?;
        self.internal.publish(context, ())
    }

    fn function_one(&self, context: &mut Context, input: String) -> HandlerResult<RpcResult<i64>> {
        Self::require_context(context)?;
        self.data.set(context, input.clone())?;
        self.keyword.set(context, input)?;
        self.integer.set(context, Self::RPC_OUTPUT as i32)?;
        self.internal.publish(context, ())?;
        Ok(RpcResult::new(Self::RPC_OUTPUT))
    }

    fn function_zero(&self, context: &mut Context) -> HandlerResult<RpcResult<i64>> {
        Self::require_context(context)?;
        self.data.set(context, Self::HARDCODED_VALUE.into())?;
        self.keyword.set(context, Self::HARDCODED_VALUE.into())?;
        self.integer.set(context, Self::RPC_OUTPUT as i32)?;
        self.internal.publish(context, ())?;
        Ok(RpcResult::new(Self::RPC_OUTPUT))
    }

    fn procedure_one(&self, context: &mut Context, input: String) -> HandlerResult<()> {
        Self::require_context(context)?;
        self.data.set(context, input.clone())?;
        self.keyword.set(context, input)?;
        self.integer.set(context, Self::RPC_OUTPUT as i32)?;
        self.internal.publish(context, ())
    }

    fn procedure_zero(&self, context: &mut Context) -> HandlerResult<()> {
        Self::require_context(context)?;
        self.data.set(context, Self::HARDCODED_VALUE.into())?;
        self.keyword.set(context, Self::HARDCODED_VALUE.into())?;
        self.integer.set(context, Self::RPC_OUTPUT as i32)?;
        self.internal.publish(context, ())
    }

    fn read_only(&self, context: &mut Context, _input: String) -> HandlerResult<RpcResult<i64>> {
        Self::require_context(context)?;
        Ok(RpcResult::new(Self::RPC_OUTPUT))
    }

    fn set_data(&self, context: &mut Context, input: Option<String>) -> HandlerResult<()> {
        Self::set_optional(context, &self.data, input)
    }

    fn get_data(&self, context: &mut Context) -> HandlerResult<RpcResult<Option<String>>> {
        Ok(RpcResult::new(self.data.get(context)?))
    }

    fn set_keyword(&self, context: &mut Context, input: Option<String>) -> HandlerResult<()> {
        Self::set_optional(context, &self.keyword, input)
    }

    fn get_keyword(&self, context: &mut Context) -> HandlerResult<RpcResult<Option<String>>> {
        Ok(RpcResult::new(self.keyword.get(context)?))
    }

    fn lock_map(&self, context: &mut Context, input: String) -> HandlerResult<RpcResult<String>> {
        self.map.set(context, "one", input.clone())?;
        Ok(RpcResult::new(input))
    }

    fn set_optional(
        context: &mut Context,
        attribute: &Attribute<String>,
        value: Option<String>,
    ) -> HandlerResult<()> {
        match value {
            Some(value) => attribute.set(context, value),
            None => attribute.delete(context),
        }
    }

    fn require_context(context: &Context) -> HandlerResult<()> {
        if context.flow_id().is_empty() || context.run_id().is_empty() {
            return Err(HandlerError::new("invalid RPC context"));
        }
        Ok(())
    }
}

impl Flow for RpcWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::start(&self.first).and(&self.output)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute(&self.data)
            .attribute(&self.keyword)
            .attribute(&self.integer)
            .attribute_map(&self.map)
            .channel(&self.internal)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new()
            .procedure_without_input(Self::NO_PERSISTENCE, Self::no_persistence)
            .function(Self::FUNCTION_ONE, Self::function_one)
            .function_without_input(Self::FUNCTION_ZERO, Self::function_zero)
            .procedure(Self::PROCEDURE_ONE, Self::procedure_one)
            .procedure_without_input(Self::PROCEDURE_ZERO, Self::procedure_zero)
            .function(Self::READ_ONLY, Self::read_only)
            .procedure(Self::SET_DATA, Self::set_data)
            .function_without_input(Self::GET_DATA, Self::get_data)
            .procedure(Self::SET_KEYWORD, Self::set_keyword)
            .function_without_input(Self::GET_KEYWORD, Self::get_keyword)
            .function(Self::LOCK_MAP.lock(self.map.lock("one")), Self::lock_map)
    }
}

struct RpcFirstStep {
    internal: Channel<()>,
}

impl Step for RpcFirstStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::until(self.internal.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&RpcOutputStep, 0))
    }
}

struct RpcOutputStep;

impl Step for RpcOutputStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        let _ = input;
        Ok(StepDecision::graceful_complete(2_i32))
    }
}

struct RpcNoStateWorkflow {
    counter: Attribute<i32>,
}

impl RpcNoStateWorkflow {
    const INCREASE_COUNTER: Rpc<(), i32> = Rpc::new("increase_counter");
    const GET_COUNTER: Rpc<(), Option<i32>> = Rpc::new("get_counter");
    const FAIL: Rpc<String, i64> = Rpc::new("fail");
    const INVOKE: Rpc<String, i64> = Rpc::new("invoke");

    fn increase_counter(&self, context: &mut Context) -> HandlerResult<RpcResult<i32>> {
        let next = self.counter.get(context)?.unwrap_or_default() + 1;
        self.counter.set(context, next)?;
        Ok(RpcResult::new(next))
    }

    fn get_counter(&self, context: &mut Context) -> HandlerResult<RpcResult<Option<i32>>> {
        Ok(RpcResult::new(self.counter.get(context)?))
    }

    fn fail(&self, _context: &mut Context, input: String) -> HandlerResult<RpcResult<i64>> {
        Err(HandlerError::new(input))
    }

    fn invoke(&self, context: &mut Context, _input: String) -> HandlerResult<RpcResult<i64>> {
        if context.flow_id().is_empty() || context.run_id().is_empty() {
            return Err(HandlerError::new("invalid RPC context"));
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

fn compile_rpc_test(client: &Client) -> SdkResult<()> {
    let workflow = RpcWorkflow::new();
    client.start_flow(&workflow, "rpc", 0)?;
    client.invoke_rpc_without_input("rpc", RpcWorkflow::NO_PERSISTENCE)?;
    let output: i64 = client.invoke_rpc("rpc", RpcWorkflow::FUNCTION_ONE, "input".into())?;
    assert_eq!(RpcWorkflow::RPC_OUTPUT, output);
    let output: i64 = client.invoke_rpc_without_input("rpc", RpcWorkflow::FUNCTION_ZERO)?;
    assert_eq!(RpcWorkflow::RPC_OUTPUT, output);
    client.invoke_rpc("rpc", RpcWorkflow::PROCEDURE_ONE, "value".into())?;
    client.invoke_rpc_without_input("rpc", RpcWorkflow::PROCEDURE_ZERO)?;
    let read_only: i64 = client.invoke_rpc("rpc", RpcWorkflow::READ_ONLY, "input".into())?;
    assert_eq!(RpcWorkflow::RPC_OUTPUT, read_only);
    client.invoke_rpc("rpc", RpcWorkflow::SET_DATA, Some("data".into()))?;
    let data: Option<String> = client.invoke_rpc_without_input("rpc", RpcWorkflow::GET_DATA)?;
    assert_eq!(Some("data".into()), data);
    client.invoke_rpc("rpc", RpcWorkflow::SET_KEYWORD, Some("keyword".into()))?;
    let keyword: Option<String> =
        client.invoke_rpc_without_input("rpc", RpcWorkflow::GET_KEYWORD)?;
    assert_eq!(Some("keyword".into()), keyword);
    let _: String = client.invoke_rpc("rpc", RpcWorkflow::LOCK_MAP, "map".into())?;
    let completed: i32 = client.wait_for_flow("rpc")?;
    assert_eq!(2, completed);
    Ok(())
}

fn compile_rpc_with_memo_test(client: &Client) -> SdkResult<()> {
    let workflow = RpcWorkflow::new();
    client.start_flow(&workflow, "rpc-memo", 999)?;
    client.invoke_rpc("rpc-memo", RpcWorkflow::SET_DATA, Some("value".into()))?;
    let data: Option<String> =
        client.invoke_rpc_without_input("rpc-memo", RpcWorkflow::GET_DATA)?;
    assert_eq!(Some("value".into()), data);
    client.invoke_rpc("rpc-memo", RpcWorkflow::SET_DATA, None)?;
    let data: Option<String> =
        client.invoke_rpc_without_input("rpc-memo", RpcWorkflow::GET_DATA)?;
    assert_eq!(None, data);
    client.invoke_rpc("rpc-memo", RpcWorkflow::SET_KEYWORD, Some("keyword".into()))?;
    let keyword: Option<String> =
        client.invoke_rpc_without_input("rpc-memo", RpcWorkflow::GET_KEYWORD)?;
    assert_eq!(Some("keyword".into()), keyword);
    Ok(())
}

fn compile_rpc_without_steps_test(client: &Client) -> SdkResult<()> {
    let _workflow = RpcNoStateWorkflow {
        counter: Attribute::new("counter"),
    };
    let next: i32 =
        client.invoke_rpc_without_input("rpc-no-state", RpcNoStateWorkflow::INCREASE_COUNTER)?;
    let current: Option<i32> =
        client.invoke_rpc_without_input("rpc-no-state", RpcNoStateWorkflow::GET_COUNTER)?;
    assert_eq!(Some(next), current);
    let _: i64 = client.invoke_rpc(
        "rpc-no-state",
        RpcNoStateWorkflow::FAIL,
        "this is an error".into(),
    )?;
    let invoked: i64 =
        client.invoke_rpc("rpc-no-state", RpcNoStateWorkflow::INVOKE, "input".into())?;
    assert_eq!(RpcWorkflow::RPC_OUTPUT, invoked);
    Ok(())
}
