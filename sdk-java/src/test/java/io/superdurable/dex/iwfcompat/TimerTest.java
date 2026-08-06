/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex.iwfcompat;

import io.superdurable.dex.Client;
import io.superdurable.dex.StepExecutionId;

import java.time.Duration;

public final class TimerTest {
    void compileTimerAndStepWait(final Client client) {
        client.startFlow(IwfFlows.TIMER, "timer", 1);
        client.waitForStepCompletion(
                "timer",
                new StepExecutionId("TimerStep"),
                Duration.ofSeconds(10));
        client.waitForFlow("timer");
    }
}
