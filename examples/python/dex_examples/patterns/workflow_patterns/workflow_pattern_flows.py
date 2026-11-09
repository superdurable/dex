# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Constructs every Workflow Patterns example Flow once."""

from __future__ import annotations

from typing import Any

from dex import Flow

from dex_examples.patterns.workflow_patterns.and_join_and_threads import (
    GeneralizedAndJoinFlow,
    ThreadSplitMergeFlow,
)
from dex_examples.patterns.workflow_patterns.basic_control_flow import (
    ExclusiveChoiceSimpleMergeFlow,
    ParallelSplitSynchronizationFlow,
    SequenceFlow,
)
from dex_examples.patterns.workflow_patterns.cancellation import (
    CancelCaseFlow,
    CancelMultipleInstanceTaskFlow,
    CancelRegionFlow,
    CancelTaskFlow,
    CompleteMultipleInstanceTaskFlow,
)
from dex_examples.patterns.workflow_patterns.iteration import (
    ArbitraryCyclesFlow,
    FactorialRecursionFlow,
    StructuredLoopFlow,
)
from dex_examples.patterns.workflow_patterns.multi_choice_and_merges import (
    GeneralSynchronizingMergeFlow,
    MultiChoiceSynchronizingMergeFlow,
    MultiMergeFlow,
    ReviewRegionFlow,
)
from dex_examples.patterns.workflow_patterns.multiple_instances import (
    CancellingPartialJoinForMultipleInstancesFlow,
    DesignTimeMultipleInstancesFlow,
    DynamicPartialJoinForMultipleInstancesFlow,
    MultipleInstancesWithoutSynchronizationFlow,
    NoPriorKnowledgeMultipleInstancesFlow,
    RunTimeMultipleInstancesFlow,
    StaticPartialJoinForMultipleInstancesFlow,
)
from dex_examples.patterns.workflow_patterns.partial_joins import (
    BlockingPartialJoinFlow,
    CancellingPartialJoinFlow,
    StructuredPartialJoinFlow,
)
from dex_examples.patterns.workflow_patterns.state_based import (
    CriticalSectionFlow,
    DeferredChoiceFlow,
    InterleavedParallelRoutingFlow,
    InterleavedRoutingFlow,
    MilestoneFlow,
)
from dex_examples.patterns.workflow_patterns.termination import (
    ExplicitTerminationFlow,
    ImplicitTerminationFlow,
)
from dex_examples.patterns.workflow_patterns.triggers import (
    PersistentTriggerFlow,
    TransientTriggerFlow,
)


class WorkflowPatternFlows:
    """Holds one registered instance of each Workflow Patterns Flow."""

    def __init__(self) -> None:
        self.sequence = SequenceFlow()
        self.parallel_split_synchronization = ParallelSplitSynchronizationFlow()
        self.exclusive_choice_simple_merge = ExclusiveChoiceSimpleMergeFlow()
        self.multi_choice_synchronizing_merge = MultiChoiceSynchronizingMergeFlow()
        self.multi_merge = MultiMergeFlow()
        self.review_region = ReviewRegionFlow()
        self.general_synchronizing_merge = GeneralSynchronizingMergeFlow(
            self.review_region
        )
        self.structured_partial_join = StructuredPartialJoinFlow()
        self.blocking_partial_join = BlockingPartialJoinFlow()
        self.cancelling_partial_join = CancellingPartialJoinFlow()
        self.generalized_and_join = GeneralizedAndJoinFlow()
        self.thread_split_merge = ThreadSplitMergeFlow()
        self.multiple_instances_without_synchronization = (
            MultipleInstancesWithoutSynchronizationFlow()
        )
        self.design_time_multiple_instances = DesignTimeMultipleInstancesFlow()
        self.run_time_multiple_instances = RunTimeMultipleInstancesFlow()
        self.no_prior_knowledge_multiple_instances = (
            NoPriorKnowledgeMultipleInstancesFlow()
        )
        self.static_partial_join_for_multiple_instances = (
            StaticPartialJoinForMultipleInstancesFlow()
        )
        self.cancelling_partial_join_for_multiple_instances = (
            CancellingPartialJoinForMultipleInstancesFlow()
        )
        self.dynamic_partial_join_for_multiple_instances = (
            DynamicPartialJoinForMultipleInstancesFlow()
        )
        self.deferred_choice = DeferredChoiceFlow()
        self.interleaved_parallel_routing = InterleavedParallelRoutingFlow()
        self.milestone = MilestoneFlow()
        self.critical_section = CriticalSectionFlow()
        self.interleaved_routing = InterleavedRoutingFlow()
        self.cancel_task = CancelTaskFlow()
        self.cancel_case = CancelCaseFlow()
        self.cancel_region = CancelRegionFlow()
        self.cancel_multiple_instance_task = CancelMultipleInstanceTaskFlow()
        self.complete_multiple_instance_task = CompleteMultipleInstanceTaskFlow()
        self.arbitrary_cycles = ArbitraryCyclesFlow()
        self.structured_loop = StructuredLoopFlow()
        self.factorial_recursion = FactorialRecursionFlow()
        self.implicit_termination = ImplicitTerminationFlow()
        self.explicit_termination = ExplicitTerminationFlow()
        self.transient_trigger = TransientTriggerFlow()
        self.persistent_trigger = PersistentTriggerFlow()

    def all_flows(self) -> list[Flow[Any]]:
        """Returns every Flow instance for the application Registry."""
        return [
            self.sequence,
            self.parallel_split_synchronization,
            self.exclusive_choice_simple_merge,
            self.multi_choice_synchronizing_merge,
            self.multi_merge,
            self.review_region,
            self.general_synchronizing_merge,
            self.structured_partial_join,
            self.blocking_partial_join,
            self.cancelling_partial_join,
            self.generalized_and_join,
            self.thread_split_merge,
            self.multiple_instances_without_synchronization,
            self.design_time_multiple_instances,
            self.run_time_multiple_instances,
            self.no_prior_knowledge_multiple_instances,
            self.static_partial_join_for_multiple_instances,
            self.cancelling_partial_join_for_multiple_instances,
            self.dynamic_partial_join_for_multiple_instances,
            self.deferred_choice,
            self.interleaved_parallel_routing,
            self.milestone,
            self.critical_section,
            self.interleaved_routing,
            self.cancel_task,
            self.cancel_case,
            self.cancel_region,
            self.cancel_multiple_instance_task,
            self.complete_multiple_instance_task,
            self.arbitrary_cycles,
            self.structured_loop,
            self.factorial_recursion,
            self.implicit_termination,
            self.explicit_termination,
            self.transient_trigger,
            self.persistent_trigger,
        ]
