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

public final class StateOptionsOverrideTest {
    void compileMovementOptionsOverride(final Client client) {
        client.startFlow(IwfFlows.STATE_OPTIONS_OVERRIDE, "options-override", "input");
        final String output = client.waitForFlow("options-override", String.class);
        consume(output);
    }

    private static void consume(final Object value) {
    }
}
