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
