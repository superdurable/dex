// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::any::TypeId;
use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::{Duration, SystemTime};

use dex_protocol::dex::flow_service_client::FlowServiceClient;
use dex_protocol::dex::{
    ActiveStepSearchMode as ProtoSearchMode, AttributeWrite, FlowAlreadyStartedOptions,
    FlowConfig as ProtoFlowConfig, FlowResetType, FlowRetryPolicy, FlowStartOptions,
    FlowStatus as ProtoFlowStatus, GetAttributesRequest, GetFlowSummaryRequest,
    IdReusePolicy as ProtoIdReusePolicy, InvokeRpcRequest, PublishToChannelRequest,
    ResetFlowRequest, SearchFlowsRequest, SetAttributesRequest, SkipTimerRequest, StartFlowRequest,
    StepDurability as ProtoStepDurability, StopFlowRequest, StopType as ProtoStopType,
    TriggerContinueAsNewRequest, UpdateFlowConfigRequest, WaitForFlowRequest, WaitForFlowResponse,
    WaitForStepCompletionRequest, WorkerTarget as ProtoWorkerTarget,
};
use tokio::runtime::Runtime;
use tonic::transport::Endpoint;
use uuid::Uuid;

use crate::reset_flow_options::ResetPoint;
use crate::sdk_error::{FlowTargetRequirement, ServiceError};
use crate::stop_flow_options::StopType;
use crate::value_hydrator::ValueHydrator;
use crate::value_mapper;
use crate::worker_dispatcher::map_step_options;
use crate::{
    ActiveStepSearchMode, Attribute, AttributeMap, BlobCache, Channel, ChannelMap, ClientOptions,
    Flow, FlowConfig, FlowErrorType, FlowInfo, FlowStatus, IdReusePolicy, Registry,
    ResetFlowOptions, RetryPolicy, Rpc, SdkError, SdkResult, SearchFlowEntry, SearchFlowsPage,
    StartFlowOptions, StepDurability, StepExecutionId, StopFlowOptions, TimerId, Value,
    WorkerTarget,
};

pub struct Client {
    runtime: Runtime,
    registry: Registry,
    service: FlowServiceClient<tonic::transport::Channel>,
    hydrator: ValueHydrator,
    options: ClientOptions,
}

impl Client {
    pub fn new(registry: Registry, blob_cache: Arc<BlobCache>, options: ClientOptions) -> Self {
        Self::try_new(registry, blob_cache, options)
            .unwrap_or_else(|error| panic!("cannot create Dex Client: {error}"))
    }

    pub fn try_new(
        registry: Registry,
        blob_cache: Arc<BlobCache>,
        options: ClientOptions,
    ) -> SdkResult<Self> {
        let runtime = Runtime::new().map_err(service_error)?;
        let endpoint = Endpoint::from_shared(endpoint_address(options.server_address_value()))
            .map_err(service_error)?;
        let service = {
            let _runtime_guard = runtime.enter();
            FlowServiceClient::new(endpoint.connect_lazy())
        };
        let hydrator = ValueHydrator::new(service.clone(), blob_cache);
        Ok(Self {
            runtime,
            registry,
            service,
            hydrator,
            options,
        })
    }

    pub fn start_flow<SomeFlow: Flow>(
        &self,
        flow: &SomeFlow,
        flow_id: &str,
        input: SomeFlow::StartInput,
    ) -> SdkResult<String> {
        self.start_flow_with_options(flow, flow_id, input, StartFlowOptions::new())
    }

