// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::marker::PhantomData;
use std::time::Duration;

use crate::{AttributeLock, Context, HandlerResult, StepMovement, Value};

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

    pub fn with_options(self, options: RpcOptions) -> RpcDefinition<Input, Output> {
        RpcDefinition { rpc: self, options }
    }
}

impl<Input, Output> Clone for Rpc<Input, Output> {
    fn clone(&self) -> Self {
        *self
    }
}

impl<Input, Output> Copy for Rpc<Input, Output> {}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct RpcOptions {
    pub timeout: Option<Duration>,
    pub locks: Vec<AttributeLock>,
}

impl RpcOptions {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn timeout(mut self, value: Duration) -> Self {
        self.timeout = Some(value);
        self
    }

    pub fn lock(mut self, value: AttributeLock) -> Self {
        self.locks.push(value);
        self
    }
}

pub struct RpcDefinition<Input, Output> {
    rpc: Rpc<Input, Output>,
    options: RpcOptions,
}

impl<Input, Output> From<Rpc<Input, Output>> for RpcDefinition<Input, Output> {
    fn from(rpc: Rpc<Input, Output>) -> Self {
        Self {
            rpc,
            options: RpcOptions::new(),
        }
    }
}

pub struct RpcList<FlowType> {
    names: Vec<&'static str>,
    marker: PhantomData<fn(&FlowType)>,
}

impl<FlowType> RpcList<FlowType> {
    pub fn new() -> Self {
        Self {
            names: Vec::new(),
            marker: PhantomData,
        }
    }

    pub fn function<Input, Output>(
        mut self,
        definition: impl Into<RpcDefinition<Input, Output>>,
        _handler: fn(&FlowType, &mut Context, Input) -> HandlerResult<RpcResult<Output>>,
    ) -> Self
    where
        Input: Value,
        Output: Value,
    {
        self.register(definition.into());
        self
    }

    pub fn function_without_input<Output>(
        mut self,
        definition: impl Into<RpcDefinition<(), Output>>,
        _handler: fn(&FlowType, &mut Context) -> HandlerResult<RpcResult<Output>>,
    ) -> Self
    where
        Output: Value,
    {
        self.register(definition.into());
        self
    }

    pub fn procedure<Input>(
        mut self,
        definition: impl Into<RpcDefinition<Input, ()>>,
        _handler: fn(&FlowType, &mut Context, Input) -> HandlerResult<()>,
    ) -> Self
    where
        Input: Value,
    {
        self.register(definition.into());
        self
    }

    pub fn procedure_without_input(
        mut self,
        definition: impl Into<RpcDefinition<(), ()>>,
        _handler: fn(&FlowType, &mut Context) -> HandlerResult<()>,
    ) -> Self {
        self.register(definition.into());
        self
    }

    fn register<Input, Output>(&mut self, definition: RpcDefinition<Input, Output>) {
        let _ = definition.options;
        self.names.push(definition.rpc.name);
    }
}

impl<FlowType> Default for RpcList<FlowType> {
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
