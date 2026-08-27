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
pub mod interruptible;
pub mod intervention;
pub mod parallel;
pub mod parent_child;
pub mod polling;
pub mod recovery;
pub mod reminders;
pub mod resettable_timer;
pub mod scalable_parallel;
pub mod timeout;
pub mod wait_for_state_completion;

use dex_sdk::{Registry, SdkResult};

pub fn register(registry: Registry) -> SdkResult<Registry> {
    registry
        .register(cron::CronScheduleFlow::default())?
        .register(drain_channels::DrainInternalChannelFlow::default())?
        .register(drain_channels::DrainingExternalChannelFlow::default())?
        .register(interruptible::InterruptibleFlow::default())?
        .register(intervention::ManualInterventionFlow::default())?
        .register(parallel::SimpleParallelStatesFlow::default())?
        .register(parallel::ParallelStatesWithAwaitFlow::default())?
        .register(parent_child::ParentFlowV2::default())?
        .register(polling::SimplePollingFlow::default())?
        .register(polling::BackoffPollingFlow::default())?
        .register(recovery::FailureRecoveryFlow::default())?
        .register(reminders::ReminderFlow::default())?
        .register(resettable_timer::ResettableTimerFlow::default())?
        .register(scalable_parallel::ChildFlow::default())?
        .register(scalable_parallel::ParentFlow::default())?
        .register(scalable_parallel::RequestReceiverFlow::default())?
        .register(entity_store::UserProfileFlow)?
        .register(timeout::FlowGracefulTimeout::default())?
        .register(wait_for_state_completion::WaitForStateCompletionFlow::default())
}