    pub fn start_flow_with_options<SomeFlow: Flow>(
        &self,
        flow: &SomeFlow,
        flow_id: &str,
        input: SomeFlow::StartInput,
        options: StartFlowOptions,
    ) -> SdkResult<String> {
        require_name(flow_id, "Flow ID")?;
        let registered = self.registry.flow(flow.flow_type())?;
        if registered.type_id != TypeId::of::<SomeFlow>() {
            return Err(SdkError::FlowDefinition {
                message: format!(
                    "Flow {} does not match the registered Rust type",
                    flow.flow_type()
                ),
            });
        }
        let (start_step_type, step_input, step_options) = match &registered.start_step {
            Some(step) => (
                step.name.to_string(),
                Some(value_mapper::encode(&input)?),
                Some(
                    map_step_options(
                        registered,
                        registered
                            .handler
                            .step_options(step.name)
                            .map_err(sdk_handler_error)?,
                    )
                    .map_err(sdk_handler_error)?,
                ),
            ),
            None => (String::new(), None, None),
        };
        let request = StartFlowRequest {
            flow_id: flow_id.to_string(),
            flow_type: registered.name.to_string(),
            flow_timeout_seconds: optional_seconds(options.timeout)?,
            start_step_type,
            step_input,
            step_options,
            flow_start_options: Some(self.map_start_options(registered, &options)?),
            request_id: options
                .request_id
                .clone()
                .unwrap_or_else(|| Uuid::new_v4().to_string()),
        };
        let mut service = self.service.clone();
        self.runtime.block_on(async {
            service
                .start_flow(request)
                .await
                .map(|response| response.into_inner().run_id)
                .map_err(|status| {
                    SdkError::from_status(
                        status,
                        "start_flow",
                        Some(flow_id),
                        FlowTargetRequirement::None,
                    )
                })
        })
    }

    pub fn invoke_rpc<Input: Value, Output: Value>(
        &self,
        flow_id: &str,
        rpc: Rpc<Input, Output>,
        input: Input,
    ) -> SdkResult<Output> {
        self.do_invoke_rpc(flow_id, rpc.name(), &input)
    }

    pub fn invoke_rpc_without_input<Output: Value>(
        &self,
        flow_id: &str,
        rpc: Rpc<(), Output>,
    ) -> SdkResult<Output> {
        self.do_invoke_rpc(flow_id, rpc.name(), &())
    }

    pub fn get_attribute<T: Value>(
        &self,
        flow_id: &str,
        attribute: &Attribute<T>,
    ) -> SdkResult<Option<T>> {
        self.get_attribute_value(flow_id, attribute.name())
    }

    pub fn get_attribute_map<T: Value>(
        &self,
        flow_id: &str,
        attribute: &AttributeMap<T>,
        instance: &str,
    ) -> SdkResult<Option<T>> {
        self.get_attribute_value(
            flow_id,
            &crate::registry::physical_name(attribute.name(), instance),
        )
    }

    pub fn set_attribute<T: Value>(
        &self,
        flow_id: &str,
        attribute: &Attribute<T>,
        value: T,
    ) -> SdkResult<()> {
        self.set_attribute_value(
            flow_id,
            attribute.name(),
            &value,
            attribute.index().map(|index| index.proto_config(false)),
        )
    }

    pub fn set_attribute_map<T: Value>(
        &self,
        flow_id: &str,
        attribute: &AttributeMap<T>,
        instance: &str,
        value: T,
    ) -> SdkResult<()> {
        self.set_attribute_value(
            flow_id,
            &crate::registry::physical_name(attribute.name(), instance),
            &value,
            attribute.index().map(|index| index.proto_config(true)),
        )
    }

    pub fn publish<T: Value>(
        &self,
        flow_id: &str,
        channel: &Channel<T>,
        value: T,
    ) -> SdkResult<()> {
        self.publish_values(flow_id, channel.name(), [value])
    }

    pub fn publish_many<T: Value>(
        &self,
        flow_id: &str,
        channel: &Channel<T>,
        values: impl IntoIterator<Item = T>,
    ) -> SdkResult<()> {
        self.publish_values(flow_id, channel.name(), values)
    }

    pub fn publish_map<T: Value>(
        &self,
        flow_id: &str,
        channel: &ChannelMap<T>,
        instance: &str,
        values: impl IntoIterator<Item = T>,
    ) -> SdkResult<()> {
        self.publish_values(
            flow_id,
            &crate::registry::physical_name(channel.name(), instance),
            values,
        )
    }

    pub fn wait_for_flow<Output: Value>(&self, flow_id: &str) -> SdkResult<Output> {
        self.wait_for_flow_response(flow_id, None)
            .and_then(|response| self.decode_flow_output(response))
    }

