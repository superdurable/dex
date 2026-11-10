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
use std::sync::Arc;

use dex_examples_rust::control_patterns::{
    cancel_case::{CreditCheckFlow, LoanApplicationFlow},
    critical_section::ContractEditFlow,
    deferred_choice::OfferResponseFlow,
};
use dex_examples_rust::patterns::{
    cron::CronScheduleFlow,
    drain_channels::{DrainInternalChannelFlow, DrainingExternalChannelFlow},
    entity_store::UserProfileFlow,
    hash_partitioned_attribute_map::CustomerDirectoryFlow,
    inactiveness_tracker::InactivenessTrackerFlow,
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
    polling::{FakeJobService, PollingFlow},
    recovery::FailureRecoveryFlow,
    reminders::ReminderFlow,
    sequentially_chunked_attribute_map::ChunkedSubscriberFlow,
    timeout::FlowGracefulTimeout,
    wait_for_step_completion::WaitForStepCompletionFlow,
};
use dex_examples_rust::products::{
    deal_dsl::DealDSLFlow, engagement::EngagementFlow, job_post::JobPostingFlow,
    microservices::OrchestrationFlow, money_transfer::MoneyTransferFlow,
    order_processing::OrderProcessingFlow, signup::UserOnboardingFlow,
    subscription::SubscriptionFlow,
};
use dex_examples_rust::workflow_patterns::{
    and_join_and_threads::{GeneralizedAndJoinFlow, ThreadSplitMergeFlow},
    basic_control_flow::{
        ExclusiveChoiceSimpleMergeFlow, ParallelSplitSynchronizationFlow, SequenceFlow,
    },
    cancellation::{
        BackgroundCheckFlow, CancelCaseFlow, CancelMultipleInstanceTaskFlow, CancelRegionFlow,
        CancelTaskFlow, CompleteMultipleInstanceTaskFlow,
    },
    iteration::{ArbitraryCyclesFlow, FactorialRecursionFlow, StructuredLoopFlow},
    multi_choice_and_merges::{
        GeneralSynchronizingMergeFlow, MultiChoiceSynchronizingMergeFlow, MultiMergeFlow,
    },
    multiple_instances::{
        CancellingPartialJoinForMultipleInstancesFlow, DesignTimeMultipleInstancesFlow,
        DynamicPartialJoinForMultipleInstancesFlow, MultipleInstancesWithoutSynchronizationFlow,
        NoPriorKnowledgeMultipleInstancesFlow, RunTimeMultipleInstancesFlow,
        StaticPartialJoinForMultipleInstancesFlow,
    },
    partial_joins::{
        BlockingPartialJoinFlow, CancellingPartialJoinFlow, StructuredPartialJoinFlow,
    },
    state_based::{
        CriticalSectionFlow, DeferredChoiceFlow, InterleavedParallelRoutingFlow,
        InterleavedRoutingFlow, MilestoneFlow,
    },
    termination::{ExplicitTerminationFlow, ImplicitTerminationFlow},
    triggers::{PersistentTriggerFlow, TransientTriggerFlow},
};
use dex_examples_rust::{
    CONTROL_PATTERN_FLOW_TYPES, PATTERN_FLOW_TYPES, PRODUCT_FLOW_TYPES,
    WORKFLOW_PATTERN_FLOW_TYPES, create_example_registry,
};
use dex_sdk::Flow;

#[test]
fn catalog_matches_every_cross_language_example() {
    let product_flows = [
        MoneyTransferFlow::default().flow_type(),
        OrderProcessingFlow::default().flow_type(),
        OrchestrationFlow::default().flow_type(),
        DealDSLFlow::default().flow_type(),
        EngagementFlow::default().flow_type(),
        SubscriptionFlow::default().flow_type(),
        UserOnboardingFlow::default().flow_type(),
        JobPostingFlow::default().flow_type(),
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
        PollingFlow::new(Arc::new(FakeJobService::default())).flow_type(),
        FailureRecoveryFlow::default().flow_type(),
        ReminderFlow::default().flow_type(),
        InactivenessTrackerFlow::default().flow_type(),
        UserProfileFlow.flow_type(),
        ChunkedSubscriberFlow.flow_type(),
        CustomerDirectoryFlow.flow_type(),
        FlowGracefulTimeout::default().flow_type(),
        WaitForStepCompletionFlow::default().flow_type(),
    ];

    assert_eq!(product_flows, PRODUCT_FLOW_TYPES);
    assert_eq!(pattern_flows, PATTERN_FLOW_TYPES);
    assert_eq!(product_flows.len() + pattern_flows.len(), 31);
    assert_eq!(
        product_flows
            .into_iter()
            .chain(pattern_flows)
            .collect::<HashSet<_>>()
            .len(),
        31
    );
    create_example_registry(Arc::new(FakeJobService::default()))
        .expect("all 31 example Flow definitions must register together");
}

