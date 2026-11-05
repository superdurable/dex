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
import io.grpc.Metadata;
import io.grpc.Server;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerInterceptor;
import io.grpc.ServerInterceptors;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import io.superdurable.dex.exceptions.DexServiceException;
import io.superdurable.dex.exceptions.RequestTimeoutException;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.HealthInfo;
import io.superdurable.gen.ServerInfo;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.function.Executable;

import java.net.InetSocketAddress;
import java.time.Duration;
import java.util.Arrays;
import java.util.Collections;
import java.util.Optional;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotSame;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class ServerProtocolCompatibilityTest {
    @Test
    void clientIsLazyAndCachesProtocolThreeFailureForEveryMethod() throws Exception {
        final ProtocolService service = new ProtocolService(3, false);
        final Server server = startLoopbackServer(service);
        try (Client client = new Client(new Registry(Collections.<Flow<?>>emptyList()), new EmptyBlobCache(),
                new ClientOptions("127.0.0.1:" + server.getPort()))) {
            assertEquals(0, service.negotiations.get());
            final IllegalStateException failure = assertThrows(IllegalStateException.class,
                    () -> client.stopFlow("flow"));
            assertTrue(failure.getMessage().contains("old-server"));
            assertTrue(failure.getMessage().contains("protocol versions 1-3"));
            assertTrue(failure.getMessage().contains("server-zd newer than v0.1.16"));
            final IllegalStateException cached = assertThrows(IllegalStateException.class, client::healthCheck);
            assertNotSame(failure, cached);
            assertEquals(failure.getMessage(), cached.getMessage());
            assertEquals(1, service.negotiations.get());
            assertEquals(0, service.healthChecks.get());
        } finally {
            server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    @Test
    void transientNegotiationFailureIsRetriedAndSuccessIsCached() throws Exception {
        final ProtocolService service = new ProtocolService(4, true);
        final Server server = startLoopbackServer(service);
        try (Client client = new Client(new Registry(Collections.<Flow<?>>emptyList()), new EmptyBlobCache(),
                new ClientOptions("127.0.0.1:" + server.getPort()))) {
            final DexServiceException unavailable = assertThrows(DexServiceException.class, client::healthCheck);
            assertEquals(Status.Code.UNAVAILABLE, unavailable.getCode());
            assertFalse(unavailable.getMessage().contains("incompatible"));
            assertTrue(client.healthCheck());
            assertTrue(client.healthCheck());
            assertEquals(2, service.negotiations.get());
            assertEquals(2, service.healthChecks.get());
        } finally {
            server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    @Test
    void everyClientNetworkMethodChecksProtocolBeforeSendingItsRequest() throws Exception {
        final ProtocolService service = new ProtocolService(3, false);
        final AtomicInteger nonNegotiationCalls = new AtomicInteger();
        final Server server = NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
                .addService(ServerInterceptors.intercept(service, new ServerInterceptor() {
                    @Override
                    public <Request, Response> ServerCall.Listener<Request> interceptCall(
                            final ServerCall<Request, Response> call, final Metadata headers,
                            final ServerCallHandler<Request, Response> next) {
                        if (!call.getMethodDescriptor().getFullMethodName().endsWith("/GetServerInfo")) {
                            nonNegotiationCalls.incrementAndGet();
                        }
                        return next.startCall(call, headers);
                    }
                })).build().start();
        final ProtocolFlow flow = new ProtocolFlow();
        try (Client client = new Client(new Registry(Collections.<Flow<?>>singletonList(flow)),
                new EmptyBlobCache(), new ClientOptions("127.0.0.1:" + server.getPort()))) {
            final ProtocolFlow rpc = client.newRpcStub(ProtocolFlow.class, "flow");
            final StepExecutionId step = StepExecutionId.of("ProtocolStep", 1);
            final java.util.List<Executable> calls = Arrays.<Executable>asList(
                    () -> client.getAttributes("flow", STATUS),
                    () -> client.getAttributes("flow", STATUS, NOTES.instance("a")),
                    () -> client.startFlow(flow, "flow", null),
                    () -> client.invokeRPC(rpc::getStatus, "input"),
                    () -> client.writeStream("flow", PROGRESS, "source", "value"),
                    () -> client.readStream("flow", PROGRESS, ""),
                    () -> client.listStreamMessages("flow", PROGRESS, 1, ""),
                    () -> client.stopFlow("flow"),
                    () -> client.waitForFlow("flow"),
                    () -> client.describeFlow("flow"),
                    () -> client.searchFlows("", 1),
                    () -> client.timeTravel("flow",
                            TimeTravelOptions.newBuilder(TimeTravelType.BEGINNING).build()),
                    () -> client.skipTimer("flow", step, TimerId.byConditionIndex(0)),
                    () -> client.waitForStepCompletion("flow", step,
                            WaitForStepCompletionOptions.newBuilder().build()),
                    () -> client.waitForAttributeMatch("flow", STATUS, AttributeMatch.equalTo("done"),
                            WaitForAttributeOptions.newBuilder().build()),
                    () -> client.waitForAttributeMatch("flow", NOTES, "a", AttributeMatch.equalTo("done"),
                            WaitForAttributeOptions.newBuilder().build()),
                    () -> client.updateFlowConfig("flow", FlowConfig.newBuilder().build()),
                    () -> client.triggerContinueAsNew("flow"),
                    client::healthCheck);
            String expectedMessage = null;
            for (Executable call : calls) {
                final IllegalStateException failure = assertThrows(IllegalStateException.class, call);
                if (expectedMessage == null) {
                    expectedMessage = failure.getMessage();
                }
                assertEquals(expectedMessage, failure.getMessage());
            }
            assertTrue(expectedMessage.contains("protocol version 4 or later"));
            assertEquals(1, service.negotiations.get());
            assertEquals(0, nonNegotiationCalls.get());
        } finally {
            server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    @Test
    void stalledNegotiationRespectsRequestTimeoutAndIsNotCached() throws Exception {
        final ProtocolService service = new ProtocolService(4, false);
        service.stall = new CountDownLatch(1);
        final Server server = startLoopbackServer(service);
        final ProtocolFlow flow = new ProtocolFlow();
        final ExecutorService callers = Executors.newFixedThreadPool(2);
        try (Client client = new Client(new Registry(Collections.<Flow<?>>singletonList(flow)),
                new EmptyBlobCache(), new ClientOptions("127.0.0.1:" + server.getPort()))) {
            final Future<Boolean> unbudgeted = callers.submit(client::healthCheck);
            assertTrue(service.stallEntered.await(5, TimeUnit.SECONDS));
            final long started = System.nanoTime();
            final Future<?> budgeted = callers.submit(() -> client.waitForStepCompletion("flow",
                    StepExecutionId.of("ProtocolStep", 1), WaitForStepCompletionOptions.newBuilder()
                            .requestTimeout(Duration.ofSeconds(1)).build()));
            final ExecutionException waiting = assertThrows(ExecutionException.class,
                    () -> budgeted.get(10, TimeUnit.SECONDS));
            assertTrue(waiting.getCause() instanceof RequestTimeoutException, waiting.getCause().toString());
            assertTrue(System.nanoTime() - started < TimeUnit.SECONDS.toNanos(5));
            service.stall.countDown();
            assertTrue(unbudgeted.get(10, TimeUnit.SECONDS));

            final ProtocolService stalled = new ProtocolService(4, false);
            stalled.stall = new CountDownLatch(1);
            final Server stalledServer = startLoopbackServer(stalled);
            try (Client stalledClient = new Client(new Registry(Collections.<Flow<?>>singletonList(flow)),
                    new EmptyBlobCache(), new ClientOptions("127.0.0.1:" + stalledServer.getPort()))) {
                final long firstCall = System.nanoTime();
                assertThrows(RequestTimeoutException.class, () -> stalledClient.waitForAttributeMatch(
                        "flow", STATUS, AttributeMatch.equalTo("done"), WaitForAttributeOptions.newBuilder()
                                .requestTimeout(Duration.ofSeconds(1)).build()));
                assertTrue(System.nanoTime() - firstCall < TimeUnit.SECONDS.toNanos(5));
                stalled.stall.countDown();
                assertTrue(stalledClient.healthCheck());
                assertEquals(2, stalled.negotiations.get());
            } finally {
                stalledServer.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
            }
        } finally {
            service.stall.countDown();
            callers.shutdownNow();
            server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    @Test
    void cancelledContextStopsWaitingForAnotherCallersNegotiation() throws Exception {
        final ProtocolService service = new ProtocolService(4, false);
        service.stall = new CountDownLatch(1);
        final Server server = startLoopbackServer(service);
        final ExecutorService callers = Executors.newFixedThreadPool(2);
        try (Client client = new Client(new Registry(Collections.<Flow<?>>emptyList()), new EmptyBlobCache(),
                new ClientOptions("127.0.0.1:" + server.getPort()))) {
            final Future<Boolean> stalled = callers.submit(client::healthCheck);
            assertTrue(service.stallEntered.await(5, TimeUnit.SECONDS));
            final io.grpc.Context.CancellableContext callerContext = io.grpc.Context.current().withCancellation();
            final CountDownLatch waiting = new CountDownLatch(1);
            final Future<Boolean> cancelled = callers.submit(() -> callerContext.call(() -> {
                waiting.countDown();
                return client.healthCheck();
            }));
            assertTrue(waiting.await(5, TimeUnit.SECONDS));
            final long cancelledAt = System.nanoTime();
            callerContext.cancel(null);
            final ExecutionException failure = assertThrows(ExecutionException.class,
                    () -> cancelled.get(5, TimeUnit.SECONDS));
            assertTrue(System.nanoTime() - cancelledAt < TimeUnit.SECONDS.toNanos(2));
            assertTrue(failure.getCause() instanceof DexServiceException, failure.getCause().toString());
            assertEquals(Status.Code.CANCELLED, ((DexServiceException) failure.getCause()).getCode());
            service.stall.countDown();
            assertTrue(stalled.get(10, TimeUnit.SECONDS));
            assertTrue(client.healthCheck());
            assertEquals(1, service.negotiations.get());
        } finally {
            service.stall.countDown();
            callers.shutdownNow();
            server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    @Test
    void getAttributesNegotiationFailureKeepsItsCallContextAndIsRetried() throws Exception {
        final ProtocolService service = new ProtocolService(4, true);
        final Server server = startLoopbackServer(service);
        try (Client client = new Client(new Registry(Collections.<Flow<?>>singletonList(new ProtocolFlow())),
                new EmptyBlobCache(), new ClientOptions("127.0.0.1:" + server.getPort()))) {
            final DexServiceException failure = assertThrows(DexServiceException.class,
                    () -> client.getAttributes("flow", STATUS));
            assertEquals(Status.Code.UNAVAILABLE, failure.getCode());
            assertTrue(failure.getMessage().contains("getAttributes Flow flow Attributes status"),
                    failure.getMessage());
            assertEquals(1, service.negotiations.get());
            client.healthCheck();
            assertEquals(2, service.negotiations.get());
        } finally {
            server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    @Test
    void unreachableServerKeepsTheServiceErrorType() throws Exception {
        try (Client client = new Client(new Registry(Collections.<Flow<?>>emptyList()), new EmptyBlobCache(),
                new ClientOptions("127.0.0.1:1"))) {
            final DexServiceException failure = assertThrows(DexServiceException.class,
                    () -> client.stopFlow("flow"));
            assertEquals(Status.Code.UNAVAILABLE, failure.getCode());
            assertFalse(failure.getMessage().contains("incompatible"));
        }
    }

    @Test
    void reportsNewerAndInvalidServerProtocolsWithoutClaimingAnOlderServer() {
        final IllegalStateException newer = assertThrows(IllegalStateException.class,
                () -> ServerProtocolCompatibility.negotiate(ServerInfo.newBuilder().setServerVersion("next")
                        .setMinimumSupportedProtocolVersion(5).setCurrentProtocolVersion(6).build(), "test"));
        assertTrue(newer.getMessage().contains("Upgrade the Java SDK"), newer.getMessage());
        assertFalse(newer.getMessage().contains("Upgrade the Server"), newer.getMessage());
        for (ServerInfo invalid : Arrays.asList(ServerInfo.getDefaultInstance(),
                ServerInfo.newBuilder().setMinimumSupportedProtocolVersion(5).setCurrentProtocolVersion(4).build())) {
            final IllegalStateException failure = assertThrows(IllegalStateException.class,
                    () -> ServerProtocolCompatibility.negotiate(invalid, "test"));
            assertTrue(failure.getMessage().contains("invalid protocol range"), failure.getMessage());
            assertFalse(failure instanceof ServerProtocolCompatibility.IncompatibleServerProtocolException);
        }
        final IllegalStateException older = assertThrows(IllegalStateException.class,
                () -> ServerProtocolCompatibility.negotiate(ServerInfo.newBuilder().setServerVersion("old")
                        .setMinimumSupportedProtocolVersion(1).setCurrentProtocolVersion(3).build(), "test"));
        assertTrue(older.getMessage().contains("protocol version "
                + ServerProtocolCompatibility.MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION + " or later"));
        assertTrue(older.getMessage().contains("Upgrade the Server"));
        assertEquals(4, ServerProtocolCompatibility.negotiate(ServerInfo.newBuilder()
                .setMinimumSupportedProtocolVersion(1).setCurrentProtocolVersion(9).build(), "test"));
    }

    @Test
    void mapsRecoveryAttemptProgressIncludingOlderAndUnknownValues() {
        for (int progress = 0; progress < 4; progress++) {
            final RecoveryErrorInfo error = RecoveryErrorInfo.fromProto(
                    io.superdurable.gen.RecoveryErrorInfo.newBuilder().setDetail("detail")
                            .setErrorType("type").setAttemptProgressValue(progress).build());
            assertEquals("detail", error.getDetail());
            assertEquals("type", error.getErrorType());
            assertEquals(progress == 1 ? StepAttemptProgress.NOT_STARTED
                    : progress == 2 ? StepAttemptProgress.NOT_COMPLETED : StepAttemptProgress.UNSPECIFIED,
                    error.getAttemptProgress());
        }
    }

    private static Server startLoopbackServer(final FlowServiceGrpc.FlowServiceImplBase service)
            throws java.io.IOException {
        return NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0)).addService(service).build().start();
    }

    private static final Attribute<String> STATUS = Attribute.define("status", String.class);
    private static final AttributeMap<String> NOTES = AttributeMap.define("notes", String.class);
    private static final Stream<String> PROGRESS = Stream.define("progress", String.class, 1024);

    public static class ProtocolFlow implements Flow<String> {
        @Override
        public String getFlowType() {
            return "ProtocolFlow";
        }

        @Override
        public StepList<String> getSteps() {
            return StepList.empty();
        }

        @Override
        public PersistenceSchema getPersistenceSchema() {
            return PersistenceSchema.of(STATUS, NOTES, PROGRESS);
        }

        @RPC
        public RPCResult<String> getStatus(final Context context, final String input) {
            return RPCResult.of(input);
        }
    }

    private static final class ProtocolService extends FlowServiceGrpc.FlowServiceImplBase {
        private final int protocol;
        private final boolean shouldFailFirst;
        private final AtomicInteger negotiations = new AtomicInteger();
        private final AtomicInteger healthChecks = new AtomicInteger();
        private final CountDownLatch stallEntered = new CountDownLatch(1);
        private volatile CountDownLatch stall;

        private ProtocolService(final int protocol, final boolean shouldFailFirst) {
            this.protocol = protocol;
            this.shouldFailFirst = shouldFailFirst;
        }

        @Override
        public void getServerInfo(final Empty request, final StreamObserver<ServerInfo> observer) {
            final int negotiation = negotiations.incrementAndGet();
            if (negotiation == 1 && shouldFailFirst) {
                observer.onError(Status.UNAVAILABLE.asRuntimeException());
                return;
            }
            final CountDownLatch currentStall = stall;
            if (negotiation == 1 && currentStall != null) {
                stallEntered.countDown();
                try {
                    currentStall.await(30, TimeUnit.SECONDS);
                } catch (InterruptedException interrupted) {
                    Thread.currentThread().interrupt();
                    return;
                }
            }
            observer.onNext(ServerInfo.newBuilder().setServerVersion("old-server")
                    .setMinimumSupportedProtocolVersion(1).setCurrentProtocolVersion(protocol).build());
            observer.onCompleted();
        }

        @Override
        public void healthCheck(final Empty request, final StreamObserver<HealthInfo> observer) {
            healthChecks.incrementAndGet();
            observer.onNext(HealthInfo.getDefaultInstance());
            observer.onCompleted();
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
