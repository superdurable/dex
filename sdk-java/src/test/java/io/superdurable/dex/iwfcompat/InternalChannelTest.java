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

import java.util.Arrays;

public final class InternalChannelTest {
    void compileBasicInternalChannel(final Client client) {
        client.startFlow(IwfFlows.BASIC_INTERNAL, "basic-internal", 1);
        final Integer output = client.waitForFlow("basic-internal", Integer.class);
        consume(output);
    }

    void compileWaitingInternalChannel(final Client client) {
        client.startFlow(IwfFlows.WAITING_INTERNAL, "waiting-internal", 1);
        client.publish(
                "waiting-internal",
                IwfFlows.WAITING_INTERNAL.channel,
                Arrays.asList(2, 3));
        final Integer output = client.waitForFlow("waiting-internal", Integer.class);
        consume(output);
    }

    private static void consume(final Object value) {
    }
}
