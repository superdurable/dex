// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::collections::{HashMap, HashSet};
use std::sync::Arc;
use std::time::Duration;

use dex_protocol::dex::{
    AttributeWrite, ChannelCondition, ChannelMessage, CloseDecision, CloseDecisionType,
    ConditionCombination as ProtoCombination, ExecuteMethodFailurePolicy,
    InvokeExecuteMethodRequest, InvokeExecuteMethodResponse, InvokeWaitForMethodRequest,
    InvokeWaitForMethodResponse, InvokeWorkerRpcRequest, InvokeWorkerRpcResponse, Kv,
    RetryPolicy as ProtoRetryPolicy, StepDecision as ProtoStepDecision,
    StepDurability as ProtoStepDurability, StepMovement as ProtoStepMovement,
    StepOptions as ProtoStepOptions, TimerCondition, WaitForMethodFailurePolicy, WaitingCondition,
    WaitingConditionType,
};

use crate::context::{Context, InvocationCancellation, InvocationMethod};
use crate::persistence::PersistenceKind;
use crate::registry::{RegisteredFlow, physical_name};
use crate::rpc::encode_rpc_output;
use crate::step::{StepDecisionKind, StepMovement};
use crate::step_options::ErasedStepOptions;
use crate::value_hydrator::ValueHydrator;
use crate::value_mapper;
use crate::wait::{Condition, ConditionKind, Wait, WaitKind};
use crate::{
    HandlerError, HandlerResult, Registry, RetryPolicy, StepDurability, WaitForFailurePolicy,
};

#[derive(Clone)]
pub(crate) struct WorkerDispatcher {
    registry: Registry,
    hydrator: ValueHydrator,
}

impl WorkerDispatcher {
    pub(crate) fn new(registry: Registry, hydrator: ValueHydrator) -> Self {
        Self { registry, hydrator }
    }

    pub(crate) async fn invoke_wait_for(
        &self,
        request: InvokeWaitForMethodRequest,
    ) -> HandlerResult<InvokeWaitForMethodResponse> {
        let request = self.hydrate_wait_for(request).await?;
        let flow = self.registered_flow(&request.flow_type)?;
        let step = flow
            .steps
            .get(request.step_type.as_str())
            .cloned()
            .ok_or_else(|| {
                HandlerError::new(format!("Step is not registered: {}", request.step_type))
            })?;
        let mut context = Context::new(
            InvocationMethod::WaitFor,
            flow.clone(),
            request
                .context
                .ok_or_else(|| HandlerError::new("Worker request Context is required"))?,
            request.attributes,
            Vec::new(),
            None,
            HashMap::new(),
        )?;
        let input = request
            .step_input
            .ok_or_else(|| HandlerError::new("Step input is required"))?;
        let cancellation = context.cancellation();
        run_handler(cancellation, move || {
            let wait = flow.handler.wait_for(step.name, &mut context, &input)?;
            let waiting_condition = map_wait(&flow, wait).map_err(|error| {
                HandlerError::invalid_step_result(flow.name, Some(step.name), "wait_for", error)
            })?;
            let (attributes, locals, events, publications) = context.take_outputs();
            Ok(InvokeWaitForMethodResponse {
                local_activity_metadata: None,
                upsert_attributes: attributes,
                waiting_condition,
                upsert_step_exe_locals: locals,
                record_events: events,
                publish_to_channel: publications,
            })
        })
        .await
    }

