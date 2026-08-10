/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex;

import io.grpc.Status;

import java.util.Collections;
import java.util.HashMap;
import java.util.Map;

/**
 * Maps Java Worker application exceptions to gRPC status codes.
 *
 * <p>Every exception thrown by a Step method or RPC becomes a structured Worker error. By default,
 * all exceptions use {@link Status.Code#INTERNAL} because they originate inside the Worker
 * application. Applications may override selected exception classes when their operational
 * semantics require a more specific status. A mapping applies to subclasses unless a closer class
 * also has a mapping; throwing a gRPC {@code StatusException} does not bypass this configuration.
 *
 * <pre>{@code
 * GrpcErrorStatusMapping mapping = GrpcErrorStatusMapping.newBuilder()
 *         .map(PaymentDeclinedException.class, Status.Code.FAILED_PRECONDITION)
 *         .map(WorkerOverloadedException.class, Status.Code.RESOURCE_EXHAUSTED)
 *         .build();
 *
 * WorkerOptions options = WorkerOptions.newBuilder()
 *         .grpcErrorStatusMapping(mapping)
 *         .build();
 * }</pre>
 *
 * <p>The mapping affects the diagnostic status stored with the Worker error. Step retry and failure
 * policies remain controlled by {@link StepOptions}; changing a gRPC status does not itself disable
 * retries.
 */
public final class GrpcErrorStatusMapping {
    private final Map<Class<? extends Throwable>, Status.Code> mappings;

    private GrpcErrorStatusMapping(final Builder builder) {
        this.mappings = Collections.unmodifiableMap(
                new HashMap<Class<? extends Throwable>, Status.Code>(builder.mappings));
    }

    /**
     * Creates a builder whose unmatched exceptions resolve to {@link Status.Code#INTERNAL}.
     *
     * @return a new mutable builder
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    Status.Code statusFor(final Throwable failure) {
        Class<?> current = failure.getClass();
        while (current != null && Throwable.class.isAssignableFrom(current)) {
            final Status.Code status = mappings.get(current);
            if (status != null) {
                return status;
            }
            current = current.getSuperclass();
        }
        return Status.Code.INTERNAL;
    }

    /** Builds immutable exception-to-status mappings. */
    public static final class Builder {
        private final Map<Class<? extends Throwable>, Status.Code> mappings =
                new HashMap<Class<? extends Throwable>, Status.Code>();

        private Builder() {
        }

        /**
         * Maps an exception class and its otherwise-unmapped subclasses to a gRPC status.
         *
         * <p>Calling this method again for the same class replaces its previous status. A mapping for
         * a subclass takes precedence over a mapping inherited from a superclass.
         *
         * @param exceptionType the nonnull exception or error class to match
         * @param status the nonnull, non-{@code OK} gRPC status code to store with matching Worker
         *     errors
         * @return this builder
         * @throws IllegalArgumentException if either argument is {@code null} or {@code status} is
         *     {@link Status.Code#OK}
         */
        public Builder map(
                final Class<? extends Throwable> exceptionType,
                final Status.Code status) {
            if (exceptionType == null || status == null) {
                throw new IllegalArgumentException("exceptionType and status are required");
            }
            if (status == Status.Code.OK) {
                throw new IllegalArgumentException("Worker errors cannot use gRPC OK status");
            }
            mappings.put(exceptionType, status);
            return this;
        }

        /**
         * Builds an immutable mapping from the current entries.
         *
         * @return the configured mapping, using {@code INTERNAL} for unmatched exceptions
         */
        public GrpcErrorStatusMapping build() {
            return new GrpcErrorStatusMapping(this);
        }
    }
}
