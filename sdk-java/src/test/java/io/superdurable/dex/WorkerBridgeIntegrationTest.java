/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex;

import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.superdurable.gen.CloseDecisionType;
import io.superdurable.gen.InvokeExecuteMethodRequest;
import io.superdurable.gen.InvokeExecuteMethodResponse;
import io.superdurable.gen.InvokeWaitForMethodRequest;
import io.superdurable.gen.InvokeWaitForMethodResponse;
import io.superdurable.gen.WorkerServiceGrpc;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.net.ServerSocket;
import java.util.Collections;
import java.util.Optional;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;

final class WorkerBridgeIntegrationTest {
    @Test
    void routesCurrentWorkerProtobufThroughRustAndJava() throws Exception {
        final int port = availablePort();
        final Registry registry = new Registry(
                Collections.<Flow<?>>singletonList(new BridgeFlow()));
        final Worker worker = new Worker(
                registry,
                new TestBlobCache(),
                WorkerOptions.newBuilder()
                        .bindAddress("127.0.0.1:" + port)
                        .build());
        final Thread workerThread = new Thread(worker::start, "test-dex-worker");
        workerThread.start();

        final ManagedChannel channel = ManagedChannelBuilder
                .forAddress("127.0.0.1", port)
                .usePlaintext()
                .build();
        try {
            final WorkerServiceGrpc.WorkerServiceBlockingStub client =
                    WorkerServiceGrpc.newBlockingStub(channel)
                            .withWaitForReady()
                            .withDeadlineAfter(10, TimeUnit.SECONDS);
            final io.superdurable.gen.Context context = io.superdurable.gen.Context.newBuilder()
                    .setFlowId("flow-1")
                    .setRunId("run-1")
                    .setFlowStartedTimestamp(1)
                    .setStepExecutionId("step-1")
                    .setFirstAttemptTimestamp(1)
                    .setAttempt(1)
                    .build();
            final InvokeWaitForMethodResponse wait = client.invokeWaitForMethod(
                    InvokeWaitForMethodRequest.newBuilder()
                            .setContext(context)
                            .setFlowType("BridgeFlow")
                            .setStepType("BridgeStep")
                            .setStepInput(io.superdurable.gen.Value.newBuilder()
                                    .setStringValue("hello"))
                            .build());
            assertFalse(wait.hasWaitingCondition());

            final InvokeExecuteMethodResponse execute = client.invokeExecuteMethod(
                    InvokeExecuteMethodRequest.newBuilder()
                            .setContext(context)
                            .setFlowType("BridgeFlow")
                            .setStepType("BridgeStep")
                            .setStepInput(io.superdurable.gen.Value.newBuilder()
                                    .setStringValue("hello"))
                            .build());
            assertEquals(
                    CloseDecisionType.CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
                    execute.getStepDecision().getCloseDecision().getCloseDecisionType());
            assertEquals(
                    "hello",
                    execute.getStepDecision().getCloseDecision().getCloseInput().getStringValue());
        } finally {
            channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
            worker.close();
            workerThread.join(5_000);
        }
    }

    private static int availablePort() throws IOException {
        try (ServerSocket socket = new ServerSocket(0)) {
            return socket.getLocalPort();
        }
    }

    private static final class BridgeFlow implements Flow<String> {
        private final BridgeStep start = new BridgeStep();

        @Override
        public StepList<String> getSteps() {
            return StepList.startStep(start);
        }

        @Override
        public String getFlowType() {
            return "BridgeFlow";
        }
    }

    private static final class BridgeStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public String getStepType() {
            return "BridgeStep";
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.gracefulComplete(input);
        }
    }

    private static final class TestBlobCache implements BlobCache {
        @Override
        public Optional<byte[]> get(final String blobId) {
            return Optional.empty();
        }

        @Override
        public boolean put(final String blobId, final byte[] payload) {
            return false;
        }

        @Override
        public void delete(final String blobId) {
        }

        @Override
        public void deleteAll() {
        }

        @Override
        public void close() {
        }
    }
}
