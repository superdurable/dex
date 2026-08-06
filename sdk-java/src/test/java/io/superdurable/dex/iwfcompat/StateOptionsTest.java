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

public final class StateOptionsTest {
    void compileTimeoutRetryDurabilityAndLocks(final Client client) {
        client.startFlow(IwfFlows.STATE_OPTIONS, "state-options", null);
        final String output = client.waitForFlow("state-options", String.class);
        consume(output);
    }

    private static void consume(final Object value) {
    }
}
