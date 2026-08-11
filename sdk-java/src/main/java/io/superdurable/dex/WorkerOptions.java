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

import com.fasterxml.jackson.databind.ObjectMapper;

/**
 * Configures a Java worker's listener, advertised target, server connection, and serialization.
 *
 * <p>The worker listens on {@code :8803} and connects to {@code localhost:8801} by default. The
 * bind address controls the local gRPC listener; the worker target is the address advertised for
 * server routing and may differ behind a proxy or service discovery layer. The default Jackson
 * mapper discovers installed modules.
 *
 * <pre>{@code
 * WorkerOptions options = WorkerOptions.newBuilder()
 *         .bindAddress("0.0.0.0:8803")
 *         .workerTarget(new WorkerTarget("orders-worker:8803", false))
 *         .serverAddress("dex:8801")
 *         .objectMapper(applicationObjectMapper)
 *         .grpcErrorStatusMapping(GrpcErrorStatusMapping.newBuilder()
 *                 .map(PaymentDeclinedException.class, Status.Code.FAILED_PRECONDITION)
 *                 .build())
 *         .build();
 * }</pre>
 */
public final class WorkerOptions {
    private final String bindAddress;
    private final WorkerTarget workerTarget;
    private final String serverAddress;
    private final ObjectMapper objectMapper;
    private final GrpcErrorStatusMapping grpcErrorStatusMapping;

    private WorkerOptions(final Builder builder) {
        this.bindAddress = builder.bindAddress;
        this.workerTarget = builder.workerTarget;
        this.serverAddress = builder.serverAddress;
        this.objectMapper = builder.objectMapper;
        this.grpcErrorStatusMapping = builder.grpcErrorStatusMapping;
    }

    /**
     * Creates a builder initialized with local development defaults.
     *
     * @return a new mutable builder
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    String getBindAddress() {
        return bindAddress;
    }

    WorkerTarget getWorkerTarget() {
        return workerTarget;
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

    /** Builds immutable {@link WorkerOptions} values. */
    public static final class Builder {
        private String bindAddress = ":8803";
        private WorkerTarget workerTarget;
        private String serverAddress = "localhost:8801";
        private ObjectMapper objectMapper = new ObjectMapper().findAndRegisterModules();
        private GrpcErrorStatusMapping grpcErrorStatusMapping =
                GrpcErrorStatusMapping.newBuilder().build();

        private Builder() {
        }

        /**
         * Sets the address on which the worker gRPC service listens.
         *
         * @param value the bind address; the default is {@code :8803}
         * @return this builder
         */
        public Builder bindAddress(final String value) {
            this.bindAddress = value;
            return this;
        }

        /**
         * Sets the worker address advertised for Flow routing.
         *
         * @param value the advertised target, or {@code null} for no explicit target
         * @return this builder
         */
        public Builder workerTarget(final WorkerTarget value) {
            this.workerTarget = value;
            return this;
        }

        /**
         * Sets the Dex server gRPC target used for worker callbacks.
         *
         * @param value the server target; the default is {@code localhost:8801}
         * @return this builder
         */
        public Builder serverAddress(final String value) {
            this.serverAddress = value;
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
         * Builds immutable worker options from the current values.
         *
         * @return the configured worker options
         */
        public WorkerOptions build() {
            return new WorkerOptions(this);
        }
    }
}
