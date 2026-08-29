/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package io.superdurable.dex.patterns.parallelsubflows;

import io.superdurable.dex.Client;
import io.superdurable.dex.Condition;
import io.superdurable.dex.ConditionCombination;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.SubFlow;
import io.superdurable.dex.SubFlowOptions;
import io.superdurable.dex.Wait;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.stereotype.Component;

import java.util.ArrayList;
import java.util.List;

@Component
public final class BasicParentFlow implements Flow<String[]> {
    private final SubFlowsStep subFlowsStep;

    public BasicParentFlow(
            final ObjectProvider<Client> clientProvider,
            final ExampleSubFlow exampleSubFlow) {
        subFlowsStep = new SubFlowsStep(clientProvider, exampleSubFlow);
    }

    @Override
    public StepList<String[]> getSteps() {
        return StepList.startStep(subFlowsStep);
    }

    static final class SubFlowsStep implements Step<String[]> {
        private final ObjectProvider<Client> clientProvider;
        private final ExampleSubFlow exampleSubFlow;

        SubFlowsStep(
                final ObjectProvider<Client> clientProvider,
                final ExampleSubFlow exampleSubFlow) {
            this.clientProvider = clientProvider;
            this.exampleSubFlow = exampleSubFlow;
        }

        @Override
        public Class<String[]> getInputType() {
            return String[].class;
        }

        @Override
        public Wait waitFor(final Context context, final String[] requests) {
            final Condition[] conditions = new Condition[requests.length];
            for (int index = 0; index < requests.length; index++) {
                conditions[index] = SubFlow.run(
                        ExampleSubFlow.class,
                        requests[index],
                        SubFlowOptions.newBuilder()
                                .conditionId("subflow-" + index)
                                .build());
            }
            return Wait.anyCombinationOf(combinations(conditions, (conditions.length + 1) / 2));
        }

        @Override
        public StepDecision execute(final Context context, final String[] requests) {
            for (int index = 0; index < requests.length; index++) {
                final FlowResult result = SubFlow.getConditionResults(context, index);
                if (result.getStatus() == FlowStatus.RUNNING) {
                    clientProvider.getObject().stopFlow(SubFlow.getFlowId(context, index));
                }
            }
            return StepDecision.gracefulComplete(null);
        }

        private static ConditionCombination[] combinations(
                final Condition[] conditions,
                final int size) {
            final List<ConditionCombination> result = new ArrayList<ConditionCombination>();
            collect(conditions, size, 0, new ArrayList<Condition>(), result);
            return result.toArray(new ConditionCombination[0]);
        }

        private static void collect(
                final Condition[] conditions,
                final int size,
                final int start,
                final List<Condition> selected,
                final List<ConditionCombination> result) {
            if (selected.size() == size) {
                result.add(ConditionCombination.of(selected.toArray(new Condition[0])));
                return;
            }
            for (int index = start;
                    index <= conditions.length - (size - selected.size());
                    index++) {
                selected.add(conditions[index]);
                collect(conditions, size, index + 1, selected, result);
                selected.remove(selected.size() - 1);
            }
        }
    }
}
