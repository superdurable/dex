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

public final class StateRecoveryTest {
    void compileWaitAndExecuteRecovery(final Client client) {
        client.startFlow(IwfFlows.STATE_RECOVERY, "state-recovery", 1);
        final Integer output = client.waitForFlow("state-recovery", Integer.class);
        consume(output);
    }

    void compileExecuteOnlyRecovery(final Client client) {
        client.startFlow(IwfFlows.STATE_RECOVERY_NO_WAIT, "state-recovery-no-wait", 1);
        final Integer output = client.waitForFlow(
                "state-recovery-no-wait",
                Integer.class);
        consume(output);
    }

    private static void consume(final Object value) {
    }
}