    pub fn wait_for_flow_with_timeout<Output: Value>(
        &self,
        flow_id: &str,
        timeout: Duration,
    ) -> SdkResult<Output> {
        self.wait_for_flow_response(flow_id, Some(timeout))
            .and_then(|response| self.decode_flow_output(response))
    }

    pub fn describe_flow(&self, flow_id: &str) -> SdkResult<FlowInfo> {
        let mut service = self.service.clone();
        let response = self.runtime.block_on(async {
            service
                .get_flow_summary(GetFlowSummaryRequest {
                    flow_id: flow_id.to_string(),
                    run_id: String::new(),
                })
                .await
                .map(|response| response.into_inner())
                .map_err(|status| {
                    SdkError::from_status(
                        status,
                        "describe_flow",
                        Some(flow_id),
                        FlowTargetRequirement::Existing,
                    )
                })
        })?;
        let execution = response
            .flow_execution_id
            .ok_or_else(|| service_error("GetFlowSummary omitted FlowExecutionID"))?;
        Ok(FlowInfo {
            flow_id: execution.flow_id,
            run_id: execution.run_id,
            flow_type: response.flow_type,
            status: map_flow_status(response.flow_status)?,
            started_at: response
                .start_time
                .map_or(SystemTime::UNIX_EPOCH, timestamp),
        })
    }

    pub fn search_flows(&self, query: &str, page_size: i32) -> SdkResult<SearchFlowsPage> {
        self.search_flows_page(query, page_size, "")
    }

    pub fn search_flows_page(
        &self,
        query: &str,
        page_size: i32,
        next_page_token: &str,
    ) -> SdkResult<SearchFlowsPage> {
        if page_size < 0 {
            return Err(invalid("search page size must not be negative"));
        }
        let mut service = self.service.clone();
        self.runtime.block_on(async {
            let response = service
                .search_flows(SearchFlowsRequest {
                    query: query.to_string(),
                    page_size,
                    next_page_token: next_page_token.to_string(),
                })
                .await
                .map(|response| response.into_inner())
                .map_err(|status| {
                    SdkError::from_status(status, "search_flows", None, FlowTargetRequirement::None)
                })?;
            let values = response
                .flow_runs
                .iter()
                .flat_map(|entry| entry.indexed_attributes.iter())
                .map(|attribute| {
                    attribute
                        .value
                        .clone()
                        .ok_or_else(|| invalid("Indexed Attribute has no Value"))
                })
                .collect::<SdkResult<Vec<_>>>()?;
            let mut values = self.hydrator.hydrate_all(values).await?.into_iter();
            let mut flows = Vec::with_capacity(response.flow_runs.len());
            for entry in response.flow_runs {
                let mut indexed_attributes = BTreeMap::new();
                for attribute in entry.indexed_attributes {
                    let value = values
                        .next()
                        .ok_or_else(|| invalid("Indexed Attribute hydration count mismatch"))?;
                    indexed_attributes.insert(attribute.key, value_mapper::decode_untyped(&value)?);
                }
                flows.push(SearchFlowEntry {
                    flow_id: entry.flow_id,
                    run_id: entry.run_id,
                    flow_type: entry.flow_type,
                    status: map_flow_status(entry.flow_status)?,
                    started_at: entry.start_time.map(timestamp),
                    closed_at: entry.close_time.map(timestamp),
                    indexed_attributes,
                });
            }
            Ok(SearchFlowsPage {
                flows,
                next_page_token: response.next_page_token,
            })
        })
    }

    pub fn stop_flow(&self, flow_id: &str, options: StopFlowOptions) -> SdkResult<()> {
        let stop_type = match options.stop_type {
            StopType::Cancel => ProtoStopType::Cancel,
            StopType::Terminate => ProtoStopType::Terminate,
            StopType::Fail => ProtoStopType::Fail,
        };
        self.call_empty(
            "stop_flow",
            Some(flow_id),
            FlowTargetRequirement::Active,
            |mut service| async move {
                service
                    .stop_flow(StopFlowRequest {
                        flow_id: flow_id.to_string(),
                        run_id: String::new(),
                        reason: options.reason.unwrap_or_default(),
                        stop_type: stop_type as i32,
                    })
                    .await
            },
        )
    }