    pub(crate) async fn invoke_execute(
        &self,
        request: InvokeExecuteMethodRequest,
    ) -> HandlerResult<InvokeExecuteMethodResponse> {
        let request = self.hydrate_execute(request).await?;
        let flow = self.registered_flow(&request.flow_type)?;
        let step = flow
            .steps
            .get(request.step_type.as_str())
            .cloned()
            .ok_or_else(|| {
                HandlerError::new(format!("Step is not registered: {}", request.step_type))
            })?;
        let mut context = Context::new(
            InvocationMethod::Execute,
            flow.clone(),
            request
                .context
                .ok_or_else(|| HandlerError::new("Worker request Context is required"))?,
            request.attributes,
            request.step_exe_locals,
            request.condition_results,
            HashMap::new(),
        )?;
        let input = request
            .step_input
            .ok_or_else(|| HandlerError::new("Step input is required"))?;
        let cancellation = context.cancellation();
        run_handler(cancellation, move || {
            let decision = flow.handler.execute(step.name, &mut context, &input)?;
            let decision = map_decision(&flow, decision).map_err(|error| {
                HandlerError::invalid_step_result(flow.name, Some(step.name), "execute", error)
            })?;
            let (attributes, locals, events, publications) = context.take_outputs();
            Ok(InvokeExecuteMethodResponse {
                local_activity_metadata: None,
                step_decision: Some(decision),
                upsert_attributes: attributes,
                record_events: events,
                upsert_step_exe_locals: locals,
                publish_to_channel: publications,
            })
        })
        .await
    }

    pub(crate) async fn invoke_rpc(
        &self,
        request: InvokeWorkerRpcRequest,
    ) -> HandlerResult<InvokeWorkerRpcResponse> {
        let request = self.hydrate_rpc(request).await?;
        let flow = self.registered_flow(&request.flow_type)?;
        let rpc = flow
            .rpcs
            .get(request.rpc_name.as_str())
            .cloned()
            .ok_or_else(|| {
                HandlerError::new(format!("RPC is not registered: {}", request.rpc_name))
            })?;
        let mut context = Context::new(
            InvocationMethod::Rpc,
            flow.clone(),
            request
                .context
                .ok_or_else(|| HandlerError::new("Worker request Context is required"))?,
            request.attributes,
            Vec::new(),
            None,
            request.channel_infos,
        )?;
        let input = request
            .input
            .ok_or_else(|| HandlerError::new("RPC input is required"))?;
        let cancellation = context.cancellation();
        run_handler(cancellation, move || {
            let result = rpc.handler.invoke(&mut context, &input)?;
            let output = encode_rpc_output(result.output.as_ref()).map_err(handler_error)?;
            let decision = if result.next_steps.is_empty() {
                None
            } else {
                Some(ProtoStepDecision {
                    next_steps: map_movements(&flow, result.next_steps).map_err(|error| {
                        HandlerError::invalid_step_result(flow.name, None, "rpc", error)
                    })?,
                    close_decision: None,
                    cancel_step_types: Vec::new(),
                    cancel_sibling_step_types: Vec::new(),
                })
            };
            let (attributes, _, events, publications) = context.take_outputs();
            Ok(InvokeWorkerRpcResponse {
                output: Some(output),
                step_decision: decision,
                upsert_attributes: attributes,
                record_events: events,
                publish_to_channel: publications,
            })
        })
        .await
    }

    async fn hydrate_wait_for(
        &self,
        mut request: InvokeWaitForMethodRequest,
    ) -> HandlerResult<InvokeWaitForMethodRequest> {
        let mut values = vec![request.step_input.take().unwrap_or_default()];
        values.extend(take_entry_values(&mut request.attributes)?);
        let hydrated = self
            .hydrator
            .hydrate_all(values)
            .await
            .map_err(handler_error)?;
        let mut hydrated = hydrated.into_iter();
        request.step_input = hydrated.next();
        restore_entry_values(&mut request.attributes, hydrated)?;
        Ok(request)
    }

