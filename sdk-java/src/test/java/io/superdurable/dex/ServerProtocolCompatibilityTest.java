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
import io.grpc.ServerBuilder;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.HealthInfo;
import io.superdurable.gen.ServerInfo;
import org.junit.jupiter.api.Test;

import java.util.Collections;
import java.util.Optional;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class ServerProtocolCompatibilityTest {
    @Test
    void clientIsLazyAndCachesProtocolThreeFailureForEveryMethod() throws Exception {
        final ProtocolService service = new ProtocolService(3, false);
        final Server server = ServerBuilder.forPort(0).addService(service).build().start();
        try (Client client = new Client(new Registry(Collections.<Flow<?>>emptyList()), new EmptyBlobCache(),
                new ClientOptions("127.0.0.1:" + server.getPort()))) {
            assertEquals(0, service.negotiations.get());
            final IllegalStateException failure = assertThrows(IllegalStateException.class,
                    () -> client.stopFlow("flow"));
            assertTrue(failure.getMessage().contains("old-server"));
            assertTrue(failure.getMessage().contains("protocol [1,3]"));
            assertTrue(failure.getMessage().contains("requires server-zd"));
            assertSame(failure, assertThrows(IllegalStateException.class, client::healthCheck));
            assertEquals(1, service.negotiations.get());
            assertEquals(0, service.healthChecks.get());
        } finally {
            server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    @Test
    void transientNegotiationFailureIsRetriedAndSuccessIsCached() throws Exception {
        final ProtocolService service = new ProtocolService(4, true);
        final Server server = ServerBuilder.forPort(0).addService(service).build().start();
        try (Client client = new Client(new Registry(Collections.<Flow<?>>emptyList()), new EmptyBlobCache(),
                new ClientOptions("127.0.0.1:" + server.getPort()))) {
            assertThrows(IllegalStateException.class, client::healthCheck);
            assertTrue(client.healthCheck());
            assertTrue(client.healthCheck());
            assertEquals(2, service.negotiations.get());
            assertEquals(2, service.healthChecks.get());
        } finally {
            server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
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

    private static final class ProtocolService extends FlowServiceGrpc.FlowServiceImplBase {
        private final int protocol;
        private final boolean shouldFailFirst;
        private final AtomicInteger negotiations = new AtomicInteger();
        private final AtomicInteger healthChecks = new AtomicInteger();

        private ProtocolService(final int protocol, final boolean shouldFailFirst) {
            this.protocol = protocol;
            this.shouldFailFirst = shouldFailFirst;
        }

        @Override
        public void getServerInfo(final Empty request, final StreamObserver<ServerInfo> observer) {
            if (negotiations.incrementAndGet() == 1 && shouldFailFirst) {
                observer.onError(Status.UNAVAILABLE.asRuntimeException());
                return;
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
