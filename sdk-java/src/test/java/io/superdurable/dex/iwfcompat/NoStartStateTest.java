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

public final class NoStartStateTest {
    private static final NoStartStateWorkflow NO_START_WORKFLOW = new NoStartStateWorkflow();
    private static final RpcNoStateWorkflow NO_STEP_WORKFLOW = new RpcNoStateWorkflow();
    private static final NoStartStateDeadEndWorkflow DEAD_END_WORKFLOW =
            new NoStartStateDeadEndWorkflow();

    void compileNoStartStep(final Client client) {
        client.startFlow(NO_START_WORKFLOW, "no-start", null);
        final NoStartStateWorkflow stub = client.newRpcStub(
                NoStartStateWorkflow.class,
                "no-start");
        final Long output = client.invokeRPC(stub::invoke, "input");
        consume(output);
    }

    void compileNoStep(final Client client) {
        client.startFlow(NO_STEP_WORKFLOW, "no-step", null);
        final RpcNoStateWorkflow stub = client.newRpcStub(
                RpcNoStateWorkflow.class,
                "no-step");
        final Integer output = client.invokeRPC(stub::increaseCounter);
        client.stopFlow("no-step");
        consume(output);
    }

    void compileDeadEnd(final Client client) {
        client.startFlow(DEAD_END_WORKFLOW, "dead-end", null);
        final NoStartStateDeadEndWorkflow stub = client.newRpcStub(
                NoStartStateDeadEndWorkflow.class,
                "dead-end");
        final Integer size = client.invokeRPC(stub::publishInternal);
        consume(size);
    }

    private static void consume(final Object value) {
    }
}
