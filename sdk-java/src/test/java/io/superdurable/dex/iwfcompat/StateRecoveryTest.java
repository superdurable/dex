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

public final class StateRecoveryTest {
    private static final StateRecoveryWorkflow WORKFLOW = new StateRecoveryWorkflow();
    private static final StateRecoveryNoWaitWorkflow NO_WAIT_WORKFLOW =
            new StateRecoveryNoWaitWorkflow();

    void compileWaitAndExecuteRecovery(final Client client) {
        client.startFlow(WORKFLOW, "state-recovery", 1);
        final Integer output = client.waitForFlow("state-recovery", Integer.class);
        consume(output);
    }

    void compileExecuteOnlyRecovery(final Client client) {
        client.startFlow(NO_WAIT_WORKFLOW, "state-recovery-no-wait", 1);
        final Integer output = client.waitForFlow(
                "state-recovery-no-wait",
                Integer.class);
        consume(output);
    }

    private static void consume(final Object value) {
    }
}