    async fn hydrate_execute(
        &self,
        mut request: InvokeExecuteMethodRequest,
    ) -> HandlerResult<InvokeExecuteMethodRequest> {
        let mut values = vec![request.step_input.take().unwrap_or_default()];
        let attribute_count = request.attributes.len();
        let local_count = request.step_exe_locals.len();
        values.extend(take_entry_values(&mut request.attributes)?);
        values.extend(take_entry_values(&mut request.step_exe_locals)?);
        let channel_counts: Vec<usize> = request
            .condition_results
            .as_ref()
            .map(|conditions| {
                conditions
                    .channel_results
                    .iter()
                    .map(|result| result.values.len())
                    .collect()
            })
            .unwrap_or_default();
        if let Some(conditions) = request.condition_results.as_mut() {
            for channel in &mut conditions.channel_results {
                values.append(&mut channel.values);
            }
        }
        let mut hydrated = self
            .hydrator
            .hydrate_all(values)
            .await
            .map_err(handler_error)?
            .into_iter();
        request.step_input = hydrated.next();
        restore_n_entry_values(&mut request.attributes, &mut hydrated, attribute_count)?;
        restore_n_entry_values(&mut request.step_exe_locals, &mut hydrated, local_count)?;
        if let Some(conditions) = request.condition_results.as_mut() {
            for (channel, count) in conditions.channel_results.iter_mut().zip(channel_counts) {
                channel.values = hydrated.by_ref().take(count).collect();
            }
        }
        Ok(request)
    }

    async fn hydrate_rpc(
        &self,
        mut request: InvokeWorkerRpcRequest,
    ) -> HandlerResult<InvokeWorkerRpcRequest> {
        let mut values = vec![request.input.take().unwrap_or_default()];
        values.extend(take_entry_values(&mut request.attributes)?);
        let hydrated = self
            .hydrator
            .hydrate_all(values)
            .await
            .map_err(handler_error)?;
        let mut hydrated = hydrated.into_iter();
        request.input = hydrated.next();
        restore_entry_values(&mut request.attributes, hydrated)?;
        Ok(request)
    }

    fn registered_flow(&self, flow_type: &str) -> HandlerResult<RegisteredFlow> {
        self.registry
            .flow(flow_type)
            .cloned()
            .map_err(handler_error)
    }
}

async fn run_handler<Output, Handler>(
    cancellation: InvocationCancellation,
    handler: Handler,
) -> HandlerResult<Output>
where
    Output: Send + 'static,
    Handler: FnOnce() -> HandlerResult<Output> + Send + 'static,
{
    let _cancel_on_drop = CancelOnDrop(cancellation);
    tokio::task::spawn_blocking(handler)
        .await
        .map_err(|error| HandlerError::new(format!("user handler task failed: {error}")))?
}

struct CancelOnDrop(InvocationCancellation);

impl Drop for CancelOnDrop {
    fn drop(&mut self) {
        self.0.cancel();
    }
}

pub(crate) fn map_step_options(
    flow: &RegisteredFlow,
    options: ErasedStepOptions,
) -> HandlerResult<ProtoStepOptions> {
    let recovery = options
        .execute_failure_step
        .map(|target| {
            let target = flow.steps.get(target).ok_or_else(|| {
                HandlerError::new(format!("execute failure Step is not registered: {target}"))
            })?;
            Ok((
                target.name,
                map_step_options_without_recovery(flow.handler.step_options(target.name)?)?,
            ))
        })
        .transpose()?;
    let mut mapped = map_step_options_without_recovery(options)?;
    if let Some((target, target_options)) = recovery {
        mapped.execute_failure_policy = ExecuteMethodFailurePolicy::ProceedToConfiguredStep as i32;
        mapped.execute_failure_proceed_step_type = target.to_string();
        mapped.execute_failure_proceed_step_options = Some(Box::new(target_options));
    }
    Ok(mapped)
}

