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

/**
 * Enables verified TLS for a Client or Worker's outbound FlowService connection.
 *
 * <p>Set this option independently on {@link ClientOptions.Builder#flowServiceTls} and
 * {@link WorkerOptions.Builder#flowServiceTls}. Omitting it keeps plaintext gRPC. An empty
 * builder enables TLS 1.2 or newer with the JDK default trust store. Additional PEM root CAs
 * add to that store. Certificates are read at construction and are not watched for changes.
 * The Worker uses TLS for startup, blob hydration, and pull task streams. Bearer credentials
 * remain call metadata. These settings do not configure the API-key token endpoint.
 *
 * <p>With TLS, the server address must be {@code host:port}, {@code dns:///host:port}, or
 * {@code dns://authority/host:port}, with port 1 through 65535. The endpoint host determines
 * certificate verification and SNI unless serverName overrides it. The DNS resolver authority
 * does not determine certificate identity. Without an override, HTTP/2 authority remains the
 * endpoint's host and port. IP addresses require a matching IP subject alternative name.
 * Netty omits SNI for IP addresses and single-label DNS names such as localhost.
 * The endpoint must negotiate ALPN {@code h2}; HTTP/1.1 and absent ALPN are rejected.
 *
 * <p>Invalid settings throw {@link IllegalArgumentException} during options or Client/Worker
 * construction. Connections open lazily. Certificate, hostname, and handshake failures surface
 * as {@code UNAVAILABLE} on a Client call or as a Worker startup failure.
 *
 * <pre>{@code
 * String rootPem = new String(java.nio.file.Files.readAllBytes(
 *         java.nio.file.Paths.get("/etc/dex/root-ca.pem")),
 *         java.nio.charset.StandardCharsets.US_ASCII);
 * FlowServiceTlsOptions tls = FlowServiceTlsOptions.newBuilder()
 *         .additionalRootCAsPem(rootPem)
 *         .build();
 * ClientOptions clientOptions = ClientOptions.newBuilder()
 *         .serverAddress("dex.example.com:443").flowServiceTls(tls).build();
 * WorkerOptions workerOptions = WorkerOptions.newBuilder()
 *         .serverAddress("dex.example.com:443").flowServiceTls(tls).build();
 * try (Client client = new Client(registry, blobCache, clientOptions);
 *         Worker worker = new Worker(registry, blobCache, workerOptions)) {
 *     Thread workerThread = new Thread(worker::start);
 *     workerThread.start();
 *     try {
 *         worker.waitPullRegistered(java.time.Duration.ofSeconds(30));
 *         client.searchFlows("", 10);
 *     } finally {
 *         worker.stop();
 *         workerThread.join();
 *     }
 * }
 * }</pre>
 */
public final class FlowServiceTlsOptions {
    private final String additionalRootCAsPem;
    private final String serverName;

    private FlowServiceTlsOptions(final Builder builder) {
        FlowServiceTlsTransport.parseAdditionalRootCertificates(builder.additionalRootCAsPem);
        if (builder.serverName != null && !FlowServiceTlsTransport.isValidServerName(builder.serverName)) {
            throw new IllegalArgumentException("flowServiceTls.serverName must be a nonempty DNS name or IP address without a port");
        }
        this.additionalRootCAsPem = builder.additionalRootCAsPem;
        this.serverName = builder.serverName;
    }

    /**
     * Creates TLS settings using the JDK default roots and the address host.
     *
     * <p>See this class's example for configuring both a Client and Worker.
     *
     * @return a new mutable builder with both optional values unset
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    String getAdditionalRootCAsPem() {
        return additionalRootCAsPem;
    }

    String getServerName() {
        return serverName;
    }

    /** Builds immutable TLS settings; see {@link FlowServiceTlsOptions} for a complete example. */
    public static final class Builder {
        private String additionalRootCAsPem;
        private String serverName;

        private Builder() {
        }

        /**
         * Adds PEM-encoded root certificates to the JDK default trust roots.
         *
         * <p>The default null or an empty string uses only the default roots. A nonempty bundle
         * must contain at least one parsable X.509 certificate. Other text and malformed certificate
         * blocks are ignored when another certificate parses. No certificate contents are logged.
         * See {@link FlowServiceTlsOptions} for a complete example.
         *
         * @param value the PEM bundle, or null to use only default roots
         * @return this builder
         */
        public Builder additionalRootCAsPem(final String value) {
            this.additionalRootCAsPem = value;
            return this;
        }

        /**
         * Overrides the certificate name, TLS SNI, and HTTP/2 authority.
         *
         * <p>The default null uses the address host for verification and SNI, and the address host
         * and port for HTTP/2 authority. Supply a DNS name or unbracketed IP address without a port
         * only when it differs from the endpoint host. An IP name requires a matching IP SAN.
         * A gateway must accept the override as its authority, without a port. Prefer a DNS override
         * for IPv6 endpoints because HTTP/2 authorities require IPv6 brackets.
         * See {@link FlowServiceTlsOptions} for an example of configuring TLS.
         *
         * @param value the certificate name, or null to use the address host; empty is invalid
         * @return this builder
         */
        public Builder serverName(final String value) {
            this.serverName = value;
            return this;
        }

        /**
         * Builds immutable TLS settings without opening a connection.
         *
         * @return the configured TLS settings
         * @throws IllegalArgumentException if a nonempty PEM bundle contains no parsable certificate,
         *     or the supplied server name is empty or invalid
         */
        public FlowServiceTlsOptions build() {
            return new FlowServiceTlsOptions(this);
        }
    }
}
