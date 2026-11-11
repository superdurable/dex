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

import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

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
    private final FlowServiceTlsOptions flowServiceTls;
    private final List<Path> flowDefinitionPaths;
    private final List<FlowDefinitionDocument> flowDefinitions;

    private WorkerOptions(final Builder builder) {
        this.serverAddress = builder.serverAddress;
        this.objectMapper = builder.objectMapper;
        this.grpcErrorStatusMapping = builder.grpcErrorStatusMapping;
        this.attributeIndexSyncTimeout = builder.attributeIndexSyncTimeout;
        this.pull = builder.pull;
        this.credential = builder.credential;
        this.flowServiceTls = builder.flowServiceTls;
        this.flowDefinitionPaths = builder.flowDefinitionPaths;
        this.flowDefinitions = builder.flowDefinitions;
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

    FlowServiceTlsOptions getFlowServiceTls() {
        return flowServiceTls;
    }

    List<Path> getFlowDefinitionPaths() {
        return flowDefinitionPaths;
    }

    List<FlowDefinitionDocument> getFlowDefinitions() {
        return flowDefinitions;
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
        private FlowServiceTlsOptions flowServiceTls;
        private List<Path> flowDefinitionPaths = Collections.emptyList();
        private List<FlowDefinitionDocument> flowDefinitions = Collections.emptyList();

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
         * Enables verified TLS for the outbound FlowService connection.
         *
         * <p>The default null keeps plaintext. An empty TLS builder uses JDK default trust roots.
         * See {@link FlowServiceTlsOptions} for supported addresses, additional roots, server name
         * overrides, connection errors, and a complete Client and Worker example.
         *
         * @param value the immutable TLS settings, or null for plaintext
         * @return this builder
         */
        public Builder flowServiceTls(final FlowServiceTlsOptions value) {
            this.flowServiceTls = value;
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
         * Sets the Flow Definition Graph files that the Worker uploads to the Dex Server at start.
         *
         * <p>Dex Web draws each Flow type's graph from the uploaded document. Generate the files
         * with {@code dexcli visualize}. Each path must exist and be readable. An explicitly listed
         * file must be a Flow Definition Graph 2.x: a UTF-8 JSON object whose {@code schemaVersion}
         * starts with {@code 2.} and whose {@code flow.name} is a non-empty string; its extension
         * is not checked. A directory is read recursively for files whose extension is
         * {@code .json} in any case; a file there that is not a Flow Definition Graph 2.x is
         * skipped with an INFO log, so a directory may hold other JSON files.
         *
         * <p>The Flow type of a file is its {@code flow.name}. Its generation time is its top-level
         * {@code generatedAt}, which must be strict RFC 3339 such as
         * {@code 2026-11-01T00:00:00Z}, or the file modification time when that is missing. Every
         * generation time is truncated to whole milliseconds.
         *
         * <p>The Worker reads the files when it is constructed. It uploads only documents for Flow
         * types that its registry registers, after protocol negotiation and before it opens task
         * streams, and sends nothing when none is left. An absolute top-level {@code source.path},
         * including a Windows drive or UNC path, is replaced by a path relative to the working
         * directory, or by the file name when the file is elsewhere or the working directory is
         * unknown. Requests hold at most 256 documents and 16 MiB of content and share one
         * {@link #attributeIndexSyncTimeout(Duration)} deadline. {@link Worker#stop()} cancels an
         * upload in progress. An upload failure, including an older Server without upload support,
         * is logged as a warning; the Worker still starts. The default is no paths. Each call
         * replaces the previous paths.
         *
         * <pre>{@code
         * WorkerOptions options = WorkerOptions.newBuilder()
         *         .serverAddress("dex:8801")
         *         .flowDefinitionPaths(Collections.singletonList(Paths.get("flow-definitions")))
         *         .build();
         * }</pre>
         *
         * @param value the nonnull files and directories; elements must be nonnull
         * @return this builder
         * @throws IllegalArgumentException if value or an element is null; the {@link Worker}
         *     constructor throws IllegalArgumentException for a missing or unreadable path, an
         *     invalid explicit file, an invalid {@code generatedAt}, or a Flow type configured twice
         * @since 1.45.0
         */
        public Builder flowDefinitionPaths(final List<Path> value) {
            this.flowDefinitionPaths = immutableCopy(value, "flowDefinitionPaths");
            return this;
        }

        /**
         * Sets in-memory Flow Definition Graph documents that the Worker uploads at start.
         *
         * <p>Use this when the application builds documents itself, for example from a template
         * with a fixed generation time. Uploading, filtering to registered Flow types, the
         * {@code source.path} rewrite, and failure handling work as for
         * {@link #flowDefinitionPaths(List)}. A Flow type may come from only one document across
         * both settings. The default is no documents. Each call replaces the previous documents.
         *
         * <pre>{@code
         * WorkerOptions options = WorkerOptions.newBuilder()
         *         .flowDefinitions(Collections.singletonList(new FlowDefinitionDocument(
         *                 "OrderFlow", orderFlowJson, Instant.parse("2026-11-01T00:00:00Z"))))
         *         .build();
         * }</pre>
         *
         * @param value the nonnull documents; elements must be nonnull
         * @return this builder
         * @throws IllegalArgumentException if value or an element is null; the {@link Worker}
         *     constructor throws IllegalArgumentException when a document is not a UTF-8 JSON
         *     object whose {@code schemaVersion} starts with {@code 2.}, its {@code flow.name}
         *     differs from its Flow type, or two sources share a Flow type
         * @since 1.45.0
         */
        public Builder flowDefinitions(final List<FlowDefinitionDocument> value) {
            this.flowDefinitions = immutableCopy(value, "flowDefinitions");
            return this;
        }

        private static <T> List<T> immutableCopy(final List<T> value, final String name) {
            if (value == null) {
                throw new IllegalArgumentException(name + " is required");
            }
            final List<T> copy = new ArrayList<T>(value);
            for (T element : copy) {
                if (element == null) {
                    throw new IllegalArgumentException(name + " must not contain null");
                }
            }
            return Collections.unmodifiableList(copy);
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