fn map_step_options_without_recovery(
    options: ErasedStepOptions,
) -> HandlerResult<ProtoStepOptions> {
    Ok(ProtoStepOptions {
        wait_for_timeout_seconds: optional_seconds(options.wait_for_method_timeout)?,
        execute_timeout_seconds: optional_seconds(options.execute_method_timeout)?,
        wait_for_retry_policy: options.wait_for_retry.map(map_retry).transpose()?,
        execute_retry_policy: options.execute_retry.map(map_retry).transpose()?,
        wait_for_failure_policy: match options.wait_for_failure {
            WaitForFailurePolicy::FailFlow => WaitForMethodFailurePolicy::FailFlowOnFailure as i32,
            WaitForFailurePolicy::Proceed => WaitForMethodFailurePolicy::ProceedOnFailure as i32,
        },
        execute_failure_policy: ExecuteMethodFailurePolicy::FailFlowOnExecuteMethodFailure as i32,
        execute_failure_proceed_step_type: String::new(),
        execute_failure_proceed_step_options: None,
        skip_wait_for: false,
        wait_for_durability_override: map_durability(options.wait_for_durability),
        execute_durability_override: map_durability(options.execute_durability),
        wait_for_lock_attribute_keys: options
            .wait_for_locks
            .iter()
            .map(|lock| lock.physical_name())
            .collect(),
        execute_lock_attribute_keys: options
            .execute_locks
            .iter()
            .map(|lock| lock.physical_name())
            .collect(),
        heartbeat_timeout_seconds: 0,
    })
}

fn map_retry(retry: RetryPolicy) -> HandlerResult<ProtoRetryPolicy> {
    Ok(ProtoRetryPolicy {
        initial_interval_seconds: optional_seconds(retry.initial_interval)?,
        backoff_coefficient: retry.backoff_coefficient.unwrap_or_default() as f32,
        maximum_interval_seconds: optional_seconds(retry.maximum_interval)?,
        maximum_attempts: i32::try_from(retry.maximum_attempts.unwrap_or_default())
            .map_err(|_| HandlerError::new("maximum attempts exceed int32"))?,
        total_duration_seconds: optional_seconds(retry.total_duration)?,
    })
}

fn map_durability(durability: StepDurability) -> i32 {
    match durability {
        StepDurability::Default => ProtoStepDurability::Unspecified as i32,
        StepDurability::Sync => ProtoStepDurability::Sync as i32,
        StepDurability::Async => ProtoStepDurability::Async as i32,
    }
}

fn map_wait(flow: &RegisteredFlow, wait: Wait) -> HandlerResult<Option<WaitingCondition>> {
    let mut mapper = ConditionMapper::new(flow);
    let (waiting_type, combinations) = match wait.kind {
        WaitKind::SkipImmediately => return Ok(None),
        WaitKind::AllOf(conditions) => {
            require_conditions(&conditions)?;
            for condition in conditions {
                mapper.add(condition, false)?;
            }
            (WaitingConditionType::AllCompleted, Vec::new())
        }
        WaitKind::AnyOf(conditions) => {
            require_conditions(&conditions)?;
            for condition in conditions {
                mapper.add(condition, false)?;
            }
            (WaitingConditionType::AnyCompleted, Vec::new())
        }
        WaitKind::AnyCombinationOf(combinations) => {
            if combinations.is_empty() {
                return Err(HandlerError::new(
                    "Wait requires at least one Condition combination",
                ));
            }
            let mut mapped = Vec::new();
            for combination in combinations {
                require_conditions(&combination.conditions)?;
                let mut condition_ids = Vec::new();
                for condition in combination.conditions {
                    condition_ids.push(mapper.add(condition, true)?);
                }
                mapped.push(ProtoCombination { condition_ids });
            }
            (WaitingConditionType::AnyCombinationCompleted, mapped)
        }
    };
    Ok(Some(WaitingCondition {
        waiting_condition_type: waiting_type as i32,
        timer_conditions: mapper.timers,
        channel_conditions: mapper.channels,
        sub_flow_conditions: Vec::new(),
        condition_combinations: combinations,
    }))
}

