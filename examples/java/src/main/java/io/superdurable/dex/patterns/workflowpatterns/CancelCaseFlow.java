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

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Client;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;
import io.superdurable.dex.SubFlow;
import io.superdurable.dex.Wait;
import io.superdurable.dex.exceptions.FlowNotActiveOrNotFoundException;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.stereotype.Component;

import java.util.List;

/** WCP-20 Cancel Case: a cancel request stops the child Flow, then fails the whole case. */
@Component
public class CancelCaseFlow implements Flow<Void> {
    public static final Attribute<String[]> stoppedChildFlowIds =
            Attribute.define("stoppedChildFlowIds", String[].class);
    public static final Channel<String> applicationReviewed =
            Channel.define("applicationReviewed", String.class);
    public static final Channel<String> cancelCase = Channel.define("cancelCase", String.class);

    private final StartApplication startApplication = new StartApplication();
    private final ReviewApplication reviewApplication = new ReviewApplication();
    private final RunBackgroundCheck runBackgroundCheck;
    private final WatchCaseCancellation watchCaseCancellation = new WatchCaseCancellation();

    public CancelCaseFlow(final ObjectProvider<Client> clientProvider) {
        runBackgroundCheck = new RunBackgroundCheck(clientProvider);
    }

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(startApplication)
                .otherSteps(reviewApplication, runBackgroundCheck, watchCaseCancellation);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(stoppedChildFlowIds, applicationReviewed, cancelCase);
    }

    @RPC
    public void cancelCase(final Context context, final String reason) {
        cancelCase.publish(context, reason);
    }

    private static StepDecision failCase(final String reason) {
        return StepDecision.forceFail("case canceled: " + reason);
    }

    static final class StartApplication implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goToMany(
                    StepMovement.of(ReviewApplication.class, null),
                    StepMovement.of(RunBackgroundCheck.class, null));
        }
    }

    static final class ReviewApplication implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(applicationReviewed.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(getStepType());
        }
    }

    static final class RunBackgroundCheck implements Step<Void> {
        private final ObjectProvider<Client> clientProvider;

        RunBackgroundCheck(final ObjectProvider<Client> clientProvider) {
            this.clientProvider = clientProvider;
        }

        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.anyOf(SubFlow.run(BackgroundCheckFlow.class, null), cancelCase.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final List<String> reasons = cancelCase.getConditionResults(context);
            if (reasons.isEmpty()) {
                return StepDecision.goTo(WatchCaseCancellation.class, null);
            }
            final String childFlowId = SubFlow.getFlowId(context, 0);
            stopChildFlow(childFlowId, reasons.get(0));
            stoppedChildFlowIds.set(context, new String[] {childFlowId});
            return failCase(reasons.get(0));
        }

        private void stopChildFlow(final String childFlowId, final String reason) {
            try {
                clientProvider.getObject().stopFlow(
                        childFlowId, new StopFlowOptions(StopType.CANCEL, reason));
            } catch (final FlowNotActiveOrNotFoundException alreadyClosed) {
                // A child that already closed or never started needs no stop.
            }
        }
    }

    static final class WatchCaseCancellation implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(cancelCase.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return failCase(cancelCase.getConditionResults(context).get(0));
        }
    }
}
