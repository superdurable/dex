/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Client;
import io.superdurable.dex.Flow;
import io.superdurable.dex.patterns.workflowpatterns.BackgroundCheckFlow;
import io.superdurable.dex.patterns.workflowpatterns.ArbitraryCyclesFlow;
import io.superdurable.dex.patterns.workflowpatterns.BlockingPartialJoinFlow;
import io.superdurable.dex.patterns.workflowpatterns.CancelCaseFlow;
import io.superdurable.dex.patterns.workflowpatterns.CancelMultipleInstanceTaskFlow;
import io.superdurable.dex.patterns.workflowpatterns.CancelRegionFlow;
import io.superdurable.dex.patterns.workflowpatterns.CancelTaskFlow;
import io.superdurable.dex.patterns.workflowpatterns.CancellingPartialJoinFlow;
import io.superdurable.dex.patterns.workflowpatterns.CancellingPartialJoinForMultipleInstancesFlow;
import io.superdurable.dex.patterns.workflowpatterns.CompleteMultipleInstanceTaskFlow;
import io.superdurable.dex.patterns.workflowpatterns.CriticalSectionFlow;
import io.superdurable.dex.patterns.workflowpatterns.DeferredChoiceFlow;
import io.superdurable.dex.patterns.workflowpatterns.DesignTimeMultipleInstancesFlow;
import io.superdurable.dex.patterns.workflowpatterns.DynamicPartialJoinForMultipleInstancesFlow;
import io.superdurable.dex.patterns.workflowpatterns.ExclusiveChoiceSimpleMergeFlow;
import io.superdurable.dex.patterns.workflowpatterns.ExplicitTerminationFlow;
import io.superdurable.dex.patterns.workflowpatterns.FactorialRecursionFlow;
import io.superdurable.dex.patterns.workflowpatterns.GeneralSynchronizingMergeFlow;
import io.superdurable.dex.patterns.workflowpatterns.GeneralizedAndJoinFlow;
import io.superdurable.dex.patterns.workflowpatterns.ImplicitTerminationFlow;
import io.superdurable.dex.patterns.workflowpatterns.InterleavedParallelRoutingFlow;
import io.superdurable.dex.patterns.workflowpatterns.InterleavedRoutingFlow;
import io.superdurable.dex.patterns.workflowpatterns.MilestoneFlow;
import io.superdurable.dex.patterns.workflowpatterns.MultiChoiceSynchronizingMergeFlow;
import io.superdurable.dex.patterns.workflowpatterns.MultiMergeFlow;
import io.superdurable.dex.patterns.workflowpatterns.MultipleInstancesWithoutSynchronizationFlow;
import io.superdurable.dex.patterns.workflowpatterns.NoPriorKnowledgeMultipleInstancesFlow;
import io.superdurable.dex.patterns.workflowpatterns.ParallelSplitSynchronizationFlow;
import io.superdurable.dex.patterns.workflowpatterns.PersistentTriggerFlow;
import io.superdurable.dex.patterns.workflowpatterns.RunTimeMultipleInstancesFlow;
import io.superdurable.dex.patterns.workflowpatterns.SequenceFlow;
import io.superdurable.dex.patterns.workflowpatterns.StaticPartialJoinForMultipleInstancesFlow;
import io.superdurable.dex.patterns.workflowpatterns.StructuredLoopFlow;
import io.superdurable.dex.patterns.workflowpatterns.StructuredPartialJoinFlow;
import io.superdurable.dex.patterns.workflowpatterns.ThreadSplitMergeFlow;
import io.superdurable.dex.patterns.workflowpatterns.TransientTriggerFlow;

import org.springframework.beans.factory.ObjectProvider;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/** The registered instance of every Workflow Patterns Flow, keyed by its class. */
final class WorkflowPatternFlows {
    private final Map<Class<?>, Flow<?>> flowsByClass = new LinkedHashMap<Class<?>, Flow<?>>();

    WorkflowPatternFlows(final ObjectProvider<Client> clientProvider) {
        for (final Flow<?> flow : List.<Flow<?>>of(
                new SequenceFlow(),
                new ParallelSplitSynchronizationFlow(),
                new ExclusiveChoiceSimpleMergeFlow(),
                new MultiChoiceSynchronizingMergeFlow(),
                new MultiMergeFlow(),
                new GeneralSynchronizingMergeFlow(),
                new StructuredPartialJoinFlow(),
                new BlockingPartialJoinFlow(),
                new CancellingPartialJoinFlow(),
                new GeneralizedAndJoinFlow(),
                new ThreadSplitMergeFlow(),
                new MultipleInstancesWithoutSynchronizationFlow(),
                new DesignTimeMultipleInstancesFlow(),
                new RunTimeMultipleInstancesFlow(),
                new NoPriorKnowledgeMultipleInstancesFlow(),
                new StaticPartialJoinForMultipleInstancesFlow(),
                new CancellingPartialJoinForMultipleInstancesFlow(),
                new DynamicPartialJoinForMultipleInstancesFlow(),
                new DeferredChoiceFlow(),
                new InterleavedParallelRoutingFlow(),
                new MilestoneFlow(),
                new CriticalSectionFlow(),
                new InterleavedRoutingFlow(),
                new CancelTaskFlow(),
                new CancelCaseFlow(clientProvider),
                new BackgroundCheckFlow(),
                new CancelRegionFlow(),
                new CancelMultipleInstanceTaskFlow(),
                new CompleteMultipleInstanceTaskFlow(),
                new ArbitraryCyclesFlow(),
                new StructuredLoopFlow(),
                new FactorialRecursionFlow(),
                new ImplicitTerminationFlow(),
                new ExplicitTerminationFlow(),
                new TransientTriggerFlow(),
                new PersistentTriggerFlow())) {
            flowsByClass.put(flow.getClass(), flow);
        }
    }

    List<Flow<?>> all() {
        return List.copyOf(flowsByClass.values());
    }

    <F extends Flow<?>> F flow(final Class<F> flowClass) {
        final Flow<?> flow = flowsByClass.get(flowClass);
        if (flow == null) {
            throw new IllegalArgumentException("Workflow Patterns Flow is not registered: " + flowClass);
        }
        return flowClass.cast(flow);
    }
}
