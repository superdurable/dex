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

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.SubFlow;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

@Component
public final class BasicParentFlow implements Flow<String[]> {
    private final SubFlowsStep subFlowsStep = new SubFlowsStep();

    @Override
    public StepList<String[]> getSteps() {
        return StepList.startStep(subFlowsStep);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    static final class SubFlowsStep implements Step<String[]> {
        @Override
        public Class<String[]> getInputType() {
            return String[].class;
        }

        @Override
        public Wait waitFor(final Context context, final String[] requests) {
            final io.superdurable.dex.Condition[] conditions =
                    new io.superdurable.dex.Condition[requests.length];
            for (int index = 0; index < requests.length; index++) {
                conditions[index] = SubFlow.run(ExampleSubFlow.class, requests[index]);
            }
            return Wait.allOf(conditions);
        }

        @Override
        public StepDecision execute(final Context context, final String[] requests) {
            return StepDecision.gracefulComplete(null);
        }
    }
}
