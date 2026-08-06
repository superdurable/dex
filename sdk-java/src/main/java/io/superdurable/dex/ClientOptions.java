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

public final class ClientOptions {
    private final String serverAddress;
    private final WorkerTarget workerTarget;
    private final ObjectMapper objectMapper;

    public ClientOptions() {
        this("localhost:8801", null, new ObjectMapper());
    }

    public ClientOptions(final String serverAddress) {
        this(serverAddress, null, new ObjectMapper());
    }

    public ClientOptions(
            final String serverAddress,
            final WorkerTarget workerTarget) {
        this(serverAddress, workerTarget, new ObjectMapper());
    }

    public ClientOptions(
            final String serverAddress,
            final WorkerTarget workerTarget,
            final ObjectMapper objectMapper) {
        if (objectMapper == null) {
            throw new IllegalArgumentException("objectMapper is required");
        }
        this.serverAddress = serverAddress;
        this.workerTarget = workerTarget;
        this.objectMapper = objectMapper;
    }

    String getServerAddress() {
        return serverAddress;
    }

    WorkerTarget getWorkerTarget() {
        return workerTarget;
    }

    ObjectMapper getObjectMapper() {
        return objectMapper;
    }
}
