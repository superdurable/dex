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

import com.google.protobuf.Empty;
import io.grpc.Server;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.StreamObserver;
import io.superdurable.gen.FlowDefinitionUploadOutcome;
import io.superdurable.gen.FlowDefinitionUploadResult;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.ServerInfo;
import io.superdurable.gen.SyncAttributeIndexRequest;
import io.superdurable.gen.SyncAttributeIndexResponse;
import io.superdurable.gen.UploadFlowDefinitionsRequest;
import io.superdurable.gen.UploadFlowDefinitionsResponse;
import org.junit.jupiter.api.Test;

import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.Instant;
import java.util.Arrays;
import java.util.List;
import java.util.Optional;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class FlowDefinitionUploadDeadlineTest {
    private static final int LARGE_CONTENT_BYTES = 9 * 1024 * 1024;

    @Test
    void sharesOneDeadlineAcrossUploadBatches() throws Exception {
        final List<Long> remainingMillis = new CopyOnWriteArrayList<Long>();
        final Server server = startServer((request, observer) -> {
            remainingMillis.add(io.grpc.Context.current().getDeadline().timeRemaining(TimeUnit.MILLISECONDS));
            sleepQuietly(600);
            respondCreated(request, observer);
        });
        final Worker worker = newWorker(server, Duration.ofMillis(1500));
        final Thread workerThread = startWorkerThread(worker);
        try {
            worker.waitPullRegistered(Duration.ofSeconds(10));
            assertEquals(2, remainingMillis.size());
            assertTrue(remainingMillis.get(0) > 1200, "first batch remaining " + remainingMillis.get(0));
            assertTrue(remainingMillis.get(1) < 1000, "second batch remaining " + remainingMillis.get(1));
        } finally {
            worker.close();
            workerThread.join(10_000L);
            server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    @Test
    void stopCancelsAnInFlightUpload() throws Exception {
        final CountDownLatch uploadStarted = new CountDownLatch(1);
        final Server server = startServer((request, observer) -> {
            uploadStarted.countDown();
            final long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(30);
            while (!io.grpc.Context.current().isCancelled() && System.nanoTime() < deadline) {
                sleepQuietly(20);
            }
        });
        final Worker worker = newWorker(server, Duration.ofMinutes(2));
        final Thread workerThread = startWorkerThread(worker);
        try {
            assertTrue(uploadStarted.await(10, TimeUnit.SECONDS));
            final long stopStarted = System.nanoTime();
            worker.stop();
            workerThread.join(10_000L);
            assertFalse(workerThread.isAlive());
            assertTrue(System.nanoTime() - stopStarted < TimeUnit.SECONDS.toNanos(10));
        } finally {
            worker.close();
            server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    private static Worker newWorker(final Server server, final Duration timeout) {
        final List<Flow<?>> flows = Arrays.<Flow<?>>asList(
                new DeadlineFlow("DeadlineFirstFlow"), new DeadlineFlow("DeadlineSecondFlow"));
        return new Worker(new Registry(flows), new EmptyBlobCache(), WorkerOptions.newBuilder()
                .serverAddress("127.0.0.1:" + server.getPort())
                .attributeIndexSyncTimeout(timeout)
                .flowDefinitions(Arrays.asList(
                        largeDocument("DeadlineFirstFlow"), largeDocument("DeadlineSecondFlow")))
                .build());
    }

    private static FlowDefinitionDocument largeDocument(final String flowType) {
        final StringBuilder content = new StringBuilder("{\"schemaVersion\":\"2.0\",\"flow\":{\"name\":\"")
                .append(flowType).append("\"},\"padding\":\"");
        while (content.length() < LARGE_CONTENT_BYTES) {
            content.append("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx");
        }
        content.append("\"}");
        return new FlowDefinitionDocument(flowType, content.toString().getBytes(StandardCharsets.UTF_8),
                Instant.parse("2026-11-01T00:00:00Z"));
    }

    private static Thread startWorkerThread(final Worker worker) {
        final Thread thread = new Thread(() -> {
            try {
                worker.start();
            } catch (RuntimeException ignored) {
                // A stopped Worker may end start with a shutdown failure; the test checks liveness.
            }
        }, "flow-definition-deadline-worker");
        thread.start();
        return thread;
    }

    private static Server startServer(final UploadHandler uploadHandler) throws Exception {
        return NettyServerBuilder.forAddress(new java.net.InetSocketAddress("127.0.0.1", 0))
                .maxInboundMessageSize(64 * 1024 * 1024)
                .addService(new PullTaskTestService())
                .addService(new FlowServiceGrpc.FlowServiceImplBase() {
                    @Override
                    public void getServerInfo(final Empty request, final StreamObserver<ServerInfo> observer) {
                        observer.onNext(ServerInfo.newBuilder()
                                .setServerVersion("test")
                                .setMinimumSupportedProtocolVersion(1)
                                .setCurrentProtocolVersion(4)
                                .build());
                        observer.onCompleted();
                    }

                    @Override
                    public void syncAttributeIndexes(final SyncAttributeIndexRequest request,
                            final StreamObserver<SyncAttributeIndexResponse> observer) {
                        observer.onNext(SyncAttributeIndexResponse.getDefaultInstance());
                        observer.onCompleted();
                    }

                    @Override
                    public void uploadFlowDefinitions(final UploadFlowDefinitionsRequest request,
                            final StreamObserver<UploadFlowDefinitionsResponse> observer) {
                        uploadHandler.handle(request, observer);
                    }
                })
                .build()
                .start();
    }

    private static void respondCreated(
            final UploadFlowDefinitionsRequest request,
            final StreamObserver<UploadFlowDefinitionsResponse> observer) {
        final UploadFlowDefinitionsResponse.Builder response = UploadFlowDefinitionsResponse.newBuilder();
        for (io.superdurable.gen.FlowDefinitionUpload upload : request.getDefinitionsList()) {
            response.addResults(FlowDefinitionUploadResult.newBuilder()
                    .setFlowType(upload.getFlowType())
                    .setOutcome(FlowDefinitionUploadOutcome.FLOW_DEFINITION_UPLOAD_OUTCOME_CREATED));
        }
        observer.onNext(response.build());
        observer.onCompleted();
    }

    private static void sleepQuietly(final long millis) {
        try {
            Thread.sleep(millis);
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
        }
    }

    private interface UploadHandler {
        void handle(UploadFlowDefinitionsRequest request, StreamObserver<UploadFlowDefinitionsResponse> observer);
    }

    private static final class DeadlineFlow implements Flow<String> {
        private final String flowType;
        private final DeadlineStep step = new DeadlineStep();

        private DeadlineFlow(final String flowType) {
            this.flowType = flowType;
        }

        @Override
        public String getFlowType() {
            return flowType;
        }

        @Override
        public StepList<String> getSteps() {
            return StepList.startStep(step);
        }
    }

    private static final class DeadlineStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.gracefulComplete(input);
        }
    }

    private static final class EmptyBlobCache implements BlobCache {
        @Override
        public Optional<byte[]> get(final String blobId) {
            return Optional.empty();
        }

        @Override
        public boolean put(final String blobId, final byte[] payload) {
            return true;
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
