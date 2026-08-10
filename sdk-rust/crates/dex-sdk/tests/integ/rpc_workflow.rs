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
    Attribute, AttributeIndex, AttributeMap, Channel, Context, Flow, HandlerError, HandlerResult,
    PersistenceSchema, Rpc, RpcList, RpcResult, Step, StepDecision, StepList, Wait,
};

pub(crate) struct RpcWorkflow {
    internal: Channel<()>,
    pub(crate) data: Attribute<String>,
    pub(crate) keyword: Attribute<String>,
    pub(crate) integer: Attribute<i32>,
    map: AttributeMap<String>,
    first: FirstStep,
    output: OutputStep,
}

impl RpcWorkflow {
    pub(crate) const RPC_OUTPUT: i64 = 100;
    pub(crate) const HARDCODED_VALUE: &str = "random-string";
    pub(crate) const PUBLISH_WITHOUT_ATTRIBUTE_ACCESS: Rpc<(), ()> =
        Rpc::new("publish_without_attribute_access");
    pub(crate) const FUNCTION_ONE: Rpc<String, i64> = Rpc::new("function_one");
    pub(crate) const FUNCTION_ZERO: Rpc<(), i64> = Rpc::new("function_zero");
    pub(crate) const PROCEDURE_ONE: Rpc<String, ()> = Rpc::new("procedure_one");
    pub(crate) const PROCEDURE_ZERO: Rpc<(), ()> = Rpc::new("procedure_zero");
    pub(crate) const READ_ONLY: Rpc<String, i64> = Rpc::new("read_only");
    pub(crate) const SET_DATA: Rpc<Option<String>, ()> = Rpc::new("set_data");
    pub(crate) const GET_DATA: Rpc<(), Option<String>> = Rpc::new("get_data");
    pub(crate) const SET_KEYWORD: Rpc<Option<String>, ()> = Rpc::new("set_keyword");
    pub(crate) const GET_KEYWORD: Rpc<(), Option<String>> = Rpc::new("get_keyword");

    pub(crate) fn new() -> Self {
        let internal = Channel::new("rpc-internal");
        Self {
            first: FirstStep {
                internal: internal.clone(),
            },
            output: OutputStep,
            internal,
            data: Attribute::new("rpc-data"),
            keyword: Attribute::new("CustomKeywordField").indexed(AttributeIndex::keyword()),
            integer: Attribute::new("CustomIntField").indexed(AttributeIndex::int()),
            map: AttributeMap::new("rpc-map"),
        }
    }

    fn publish_without_attribute_access(&self, context: &mut Context) -> HandlerResult<()> {
        Self::require_context(context)?;
        self.internal.publish(context, ())
    }

    fn function_one(&self, context: &mut Context, input: String) -> HandlerResult<RpcResult<i64>> {
        Self::require_context(context)?;
        self.data.delete(context)?;
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

    fn steps(&self) -> StepList<'_, Self::StartInput> {
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
            .procedure_without_input(
                Self::PUBLISH_WITHOUT_ATTRIBUTE_ACCESS,
                Self::publish_without_attribute_access,
            )
            .function(Self::FUNCTION_ONE, Self::function_one)
            .function_without_input(Self::FUNCTION_ZERO, Self::function_zero)
            .procedure(Self::PROCEDURE_ONE, Self::procedure_one)
            .procedure_without_input(Self::PROCEDURE_ZERO, Self::procedure_zero)
            .function(Self::READ_ONLY, Self::read_only)
            .procedure(Self::SET_DATA, Self::set_data)
            .function_without_input(Self::GET_DATA, Self::get_data)
            .procedure(Self::SET_KEYWORD, Self::set_keyword)
            .function_without_input(Self::GET_KEYWORD, Self::get_keyword)
    }
}

struct FirstStep {
    internal: Channel<()>,
}

impl Step for FirstStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::until(self.internal.for_one()))
    }

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&OutputStep, 0))
    }
}

struct OutputStep;

impl Step for OutputStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(2_i32))
    }
}
