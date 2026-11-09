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

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

/**
 * WCP-13 Multiple Instances with a priori Design-Time Knowledge: the instance count is fixed in
 * the Flow code. RunTimeMultipleInstancesFlow reuses the ReviewDocument and CollectReviews Steps.
 */
@Component
public class DesignTimeMultipleInstancesFlow implements Flow<Void> {
    public static final Channel<String> reviewCompleted =
            Channel.define("reviewCompleted", String.class);
    private static final String[] DESIGN_TIME_REVIEWERS = {"legal", "finance", "security"};

    private final StartDesignTimeReviews startDesignTimeReviews = new StartDesignTimeReviews();
    private final ReviewDocument reviewDocument = new ReviewDocument();
    private final CollectReviews collectReviews = new CollectReviews();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(startDesignTimeReviews).otherSteps(reviewDocument, collectReviews);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(reviewCompleted);
    }

    static StepDecision startReviews(final String[] reviewers) {
        final StepMovement<?>[] movements = new StepMovement<?>[reviewers.length + 1];
        for (int index = 0; index < reviewers.length; index++) {
            movements[index] = StepMovement.of(ReviewDocument.class, reviewers[index]);
        }
        movements[reviewers.length] = StepMovement.of(CollectReviews.class, reviewers.length);
        return StepDecision.goToMany(movements);
    }

    static final class StartDesignTimeReviews implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return startReviews(DESIGN_TIME_REVIEWERS);
        }
    }

    static final class ReviewDocument implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String reviewer) {
            reviewCompleted.publish(context, reviewer);
            return StepDecision.deadEnd();
        }
    }

    static final class CollectReviews implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer reviewCount) {
            return Wait.until(reviewCompleted.forN(reviewCount));
        }

        @Override
        public StepDecision execute(final Context context, final Integer reviewCount) {
            return StepDecision.gracefulComplete(
                    reviewCompleted.getConditionResults(context).stream().sorted().toList());
        }
    }
}
