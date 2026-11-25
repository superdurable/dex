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
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepCompletion;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.TimerId;
import io.superdurable.dex.Wait;
import io.superdurable.dex.WaitForStepCompletionOptions;
import io.superdurable.dex.exceptions.DexServiceException;
import io.superdurable.dex.exceptions.RpcLockConflictException;
import io.superdurable.gen.ActiveStepExecutionState;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.GetFlowStateRequest;
import io.superdurable.gen.GetFlowStateResponse;
import io.superdurable.gen.StepMethodPendingReason;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.superdurable.dex.patterns.deferredchoice.OfferResponseFlow;
import io.superdurable.dex.patterns.deferredchoice.OfferResponseFlow.AwaitOfferResponse;
import io.superdurable.dex.patterns.deferredchoice.OfferResponseFlow.CloseRejectedOffer;
import io.superdurable.dex.patterns.deferredchoice.OfferResponseFlow.ExpireOffer;
import io.superdurable.dex.patterns.deferredchoice.OfferResponseFlow.FulfillOffer;
import io.superdurable.dex.patterns.deferredchoice.OfferResponseFlow.RouteResponseBeforeExpiry;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

import java.time.Duration;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.function.Supplier;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Asserts the reply window lock of the Decision with Deadline example. The probe Flow runs the
 * example's Steps beside a gated Step, so late responses reach the window check.
 */
@ExtendWith(SharedIntegExtension.class)
public class OfferResponseWindowIntegTest {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(45);
    private static final int LOCK_CONFLICT_ATTEMPTS = 10;

    static final Channel<Void> offerProbeGateOpened =
            Channel.define("offerProbeGateOpened", Void.class);

    // Each test that uses the held response RPC creates fresh latches before it starts.
    private static volatile CountDownLatch heldResponseEntered = new CountDownLatch(1);
    private static volatile CountDownLatch heldResponseReleased = new CountDownLatch(1);

    // The Client starts only the Flow instances registered with the Worker.
    private static final OfferResponseWindowProbeFlow offerResponseWindowProbeFlow =
            new OfferResponseWindowProbeFlow();

    static List<Flow<?>> offerResponseWindowProbeFlows() {
        return List.of(offerResponseWindowProbeFlow);
    }

