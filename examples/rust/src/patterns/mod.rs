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

pub mod cron;
pub mod drain_channels;
pub mod entity_store;
pub mod inactiveness_tracker;
pub mod interruptible;
pub mod intervention;
pub mod parallel;
pub mod parallel_subflows;
pub mod polling;
pub mod recovery;
pub mod reminders;
pub mod timeout;
pub mod wait_for_step_completion;

use std::sync::Arc;

use dex_sdk::{Client, Registry, SdkResult};

pub fn register(registry: Registry) -> SdkResult<Registry> {
    register_with_client(registry, None)
}

pub fn register_worker(registry: Registry, client: Arc<Client>) -> SdkResult<Registry> {
    register_with_client(registry, Some(client))
}

fn register_with_client(registry: Registry, client: Option<Arc<Client>>) -> SdkResult<Registry> {
    let wait_for_half_parent = client
        .as_ref()
        .map_or_else(parallel_subflows::WaitForHalfParentFlow::default, |value| {
            parallel_subflows::WaitForHalfParentFlow::new(Arc::clone(value))
        });
    let submit_request = client
        .map_or_else(parallel_subflows::SubmitRequestFlow::default, |value| {
            parallel_subflows::SubmitRequestFlow::new(value)
        });
    registry
        .register(cron::CronScheduleFlow::default())?
        .register(drain_channels::DrainInternalChannelFlow::default())?
        .register(drain_channels::DrainingExternalChannelFlow::default())?
        .register(interruptible::InterruptibleFlow::default())?
        .register(intervention::ManualRecoveryFlow::default())?
        .register(parallel::StaticParallelStepsFlow::default())?
        .register(parallel::DynamicParallelStepsFlow::default())?
        .register(parallel::AwaitParallelStepsFlow::default())?
        .register(parallel::FirstWinParallelStepsFlow::default())?
        .register(parallel_subflows::ExampleSubFlow::default())?
        .register(parallel_subflows::BasicParentFlow::default())?
        .register(wait_for_half_parent)?
        .register(parallel_subflows::AdvancedLongLiveParentFlow::default())?
        .register(parallel_subflows::AdvancedShortLiveParentFlow::default())?
        .register(submit_request)?
        .register(polling::PollingWithTimerFlow::default())?
        .register(polling::BackoffPollingFlow::default())?
        .register(polling::IterationFlow::default())?
        .register(recovery::FailureRecoveryFlow::default())?
        .register(reminders::ReminderFlow::default())?
        .register(inactiveness_tracker::InactivenessTrackerFlow::default())?
        .register(entity_store::UserProfileFlow)?
        .register(timeout::FlowGracefulTimeout::default())?
        .register(wait_for_step_completion::WaitForStepCompletionFlow::default())
}
