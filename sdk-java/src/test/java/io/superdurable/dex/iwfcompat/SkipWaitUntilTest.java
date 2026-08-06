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

public final class SkipWaitUntilTest {
    private static final SkipWaitUntilWorkflow WORKFLOW = new SkipWaitUntilWorkflow();
    private static final SkipWaitUntilMixedWaitWorkflow MIXED_WAIT_WORKFLOW =
            new SkipWaitUntilMixedWaitWorkflow();

    void compileExecuteOnlySteps(final Client client) {
        client.startFlow(WORKFLOW, "execute-only", 0);
        final Integer output = client.waitForFlow("execute-only", Integer.class);
        consume(output);
    }

    void compileMixedWaitStyles(final Client client) {
        client.startFlow(MIXED_WAIT_WORKFLOW, "mixed-wait", 0);
        final Integer output = client.waitForFlow("mixed-wait", Integer.class);
        consume(output);
    }

    private static void consume(final Object value) {
    }
}
