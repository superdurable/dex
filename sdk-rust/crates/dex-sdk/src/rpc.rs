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

use crate::attribute::{AttributeLock, AttributeMap, AttributeMapLoad};
use crate::channel::{Channel, ChannelLoad, ChannelMap, ChannelMapLoad};
use crate::step::{ErasedValue, TypedValue};
use crate::value_mapper;
use crate::{Context, HandlerResult, SdkResult, StepMovement, Value};

/// Names one typed RPC exposed by a Flow.
///
/// Use the same value when binding the handler in [`RpcList`] and invoking it through
/// [`crate::Client`]. Input and output types must implement [`Value`]. The stable name becomes part
/// of the protocol and must remain compatible with running Flows.
///
/// # Examples
///
/// ```
/// use dex_sdk::{Context, Flow, HandlerResult, Rpc, RpcList, RpcResult};
///
/// const STATUS: Rpc<(), String> = Rpc::new("status");
/// struct OrderFlow;
///
/// impl OrderFlow {
///     fn status(&self, _context: &mut Context) -> HandlerResult<RpcResult<String>> {
///         Ok(RpcResult::new("ready".to_owned()))
///     }
/// }
///
/// impl Flow for OrderFlow {
///     type StartInput = ();
///
///     fn rpcs(&self) -> RpcList<Self> {
///         RpcList::new().function_without_input(STATUS, Self::status)
///     }
/// }
/// ```
pub struct Rpc<Input, Output> {
    name: &'static str,
    marker: PhantomData<fn(Input) -> Output>,
}

impl<Input, Output> Rpc<Input, Output> {
    /// Defines an RPC with a stable protocol `name` and no option overrides.
    pub const fn new(name: &'static str) -> Self {
        Self {
            name,
            marker: PhantomData,
        }
    }

    /// Converts this RPC into a definition with a handler execution timeout.
    pub fn timeout(self, timeout: Duration) -> RpcDefinition<Input, Output> {
        RpcDefinition::from(self).timeout(timeout)
    }

    /// Converts this RPC into a definition holding one Attribute lock during invocation.
    pub fn lock(self, lock: AttributeLock) -> RpcDefinition<Input, Output> {
        RpcDefinition::from(self).lock(lock)
    }

    /// Requests transactional reads and writes for this RPC.
    pub fn is_transactional(self) -> RpcDefinition<Input, Output> {
        RpcDefinition::from(self).is_transactional()
    }

    /// Converts this RPC into a definition that loads every current AttributeMap instance.
    pub fn load_attribute_map<T>(
        self,
        attribute_map: &AttributeMap<T>,
    ) -> RpcDefinition<Input, Output> {
        RpcDefinition::from(self).load_attribute_map(attribute_map)
    }

    /// Converts this RPC into a definition that loads one AttributeMap instance.
    pub fn load_attribute_map_instance(
        self,
        load: AttributeMapLoad,
    ) -> RpcDefinition<Input, Output> {
        RpcDefinition::from(self).load_attribute_map_instance(load)
    }

    /// Converts this RPC into a definition that loads one singleton Channel's messages.
    pub fn load_channel<T>(self, channel: &Channel<T>) -> RpcDefinition<Input, Output> {
        RpcDefinition::from(self).load_channel(channel)
    }

    /// Converts this RPC into a definition that loads every current ChannelMap instance.
    pub fn load_channel_map<T>(self, channel_map: &ChannelMap<T>) -> RpcDefinition<Input, Output> {
        RpcDefinition::from(self).load_channel_map(channel_map)
    }

