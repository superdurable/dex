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
import io.superdurable.dex.StartFlowOptions;

import java.time.Duration;

public final class AnyCommandCombinationTest {
    private static final AnyCommandCombinationWorkflow WORKFLOW =
            new AnyCommandCombinationWorkflow();

    void compileStateApiFailure(final Client client) {
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .timeout(Duration.ofSeconds(10))
                .build();
        client.startFlow(WORKFLOW, "any-combination", 0, options);
        final Integer result = client.waitForFlow("any-combination", Integer.class);
        consume(result);
    }

    private static void consume(final Object value) {
    }
}
