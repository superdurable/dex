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
    InvokeExecuteMethodOutput, InvokeExecuteMethodRequest, InvokeExecuteMethodResponse,
    InvokeWaitForMethodOutput, InvokeWaitForMethodRequest, InvokeWaitForMethodResponse,
    InvokeWorkerRpcRequest, InvokeWorkerRpcResponse, Kv, RetryPolicy as ProtoRetryPolicy,
    StepDecision as ProtoStepDecision, StepDurability as ProtoStepDurability,
    StepMovement as ProtoStepMovement, StepOptions as ProtoStepOptions,
    SubFlowCondition as ProtoSubFlowCondition, SubFlowOptions as ProtoSubFlowOptions,
    SubFlowReusePolicy as ProtoSubFlowReusePolicy, TimerCondition, WaitForMethodFailurePolicy,
    WaitingCondition, WaitingConditionType, invoke_execute_method_output,
    invoke_wait_for_method_output,
};
use tokio::sync::mpsc;

use crate::context::{Context, ContextInput, InvocationCancellation, InvocationMethod};
use crate::persistence::PersistenceKind;
use crate::registry::{RegisteredFlow, physical_name};
use crate::rpc::encode_rpc_output;
use crate::step::{StepDecisionKind, StepMovement};
use crate::step_options::ErasedStepOptions;
use crate::value_hydrator::ValueHydrator;
use crate::value_mapper;
use crate::wait::{Condition, ConditionKind, Wait, WaitKind};
use crate::worker_output::{StepOutputEmitter, WorkerInvocation};
use crate::{
    HandlerError, HandlerResult, Registry, RetryPolicy, SdkError, StepDurability,
    SubFlowReusePolicy, WaitForFailurePolicy,
};

const TIMEOUT_HANDLER_STEP_TYPE: &str = "sys:timeout_handler";

#[derive(Clone)]
pub(crate) struct WorkerDispatcher {
    registry: Registry,
    hydrator: ValueHydrator,
}

impl WorkerDispatcher {
    pub(crate) fn new(registry: Registry, hydrator: ValueHydrator) -> Self {
        Self { registry, hydrator }
    }

    pub(crate) fn invoke_wait_for(
        &self,
        request: InvokeWaitForMethodRequest,
    ) -> WorkerInvocation<InvokeWaitForMethodOutput> {
        let (output_emitter, receiver) = StepOutputEmitter::wait_for();
        let sender = output_emitter.wait_for_sender();
        let cancellation = InvocationCancellation::new();
        let producer_cancellation = cancellation.clone();
        let dispatcher = self.clone();
        let producer = tokio::spawn(async move {
            let result = dispatcher
                .do_invoke_wait_for(request, output_emitter, producer_cancellation.clone())
                .await
                .map(|result| InvokeWaitForMethodOutput {
                    output: Some(invoke_wait_for_method_output::Output::Result(result)),
                });
            send_handler_result(sender, producer_cancellation, result).await;
        });
        WorkerInvocation::new(receiver, cancellation, producer)
    }