    pub fn reset_flow(&self, flow_id: &str, options: ResetFlowOptions) -> SdkResult<String> {
        let mut request = ResetFlowRequest {
            flow_id: flow_id.to_string(),
            run_id: String::new(),
            reset_type: FlowResetType::Unspecified as i32,
            history_event_id: 0,
            reason: options.reason.unwrap_or_default(),
            history_event_time: String::new(),
            step_type: String::new(),
            step_execution_id: String::new(),
            skip_channel_messages_reapply: options.skip_channel_messages_reapply,
            skip_locking_rpc_reapply: options.skip_locking_rpc_reapply,
        };
        match options.point {
            ResetPoint::Beginning => request.reset_type = FlowResetType::Beginning as i32,
            ResetPoint::HistoryEventId(event_id) => {
                request.reset_type = FlowResetType::HistoryEventId as i32;
                request.history_event_id = i32::try_from(event_id)
                    .map_err(|_| invalid("history event ID exceeds int32"))?;
            }
            ResetPoint::HistoryEventTime(time) => {
                request.reset_type = FlowResetType::HistoryEventTime as i32;
                request.history_event_time = rfc3339(time)?;
            }
            ResetPoint::StepType(step_type) => {
                request.reset_type = FlowResetType::StepType as i32;
                request.step_type = step_type.to_string();
            }
            ResetPoint::StepExecution(execution) => {
                request.reset_type = FlowResetType::StepExecutionId as i32;
                request.step_execution_id =
                    format!("{}-{}", execution.step_type, execution.execution_number);
            }
        }
        let mut service = self.service.clone();
        self.runtime.block_on(async {
            service
                .reset_flow(request)
                .await
                .map(|response| response.into_inner().run_id)
                .map_err(|status| {
                    SdkError::from_status(
                        status,
                        "reset_flow",
                        Some(flow_id),
                        FlowTargetRequirement::Existing,
                    )
                })
        })
    }

    pub fn skip_timer(
        &self,
        flow_id: &str,
        step_execution: StepExecutionId,
        timer: TimerId,
    ) -> SdkResult<()> {
        let (timer_condition_id, timer_condition_index) = match timer {
            TimerId::ConditionId(id) => (id, None),
            TimerId::ConditionIndex(index) => (
                String::new(),
                Some(i32::try_from(index).map_err(|_| invalid("timer index exceeds int32"))?),
            ),
        };
        self.call_empty(
            "skip_timer",
            Some(flow_id),
            FlowTargetRequirement::Active,
            |mut service| async move {
                service
                    .skip_timer(SkipTimerRequest {
                        flow_id: flow_id.to_string(),
                        run_id: String::new(),
                        step_execution_id: format!(
                            "{}-{}",
                            step_execution.step_type, step_execution.execution_number
                        ),
                        timer_condition_id,
                        timer_condition_index,
                    })
                    .await
            },
        )
    }

    pub fn wait_for_step_completion(
        &self,
        flow_id: &str,
        step_execution: StepExecutionId,
        timeout: Duration,
    ) -> SdkResult<()> {
        let wait_time_seconds = seconds32(timeout)?;
        self.call_empty(
            "wait_for_step_completion",
            Some(flow_id),
            FlowTargetRequirement::Active,
            |mut service| async move {
                service
                    .wait_for_step_completion(WaitForStepCompletionRequest {
                        flow_id: flow_id.to_string(),
                        step_type: step_execution.step_type.to_string(),
                        step_execution_number: step_execution.execution_number.to_string(),
                        wait_time_seconds,
                        request_id: Uuid::new_v4().to_string(),
                    })
                    .await
            },
        )
    }

    pub fn update_flow_config(&self, flow_id: &str, config: FlowConfig) -> SdkResult<()> {
        let flow_config = self.map_flow_config(Some(&config))?;
        self.call_empty(
            "update_flow_config",
            Some(flow_id),
            FlowTargetRequirement::Active,
            |mut service| async move {
                service
                    .update_flow_config(UpdateFlowConfigRequest {
                        flow_id: flow_id.to_string(),
                        run_id: String::new(),
                        flow_config: Some(flow_config),
                    })
                    .await
            },
        )
    }

