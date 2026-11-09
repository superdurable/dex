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
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import org.springframework.stereotype.Component;

/** WCP-4 Exclusive Choice and WCP-5 Simple Merge: one branch runs, then both paths merge. */
@Component
public class ExclusiveChoiceSimpleMergeFlow implements Flow<Integer> {
    private static final int MANUAL_REVIEW_THRESHOLD = 1000;

    private final RouteClaim routeClaim = new RouteClaim();
    private final ManualReview manualReview = new ManualReview();
    private final AutoApprove autoApprove = new AutoApprove();
    private final CloseClaim closeClaim = new CloseClaim();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(routeClaim).otherSteps(manualReview, autoApprove, closeClaim);
    }

    static final class RouteClaim implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer claimAmount) {
            final String[] trail = {getStepType()};
            if (claimAmount >= MANUAL_REVIEW_THRESHOLD) {
                return StepDecision.goTo(ManualReview.class, trail);
            }
            return StepDecision.goTo(AutoApprove.class, trail);
        }
    }

    static final class ManualReview implements Step<String[]> {
        @Override
        public Class<String[]> getInputType() {
            return String[].class;
        }

        @Override
        public StepDecision execute(final Context context, final String[] trail) {
            return StepDecision.goTo(CloseClaim.class, StepTrail.append(trail, getStepType()));
        }
    }

    static final class AutoApprove implements Step<String[]> {
        @Override
        public Class<String[]> getInputType() {
            return String[].class;
        }

        @Override
        public StepDecision execute(final Context context, final String[] trail) {
            return StepDecision.goTo(CloseClaim.class, StepTrail.append(trail, getStepType()));
        }
    }

    static final class CloseClaim implements Step<String[]> {
        @Override
        public Class<String[]> getInputType() {
            return String[].class;
        }

        @Override
        public StepDecision execute(final Context context, final String[] trail) {
            return StepDecision.gracefulComplete(StepTrail.append(trail, getStepType()));
        }
    }
}
