// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::any::TypeId;
use std::time::Duration;

use dex_protocol::dex::WaitingCondition;

use crate::start_flow_options::InitialAttribute;
use crate::wait::SubFlowDefinition;
use crate::{
    Attribute, AttributeMap, Condition, Context, Flow, FlowConfig, FlowResult,
    FlowTimeoutHandlerOptions, FlowTimeoutPolicy, HandlerError, HandlerResult, RetryPolicy,
    SdkResult, Value, value_mapper,
};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
/// Controls how a generated SubFlow Flow ID resolves an existing execution.
pub enum SubFlowReusePolicy {
    /// Attaches to a running execution or returns its existing terminal result.
    Attach,
    /// Restarts abnormal executions, attaches while running, and returns completed results.
    RestartIfPreviousExitsAbnormally,
    /// Replaces any different existing execution, including a running one.
    AlwaysRestart,
}

#[derive(Clone)]
/// Configures one durable SubFlow Condition.
///
/// Changed in 1.25.0: the SubFlow always selects pull Workers. Other omitted
/// configuration fields inherit the parent's effective settings. Dex generates its Flow ID and
/// request ID. Builder methods preserve normal Flow start defaults when omitted.
/// Matching generated request IDs always attach to an existing child, regardless of reuse policy.
/// For different request IDs, the reuse policy decides whether to attach or restart.
/// Completing an any-of wait does not stop non-winning SubFlows; use their generated Flow IDs
/// with [`crate::Client::stop_flow`] when those children should stop.
pub struct SubFlowOptions {
    pub(crate) timeout: Option<Duration>,
    pub(crate) timeout_policy: FlowTimeoutPolicy,
    pub(crate) timeout_handler_options: Option<FlowTimeoutHandlerOptions>,
    pub(crate) start_delay: Option<Duration>,
    pub(crate) retry_policy: Option<RetryPolicy>,
    pub(crate) config_override: Option<FlowConfig>,
    pub(crate) attributes: Vec<InitialAttribute>,
    pub(crate) reuse_policy: SubFlowReusePolicy,
    pub(crate) condition_id: Option<String>,
}

impl SubFlowOptions {
    /// Creates options using the abnormal-restart reuse policy and normal start defaults.
    pub fn new() -> Self {
        Self {
            timeout: None,
            timeout_policy: FlowTimeoutPolicy::Default,
            timeout_handler_options: None,
            start_delay: None,
            retry_policy: None,
            config_override: None,
            attributes: Vec::new(),
            reuse_policy: SubFlowReusePolicy::RestartIfPreviousExitsAbnormally,
            condition_id: None,
        }
    }

    /// Sets the maximum total SubFlow execution duration.
    pub fn timeout(mut self, value: Duration) -> Self {
        self.timeout = Some(value);
        self
    }

    /// Selects what Dex does when the positive soft SubFlow timeout expires.
    pub fn timeout_policy(mut self, value: FlowTimeoutPolicy) -> Self {
        self.timeout_policy = value;
        self
    }

    /// Configures execution and selective state loading for the target Flow's timeout handler.
    pub fn timeout_handler_options(mut self, value: FlowTimeoutHandlerOptions) -> Self {
        self.timeout_handler_options = Some(value);
        self
    }

    /// Sets a delay before the SubFlow starting Step after server acceptance.
    ///
    /// The Server currently supports only a zero delay. It rejects a Step result that requests a
    /// SubFlow with a positive delay as invalid input, so that Step attempt fails.
    pub fn start_delay(mut self, value: Duration) -> Self {
        self.start_delay = Some(value);
        self
    }

    /// Sets a whole-Flow retry policy for the SubFlow.
    ///
    /// The Server does not support whole-Flow retry. It rejects a Step result that requests a
    /// SubFlow with this option as invalid input, so that Step attempt fails. Configure
    /// [`crate::RetryPolicy`] on the SubFlow's Step methods through [`crate::StepOptions`]
    /// instead.
    pub fn retry_policy(mut self, value: RetryPolicy) -> Self {
        self.retry_policy = Some(value);
        self
    }

    /// Applies fields over the inherited parent Flow configuration.
    pub fn config_override(mut self, value: FlowConfig) -> Self {
        self.config_override = Some(value);
        self
    }

    /// Selects existing-execution resolution behavior.
    pub fn reuse_policy(mut self, value: SubFlowReusePolicy) -> Self {
        self.reuse_policy = value;
        self
    }

    /// Assigns the stable ID required by [`crate::Wait::any_combination_of`].
    pub fn condition_id(mut self, value: impl Into<String>) -> Self {
        self.condition_id = Some(value.into());
        self
    }

    /// Adds one initial singleton Attribute owned by the target SubFlow.
    pub fn initial_attribute<T: Value>(mut self, attribute: &Attribute<T>, value: T) -> Self {
        self.attributes
            .push(InitialAttribute::for_attribute(attribute, value));
        self
    }

    /// Adds one initial Attribute-map value for an instance.
    /// Slash is prohibited in instance keys because it is a reserved character.
    pub fn initial_attribute_map<T: Value>(
        mut self,
        attribute: &AttributeMap<T>,
        instance: &str,
        value: T,
    ) -> Self {
        self.attributes.push(InitialAttribute::for_attribute_map(
            attribute, instance, value,
        ));
        self
    }
}

impl Default for SubFlowOptions {
    fn default() -> Self {
        Self::new()
    }
}