    pub fn trigger_continue_as_new(&self, flow_id: &str) -> SdkResult<()> {
        self.call_empty(
            "trigger_continue_as_new",
            Some(flow_id),
            FlowTargetRequirement::Active,
            |mut service| async move {
                service
                    .trigger_continue_as_new(TriggerContinueAsNewRequest {
                        flow_id: flow_id.to_string(),
                        run_id: String::new(),
                    })
                    .await
            },
        )
    }

    pub fn health_check(&self) -> SdkResult<()> {
        self.call_empty(
            "health_check",
            None,
            FlowTargetRequirement::None,
            |mut service| async move { service.health_check(()).await },
        )
    }

    fn do_invoke_rpc<Input: Value, Output: Value>(
        &self,
        flow_id: &str,
        rpc_name: &str,
        input: &Input,
    ) -> SdkResult<Output> {
        let rpc = self.registry.rpc(rpc_name)?;
        let request = InvokeRpcRequest {
            flow_id: flow_id.to_string(),
            run_id: String::new(),
            rpc_name: rpc_name.to_string(),
            input: Some(value_mapper::encode(input)?),
            timeout_seconds: optional_seconds(rpc.timeout)?,
            lock_attribute_keys: rpc.locks.iter().map(|lock| lock.physical_name()).collect(),
            request_id: Uuid::new_v4().to_string(),
        };
        let mut service = self.service.clone();
        let output = self.runtime.block_on(async {
            service
                .invoke_rpc(request)
                .await
                .map(|response| response.into_inner().output)
                .map_err(|status| {
                    SdkError::from_status(
                        status,
                        "invoke_rpc",
                        Some(flow_id),
                        FlowTargetRequirement::Active,
                    )
                })
        })?;
        let output = output.ok_or_else(|| invalid("InvokeRPC omitted output"))?;
        let output = self.runtime.block_on(self.hydrator.hydrate(output))?;
        value_mapper::decode(&output)
    }

    fn get_attribute_value<T: Value>(&self, flow_id: &str, key: &str) -> SdkResult<Option<T>> {
        let mut service = self.service.clone();
        let response = self.runtime.block_on(async {
            service
                .get_attributes(GetAttributesRequest {
                    flow_id: flow_id.to_string(),
                    run_id: String::new(),
                    keys: vec![key.to_string()],
                    all_keys: false,
                })
                .await
                .map(|response| response.into_inner())
                .map_err(|status| {
                    SdkError::from_status(
                        status,
                        "get_attribute",
                        Some(flow_id),
                        FlowTargetRequirement::Existing,
                    )
                })
        })?;
        let Some(entry) = response.attributes.into_iter().next() else {
            return Ok(None);
        };
        let value = entry
            .value
            .ok_or_else(|| invalid("GetAttributes returned an empty Value"))?;
        let value = self.runtime.block_on(self.hydrator.hydrate(value))?;
        value_mapper::decode(&value).map(Some)
    }

    fn set_attribute_value<T: Value>(
        &self,
        flow_id: &str,
        key: &str,
        value: &T,
        index_config: Option<dex_protocol::dex::IndexConfig>,
    ) -> SdkResult<()> {
        let write = AttributeWrite {
            key: key.to_string(),
            value: Some(value_mapper::encode(value)?),
            index_config,
            sync_config: None,
        };
        self.call_empty(
            "set_attribute",
            Some(flow_id),
            FlowTargetRequirement::Active,
            |mut service| async move {
                service
                    .set_attributes(SetAttributesRequest {
                        flow_id: flow_id.to_string(),
                        run_id: String::new(),
                        attributes: vec![write],
                        request_id: Uuid::new_v4().to_string(),
                    })
                    .await
            },
        )
    }