fn map_decision(
    flow: &RegisteredFlow,
    decision: crate::StepDecision,
) -> HandlerResult<ProtoStepDecision> {
    match decision.kind {
        StepDecisionKind::Next(movements) => {
            if movements.is_empty() {
                return Err(HandlerError::new("go_to_many requires a movement"));
            }
            Ok(ProtoStepDecision {
                next_steps: map_movements(flow, movements)?,
                close_decision: None,
                cancel_step_types: Vec::new(),
                cancel_sibling_step_types: Vec::new(),
            })
        }
        StepDecisionKind::GracefulComplete(output) => close_decision(
            CloseDecisionType::GracefulComplete,
            output.encode().map_err(handler_error)?,
        ),
        StepDecisionKind::ForceComplete(output) => close_decision(
            CloseDecisionType::ForceComplete,
            output.encode().map_err(handler_error)?,
        ),
        StepDecisionKind::ForceFail(reason) => close_decision(
            CloseDecisionType::ForceFail,
            value_mapper::encode_handler(&reason)?,
        ),
        StepDecisionKind::DeadEnd => Ok(ProtoStepDecision {
            next_steps: Vec::new(),
            close_decision: Some(CloseDecision {
                close_decision_type: CloseDecisionType::DeadEnd as i32,
                conditional_channel_names: Vec::new(),
                close_input: None,
            }),
            cancel_step_types: Vec::new(),
            cancel_sibling_step_types: Vec::new(),
        }),
        StepDecisionKind::ForceCompleteIfChannelsEmpty {
            output,
            fallback,
            channels,
        } => Ok(ProtoStepDecision {
            next_steps: vec![map_movement(flow, *fallback)?],
            close_decision: Some(CloseDecision {
                close_decision_type: CloseDecisionType::ForceCompleteOnChannelsEmpty as i32,
                conditional_channel_names: channels
                    .iter()
                    .map(|channel| channel.physical_name())
                    .collect(),
                close_input: Some(output.encode().map_err(handler_error)?),
            }),
            cancel_step_types: Vec::new(),
            cancel_sibling_step_types: Vec::new(),
        }),
    }
}

fn close_decision(
    close_type: CloseDecisionType,
    output: dex_protocol::dex::Value,
) -> HandlerResult<ProtoStepDecision> {
    Ok(ProtoStepDecision {
        next_steps: Vec::new(),
        close_decision: Some(CloseDecision {
            close_decision_type: close_type as i32,
            conditional_channel_names: Vec::new(),
            close_input: Some(output),
        }),
        cancel_step_types: Vec::new(),
        cancel_sibling_step_types: Vec::new(),
    })
}

fn map_movements(
    flow: &RegisteredFlow,
    movements: Vec<StepMovement>,
) -> HandlerResult<Vec<ProtoStepMovement>> {
    movements
        .into_iter()
        .map(|movement| map_movement(flow, movement))
        .collect()
}

fn map_movement(flow: &RegisteredFlow, movement: StepMovement) -> HandlerResult<ProtoStepMovement> {
    let target = flow
        .steps
        .get(movement.target_step_type)
        .ok_or_else(|| HandlerError::new("Step movement target does not belong to Flow"))?;
    let step_input = movement.encode_input().map_err(handler_error)?;
    let options = movement
        .options_override
        .map_or_else(|| flow.handler.step_options(target.name), Ok)?;
    Ok(ProtoStepMovement {
        step_type: target.name.to_string(),
        step_input: Some(step_input),
        step_options: Some(map_step_options(flow, options)?),
        from_step_execution_id_internal_only: String::new(),
        recovery_error_internal_only: None,
    })
}

struct ConditionMapper<'a> {
    flow: &'a RegisteredFlow,
    used_ids: HashSet<String>,
    ids_by_condition: HashMap<usize, String>,
    timers: Vec<TimerCondition>,
    channels: Vec<ChannelCondition>,
}

impl<'a> ConditionMapper<'a> {
    fn new(flow: &'a RegisteredFlow) -> Self {
        Self {
            flow,
            used_ids: HashSet::new(),
            ids_by_condition: HashMap::new(),
            timers: Vec::new(),
            channels: Vec::new(),
        }
    }

