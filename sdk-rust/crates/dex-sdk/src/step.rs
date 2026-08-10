// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::marker::PhantomData;

use dex_protocol::dex::Value as ProtoValue;

use crate::channel::ChannelGuard;
use crate::step_options::ErasedStepOptions;
use crate::value_mapper;
use crate::{Context, HandlerResult, SdkResult, StepOptions, Value, Wait, short_type_name};

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

pub struct StepList<'a, StartInput> {
    definitions: Vec<StepDefinition<'a>>,
    marker: PhantomData<fn(StartInput)>,
}

impl<'a, StartInput: Value> StepList<'a, StartInput> {
    pub fn empty() -> Self {
        Self {
            definitions: Vec::new(),
            marker: PhantomData,
        }
    }

    pub fn start<StartStep>(step: &'a StartStep) -> Self
    where
        StartStep: Step<Input = StartInput>,
    {
        Self {
            definitions: vec![StepDefinition::new(step, true)],
            marker: PhantomData,
        }
    }

    pub fn and<OtherStep>(mut self, step: &'a OtherStep) -> Self
    where
        OtherStep: Step,
    {
        self.definitions.push(StepDefinition::new(step, false));
        self
    }

    pub(crate) fn into_definitions(self) -> Vec<RegisteredStep> {
        self.definitions
            .into_iter()
            .map(|definition| RegisteredStep {
                name: definition.name,
                starting: definition.starting,
            })
            .collect()
    }

    pub(crate) fn find(&self, name: &str) -> Option<&dyn ErasedStep> {
        self.definitions
            .iter()
            .find(|definition| definition.name == name)
            .map(|definition| definition.handler)
    }
}

impl<StartInput: Value> Default for StepList<'_, StartInput> {
    fn default() -> Self {
        Self::empty()
    }
}

pub struct StepMovement {
    pub(crate) target_step_type: &'static str,
    input: Box<dyn ErasedValue>,
    pub(crate) options_override: Option<ErasedStepOptions>,
}

impl StepMovement {
    pub fn to<TargetStep>(step: &TargetStep, input: TargetStep::Input) -> Self
    where
        TargetStep: Step,
    {
        Self {
            target_step_type: step.step_type(),
            input: Box::new(TypedValue(input)),
            options_override: None,
        }
    }

    pub fn to_with_options<TargetStep>(
        step: &TargetStep,
        input: TargetStep::Input,
        options: StepOptions<TargetStep::Input>,
    ) -> Self
    where
        TargetStep: Step,
    {
        Self {
            target_step_type: step.step_type(),
            input: Box::new(TypedValue(input)),
            options_override: Some(options.into()),
        }
    }

    pub(crate) fn encode_input(&self) -> SdkResult<ProtoValue> {
        self.input.encode()
    }
}

pub struct StepDecision {
    pub(crate) kind: StepDecisionKind,
}

pub(crate) enum StepDecisionKind {
    Next(Vec<StepMovement>),
    GracefulComplete(Box<dyn ErasedValue>),
    ForceComplete(Box<dyn ErasedValue>),
    ForceCompleteWhenChannelsEmpty {
        output: Box<dyn ErasedValue>,
        fallback: Box<StepMovement>,
        channels: Vec<ChannelGuard>,
    },
    ForceFail(String),
    DeadEnd,
}

impl StepDecision {
    pub fn go_to<TargetStep>(step: &TargetStep, input: TargetStep::Input) -> Self
    where
        TargetStep: Step,
    {
        Self::go_to_many([StepMovement::to(step, input)])
    }

    pub fn go_to_many(movements: impl IntoIterator<Item = StepMovement>) -> Self {
        Self {
            kind: StepDecisionKind::Next(movements.into_iter().collect()),
        }
    }

    pub fn graceful_complete<Output: Value>(output: Output) -> Self {
        Self {
            kind: StepDecisionKind::GracefulComplete(Box::new(TypedValue(output))),
        }
    }

    pub fn force_complete<Output: Value>(output: Output) -> Self {
        Self {
            kind: StepDecisionKind::ForceComplete(Box::new(TypedValue(output))),
        }
    }

    pub fn force_complete_when_channels_empty<Output: Value>(
        output: Output,
        fallback: StepMovement,
        channels: impl IntoIterator<Item = ChannelGuard>,
    ) -> Self {
        Self {
            kind: StepDecisionKind::ForceCompleteWhenChannelsEmpty {
                output: Box::new(TypedValue(output)),
                fallback: Box::new(fallback),
                channels: channels.into_iter().collect(),
            },
        }
    }

    pub fn force_fail(reason: impl Into<String>) -> Self {
        Self {
            kind: StepDecisionKind::ForceFail(reason.into()),
        }
    }

    pub fn dead_end() -> Self {
        Self {
            kind: StepDecisionKind::DeadEnd,
        }
    }
}

#[derive(Clone)]
pub(crate) struct RegisteredStep {
    pub(crate) name: &'static str,
    pub(crate) starting: bool,
}

struct StepDefinition<'a> {
    name: &'static str,
    starting: bool,
    handler: &'a dyn ErasedStep,
}

impl<'a> StepDefinition<'a> {
    fn new<SomeStep>(step: &'a SomeStep, starting: bool) -> Self
    where
        SomeStep: Step,
    {
        let name = step.step_type();
        Self {
            name,
            starting,
            handler: step,
        }
    }
}

pub(crate) trait ErasedStep: Send + Sync {
    fn wait_for(&self, context: &mut Context, input: &ProtoValue) -> HandlerResult<Wait>;
    fn execute(&self, context: &mut Context, input: &ProtoValue) -> HandlerResult<StepDecision>;
    fn options(&self) -> ErasedStepOptions;
}

impl<SomeStep> ErasedStep for SomeStep
where
    SomeStep: Step,
{
    fn wait_for(&self, context: &mut Context, input: &ProtoValue) -> HandlerResult<Wait> {
        Step::wait_for(self, context, value_mapper::decode_handler(input)?)
    }

    fn execute(&self, context: &mut Context, input: &ProtoValue) -> HandlerResult<StepDecision> {
        Step::execute(self, context, value_mapper::decode_handler(input)?)
    }

    fn options(&self) -> ErasedStepOptions {
        Step::options(self).into()
    }
}

pub(crate) trait ErasedValue: Send + Sync {
    fn encode(&self) -> SdkResult<ProtoValue>;
}

pub(crate) struct TypedValue<T>(pub(crate) T);

impl<T: Value> ErasedValue for TypedValue<T> {
    fn encode(&self) -> SdkResult<ProtoValue> {
        value_mapper::encode(&self.0)
    }
}