    async fn do_invoke_wait_for(
        &self,
        request: InvokeWaitForMethodRequest,
        output_emitter: StepOutputEmitter,
        cancellation: InvocationCancellation,
    ) -> HandlerResult<InvokeWaitForMethodResponse> {
        let request = self.hydrate_wait_for(request).await?;
        let flow = self.registered_flow(&request.flow_type)?;
        let step = flow
            .steps
            .get(request.step_type.as_str())
            .cloned()
            .ok_or_else(|| {
                HandlerError::new(
                    "dex_sdk::HandlerError",
                    format!("Step is not registered: {}", request.step_type),
                )
            })?;
        let mut context = Context::new(
            ContextInput {
                method: InvocationMethod::WaitFor,
                flow: flow.clone(),
                metadata: request.context.ok_or_else(|| {
                    HandlerError::new(
                        "dex_sdk::HandlerError",
                        "Worker request Context is required",
                    )
                })?,
                attributes: request.attributes,
                locals: Vec::new(),
                condition_results: None,
                channel_infos: HashMap::new(),
            },
            Some(output_emitter),
            cancellation,
        )?;
        let input = request
            .step_input
            .ok_or_else(|| HandlerError::new("dex_sdk::HandlerError", "Step input is required"))?;
        let cancellation = context.cancellation();
        let registry = self.registry.clone();
        run_handler(cancellation, move || {
            let wait = flow.handler.wait_for(step.name, &mut context, &input)?;
            let waiting_condition = map_wait(&registry, &flow, wait).map_err(|error| {
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

    pub(crate) fn invoke_execute(
        &self,
        request: InvokeExecuteMethodRequest,
    ) -> WorkerInvocation<InvokeExecuteMethodOutput> {
        let (output_emitter, receiver) = StepOutputEmitter::execute();
        let sender = output_emitter.execute_sender();
        let cancellation = InvocationCancellation::new();
        let producer_cancellation = cancellation.clone();
        let dispatcher = self.clone();
        let producer = tokio::spawn(async move {
            let result = dispatcher
                .do_invoke_execute(request, output_emitter, producer_cancellation.clone())
                .await
                .map(|result| InvokeExecuteMethodOutput {
                    output: Some(invoke_execute_method_output::Output::Result(result)),
                });
            send_handler_result(sender, producer_cancellation, result).await;
        });
        WorkerInvocation::new(receiver, cancellation, producer)
    }

    async fn do_invoke_execute(
        &self,
        request: InvokeExecuteMethodRequest,
        output_emitter: StepOutputEmitter,
        cancellation: InvocationCancellation,
    ) -> HandlerResult<InvokeExecuteMethodResponse> {
        let request = self.hydrate_execute(request).await?;
        let flow = self.registered_flow(&request.flow_type)?;
        if request.step_type == TIMEOUT_HANDLER_STEP_TYPE {
            return self
                .invoke_timeout_handler(request, flow, cancellation)
                .await;
        }
        let step = flow
            .steps
            .get(request.step_type.as_str())
            .cloned()
            .ok_or_else(|| {
                HandlerError::new(
                    "dex_sdk::HandlerError",
                    format!("Step is not registered: {}", request.step_type),
                )
            })?;
        let mut context = Context::new(
            ContextInput {
                method: InvocationMethod::Execute,
                flow: flow.clone(),
                metadata: request.context.ok_or_else(|| {
                    HandlerError::new(
                        "dex_sdk::HandlerError",
                        "Worker request Context is required",
                    )
                })?,
                attributes: request.attributes,
                locals: request.step_exe_locals,
                condition_results: request.condition_results,
                channel_infos: HashMap::new(),
            },
            Some(output_emitter),
            cancellation,
        )?;
        let input = request
            .step_input
            .ok_or_else(|| HandlerError::new("dex_sdk::HandlerError", "Step input is required"))?;
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

    async fn invoke_timeout_handler(
        &self,
        request: InvokeExecuteMethodRequest,
        flow: RegisteredFlow,
        cancellation: InvocationCancellation,
    ) -> HandlerResult<InvokeExecuteMethodResponse> {
        if request.step_input.is_some() {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "timeout handler input must be absent",
            ));
        }
        if !flow.handler.has_timeout_handler() {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "Flow has no timeout handler",
            ));
        }
        let mut context = Context::new(
            ContextInput {
                method: InvocationMethod::Execute,
                flow: flow.clone(),
                metadata: request.context.ok_or_else(|| {
                    HandlerError::new(
                        "dex_sdk::HandlerError",
                        "Worker request Context is required",
                    )
                })?,
                attributes: request.attributes,
                locals: request.step_exe_locals,
                condition_results: request.condition_results,
                channel_infos: HashMap::new(),
            },
            None,
            cancellation,
        )?;
        let cancellation = context.cancellation();
        run_handler(cancellation, move || {
            let decision = flow.handler.handle_timeout(&mut context)?;
            let decision = map_decision(&flow, decision).map_err(|error| {
                HandlerError::invalid_step_result(
                    flow.name,
                    Some(TIMEOUT_HANDLER_STEP_TYPE),
                    "execute",
                    error,
                )
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
                HandlerError::new(
                    "dex_sdk::HandlerError",
                    format!("RPC is not registered: {}", request.rpc_name),
                )
            })?;
        let mut context = Context::new(
            ContextInput {
                method: InvocationMethod::Rpc,
                flow: flow.clone(),
                metadata: request.context.ok_or_else(|| {
                    HandlerError::new(
                        "dex_sdk::HandlerError",
                        "Worker request Context is required",
                    )
                })?,
                attributes: request.attributes,
                locals: Vec::new(),
                condition_results: None,
                channel_infos: request.channel_infos,
            },
            None,
            InvocationCancellation::new(),
        )?;
        let input = request
            .input
            .ok_or_else(|| HandlerError::new("dex_sdk::HandlerError", "RPC input is required"))?;
        let cancellation = context.cancellation();
        run_handler(cancellation, move || {
            let result = rpc.handler.invoke(&mut context, &input)?;
            let output = encode_rpc_output(result.output.as_ref()).map_err(handler_error)?;
            let cancel_step_types = map_cancellation_step_types(&flow, result.cancel_step_types)?;
            let decision = if result.next_steps.is_empty() && cancel_step_types.is_empty() {
                None
            } else {
                Some(ProtoStepDecision {
                    next_steps: map_movements(&flow, result.next_steps).map_err(|error| {
                        HandlerError::invalid_step_result(flow.name, None, "rpc", error)
                    })?,
                    close_decision: None,
                    cancel_step_types,
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
        let step_input = request.step_input.take();
        let has_step_input = step_input.is_some();
        let mut values = step_input.into_iter().collect::<Vec<_>>();
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
        let sub_flow_counts: Vec<usize> = request
            .condition_results
            .as_ref()
            .map(|conditions| {
                conditions
                    .sub_flow_results
                    .iter()
                    .map(|result| result.results.len())
                    .collect()
            })
            .unwrap_or_default();
        if let Some(conditions) = request.condition_results.as_mut() {
            for channel in &mut conditions.channel_results {
                values.append(&mut channel.values);
            }
            for flow_result in &mut conditions.sub_flow_results {
                for completion in &mut flow_result.results {
                    values.push(completion.completed_step_output.take().ok_or_else(|| {
                        HandlerError::new(
                            "dex_sdk::HandlerError",
                            "SubFlow Step completion output is required",
                        )
                    })?);
                }
            }
        }
        let mut hydrated = self
            .hydrator
            .hydrate_all(values)
            .await
            .map_err(handler_error)?
            .into_iter();
        if has_step_input {
            request.step_input = hydrated.next();
        }
        restore_n_entry_values(&mut request.attributes, &mut hydrated, attribute_count)?;
        restore_n_entry_values(&mut request.step_exe_locals, &mut hydrated, local_count)?;
        if let Some(conditions) = request.condition_results.as_mut() {
            for (channel, count) in conditions.channel_results.iter_mut().zip(channel_counts) {
                channel.values = hydrated.by_ref().take(count).collect();
            }
            for (flow_result, count) in conditions.sub_flow_results.iter_mut().zip(sub_flow_counts)
            {
                for completion in flow_result.results.iter_mut().take(count) {
                    completion.completed_step_output = hydrated.next();
                }
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

async fn send_handler_result<Output>(
    sender: mpsc::Sender<HandlerResult<Output>>,
    cancellation: InvocationCancellation,
    result: HandlerResult<Output>,
) {
    if sender.send(result).await.is_err() {
        cancellation.cancel();
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
        .map_err(|error| {
            HandlerError::new(
                "dex_sdk::HandlerError",
                format!("user handler task failed: {error}"),
            )
        })?
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
                HandlerError::new(
                    "dex_sdk::HandlerError",
                    format!("execute failure Step is not registered: {target}"),
                )
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
        heartbeat_timeout_seconds: optional_seconds(options.heartbeat_timeout)?,
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
    })
}

fn map_retry(retry: RetryPolicy) -> HandlerResult<ProtoRetryPolicy> {
    Ok(ProtoRetryPolicy {
        initial_interval_seconds: optional_seconds(retry.initial_interval)?,
        backoff_coefficient: retry.backoff_coefficient.unwrap_or_default() as f32,
        maximum_interval_seconds: optional_seconds(retry.maximum_interval)?,
        maximum_attempts: i32::try_from(retry.maximum_attempts.unwrap_or_default()).map_err(
            |_| HandlerError::new("dex_sdk::HandlerError", "maximum attempts exceed int32"),
        )?,
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

fn map_wait(
    registry: &Registry,
    flow: &RegisteredFlow,
    wait: Wait,
) -> HandlerResult<Option<WaitingCondition>> {
    let mut mapper = ConditionMapper::new(registry, flow);
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
                    "dex_sdk::HandlerError",
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
        sub_flow_conditions: mapper.sub_flows,
        condition_combinations: combinations,
    }))
}

fn map_decision(
    flow: &RegisteredFlow,
    decision: crate::StepDecision,
) -> HandlerResult<ProtoStepDecision> {
    let cancel_step_types = map_cancellation_step_types(flow, decision.cancel_step_types)?;
    let global_types: HashSet<&str> = cancel_step_types.iter().map(String::as_str).collect();
    let cancel_sibling_step_types =
        map_cancellation_step_types(flow, decision.cancel_sibling_step_types)?
            .into_iter()
            .filter(|step_type| !global_types.contains(step_type.as_str()))
            .collect();
    let mut mapped = match decision.kind {
        StepDecisionKind::Next(movements) => {
            if movements.is_empty() {
                return Err(HandlerError::new(
                    "dex_sdk::HandlerError",
                    "go_to_many requires a movement",
                ));
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
    }?;
    mapped.cancel_step_types = cancel_step_types;
    mapped.cancel_sibling_step_types = cancel_sibling_step_types;
    Ok(mapped)
}

fn map_cancellation_step_types(
    flow: &RegisteredFlow,
    selected: Vec<&'static str>,
) -> HandlerResult<Vec<String>> {
    let mut step_types = Vec::with_capacity(selected.len());
    let mut seen = HashSet::with_capacity(selected.len());
    for step_type in selected {
        if !flow.steps.contains_key(step_type) {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "cancellation Step does not belong to Flow",
            ));
        }
        if !seen.insert(step_type) {
            continue;
        }
        step_types.push(step_type.to_string());
    }
    Ok(step_types)
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
    let target = flow.steps.get(movement.target_step_type).ok_or_else(|| {
        HandlerError::new(
            "dex_sdk::HandlerError",
            "Step movement target does not belong to Flow",
        )
    })?;
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
    registry: &'a Registry,
    flow: &'a RegisteredFlow,
    used_ids: HashSet<String>,
    ids_by_condition: HashMap<usize, String>,
    timers: Vec<TimerCondition>,
    channels: Vec<ChannelCondition>,
    sub_flows: Vec<ProtoSubFlowCondition>,
}

impl<'a> ConditionMapper<'a> {
    fn new(registry: &'a Registry, flow: &'a RegisteredFlow) -> Self {
        Self {
            registry,
            flow,
            used_ids: HashSet::new(),
            ids_by_condition: HashMap::new(),
            timers: Vec::new(),
            channels: Vec::new(),
            sub_flows: Vec::new(),
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
                "dex_sdk::HandlerError",
                "any_combination_of requires every Condition to have an ID",
            ));
        }
        if id_was_provided && id.is_empty() {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "empty Condition ID",
            ));
        }
        if !id.is_empty() && self.used_ids.contains(&id) {
            return Err(HandlerError::new(
                "dex_sdk::HandlerError",
                "duplicate Condition ID",
            ));
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
                    HandlerError::new(
                        "dex_sdk::HandlerError",
                        format!("Channel is not registered: {name}"),
                    )
                })?;
                let channel_name = match definition.kind {
                    PersistenceKind::Channel if instance.is_none() => name,
                    PersistenceKind::ChannelMap => physical_name(
                        &name,
                        instance.as_deref().ok_or_else(|| {
                            HandlerError::new(
                                "dex_sdk::HandlerError",
                                format!("ChannelMap {name} requires an instance"),
                            )
                        })?,
                    ),
                    _ => {
                        return Err(HandlerError::new(
                            "dex_sdk::HandlerError",
                            format!("Channel is not registered: {name}"),
                        ));
                    }
                };
                self.channels.push(ChannelCondition {
                    condition_id: id.clone(),
                    channel_name,
                    at_least: optional_count(at_least)?,
                    at_most: optional_count(at_most)?,
                });
            }
            ConditionKind::SubFlow(definition) => {
                let target = self
                    .registry
                    .flow(definition.flow_type)
                    .map_err(handler_error)?;
                if target.type_id != definition.type_id {
                    return Err(HandlerError::new(
                        "dex_sdk::HandlerError",
                        format!(
                            "SubFlow {} does not match the registered Rust type",
                            definition.flow_type
                        ),
                    ));
                }
                let start = target.start_step.as_ref().ok_or_else(|| {
                    HandlerError::new(
                        "dex_sdk::HandlerError",
                        format!("SubFlow {} has no starting Step", definition.flow_type),
                    )
                })?;
                let step_options =
                    map_step_options(target, target.handler.step_options(start.name)?)?;
                let options = map_sub_flow_options(target, &definition.options)?;
                self.sub_flows.push(ProtoSubFlowCondition {
                    condition_id: id.clone(),
                    sub_flow_type: target.name.to_string(),
                    start_step_type: start.name.to_string(),
                    step_input: Some(definition.input),
                    step_options: Some(step_options),
                    options: Some(options),
                    sub_flow_index: i32::try_from(self.sub_flows.len()).map_err(|_| {
                        HandlerError::new("dex_sdk::HandlerError", "too many SubFlow Conditions")
                    })?,
                });
            }
        }
        Ok(id)
    }
}

fn map_sub_flow_options(
    target: &RegisteredFlow,
    options: &crate::SubFlowOptions,
) -> HandlerResult<ProtoSubFlowOptions> {
    let flow_timeout_seconds = optional_seconds(options.timeout)?;
    let flow_timeout_policy = crate::client::map_flow_timeout_policy(
        target,
        flow_timeout_seconds,
        options.timeout_policy,
    )
    .map_err(handler_error)?;
    let attributes = options
        .attributes
        .iter()
        .map(|attribute| {
            let logical_name = attribute.key.split('/').next().unwrap_or(&attribute.key);
            if !target.persistence.contains_key(logical_name) {
                return Err(HandlerError::new(
                    "dex_sdk::HandlerError",
                    format!("SubFlow Attribute is not registered: {}", attribute.key),
                ));
            }
            Ok(AttributeWrite {
                key: attribute.key.clone(),
                value: Some(attribute.value.encode().map_err(handler_error)?),
                index_config: attribute.index_config.clone(),
                sync_config: attribute.sync_config,
            })
        })
        .collect::<HandlerResult<Vec<_>>>()?;
    Ok(ProtoSubFlowOptions {
        reuse_policy: match options.reuse_policy {
            SubFlowReusePolicy::Attach => ProtoSubFlowReusePolicy::Attach,
            SubFlowReusePolicy::RestartIfPreviousExitsAbnormally => {
                ProtoSubFlowReusePolicy::RestartIfPreviousExitsAbnormally
            }
            SubFlowReusePolicy::AlwaysRestart => ProtoSubFlowReusePolicy::AlwaysRestart,
        } as i32,
        flow_timeout_seconds,
        flow_timeout_policy,
        flow_start_delay_seconds: optional_seconds(options.start_delay)?,
        retry_policy: options
            .retry_policy
            .clone()
            .map(crate::client::map_flow_retry)
            .transpose()
            .map_err(handler_error)?,
        attributes,
        flow_config_override: options
            .config_override
            .as_ref()
            .map(|config| crate::client::map_flow_config(Some(config), None))
            .transpose()
            .map_err(handler_error)?,
    })
}

fn take_entry_values(entries: &mut [Kv]) -> HandlerResult<Vec<dex_protocol::dex::Value>> {
    entries
        .iter_mut()
        .map(|entry| {
            entry.value.take().ok_or_else(|| {
                HandlerError::new(
                    "dex_sdk::HandlerError",
                    format!("{} has no Value", entry.key),
                )
            })
        })
        .collect()
}

fn restore_entry_values(
    entries: &mut [Kv],
    values: impl IntoIterator<Item = dex_protocol::dex::Value>,
) -> HandlerResult<()> {
    let mut values = values.into_iter();
    for entry in entries {
        entry.value = Some(values.next().ok_or_else(|| {
            HandlerError::new("dex_sdk::HandlerError", "hydrated Value count mismatch")
        })?);
    }
    if values.next().is_some() {
        return Err(HandlerError::new(
            "dex_sdk::HandlerError",
            "hydrated Value count mismatch",
        ));
    }
    Ok(())
}

fn restore_n_entry_values(
    entries: &mut [Kv],
    values: &mut impl Iterator<Item = dex_protocol::dex::Value>,
    count: usize,
) -> HandlerResult<()> {
    if entries.len() != count {
        return Err(HandlerError::new(
            "dex_sdk::HandlerError",
            "hydrated Value count mismatch",
        ));
    }
    for entry in entries {
        entry.value = Some(values.next().ok_or_else(|| {
            HandlerError::new("dex_sdk::HandlerError", "hydrated Value count mismatch")
        })?);
    }
    Ok(())
}

fn require_conditions(conditions: &[Condition]) -> HandlerResult<()> {
    if conditions.is_empty() {
        Err(HandlerError::new(
            "dex_sdk::HandlerError",
            "Wait requires at least one Condition",
        ))
    } else {
        Ok(())
    }
}

fn optional_count(value: Option<usize>) -> HandlerResult<Option<i32>> {
    value
        .map(|value| {
            i32::try_from(value).map_err(|_| {
                HandlerError::new("dex_sdk::HandlerError", "channel count exceeds int32")
            })
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
        return Err(HandlerError::new(
            "dex_sdk::HandlerError",
            "Duration must use whole seconds",
        ));
    }
    i32::try_from(duration.as_secs())
        .map_err(|_| HandlerError::new("dex_sdk::HandlerError", "Duration exceeds int32"))
}

fn seconds64(duration: Duration) -> HandlerResult<i64> {
    if duration.subsec_nanos() != 0 {
        return Err(HandlerError::new(
            "dex_sdk::HandlerError",
            "Duration must use whole seconds",
        ));
    }
    i64::try_from(duration.as_secs())
        .map_err(|_| HandlerError::new("dex_sdk::HandlerError", "Duration exceeds int64"))
}

fn handler_error(error: SdkError) -> HandlerError {
    HandlerError::from_error(error)
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
            &registry,
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
            &registry,
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
            &registry,
            flow,
            Wait::any_combination_of([ConditionCombination::all_of([channel.for_one()])]),
        )
        .expect_err("unnamed combination must fail");
        assert!(missing.to_string().contains("requires every Condition"));

        let duplicate = map_wait(
            &registry,
            flow,
            Wait::any_combination_of([ConditionCombination::all_of([
                channel.for_one().with_id("same"),
                Timer::by_duration(Duration::from_secs(1)).with_id("same"),
            ])]),
        )
        .expect_err("duplicate IDs must fail");
        assert!(duplicate.to_string().contains("duplicate Condition ID"));

        let empty = map_wait(
            &registry,
            flow,
            Wait::all_of([channel.for_one().with_id("")]),
        )
        .expect_err("empty ID must fail");
        assert!(empty.to_string().contains("empty Condition ID"));
    }
}
