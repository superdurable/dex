// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::marker::PhantomData;
use std::sync::Arc;
use std::time::Duration;

use dex_protocol::dex::Value as ProtoValue;

use crate::attribute::AttributeLock;
use crate::step::{ErasedValue, TypedValue};
use crate::value_mapper;
use crate::{Context, HandlerResult, SdkResult, StepMovement, Value};

pub struct Rpc<Input, Output> {
    name: &'static str,
    marker: PhantomData<fn(Input) -> Output>,
}

impl<Input, Output> Rpc<Input, Output> {
    pub const fn new(name: &'static str) -> Self {
        Self {
            name,
            marker: PhantomData,
        }
    }

    pub fn timeout(self, timeout: Duration) -> RpcDefinition<Input, Output> {
        RpcDefinition::from(self).timeout(timeout)
    }

    pub fn lock(self, lock: AttributeLock) -> RpcDefinition<Input, Output> {
        RpcDefinition::from(self).lock(lock)
    }

    pub(crate) fn name(&self) -> &'static str {
        self.name
    }
}

impl<Input, Output> Clone for Rpc<Input, Output> {
    fn clone(&self) -> Self {
        *self
    }
}

impl<Input, Output> Copy for Rpc<Input, Output> {}

pub struct RpcDefinition<Input, Output> {
    rpc: Rpc<Input, Output>,
    timeout: Option<Duration>,
    locks: Vec<AttributeLock>,
}

impl<Input, Output> RpcDefinition<Input, Output> {
    pub fn timeout(mut self, timeout: Duration) -> Self {
        self.timeout = Some(timeout);
        self
    }

    pub fn lock(mut self, lock: AttributeLock) -> Self {
        self.locks.push(lock);
        self
    }
}

impl<Input, Output> From<Rpc<Input, Output>> for RpcDefinition<Input, Output> {
    fn from(rpc: Rpc<Input, Output>) -> Self {
        Self {
            rpc,
            timeout: None,
            locks: Vec::new(),
        }
    }
}

pub struct RpcList<FlowType> {
    definitions: Vec<Box<dyn RpcBinder<FlowType>>>,
}

impl<FlowType> RpcList<FlowType>
where
    FlowType: Send + Sync + 'static,
{
    pub fn new() -> Self {
        Self {
            definitions: Vec::new(),
        }
    }

    pub fn function<Input, Output>(
        mut self,
        definition: impl Into<RpcDefinition<Input, Output>>,
        handler: fn(&FlowType, &mut Context, Input) -> HandlerResult<RpcResult<Output>>,
    ) -> Self
    where
        Input: Value,
        Output: Value,
    {
        self.definitions.push(Box::new(FunctionBinder {
            definition: definition.into(),
            handler,
        }));
        self
    }

    pub fn function_without_input<Output>(
        mut self,
        definition: impl Into<RpcDefinition<(), Output>>,
        handler: fn(&FlowType, &mut Context) -> HandlerResult<RpcResult<Output>>,
    ) -> Self
    where
        Output: Value,
    {
        self.definitions.push(Box::new(FunctionWithoutInputBinder {
            definition: definition.into(),
            handler,
        }));
        self
    }

    pub fn procedure<Input>(
        mut self,
        definition: impl Into<RpcDefinition<Input, ()>>,
        handler: fn(&FlowType, &mut Context, Input) -> HandlerResult<()>,
    ) -> Self
    where
        Input: Value,
    {
        self.definitions.push(Box::new(ProcedureBinder {
            definition: definition.into(),
            handler,
        }));
        self
    }

    pub fn procedure_without_input(
        mut self,
        definition: impl Into<RpcDefinition<(), ()>>,
        handler: fn(&FlowType, &mut Context) -> HandlerResult<()>,
    ) -> Self {
        self.definitions.push(Box::new(ProcedureWithoutInputBinder {
            definition: definition.into(),
            handler,
        }));
        self
    }

    pub(crate) fn bind(self, flow: Arc<FlowType>) -> Vec<RegisteredRpc> {
        self.definitions
            .into_iter()
            .map(|definition| definition.bind(Arc::clone(&flow)))
            .collect()
    }
}

impl<FlowType> Default for RpcList<FlowType>
where
    FlowType: Send + Sync + 'static,
{
    fn default() -> Self {
        Self::new()
    }
}

pub struct RpcResult<Output> {
    output: Output,
    next_steps: Vec<StepMovement>,
}

impl<Output: Value> RpcResult<Output> {
    pub fn new(output: Output) -> Self {
        Self {
            output,
            next_steps: Vec::new(),
        }
    }

    pub fn then(mut self, movement: StepMovement) -> Self {
        self.next_steps.push(movement);
        self
    }

    pub fn output(&self) -> &Output {
        &self.output
    }

    pub fn next_steps(&self) -> &[StepMovement] {
        &self.next_steps
    }
}

pub(crate) struct ErasedRpcResult {
    pub(crate) output: Box<dyn ErasedValue>,
    pub(crate) next_steps: Vec<StepMovement>,
}

#[derive(Clone)]
pub(crate) struct RegisteredRpc {
    pub(crate) name: &'static str,
    pub(crate) timeout: Option<Duration>,
    pub(crate) locks: Vec<AttributeLock>,
    pub(crate) handler: Arc<dyn ErasedRpc>,
}

pub(crate) trait ErasedRpc: Send + Sync {
    fn invoke(&self, context: &mut Context, input: &ProtoValue) -> HandlerResult<ErasedRpcResult>;
}

trait RpcBinder<FlowType>: Send + Sync {
    fn bind(self: Box<Self>, flow: Arc<FlowType>) -> RegisteredRpc;
}

