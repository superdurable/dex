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
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.patterns.workflowpatterns.DesignTimeMultipleInstancesFlow.CollectReviews;
import io.superdurable.dex.patterns.workflowpatterns.DesignTimeMultipleInstancesFlow.ReviewDocument;
import org.springframework.stereotype.Component;

/** WCP-14 Multiple Instances with Run-Time Knowledge: the start input sets the instance count. */
@Component
public class RunTimeMultipleInstancesFlow implements Flow<String[]> {
    private final StartRunTimeReviews startRunTimeReviews = new StartRunTimeReviews();
    private final ReviewDocument reviewDocument = new ReviewDocument();
    private final CollectReviews collectReviews = new CollectReviews();

    @Override
    public StepList<String[]> getSteps() {
        return StepList.startStep(startRunTimeReviews).otherSteps(reviewDocument, collectReviews);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return DesignTimeMultipleInstancesFlow.reviewPersistenceSchema();
    }

    /** Counts the ReviewDocument instances that are in flight at the gate. */
    @RPC(loadAttributeMaps = {"startedReviewers"})
    public RPCResult<Integer> getStartedRunTimeReviewCount(final Context context) {
        return RPCResult.of(DesignTimeMultipleInstancesFlow.startedReviewers.getMapSize(context));
    }

    /** Lets count waiting ReviewDocument instances finish. */
    @RPC
    public void openRunTimeReviewGate(final Context context, final Integer count) {
        for (int index = 0; index < count; index++) {
            DesignTimeMultipleInstancesFlow.reviewGateOpened.publish(context, "opened");
        }
    }

    static final class StartRunTimeReviews implements Step<String[]> {
        @Override
        public Class<String[]> getInputType() {
            return String[].class;
        }

        @Override
        public StepDecision execute(final Context context, final String[] reviewers) {
            return DesignTimeMultipleInstancesFlow.startReviews(context, reviewers);
        }
    }
}
