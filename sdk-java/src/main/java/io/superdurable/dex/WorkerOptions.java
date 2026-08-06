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

public final class WorkerOptions {
    private final String bindAddress;
    private final WorkerTarget workerTarget;
    private final String serverAddress;
    private final ObjectMapper objectMapper;

    private WorkerOptions(final Builder builder) {
        this.bindAddress = builder.bindAddress;
        this.workerTarget = builder.workerTarget;
        this.serverAddress = builder.serverAddress;
        this.objectMapper = builder.objectMapper;
    }

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

    public static final class Builder {
        private String bindAddress = ":8803";
        private WorkerTarget workerTarget;
        private String serverAddress = "localhost:8801";
        private ObjectMapper objectMapper = new ObjectMapper();

        private Builder() {
        }

        public Builder bindAddress(final String value) {
            this.bindAddress = value;
            return this;
        }

        public Builder workerTarget(final WorkerTarget value) {
            this.workerTarget = value;
            return this;
        }

        public Builder serverAddress(final String value) {
            this.serverAddress = value;
            return this;
        }

        public Builder objectMapper(final ObjectMapper value) {
            if (value == null) {
                throw new IllegalArgumentException("objectMapper is required");
            }
            this.objectMapper = value;
            return this;
        }

        public WorkerOptions build() {
            return new WorkerOptions(this);
        }
    }
}
