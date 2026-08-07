// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::marker::PhantomData;
use std::time::Duration;

use crate::state::AttributeLock;
use crate::{Context, HandlerResult, StepMovement, Value};

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
        let _ = definition.timeout;
        let _ = definition.locks;
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
