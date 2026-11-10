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

package io.superdurable.dex.integ;

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
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import io.superdurable.dex.patterns.cancelcase.LoanApplicationFlow;
import io.superdurable.dex.patterns.deferredchoice.OfferResponseFlow;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;

/**
 * Asserts that the first-declared condition wins when both competing messages are pending. The
 * probe Flows hold their first wait behind a gate, so both messages are pending before the AnyOf
 * registers.
 */
@ExtendWith(SharedIntegExtension.class)
public class ConditionOrderIntegTest {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(45);

    static final Channel<Void> probeGateOpened = Channel.define("probeGateOpened", Void.class);

    /** Which condition won and how many messages the other left pending. */
    public record ConditionOrderOutcome(String winner, int loserPendingCount) {
    }

    // The Client starts only the Flow instances registered with the Worker.
    private static final DeferredChoiceOrderProbeFlow deferredChoiceOrderProbeFlow =
            new DeferredChoiceOrderProbeFlow();
    private static final CancelCaseOrderProbeFlow cancelCaseOrderProbeFlow =
            new CancelCaseOrderProbeFlow();

    static List<Flow<?>> conditionOrderProbeFlows() {
        return List.of(deferredChoiceOrderProbeFlow, cancelCaseOrderProbeFlow);
    }

    @Test
    void deferredChoiceDeclaredFirstResponseWinsWhenBothArePending() {
        final String flowId =
                startProbe(deferredChoiceOrderProbeFlow, "pattern-deferred-choice-order");
        final DeferredChoiceOrderProbeFlow stub =
                client().newRpcStub(DeferredChoiceOrderProbeFlow.class, flowId);
        // Publish the later-declared response first: declaration order wins, not publish order.
        client().invokeRPC(stub::rejectProbeOffer, "no");
        client().invokeRPC(stub::acceptProbeOffer, "yes");
        client().invokeRPC(stub::openProbeGate);
        assertEquals(
                List.of(new ConditionOrderOutcome("accepted", 1)),
                completedOutputs(flowId, "AwaitOfferProbeResponse"));
    }

    @Test
    void cancelCaseCancelWinsWhenBothArePending() {
        final String flowId = startProbe(cancelCaseOrderProbeFlow, "pattern-cancel-case-order");
        final CancelCaseOrderProbeFlow stub =
                client().newRpcStub(CancelCaseOrderProbeFlow.class, flowId);
        client().invokeRPC(stub::decideProbeApplication, "approved");
        client().invokeRPC(stub::cancelProbeApplication, "applicant withdrew");
        client().invokeRPC(stub::openProbeGate);
        assertEquals(
                List.of(new ConditionOrderOutcome("canceled", 1)),
                completedOutputs(flowId, "AwaitProbeDecision"));
    }

    private static Client client() {
        return SharedIntegExtension.environment().client();
    }

    private static String startProbe(final Flow<Void> flow, final String prefix) {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final String flowId = environment.newFlowId(prefix);
        client().startFlow(flow, flowId, null, environment.startOptions());
        return flowId;
    }

    private static List<ConditionOrderOutcome> completedOutputs(
            final String flowId, final String stepType) {
        final FlowResult result = client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.COMPLETED, result.getStatus(), flowId + ": " + result.getErrorMessage());
        return result.getCompletions().stream()
                .filter(completion -> completion.getStepType().equals(stepType))
                .map(completion -> completion.getOutput(ConditionOrderOutcome.class))
                .toList();
    }

    public static class DeferredChoiceOrderProbeFlow implements Flow<Void> {
        private final HoldOfferProbe holdOfferProbe = new HoldOfferProbe();
        private final AwaitOfferProbeResponse awaitOfferProbeResponse = new AwaitOfferProbeResponse();

        @Override
        public StepList<Void> getSteps() {
            return StepList.startStep(holdOfferProbe).otherSteps(awaitOfferProbeResponse);
        }

        @Override
        public PersistenceSchema getPersistenceSchema() {
            return PersistenceSchema.of(
                    probeGateOpened, OfferResponseFlow.offerAccepted, OfferResponseFlow.offerRejected);
        }

        @RPC
        public void acceptProbeOffer(final Context context, final String note) {
            OfferResponseFlow.offerAccepted.publish(context, note);
        }

        @RPC
        public void rejectProbeOffer(final Context context, final String note) {
            OfferResponseFlow.offerRejected.publish(context, note);
        }

        @RPC
        public void openProbeGate(final Context context) {
            probeGateOpened.publish(context, null);
        }
    }

    static final class HoldOfferProbe implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(probeGateOpened.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goTo(AwaitOfferProbeResponse.class, null);
        }
    }

    static final class AwaitOfferProbeResponse implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.anyOf(
                    OfferResponseFlow.offerAccepted.forOne(),
                    OfferResponseFlow.offerRejected.forOne(),
                    Timer.byDuration(OfferResponseFlow.OFFER_EXPIRY));
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            if (!OfferResponseFlow.offerAccepted.getConditionResults(context).isEmpty()) {
                return StepDecision.gracefulComplete(new ConditionOrderOutcome(
                        "accepted", OfferResponseFlow.offerRejected.size(context)));
            }
            return StepDecision.gracefulComplete(new ConditionOrderOutcome(
                    "rejected", OfferResponseFlow.offerAccepted.size(context)));
        }
    }

    public static class CancelCaseOrderProbeFlow implements Flow<Void> {
        private final HoldDecisionProbe holdDecisionProbe = new HoldDecisionProbe();
        private final AwaitProbeDecision awaitProbeDecision = new AwaitProbeDecision();

        @Override
        public StepList<Void> getSteps() {
            return StepList.startStep(holdDecisionProbe).otherSteps(awaitProbeDecision);
        }

        @Override
        public PersistenceSchema getPersistenceSchema() {
            return PersistenceSchema.of(
                    probeGateOpened,
                    LoanApplicationFlow.applicationCanceled,
                    LoanApplicationFlow.underwriterDecided);
        }

        @RPC
        public void cancelProbeApplication(final Context context, final String reason) {
            LoanApplicationFlow.applicationCanceled.publish(context, reason);
        }

        @RPC
        public void decideProbeApplication(final Context context, final String decision) {
            LoanApplicationFlow.underwriterDecided.publish(context, decision);
        }

        @RPC
        public void openProbeGate(final Context context) {
            probeGateOpened.publish(context, null);
        }
    }

    static final class HoldDecisionProbe implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(probeGateOpened.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goTo(AwaitProbeDecision.class, null);
        }
    }

    static final class AwaitProbeDecision implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.anyOf(
                    LoanApplicationFlow.applicationCanceled.forOne(),
                    LoanApplicationFlow.underwriterDecided.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            if (!LoanApplicationFlow.applicationCanceled.getConditionResults(context).isEmpty()) {
                return StepDecision.gracefulComplete(new ConditionOrderOutcome(
                        "canceled", LoanApplicationFlow.underwriterDecided.size(context)));
            }
            return StepDecision.gracefulComplete(new ConditionOrderOutcome(
                    "decided", LoanApplicationFlow.applicationCanceled.size(context)));
        }
    }
}
