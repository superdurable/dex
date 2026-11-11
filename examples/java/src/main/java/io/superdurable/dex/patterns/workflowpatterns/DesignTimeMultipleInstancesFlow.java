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

import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Join;
import io.superdurable.dex.JoinRound;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;
import java.util.function.Function;

/** WCP-13 Multiple Instances with Design-Time Knowledge: the Flow code fixes the instance count. */
@Component
public class DesignTimeMultipleInstancesFlow implements Flow<Void> {
    public static final AttributeMap<Boolean> startedReviewers =
            AttributeMap.define("startedReviewers", Boolean.class);
    public static final Channel<String> reviewGateOpened =
            Channel.define("reviewGateOpened", String.class);
    public static final Join<String> reviewCompleted =
            Join.define("reviewCompleted", String.class);
    private static final String[] DESIGN_TIME_REVIEWERS = {"legal", "finance", "security"};

    /** One review instance: its reviewer and the join round it reports to. */
    public record ReviewAssignment(String reviewer, JoinRound round) {
    }

    private final StartDesignTimeReviews startDesignTimeReviews = new StartDesignTimeReviews();
    private final ReviewDocument reviewDocument = new ReviewDocument();
    private final CollectReviews collectReviews = new CollectReviews();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(startDesignTimeReviews).otherSteps(reviewDocument, collectReviews);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return reviewPersistenceSchema();
    }

    static PersistenceSchema reviewPersistenceSchema() {
        return PersistenceSchema.of(startedReviewers, reviewGateOpened, reviewCompleted);
    }

    /** Counts the ReviewDocument instances that are in flight at the gate. */
    @RPC(loadAttributeMaps = {"startedReviewers"})
    public RPCResult<Integer> getStartedDesignTimeReviewCount(final Context context) {
        return RPCResult.of(DesignTimeMultipleInstancesFlow.startedReviewers.getMapSize(context));
    }

    /** Lets count waiting ReviewDocument instances finish. */
    @RPC
    public void openDesignTimeReviewGate(final Context context, final Integer count) {
        for (int index = 0; index < count; index++) {
            DesignTimeMultipleInstancesFlow.reviewGateOpened.publish(context, "opened");
        }
    }

    static StepDecision startReviews(final Context context, final String[] reviewers) {
        final List<Function<JoinRound, StepMovement<?>>> reviews = Arrays.stream(reviewers)
                .<Function<JoinRound, StepMovement<?>>>map(reviewer -> round ->
                        StepMovement.of(ReviewDocument.class, new ReviewAssignment(reviewer, round)))
                .toList();
        return StepDecision.goToMany(reviewCompleted.fork(
                context, reviews, round -> StepMovement.of(CollectReviews.class, round)));
    }

    static final class StartDesignTimeReviews implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return startReviews(context, DESIGN_TIME_REVIEWERS);
        }
    }

    static final class ReviewDocument implements Step<ReviewAssignment> {
        @Override
        public Class<ReviewAssignment> getInputType() {
            return ReviewAssignment.class;
        }

        @Override
        public Wait waitFor(final Context context, final ReviewAssignment assignment) {
            startedReviewers.set(context, assignment.reviewer(), true);
            return Wait.until(reviewGateOpened.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final ReviewAssignment assignment) {
            return reviewCompleted.endBranch(context, assignment.round(), assignment.reviewer());
        }
    }

    static final class CollectReviews implements Step<JoinRound> {
        @Override
        public Class<JoinRound> getInputType() {
            return JoinRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final JoinRound round) {
            return Wait.until(reviewCompleted.forAllBranches(round));
        }

        @Override
        public StepDecision execute(final Context context, final JoinRound round) {
            return StepDecision.gracefulComplete(
                    reviewCompleted.getBranchResults(context, round).stream().sorted().toList());
        }
    }
}
