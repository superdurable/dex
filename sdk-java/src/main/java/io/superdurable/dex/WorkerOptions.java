/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Sustainable Use License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex;

import com.fasterxml.jackson.databind.ObjectMapper;

import java.time.Duration;

/**
 * Configures a Java Worker's outbound polling, connection, and serialization.
 *
 * <p>The Worker connects to {@code localhost:8801} by default and receives tasks over outbound
 * streams. It needs only an outbound connection to the Dex Server. Pull settings use
 * {@link PullWorkerOptions} defaults unless explicitly configured. The default Jackson mapper
 * discovers installed modules.
 *
 * <pre>{@code
 * WorkerOptions options = WorkerOptions.newBuilder()
 *         .serverAddress("dex:8801")
 *         .objectMapper(applicationObjectMapper)
 *         .grpcErrorStatusMapping(GrpcErrorStatusMapping.newBuilder()
 *                 .map(PaymentDeclinedException.class, Status.Code.FAILED_PRECONDITION)
 *                 .build())
 *         .build();
 * }</pre>
 */
public final class WorkerOptions {
    private final String serverAddress;
    private final ObjectMapper objectMapper;
    private final GrpcErrorStatusMapping grpcErrorStatusMapping;
    private final Duration attributeIndexSyncTimeout;
    private final PullWorkerOptions pull;
    private final Credential credential;

    private WorkerOptions(final Builder builder) {
        this.serverAddress = builder.serverAddress;
        this.objectMapper = builder.objectMapper;
        this.grpcErrorStatusMapping = builder.grpcErrorStatusMapping;
        this.attributeIndexSyncTimeout = builder.attributeIndexSyncTimeout;
        this.pull = builder.pull;
        this.credential = builder.credential;
    }

    /**
     * Creates a builder initialized with local development defaults.
     *
     * @return a new mutable builder
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    String getServerAddress() {
        return serverAddress;
    }

    ObjectMapper getObjectMapper() {
        return objectMapper;
    }

    GrpcErrorStatusMapping getGrpcErrorStatusMapping() {
        return grpcErrorStatusMapping;
    }

    Duration getAttributeIndexSyncTimeout() {
        return attributeIndexSyncTimeout;
    }

    PullWorkerOptions getPull() {
        return pull;
    }

    Credential getCredential() {
        return credential;
    }

    /** Builds immutable {@link WorkerOptions} values. */
    public static final class Builder {
        private String serverAddress = "localhost:8801";
        private ObjectMapper objectMapper = new ObjectMapper().findAndRegisterModules();
        private GrpcErrorStatusMapping grpcErrorStatusMapping =
                GrpcErrorStatusMapping.newBuilder().build();
        private Duration attributeIndexSyncTimeout = Duration.ofMinutes(2);
        private PullWorkerOptions pull = PullWorkerOptions.newBuilder().build();
        private Credential credential;

        private Builder() {
        }

        /**
         * Configures outbound task polling capacity and lifecycle.
         *
         * <p>Omitting this setting uses {@link PullWorkerOptions} defaults.
         *
         * <pre>{@code
         * WorkerOptions options = WorkerOptions.newBuilder()
         *         .serverAddress("dex:8801")
         *         .pull(PullWorkerOptions.newBuilder().maxConcurrentTasks(8).build())
         *         .build();
         * }</pre>
         *
         * @param value the nonnull pull settings
         * @return this builder
         * @throws IllegalArgumentException if value is null
         */
        public Builder pull(final PullWorkerOptions value) {
            if (value == null) {
                throw new IllegalArgumentException("pull options are required");
            }
            this.pull = value;
            return this;
        }

        /**
         * Sets the Dex Server gRPC target used for outbound task streams and API calls.
         *
         * @param value the server target; the default is {@code localhost:8801}
         * @return this builder
         */
        public Builder serverAddress(final String value) {
            this.serverAddress = value;
            return this;
        }

        /**
         * Sets the token source for Worker FlowService calls and pull stream opens.
         *
         * <p>The source can be called concurrently and must not block. A failure ends the call
         * locally; Worker startup throws {@link IllegalStateException} on authentication failure,
         * and pull streams retry opening after a backoff. The default {@code null} sends no
         * authorization metadata.
         *
         * <pre>{@code
         * WorkerOptions options = WorkerOptions.newBuilder()
         *         .serverAddress("dex:8801")
         *         .pull(PullWorkerOptions.newBuilder().build())
         *         .credential(new FileCredential("/var/run/secrets/dex/token"))
         *         .build();
         * }</pre>
         *
         * @param value the credential, or {@code null} to send no token
         * @return this builder
         */
        public Builder credential(final Credential value) {
            this.credential = value;
            return this;
        }

        /**
         * Sets the Jackson mapper used for application values.
         *
         * @param value the nonnull application mapper
         * @return this builder
         * @throws IllegalArgumentException if {@code value} is {@code null}
         */
        public Builder objectMapper(final ObjectMapper value) {
            if (value == null) {
                throw new IllegalArgumentException("objectMapper is required");
            }
            this.objectMapper = value;
            return this;
        }

        /**
         * Sets how Worker application exceptions map to gRPC status codes.
         *
         * <p>The default mapping reports every Step-method and RPC exception as
         * {@link io.grpc.Status.Code#INTERNAL}. Use a custom mapping only when an exception class has
         * a stable operational meaning for the owner of the Worker application. The selected status
         * is stored for diagnostics and does not replace Step retry or failure-policy configuration.
         *
         * @param value the nonnull immutable exception status mapping
         * @return this builder
         * @throws IllegalArgumentException if {@code value} is {@code null}
         */
        public Builder grpcErrorStatusMapping(final GrpcErrorStatusMapping value) {
            if (value == null) {
                throw new IllegalArgumentException("grpcErrorStatusMapping is required");
            }
            this.grpcErrorStatusMapping = value;
            return this;
        }

        /**
         * Sets the maximum duration for startup index synchronization.
         *
         * @param value a positive duration; the default is two minutes
         * @return this builder
         * @throws IllegalArgumentException if the duration is null, zero, or negative
         */
        public Builder attributeIndexSyncTimeout(final Duration value) {
            if (value == null || value.isZero() || value.isNegative()) {
                throw new IllegalArgumentException("attributeIndexSyncTimeout must be positive");
            }
            this.attributeIndexSyncTimeout = value;
            return this;
        }

        /**
         * Builds immutable worker options from the current values.
         *
         * @return the configured worker options
         */
        public WorkerOptions build() {
            return new WorkerOptions(this);
        }
    }
}