    /// Converts this RPC into a definition that loads one ChannelMap instance.
    pub fn load_channel_map_instance(self, load: ChannelMapLoad) -> RpcDefinition<Input, Output> {
        RpcDefinition::from(self).load_channel_map_instance(load)
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

/// Adds execution options to a typed [`Rpc`] before handler binding.
///
/// Definitions are created implicitly from `Rpc` or explicitly through [`Rpc::timeout`] and
/// [`Rpc::lock`]. Multiple locks are acquired together for the invocation.
pub struct RpcDefinition<Input, Output> {
    rpc: Rpc<Input, Output>,
    timeout: Option<Duration>,
    locks: Vec<AttributeLock>,
    is_transactional: bool,
    load_attribute_maps: Vec<AttributeMapLoad>,
    load_channels: Vec<ChannelLoad>,
    load_channel_maps: Vec<ChannelMapLoad>,
}

impl<Input, Output> RpcDefinition<Input, Output> {
    /// Sets the maximum duration of one RPC handler attempt.
    pub fn timeout(mut self, timeout: Duration) -> Self {
        self.timeout = Some(timeout);
        self
    }

    /// Adds an Attribute or Attribute-map instance lock.
    pub fn lock(mut self, lock: AttributeLock) -> Self {
        self.locks.push(lock);
        self
    }

    /// Requests transactional reads and writes even when no Attribute lock is configured.
    pub fn is_transactional(mut self) -> Self {
        self.is_transactional = true;
        self
    }

    /// Loads every current instance of one AttributeMap into the invocation snapshot.
    pub fn load_attribute_map<T>(mut self, attribute_map: &AttributeMap<T>) -> Self {
        self.load_attribute_maps.push(AttributeMapLoad {
            name: attribute_map.name().to_owned(),
            instance: None,
        });
        self
    }

    /// Loads one AttributeMap instance into the invocation snapshot.
    pub fn load_attribute_map_instance(mut self, load: AttributeMapLoad) -> Self {
        self.load_attribute_maps.push(load);
        self
    }

    /// Adds one singleton Channel's pending messages to the invocation snapshot.
    pub fn load_channel<T>(mut self, channel: &Channel<T>) -> Self {
        self.load_channels.push(ChannelLoad {
            name: channel.name().to_owned(),
        });
        self
    }

    /// Loads every current instance of one ChannelMap into the invocation snapshot.
    pub fn load_channel_map<T>(mut self, channel_map: &ChannelMap<T>) -> Self {
        self.load_channel_maps.push(ChannelMapLoad {
            name: channel_map.name().to_owned(),
            instance: None,
        });
        self
    }

    /// Loads one ChannelMap instance into the invocation snapshot.
    pub fn load_channel_map_instance(mut self, load: ChannelMapLoad) -> Self {
        self.load_channel_maps.push(load);
        self
    }
}

impl<Input, Output> From<Rpc<Input, Output>> for RpcDefinition<Input, Output> {
    fn from(rpc: Rpc<Input, Output>) -> Self {
        Self {
            rpc,
            timeout: None,
            locks: Vec::new(),
            is_transactional: false,
            load_attribute_maps: Vec::new(),
            load_channels: Vec::new(),
            load_channel_maps: Vec::new(),
        }
    }
}

/// Binds typed RPC definitions to methods on one Flow type.
///
/// Return the completed list from [`crate::Flow::rpcs`]. Handler signatures encode whether input or
/// output is present, preventing invalid calls at compile time.
pub struct RpcList<FlowType> {
    definitions: Vec<Box<dyn RpcBinder<FlowType>>>,
}

impl<FlowType> RpcList<FlowType>
where
    FlowType: Send + Sync + 'static,
{
    /// Creates an empty RPC list.
    pub fn new() -> Self {
        Self {
            definitions: Vec::new(),
        }
    }

    /// Binds an RPC that accepts typed input and returns typed output plus optional Step movements.
    ///
    /// Handler errors are returned to Dex and follow RPC failure semantics.
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

    /// Binds an RPC without input that returns typed output and optional Step movements.
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

    /// Binds an RPC that accepts typed input and returns no output.
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

    /// Binds an RPC with neither input nor output.
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

/// Carries a typed RPC output and optional Step movements.
///
/// [`Self::then`] may be called repeatedly to schedule multiple active Steps after the RPC commits.
pub struct RpcResult<Output> {
    output: Output,
    next_steps: Vec<StepMovement>,
    cancel_step_types: Vec<&'static str>,
}

impl<Output: Value> RpcResult<Output> {
    /// Creates a result with `output` and no Step movements.
    pub fn new(output: Output) -> Self {
        Self {
            output,
            next_steps: Vec::new(),
            cancel_step_types: Vec::new(),
        }
    }

    /// Appends one Step movement executed after the RPC succeeds.
    pub fn then(mut self, movement: StepMovement) -> Self {
        self.next_steps.push(movement);
        self
    }

    /// Selects every queued or active execution of one registered Step type.
    ///
    /// Dex resolves cancellation after RPC persistence commits and before next Steps are queued.
    /// Finished, already-canceled, and absent executions are no-ops. RPCs cannot select siblings.
    pub fn cancel_step<SelectedStep>(mut self, step: &SelectedStep) -> Self
    where
        SelectedStep: crate::Step,
    {
        let step_type = step.step_type();
        if !self.cancel_step_types.contains(&step_type) {
            self.cancel_step_types.push(step_type);
        }
        self
    }

    /// Returns the typed output by reference.
    pub fn output(&self) -> &Output {
        &self.output
    }

    /// Returns the scheduled Step movements in insertion order.
    pub fn next_steps(&self) -> &[StepMovement] {
        &self.next_steps
    }
}

pub(crate) struct ErasedRpcResult {
    pub(crate) output: Box<dyn ErasedValue>,
    pub(crate) next_steps: Vec<StepMovement>,
    pub(crate) cancel_step_types: Vec<&'static str>,
}

#[derive(Clone)]
pub(crate) struct RegisteredRpc {
    pub(crate) name: &'static str,
    pub(crate) timeout: Option<Duration>,
    pub(crate) locks: Vec<AttributeLock>,
    pub(crate) is_transactional: bool,
    pub(crate) load_attribute_maps: Vec<AttributeMapLoad>,
    pub(crate) load_channels: Vec<ChannelLoad>,
    pub(crate) load_channel_maps: Vec<ChannelMapLoad>,
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
            cancel_step_types: result.cancel_step_types,
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
            cancel_step_types: result.cancel_step_types,
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
        is_transactional: definition.is_transactional,
        load_attribute_maps: definition.load_attribute_maps,
        load_channels: definition.load_channels,
        load_channel_maps: definition.load_channel_maps,
        handler,
    }
}

fn empty_rpc_result() -> ErasedRpcResult {
    ErasedRpcResult {
        output: Box::new(TypedValue(())),
        next_steps: Vec::new(),
        cancel_step_types: Vec::new(),
    }
}

pub(crate) fn encode_rpc_output(output: &dyn ErasedValue) -> SdkResult<ProtoValue> {
    output.encode()
}
