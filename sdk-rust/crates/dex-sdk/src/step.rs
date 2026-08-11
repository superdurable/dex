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

/// Defines one durable state transition in a Flow.
///
/// Dex calls [`Self::wait_for`] to obtain durable conditions, then calls [`Self::execute`] after the
/// wait is satisfied. The default `wait_for` skips immediately, making execute-only Steps concise.
/// Handler code may read and stage persistence changes through [`Context`].
///
/// # Examples
///
/// ```
/// use dex_sdk::{Context, HandlerResult, Step, StepDecision};
///
/// struct CompleteOrder;
///
/// impl Step for CompleteOrder {
///     type Input = String;
///
///     fn execute(
///         &self,
///         _context: &mut Context,
///         order_id: Self::Input,
///     ) -> HandlerResult<StepDecision> {
///         Ok(StepDecision::graceful_complete(order_id))
///     }
/// }
/// ```
pub trait Step: Send + Sync + 'static {
    /// Value decoded for both `wait_for` and `execute`.
    type Input: Value;

    /// Produces the next movement or terminal Flow decision.
    ///
    /// `context` represents one isolated invocation and `input` is a newly decoded owned value.
    /// Returning [`HandlerError`](crate::HandlerError) activates the configured execute retry and
    /// failure behavior.
    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision>;

    /// Returns the durable conditions Dex must satisfy before `execute`.
    ///
    /// The default returns [`Wait::skip_immediately`]. Returning an error activates the configured
    /// `wait_for` retry and failure behavior.
    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::skip_immediately())
    }

    /// Returns the stable Step type sent to Dex.
    ///
    /// The default is the final Rust type-name segment.
    fn step_type(&self) -> &'static str {
        short_type_name::<Self>()
    }

    /// Returns timeouts, retries, locks, durability, and failure behavior for this Step.
    ///
    /// The default preserves server defaults and fails the Flow when `wait_for` exhausts retries.
    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
    }
}

/// Collects a Flow's starting Step and remaining Step definitions.
///
/// The lifetime ties registered definitions to Step values owned by the Flow. A list may contain at
/// most one starting Step; [`Self::and`] adds non-starting Steps with arbitrary input types.
pub struct StepList<'a, StartInput> {
    definitions: Vec<StepDefinition<'a>>,
    marker: PhantomData<fn(StartInput)>,
}

impl<'a, StartInput: Value> StepList<'a, StartInput> {
    /// Creates a list without a starting Step.
    pub fn empty() -> Self {
        Self {
            definitions: Vec::new(),
            marker: PhantomData,
        }
    }

    /// Creates a list whose starting Step accepts the Flow's start input.
    pub fn start<StartStep>(step: &'a StartStep) -> Self
    where
        StartStep: Step<Input = StartInput>,
    {
        Self {
            definitions: vec![StepDefinition::new(step, true)],
            marker: PhantomData,
        }
    }

    /// Adds a non-starting Step and returns the updated list.
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

/// Targets a Step with encoded input and an optional per-movement options override.
///
/// Create movements with [`Self::to`] or [`Self::to_with_options`], then pass them to
/// [`StepDecision::go_to_many`], [`crate::RpcResult::then`], or conditional completion.
pub struct StepMovement {
    pub(crate) target_step_type: &'static str,
    input: Box<dyn ErasedValue>,
    pub(crate) options_override: Option<ErasedStepOptions>,
}

impl StepMovement {
    /// Targets `step` with typed `input` and the Step's registered options.
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

    /// Targets `step` with typed input and options used only for this movement.
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

/// Describes the durable outcome of one successful Step execution.
///
/// A decision moves to one or more Steps, completes or fails the Flow, waits for Channels to empty,
/// or deliberately leaves the Flow at a dead end.
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
    /// Moves to one typed target Step.
    pub fn go_to<TargetStep>(step: &TargetStep, input: TargetStep::Input) -> Self
    where
        TargetStep: Step,
    {
        Self::go_to_many([StepMovement::to(step, input)])
    }

    /// Moves to all targets, enabling concurrent active Steps.
    pub fn go_to_many(movements: impl IntoIterator<Item = StepMovement>) -> Self {
        Self {
            kind: StepDecisionKind::Next(movements.into_iter().collect()),
        }
    }

    /// Completes only after all other active Steps finish, returning `output`.
    pub fn graceful_complete<Output: Value>(output: Output) -> Self {
        Self {
            kind: StepDecisionKind::GracefulComplete(Box::new(TypedValue(output))),
        }
    }

    /// Completes immediately, abandoning other active Steps and returning `output`.
    pub fn force_complete<Output: Value>(output: Output) -> Self {
        Self {
            kind: StepDecisionKind::ForceComplete(Box::new(TypedValue(output))),
        }
    }

    /// Completes with `output` when every guard is empty; otherwise follows `fallback`.
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

    /// Fails the Flow immediately with an application-provided reason.
    pub fn force_fail(reason: impl Into<String>) -> Self {
        Self {
            kind: StepDecisionKind::ForceFail(reason.into()),
        }
    }

    /// Leaves the Flow running with no next Step.
    ///
    /// Use this only when an RPC or external operation will resume the Flow later.
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
