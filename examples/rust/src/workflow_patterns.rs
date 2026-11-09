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

//! Workflow Patterns (WCP-1 to WCP-43) expressed as Dex Flows.
//!
//! `examples/workflow-patterns-mapping.md` lists each pattern, its rating, and
//! the behavior that the integration tests assert.

pub mod and_join_and_threads;
pub mod basic_control_flow;
pub mod cancellation;
pub mod iteration;
pub mod multi_choice_and_merges;
pub mod multiple_instances;
pub mod partial_joins;
pub mod state_based;
pub mod termination;
pub mod triggers;

use std::time::{SystemTime, UNIX_EPOCH};

use dex_sdk::{HandlerError, HandlerResult, Registry, SdkResult};
use serde::{Deserialize, Serialize};

/// The time one task held a shared resource, in Unix epoch milliseconds.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TaskInterval {
    pub task: String,
    pub started_at_millis: i64,
    pub finished_at_millis: i64,
}

impl TaskInterval {
    /// Returns whether both intervals held the resource at the same time.
    pub fn overlaps(&self, other: &TaskInterval) -> bool {
        self.started_at_millis < other.finished_at_millis
            && other.started_at_millis < self.finished_at_millis
    }
}

pub fn register(registry: Registry) -> SdkResult<Registry> {
    registry
        .register(basic_control_flow::SequenceFlow::default())?
        .register(basic_control_flow::ParallelSplitSynchronizationFlow::default())?
        .register(basic_control_flow::ExclusiveChoiceSimpleMergeFlow::default())?
        .register(multi_choice_and_merges::MultiChoiceSynchronizingMergeFlow::default())?
        .register(multi_choice_and_merges::MultiMergeFlow::default())?
        .register(multi_choice_and_merges::GeneralSynchronizingMergeFlow::default())?
        .register(multi_choice_and_merges::ReviewRegionFlow::default())?
        .register(partial_joins::StructuredPartialJoinFlow::default())?
        .register(partial_joins::BlockingPartialJoinFlow::default())?
        .register(partial_joins::CancellingPartialJoinFlow::default())?
        .register(and_join_and_threads::GeneralizedAndJoinFlow::default())?
        .register(and_join_and_threads::ThreadSplitMergeFlow::default())?
        .register(multiple_instances::MultipleInstancesWithoutSynchronizationFlow::default())?
        .register(multiple_instances::DesignTimeMultipleInstancesFlow::default())?
        .register(multiple_instances::RunTimeMultipleInstancesFlow::default())?
        .register(multiple_instances::NoPriorKnowledgeMultipleInstancesFlow::default())?
        .register(multiple_instances::StaticPartialJoinForMultipleInstancesFlow::default())?
        .register(multiple_instances::CancellingPartialJoinForMultipleInstancesFlow::default())?
        .register(multiple_instances::DynamicPartialJoinForMultipleInstancesFlow::default())?
        .register(state_based::DeferredChoiceFlow::default())?
        .register(state_based::InterleavedParallelRoutingFlow::default())?
        .register(state_based::MilestoneFlow::default())?
        .register(state_based::CriticalSectionFlow::default())?
        .register(state_based::InterleavedRoutingFlow::default())?
        .register(cancellation::CancelTaskFlow::default())?
        .register(cancellation::CancelCaseFlow::default())?
        .register(cancellation::CancelRegionFlow::default())?
        .register(cancellation::CancelMultipleInstanceTaskFlow::default())?
        .register(cancellation::CompleteMultipleInstanceTaskFlow::default())?
        .register(iteration::ArbitraryCyclesFlow::default())?
        .register(iteration::StructuredLoopFlow::default())?
        .register(iteration::FactorialRecursionFlow::default())?
        .register(termination::ImplicitTerminationFlow::default())?
        .register(termination::ExplicitTerminationFlow::default())?
        .register(triggers::TransientTriggerFlow::default())?
        .register(triggers::PersistentTriggerFlow::default())
}

/// Returns `trail` with `step_type` appended, the Step types run so far.
pub(crate) fn extend_trail(mut trail: Vec<String>, step_type: &str) -> Vec<String> {
    trail.push(step_type.to_string());
    trail
}

/// Returns `values` in ascending order so outputs do not depend on arrival order.
pub(crate) fn sorted<T: Ord>(mut values: Vec<T>) -> Vec<T> {
    values.sort();
    values
}

pub(crate) fn current_unix_millis() -> HandlerResult<i64> {
    let elapsed = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(HandlerError::from_error)?;
    i64::try_from(elapsed.as_millis()).map_err(HandlerError::from_error)
}