    fn publish_values<T: Value>(
        &self,
        flow_id: &str,
        channel_name: &str,
        values: impl IntoIterator<Item = T>,
    ) -> SdkResult<()> {
        let messages = values
            .into_iter()
            .map(|value| {
                Ok(dex_protocol::dex::ChannelMessage {
                    channel_name: channel_name.to_string(),
                    value: Some(value_mapper::encode(&value)?),
                })
            })
            .collect::<SdkResult<Vec<_>>>()?;
        self.call_empty(
            "publish",
            Some(flow_id),
            FlowTargetRequirement::Active,
            |mut service| async move {
                service
                    .publish_to_channel(PublishToChannelRequest {
                        flow_id: flow_id.to_string(),
                        run_id: String::new(),
                        messages,
                    })
                    .await
            },
        )
    }

    fn wait_for_flow_response(
        &self,
        flow_id: &str,
        timeout: Option<Duration>,
    ) -> SdkResult<WaitForFlowResponse> {
        let mut service = self.service.clone();
        let response = self.runtime.block_on(async {
            service
                .wait_for_flow(WaitForFlowRequest {
                    flow_id: flow_id.to_string(),
                    run_id: String::new(),
                    needs_results: true,
                    wait_time_seconds: optional_seconds(timeout)?,
                })
                .await
                .map(|response| response.into_inner())
                .map_err(|status| {
                    SdkError::from_status(
                        status,
                        "wait_for_flow",
                        Some(flow_id),
                        FlowTargetRequirement::Existing,
                    )
                })
        });
        let response = response?;
        let status = map_flow_status(response.flow_status)?;
        if status != FlowStatus::Completed {
            let flow = self.describe_flow(flow_id)?;
            return Err(SdkError::FlowUncompleted {
                run_id: flow.run_id,
                status,
                error_type: map_flow_error_type(response.error_type),
                message: (!response.error_message.is_empty()).then_some(response.error_message),
                result_count: response.results.len(),
            });
        }
        Ok(response)
    }

    fn decode_flow_output<Output: Value>(
        &self,
        response: WaitForFlowResponse,
    ) -> SdkResult<Output> {
        let output = response
            .results
            .into_iter()
            .rev()
            .find_map(|result| result.completed_step_output)
            .ok_or_else(|| invalid("completed Flow has no Step output"))?;
        let output = self.runtime.block_on(self.hydrator.hydrate(output))?;
        value_mapper::decode(&output)
    }

    fn map_start_options(
        &self,
        flow: &crate::registry::RegisteredFlow,
        options: &StartFlowOptions,
    ) -> SdkResult<FlowStartOptions> {
        let attributes = options
            .attributes
            .iter()
            .map(|attribute| {
                let logical_name = attribute.key.split('/').next().unwrap_or(&attribute.key);
                if !flow.persistence.contains_key(logical_name) {
                    return Err(invalid(format!(
                        "initial Attribute is not registered: {}",
                        attribute.key
                    )));
                }
                Ok(AttributeWrite {
                    key: attribute.key.clone(),
                    value: Some(attribute.value.encode()?),
                    index_config: attribute.index_config.clone(),
                    sync_config: None,
                })
            })
            .collect::<SdkResult<Vec<_>>>()?;
        let config = options.config_override.as_ref();
        let flow_config_override =
            if config.is_some() || self.options.worker_target_value().is_some() {
                Some(self.map_flow_config(config)?)
            } else {
                None
            };
        Ok(FlowStartOptions {
            id_reuse_policy: match options.id_reuse_policy {
                IdReusePolicy::Default => ProtoIdReusePolicy::Unspecified,
                IdReusePolicy::AllowIfPreviousFailed => {
                    ProtoIdReusePolicy::AllowIfPreviousExistsAbnormally
                }
                IdReusePolicy::AllowIfNotRunning => ProtoIdReusePolicy::AllowIfNoRunning,
                IdReusePolicy::Disallow => ProtoIdReusePolicy::DisallowReuse,
                IdReusePolicy::TerminateIfRunning => ProtoIdReusePolicy::AllowTerminateIfRunning,
            } as i32,
            cron_schedule: options.cron_schedule.clone().unwrap_or_default(),
            flow_start_delay_seconds: optional_seconds(options.start_delay)?,
            retry_policy: options
                .retry_policy
                .clone()
                .map(map_flow_retry)
                .transpose()?,
            attributes,
            flow_config_override,
            flow_already_started_options: Some(FlowAlreadyStartedOptions {
                ignore_already_started_error: options.ignore_already_started,
            }),
        })
    }

