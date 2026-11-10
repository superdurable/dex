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

package io.superdurable.dex.patterns.cancelcase;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Client;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;
import io.superdurable.dex.SubFlow;
import io.superdurable.dex.SubFlowOptions;
import io.superdurable.dex.SubFlowReusePolicy;
import io.superdurable.dex.Wait;
import io.superdurable.dex.exceptions.FlowClosingException;
import io.superdurable.dex.exceptions.FlowNotActiveOrNotFoundException;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.patterns.cancelcase.CreditCheckFlow.CreditCheckRequest;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.util.List;

/**
 * Runs a credit check in a child Flow. Canceling the application also stops the child Flow.
 */
@Component
public class LoanApplicationFlow implements Flow<LoanApplicationFlow.LoanApplication> {
    /**
     * The child's own Flow timeout. It stops the child even when the parent closes without
     * stopping it.
     */
    public static final Duration CREDIT_CHECK_TIMEOUT = Duration.ofDays(7);

    private static final String APPLICATION_CANCELED_PREFIX = "application canceled: ";

    /** The input of {@link LoanApplicationFlow}. */
    public record LoanApplication(String applicantId, int creditReportDelaySeconds) {
    }

    public static final Attribute<String> stoppedCreditCheckFlowId =
            Attribute.define("stoppedCreditCheckFlowId", String.class);
    public static final Channel<String> applicationCanceled =
            Channel.define("applicationCanceled", String.class);
    public static final Channel<String> underwriterDecided =
            Channel.define("underwriterDecided", String.class);

    private final RunCreditCheck runCreditCheck;
    private final AwaitUnderwriterDecision awaitUnderwriterDecision =
            new AwaitUnderwriterDecision();

    public LoanApplicationFlow(final ObjectProvider<Client> clientProvider) {
        runCreditCheck = new RunCreditCheck(clientProvider);
    }

    @Override
    public StepList<LoanApplication> getSteps() {
        return StepList.startStep(runCreditCheck).otherSteps(awaitUnderwriterDecision);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                stoppedCreditCheckFlowId, applicationCanceled, underwriterDecided);
    }

    /** Cancels the application and the credit check it started. */
    @RPC
    public void cancelApplication(final Context context, final String reason) {
        applicationCanceled.publish(context, reason);
    }

    /** Records the underwriter's decision. */
    @RPC
    public void decideApplication(final Context context, final String decision) {
        underwriterDecided.publish(context, decision);
    }

    /** Waits for its child Flow or a cancellation, whichever comes first. */
    static final class RunCreditCheck implements Step<LoanApplication> {
        private final ObjectProvider<Client> clientProvider;

        RunCreditCheck(final ObjectProvider<Client> clientProvider) {
            this.clientProvider = clientProvider;
        }

        @Override
        public Class<LoanApplication> getInputType() {
            return LoanApplication.class;
        }

        @Override
        public Wait waitFor(final Context context, final LoanApplication application) {
            final CreditCheckRequest request = new CreditCheckRequest(
                    application.applicantId(), application.creditReportDelaySeconds());
            // The cancel condition comes first: it wins when both are ready.
            return Wait.anyOf(
                    applicationCanceled.forOne(),
                    SubFlow.run(CreditCheckFlow.class, request, SubFlowOptions.newBuilder()
                            .timeout(CREDIT_CHECK_TIMEOUT)
                            .reusePolicy(SubFlowReusePolicy.RESTART_IF_PREVIOUS_EXITS_ABNORMALLY)
                            .build()));
        }

        @Override
        public StepDecision execute(final Context context, final LoanApplication application) {
            final List<String> reasons = applicationCanceled.getConditionResults(context);
            if (!reasons.isEmpty()) {
                final String childFlowId = SubFlow.getFlowId(context);
                stopCreditCheck(childFlowId);
                stoppedCreditCheckFlowId.set(context, childFlowId);
                return StepDecision.forceFail(APPLICATION_CANCELED_PREFIX + reasons.get(0));
            }
            final FlowResult result = SubFlow.getConditionResults(context);
            if (result.getStatus() != FlowStatus.COMPLETED) {
                return StepDecision.forceFail(
                        "credit check ended with status " + result.getStatus());
            }
            final String report = result.getSingleOutput(String.class);
            return StepDecision.goTo(AwaitUnderwriterDecision.class, report);
        }

        /** Cancels the child; a missing or already closed child counts as stopped. */
        private void stopCreditCheck(final String childFlowId) {
            try {
                clientProvider.getObject().stopFlow(childFlowId,
                        new StopFlowOptions(StopType.CANCEL, "loan application canceled"));
            } catch (final FlowNotFoundException
                    | FlowNotActiveOrNotFoundException
                    | FlowClosingException alreadyStopped) {
                // The child is already gone or closing.
            }
        }
    }

    /**
     * Also waits for the cancel Channel, so a cancellation after the credit check still closes the
     * application.
     */
    static final class AwaitUnderwriterDecision implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String report) {
            return Wait.anyOf(applicationCanceled.forOne(), underwriterDecided.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final String report) {
            final List<String> reasons = applicationCanceled.getConditionResults(context);
            if (!reasons.isEmpty()) {
                return StepDecision.forceFail(APPLICATION_CANCELED_PREFIX + reasons.get(0));
            }
            final String decision = underwriterDecided.getConditionResults(context).get(0);
            return StepDecision.gracefulComplete(decision + " after " + report);
        }
    }
}
