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
import io.superdurable.dex.ChannelMessage;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.List;

/**
 * WCP-27 Complete Multiple Instance Activity: an RPC withdraws the remaining instances and the
 * join, then continues with the reviews submitted so far.
 */
@Component
public class CompleteMultipleInstanceTaskFlow implements Flow<Integer> {
    public static final Channel<String> peerReviewReleased =
            Channel.define("peerReviewReleased", String.class);
    public static final Channel<Integer> peerReviewSubmitted =
            Channel.define("peerReviewSubmitted", Integer.class);

    private final StartPeerReviews startPeerReviews = new StartPeerReviews();
    private final SubmitPeerReview submitPeerReview = new SubmitPeerReview();
    private final CollectPeerReviews collectPeerReviews = new CollectPeerReviews();
    private final PublishReviewResults publishReviewResults = new PublishReviewResults();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(startPeerReviews)
                .otherSteps(submitPeerReview, collectPeerReviews, publishReviewResults);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(peerReviewReleased, peerReviewSubmitted);
    }

    @RPC
    public void releasePeerReview(final Context context) {
        peerReviewReleased.publish(context, "released");
    }

    @RPC(loadChannels = {"peerReviewSubmitted"})
    public RPCResult<Integer> getSubmittedPeerReviewCount(final Context context) {
        return RPCResult.of(peerReviewSubmitted.size(context));
    }

    @RPC(loadChannels = {"peerReviewSubmitted"})
    @SuppressWarnings("unchecked")
    public RPCResult<Void> completePeerReviewsEarly(final Context context) {
        final List<Integer> submitted = peerReviewSubmitted.pendingMessages(context).stream()
                .map(ChannelMessage::getValue)
                .sorted()
                .toList();
        return RPCResult.of(
                        (Void) null,
                        StepMovement.of(
                                PublishReviewResults.class, submitted.toArray(new Integer[0])))
                .withCancelingSteps(SubmitPeerReview.class, CollectPeerReviews.class);
    }

    static final class StartPeerReviews implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer reviewerCount) {
            final StepMovement<?>[] movements = new StepMovement<?>[reviewerCount + 1];
            for (int index = 0; index < reviewerCount; index++) {
                movements[index] = StepMovement.of(SubmitPeerReview.class, index);
            }
            movements[reviewerCount] = StepMovement.of(CollectPeerReviews.class, reviewerCount);
            return StepDecision.goToMany(movements);
        }
    }

    static final class SubmitPeerReview implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer reviewerIndex) {
            return Wait.until(peerReviewReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer reviewerIndex) {
            peerReviewSubmitted.publish(context, reviewerIndex);
            return StepDecision.deadEnd();
        }
    }

    static final class CollectPeerReviews implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer reviewerCount) {
            return Wait.until(peerReviewSubmitted.forN(reviewerCount));
        }

        @Override
        public StepDecision execute(final Context context, final Integer reviewerCount) {
            final List<Integer> submitted =
                    peerReviewSubmitted.getConditionResults(context).stream().sorted().toList();
            return StepDecision.goTo(PublishReviewResults.class, submitted.toArray(new Integer[0]));
        }
    }

    static final class PublishReviewResults implements Step<Integer[]> {
        @Override
        public Class<Integer[]> getInputType() {
            return Integer[].class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer[] reviewerIndexes) {
            return StepDecision.gracefulComplete(reviewerIndexes);
        }
    }
}
