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
