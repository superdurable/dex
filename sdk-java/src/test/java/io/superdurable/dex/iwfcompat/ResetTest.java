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
import io.superdurable.dex.ResetFlowOptions;
import io.superdurable.dex.ResetType;

public final class ResetTest {
    private static final ResetWorkflow WORKFLOW = new ResetWorkflow();

    void compileLockingRpcReapply(final Client client) {
        client.startFlow(WORKFLOW, "reset-locking", null);
        final ResetWorkflow stub = client.newRpcStub(
                ResetWorkflow.class,
                "reset-locking");
        client.invokeRPC(stub::withLocking);
        client.invokeRPC(stub::withAttributeMapLock);
        final ResetFlowOptions options = ResetFlowOptions.newBuilder(ResetType.BEGINNING)
                .reason("replay locking RPC")
                .skipLockingRpcReapply(false)
                .build();
        final String runId = client.resetFlow("reset-locking", options);
        consume(runId);
    }

    void compileSkipRpcAndChannelReapply(final Client client) {
        final ResetFlowOptions options = ResetFlowOptions.newBuilder(ResetType.STEP_TYPE)
                .stepType("LockWaitStep")
                .skipLockingRpcReapply(true)
                .skipChannelMessagesReapply(true)
                .build();
        final String runId = client.resetFlow("reset-locking", options);
        consume(runId);
    }

    private static void consume(final Object value) {
    }
}
