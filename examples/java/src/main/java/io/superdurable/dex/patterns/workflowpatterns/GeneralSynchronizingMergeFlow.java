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

package io.superdurable.dex.patterns.workflowpatterns;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepCompletion;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.SubFlow;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.List;

/**
 * WCP-38 General Synchronizing Merge: a ReviewRegionFlow SubFlow scopes the region, and its
 * completion fires once no Step in it can run.
 */
@Component
public class GeneralSynchronizingMergeFlow implements Flow<Void> {
    private final RunReviewRegion runReviewRegion = new RunReviewRegion();
    private final PublishDecision publishDecision = new PublishDecision();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(runReviewRegion).otherSteps(publishDecision);
    }

    static final class RunReviewRegion implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(SubFlow.run(ReviewRegionFlow.class, 0));
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final FlowResult region = SubFlow.getConditionResults(context);
            final List<String> outputs = region.getCompletions().stream()
                    .map((StepCompletion completion) -> completion.getOutput(String.class))
                    .sorted()
                    .toList();
            return StepDecision.goTo(PublishDecision.class, outputs.toArray(new String[0]));
        }
    }

    static final class PublishDecision implements Step<String[]> {
        @Override
        public Class<String[]> getInputType() {
            return String[].class;
        }

        @Override
        public StepDecision execute(final Context context, final String[] decision) {
            return StepDecision.gracefulComplete(decision);
        }
    }
}