    fn map_flow_config(&self, config: Option<&FlowConfig>) -> SdkResult<ProtoFlowConfig> {
        let target = config
            .and_then(|config| config.worker_target.as_ref())
            .or_else(|| self.options.worker_target_value());
        Ok(ProtoFlowConfig {
            active_step_search_mode: config
                .and_then(|config| config.active_step_search_mode)
                .map(|mode| match mode {
                    ActiveStepSearchMode::All => ProtoSearchMode::EnabledForAll as i32,
                    ActiveStepSearchMode::WithWaitFor => {
                        ProtoSearchMode::EnabledForStepsWithWaitFor as i32
                    }
                    ActiveStepSearchMode::Disabled => ProtoSearchMode::Disabled as i32,
                }),
            continue_as_new_threshold: config
                .and_then(|config| config.continue_as_new_threshold)
                .map(i32::try_from)
                .transpose()
                .map_err(|_| invalid("continue-as-new threshold exceeds int32"))?,
            continue_as_new_page_size_in_bytes: config
                .and_then(|config| config.continue_as_new_page_size_bytes)
                .map(i32::try_from)
                .transpose()
                .map_err(|_| invalid("continue-as-new page size exceeds int32"))?,
            step_durability: config
                .and_then(|config| config.step_durability)
                .map(|durability| {
                    (match durability {
                        StepDurability::Default => ProtoStepDurability::Unspecified,
                        StepDurability::Sync => ProtoStepDurability::Sync,
                        StepDurability::Async => ProtoStepDurability::Async,
                    }) as i32
                }),
            worker_target: target.map(map_worker_target),
            attribute_sync_config_name: None,
        })
    }

    fn call_empty<Response, Future, Call>(
        &self,
        operation: &'static str,
        flow_id: Option<&str>,
        requirement: FlowTargetRequirement,
        call: Call,
    ) -> SdkResult<()>
    where
        Call: FnOnce(FlowServiceClient<tonic::transport::Channel>) -> Future,
        Future: std::future::Future<Output = Result<tonic::Response<Response>, tonic::Status>>,
    {
        let service = self.service.clone();
        self.runtime
            .block_on(call(service))
            .map(|_| ())
            .map_err(|status| SdkError::from_status(status, operation, flow_id, requirement))
    }
}

fn map_flow_retry(retry: RetryPolicy) -> SdkResult<FlowRetryPolicy> {
    Ok(FlowRetryPolicy {
        initial_interval_seconds: optional_seconds(retry.initial_interval)?,
        backoff_coefficient: retry.backoff_coefficient.unwrap_or_default() as f32,
        maximum_interval_seconds: optional_seconds(retry.maximum_interval)?,
        maximum_attempts: i32::try_from(retry.maximum_attempts.unwrap_or_default())
            .map_err(|_| invalid("maximum attempts exceed int32"))?,
    })
}

fn map_worker_target(target: &WorkerTarget) -> ProtoWorkerTarget {
    ProtoWorkerTarget {
        address: target.address().to_string(),
        is_headless_address: target.is_headless(),
    }
}

fn map_flow_status(status: i32) -> SdkResult<FlowStatus> {
    match ProtoFlowStatus::try_from(status).ok() {
        Some(ProtoFlowStatus::Running) => Ok(FlowStatus::Running),
        Some(ProtoFlowStatus::Completed) => Ok(FlowStatus::Completed),
        Some(ProtoFlowStatus::Failed) => Ok(FlowStatus::Failed),
        Some(ProtoFlowStatus::Timeout) => Ok(FlowStatus::TimedOut),
        Some(ProtoFlowStatus::Terminated) => Ok(FlowStatus::Terminated),
        Some(ProtoFlowStatus::Canceled) => Ok(FlowStatus::Canceled),
        Some(ProtoFlowStatus::ContinuedAsNew) => Ok(FlowStatus::ContinuedAsNew),
        _ => Err(invalid(format!("unknown Flow status {status}"))),
    }
}