    fn add(&mut self, condition: Condition, id_required: bool) -> HandlerResult<String> {
        let identity = Arc::as_ptr(&condition.identity) as usize;
        if let Some(id) = self.ids_by_condition.get(&identity) {
            return Ok(id.clone());
        }
        let id_was_provided = condition.id.is_some();
        let id = condition.id.unwrap_or_default();
        if id_required && id.is_empty() {
            return Err(HandlerError::new(
                "any_combination_of requires every Condition to have an ID",
            ));
        }
        if id_was_provided && id.is_empty() {
            return Err(HandlerError::new("empty Condition ID"));
        }
        if !id.is_empty() && self.used_ids.contains(&id) {
            return Err(HandlerError::new("duplicate Condition ID"));
        }
        if !id.is_empty() {
            self.used_ids.insert(id.clone());
        }
        self.ids_by_condition.insert(identity, id.clone());
        match condition.kind {
            ConditionKind::Timer(duration) => self.timers.push(TimerCondition {
                condition_id: id.clone(),
                duration_seconds: seconds64(duration)?,
                firing_unix_timestamp_seconds: 0,
            }),
            ConditionKind::Channel {
                name,
                instance,
                at_least,
                at_most,
            } => {
                let definition = self.flow.persistence.get(&name).ok_or_else(|| {
                    HandlerError::new(format!("Channel is not registered: {name}"))
                })?;
                let channel_name = match definition.kind {
                    PersistenceKind::Channel if instance.is_none() => name,
                    PersistenceKind::ChannelMap => physical_name(
                        &name,
                        instance.as_deref().ok_or_else(|| {
                            HandlerError::new(format!("ChannelMap {name} requires an instance"))
                        })?,
                    ),
                    _ => {
                        return Err(HandlerError::new(format!(
                            "Channel is not registered: {name}"
                        )));
                    }
                };
                self.channels.push(ChannelCondition {
                    condition_id: id.clone(),
                    channel_name,
                    at_least: optional_count(at_least)?,
                    at_most: optional_count(at_most)?,
                });
            }
        }
        Ok(id)
    }
}

fn take_entry_values(entries: &mut [Kv]) -> HandlerResult<Vec<dex_protocol::dex::Value>> {
    entries
        .iter_mut()
        .map(|entry| {
            entry
                .value
                .take()
                .ok_or_else(|| HandlerError::new(format!("{} has no Value", entry.key)))
        })
        .collect()
}

fn restore_entry_values(
    entries: &mut [Kv],
    values: impl IntoIterator<Item = dex_protocol::dex::Value>,
) -> HandlerResult<()> {
    let mut values = values.into_iter();
    for entry in entries {
        entry.value = Some(
            values
                .next()
                .ok_or_else(|| HandlerError::new("hydrated Value count mismatch"))?,
        );
    }
    if values.next().is_some() {
        return Err(HandlerError::new("hydrated Value count mismatch"));
    }
    Ok(())
}

fn restore_n_entry_values(
    entries: &mut [Kv],
    values: &mut impl Iterator<Item = dex_protocol::dex::Value>,
    count: usize,
) -> HandlerResult<()> {
    if entries.len() != count {
        return Err(HandlerError::new("hydrated Value count mismatch"));
    }
    for entry in entries {
        entry.value = Some(
            values
                .next()
                .ok_or_else(|| HandlerError::new("hydrated Value count mismatch"))?,
        );
    }
    Ok(())
}

fn require_conditions(conditions: &[Condition]) -> HandlerResult<()> {
    if conditions.is_empty() {
        Err(HandlerError::new("Wait requires at least one Condition"))
    } else {
        Ok(())
    }
}

fn optional_count(value: Option<usize>) -> HandlerResult<Option<i32>> {
    value
        .map(|value| {
            i32::try_from(value).map_err(|_| HandlerError::new("channel count exceeds int32"))
        })
        .transpose()
}

