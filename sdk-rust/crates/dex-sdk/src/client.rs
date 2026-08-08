// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::sync::Arc;
use std::time::Duration;

use crate::{
    Attribute, AttributeMap, BlobCache, Channel, ChannelMap, ClientOptions, Flow, FlowConfig,
    FlowInfo, Registry, ResetFlowOptions, Rpc, SdkError, SdkResult, StartFlowOptions,
    StepExecutionId, StopFlowOptions, TimerId, Value,
};

pub struct Client {
    _private: (),
}

impl Client {
    pub fn new(_registry: Registry, _blob_cache: Arc<BlobCache>, _options: ClientOptions) -> Self {
        Self { _private: () }
    }

    pub fn start_flow<SomeFlow: Flow>(
        &self,
        _flow: &SomeFlow,
        _flow_id: &str,
        _input: SomeFlow::StartInput,
    ) -> SdkResult<String> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn start_flow_with_options<SomeFlow: Flow>(
        &self,
        _flow: &SomeFlow,
        _flow_id: &str,
        _input: SomeFlow::StartInput,
        _options: StartFlowOptions,
    ) -> SdkResult<String> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn invoke_rpc<Input: Value, Output: Value>(
        &self,
        _flow_id: &str,
        _rpc: Rpc<Input, Output>,
        _input: Input,
    ) -> SdkResult<Output> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn invoke_rpc_without_input<Output: Value>(
        &self,
        _flow_id: &str,
        _rpc: Rpc<(), Output>,
    ) -> SdkResult<Output> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn get_attribute<T: Value>(
        &self,
        _flow_id: &str,
        _attribute: &Attribute<T>,
    ) -> SdkResult<Option<T>> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn get_attribute_map<T: Value>(
        &self,
        _flow_id: &str,
        _attribute: &AttributeMap<T>,
        _instance: &str,
    ) -> SdkResult<Option<T>> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn set_attribute<T: Value>(
        &self,
        _flow_id: &str,
        _attribute: &Attribute<T>,
        _value: T,
    ) -> SdkResult<()> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn set_attribute_map<T: Value>(
        &self,
        _flow_id: &str,
        _attribute: &AttributeMap<T>,
        _instance: &str,
        _value: T,
    ) -> SdkResult<()> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn publish<T: Value>(
        &self,
        _flow_id: &str,
        _channel: &Channel<T>,
        _value: T,
    ) -> SdkResult<()> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn publish_many<T: Value>(
        &self,
        _flow_id: &str,
        _channel: &Channel<T>,
        _values: impl IntoIterator<Item = T>,
    ) -> SdkResult<()> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn publish_map<T: Value>(
        &self,
        _flow_id: &str,
        _channel: &ChannelMap<T>,
        _instance: &str,
        _values: impl IntoIterator<Item = T>,
    ) -> SdkResult<()> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn wait_for_flow<Output: Value>(&self, _flow_id: &str) -> SdkResult<Output> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn wait_for_flow_with_timeout<Output: Value>(
        &self,
        _flow_id: &str,
        _timeout: Duration,
    ) -> SdkResult<Output> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn describe_flow(&self, _flow_id: &str) -> SdkResult<FlowInfo> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn stop_flow(&self, _flow_id: &str, _options: StopFlowOptions) -> SdkResult<()> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn reset_flow(&self, _flow_id: &str, _options: ResetFlowOptions) -> SdkResult<String> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn skip_timer(
        &self,
        _flow_id: &str,
        _step_execution: StepExecutionId,
        _timer: TimerId,
    ) -> SdkResult<()> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn wait_for_step_completion(
        &self,
        _flow_id: &str,
        _step_execution: StepExecutionId,
        _timeout: Duration,
    ) -> SdkResult<()> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn update_flow_config(&self, _flow_id: &str, _config: FlowConfig) -> SdkResult<()> {
        Err(SdkError::NotImplemented("Client transport"))
    }

    pub fn trigger_continue_as_new(&self, _flow_id: &str) -> SdkResult<()> {
        Err(SdkError::NotImplemented("Client transport"))
    }
}
