// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

pub mod attribute;
pub mod channel;
pub mod client_apis;
pub mod rpc;
pub mod step;
pub mod subflow;
pub mod timer;

use dex_sdk::{Registry, SdkResult};

pub fn register(registry: Registry) -> SdkResult<Registry> {
    registry
        .register(step::flow::StepFlow::default())?
        .register(step::retry_flow::RetryFlow::default())?
        .register(attribute::flow::AttributeFlow::default())?
        .register(channel::flow::ChannelFlow::default())?
        .register(timer::flow::TimerFlow::default())?
        .register(rpc::flow::RpcFlow::default())?
        .register(subflow::flow::SubFlowChildFlow::default())?
        .register(subflow::flow::SubFlowParentFlow::new())?
        .register(client_apis::flow::ClientApisFlow::default())
}