fn optional_seconds(value: Option<Duration>) -> HandlerResult<i32> {
    value
        .map(seconds32)
        .transpose()
        .map(Option::unwrap_or_default)
}

fn seconds32(duration: Duration) -> HandlerResult<i32> {
    if duration.subsec_nanos() != 0 {
        return Err(HandlerError::new("Duration must use whole seconds"));
    }
    i32::try_from(duration.as_secs()).map_err(|_| HandlerError::new("Duration exceeds int32"))
}

fn seconds64(duration: Duration) -> HandlerResult<i64> {
    if duration.subsec_nanos() != 0 {
        return Err(HandlerError::new("Duration must use whole seconds"));
    }
    i64::try_from(duration.as_secs()).map_err(|_| HandlerError::new("Duration exceeds int64"))
}

fn handler_error(error: impl std::fmt::Display) -> HandlerError {
    HandlerError::new(error.to_string())
}

#[allow(dead_code)]
fn _keep_output_types(
    _attributes: Vec<AttributeWrite>,
    _locals: Vec<Kv>,
    _events: Vec<Kv>,
    _publications: Vec<ChannelMessage>,
) {
}

#[cfg(test)]
mod tests {
    use super::map_wait;
    use crate::{Channel, ConditionCombination, Flow, PersistenceSchema, Registry, Timer, Wait};
    use std::time::Duration;

    struct ConditionFlow {
        channel: Channel<()>,
    }

    impl Flow for ConditionFlow {
        type StartInput = ();

        fn persistence(&self) -> PersistenceSchema {
            PersistenceSchema::new().channel(&self.channel)
        }
    }

    #[test]
    fn maps_only_user_provided_condition_ids() {
        let registry = Registry::new()
            .register(ConditionFlow {
                channel: Channel::new("commands"),
            })
            .expect("register Condition Flow");
        let flow = registry
            .flow("ConditionFlow")
            .expect("lookup Condition Flow");
        let channel = Channel::<()>::new("commands");

        let unnamed = map_wait(
            flow,
            Wait::any_of([
                Timer::by_duration(Duration::from_secs(1)),
                channel.for_one(),
            ]),
        )
        .expect("map unnamed wait")
        .expect("waiting condition");
        assert_eq!("", unnamed.timer_conditions[0].condition_id);
        assert_eq!("", unnamed.channel_conditions[0].condition_id);

        let reused = channel.for_one().with_id("__dex_internal_condition_0");
        let reused_wait = map_wait(
            flow,
            Wait::any_combination_of([
                ConditionCombination::all_of([reused.clone()]),
                ConditionCombination::all_of([reused]),
            ]),
        )
        .expect("map reused wait")
        .expect("waiting condition");
        assert_eq!(1, reused_wait.channel_conditions.len());
        assert_eq!(
            "__dex_internal_condition_0",
            reused_wait.channel_conditions[0].condition_id
        );
        assert_eq!(
            reused_wait.condition_combinations[0].condition_ids,
            reused_wait.condition_combinations[1].condition_ids
        );

        let missing = map_wait(
            flow,
            Wait::any_combination_of([ConditionCombination::all_of([channel.for_one()])]),
        )
        .expect_err("unnamed combination must fail");
        assert!(missing.to_string().contains("requires every Condition"));

        let duplicate = map_wait(
            flow,
            Wait::any_combination_of([ConditionCombination::all_of([
                channel.for_one().with_id("same"),
                Timer::by_duration(Duration::from_secs(1)).with_id("same"),
            ])]),
        )
        .expect_err("duplicate IDs must fail");
        assert!(duplicate.to_string().contains("duplicate Condition ID"));

        let empty = map_wait(flow, Wait::all_of([channel.for_one().with_id("")]))
            .expect_err("empty ID must fail");
        assert!(empty.to_string().contains("empty Condition ID"));
    }
}
