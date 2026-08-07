// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::marker::PhantomData;

use serde::Serialize;
use serde::de::DeserializeOwned;

use crate::{Context, HandlerResult, PersistenceSchema, RpcList, StepOptions, Wait};

pub trait Value: DeserializeOwned + Send + Serialize + Sync + 'static {}

impl<T> Value for T where T: DeserializeOwned + Send + Serialize + Sync + 'static {}

pub trait Flow: Send + Sync + 'static {
    type StartInput: Value;

    fn flow_type(&self) -> &'static str {
        short_type_name::<Self>()
    }

    fn steps(&self) -> StepList<Self::StartInput> {
        StepList::empty()
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
    }

    fn rpcs(&self) -> RpcList<Self>
    where
        Self: Sized,
    {
        RpcList::new()
    }
}

pub trait Step: Send + Sync + 'static {
    type Input: Value;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision>;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::skip_immediately())
    }

    fn step_type(&self) -> &'static str {
        short_type_name::<Self>()
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StepList<StartInput> {
    definitions: Vec<StepDefinition>,
    marker: PhantomData<fn(StartInput)>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
struct StepDefinition {
    step_type: &'static str,
    is_start: bool,
}

impl<StartInput: Value> StepList<StartInput> {
    pub fn empty() -> Self {
        Self {
            definitions: Vec::new(),
            marker: PhantomData,
        }
    }

    pub fn start<StartStep>(step: &StartStep) -> Self
    where
        StartStep: Step<Input = StartInput>,
    {
        Self {
            definitions: vec![StepDefinition {
                step_type: step.step_type(),
                is_start: true,
            }],
            marker: PhantomData,
        }
    }

    pub fn and<OtherStep>(mut self, step: &OtherStep) -> Self
    where
        OtherStep: Step,
    {
        self.definitions.push(StepDefinition {
            step_type: step.step_type(),
            is_start: false,
        });
        self
    }
}

impl<StartInput: Value> Default for StepList<StartInput> {
    fn default() -> Self {
        Self::empty()
    }
}

pub struct StepMovement {
    target_step_type: &'static str,
    has_options_override: bool,
}

impl StepMovement {
    pub fn to<TargetStep>(step: &TargetStep, _input: TargetStep::Input) -> Self
    where
        TargetStep: Step,
    {
        Self {
            target_step_type: step.step_type(),
            has_options_override: false,
        }
    }

    pub fn to_with_options<TargetStep>(
        step: &TargetStep,
        _input: TargetStep::Input,
        _options: StepOptions<TargetStep::Input>,
    ) -> Self
    where
        TargetStep: Step,
    {
        Self {
            target_step_type: step.step_type(),
            has_options_override: true,
        }
    }
}

pub struct StepDecision {
    _private: (),
}

impl StepDecision {
    pub fn go_to<TargetStep>(step: &TargetStep, input: TargetStep::Input) -> Self
    where
        TargetStep: Step,
    {
        Self::go_to_many([StepMovement::to(step, input)])
    }

    pub fn go_to_many(movements: impl IntoIterator<Item = StepMovement>) -> Self {
        for movement in movements {
            let _ = movement.target_step_type;
            let _ = movement.has_options_override;
        }
        Self { _private: () }
    }

    pub fn graceful_complete<Output: Value>(_output: Output) -> Self {
        Self { _private: () }
    }

    pub fn force_complete<Output: Value>(_output: Output) -> Self {
        Self { _private: () }
    }

    pub fn force_complete_when_channels_empty<Output: Value>(
        _output: Output,
        _fallback: StepMovement,
        _channels: impl IntoIterator<Item = crate::state::ChannelGuard>,
    ) -> Self {
        Self { _private: () }
    }

    pub fn force_fail(_reason: impl Into<String>) -> Self {
        Self { _private: () }
    }

    pub fn dead_end() -> Self {
        Self { _private: () }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StepExecutionId {
    step_type: &'static str,
    execution_number: u32,
}

impl StepExecutionId {
    pub fn of<SomeStep: Step>(step: &SomeStep) -> Self {
        Self {
            step_type: step.step_type(),
            execution_number: 1,
        }
    }

    pub fn execution_number(mut self, execution_number: u32) -> Self {
        self.execution_number = execution_number;
        self
    }
}

fn short_type_name<T: ?Sized>() -> &'static str {
    std::any::type_name::<T>()
        .rsplit("::")
        .next()
        .expect("Rust type names are non-empty")
}
