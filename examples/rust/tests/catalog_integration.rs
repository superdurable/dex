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

use std::collections::HashSet;

use dex_examples_rust::patterns::{
    cron::CronScheduleFlow,
    drain_channels::{DrainInternalChannelFlow, DrainingExternalChannelFlow},
    entity_store::UserProfileFlow,
    interruptible::InterruptibleFlow,
    intervention::ManualRecoveryFlow,
    parallel::{
        AwaitParallelStepsFlow, DynamicParallelStepsFlow, FirstWinParallelStepsFlow,
        StaticParallelStepsFlow,
    },
    parallel_subflows::{
        AdvancedLongLiveParentFlow, AdvancedShortLiveParentFlow, BasicParentFlow, ExampleSubFlow,
        SubmitRequestFlow,
    },
    polling::{BackoffPollingFlow, IterationFlow, PollingWithTimerFlow},
    recovery::FailureRecoveryFlow,
    reminders::ReminderFlow,
    resettable_timer::ResettableTimerFlow,
    timeout::FlowGracefulTimeout,
    wait_for_state_completion::WaitForStateCompletionFlow,
};
use dex_examples_rust::products::{
    engagement::EngagementFlow,
    job_post::JobPostFlow,
    microservices::OrchestrationFlow,
    money_transfer::MoneyTransferFlow,
    order_processing::OrderProcessingFlow,
    polling::PollingFlow,
    shortlist_candidates::{EmployerOptInFlow, ShortlistFlow},
    signup::UserSignupFlow,
    subscription::SubscriptionFlow,
};
use dex_examples_rust::{PATTERN_FLOW_TYPES, PRODUCT_FLOW_TYPES, create_example_registry};
use dex_sdk::Flow;

#[test]
fn catalog_matches_every_cross_language_example() {
    let product_flows = [
        MoneyTransferFlow::default().flow_type(),
        OrderProcessingFlow::default().flow_type(),
        OrchestrationFlow::default().flow_type(),
        EngagementFlow::default().flow_type(),
        SubscriptionFlow::default().flow_type(),
        PollingFlow::default().flow_type(),
        UserSignupFlow::default().flow_type(),
        JobPostFlow::default().flow_type(),
        EmployerOptInFlow::default().flow_type(),
        ShortlistFlow::default().flow_type(),
    ];
    let pattern_flows = [
        CronScheduleFlow::default().flow_type(),
        DrainInternalChannelFlow::default().flow_type(),
        DrainingExternalChannelFlow::default().flow_type(),
        InterruptibleFlow::default().flow_type(),
        ManualRecoveryFlow::default().flow_type(),
        StaticParallelStepsFlow::default().flow_type(),
        DynamicParallelStepsFlow::default().flow_type(),
        AwaitParallelStepsFlow::default().flow_type(),
        FirstWinParallelStepsFlow::default().flow_type(),
        ExampleSubFlow::default().flow_type(),
        BasicParentFlow::default().flow_type(),
        AdvancedLongLiveParentFlow::default().flow_type(),
        AdvancedShortLiveParentFlow::default().flow_type(),
        SubmitRequestFlow::default().flow_type(),
        PollingWithTimerFlow::default().flow_type(),
        BackoffPollingFlow::default().flow_type(),
        IterationFlow::default().flow_type(),
        FailureRecoveryFlow::default().flow_type(),
        ReminderFlow::default().flow_type(),
        ResettableTimerFlow::default().flow_type(),
        UserProfileFlow.flow_type(),
        FlowGracefulTimeout::default().flow_type(),
        WaitForStateCompletionFlow::default().flow_type(),
    ];

    assert_eq!(product_flows, PRODUCT_FLOW_TYPES);
    assert_eq!(pattern_flows, PATTERN_FLOW_TYPES);
    assert_eq!(product_flows.len() + pattern_flows.len(), 33);
    assert_eq!(
        product_flows
            .into_iter()
            .chain(pattern_flows)
            .collect::<HashSet<_>>()
            .len(),
        33
    );
    create_example_registry().expect("all 33 example Flow definitions must register together");
}

#[test]
fn manifest_uses_the_published_sdk_only() {
    let manifest = include_str!("../Cargo.toml");
    let dex_sdk = manifest
        .lines()
        .find(|line| line.trim_start().starts_with("dex-sdk ="))
        .expect("examples/rust must depend on dex-sdk");
    assert!(
        dex_sdk.contains("\"="),
        "examples/rust must pin a published dex-sdk version: {dex_sdk}"
    );
    assert!(
        !dex_sdk.contains("path"),
        "examples/rust must depend on published dex-sdk, not a path dependency"
    );
}
