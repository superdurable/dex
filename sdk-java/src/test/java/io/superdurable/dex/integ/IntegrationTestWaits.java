/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Super Durable Source License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Client;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.TimerId;
import io.superdurable.dex.exceptions.DexServiceException;

import java.time.Duration;

final class IntegrationTestWaits {
    private IntegrationTestWaits() {
    }

    static void skipTimerWhenRegistered(
            final Client client,
            final String flowId,
            final StepExecutionId stepExecutionId,
            final TimerId timerId) {
        final long deadline = System.nanoTime() + Duration.ofSeconds(30).toNanos();
        DexServiceException lastFailure = null;
        while (System.nanoTime() < deadline) {
            try {
                client.skipTimer(flowId, stepExecutionId, timerId);
                return;
            } catch (DexServiceException failure) {
                if (!failure.getDetail().contains(
                        "timer condition does not exist or is not pending")) {
                    throw failure;
                }
                lastFailure = failure;
                Thread.yield();
            }
        }
        throw new AssertionError("timer condition was not registered", lastFailure);
    }
}
