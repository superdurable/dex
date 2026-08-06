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

public final class ConditionalCompleteTest {
    void compileSignalChannel(final Client client) {
        client.startFlow(IwfFlows.CONDITIONAL_COMPLETE, "conditional-signal", true);
        client.publish("conditional-signal", IwfFlows.CONDITIONAL_COMPLETE.signal, (Void) null);
        final Integer output = client.waitForFlow("conditional-signal", Integer.class);
        consume(output);
    }

    void compileInternalChannel(final Client client) {
        client.startFlow(IwfFlows.CONDITIONAL_COMPLETE, "conditional-internal", false);
        final ConditionalCompleteFlow stub = client.newRpcStub(
                ConditionalCompleteFlow.class,
                "conditional-internal");
        client.invokeRPC(stub::publishToInternalChannel);
    }

    private static void consume(final Object value) {
    }
}
