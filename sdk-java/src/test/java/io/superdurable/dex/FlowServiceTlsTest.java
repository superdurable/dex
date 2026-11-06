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
import io.grpc.ManagedChannel;
import io.grpc.Server;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.StreamObserver;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.ServerInfo;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.OutputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyStore;
import java.security.cert.X509Certificate;
import java.util.Arrays;
import java.util.Collections;
import java.util.HashSet;
import java.util.Set;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.*;

final class FlowServiceTlsTest {
    @TempDir
    Path directory;

    @Test
    void acceptsSupportedAddressesAndKeepsPlaintextTargets() throws Exception {
        for (final String address : Arrays.asList("localhost:443", "dex.example.com:1", "127.0.0.1:65535",
                "[::1]:443", "dns:///localhost:443", "dns://127.0.0.1:53/localhost:443")) {
            assertEquals(address, FlowServiceTlsTransport.validateTarget(address));
        }
        assertEquals("dns:///localhost:443", FlowServiceTlsTransport.validateTarget("DNS:///localhost:443"));
        final ManagedChannel plaintext = FlowServiceTlsTransport.channelBuilder("dns:///localhost:8801", null).build();
        try {
            assertEquals("localhost:8801", plaintext.authority());
        } finally {
            closeChannel(plaintext);
        }
    }

    @Test
    void rejectsMalformedAddressesAtClientAndWorkerConstruction() {
        final Registry registry = new Registry(Collections.<Flow<?>>emptyList());
        final FlowServiceTlsOptions tls = FlowServiceTlsOptions.newBuilder().build();
        final BlobCache cache = new ValidationBlobCache();
        for (final String address : Arrays.asList("unix:8443", "Unix:8443", "passthrough:443", "dns:443",
                "unix:///tmp/dex.sock", "dns:///localhost/path:443", "https://localhost:443", "localhost",
                "::1:443", "[::1]", "local host:443", "dns:/localhost:443", "dns://bad authority/localhost:443",
                "dns://bad\nauthority/localhost:443", "user@localhost:443", "[localhost]:443", "[127.0.0.1]:443", "dns:///[localhost]:443",
                "localhost:0", "localhost:65536", "localhost:https", "localhost:", "localhost:+443",
                "localhost:-1", "[::1]:+443", "localhost:999999999999999999999", "localhost:443?query")) {
            assertTrue(assertThrows(IllegalArgumentException.class, () -> new Client(registry, cache,
                    ClientOptions.newBuilder().serverAddress(address).flowServiceTls(tls).build()))
                    .getMessage().contains("flowServiceTls requires serverAddress"), address);
            assertTrue(assertThrows(IllegalArgumentException.class, () -> new Worker(registry, cache,
                    WorkerOptions.newBuilder().serverAddress(address).flowServiceTls(tls).build()))
                    .getMessage().contains("flowServiceTls requires serverAddress"), address);
        }
    }

    @Test
    void validatesPemAndServerNameWithoutDisclosingCertificates() throws Exception {
        for (final String serverName : Arrays.asList("", "localhost:443", "dex.example.com/path", "bad name",
                "user@dex.example.com", "[::1]", "fe80::1%en0")) {
            assertTrue(assertThrows(IllegalArgumentException.class,
                    () -> FlowServiceTlsOptions.newBuilder().serverName(serverName).build())
                    .getMessage().contains("flowServiceTls.serverName"));
        }
        for (final String serverName : Arrays.asList("localhost", "127.0.0.1", "::1")) {
            assertEquals(serverName, FlowServiceTlsOptions.newBuilder().serverName(serverName).build().getServerName());
        }
        for (final String pem : Arrays.asList("invalid-private-data", "-----BEGIN CERTIFICATE-----invalid-----END CERTIFICATE-----")) {
            final IllegalArgumentException failure = assertThrows(IllegalArgumentException.class,
                    () -> FlowServiceTlsOptions.newBuilder().additionalRootCAsPem(pem).build());
            assertEquals("flowServiceTls.additionalRootCAsPem contains no PEM certificate", failure.getMessage());
            assertNull(failure.getCause());
        }
        final TlsTestCertificates certificates = TlsTestCertificates.create(directory, "parse");
        assertDoesNotThrow(() -> FlowServiceTlsOptions.newBuilder().additionalRootCAsPem("").build());
        assertDoesNotThrow(() -> FlowServiceTlsOptions.newBuilder().additionalRootCAsPem(
                "-----BEGIN CERTIFICATE-----invalid-----END CERTIFICATE-----" + certificates.caPem).build());
        final Set<X509Certificate> defaults = new HashSet<X509Certificate>(Arrays.asList(
                FlowServiceTlsTransport.trustManager(null).getAcceptedIssuers()));
        assertFalse(defaults.isEmpty());
        final Set<X509Certificate> combined = new HashSet<X509Certificate>(Arrays.asList(
                FlowServiceTlsTransport.trustManager(certificates.caPem).getAcceptedIssuers()));
        assertTrue(combined.containsAll(defaults));
        assertTrue(combined.containsAll(FlowServiceTlsTransport.parseAdditionalRootCertificates(certificates.caPem)));
    }

