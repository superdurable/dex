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
