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

import io.grpc.Status;
import io.superdurable.dex.exceptions.NonRetryableException;
import io.superdurable.dex.exceptions.RetryAfterException;
import io.superdurable.gen.WorkerErrorResponse;

import java.io.PrintWriter;
import java.io.StringWriter;
import java.nio.charset.StandardCharsets;
import java.util.ArrayDeque;
import java.util.Collections;
import java.util.Deque;
import java.util.IdentityHashMap;
import java.util.Set;

final class WorkerErrorMapper {
    static final int MAX_WORKER_ERROR_DETAIL_BYTES = 1024;
    static final int MAX_WORKER_ERROR_TYPE_BYTES = 256;
    static final int MAX_WORKER_STACK_TRACE_BYTES = 4 * 1024;
    static final byte[] ERROR_DETAIL_TRUNCATION_MARKER =
            "\n... error detail truncated by Dex Java SDK ..."
                    .getBytes(StandardCharsets.UTF_8);
    private static final byte[] ERROR_TYPE_TRUNCATION_MARKER =
            "\n... error type truncated by Dex Java SDK ..."
                    .getBytes(StandardCharsets.UTF_8);
    private static final byte[] STACK_TRACE_TRUNCATION_MARKER =
            "\n... stack trace truncated by Dex Java SDK ..."
                    .getBytes(StandardCharsets.UTF_8);
    private final GrpcErrorStatusMapping grpcErrorStatusMapping;

    WorkerErrorMapper(final GrpcErrorStatusMapping grpcErrorStatusMapping) {
        this.grpcErrorStatusMapping = grpcErrorStatusMapping;
    }

    WorkerErrorResponse mapFailure(final Throwable failure, final Status.Code cancellationStatus) {
        if (cancellationStatus != null) {
            return WorkerErrorResponse.newBuilder()
                    .setDetail(cancellationStatus == Status.Code.DEADLINE_EXCEEDED
                            ? "Java Worker invocation deadline exceeded"
                            : "Java Worker invocation canceled")
                    .setGrpcStatus(cancellationStatus.value())
                    .build();
        }
        RetryAfterException retryAfter = null;
        Throwable reportedFailure = failure;
        final Set<Throwable> unwrapped = Collections.newSetFromMap(new IdentityHashMap<>());
        while (unwrapped.add(reportedFailure)) {
            final boolean isControlWrapper = reportedFailure instanceof RetryAfterException
                    || (reportedFailure instanceof NonRetryableException
                            && reportedFailure.getCause() != null);
            if (!isControlWrapper) {
                break;
            }
            if (reportedFailure instanceof RetryAfterException && retryAfter == null) {
                retryAfter = (RetryAfterException) reportedFailure;
            }
            reportedFailure = reportedFailure.getCause();
        }
        final boolean isNonRetryable = hasNonRetryableCause(failure);
        final String message = truncateUtf8(reportedFailure.getMessage() == null
                ? reportedFailure.toString()
                : reportedFailure.getMessage(),
                MAX_WORKER_ERROR_DETAIL_BYTES,
                ERROR_DETAIL_TRUNCATION_MARKER);
        final WorkerErrorResponse.Builder details = WorkerErrorResponse.newBuilder()
                .setDetail(message)
                .setErrorType(truncateUtf8(
                        reportedFailure.getClass().getName(),
                        MAX_WORKER_ERROR_TYPE_BYTES,
                        ERROR_TYPE_TRUNCATION_MARKER))
                .setStackTrace(stackTrace(reportedFailure))
                .setGrpcStatus(grpcErrorStatusMapping.statusFor(reportedFailure).value())
                .setNonRetryable(isNonRetryable);
        if (retryAfter != null) {
            details.setRetryAfterSeconds((int) retryAfter.getRetryAfter().getSeconds());
        }
        return details.build();
    }

    /**
     * Returns whether {@code failure} or any exception reachable through causes and suppressed
     * exceptions is a NonRetryableException. Buffered Stream finalizer failures are suppressed.
     */
    private static boolean hasNonRetryableCause(final Throwable failure) {
        final Set<Throwable> visited = Collections.newSetFromMap(new IdentityHashMap<>());
        final Deque<Throwable> pending = new ArrayDeque<>();
        pending.push(failure);
        while (!pending.isEmpty()) {
            final Throwable current = pending.pop();
            if (!visited.add(current)) {
                continue;
            }
            if (current instanceof NonRetryableException) {
                return true;
            }
            if (current.getCause() != null) {
                pending.push(current.getCause());
            }
            for (final Throwable suppressed : current.getSuppressed()) {
                pending.push(suppressed);
            }
        }
        return false;
    }

    private static String stackTrace(final Throwable failure) {
        final StringWriter buffer = new StringWriter();
        final PrintWriter writer = new PrintWriter(buffer);
        failure.printStackTrace(writer);
        writer.flush();
        return truncateUtf8(
                buffer.toString(),
                MAX_WORKER_STACK_TRACE_BYTES,
                STACK_TRACE_TRUNCATION_MARKER);
    }

    private static String truncateUtf8(
            final String value,
            final int maximumBytes,
            final byte[] truncationMarker) {
        final byte[] encoded = value.getBytes(StandardCharsets.UTF_8);
        if (encoded.length <= maximumBytes) {
            return value;
        }
        int prefixLength = maximumBytes - truncationMarker.length;
        while (prefixLength > 0 && (encoded[prefixLength] & 0xc0) == 0x80) {
            prefixLength--;
        }
        return new String(encoded, 0, prefixLength, StandardCharsets.UTF_8)
                + new String(truncationMarker, StandardCharsets.UTF_8);
    }
}
