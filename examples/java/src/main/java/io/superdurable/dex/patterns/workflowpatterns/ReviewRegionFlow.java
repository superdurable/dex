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
import io.superdurable.dex.StepMovement;
import org.springframework.stereotype.Component;

/** The review region of GeneralSynchronizingMergeFlow: parallel branches plus a cycle. */
@Component
public class ReviewRegionFlow implements Flow<Integer> {
    private static final int FINAL_REVISION = 1;

    private final DraftProposal draftProposal = new DraftProposal();
    private final ReviewProposal reviewProposal = new ReviewProposal();
    private final TranslateProposal translateProposal = new TranslateProposal();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(draftProposal).otherSteps(reviewProposal, translateProposal);
    }

    static final class DraftProposal implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer revision) {
            return StepDecision.goToMany(
                    StepMovement.of(ReviewProposal.class, revision),
                    StepMovement.of(TranslateProposal.class, revision));
        }
    }

    static final class ReviewProposal implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer revision) {
            if (revision < FINAL_REVISION) {
                return StepDecision.goTo(DraftProposal.class, revision + 1);
            }
            return StepDecision.gracefulComplete(getStepType() + "-" + revision);
        }
    }

    static final class TranslateProposal implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer revision) {
            return StepDecision.gracefulComplete(getStepType() + "-" + revision);
        }
    }
}
