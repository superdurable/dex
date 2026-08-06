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
    private final String flowServiceAddress;
    private final int maxConcurrentInvocations;
    private final int activationQueueCapacity;
    private final ObjectMapper objectMapper;

    public WorkerOptions() {
        this(":8803", null, "localhost:8801", 32, 64, new ObjectMapper());
    }

    public WorkerOptions(
            final String bindAddress,
            final WorkerTarget workerTarget,
            final String flowServiceAddress,
            final int maxConcurrentInvocations,
            final int activationQueueCapacity) {
        this(
                bindAddress,
                workerTarget,
                flowServiceAddress,
                maxConcurrentInvocations,
                activationQueueCapacity,
                new ObjectMapper());
    }

    public WorkerOptions(
            final String bindAddress,
            final WorkerTarget workerTarget,
            final String flowServiceAddress,
            final int maxConcurrentInvocations,
            final int activationQueueCapacity,
            final ObjectMapper objectMapper) {
        if (objectMapper == null) {
            throw new IllegalArgumentException("objectMapper is required");
        }
        this.bindAddress = bindAddress;
        this.workerTarget = workerTarget;
        this.flowServiceAddress = flowServiceAddress;
        this.maxConcurrentInvocations = maxConcurrentInvocations;
        this.activationQueueCapacity = activationQueueCapacity;
        this.objectMapper = objectMapper;
    }

    String getBindAddress() {
        return bindAddress;
    }

    WorkerTarget getWorkerTarget() {
        return workerTarget;
    }

    String getFlowServiceAddress() {
        return flowServiceAddress;
    }

    int getMaxConcurrentInvocations() {
        return maxConcurrentInvocations;
    }

    int getActivationQueueCapacity() {
        return activationQueueCapacity;
    }

    ObjectMapper getObjectMapper() {
        return objectMapper;
    }
}