struct FunctionBinder<FlowType, Input, Output> {
    definition: RpcDefinition<Input, Output>,
    handler: fn(&FlowType, &mut Context, Input) -> HandlerResult<RpcResult<Output>>,
}

impl<FlowType, Input, Output> RpcBinder<FlowType> for FunctionBinder<FlowType, Input, Output>
where
    FlowType: Send + Sync + 'static,
    Input: Value,
    Output: Value,
{
    fn bind(self: Box<Self>, flow: Arc<FlowType>) -> RegisteredRpc {
        registered_rpc(
            self.definition,
            Arc::new(BoundFunction {
                flow,
                handler: self.handler,
            }),
        )
    }
}

struct BoundFunction<FlowType, Input, Output> {
    flow: Arc<FlowType>,
    handler: fn(&FlowType, &mut Context, Input) -> HandlerResult<RpcResult<Output>>,
}

impl<FlowType, Input, Output> ErasedRpc for BoundFunction<FlowType, Input, Output>
where
    FlowType: Send + Sync + 'static,
    Input: Value,
    Output: Value,
{
    fn invoke(&self, context: &mut Context, input: &ProtoValue) -> HandlerResult<ErasedRpcResult> {
        let result = (self.handler)(&self.flow, context, value_mapper::decode_handler(input)?)?;
        Ok(ErasedRpcResult {
            output: Box::new(TypedValue(result.output)),
            next_steps: result.next_steps,
        })
    }
}

struct FunctionWithoutInputBinder<FlowType, Output> {
    definition: RpcDefinition<(), Output>,
    handler: fn(&FlowType, &mut Context) -> HandlerResult<RpcResult<Output>>,
}

impl<FlowType, Output> RpcBinder<FlowType> for FunctionWithoutInputBinder<FlowType, Output>
where
    FlowType: Send + Sync + 'static,
    Output: Value,
{
    fn bind(self: Box<Self>, flow: Arc<FlowType>) -> RegisteredRpc {
        registered_rpc(
            self.definition,
            Arc::new(BoundFunctionWithoutInput {
                flow,
                handler: self.handler,
            }),
        )
    }
}

struct BoundFunctionWithoutInput<FlowType, Output> {
    flow: Arc<FlowType>,
    handler: fn(&FlowType, &mut Context) -> HandlerResult<RpcResult<Output>>,
}

impl<FlowType, Output> ErasedRpc for BoundFunctionWithoutInput<FlowType, Output>
where
    FlowType: Send + Sync + 'static,
    Output: Value,
{
    fn invoke(&self, context: &mut Context, _input: &ProtoValue) -> HandlerResult<ErasedRpcResult> {
        let result = (self.handler)(&self.flow, context)?;
        Ok(ErasedRpcResult {
            output: Box::new(TypedValue(result.output)),
            next_steps: result.next_steps,
        })
    }
}

struct ProcedureBinder<FlowType, Input> {
    definition: RpcDefinition<Input, ()>,
    handler: fn(&FlowType, &mut Context, Input) -> HandlerResult<()>,
}

impl<FlowType, Input> RpcBinder<FlowType> for ProcedureBinder<FlowType, Input>
where
    FlowType: Send + Sync + 'static,
    Input: Value,
{
    fn bind(self: Box<Self>, flow: Arc<FlowType>) -> RegisteredRpc {
        registered_rpc(
            self.definition,
            Arc::new(BoundProcedure {
                flow,
                handler: self.handler,
            }),
        )
    }
}

struct BoundProcedure<FlowType, Input> {
    flow: Arc<FlowType>,
    handler: fn(&FlowType, &mut Context, Input) -> HandlerResult<()>,
}

impl<FlowType, Input> ErasedRpc for BoundProcedure<FlowType, Input>
where
    FlowType: Send + Sync + 'static,
    Input: Value,
{
    fn invoke(&self, context: &mut Context, input: &ProtoValue) -> HandlerResult<ErasedRpcResult> {
        (self.handler)(&self.flow, context, value_mapper::decode_handler(input)?)?;
        Ok(empty_rpc_result())
    }
}

struct ProcedureWithoutInputBinder<FlowType> {
    definition: RpcDefinition<(), ()>,
    handler: fn(&FlowType, &mut Context) -> HandlerResult<()>,
}

impl<FlowType> RpcBinder<FlowType> for ProcedureWithoutInputBinder<FlowType>
where
    FlowType: Send + Sync + 'static,
{
    fn bind(self: Box<Self>, flow: Arc<FlowType>) -> RegisteredRpc {
        registered_rpc(
            self.definition,
            Arc::new(BoundProcedureWithoutInput {
                flow,
                handler: self.handler,
            }),
        )
    }
}

struct BoundProcedureWithoutInput<FlowType> {
    flow: Arc<FlowType>,
    handler: fn(&FlowType, &mut Context) -> HandlerResult<()>,
}

impl<FlowType> ErasedRpc for BoundProcedureWithoutInput<FlowType>
where
    FlowType: Send + Sync + 'static,
{
    fn invoke(&self, context: &mut Context, _input: &ProtoValue) -> HandlerResult<ErasedRpcResult> {
        (self.handler)(&self.flow, context)?;
        Ok(empty_rpc_result())
    }
}

fn registered_rpc<Input, Output>(
    definition: RpcDefinition<Input, Output>,
    handler: Arc<dyn ErasedRpc>,
) -> RegisteredRpc {
    RegisteredRpc {
        name: definition.rpc.name,
        timeout: definition.timeout,
        locks: definition.locks,
        handler,
    }
}

fn empty_rpc_result() -> ErasedRpcResult {
    ErasedRpcResult {
        output: Box::new(TypedValue(())),
        next_steps: Vec::new(),
    }
}

pub(crate) fn encode_rpc_output(output: &dyn ErasedValue) -> SdkResult<ProtoValue> {
    output.encode()
}
