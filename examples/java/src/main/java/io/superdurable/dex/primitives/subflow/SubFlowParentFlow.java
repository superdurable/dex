/*
 * Copyright (c) 2026 Super Durable, Inc.
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

package io.superdurable.dex.primitives.subflow;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.FlowTimeoutPolicy;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.SubFlow;
import io.superdurable.dex.SubFlowOptions;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public final class SubFlowParentFlow implements Flow<Integer> {
    private final SubFlowParentStep start = new SubFlowParentStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(start);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    static final class SubFlowParentStep implements Step<Integer> {
        private final SubFlowOptions options = SubFlowOptions.newBuilder()
                .timeout(Duration.ofHours(1))
                .timeoutPolicy(FlowTimeoutPolicy.CANCEL)
                .build();

        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.until(SubFlow.run(SubFlowChildFlow.class, input, options));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            final Integer output =
                    SubFlow.getConditionResults(context).getSingleOutput(Integer.class);
            return StepDecision.gracefulComplete(SubFlow.getFlowId(context) + "|" + output);
        }
    }
}
