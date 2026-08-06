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
import io.superdurable.dex.StartFlowOptions;

import java.time.Duration;

public final class AnyCommandCombinationTest {
    void compileStateApiFailure(final Client client) {
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .timeout(Duration.ofSeconds(10))
                .build();
        client.startFlow(IwfFlows.ANY_COMBINATION_FAIL, "any-combination", 0, options);
        final Integer result = client.waitForFlow("any-combination", Integer.class);
        consume(result);
    }

    private static void consume(final Object value) {
    }
}