/// Creates durable SubFlow Conditions and reads their Execute results.
///
/// Construction encodes input and returns a Condition; Dex starts or attaches to the child when
/// the Worker submits that wait. The target Flow must be in the Worker's Registry. The parent
/// waits durably without holding a handler thread. Inspect [`FlowResult`] for child failure;
/// a child's abnormal terminal status is a result rather than a local value-mapping error.
/// Result indexes are zero-based within the SubFlow Conditions, not all Condition kinds.
///
/// Dex generates each child Flow ID as `SubFlow:{parent Flow ID}-{Step execution ID}-{index}`.
/// The Step execution ID is `{Step type}-{n}`.
/// Because these parts are joined with `-`, only a Step whose Step type contains no `-` can start
/// SubFlows.
/// When a Step whose Step type contains `-` returns a Wait with a SubFlow Condition, the Worker
/// fails that `wait_for` attempt with an invalid Step result error and sends no Conditions.
/// Rename the Step type, for example by overriding [`crate::Step::step_type`], to start
/// SubFlows from it.
///
/// Changed in 1.44.0: a Step whose Step type contains `-` can no longer start SubFlows, because
/// two different parents could otherwise generate the same child Flow ID.
///
/// ```no_run
/// use dex_sdk::{Flow, SdkResult, SubFlow, SubFlowOptions, Wait};
/// use std::time::Duration;
/// fn wait_for_child<F: Flow>(flow: &F, input: F::StartInput) -> SdkResult<Wait> {
///     let child = SubFlow::run_with_options(flow, input,
///         SubFlowOptions::new().timeout(Duration::from_secs(60)))?;
///     Ok(Wait::until(child))
/// }
/// ```
pub struct SubFlow;

impl SubFlow {
    /// Creates a SubFlow Condition with default options.
    ///
    /// # Errors
    ///
    /// Returns a value-mapping error when `input` cannot be encoded.
    /// Worker mapping fails the `wait_for` attempt when the current Step type contains `-`.
    /// See [`SubFlow`] for this rule.
    ///
    /// Changed in 1.44.0: a Step whose Step type contains `-` can no longer start SubFlows.
    pub fn run<SomeFlow: Flow>(
        flow: &SomeFlow,
        input: SomeFlow::StartInput,
    ) -> SdkResult<Condition> {
        Self::run_with_options(flow, input, SubFlowOptions::new())
    }

    /// Creates a SubFlow Condition with explicit options.
    ///
    /// # Errors
    ///
    /// Returns a value-mapping error when `input` cannot be encoded. Worker mapping later validates
    /// the exact registered Rust Flow type, starting Step, Attributes, and option durations.
    /// Worker mapping also fails the `wait_for` attempt when the current Step type contains `-`.
    /// See [`SubFlow`] for this rule.
    ///
    /// Changed in 1.44.0: a Step whose Step type contains `-` can no longer start SubFlows.
    pub fn run_with_options<SomeFlow: Flow>(
        flow: &SomeFlow,
        input: SomeFlow::StartInput,
        options: SubFlowOptions,
    ) -> SdkResult<Condition> {
        Ok(Condition::sub_flow(SubFlowDefinition {
            flow_type: flow.flow_type(),
            type_id: TypeId::of::<SomeFlow>(),
            input: value_mapper::encode(&input)?,
            options,
        }))
    }

    /// Returns the first SubFlow result during Step `execute`.
    ///
    /// # Errors
    ///
    /// Returns [`crate::HandlerError`] outside Execute or when the selected SubFlow result is
    /// unavailable or malformed. See [`SubFlow`] for constructing the matching wait.
    pub fn condition_result(context: &Context) -> HandlerResult<FlowResult> {
        Self::condition_result_at(context, 0)
    }

    /// Returns one stable-indexed SubFlow result during Step `execute`.
    ///
    /// # Errors
    ///
    /// Returns [`crate::HandlerError`] outside Execute or when the selected SubFlow result is
    /// unavailable or malformed. See [`SubFlow`] for constructing the matching wait.
    pub fn condition_result_at(context: &Context, index: usize) -> HandlerResult<FlowResult> {
        context.sub_flow_result(index)
    }

    /// Returns the generated Flow ID for the first SubFlow Condition during Step `execute`.
    ///
    /// # Errors
    ///
    /// Returns [`crate::HandlerError`] outside Execute or when the selected SubFlow result is
    /// unavailable or malformed. See [`SubFlow`] for constructing the matching wait.
    pub fn flow_id(context: &Context) -> HandlerResult<String> {
        Self::flow_id_at(context, 0)
    }

    /// Returns the generated Flow ID for one stable-indexed SubFlow Condition during `execute`.
    ///
    /// # Errors
    ///
    /// Returns [`crate::HandlerError`] outside Execute or when the selected SubFlow result is
    /// unavailable or malformed. See [`SubFlow`] for constructing the matching wait.
    pub fn flow_id_at(context: &Context, index: usize) -> HandlerResult<String> {
        context.sub_flow_id(index)
    }
}

/// Rejects SubFlow Conditions from a dashed Step type, which would make child Flow IDs ambiguous.
pub(crate) fn require_sub_flow_step_type(
    step_type: &str,
    condition: Option<WaitingCondition>,
) -> HandlerResult<Option<WaitingCondition>> {
    let has_sub_flows = condition
        .as_ref()
        .is_some_and(|condition| !condition.sub_flow_conditions.is_empty());
    if has_sub_flows && step_type.contains('-') {
        return Err(HandlerError::new(
            "dex_sdk::HandlerError",
            format!(
                "Step type \"{step_type}\" contains \"-\" and cannot start SubFlows: the child Flow \
                 ID joins the parent Flow ID, Step execution ID, and SubFlow index with \"-\", so a \
                 dash makes it ambiguous. Rename the Step type."
            ),
        ));
    }
    Ok(condition)
}

#[cfg(test)]
#[path = "sub_flow_tests.rs"]
mod tests;
