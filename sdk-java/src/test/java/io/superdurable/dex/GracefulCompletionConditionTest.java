/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.superdurable.dex.exceptions.GracefulCompletionConditionUnsupportedException;
import io.superdurable.dex.exceptions.InvalidStepResultException;
import io.superdurable.gen.ConditionResults;
import io.superdurable.gen.ConditionStatus;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.GracefulCompletionResult;
import io.superdurable.gen.InvokeExecuteMethodRequest;
import io.superdurable.gen.InvokeWaitForMethodRequest;
import io.superdurable.gen.WaitingCondition;
import io.superdurable.gen.WaitingConditionType;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.Test;

import java.util.Arrays;
import java.util.Collections;
import java.util.Optional;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class GracefulCompletionConditionTest {
    private static final ManagedChannel UNUSED_CHANNEL =
            ManagedChannelBuilder.forTarget("127.0.0.1:1").usePlaintext().build();
    private static final Channel<String> MESSAGES = Channel.define("messages", String.class);
    private static final AtomicReference<Boolean> OBSERVED_COMPLETION = new AtomicReference<Boolean>();

    @AfterAll
    static void closeUnusedChannel() throws InterruptedException {
        UNUSED_CHANNEL.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    }

    @Test
    void mapsTheConditionWhenTheServerNegotiatedProtocolSeven() throws Throwable {
        final WorkerDispatcher dispatcher = dispatcher(negotiated(7));

        final WaitingCondition anyOf = dispatcher.invokeWaitFor(waitForRequest("any-of"), null)
                .getWaitingCondition();
        assertEquals(WaitingConditionType.WAITING_CONDITION_TYPE_ANY_COMPLETED,
                anyOf.getWaitingConditionType());
        assertEquals(1, anyOf.getChannelConditionsCount());
        assertEquals(1, anyOf.getGracefulCompletionConditionsCount());
        assertEquals("", anyOf.getGracefulCompletionConditions(0).getConditionId());

        final WaitingCondition combination = dispatcher.invokeWaitFor(
                waitForRequest("combination"), null).getWaitingCondition();
        assertEquals(1, combination.getGracefulCompletionConditionsCount());
        assertEquals("graceful", combination.getGracefulCompletionConditions(0).getConditionId());
        assertEquals(2, combination.getConditionCombinationsCount());
        assertEquals(Collections.singletonList("graceful"),
                combination.getConditionCombinations(1).getConditionIdsList());
    }

    @Test
    void rejectsTheConditionBeforeSendingWhenTheServerIsOlder() throws Throwable {
        for (NegotiatedServerProtocol olderProtocol
                : Arrays.asList(negotiated(6), new NegotiatedServerProtocol())) {
            final WorkerDispatcher dispatcher = dispatcher(olderProtocol);
            final GracefulCompletionConditionUnsupportedException failure = assertThrows(
                    GracefulCompletionConditionUnsupportedException.class,
                    () -> dispatcher.invokeWaitFor(waitForRequest("any-of"), null));
            assertEquals(7, failure.getRequiredServerProtocolVersion());
            assertEquals(olderProtocol.getNegotiatedVersion(),
                    failure.getNegotiatedServerProtocolVersion());
            assertTrue(failure.getMessage().contains("requires Server protocol 7"), failure.getMessage());
            assertEquals(1, dispatcher.invokeWaitFor(waitForRequest("channel-only"), null)
                    .getWaitingCondition().getChannelConditionsCount());
        }
    }

    @Test
    void rejectsASecondGracefulCompletionConditionInOneWait() {
        final WorkerDispatcher dispatcher = dispatcher(negotiated(7));
        final InvalidStepResultException failure = assertThrows(
                InvalidStepResultException.class,
                () -> dispatcher.invokeWaitFor(waitForRequest("two-graceful"), null));
        assertTrue(failure.getMessage().contains("at most one graceful completion condition"),
                failure.getMessage());
    }

    @Test
    void executeReadsTheGracefulCompletionResult() throws Throwable {
        final WorkerDispatcher dispatcher = dispatcher(negotiated(7));
        for (ConditionStatus status : Arrays.asList(
                ConditionStatus.CONDITION_STATUS_COMPLETED, ConditionStatus.CONDITION_STATUS_WAITING)) {
            OBSERVED_COMPLETION.set(null);
            dispatcher.invokeExecute(executeRequest(ConditionResults.newBuilder()
                    .addGracefulCompletionResults(GracefulCompletionResult.newBuilder()
                            .setConditionStatus(status))
                    .build()), null);
            assertEquals(status == ConditionStatus.CONDITION_STATUS_COMPLETED, OBSERVED_COMPLETION.get());
        }
        OBSERVED_COMPLETION.set(null);
        dispatcher.invokeExecute(executeRequest(null), null);
        assertFalse(OBSERVED_COMPLETION.get());
    }

    private static NegotiatedServerProtocol negotiated(final int version) {
        final NegotiatedServerProtocol protocol = new NegotiatedServerProtocol();
        protocol.recordNegotiatedVersion(version);
        return protocol;
    }

    private static WorkerDispatcher dispatcher(final NegotiatedServerProtocol protocol) {
        return new WorkerDispatcher(
                new Registry(Collections.<Flow<?>>singletonList(new GracefulConditionFlow())),
                values(),
                new ValueHydrator(FlowServiceGrpc.newBlockingStub(UNUSED_CHANNEL), new EmptyBlobCache()),
                protocol);
    }

    private static ValueMapper values() {
        return new ValueMapper(new ObjectMapper());
    }

    private static InvokeWaitForMethodRequest waitForRequest(final String shape) {
        return InvokeWaitForMethodRequest.newBuilder()
                .setFlowType("GracefulConditionFlow")
                .setStepType("GracefulConditionStep")
                .setContext(io.superdurable.gen.Context.newBuilder().setFlowId("graceful"))
                .setStepInput(values().encode(shape))
                .build();
    }

    private static InvokeExecuteMethodRequest executeRequest(final ConditionResults results) {
        final InvokeExecuteMethodRequest.Builder request = InvokeExecuteMethodRequest.newBuilder()
                .setFlowType("GracefulConditionFlow")
                .setStepType("GracefulConditionStep")
                .setContext(io.superdurable.gen.Context.newBuilder().setFlowId("graceful"))
                .setStepInput(values().encode("any-of"));
        if (results != null) {
            request.setConditionResults(results);
        }
        return request.build();
    }

    private static final class GracefulConditionFlow implements Flow<String> {
        @Override
        public StepList<String> getSteps() {
            return StepList.startStep(new GracefulConditionStep());
        }

        @Override
        public PersistenceSchema getPersistenceSchema() {
            return PersistenceSchema.of(MESSAGES);
        }
    }

    private static final class GracefulConditionStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String shape) {
            switch (shape) {
                case "channel-only":
                    return Wait.anyOf(MESSAGES.forOne());
                case "combination":
                    final Condition graceful = GracefulCompletion.requested("graceful");
                    return Wait.anyCombinationOf(
                            ConditionCombination.of(MESSAGES.forOne("message"), graceful),
                            ConditionCombination.of(graceful));
                case "two-graceful":
                    return Wait.anyOf(GracefulCompletion.requested(), GracefulCompletion.requested());
                default:
                    return Wait.anyOf(MESSAGES.forOne(), GracefulCompletion.requested());
            }
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            OBSERVED_COMPLETION.set(context.isGracefulCompletionConditionCompleted());
            return StepDecision.deadEnd();
        }
    }

    private static final class EmptyBlobCache implements BlobCache {
        @Override
        public Optional<byte[]> get(final String blobID) {
            return Optional.empty();
        }

        @Override
        public boolean put(final String blobID, final byte[] payload) {
            return false;
        }

        @Override
        public void delete(final String blobID) {
        }

        @Override
        public void deleteAll() {
        }

        @Override
        public void close() {
        }
    }
}