    @Test
    void retainsConfiguredDefaultTrustStoreWhenAddingAnotherCa() throws Exception {
        final TlsTestCertificates defaults = TlsTestCertificates.create(directory, "default");
        final TlsTestCertificates additional = TlsTestCertificates.create(directory, "additional");
        final Path trustStore = directory.resolve("default.jks");
        final KeyStore store = KeyStore.getInstance("JKS");
        store.load(null, null);
        store.setCertificateEntry("default", FlowServiceTlsTransport.parseAdditionalRootCertificates(defaults.caPem).get(0));
        try (OutputStream output = Files.newOutputStream(trustStore)) {
            store.store(output, "changeit".toCharArray());
        }
        final String oldStore = System.getProperty("javax.net.ssl.trustStore");
        final String oldType = System.getProperty("javax.net.ssl.trustStoreType");
        final String oldPassword = System.getProperty("javax.net.ssl.trustStorePassword");
        try {
            System.setProperty("javax.net.ssl.trustStore", trustStore.toString());
            System.setProperty("javax.net.ssl.trustStoreType", "JKS");
            System.setProperty("javax.net.ssl.trustStorePassword", "changeit");
            final Server server = startPlaintextServer();
            try (TlsTestProxy defaultProxy = new TlsTestProxy(defaults, "127.0.0.1", server.getPort(), "h2");
                    TlsTestProxy additionalProxy = new TlsTestProxy(additional, "127.0.0.1", server.getPort(), "h2")) {
                final FlowServiceTlsOptions tls = FlowServiceTlsOptions.newBuilder()
                        .additionalRootCAsPem(additional.caPem).build();
                assertEquals("tls-test", serverInfo("localhost:" + defaultProxy.port(), tls).getServerVersion());
                assertEquals("tls-test", serverInfo("localhost:" + additionalProxy.port(), tls).getServerVersion());
                assertUnavailable("localhost:" + additionalProxy.port(), FlowServiceTlsOptions.newBuilder().build());
            } finally {
                server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
            }
        } finally {
            restoreProperty("javax.net.ssl.trustStore", oldStore);
            restoreProperty("javax.net.ssl.trustStoreType", oldType);
            restoreProperty("javax.net.ssl.trustStorePassword", oldPassword);
        }
    }