#[test]
fn workflow_pattern_catalog_matches_the_mapping() {
    let workflow_pattern_flows = [
        SequenceFlow::default().flow_type(),
        ParallelSplitSynchronizationFlow::default().flow_type(),
        ExclusiveChoiceSimpleMergeFlow::default().flow_type(),
        MultiChoiceSynchronizingMergeFlow::default().flow_type(),
        MultiMergeFlow::default().flow_type(),
        GeneralSynchronizingMergeFlow::default().flow_type(),
        StructuredPartialJoinFlow::default().flow_type(),
        BlockingPartialJoinFlow::default().flow_type(),
        CancellingPartialJoinFlow::default().flow_type(),
        GeneralizedAndJoinFlow::default().flow_type(),
        ThreadSplitMergeFlow::default().flow_type(),
        MultipleInstancesWithoutSynchronizationFlow::default().flow_type(),
        DesignTimeMultipleInstancesFlow::default().flow_type(),
        RunTimeMultipleInstancesFlow::default().flow_type(),
        NoPriorKnowledgeMultipleInstancesFlow::default().flow_type(),
        StaticPartialJoinForMultipleInstancesFlow::default().flow_type(),
        CancellingPartialJoinForMultipleInstancesFlow::default().flow_type(),
        DynamicPartialJoinForMultipleInstancesFlow::default().flow_type(),
        DeferredChoiceFlow::default().flow_type(),
        InterleavedParallelRoutingFlow::default().flow_type(),
        MilestoneFlow::default().flow_type(),
        CriticalSectionFlow::default().flow_type(),
        InterleavedRoutingFlow::default().flow_type(),
        CancelTaskFlow::default().flow_type(),
        CancelCaseFlow::default().flow_type(),
        BackgroundCheckFlow::default().flow_type(),
        CancelRegionFlow::default().flow_type(),
        CancelMultipleInstanceTaskFlow::default().flow_type(),
        CompleteMultipleInstanceTaskFlow::default().flow_type(),
        ArbitraryCyclesFlow::default().flow_type(),
        StructuredLoopFlow::default().flow_type(),
        FactorialRecursionFlow::default().flow_type(),
        ImplicitTerminationFlow::default().flow_type(),
        ExplicitTerminationFlow::default().flow_type(),
        TransientTriggerFlow::default().flow_type(),
        PersistentTriggerFlow::default().flow_type(),
    ];

    assert_eq!(workflow_pattern_flows, WORKFLOW_PATTERN_FLOW_TYPES);
    let every_flow_type = PRODUCT_FLOW_TYPES
        .into_iter()
        .chain(PATTERN_FLOW_TYPES)
        .chain(workflow_pattern_flows)
        .collect::<HashSet<_>>();
    assert_eq!(
        every_flow_type.len(),
        PRODUCT_FLOW_TYPES.len() + PATTERN_FLOW_TYPES.len() + WORKFLOW_PATTERN_FLOW_TYPES.len()
    );
    create_example_registry(Arc::new(FakeJobService::default()))
        .expect("Workflow Patterns Flows must register with every other example");
}

#[test]
fn control_pattern_catalog_matches_every_flow() {
    let control_pattern_flows = [
        OfferResponseFlow::default().flow_type(),
        ContractEditFlow::default().flow_type(),
        CreditCheckFlow::default().flow_type(),
        LoanApplicationFlow::default().flow_type(),
    ];

    assert_eq!(control_pattern_flows, CONTROL_PATTERN_FLOW_TYPES);
    let every_flow_type = PRODUCT_FLOW_TYPES
        .into_iter()
        .chain(PATTERN_FLOW_TYPES)
        .chain(WORKFLOW_PATTERN_FLOW_TYPES)
        .chain(control_pattern_flows)
        .collect::<HashSet<_>>();
    assert_eq!(
        every_flow_type.len(),
        PRODUCT_FLOW_TYPES.len()
            + PATTERN_FLOW_TYPES.len()
            + WORKFLOW_PATTERN_FLOW_TYPES.len()
            + CONTROL_PATTERN_FLOW_TYPES.len()
    );
    create_example_registry(Arc::new(FakeJobService::default()))
        .expect("control pattern Flows must register with every other example");
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