    @Test
    void responseAfterTheDecisionGetsWindowClosed() {
        final String flowId = startProbe("pattern-deferred-choice-window", "offer-3");
        final OfferResponseWindowProbeFlow stub = probeStub(flowId);
        assertEquals(
                OfferResponseFlow.OFFER_RESPONSE_RECORDED,
                client().invokeRPC(stub::acceptProbeOffer, "yes"));
        waitForStep(flowId, "AwaitOfferResponse");

        assertEquals(
                OfferResponseFlow.OFFER_RESPONSE_WINDOW_CLOSED,
                client().invokeRPC(stub::rejectProbeOffer, "changed my mind"));

        client().invokeRPC(stub::openProbeGate);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("fulfilled offer-3"), outputsOf(completions, "FulfillOffer"));
        assertTrue(outputsOf(completions, "CloseRejectedOffer").isEmpty());
    }

    @Test
    void secondResponseGetsWindowClosedEvenBeforeTheDecision() throws InterruptedException {
        final String flowId = startProbe("pattern-deferred-choice-double", "offer-5");
        final OfferResponseWindowProbeFlow stub = probeStub(flowId);
        // Both responses can commit before the wait registers or before execute takes the lock.
        assertEquals(
                OfferResponseFlow.OFFER_RESPONSE_RECORDED,
                invokeRetryingLockConflicts(() -> client().invokeRPC(stub::acceptProbeOffer, "yes")));
        assertEquals(
                OfferResponseFlow.OFFER_RESPONSE_WINDOW_CLOSED,
                invokeRetryingLockConflicts(() -> client().invokeRPC(stub::rejectProbeOffer, "no")));

        waitForStep(flowId, "AwaitOfferResponse");
        client().invokeRPC(stub::openProbeGate);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("fulfilled offer-5"), outputsOf(completions, "FulfillOffer"));
        assertTrue(outputsOf(completions, "CloseRejectedOffer").isEmpty());
        assertTrue(outputsOf(completions, "RouteResponseBeforeExpiry").isEmpty());
    }

    @Test
    void responseCommittedAfterTheTimerWonIsTaken() throws Exception {
        heldResponseEntered = new CountDownLatch(1);
        heldResponseReleased = new CountDownLatch(1);
        final String flowId = startProbe("pattern-deferred-choice-late", "offer-4");
        waitForStep(flowId, "OpenOfferProbe");
        final OfferResponseWindowProbeFlow stub = probeStub(flowId);

        final CompletableFuture<String> held = CompletableFuture.supplyAsync(
                () -> client().invokeRPC(stub::acceptProbeOfferWhenReleased, "yes, just in time"));
        try {
            assertTrue(
                    heldResponseEntered.await(WAIT_TIMEOUT.toSeconds(), TimeUnit.SECONDS),
                    flowId + ": the held response RPC never ran");
            // The Timer wins while the RPC holds the window lock, so execute waits for the RPC.
            skipOfferExpiry(flowId);
            awaitExecuteWaitingForLocks(flowId);
        } finally {
            heldResponseReleased.countDown();
        }
        assertEquals(
                OfferResponseFlow.OFFER_RESPONSE_RECORDED,
                held.get(WAIT_TIMEOUT.toSeconds(), TimeUnit.SECONDS));

        waitForStep(flowId, "RouteResponseBeforeExpiry");
        client().invokeRPC(stub::openProbeGate);
        final List<StepCompletion> completions = waitForCompletedFlow(flowId);
        assertEquals(List.of("fulfilled offer-4"), outputsOf(completions, "FulfillOffer"));
        assertTrue(outputsOf(completions, "ExpireOffer").isEmpty());
    }

    private static Client client() {
        return SharedIntegExtension.environment().client();
    }

    private static OfferResponseWindowProbeFlow probeStub(final String flowId) {
        return client().newRpcStub(OfferResponseWindowProbeFlow.class, flowId);
    }

    private static String startProbe(final String prefix, final String offerId) {
        final IntegEnvironment environment = SharedIntegExtension.environment();
        final String flowId = environment.newFlowId(prefix);
        client().startFlow(offerResponseWindowProbeFlow, flowId, offerId, environment.startOptions());
        return flowId;
    }

    private static void waitForStep(final String flowId, final String stepType) {
        client().waitForStepCompletion(
                flowId,
                StepExecutionId.of(stepType, 1),
                WaitForStepCompletionOptions.newBuilder().requestTimeout(WAIT_TIMEOUT).build());
    }

    // Retries until the Timer of AwaitOfferResponse is registered.
    private static void skipOfferExpiry(final String flowId) throws InterruptedException {
        SharedIntegExtension.environment().awaitCondition(() -> {
            try {
                client().skipTimer(
                        flowId,
                        StepExecutionId.of("AwaitOfferResponse", 1),
                        TimerId.byConditionIndex(0));
                return true;
            } catch (final DexServiceException notReady) {
                return false;
            }
        }, Boolean::booleanValue, WAIT_TIMEOUT, flowId + " offer expiry Timer was never skipped");
    }

    // A lock conflict committed nothing, so retrying it is safe.
    private static String invokeRetryingLockConflicts(final Supplier<String> invocation)
            throws InterruptedException {
        for (int attempt = 1; ; attempt++) {
            try {
                return invocation.get();
            } catch (final RpcLockConflictException conflict) {
                if (attempt == LOCK_CONFLICT_ATTEMPTS) {
                    throw conflict;
                }
                Thread.sleep(attempt * 200L);
            }
        }
    }

    // Fails unless AwaitOfferResponse execute reports waiting for its locks.
    private static void awaitExecuteWaitingForLocks(final String flowId)
            throws InterruptedException {
        final ManagedChannel channel = ManagedChannelBuilder
                .forTarget(System.getenv().getOrDefault("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801"))
                .usePlaintext()
                .build();
        try {
            final FlowServiceGrpc.FlowServiceBlockingStub flowService =
                    FlowServiceGrpc.newBlockingStub(channel);
            SharedIntegExtension.environment().awaitCondition(
                    () -> flowService.getFlowState(
                            GetFlowStateRequest.newBuilder().setFlowId(flowId).build()),
                    OfferResponseWindowIntegTest::isAwaitOfferResponseWaitingForLocks,
                    WAIT_TIMEOUT,
                    flowId + ": AwaitOfferResponse execute never waited for its locks");
        } finally {
            channel.shutdownNow();
        }
    }

    private static boolean isAwaitOfferResponseWaitingForLocks(final GetFlowStateResponse state) {
        for (final ActiveStepExecutionState step : state.getActiveStepExecutionsList()) {
            if (step.getStepType().equals("AwaitOfferResponse")
                    && step.hasExecuteInvocation()
                    && step.getExecuteInvocation().getPendingReason()
                            == StepMethodPendingReason.STEP_METHOD_PENDING_REASON_WAITING_FOR_LOCKS) {
                return true;
            }
        }
        return false;
    }

    private static List<StepCompletion> waitForCompletedFlow(final String flowId) {
        final FlowResult result = client().waitForFlow(flowId, WAIT_TIMEOUT);
        assertEquals(FlowStatus.COMPLETED, result.getStatus(), flowId + ": " + result.getErrorMessage());
        return result.getCompletions();
    }

    private static List<String> outputsOf(
            final List<StepCompletion> completions, final String stepType) {
        return completions.stream()
                .filter(completion -> completion.getStepType().equals(stepType))
                .map(completion -> completion.getOutput(String.class))
                .toList();
    }

    /** Runs the Decision with Deadline Steps beside a gated Step that keeps the Flow open. */
    public static class OfferResponseWindowProbeFlow implements Flow<String> {
        private final OfferResponseFlow offerResponseFlow = new OfferResponseFlow();

        @Override
        public StepList<String> getSteps() {
            return StepList.startStep(new OpenOfferProbe()).otherSteps(
                    new HoldOfferProbeOpen(),
                    new AwaitOfferResponse(),
                    new FulfillOffer(),
                    new CloseRejectedOffer(),
                    new RouteResponseBeforeExpiry(),
                    new ExpireOffer());
        }

        @Override
        public PersistenceSchema getPersistenceSchema() {
            return PersistenceSchema.of(
                    offerProbeGateOpened,
                    OfferResponseFlow.offerAccepted,
                    OfferResponseFlow.offerRejected,
                    OfferResponseFlow.offerResponseWindow);
        }

        @RPC(lockAttributes = {"offerResponseWindow"})
        public RPCResult<String> acceptProbeOffer(final Context context, final String note) {
            return offerResponseFlow.acceptCustomerOffer(context, note);
        }

        @RPC(lockAttributes = {"offerResponseWindow"})
        public RPCResult<String> rejectProbeOffer(final Context context, final String note) {
            return offerResponseFlow.rejectCustomerOffer(context, note);
        }

        /** Holds the window lock until the test releases it. */
        @RPC(lockAttributes = {"offerResponseWindow"})
        public RPCResult<String> acceptProbeOfferWhenReleased(
                final Context context, final String note) {
            heldResponseEntered.countDown();
            try {
                if (!heldResponseReleased.await(WAIT_TIMEOUT.toSeconds(), TimeUnit.SECONDS)) {
                    throw new IllegalStateException("the test never released the held response");
                }
            } catch (final InterruptedException interrupted) {
                Thread.currentThread().interrupt();
                throw new IllegalStateException("interrupted holding the response", interrupted);
            }
            return offerResponseFlow.acceptCustomerOffer(context, note);
        }

        @RPC
        public void openProbeGate(final Context context) {
            offerProbeGateOpened.publish(context, null);
        }
    }

    static final class OpenOfferProbe implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String offerId) {
            return StepDecision.goToMany(
                    StepMovement.of(AwaitOfferResponse.class, offerId),
                    StepMovement.of(HoldOfferProbeOpen.class, null));
        }
    }

    /** Keeps the Flow open until the test opens the gate. */
    static final class HoldOfferProbeOpen implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(offerProbeGateOpened.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.deadEnd();
        }
    }
}
