// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

use std::sync::Arc;
use std::time::{Duration, SystemTime};

use crate::{
    Attribute, AttributeMap, BlobCache, Channel, ChannelMap, Flow, FlowConfig, ResetFlowOptions,
    Rpc, SdkError, SdkResult, StartFlowOptions, StepExecutionId, StopFlowOptions, TimerId, Value,
    WorkerTarget,
};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum FlowStatus {
    Running,
    Completed,
    Failed,
    TimedOut,
    Terminated,
    Canceled,
    ContinuedAsNew,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum FlowErrorType {
    StepDecisionFailed,
    ClientApiFailed,
    WorkerApiFailed,
    InvalidUserFlowCode,
    Internal,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FlowInfo {
    pub flow_id: String,
    pub run_id: String,
    pub flow_type: String,
    pub status: FlowStatus,
    pub started_at: SystemTime,
}

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

#[derive(Clone)]
pub struct Registry {
    _private: (),
}

#[derive(Clone, Debug)]
pub struct ClientOptions {
    server_address: String,
    worker_target: Option<WorkerTarget>,
}

impl ClientOptions {
    pub fn new() -> Self {
        Self {
            server_address: "127.0.0.1:8801".to_string(),
            worker_target: None,
        }
    }

    pub fn server_address(mut self, value: impl Into<String>) -> Self {
        self.server_address = value.into();
        self
    }

    pub fn worker_target(mut self, value: WorkerTarget) -> Self {
        self.worker_target = Some(value);
        self
    }
}

impl Default for ClientOptions {
    fn default() -> Self {
        Self::new()
    }
}

impl Registry {
    pub fn new() -> Self {
        Self { _private: () }
    }

    pub fn register<SomeFlow: Flow>(self, _flow: SomeFlow) -> Self {
        self
    }
}

impl Default for Registry {
    fn default() -> Self {
        Self::new()
    }
}

#[derive(Clone, Debug)]
pub struct WorkerOptions {
    bind_address: String,
    server_address: String,
    worker_target: Option<WorkerTarget>,
}

impl WorkerOptions {
    pub fn new() -> Self {
        Self {
            bind_address: "0.0.0.0:8803".to_string(),
            server_address: "127.0.0.1:8801".to_string(),
            worker_target: None,
        }
    }

    pub fn bind_address(mut self, value: impl Into<String>) -> Self {
        self.bind_address = value.into();
        self
    }

    pub fn server_address(mut self, value: impl Into<String>) -> Self {
        self.server_address = value.into();
        self
    }

    pub fn worker_target(mut self, value: WorkerTarget) -> Self {
        self.worker_target = Some(value);
        self
    }
}

impl Default for WorkerOptions {
    fn default() -> Self {
        Self::new()
    }
}

pub struct Worker {
    _private: (),
}

impl Worker {
    pub fn new(_registry: Registry, _blob_cache: Arc<BlobCache>, _options: WorkerOptions) -> Self {
        Self { _private: () }
    }
}
