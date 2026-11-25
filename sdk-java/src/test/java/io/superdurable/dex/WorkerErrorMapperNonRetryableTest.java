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

import io.superdurable.dex.exceptions.NonRetryableException;
import io.superdurable.dex.exceptions.RetryAfterException;
import io.superdurable.gen.WorkerErrorResponse;
import org.junit.jupiter.api.Test;

import java.time.Duration;
import java.util.Arrays;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class WorkerErrorMapperNonRetryableTest {
    private final WorkerErrorMapper mapper =
            new WorkerErrorMapper(GrpcErrorStatusMapping.newBuilder().build());

    @Test
    void nonRetryableReportsTheCauseInEitherNestingOrder() {
        final IllegalStateException cause = new IllegalStateException("card rejected");
        for (final Throwable failure : Arrays.<Throwable>asList(
                new NonRetryableException(cause),
                RetryAfterException.after(Duration.ofSeconds(7), new NonRetryableException(cause)),
                new NonRetryableException(RetryAfterException.after(Duration.ofSeconds(7), cause)))) {
            final WorkerErrorResponse response = mapper.mapFailure(failure, null);
            assertTrue(response.getNonRetryable());
            assertEquals(IllegalStateException.class.getName(), response.getErrorType());
            assertEquals("card rejected", response.getDetail());
        }
    }

    @Test
    void messageOnlyNonRetryableReportsItself() {
        final WorkerErrorResponse response =
                mapper.mapFailure(new NonRetryableException("bad input"), null);
        assertTrue(response.getNonRetryable());
        assertEquals(NonRetryableException.class.getName(), response.getErrorType());
        assertEquals("bad input", response.getDetail());
    }

    @Test
    void markerInTheCauseChainIsNonRetryableAndReportsTheOuterException() {
        final WorkerErrorResponse response = mapper.mapFailure(
                new IllegalStateException("charge failed", new NonRetryableException("bad input")), null);
        assertTrue(response.getNonRetryable());
        assertEquals(IllegalStateException.class.getName(), response.getErrorType());
        assertEquals("charge failed", response.getDetail());
    }

    @Test
    void repeatedRetryAfterKeepsTheOuterDelayAndReportsTheInnerCause() {
        final IllegalStateException cause = new IllegalStateException("card rejected");
        final WorkerErrorResponse response = mapper.mapFailure(
                RetryAfterException.after(Duration.ofSeconds(7), new NonRetryableException(
                        RetryAfterException.after(Duration.ofSeconds(8), cause))),
                null);
        assertTrue(response.getNonRetryable());
        assertEquals(7, response.getRetryAfterSeconds());
        assertEquals(IllegalStateException.class.getName(), response.getErrorType());
    }

    @Test
    void plainFailureStaysRetryable() {
        assertFalse(mapper.mapFailure(new IllegalStateException("boom"), null).getNonRetryable());
    }
}