fn map_flow_error_type(error_type: i32) -> Option<FlowErrorType> {
    use dex_protocol::dex::FlowErrorType as ProtoFlowErrorType;

    match ProtoFlowErrorType::try_from(error_type).ok() {
        Some(ProtoFlowErrorType::StepDecisionFailingFlow) => {
            Some(FlowErrorType::StepDecisionFailed)
        }
        Some(ProtoFlowErrorType::ClientApiFailingFlow) => Some(FlowErrorType::ClientApiFailed),
        Some(ProtoFlowErrorType::WorkerApiFail) => Some(FlowErrorType::WorkerApiFailed),
        Some(ProtoFlowErrorType::InvalidUserFlowCode) => Some(FlowErrorType::InvalidUserFlowCode),
        Some(ProtoFlowErrorType::Internal) => Some(FlowErrorType::Internal),
        _ => None,
    }
}

fn timestamp(timestamp: prost_types::Timestamp) -> SystemTime {
    if timestamp.seconds >= 0 {
        SystemTime::UNIX_EPOCH
            + Duration::from_secs(timestamp.seconds as u64)
            + Duration::from_nanos(timestamp.nanos.max(0) as u64)
    } else {
        SystemTime::UNIX_EPOCH - Duration::from_secs(timestamp.seconds.unsigned_abs())
    }
}

fn rfc3339(time: SystemTime) -> SdkResult<String> {
    let duration = time
        .duration_since(SystemTime::UNIX_EPOCH)
        .map_err(service_error)?;
    let days = (duration.as_secs() / 86_400) as i64;
    let seconds = duration.as_secs() % 86_400;
    let (year, month, day) = civil_from_days(days);
    let hour = seconds / 3_600;
    let minute = seconds % 3_600 / 60;
    let second = seconds % 60;
    Ok(format!(
        "{year:04}-{month:02}-{day:02}T{hour:02}:{minute:02}:{second:02}Z"
    ))
}

fn civil_from_days(days: i64) -> (i64, i64, i64) {
    let days = days + 719_468;
    let era = if days >= 0 { days } else { days - 146_096 } / 146_097;
    let day_of_era = days - era * 146_097;
    let year_of_era =
        (day_of_era - day_of_era / 1_460 + day_of_era / 36_524 - day_of_era / 146_096) / 365;
    let mut year = year_of_era + era * 400;
    let day_of_year = day_of_era - (365 * year_of_era + year_of_era / 4 - year_of_era / 100);
    let month_prime = (5 * day_of_year + 2) / 153;
    let day = day_of_year - (153 * month_prime + 2) / 5 + 1;
    let month = month_prime + if month_prime < 10 { 3 } else { -9 };
    year += i64::from(month <= 2);
    (year, month, day)
}

fn optional_seconds(duration: Option<Duration>) -> SdkResult<i32> {
    duration
        .map(seconds32)
        .transpose()
        .map(Option::unwrap_or_default)
}

fn seconds32(duration: Duration) -> SdkResult<i32> {
    if duration.subsec_nanos() != 0 {
        return Err(invalid("Duration must use whole seconds"));
    }
    i32::try_from(duration.as_secs()).map_err(|_| invalid("Duration exceeds int32"))
}

fn endpoint_address(address: &str) -> String {
    if address.contains("://") {
        address.to_string()
    } else {
        format!("http://{address}")
    }
}

fn require_name(value: &str, kind: &str) -> SdkResult<()> {
    if value.is_empty() {
        Err(invalid(format!("{kind} is required")))
    } else {
        Ok(())
    }
}

fn sdk_handler_error(error: impl std::fmt::Display) -> SdkError {
    invalid(error.to_string())
}

fn invalid(message: impl Into<String>) -> SdkError {
    SdkError::InvalidArgument {
        message: message.into(),
    }
}

fn service_error(error: impl std::fmt::Display) -> SdkError {
    SdkError::Service {
        service: ServiceError::local("client", error.to_string()),
    }
}
