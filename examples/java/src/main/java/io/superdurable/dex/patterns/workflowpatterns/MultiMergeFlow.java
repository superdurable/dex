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

/** WCP-8 Multi-Merge: every branch arrival runs the merge Step again. */
@Component
public class MultiMergeFlow implements Flow<Void> {
    private final StartReviews startReviews = new StartReviews();
    private final LegalReview legalReview = new LegalReview();
    private final FinanceReview financeReview = new FinanceReview();
    private final RecordReview recordReview = new RecordReview();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(startReviews).otherSteps(legalReview, financeReview, recordReview);
    }

    static final class StartReviews implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goToMany(
                    StepMovement.of(LegalReview.class, null),
                    StepMovement.of(FinanceReview.class, null));
        }
    }

    static final class LegalReview implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goTo(RecordReview.class, getStepType());
        }
    }

    static final class FinanceReview implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goTo(RecordReview.class, getStepType());
        }
    }

    static final class RecordReview implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String reviewer) {
            return StepDecision.gracefulComplete(reviewer);
        }
    }
}