    @Test
    void verifiesNamesAuthorityDnsTargetsAndAlpn() throws Exception {
        final TlsTestCertificates certificates = TlsTestCertificates.create(directory, "transport");
        final FlowServiceTlsOptions tls = FlowServiceTlsOptions.newBuilder()
                .additionalRootCAsPem(certificates.caPem).build();
        final Server server = startPlaintextServer();
        try (TlsTestProxy proxy = new TlsTestProxy(certificates, "127.0.0.1", server.getPort(), "h2");
                TlsTestProxy httpOne = new TlsTestProxy(certificates, "127.0.0.1", server.getPort(), "http/1.1");
                TlsTestProxy noAlpn = new TlsTestProxy(certificates, "127.0.0.1", server.getPort())) {
            for (final String address : Arrays.asList("localhost:" + proxy.port(), "dns:///localhost:" + proxy.port(),
                    "DNS:///localhost:" + proxy.port(), "dns://127.0.0.1:53/127.0.0.1:" + proxy.port())) {
                assertEquals("tls-test", serverInfo(address, tls).getServerVersion());
            }
            final FlowServiceTlsOptions override = FlowServiceTlsOptions.newBuilder()
                    .additionalRootCAsPem(certificates.caPem).serverName("dex.test").build();
            final ManagedChannel channel = FlowServiceTlsTransport.channelBuilder("127.0.0.1:" + proxy.port(), override).build();
            try {
                assertEquals("dex.test", channel.authority());
                assertEquals("tls-test", FlowServiceGrpc.newBlockingStub(channel).withDeadlineAfter(5, TimeUnit.SECONDS)
                        .getServerInfo(Empty.getDefaultInstance()).getServerVersion());
            } finally {
                closeChannel(channel);
            }
            assertTrue(proxy.hasHandshake("dex.test", "h2"), proxy.handshakeDescriptions());
            assertUnavailable("localhost:" + proxy.port(), FlowServiceTlsOptions.newBuilder().build());
            assertUnavailable("localhost:" + proxy.port(), FlowServiceTlsOptions.newBuilder()
                    .additionalRootCAsPem(certificates.caPem).serverName("wrong.example").build());
            assertUnavailable("localhost:" + httpOne.port(), tls);
            assertUnavailable("localhost:" + noAlpn.port(), tls);
            assertEquals("tls-test", serverInfo("127.0.0.1:" + server.getPort(), null).getServerVersion());
        } finally {
            server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    private static final class ValidationBlobCache implements BlobCache {
        @Override
        public java.util.Optional<byte[]> get(final String blobId) {
            throw new AssertionError("invalid TLS addresses must fail before cache access");
        }

        @Override
        public boolean put(final String blobId, final byte[] payload) {
            throw new AssertionError("invalid TLS addresses must fail before cache access");
        }

        @Override
        public void delete(final String blobId) {
            throw new AssertionError("invalid TLS addresses must fail before cache access");
        }

        @Override
        public void deleteAll() {
            throw new AssertionError("invalid TLS addresses must fail before cache access");
        }

        @Override
        public void close() {
        }
    }

    private static Server startPlaintextServer() throws Exception {
        return NettyServerBuilder.forAddress(new java.net.InetSocketAddress("127.0.0.1", 0))
                .addService(new TestFlowService()).build().start();
    }

    private static final class TestFlowService extends FlowServiceGrpc.FlowServiceImplBase {
        @Override
        public void getServerInfo(final Empty request, final StreamObserver<ServerInfo> response) {
            response.onNext(ServerInfo.newBuilder().setServerVersion("tls-test").build());
            response.onCompleted();
        }
    }

    private static ServerInfo serverInfo(final String address, final FlowServiceTlsOptions tls) throws Exception {
        final ManagedChannel channel = FlowServiceTlsTransport.channelBuilder(address, tls).build();
        try {
            return FlowServiceGrpc.newBlockingStub(channel).withDeadlineAfter(5, TimeUnit.SECONDS)
                    .getServerInfo(Empty.getDefaultInstance());
        } finally {
            closeChannel(channel);
        }
    }

    private static void assertUnavailable(final String address, final FlowServiceTlsOptions tls) {
        assertEquals(Status.Code.UNAVAILABLE,
                assertThrows(StatusRuntimeException.class, () -> serverInfo(address, tls)).getStatus().getCode());
    }

    private static void closeChannel(final ManagedChannel channel) throws InterruptedException {
        channel.shutdownNow();
        assertTrue(channel.awaitTermination(5, TimeUnit.SECONDS));
    }

    private static void restoreProperty(final String name, final String value) {
        if (value == null) {
            System.clearProperty(name);
        } else {
            System.setProperty(name, value);
        }
    }
}
