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

pub mod engagement;
pub mod job_post;
pub mod microservices;
pub mod money_transfer;
pub mod order_processing;
pub mod polling;
pub mod shortlist_candidates;
pub mod signup;
pub mod subscription;

use dex_sdk::{Registry, SdkResult};

use crate::shared::MyDependencyService;

pub fn register(registry: Registry) -> SdkResult<Registry> {
    registry
        .register(money_transfer::MoneyTransferFlow::default())?
        .register(order_processing::OrderProcessingFlow::new(
            MyDependencyService,
        ))?
        .register(microservices::OrchestrationFlow::default())?
        .register(engagement::EngagementFlow::default())?
        .register(subscription::SubscriptionFlow::default())?
        .register(polling::PollingFlow::default())?
        .register(signup::UserSignupFlow::default())?
        .register(job_post::JobPostFlow::default())?
        .register(shortlist_candidates::EmployerOptInFlow::default())?
        .register(shortlist_candidates::ShortlistFlow::default())
}
