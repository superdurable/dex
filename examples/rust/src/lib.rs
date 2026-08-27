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

pub mod patterns;
pub mod primitives;
pub mod products;
pub mod server;
pub mod shared;

use dex_sdk::{Registry, SdkResult};

pub const PRODUCT_FLOW_TYPES: [&str; 10] = [
    "MoneyTransferFlow",
    "OrderProcessingFlow",
    "OrchestrationFlow",
    "EngagementFlow",
    "SubscriptionFlow",
    "PollingFlow",
    "UserSignupFlow",
    "JobPostFlow",
    "EmployerOptInFlow",
    "ShortlistFlow",
];

pub const PATTERN_FLOW_TYPES: [&str; 19] = [
    "CronScheduleFlow",
    "DrainInternalChannelsFlow",
    "DrainingExternalChannelFlow",
    "InterruptibleExecutionFlow",
    "ManualInterventionFlow",
    "SimpleParallelStatesFlow",
    "ParallelStatesWithAwaitFlow",
    "ParentFlowV2",
    "SimplePollingFlow",
    "BackoffPollingFlow",
    "FailureRecoveryFlow",
    "ReminderFlow",
    "ResettableTimerFlow",
    "ChildFlow",
    "ParentFlow",
    "RequestReceiverFlow",
    "UserProfileFlow",
    "FlowGracefulTimeout",
    "WaitForStateCompletionFlow",
];

pub fn create_example_registry() -> SdkResult<Registry> {
    products::register(Registry::new())
        .and_then(patterns::register)
        .and_then(primitives::register)
}
