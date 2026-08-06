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
import io.superdurable.dex.TimerId;

public final class SignalTest {
    void compileSignalsAndTimerSkip(final Client client) {
        client.startFlow(IwfFlows.SIGNAL, "signal", 0);
        client.publish("signal", IwfFlows.SIGNAL.first, 1);
        client.publish("signal", IwfFlows.SIGNAL.second, 2);
        client.publish("signal", IwfFlows.SIGNAL.third, 3, 4);
        client.publish("signal", IwfFlows.SIGNAL.signalMap, "one", 5);
        client.skipTimer(
                "signal",
                new StepExecutionId("SignalCombinationStep"),
                TimerId.byConditionId("test-timer-id"));
        final Integer output = client.waitForFlow("signal", Integer.class);
        consume(output);
    }

    private static void consume(final Object value) {
    }
}
